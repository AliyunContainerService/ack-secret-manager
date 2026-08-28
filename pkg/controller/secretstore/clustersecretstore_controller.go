/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package secretstore

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	v1alpha1 "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// ClusterSecretStoreReconciler reconciles a ClusterSecretStore object
type ClusterSecretStoreReconciler struct {
	*CommonReconciler
	Client               client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	Ctx                  context.Context
	ReconciliationPeriod time.Duration
}

//+kubebuilder:rbac:groups=alibabacloud.com,resources=clustersecretstores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=alibabacloud.com,resources=clustersecretstores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=alibabacloud.com,resources=clustersecretstores/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ClusterSecretStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ClusterSecretStore", req.NamespacedName)
	clusterSecretStore := &v1alpha1.ClusterSecretStore{}

	err := r.Get(ctx, req.NamespacedName, clusterSecretStore)
	if err != nil {
		// NotFound is normal when the resource was deleted
		return ctrl.Result{}, utils.IgnoreNotFoundError(err)
	}
	log.Info("cluster secret store info", "name", req.String())

	clientName := backend.ClusterStoreKey(clusterSecretStore.Name)
	kmsProvider := backend.GetProviderByName(backend.ProviderKMSName)
	oosProvider := backend.GetProviderByName(backend.ProviderOOSName)

	storeWrapper := &ClusterSecretStoreWrapper{clusterSecretStore}

	if clusterSecretStore.GetDeletionTimestamp() != nil {
		return r.handleDeletion(log, clusterSecretStore.GetFinalizers(), clusterSecretStore, clientName, kmsProvider, oosProvider, func(obj client.Object) error {
			return r.Update(ctx, obj)
		})
	}

	if !utils.Contains(clusterSecretStore.GetFinalizers(), clusterSecretFinalizer) {
		if err := r.addFinalizer(ctx, log, clusterSecretStore); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
		// Re-fetch after updating finalizers
		if err := r.Get(ctx, req.NamespacedName, clusterSecretStore); err != nil {
			log.Error(err, "failed to re-fetch ClusterSecretStore after adding finalizer")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
	}

	validationErr := r.validateClusterSecretStoreSpec(storeWrapper)
	if validationErr != nil {
		log.Error(validationErr, "ClusterSecretStore validation failed")
		err := r.updateStoreStatusWithError(ctx, log, clusterSecretStore, v1alpha1.ReasonValidationFailed, validationErr.Error())
		if err != nil {
			log.Error(err, "failed to update ClusterSecretStore status")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
		return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, validationErr
	}

	// Trigger annotation forces client recreation
	triggerAnnotation := clusterSecretStore.Annotations[TriggerReconcileAnnotation]
	needRecreateClient := triggerAnnotation != ""

	if !needRecreateClient {
		needRecreateClient = r.needRecreateClient(storeWrapper, clientName, clusterSecretStore.Generation, clusterSecretStore.Status.Conditions, kmsProvider, oosProvider)
	}

	if needRecreateClient {
		err := r.recreateClient(ctx, log, clientName, kmsProvider, oosProvider, storeWrapper)
		if err != nil {
			updateErr := r.updateStoreStatusWithError(ctx, log, clusterSecretStore, v1alpha1.ReasonClientCreationFailed, err.Error())
			if updateErr != nil {
				log.Error(updateErr, "failed to update ClusterSecretStore status with error")
			}
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}

		// Persist the status (with the clientGeneration bump) BEFORE clearing
		// the trigger annotation: the persisted bump is the signal the
		// ExternalSecret reverse watch consumes, and clearing the annotation
		// first would lose that signal if the status write then failed.
		err = r.updateStoreStatusWithReadyAndGeneration(ctx, log, clusterSecretStore, v1alpha1.SecretStoreReadOnly, true)
		if err != nil {
			log.Error(err, "failed to update ClusterSecretStore status")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}

		// Clear the trigger annotation only after the status write succeeded.
		// A clearing failure is returned (not swallowed): the annotation stays,
		// the retry round rebuilds idempotently, re-persists status+bump and
		// retries the clear, so the trigger can never be silently dropped.
		//
		// The clear patch carries an optimistic lock (resourceVersion guard)
		// so a concurrently set new trigger value is never erased by this
		// clear: on conflict the patch fails and the retry path re-reads the
		// new trigger value instead of losing it. The base object is
		// re-fetched first because the status write above advanced the
		// resourceVersion on a fresh copy, so the pre-status object is
		// guaranteed to conflict.
		if triggerAnnotation != "" {
			latestStore := &v1alpha1.ClusterSecretStore{}
			if err := r.Get(ctx, req.NamespacedName, latestStore); err != nil {
				log.Error(err, "failed to re-fetch ClusterSecretStore before clearing trigger annotation, will retry", "ClusterSecretStore", req.NamespacedName)
				return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, utils.IgnoreNotFoundError(err)
			}
			if latestStore.Annotations[TriggerReconcileAnnotation] != triggerAnnotation {
				// A different trigger value appeared while this round was
				// running: never clear it -- the next round must process
				// the new value with a fresh client rebuild.
				log.Info("trigger annotation changed during reconciliation, keeping the new value for the next round", "ClusterSecretStore", req.NamespacedName)
				return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
			}
			updatedStore := latestStore.DeepCopy()

			delete(updatedStore.Annotations, TriggerReconcileAnnotation)

			if err := r.Patch(ctx, updatedStore, client.MergeFromWithOptions(latestStore, client.MergeFromWithOptimisticLock{})); err != nil {
				log.Error(err, "failed to clear ClusterSecretStore trigger annotation after status write, will retry", "ClusterSecretStore", req.NamespacedName)
				return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
			}
		}

		// The persisted status.clientGeneration bump (done inside the status
		// write above) is the signal the ExternalSecret controller's reverse
		// watch uses to reconcile with the freshly recreated client; no
		// in-process notification channel is needed.
		return ctrl.Result{}, nil
	}

	// Initialize status when no conditions exist yet
	if len(clusterSecretStore.Status.Conditions) == 0 {
		if statusErr := r.updateStoreStatusWithReadyAndGeneration(ctx, log, clusterSecretStore, v1alpha1.SecretStoreReadOnly, false); statusErr != nil {
			log.Error(statusErr, "failed to initialize ClusterSecretStore status")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, statusErr
		}
		return ctrl.Result{}, nil
	}

	// (false, nil) means the status was already up-to-date: not an error and
	// must not trigger a requeue (would form a self-reinforcing loop).
	statusUpdated, err := r.updateStoreStatus(ctx, log, clusterSecretStore)
	if err != nil {
		log.Error(err, "failed to update ClusterSecretStore status")
		return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
	}
	if !statusUpdated {
		log.Info("ClusterSecretStore status unchanged, skipping status write")
	}

	// Schedule the periodic requeue so reconciliation keeps running even
	// though the predicates ignore status-only updates.
	return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterSecretStoreReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	// rest.Config is used later to create a kubernetes.Interface
	r.RestConfig = mgr.GetConfig()

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterSecretStore{}, builder.WithPredicates(ClusterSecretStorePredicate{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

func (r *ClusterSecretStoreReconciler) addFinalizer(ctx context.Context, logger logr.Logger, css *v1alpha1.ClusterSecretStore) error {
	logger.Info("Adding Finalizer for the clustersecretstore", "name", css.Name)
	css.SetFinalizers(append(css.GetFinalizers(), clusterSecretFinalizer))
	err := r.Update(ctx, css)
	if err != nil {
		logger.Error(err, "Failed to update clustersecretstore with finalizer", "name", css.Name)
		return err
	}
	return nil
}

// updateStoreStatus updates the ClusterSecretStore status based on validation results
func (r *ClusterSecretStoreReconciler) updateStoreStatus(ctx context.Context, logger logr.Logger, clusterSecretStore *v1alpha1.ClusterSecretStore) (bool, error) {
	storeWrapper := &ClusterSecretStoreWrapper{clusterSecretStore}
	return r.updateStatusWithReady(ctx, logger, storeWrapper)
}

// updateStoreStatusWithError updates the status with an error condition
func (r *ClusterSecretStoreReconciler) updateStoreStatusWithError(ctx context.Context, logger logr.Logger, clusterSecretStore *v1alpha1.ClusterSecretStore, reason, message string) error {
	storeWrapper := &ClusterSecretStoreWrapper{clusterSecretStore}
	return r.updateStatusWithError(ctx, logger, storeWrapper, reason, message)
}

// updateStoreStatusWithReadyAndGeneration marks the store ready with the
// given capabilities and records the observed generation; clientRecreated
// bumps status.clientGeneration so the ExternalSecret reverse watch
// observes the client rebuild.
func (r *ClusterSecretStoreReconciler) updateStoreStatusWithReadyAndGeneration(ctx context.Context, logger logr.Logger, clusterSecretStore *v1alpha1.ClusterSecretStore, capabilities v1alpha1.SecretStoreCapabilities, clientRecreated bool) error {
	storeWrapper := &ClusterSecretStoreWrapper{clusterSecretStore}
	return r.updateStatusWithReadyAndGeneration(ctx, logger, storeWrapper, capabilities, clientRecreated)
}

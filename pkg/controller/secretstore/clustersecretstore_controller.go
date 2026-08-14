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
	"fmt"
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
		// Only return error if it's not NotFound - NotFound is normal when resource is deleted
		return ctrl.Result{}, utils.IgnoreNotFoundError(err)
	}
	log.Info("cluster secret store info", "name", req.String())

	clientName := fmt.Sprintf("cluster/%s", clusterSecretStore.Name)
	kmsProvider := backend.GetProviderByName(backend.ProviderKMSName)
	oosProvider := backend.GetProviderByName(backend.ProviderOOSName)

	// Wrap the ClusterSecretStore to implement StoreInterface
	storeWrapper := &ClusterSecretStoreWrapper{clusterSecretStore}

	// Handle deletion
	if clusterSecretStore.GetDeletionTimestamp() != nil {
		return r.handleDeletion(log, clusterSecretStore.GetFinalizers(), clusterSecretStore, clientName, kmsProvider, oosProvider, func(obj client.Object) error {
			return r.Update(ctx, obj)
		})
	}

	// Add finalizer if not present
	if !utils.Contains(clusterSecretStore.GetFinalizers(), clusterSecretFinalizer) {
		if err := r.addFinalizer(ctx, log, clusterSecretStore); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
		// Re-fetch the clusterSecretStore after updating finalizers
		if err := r.Get(ctx, req.NamespacedName, clusterSecretStore); err != nil {
			log.Error(err, "failed to re-fetch ClusterSecretStore after adding finalizer")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
	}

	// Validate the ClusterSecretStore spec
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

	// Check if we need to recreate the client based on trigger annotation
	triggerAnnotation := clusterSecretStore.Annotations[TriggerReconcileAnnotation]
	needRecreateClient := triggerAnnotation != ""

	if !needRecreateClient {
		// Check if there are other reasons to recreate (like spec changes)
		needRecreateClient = r.needRecreateClient(clientName, clusterSecretStore.Generation, clusterSecretStore.Status.Conditions, kmsProvider, oosProvider)
	}

	// Recreate client if needed
	if needRecreateClient {
		err := r.recreateClient(ctx, log, clientName, kmsProvider, oosProvider, storeWrapper)
		if err != nil {
			updateErr := r.updateStoreStatusWithError(ctx, log, clusterSecretStore, v1alpha1.ReasonClientCreationFailed, err.Error())
			if updateErr != nil {
				log.Error(updateErr, "failed to update ClusterSecretStore status with error")
			}
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}

		// Clear the trigger annotation after processing
		if triggerAnnotation != "" {
			updatedStore := clusterSecretStore.DeepCopy()

			delete(updatedStore.Annotations, TriggerReconcileAnnotation)

			err = r.Patch(ctx, updatedStore, client.MergeFrom(clusterSecretStore))
			if err != nil {
				log.Info("Warning: Failed to update ClusterSecretStore annotation", "error", err)
				// Don't return error here as it's not critical for functionality
			}
		}

		// Update status to indicate readiness
		err = r.updateStoreStatusWithReadyAndGeneration(ctx, log, clusterSecretStore, v1alpha1.SecretStoreReadOnly)
		if err != nil {
			log.Error(err, "failed to update ClusterSecretStore status")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
		return ctrl.Result{}, nil
	}

	// Ensure status is initialized even if no changes are detected
	// If no conditions exist, initialize the status
	if len(clusterSecretStore.Status.Conditions) == 0 {
		if statusErr := r.updateStoreStatusWithReadyAndGeneration(ctx, log, clusterSecretStore, v1alpha1.SecretStoreReadOnly); statusErr != nil {
			log.Error(statusErr, "failed to initialize ClusterSecretStore status")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, statusErr
		}
		return ctrl.Result{}, nil
	}

	// Check if we need to update status. A return of (false, nil) means the
	// status was already up-to-date, which is not an error and must not
	// trigger a requeue (it would otherwise form a self-reinforcing loop).
	statusUpdated, err := r.updateStoreStatus(ctx, log, clusterSecretStore)
	if err != nil {
		log.Error(err, "failed to update ClusterSecretStore status")
		return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
	}
	if !statusUpdated {
		log.V(2).Info("ClusterSecretStore status unchanged, skipping status write")
	}

	// Schedule the periodic requeue so reconciliation keeps running even
	// though the predicates ignore status-only updates.
	return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterSecretStoreReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	// Store the rest.Config for later use in creating kubernetes.Interface
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
	//update cluster secret store instance
	err := r.Update(ctx, css)
	if err != nil {
		logger.Error(err, "Failed to update clustersecretstore with finalizer", "name", css.Name)
		return err
	}
	return nil
}

// updateStatus updates the status of the ClusterSecretStore based on validation results
func (r *ClusterSecretStoreReconciler) updateStoreStatus(ctx context.Context, logger logr.Logger, clusterSecretStore *v1alpha1.ClusterSecretStore) (bool, error) {
	storeWrapper := &ClusterSecretStoreWrapper{clusterSecretStore}
	return r.updateStatusWithReady(ctx, logger, storeWrapper)
}

// updateStatusWithError updates the status with an error condition
func (r *ClusterSecretStoreReconciler) updateStoreStatusWithError(ctx context.Context, logger logr.Logger, clusterSecretStore *v1alpha1.ClusterSecretStore, reason, message string) error {
	storeWrapper := &ClusterSecretStoreWrapper{clusterSecretStore}
	return r.updateStatusWithError(ctx, logger, storeWrapper, reason, message)
}

// updateStatusWithReadyAndGeneration updates the status to indicate the store is ready with specified capabilities and records the observed generation
func (r *ClusterSecretStoreReconciler) updateStoreStatusWithReadyAndGeneration(ctx context.Context, logger logr.Logger, clusterSecretStore *v1alpha1.ClusterSecretStore, capabilities v1alpha1.SecretStoreCapabilities) error {
	storeWrapper := &ClusterSecretStoreWrapper{clusterSecretStore}
	return r.updateStatusWithReadyAndGeneration(ctx, logger, storeWrapper, capabilities)
}

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

// SecretStoreReconciler reconciles a SecretStore object
type SecretStoreReconciler struct {
	*CommonReconciler
	Client               client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	Ctx                  context.Context
	ReconciliationPeriod time.Duration
}

//+kubebuilder:rbac:groups=alibabacloud.com,resources=secretstores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=alibabacloud.com,resources=secretstores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=alibabacloud.com,resources=secretstores/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SecretStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("SecretStore", req.NamespacedName)
	secretStore := &v1alpha1.SecretStore{}

	err := r.Get(ctx, req.NamespacedName, secretStore)
	if err != nil {
		// Only return error if it's not NotFound - NotFound is normal when resource is deleted
		return ctrl.Result{}, utils.IgnoreNotFoundError(err)
	}
	log.Info("secret store info", "name", req.String())

	clientName := fmt.Sprintf("namespace/%s/%s", secretStore.Namespace, secretStore.Name)
	kmsProvider := backend.GetProviderByName(backend.ProviderKMSName)
	oosProvider := backend.GetProviderByName(backend.ProviderOOSName)

	// Wrap the SecretStore to implement StoreInterface
	storeWrapper := &SecretStoreWrapper{secretStore}

	// Handle deletion
	if secretStore.GetDeletionTimestamp() != nil {
		return r.handleDeletion(log, secretStore.GetFinalizers(), secretStore, clientName, kmsProvider, oosProvider, func(obj client.Object) error {
			return r.Update(ctx, obj)
		})
	}

	// Add finalizer if not present
	if !utils.Contains(secretStore.GetFinalizers(), secretFinalizer) {
		if err := r.addFinalizer(ctx, log, secretStore); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
		// Re-fetch the secretStore after updating finalizers
		if err := r.Get(ctx, req.NamespacedName, secretStore); err != nil {
			log.Error(err, "failed to re-fetch SecretStore after adding finalizer")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
	}

	// Validate the SecretStore spec
	validationErr := r.validateSecretStoreSpec(storeWrapper)
	if validationErr != nil {
		log.Error(validationErr, "SecretStore validation failed")
		err := r.updateStoreStatusWithError(ctx, log, secretStore, v1alpha1.ReasonValidationFailed, validationErr.Error())
		if err != nil {
			log.Error(err, "failed to update SecretStore status")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
		return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, validationErr
	}

	// Check if we need to recreate the client based on trigger annotation
	triggerAnnotation := secretStore.Annotations[TriggerReconcileAnnotation]
	needRecreateClient := triggerAnnotation != ""

	if !needRecreateClient {
		// Check if there are other reasons to recreate (like spec changes)
		needRecreateClient = r.needRecreateClient(clientName, secretStore.Generation, secretStore.Status.Conditions, kmsProvider, oosProvider)
	}

	// Recreate client if needed
	if needRecreateClient {
		err := r.recreateClient(ctx, log, clientName, kmsProvider, oosProvider, storeWrapper)
		if err != nil {
			updateErr := r.updateStoreStatusWithError(ctx, log, secretStore, v1alpha1.ReasonClientCreationFailed, err.Error())
			if updateErr != nil {
				log.Error(updateErr, "failed to update secretStore status with error")
			}
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}

		// Clear the trigger annotation after processing
		if triggerAnnotation != "" {
			updatedStore := secretStore.DeepCopy()

			delete(updatedStore.Annotations, TriggerReconcileAnnotation)

			err = r.Patch(ctx, updatedStore, client.MergeFrom(secretStore))
			if err != nil {
				log.Info("Warning: Failed to update secretStore annotation", "error", err)
			}
		}

		// Update status to indicate readiness
		if statusErr := r.updateStoreStatusWithReadyAndGeneration(ctx, log, secretStore, v1alpha1.SecretStoreReadOnly); statusErr != nil {
			log.Error(statusErr, "failed to update SecretStore status")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, statusErr
		}
		return ctrl.Result{}, nil
	}

	// Ensure status is initialized even if no changes are detected
	// If no conditions exist, initialize the status
	if len(secretStore.Status.Conditions) == 0 {
		if statusErr := r.updateStoreStatusWithReadyAndGeneration(ctx, log, secretStore, v1alpha1.SecretStoreReadOnly); statusErr != nil {
			log.Error(statusErr, "failed to initialize SecretStore status")
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, statusErr
		}
		return ctrl.Result{}, nil
	}

	// Check if we need to update status. A return of (false, nil) means the
	// status was already up-to-date, which is not an error and must not
	// trigger a requeue (it would otherwise form a self-reinforcing loop).
	statusUpdated, err := r.updateStoreStatus(ctx, log, secretStore)
	if err != nil {
		log.Error(err, "failed to update SecretStore status")
		return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
	}
	if !statusUpdated {
		log.V(2).Info("SecretStore status unchanged, skipping status write")
	}

	// Schedule the periodic requeue so reconciliation keeps running even
	// though the predicates ignore status-only updates.
	return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretStoreReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	// Store the rest.Config for later use in creating kubernetes.Interface
	r.RestConfig = mgr.GetConfig()

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SecretStore{}, builder.WithPredicates(SecretStorePredicate{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

func (r *SecretStoreReconciler) addFinalizer(ctx context.Context, logger logr.Logger, ss *v1alpha1.SecretStore) error {
	logger.Info("Adding Finalizer for the secretstore", "name", ss.Name, "namespace", ss.Namespace)
	ss.SetFinalizers(append(ss.GetFinalizers(), secretFinalizer))
	//update external secret instance
	err := r.Update(ctx, ss)
	if err != nil {
		logger.Error(err, "Failed to update secretstore with finalizer", "name", ss.Name, "namespace", ss.Namespace)
		return err
	}
	return nil
}

// updateStatus updates the status of the SecretStore based on validation results
func (r *SecretStoreReconciler) updateStoreStatus(ctx context.Context, logger logr.Logger, secretStore *v1alpha1.SecretStore) (bool, error) {
	storeWrapper := &SecretStoreWrapper{secretStore}
	return r.updateStatusWithReady(ctx, logger, storeWrapper)
}

func (r *SecretStoreReconciler) updateStoreStatusWithError(ctx context.Context, logger logr.Logger, secretStore *v1alpha1.SecretStore, reason, message string) error {
	storeWrapper := &SecretStoreWrapper{secretStore}
	return r.updateStatusWithError(ctx, logger, storeWrapper, reason, message)
}

// updateStatusWithReadyAndGeneration updates the status to indicate the store is ready with specified capabilities and records the observed generation
func (r *SecretStoreReconciler) updateStoreStatusWithReadyAndGeneration(ctx context.Context, logger logr.Logger, secretStore *v1alpha1.SecretStore, capabilities v1alpha1.SecretStoreCapabilities) error {
	storeWrapper := &SecretStoreWrapper{secretStore}
	return r.updateStatusWithReadyAndGeneration(ctx, logger, storeWrapper, capabilities)
}

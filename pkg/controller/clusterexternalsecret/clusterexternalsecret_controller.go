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

package clusterexternalsecret

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	clusterExternalSecretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"
	// ClusterExternalSecretReady indicates that the ClusterExternalSecret is ready
	ClusterExternalSecretReady api.ClusterExternalSecretConditionType = "Ready"
)

// ClusterExternalSecretReconciler reconciles a ClusterExternalSecret object
type ClusterExternalSecretReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	Ctx                  context.Context
	ReconciliationPeriod time.Duration
	RotationInterval     time.Duration
	DisablePolling       bool
	EnableCrossNamespace bool
}

func (r *ClusterExternalSecretReconciler) Requeue(result ctrl.Result) ctrl.Result {
	if r.DisablePolling {
		return ctrl.Result{Requeue: false}
	}
	return result
}

//+kubebuilder:rbac:groups=alibabacloud.com,resources=clusterexternalsecrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=alibabacloud.com,resources=clusterexternalsecrets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=alibabacloud.com,resources=clusterexternalsecrets/finalizers,verbs=update
//+kubebuilder:rbac:groups=alibabacloud.com,resources=externalsecrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *ClusterExternalSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ClusterExternalSecret", req.NamespacedName)

	clusterExternalSecret := &api.ClusterExternalSecret{}

	err := r.Get(ctx, req.NamespacedName, clusterExternalSecret)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	rotationInterval := r.RotationInterval
	if clusterExternalSecret.Spec.RotationInterval != nil {
		rotationInterval = clusterExternalSecret.Spec.RotationInterval.Duration
	}

	// Handle deletion
	if clusterExternalSecret.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, log, rotationInterval, clusterExternalSecret)
	}

	// Add finalizer if it doesn't exist
	if !utils.Contains(clusterExternalSecret.GetFinalizers(), clusterExternalSecretFinalizer) {
		if err := r.addFinalizer(log, clusterExternalSecret); err != nil {
			return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
		}
	}

	// Reconcile ExternalSecrets in matching namespaces
	if err := r.reconcileExternalSecrets(log, clusterExternalSecret); err != nil {
		log.Error(err, "failed to reconcile ExternalSecrets")
		r.updateStatusWithError(log, clusterExternalSecret, err)
		return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
	}

	// Update status to ready
	r.updateStatusWithReady(log, clusterExternalSecret)

	return r.Requeue(ctrl.Result{RequeueAfter: rotationInterval}), nil
}

// handleDeletion handle resource deletion logic
func (r *ClusterExternalSecretReconciler) handleDeletion(ctx context.Context, log logr.Logger, rotationInterval time.Duration, clusterExternalSecret *api.ClusterExternalSecret) (ctrl.Result, error) {
	if !utils.Contains(clusterExternalSecret.GetFinalizers(), clusterExternalSecretFinalizer) {
		return r.Requeue(reconcile.Result{RequeueAfter: rotationInterval}), nil
	}

	// exec the clean work in secretFinalizer
	// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
	if err := r.finalizeClusterExternalSecret(log, clusterExternalSecret); err != nil {
		log.Error(err, "failed to finalize ClusterExternalSecret")
		return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
	}

	// remove finalizer and update
	clusterExternalSecret.SetFinalizers(utils.Remove(clusterExternalSecret.GetFinalizers(), clusterExternalSecretFinalizer))
	err := r.Update(ctx, clusterExternalSecret)
	if err != nil {
		log.Error(err, "failed to remove finalizer from ClusterExternalSecret")
		return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
	}

	return r.Requeue(reconcile.Result{RequeueAfter: rotationInterval}), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterExternalSecretReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.ClusterExternalSecret{}, builder.WithPredicates(ClusterExternalSecretPredicate{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

func (r *ClusterExternalSecretReconciler) addFinalizer(logger logr.Logger, ces *api.ClusterExternalSecret) error {
	logger.Info("Adding Finalizer for the ClusterExternalSecret", "name", ces.Name)
	ces.SetFinalizers(append(ces.GetFinalizers(), clusterExternalSecretFinalizer))
	err := r.Update(context.TODO(), ces)
	if err != nil {
		logger.Error(err, "Failed to update ClusterExternalSecret with finalizer", "name", ces.Name)
		return err
	}
	return nil
}

// finalizeClusterExternalSecret cleans up ExternalSecrets created by this ClusterExternalSecret
func (r *ClusterExternalSecretReconciler) finalizeClusterExternalSecret(logger logr.Logger, ces *api.ClusterExternalSecret) error {
	logger.Info("Finalizing ClusterExternalSecret", "name", ces.Name)

	// Get all namespaces where ExternalSecrets were created
	namespaces := ces.Status.ProvisionedNamespaces
	for _, namespace := range namespaces {
		err := r.deleteExternalSecret(logger, ces, namespace)
		if err != nil {
			logger.Error(err, "Failed to delete ExternalSecret in namespace due to delete ClusterExternalSecret", "namespace", namespace)
		}
	}

	// Also try to delete ExternalSecret from all matching namespaces to ensure cleanup
	matchingNamespaces, err := r.getMatchingNamespaces(ces)
	if err != nil {
		logger.Error(err, "Failed to get matching namespaces during finalization")
	} else {
		for _, namespace := range matchingNamespaces {
			// Skip if already processed above
			found := false
			for _, provisionedNS := range namespaces {
				if provisionedNS == namespace {
					found = true
					break
				}
			}
			if found {
				continue
			}

			err := r.deleteExternalSecret(logger, ces, namespace)
			if err != nil {
				logger.Error(err, "Failed to delete ExternalSecret in namespace due to delete ClusterExternalSecret", "namespace", namespace)
			}
		}
	}

	logger.Info("Successfully finalized ClusterExternalSecret")
	return nil
}

// reconcileExternalSecrets creates or updates ExternalSecrets in matching namespaces
func (r *ClusterExternalSecretReconciler) reconcileExternalSecrets(logger logr.Logger, ces *api.ClusterExternalSecret) error {
	// Get namespaces that match the selectors
	matchingNamespaces, err := r.getMatchingNamespaces(ces)
	if err != nil {
		// Update status with the error
		failedNamespaces := []api.ClusterExternalSecretNamespaceFailure{
			{
				Namespace: "",
				Reason:    fmt.Sprintf("Failed to list namespaces: %v", err),
			},
		}
		ces.Status.ProvisionedNamespaces = []string{}
		ces.Status.FailedNamespaces = failedNamespaces

		// Try to update status even though we're returning an error
		statusErr := r.Status().Update(context.Background(), ces)
		if statusErr != nil {
			logger.Error(statusErr, "Failed to update ClusterExternalSecret status")
		}

		return fmt.Errorf("failed to get matching namespaces: %w", err)
	}

	// Track successful and failed namespaces
	var provisionedNamespaces []string
	var failedNamespaces []api.ClusterExternalSecretNamespaceFailure

	// Handle case when no namespaces match
	if len(matchingNamespaces) == 0 {
		provisionedNamespaces, failedNamespaces = r.handleNoMatchingNamespaces(logger, ces)
	} else {
		// Handle case when there are matching namespaces
		provisionedNamespaces, failedNamespaces = r.handleMatchingNamespaces(logger, ces, matchingNamespaces)
	}

	// Delete ExternalSecrets in namespaces that were previously provisioned but are no longer valid
	// This includes namespaces that no longer match selectors or no longer have access to SecretStores
	r.cleanupOrphanedExternalSecrets(logger, ces, matchingNamespaces)

	// Update status with provisioned and failed namespaces
	ces.Status.ProvisionedNamespaces = provisionedNamespaces
	ces.Status.FailedNamespaces = failedNamespaces

	return nil
}

// handleNoMatchingNamespaces handles the case when no namespaces match the conditions
func (r *ClusterExternalSecretReconciler) handleNoMatchingNamespaces(logger logr.Logger, ces *api.ClusterExternalSecret) ([]string, []api.ClusterExternalSecretNamespaceFailure) {
	provisionedNamespaces := make([]string, 0)
	failedNamespaces := make([]api.ClusterExternalSecretNamespaceFailure, 0)

	// Check if we have legacy NamespaceSelectors or new Conditions
	hasSelectors := len(ces.Spec.Conditions) > 0 || len(ces.Spec.NamespaceSelectors) > 0

	if hasSelectors {
		// Log that no namespaces matched the selectors
		logger.Info("No namespaces match the provided selectors. Checking all namespaces to provide detailed failure reasons.")

		// Get all namespaces to check why they don't match
		allNamespacesList := &corev1.NamespaceList{}
		listErr := r.List(r.Ctx, allNamespacesList)
		if listErr != nil {
			logger.Error(listErr, "Failed to list all namespaces for diagnostic purposes")
			failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
				Namespace: "",
				Reason:    fmt.Sprintf("Failed to list all namespaces for diagnostic: %v", listErr),
			})
		} else {
			// Check each namespace and record why it didn't match
			hasNamespace := false
			for _, namespace := range allNamespacesList.Items {
				hasNamespace = true
				if !utils.IsNamespaceAllowedForClusterExternalSecret(ces, namespace, r.Get) {
					// Namespace doesn't match, add to failed namespaces with reason
					failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
						Namespace: namespace.Name,
						Reason:    fmt.Sprintf("Namespace does not match provided selectors: labels=%v", namespace.Labels),
					})
				}
			}

			// If we still have no matching namespaces and no failed namespaces were added,
			// it means there are no namespaces in the cluster at all
			if !hasNamespace {
				failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
					Namespace: "",
					Reason:    "No namespaces found in the cluster",
				})
			}

			// If we have namespaces but none failed, it means all namespaces match but something else went wrong
			if hasNamespace && len(failedNamespaces) == 0 {
				failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
					Namespace: "",
					Reason:    "All namespaces match selectors but encountered other issues",
				})
			}
		}
	} else {
		// No selectors specified (neither old nor new), should match all namespaces but somehow got empty list
		failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
			Namespace: "",
			Reason:    "No namespaces found in the cluster when attempting to match all namespaces",
		})
	}

	return provisionedNamespaces, failedNamespaces
}

// handleMatchingNamespaces processes each matching namespace and returns the results
func (r *ClusterExternalSecretReconciler) handleMatchingNamespaces(logger logr.Logger, ces *api.ClusterExternalSecret, matchingNamespaces []string) ([]string, []api.ClusterExternalSecretNamespaceFailure) {
	// Track successful and failed namespaces
	provisionedNamespaces := make([]string, 0)
	failedNamespaces := make([]api.ClusterExternalSecretNamespaceFailure, 0)

	// Process each matching namespace
	for _, namespace := range matchingNamespaces {
		// First validate access to SecretStores
		if err := r.validateSecretStoreAccess(ces, namespace); err != nil {
			// Namespace matches selectors but fails access validation
			failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
				Namespace: namespace,
				Reason:    err.Error(),
			})

			// If ExternalSecret was previously created in this namespace, delete it now
			// because it no longer has access to required SecretStores
			if r.isNamespaceProvisioned(ces, namespace) {
				deleteErr := r.deleteExternalSecret(logger, ces, namespace)
				if deleteErr != nil {
					logger.Error(deleteErr, "Failed to delete ExternalSecret in namespace that no longer has access", "namespace", namespace)
				}
			}

			logger.Error(err, "Access validation failed for namespace", "namespace", namespace)
			continue
		}

		// Namespace matches selectors and has access, create or update ExternalSecret
		err := r.createOrUpdateExternalSecret(logger, ces, namespace)
		if err != nil {
			failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
				Namespace: namespace,
				Reason:    err.Error(),
			})
			logger.Error(err, "Failed to create/update ExternalSecret in namespace", "namespace", namespace)
		} else {
			provisionedNamespaces = append(provisionedNamespaces, namespace)
		}
	}

	return provisionedNamespaces, failedNamespaces
}

// getMatchingNamespaces returns namespaces that match the ClusterExternalSecret's conditions
func (r *ClusterExternalSecretReconciler) getMatchingNamespaces(ces *api.ClusterExternalSecret) ([]string, error) {
	namespaceList := &corev1.NamespaceList{}
	err := r.List(r.Ctx, namespaceList)
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	matchingNamespaces := make([]string, 0)

	// Check each namespace against the conditions using new utility function
	for _, namespace := range namespaceList.Items {
		if utils.IsNamespaceAllowedForClusterExternalSecret(ces, namespace, r.Get) {
			matchingNamespaces = append(matchingNamespaces, namespace.Name)
		}
	}

	return matchingNamespaces, nil
}

// createOrUpdateExternalSecret creates or updates an ExternalSecret in the specified namespace
func (r *ClusterExternalSecretReconciler) createOrUpdateExternalSecret(logger logr.Logger, ces *api.ClusterExternalSecret, namespace string) error {
	// Check if ExternalSecretSpec references a ClusterSecretStore and validate namespace access
	if err := r.validateSecretStoreAccess(ces, namespace); err != nil {
		return fmt.Errorf("access validation failed for namespace %s: %w", namespace, err)
	}

	externalSecretName := getExternalSecretName(ces)
	externalSecret := &api.ExternalSecret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ExternalSecret",
			APIVersion: api.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        externalSecretName,
			Namespace:   namespace,
			Labels:      ces.Spec.ExternalSecretMetadata.Labels,
			Annotations: ces.Spec.ExternalSecretMetadata.Annotations,
		},
		Spec: ces.Spec.ExternalSecretSpec,
	}

	// Set owner reference to allow garbage collection
	if err := ctrl.SetControllerReference(ces, externalSecret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	// Use Patch operation instead of separate Create/Update
	// This is more efficient and avoids race conditions
	clientOpts := []client.PatchOption{client.ForceOwnership, client.FieldOwner("ack-secret-manager")}

	err := r.Patch(r.Ctx, externalSecret, client.Apply, clientOpts...)
	if err != nil {
		return fmt.Errorf("failed to apply ExternalSecret: %w", err)
	}

	logger.Info("Applied ExternalSecret", "name", externalSecretName, "namespace", namespace)
	return nil
}

// validateSecretStoreAccess validates that the namespace has access to referenced SecretStore/ClusterSecretStore
func (r *ClusterExternalSecretReconciler) validateSecretStoreAccess(ces *api.ClusterExternalSecret, namespace string) error {
	// Check SecretStoreRef in the ExternalSecretSpec
	// ExternalSecretSpec doesn't have a top-level SecretStoreRef, it's per-data source
	// We need to check all data sources for SecretStoreRef

	// Check data sources
	for _, dataSource := range ces.Spec.ExternalSecretSpec.Data {
		if err := r.validateDataSourceSecretStoreAccess(dataSource, namespace); err != nil {
			return err
		}
	}

	// Check dataProcess sources
	for _, dataProcess := range ces.Spec.ExternalSecretSpec.DataProcess {
		if dataProcess.Extract != nil {
			if err := r.validateDataSourceSecretStoreAccess(*dataProcess.Extract, namespace); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateDataSourceSecretStoreAccess validates that the namespace has access to SecretStore/ClusterSecretStore referenced in a data source
func (r *ClusterExternalSecretReconciler) validateDataSourceSecretStoreAccess(dataSource api.DataSource, namespace string) error {
	secretStoreRef := dataSource.SecretStoreRef
	if secretStoreRef == nil || secretStoreRef.Name == "" {
		// No SecretStoreRef specified, nothing to validate
		return nil
	}

	// Check if cross namespace reference is enabled
	if !r.EnableCrossNamespace && secretStoreRef.Namespace != "" && secretStoreRef.Namespace != namespace && secretStoreRef.Kind != "ClusterSecretStore" {
		return fmt.Errorf("cross namespace SecretStore reference is disabled, cannot reference SecretStore in namespace %s from namespace %s", secretStoreRef.Namespace, namespace)
	}

	// Determine the kind of SecretStoreRef
	kind := secretStoreRef.Kind
	if kind == "" {
		kind = "SecretStore" // Default to SecretStore
	}

	switch kind {
	case "SecretStore":
		// SecretStore is namespace-scoped, no additional validation needed as ClusterExternalSecret
		// can only create ExternalSecrets in namespaces where it has permissions

	case "ClusterSecretStore":
		// Check if namespace is allowed to access this ClusterSecretStore
		clusterSecretStore := &api.ClusterSecretStore{}
		err := r.Get(context.Background(), client.ObjectKey{
			Name: secretStoreRef.Name,
		}, clusterSecretStore)
		if err != nil {
			return fmt.Errorf("failed to get ClusterSecretStore %s: %w", secretStoreRef.Name, err)
		}

		// Validate if namespace is allowed to access this ClusterSecretStore
		if !utils.IsNamespaceAllowedForClusterSecretStore(clusterSecretStore, namespace, r.Get) {
			return fmt.Errorf("namespace %s is not allowed to access ClusterSecretStore %s", namespace, secretStoreRef.Name)
		}

	default:
		return fmt.Errorf("unsupported SecretStoreRef Kind: %s, must be SecretStore or ClusterSecretStore", kind)
	}

	return nil
}

// isNamespaceProvisioned checks if a namespace is in the list of provisioned namespaces
func (r *ClusterExternalSecretReconciler) isNamespaceProvisioned(ces *api.ClusterExternalSecret, namespace string) bool {
	for _, ns := range ces.Status.ProvisionedNamespaces {
		if ns == namespace {
			return true
		}
	}

	return false
}

// deleteExternalSecret deletes an ExternalSecret in the specified namespace
func (r *ClusterExternalSecretReconciler) deleteExternalSecret(logger logr.Logger, ces *api.ClusterExternalSecret, namespace string) error {
	externalSecretName := getExternalSecretName(ces)
	externalSecret := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      externalSecretName,
			Namespace: namespace,
		},
	}

	err := r.Delete(r.Ctx, externalSecret)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete ExternalSecret %s in namespace %s: %w", externalSecretName, namespace, err)
	}

	logger.Info("Deleted ExternalSecret", "name", externalSecretName, "namespace", namespace)
	return nil
}

// cleanupOrphanedExternalSecrets deletes ExternalSecrets in namespaces that were previously provisioned
// but are no longer valid (either don't match selectors or lost access to SecretStores)
func (r *ClusterExternalSecretReconciler) cleanupOrphanedExternalSecrets(logger logr.Logger, ces *api.ClusterExternalSecret, matchingNamespaces []string) {
	// Create a set of currently matching namespaces for quick lookup
	matchingNamespaceSet := make(map[string]bool)
	for _, ns := range matchingNamespaces {
		matchingNamespaceSet[ns] = true
	}

	// Delete ExternalSecrets in namespaces that were previously provisioned but are no longer valid
	for _, namespace := range ces.Status.ProvisionedNamespaces {
		// If namespace still matches selectors, it was already handled in the main loop
		// So we only need to delete ExternalSecrets in namespaces that no longer match
		if !matchingNamespaceSet[namespace] {
			err := r.deleteExternalSecret(logger, ces, namespace)
			if err != nil {
				logger.Error(err, "Failed to delete orphaned ExternalSecret in namespace that no longer matches", "namespace", namespace)
			}
		}
	}
}

// updateStatusWithError updates the status with an error condition
func (r *ClusterExternalSecretReconciler) updateStatusWithError(logger logr.Logger, ces *api.ClusterExternalSecret, err error) {
	condition := api.ClusterExternalSecretStatusCondition{
		Type:    ClusterExternalSecretReady,
		Status:  corev1.ConditionFalse,
		Message: err.Error(),
	}

	r.setCondition(ces, condition)

	statusErr := r.Status().Update(context.Background(), ces)
	if statusErr != nil {
		logger.Error(statusErr, "Failed to update ClusterExternalSecret status with error")
	}
}

// updateStatusWithReady updates the status to indicate the store is ready
func (r *ClusterExternalSecretReconciler) updateStatusWithReady(logger logr.Logger, ces *api.ClusterExternalSecret) {
	condition := api.ClusterExternalSecretStatusCondition{
		Type:   ClusterExternalSecretReady,
		Status: corev1.ConditionTrue,
	}

	r.setCondition(ces, condition)

	statusErr := r.Status().Update(context.Background(), ces)
	if statusErr != nil {
		logger.Error(statusErr, "Failed to update ClusterExternalSecret status with ready condition")
	}
}

// setCondition sets a condition in the ClusterExternalSecret status
func (r *ClusterExternalSecretReconciler) setCondition(ces *api.ClusterExternalSecret, condition api.ClusterExternalSecretStatusCondition) {
	now := metav1.Now()
	// Check if condition already exists
	for i, c := range ces.Status.Conditions {
		if c.Type == condition.Type {
			// If condition status is changing, update the transition time
			if c.Status != condition.Status {
				condition.LastTransitionTime = now
				ces.Status.Conditions[i] = condition
			} else {
				// Preserve the original transition time if status hasn't changed
				condition.LastTransitionTime = c.LastTransitionTime
				ces.Status.Conditions[i] = condition
			}
			return
		}
	}

	// Add new condition with current time
	condition.LastTransitionTime = now
	ces.Status.Conditions = append(ces.Status.Conditions, condition)
}

func getExternalSecretName(ces *api.ClusterExternalSecret) string {
	externalSecretName := ces.Spec.ExternalSecretName
	if externalSecretName == "" {
		externalSecretName = ces.Name
	}

	return externalSecretName
}

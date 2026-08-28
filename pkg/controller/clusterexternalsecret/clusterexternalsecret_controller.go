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
	"reflect"
	"strings"
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
	"sigs.k8s.io/controller-runtime/pkg/handler"
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
			// Deleted meanwhile; owned objects are garbage collected.
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	rotationInterval := r.RotationInterval
	if clusterExternalSecret.Spec.RotationInterval != nil {
		rotationInterval = clusterExternalSecret.Spec.RotationInterval.Duration
	}

	if clusterExternalSecret.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, log, rotationInterval, clusterExternalSecret)
	}

	if !utils.Contains(clusterExternalSecret.GetFinalizers(), clusterExternalSecretFinalizer) {
		if err := r.addFinalizer(log, clusterExternalSecret); err != nil {
			return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
		}
	}

	// Snapshot the status BEFORE reconcileExternalSecrets mutates the
	// namespace ledgers in place: the debounce comparison must cover those
	// list changes too, otherwise a stable Ready condition (preserved
	// LastTransitionTime) would swallow ProvisionedNamespaces updates
	// forever.
	originalStatus := clusterExternalSecret.Status.DeepCopy()

	if err := r.reconcileExternalSecrets(log, clusterExternalSecret, originalStatus); err != nil {
		log.Error(err, "failed to reconcile ExternalSecrets")
		// The reconcile error already drives the retry; the status write here
		// is best-effort and its failure is logged rather than masking err.
		if statusErr := r.updateStatusWithError(log, clusterExternalSecret, err); statusErr != nil {
			log.Error(statusErr, "failed to update ClusterExternalSecret status with error")
		}
		return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
	}

	// Ready only when every selected namespace was provisioned; any failure
	// flips Ready to False with a summary so partial failures are observable.
	// A failed status write is returned so the workqueue retries with backoff,
	// matching the secretstore controller instead of swallowing the error.
	if len(clusterExternalSecret.Status.FailedNamespaces) > 0 {
		if statusErr := r.updateStatusWithFailure(log, clusterExternalSecret, originalStatus, clusterExternalSecret.Status.FailedNamespaces); statusErr != nil {
			return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), statusErr
		}
	} else {
		if statusErr := r.updateStatusWithReady(log, clusterExternalSecret, originalStatus); statusErr != nil {
			return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), statusErr
		}
	}

	return r.Requeue(ctrl.Result{RequeueAfter: rotationInterval}), nil
}

// handleDeletion handle resource deletion logic
func (r *ClusterExternalSecretReconciler) handleDeletion(ctx context.Context, log logr.Logger, rotationInterval time.Duration, clusterExternalSecret *api.ClusterExternalSecret) (ctrl.Result, error) {
	if !utils.Contains(clusterExternalSecret.GetFinalizers(), clusterExternalSecretFinalizer) {
		return r.Requeue(reconcile.Result{RequeueAfter: rotationInterval}), nil
	}

	// Keep the finalizer while cleanup fails; it retries next reconcile
	if err := r.finalizeClusterExternalSecret(log, clusterExternalSecret); err != nil {
		log.Error(err, "failed to finalize ClusterExternalSecret")
		return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
	}

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
		// Watch namespaces so newly created or relabeled namespaces are
		// provisioned immediately instead of waiting for the rotation poll.
		Watches(&corev1.Namespace{},
			handler.EnqueueRequestsFromMapFunc(r.mapNamespaceToCES),
			builder.WithPredicates(NamespaceWatchPredicate{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

// mapNamespaceToCES enqueues every ClusterExternalSecret selecting the
// changed namespace (delete events included, to clean the
// provisionedNamespaces ledger). Matching reuses
// utils.IsNamespaceAllowedForClusterExternalSecret to stay consistent with
// the provisioning path.
func (r *ClusterExternalSecretReconciler) mapNamespaceToCES(ctx context.Context, obj client.Object) []reconcile.Request {
	namespace, ok := obj.(*corev1.Namespace)
	if !ok {
		return nil
	}

	cesList := &api.ClusterExternalSecretList{}
	if err := r.List(ctx, cesList); err != nil {
		r.Log.Error(err, "Failed to list ClusterExternalSecrets while mapping namespace event", "namespace", namespace.Name)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(cesList.Items))
	for i := range cesList.Items {
		ces := &cesList.Items[i]
		if utils.IsNamespaceAllowedForClusterExternalSecret(ces, *namespace) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKey{Name: ces.Name}})
		}
	}
	return requests
}

func (r *ClusterExternalSecretReconciler) addFinalizer(logger logr.Logger, ces *api.ClusterExternalSecret) error {
	logger.Info("Adding Finalizer for the ClusterExternalSecret", "name", ces.Name)
	ces.SetFinalizers(append(ces.GetFinalizers(), clusterExternalSecretFinalizer))
	err := r.Update(r.Ctx, ces)
	if err != nil {
		logger.Error(err, "Failed to update ClusterExternalSecret with finalizer", "name", ces.Name)
		return err
	}
	return nil
}

// finalizeClusterExternalSecret cleans up ExternalSecrets created by this ClusterExternalSecret
func (r *ClusterExternalSecretReconciler) finalizeClusterExternalSecret(logger logr.Logger, ces *api.ClusterExternalSecret) error {
	logger.Info("Finalizing ClusterExternalSecret", "name", ces.Name)

	namespaces := ces.Status.ProvisionedNamespaces
	for _, namespace := range namespaces {
		err := r.deleteExternalSecret(logger, ces, namespace)
		if err != nil {
			logger.Error(err, "Failed to delete ExternalSecret in namespace due to delete ClusterExternalSecret", "namespace", namespace)
		}
	}

	// Also sweep all matching namespaces to cover stale entries
	matchingNamespaces, err := r.getMatchingNamespaces(ces)
	if err != nil {
		logger.Error(err, "Failed to get matching namespaces during finalization")
	} else {
		for _, namespace := range matchingNamespaces {
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

// reconcileExternalSecrets creates or updates ExternalSecrets in matching
// namespaces. originalStatus is the status snapshot taken by Reconcile before
// any in-place mutation, reused for the debounced status write-back.
func (r *ClusterExternalSecretReconciler) reconcileExternalSecrets(logger logr.Logger, ces *api.ClusterExternalSecret, originalStatus *api.ClusterExternalSecretStatus) error {
	matchingNamespaces, err := r.getMatchingNamespaces(ces)
	if err != nil {
		// Record the failure but keep the ProvisionedNamespaces ledger intact:
		// a transient List failure must not wipe it (finalizer cleanup depends
		// on it).
		ces.Status.FailedNamespaces = []api.ClusterExternalSecretNamespaceFailure{
			{
				Namespace: "",
				Reason:    fmt.Sprintf("Failed to list namespaces: %v", err),
			},
		}

		// Best-effort status update (debounced: no Update when unchanged); the
		// returned error is logged since this path already returns a reconcile
		// error that drives the retry.
		if statusErr := r.updateStatusIfNeeded(logger, ces, originalStatus); statusErr != nil {
			logger.Error(statusErr, "Failed to update ClusterExternalSecret status")
		}

		return fmt.Errorf("failed to get matching namespaces: %w", err)
	}

	var provisionedNamespaces []string
	var failedNamespaces []api.ClusterExternalSecretNamespaceFailure

	if len(matchingNamespaces) == 0 {
		provisionedNamespaces, failedNamespaces = r.handleNoMatchingNamespaces(logger, ces)
	} else {
		provisionedNamespaces, failedNamespaces = r.handleMatchingNamespaces(logger, ces, matchingNamespaces)
	}

	// Delete ExternalSecrets in namespaces no longer valid (selector mismatch
	// or lost SecretStore access)
	r.cleanupOrphanedExternalSecrets(logger, ces, matchingNamespaces)

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
		logger.Info("No namespaces match the provided selectors. Checking all namespaces to provide detailed failure reasons.")

		// List all namespaces to record why each one did not match
		allNamespacesList := &corev1.NamespaceList{}
		listErr := r.List(r.Ctx, allNamespacesList)
		if listErr != nil {
			logger.Error(listErr, "Failed to list all namespaces for diagnostic purposes")
			failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
				Namespace: "",
				Reason:    fmt.Sprintf("Failed to list all namespaces for diagnostic: %v", listErr),
			})
		} else {
			hasNamespace := false
			for _, namespace := range allNamespacesList.Items {
				hasNamespace = true
				if !utils.IsNamespaceAllowedForClusterExternalSecret(ces, namespace) {
					failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
						Namespace: namespace.Name,
						Reason:    fmt.Sprintf("Namespace does not match provided selectors: labels=%v", namespace.Labels),
					})
				}
			}

			// No namespaces at all in the cluster
			if !hasNamespace {
				failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
					Namespace: "",
					Reason:    "No namespaces found in the cluster",
				})
			}

			// Namespaces exist and none failed: all match but something else went wrong
			if hasNamespace && len(failedNamespaces) == 0 {
				failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
					Namespace: "",
					Reason:    "All namespaces match selectors but encountered other issues",
				})
			}
		}
	} else {
		// No selectors configured: should match all namespaces, so an empty
		// list means the cluster has none
		failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
			Namespace: "",
			Reason:    "No namespaces found in the cluster when attempting to match all namespaces",
		})
	}

	return provisionedNamespaces, failedNamespaces
}

// handleMatchingNamespaces processes each matching namespace and returns the results
func (r *ClusterExternalSecretReconciler) handleMatchingNamespaces(logger logr.Logger, ces *api.ClusterExternalSecret, matchingNamespaces []string) ([]string, []api.ClusterExternalSecretNamespaceFailure) {
	provisionedNamespaces := make([]string, 0)
	failedNamespaces := make([]api.ClusterExternalSecretNamespaceFailure, 0)

	for _, namespace := range matchingNamespaces {
		// Validate SecretStore access first
		if err := r.validateSecretStoreAccess(ces, namespace); err != nil {
			failedNamespaces = append(failedNamespaces, api.ClusterExternalSecretNamespaceFailure{
				Namespace: namespace,
				Reason:    err.Error(),
			})

			// Access lost: remove a previously provisioned ExternalSecret
			if r.isNamespaceProvisioned(ces, namespace) {
				deleteErr := r.deleteExternalSecret(logger, ces, namespace)
				if deleteErr != nil {
					logger.Error(deleteErr, "Failed to delete ExternalSecret in namespace that no longer has access", "namespace", namespace)
				}
			}

			logger.Error(err, "Access validation failed for namespace", "namespace", namespace)
			continue
		}

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

	for _, namespace := range namespaceList.Items {
		if utils.IsNamespaceAllowedForClusterExternalSecret(ces, namespace) {
			matchingNamespaces = append(matchingNamespaces, namespace.Name)
		}
	}

	return matchingNamespaces, nil
}

// createOrUpdateExternalSecret creates or updates an ExternalSecret in the specified namespace
func (r *ClusterExternalSecretReconciler) createOrUpdateExternalSecret(logger logr.Logger, ces *api.ClusterExternalSecret, namespace string) error {
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

	if err := ctrl.SetControllerReference(ces, externalSecret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference: %w", err)
	}

	// Server-side apply avoids Create/Update races
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
	// SecretStoreRef is per-data-source, so every source must be checked
	for _, dataSource := range ces.Spec.ExternalSecretSpec.Data {
		if err := r.validateDataSourceSecretStoreAccess(dataSource, namespace); err != nil {
			return err
		}
	}

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
		return nil
	}

	if !r.EnableCrossNamespace && secretStoreRef.Namespace != "" && secretStoreRef.Namespace != namespace && secretStoreRef.Kind != "ClusterSecretStore" {
		return fmt.Errorf("cross namespace SecretStore reference is disabled, cannot reference SecretStore in namespace %s from namespace %s", secretStoreRef.Namespace, namespace)
	}

	kind := secretStoreRef.Kind
	if kind == "" {
		kind = "SecretStore" // Default to SecretStore
	}

	switch kind {
	case "SecretStore":
		// Namespace-scoped; the created ExternalSecret lives in the same
		// namespace, so no further validation is needed

	case "ClusterSecretStore":
		clusterSecretStore := &api.ClusterSecretStore{}
		err := r.Get(r.Ctx, client.ObjectKey{
			Name: secretStoreRef.Name,
		}, clusterSecretStore)
		if err != nil {
			return fmt.Errorf("failed to get ClusterSecretStore %s: %w", secretStoreRef.Name, err)
		}

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
// but are no longer valid (either don't match selectors or lost access to
// SecretStores)
func (r *ClusterExternalSecretReconciler) cleanupOrphanedExternalSecrets(logger logr.Logger, ces *api.ClusterExternalSecret, matchingNamespaces []string) {
	matchingNamespaceSet := make(map[string]bool)
	for _, ns := range matchingNamespaces {
		matchingNamespaceSet[ns] = true
	}

	for _, namespace := range ces.Status.ProvisionedNamespaces {
		// Still-matching namespaces were already handled in the main loop
		if !matchingNamespaceSet[namespace] {
			err := r.deleteExternalSecret(logger, ces, namespace)
			if err != nil {
				logger.Error(err, "Failed to delete orphaned ExternalSecret in namespace that no longer matches", "namespace", namespace)
			}
		}
	}
}

// updateStatusWithError updates the status with an error condition. The
// error path is reached before reconcileExternalSecrets mutates the ledgers,
// so a snapshot taken here still covers every status change of this pass.
func (r *ClusterExternalSecretReconciler) updateStatusWithError(logger logr.Logger, ces *api.ClusterExternalSecret, err error) error {
	condition := api.ClusterExternalSecretStatusCondition{
		Type:    ClusterExternalSecretReady,
		Status:  corev1.ConditionFalse,
		Message: err.Error(),
	}

	originalStatus := ces.Status.DeepCopy()
	r.setCondition(ces, condition)
	return r.updateStatusIfNeeded(logger, ces, originalStatus)
}

// updateStatusWithReady updates the status to indicate the resource is
// ready. originalStatus is the snapshot taken before the namespace ledgers
// were rebuilt, so the debounce also detects list-only changes.
func (r *ClusterExternalSecretReconciler) updateStatusWithReady(logger logr.Logger, ces *api.ClusterExternalSecret, originalStatus *api.ClusterExternalSecretStatus) error {
	condition := api.ClusterExternalSecretStatusCondition{
		Type:   ClusterExternalSecretReady,
		Status: corev1.ConditionTrue,
	}

	r.setCondition(ces, condition)
	return r.updateStatusIfNeeded(logger, ces, originalStatus)
}

// updateStatusWithFailure marks Ready=False with a failure summary when some
// selected namespaces failed to be provisioned. originalStatus is the
// snapshot taken before the namespace ledgers were rebuilt, so the debounce
// also detects list-only changes.
func (r *ClusterExternalSecretReconciler) updateStatusWithFailure(logger logr.Logger, ces *api.ClusterExternalSecret, originalStatus *api.ClusterExternalSecretStatus, failedNamespaces []api.ClusterExternalSecretNamespaceFailure) error {
	condition := api.ClusterExternalSecretStatusCondition{
		Type:    ClusterExternalSecretReady,
		Status:  corev1.ConditionFalse,
		Message: summarizeNamespaceFailures(failedNamespaces),
	}

	r.setCondition(ces, condition)
	return r.updateStatusIfNeeded(logger, ces, originalStatus)
}

// summarizeNamespaceFailures builds a short human-readable summary listing
// at most a few namespace names plus the total count.
func summarizeNamespaceFailures(failedNamespaces []api.ClusterExternalSecretNamespaceFailure) string {
	names := make([]string, 0, len(failedNamespaces))
	for _, f := range failedNamespaces {
		name := f.Namespace
		if name == "" {
			name = "<cluster-wide>"
		}
		names = append(names, name)
	}

	const maxListedNames = 5
	if len(names) > maxListedNames {
		return fmt.Sprintf("failed to provision ExternalSecrets in %d namespace(s): %s, ...",
			len(failedNamespaces), strings.Join(names[:maxListedNames], ", "))
	}
	return fmt.Sprintf("failed to provision ExternalSecrets in %d namespace(s): %s",
		len(failedNamespaces), strings.Join(names, ", "))
}

// normalizeStatusLists collapses empty lists to nil so the debounce
// comparison treats both as equal: the API server round-trip serializes an
// empty slice as JSON null, so a stored empty list reads back as nil and a
// raw DeepEqual would report a spurious diff against the freshly rebuilt
// (empty, non-nil) in-memory lists.
func normalizeStatusLists(status *api.ClusterExternalSecretStatus) {
	if len(status.ProvisionedNamespaces) == 0 {
		status.ProvisionedNamespaces = nil
	}
	if len(status.FailedNamespaces) == 0 {
		status.FailedNamespaces = nil
	}
}

// updateStatusIfNeeded writes the status subresource only when it changed
// (debounce; LastTransitionTime preservation keeps unchanged statuses equal).
// The list fields are normalized (empty == nil) on both sides because the
// status read back from the API server loses the empty/nil distinction.
// A failed write is returned (not swallowed) so Reconcile can requeue with
// workqueue backoff, matching the secretstore controller convention.
func (r *ClusterExternalSecretReconciler) updateStatusIfNeeded(logger logr.Logger, ces *api.ClusterExternalSecret, originalStatus *api.ClusterExternalSecretStatus) error {
	// Normalize list fields on copies of both sides so empty and nil compare
	// equal without touching the caller-owned objects.
	original := *originalStatus
	normalizeStatusLists(&original)
	current := ces.Status
	normalizeStatusLists(&current)
	if reflect.DeepEqual(&original, &current) {
		return nil
	}

	statusErr := r.Status().Update(r.Ctx, ces)
	if statusErr != nil {
		if errors.IsConflict(statusErr) {
			logger.Info("conflict when updating ClusterExternalSecret status, will be retried by workqueue backoff", "error", statusErr)
		} else {
			logger.Error(statusErr, "Failed to update ClusterExternalSecret status")
		}
		return statusErr
	}
	return nil
}

// setCondition sets a condition in the ClusterExternalSecret status
func (r *ClusterExternalSecretReconciler) setCondition(ces *api.ClusterExternalSecret, condition api.ClusterExternalSecretStatusCondition) {
	now := metav1.Now()
	for i, c := range ces.Status.Conditions {
		if c.Type == condition.Type {
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

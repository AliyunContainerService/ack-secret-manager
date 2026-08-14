/*

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

package externalsecret

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	secretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"
)

// ExternalSecretReconciler reconciles a ExternalSecret object
type ExternalSecretReconciler struct {
	client.Client
	APIReader              client.Reader
	Log                    logr.Logger
	Ctx                    context.Context
	WatchNamespaces        map[string]bool
	ReconciliationPeriod   time.Duration
	CleanUpSecretOnFailure bool
	DisablePolling         bool
	RotationInterval       time.Duration // Key rotation job running interval.
	KmsLimiter             KmsLimiter
	OosLimiter             OosLimiter
	EnableCrossNamespace   bool
	RestConfig             *rest.Config

	// Composite-key client tracker: the previous-round set of
	// "clientName#endpoint" cache keys per ExternalSecret UID. It is the
	// diff baseline for reclaiming composite clients when a spec's
	// kmsEndpoint is modified or removed (see
	// reconcileStaleCompositeClients). In-memory only: after a restart the
	// first observation round re-baselines without reclaiming.
	compositeKeysMu   sync.Mutex
	compositeKeysByES map[types.UID]map[string]struct{}
}

// WrappedClient wraps both controller-runtime client and kubernetes client
type WrappedClient struct {
	client.Client
	KubeClient kubernetes.Interface
}

// GetKubeClient returns the kubernetes client interface
func (w *WrappedClient) GetKubeClient() kubernetes.Interface {
	return w.KubeClient
}

// getCurrentData gets the current Secret state (data plus labels and
// annotations) from the secret api. The labels/annotations are consumed by
// the template-metadata debounce so a round whose metadata targets are
// already applied does not force a Secret rewrite.
func (r *ExternalSecretReconciler) getCurrentData(ctx context.Context, namespace string, name string) (map[string][]byte, map[string]string, map[string]string, error) {
	reader := r.APIReader
	data := make(map[string][]byte)
	labels := make(map[string]string)
	annotations := make(map[string]string)
	secret := &corev1.Secret{}
	r.Log.Info("getCurrentData for", "ns", namespace, "name", name)
	err := reader.Get(ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, secret)
	if err != nil {
		return data, labels, annotations, err
	}
	for k, v := range secret.Data {
		data[k] = v
	}
	for k, v := range secret.Labels {
		labels[k] = v
	}
	for k, v := range secret.Annotations {
		annotations[k] = v
	}
	return data, labels, annotations, err
}

// updateSecret writes the resolved dataset (and template metadata targets) to
// the target Secret via the secret operation handler, which also applies the
// CleanUpSecretOnFailure deletion contract for empty datasets.
func (r *ExternalSecretReconciler) updateSecret(externalSec *api.ExternalSecret, secretMap map[string][]byte, currentData map[string][]byte, metadataTargets map[string]map[string]string) error {
	// Validate input parameters
	if externalSec == nil {
		return fmt.Errorf("externalSec cannot be nil")
	}
	if secretMap == nil {
		secretMap = make(map[string][]byte)
	}
	if currentData == nil {
		currentData = make(map[string][]byte)
	}

	// Create secret operation handler with all required context
	handler := NewSimpleSecretOperationHandler(r.Client, r.CleanUpSecretOnFailure, r.Log)

	// Execute the complete secret operation with all policies and template processing
	err := handler.HandleSecretOperation(r.Ctx, externalSec, secretMap, currentData, metadataTargets)
	if err != nil {
		return err
	}

	return nil
}

// shouldWatch will return true if the ExternalSecret is in a watchable namespace
func (r *ExternalSecretReconciler) shouldWatch(externalSecNamespace string) bool {
	if len(r.WatchNamespaces) > 0 {
		return r.WatchNamespaces[externalSecNamespace]
	}
	return true
}

// isNamespaceTerminating checks if a namespace is in terminating state
func (r *ExternalSecretReconciler) isNamespaceTerminating(namespace string) (bool, error) {
	ns := &corev1.Namespace{}
	err := r.Get(r.Ctx, client.ObjectKey{Name: namespace}, ns)
	if err != nil {
		return false, err
	}

	// Check if namespace is terminating
	return ns.Status.Phase == corev1.NamespaceTerminating, nil
}

func (r *ExternalSecretReconciler) RequeueAfter(duration time.Duration) ctrl.Result {
	if r.DisablePolling {
		return ctrl.Result{Requeue: false}
	}
	return ctrl.Result{RequeueAfter: duration}
}

func (r *ExternalSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ExternalSecret", req.NamespacedName)

	externalSec := &api.ExternalSecret{}

	// only do not requeue when getting CR fails.
	err := r.Get(r.Ctx, req.NamespacedName, externalSec)
	if err != nil {
		// Only return error if it's not NotFound - NotFound is normal when resource is deleted
		return ctrl.Result{}, utils.IgnoreNotFoundError(err)
	}

	// Determine the actual secret name to use
	secretName := externalSec.Name
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Name != "" {
		secretName = externalSec.Spec.Target.Name
	}

	secretNamespace := externalSec.Namespace
	secretIndex := fmt.Sprintf("namespace/%s/%s", secretNamespace, secretName)
	log = log.WithValues("secret", secretIndex)
	r.Log.Info("externalSec info", "secretName", secretName, "secretNamespace", secretNamespace)

	rotationInterval := r.RotationInterval
	if externalSec.Spec.RotationInterval != nil {
		rotationInterval = externalSec.Spec.RotationInterval.Duration
	}

	// Handle deletion - resource lifecycle management
	if externalSec.GetDeletionTimestamp() != nil {
		r.updateResourceManagementStatus(externalSec, "operation", fmt.Errorf("external secret is being deleted"))
		return r.handleDeletion(ctx, log, externalSec, rotationInterval, secretName)
	}

	klog.Infof("reconcile external secret %v", secretIndex)

	// add Finalizer to external secret instance
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		if err := r.addFinalizer(ctx, log, externalSec); err != nil {
			r.updateResourceManagementStatus(externalSec, "finalizer", err)
			return r.RequeueAfter(r.ReconciliationPeriod), err
		}
	}

	if !r.shouldWatch(secretNamespace) {
		watchErr := fmt.Errorf("namespace %s is not in watched namespaces", secretNamespace)
		r.updateResourceManagementStatus(externalSec, "namespace_access", watchErr)
		return ctrl.Result{}, nil
	}

	// Check if namespace is terminating before proceeding
	if isTerminating, err := r.isNamespaceTerminating(secretNamespace); err != nil {
		r.updateResourceManagementStatus(externalSec, "namespace_check", err)
		return ctrl.Result{}, err
	} else if isTerminating {
		// Namespace is terminating, skip secret creation
		r.Log.Info("Skipping secret creation as namespace is terminating", "namespace", secretNamespace)
		r.updateResourceManagementStatus(externalSec, "namespace_terminating", fmt.Errorf("namespace %s is terminating", secretNamespace))
		return ctrl.Result{}, nil
	}

	// Reclaim composite ("clientName#endpoint") clients whose endpoint was
	// modified or removed from the spec since the previous round. Runs
	// before the data sync so this round's on-demand client re-creation
	// sees a consistent cache. Fail-closed: a cleanup failure is reported
	// and retried, never treated as success.
	if err := r.reconcileStaleCompositeClients(ctx, log, externalSec); err != nil {
		// Context cancellation ends the round early; it is not a reconcile error.
		if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
			log.V(4).Info("reconcile ended early due to context cancellation/deadline", "stage", "endpoint_client_cleanup")
			return ctrl.Result{}, nil
		}
		r.updateResourceManagementStatus(externalSec, "endpoint_client_cleanup", err)
		return ctrl.Result{}, err
	}

	// Delegate ALL data synchronization to syncIfNeedUpdate.
	// Request-scoped ctx covers fetch/pull-limiter/backoff waits; Secret writes
	// and status updates use r.Ctx.
	_, syncErr := r.syncIfNeedUpdate(ctx, externalSec)

	// syncIfNeedUpdate handles its own status updates for data-related operations
	// We only handle requeuing logic here
	if syncErr != nil {
		// Context cancellation ends the round early; it is not a reconcile error.
		if stderrors.Is(syncErr, context.Canceled) || stderrors.Is(syncErr, context.DeadlineExceeded) {
			log.V(4).Info("reconcile ended early due to context cancellation/deadline")
			return ctrl.Result{}, nil
		}
		// Return only error to let controller-runtime handle exponential backoff
		// RequeueAfter result is ignored when error is non-nil
		return ctrl.Result{}, syncErr
	}

	log.Info("update secret store", "index", secretIndex)
	return r.RequeueAfter(rotationInterval), nil
}

// updateResourceManagementStatus records a resource-level (non data-sync)
// error as a placeholder entry. Placeholders replace previous placeholders
// but never mask real data-key failures; identical consecutive reports are
// debounced.
func (r *ExternalSecretReconciler) updateResourceManagementStatus(externalSec *api.ExternalSecret, errorType string, err error) {
	placeholder := api.DataSyncResult{
		ExternalSecretKey:   errorType,
		Status:              "Failed",
		Reason:              err.Error(),
		SynchronizationTime: metav1.Time{Time: time.Now()},
	}
	merged := mergeResourceManagementResults(externalSec.Status.DataSyncResults, placeholder)
	// Skip the API write (and keep the existing timestamps) when the merged
	// results carry no new information.
	if !dataSyncResultsChanged(externalSec.Status.DataSyncResults, merged) {
		return
	}
	externalSec.Status.DataSyncResults = merged
	if updateErr := r.Status().Update(r.Ctx, externalSec); updateErr != nil {
		klog.Errorf("update external secret status error %v", updateErr)
	}
}

// handleDeletion handle resource deletion logic
func (r *ExternalSecretReconciler) handleDeletion(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret, rotationInterval time.Duration, secretName string) (ctrl.Result, error) {
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		return r.RequeueAfter(rotationInterval), nil
	}

	// exec the clean work in secretFinalizer
	// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
	if err := r.finalizeExternalSecret(ctx, log, externalSec, secretName); err != nil {
		log.Error(err, "failed to clean secret")
		return reconcile.Result{RequeueAfter: r.ReconciliationPeriod}, err
	}

	// remove secretFinalizer
	log.Info("removing finalizer", "currentFinalizers", externalSec.GetFinalizers())
	externalSec.SetFinalizers(utils.Remove(externalSec.GetFinalizers(), secretFinalizer))
	err := r.Update(ctx, externalSec)
	if err != nil {
		log.Error(err, "failed to update externalSec when clean finalizers")
		return r.RequeueAfter(r.ReconciliationPeriod), err
	}

	return r.RequeueAfter(rotationInterval), nil
}

func (r *ExternalSecretReconciler) finalizeExternalSecret(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret, secretName string) error {
	log.Info("Cleaning up secret for ExternalSecret", "externalSecret", externalSec.Name, "secret", secretName)

	// Get the secret
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: externalSec.Namespace,
		Name:      secretName,
	}, secret)

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	if err == nil {
		// Delete the secret directly (no owner reference is set)
		if delErr := r.Delete(ctx, secret); delErr != nil && !errors.IsNotFound(delErr) {
			return fmt.Errorf("failed to delete secret: %w", delErr)
		}
		log.Info("Deleted secret (original orphan mode behavior)",
			"namespace", externalSec.Namespace, "name", secretName)
	} else {
		log.Info("Secret already deleted", "namespace", externalSec.Namespace, "name", secretName)
	}

	// Deregister the endpoint-specific composite-key clients
	// ("clientName#endpoint") this ExternalSecret used, once no other active
	// ExternalSecret references the same storeRef+kmsEndpoint combination.
	// Idempotent: provider.Delete tolerates repeated calls, and the reference
	// check is re-evaluated on every finalizer retry.
	return r.cleanupEndpointClients(ctx, log, externalSec)
}

// resolveClientName derives the plain client cache key for a data-source
// entry, mirroring getExternalSecret / getExternalSecretWithExtract exactly:
// ENV client when no secretStoreRef is configured, "namespace/<ns>/<name>"
// for SecretStore references and "cluster/<name>" for ClusterSecretStore
// references.
func resolveClientName(secretStoreRef *api.SecretStoreRef, externalSecretNamespace string) string {
	if secretStoreRef == nil {
		return backend.EnvClient
	}
	kind := secretStoreRef.Kind
	if kind == "" {
		kind = "SecretStore"
	}
	switch kind {
	case "ClusterSecretStore":
		return fmt.Sprintf("cluster/%s", secretStoreRef.Name)
	default:
		namespace := externalSecretNamespace
		if secretStoreRef.Namespace != "" {
			namespace = secretStoreRef.Namespace
		}
		return fmt.Sprintf("namespace/%s/%s", namespace, secretStoreRef.Name)
	}
}

// normalizeEndpoint trims whitespace so the composite cache key
// ("clientName#endpoint") aligns with the provider-side registration key; a
// blank endpoint normalizes to "" (default endpoint, no composite key).
func normalizeEndpoint(endpoint string) string {
	return strings.TrimSpace(endpoint)
}

// compositeClientKeysFromSpec derives the set of composite cache keys
// ("clientName#endpoint") the ExternalSecret spec uses. Only entries with a
// custom kmsEndpoint produce composite keys; default-endpoint entries use
// plain clientName keys whose lifecycle is owned by the SecretStore
// controller (or the startup ENV registration) and must NOT be deleted here.
func compositeClientKeysFromSpec(externalSec *api.ExternalSecret) map[string]struct{} {
	keys := make(map[string]struct{})
	for i := range externalSec.Spec.Data {
		data := &externalSec.Spec.Data[i]
		endpoint := normalizeEndpoint(data.KmsEndpoint)
		if endpoint == "" {
			continue
		}
		keys[fmt.Sprintf("%s#%s", resolveClientName(data.SecretStoreRef, externalSec.Namespace), endpoint)] = struct{}{}
	}
	for i := range externalSec.Spec.DataProcess {
		extract := externalSec.Spec.DataProcess[i].Extract
		if extract == nil {
			continue
		}
		endpoint := normalizeEndpoint(extract.KmsEndpoint)
		if endpoint == "" {
			continue
		}
		keys[fmt.Sprintf("%s#%s", resolveClientName(extract.SecretStoreRef, externalSec.Namespace), endpoint)] = struct{}{}
	}
	return keys
}

// cleanupEndpointClients deregisters composite-key ("clientName#endpoint")
// clients no longer referenced by any other active ExternalSecret (the last
// deleter performs the deletion; a List failure aborts fail-closed so the
// finalizer retries). The key set unions the current spec keys with the
// tracked keys from previous rounds, because event coalescing can deliver an
// endpoint change together with the deletion in a single round. Plain
// clientName clients are owned by the SecretStore controller and never touched.
func (r *ExternalSecretReconciler) cleanupEndpointClients(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret) error {
	myKeys := compositeClientKeysFromSpec(externalSec)
	if tracked, ok := r.snapshotCompositeKeys(externalSec.UID); ok {
		for k := range tracked {
			myKeys[k] = struct{}{}
		}
	}
	if len(myKeys) > 0 {
		if err := r.deleteUnreferencedCompositeClients(ctx, log, externalSec, myKeys, true); err != nil {
			return err
		}
	}
	// Drop the tracked key set: the deletion path is finished with it, and
	// any re-created ExternalSecret with the same name gets a new UID.
	r.forgetCompositeKeys(externalSec.UID)
	return nil
}

// deleteUnreferencedCompositeClients deregisters the given composite-key
// clients unless other active ExternalSecrets still reference them. List
// failures are fail-closed. Provider absence is path-dependent: on the
// deletion path it logs a warning and continues (deletion must stay the
// escape hatch for misconfigured resources); on the reconcile path it stays
// fail-closed so the next round retries. Plain clientName clients are owned
// by the SecretStore controller and never touched.
func (r *ExternalSecretReconciler) deleteUnreferencedCompositeClients(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret, keys map[string]struct{}, onDeletion bool) error {
	if len(keys) == 0 {
		return nil
	}

	providerName := externalSec.Spec.Provider
	if providerName == "" {
		providerName = backend.ProviderKMSName
	}
	provider := backend.GetProviderByName(providerName)
	if provider == nil {
		if onDeletion {
			// The resource is going away; blocking its removal forever on a
			// provider that may never exist (bad spec.provider) is worse than
			// the in-memory client leak. Log loudly and continue.
			log.Info("provider not found during endpoint client cleanup on deletion, skipping client deregistration",
				"provider", providerName, "externalSecret", externalSec.Name)
			return nil
		}
		// Reconcile path: providers are registered at startup and never
		// removed, so a miss is transient at worst; retrying is safer than
		// silently leaking clients (the old skip-and-succeed behavior).
		return fmt.Errorf("provider %s not found, cannot clean up endpoint clients", providerName)
	}

	// Collect keys referenced by other active ExternalSecrets; deleted/in-deletion ones are excluded.
	esList := &api.ExternalSecretList{}
	if err := r.List(ctx, esList); err != nil {
		return fmt.Errorf("failed to list ExternalSecrets for endpoint client cleanup: %w", err)
	}
	referenced := make(map[string]struct{})
	for i := range esList.Items {
		other := &esList.Items[i]
		if other.UID == externalSec.UID {
			continue
		}
		if other.DeletionTimestamp != nil {
			continue
		}
		for k := range compositeClientKeysFromSpec(other) {
			referenced[k] = struct{}{}
		}
	}

	for key := range keys {
		if _, inUse := referenced[key]; inUse {
			log.Info("composite client still referenced by active ExternalSecrets, retaining", "clientKey", key)
			continue
		}
		provider.Delete(key)
		log.Info("deleted endpoint-specific client", "clientKey", key, "provider", providerName)
	}
	return nil
}

// snapshotCompositeKeys returns a copy of the composite-key set recorded for
// the ExternalSecret during its previous reconcile round, and whether it was
// tracked at all.
func (r *ExternalSecretReconciler) snapshotCompositeKeys(uid types.UID) (map[string]struct{}, bool) {
	r.compositeKeysMu.Lock()
	defer r.compositeKeysMu.Unlock()
	prev, ok := r.compositeKeysByES[uid]
	if !ok {
		return nil, false
	}
	cp := make(map[string]struct{}, len(prev))
	for k := range prev {
		cp[k] = struct{}{}
	}
	return cp, true
}

// storeCompositeKeys records the composite-key set observed in the current
// spec. An empty set removes the entry so endpoint-free ExternalSecrets
// never accumulate tracker state.
func (r *ExternalSecretReconciler) storeCompositeKeys(uid types.UID, keys map[string]struct{}) {
	r.compositeKeysMu.Lock()
	defer r.compositeKeysMu.Unlock()
	if len(keys) == 0 {
		delete(r.compositeKeysByES, uid)
		return
	}
	if r.compositeKeysByES == nil {
		r.compositeKeysByES = make(map[types.UID]map[string]struct{})
	}
	cp := make(map[string]struct{}, len(keys))
	for k := range keys {
		cp[k] = struct{}{}
	}
	r.compositeKeysByES[uid] = cp
}

// forgetCompositeKeys drops the tracked key set of a deleted ExternalSecret.
func (r *ExternalSecretReconciler) forgetCompositeKeys(uid types.UID) {
	r.compositeKeysMu.Lock()
	defer r.compositeKeysMu.Unlock()
	delete(r.compositeKeysByES, uid)
}

// reconcileStaleCompositeClients reclaims composite ("clientName#endpoint")
// clients whose endpoint was modified or removed from the spec since the
// previous reconcile round. The diff against the tracked key set is the only
// per-round work; the List + reference check runs solely when the spec
// actually dropped at least one composite key, so steady-state rounds (and
// endpoint-free ExternalSecrets) pay no scan cost. On cleanup failure the
// tracker keeps the previous snapshot so the next round retries fail-closed.
func (r *ExternalSecretReconciler) reconcileStaleCompositeClients(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret) error {
	current := compositeClientKeysFromSpec(externalSec)
	prev, tracked := r.snapshotCompositeKeys(externalSec.UID)
	if !tracked {
		// First observation (e.g. controller start): nothing to reclaim;
		// just establish the baseline for future diffs.
		r.storeCompositeKeys(externalSec.UID, current)
		return nil
	}
	stale := make(map[string]struct{})
	for k := range prev {
		if _, stillUsed := current[k]; !stillUsed {
			stale[k] = struct{}{}
		}
	}
	if len(stale) > 0 {
		if err := r.deleteUnreferencedCompositeClients(ctx, log, externalSec, stale, false); err != nil {
			return err
		}
	}
	r.storeCompositeKeys(externalSec.UID, current)
	return nil
}

func (r *ExternalSecretReconciler) addFinalizer(ctx context.Context, logger logr.Logger, es *api.ExternalSecret) error {
	logger.Info("Adding finalizer for the externalsecret", "name", es.Name)
	es.SetFinalizers(append(es.GetFinalizers(), secretFinalizer))
	err := r.Update(ctx, es)
	if err != nil {
		logger.Error(err, "Failed to update externalsecret with finalizer", "name", es.Name)
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ExternalSecretReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	r.RestConfig = mgr.GetConfig()

	return ctrl.NewControllerManagedBy(mgr).
		For(&api.ExternalSecret{}, builder.WithPredicates(ExternalSecretsPredicate{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

// getExternalSecret fetches all spec.data entries. Besides the merged output
// and the error map (both keyed by data.Key), it returns the set of data.Keys
// that were successfully fetched this round, used by the duplicate-data.Key
// twin exemption in failure counting and status reporting. Only a nil-error
// fetch is recorded; every error path continues without recording.
func (r *ExternalSecretReconciler) getExternalSecret(ctx context.Context, provider backend.Provider, dataSources []api.DataSource, externalSecretNamespace string) (map[string][]byte, map[string]error, map[string]struct{}) {
	out := make(map[string][]byte)
	errorsMap := make(map[string]error)
	succeededKeys := make(map[string]struct{})
	for _, data := range dataSources {
		clientName := resolveClientName(data.SecretStoreRef, externalSecretNamespace)
		secretStoreRef := data.SecretStoreRef

		klog.V(2).Infof("client name %v,data key %v", clientName, data.Key)

		secretClient, clientKey, err := r.getOrCreateClient(ctx, provider, clientName, secretStoreRef, externalSecretNamespace, data.KmsEndpoint)
		if err != nil {
			errorsMap[data.Key] = err
			continue
		}

		// The request-scoped ctx is used so fetch retries are canceled together with this reconcile.
		singleMap, err := secretClient.GetExternalSecret(ctx, &data, r.Client)
		if err != nil {
			errorsMap[data.Key] = fmt.Errorf("client %s get data failed: %v", clientKey, err)
			continue
		}
		// Record this round's successful key for the duplicate-key twin exemption (see dataEntryTargetCovered).
		succeededKeys[data.Key] = struct{}{}

		// Process each result from backend according to original JMESPath configuration
		for secretKey, secretData := range singleMap {
			// For JMESPath results, the secretKey is already the ObjectAlias
			// So we can use it directly as the final key
			finalKey := secretKey

			// Only resolve the target key (name, falling back to key) if no
			// JMESPath processing occurred. This is the case for non-JMESPath
			// data sources.
			// When name is omitted, ResolveTargetKey falls back to data.Key,
			// matching collectFailedEntryTargetKeys and the documented contract.
			if len(data.JMESPath) == 0 {
				finalKey = common.ResolveTargetKey(&data)
			}

			out[finalKey] = secretData
		}
	}
	return out, errorsMap, succeededKeys
}

// getExternalSecretWithExtract fetches all spec.dataProcess entries. Besides
// the merged output and the error map (both keyed by extract.Key), it returns
// the set of extract.Keys that were successfully fetched and processed this
// round, used by the duplicate-extract.Key twin exemption in failure counting
// and status reporting.
func (r *ExternalSecretReconciler) getExternalSecretWithExtract(ctx context.Context, provider backend.Provider, dataSources []api.DataProcess, externalSecretNamespace string) (map[string][]byte, map[string]error, map[string]struct{}) {
	out := make(map[string][]byte)
	errorsMap := make(map[string]error)
	succeededKeys := make(map[string]struct{})
	for _, data := range dataSources {
		if data.Extract == nil {
			continue
		}

		clientName := resolveClientName(data.Extract.SecretStoreRef, externalSecretNamespace)
		secretStoreRef := data.Extract.SecretStoreRef

		klog.V(2).Infof("client name %v,data key %v", clientName, data.Extract.Key)

		secretClient, clientKey, err := r.getOrCreateClient(ctx, provider, clientName, secretStoreRef, externalSecretNamespace, data.Extract.KmsEndpoint)
		if err != nil {
			errorsMap[data.Extract.Key] = err
			continue
		}

		// The request-scoped ctx is used so fetch retries are canceled together with this reconcile.
		singleMap, err := secretClient.GetExternalSecretWithExtract(ctx, &data, r.Client)
		if err != nil {
			errorsMap[data.Extract.Key] = fmt.Errorf("client %s get data failed: %v", clientKey, err)
			continue
		}
		// Record this round's successful key for the duplicate-key twin exemption (see dataEntryTargetCovered).
		succeededKeys[data.Extract.Key] = struct{}{}

		// Process each result from backend according to original JMESPath configuration
		for secretKey, secretData := range singleMap {
			// Apply replace rules if any
			finalValue := secretData
			if len(data.ReplaceKey) > 0 {
				finalValueStr := string(secretData)
				for _, rule := range data.ReplaceKey {
					finalValueStr = strings.ReplaceAll(finalValueStr, rule.Source, rule.Target)
				}
				finalValue = []byte(finalValueStr)
			}

			// Determine the final key name based on the original JMESPath configuration
			finalKey := secretKey // default to backend key
			for _, jp := range data.Extract.JMESPath {
				// If the backend key matches the original JMESPath expression, use the alias
				if secretKey == jp.Path {
					if jp.ObjectAlias != "" {
						finalKey = jp.ObjectAlias
					}
					break
				}
			}

			out[finalKey] = finalValue
		}
	}
	return out, errorsMap, succeededKeys
}

// getOrCreateClient gets or creates a secret client.
//
// Caching: generic clients (no custom endpoint) are registered at startup (ENV)
// or by the SecretStore controller, keyed by clientName; endpoint-specific
// clients are created on-demand for a custom kmsEndpoint, keyed by
// "clientName#endpoint". Returns the resolved client, the cache key used (for
// error reporting), and any error.
func (r *ExternalSecretReconciler) getOrCreateClient(ctx context.Context, provider backend.Provider, clientName string, secretStoreRef *api.SecretStoreRef, externalSecretNamespace string, kmsEndpoint string) (backend.SecretClient, string, error) {
	// Normalize before keying and before handing the endpoint to the
	// provider, so the composite cache key matches the provider-side
	// registration key (see normalizeEndpoint).
	normalizedEndpoint := normalizeEndpoint(kmsEndpoint)
	if kmsEndpoint != "" && normalizedEndpoint == "" {
		// The user configured a whitespace-only endpoint; it silently maps
		// to the default endpoint, so surface it via a Warning.
		klog.Warningf("client %s: kmsEndpoint contains only whitespace and is normalized to empty, default endpoint will be used", clientName)
	}
	kmsEndpoint = normalizedEndpoint

	// For custom endpoint: use composite key to isolate from generic client
	// For default endpoint: use plain clientName to match pre-registered generic client
	clientKey := clientName
	if kmsEndpoint != "" {
		clientKey = fmt.Sprintf("%s#%s", clientName, kmsEndpoint)
	}

	secretClient, err := provider.GetClient(clientKey)
	if err == nil {
		// Client found in cache - validate namespace access for SecretStore-based clients
		if secretStoreRef != nil {
			if validateErr := r.validateSecretStoreAccess(ctx, secretStoreRef, externalSecretNamespace); validateErr != nil {
				return nil, clientKey, validateErr
			}
		}
		return secretClient, clientKey, nil
	}

	// Cache miss - need to create a new client
	klog.V(2).Infof("client %v get client error %v", clientKey, err)

	// === ENV authentication path (no SecretStoreRef) ===
	if secretStoreRef == nil {
		if kmsEndpoint == "" {
			// No custom endpoint: the generic ENV client should have been registered at startup.
			// If we reach here, it means startup registration failed - don't try to recreate.
			err := fmt.Errorf("generic ENV client not found (key=%s), startup registration may have failed", clientKey)
			klog.Errorf("client %s get or create client failed: %v", clientKey, err)
			return nil, clientKey, err
		}
		// Custom endpoint: create an endpoint-specific ENV client
		secretClient, err = provider.NewClientByENV(kmsEndpoint)
		if err != nil {
			err = fmt.Errorf("init ENV client %s with endpoint %s failed: %v", clientKey, kmsEndpoint, err)
			klog.Errorf("client %s get or create client failed: %v", clientKey, err)
			return nil, clientKey, err
		}
		provider.Register(clientKey, secretClient)
		return secretClient, clientKey, nil
	}

	// === SecretStore authentication path ===
	store, err := r.getSecretStore(ctx, secretStoreRef, externalSecretNamespace)
	if err != nil {
		err = fmt.Errorf("get client %s failed: %v", clientKey, err)
		klog.Errorf("client %s get or create client failed: %v", clientKey, err)
		return nil, clientKey, err
	}

	// Create kubernetes.Interface from rest.Config for dynamic token acquisition
	var kubeClient kubernetes.Interface
	if r.RestConfig != nil {
		kubeClient, err = kubernetes.NewForConfig(r.RestConfig)
		if err != nil {
			klog.Errorf("Failed to create kubernetes clientset from rest.Config: %v", err)
		}
	}

	wrapperClient := &WrappedClient{
		Client:     r.Client,
		KubeClient: kubeClient,
	}

	// Create client with endpoint (empty string means use default).
	// The request-scoped ctx bounds the client construction (including any
	// RAM authentication round-trips) to this reconcile.
	secretClient, err = provider.NewClient(ctx, store, wrapperClient, kmsEndpoint)
	if err != nil {
		err = fmt.Errorf("init client %s failed: %v", clientKey, err)
		klog.Errorf("client %s get or create client failed: %v", clientKey, err)
		return nil, clientKey, err
	}
	provider.Register(clientKey, secretClient)
	return secretClient, clientKey, nil
}

// validateSecretStoreAccess checks if the namespace is allowed to access the referenced SecretStore
func (r *ExternalSecretReconciler) validateSecretStoreAccess(ctx context.Context, secretStoreRef *api.SecretStoreRef, externalSecretNamespace string) error {
	kind := secretStoreRef.Kind
	if kind == "" {
		kind = "SecretStore"
	}

	switch kind {
	case "ClusterSecretStore":
		clusterSecretStore := &api.ClusterSecretStore{}
		err := r.Get(ctx, client.ObjectKey{
			Name: secretStoreRef.Name,
		}, clusterSecretStore)
		if err != nil {
			return fmt.Errorf("failed to get ClusterSecretStore %s: %v", secretStoreRef.Name, err)
		}

		if !utils.IsNamespaceAllowedForClusterSecretStore(clusterSecretStore, externalSecretNamespace, r.Get) {
			return fmt.Errorf("namespace %s is not allowed to access ClusterSecretStore %s", externalSecretNamespace, secretStoreRef.Name)
		}
	case "SecretStore":
		namespace := externalSecretNamespace
		if secretStoreRef.Namespace != "" {
			namespace = secretStoreRef.Namespace
		}

		if !r.EnableCrossNamespace && secretStoreRef.Namespace != "" && secretStoreRef.Namespace != externalSecretNamespace {
			return fmt.Errorf("cross namespace SecretStore reference is disabled, cannot reference SecretStore in namespace %s from namespace %s", secretStoreRef.Namespace, externalSecretNamespace)
		}

		secretStore := &api.SecretStore{}
		err := r.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      secretStoreRef.Name,
		}, secretStore)
		if err != nil {
			return fmt.Errorf("failed to get SecretStore %s in namespace %s: %v", secretStoreRef.Name, namespace, err)
		}
	default:
		return fmt.Errorf("unsupported SecretStoreRef Kind: %s, must be SecretStore or ClusterSecretStore", kind)
	}
	return nil
}

// isWaitErrFromCancellation reports whether the error stems from
// request-context cancellation rather than a genuine condition (e.g.
// rate-limit timeout). Only a canceled request context qualifies.
func isWaitErrFromCancellation(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() != nil
}

// syncIfNeedUpdate processes the external secret and determines if an update is needed
func (r *ExternalSecretReconciler) syncIfNeedUpdate(ctx context.Context, externalSec *api.ExternalSecret) (bool, error) {
	// Graceful shutdown guard: a canceled request context must not be
	// misreported as rate_limit/state_retrieval/data failure; see isWaitErrFromCancellation.
	if ctxErr := ctx.Err(); isWaitErrFromCancellation(ctx, ctxErr) {
		return false, ctx.Err()
	}

	var templateResult *UnifiedTemplateResult
	providerName := externalSec.Spec.Provider
	if providerName == "" {
		providerName = backend.ProviderKMSName
	}
	waitTimeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var err error
	switch providerName {
	case backend.ProviderKMSName:
		err = r.KmsLimiter.Wait(waitTimeoutCtx)
	case backend.ProviderOOSName:
		err = r.OosLimiter.Wait(waitTimeoutCtx)
	default:
		// Provider configuration is a SYSTEM-LEVEL error, not data-source specific
		// Use resource management status update instead of polluting data error maps
		providerErr := fmt.Errorf("unsupported provider: %v, only support kms or oos", providerName)
		r.updateResourceManagementStatus(externalSec, "provider_configuration", providerErr)
		return false, providerErr
	}

	if err != nil {
		// Graceful shutdown guard: a canceled request context must not be
		// misreported as rate_limit/state_retrieval/data failure; see isWaitErrFromCancellation.
		if isWaitErrFromCancellation(ctx, err) {
			return false, ctx.Err()
		}
		// Rate limiting is a SYSTEM-LEVEL error, not data-source specific
		r.Log.Error(err, "secret pull rate limit exceeded, consider increasing --max-concurrent-secret-pulls or the provider-specific flag",
			"provider", providerName,
			"externalSecret", fmt.Sprintf("%s/%s", externalSec.Namespace, externalSec.Name))
		r.updateResourceManagementStatus(externalSec, "rate_limit", err)
		return false, err
	}

	// Determine the actual secret name to use
	secretName := externalSec.Name
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Name != "" {
		secretName = externalSec.Spec.Target.Name
	}

	esIndex := fmt.Sprintf("%s/%s", externalSec.Namespace, secretName)
	log := r.Log.WithValues("secret", esIndex)
	provider := backend.GetProviderByName(providerName)
	if provider == nil {
		// Provider lookup failure is a SYSTEM-LEVEL error
		lookupErr := fmt.Errorf("provider %v not found", providerName)
		r.updateResourceManagementStatus(externalSec, "provider_lookup", lookupErr)
		return false, lookupErr
	}

	secretMap := make(map[string][]byte)
	dataErrorsMap := make(map[string]error)
	extractDataErrorsMap := make(map[string]error)
	var dataSucceededKeys map[string]struct{}
	var extractSucceededKeys map[string]struct{}
	var currentData map[string][]byte

	// Process ExternalSecret.Spec.Data EXCLUSIVELY - ONLY populate dataErrorsMap
	if len(externalSec.Spec.Data) != 0 {
		out, errorsMap, succeededKeys := r.getExternalSecret(ctx, provider, externalSec.Spec.Data, externalSec.Namespace)
		dataSucceededKeys = succeededKeys
		// EXCLUSIVELY populate dataErrorsMap with Data-specific errors
		for k, v := range errorsMap {
			dataErrorsMap[k] = v
		}
		for k, v := range out {
			secretMap[k] = v
		}
	}

	// Process ExternalSecret.Spec.DataProcess EXCLUSIVELY - ONLY populate extractDataErrorsMap
	if len(externalSec.Spec.DataProcess) != 0 {
		out, errorsMap, succeededKeys := r.getExternalSecretWithExtract(ctx, provider, externalSec.Spec.DataProcess, externalSec.Namespace)
		extractSucceededKeys = succeededKeys
		// EXCLUSIVELY populate extractDataErrorsMap with DataProcess-specific errors
		for k, v := range errorsMap {
			extractDataErrorsMap[k] = v
		}
		for k, v := range out {
			secretMap[k] = v
		}
	}

	// Graceful shutdown guard: a canceled request context must not be
	// misreported as rate_limit/state_retrieval/data failure; see isWaitErrFromCancellation.
	if ctxErr := ctx.Err(); isWaitErrFromCancellation(ctx, ctxErr) {
		return false, ctx.Err()
	}

	// Duplicate-key protection: per-entry decisions use failedUncoveredEntries,
	// not the collapsed error map alone.
	failedUncovered := failedUncoveredEntries(externalSec, dataErrorsMap, secretMap, dataSucceededKeys)

	// Get current secret state (data plus labels/annotations: the latter
	// drive the template-metadata debounce below).
	var getCurrentDataErr error
	var currentLabels, currentAnnotations map[string]string
	currentData, currentLabels, currentAnnotations, getCurrentDataErr = r.getCurrentData(ctx, externalSec.Namespace, secretName)
	if getCurrentDataErr != nil && !errors.IsNotFound(getCurrentDataErr) {
		// Graceful shutdown guard: a canceled request context must not be
		// misreported as rate_limit/state_retrieval/data failure; see isWaitErrFromCancellation.
		if isWaitErrFromCancellation(ctx, getCurrentDataErr) {
			return false, ctx.Err()
		}
		// State retrieval is a SYSTEM-LEVEL error affecting comparison logic
		r.updateResourceManagementStatus(externalSec, "state_retrieval", getCurrentDataErr)
		return false, getCurrentDataErr
	}

	// Count failures before template processing; see countFailedSources for
	// duplicate-key exemption semantics.
	totalDataSources, failedSources, failedKeyCount := r.countFailedSources(externalSec, failedUncovered, dataErrorsMap, extractDataErrorsMap, extractSucceededKeys)
	// Check if every configured data source entry has failed
	allDataSourcesFailed := totalDataSources > 0 && failedSources == totalDataSources

	// Zero-output fail-closed guard (pre-template): an error-free round producing
	// zero keys must not write an empty dataset (which would delete/clear the
	// existing Secret while status reports Succeeded). Evaluated before template
	// processing on the raw secretMap; on a guard round only the zero_output_guard
	// entry is persisted. See hasDeclaredSourcesButZeroOutput /
	// hasNoDeclaredSourcesButExistingData.
	templateConfigured := externalSec.Spec.Target != nil && externalSec.Spec.Target.Template != nil
	if hasDeclaredSourcesButZeroOutput(totalDataSources, failedKeyCount, len(secretMap)) ||
		hasNoDeclaredSourcesButExistingData(totalDataSources, templateConfigured, len(currentData)) {
		declaredDataKeys := make([]string, 0, len(externalSec.Spec.Data))
		for i := range externalSec.Spec.Data {
			declaredDataKeys = append(declaredDataKeys, externalSec.Spec.Data[i].Key)
		}
		declaredExtractKeys := make([]string, 0, len(externalSec.Spec.DataProcess))
		for i := range externalSec.Spec.DataProcess {
			if extract := externalSec.Spec.DataProcess[i].Extract; extract != nil {
				declaredExtractKeys = append(declaredExtractKeys, extract.Key)
			}
		}
		log.Info("zero-output guard triggered: secret write skipped and deletion withheld",
			"totalDataSources", totalDataSources,
			"declaredDataKeys", declaredDataKeys,
			"declaredExtractKeys", declaredExtractKeys,
			"existingKeyCount", len(currentData))
		// Distinct messages per predicate so the status pinpoints the
		// scenario: O-2 (declared sources produced nothing) vs O-3 (no
		// source declared at all while a Secret with data exists). The two
		// predicates are mutually exclusive on totalDataSources.
		if hasDeclaredSourcesButZeroOutput(totalDataSources, failedKeyCount, len(secretMap)) {
			r.updateResourceManagementStatus(externalSec, "zero_output_guard",
				fmt.Errorf("declared %d data source(s) produced 0 keys with 0 errors; secret write skipped and deletion withheld", totalDataSources))
		} else {
			r.updateResourceManagementStatus(externalSec, "zero_output_guard",
				fmt.Errorf("no data source declared but existing Secret holds %d key(s); secret write skipped and deletion withheld", len(currentData)))
		}
		return false, nil
	}

	// The data-sync status write is deferred until the template conclusion is known.
	// Snapshot the PRE-template secretMap so the status-side coverage check shares
	// the same baseline as the counting side; the write decision keeps using the
	// post-template map.
	preTemplateSecretMap := make(map[string][]byte, len(secretMap))
	for k, v := range secretMap {
		preTemplateSecretMap[k] = v
	}

	// If there are template processing requirements, process them now
	// This must happen AFTER getting current data but BEFORE checking update conditions
	templateProcessed := false                       // Track whether template processing occurred
	var metadataTargets map[string]map[string]string // TemplateFrom metadata targets

	// placeholderWrittenThisRound is true when THIS round already wrote a
	// resource-management placeholder and continued; it keeps that placeholder
	// visible in the deferred status write while stale placeholders from previous
	// rounds are superseded.
	placeholderWrittenThisRound := false

	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Template != nil {
		// Capture the pre-template key count for the post-template zero-output
		// guard below: secretMap is replaced by the template result afterwards,
		// so the "source data was non-empty" condition must be snapshotted here.
		preTemplateDataKeys := len(secretMap)
		tp := NewSimpleTemplateProcessor(r.Client)
		var err error
		templateResult, err = tp.ProcessAllTemplates(r.Ctx, externalSec, secretMap)
		if err != nil {
			// Contract precedence: data-source failures outrank template fatal errors.
			// With data-source failures present, the template error is downgraded to a
			// warning status and the round follows the failure contracts; otherwise a
			// fatal template error fails the ExternalSecret with zero writes.
			if failedKeyCount > 0 || allDataSourcesFailed {
				log.Info("template processing failed while data sources also failed; deferring to the data-source failure contract",
					"templateError", err.Error(),
					"failedKeyCount", failedKeyCount,
					"allDataSourcesFailed", allDataSourcesFailed)
				r.updateResourceManagementStatus(externalSec, "template_processing_errors", err)
				placeholderWrittenThisRound = true
				// Keep fail-closed semantics for the rest of the round: the
				// write decision treats this round as template-processed
				// (templates may reference failed keys), but no template
				// output (data or metadata) is applied.
				templateProcessed = true
				templateResult = nil
			} else {
				// Fatal template processing error - mark ExternalSecret as Failed
				r.updateResourceManagementStatus(externalSec, "template_processing_fatal", err)
				return false, err
			}
		} else {
			templateProcessed = true

			// Check if there were any non-fatal template processing errors and log them
			if len(templateResult.Stats.Errors) > 0 {
				// Log recoverable errors but don't fail the ExternalSecret
				for _, errMsg := range templateResult.Stats.Errors {
					r.Log.Info("template processing warning", "error", errMsg)
				}

				// Optionally update status to indicate warnings (but not failure)
				var warningMsg strings.Builder
				warningMsg.WriteString("template processing completed with warnings: ")
				for i, errMsg := range templateResult.Stats.Errors {
					if i > 0 {
						warningMsg.WriteString("; ")
					}
					warningMsg.WriteString(errMsg)
				}
				// Use a different error type that indicates warnings rather than failures
				r.updateResourceManagementStatus(externalSec, "template_processing_warnings", fmt.Errorf("%s", warningMsg.String()))
				placeholderWrittenThisRound = true
			}

			// Collect metadata targets from template result
			if len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0 {
				metadataTargets = make(map[string]map[string]string)
				if len(templateResult.Metadata.Annotations) > 0 {
					metadataTargets["annotations"] = templateResult.Metadata.Annotations
				}
				if len(templateResult.Metadata.Labels) > 0 {
					metadataTargets["labels"] = templateResult.Metadata.Labels
				}
			}

			// Use Data target for secret creation
			secretMap = templateResult.Data

			// Post-template zero-output fail-closed guard: rendering can collapse a
			// non-empty dataset (or a source-less template-only spec) to zero keys;
			// writing that would delete/clear the existing Secret. Skip the write and
			// withhold deletion. See templateRenderedZeroOutput.
			if templateRenderedZeroOutput(failedKeyCount, len(secretMap), preTemplateDataKeys, len(currentData)) {
				log.Info("post-template zero-output guard triggered: secret write skipped and deletion withheld",
					"preTemplateDataKeys", preTemplateDataKeys,
					"existingKeyCount", len(currentData),
					"templateErrors", templateResult.Stats.Errors)
				// Distinct messages per trigger leg, mirroring the O-2/O-3
				// split of the pre-template guard: source data collapsed
				// during rendering vs a source-less template round rendering
				// nothing while a Secret with data exists.
				if preTemplateDataKeys > 0 {
					r.updateResourceManagementStatus(externalSec, "template_zero_output_guard",
						fmt.Errorf("template rendering produced 0 data keys while source data was non-empty; secret write skipped and deletion withheld"))
				} else {
					r.updateResourceManagementStatus(externalSec, "template_zero_output_guard",
						fmt.Errorf("no data source declared but existing Secret holds %d key(s) and template rendering produced 0 data keys; secret write skipped and deletion withheld", len(currentData)))
				}
				return false, nil
			}

			// Known limitation (non-fatal semantics preserved): in Replace mode
			// a partially failed rendering writes the REDUCED dataset -- the
			// failed templates' keys are dropped while the successful ones are
			// written. Only a warning is emitted; the round is not treated as
			// failed (documented in docs/advanced_usage.md).
			if len(templateResult.Stats.Errors) > 0 &&
				(externalSec.Spec.Target.Template.MergePolicy == "" ||
					externalSec.Spec.Target.Template.MergePolicy == api.MergePolicyReplace) {
				log.Info("WARNING: template rendering partially failed in Replace mode; writing the reduced dataset (known limitation)",
					"templateErrors", templateResult.Stats.Errors,
					"writtenKeyCount", len(secretMap))
			}
		}
	}

	// Persist the pure data-source status now that the template conclusion is
	// known. dropPlaceholder keeps this round's own placeholder visible; secretMap
	// is the pre-template snapshot (see updateExternalSecretStatus).
	r.updateExternalSecretStatus(externalSec, dataErrorsMap, extractDataErrorsMap, preTemplateSecretMap, dataSucceededKeys, extractSucceededKeys, !placeholderWrittenThisRound, false)

	// The degradation branch keeps templateProcessed=true with a nil result, so
	// both checks are required.
	hasSuccessfulData := len(secretMap) > 0
	if templateProcessed && templateResult != nil {
		hasSuccessfulData = hasSuccessfulData || (len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0)
	}

	// Fail-closed skip guard with partial-failure merge; see shouldSkipSecretWrite
	// for the truth table. Skipped rounds report via status, so no error is returned.
	if shouldSkipSecretWrite(len(secretMap), failedKeyCount, allDataSourcesFailed, r.CleanUpSecretOnFailure, templateProcessed) {
		log.Info("skipping secret write due to data sync failures, existing secret retained",
			"failedKeys", collectFailedKeys(dataErrorsMap, extractDataErrorsMap),
			"secretMapLength", len(secretMap),
			"failedKeyCount", failedKeyCount,
			"allDataSourcesFailed", allDataSourcesFailed)
		return false, nil
	}

	// Resolve the dataset to write (see resolveWriteData for the total-failure
	// deletion contract and the partial-failure merge strategy).
	writeData := r.resolveWriteData(log, externalSec, dataErrorsMap, extractDataErrorsMap, secretMap, currentData, allDataSourcesFailed, failedKeyCount, templateProcessed)

	// Template metadata contributes to the update condition only when debounced:
	// rendered labels/annotations force a write only while absent or different from
	// the current Secret metadata (see templateMetadataTargetsApplied). Same flag+nil
	// double check as hasSuccessfulData.
	templateMetadataPresent := false
	if templateProcessed && templateResult != nil {
		templateMetadataPresent = len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0
	}
	metadataNeedsUpdate := templateMetadataPresent && !templateMetadataTargetsApplied(metadataTargets, currentLabels, currentAnnotations)
	eq := reflect.DeepEqual(writeData, currentData) && !metadataNeedsUpdate

	if !eq {
		log.Info("found secret need to update", "hasSuccessfulData", hasSuccessfulData, "secretMapLength", len(secretMap), "allDataSourcesFailed", allDataSourcesFailed, "totalDataSources", totalDataSources, "failedKeyCount", failedKeyCount)

		// If there is no successful data but we still need to update (e.g., to delete the secret), proceed
		if err := r.updateSecret(externalSec, writeData, currentData, metadataTargets); err != nil {
			log.Error(err, "failed to update secret", "hasSuccessfulData", hasSuccessfulData, "secretMapLength", len(secretMap), "allDataSourcesFailed", allDataSourcesFailed)

			// No empty-dataset special-casing: the deletion contract already ran inside
			// HandleSecretOperation; any error here is surfaced as update_operation.
			// Update operation failure is a SYSTEM-LEVEL error
			r.updateResourceManagementStatus(externalSec, "update_operation", err)
			return false, err
		}
		log.Info("secret has sync from external backend", "secretMapLength", len(secretMap), "hasSuccessfulData", hasSuccessfulData, "allDataSourcesFailed", allDataSourcesFailed)

		// Forced status refresh after an actual Secret write, bypassing the
		// fingerprint debounce so SynchronizationTime advances with the data.
		r.updateExternalSecretStatus(externalSec, dataErrorsMap, extractDataErrorsMap, preTemplateSecretMap, dataSucceededKeys, extractSucceededKeys, !placeholderWrittenThisRound, true)
		return true, nil
	}

	// No update needed
	return false, nil
}

// templateMetadataTargetsApplied reports whether every rendered metadata target
// is already present with an equal value in the current Secret; the debounce
// primitive for the update condition. Empty targets return false.
func templateMetadataTargetsApplied(metadataTargets map[string]map[string]string, currentLabels, currentAnnotations map[string]string) bool {
	if len(metadataTargets) == 0 {
		return false
	}
	for k, v := range metadataTargets["labels"] {
		if currentLabels[k] != v {
			return false
		}
	}
	for k, v := range metadataTargets["annotations"] {
		if currentAnnotations[k] != v {
			return false
		}
	}
	return true
}

// countFailedSources counts configured data source entries and how many of
// them genuinely failed this round, plus the number of distinct failed keys
// (error map length) used for skip/merge decisions and logging.
//
// The error maps are keyed by data.Key / extract.Key, so duplicate entries
// collapse into one map entry; counting map entries would under-report
// failures. dataProcess entries whose extract is nil are excluded from both
// totals.
//
// Twin exemptions: a failed spec.data entry is not counted when a successful
// twin covers its target output (failedUncovered[i] == false), and a failed
// extract entry is not counted when its extract.Key appears in
// extractSucceededKeys. See dataEntryTargetCovered for the canonical
// twin-exemption semantics; the merge layer stays conservative.
func (r *ExternalSecretReconciler) countFailedSources(externalSec *api.ExternalSecret, failedUncovered []bool, dataErrorsMap, extractDataErrorsMap map[string]error, extractSucceededKeys map[string]struct{}) (totalDataSources, failedSources, failedKeyCount int) {
	totalDataSources = len(externalSec.Spec.Data)
	for i := range externalSec.Spec.Data {
		if failedUncovered[i] {
			failedSources++
		}
	}
	for _, dp := range externalSec.Spec.DataProcess {
		if dp.Extract == nil {
			continue
		}
		totalDataSources++
		if _, failed := extractDataErrorsMap[dp.Extract.Key]; !failed {
			continue
		}
		if _, succeeded := extractSucceededKeys[dp.Extract.Key]; succeeded {
			// Twin exemption: a sibling dataProcess entry already fetched this
			// extract.Key successfully this round.
			continue
		}
		failedSources++
	}
	// Number of distinct failed keys (map length); deliberately computed
	// from the raw error maps so the twin-exempted extract error still
	// counts here (the merge layer stays conservative).
	failedKeyCount = len(dataErrorsMap) + len(extractDataErrorsMap)
	return totalDataSources, failedSources, failedKeyCount
}

// resolveWriteData resolves the dataset to write:
//  1. total failure + CleanUpSecretOnFailure -> nil (deletion contract takes
//     over, template output discarded);
//  2. partial failure without template -> merge fresh values with retained
//     previous values of failed keys (see collectFailedEntryTargetKeys /
//     mergeWithFailedKeys);
//  3. otherwise the fetched secretMap as-is.
func (r *ExternalSecretReconciler) resolveWriteData(log logr.Logger, externalSec *api.ExternalSecret, dataErrorsMap, extractDataErrorsMap map[string]error, secretMap, currentData map[string][]byte, allDataSourcesFailed bool, failedKeyCount int, templateProcessed bool) map[string][]byte {
	if allDataSourcesFailed && r.CleanUpSecretOnFailure {
		return nil
	}
	if failedKeyCount > 0 && len(secretMap) > 0 && !templateProcessed {
		failedTargetKeys, retainAllUnmapped := collectFailedEntryTargetKeys(externalSec, dataErrorsMap, extractDataErrorsMap)
		writeData := mergeWithFailedKeys(secretMap, currentData, failedTargetKeys, retainAllUnmapped)
		log.Info("partial failure detected, merging successful keys with retained values of failed keys",
			"failedKeys", collectFailedKeys(dataErrorsMap, extractDataErrorsMap),
			"failedTargetKeys", failedTargetKeys,
			"retainAllUnmapped", retainAllUnmapped,
			"mergedDataLength", len(writeData),
			"retainedKeyCount", len(writeData)-len(secretMap))
		return writeData
	}
	return secretMap
}

func (r *ExternalSecretReconciler) getSecretStore(ctx context.Context, secretStoreRef *api.SecretStoreRef, externalSecretNamespace string) (*api.SecretStore, error) {
	if secretStoreRef == nil {
		return nil, fmt.Errorf("secret store ref is nil")
	}

	// Check if cross namespace reference is enabled
	if !r.EnableCrossNamespace && secretStoreRef.Namespace != "" && secretStoreRef.Namespace != externalSecretNamespace && secretStoreRef.Kind != "ClusterSecretStore" {
		return nil, fmt.Errorf("cross namespace SecretStore reference is disabled, cannot reference SecretStore in namespace %s from namespace %s", secretStoreRef.Namespace, externalSecretNamespace)
	}

	// If namespace is specified, issue a warning as this field is deprecated
	if secretStoreRef.Namespace != "" && secretStoreRef.Kind != "ClusterSecretStore" {
		klog.Warningf("Namespace field in SecretStoreRef is deprecated. Use SecretStore in the same namespace as ExternalSecret, or use ClusterSecretStore.")
	}

	// If Kind is not specified, default to SecretStore
	kind := secretStoreRef.Kind
	if kind == "" {
		kind = "SecretStore"
	}

	switch kind {
	case "SecretStore":
		// Get SecretStore in the same namespace
		namespace := externalSecretNamespace
		// If namespace is explicitly specified (backward compatibility), use the specified namespace
		if secretStoreRef.Namespace != "" {
			namespace = secretStoreRef.Namespace
		}

		secretStore := &api.SecretStore{}
		err := r.Get(ctx, client.ObjectKey{
			Namespace: namespace,
			Name:      secretStoreRef.Name,
		}, secretStore)

		if err != nil {
			return nil, fmt.Errorf("failed to get SecretStore %s in namespace %s: %v", secretStoreRef.Name, namespace, err)
		}
		return secretStore, nil

	case "ClusterSecretStore":
		// Get ClusterSecretStore
		clusterSecretStore := &api.ClusterSecretStore{}
		err := r.Get(ctx, client.ObjectKey{
			Name: secretStoreRef.Name,
		}, clusterSecretStore)

		if err != nil {
			return nil, fmt.Errorf("failed to get ClusterSecretStore %s: %v", secretStoreRef.Name, err)
		}

		// Validate if namespace is allowed to access this ClusterSecretStore
		if !utils.IsNamespaceAllowedForClusterSecretStore(clusterSecretStore, externalSecretNamespace, r.Get) {
			return nil, fmt.Errorf("namespace %s is not allowed to access ClusterSecretStore %s", externalSecretNamespace, secretStoreRef.Name)
		}

		// Convert ClusterSecretStore to SecretStore and return
		converted := &api.SecretStore{
			TypeMeta:   clusterSecretStore.TypeMeta,
			ObjectMeta: clusterSecretStore.ObjectMeta,
			Spec: api.SecretStoreSpec{
				KMS: clusterSecretStore.Spec.KMS,
				OOS: clusterSecretStore.Spec.OOS,
			},
			Status: api.SecretStoreStatus{
				Conditions:   clusterSecretStore.Status.Conditions,
				Capabilities: clusterSecretStore.Status.Capabilities,
			},
		}
		// Clear namespace since ClusterSecretStore is cluster-scoped
		converted.Namespace = ""
		return converted, nil

	default:
		return nil, fmt.Errorf("unsupported SecretStoreRef Kind: %s, must be SecretStore or ClusterSecretStore", kind)
	}
}

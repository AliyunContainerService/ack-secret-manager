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
	"sort"
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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	secretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"

	// storeClientStaleRetryInterval is the short backoff applied when the
	// freshness guard rejects a cached client mid Store-client rebuild.
	storeClientStaleRetryInterval = 2 * time.Second

	// cssControllerDisabledStatusKey is the status key of the notice persisted
	// on ExternalSecrets referencing a ClusterSecretStore while its controller
	// is disabled: store updates take effect only after an operator restart.
	cssControllerDisabledStatusKey = "cluster_secret_store_controller_disabled"
)

// errStoreClientStale marks errors from the store-client freshness guard:
// the store is mid rebuild, so the cached client must not be used. Reconcile
// detects it via errors.Is and returns ctrl.Result{RequeueAfter} directly
// (NOT r.RequeueAfter, which disables requeuing under --disable-polling).
var errStoreClientStale = stderrors.New("store client is stale, Store controller is rebuilding it")

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
	KmsLimiter             ProviderLimiter
	OosLimiter             ProviderLimiter
	EnableCrossNamespace   bool
	RestConfig             *rest.Config
	// ProcessClusterSecretStore mirrors --process-cluster-secret-store: when
	// false the CSS controller is unregistered, so the freshness guard and
	// status reporting degrade instead of waiting for a rebuild that never
	// happens (see ensureStoreClientFresh).
	ProcessClusterSecretStore bool

	// Previous-round "clientName#endpoint" keys per ES UID: diff baseline for
	// reclaiming stale composite clients; in-memory only, re-baselined on the
	// first round after a restart.
	compositeKeysMu   sync.Mutex
	compositeKeysByES map[types.UID]map[string]struct{}

	// cssDisabledWarned deduplicates the CSS-controller-disabled warning to
	// once per clientKey per process.
	cssDisabledWarned sync.Map
}

// getCurrentData gets the current Secret data plus labels/annotations;
// the latter drive the template-metadata debounce.
func (r *ExternalSecretReconciler) getCurrentData(ctx context.Context, namespace string, name string) (map[string][]byte, map[string]string, map[string]string, error) {
	reader := r.APIReader
	data := make(map[string][]byte)
	labels := make(map[string]string)
	annotations := make(map[string]string)
	secret := &corev1.Secret{}
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

// updateSecret writes the resolved dataset (and template metadata targets)
// to the target Secret via the secret operation handler.
func (r *ExternalSecretReconciler) updateSecret(externalSec *api.ExternalSecret, secretMap map[string][]byte, currentData map[string][]byte, metadataTargets map[string]map[string]string) error {
	if externalSec == nil {
		return fmt.Errorf("externalSec cannot be nil")
	}
	if secretMap == nil {
		secretMap = make(map[string][]byte)
	}
	if currentData == nil {
		currentData = make(map[string][]byte)
	}

	handler := NewSimpleSecretOperationHandler(r.Client, r.CleanUpSecretOnFailure, r.Log)
	return handler.HandleSecretOperation(r.Ctx, externalSec, secretMap, currentData, metadataTargets)
}

// shouldWatch reports whether the namespace is watchable; the include/exclude
// mode decision is delegated to utils.IsNamespaceWatched.
func (r *ExternalSecretReconciler) shouldWatch(namespace string) bool {
	return utils.IsNamespaceWatched(r.WatchNamespaces, namespace)
}

// namespaceExcludePredicate derives the blacklist predicate from the false
// (exclude) entries of WatchNamespaces, enforcing the exclude leg at the
// watch level; the Reconcile entry guard (shouldWatch) stays the second
// defense-in-depth layer.
func (r *ExternalSecretReconciler) namespaceExcludePredicate() namespaceExcludePredicate {
	excluded := make(map[string]struct{})
	for ns, included := range r.WatchNamespaces {
		if !included {
			excluded[ns] = struct{}{}
		}
	}
	return namespaceExcludePredicate{excluded: excluded}
}

// isNamespaceTerminating checks if a namespace is in terminating state
func (r *ExternalSecretReconciler) isNamespaceTerminating(namespace string) (bool, error) {
	ns := &corev1.Namespace{}
	err := r.Get(r.Ctx, client.ObjectKey{Name: namespace}, ns)
	if err != nil {
		return false, err
	}

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

	// Only skip requeue when getting the CR fails.
	err := r.Get(r.Ctx, req.NamespacedName, externalSec)
	if err != nil {
		// NotFound is normal when the resource is deleted
		return ctrl.Result{}, utils.IgnoreNotFoundError(err)
	}

	secretName := externalSec.Name
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Name != "" {
		secretName = externalSec.Spec.Target.Name
	}

	secretNamespace := externalSec.Namespace
	secretIndex := fmt.Sprintf("namespace/%s/%s", secretNamespace, secretName)
	log = log.WithValues("secret", secretIndex)

	rotationInterval := r.RotationInterval
	if externalSec.Spec.RotationInterval != nil {
		rotationInterval = externalSec.Spec.RotationInterval.Duration
	}

	if externalSec.GetDeletionTimestamp() != nil {
		r.updateResourceManagementStatus(externalSec, "operation", fmt.Errorf("external secret is being deleted"))
		return r.handleDeletion(ctx, log, externalSec, rotationInterval, secretName)
	}

	klog.Infof("reconcile external secret %v", secretIndex)

	// Namespace scope guard BEFORE touching the finalizer: a bypassing
	// Create/Update must never attach a finalizer the controller refuses to
	// remove (would leave the object stuck in Terminating). Deletion is
	// unaffected: a non-nil DeletionTimestamp routes straight to handleDeletion.
	if !r.shouldWatch(secretNamespace) {
		watchErr := fmt.Errorf("namespace %s is not in watched namespaces", secretNamespace)
		log.Info("external secret rejected: namespace not in watch scope", "namespace", secretNamespace, "reason", watchErr.Error())
		r.updateResourceManagementStatus(externalSec, "namespace_access", watchErr)
		return ctrl.Result{}, nil
	}

	// add Finalizer to external secret instance
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		if err := r.addFinalizer(ctx, log, externalSec); err != nil {
			r.updateResourceManagementStatus(externalSec, "finalizer", err)
			return r.RequeueAfter(r.ReconciliationPeriod), err
		}
	}

	if isTerminating, err := r.isNamespaceTerminating(secretNamespace); err != nil {
		r.updateResourceManagementStatus(externalSec, "namespace_check", err)
		return ctrl.Result{}, err
	} else if isTerminating {
		r.Log.Info("Skipping secret creation as namespace is terminating", "namespace", secretNamespace)
		r.updateResourceManagementStatus(externalSec, "namespace_terminating", fmt.Errorf("namespace %s is terminating", secretNamespace))
		return ctrl.Result{}, nil
	}

	// Reclaim composite clients whose endpoint changed since the previous
	// round; runs before data sync so re-creation sees a consistent cache.
	// Fail-closed: cleanup failure is reported and retried.
	if err := r.reconcileStaleCompositeClients(ctx, log, externalSec); err != nil {
		if stderrors.Is(err, context.Canceled) || stderrors.Is(err, context.DeadlineExceeded) {
			log.Info("reconcile ended early due to context cancellation/deadline", "stage", "endpoint_client_cleanup")
			return ctrl.Result{}, nil
		}
		r.updateResourceManagementStatus(externalSec, "endpoint_client_cleanup", err)
		return ctrl.Result{}, err
	}

	// Request-scoped ctx covers fetch/pull-limiter/backoff waits; Secret writes
	// and status updates use r.Ctx.
	_, syncErr := r.syncIfNeedUpdate(ctx, externalSec)

	if syncErr != nil {
		if stderrors.Is(syncErr, context.Canceled) || stderrors.Is(syncErr, context.DeadlineExceeded) {
			log.Info("reconcile ended early due to context cancellation/deadline")
			return ctrl.Result{}, nil
		}
		if stderrors.Is(syncErr, errStoreClientStale) {
			// The referenced store is mid client rebuild; retry shortly.
			// RequeueAfter is returned directly (not via r.RequeueAfter) so
			// the backoff also applies under --disable-polling=true.
			log.Info("store client is being rebuilt by the Store controller, retrying shortly",
				"requeueAfter", storeClientStaleRetryInterval, "reason", syncErr.Error())
			return ctrl.Result{RequeueAfter: storeClientStaleRetryInterval}, nil
		}
		// Return error only: controller-runtime applies exponential backoff
		// (RequeueAfter is ignored when error is non-nil)
		return ctrl.Result{}, syncErr
	}

	log.Info("reconcile completed, scheduling next sync", "index", secretIndex)
	return r.RequeueAfter(rotationInterval), nil
}

// updateResourceManagementStatus records a resource-level (non data-sync)
// error as a placeholder entry: placeholders replace previous placeholders
// but never mask real data-key failures; identical reports are debounced.
func (r *ExternalSecretReconciler) updateResourceManagementStatus(externalSec *api.ExternalSecret, errorType string, err error) {
	placeholder := api.DataSyncResult{
		ExternalSecretKey:   errorType,
		Status:              "Failed",
		Reason:              err.Error(),
		SynchronizationTime: metav1.Time{Time: time.Now()},
	}
	merged := mergeResourceManagementResults(externalSec.Status.DataSyncResults, placeholder)
	// Skip the API write when the merged results carry no new information.
	if !dataSyncResultsChanged(externalSec.Status.DataSyncResults, merged) {
		return
	}
	externalSec.Status.DataSyncResults = merged
	if updateErr := r.Status().Update(r.Ctx, externalSec); updateErr != nil {
		klog.Errorf("update external secret %s/%s status error %v", externalSec.Namespace, externalSec.Name, updateErr)
	}
}

// handleDeletion handles resource deletion logic
func (r *ExternalSecretReconciler) handleDeletion(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret, rotationInterval time.Duration, secretName string) (ctrl.Result, error) {
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		return r.RequeueAfter(rotationInterval), nil
	}

	// Do not remove the Finalizer if cleanup failed; it retries next reconcile.
	if err := r.finalizeExternalSecret(ctx, log, externalSec, secretName); err != nil {
		log.Error(err, "failed to clean secret")
		return reconcile.Result{RequeueAfter: r.ReconciliationPeriod}, err
	}

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

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: externalSec.Namespace,
		Name:      secretName,
	}, secret)

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	if err == nil {
		// No owner reference is set, delete directly
		if delErr := r.Delete(ctx, secret); delErr != nil && !errors.IsNotFound(delErr) {
			return fmt.Errorf("failed to delete secret: %w", delErr)
		}
		log.Info("Deleted secret (original orphan mode behavior)",
			"namespace", externalSec.Namespace, "name", secretName)
	} else {
		log.Info("Secret already deleted", "namespace", externalSec.Namespace, "name", secretName)
	}

	// Deregister this ExternalSecret's composite-key clients once no other
	// active ExternalSecret references the same storeRef+kmsEndpoint;
	// idempotent across finalizer retries.
	return r.cleanupEndpointClients(ctx, log, externalSec)
}

// resolveClientName derives the plain client cache key for a data-source
// entry: ENV client without secretStoreRef, "namespace/<ns>/<name>" for
// SecretStore, "cluster/<name>" for ClusterSecretStore.
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
		return backend.ClusterStoreKey(secretStoreRef.Name)
	default:
		namespace := externalSecretNamespace
		if secretStoreRef.Namespace != "" {
			namespace = secretStoreRef.Namespace
		}
		return backend.SecretStoreKey(namespace, secretStoreRef.Name)
	}
}

// normalizeEndpoint trims whitespace so the composite cache key aligns with
// the provider-side registration key; blank normalizes to "" (default endpoint).
func normalizeEndpoint(endpoint string) string {
	return strings.TrimSpace(endpoint)
}

// compositeClientKeysFromSpec derives the composite cache keys used by the
// spec. Only entries with a custom kmsEndpoint produce composite keys;
// default-endpoint entries use plain clientName keys whose lifecycle is owned
// by the SecretStore controller (or the ENV client) and must NOT be deleted here.
func compositeClientKeysFromSpec(externalSec *api.ExternalSecret) map[string]struct{} {
	keys := make(map[string]struct{})
	for i := range externalSec.Spec.Data {
		data := &externalSec.Spec.Data[i]
		endpoint := normalizeEndpoint(data.KmsEndpoint)
		if endpoint == "" {
			continue
		}
		keys[backend.CompositeClientKey(resolveClientName(data.SecretStoreRef, externalSec.Namespace), endpoint)] = struct{}{}
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
		keys[backend.CompositeClientKey(resolveClientName(extract.SecretStoreRef, externalSec.Namespace), endpoint)] = struct{}{}
	}
	return keys
}

// cleanupEndpointClients deregisters composite-key clients no longer
// referenced by other active ExternalSecrets (last deleter wins; a List
// failure aborts fail-closed so the finalizer retries). The key set unions
// spec and tracked keys because event coalescing can deliver an endpoint
// change together with the deletion. Plain clientName clients are owned by
// the SecretStore controller.
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
	// Drop the tracked key set: a re-created ExternalSecret with the same
	// name gets a new UID.
	r.forgetCompositeKeys(externalSec.UID)
	return nil
}

// deleteUnreferencedCompositeClients deregisters the given composite-key
// clients unless other active ExternalSecrets still reference them. List
// failures are fail-closed. Provider absence is path-dependent: on deletion
// it logs and continues (deletion stays the escape hatch for misconfigured
// resources); on reconcile it fails so the next round retries.
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
			// Blocking removal forever on a provider that may never exist (bad
			// spec.provider) is worse than the in-memory client leak.
			log.Info("provider not found during endpoint client cleanup on deletion, skipping client deregistration",
				"provider", providerName, "externalSecret", externalSec.Name)
			return nil
		}
		// Reconcile path: providers are registered at startup and never removed,
		// so a miss is transient; retrying is safer than leaking clients.
		return fmt.Errorf("provider %s not found, cannot clean up endpoint clients", providerName)
	}

	// Collect keys referenced by other active (non-deleting) ExternalSecrets.
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
// the ExternalSecret in its previous reconcile round, and whether it was tracked.
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
// accumulate no tracker state.
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

// reconcileStaleCompositeClients reclaims composite clients whose endpoint
// was modified/removed since the previous round; the reference scan runs only
// when a composite key was actually dropped, so steady-state rounds pay no
// scan cost. On cleanup failure the tracker keeps the previous snapshot so
// the next round retries fail-closed.
func (r *ExternalSecretReconciler) reconcileStaleCompositeClients(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret) error {
	current := compositeClientKeysFromSpec(externalSec)
	prev, tracked := r.snapshotCompositeKeys(externalSec.UID)
	if !tracked {
		// First observation (e.g. controller start): establish the baseline.
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

// SetupWithManager sets up the controller with the Manager. Reverse watches
// on SecretStore/ClusterSecretStore cascade store changes (spec edits,
// deletions, status.clientGeneration bumps) to referencing ExternalSecrets,
// closing the gap where --disable-polling would never observe a store change;
// the consumer-side freshness guard keeps reconciles off stale cached clients
// until the Store controller finishes rebuilding.
func (r *ExternalSecretReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	r.RestConfig = mgr.GetConfig()

	// Field index on store references (see store_watch.go), used by the
	// reverse store watches; must be registered before the cache starts.
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &api.ExternalSecret{}, storeRefIndexField, storeRefIndexKeys); err != nil {
		return fmt.Errorf("register ExternalSecret store-ref field index failed: %w", err)
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&api.ExternalSecret{}, builder.WithPredicates(predicate.And(ExternalSecretsPredicate{}, r.namespaceExcludePredicate()))).
		Watches(&api.SecretStore{},
			handler.EnqueueRequestsFromMapFunc(r.mapStoreToExternalSecrets),
			builder.WithPredicates(SecretStoreWatchPredicate{})).
		Watches(&api.ClusterSecretStore{},
			handler.EnqueueRequestsFromMapFunc(r.mapClusterStoreToExternalSecrets),
			builder.WithPredicates(ClusterSecretStoreWatchPredicate{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

// getExternalSecret fetches all spec.data entries. Returns the merged output
// and the error map (both keyed by data.Key) plus the set of data.Keys
// fetched successfully this round (duplicate-key twin exemption).
func (r *ExternalSecretReconciler) getExternalSecret(ctx context.Context, provider backend.Provider, dataSources []api.DataSource, externalSecretNamespace string) (map[string][]byte, map[string]error, map[string]struct{}) {
	out := make(map[string][]byte)
	errorsMap := make(map[string]error)
	succeededKeys := make(map[string]struct{})
	for _, data := range dataSources {
		clientName := resolveClientName(data.SecretStoreRef, externalSecretNamespace)
		secretStoreRef := data.SecretStoreRef

		klog.V(2).Infof("client name %v, data key %v", clientName, data.Key)

		secretClient, clientKey, err := r.getOrCreateClient(ctx, provider, clientName, secretStoreRef, externalSecretNamespace, data.KmsEndpoint)
		if err != nil {
			errorsMap[data.Key] = err
			continue
		}

		// Request-scoped ctx so fetch retries are canceled with this reconcile.
		singleMap, err := secretClient.GetExternalSecret(ctx, &data, r.Client)
		if err != nil {
			errorsMap[data.Key] = fmt.Errorf("client %s get data failed: %v", clientKey, err)
			continue
		}
		// Successful key of this round, for the duplicate-key twin exemption.
		succeededKeys[data.Key] = struct{}{}

		for secretKey, secretData := range singleMap {
			// For JMESPath results the secretKey is already the ObjectAlias.
			finalKey := secretKey

			// Non-JMESPath sources resolve the target key (name, falling back
			// to key), matching collectFailedEntryTargetKeys.
			if len(data.JMESPath) == 0 {
				finalKey = common.ResolveTargetKey(&data)
			}

			out[finalKey] = secretData
		}
	}
	return out, errorsMap, succeededKeys
}

// getExternalSecretWithExtract fetches all spec.dataProcess entries. Returns
// the merged output and the error map (both keyed by extract.Key) plus the
// set of extract.Keys successfully fetched this round (twin exemption).
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

		klog.V(2).Infof("client name %v, data key %v", clientName, data.Extract.Key)

		secretClient, clientKey, err := r.getOrCreateClient(ctx, provider, clientName, secretStoreRef, externalSecretNamespace, data.Extract.KmsEndpoint)
		if err != nil {
			errorsMap[data.Extract.Key] = err
			continue
		}

		// Request-scoped ctx so fetch retries are canceled with this reconcile.
		singleMap, err := secretClient.GetExternalSecretWithExtract(ctx, &data, r.Client)
		if err != nil {
			errorsMap[data.Extract.Key] = fmt.Errorf("client %s get data failed: %v", clientKey, err)
			continue
		}
		// Successful key of this round, for the duplicate-key twin exemption.
		succeededKeys[data.Extract.Key] = struct{}{}

		for secretKey, secretData := range singleMap {
			finalValue := secretData
			if len(data.ReplaceKey) > 0 {
				finalValueStr := string(secretData)
				for _, rule := range data.ReplaceKey {
					finalValueStr = strings.ReplaceAll(finalValueStr, rule.Source, rule.Target)
				}
				finalValue = []byte(finalValueStr)
			}

			// If the backend key matches the JMESPath expression, use the alias
			finalKey := secretKey
			for _, jp := range data.Extract.JMESPath {
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

// getOrCreateClient gets or creates a secret client and returns it with the
// cache key used (for error reporting). Generic clients (no custom endpoint)
// are lazily registered on first ENV consumption or by the SecretStore
// controller; endpoint-specific clients are created on demand, keyed by
// "clientName#endpoint".
func (r *ExternalSecretReconciler) getOrCreateClient(ctx context.Context, provider backend.Provider, clientName string, secretStoreRef *api.SecretStoreRef, externalSecretNamespace string, kmsEndpoint string) (backend.SecretClient, string, error) {
	// Normalize before keying so the composite cache key matches the
	// provider-side registration key.
	normalizedEndpoint := normalizeEndpoint(kmsEndpoint)
	if kmsEndpoint != "" && normalizedEndpoint == "" {
		// Whitespace-only endpoint silently maps to the default endpoint.
		klog.Warningf("client %s: kmsEndpoint contains only whitespace and is normalized to empty, default endpoint will be used", clientName)
	}
	kmsEndpoint = normalizedEndpoint

	// For custom endpoint: use composite key to isolate from generic client
	// For default endpoint: use plain clientName to match pre-registered generic client
	clientKey := backend.CompositeClientKey(clientName, kmsEndpoint)

	secretClient, err := provider.GetClient(clientKey)
	if err == nil {
		// Cached: still validate namespace access for SecretStore-based clients
		if secretStoreRef != nil {
			if validateErr := r.validateSecretStoreAccess(ctx, secretStoreRef, externalSecretNamespace); validateErr != nil {
				return nil, clientKey, validateErr
			}
			// Freshness guard: reject a stale cached client mid rebuild and
			// retry after a short backoff (see errStoreClientStale).
			if freshErr := r.ensureStoreClientFresh(ctx, secretStoreRef, externalSecretNamespace, clientKey); freshErr != nil {
				klog.Warningf("client %s freshness guard failed: %v", clientKey, freshErr)
				return nil, clientKey, freshErr
			}
		}
		return secretClient, clientKey, nil
	}

	// Cache miss - create a new client
	klog.V(2).Infof("client %v cache miss, creating new client", clientKey)

	// === ENV authentication path (no SecretStoreRef) ===
	if secretStoreRef == nil {
		if kmsEndpoint == "" {
			// Lazily register the ENV client on first use (see EnsureENVClient).
			// The error may come from another provider; probe GetClient first and
			// only report when this provider is affected.
			initErr := backend.EnsureENVClient()
			secretClient, getErr := provider.GetClient(clientKey)
			if getErr == nil {
				if initErr != nil {
					klog.Warningf("client %s: lazy ENV client initialization partially failed (other providers affected): %v; current provider client is ready", clientKey, initErr)
				}
				return secretClient, clientKey, nil
			}
			// Wrap initErr with %w so the error chain exposes the concrete failure.
			var err error
			if initErr != nil {
				err = fmt.Errorf("generic ENV client not available (key=%s), lazy ENV client initialization may have failed: %w", clientKey, initErr)
			} else {
				err = fmt.Errorf("generic ENV client not available (key=%s), lazy ENV client initialization may have failed", clientKey)
			}
			klog.Errorf("client %s get or create client failed (ENV auth, default endpoint): %v", clientKey, err)
			return nil, clientKey, err
		}
		// Custom endpoint: endpoint-specific ENV client
		secretClient, err = provider.NewClientByENV(kmsEndpoint)
		if err != nil {
			err = fmt.Errorf("init ENV client %s with endpoint %s failed: %v", clientKey, kmsEndpoint, err)
			klog.Errorf("client %s get or create client failed (ENV auth, custom endpoint): %v", clientKey, err)
			return nil, clientKey, err
		}
		provider.Register(clientKey, secretClient)
		return secretClient, clientKey, nil
	}

	// === SecretStore authentication path ===
	store, err := r.getSecretStore(ctx, secretStoreRef, externalSecretNamespace)
	if err != nil {
		err = fmt.Errorf("get client %s failed: %v", clientKey, err)
		klog.Errorf("client %s get or create client failed (SecretStore auth, store lookup): %v", clientKey, err)
		return nil, clientKey, err
	}

	// kubeClient from rest.Config enables dynamic token acquisition
	var kubeClient kubernetes.Interface
	if r.RestConfig != nil {
		kubeClient, err = kubernetes.NewForConfig(r.RestConfig)
		if err != nil {
			klog.Errorf("Failed to create kubernetes clientset from rest.Config: %v", err)
		}
	}

	wrapperClient := &backend.WrappedClient{
		Client:     r.Client,
		KubeClient: kubeClient,
	}

	// Empty endpoint means default; request-scoped ctx bounds client
	// construction (including RAM authentication round-trips).
	secretClient, err = provider.NewClient(ctx, store, wrapperClient, kmsEndpoint)
	if err != nil {
		err = fmt.Errorf("init client %s failed: %v", clientKey, err)
		klog.Errorf("client %s get or create client failed (SecretStore auth, client construction): %v", clientKey, err)
		return nil, clientKey, err
	}
	provider.Register(clientKey, secretClient)
	return secretClient, clientKey, nil
}

// ensureStoreClientFresh rejects a cached client while the referenced Store
// is mid rebuild (trigger annotation pending or ObservedGeneration behind),
// since it may carry stale credentials; callers retry after a short backoff
// (errStoreClientStale). Condition-less stores are treated as unmanaged
// (legacy behavior), and a missing store surfaces a regular error.
func (r *ExternalSecretReconciler) ensureStoreClientFresh(ctx context.Context, secretStoreRef *api.SecretStoreRef, externalSecretNamespace string, clientKey string) error {
	kind := secretStoreRef.Kind
	if kind == "" {
		kind = "SecretStore"
	}

	var storeDesc string
	var generation int64
	var annotations map[string]string
	var conditions []api.SecretStoreStatusCondition

	switch kind {
	case "ClusterSecretStore":
		css := &api.ClusterSecretStore{}
		if err := r.Get(ctx, client.ObjectKey{Name: secretStoreRef.Name}, css); err != nil {
			return fmt.Errorf("failed to get ClusterSecretStore %s for client freshness check: %v", secretStoreRef.Name, err)
		}
		storeDesc = fmt.Sprintf("ClusterSecretStore %s", secretStoreRef.Name)
		generation, annotations, conditions = css.Generation, css.Annotations, css.Status.Conditions
	case "SecretStore":
		namespace := externalSecretNamespace
		if secretStoreRef.Namespace != "" {
			namespace = secretStoreRef.Namespace
		}
		ss := &api.SecretStore{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretStoreRef.Name}, ss); err != nil {
			return fmt.Errorf("failed to get SecretStore %s in namespace %s for client freshness check: %v", secretStoreRef.Name, namespace, err)
		}
		storeDesc = fmt.Sprintf("SecretStore %s/%s", namespace, secretStoreRef.Name)
		generation, annotations, conditions = ss.Generation, ss.Annotations, ss.Status.Conditions
	default:
		return fmt.Errorf("unsupported SecretStoreRef Kind: %s, must be SecretStore or ClusterSecretStore", kind)
	}

	// Degraded-mode exemption (CSS controller disabled via flag or namespace
	// scoping): with the controller unregistered nothing will ever rebuild
	// this client, catch its ObservedGeneration up, or clear the trigger
	// annotation, so the stale state can never converge and refusing the
	// cached client would loop forever. Keep serving with the existing client;
	// store updates take effect only after an operator restart (surfaced via
	// warnCSSControllerDisabledOnce and the ES status notice). With the
	// controller enabled the original stale semantics below apply unchanged.
	if kind == "ClusterSecretStore" && !r.ProcessClusterSecretStore {
		r.warnCSSControllerDisabledOnce(clientKey, storeDesc)
		return nil
	}

	// Locate the Ready condition by Type (never assume list position). No
	// Ready condition means no Store controller has ever managed this object,
	// so nothing would clear a trigger annotation or catch the generation up:
	// keep the legacy behavior of using the cached client.
	observed, found := utils.ReadyConditionObservedGeneration(conditions)
	if !found {
		klog.Infof("client %s freshness guard: %s has no conditions (never managed by a Store controller), using cached client", clientKey, storeDesc)
		return nil
	}

	if observed != generation {
		return fmt.Errorf("client %s not used: %s status observedGeneration %d is behind generation %d; waiting for the Store controller to finish rebuilding: %w",
			clientKey, storeDesc, observed, generation, errStoreClientStale)
	}
	if annotations[utils.TriggerReconcileAnnotation] != "" {
		return fmt.Errorf("client %s not used: %s still carries the trigger-reconcile annotation; waiting for the Store controller to finish rebuilding: %w",
			clientKey, storeDesc, errStoreClientStale)
	}
	return nil
}

// warnCSSControllerDisabledOnce emits the degraded-mode warning at most once
// per clientKey per process (the condition is stable until restart, so
// logging every reconcile round would be pure noise).
func (r *ExternalSecretReconciler) warnCSSControllerDisabledOnce(clientKey, storeDesc string) {
	if _, alreadyWarned := r.cssDisabledWarned.LoadOrStore(clientKey, struct{}{}); alreadyWarned {
		return
	}
	klog.Warningf("client %s: %s is not processed because the ClusterSecretStore controller is disabled; credential/configuration changes to this store will not take effect until the operator restarts; continuing to use the existing cached client and credentials",
		clientKey, storeDesc)
}

// disabledCSSReferences returns the deduplicated, sorted names of the
// ClusterSecretStores referenced by the ExternalSecret's data sources; a nil
// result means no ClusterSecretStore is referenced.
func disabledCSSReferences(externalSec *api.ExternalSecret) []string {
	seen := make(map[string]struct{})
	for i := range externalSec.Spec.Data {
		if ref := externalSec.Spec.Data[i].SecretStoreRef; ref != nil && ref.Kind == "ClusterSecretStore" {
			seen[ref.Name] = struct{}{}
		}
	}
	for i := range externalSec.Spec.DataProcess {
		if extract := externalSec.Spec.DataProcess[i].Extract; extract != nil &&
			extract.SecretStoreRef != nil && extract.SecretStoreRef.Kind == "ClusterSecretStore" {
			seen[extract.SecretStoreRef.Name] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// recordCSSControllerDisabledStatus persists the user-visible notice that the
// ClusterSecretStore controller is disabled and store updates take effect only
// after an operator restart. The notice behaves like a data-key entry, not a
// round-level placeholder: exempt from placeholder-supersede semantics (see
// mergeResourceManagementResults) and retained while active. Identical notices
// are debounced by fingerprint comparison so repeated rounds do not churn the
// resourceVersion. The merge preserves every existing entry except an earlier
// copy of this notice: the round still SUCCEEDS, so dropping the round-level
// Succeeded verdict here would only have it re-added by the deferred status
// write and flip-flop the status forever. Returns whether an API write
// actually happened.
func (r *ExternalSecretReconciler) recordCSSControllerDisabledStatus(externalSec *api.ExternalSecret, cssNames []string) bool {
	placeholder := api.DataSyncResult{
		ExternalSecretKey: cssControllerDisabledStatusKey,
		Status:            "Warning",
		Reason: fmt.Sprintf("ClusterSecretStore controller disabled; credential/config updates for ClusterSecretStore(s) %s take effect only after the operator restarts",
			strings.Join(cssNames, ", ")),
		SynchronizationTime: metav1.Time{Time: time.Now()},
	}
	merged := make([]api.DataSyncResult, 0, len(externalSec.Status.DataSyncResults)+1)
	for _, res := range externalSec.Status.DataSyncResults {
		if res.ExternalSecretKey == cssControllerDisabledStatusKey {
			// Superseded by the fresh notice appended below.
			continue
		}
		merged = append(merged, res)
	}
	merged = append(merged, placeholder)
	// Skip the API write when the merged results carry no new information.
	if !dataSyncResultsChanged(externalSec.Status.DataSyncResults, merged) {
		return false
	}
	externalSec.Status.DataSyncResults = merged
	if updateErr := r.Status().Update(r.Ctx, externalSec); updateErr != nil {
		klog.Errorf("update external secret %s/%s status error %v", externalSec.Namespace, externalSec.Name, updateErr)
	}
	return true
}

// cssDisabledNoticeActive reports whether the degraded-mode notice applies
// this round: the CSS controller is disabled AND a ClusterSecretStore is
// still referenced. This exact predicate gates both the notice write and its
// retention, so the entry disappears once either condition stops holding.
func (r *ExternalSecretReconciler) cssDisabledNoticeActive(externalSec *api.ExternalSecret) bool {
	return !r.ProcessClusterSecretStore && len(disabledCSSReferences(externalSec)) > 0
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
// rate-limit timeout), so shutdown is not misreported as a sync failure.
func isWaitErrFromCancellation(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() != nil
}

// syncIfNeedUpdate processes the external secret and decides if an update is needed
func (r *ExternalSecretReconciler) syncIfNeedUpdate(ctx context.Context, externalSec *api.ExternalSecret) (bool, error) {
	// Graceful shutdown guard, see isWaitErrFromCancellation.
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
		providerErr := fmt.Errorf("unsupported provider: %v, only support kms or oos", providerName)
		r.updateResourceManagementStatus(externalSec, "provider_configuration", providerErr)
		return false, providerErr
	}

	if err != nil {
		if isWaitErrFromCancellation(ctx, err) {
			return false, ctx.Err()
		}
		// Rate limiting is a SYSTEM-LEVEL error, not data-source specific.
		// Guide operators to the provider-specific flag; the global
		// --max-concurrent-secret-pulls is deprecated (see cmd/manager/main.go).
		rateLimitFlag := "--max-concurrent-kms-secret-pulls"
		if providerName == backend.ProviderOOSName {
			rateLimitFlag = "--max-concurrent-oos-secret-pulls"
		}
		r.Log.Error(err, fmt.Sprintf("secret pull rate limit exceeded, consider increasing %s", rateLimitFlag),
			"provider", providerName,
			"externalSecret", fmt.Sprintf("%s/%s", externalSec.Namespace, externalSec.Name))
		r.updateResourceManagementStatus(externalSec, "rate_limit", err)
		return false, err
	}

	secretName := externalSec.Name
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Name != "" {
		secretName = externalSec.Spec.Target.Name
	}

	esIndex := fmt.Sprintf("%s/%s", externalSec.Namespace, secretName)
	log := r.Log.WithValues("secret", esIndex)
	provider := backend.GetProviderByName(providerName)
	if provider == nil {
		// SYSTEM-LEVEL error
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

	// Fetch spec.Data exclusively into dataErrorsMap
	if len(externalSec.Spec.Data) != 0 {
		out, errorsMap, succeededKeys := r.getExternalSecret(ctx, provider, externalSec.Spec.Data, externalSec.Namespace)
		dataSucceededKeys = succeededKeys
		for k, v := range errorsMap {
			dataErrorsMap[k] = v
		}
		for k, v := range out {
			secretMap[k] = v
		}
	}

	// Fetch spec.DataProcess exclusively into extractDataErrorsMap
	if len(externalSec.Spec.DataProcess) != 0 {
		out, errorsMap, succeededKeys := r.getExternalSecretWithExtract(ctx, provider, externalSec.Spec.DataProcess, externalSec.Namespace)
		extractSucceededKeys = succeededKeys
		for k, v := range errorsMap {
			extractDataErrorsMap[k] = v
		}
		for k, v := range out {
			secretMap[k] = v
		}
	}

	// Store-client freshness guard: when any data source hit a stale store
	// client, retry the whole round shortly instead of recording data-key
	// failures; the Store controller's clientGeneration write re-triggers this
	// ExternalSecret once the rebuild completes.
	for _, errMap := range []map[string]error{dataErrorsMap, extractDataErrorsMap} {
		for _, dataErr := range errMap {
			if stderrors.Is(dataErr, errStoreClientStale) {
				return false, dataErr
			}
		}
	}

	if ctxErr := ctx.Err(); isWaitErrFromCancellation(ctx, ctxErr) {
		return false, ctx.Err()
	}

	// Duplicate-key protection: per-entry decisions use failedUncoveredEntries,
	// not the collapsed error map alone.
	failedUncovered := failedUncoveredEntries(externalSec, dataErrorsMap, secretMap, dataSucceededKeys)

	// Current secret state; labels/annotations drive the template-metadata debounce.
	var getCurrentDataErr error
	var currentLabels, currentAnnotations map[string]string
	currentData, currentLabels, currentAnnotations, getCurrentDataErr = r.getCurrentData(ctx, externalSec.Namespace, secretName)
	if getCurrentDataErr != nil && !errors.IsNotFound(getCurrentDataErr) {
		if isWaitErrFromCancellation(ctx, getCurrentDataErr) {
			return false, ctx.Err()
		}
		// State retrieval is a SYSTEM-LEVEL error affecting comparison logic
		r.updateResourceManagementStatus(externalSec, "state_retrieval", getCurrentDataErr)
		return false, getCurrentDataErr
	}

	// Count failures before template processing (see countFailedSources).
	totalDataSources, failedSources, failedKeyCount := r.countFailedSources(externalSec, failedUncovered, dataErrorsMap, extractDataErrorsMap, extractSucceededKeys)
	allDataSourcesFailed := totalDataSources > 0 && failedSources == totalDataSources

	// Zero-output fail-closed guard (pre-template): an error-free round
	// producing zero keys must not write an empty dataset (which would clear
	// the existing Secret while status reports Succeeded). Evaluated on the
	// raw secretMap; a guard round persists only the zero_output_guard entry.
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
		// Distinct messages per predicate: O-2 (sources produced nothing) vs
		// O-3 (no source declared while a Secret with data exists); mutually
		// exclusive on totalDataSources.
		if hasDeclaredSourcesButZeroOutput(totalDataSources, failedKeyCount, len(secretMap)) {
			r.updateResourceManagementStatus(externalSec, "zero_output_guard",
				fmt.Errorf("declared %d data source(s) produced 0 keys with 0 errors; secret write skipped and deletion withheld", totalDataSources))
		} else {
			r.updateResourceManagementStatus(externalSec, "zero_output_guard",
				fmt.Errorf("no data source declared but existing Secret holds %d key(s); secret write skipped and deletion withheld", len(currentData)))
		}
		return false, nil
	}

	// The data-sync status write is deferred until the template conclusion is
	// known. Snapshot the PRE-template secretMap so the status-side coverage
	// check shares the counting-side baseline; write decisions keep using the
	// post-template map.
	preTemplateSecretMap := make(map[string][]byte, len(secretMap))
	for k, v := range secretMap {
		preTemplateSecretMap[k] = v
	}

	// Template processing happens AFTER getting current data but BEFORE checking update conditions.
	templateProcessed := false                       // whether template processing occurred
	var metadataTargets map[string]map[string]string // TemplateFrom metadata targets

	// True when THIS round already wrote a resource-management placeholder;
	// keeps it visible in the deferred status write while stale placeholders
	// from previous rounds are superseded.
	placeholderWrittenThisRound := false

	// Observability fallback for the disabled CSS controller: trigger
	// controllers skip CSS patches and no client-rebuild chain exists, so
	// store changes would otherwise stall silently. Persist a debounced status
	// notice (the fetch continues with the cached client via the
	// ensureStoreClientFresh exemption). The notice is NOT a round-level
	// placeholder: exempt from placeholder-supersede semantics and retained
	// while active (see retainCSSDisabledNotice), so placeholderWrittenThisRound
	// only flips when a write actually happened; once degraded mode no longer
	// applies, retention stops and the entry converges away.
	if cssNames := disabledCSSReferences(externalSec); !r.ProcessClusterSecretStore && len(cssNames) > 0 {
		if r.recordCSSControllerDisabledStatus(externalSec, cssNames) {
			placeholderWrittenThisRound = true
		}
	}

	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Template != nil {
		// Snapshot for the post-template zero-output guard: secretMap is
		// replaced by the template result afterwards.
		preTemplateDataKeys := len(secretMap)
		tp := NewSimpleTemplateProcessor(r.Client)
		var err error
		templateResult, err = tp.ProcessAllTemplates(r.Ctx, externalSec, secretMap)
		if err != nil {
			// Contract precedence: data-source failures outrank template fatal
			// errors. With data-source failures the template error is downgraded
			// to a warning status; otherwise a fatal template error fails the
			// ExternalSecret with zero writes.
			if failedKeyCount > 0 || allDataSourcesFailed {
				log.Info("template processing failed while data sources also failed; deferring to the data-source failure contract",
					"templateError", err.Error(),
					"failedKeyCount", failedKeyCount,
					"allDataSourcesFailed", allDataSourcesFailed)
				r.updateResourceManagementStatus(externalSec, "template_processing_errors", err)
				placeholderWrittenThisRound = true
				// Fail-closed: the write decision treats this round as
				// template-processed (templates may reference failed keys) but
				// applies no template output.
				templateProcessed = true
				templateResult = nil
			} else {
				// Fatal template error: mark ExternalSecret as Failed
				r.updateResourceManagementStatus(externalSec, "template_processing_fatal", err)
				return false, err
			}
		} else {
			templateProcessed = true

			// Non-fatal template errors: log and record as warnings, don't fail
			if len(templateResult.Stats.Errors) > 0 {
				for _, errMsg := range templateResult.Stats.Errors {
					r.Log.Info("template processing warning", "error", errMsg)
				}

				var warningMsg strings.Builder
				warningMsg.WriteString("template processing completed with warnings: ")
				for i, errMsg := range templateResult.Stats.Errors {
					if i > 0 {
						warningMsg.WriteString("; ")
					}
					warningMsg.WriteString(errMsg)
				}
				// Warning-type status entry, not a failure
				r.updateResourceManagementStatus(externalSec, "template_processing_warnings", fmt.Errorf("%s", warningMsg.String()))
				placeholderWrittenThisRound = true
			}

			if len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0 {
				metadataTargets = make(map[string]map[string]string)
				if len(templateResult.Metadata.Annotations) > 0 {
					metadataTargets["annotations"] = templateResult.Metadata.Annotations
				}
				if len(templateResult.Metadata.Labels) > 0 {
					metadataTargets["labels"] = templateResult.Metadata.Labels
				}
			}

			secretMap = templateResult.Data

			// Post-template zero-output fail-closed guard: rendering can
			// collapse a dataset to zero keys; writing that would clear the
			// existing Secret. Skip the write and withhold deletion.
			if templateRenderedZeroOutput(failedKeyCount, len(secretMap), preTemplateDataKeys, len(currentData)) {
				log.Info("post-template zero-output guard triggered: secret write skipped and deletion withheld",
					"preTemplateDataKeys", preTemplateDataKeys,
					"existingKeyCount", len(currentData),
					"templateErrors", templateResult.Stats.Errors)
				// Distinct messages per trigger leg, mirroring the O-2/O-3 split.
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
			// a partially failed rendering writes the REDUCED dataset - failed
			// templates' keys are dropped, successful ones written (documented
			// in docs/advanced_usage.md).
			if len(templateResult.Stats.Errors) > 0 &&
				(externalSec.Spec.Target.Template.MergePolicy == "" ||
					externalSec.Spec.Target.Template.MergePolicy == api.MergePolicyReplace) {
				log.Info("WARNING: template rendering partially failed in Replace mode; writing the reduced dataset (known limitation)",
					"templateErrors", templateResult.Stats.Errors,
					"writtenKeyCount", len(secretMap))
			}
		}
	}

	// Persist the data-source status now that the template conclusion is
	// known; dropPlaceholder keeps this round's own placeholder visible;
	// secretMap is the pre-template snapshot (see updateExternalSecretStatus).
	r.updateExternalSecretStatus(externalSec, dataErrorsMap, extractDataErrorsMap, preTemplateSecretMap, dataSucceededKeys, extractSucceededKeys, !placeholderWrittenThisRound, false)

	// The degradation branch keeps templateProcessed=true with a nil result,
	// so both checks are required.
	hasSuccessfulData := len(secretMap) > 0
	if templateProcessed && templateResult != nil {
		hasSuccessfulData = hasSuccessfulData || (len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0)
	}

	// Fail-closed skip guard with partial-failure merge; see shouldSkipSecretWrite.
	// Skipped rounds report via status, so no error is returned.
	if shouldSkipSecretWrite(len(secretMap), failedKeyCount, allDataSourcesFailed, r.CleanUpSecretOnFailure, templateProcessed) {
		log.Info("skipping secret write due to data sync failures, existing secret retained",
			"failedKeys", collectFailedKeys(dataErrorsMap, extractDataErrorsMap),
			"failedErrors", collectFailedErrors(dataErrorsMap, extractDataErrorsMap),
			"secretMapLength", len(secretMap),
			"failedKeyCount", failedKeyCount,
			"allDataSourcesFailed", allDataSourcesFailed)
		return false, nil
	}

	writeData := r.resolveWriteData(log, externalSec, dataErrorsMap, extractDataErrorsMap, secretMap, currentData, allDataSourcesFailed, failedKeyCount, templateProcessed)

	// Template metadata forces a write only while absent or different from the
	// current Secret metadata (debounce, see templateMetadataTargetsApplied).
	templateMetadataPresent := false
	if templateProcessed && templateResult != nil {
		templateMetadataPresent = len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0
	}
	metadataNeedsUpdate := templateMetadataPresent && !templateMetadataTargetsApplied(metadataTargets, currentLabels, currentAnnotations)
	eq := reflect.DeepEqual(writeData, currentData) && !metadataNeedsUpdate

	if !eq {
		log.Info("found secret need to update", "hasSuccessfulData", hasSuccessfulData, "secretMapLength", len(secretMap), "allDataSourcesFailed", allDataSourcesFailed, "totalDataSources", totalDataSources, "failedKeyCount", failedKeyCount)

		if err := r.updateSecret(externalSec, writeData, currentData, metadataTargets); err != nil {
			log.Error(err, "failed to update secret", "hasSuccessfulData", hasSuccessfulData, "secretMapLength", len(secretMap), "allDataSourcesFailed", allDataSourcesFailed)

			// The deletion contract already ran inside HandleSecretOperation;
			// any error here is a SYSTEM-LEVEL update_operation error.
			r.updateResourceManagementStatus(externalSec, "update_operation", err)
			return false, err
		}
		log.Info("secret has sync from external backend", "secretMapLength", len(secretMap), "hasSuccessfulData", hasSuccessfulData, "allDataSourcesFailed", allDataSourcesFailed)

		// Forced status refresh after an actual write, bypassing the
		// fingerprint debounce so SynchronizationTime advances with the data.
		r.updateExternalSecretStatus(externalSec, dataErrorsMap, extractDataErrorsMap, preTemplateSecretMap, dataSucceededKeys, extractSucceededKeys, !placeholderWrittenThisRound, true)
		return true, nil
	}

	return false, nil
}

// templateMetadataTargetsApplied reports whether every rendered metadata
// target already exists with an equal value (update-condition debounce).
// Empty targets return false.
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

// countFailedSources counts configured data source entries and how many
// genuinely failed this round, plus the distinct failed keys for skip/merge
// decisions. Error maps are keyed by data.Key / extract.Key, so duplicate
// entries collapse. Twin exemptions: a failed entry is skipped when a
// successful twin covers its output; the merge layer stays conservative.
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
			// Twin exemption: a sibling entry fetched this key successfully.
			continue
		}
		failedSources++
	}
	// Distinct failed keys: union of both error maps (a key failing on both
	// sides counts once; nil errors skipped, matching collectFailedKeys), so
	// twin-exempted extract errors still count here -- the merge layer stays
	// conservative.
	failedKeySet := make(map[string]struct{}, len(dataErrorsMap)+len(extractDataErrorsMap))
	for k, v := range dataErrorsMap {
		if v == nil {
			continue
		}
		failedKeySet[k] = struct{}{}
	}
	for k, v := range extractDataErrorsMap {
		if v == nil {
			continue
		}
		failedKeySet[k] = struct{}{}
	}
	failedKeyCount = len(failedKeySet)
	return totalDataSources, failedSources, failedKeyCount
}

// resolveWriteData resolves the dataset to write:
//  1. total failure + CleanUpSecretOnFailure -> nil (deletion contract);
//  2. partial failure without template -> merge fresh values with retained
//     previous values of failed keys;
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
			"failedErrors", collectFailedErrors(dataErrorsMap, extractDataErrorsMap),
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

	// Cross-namespace reference gate
	if !r.EnableCrossNamespace && secretStoreRef.Namespace != "" && secretStoreRef.Namespace != externalSecretNamespace && secretStoreRef.Kind != "ClusterSecretStore" {
		return nil, fmt.Errorf("cross namespace SecretStore reference is disabled, cannot reference SecretStore in namespace %s from namespace %s", secretStoreRef.Namespace, externalSecretNamespace)
	}

	// Deprecated field: warn when namespace is specified
	if secretStoreRef.Namespace != "" && secretStoreRef.Kind != "ClusterSecretStore" {
		klog.Warningf("Namespace field in SecretStoreRef is deprecated (SecretStore %s/%s). Use SecretStore in the same namespace as ExternalSecret, or use ClusterSecretStore.", secretStoreRef.Namespace, secretStoreRef.Name)
	}

	kind := secretStoreRef.Kind
	if kind == "" {
		kind = "SecretStore"
	}

	switch kind {
	case "SecretStore":
		namespace := externalSecretNamespace
		// Explicit namespace kept for backward compatibility
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
		clusterSecretStore := &api.ClusterSecretStore{}
		err := r.Get(ctx, client.ObjectKey{
			Name: secretStoreRef.Name,
		}, clusterSecretStore)

		if err != nil {
			return nil, fmt.Errorf("failed to get ClusterSecretStore %s: %v", secretStoreRef.Name, err)
		}

		if !utils.IsNamespaceAllowedForClusterSecretStore(clusterSecretStore, externalSecretNamespace, r.Get) {
			return nil, fmt.Errorf("namespace %s is not allowed to access ClusterSecretStore %s", externalSecretNamespace, secretStoreRef.Name)
		}

		// Convert to SecretStore
		converted := &api.SecretStore{
			TypeMeta:   clusterSecretStore.TypeMeta,
			ObjectMeta: clusterSecretStore.ObjectMeta,
			Spec: api.SecretStoreSpec{
				KMS: clusterSecretStore.Spec.KMS,
				OOS: clusterSecretStore.Spec.OOS,
			},
			Status: api.SecretStoreStatus{
				Conditions:       clusterSecretStore.Status.Conditions,
				Capabilities:     clusterSecretStore.Status.Capabilities,
				ClientGeneration: clusterSecretStore.Status.ClientGeneration,
			},
		}
		// Clear namespace since ClusterSecretStore is cluster-scoped
		converted.Namespace = ""
		return converted, nil

	default:
		return nil, fmt.Errorf("unsupported SecretStoreRef Kind: %s, must be SecretStore or ClusterSecretStore", kind)
	}
}

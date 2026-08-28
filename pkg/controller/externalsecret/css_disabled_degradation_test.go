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

// css_disabled_degradation_test.go covers the degraded mode applied when the
// ClusterSecretStore controller is not registered (--process-cluster-secret-
// store=false, explicit or forced by namespace scoping):
//
//   - Defect-B fix: the store-client freshness guard must NOT refuse a cached
//     ClusterSecretStore client for a lagging ObservedGeneration or a
//     residual trigger annotation — with the controller gone the stale state
//     can never converge, and refusing would loop forever (2s retry, no
//     recovery). The cached client is reused instead. With the controller
//     enabled the original stale semantics must stay untouched (regression).
//   - Defect-C observability: consuming a ClusterSecretStore-backed client in
//     this mode persists a debounced, user-visible status notice (credential/
//     config updates take effect only after an operator restart) and logs the
//     degraded mode once per clientKey. The notice behaves like a data-key
//     entry, not a round-level placeholder: resource-management placeholder
//     writes must keep it visible, and it converges away automatically once
//     the degraded mode no longer applies.

package externalsecret

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// managedCSS builds a ClusterSecretStore whose Ready condition reports the
// given observedGeneration (fresh when equal to generation).
func managedCSS(name string, generation, observed int64) *api.ClusterSecretStore {
	return &api.ClusterSecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Generation: generation},
		Status: api.ClusterSecretStoreStatus{Conditions: []api.SecretStoreStatusCondition{{
			Type:               api.SecretStoreReady,
			Status:             corev1.ConditionTrue,
			ObservedGeneration: observed,
		}}},
	}
}

// TestEnsureStoreClientFreshCSSControllerDisabled pins the degraded-mode
// exemption: with the ClusterSecretStore controller disabled BOTH stale
// rejection legs (lagging ObservedGeneration, residual trigger annotation)
// pass through with the cached client for ClusterSecretStore references,
// while SecretStore references keep the strict semantics (the SecretStore
// controller still runs and will converge).
func TestEnsureStoreClientFreshCSSControllerDisabled(t *testing.T) {
	annotatedCSS := managedCSS("css-annotated", 2, 2)
	annotatedCSS.Annotations = map[string]string{utils.TriggerReconcileAnnotation: "rotate-leftover"}

	r := newTestReconciler(t, false, nil,
		managedCSS("css-lagging", 3, 2), // generation advanced after the controller vanished
		annotatedCSS,                    // trigger annotation left behind at disable time
		storeWithObserved("default", "ss-lagging", 2, 1),
	)
	r.ProcessClusterSecretStore = false

	ctx := context.Background()
	tests := []struct {
		name      string
		ref       *api.SecretStoreRef
		wantStale bool
	}{
		{name: "disabled CSS with lagging ObservedGeneration passes",
			ref: &api.SecretStoreRef{Name: "css-lagging", Kind: "ClusterSecretStore"}},
		{name: "disabled CSS with residual trigger annotation passes",
			ref: &api.SecretStoreRef{Name: "css-annotated", Kind: "ClusterSecretStore"}},
		{name: "SecretStore with lagging ObservedGeneration stays stale",
			ref: &api.SecretStoreRef{Name: "ss-lagging"}, wantStale: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.ensureStoreClientFresh(ctx, tt.ref, "default", "cluster/"+tt.ref.Name)
			if tt.wantStale {
				if !stderrors.Is(err, errStoreClientStale) {
					t.Fatalf("err = %v, want errors.Is(errStoreClientStale)", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want nil (degraded mode must reuse the cached client)", err)
			}
		})
	}
}

// TestEnsureStoreClientFreshCSSControllerEnabledRegression guards the other
// side of the exemption boundary: with the ClusterSecretStore controller
// enabled the freshness guard must keep refusing stale ClusterSecretStore
// clients exactly as before.
func TestEnsureStoreClientFreshCSSControllerEnabledRegression(t *testing.T) {
	annotatedCSS := managedCSS("css-annotated", 2, 2)
	annotatedCSS.Annotations = map[string]string{utils.TriggerReconcileAnnotation: "rotate-1"}

	r := newTestReconciler(t, false, nil,
		managedCSS("css-lagging", 3, 2),
		annotatedCSS,
	)
	r.ProcessClusterSecretStore = true

	for name, ref := range map[string]*api.SecretStoreRef{
		"lagging ObservedGeneration is stale": {Name: "css-lagging", Kind: "ClusterSecretStore"},
		"pending trigger annotation is stale": {Name: "css-annotated", Kind: "ClusterSecretStore"},
	} {
		t.Run(name, func(t *testing.T) {
			err := r.ensureStoreClientFresh(context.Background(), ref, "default", "cluster/"+ref.Name)
			if !stderrors.Is(err, errStoreClientStale) {
				t.Fatalf("err = %v, want errors.Is(errStoreClientStale)", err)
			}
		})
	}
}

// TestWarnCSSControllerDisabledLoggedOnce verifies the debounce backing the
// once-per-client degraded-mode warning: repeated calls for the same
// clientKey record a single entry, distinct clientKeys are tracked
// independently.
func TestWarnCSSControllerDisabledLoggedOnce(t *testing.T) {
	r := &ExternalSecretReconciler{}
	r.warnCSSControllerDisabledOnce("cluster/css-a", "ClusterSecretStore css-a")
	r.warnCSSControllerDisabledOnce("cluster/css-a", "ClusterSecretStore css-a")
	r.warnCSSControllerDisabledOnce("cluster/css-a#ep", "ClusterSecretStore css-a")

	count := 0
	r.cssDisabledWarned.Range(func(key, _ interface{}) bool {
		count++
		return true
	})
	if count != 2 {
		t.Fatalf("cssDisabledWarned holds %d entries, want 2 (one per distinct clientKey)", count)
	}
}

// TestDisabledCSSReferences checks the spec scan feeding the status notice:
// ClusterSecretStore refs from both spec.data and spec.dataProcess are
// collected, deduplicated and sorted; other refs contribute nothing.
func TestDisabledCSSReferences(t *testing.T) {
	es := &api.ExternalSecret{
		Spec: api.ExternalSecretSpec{
			Data: []api.DataSource{
				{Key: "k1", SecretStoreRef: &api.SecretStoreRef{Name: "css-b", Kind: "ClusterSecretStore"}},
				{Key: "k2", SecretStoreRef: &api.SecretStoreRef{Name: "ss", Kind: "SecretStore"}},
				{Key: "k3"}, // ENV auth
				{Key: "k4", SecretStoreRef: &api.SecretStoreRef{Name: "css-a", Kind: "ClusterSecretStore"}},
			},
			DataProcess: []api.DataProcess{
				{Extract: &api.DataSource{Key: "e1", SecretStoreRef: &api.SecretStoreRef{Name: "css-b", Kind: "ClusterSecretStore"}}},
				{Extract: nil},
			},
		},
	}
	got := disabledCSSReferences(es)
	if strings.Join(got, ",") != "css-a,css-b" {
		t.Fatalf("disabledCSSReferences() = %v, want [css-a css-b] (deduplicated, sorted)", got)
	}
	if got := disabledCSSReferences(&api.ExternalSecret{}); got != nil {
		t.Fatalf("disabledCSSReferences() = %v, want nil without any CSS reference", got)
	}
}

// findCSSNotice returns the cluster_secret_store_controller_disabled entry
// of the ExternalSecret status, or nil when absent.
func findCSSNotice(es *api.ExternalSecret) *api.DataSyncResult {
	for i := range es.Status.DataSyncResults {
		if es.Status.DataSyncResults[i].ExternalSecretKey == cssControllerDisabledStatusKey {
			return &es.Status.DataSyncResults[i]
		}
	}
	return nil
}

// TestReconcileCSSDisabledServesCachedClientAndRecordsStatus drives full
// Reconcile rounds in degraded mode: an ES referencing a ClusterSecretStore
// whose generation outruns ObservedGeneration AND which carries a residual
// trigger annotation (both rejection legs) must still sync via the cached
// client — no stale retry loop, target Secret written — and must carry the
// user-visible status notice. Follow-up rounds prove the notice idempotence
// (no further status write, resourceVersion stable) while the sync keeps
// working, and flipping the flag back on converges the notice away.
func TestReconcileCSSDisabledServesCachedClientAndRecordsStatus(t *testing.T) {
	css := managedCSS("cluster-store", 5, 4)
	css.Annotations = map[string]string{utils.TriggerReconcileAnnotation: "leftover"}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-css", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data: []api.DataSource{
				{Key: "k1", SecretStoreRef: &api.SecretStoreRef{Name: "cluster-store", Kind: "ClusterSecretStore"}},
			},
		},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{"k1": {"field": []byte("value")}}}
	r := newTestReconciler(t, false, sc, ns, css, es)
	r.ProcessClusterSecretStore = false

	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "es-css"}}

	// Round 1: the stale guard must NOT fire; the cached client serves the sync.
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil in degraded mode", err)
	}
	if res.RequeueAfter == storeClientStaleRetryInterval {
		t.Fatal("Reconcile() applied the stale backoff: degraded mode must reuse the cached client instead of retrying forever")
	}
	if getTestSecret(t, r, "default", "es-css") == nil {
		t.Fatal("target Secret missing: degraded mode must keep syncing with the cached client")
	}

	// Status notice: user-visible, naming the affected ClusterSecretStore.
	refreshed := &api.ExternalSecret{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "es-css"}, refreshed); err != nil {
		t.Fatalf("get ExternalSecret: %v", err)
	}
	notice := findCSSNotice(refreshed)
	if notice == nil {
		t.Fatalf("status lacks the %q notice, got %+v", cssControllerDisabledStatusKey, refreshed.Status.DataSyncResults)
	}
	if notice.Status != "Warning" {
		t.Errorf("notice status = %q, want Warning (the sync itself succeeds)", notice.Status)
	}
	if !strings.Contains(notice.Reason, "ClusterSecretStore controller disabled") || !strings.Contains(notice.Reason, "cluster-store") {
		t.Errorf("notice reason = %q, want the disabled-controller semantics naming cluster-store", notice.Reason)
	}
	rvAfterRound1 := refreshed.ResourceVersion

	// Round 2: identical conditions — the notice must be debounced, i.e. no
	// further status write and therefore a stable resourceVersion, while the
	// degraded sync itself keeps working (no stale backoff, Secret retained).
	res, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() round 2 error = %v", err)
	}
	if res.RequeueAfter == storeClientStaleRetryInterval {
		t.Fatal("Reconcile() round 2 applied the stale backoff: the degraded-mode exemption must hold across rounds")
	}
	if getTestSecret(t, r, "default", "es-css") == nil {
		t.Fatal("target Secret missing in round 2: degraded mode must keep syncing")
	}
	afterRound2 := &api.ExternalSecret{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "es-css"}, afterRound2); err != nil {
		t.Fatalf("get ExternalSecret after round 2: %v", err)
	}
	if afterRound2.ResourceVersion != rvAfterRound1 {
		t.Fatalf("ExternalSecret resourceVersion changed across identical degraded-mode rounds (%s -> %s): status notice is not idempotent",
			rvAfterRound1, afterRound2.ResourceVersion)
	}
	if findCSSNotice(afterRound2) == nil {
		t.Fatalf("status lost the %q notice in round 2", cssControllerDisabledStatusKey)
	}

	// Round 3 (re-enable): simulate the ClusterSecretStore controller coming
	// back — it catches ObservedGeneration up and clears the trigger
	// annotation, then ProcessClusterSecretStore flips back to true. The
	// notice must converge away through the regular deferred status write
	// (no recordCSS call, no retention), while the sync keeps succeeding.
	fetchedCSS := &api.ClusterSecretStore{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: "cluster-store"}, fetchedCSS); err != nil {
		t.Fatalf("get ClusterSecretStore: %v", err)
	}
	fetchedCSS.Status.Conditions[0].ObservedGeneration = fetchedCSS.Generation
	delete(fetchedCSS.Annotations, utils.TriggerReconcileAnnotation)
	if err := r.Update(context.Background(), fetchedCSS); err != nil {
		t.Fatalf("update ClusterSecretStore: %v", err)
	}
	r.ProcessClusterSecretStore = true

	res, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() after re-enable error = %v", err)
	}
	if res.RequeueAfter == storeClientStaleRetryInterval {
		t.Fatal("Reconcile() after re-enable applied the stale backoff although the store is fresh")
	}
	afterReenable := &api.ExternalSecret{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "es-css"}, afterReenable); err != nil {
		t.Fatalf("get ExternalSecret after re-enable: %v", err)
	}
	if notice := findCSSNotice(afterReenable); notice != nil {
		t.Fatalf("the %q notice must converge away once the controller is re-enabled, still present: %+v",
			cssControllerDisabledStatusKey, *notice)
	}
	if getTestSecret(t, r, "default", "es-css") == nil {
		t.Fatal("target Secret missing after re-enable")
	}
}

// TestCSSDisabledNoticeSurvivesResourceManagementWrites pins the per-key
// exemption of the notice from the placeholder lifecycle: a same-round
// resource-management placeholder write (zero-output guard, template
// degradation, ...) must keep the notice visible (merge exemption), the
// deferred status write retains it while the degraded mode applies even
// with dropPlaceholder=true, and flipping the mode off converges it away.
func TestCSSDisabledNoticeSurvivesResourceManagementWrites(t *testing.T) {
	css := managedCSS("cluster-store", 1, 1)
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-css", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data: []api.DataSource{
				{Key: "k1", SecretStoreRef: &api.SecretStoreRef{Name: "cluster-store", Kind: "ClusterSecretStore"}},
			},
		},
	}
	r := newTestReconciler(t, false, nil, css, es)
	r.ProcessClusterSecretStore = false

	// First degraded-mode round: the notice appears.
	if !r.recordCSSControllerDisabledStatus(es, disabledCSSReferences(es)) {
		t.Fatal("recordCSSControllerDisabledStatus() = false, want the first notice write")
	}
	// Second identical call: debounced, no write (idempotence at the source).
	if r.recordCSSControllerDisabledStatus(es, disabledCSSReferences(es)) {
		t.Fatal("recordCSSControllerDisabledStatus() = true on an unchanged notice, want debounce")
	}

	// Same-round resource-management placeholder (e.g. the zero-output
	// guard): the merge must keep the notice — dropping it here was the
	// per-round 2-3x status-write churn.
	r.updateResourceManagementStatus(es, "zero_output_guard", fmt.Errorf("declared 1 data source(s) produced 0 keys"))
	notice := findCSSNotice(es)
	if notice == nil {
		t.Fatalf("notice dropped by updateResourceManagementStatus merge, got %+v", es.Status.DataSyncResults)
	}
	if !statusHasKey(es, "zero_output_guard") {
		t.Fatalf("zero_output_guard placeholder missing after updateResourceManagementStatus, got %+v", es.Status.DataSyncResults)
	}

	// Deferred status write with dropPlaceholder=true (a placeholder-free
	// follow-up round) while the mode still applies: the placeholder is
	// superseded but the notice is retained.
	r.updateExternalSecretStatus(es, nil, nil, map[string][]byte{}, nil, nil, true, false)
	if findCSSNotice(es) == nil {
		t.Fatalf("notice dropped by the deferred status write although the degraded mode still applies, got %+v", es.Status.DataSyncResults)
	}
	if statusHasKey(es, "zero_output_guard") {
		t.Fatalf("stale zero_output_guard placeholder not superseded by the dropPlaceholder=true write, got %+v", es.Status.DataSyncResults)
	}

	// Re-enable: recordCSS no longer runs, retention stops, and the regular
	// deferred write converges the notice away.
	r.ProcessClusterSecretStore = true
	r.updateExternalSecretStatus(es, nil, nil, map[string][]byte{}, nil, nil, true, false)
	if findCSSNotice(es) != nil {
		t.Fatalf("notice must converge away once the controller is re-enabled, got %+v", es.Status.DataSyncResults)
	}
}

// Copyright © 2025 Alibaba Cloud. All rights reserved.

// composite_client_lifecycle_test.go covers the lifecycle of composite-key
// ("clientName#endpoint") clients in the ExternalSecret controller:
//
//   - finalizer cleanup: the finalizer deregisters endpoint-specific clients
//     with a reference check, never touching plain clientName clients;
//   - spec-change garbage collection: a modified/removed kmsEndpoint
//     reclaims the stale composite client (with the same reference check as
//     the finalizer path); steady-state rounds and endpoint-free
//     ExternalSecrets pay no cleanup cost (regression red line); provider
//     absence is fail-closed on every cleanup path.
//
// It also covers the related hardening behaviors:
//   - template metadata debounce: rendered labels/annotations only force a
//     Secret write when not already applied (first/add/change states write,
//     steady state does not);
//   - status write timing: skipped failure rounds still persist their
//     data-source failures (the fatal-template counterpart of this contract
//     lives in zero_output_guard_test.go);
//   - placeholder retention in the deferred status write.

package externalsecret

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
)

// --- Composite key derivation ------------------------------------------------

func TestResolveClientName(t *testing.T) {
	tests := []struct {
		name      string
		ref       *api.SecretStoreRef
		namespace string
		expected  string
	}{
		{"nil ref is the ENV client", nil, "default", backend.EnvClient},
		{"default kind uses the ExternalSecret namespace", &api.SecretStoreRef{Name: "store"}, "ns-a", "namespace/ns-a/store"},
		{"explicit namespace overrides", &api.SecretStoreRef{Name: "store", Namespace: "ns-b"}, "ns-a", "namespace/ns-b/store"},
		{"explicit SecretStore kind", &api.SecretStoreRef{Name: "store", Kind: "SecretStore"}, "ns-a", "namespace/ns-a/store"},
		{"ClusterSecretStore is cluster-scoped", &api.SecretStoreRef{Name: "css", Kind: "ClusterSecretStore"}, "ns-a", "cluster/css"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveClientName(tt.ref, tt.namespace); got != tt.expected {
				t.Errorf("resolveClientName() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestCompositeClientKeysFromSpec(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Data: []api.DataSource{
				{Key: "k1", KmsEndpoint: "ep1"}, // ENV composite
				{Key: "k2"},                     // plain key: excluded
				{Key: "k3", KmsEndpoint: "ep2", SecretStoreRef: &api.SecretStoreRef{Name: "store"}}, // store composite
				{Key: "k4", KmsEndpoint: "ep2", SecretStoreRef: &api.SecretStoreRef{Name: "css", Kind: "ClusterSecretStore"}},
				{Key: "k5", KmsEndpoint: "   "}, // whitespace-only: normalizes to "" -> excluded
			},
			DataProcess: []api.DataProcess{
				{Extract: &api.DataSource{Key: "e1", KmsEndpoint: "ep3"}}, // extract composite
				{Extract: &api.DataSource{Key: "e2"}},                     // plain key: excluded
				{Extract: nil},                                            // skipped
			},
		},
	}
	got := compositeClientKeysFromSpec(es)
	expected := map[string]struct{}{
		"env.client#ep1":              {},
		"namespace/default/store#ep2": {},
		"cluster/css#ep2":             {},
		"env.client#ep3":              {},
	}
	if len(got) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
	for k := range expected {
		if _, ok := got[k]; !ok {
			t.Fatalf("expected composite key %q in %v", k, got)
		}
	}
	// A whitespace-only kmsEndpoint normalizes to the empty string (default
	// endpoint) and must not produce any composite key.
	for k := range got {
		if strings.Contains(k, "# ") || strings.Contains(k, "#\t") || strings.HasSuffix(k, "#") {
			t.Fatalf("whitespace-only kmsEndpoint must not produce a composite key, got %q", k)
		}
	}
	// A spec without any custom endpoint derives no composite keys.
	if keys := compositeClientKeysFromSpec(&api.ExternalSecret{
		Spec: api.ExternalSecretSpec{Data: []api.DataSource{{Key: "k"}}},
	}); len(keys) != 0 {
		t.Fatalf("expected no composite keys without kmsEndpoint, got %v", keys)
	}
}

// --- Finalizer client cleanup with reference check ----------------------------

func TestFinalizerCleanupDeletesUnreferencedCompositeClient(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "kms.vpc.aliyuncs.com")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)

	if err := r.finalizeExternalSecret(context.Background(), logr.Discard(), es, "es-a"); err != nil {
		t.Fatalf("finalizeExternalSecret returned error: %v", err)
	}
	expected := "namespace/default/store#kms.vpc.aliyuncs.com"
	if len(provider.deleted) != 1 || provider.deleted[0] != expected {
		t.Fatalf("expected exactly [%s] to be deleted, got %v", expected, provider.deleted)
	}
	// The target Secret must be deleted by the finalizer as before.
	got := &corev1.Secret{}
	if err := r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "es-a"}, got); err == nil {
		t.Fatalf("expected the target Secret to be deleted")
	}
}

func TestFinalizerCleanupRetainsReferencedCompositeClient(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	storeRef := &api.SecretStoreRef{Name: "store"}
	es := endpointES("es-a", "uid-a", storeRef, "kms.vpc.aliyuncs.com")
	// Another ACTIVE ExternalSecret uses the same storeRef+endpoint combo.
	other := endpointES("es-b", "uid-b", storeRef, "kms.vpc.aliyuncs.com")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, other, secret)

	if err := r.finalizeExternalSecret(context.Background(), logr.Discard(), es, "es-a"); err != nil {
		t.Fatalf("finalizeExternalSecret returned error: %v", err)
	}
	if len(provider.deleted) != 0 {
		t.Fatalf("shared composite client must NOT be deleted while another active ExternalSecret references it, got %v", provider.deleted)
	}
}

func TestFinalizerCleanupIgnoresInDeletionSharer(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	storeRef := &api.SecretStoreRef{Name: "store"}
	es := endpointES("es-a", "uid-a", storeRef, "kms.vpc.aliyuncs.com")
	// The only sharer is itself being deleted (and runs its own
	// reference-checked cleanup), so it does not block the deregistration.
	now := metav1.Now()
	other := endpointES("es-b", "uid-b", storeRef, "kms.vpc.aliyuncs.com")
	other.DeletionTimestamp = &now
	other.Finalizers = []string{"keep-in-tracker"}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, other, secret)

	if err := r.finalizeExternalSecret(context.Background(), logr.Discard(), es, "es-a"); err != nil {
		t.Fatalf("finalizeExternalSecret returned error: %v", err)
	}
	expected := "namespace/default/store#kms.vpc.aliyuncs.com"
	if len(provider.deleted) != 1 || provider.deleted[0] != expected {
		t.Fatalf("expected [%s] to be deleted once the only sharer is in deletion, got %v", expected, provider.deleted)
	}
}

func TestFinalizerCleanupENVCompositeClient(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	// ENV authentication path: no secretStoreRef, custom endpoint.
	es := endpointES("es-env", "uid-env", nil, "kms.vpc.aliyuncs.com")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-env", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)

	if err := r.finalizeExternalSecret(context.Background(), logr.Discard(), es, "es-env"); err != nil {
		t.Fatalf("finalizeExternalSecret returned error: %v", err)
	}
	expected := "env.client#kms.vpc.aliyuncs.com"
	if len(provider.deleted) != 1 || provider.deleted[0] != expected {
		t.Fatalf("expected exactly [%s] to be deleted, got %v", expected, provider.deleted)
	}
}

func TestFinalizerCleanupNoCompositeKeysIsNoop(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-plain", Namespace: "default", UID: "uid-plain"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "k", SecretStoreRef: &api.SecretStoreRef{Name: "store"}}},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-plain", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)

	if err := r.finalizeExternalSecret(context.Background(), logr.Discard(), es, "es-plain"); err != nil {
		t.Fatalf("finalizeExternalSecret returned error: %v", err)
	}
	if len(provider.deleted) != 0 {
		t.Fatalf("default-endpoint plain-key clients are owned by the SecretStore controller and must never be deleted here, got %v", provider.deleted)
	}
}

// --- Template metadata debounce ------------------------------------------------

func TestTemplateMetadataTargetsAppliedTruthTable(t *testing.T) {
	targets := map[string]map[string]string{
		"labels":      {"app": "v1"},
		"annotations": {"managed-by": "ack-secret-manager"},
	}
	tests := []struct {
		name        string
		targets     map[string]map[string]string
		labels      map[string]string
		annotations map[string]string
		expected    bool
	}{
		{"nil targets are not applied", nil, map[string]string{"app": "v1"}, nil, false},
		{"empty targets are not applied", map[string]map[string]string{}, nil, nil, false},
		{"missing current metadata is not applied", targets, nil, nil, false},
		{"missing one key is not applied", targets, map[string]string{"app": "v1"}, map[string]string{}, false},
		{"differing value is not applied", targets, map[string]string{"app": "v2"}, map[string]string{"managed-by": "ack-secret-manager"}, false},
		{"all targets present and equal are applied", targets, map[string]string{"app": "v1"}, map[string]string{"managed-by": "ack-secret-manager"}, true},
		{"extra current keys do not prevent applied", targets, map[string]string{"app": "v1", "extra": "x"}, map[string]string{"managed-by": "ack-secret-manager", "extra": "y"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := templateMetadataTargetsApplied(tt.targets, tt.labels, tt.annotations); got != tt.expected {
				t.Errorf("templateMetadataTargetsApplied() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

// TestMetadataDebounceThreeStates drives syncIfNeedUpdate through the three
// metadata states with otherwise stable data: first round writes (annotation
// absent), steady state does NOT rewrite (all targets applied), and a changed
// target writes again.
func TestMetadataDebounceThreeStates(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "metadata-debounce-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "src-key"}},
			Target: &api.ExternalSecretTarget{
				Name: "metadata-debounce-secret",
				Template: &api.ExternalSecretTemplate{
					TemplateFrom: []api.TemplateFrom{
						{Literal: strPtr("app=v1"), Target: api.TemplateTargetAnnotations},
					},
				},
			},
		},
	}
	// Existing Secret carries the SAME data the backend returns: only the
	// metadata can drive the update decision.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "metadata-debounce-secret", Namespace: "default"},
		Data:       map[string][]byte{"src-key": []byte("v")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"src-key": {"src-key": []byte("v")},
	}}
	r := newTestReconciler(t, false, sc, es, secret)

	// State 1 (first/added): the annotation is absent -> the round writes.
	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("round 1 returned error: %v", err)
	}
	if !updated {
		t.Fatalf("first metadata round must write the Secret")
	}
	got := getTestSecret(t, r, "default", "metadata-debounce-secret")
	if got.Annotations["literal"] != "app=v1" {
		t.Fatalf("expected rendered annotation after round 1, got %v", got.Annotations)
	}

	// State 2 (steady): data unchanged and every target applied -> NO write.
	updated, err = r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("round 2 returned error: %v", err)
	}
	if updated {
		t.Fatalf("steady-state round with applied metadata must NOT rewrite the Secret")
	}

	// State 3 (changed): the rendered value differs -> the round writes again.
	es.Spec.Target.Template.TemplateFrom[0].Literal = strPtr("app=v2")
	updated, err = r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("round 3 returned error: %v", err)
	}
	if !updated {
		t.Fatalf("changed metadata target must trigger a write")
	}
	got = getTestSecret(t, r, "default", "metadata-debounce-secret")
	if got.Annotations["literal"] != "app=v2" {
		t.Fatalf("expected updated annotation after round 3, got %v", got.Annotations)
	}
}

// --- Status write timing -------------------------------------------------------

// TestSkippedFailureRoundStillPersistsDataFailures proves the deferred status
// write still happens on the fail-closed skip path (total failure with
// cleanup=false): the data-source failure entry must reach the status even
// though no Secret write occurs.
func TestSkippedFailureRoundStillPersistsDataFailures(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "skipped-failure-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "bad-key"}},
			Target:   &api.ExternalSecretTarget{Name: "skipped-failure-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "skipped-failure-secret", Namespace: "default"},
		Data:       map[string][]byte{"existing": []byte("value")},
	}
	sc := &fakeSecretClient{failByKey: map[string]error{"bad-key": fmt.Errorf("backend failure")}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("syncIfNeedUpdate returned error: %v", err)
	}
	if updated {
		t.Fatalf("total failure with cleanup=false must skip the write")
	}
	if !statusHasKey(es, "bad-key") {
		t.Fatalf("the deferred status write must persist the data-source failure, got %+v", es.Status.DataSyncResults)
	}
}

// --- Placeholder retention in the deferred status write ------------------------

func TestRetainResourceManagementPlaceholder(t *testing.T) {
	placeholder := api.DataSyncResult{
		ExternalSecretKey: "template_processing_errors",
		Status:            "Failed",
		Reason:            "parse error",
	}
	dataFailure := api.DataSyncResult{
		ExternalSecretKey: "bad-key",
		Status:            "Failed",
		Reason:            "backend down",
	}

	t.Run("placeholder from old is retained alongside fresh data entries", func(t *testing.T) {
		fresh := []api.DataSyncResult{dataFailure}
		old := []api.DataSyncResult{placeholder}
		got := retainResourceManagementPlaceholder(fresh, old)
		if len(got) != 2 {
			t.Fatalf("expected 2 entries (data failure + placeholder), got %+v", got)
		}
		// Inputs must not be mutated.
		if len(fresh) != 1 || len(old) != 1 {
			t.Fatalf("inputs were mutated: fresh=%v old=%v", fresh, old)
		}
	})

	t.Run("genuine data-key entries in old are not retained as placeholders", func(t *testing.T) {
		got := retainResourceManagementPlaceholder(
			[]api.DataSyncResult{dataFailure},
			[]api.DataSyncResult{{ExternalSecretKey: "other-key", Status: "Failed", Reason: "x"}},
		)
		if len(got) != 1 {
			t.Fatalf("expected only the fresh entry, got %+v", got)
		}
	})

	t.Run("no duplication when fresh already carries the placeholder key", func(t *testing.T) {
		got := retainResourceManagementPlaceholder(
			[]api.DataSyncResult{placeholder},
			[]api.DataSyncResult{placeholder},
		)
		if len(got) != 1 {
			t.Fatalf("expected a single placeholder entry, got %+v", got)
		}
	})

	t.Run("empty-key Succeeded entry in old is superseded, not retained", func(t *testing.T) {
		got := retainResourceManagementPlaceholder(
			[]api.DataSyncResult{dataFailure},
			[]api.DataSyncResult{{ExternalSecretKey: "", Status: "Succeeded", SynchronizationTime: metav1.Time{Time: time.Now()}}},
		)
		if len(got) != 1 || got[0].ExternalSecretKey != "bad-key" {
			t.Fatalf("the stale round verdict must be superseded by the fresh failures, got %+v", got)
		}
	})
}

// TestDegradedRoundRetainsSameRoundPlaceholder drives a full degraded round
// (data-source failure + fatal template error): the template_processing_errors
// placeholder written earlier in the SAME round must stay visible alongside
// the real data-key failure entry persisted by the deferred status write.
func TestDegradedRoundRetainsSameRoundPlaceholder(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "placeholder-degraded-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "bad-key"}},
			Target: &api.ExternalSecretTarget{
				Name: "placeholder-degraded-secret",
				Template: &api.ExternalSecretTemplate{
					Data: map[string]string{"out": "{{ if"},
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "placeholder-degraded-secret", Namespace: "default"},
		Data:       map[string][]byte{"existing": []byte("value")},
	}
	sc := &fakeSecretClient{failByKey: map[string]error{"bad-key": fmt.Errorf("backend failure")}}
	r := newTestReconciler(t, false, sc, es, secret)

	updated, err := r.syncIfNeedUpdate(context.Background(), es)
	if err != nil {
		t.Fatalf("degraded round must not return an error: %v", err)
	}
	if updated {
		t.Fatalf("degraded round must skip the Secret write")
	}
	if !statusHasKey(es, "template_processing_errors") {
		t.Fatalf("the same-round template_processing_errors placeholder must be retained, got %+v", es.Status.DataSyncResults)
	}
	if !statusHasKey(es, "bad-key") {
		t.Fatalf("the real data-key failure entry must be persisted, got %+v", es.Status.DataSyncResults)
	}
}

// TestRecoveryRoundDropsStalePlaceholder pins the convergence fix: a
// placeholder persisted by a PREVIOUS round (rate_limit here) must be
// superseded once a normal data-sync round completes -- it must NOT be merged
// into the fresh results forever.
func TestRecoveryRoundDropsStalePlaceholder(t *testing.T) {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "placeholder-recover-es", Namespace: "default"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "good-key"}},
			Target:   &api.ExternalSecretTarget{Name: "placeholder-recover-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "placeholder-recover-secret", Namespace: "default"},
		Data:       map[string][]byte{"good-key": []byte("stale")},
	}
	sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{
		"good-key": {"good-key": []byte("fresh")},
	}}
	r := newTestReconciler(t, false, sc, es, secret)

	// Simulate a previous throttled round that persisted the placeholder.
	r.updateResourceManagementStatus(es, "rate_limit", fmt.Errorf("throttled"))
	if !statusHasKey(es, "rate_limit") {
		t.Fatalf("precondition: rate_limit placeholder must be persisted, got %+v", es.Status.DataSyncResults)
	}

	// Recovery round: every data source succeeds.
	if _, err := r.syncIfNeedUpdate(context.Background(), es); err != nil {
		t.Fatalf("recovery round returned error: %v", err)
	}
	if statusHasKey(es, "rate_limit") {
		t.Fatalf("stale rate_limit placeholder from a previous round must be dropped on recovery, got %+v", es.Status.DataSyncResults)
	}
	if len(es.Status.DataSyncResults) != 1 || es.Status.DataSyncResults[0].Status != "Succeeded" {
		t.Fatalf("recovery round must converge to a single Succeeded entry, got %+v", es.Status.DataSyncResults)
	}
}

// --- Spec-change garbage collection of composite clients -----------------------

// TestSpecEndpointChangeReclaimsStaleCompositeClient drives the reconcile
// garbage collector across three rounds: baseline (endpoint A), spec change
// (endpoint B -> the stale A client is reclaimed), and steady state (no
// further deletions).
func TestSpecEndpointChangeReclaimsStaleCompositeClient(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-old")
	r := newCleanupReconciler(t, provider, es)

	// Round 1: first observation only establishes the baseline.
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("baseline round returned error: %v", err)
	}
	if len(provider.deleted) != 0 {
		t.Fatalf("baseline round must not delete anything, got %v", provider.deleted)
	}

	// Round 2: the endpoint is modified -> the stale composite client is reclaimed.
	es.Spec.Data[0].KmsEndpoint = "ep-new"
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("spec-change round returned error: %v", err)
	}
	expected := "namespace/default/store#ep-old"
	if len(provider.deleted) != 1 || provider.deleted[0] != expected {
		t.Fatalf("expected exactly [%s] to be reclaimed, got %v", expected, provider.deleted)
	}

	// Round 3: steady state -> no additional deletions.
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("steady round returned error: %v", err)
	}
	if len(provider.deleted) != 1 {
		t.Fatalf("steady-state round must not delete anything more, got %v", provider.deleted)
	}
}

// TestSpecEndpointRemovedReclaimsStaleCompositeClient covers the deletion
// variant: dropping kmsEndpoint entirely reclaims the former composite
// client and leaves no tracker entry behind.
func TestSpecEndpointRemovedReclaimsStaleCompositeClient(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-old")
	r := newCleanupReconciler(t, provider, es)

	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("baseline round returned error: %v", err)
	}

	// Every entry loses its custom endpoint.
	for i := range es.Spec.Data {
		es.Spec.Data[i].KmsEndpoint = ""
	}
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("endpoint-removal round returned error: %v", err)
	}
	expected := "namespace/default/store#ep-old"
	if len(provider.deleted) != 1 || provider.deleted[0] != expected {
		t.Fatalf("expected exactly [%s] to be reclaimed, got %v", expected, provider.deleted)
	}

	// The endpoint-free ExternalSecret must not keep any tracker state.
	if _, tracked := r.snapshotCompositeKeys(es.UID); tracked {
		t.Fatalf("endpoint-free ExternalSecret must not retain tracker state")
	}
}

// TestSpecChangeRetainsStaleKeyReferencedByOther proves the spec-change path
// shares the finalizer's reference check: a stale composite key still used
// by another active ExternalSecret is retained.
func TestSpecChangeRetainsStaleKeyReferencedByOther(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	storeRef := &api.SecretStoreRef{Name: "store"}
	es := endpointES("es-a", "uid-a", storeRef, "ep-old")
	other := endpointES("es-b", "uid-b", storeRef, "ep-old")
	r := newCleanupReconciler(t, provider, es, other)

	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("baseline round returned error: %v", err)
	}

	es.Spec.Data[0].KmsEndpoint = "ep-new"
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("spec-change round returned error: %v", err)
	}
	if len(provider.deleted) != 0 {
		t.Fatalf("stale composite key still referenced by another active ExternalSecret must be retained, got %v", provider.deleted)
	}
}

// TestWhitespaceEndpointTreatedAsSameKey pins the endpoint normalization
// contract of the composite-key GC: a whitespace-padded kmsEndpoint produces
// the same composite key as its trimmed form, so switching between the two
// is a no-op, and a subsequent real endpoint change still reclaims the
// trimmed-form key (the provider side registers under the trimmed key too).
func TestWhitespaceEndpointTreatedAsSameKey(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, " ep-old ")
	r := newCleanupReconciler(t, provider, es)

	// Round 1: baseline with the whitespace-padded endpoint.
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("baseline round returned error: %v", err)
	}

	// Round 2: switching to the trimmed form is the SAME key -> no deletion.
	es.Spec.Data[0].KmsEndpoint = "ep-old"
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("whitespace-normalization round returned error: %v", err)
	}
	if len(provider.deleted) != 0 {
		t.Fatalf("whitespace-padded and trimmed endpoints must map to the same composite key, got deletions %v", provider.deleted)
	}

	// Round 3: a real endpoint change reclaims the trimmed-form key exactly.
	es.Spec.Data[0].KmsEndpoint = "ep-new"
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("endpoint-change round returned error: %v", err)
	}
	expected := "namespace/default/store#ep-old"
	if len(provider.deleted) != 1 || provider.deleted[0] != expected {
		t.Fatalf("expected exactly [%s] to be reclaimed (trimmed form), got %v", expected, provider.deleted)
	}
}

// TestSpecEndpointChangedToWhitespaceOnlyReclaimsStaleCompositeClient covers
// the whitespace boundary: switching kmsEndpoint from a real endpoint to a
// whitespace-only value is equivalent to removing the endpoint — the old
// trimmed-form composite client is reclaimed and no tracker state remains.
func TestSpecEndpointChangedToWhitespaceOnlyReclaimsStaleCompositeClient(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-old")
	r := newCleanupReconciler(t, provider, es)

	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("baseline round returned error: %v", err)
	}

	// Every entry's endpoint becomes whitespace-only.
	for i := range es.Spec.Data {
		es.Spec.Data[i].KmsEndpoint = "   "
	}
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("whitespace-only round returned error: %v", err)
	}
	expected := "namespace/default/store#ep-old"
	if len(provider.deleted) != 1 || provider.deleted[0] != expected {
		t.Fatalf("expected exactly [%s] to be reclaimed (whitespace-only == endpoint removed), got %v", expected, provider.deleted)
	}

	// A whitespace-only endpoint normalizes to "" and must not keep any
	// tracker state, exactly like an endpoint-free ExternalSecret.
	if _, tracked := r.snapshotCompositeKeys(es.UID); tracked {
		t.Fatalf("whitespace-only endpoint ExternalSecret must not retain tracker state")
	}

	// Steady state: repeating the whitespace-only round deletes nothing more.
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("steady whitespace round returned error: %v", err)
	}
	if len(provider.deleted) != 1 {
		t.Fatalf("steady whitespace round must not delete anything more, got %v", provider.deleted)
	}
}

// TestPlainSpecNeverTrackedIsRegressionGuard pins the red line: an
// endpoint-free ExternalSecret never enters the tracker and never triggers
// any client deletion, so the no-endpoint behavior is completely unchanged.
func TestPlainSpecNeverTrackedIsRegressionGuard(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-plain", Namespace: "default", UID: "uid-plain"},
		Spec: api.ExternalSecretSpec{
			Provider: "kms",
			Data:     []api.DataSource{{Key: "k", SecretStoreRef: &api.SecretStoreRef{Name: "store"}}},
		},
	}
	r := newCleanupReconciler(t, provider, es)

	for i := 0; i < 3; i++ {
		if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
			t.Fatalf("round %d returned error: %v", i+1, err)
		}
	}
	if len(provider.deleted) != 0 {
		t.Fatalf("endpoint-free ExternalSecret must never trigger client deletion, got %v", provider.deleted)
	}
	if _, tracked := r.snapshotCompositeKeys(es.UID); tracked {
		t.Fatalf("endpoint-free ExternalSecret must never be tracked")
	}
}

// TestCleanupProviderNotFoundSemantics verifies the path-specific handling
// of a missing provider: the deletion path (finalizer) warns and continues
// so deletion stays the escape hatch for misconfigured resources, while the
// reconcile path stays fail-closed.
func TestCleanupProviderNotFoundSemantics(t *testing.T) {
	t.Run("deletion path warns and continues", func(t *testing.T) {
		provider := &cleanupRecordingProvider{}
		es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-old")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
			Data:       map[string][]byte{"k": []byte("v")},
		}
		r := newCleanupReconciler(t, provider, es, secret)

		// Simulate the provider being unavailable during cleanup (e.g. a
		// legacy ExternalSecret with a misspelled spec.provider).
		prev := backend.GetProviderByName(backend.ProviderKMSName)
		backend.DeleteProvider(backend.ProviderKMSName)
		t.Cleanup(func() { backend.RegisterProvider(backend.ProviderKMSName, prev) })

		// Must NOT fail: a missing provider may never block the removal of
		// an ExternalSecret forever (it stays stuck Terminating otherwise).
		if err := r.cleanupEndpointClients(context.Background(), logr.Discard(), es); err != nil {
			t.Fatalf("cleanupEndpointClients() error = %v, want nil on the deletion path (warn-and-continue)", err)
		}
		if len(provider.deleted) != 0 {
			t.Fatalf("no client may be deleted when the provider is missing, got %v", provider.deleted)
		}
	})

	t.Run("spec-change path fails closed", func(t *testing.T) {
		provider := &cleanupRecordingProvider{}
		es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-new")
		r := newCleanupReconciler(t, provider, es)

		// Seed the tracker with a stale key from a previous round.
		r.storeCompositeKeys(es.UID, map[string]struct{}{
			"namespace/default/store#ep-old": {},
			"namespace/default/store#ep-new": {},
		})

		prev := backend.GetProviderByName(backend.ProviderKMSName)
		backend.DeleteProvider(backend.ProviderKMSName)
		t.Cleanup(func() { backend.RegisterProvider(backend.ProviderKMSName, prev) })

		err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es)
		if err == nil || !strings.Contains(err.Error(), "provider kms not found") {
			t.Fatalf("reconcileStaleCompositeClients() error = %v, want provider-not-found error (fail-closed)", err)
		}

		// The failed round must keep the previous snapshot so the next round
		// retries the same stale-key diff.
		if _, tracked := r.snapshotCompositeKeys(es.UID); !tracked {
			t.Fatalf("failed cleanup must retain the tracked snapshot for retry")
		}

		// Recovery: re-register the recording provider and retry; the stale
		// key must be reclaimed now.
		backend.RegisterProvider(backend.ProviderKMSName, provider)
		if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
			t.Fatalf("retry round after provider recovery returned error: %v", err)
		}
		expected := "namespace/default/store#ep-old"
		if len(provider.deleted) != 1 || provider.deleted[0] != expected {
			t.Fatalf("retry round must reclaim [%s], got %v", expected, provider.deleted)
		}
	})
}

// TestFinalizerCleanupForgetsTracker verifies the deletion path drops the
// tracked key set after a successful cleanup.
func TestFinalizerCleanupForgetsTracker(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-old")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)

	// Establish a tracked baseline as a previous reconcile round would.
	if err := r.reconcileStaleCompositeClients(context.Background(), logr.Discard(), es); err != nil {
		t.Fatalf("baseline round returned error: %v", err)
	}

	if err := r.finalizeExternalSecret(context.Background(), logr.Discard(), es, "es-a"); err != nil {
		t.Fatalf("finalizeExternalSecret returned error: %v", err)
	}
	if _, tracked := r.snapshotCompositeKeys(es.UID); tracked {
		t.Fatalf("finalizer cleanup must drop the tracked key set")
	}
}

// TestFinalizerCleanupReclaimsTrackedStaleKey pins the deletion-path leak
// fix: workqueue event coalescing can deliver an endpoint change together
// with the deletion in a single round (handleDeletion bypasses
// reconcileStaleCompositeClients), so the finalizer must reclaim stale keys
// from the tracked snapshot, not just the current spec keys.
func TestFinalizerCleanupReclaimsTrackedStaleKey(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-new")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)

	// A previous round tracked the pre-change endpoint key; the spec-change
	// GC round never ran (event coalescing or cleanup backoff).
	r.storeCompositeKeys(es.UID, map[string]struct{}{
		"namespace/default/store#ep-old": {},
	})

	if err := r.finalizeExternalSecret(context.Background(), logr.Discard(), es, "es-a"); err != nil {
		t.Fatalf("finalizeExternalSecret returned error: %v", err)
	}
	got := map[string]bool{}
	for _, k := range provider.deleted {
		got[k] = true
	}
	if len(provider.deleted) != 2 || !got["namespace/default/store#ep-old"] || !got["namespace/default/store#ep-new"] {
		t.Fatalf("expected both the spec key and the tracked stale key to be reclaimed, got %v", provider.deleted)
	}
	if _, tracked := r.snapshotCompositeKeys(es.UID); tracked {
		t.Fatalf("finalizer cleanup must drop the tracked key set")
	}
}

// TestFinalizerCleanupFailureRetainsTracker pins the counterpart of
// TestFinalizerCleanupForgetsTracker: when the cleanup fails (here a List
// error, fail-closed), forgetCompositeKeys must NOT run, so the next
// finalizer attempt retries the same union of tracked + spec keys.
func TestFinalizerCleanupFailureRetainsTracker(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-old")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("add api scheme: %v", err)
	}
	listErr := errors.New("api server unavailable")
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(es, secret).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return listErr
			},
		}).Build()
	prev := backend.GetProviderByName(backend.ProviderKMSName)
	backend.RegisterProvider(backend.ProviderKMSName, provider)
	t.Cleanup(func() { backend.RegisterProvider(backend.ProviderKMSName, prev) })
	r := &ExternalSecretReconciler{Client: cl, APIReader: cl, Log: logr.Discard(), Ctx: context.Background()}

	r.storeCompositeKeys(es.UID, map[string]struct{}{
		"namespace/default/store#ep-old": {},
	})

	err := r.cleanupEndpointClients(context.Background(), logr.Discard(), es)
	if !errors.Is(err, listErr) {
		t.Fatalf("cleanupEndpointClients() error = %v, want the List error (fail-closed)", err)
	}
	if len(provider.deleted) != 0 {
		t.Fatalf("no client may be deleted when the reference-check List fails, got %v", provider.deleted)
	}
	if _, tracked := r.snapshotCompositeKeys(es.UID); !tracked {
		t.Fatalf("failed cleanup must retain the tracked snapshot so the finalizer retries")
	}
}

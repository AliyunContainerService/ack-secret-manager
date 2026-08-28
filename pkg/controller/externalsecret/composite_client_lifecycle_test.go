// Copyright © 2025 Alibaba Cloud. All rights reserved.

// composite_client_lifecycle_test.go covers the lifecycle of composite-key
// ("clientName#endpoint") clients: finalizer cleanup and spec-change garbage
// collection (both reference-checked, never touching plain clientName
// clients; provider absence is fail-closed on the reconcile path), plus the
// related hardening behaviors: template metadata debounce, status write
// timing on skipped failure rounds, and placeholder retention in the
// deferred status write.

package externalsecret

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	// A whitespace-only kmsEndpoint normalizes to "" and must not produce a
	// composite key.
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
	// The only sharer is itself being deleted, so it does not block the
	// deregistration.
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

// --- Spec-change garbage collection of composite clients -----------------------

// TestSpecEndpointChangeReclaimsStaleCompositeClient drives the GC across
// three rounds: baseline (endpoint A), spec change (endpoint B -> the stale
// A client is reclaimed), and steady state (no further deletions).
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

// TestSpecEndpointRemovedReclaimsStaleCompositeClient: dropping kmsEndpoint
// entirely reclaims the former composite client and leaves no tracker entry.
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

// TestSpecChangeRetainsStaleKeyReferencedByOther: the spec-change path
// shares the finalizer's reference check; a stale key still used by another
// active ExternalSecret is retained.
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
// contract: a whitespace-padded kmsEndpoint maps to the same composite key
// as its trimmed form (switching is a no-op), and a real endpoint change
// still reclaims the trimmed-form key.
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

// TestSpecEndpointChangedToWhitespaceOnlyReclaimsStaleCompositeClient:
// switching to a whitespace-only endpoint equals removing the endpoint --
// the old composite client is reclaimed and no tracker state remains.
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

	// A whitespace-only endpoint normalizes to "" and keeps no tracker state.
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
// client deletion.
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

// TestCleanupProviderNotFoundSemantics: the deletion path warns and
// continues (deletion stays the escape hatch), while the reconcile path
// stays fail-closed.
func TestCleanupProviderNotFoundSemantics(t *testing.T) {
	t.Run("deletion path warns and continues", func(t *testing.T) {
		provider := &cleanupRecordingProvider{}
		es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-old")
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
			Data:       map[string][]byte{"k": []byte("v")},
		}
		r := newCleanupReconciler(t, provider, es, secret)

		// Simulate the provider being unavailable during cleanup.
		prev := backend.GetProviderByName(backend.ProviderKMSName)
		backend.DeleteProvider(backend.ProviderKMSName)
		t.Cleanup(func() { backend.RegisterProvider(backend.ProviderKMSName, prev) })

		// Must NOT fail: a missing provider may never block the removal of an
		// ExternalSecret forever (it stays stuck Terminating otherwise).
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

		// The failed round keeps the previous snapshot so the next round
		// retries the same stale-key diff.
		if _, tracked := r.snapshotCompositeKeys(es.UID); !tracked {
			t.Fatalf("failed cleanup must retain the tracked snapshot for retry")
		}

		// Recovery: re-register the provider and retry; the stale key must be
		// reclaimed now.
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
// fix: event coalescing can deliver an endpoint change together with the
// deletion, so the finalizer must reclaim stale keys from the tracked
// snapshot, not just the current spec keys.
func TestFinalizerCleanupReclaimsTrackedStaleKey(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := endpointES("es-a", "uid-a", &api.SecretStoreRef{Name: "store"}, "ep-new")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-a", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)

	// A previous round tracked the pre-change endpoint key; the spec-change
	// GC round never ran (event coalescing).
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

// TestFinalizerCleanupFailureRetainsTracker: when cleanup fails (fail-closed
// List error), forgetCompositeKeys must NOT run, so the next finalizer
// attempt retries the same union of tracked + spec keys.
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

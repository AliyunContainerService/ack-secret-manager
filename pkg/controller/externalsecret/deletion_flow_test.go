// Copyright © 2025 Alibaba Cloud. All rights reserved.

// deletion_flow_test.go covers the Reconcile-level deletion path
// (handleDeletion): an ExternalSecret carrying a DeletionTimestamp and the
// controller finalizer must have its target Secret deleted, its composite
// endpoint clients deregistered, and its finalizer removed; without the
// finalizer nothing is touched.
//
// SERIAL: the builders mutate the process-global provider registry (restored
// via t.Cleanup); t.Parallel() is forbidden in this package.

package externalsecret

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
)

// deletionBaselineTime is a fixed timestamp for DeletionTimestamp fixtures so
// deletion tests never depend on the wall clock.
var deletionBaselineTime = metav1.NewTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

// deletingES builds an ExternalSecret already marked for deletion (fixed
// DeletionTimestamp) carrying the given finalizers.
func deletingES(name, uid string, finalizers []string, spec api.ExternalSecretSpec) *api.ExternalSecret {
	return &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			UID:               types.UID(uid),
			Finalizers:        finalizers,
			DeletionTimestamp: &deletionBaselineTime,
		},
		Spec: spec,
	}
}

// reconcileES drives one Reconcile round for the given ExternalSecret.
func reconcileES(t *testing.T, r *ExternalSecretReconciler, es *api.ExternalSecret) (ctrl.Result, error) {
	t.Helper()
	return r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: es.Namespace, Name: es.Name},
	})
}

// getTestExternalSecret fetches the ExternalSecret, returning nil when it no
// longer exists (finalizer removed while deleting -> object gone).
func getTestExternalSecret(t *testing.T, r *ExternalSecretReconciler, ns, name string) *api.ExternalSecret {
	t.Helper()
	es := &api.ExternalSecret{}
	err := r.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, es)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("get externalsecret %s/%s: %v", ns, name, err)
	}
	return es
}

// TestReconcileDeletionRemovesFinalizerAndCleansUp drives the full deletion
// path: target Secret deleted, composite endpoint client deregistered,
// finalizer removed (object disappears from the tracker).
func TestReconcileDeletionRemovesFinalizerAndCleansUp(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := deletingES("es-del", "uid-del", []string{secretFinalizer}, api.ExternalSecretSpec{
		Provider: "kms",
		Data: []api.DataSource{
			{Key: "k", KmsEndpoint: "kms.vpc.aliyuncs.com", SecretStoreRef: &api.SecretStoreRef{Name: "store"}},
		},
		// Custom target name: secretName must derive from Spec.Target.Name.
		Target: &api.ExternalSecretTarget{Name: "custom-target"},
	})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "custom-target", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)

	res, err := reconcileES(t, r, es)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if res.Requeue {
		t.Fatalf("unexpected Requeue flag, got %+v", res)
	}

	// The target Secret must be deleted by the finalizer flow.
	if got := getTestSecret(t, r, "default", "custom-target"); got != nil {
		t.Fatalf("expected the target Secret to be deleted, still present")
	}
	// The composite endpoint client must be deregistered exactly once.
	expectedKey := "namespace/default/store#kms.vpc.aliyuncs.com"
	if len(provider.deleted) != 1 || provider.deleted[0] != expectedKey {
		t.Fatalf("expected provider Delete(%q), got %v", expectedKey, provider.deleted)
	}
	// Finalizer removed while deleting: the fake tracker drops the object.
	if got := getTestExternalSecret(t, r, "default", "es-del"); got != nil {
		t.Fatalf("expected the ExternalSecret to be gone after finalizer removal, finalizers=%v", got.Finalizers)
	}
}

// TestReconcileDeletionWithoutOurFinalizerTouchesNothing pins the early
// return: an ExternalSecret being deleted but NOT carrying the controller
// finalizer leaves the target Secret and the provider cache untouched.
func TestReconcileDeletionWithoutOurFinalizerTouchesNothing(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := deletingES("es-foreign", "uid-foreign", []string{"other-controller/finalizer"}, api.ExternalSecretSpec{
		Provider: "kms",
		Data: []api.DataSource{
			{Key: "k", KmsEndpoint: "kms.vpc.aliyuncs.com", SecretStoreRef: &api.SecretStoreRef{Name: "store"}},
		},
	})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-foreign", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)

	if _, err := reconcileES(t, r, es); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}

	// Not our finalizer -> cleanup skipped: Secret retained.
	if got := getTestSecret(t, r, "default", "es-foreign"); got == nil {
		t.Fatalf("target Secret must be retained when the controller finalizer is absent")
	}
	// No provider client deregistration.
	if len(provider.deleted) != 0 {
		t.Fatalf("expected no provider Delete calls, got %v", provider.deleted)
	}
	// The ExternalSecret itself must still exist (foreign finalizer holds it).
	if got := getTestExternalSecret(t, r, "default", "es-foreign"); got == nil {
		t.Fatalf("ExternalSecret must still exist behind the foreign finalizer")
	}
}

// TestReconcileDeletionSecretAlreadyGone verifies idempotency: when the
// target Secret was already deleted, finalization still completes and the
// finalizer is removed.
func TestReconcileDeletionSecretAlreadyGone(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := deletingES("es-gone", "uid-gone", []string{secretFinalizer}, api.ExternalSecretSpec{
		Provider: "kms",
		Data:     []api.DataSource{{Key: "k", SecretStoreRef: &api.SecretStoreRef{Name: "store"}}},
	})
	r := newCleanupReconciler(t, provider, es)

	if _, err := reconcileES(t, r, es); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if got := getTestExternalSecret(t, r, "default", "es-gone"); got != nil {
		t.Fatalf("expected finalizer removal, finalizers=%v", got.Finalizers)
	}
	// Plain (default-endpoint) entries own no composite client: nothing deleted.
	if len(provider.deleted) != 0 {
		t.Fatalf("expected no provider Delete calls for plain entries, got %v", provider.deleted)
	}
}

// TestReconcileDeletionIgnoresCleanupOnFailureFlag documents the deletion
// contract: the target Secret is always removed on ExternalSecret deletion,
// even with CleanUpSecretOnFailure=false. That flag only governs the
// total-provider-failure write path, never the deletion flow.
func TestReconcileDeletionIgnoresCleanupOnFailureFlag(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := deletingES("es-noclean", "uid-noclean", []string{secretFinalizer}, api.ExternalSecretSpec{
		Provider: "kms",
		Data:     []api.DataSource{{Key: "k", SecretStoreRef: &api.SecretStoreRef{Name: "store"}}},
	})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "es-noclean", Namespace: "default"},
		Data:       map[string][]byte{"k": []byte("v")},
	}
	r := newCleanupReconciler(t, provider, es, secret)
	r.CleanUpSecretOnFailure = false

	if _, err := reconcileES(t, r, es); err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if got := getTestSecret(t, r, "default", "es-noclean"); got != nil {
		t.Fatalf("deletion flow must delete the target Secret regardless of CleanUpSecretOnFailure")
	}
	if got := getTestExternalSecret(t, r, "default", "es-noclean"); got != nil {
		t.Fatalf("expected finalizer removal, finalizers=%v", got.Finalizers)
	}
}

// TestReconcileDeletionRetainsFinalizerOnCleanupFailure pins the fail-closed
// branch: when finalization fails, the finalizer stays so the next reconcile
// retries, and the error is surfaced to the controller runtime.
func TestReconcileDeletionRetainsFinalizerOnCleanupFailure(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := deletingES("es-fail", "uid-fail", []string{secretFinalizer}, api.ExternalSecretSpec{
		Provider: "kms",
		Data:     []api.DataSource{{Key: "k", SecretStoreRef: &api.SecretStoreRef{Name: "store"}}},
	})

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("add api scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&api.ExternalSecret{}).
		WithObjects(es).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, isSecret := obj.(*corev1.Secret); isSecret {
					return errors.New("injected secret get failure")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).Build()

	// Register the recording provider exactly like newCleanupReconciler does.
	prev := backend.GetProviderByName(backend.ProviderKMSName)
	backend.RegisterProvider(backend.ProviderKMSName, provider)
	t.Cleanup(func() {
		if prev != nil {
			backend.RegisterProvider(backend.ProviderKMSName, prev)
		} else {
			backend.DeleteProvider(backend.ProviderKMSName)
		}
	})
	r := &ExternalSecretReconciler{
		Client:               cl,
		APIReader:            cl,
		Log:                  logr.Discard(),
		Ctx:                  context.Background(),
		ReconciliationPeriod: time.Minute,
	}

	res, err := reconcileES(t, r, es)
	if err == nil {
		t.Fatalf("expected Reconcile to surface the cleanup failure")
	}
	if res.RequeueAfter != time.Minute {
		t.Fatalf("expected retry after ReconciliationPeriod, got %+v", res)
	}
	// Finalizer retained -> object still present for the retry round.
	got := getTestExternalSecret(t, r, "default", "es-fail")
	if got == nil {
		t.Fatalf("ExternalSecret must survive a failed cleanup round")
	}
	found := false
	for _, f := range got.Finalizers {
		if f == secretFinalizer {
			found = true
		}
	}
	if !found {
		t.Fatalf("finalizer must be retained on cleanup failure, got %v", got.Finalizers)
	}
}

// TestReconcileDeletionSurfacesFinalizerUpdateFailure covers the last-resort
// branch: cleanup succeeded but the finalizer-removal Update fails; the error
// is surfaced so the round retries.
func TestReconcileDeletionSurfacesFinalizerUpdateFailure(t *testing.T) {
	provider := &cleanupRecordingProvider{}
	es := deletingES("es-updfail", "uid-updfail", []string{secretFinalizer}, api.ExternalSecretSpec{
		Provider: "kms",
		Data:     []api.DataSource{{Key: "k", SecretStoreRef: &api.SecretStoreRef{Name: "store"}}},
	})

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("add api scheme: %v", err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&api.ExternalSecret{}).
		WithObjects(es).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if _, isES := obj.(*api.ExternalSecret); isES {
					return errors.New("injected update failure")
				}
				return c.Update(ctx, obj, opts...)
			},
		}).Build()

	prev := backend.GetProviderByName(backend.ProviderKMSName)
	backend.RegisterProvider(backend.ProviderKMSName, provider)
	t.Cleanup(func() {
		if prev != nil {
			backend.RegisterProvider(backend.ProviderKMSName, prev)
		} else {
			backend.DeleteProvider(backend.ProviderKMSName)
		}
	})
	r := &ExternalSecretReconciler{
		Client:               cl,
		APIReader:            cl,
		Log:                  logr.Discard(),
		Ctx:                  context.Background(),
		ReconciliationPeriod: time.Minute,
	}

	if _, err := reconcileES(t, r, es); err == nil {
		t.Fatalf("expected Reconcile to surface the finalizer update failure")
	}
	// The persisted object still carries the finalizer, so the retry round
	// re-runs the idempotent cleanup.
	got := getTestExternalSecret(t, r, "default", "es-updfail")
	if got == nil {
		t.Fatalf("ExternalSecret must survive the failed finalizer update")
	}
	found := false
	for _, f := range got.Finalizers {
		if f == secretFinalizer {
			found = true
		}
	}
	if !found {
		t.Fatalf("finalizer must still be present after a failed update, got %v", got.Finalizers)
	}
}

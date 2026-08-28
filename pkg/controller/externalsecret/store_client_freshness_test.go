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

// store_client_freshness_test.go covers the consumer-side freshness guard:
// a cached store client must not be used while the Store controller is mid
// rebuild (ObservedGeneration behind metadata.generation or the
// trigger-reconcile annotation still present). A store with NO conditions
// is treated as "never managed by a Store controller" (e.g. a
// ClusterSecretStore while --process-cluster-secret-store=false) and passes
// with the cached client — judging it stale would loop forever. Stale
// rounds are retried after a short backoff via ctrl.Result{RequeueAfter} —
// returned directly so the retry also works under --disable-polling=true —
// while a missing store surfaces through the regular error path (no retry
// loop).

package externalsecret

import (
	"context"
	stderrors "errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// freshStore builds a SecretStore whose status says the current generation
// has been fully processed and no trigger annotation is pending.
func freshStore(ns, name string, generation int64) *api.SecretStore {
	return &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Generation: generation},
		Status: api.SecretStoreStatus{
			Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				ObservedGeneration: generation,
			}},
		},
	}
}

// storeWithObserved builds a SecretStore whose ObservedGeneration lags its
// metadata.generation (Store controller mid rebuild).
func storeWithObserved(ns, name string, generation, observed int64) *api.SecretStore {
	s := freshStore(ns, name, generation)
	s.Status.Conditions[0].ObservedGeneration = observed
	return s
}

func TestEnsureStoreClientFresh(t *testing.T) {
	const clientKey = "namespace/default/store"

	r := newTestReconciler(t, false, nil,
		freshStore("default", "store", 1),
		storeWithObserved("default", "stale", 2, 1),
		// No conditions at all: never managed by a Store controller -> the
		// guard must not judge it stale (legacy behavior, cached client used).
		&api.SecretStore{ObjectMeta: metav1.ObjectMeta{Name: "no-cond", Namespace: "default", Generation: 1}},
		// No conditions + pending trigger annotation: still unmanaged (e.g.
		// --process-cluster-secret-store=false leaves the annotation forever)
		// -> must NOT be stale.
		&api.SecretStore{ObjectMeta: metav1.ObjectMeta{
			Name: "no-cond-annotated", Namespace: "default", Generation: 1,
			Annotations: map[string]string{utils.TriggerReconcileAnnotation: "rotate-1"},
		}},
		&api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-store", Generation: 4},
			Status: api.ClusterSecretStoreStatus{Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				ObservedGeneration: 4,
			}}},
		},
		&api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-store-stale", Generation: 3},
			Status: api.ClusterSecretStoreStatus{Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				ObservedGeneration: 2,
			}}},
		},
		// ClusterSecretStore never managed (no conditions), with and without
		// a trigger annotation: the --process-cluster-secret-store=false
		// regression pair — both must pass.
		&api.ClusterSecretStore{ObjectMeta: metav1.ObjectMeta{Name: "css-no-cond", Generation: 1}},
		&api.ClusterSecretStore{ObjectMeta: metav1.ObjectMeta{
			Name: "css-no-cond-annotated", Generation: 1,
			Annotations: map[string]string{utils.TriggerReconcileAnnotation: "rotate-2"},
		}},
	)

	ctx := context.Background()

	tests := []struct {
		name      string
		ref       *api.SecretStoreRef
		wantStale bool
		wantErr   bool // non-stale error (e.g. NotFound, unsupported kind)
	}{
		{name: "fresh SecretStore passes", ref: &api.SecretStoreRef{Name: "store"}},
		{name: "explicit kind fresh SecretStore passes", ref: &api.SecretStoreRef{Name: "store", Kind: "SecretStore"}},
		{name: "ObservedGeneration behind is stale", ref: &api.SecretStoreRef{Name: "stale"}, wantStale: true},
		{name: "no conditions means unmanaged and passes", ref: &api.SecretStoreRef{Name: "no-cond"}},
		{name: "no conditions with pending annotation means unmanaged and passes", ref: &api.SecretStoreRef{Name: "no-cond-annotated"}},
		{name: "fresh ClusterSecretStore passes", ref: &api.SecretStoreRef{Name: "cluster-store", Kind: "ClusterSecretStore"}},
		{name: "stale ClusterSecretStore", ref: &api.SecretStoreRef{Name: "cluster-store-stale", Kind: "ClusterSecretStore"}, wantStale: true},
		{name: "unmanaged ClusterSecretStore without annotation passes", ref: &api.SecretStoreRef{Name: "css-no-cond", Kind: "ClusterSecretStore"}},
		{name: "unmanaged ClusterSecretStore with annotation passes", ref: &api.SecretStoreRef{Name: "css-no-cond-annotated", Kind: "ClusterSecretStore"}},
		{name: "missing SecretStore is a regular error", ref: &api.SecretStoreRef{Name: "missing"}, wantErr: true},
		{name: "missing ClusterSecretStore is a regular error", ref: &api.SecretStoreRef{Name: "missing", Kind: "ClusterSecretStore"}, wantErr: true},
		{name: "unsupported kind is a regular error", ref: &api.SecretStoreRef{Name: "store", Kind: "Weird"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.ensureStoreClientFresh(ctx, tt.ref, "default", clientKey)
			switch {
			case tt.wantStale:
				if !stderrors.Is(err, errStoreClientStale) {
					t.Fatalf("err = %v, want errors.Is(errStoreClientStale)", err)
				}
			case tt.wantErr:
				if err == nil {
					t.Fatalf("err = nil, want a non-stale error")
				}
				if stderrors.Is(err, errStoreClientStale) {
					t.Fatalf("err = %v, must NOT be stale (would retry forever)", err)
				}
			default:
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			}
		})
	}
}

// TestEnsureStoreClientFreshAnnotationPending verifies the trigger-annotation
// window: ObservedGeneration already matches, but the annotation has not
// been cleared yet, so the cached client must still be refused.
func TestEnsureStoreClientFreshAnnotationPending(t *testing.T) {
	store := freshStore("default", "store", 1)
	store.Annotations = map[string]string{utils.TriggerReconcileAnnotation: "rotate-1"}
	r := newTestReconciler(t, false, nil, store)

	err := r.ensureStoreClientFresh(context.Background(), &api.SecretStoreRef{Name: "store"}, "default", "namespace/default/store")
	if !stderrors.Is(err, errStoreClientStale) {
		t.Fatalf("err = %v, want errors.Is(errStoreClientStale)", err)
	}
}

// TestGetOrCreateClientRefusesStaleCachedClient proves the guard sits on the
// cache-hit path: a provider serving a cached client for a stale store must
// be refused with errStoreClientStale, while the same setup with a fresh
// store returns the cached client.
func TestGetOrCreateClientRefusesStaleCachedClient(t *testing.T) {
	sc := &fakeSecretClient{}
	r := newTestReconciler(t, false, sc, storeWithObserved("default", "store", 2, 1))
	provider := backend.GetProviderByName(backend.ProviderKMSName)
	ref := &api.SecretStoreRef{Name: "store"}

	_, _, err := r.getOrCreateClient(context.Background(), provider, "namespace/default/store", ref, "default", "")
	if !stderrors.Is(err, errStoreClientStale) {
		t.Fatalf("getOrCreateClient() err = %v, want errors.Is(errStoreClientStale) on stale cached client", err)
	}

	// Make the store fresh: guard passes and the cached client is returned.
	got := &api.SecretStore{}
	if err := r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, got); err != nil {
		t.Fatalf("get store: %v", err)
	}
	got.Status.Conditions[0].ObservedGeneration = 2
	if err := r.Update(context.Background(), got); err != nil {
		t.Fatalf("update store: %v", err)
	}

	cached, key, err := r.getOrCreateClient(context.Background(), provider, "namespace/default/store", ref, "default", "")
	if err != nil {
		t.Fatalf("getOrCreateClient() err = %v, want cached client for fresh store", err)
	}
	if cached == nil || key == "" {
		t.Fatalf("getOrCreateClient() = (%v, %q), want non-nil cached client", cached, key)
	}
}

// TestReconcileRequeuesShortlyOnStaleStoreClient drives a full Reconcile
// round with a stale store behind a cached client: the round must not sync
// (no target Secret written) and must requeue after the short stale backoff
// — including under DisablePolling=true, where the normal r.RequeueAfter
// path would drop the requeue entirely. Once the store turns fresh the same
// ES syncs normally.
func TestReconcileRequeuesShortlyOnStaleStoreClient(t *testing.T) {
	for _, disablePolling := range []bool{false, true} {
		mode := "polling"
		if disablePolling {
			mode = "disable-polling"
		}
		t.Run(mode, func(t *testing.T) {
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "default"},
				Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
			}
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "es1", Namespace: "default"},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{Key: "k1", SecretStoreRef: &api.SecretStoreRef{Name: "store"}},
					},
				},
			}
			sc := &fakeSecretClient{dataByKey: map[string]map[string][]byte{"k1": {"field": []byte("value")}}}
			r := newTestReconciler(t, false, sc, ns, storeWithObserved("default", "store", 2, 1), es)
			r.DisablePolling = disablePolling

			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "es1"}}
			res, err := r.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile() error = %v, want nil (stale must not surface as error)", err)
			}
			if res.RequeueAfter != storeClientStaleRetryInterval {
				t.Fatalf("Reconcile() RequeueAfter = %v, want %v (short stale backoff)",
					res.RequeueAfter, storeClientStaleRetryInterval)
			}
			if getTestSecret(t, r, "default", "es1") != nil {
				t.Fatal("target Secret must NOT be written while the store client is stale")
			}

			// Store controller finishes the rebuild: ObservedGeneration catches
			// up. The retry round must now sync with the cached client.
			store := &api.SecretStore{}
			if err := r.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, store); err != nil {
				t.Fatalf("get store: %v", err)
			}
			store.Status.Conditions[0].ObservedGeneration = store.Generation
			if err := r.Update(context.Background(), store); err != nil {
				t.Fatalf("update store: %v", err)
			}

			res, err = r.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile() after store became fresh: error = %v", err)
			}
			if res.RequeueAfter == storeClientStaleRetryInterval {
				t.Fatal("Reconcile() still applied the stale backoff after the store became fresh")
			}
			if getTestSecret(t, r, "default", "es1") == nil {
				t.Fatal("target Secret missing after the store became fresh")
			}
		})
	}
}

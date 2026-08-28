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

// client_generation_test.go covers the status-driven Store->ExternalSecret
// trigger contract: every successfully persisted client recreation bumps
// status.clientGeneration (spec-change path AND trigger-annotation path),
// while a failed status write persists no bump and the retry round bumps
// exactly once. The ExternalSecret controller's reverse watch on this field
// replaces the former in-process notification channel.

package secretstore

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/kms"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/testutil"
)

// readyStubProvider is a backend.Provider stub whose NewClient returns a
// real *kms.KMSClient value so recreateClient's type assertion succeeds and
// the reconcile can reach the status-write path without any cloud access.
type readyStubProvider struct {
	name          string
	clientPresent bool
}

func (p *readyStubProvider) Register(clientKey string, secretClient backend.SecretClient) {
}

func (p *readyStubProvider) GetClient(clientKey string) (backend.SecretClient, error) {
	if p.clientPresent {
		return &stubSecretClient{}, nil
	}
	return nil, fmt.Errorf("client %s not found", clientKey)
}

func (p *readyStubProvider) Delete(clientKey string) {
}

func (p *readyStubProvider) DeletePrefixed(clientKey string) {
}

func (p *readyStubProvider) NewClient(ctx context.Context, store *api.SecretStore, kube client.Client, endpoint string) (backend.SecretClient, error) {
	return &kms.KMSClient{}, nil
}

func (p *readyStubProvider) NewClientByENV(endpoint string) (backend.SecretClient, error) {
	return nil, nil
}

func (p *readyStubProvider) GetName() string      { return p.name }
func (p *readyStubProvider) GetRegion() string    { return "" }
func (p *readyStubProvider) GetEndpoint() string  { return "" }
func (p *readyStubProvider) GetClusterId() string { return "" }
func (p *readyStubProvider) GetUid() string       { return "" }

// registerReadyStubProviders installs stub providers into the global backend
// registry for the duration of a test and returns a cleanup function.
func registerReadyStubProviders(t *testing.T) func() {
	t.Helper()
	backend.RegisterProvider(backend.ProviderKMSName, &readyStubProvider{name: backend.ProviderKMSName, clientPresent: true})
	backend.RegisterProvider(backend.ProviderOOSName, &readyStubProvider{name: backend.ProviderOOSName})
	return func() {
		backend.DeleteProvider(backend.ProviderKMSName)
		backend.DeleteProvider(backend.ProviderOOSName)
	}
}

// TestReconcileBumpsClientGenerationOnSpecChangePath proves the
// clientGeneration bump is NOT gated on the trigger annotation: a
// spec-change-driven client recreation (generation ahead of
// ObservedGeneration, annotation empty) must persist the bump so the
// ExternalSecret reverse watch wakes up. Without this, --disable-polling
// deployments keep syncing with a stale client until the next annotation
// trigger.
func TestReconcileBumpsClientGenerationOnSpecChangePath(t *testing.T) {
	t.Run("SecretStore spec change without annotation bumps", func(t *testing.T) {
		defer registerReadyStubProviders(t)()

		scheme := testutil.NewTestScheme(t)
		// Generation 2 vs ObservedGeneration 1 => spec change drives
		// recreation; no trigger annotation present.
		store := &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "store",
				Namespace:  "default",
				Generation: 2,
				Finalizers: []string{secretFinalizer},
			},
			Spec: api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
			Status: api.SecretStoreStatus{
				Conditions: []api.SecretStoreStatusCondition{{
					Type:               api.SecretStoreReady,
					Status:             corev1.ConditionTrue,
					Reason:             api.ReasonStoreValid,
					ObservedGeneration: 1,
				}},
			},
		}
		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(store).
			WithStatusSubresource(&api.SecretStore{}).
			Build()

		r := &SecretStoreReconciler{
			CommonReconciler:     &CommonReconciler{Client: cl},
			Log:                  logr.Discard(),
			ReconciliationPeriod: time.Minute,
		}

		res, err := r.Reconcile(context.Background(), reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: "default", Name: "store"},
		})
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if res != (reconcile.Result{}) {
			t.Errorf("Reconcile() result = %+v, want empty on successful recreation", res)
		}

		got := &api.SecretStore{}
		if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, got); err != nil {
			t.Fatalf("failed to get store: %v", err)
		}
		if got.Status.ClientGeneration != 1 {
			t.Errorf("persisted status.clientGeneration = %d, want 1 after spec-change-driven recreation", got.Status.ClientGeneration)
		}
		if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].ObservedGeneration != 2 {
			t.Errorf("persisted conditions = %+v, want ObservedGeneration 2", got.Status.Conditions)
		}
	})

	t.Run("ClusterSecretStore spec change without annotation bumps", func(t *testing.T) {
		defer registerReadyStubProviders(t)()

		scheme := testutil.NewTestScheme(t)
		store := &api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "cluster-store",
				Generation: 2,
				Finalizers: []string{clusterSecretFinalizer},
			},
			Spec: api.ClusterSecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
			Status: api.ClusterSecretStoreStatus{
				Conditions: []api.SecretStoreStatusCondition{{
					Type:               api.SecretStoreReady,
					Status:             corev1.ConditionTrue,
					Reason:             api.ReasonStoreValid,
					ObservedGeneration: 1,
				}},
			},
		}
		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(store).
			WithStatusSubresource(&api.ClusterSecretStore{}).
			Build()

		r := &ClusterSecretStoreReconciler{
			CommonReconciler:     &CommonReconciler{Client: cl},
			Log:                  logr.Discard(),
			ReconciliationPeriod: time.Minute,
		}

		if _, err := r.Reconcile(context.Background(), reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "cluster-store"},
		}); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}

		got := &api.ClusterSecretStore{}
		if err := cl.Get(context.Background(), client.ObjectKey{Name: "cluster-store"}, got); err != nil {
			t.Fatalf("failed to get cluster store: %v", err)
		}
		if got.Status.ClientGeneration != 1 {
			t.Errorf("persisted status.clientGeneration = %d, want 1 after spec-change-driven recreation", got.Status.ClientGeneration)
		}
	})
}

// TestReconcileBumpsClientGenerationOnAnnotationPath proves the
// trigger-annotation path (metadata.generation unchanged) also persists a
// clientGeneration bump — the exact gap the former channel-free status
// comparison would have silently swallowed. The annotation must also be
// cleared after processing.
func TestReconcileBumpsClientGenerationOnAnnotationPath(t *testing.T) {
	t.Run("SecretStore annotation path bumps", func(t *testing.T) {
		defer registerReadyStubProviders(t)()

		scheme := testutil.NewTestScheme(t)
		// Steady-state generation, but the trigger annotation forces
		// recreation and must also produce a persisted bump.
		store := &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "store",
				Namespace:  "default",
				Generation: 1,
				Finalizers: []string{secretFinalizer},
				Annotations: map[string]string{
					TriggerReconcileAnnotation: "2026-01-01T00:00:00Z",
				},
			},
			Spec: api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
			Status: api.SecretStoreStatus{
				Conditions: []api.SecretStoreStatusCondition{{
					Type:               api.SecretStoreReady,
					Status:             corev1.ConditionTrue,
					Reason:             api.ReasonStoreValid,
					ObservedGeneration: 1,
				}},
			},
		}
		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(store).
			WithStatusSubresource(&api.SecretStore{}).
			Build()

		r := &SecretStoreReconciler{
			CommonReconciler:     &CommonReconciler{Client: cl},
			Log:                  logr.Discard(),
			ReconciliationPeriod: time.Minute,
		}

		if _, err := r.Reconcile(context.Background(), reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: "default", Name: "store"},
		}); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}

		got := &api.SecretStore{}
		if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, got); err != nil {
			t.Fatalf("failed to get store: %v", err)
		}
		if got.Status.ClientGeneration != 1 {
			t.Errorf("persisted status.clientGeneration = %d, want 1 on the annotation path", got.Status.ClientGeneration)
		}
		if _, stillSet := got.Annotations[TriggerReconcileAnnotation]; stillSet {
			t.Errorf("trigger annotation still present after processing: %v", got.Annotations)
		}
	})

	t.Run("ClusterSecretStore annotation path bumps", func(t *testing.T) {
		defer registerReadyStubProviders(t)()

		scheme := testutil.NewTestScheme(t)
		// Steady-state generation (Generation == ObservedGeneration), but the
		// trigger annotation forces recreation and must also persist a bump —
		// symmetric to the SecretStore annotation path.
		store := &api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "cluster-store",
				Generation: 1,
				Finalizers: []string{clusterSecretFinalizer},
				Annotations: map[string]string{
					TriggerReconcileAnnotation: "2026-01-01T00:00:00Z",
				},
			},
			Spec: api.ClusterSecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
			Status: api.ClusterSecretStoreStatus{
				Conditions: []api.SecretStoreStatusCondition{{
					Type:               api.SecretStoreReady,
					Status:             corev1.ConditionTrue,
					Reason:             api.ReasonStoreValid,
					ObservedGeneration: 1,
				}},
			},
		}
		cl := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(store).
			WithStatusSubresource(&api.ClusterSecretStore{}).
			Build()

		r := &ClusterSecretStoreReconciler{
			CommonReconciler:     &CommonReconciler{Client: cl},
			Log:                  logr.Discard(),
			ReconciliationPeriod: time.Minute,
		}

		if _, err := r.Reconcile(context.Background(), reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "cluster-store"},
		}); err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}

		got := &api.ClusterSecretStore{}
		if err := cl.Get(context.Background(), client.ObjectKey{Name: "cluster-store"}, got); err != nil {
			t.Fatalf("failed to get cluster store: %v", err)
		}
		if got.Status.ClientGeneration != 1 {
			t.Errorf("persisted status.clientGeneration = %d, want 1 on the annotation path", got.Status.ClientGeneration)
		}
	})
}

// TestReconcileStatusUpdateFailureRetryBumps verifies the clientGeneration
// contract around a failed status write:
//  1. When the post-recreation status update fails, that reconcile round
//     returns an error and persists NO bump (the clientGeneration must only
//     advance together with a successful status write).
//  2. On the retry round after the injected error is lifted, recreation
//     happens again (ObservedGeneration was never persisted, so
//     needRecreateClient is still true) and exactly one bump is persisted —
//     no double increment from the failed round.
func TestReconcileStatusUpdateFailureRetryBumps(t *testing.T) {
	defer registerReadyStubProviders(t)()

	scheme := testutil.NewTestScheme(t)
	// Generation 2 vs ObservedGeneration 1 => spec change drives recreation.
	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "store",
			Namespace:  "default",
			Generation: 2,
			Finalizers: []string{secretFinalizer},
		},
		Spec: api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
		Status: api.SecretStoreStatus{
			Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				Reason:             api.ReasonStoreValid,
				ObservedGeneration: 1,
			}},
		},
	}

	var failStatus atomic.Bool
	failStatus.Store(true)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&api.SecretStore{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, clnt client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" && failStatus.Load() {
					return fmt.Errorf("simulated status update failure")
				}
				return clnt.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()

	r := &SecretStoreReconciler{
		CommonReconciler:     &CommonReconciler{Client: cl},
		Log:                  logr.Discard(),
		ReconciliationPeriod: time.Minute,
	}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "store"}}

	// Round 1: status update fails => error is surfaced and no bump is
	// persisted for this round.
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("Reconcile() error = nil, want the injected status update failure")
	}

	gotStore := &api.SecretStore{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, gotStore); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if gotStore.Status.ClientGeneration != 0 {
		t.Errorf("persisted status.clientGeneration = %d, want 0 after the failed status write", gotStore.Status.ClientGeneration)
	}
	if len(gotStore.Status.Conditions) == 0 || gotStore.Status.Conditions[0].ObservedGeneration != 1 {
		t.Fatalf("stored conditions = %+v, want ObservedGeneration still 1 after the failed write",
			gotStore.Status.Conditions)
	}

	// Round 2: injected error lifted. ObservedGeneration is still behind, so
	// needRecreateClient remains true; the rebuilt client must be followed by
	// exactly one persisted bump.
	failStatus.Store(false)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile() retry error = %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, gotStore); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if gotStore.Status.ClientGeneration != 1 {
		t.Errorf("persisted status.clientGeneration = %d, want exactly 1 on the retry round (no double bump from the failed round)",
			gotStore.Status.ClientGeneration)
	}
	if len(gotStore.Status.Conditions) == 0 || gotStore.Status.Conditions[0].ObservedGeneration != 2 {
		t.Errorf("stored conditions = %+v, want ObservedGeneration 2 after the retry round", gotStore.Status.Conditions)
	}
}

// TestReconcileSteadyStateDoesNotBumpClientGeneration guards against
// self-trigger storms: a reconcile round without client recreation must not
// bump clientGeneration nor rewrite the status.
func TestReconcileSteadyStateDoesNotBumpClientGeneration(t *testing.T) {
	defer registerReadyStubProviders(t)()

	scheme := testutil.NewTestScheme(t)
	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "store",
			Namespace:  "default",
			Generation: 1,
			Finalizers: []string{secretFinalizer},
		},
		Spec: api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
		Status: api.SecretStoreStatus{
			Capabilities:     api.SecretStoreReadOnly,
			ClientGeneration: 3,
			Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				Reason:             api.ReasonStoreValid,
				ObservedGeneration: 1,
			}},
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&api.SecretStore{}).
		Build()

	r := &SecretStoreReconciler{
		CommonReconciler:     &CommonReconciler{Client: cl},
		Log:                  logr.Discard(),
		ReconciliationPeriod: time.Minute,
	}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "default", Name: "store"},
	}); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	got := &api.SecretStore{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, got); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if got.Status.ClientGeneration != 3 {
		t.Errorf("persisted status.clientGeneration = %d, want unchanged 3 on a steady-state round", got.Status.ClientGeneration)
	}
}

// TestReconcileAnnotationPathStatusFailureRetryEventuallyBumps pins the F1
// ordering contract on the trigger-annotation path: the status write (with
// the clientGeneration bump) is persisted BEFORE the trigger annotation is
// cleared. If the status write fails, the annotation must still be present
// so the retry round re-detects the pending trigger and eventually persists
// the bump — under the old clear-first ordering the retry round saw a
// cleared annotation with an unchanged generation, never re-created the
// client, and the clientGeneration signal was lost forever (idle
// ExternalSecrets under --disable-polling never synced new credentials).
func TestReconcileAnnotationPathStatusFailureRetryEventuallyBumps(t *testing.T) {
	defer registerReadyStubProviders(t)()

	scheme := testutil.NewTestScheme(t)
	// Steady-state generation; the trigger annotation alone forces recreation.
	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "store",
			Namespace:  "default",
			Generation: 1,
			Finalizers: []string{secretFinalizer},
			Annotations: map[string]string{
				TriggerReconcileAnnotation: "2026-01-01T00:00:00Z",
			},
		},
		Spec: api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
		Status: api.SecretStoreStatus{
			Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				Reason:             api.ReasonStoreValid,
				ObservedGeneration: 1,
			}},
		},
	}

	var failStatus atomic.Bool
	failStatus.Store(true)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&api.SecretStore{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, clnt client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if subResourceName == "status" && failStatus.Load() {
					return fmt.Errorf("simulated status update failure (e.g. 409 conflict from informer lag)")
				}
				return clnt.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()

	r := &SecretStoreReconciler{
		CommonReconciler:     &CommonReconciler{Client: cl},
		Log:                  logr.Discard(),
		ReconciliationPeriod: time.Minute,
	}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "store"}}

	// Round 1: status write fails => error surfaced, no bump persisted, and
	// critically the trigger annotation must STILL be present.
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("Reconcile() error = nil, want the injected status update failure")
	}
	gotStore := &api.SecretStore{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, gotStore); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if gotStore.Status.ClientGeneration != 0 {
		t.Errorf("persisted status.clientGeneration = %d, want 0 after the failed status write", gotStore.Status.ClientGeneration)
	}
	if gotStore.Annotations[TriggerReconcileAnnotation] == "" {
		t.Fatal("trigger annotation cleared despite the failed status write; the retry round would lose the clientGeneration signal")
	}

	// Round 2: error lifted. The still-present annotation keeps the trigger
	// alive, recreation runs again, and exactly one bump is persisted.
	failStatus.Store(false)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile() retry error = %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, gotStore); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if gotStore.Status.ClientGeneration != 1 {
		t.Errorf("persisted status.clientGeneration = %d, want 1 after the retry round", gotStore.Status.ClientGeneration)
	}
	if _, stillSet := gotStore.Annotations[TriggerReconcileAnnotation]; stillSet {
		t.Errorf("trigger annotation still present after successful retry: %v", gotStore.Annotations)
	}
}

// TestReconcileAnnotationClearFailureReturnsErrorAndRetries covers the
// second half of the F1 contract: when the trigger-annotation clearing
// patch fails AFTER a successful status write, Reconcile returns the error
// (no silent swallow). The retry round sees the annotation, rebuilds
// idempotently, re-persists status+bump and retries the clear — the trigger
// is never dropped.
func TestReconcileAnnotationClearFailureReturnsErrorAndRetries(t *testing.T) {
	defer registerReadyStubProviders(t)()

	scheme := testutil.NewTestScheme(t)
	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "store",
			Namespace:  "default",
			Generation: 1,
			Finalizers: []string{secretFinalizer},
			Annotations: map[string]string{
				TriggerReconcileAnnotation: "2026-01-01T00:00:00Z",
			},
		},
		Spec: api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
		Status: api.SecretStoreStatus{
			Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				Reason:             api.ReasonStoreValid,
				ObservedGeneration: 1,
			}},
		},
	}

	var failPatch atomic.Bool
	failPatch.Store(true)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&api.SecretStore{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, clnt client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if failPatch.Load() {
					return fmt.Errorf("simulated trigger annotation patch failure")
				}
				return clnt.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	r := &SecretStoreReconciler{
		CommonReconciler:     &CommonReconciler{Client: cl},
		Log:                  logr.Discard(),
		ReconciliationPeriod: time.Minute,
	}
	req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "store"}}

	// Round 1: status write succeeds (bump persisted), annotation clearing
	// fails => the error must be returned so the workqueue retries.
	if _, err := r.Reconcile(context.Background(), req); err == nil {
		t.Fatal("Reconcile() error = nil, want the annotation-clearing failure to be surfaced")
	}
	gotStore := &api.SecretStore{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, gotStore); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if gotStore.Status.ClientGeneration != 1 {
		t.Errorf("persisted status.clientGeneration = %d, want 1 (status was written before the annotation clear)", gotStore.Status.ClientGeneration)
	}
	if gotStore.Annotations[TriggerReconcileAnnotation] == "" {
		t.Fatal("trigger annotation must remain until the clearing patch succeeds")
	}

	// Round 2: patch works. The annotation keeps needRecreateClient true,
	// the rebuild is idempotent, status+bump is re-persisted and the
	// annotation is finally cleared.
	failPatch.Store(false)
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile() retry error = %v", err)
	}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, gotStore); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if gotStore.Status.ClientGeneration != 2 {
		t.Errorf("persisted status.clientGeneration = %d, want 2 (idempotent retry round re-persists status+bump)", gotStore.Status.ClientGeneration)
	}
	if _, stillSet := gotStore.Annotations[TriggerReconcileAnnotation]; stillSet {
		t.Errorf("trigger annotation still present after successful retry: %v", gotStore.Annotations)
	}
}

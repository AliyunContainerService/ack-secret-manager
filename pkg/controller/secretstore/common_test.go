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

package secretstore

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add api to scheme: %v", err)
	}
	return scheme
}

// TestSetConditionPreservesTransitionTime verifies that re-setting a condition
// whose Type/Status/Reason/Message are all unchanged keeps the original
// LastTransitionTime instead of refreshing it every reconcile round.
func TestSetConditionPreservesTransitionTime(t *testing.T) {
	r := &CommonReconciler{}
	oldTime := metav1.NewTime(time.Now().Add(-24 * time.Hour))

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 1},
		Status: api.SecretStoreStatus{
			Conditions: []api.SecretStoreStatusCondition{
				{
					Type:               api.SecretStoreReady,
					Status:             corev1.ConditionTrue,
					Reason:             api.ReasonStoreValid,
					Message:            "ok",
					LastTransitionTime: oldTime,
					ObservedGeneration: 1,
				},
			},
		},
	}
	wrapper := &SecretStoreWrapper{store}

	// Bump generation to prove that ObservedGeneration is refreshed while the
	// transition timestamp stays untouched.
	store.Generation = 2

	r.setCondition(wrapper, api.SecretStoreStatusCondition{
		Type:    api.SecretStoreReady,
		Status:  corev1.ConditionTrue,
		Reason:  api.ReasonStoreValid,
		Message: "ok",
	})

	cond := store.Status.Conditions[0]
	if !cond.LastTransitionTime.Equal(&oldTime) {
		t.Errorf("LastTransitionTime = %v, want preserved %v", cond.LastTransitionTime, oldTime)
	}
	if cond.ObservedGeneration != 2 {
		t.Errorf("ObservedGeneration = %d, want 2", cond.ObservedGeneration)
	}
}

// TestSetConditionRefreshesOnTransition verifies that LastTransitionTime is
// refreshed when the condition actually transitions.
func TestSetConditionRefreshesOnTransition(t *testing.T) {
	tests := []struct {
		name string
		new  api.SecretStoreStatusCondition
	}{
		{
			name: "status changed",
			new: api.SecretStoreStatusCondition{
				Type:    api.SecretStoreReady,
				Status:  corev1.ConditionFalse,
				Reason:  api.ReasonStoreValid,
				Message: "ok",
			},
		},
		{
			name: "reason changed",
			new: api.SecretStoreStatusCondition{
				Type:    api.SecretStoreReady,
				Status:  corev1.ConditionTrue,
				Reason:  api.ReasonClientCreationFailed,
				Message: "ok",
			},
		},
		{
			name: "message changed",
			new: api.SecretStoreStatusCondition{
				Type:    api.SecretStoreReady,
				Status:  corev1.ConditionTrue,
				Reason:  api.ReasonStoreValid,
				Message: "something else",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &CommonReconciler{}
			oldTime := metav1.NewTime(time.Now().Add(-24 * time.Hour))

			store := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 1},
				Status: api.SecretStoreStatus{
					Conditions: []api.SecretStoreStatusCondition{
						{
							Type:               api.SecretStoreReady,
							Status:             corev1.ConditionTrue,
							Reason:             api.ReasonStoreValid,
							Message:            "ok",
							LastTransitionTime: oldTime,
							ObservedGeneration: 1,
						},
					},
				},
			}

			r.setCondition(&SecretStoreWrapper{store}, tt.new)

			cond := store.Status.Conditions[0]
			if cond.LastTransitionTime.Equal(&oldTime) {
				t.Errorf("LastTransitionTime should be refreshed on transition, still %v", oldTime)
			}
			if cond.LastTransitionTime.Time.Before(oldTime.Time) {
				t.Errorf("LastTransitionTime = %v, want after %v", cond.LastTransitionTime, oldTime)
			}
		})
	}

	// A brand new condition must also carry a fresh transition time.
	t.Run("new condition gets current time", func(t *testing.T) {
		r := &CommonReconciler{}
		store := &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 1},
		}
		before := time.Now().Add(-time.Second)

		r.setCondition(&SecretStoreWrapper{store}, api.SecretStoreStatusCondition{
			Type:   api.SecretStoreReady,
			Status: corev1.ConditionTrue,
		})

		if len(store.Status.Conditions) != 1 {
			t.Fatalf("expected 1 condition, got %d", len(store.Status.Conditions))
		}
		if store.Status.Conditions[0].LastTransitionTime.Time.Before(before) {
			t.Errorf("new condition LastTransitionTime = %v, want >= %v",
				store.Status.Conditions[0].LastTransitionTime, before)
		}
	})
}

// TestStatusEqualIgnoresTransitionTime verifies statusEqual treats statuses
// differing only in LastTransitionTime as equal, while still detecting real
// semantic changes.
func TestStatusEqualIgnoresTransitionTime(t *testing.T) {
	r := &CommonReconciler{}
	t1 := metav1.NewTime(time.Now().Add(-24 * time.Hour))
	t2 := metav1.Now()

	base := func(ts metav1.Time) api.SecretStoreStatus {
		return api.SecretStoreStatus{
			Capabilities: api.SecretStoreReadOnly,
			Conditions: []api.SecretStoreStatusCondition{
				{
					Type:               api.SecretStoreReady,
					Status:             corev1.ConditionTrue,
					Reason:             api.ReasonStoreValid,
					LastTransitionTime: ts,
					ObservedGeneration: 1,
				},
			},
		}
	}
	wrap := func(s api.SecretStoreStatus) StoreStatusInterface {
		return &SecretStoreStatusWrapper{SecretStoreStatus: &s}
	}

	tests := []struct {
		name string
		old  api.SecretStoreStatus
		new  api.SecretStoreStatus
		want bool
	}{
		{
			name: "only timestamp differs",
			old:  base(t1),
			new:  base(t2),
			want: true,
		},
		{
			name: "identical statuses",
			old:  base(t1),
			new:  base(t1),
			want: true,
		},
		{
			name: "condition status differs",
			old:  base(t1),
			new: func() api.SecretStoreStatus {
				s := base(t1)
				s.Conditions[0].Status = corev1.ConditionFalse
				return s
			}(),
			want: false,
		},
		{
			name: "observed generation differs",
			old:  base(t1),
			new: func() api.SecretStoreStatus {
				s := base(t1)
				s.Conditions[0].ObservedGeneration = 2
				return s
			}(),
			want: false,
		},
		{
			name: "capabilities differ",
			old:  base(t1),
			new: func() api.SecretStoreStatus {
				s := base(t1)
				s.Capabilities = api.SecretStoreReadWrite
				return s
			}(),
			want: false,
		},
		{
			name: "condition count differs",
			old:  base(t1),
			new: func() api.SecretStoreStatus {
				s := base(t1)
				s.Conditions = append(s.Conditions, s.Conditions[0])
				return s
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.statusEqual(wrap(tt.old), wrap(tt.new)); got != tt.want {
				t.Errorf("statusEqual() = %v, want %v", got, tt.want)
			}
		})
	}

	// Same semantics must hold for the ClusterSecretStore wrapper.
	t.Run("cluster store wrapper ignores timestamp", func(t *testing.T) {
		oldS := &ClusterSecretStoreStatusWrapper{ClusterSecretStoreStatus: &api.ClusterSecretStoreStatus{
			Capabilities: api.SecretStoreReadOnly,
			Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: t1,
				ObservedGeneration: 1,
			}},
		}}
		newS := &ClusterSecretStoreStatusWrapper{ClusterSecretStoreStatus: &api.ClusterSecretStoreStatus{
			Capabilities: api.SecretStoreReadOnly,
			Conditions: []api.SecretStoreStatusCondition{{
				Type:               api.SecretStoreReady,
				Status:             corev1.ConditionTrue,
				LastTransitionTime: t2,
				ObservedGeneration: 1,
			}},
		}}
		if !r.statusEqual(oldS, newS) {
			t.Errorf("statusEqual() = false, want true for timestamp-only diff")
		}
	})
}

// TestUpdateStatusUnchangedNoErrorNoWrite verifies that updateStatus reports
// (false, nil) — not an error — when the status is already up-to-date, and
// performs no write against the API server.
func TestUpdateStatusUnchangedNoErrorNoWrite(t *testing.T) {
	scheme := newTestScheme(t)
	// Truncate to seconds to match API server timestamp serialization precision.
	oldTime := metav1.NewTime(time.Now().Add(-24 * time.Hour).Truncate(time.Second))

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 1},
		Status: api.SecretStoreStatus{
			Capabilities: api.SecretStoreReadOnly,
			Conditions: []api.SecretStoreStatusCondition{
				{
					Type:               api.SecretStoreReady,
					Status:             corev1.ConditionTrue,
					Reason:             api.ReasonStoreValid,
					LastTransitionTime: oldTime,
					ObservedGeneration: 1,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&api.SecretStore{}).
		Build()

	r := &CommonReconciler{Client: cl}
	updated, err := r.updateStatusWithReady(context.Background(), logr.Discard(), &SecretStoreWrapper{store})
	if err != nil {
		t.Fatalf("updateStatusWithReady() error = %v, want nil for unchanged status", err)
	}
	if updated {
		t.Errorf("updateStatusWithReady() updated = true, want false for unchanged status")
	}

	// The stored object must be untouched, including the old transition time.
	got := &api.SecretStore{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, got); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if !got.Status.Conditions[0].LastTransitionTime.Equal(&oldTime) {
		t.Errorf("stored LastTransitionTime = %v, want untouched %v",
			got.Status.Conditions[0].LastTransitionTime, oldTime)
	}
}

// TestUpdateStatusWritesOnTransition verifies that a real transition is
// written and reported as (true, nil).
func TestUpdateStatusWritesOnTransition(t *testing.T) {
	scheme := newTestScheme(t)
	oldTime := metav1.NewTime(time.Now().Add(-24 * time.Hour))

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 1},
		Status: api.SecretStoreStatus{
			Conditions: []api.SecretStoreStatusCondition{
				{
					Type:               api.SecretStoreReady,
					Status:             corev1.ConditionFalse,
					Reason:             api.ReasonClientCreationFailed,
					Message:            "boom",
					LastTransitionTime: oldTime,
					ObservedGeneration: 1,
				},
			},
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&api.SecretStore{}).
		Build()

	r := &CommonReconciler{Client: cl}
	updated, err := r.updateStatusWithReady(context.Background(), logr.Discard(), &SecretStoreWrapper{store})
	if err != nil {
		t.Fatalf("updateStatusWithReady() error = %v", err)
	}
	if !updated {
		t.Errorf("updateStatusWithReady() updated = false, want true for transitioning status")
	}

	got := &api.SecretStore{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, got); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	cond := got.Status.Conditions[0]
	if cond.Status != corev1.ConditionTrue || cond.Reason != api.ReasonStoreValid {
		t.Errorf("stored condition = %+v, want Ready=True/Valid", cond)
	}
	if cond.LastTransitionTime.Equal(&oldTime) {
		t.Errorf("stored LastTransitionTime should be refreshed on transition, still %v", oldTime)
	}
}

// TestUpdateStatusInitializesEmptyStatus verifies that a store without any
// conditions gets its status written on the first reconcile.
func TestUpdateStatusInitializesEmptyStatus(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 1},
	}

	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(store).
		WithStatusSubresource(&api.SecretStore{}).
		Build()

	r := &CommonReconciler{Client: cl}
	updated, err := r.updateStatusWithReady(context.Background(), logr.Discard(), &SecretStoreWrapper{store})
	if err != nil {
		t.Fatalf("updateStatusWithReady() error = %v", err)
	}
	if !updated {
		t.Errorf("updateStatusWithReady() updated = false, want true for empty status")
	}

	got := &api.SecretStore{}
	if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, got); err != nil {
		t.Fatalf("failed to get store: %v", err)
	}
	if len(got.Status.Conditions) != 1 || got.Status.Conditions[0].Status != corev1.ConditionTrue {
		t.Errorf("stored conditions = %+v, want one Ready=True condition", got.Status.Conditions)
	}
}

// TestValidateStoreSpecProviderCounting verifies that validateStoreSpec counts
// providers by their inner auth fields, so an empty provider block (`kms: {}`)
// does not count as a configured provider.
func TestValidateStoreSpecProviderCounting(t *testing.T) {
	r := &CommonReconciler{}

	tests := []struct {
		name    string
		kms     *api.KMSProvider
		oos     *api.OOSProvider
		wantErr bool
	}{
		{
			name: "valid KMS only",
			kms:  &api.KMSProvider{KMS: &api.KMSAuth{}},
		},
		{
			name: "valid OOS only",
			oos:  &api.OOSProvider{OOS: &api.OOSAuth{}},
		},
		{
			name:    "no provider",
			wantErr: true,
		},
		{
			name:    "empty KMS block only",
			kms:     &api.KMSProvider{},
			wantErr: true,
		},
		{
			name:    "empty OOS block only",
			oos:     &api.OOSProvider{},
			wantErr: true,
		},
		{
			name: "empty KMS block plus valid OOS passes",
			kms:  &api.KMSProvider{},
			oos:  &api.OOSProvider{OOS: &api.OOSAuth{}},
		},
		{
			name:    "both providers configured",
			kms:     &api.KMSProvider{KMS: &api.KMSAuth{}},
			oos:     &api.OOSProvider{OOS: &api.OOSAuth{}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.validateStoreSpec(tt.kms, tt.oos, "SecretStore")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStoreSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// branchStubProvider is a minimal backend.Provider stub that records whether
// its NewClient was invoked, so tests can observe which provider branch
// recreateClient entered without touching real cloud clients.
type branchStubProvider struct {
	name            string
	newClientCalled bool
}

func (p *branchStubProvider) Register(clientKey string, secretClient backend.SecretClient) {
}

func (p *branchStubProvider) GetClient(clientKey string) (backend.SecretClient, error) {
	return nil, nil
}

func (p *branchStubProvider) Delete(clientKey string) {
}

func (p *branchStubProvider) DeletePrefixed(clientKey string) {
}

func (p *branchStubProvider) NewClient(ctx context.Context, store *api.SecretStore, kube client.Client, endpoint string) (backend.SecretClient, error) {
	p.newClientCalled = true
	return nil, fmt.Errorf("stub %s provider entered", p.name)
}

func (p *branchStubProvider) NewClientByENV(endpoint string) (backend.SecretClient, error) {
	return nil, nil
}

func (p *branchStubProvider) GetName() string      { return p.name }
func (p *branchStubProvider) GetRegion() string    { return "" }
func (p *branchStubProvider) GetEndpoint() string  { return "" }
func (p *branchStubProvider) GetClusterId() string { return "" }
func (p *branchStubProvider) GetUid() string       { return "" }

// TestRecreateClientBranchAlignsWithValidation verifies that the provider
// branch selection in recreateClient uses the same inner-field criterion as
// validateStoreSpec: a `kms: {}` block alongside a valid OOS config must
// enter the OOS branch instead of failing on the KMS branch (and vice versa).
func TestRecreateClientBranchAlignsWithValidation(t *testing.T) {
	tests := []struct {
		name    string
		kmsSpec *api.KMSProvider
		oosSpec *api.OOSProvider
		wantKMS bool
	}{
		{
			name:    "empty kms block plus valid oos enters OOS branch",
			kmsSpec: &api.KMSProvider{}, // empty block: outer pointer non-nil, inner field nil
			oosSpec: &api.OOSProvider{OOS: &api.OOSAuth{}},
			wantKMS: false,
		},
		{
			name:    "valid kms plus empty oos block enters KMS branch",
			kmsSpec: &api.KMSProvider{KMS: &api.KMSAuth{}},
			oosSpec: &api.OOSProvider{}, // empty block: outer pointer non-nil, inner field nil
			wantKMS: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 1},
				Spec:       api.SecretStoreSpec{KMS: tt.kmsSpec, OOS: tt.oosSpec},
			}
			wrapper := &SecretStoreWrapper{store}

			r := &CommonReconciler{}

			// The config must pass validation, which proves exactly one
			// provider is configured by the inner-field criterion.
			if err := r.validateSecretStoreSpec(wrapper); err != nil {
				t.Fatalf("validateSecretStoreSpec() error = %v, want nil", err)
			}

			kmsStub := &branchStubProvider{name: "kms"}
			oosStub := &branchStubProvider{name: "oos"}

			// The stubs fail NewClient on purpose; only the branch choice matters.
			if err := r.recreateClient(context.Background(), logr.Discard(), "namespace/default/store", kmsStub, oosStub, wrapper); err == nil {
				t.Fatalf("recreateClient() error = nil, want stub provider error")
			}

			if got := kmsStub.newClientCalled; got != tt.wantKMS {
				t.Errorf("KMS branch entered = %v, want %v", got, tt.wantKMS)
			}
			if got := oosStub.newClientCalled; got == tt.wantKMS {
				t.Errorf("OOS branch entered = %v, want %v", got, !tt.wantKMS)
			}
		})
	}
}

// TestRecreateClientPrefixedCleanup verifies that recreateClient retires the
// full client family of the store — the plain clientName client plus every
// composite "clientName#endpoint" variant — via DeletePrefixed before the
// bare client is rebuilt. Composite variants are re-created on demand by the
// ExternalSecret controller with the refreshed store credentials.
func TestRecreateClientPrefixedCleanup(t *testing.T) {
	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 2},
		Spec:       api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
	}
	wrapper := &SecretStoreWrapper{store}
	r := &CommonReconciler{}
	kmsStub := &registryStubProvider{name: "kms"}
	oosStub := &registryStubProvider{name: "oos"}

	// The stub provider's NewClient returns a nil client, so client creation
	// fails on purpose; only the cleanup behavior before it matters here.
	if err := r.recreateClient(context.Background(), logr.Discard(), "namespace/default/store", kmsStub, oosStub, wrapper); err == nil {
		t.Fatalf("recreateClient() error = nil, want stub provider error")
	}

	for name, stub := range map[string]*registryStubProvider{"kms": kmsStub, "oos": oosStub} {
		if len(stub.deletedPrefixed) != 1 || stub.deletedPrefixed[0] != "namespace/default/store" {
			t.Errorf("%s prefixed deletions = %v, want [namespace/default/store]", name, stub.deletedPrefixed)
		}
		if len(stub.deletedKeys) != 0 {
			t.Errorf("%s bare Delete must not be used on the recreate path: %v", name, stub.deletedKeys)
		}
	}
}

// stubSecretClient satisfies backend.SecretClient so provider stubs can
// report an "existing" client without touching real cloud services.
type stubSecretClient struct{}

func (c *stubSecretClient) GetName() string { return "stub-client" }

func (c *stubSecretClient) GetExternalSecret(ctx context.Context, data *api.DataSource, kube client.Client) (map[string][]byte, error) {
	return nil, nil
}

func (c *stubSecretClient) GetExternalSecretWithExtract(ctx context.Context, data *api.DataProcess, kube client.Client) (map[string][]byte, error) {
	return nil, nil
}

// registryStubProvider is a backend.Provider stub with controllable client
// presence and recorded Delete/DeletePrefixed calls, for
// needRecreateClient/recreateClient/handleDeletion.
type registryStubProvider struct {
	name            string
	clientPresent   bool
	deletedKeys     []string
	deletedPrefixed []string
}

func (p *registryStubProvider) Register(clientKey string, secretClient backend.SecretClient) {
}

func (p *registryStubProvider) GetClient(clientKey string) (backend.SecretClient, error) {
	if p.clientPresent {
		return &stubSecretClient{}, nil
	}
	return nil, nil
}

func (p *registryStubProvider) Delete(clientKey string) {
	p.deletedKeys = append(p.deletedKeys, clientKey)
}

func (p *registryStubProvider) DeletePrefixed(clientKey string) {
	p.deletedPrefixed = append(p.deletedPrefixed, clientKey)
}

func (p *registryStubProvider) NewClient(ctx context.Context, store *api.SecretStore, kube client.Client, endpoint string) (backend.SecretClient, error) {
	return nil, nil
}

func (p *registryStubProvider) NewClientByENV(endpoint string) (backend.SecretClient, error) {
	return nil, nil
}

func (p *registryStubProvider) GetName() string      { return p.name }
func (p *registryStubProvider) GetRegion() string    { return "" }
func (p *registryStubProvider) GetEndpoint() string  { return "" }
func (p *registryStubProvider) GetClusterId() string { return "" }
func (p *registryStubProvider) GetUid() string       { return "" }

// TestNeedRecreateClient covers all decision branches: missing client,
// generation change, initial reconcile without conditions, and the steady
// state where no recreation is needed.
func TestNeedRecreateClient(t *testing.T) {
	r := &CommonReconciler{}
	readyCond := func(gen int64) []api.SecretStoreStatusCondition {
		return []api.SecretStoreStatusCondition{{
			Type:               api.SecretStoreReady,
			Status:             corev1.ConditionTrue,
			ObservedGeneration: gen,
		}}
	}

	tests := []struct {
		name          string
		clientPresent bool
		useOOS        bool
		generation    int64
		conditions    []api.SecretStoreStatusCondition
		want          bool
	}{
		{
			name:          "client missing with kms provider",
			clientPresent: false,
			useOOS:        false,
			generation:    1,
			conditions:    readyCond(1),
			want:          true,
		},
		{
			name:          "client missing with oos provider",
			clientPresent: false,
			useOOS:        true,
			generation:    1,
			conditions:    readyCond(1),
			want:          true,
		},
		{
			name:          "generation changed forces recreation",
			clientPresent: true,
			useOOS:        false,
			generation:    2,
			conditions:    readyCond(1),
			want:          true,
		},
		{
			name:          "no conditions means initial reconcile",
			clientPresent: true,
			useOOS:        false,
			generation:    1,
			conditions:    nil,
			want:          true,
		},
		{
			name:          "client exists and generation unchanged",
			clientPresent: true,
			useOOS:        false,
			generation:    1,
			conditions:    readyCond(1),
			want:          false,
		},
		{
			name:          "oos client exists and generation unchanged",
			clientPresent: true,
			useOOS:        true,
			generation:    1,
			conditions:    readyCond(1),
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Only one provider is non-nil, matching how the controllers call
			// needRecreateClient for single-provider stores.
			var kmsArg, oosArg backend.Provider
			if tt.useOOS {
				oosArg = &registryStubProvider{name: "oos", clientPresent: tt.clientPresent}
			} else {
				kmsArg = &registryStubProvider{name: "kms", clientPresent: tt.clientPresent}
			}

			got := r.needRecreateClient("namespace/default/store", tt.generation, tt.conditions, kmsArg, oosArg)
			if got != tt.want {
				t.Errorf("needRecreateClient() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHandleDeletion verifies that the deletion path cleans up both provider
// clients and removes the finalizer via the provided update function.
func TestHandleDeletion(t *testing.T) {
	r := &CommonReconciler{}

	t.Run("finalizer removed and provider clients cleaned", func(t *testing.T) {
		store := &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Finalizers: []string{secretFinalizer}},
		}
		kmsStub := &registryStubProvider{name: "kms"}
		oosStub := &registryStubProvider{name: "oos"}

		var updated client.Object
		updateFunc := func(obj client.Object) error {
			updated = obj
			return nil
		}

		res, err := r.handleDeletion(logr.Discard(), store.Finalizers, store, "client-key", kmsStub, oosStub, updateFunc)
		if err != nil {
			t.Fatalf("handleDeletion() error = %v", err)
		}
		if res != (reconcile.Result{}) {
			t.Errorf("handleDeletion() result = %+v, want empty", res)
		}

		// Both providers must have their clients deleted (prefixed: plain key
		// plus every composite variant) before the finalizer is removed.
		if len(kmsStub.deletedPrefixed) != 1 || kmsStub.deletedPrefixed[0] != "client-key" {
			t.Errorf("kms prefixed deletions = %v, want [client-key]", kmsStub.deletedPrefixed)
		}
		if len(oosStub.deletedPrefixed) != 1 || oosStub.deletedPrefixed[0] != "client-key" {
			t.Errorf("oos prefixed deletions = %v, want [client-key]", oosStub.deletedPrefixed)
		}
		if len(kmsStub.deletedKeys) != 0 || len(oosStub.deletedKeys) != 0 {
			t.Errorf("bare Delete must not be used on the deletion path: kms=%v oos=%v", kmsStub.deletedKeys, oosStub.deletedKeys)
		}

		if updated == nil {
			t.Fatalf("updateFunc was not called")
		}
		if got := updated.GetFinalizers(); len(got) != 0 {
			t.Errorf("finalizers after update = %v, want empty", got)
		}
	})

	t.Run("cluster secret store finalizer removed", func(t *testing.T) {
		store := &api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-store", Finalizers: []string{clusterSecretFinalizer}},
		}
		kmsStub := &registryStubProvider{name: "kms"}
		oosStub := &registryStubProvider{name: "oos"}

		var updated client.Object
		updateFunc := func(obj client.Object) error {
			updated = obj
			return nil
		}

		if _, err := r.handleDeletion(logr.Discard(), store.Finalizers, store, "client-key", kmsStub, oosStub, updateFunc); err != nil {
			t.Fatalf("handleDeletion() error = %v", err)
		}
		if updated == nil {
			t.Fatalf("updateFunc was not called")
		}
		if got := updated.GetFinalizers(); len(got) != 0 {
			t.Errorf("finalizers after update = %v, want empty", got)
		}
	})

	t.Run("update failure is propagated", func(t *testing.T) {
		store := &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Finalizers: []string{secretFinalizer}},
		}
		kmsStub := &registryStubProvider{name: "kms"}
		oosStub := &registryStubProvider{name: "oos"}

		updateErr := fmt.Errorf("simulated update failure")
		_, err := r.handleDeletion(logr.Discard(), store.Finalizers, store, "client-key", kmsStub, oosStub, func(client.Object) error {
			return updateErr
		})
		if err != updateErr {
			t.Errorf("handleDeletion() error = %v, want %v", err, updateErr)
		}
	})

	t.Run("without finalizer clients are still cleaned but no update", func(t *testing.T) {
		store := &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default"},
		}
		kmsStub := &registryStubProvider{name: "kms"}
		oosStub := &registryStubProvider{name: "oos"}

		updateCalled := false
		res, err := r.handleDeletion(logr.Discard(), store.Finalizers, store, "client-key", kmsStub, oosStub, func(client.Object) error {
			updateCalled = true
			return nil
		})
		if err != nil {
			t.Fatalf("handleDeletion() error = %v", err)
		}
		if res != (reconcile.Result{}) {
			t.Errorf("handleDeletion() result = %+v, want empty", res)
		}
		if updateCalled {
			t.Errorf("updateFunc was called, want skipped when finalizer absent")
		}
		if len(kmsStub.deletedPrefixed) != 1 || len(oosStub.deletedPrefixed) != 1 {
			t.Errorf("client cleanup skipped: kms=%v oos=%v", kmsStub.deletedPrefixed, oosStub.deletedPrefixed)
		}
	})
}

// TestValidateSecretStoreSpecCrossNamespaceRestrictions covers the cross
// namespace guard in validateSecretStoreSpec for all three reference fields.
func TestValidateSecretStoreSpecCrossNamespaceRestrictions(t *testing.T) {
	kmsWithSARef := func(ns string) api.SecretStoreSpec {
		return api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{
			ServiceAccountRef: &api.ServiceAccountRef{Name: "sa", Namespace: ns},
		}}}
	}
	kmsWithAccessKey := func(ns string) api.SecretStoreSpec {
		return api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{
			AccessKey: &api.SecretRef{Name: "cred", Namespace: ns, Key: "accessKeyId"},
		}}}
	}
	kmsWithAccessKeySecret := func(ns string) api.SecretStoreSpec {
		return api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{
			AccessKeySecret: &api.SecretRef{Name: "cred", Namespace: ns, Key: "accessKeySecret"},
		}}}
	}

	tests := []struct {
		name       string
		enable     bool
		spec       api.SecretStoreSpec
		wantErr    bool
		errContent string
	}{
		{
			name:       "cross namespace serviceAccountRef disabled",
			enable:     false,
			spec:       kmsWithSARef("other"),
			wantErr:    true,
			errContent: "cross namespace ServiceAccountRef is disabled",
		},
		{
			name:       "cross namespace accessKey disabled",
			enable:     false,
			spec:       kmsWithAccessKey("other"),
			wantErr:    true,
			errContent: "cross namespace AccessKey is disabled",
		},
		{
			name:       "cross namespace accessKeySecret disabled",
			enable:     false,
			spec:       kmsWithAccessKeySecret("other"),
			wantErr:    true,
			errContent: "cross namespace AccessKeySecret is disabled",
		},
		{
			name:    "same namespace refs allowed when disabled",
			enable:  false,
			spec:    kmsWithSARef("default"),
			wantErr: false,
		},
		{
			name:    "omitted namespace allowed when disabled",
			enable:  false,
			spec:    kmsWithSARef(""),
			wantErr: false,
		},
		{
			name:    "cross namespace allowed when enabled",
			enable:  true,
			spec:    kmsWithSARef("other"),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &CommonReconciler{EnableCrossNamespaceAuthRef: tt.enable}
			store := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default"},
				Spec:       tt.spec,
			}
			err := r.validateSecretStoreSpec(&SecretStoreWrapper{store})
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateSecretStoreSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errContent) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.errContent)
			}
		})
	}
}

// TestValidateClusterSecretStoreSpec covers the mandatory namespace checks
// and the conditions selector/regex validation for ClusterSecretStore.
func TestValidateClusterSecretStoreSpec(t *testing.T) {
	clusterStore := func(spec api.ClusterSecretStoreSpec) *ClusterSecretStoreWrapper {
		return &ClusterSecretStoreWrapper{&api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-store"},
			Spec:       spec,
		}}
	}
	kmsSpec := func(mutate func(*api.KMSAuth)) api.ClusterSecretStoreSpec {
		auth := &api.KMSAuth{}
		if mutate != nil {
			mutate(auth)
		}
		return api.ClusterSecretStoreSpec{KMS: &api.KMSProvider{KMS: auth}}
	}

	tests := []struct {
		name       string
		spec       api.ClusterSecretStoreSpec
		wantErr    bool
		errContent string
	}{
		{
			name: "serviceAccountRef without namespace",
			spec: kmsSpec(func(a *api.KMSAuth) {
				a.ServiceAccountRef = &api.ServiceAccountRef{Name: "sa"}
			}),
			wantErr:    true,
			errContent: "ServiceAccountRef.Namespace is required",
		},
		{
			name: "accessKey without namespace",
			spec: kmsSpec(func(a *api.KMSAuth) {
				a.AccessKey = &api.SecretRef{Name: "cred", Key: "accessKeyId"}
			}),
			wantErr:    true,
			errContent: "AccessKey.Namespace is required",
		},
		{
			name: "accessKeySecret without namespace",
			spec: kmsSpec(func(a *api.KMSAuth) {
				a.AccessKeySecret = &api.SecretRef{Name: "cred", Key: "accessKeySecret"}
			}),
			wantErr:    true,
			errContent: "AccessKeySecret.Namespace is required",
		},
		{
			name: "all refs with namespace pass",
			spec: kmsSpec(func(a *api.KMSAuth) {
				a.ServiceAccountRef = &api.ServiceAccountRef{Name: "sa", Namespace: "ns1"}
				a.AccessKey = &api.SecretRef{Name: "cred", Namespace: "ns1", Key: "accessKeyId"}
				a.AccessKeySecret = &api.SecretRef{Name: "cred", Namespace: "ns1", Key: "accessKeySecret"}
			}),
			wantErr: false,
		},
		{
			name: "invalid namespace selector in condition",
			spec: func() api.ClusterSecretStoreSpec {
				spec := kmsSpec(nil)
				spec.Conditions = []api.ClusterSecretStoreCondition{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchExpressions: []metav1.LabelSelectorRequirement{{
							Key:      "env",
							Operator: "NotARealOperator",
							Values:   []string{"prod"},
						}},
					},
				}}
				return spec
			}(),
			wantErr:    true,
			errContent: "invalid label selector in condition 0",
		},
		{
			name: "valid namespace selector in condition",
			spec: func() api.ClusterSecretStoreSpec {
				spec := kmsSpec(nil)
				spec.Conditions = []api.ClusterSecretStoreCondition{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"env": "prod"},
					},
				}}
				return spec
			}(),
			wantErr: false,
		},
		{
			name: "invalid namespace regex in condition",
			spec: func() api.ClusterSecretStoreSpec {
				spec := kmsSpec(nil)
				spec.Conditions = []api.ClusterSecretStoreCondition{{
					NamespaceRegexes: []string{"(unclosed"},
				}}
				return spec
			}(),
			wantErr:    true,
			errContent: "invalid regex (unclosed in condition 0 regex 0",
		},
		{
			name: "valid namespace regexes in condition",
			spec: func() api.ClusterSecretStoreSpec {
				spec := kmsSpec(nil)
				spec.Conditions = []api.ClusterSecretStoreCondition{{
					NamespaceRegexes: []string{"^team-.*$", "^prod-[0-9]+$"},
				}}
				return spec
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &CommonReconciler{}
			err := r.validateClusterSecretStoreSpec(clusterStore(tt.spec))
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateClusterSecretStoreSpec() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errContent) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.errContent)
			}
		})
	}
}

// TestUpdateStatusConflictError verifies that a conflict on the status
// subresource update is returned to the caller (for workqueue backoff retry)
// and reported as not-updated, and that non-conflict errors behave the same.
func TestUpdateStatusConflictError(t *testing.T) {
	tests := []struct {
		name       string
		injectErr  error
		wantStatus bool
	}{
		{
			name: "conflict error returned",
			injectErr: errors.NewConflict(
				schema.GroupResource{Group: "alibabacloud.com", Resource: "secretstores"},
				"store",
				fmt.Errorf("simulated conflict"),
			),
			wantStatus: true,
		},
		{
			name:      "non-conflict error returned",
			injectErr: fmt.Errorf("simulated update failure"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			// Empty status forces shouldUpdate=true so the status write is attempted.
			store := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{Name: "store", Namespace: "default", Generation: 1},
			}

			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(store).
				WithStatusSubresource(&api.SecretStore{}).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourceUpdate: func(ctx context.Context, clnt client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
						if subResourceName == "status" {
							return tt.injectErr
						}
						return clnt.SubResource(subResourceName).Update(ctx, obj, opts...)
					},
				}).
				Build()

			r := &CommonReconciler{Client: cl}
			updated, err := r.updateStatusWithReady(context.Background(), logr.Discard(), &SecretStoreWrapper{store})
			if err == nil {
				t.Fatalf("updateStatusWithReady() error = nil, want injected error")
			}
			if updated {
				t.Errorf("updateStatusWithReady() updated = true, want false on failed write")
			}
			if got := errors.IsConflict(err); got != tt.wantStatus {
				t.Errorf("errors.IsConflict(err) = %v, want %v", got, tt.wantStatus)
			}

			// The stored status must remain untouched after the failed write.
			gotStore := &api.SecretStore{}
			if err := cl.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "store"}, gotStore); err != nil {
				t.Fatalf("failed to get store: %v", err)
			}
			if len(gotStore.Status.Conditions) != 0 {
				t.Errorf("stored conditions = %+v, want empty after failed status write", gotStore.Status.Conditions)
			}
		})
	}
}

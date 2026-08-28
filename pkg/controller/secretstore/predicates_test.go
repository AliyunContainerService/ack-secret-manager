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

// predicates_test.go covers the store-side watch predicates
// (SecretStorePredicate / ClusterSecretStorePredicate): the trigger
// annotation four-state semantics (appearing / value change / cleared /
// unchanged) and the status-only update blocking that breaks the
// status-write self-trigger loop.

package secretstore

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// newSecretStorePair builds an old/new SecretStore pair sharing spec and
// generation, so only the status/annotation delta decides the predicate.
func newSecretStorePair() (*api.SecretStore, *api.SecretStore) {
	base := func() *api.SecretStore {
		return &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "store",
				Namespace:  "default",
				Generation: 1,
			},
			Spec: api.SecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
		}
	}
	return base(), base()
}

func withTrigger(ss *api.SecretStore, v string) *api.SecretStore {
	if v != "" {
		if ss.Annotations == nil {
			ss.Annotations = map[string]string{}
		}
		ss.Annotations[TriggerReconcileAnnotation] = v
	} else {
		delete(ss.Annotations, TriggerReconcileAnnotation)
	}
	return ss
}

// TestSecretStorePredicateTriggerAnnotationFourStates pins the trigger
// annotation semantics of the store-side predicate: a newly appearing or
// changed trigger value enqueues a reconcile (the controller clears the
// annotation after recreating the client), while a cleared or unchanged
// annotation must not trigger anything.
func TestSecretStorePredicateTriggerAnnotationFourStates(t *testing.T) {
	p := SecretStorePredicate{}

	tests := []struct {
		name   string
		oldVal string
		newVal string
		want   bool
	}{
		{name: "appearing trigger value passes", oldVal: "", newVal: "rotate-1", want: true},
		{name: "changed trigger value passes", oldVal: "rotate-1", newVal: "rotate-2", want: true},
		{name: "cleared trigger annotation is blocked", oldVal: "rotate-1", newVal: "", want: false},
		{name: "unchanged trigger annotation is blocked", oldVal: "rotate-1", newVal: "rotate-1", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj, newObj := newSecretStorePair()
			withTrigger(oldObj, tt.oldVal)
			withTrigger(newObj, tt.newVal)
			if got := p.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}); got != tt.want {
				t.Errorf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSecretStorePredicateStatusOnlyBlocked: status-only updates (the
// controller's own status writes) must not re-enqueue the store; the
// periodic ReconciliationPeriod requeue is the safety net.
func TestSecretStorePredicateStatusOnlyBlocked(t *testing.T) {
	p := SecretStorePredicate{}

	t.Run("status-only change is blocked", func(t *testing.T) {
		oldObj, newObj := newSecretStorePair()
		newObj.Status.Conditions = []api.SecretStoreStatusCondition{{
			Type:   api.SecretStoreReady,
			Status: corev1.ConditionTrue,
		}}
		newObj.Status.ClientGeneration = 1
		if p.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Fatal("status-only update must be blocked")
		}
	})

	t.Run("status-only change plus cleared trigger annotation is blocked", func(t *testing.T) {
		oldObj, newObj := newSecretStorePair()
		withTrigger(oldObj, "rotate-1")
		withTrigger(newObj, "")
		newObj.Status.ClientGeneration = 1
		if p.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Fatal("status write that clears the trigger annotation must be blocked")
		}
	})

	t.Run("spec change passes", func(t *testing.T) {
		oldObj, newObj := newSecretStorePair()
		newObj.Spec.KMS = nil
		if !p.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Fatal("spec change must pass")
		}
	})

	t.Run("generation change passes", func(t *testing.T) {
		oldObj, newObj := newSecretStorePair()
		newObj.Generation = 2
		if !p.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Fatal("generation change must pass")
		}
	})

	t.Run("wrong type is blocked", func(t *testing.T) {
		wrong := event.UpdateEvent{ObjectOld: &api.ClusterSecretStore{}, ObjectNew: &api.ClusterSecretStore{}}
		if p.Update(wrong) {
			t.Fatal("non-SecretStore objects must be blocked")
		}
	})

	t.Run("create, delete and generic always pass", func(t *testing.T) {
		obj, _ := newSecretStorePair()
		if !p.Create(event.CreateEvent{Object: obj}) {
			t.Fatal("Create must pass")
		}
		if !p.Delete(event.DeleteEvent{Object: obj}) {
			t.Fatal("Delete must pass")
		}
		if !p.Generic(event.GenericEvent{Object: obj}) {
			t.Fatal("Generic must pass")
		}
	})
}

// newClusterSecretStorePair mirrors newSecretStorePair for the cluster-scoped store.
func newClusterSecretStorePair() (*api.ClusterSecretStore, *api.ClusterSecretStore) {
	base := func() *api.ClusterSecretStore {
		return &api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "cluster-store",
				Generation: 1,
			},
			Spec: api.ClusterSecretStoreSpec{KMS: &api.KMSProvider{KMS: &api.KMSAuth{}}},
		}
	}
	return base(), base()
}

func withClusterTrigger(css *api.ClusterSecretStore, v string) *api.ClusterSecretStore {
	if v != "" {
		if css.Annotations == nil {
			css.Annotations = map[string]string{}
		}
		css.Annotations[TriggerReconcileAnnotation] = v
	} else {
		delete(css.Annotations, TriggerReconcileAnnotation)
	}
	return css
}

// TestClusterSecretStorePredicateTriggerAnnotationFourStates: the same
// four-state trigger contract applies to the cluster-scoped store.
func TestClusterSecretStorePredicateTriggerAnnotationFourStates(t *testing.T) {
	p := ClusterSecretStorePredicate{}

	tests := []struct {
		name   string
		oldVal string
		newVal string
		want   bool
	}{
		{name: "appearing trigger value passes", oldVal: "", newVal: "rotate-1", want: true},
		{name: "changed trigger value passes", oldVal: "rotate-1", newVal: "rotate-2", want: true},
		{name: "cleared trigger annotation is blocked", oldVal: "rotate-1", newVal: "", want: false},
		{name: "unchanged trigger annotation is blocked", oldVal: "rotate-1", newVal: "rotate-1", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj, newObj := newClusterSecretStorePair()
			withClusterTrigger(oldObj, tt.oldVal)
			withClusterTrigger(newObj, tt.newVal)
			if got := p.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}); got != tt.want {
				t.Errorf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestClusterSecretStorePredicateStatusOnlyBlocked mirrors the SecretStore
// status-only contract for the cluster-scoped store.
func TestClusterSecretStorePredicateStatusOnlyBlocked(t *testing.T) {
	p := ClusterSecretStorePredicate{}

	t.Run("status-only change is blocked", func(t *testing.T) {
		oldObj, newObj := newClusterSecretStorePair()
		newObj.Status.Conditions = []api.SecretStoreStatusCondition{{
			Type:   api.SecretStoreReady,
			Status: corev1.ConditionTrue,
		}}
		newObj.Status.ClientGeneration = 1
		if p.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Fatal("status-only update must be blocked")
		}
	})

	t.Run("spec change passes", func(t *testing.T) {
		oldObj, newObj := newClusterSecretStorePair()
		newObj.Spec.KMS = nil
		if !p.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Fatal("spec change must pass")
		}
	})

	t.Run("wrong type is blocked", func(t *testing.T) {
		wrong := event.UpdateEvent{ObjectOld: &api.SecretStore{}, ObjectNew: &api.SecretStore{}}
		if p.Update(wrong) {
			t.Fatal("non-ClusterSecretStore objects must be blocked")
		}
	})
}

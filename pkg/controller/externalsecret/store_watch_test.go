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

// store_watch_test.go covers the reverse store->ExternalSecret watch: the
// MapFunc reference matching (positive/negative, cross-namespace, namespace
// filtering, List failure) and the store watch predicates (status-only
// blocking, status.clientGeneration pass-through, trigger-annotation
// semantics).

package externalsecret

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// watchTestES builds an ExternalSecret with one spec.data entry per dataRef
// and one spec.dataProcess[].extract entry per extractRef.
func watchTestES(name, ns string, dataRefs []*api.SecretStoreRef, extractRefs []*api.SecretStoreRef) *api.ExternalSecret {
	es := &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       api.ExternalSecretSpec{Provider: "kms"},
	}
	for i, ref := range dataRefs {
		es.Spec.Data = append(es.Spec.Data, api.DataSource{
			Key:            fmt.Sprintf("data-key-%d", i),
			SecretStoreRef: ref,
		})
	}
	for i, ref := range extractRefs {
		es.Spec.DataProcess = append(es.Spec.DataProcess, api.DataProcess{
			Extract: &api.DataSource{
				Key:            fmt.Sprintf("extract-key-%d", i),
				SecretStoreRef: ref,
			},
		})
	}
	return es
}

// requestNames converts reconcile requests to a sorted "ns/name" slice.
func requestNames(reqs []reconcile.Request) []string {
	names := make([]string, 0, len(reqs))
	for _, req := range reqs {
		names = append(names, req.String())
	}
	sort.Strings(names)
	return names
}

func assertRequestNames(t *testing.T, got []reconcile.Request, want ...string) {
	t.Helper()
	names := requestNames(got)
	sort.Strings(want)
	if len(names) != len(want) {
		t.Fatalf("expected enqueued requests %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("expected enqueued requests %v, got %v", want, names)
		}
	}
}

func TestMapStoreToExternalSecrets_ReferenceMatching(t *testing.T) {
	store := &api.SecretStore{ObjectMeta: metav1.ObjectMeta{Name: "store1", Namespace: "ns-a"}}
	r := newTestReconciler(t, false, nil,
		// default kind -> SecretStore, same namespace: match
		watchTestES("es-default-kind", "ns-a", []*api.SecretStoreRef{{Name: "store1"}}, nil),
		// explicit SecretStore kind via spec.dataProcess[].extract: match
		watchTestES("es-extract-ref", "ns-a", nil, []*api.SecretStoreRef{{Name: "store1", Kind: "SecretStore"}}),
		// same name but ClusterSecretStore kind: no match
		watchTestES("es-css-kind", "ns-a", []*api.SecretStoreRef{{Name: "store1", Kind: "ClusterSecretStore"}}, nil),
		// different store name: no match
		watchTestES("es-other-store", "ns-a", []*api.SecretStoreRef{{Name: "store2"}}, nil),
		// same name, different namespace: no match
		watchTestES("es-other-ns", "ns-b", []*api.SecretStoreRef{{Name: "store1"}}, nil),
		// ENV auth (nil ref): no match
		watchTestES("es-env", "ns-a", []*api.SecretStoreRef{nil}, nil),
	)
	reqs := r.mapStoreToExternalSecrets(context.Background(), store)
	assertRequestNames(t, reqs, "ns-a/es-default-kind", "ns-a/es-extract-ref")
}

// A deprecated cross-namespace ref.Namespace pointing at the store's
// namespace must be mapped even when the ExternalSecret lives elsewhere.
func TestMapStoreToExternalSecrets_CrossNamespaceRef(t *testing.T) {
	store := &api.SecretStore{ObjectMeta: metav1.ObjectMeta{Name: "store1", Namespace: "ns-a"}}
	r := newTestReconciler(t, false, nil,
		watchTestES("es-cross-ns", "ns-b", []*api.SecretStoreRef{{Name: "store1", Namespace: "ns-a"}}, nil),
	)
	reqs := r.mapStoreToExternalSecrets(context.Background(), store)
	assertRequestNames(t, reqs, "ns-b/es-cross-ns")
}

// --watch-namespaces/--exclude-namespaces must be honored by the mapping.
func TestMapStoreToExternalSecrets_NamespaceFiltering(t *testing.T) {
	store := &api.SecretStore{ObjectMeta: metav1.ObjectMeta{Name: "store1", Namespace: "ns-a"}}
	objs := []client.Object{
		watchTestES("es-watched", "ns-a", []*api.SecretStoreRef{{Name: "store1"}}, nil),
		watchTestES("es-not-watched", "ns-c", []*api.SecretStoreRef{{Name: "store1", Namespace: "ns-a"}}, nil),
		watchTestES("es-excluded", "ns-x", []*api.SecretStoreRef{{Name: "store1", Namespace: "ns-a"}}, nil),
	}
	r := newTestReconciler(t, false, nil, objs...)
	r.WatchNamespaces = map[string]bool{"ns-a": true, "ns-x": false}
	reqs := r.mapStoreToExternalSecrets(context.Background(), store)
	assertRequestNames(t, reqs, "ns-a/es-watched")
}

func TestMapClusterStoreToExternalSecrets(t *testing.T) {
	css := &api.ClusterSecretStore{ObjectMeta: metav1.ObjectMeta{Name: "cluster-store"}}
	r := newTestReconciler(t, false, nil,
		watchTestES("es-css-match", "ns-a", []*api.SecretStoreRef{{Name: "cluster-store", Kind: "ClusterSecretStore"}}, nil),
		watchTestES("es-css-extract", "ns-b", nil, []*api.SecretStoreRef{{Name: "cluster-store", Kind: "ClusterSecretStore"}}),
		// default kind resolves to SecretStore: no match
		watchTestES("es-default-kind", "ns-a", []*api.SecretStoreRef{{Name: "cluster-store"}}, nil),
		// different cluster store name: no match
		watchTestES("es-other-css", "ns-a", []*api.SecretStoreRef{{Name: "other-cluster-store", Kind: "ClusterSecretStore"}}, nil),
	)
	reqs := r.mapClusterStoreToExternalSecrets(context.Background(), css)
	assertRequestNames(t, reqs, "ns-a/es-css-match", "ns-b/es-css-extract")

	// Watched-namespace filtering applies to the cluster-scoped store too.
	r.WatchNamespaces = map[string]bool{"ns-a": true}
	reqs = r.mapClusterStoreToExternalSecrets(context.Background(), css)
	assertRequestNames(t, reqs, "ns-a/es-css-match")
}

// A List failure must not panic and must yield no requests (the periodic
// requeue, when polling is enabled, remains the fallback).
func TestMapStoreToExternalSecrets_ListFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("add api scheme: %v", err)
	}
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithIndex(&api.ExternalSecret{}, storeRefIndexField, storeRefIndexKeys).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return fmt.Errorf("injected list failure")
			},
		}).
		Build()
	r := &ExternalSecretReconciler{Client: cl, Log: logr.Discard(), Ctx: context.Background()}
	store := &api.SecretStore{ObjectMeta: metav1.ObjectMeta{Name: "store1", Namespace: "ns-a"}}
	if reqs := r.mapStoreToExternalSecrets(context.Background(), store); len(reqs) != 0 {
		t.Fatalf("expected no requests on List failure, got %v", requestNames(reqs))
	}
}

// storeRefKey and resolveClientName must agree on the key format for every
// supported kind and namespace combination, otherwise the field-index query
// side would silently miss the index entries. ENV auth (nil ref) is excluded
// by design: storeRefKey emits "" (no watch matching) while
// resolveClientName returns the ENV client key.
func TestStoreRefKeyMatchesResolveClientName(t *testing.T) {
	const esNamespace = "es-ns"
	cases := []struct {
		name string
		ref  *api.SecretStoreRef
	}{
		{"default kind", &api.SecretStoreRef{Name: "store"}},
		{"default kind with ref.Namespace", &api.SecretStoreRef{Name: "store", Namespace: "other-ns"}},
		{"SecretStore kind", &api.SecretStoreRef{Name: "store", Kind: "SecretStore"}},
		{"SecretStore kind with ref.Namespace", &api.SecretStoreRef{Name: "store", Kind: "SecretStore", Namespace: "other-ns"}},
		{"ClusterSecretStore kind", &api.SecretStoreRef{Name: "cluster-store", Kind: "ClusterSecretStore"}},
		{"ClusterSecretStore kind ignores ref.Namespace", &api.SecretStoreRef{Name: "cluster-store", Kind: "ClusterSecretStore", Namespace: "other-ns"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			indexKey := storeRefKey(tc.ref, esNamespace)
			clientName := resolveClientName(tc.ref, esNamespace)
			if indexKey == "" {
				t.Fatalf("storeRefKey unexpectedly produced no key")
			}
			if indexKey != clientName {
				t.Fatalf("key contract drift: storeRefKey=%q, resolveClientName=%q", indexKey, clientName)
			}
		})
	}
}

// storeRefIndexKeys must emit exactly the keys the query side constructs:
// default kind -> namespace/<ES ns>/<name>, deprecated cross-namespace
// ref.Namespace honored, ClusterSecretStore -> cluster/<name>, ENV (nil
// ref) and unsupported kinds emit nothing, duplicates collapse.
func TestStoreRefIndexKeys(t *testing.T) {
	es := watchTestES("es", "ns-a",
		[]*api.SecretStoreRef{
			{Name: "store1"},                          // default kind, same ns
			{Name: "store2", Namespace: "ns-b"},       // deprecated cross-ns
			{Name: "css", Kind: "ClusterSecretStore"}, // cluster-scoped
			{Name: "store1"},                          // duplicate of first ref
			nil,                                       // ENV auth: no key
			{Name: "weird", Kind: "UnknownKind"},      // unsupported kind: no key
		},
		[]*api.SecretStoreRef{
			{Name: "store3", Kind: "SecretStore"},     // extract ref
			{Name: "css", Kind: "ClusterSecretStore"}, // duplicate via extract
		},
	)
	got := storeRefIndexKeys(es)
	sort.Strings(got)
	want := []string{
		"cluster/css",
		"namespace/ns-a/store1",
		"namespace/ns-a/store3",
		"namespace/ns-b/store2",
	}
	if len(got) != len(want) {
		t.Fatalf("expected keys %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected keys %v, got %v", want, got)
		}
	}

	// Non-ExternalSecret objects yield no keys.
	if keys := storeRefIndexKeys(&api.SecretStore{}); len(keys) != 0 {
		t.Fatalf("expected no keys for non-ExternalSecret objects, got %v", keys)
	}
}

// The index-based lookup must enqueue only ExternalSecrets referencing the
// store: an ES holding refs to multiple stores is enqueued exactly once per
// store event, and refs to other stores never leak into the result.
func TestMapStoreToExternalSecrets_IndexTargetsOnlyReferencers(t *testing.T) {
	store := &api.SecretStore{ObjectMeta: metav1.ObjectMeta{Name: "store1", Namespace: "ns-a"}}
	r := newTestReconciler(t, false, nil,
		// references store1 twice (data + extract): enqueued exactly once
		watchTestES("es-multi-ref", "ns-a",
			[]*api.SecretStoreRef{{Name: "store1"}, {Name: "other"}},
			[]*api.SecretStoreRef{{Name: "store1"}}),
		// only references other stores: must not be enqueued
		watchTestES("es-unrelated", "ns-a",
			[]*api.SecretStoreRef{{Name: "other"}, {Name: "store1", Kind: "ClusterSecretStore"}}, nil),
	)
	reqs := r.mapStoreToExternalSecrets(context.Background(), store)
	assertRequestNames(t, reqs, "ns-a/es-multi-ref")
}

func TestSecretStoreWatchPredicate(t *testing.T) {
	p := SecretStoreWatchPredicate{}
	now := metav1.Time{Time: time.Now()}

	base := func() *api.SecretStore {
		return &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "store1", Namespace: "ns-a", Generation: 1},
			Spec:       api.SecretStoreSpec{},
		}
	}
	update := func(oldObj, newObj *api.SecretStore) event.UpdateEvent {
		return event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
	}

	t.Run("create and delete always pass", func(t *testing.T) {
		if !p.Create(event.CreateEvent{Object: base()}) {
			t.Fatal("create event must pass")
		}
		if !p.Delete(event.DeleteEvent{Object: base()}) {
			t.Fatal("delete event must pass")
		}
	})

	t.Run("status-only update is blocked", func(t *testing.T) {
		oldObj, newObj := base(), base()
		newObj.Status.Conditions = append(newObj.Status.Conditions, api.SecretStoreStatusCondition{Type: api.SecretStoreReady})
		if p.Update(update(oldObj, newObj)) {
			t.Fatal("status-only update must be blocked")
		}
	})

	t.Run("clientGeneration change passes", func(t *testing.T) {
		oldObj, newObj := base(), base()
		newObj.Status.ClientGeneration = 1
		if !p.Update(update(oldObj, newObj)) {
			t.Fatal("status.clientGeneration change must pass (client rebuild signal)")
		}
	})

	t.Run("unchanged clientGeneration with other status change is blocked", func(t *testing.T) {
		oldObj, newObj := base(), base()
		oldObj.Status.ClientGeneration = 3
		newObj.Status.ClientGeneration = 3
		newObj.Status.Conditions = append(newObj.Status.Conditions, api.SecretStoreStatusCondition{Type: api.SecretStoreReady})
		if p.Update(update(oldObj, newObj)) {
			t.Fatal("status change without clientGeneration bump must be blocked")
		}
	})

	t.Run("spec change passes", func(t *testing.T) {
		oldObj, newObj := base(), base()
		newObj.Spec.KMS = &api.KMSProvider{}
		if !p.Update(update(oldObj, newObj)) {
			t.Fatal("spec change must pass")
		}
	})

	t.Run("deletionTimestamp change passes", func(t *testing.T) {
		oldObj, newObj := base(), base()
		newObj.DeletionTimestamp = &now
		newObj.Finalizers = []string{"finalizer.ack.secrets-manager.alibabacloud.com"}
		if !p.Update(update(oldObj, newObj)) {
			t.Fatal("deletion must pass")
		}
	})

	t.Run("annotation-only changes are blocked", func(t *testing.T) {
		withTrigger := func(v string) *api.SecretStore {
			o := base()
			if v != "" {
				o.Annotations = map[string]string{utils.TriggerReconcileAnnotation: v}
			}
			return o
		}
		// empty -> non-empty: blocked (trigger-annotation-driven ES
		// reconciliation is signalled via the status.clientGeneration bump
		// persisted by the Store controller)
		if p.Update(update(withTrigger(""), withTrigger("rotate-1"))) {
			t.Fatal("trigger annotation appearing must be blocked")
		}
		// value change: blocked
		if p.Update(update(withTrigger("rotate-1"), withTrigger("rotate-2"))) {
			t.Fatal("trigger annotation value change must be blocked")
		}
		// cleared: blocked
		if p.Update(update(withTrigger("rotate-1"), withTrigger(""))) {
			t.Fatal("cleared trigger annotation must be blocked")
		}
		// unchanged: blocked
		if p.Update(update(withTrigger("rotate-1"), withTrigger("rotate-1"))) {
			t.Fatal("unchanged trigger annotation must be blocked")
		}
	})

	t.Run("wrong type is blocked", func(t *testing.T) {
		wrong := event.UpdateEvent{ObjectOld: &api.ExternalSecret{}, ObjectNew: &api.ExternalSecret{}}
		if p.Update(wrong) {
			t.Fatal("non-store objects must be blocked")
		}
	})
}

func TestClusterSecretStoreWatchPredicate(t *testing.T) {
	p := ClusterSecretStoreWatchPredicate{}

	base := func() *api.ClusterSecretStore {
		return &api.ClusterSecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-store", Generation: 1},
			Spec:       api.ClusterSecretStoreSpec{},
		}
	}
	update := func(oldObj, newObj *api.ClusterSecretStore) event.UpdateEvent {
		return event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
	}

	if !p.Create(event.CreateEvent{Object: base()}) || !p.Delete(event.DeleteEvent{Object: base()}) {
		t.Fatal("create and delete events must pass")
	}

	// status-only update is blocked
	oldObj, newObj := base(), base()
	newObj.Status.Conditions = append(newObj.Status.Conditions, api.SecretStoreStatusCondition{Type: api.SecretStoreReady})
	if p.Update(update(oldObj, newObj)) {
		t.Fatal("status-only update must be blocked")
	}

	// status.clientGeneration change passes (client rebuild signal)
	oldObj, newObj = base(), base()
	newObj.Status.ClientGeneration = 1
	if !p.Update(update(oldObj, newObj)) {
		t.Fatal("status.clientGeneration change must pass")
	}

	// spec change passes
	oldObj, newObj = base(), base()
	newObj.Spec.KMS = &api.KMSProvider{}
	if !p.Update(update(oldObj, newObj)) {
		t.Fatal("spec change must pass")
	}

	// annotation-only changes are blocked (trigger-annotation-driven ES
	// reconciliation is signalled via the status.clientGeneration bump
	// persisted by the Store controller)
	oldObj, newObj = base(), base()
	oldObj.Annotations = map[string]string{utils.TriggerReconcileAnnotation: "rotate-1"}
	if p.Update(update(oldObj, newObj)) {
		t.Fatal("cleared trigger annotation must be blocked")
	}
}

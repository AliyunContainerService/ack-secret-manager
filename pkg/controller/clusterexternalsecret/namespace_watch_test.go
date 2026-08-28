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

package clusterexternalsecret

import (
	"context"
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/testutil"
)

// labeledNamespace builds a namespace carrying the given labels.
func labeledNamespace(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

// mapCESNames extracts and sorts CES names for order-independent comparison.
func mapCESNames(requests []reconcile.Request) []string {
	names := make([]string, 0, len(requests))
	for _, req := range requests {
		names = append(names, req.Name)
	}
	sort.Strings(names)
	return names
}

// selectorCES builds a ClusterExternalSecret that selects namespaces by
// label selector (conditions path).
func selectorCES(name string, matchLabels map[string]string) *api.ClusterExternalSecret {
	return &api.ClusterExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: api.ClusterExternalSecretSpec{
			Conditions: []api.ClusterExternalSecretCondition{
				{NamespaceSelector: &metav1.LabelSelector{MatchLabels: matchLabels}},
			},
		},
	}
}

// regexCES builds a ClusterExternalSecret that selects namespaces by regex.
func regexCES(name string, regexes ...string) *api.ClusterExternalSecret {
	return &api.ClusterExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: api.ClusterExternalSecretSpec{
			Conditions: []api.ClusterExternalSecretCondition{
				{NamespaceRegexes: regexes},
			},
		},
	}
}

// TestMapNamespaceToCES verifies the mapping over all three condition
// flavors plus empty-config (match-all) and non-matching namespaces.
func TestMapNamespaceToCES(t *testing.T) {
	scheme := testutil.NewTestScheme(t)

	cesByLabels := selectorCES("ces-labels", map[string]string{"team": "platform"})
	cesByNames := newTestCES("ces-names", "prod-a")
	cesByRegex := regexCES("ces-regex", "team-.*")
	cesMatchAll := &api.ClusterExternalSecret{ObjectMeta: metav1.ObjectMeta{Name: "ces-match-all"}}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cesByLabels, cesByNames, cesByRegex, cesMatchAll).
		Build()
	r := newTestReconciler(c, scheme)

	cases := []struct {
		name      string
		namespace *corev1.Namespace
		want      []string
	}{
		{
			name:      "label selector hit enqueues the selector CES (plus match-all)",
			namespace: labeledNamespace("any-ns", map[string]string{"team": "platform"}),
			want:      []string{"ces-labels", "ces-match-all"},
		},
		{
			name:      "label selector miss only enqueues match-all",
			namespace: labeledNamespace("any-ns", map[string]string{"team": "other"}),
			want:      []string{"ces-match-all"},
		},
		{
			name:      "namespace name list hit",
			namespace: labeledNamespace("prod-a", nil),
			want:      []string{"ces-match-all", "ces-names"},
		},
		{
			name:      "namespace name list miss",
			namespace: labeledNamespace("prod-b", nil),
			want:      []string{"ces-match-all"},
		},
		{
			name:      "regex hit",
			namespace: labeledNamespace("team-payments", nil),
			want:      []string{"ces-match-all", "ces-regex"},
		},
		{
			name:      "regex hit (substring match)",
			namespace: labeledNamespace("x-team-payments", nil),
			want:      []string{"ces-match-all", "ces-regex"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapCESNames(r.mapNamespaceToCES(context.Background(), tc.namespace))
			if len(got) != len(tc.want) {
				t.Fatalf("mapNamespaceToCES() = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("mapNamespaceToCES() = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestMapNamespaceToCESDeleteEvent verifies a deleted namespace maps the
// same way, so reconciles can clean the provisionedNamespaces ledger.
func TestMapNamespaceToCESDeleteEvent(t *testing.T) {
	scheme := testutil.NewTestScheme(t)
	ces := newTestCES("ces-delete", "ns-gone")

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ces).Build()
	r := newTestReconciler(c, scheme)

	// The informer delivers the last-known object on delete; mapping must
	// enqueue the CES even though the namespace is gone from the cluster.
	deleted := labeledNamespace("ns-gone", nil)
	requests := r.mapNamespaceToCES(context.Background(), deleted)
	got := mapCESNames(requests)
	if len(got) != 1 || got[0] != "ces-delete" {
		t.Fatalf("delete event mapping = %v, want [ces-delete]", got)
	}
}

// TestMapNamespaceToCESListError verifies a failing CES List produces no
// requests instead of a partial mapping.
func TestMapNamespaceToCESListError(t *testing.T) {
	scheme := testutil.NewTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, clnt client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				return context.DeadlineExceeded
			},
		}).
		Build()
	r := newTestReconciler(c, scheme)

	requests := r.mapNamespaceToCES(context.Background(), labeledNamespace("ns-a", nil))
	if len(requests) != 0 {
		t.Fatalf("mapNamespaceToCES() on List error = %v, want no requests", requests)
	}
}

// TestMapNamespaceToCESNonNamespaceObject guards against misrouted events:
// an object that is not a Namespace yields no requests.
func TestMapNamespaceToCESNonNamespaceObject(t *testing.T) {
	scheme := testutil.NewTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := newTestReconciler(c, scheme)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "stray-pod", Namespace: "default"}}
	if requests := r.mapNamespaceToCES(context.Background(), pod); len(requests) != 0 {
		t.Fatalf("mapNamespaceToCES(non-namespace) = %v, want no requests", requests)
	}
}

// TestNamespaceWatchPredicate pins the event filtering contract: create and
// delete always pass, updates pass only when labels changed.
func TestNamespaceWatchPredicate(t *testing.T) {
	p := NamespaceWatchPredicate{}

	createEvent := event.CreateEvent{Object: labeledNamespace("ns-new", nil)}
	if !p.Create(createEvent) {
		t.Fatal("Create() = false, want true")
	}

	deleteEvent := event.DeleteEvent{Object: labeledNamespace("ns-gone", nil)}
	if !p.Delete(deleteEvent) {
		t.Fatal("Delete() = false, want true")
	}

	labelChange := event.UpdateEvent{
		ObjectOld: labeledNamespace("ns-x", map[string]string{"team": "a"}),
		ObjectNew: labeledNamespace("ns-x", map[string]string{"team": "b"}),
	}
	if !p.Update(labelChange) {
		t.Fatal("Update() with label change = false, want true")
	}

	statusOnly := event.UpdateEvent{
		ObjectOld: labeledNamespace("ns-x", map[string]string{"team": "a"}),
		ObjectNew: labeledNamespace("ns-x", map[string]string{"team": "a"}),
	}
	if p.Update(statusOnly) {
		t.Fatal("Update() with unchanged labels = true, want false (status-only change must be filtered)")
	}

	generic := event.GenericEvent{Object: labeledNamespace("ns-x", nil)}
	if p.Generic(generic) {
		t.Fatal("Generic() = true, want false")
	}
}

// TestMapNamespaceToCESMatchesProvisioning guarantees the watch mapping and
// the provisioning path agree on the same namespace set.
func TestMapNamespaceToCESMatchesProvisioning(t *testing.T) {
	scheme := testutil.NewTestScheme(t)
	ces := selectorCES("ces-consistency", map[string]string{"env": "prod"})

	namespaces := []client.Object{
		labeledNamespace("ns-prod-1", map[string]string{"env": "prod"}),
		labeledNamespace("ns-dev-1", map[string]string{"env": "dev"}),
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(append([]client.Object{ces}, namespaces...)...).
		Build()
	r := newTestReconciler(c, scheme)

	for _, obj := range namespaces {
		ns := obj.(*corev1.Namespace)
		mapped := len(r.mapNamespaceToCES(context.Background(), ns)) > 0

		matching, err := r.getMatchingNamespaces(ces)
		if err != nil {
			t.Fatalf("getMatchingNamespaces() error: %v", err)
		}
		provisioned := false
		for _, name := range matching {
			if name == ns.Name {
				provisioned = true
				break
			}
		}

		if mapped != provisioned {
			t.Fatalf("namespace %s: watch mapping = %v, provisioning path = %v, must agree",
				ns.Name, mapped, provisioned)
		}
	}
}

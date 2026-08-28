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

package externalsecret

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// newESInNamespace builds a minimal ExternalSecret living in the given
// namespace for predicate event construction.
func newESInNamespace(namespace string) *api.ExternalSecret {
	return &api.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "probe-es",
			Namespace: namespace,
		},
	}
}

// TestNamespaceExcludePredicate covers the blacklist predicate: an empty
// exclude set passes everything, an excluded namespace is blocked on
// Create/Update/Generic, and every other namespace passes. Delete events
// always pass so legacy finalizers in excluded namespaces can be removed.
func TestNamespaceExcludePredicate(t *testing.T) {
	tests := []struct {
		name     string
		excluded map[string]struct{}
		event    event.GenericEvent // namespace source; rebuilt per kind below
		want     bool               // expected for Create/Update/Generic
	}{
		{name: "empty set passes any namespace", excluded: nil, event: event.GenericEvent{Object: newESInNamespace("ns-a")}, want: true},
		{name: "excluded namespace blocked on create/update/generic", excluded: map[string]struct{}{"ns-x": {}}, event: event.GenericEvent{Object: newESInNamespace("ns-x")}, want: false},
		{name: "non-excluded namespace passes", excluded: map[string]struct{}{"ns-x": {}}, event: event.GenericEvent{Object: newESInNamespace("ns-a")}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := namespaceExcludePredicate{excluded: tt.excluded}
			obj := tt.event.Object

			if got := p.Create(event.CreateEvent{Object: obj}); got != tt.want {
				t.Errorf("Create() = %v, want %v", got, tt.want)
			}
			if got := p.Update(event.UpdateEvent{ObjectOld: obj, ObjectNew: obj}); got != tt.want {
				t.Errorf("Update() = %v, want %v", got, tt.want)
			}
			if got := p.Generic(event.GenericEvent{Object: obj}); got != tt.want {
				t.Errorf("Generic() = %v, want %v", got, tt.want)
			}
			// Delete always passes, regardless of the exclusion.
			if got := p.Delete(event.DeleteEvent{Object: obj}); !got {
				t.Errorf("Delete() = false, want true (delete events always pass)")
			}
		})
	}
}

// TestNamespaceExcludePredicateDeletePassesThrough pins the finalizer escape
// hatch: even with an excluded namespace configured, Delete events pass so
// legacy (<=0.6.6) finalizers on out-of-scope ExternalSecrets can be
// removed instead of hanging in Terminating forever.
func TestNamespaceExcludePredicateDeletePassesThrough(t *testing.T) {
	p := namespaceExcludePredicate{excluded: map[string]struct{}{"ns-x": {}}}
	if got := p.Delete(event.DeleteEvent{Object: newESInNamespace("ns-x")}); !got {
		t.Error("expected Delete events from an excluded namespace to pass through")
	}
}

// TestNamespaceExcludePredicateDerivedFromWatchNamespaces pins the wiring:
// only the false (exclude) entries of WatchNamespaces feed the predicate,
// true (include) entries and unlisted namespaces are never excluded by it.
func TestNamespaceExcludePredicateDerivedFromWatchNamespaces(t *testing.T) {
	r := &ExternalSecretReconciler{
		WatchNamespaces: map[string]bool{"ns-a": true, "ns-x": false},
	}
	p := r.namespaceExcludePredicate()

	if got := p.Create(event.CreateEvent{Object: newESInNamespace("ns-x")}); got {
		t.Error("expected the exclude entry ns-x to be blocked")
	}
	if got := p.Create(event.CreateEvent{Object: newESInNamespace("ns-a")}); !got {
		t.Error("expected the include entry ns-a to pass the blacklist predicate")
	}
	if got := p.Create(event.CreateEvent{Object: newESInNamespace("ns-b")}); !got {
		t.Error("expected an unlisted namespace to pass the blacklist predicate")
	}
}

// ---------------------------------------------------------------------------
// shouldWatch combinations (merged from should_watch_test.go)
// ---------------------------------------------------------------------------

// TestShouldWatchCombinations covers the four --watch-namespaces /
// --exclude-namespaces combinations of shouldWatch.
func TestShouldWatchCombinations(t *testing.T) {
	tests := []struct {
		name            string
		watchNamespaces map[string]bool
		namespace       string
		want            bool
	}{
		// No flags: everything is watched.
		{name: "no config passes any namespace", watchNamespaces: nil, namespace: "ns-a", want: true},
		{name: "empty map passes any namespace", watchNamespaces: map[string]bool{}, namespace: "ns-a", want: true},

		// Include only: only listed namespaces are watched.
		{name: "include only: listed namespace watched", watchNamespaces: map[string]bool{"ns-a": true}, namespace: "ns-a", want: true},
		{name: "include only: unlisted namespace not watched", watchNamespaces: map[string]bool{"ns-a": true}, namespace: "ns-b", want: false},

		// Exclude only: only explicitly excluded namespaces are blocked.
		{name: "exclude only: excluded namespace blocked", watchNamespaces: map[string]bool{"ns-x": false}, namespace: "ns-x", want: false},
		{name: "exclude only: other namespace watched", watchNamespaces: map[string]bool{"ns-x": false}, namespace: "ns-a", want: true},

		// Both: include semantics apply, excluded entries stay blocked.
		{name: "both: included namespace watched", watchNamespaces: map[string]bool{"ns-a": true, "ns-x": false}, namespace: "ns-a", want: true},
		{name: "both: excluded namespace blocked", watchNamespaces: map[string]bool{"ns-a": true, "ns-x": false}, namespace: "ns-x", want: false},
		{name: "both: unlisted namespace not watched", watchNamespaces: map[string]bool{"ns-a": true, "ns-x": false}, namespace: "ns-b", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ExternalSecretReconciler{WatchNamespaces: tt.watchNamespaces}
			if got := r.shouldWatch(tt.namespace); got != tt.want {
				t.Errorf("shouldWatch(%q) = %v, want %v (WatchNamespaces=%v)", tt.namespace, got, tt.want, tt.watchNamespaces)
			}
		})
	}
}

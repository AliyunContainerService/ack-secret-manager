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

package v1alpha1

// This file replaces the former Ginkgo+envtest suite that was dead code: it
// referenced an uninitialized testEnv (nil-pointer at testEnv.Start()), had no
// CRD path, no AfterSuite cleanup, and no RunSpecs bootstrap, so it never ran.
// The intent -- exercise ExternalSecret type registration and round-trip
// CRUD/validation -- is preserved here as standard testing.T table-driven unit
// tests over a controller-runtime fake client, matching the pattern used in
// pkg/controller. The scheme is built inline (not via pkg/testutil) because
// this file lives in package v1alpha1 and testutil imports v1alpha1.

import (
	"context"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newExternalSecretScheme registers the alibabacloud API types on a fresh
// scheme so the fake client can persist ExternalSecret objects.
func newExternalSecretScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register alibabacloud types on scheme: %v", err)
	}
	return scheme
}

// TestExternalSecretRoundTrip pins that ExternalSecret is a registered
// runtime.Object that survives a Create/Get/Delete round trip through the
// scheme without losing its spec, across representative spec shapes.
func TestExternalSecretRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		spec ExternalSecretSpec
	}{
		{
			name: "multi-key opaque data",
			spec: ExternalSecretSpec{
				Type: "Opaque",
				Data: []DataSource{
					{Key: "test", Name: "foo", VersionStage: "v1test"},
					{Key: "test2", Name: "foo2", VersionStage: "v2test"},
				},
			},
		},
		{
			name: "empty spec",
			spec: ExternalSecretSpec{},
		},
		{
			name: "provider with data process",
			spec: ExternalSecretSpec{
				Provider: "kms",
				Data:     []DataSource{{Key: "only", Name: "only"}},
				DataProcess: []DataProcess{
					{Extract: &DataSource{Key: "only"}, ReplaceKey: []ReplaceRule{{Target: "a", Source: "b"}}},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newExternalSecretScheme(t)
			created := &ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
				Spec:       tc.spec,
			}
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			ctx := context.Background()

			if err := c.Create(ctx, created); err != nil {
				t.Fatalf("Create returned error: %v", err)
			}

			key := types.NamespacedName{Name: "foo", Namespace: "default"}
			fetched := &ExternalSecret{}
			if err := c.Get(ctx, key, fetched); err != nil {
				t.Fatalf("Get returned error: %v", err)
			}
			if !reflect.DeepEqual(fetched.Spec, tc.spec) {
				t.Errorf("round-tripped spec = %+v, want %+v", fetched.Spec, tc.spec)
			}

			if err := c.Delete(ctx, created); err != nil {
				t.Fatalf("Delete returned error: %v", err)
			}
			if err := c.Get(ctx, key, &ExternalSecret{}); err == nil {
				t.Error("expected a not-found error after Delete, got nil")
			}
		})
	}
}

// TestExternalSecretDeepCopyIndependence pins that the generated DeepCopy
// produces a fully independent object: mutating the copy's nested slices must
// not bleed back into the original.
func TestExternalSecretDeepCopyIndependence(t *testing.T) {
	original := &ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"},
		Spec: ExternalSecretSpec{
			Type: "Opaque",
			Data: []DataSource{{Key: "k1", Name: "n1", VersionStage: "v1"}},
		},
	}

	copied := original.DeepCopy()
	if !reflect.DeepEqual(original, copied) {
		t.Fatalf("DeepCopy must equal the original, got %+v", copied)
	}

	// Mutating the copy must not affect the original (independent backing
	// arrays), which is the whole point of the generated DeepCopy.
	copied.Spec.Data[0].Key = "mutated"
	copied.Spec.Data = append(copied.Spec.Data, DataSource{Key: "extra"})
	if original.Spec.Data[0].Key != "k1" {
		t.Errorf("mutating the copy leaked into the original element, got %q", original.Spec.Data[0].Key)
	}
	if len(original.Spec.Data) != 1 {
		t.Errorf("mutating the copy's slice length leaked into the original, got %d elements", len(original.Spec.Data))
	}
}

// TestExternalSecretListDeepCopyObject pins that ExternalSecretList is a
// registered runtime.Object whose DeepCopyObject preserves its items.
func TestExternalSecretListDeepCopyObject(t *testing.T) {
	list := &ExternalSecretList{
		Items: []ExternalSecret{
			{ObjectMeta: metav1.ObjectMeta{Name: "a"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "b"}},
		},
	}
	obj := list.DeepCopyObject()
	copied, ok := obj.(*ExternalSecretList)
	if !ok {
		t.Fatalf("DeepCopyObject returned %T, want *ExternalSecretList", obj)
	}
	if len(copied.Items) != 2 || copied.Items[0].Name != "a" || copied.Items[1].Name != "b" {
		t.Errorf("DeepCopyObject lost list items, got %+v", copied.Items)
	}
}

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
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
)

// storeRefIndexField is the field index mapping each ExternalSecret to the
// store-reference keys it consumes, enabling targeted lookups in the reverse
// store watches instead of a cluster-wide scan.
const storeRefIndexField = "metadata.storeRefKeys"

// The store key formats themselves are defined exactly once in
// backend.SecretStoreKey/backend.ClusterStoreKey (pkg/backend), shared with
// the Store controllers' provider registration key space.

// storeRefKey derives the index/query key of a single SecretStoreRef,
// aligned with resolveClientName: "namespace/<ns>/<name>" for SecretStore,
// "cluster/<name>" for ClusterSecretStore. ENV auth (nil ref) and
// unsupported kinds yield "" so they never match any store watch event.
func storeRefKey(ref *api.SecretStoreRef, externalSecretNamespace string) string {
	if ref == nil {
		return ""
	}
	kind := ref.Kind
	if kind == "" {
		kind = "SecretStore"
	}
	switch kind {
	case "ClusterSecretStore":
		return backend.ClusterStoreKey(ref.Name)
	case "SecretStore":
		namespace := externalSecretNamespace
		if ref.Namespace != "" {
			namespace = ref.Namespace
		}
		return backend.SecretStoreKey(namespace, ref.Name)
	default:
		// Deliberate fail-closed divergence from resolveClientName (which
		// treats unknown kinds as SecretStore): unknown kinds are rejected by
		// the CRD enum at admission, and both sync paths fail closed on them,
		// so a missed store-event trigger has no observable consequence.
		return ""
	}
}

// storeRefIndexKeys extracts all store-reference keys declared by an
// ExternalSecret (spec.data[].secretStoreRef and
// spec.dataProcess[].extract.secretStoreRef), shared by the field indexer
// and the test fakes; duplicates are collapsed so each referencing
// ExternalSecret is enqueued once.
func storeRefIndexKeys(obj client.Object) []string {
	es, ok := obj.(*api.ExternalSecret)
	if !ok {
		return nil
	}
	seen := make(map[string]struct{})
	for i := range es.Spec.Data {
		if key := storeRefKey(es.Spec.Data[i].SecretStoreRef, es.Namespace); key != "" {
			seen[key] = struct{}{}
		}
	}
	for i := range es.Spec.DataProcess {
		if extract := es.Spec.DataProcess[i].Extract; extract != nil {
			if key := storeRefKey(extract.SecretStoreRef, es.Namespace); key != "" {
				seen[key] = struct{}{}
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	return keys
}

// enqueueReferencingExternalSecrets enqueues watched ExternalSecrets
// referencing the store via the storeRefIndexField field index. The index
// spans every namespace visible to the cache to cover the deprecated
// cross-namespace SecretStoreRef.Namespace; a List failure yields no requests.
func (r *ExternalSecretReconciler) enqueueReferencingExternalSecrets(ctx context.Context, storeKey string) []reconcile.Request {
	esList := &api.ExternalSecretList{}
	if err := r.List(ctx, esList, client.MatchingFields{storeRefIndexField: storeKey}); err != nil {
		klog.Errorf("list ExternalSecrets referencing store %s for watch mapping failed: %v", storeKey, err)
		return nil
	}
	var requests []reconcile.Request
	for i := range esList.Items {
		es := &esList.Items[i]
		if !r.shouldWatch(es.Namespace) {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: es.Namespace, Name: es.Name},
		})
	}
	return requests
}

// mapStoreToExternalSecrets maps a SecretStore event to referencing
// ExternalSecrets (client key "namespace/<ns>/<name>").
func (r *ExternalSecretReconciler) mapStoreToExternalSecrets(ctx context.Context, obj client.Object) []reconcile.Request {
	storeKey := backend.SecretStoreKey(obj.GetNamespace(), obj.GetName())
	return r.enqueueReferencingExternalSecrets(ctx, storeKey)
}

// mapClusterStoreToExternalSecrets maps a ClusterSecretStore event to
// referencing ExternalSecrets (client key "cluster/<name>").
func (r *ExternalSecretReconciler) mapClusterStoreToExternalSecrets(ctx context.Context, obj client.Object) []reconcile.Request {
	storeKey := backend.ClusterStoreKey(obj.GetName())
	return r.enqueueReferencingExternalSecrets(ctx, storeKey)
}

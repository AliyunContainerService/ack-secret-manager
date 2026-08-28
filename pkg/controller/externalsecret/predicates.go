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
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type ExternalSecretsPredicate struct{}

func (p ExternalSecretsPredicate) Create(e event.CreateEvent) bool {
	return true
}

func (p ExternalSecretsPredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (p ExternalSecretsPredicate) Update(e event.UpdateEvent) bool {
	oldObj, ok := e.ObjectOld.(*api.ExternalSecret)
	if !ok {
		return false
	}
	newObj, ok := e.ObjectNew.(*api.ExternalSecret)
	if !ok {
		return false
	}
	if !reflect.DeepEqual(oldObj.Spec, newObj.Spec) ||
		oldObj.GetDeletionTimestamp() != newObj.GetDeletionTimestamp() ||
		oldObj.GetGeneration() != newObj.GetGeneration() {
		return true
	}
	return false
}

func (p ExternalSecretsPredicate) Generic(e event.GenericEvent) bool {
	return true
}

// namespaceExcludePredicate is the blacklist leg of the namespace scope:
// Create/Update/Generic events whose object lives in an excluded namespace
// (--exclude-namespaces entries, the false keys of the reconciler's
// WatchNamespaces map) are dropped before they reach the work queue. An
// empty exclude set passes every event. The include whitelist is enforced
// one layer below, via the manager cache's DefaultNamespaces, so this
// predicate only ever sees the blacklist.
type namespaceExcludePredicate struct {
	excluded map[string]struct{}
}

func (p namespaceExcludePredicate) allowed(obj client.Object) bool {
	if len(p.excluded) == 0 || obj == nil {
		return true
	}
	_, excluded := p.excluded[obj.GetNamespace()]
	return !excluded
}

func (p namespaceExcludePredicate) Create(e event.CreateEvent) bool {
	return p.allowed(e.Object)
}

// Delete always passes: legacy versions (<=0.6.6) attached the finalizer
// before the scope guard, so ExternalSecrets already finalized in excluded
// namespaces must still reach the reconciler to have their finalizer
// removed -- blocking Delete would leave them stuck in Terminating forever.
// In exclude-only mode the cache stays cluster-wide, so these events
// objectively exist; under an include whitelist the cache never sees them
// anyway.
func (p namespaceExcludePredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (p namespaceExcludePredicate) Update(e event.UpdateEvent) bool {
	return p.allowed(e.ObjectNew)
}

func (p namespaceExcludePredicate) Generic(e event.GenericEvent) bool {
	return p.allowed(e.Object)
}

// storeUpdateRelevant is the shared update filter of the store watch
// predicates; specEqual means the spec did NOT change. Beside
// spec/generation/deletion, a status.clientGeneration change passes: the
// Store controllers bump it on every successfully persisted client rebuild,
// so trigger-annotation-driven (generation-unchanged) rebuilds reach the
// ExternalSecret controller through this native watch. All other
// status-only updates and annotation-only changes stay blocked to prevent
// self-trigger storms.
func storeUpdateRelevant(specEqual bool, oldGeneration, newGeneration int64, oldDeletion, newDeletion *metav1.Time, oldClientGeneration, newClientGeneration int64) bool {
	return !specEqual ||
		oldGeneration != newGeneration ||
		!reflect.DeepEqual(oldDeletion, newDeletion) ||
		oldClientGeneration != newClientGeneration
}

// SecretStoreWatchPredicate filters SecretStore events for the ExternalSecret
// controller's reverse watch: spec/generation changes, deletion, and
// status.clientGeneration changes pass; other status-only and
// annotation-only changes are blocked.
type SecretStoreWatchPredicate struct{}

func (p SecretStoreWatchPredicate) Create(e event.CreateEvent) bool {
	return true
}

func (p SecretStoreWatchPredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (p SecretStoreWatchPredicate) Update(e event.UpdateEvent) bool {
	oldObj, ok := e.ObjectOld.(*api.SecretStore)
	if !ok {
		return false
	}
	newObj, ok := e.ObjectNew.(*api.SecretStore)
	if !ok {
		return false
	}
	return storeUpdateRelevant(reflect.DeepEqual(oldObj.Spec, newObj.Spec),
		oldObj.GetGeneration(), newObj.GetGeneration(),
		oldObj.GetDeletionTimestamp(), newObj.GetDeletionTimestamp(),
		oldObj.Status.ClientGeneration, newObj.Status.ClientGeneration)
}

func (p SecretStoreWatchPredicate) Generic(e event.GenericEvent) bool {
	return true
}

// ClusterSecretStoreWatchPredicate mirrors SecretStoreWatchPredicate for the
// cluster-scoped store variant.
type ClusterSecretStoreWatchPredicate struct{}

func (p ClusterSecretStoreWatchPredicate) Create(e event.CreateEvent) bool {
	return true
}

func (p ClusterSecretStoreWatchPredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (p ClusterSecretStoreWatchPredicate) Update(e event.UpdateEvent) bool {
	oldObj, ok := e.ObjectOld.(*api.ClusterSecretStore)
	if !ok {
		return false
	}
	newObj, ok := e.ObjectNew.(*api.ClusterSecretStore)
	if !ok {
		return false
	}
	return storeUpdateRelevant(reflect.DeepEqual(oldObj.Spec, newObj.Spec),
		oldObj.GetGeneration(), newObj.GetGeneration(),
		oldObj.GetDeletionTimestamp(), newObj.GetDeletionTimestamp(),
		oldObj.Status.ClientGeneration, newObj.Status.ClientGeneration)
}

func (p ClusterSecretStoreWatchPredicate) Generic(e event.GenericEvent) bool {
	return true
}

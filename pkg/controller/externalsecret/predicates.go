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

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"

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

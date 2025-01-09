package secretstore

import (
	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

type SecretStorePredicate[object any] struct{}

func (p SecretStorePredicate[object]) Create(e event.TypedCreateEvent[object]) bool {
	return true
}

func (p SecretStorePredicate[object]) Delete(e event.TypedDeleteEvent[object]) bool {
	return true
}

func (p SecretStorePredicate[object]) Update(e event.TypedUpdateEvent[object]) bool {
	var oldObjInterface interface{} = e.ObjectOld
	var newObjInterface interface{} = e.ObjectNew
	oldObj, ok := oldObjInterface.(*api.SecretStore)
	if !ok {
		return false
	}
	newObj, ok := newObjInterface.(*api.SecretStore)
	if !ok {
		return false
	}
	if !reflect.DeepEqual(oldObj.Spec, newObj.Spec) || !reflect.DeepEqual(oldObj.Status, newObj.Status) ||
		oldObj.GetDeletionTimestamp() != newObj.GetDeletionTimestamp() ||
		oldObj.GetGeneration() != newObj.GetGeneration() {
		return true
	}
	return false
}

func (p SecretStorePredicate[object]) Generic(e event.TypedGenericEvent[object]) bool {
	return true
}

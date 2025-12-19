package secretstore

import (
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/event"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

type SecretStorePredicate struct{}

func (p SecretStorePredicate) Create(e event.CreateEvent) bool {
	return true
}

func (p SecretStorePredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (p SecretStorePredicate) Update(e event.UpdateEvent) bool {
	oldObj, ok := e.ObjectOld.(*api.SecretStore)
	if !ok {
		return false
	}
	newObj, ok := e.ObjectNew.(*api.SecretStore)
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

func (p SecretStorePredicate) Generic(e event.GenericEvent) bool {
	return true
}

type ClusterSecretStorePredicate struct{}

func (p ClusterSecretStorePredicate) Create(e event.CreateEvent) bool {
	return true
}

func (p ClusterSecretStorePredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (p ClusterSecretStorePredicate) Update(e event.UpdateEvent) bool {
	oldObj, ok := e.ObjectOld.(*api.ClusterSecretStore)
	if !ok {
		return false
	}
	newObj, ok := e.ObjectNew.(*api.ClusterSecretStore)
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

func (p ClusterSecretStorePredicate) Generic(e event.GenericEvent) bool {
	return true
}

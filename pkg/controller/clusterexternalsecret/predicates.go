package clusterexternalsecret

import (
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/event"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

type ClusterExternalSecretPredicate struct{}

func (p ClusterExternalSecretPredicate) Create(e event.CreateEvent) bool {
	return true
}

func (p ClusterExternalSecretPredicate) Delete(e event.DeleteEvent) bool {
	return true
}

func (p ClusterExternalSecretPredicate) Update(e event.UpdateEvent) bool {
	oldObj, ok := e.ObjectOld.(*api.ClusterExternalSecret)
	if !ok {
		return false
	}
	newObj, ok := e.ObjectNew.(*api.ClusterExternalSecret)
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

func (p ClusterExternalSecretPredicate) Generic(e event.GenericEvent) bool {
	return true
}

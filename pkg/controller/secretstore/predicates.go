package secretstore

import (
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/event"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	// TriggerReconcileAnnotation is defined in pkg/utils so the
	// store controllers and the Secret/ServiceAccount trigger controllers
	// share the same literal.
	TriggerReconcileAnnotation = utils.TriggerReconcileAnnotation
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
	// Status-only changes are ignored to break the status-write self-trigger
	// loop; the periodic ReconciliationPeriod requeue is the safety net.
	if !reflect.DeepEqual(oldObj.Spec, newObj.Spec) ||
		oldObj.GetDeletionTimestamp() != newObj.GetDeletionTimestamp() ||
		oldObj.GetGeneration() != newObj.GetGeneration() {
		return true
	}

	// A cleared trigger annotation must not trigger a reconcile
	oldTriggerAnnotation := oldObj.GetAnnotations()[TriggerReconcileAnnotation]
	newTriggerAnnotation := newObj.GetAnnotations()[TriggerReconcileAnnotation]
	if oldTriggerAnnotation != newTriggerAnnotation && newTriggerAnnotation != "" {
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
	// Same status-only and trigger-annotation semantics as SecretStorePredicate
	if !reflect.DeepEqual(oldObj.Spec, newObj.Spec) ||
		oldObj.GetDeletionTimestamp() != newObj.GetDeletionTimestamp() ||
		oldObj.GetGeneration() != newObj.GetGeneration() {
		return true
	}

	oldTriggerAnnotation := oldObj.GetAnnotations()[TriggerReconcileAnnotation]
	newTriggerAnnotation := newObj.GetAnnotations()[TriggerReconcileAnnotation]
	if oldTriggerAnnotation != newTriggerAnnotation && newTriggerAnnotation != "" {
		return true
	}

	return false
}

func (p ClusterSecretStorePredicate) Generic(e event.GenericEvent) bool {
	return true
}

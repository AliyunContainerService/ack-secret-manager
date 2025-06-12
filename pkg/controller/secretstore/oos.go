package secretstore

import (
	"context"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

func (r *SecretStoreReconciler) ReconcileOOS(ctx context.Context, log logr.Logger, secretStore *v1alpha1.SecretStore) (ctrl.Result, error) {
	isSecretStoretMarkedToBeDeleted := secretStore.GetDeletionTimestamp() != nil
	if isSecretStoretMarkedToBeDeleted {
		log.Info("SecretStore oos is marked to be deleted")
		if utils.Contains(secretStore.GetFinalizers(), secretFinalizer) {
			// exec the clean work in secretFinalizer
			// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
			// remove secretFinalizer
			log.Info("removing finalizer", "currentFinalizers", secretStore.GetFinalizers())
			secretStore.SetFinalizers(utils.Remove(secretStore.GetFinalizers(), secretFinalizer))
			err := r.Update(context.TODO(), secretStore)
			if err != nil {
				log.Error(err, "failed to update externalSec when clean finalizers")
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	if !utils.Contains(secretStore.GetFinalizers(), secretFinalizer) {
		if err := r.addFinalizer(log, secretStore); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

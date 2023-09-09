package secretstore

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	kmsprovider "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/kms"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	secretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"
)

func (r *SecretStoreReconciler) ReconcileKMS(ctx context.Context, log logr.Logger, secretStore *v1alpha1.SecretStore) (ctrl.Result, error) {
	provider := backend.GetProviderByName(kmsprovider.ProviderName)
	clientName := fmt.Sprintf("%s/%s", secretStore.Namespace, secretStore.Name)
	isSecretStoretMarkedToBeDeleted := secretStore.GetDeletionTimestamp() != nil
	if isSecretStoretMarkedToBeDeleted {
		if utils.Contains(secretStore.GetFinalizers(), secretFinalizer) {
			// exec the clean work in secretFinalizer
			// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
			provider.Delete(clientName)
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
	secretClient, err := provider.NewClient(ctx, secretStore, r.Client)
	if err != nil {
		log.Error(err, fmt.Sprintf("could not new client %s", clientName))
		return ctrl.Result{}, err
	}
	kmsClient, ok := secretClient.(*kmsprovider.KMSClient)
	if !ok {
		klog.Errorf("client type error")
		return ctrl.Result{}, err
	}
	provider.Register(kmsClient.GetName(), kmsClient)
	return ctrl.Result{}, nil
}

func (r *SecretStoreReconciler) addFinalizer(logger logr.Logger, ss *v1alpha1.SecretStore) error {
	logger.Info("Adding Finalizer for the secretstore", "name", ss.Name, "namespace", ss.Namespace)
	ss.SetFinalizers(append(ss.GetFinalizers(), secretFinalizer))
	//update external secret instance
	err := r.Client.Update(context.TODO(), ss)
	if err != nil {
		logger.Error(err, "Failed to update secretstore with finalizer", "name", ss.Name, "namespace", ss.Namespace)
		return err
	}
	return nil
}

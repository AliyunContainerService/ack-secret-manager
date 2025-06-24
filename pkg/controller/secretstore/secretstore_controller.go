/*
Copyright 2023.

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

package secretstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/kms"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/oos"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
	"github.com/go-logr/logr"
)

const (
	secretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"
)

// SecretStoreReconciler reconciles a SecretStore object
type SecretStoreReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	Ctx                  context.Context
	ReconciliationPeriod time.Duration
}

//+kubebuilder:rbac:groups=alibabacloud.com.my.domain,resources=secretstores,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=alibabacloud.com.my.domain,resources=secretstores/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=alibabacloud.com.my.domain,resources=secretstores/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SecretStore object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *SecretStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("SecretStore", req.NamespacedName)
	secretStore := &v1alpha1.SecretStore{}

	err := r.Get(r.Ctx, req.NamespacedName, secretStore)
	if err != nil {
		log.Error(err, fmt.Sprintf("could not get SecretStore '%s'", req.NamespacedName))
		return ctrl.Result{}, utils.IgnoreNotFoundError(err)
	}
	r.Log.Info("secret store info", req.NamespacedName)

	clientName := fmt.Sprintf("%s/%s", secretStore.Namespace, secretStore.Name)
	kmsProvider := backend.GetProviderByName(backend.ProviderKMSName)
	oosProvider := backend.GetProviderByName(backend.ProviderOOSName)
	// clean up the old client if it exists
	kmsProvider.Delete(clientName)
	oosProvider.Delete(clientName)

	// if secret store is marked to be deleted, remove the finalizer
	isSecretStoretMarkedToBeDeleted := secretStore.GetDeletionTimestamp() != nil
	if isSecretStoretMarkedToBeDeleted {
		log.Info("SecretStore kms is marked to be deleted")
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

	// if secret store is not marked to be deleted, ensure the finalizer is present
	if !utils.Contains(secretStore.GetFinalizers(), secretFinalizer) {
		if err := r.addFinalizer(log, secretStore); err != nil {
			log.Error(err, "failed to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// init the secret store client
	if secretStore.Spec.KMS != nil {
		secretClient, err := kmsProvider.NewClient(ctx, secretStore, r.Client)
		if err != nil {
			log.Error(err, fmt.Sprintf("could not new kms client %s", clientName))
			return ctrl.Result{}, err
		}
		kmsClient, ok := secretClient.(*kms.KMSClient)
		if !ok {
			log.Error(errors.New("client type error"), fmt.Sprintf("could not new kms client %s", clientName))
			return ctrl.Result{}, err
		}
		kmsProvider.Register(kmsClient.GetName(), kmsClient)
		return ctrl.Result{}, nil
	}

	if secretStore.Spec.OOS != nil {
		secretClient, err := oosProvider.NewClient(ctx, secretStore, r.Client)
		if err != nil {
			log.Error(err, fmt.Sprintf("could not new oos client %s", clientName))
			return ctrl.Result{}, err
		}
		oosClient, ok := secretClient.(*oos.OOSClient)
		if !ok {
			log.Error(errors.New("client type error"), fmt.Sprintf("could not new kms client %s", clientName))
			return ctrl.Result{}, err
		}
		oosProvider.Register(oosClient.GetName(), oosClient)
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretStoreReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	options := controller.Options{
		MaxConcurrentReconciles: reconcileCount,
		Reconciler:              r,
	}
	secretStoreController, err := controller.New("secretStore-controller", mgr, options)
	if err != nil {
		return err
	}
	err = secretStoreController.Watch(source.Kind(mgr.GetCache(), &v1alpha1.SecretStore{}, &handler.TypedEnqueueRequestForObject[*v1alpha1.SecretStore]{}, SecretStorePredicate[*v1alpha1.SecretStore]{}))
	if err != nil {
		return err
	}
	return nil
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

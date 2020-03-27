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

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	timestampFormat = "2006-01-02T15.04.05Z"
	secretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"
)

// ExternalSecretReconciler reconciles a ExternalSecret object
type ExternalSecretReconciler struct {
	client.Client
	APIReader            client.Reader
	Backend              backend.Client
	Log                  logr.Logger
	Ctx                  context.Context
	WatchNamespaces      map[string]bool
	ReconciliationPeriod time.Duration
}

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

// Ignore not found errors
func ignoreNotFoundError(err error) error {
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

// getDesiredData reads the content from the Datasource for later comparison
func (r *ExternalSecretReconciler) getDesiredData(dataList []api.DataSource) (map[string][]byte, error) {
	desiredData := make(map[string][]byte)
	var err error
	for _, data := range dataList {
		queryCondition := backend.SecretQueryCondition{
			VersionId:    data.VersionId,
			VersionStage: data.VersionStage,
		}
		r.Log.Info("checking...", "backend", r.Backend)
		externalSecData, err := r.Backend.GetSecret(data.Key, &queryCondition)
		if err != nil {
			r.Log.Error(err, "unable to read secret from backend", "key", data.Key, "query", queryCondition)
			return nil, err
		}
		desiredData[data.Name] = []byte(externalSecData)
	}
	return desiredData, err
}

// getCurrentData get the current data from secret api
func (r *ExternalSecretReconciler) getCurrentData(namespace string, name string) (map[string][]byte, error) {
	reader := r.APIReader
	data := make(map[string][]byte)
	secret := &corev1.Secret{}
	err := reader.Get(r.Ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, secret)
	if err != nil {
		r.Log.Error(err, "failed to get secret from ")
		return data, err
	}
	data = secret.Data
	return data, err
}

// upsertSecret will create or update a secret
func (r *ExternalSecretReconciler) upsertSecret(externalSec *api.ExternalSecret, data map[string][]byte) error {
	secType := corev1.SecretTypeOpaque
	if externalSec.Spec.Type != "" {
		secType = corev1.SecretType(externalSec.Spec.Type)
	}
	secret := &corev1.Secret{
		Type: secType,
		ObjectMeta: metav1.ObjectMeta{
			Namespace: externalSec.Namespace,
			Labels: map[string]string{
				"lastUpdatedAt": time.Now().Format(timestampFormat),
			},
			Name: externalSec.Spec.Name,
		},
		Data: data,
	}
	err := r.Create(r.Ctx, secret)
	if errors.IsAlreadyExists(err) {
		err = r.Update(r.Ctx, secret)
	}
	return err
}

// deleteSecret will delete a secret given its namespace and name
func (r *ExternalSecretReconciler) deleteSecret(namespace string, name string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	return r.Delete(r.Ctx, secret)
}

// shouldWatch will return true if the ExternalSecret is in a watchable namespace
func (r *ExternalSecretReconciler) shouldWatch(externalSecNamespace string) bool {
	if len(r.WatchNamespaces) > 0 {
		return r.WatchNamespaces[externalSecNamespace]
	}
	return true
}

// AddFinalizerIfNotPresent will check if finalizerName is the finalizers slice
func (r *ExternalSecretReconciler) AddFinalizerIfNotPresent(externalSec *api.ExternalSecret, finalizerName string) error {
	if !containsString(externalSec.ObjectMeta.Finalizers, finalizerName) {
		externalSec.ObjectMeta.Finalizers = append(externalSec.ObjectMeta.Finalizers, finalizerName)
		return r.Update(r.Ctx, externalSec)
	}
	return nil
}

func (r *ExternalSecretReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ExternalSecret", req.NamespacedName)

	externalSec := &api.ExternalSecret{}

	err := r.Get(r.Ctx, req.NamespacedName, externalSec)
	if err != nil {
		log.Error(err, fmt.Sprintf("could not get ExternalSecret '%s'", req.NamespacedName))
		return ctrl.Result{}, ignoreNotFoundError(err)
	}

	secretName := externalSec.Spec.Name
	secretNamespace := externalSec.Namespace

	// check if deletionTimestamp set
	isSecretMarkedToBeDeleted := externalSec.GetDeletionTimestamp() != nil
	if isSecretMarkedToBeDeleted {
		if utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
			// exec the clean work in secretFinalizer
			// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
			if err := r.finalizeExternalSecret(log, secretNamespace, secretName); err != nil {
				return reconcile.Result{RequeueAfter: r.ReconciliationPeriod}, err
			}

			// 删除secretFinalizer，当Finalizers列表被清空时资源删除
			externalSec.SetFinalizers(utils.Remove(externalSec.GetFinalizers(), secretFinalizer))

		}
		return reconcile.Result{}, nil
	}

	// add Finalizer to external secret instance
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		if err := r.addFinalizer(log, externalSec); err != nil {
			return reconcile.Result{}, err
		}
	}
	if !r.shouldWatch(secretNamespace) {
		log.Info(fmt.Sprintf("ignoring unwatch ns %s", secretNamespace), "watched_namespaces", r.WatchNamespaces)
		return ctrl.Result{}, nil
	}
	// Get data from the secret source of truth
	desiredData, err := r.getDesiredData(externalSec.Spec.Data)

	if err != nil {
		log.Error(err, "unable to get desired state for secret")
		return ctrl.Result{}, err
	}

	// Get the actual secret from Kubernetes
	currentData, err := r.getCurrentData(secretNamespace, secretName)

	if err != nil && !errors.IsNotFound(err) {
		log.Error(err, "unable to get current state of secret")
		return ctrl.Result{}, ignoreNotFoundError(err)
	}

	eq := reflect.DeepEqual(desiredData, currentData)
	if !eq {
		log.Info("found secret need to update", "namespace", secretNamespace, "name", secretName)
		if err := r.upsertSecret(externalSec, desiredData); err != nil {
			log.Error(err, "failed to update secret", "namespace", secretNamespace, "name", secretName)
			//reconcile again
			return ctrl.Result{}, err
		}
		log.Info("secret update finished", "namespace", secretNamespace, "name", secretName)
	}

	return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
}

func (r *ExternalSecretReconciler) finalizeExternalSecret(log logr.Logger, secretNamespace, secretName string) error {

	log.Info("Successfully finalized memcached")
	if err := r.deleteSecret(secretNamespace, secretName); err != nil && !errors.IsNotFound(err) {
		log.Error(err, "unable to delete secret", "namespace", secretNamespace, "name", secretName)
		return err
	}
	log.Info("secret deleted successfully", "namespace", secretNamespace, "name", secretName)

	return nil
}

func (r *ExternalSecretReconciler) addFinalizer(logger logr.Logger, es *api.ExternalSecret) error {
	logger.Info("Adding Finalizer for the externalsecret", "name", es.Name)
	es.SetFinalizers(append(es.GetFinalizers(), secretFinalizer))
	//update external secret instance
	err := r.Client.Update(context.TODO(), es)
	if err != nil {
		logger.Error(err, "Failed to update externalsecret with finalizer", "name", es.Name)
		return err
	}
	return nil
}

// SetupWithManager will register the controller
func (r *ExternalSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&api.ExternalSecret{}).
		Complete(r)
}

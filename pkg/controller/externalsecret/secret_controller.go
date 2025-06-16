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
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	timestampFormat = "2006-01-02T15.04.05Z"
	secretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"
)

// ExternalSecretReconciler reconciles a ExternalSecret object
type ExternalSecretReconciler struct {
	client.Client
	APIReader            client.Reader
	Log                  logr.Logger
	Ctx                  context.Context
	WatchNamespaces      map[string]bool
	ReconciliationPeriod time.Duration

	DisablePolling   bool
	RotationInterval time.Duration // Key rotation job running interval.
	KmsLimiter       KmsLimiter
	OosLimiter       OosLimiter

	previousVersions sync.Map
}

// getCurrentData get the current data from secret api
func (r *ExternalSecretReconciler) getCurrentData(namespace string, name string) (map[string][]byte, error) {
	reader := r.APIReader
	data := make(map[string][]byte)
	secret := &corev1.Secret{}
	r.Log.Info("getCurrentData for", "ns", namespace, "name", name)
	err := reader.Get(r.Ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, secret)
	if err != nil {
		r.Log.Error(err, "failed to get current secret")
		return data, err
	}
	data = secret.Data
	return data, err
}

// upsertSecret will create or update a secret
func (r *ExternalSecretReconciler) updateSecret(externalSec *api.ExternalSecret, data map[string][]byte) error {
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
			Name: externalSec.Name,
		},
		Data: data,
	}
	err := r.Create(r.Ctx, secret)
	if kerrors.IsAlreadyExists(err) {
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
	if !utils.Contains(externalSec.ObjectMeta.Finalizers, finalizerName) {
		externalSec.ObjectMeta.Finalizers = append(externalSec.ObjectMeta.Finalizers, finalizerName)
		return r.Update(r.Ctx, externalSec)
	}
	return nil
}

func (r *ExternalSecretReconciler) Requeue(result ctrl.Result) ctrl.Result {
	if r.DisablePolling {
		return ctrl.Result{Requeue: false}
	}
	return result
}

func (r *ExternalSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ExternalSecret", req.NamespacedName)

	externalSec := &api.ExternalSecret{}

	// only do not requeue when getting CR fails.
	err := r.Get(r.Ctx, req.NamespacedName, externalSec)
	if err != nil {
		log.Error(err, fmt.Sprintf("could not get ExternalSecret '%s'", req.NamespacedName))
		return ctrl.Result{}, utils.IgnoreNotFoundError(err)
	}
	secretName := externalSec.Name
	secretNamespace := externalSec.Namespace
	secretIndex := fmt.Sprintf("%s/%s", secretNamespace, secretName)
	log = log.WithValues("secret", secretIndex)
	r.Log.Info("externalSec info", "secretName", secretName, "secretNamespace", secretNamespace)
	// check if deletionTimestamp set
	provider := getProvider(externalSec)
	if provider == nil {
		log.Info("no provider found, skip reconcile")
		return ctrl.Result{}, nil
	}

	isSecretMarkedToBeDeleted := externalSec.GetDeletionTimestamp() != nil
	if isSecretMarkedToBeDeleted {
		if utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
			// exec the clean work in secretFinalizer
			// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
			cleanClient(externalSec, provider)
			r.previousVersions.Delete(req.NamespacedName)

			if err := r.finalizeExternalSecret(log, secretNamespace, secretName); err != nil {
				log.Error(err, "failed to clean secret")
				return reconcile.Result{RequeueAfter: r.ReconciliationPeriod}, err
			}
			// remove secretFinalizer
			log.Info("removing finalizer", "currentFinalizers", externalSec.GetFinalizers())
			externalSec.SetFinalizers(utils.Remove(externalSec.GetFinalizers(), secretFinalizer))
			err := r.Update(context.TODO(), externalSec)
			if err != nil {
				log.Error(err, "failed to update externalSec when clean finalizers")
				return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
			}
		}
		return r.Requeue(reconcile.Result{RequeueAfter: r.RotationInterval}), nil
	}

	klog.Infof("reconcile external secret %v", secretIndex)

	// add Finalizer to external secret instance
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		if err := r.addFinalizer(log, externalSec); err != nil {
			return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
		}
	}
	if !r.shouldWatch(secretNamespace) {
		log.Info(fmt.Sprintf("ignoring unwatch ns %s", secretNamespace), "watched_namespaces", r.WatchNamespaces)
		return ctrl.Result{}, nil
	}

	if externalSec.Status.ObservedGeneration != externalSec.Generation {
		if externalSec.Status.ObservedGeneration != 0 {
			if oldObj, loaded := r.previousVersions.LoadAndDelete(req.NamespacedName); loaded {
				old := oldObj.(*api.ExternalSecret)
				cleanClient(old, provider)
			}
		}

		// update ObservedGeneration
		externalSec.Status.ObservedGeneration = externalSec.Generation
		if err := r.Status().Update(ctx, externalSec); err != nil {
			log.Error(err, "failed to update status")
			return ctrl.Result{}, err
		}

	}
	r.previousVersions.Store(req.NamespacedName, externalSec.DeepCopy())
	errs := initClient(externalSec, provider, r)
	if len(errs) > 0 {
		for _, err := range errs {
			log.Error(err, "failed to init client")
		}
		return ctrl.Result{}, errors.Join(errs...)
	}

	_, syncErr := r.syncIfNeedUpdate(externalSec, provider)
	if syncErr != nil {
		return r.Requeue(ctrl.Result{RequeueAfter: r.ReconciliationPeriod}), syncErr
	}
	log.Info("update secret store", "index", secretIndex)
	return r.Requeue(ctrl.Result{RequeueAfter: r.RotationInterval}), nil
}

func (r *ExternalSecretReconciler) finalizeExternalSecret(log logr.Logger, secretNamespace, secretName string) error {

	log.Info("Successfully finalized external secret")
	if err := r.deleteSecret(secretNamespace, secretName); err != nil && !kerrors.IsNotFound(err) {
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
func (r *ExternalSecretReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	options := controller.Options{
		MaxConcurrentReconciles: reconcileCount,
		Reconciler:              r,
	}
	externalSecretController, err := controller.New("externalSecret-controller", mgr, options)
	if err != nil {
		return err
	}
	// Watch for Pod create / update / delete events and call Reconcile
	err = externalSecretController.Watch(source.Kind(mgr.GetCache(), &api.ExternalSecret{}, &handler.TypedEnqueueRequestForObject[*api.ExternalSecret]{}, ExternalSecretsPredicate[*api.ExternalSecret]{}))
	if err != nil {
		return err
	}
	return nil
}

func getProvider(externalSec *api.ExternalSecret) backend.Provider {
	providerName := externalSec.Spec.Provider
	if providerName == "" {
		providerName = backend.ProviderKMSName
	}

	return backend.GetProviderByName(providerName)
}

func getClientName(provider, name, namespace string, data api.DataSource) string {
	clientName := backend.EnvClient

	if provider == "" {
		provider = backend.ProviderKMSName
	}

	namePrefix := fmt.Sprintf("%s/%s/%s", namespace, name, provider)
	if data.KmsEndpoint != "" {
		clientName = fmt.Sprintf("%s/%s", namePrefix, data.KmsEndpoint)
	}

	if data.SecretStoreRef != nil {
		clientName = fmt.Sprintf("%s/%s/%s", namePrefix, data.SecretStoreRef.Name, data.SecretStoreRef.Namespace)
	}

	return clientName
}

func cleanClient(externalSec *api.ExternalSecret, provider backend.Provider) {
	for _, data := range externalSec.Spec.Data {
		clientName := getClientName(externalSec.Spec.Provider, externalSec.Name, externalSec.Namespace, data)
		provider.Delete(clientName)
	}

	for _, dataProcess := range externalSec.Spec.DataProcess {
		clientName := getClientName(externalSec.Spec.Provider, externalSec.Name, externalSec.Namespace, *dataProcess.Extract)
		provider.Delete(clientName)
	}
}

func initClient(externalSec *api.ExternalSecret, provider backend.Provider, r *ExternalSecretReconciler) []error {
	var errors []error

	for _, data := range externalSec.Spec.Data {
		if err := initClientByData(externalSec.Spec.Provider, externalSec.Name, externalSec.Namespace, data, provider, r); err != nil {
			errors = append(errors, err)
		}
	}

	for _, dataProcess := range externalSec.Spec.DataProcess {
		if err := initClientByData(externalSec.Spec.Provider, externalSec.Name, externalSec.Namespace, *dataProcess.Extract, provider, r); err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}

func initClientByData(providerName, name, namespace string, data api.DataSource, provider backend.Provider, r *ExternalSecretReconciler) error {
	clientName := getClientName(providerName, name, namespace, data)
	if _, err := provider.GetClient(clientName); err != nil {
		store := &api.SecretStore{}
		if data.SecretStoreRef != nil {
			store, err = r.getSecretStore(data.SecretStoreRef)
			if err != nil {
				return fmt.Errorf("get secret store %v error %v", data.SecretStoreRef.Name, err)
			}
		}

		client, err := provider.NewClient(context.Background(), data.KmsEndpoint, clientName, store, r.Client)
		if err != nil {
			return fmt.Errorf("could not new provider client %s", clientName)
		}

		provider.Register(clientName, client)
	}

	return nil
}

func getClient(name, namespace string, provider backend.Provider, data api.DataSource) (backend.SecretClient, string, error) {
	clientName := getClientName(provider.GetName(), name, namespace, data)
	secretClient, err := provider.GetClient(clientName)

	klog.Infof("client name %s,data key %s", clientName, data.Key)
	return secretClient, clientName, err
}

func (r *ExternalSecretReconciler) getExternalSecret(name, namespace string, provider backend.Provider, dataSources []api.DataSource) (map[string][]byte, error) {
	out := make(map[string][]byte)
	errors := make([]error, 0)
	for _, data := range dataSources {
		secretClient, clientName, err := getClient(name, namespace, provider, data)
		if err != nil {
			errors = append(errors, fmt.Errorf("client %s get client error %v", clientName, err))
			continue
		}
		singleMap, err := secretClient.GetExternalSecret(context.Background(), &data, r.Client)
		if err != nil {
			errors = append(errors, fmt.Errorf("client %v get data error %v", clientName, err))
			continue
		}
		for k, v := range singleMap {
			out[k] = v
		}
	}
	if len(errors) != 0 {
		return out, fmt.Errorf("%v", errors)
	}
	return out, nil
}

func (r *ExternalSecretReconciler) getExternalSecretWithExtract(name, namespace string, provider backend.Provider, dataSources []api.DataProcess) (map[string][]byte, error) {
	out := make(map[string][]byte)
	errors := make([]error, 0)
	for _, data := range dataSources {
		secretClient, clientName, err := getClient(name, namespace, provider, *data.Extract)
		if err != nil {
			errors = append(errors, fmt.Errorf("client %s get client error %v", clientName, err))
			continue
		}
		singleMap, err := secretClient.GetExternalSecretWithExtract(context.Background(), &data, r.Client)
		if err != nil {
			errors = append(errors, fmt.Errorf("client %s get data error", clientName))
			continue
		}
		for k, v := range singleMap {
			out[k] = v
		}
	}
	if len(errors) != 0 {
		return out, fmt.Errorf("%v", errors)
	}
	return out, nil
}

func (r *ExternalSecretReconciler) syncIfNeedUpdate(externalSec *api.ExternalSecret, provider backend.Provider) (bool, error) {

	waitTimeoutCtx, cancel := context.WithTimeout(r.Ctx, 5*time.Minute)
	defer cancel()

	var err error
	providerName := provider.GetName()
	switch providerName {
	case backend.ProviderKMSName:
		err = r.KmsLimiter.SecretPullLimiter.Wait(waitTimeoutCtx)
	case backend.ProviderOOSName:
		err = r.OosLimiter.SecretPullLimiter.Wait(waitTimeoutCtx)
	default:
		return false, fmt.Errorf("provider %s not found, only support kms or oos", providerName)
	}
	if err != nil {
		return false, err
	}

	esIndex := fmt.Sprintf("%s/%s", externalSec.Namespace, externalSec.Name)
	log := r.Log.WithValues("secret", esIndex)

	secretMap := make(map[string][]byte)
	if len(externalSec.Spec.Data) != 0 {
		out, err := r.getExternalSecret(externalSec.Name, externalSec.Namespace, provider, externalSec.Spec.Data)
		if err != nil {
			klog.Errorf("get external secret error %v", err)
		}

		for k, v := range out {
			secretMap[k] = v
		}
	}
	if len(externalSec.Spec.DataProcess) != 0 {
		out, err := r.getExternalSecretWithExtract(externalSec.Name, externalSec.Namespace, provider, externalSec.Spec.DataProcess)
		if err != nil {
			klog.Errorf("get external secret error %v", err)
		}

		for k, v := range out {
			secretMap[k] = v
		}
	}

	// Get the actual secret from Kubernetes
	currentData, err := r.getCurrentData(externalSec.Namespace, externalSec.Name)
	if err != nil && !kerrors.IsNotFound(err) {
		log.Error(err, "unable to get current state of secret when sync")
		return false, utils.IgnoreNotFoundError(err)
	}
	eq := reflect.DeepEqual(secretMap, currentData)
	if !eq {
		log.Info("found secret need to update")
		if err := r.updateSecret(externalSec, secretMap); err != nil {
			log.Error(err, "failed to update secret")
			return false, err
		}
		log.Info("secret has sync from external backend")
		return true, nil
	}
	return false, nil
}

func (r *ExternalSecretReconciler) getSecretStore(secretStoreRef *api.SecretStoreRef) (*api.SecretStore, error) {
	if secretStoreRef == nil {
		return nil, fmt.Errorf("secret store ref is nil")
	}
	secretStore := &api.SecretStore{}
	err := r.Get(context.Background(), client.ObjectKey{
		Namespace: secretStoreRef.Namespace,
		Name:      secretStoreRef.Name,
	}, secretStore)
	if err != nil {
		return nil, err
	}
	return secretStore, nil
}

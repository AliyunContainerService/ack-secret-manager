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
	"encoding/json"
	"fmt"
	"golang.org/x/sync/semaphore"
	"reflect"
	"sync"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	kmsprovider "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/kms"
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
	Log                  logr.Logger
	Ctx                  context.Context
	WatchNamespaces      map[string]bool
	ReconciliationPeriod time.Duration
	RotationInterval     time.Duration // Key rotation job running interval.
	rotationTicker       *time.Ticker
	secrets              sync.Map  // secrets map is the cache for secrets.
	closing              chan bool // close channel.
	ConcurrentController *semaphore.Weighted
}

var (
	externalSecretGRV = schema.GroupVersionResource{
		Group:    "alibabacloud.com",
		Version:  "v1alpha1",
		Resource: "externalsecrets",
	}
)

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
	if !utils.Contains(externalSec.ObjectMeta.Finalizers, finalizerName) {
		externalSec.ObjectMeta.Finalizers = append(externalSec.ObjectMeta.Finalizers, finalizerName)
		return r.Update(r.Ctx, externalSec)
	}
	return nil
}

func (r *ExternalSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ExternalSecret", req.NamespacedName)

	externalSec := &api.ExternalSecret{}

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
	isSecretMarkedToBeDeleted := externalSec.GetDeletionTimestamp() != nil
	if isSecretMarkedToBeDeleted {
		if utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
			// exec the clean work in secretFinalizer
			// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
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
				return reconcile.Result{}, err
			}
			r.secrets.Delete(secretIndex)
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
	_, syncErr := r.syncIfNeedUpdate(externalSec)
	if syncErr != nil {
		return ctrl.Result{}, syncErr
	}
	r.secrets.Store(secretIndex, externalSec)
	log.Info("update secret store", "index", secretIndex)
	return ctrl.Result{Requeue: false}, nil
}

func (r *ExternalSecretReconciler) finalizeExternalSecret(log logr.Logger, secretNamespace, secretName string) error {

	log.Info("Successfully finalized external secret")
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
func (r *ExternalSecretReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	options := controller.Options{
		MaxConcurrentReconciles: reconcileCount,
		Reconciler:              r,
	}
	return ctrl.NewControllerManagedBy(mgr).WithOptions(options).WithEventFilter(getWatchPredicateForExternalSecret()).
		For(&api.ExternalSecret{}).
		Complete(r)
}

// secretRotationJob refresh the secret if updated in kms secret-manager
func (r *ExternalSecretReconciler) SecretRotationJob() {
	r.Log.Info("begin polling job", "polling interval", r.RotationInterval)
	r.closing = make(chan bool)
	r.rotationTicker = time.NewTicker(r.RotationInterval)
	for {
		select {
		case <-r.rotationTicker.C:
			r.rotate()
		case <-r.closing:
			if r.rotationTicker != nil {
				r.rotationTicker.Stop()
			}
		}
	}
}

func (r *ExternalSecretReconciler) rotate() {
	r.Log.Info("rotate job running")

	var secretMap sync.Map
	wg := sync.WaitGroup{}
	r.secrets.Range(func(k interface{}, v interface{}) bool {
		es := v.(*api.ExternalSecret)
		r.Log.Info("rotate checking secret", "index", k)
		// Re-generate secret if update in kms secret-manager.
		timeoutContext, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if err := r.ConcurrentController.Acquire(timeoutContext, 1); err != nil {
			cancel()
			return true
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer r.ConcurrentController.Release(1)
			defer cancel()
			updated, _ := r.syncIfNeedUpdate(es)
			if updated {
				secretMap.Store(fmt.Sprintf("%s/%s", es.Namespace, es.Name), es)
			}
		}()
		return true
	})

	wg.Wait()
	secretMap.Range(func(k interface{}, v interface{}) bool {
		secretIndex := k.(string)
		r.Log.Info("sync secret store", "index", secretIndex)
		es, ok := v.(*api.ExternalSecret)
		if !ok {
			return true
		}
		r.secrets.Store(secretIndex, es)
		return true
	})
}

func (r *ExternalSecretReconciler) getExternalSecret(provider backend.Provider, dataSources []api.DataSource) (map[string][]byte, error) {
	out := make(map[string][]byte)
	errors := make([]error, 0)
	for _, data := range dataSources {
		clientName := backend.EnvClient
		if data.SecretStoreRef != nil {
			clientName = fmt.Sprintf("%s/%s", data.SecretStoreRef.Namespace, data.SecretStoreRef.Name)
		}
		klog.Infof("client name %v,data key %v", clientName, data.Key)
		secretClient, err := provider.GetClient(clientName)
		if err != nil {
			//err, "get client error,client name", clientName
			klog.Errorf("client %v get client error %v", clientName, err)
			store, err := r.getSecretStore(data.SecretStoreRef)
			if err != nil {
				errors = append(errors, fmt.Errorf("get secret store %v error %v", clientName, err))
				continue
			}
			secretClient, err = provider.NewClient(context.Background(), store, r.Client)
			if err != nil {
				errors = append(errors, fmt.Errorf("new client from secretstore %v error %v", clientName, err))
				continue
			}
			provider.Register(clientName, secretClient)
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

func (r *ExternalSecretReconciler) getExternalSecretWithExtract(provider backend.Provider, dataSources []api.DataProcess) (map[string][]byte, error) {
	out := make(map[string][]byte)
	errors := make([]error, 0)
	for _, data := range dataSources {
		clientName := backend.EnvClient
		if data.Extract.SecretStoreRef != nil {
			clientName = fmt.Sprintf("%s/%s", data.Extract.SecretStoreRef.Namespace, data.Extract.SecretStoreRef.Name)
		}
		klog.Infof("client name %v,data key %v", clientName, data.Extract.Key)
		secretClient, err := provider.GetClient(clientName)
		if err != nil {
			klog.Errorf("client %v get client error %v", clientName, err)
			store, err := r.getSecretStore(data.Extract.SecretStoreRef)
			if err != nil {
				errors = append(errors, fmt.Errorf("get secret store %v error %v", clientName, err))
				continue
			}
			secretClient, err = provider.NewClient(context.Background(), store, r.Client)
			if err != nil {
				errors = append(errors, fmt.Errorf("new client from secretstore %v error %v", clientName, err))
				continue
			}
			provider.Register(clientName, secretClient)
		}
		singleMap, err := secretClient.GetExternalSecretWithExtract(context.Background(), &data, r.Client)
		if err != nil {
			errors = append(errors, fmt.Errorf("client %v get data error", clientName))
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

func (r *ExternalSecretReconciler) syncIfNeedUpdate(externalSec *api.ExternalSecret) (bool, error) {
	esIndex := fmt.Sprintf("%s/%s", externalSec.Namespace, externalSec.Name)
	log := r.Log.WithValues("secret", esIndex)
	providerName := externalSec.Spec.Provider
	if providerName == "" {
		providerName = kmsprovider.ProviderName
	}
	provider := backend.GetProviderByName(providerName)
	secretMap := make(map[string][]byte)
	if externalSec.Spec.Data != nil && len(externalSec.Spec.Data) != 0 {
		out, err := r.getExternalSecret(provider, externalSec.Spec.Data)
		if err != nil {
			klog.Errorf("get external secret error %v", err)
		}
		for k, v := range out {
			secretMap[k] = v
		}
	}
	if externalSec.Spec.DataProcess != nil && len(externalSec.Spec.DataProcess) != 0 {
		out, err := r.getExternalSecretWithExtract(provider, externalSec.Spec.DataProcess)
		if err != nil {
			klog.Errorf("get external secret error %v", err)
		}
		for k, v := range out {
			secretMap[k] = v
		}
	}
	// Get the actual secret from Kubernetes
	currentData, err := r.getCurrentData(externalSec.Namespace, externalSec.Name)
	if err != nil && !errors.IsNotFound(err) {
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

func (r *ExternalSecretReconciler) InitSecretCache() error {
	k8sCli, err := utils.GetKubernetesClients()
	if err != nil {
		r.Log.Error(err, "failed to get external secret clientset")
	}

	esList, err := k8sCli.Resource(externalSecretGRV).Namespace("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		r.Log.Error(err, "failed to list all external secrets")
		return err
	}

	//add existing secret into reconciler secret map
	for _, esObj := range esList.Items {
		esIndex := fmt.Sprintf("%s/%s", esObj.GetNamespace(), esObj.GetName())
		r.Log.Info("init secret store", "key", esIndex)
		esBytes, _ := esObj.MarshalJSON()
		es := &api.ExternalSecret{}
		if err := json.Unmarshal(esBytes, &es); err != nil {
			r.Log.Error(err, "failed to unmarshal externalsecret during init secret store")
			continue
		}
		r.secrets.Store(esIndex, es)
	}
	return nil
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

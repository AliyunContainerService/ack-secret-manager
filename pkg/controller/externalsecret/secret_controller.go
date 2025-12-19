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
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/jmespath/go-jmespath"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
	APIReader              client.Reader
	Log                    logr.Logger
	Ctx                    context.Context
	WatchNamespaces        map[string]bool
	ReconciliationPeriod   time.Duration
	CleanUpSecretOnFailure bool
	DisablePolling         bool
	RotationInterval       time.Duration // Key rotation job running interval.
	KmsLimiter             KmsLimiter
	OosLimiter             OosLimiter
	EnableCrossNamespace   bool
	RestConfig             *rest.Config
}

// WrappedClient wraps both controller-runtime client and kubernetes client
type WrappedClient struct {
	client.Client
	KubeClient kubernetes.Interface
}

// GetKubeClient returns the kubernetes client interface
func (w *WrappedClient) GetKubeClient() kubernetes.Interface {
	return w.KubeClient
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
func (r *ExternalSecretReconciler) updateSecret(externalSec *api.ExternalSecret, secretMap, dataSecretMap, dataExtractSecretMap map[string][]byte,
	currentData map[string][]byte, dataErrorMap, extractDataErrorMap map[string]error) error {
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
	}
	if r.CleanUpSecretOnFailure {
		klog.Infof("flag cleanup-secret-on-failure is enabled, replace old data with the new data")
		// If cleanup on failure is enabled , just replace the old data with the new data.
		secret.Data = secretMap
	} else if len(dataErrorMap) == 0 && len(extractDataErrorMap) == 0 {
		// If there is no error, just replace the old data with the new data.
		secret.Data = secretMap
	} else {
		// If there is any sync issue, only overwrite the fields that were successfully synced, and do not delete other fields.
		var mergedMap = currentData
		for k, v := range dataSecretMap {
			mergedMap[k] = v
		}
		for k, v := range dataExtractSecretMap {
			mergedMap[k] = v
		}
		secret.Data = mergedMap
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
	secretIndex := fmt.Sprintf("namespace/%s/%s", secretNamespace, secretName)
	log = log.WithValues("secret", secretIndex)
	r.Log.Info("externalSec info", "secretName", secretName, "secretNamespace", secretNamespace)

	rotationInterval := r.RotationInterval
	if externalSec.Spec.RotationInterval != nil {
		rotationInterval = externalSec.Spec.RotationInterval.Duration
	}

	// Handle deletion
	if externalSec.GetDeletionTimestamp() != nil {
		return r.handleDeletion(ctx, log, externalSec, rotationInterval, secretNamespace, secretName)
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

	_, syncErr := r.syncIfNeedUpdate(externalSec)
	if syncErr != nil {
		return r.Requeue(ctrl.Result{RequeueAfter: r.ReconciliationPeriod}), syncErr
	}

	log.Info("update secret store", "index", secretIndex)
	return r.Requeue(ctrl.Result{RequeueAfter: rotationInterval}), nil
}

// handleDeletion handle resource deletion logic
func (r *ExternalSecretReconciler) handleDeletion(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret, rotationInterval time.Duration, secretNamespace, secretName string) (ctrl.Result, error) {
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		return r.Requeue(reconcile.Result{RequeueAfter: rotationInterval}), nil
	}

	// exec the clean work in secretFinalizer
	// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
	if err := r.finalizeExternalSecret(log, secretNamespace, secretName); err != nil {
		log.Error(err, "failed to clean secret")
		return reconcile.Result{RequeueAfter: r.ReconciliationPeriod}, err
	}

	// remove secretFinalizer
	log.Info("removing finalizer", "currentFinalizers", externalSec.GetFinalizers())
	externalSec.SetFinalizers(utils.Remove(externalSec.GetFinalizers(), secretFinalizer))
	err := r.Update(ctx, externalSec)
	if err != nil {
		log.Error(err, "failed to update externalSec when clean finalizers")
		return r.Requeue(reconcile.Result{RequeueAfter: r.ReconciliationPeriod}), err
	}

	return r.Requeue(reconcile.Result{RequeueAfter: rotationInterval}), nil
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

// SetupWithManager sets up the controller with the Manager.
func (r *ExternalSecretReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	r.RestConfig = mgr.GetConfig()

	return ctrl.NewControllerManagedBy(mgr).
		For(&api.ExternalSecret{}, builder.WithPredicates(ExternalSecretsPredicate{})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

func setEndpoint(provider backend.Provider, data *api.DataSource, secretClient backend.SecretClient) {
	if provider.GetName() == backend.ProviderKMSName {
		if data.KmsEndpoint != "" {
			secretClient.SetEndpoint(data.KmsEndpoint)
		} else {
			secretClient.SetEndpoint(provider.GetEndpoint())
		}
	}
}

// processJMESPath processes JMESPath expressions on the secret data
func (r *ExternalSecretReconciler) processJMESPath(data []byte, jmesPaths []api.JMESPathObject) (map[string][]byte, error) {
	resultMap := make(map[string][]byte)

	// If no JMESPath expressions, return the raw data with a key based on context
	if len(jmesPaths) == 0 {
		resultMap["data"] = data
		return resultMap, nil
	}

	// Parse JSON data
	var jsonData interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal secret data: %v", err)
	}

	// Process each JMESPath expression
	for _, jp := range jmesPaths {
		result, err := jmespath.Search(jp.Path, jsonData)
		if err != nil {
			return nil, fmt.Errorf("failed to apply JMESPath %s: %v", jp.Path, err)
		}

		// Convert result to bytes
		resultBytes, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal JMESPath result: %v", err)
		}

		key := jp.ObjectAlias
		if key == "" {
			// If an alias is not specified, use the path as the key
			key = jp.Path
		}
		resultMap[key] = resultBytes
	}

	return resultMap, nil
}

func (r *ExternalSecretReconciler) getExternalSecret(provider backend.Provider, dataSources []api.DataSource, externalSecretNamespace string) (map[string][]byte, map[string]error) {
	out := make(map[string][]byte)
	errorsMap := make(map[string]error)
	for _, data := range dataSources {
		clientName := backend.EnvClient
		var secretStoreRef *api.SecretStoreRef

		// get SecretStoreRef and clientName
		if data.SecretStoreRef != nil {
			secretStoreRef = data.SecretStoreRef
			kind := secretStoreRef.Kind
			if kind == "" {
				kind = "SecretStore"
			}

			switch kind {
			case "SecretStore":
				namespace := externalSecretNamespace
				if secretStoreRef.Namespace != "" {
					namespace = secretStoreRef.Namespace
				}
				clientName = fmt.Sprintf("namespace/%s/%s", namespace, secretStoreRef.Name)
			case "ClusterSecretStore":
				clientName = fmt.Sprintf("cluster/%s", secretStoreRef.Name)
			}
		}

		klog.Infof("client name %v,data key %v", clientName, data.Key)

		// get or create client
		secretClient, err := r.getOrCreateClient(provider, clientName, secretStoreRef, externalSecretNamespace)
		if err != nil {
			errorsMap[data.Key] = err
			continue
		}

		setEndpoint(provider, &data, secretClient)

		// Get the secret with version information
		singleMap, err := secretClient.GetExternalSecret(&data, r.Client)
		if err != nil {
			errorsMap[data.Key] = fmt.Errorf("client %v get data error %v", clientName, err)
			continue
		}

		// Process JMESPath expressions if any
		for secretKey, secretData := range singleMap {
			processedData, err := r.processJMESPath(secretData, data.JMESPath)
			if err != nil {
				errorsMap[data.Key] = fmt.Errorf("failed to process JMESPath for key %s: %v", secretKey, err)
				continue
			}

			// Add processed data to output map
			for processedKey, processedValue := range processedData {
				finalKey := processedKey
				if len(data.JMESPath) > 1 || (len(data.JMESPath) == 1 && data.JMESPath[0].ObjectAlias != "") {
					// Use custom key name if provided
					finalKey = processedKey
				} else if len(data.JMESPath) == 0 {
					// Use original key if no JMESPath
					finalKey = secretKey
				}
				out[finalKey] = processedValue
			}
		}
	}
	return out, errorsMap
}
func (r *ExternalSecretReconciler) getExternalSecretWithExtract(provider backend.Provider, dataSources []api.DataProcess, externalSecretNamespace string) (map[string][]byte, map[string]error) {
	out := make(map[string][]byte)
	errorsMap := make(map[string]error)
	for _, data := range dataSources {
		if data.Extract == nil {
			continue
		}

		clientName := backend.EnvClient
		var secretStoreRef *api.SecretStoreRef

		// get SecretStoreRef and clientName
		if data.Extract.SecretStoreRef != nil {
			secretStoreRef = data.Extract.SecretStoreRef
			kind := secretStoreRef.Kind
			if kind == "" {
				kind = "SecretStore"
			}

			switch kind {
			case "SecretStore":
				namespace := externalSecretNamespace
				if secretStoreRef.Namespace != "" {
					namespace = secretStoreRef.Namespace
				}
				clientName = fmt.Sprintf("namespace/%s/%s", namespace, secretStoreRef.Name)
			case "ClusterSecretStore":
				clientName = fmt.Sprintf("cluster/%s", secretStoreRef.Name)
			}
		}
		klog.Infof("client name %v,data key %v", clientName, data.Extract.Key)

		// get or create client
		secretClient, err := r.getOrCreateClient(provider, clientName, secretStoreRef, externalSecretNamespace)
		if err != nil {
			errorsMap[data.Extract.Key] = err
			continue
		}

		setEndpoint(provider, data.Extract, secretClient)

		// Get the secret with version information
		singleMap, err := secretClient.GetExternalSecretWithExtract(&data, r.Client)
		if err != nil {
			errorsMap[data.Extract.Key] = fmt.Errorf("client %s get data failed: %v", clientName, err)
			continue
		}

		// Process JMESPath expressions if any
		for secretKey, secretData := range singleMap {
			processedData, err := r.processJMESPath(secretData, data.Extract.JMESPath)
			if err != nil {
				errorsMap[data.Extract.Key] = fmt.Errorf("failed to process JMESPath for key %s: %v", secretKey, err)
				continue
			}

			// Apply replace rules if any
			for processedKey, processedValue := range processedData {
				finalValue := processedValue
				if len(data.ReplaceKey) > 0 {
					finalValueStr := string(processedValue)
					for _, rule := range data.ReplaceKey {
						finalValueStr = strings.ReplaceAll(finalValueStr, rule.Source, rule.Target)
					}
					finalValue = []byte(finalValueStr)
				}

				finalKey := processedKey
				if len(data.Extract.JMESPath) > 1 || (len(data.Extract.JMESPath) == 1 && data.Extract.JMESPath[0].ObjectAlias != "") {
					// Use custom key name if provided
					finalKey = processedKey
				} else if len(data.Extract.JMESPath) == 0 {
					// Use original key if no JMESPath
					finalKey = secretKey
				}
				out[finalKey] = finalValue
			}
		}
	}
	return out, errorsMap
}

// getOrCreateClient get or create secret client
func (r *ExternalSecretReconciler) getOrCreateClient(provider backend.Provider, clientName string, secretStoreRef *api.SecretStoreRef, externalSecretNamespace string) (backend.SecretClient, error) {
	secretClient, err := provider.GetClient(clientName)
	if err != nil {
		//err, "get client error,client name", clientName
		klog.Errorf("client %v get client error %v", clientName, err)
		store, err := r.getSecretStore(secretStoreRef, externalSecretNamespace)
		if err != nil {
			return nil, fmt.Errorf("get client %s failed: %v", clientName, err)
		}

		// Create kubernetes.Interface from rest.Config for dynamic token acquisition
		var kubeClient kubernetes.Interface
		if r.RestConfig != nil {
			kubeClient, err = kubernetes.NewForConfig(r.RestConfig)
			if err != nil {
				klog.Errorf("Failed to create kubernetes clientset from rest.Config: %v", err)
			}
		}

		// Create a wrapper client that includes both controller-runtime client and kubernetes client
		wrapperClient := &WrappedClient{
			Client:     r.Client,
			KubeClient: kubeClient,
		}

		secretClient, err = provider.NewClient(context.Background(), store, wrapperClient)
		if err != nil {
			return nil, fmt.Errorf("init client %s failed: %v", clientName, err)
		}
		provider.Register(clientName, secretClient)
		return secretClient, nil
	} else {
		// Client exists, but still need to verify if the current namespace has access permission
		if secretStoreRef != nil {
			kind := secretStoreRef.Kind
			if kind == "" {
				kind = "SecretStore"
			}

			switch kind {
			case "ClusterSecretStore":
				clusterSecretStore := &api.ClusterSecretStore{}
				err := r.Get(context.Background(), client.ObjectKey{
					Name: secretStoreRef.Name,
				}, clusterSecretStore)
				if err != nil {
					return nil, fmt.Errorf("failed to get ClusterSecretStore %s: %v", secretStoreRef.Name, err)
				}

				if !utils.IsNamespaceAllowedForClusterSecretStore(clusterSecretStore, externalSecretNamespace, r.Get) {
					return nil, fmt.Errorf("namespace %s is not allowed to access ClusterSecretStore %s", externalSecretNamespace, secretStoreRef.Name)
				}
			case "SecretStore":
				// Also validate cross namespace restriction for SecretStore
				// Check if cross namespace reference is enabled
				namespace := externalSecretNamespace
				if secretStoreRef.Namespace != "" {
					namespace = secretStoreRef.Namespace
				}

				if !r.EnableCrossNamespace && secretStoreRef.Namespace != "" && secretStoreRef.Namespace != externalSecretNamespace {
					return nil, fmt.Errorf("cross namespace SecretStore reference is disabled, cannot reference SecretStore in namespace %s from namespace %s", secretStoreRef.Namespace, externalSecretNamespace)
				}

				// Verify that the SecretStore actually exists in the target namespace
				secretStore := &api.SecretStore{}
				err := r.Get(context.Background(), client.ObjectKey{
					Namespace: namespace,
					Name:      secretStoreRef.Name,
				}, secretStore)
				if err != nil {
					return nil, fmt.Errorf("failed to get SecretStore %s in namespace %s: %v", secretStoreRef.Name, namespace, err)
				}
			default:
				return nil, fmt.Errorf("unsupported SecretStoreRef Kind: %s, must be SecretStore or ClusterSecretStore", kind)
			}
		}
		return secretClient, nil
	}
}

func (r *ExternalSecretReconciler) syncIfNeedUpdate(externalSec *api.ExternalSecret) (bool, error) {
	providerName := externalSec.Spec.Provider
	if providerName == "" {
		providerName = backend.ProviderKMSName
	}
	waitTimeoutCtx, cancel := context.WithTimeout(r.Ctx, 5*time.Minute)
	defer cancel()

	var err error
	switch providerName {
	case backend.ProviderKMSName:
		err = r.KmsLimiter.SecretPullLimiter.Wait(waitTimeoutCtx)
	case backend.ProviderOOSName:
		err = r.OosLimiter.SecretPullLimiter.Wait(waitTimeoutCtx)
	default:
		return false, fmt.Errorf("provider %v not found, only support kms or oos", providerName)
	}
	if err != nil {
		return false, err
	}

	esIndex := fmt.Sprintf("%s/%s", externalSec.Namespace, externalSec.Name)
	log := r.Log.WithValues("secret", esIndex)
	provider := backend.GetProviderByName(providerName)
	if provider == nil {
		return false, fmt.Errorf("provider %v not found, only support kms or oos", providerName)
	}

	secretMap := make(map[string][]byte)
	dataSecretMap := make(map[string][]byte)
	extractDataSecretMap := make(map[string][]byte)
	dataErrorsMap := make(map[string]error)
	extractDataErrorsMap := make(map[string]error)

	// Merge the logic for Data and DataProcess processing
	if len(externalSec.Spec.Data) != 0 {
		out, errorsMap := r.getExternalSecret(provider, externalSec.Spec.Data, externalSec.Namespace)
		if len(errorsMap) > 0 {
			log.Error(fmt.Errorf("get external secret error"), "external secret errors", "errors", errorsMap)
		}
		dataErrorsMap = errorsMap
		for k, v := range out {
			dataSecretMap[k] = v
			secretMap[k] = v
		}
	}

	if len(externalSec.Spec.DataProcess) != 0 {
		out, errorsMap := r.getExternalSecretWithExtract(provider, externalSec.Spec.DataProcess, externalSec.Namespace)
		if len(errorsMap) > 0 {
			log.Error(fmt.Errorf("get extract external secret error"), "extract external secret errors", "errors", errorsMap)
		}
		extractDataErrorsMap = errorsMap
		for k, v := range out {
			extractDataSecretMap[k] = v
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
		if err := r.updateSecret(externalSec, secretMap, dataSecretMap,
			extractDataSecretMap, currentData, dataErrorsMap, extractDataErrorsMap); err != nil {
			log.Error(err, "failed to update secret")
			return false, err
		}
		r.updateExternalSecretStatus(externalSec, dataErrorsMap, extractDataErrorsMap)
		log.Info("secret has sync from external backend")
		return true, nil
	}

	r.updateExternalSecretStatus(externalSec, dataErrorsMap, extractDataErrorsMap)
	return false, nil
}

func (r *ExternalSecretReconciler) getSecretStore(secretStoreRef *api.SecretStoreRef, externalSecretNamespace string) (*api.SecretStore, error) {
	if secretStoreRef == nil {
		return nil, fmt.Errorf("secret store ref is nil")
	}

	// Check if cross namespace reference is enabled
	if !r.EnableCrossNamespace && secretStoreRef.Namespace != "" && secretStoreRef.Namespace != externalSecretNamespace && secretStoreRef.Kind != "ClusterSecretStore" {
		return nil, fmt.Errorf("cross namespace SecretStore reference is disabled, cannot reference SecretStore in namespace %s from namespace %s", secretStoreRef.Namespace, externalSecretNamespace)
	}

	// If namespace is specified, issue a warning as this field is deprecated
	if secretStoreRef.Namespace != "" && secretStoreRef.Kind != "ClusterSecretStore" {
		klog.Warningf("Namespace field in SecretStoreRef is deprecated. Use SecretStore in the same namespace as ExternalSecret, or use ClusterSecretStore.")
	}

	// If Kind is not specified, default to SecretStore
	kind := secretStoreRef.Kind
	if kind == "" {
		kind = "SecretStore"
	}

	switch kind {
	case "SecretStore":
		// Get SecretStore in the same namespace
		namespace := externalSecretNamespace
		// If namespace is explicitly specified (backward compatibility), use the specified namespace
		if secretStoreRef.Namespace != "" {
			namespace = secretStoreRef.Namespace
		}

		secretStore := &api.SecretStore{}
		err := r.Get(context.Background(), client.ObjectKey{
			Namespace: namespace,
			Name:      secretStoreRef.Name,
		}, secretStore)

		if err != nil {
			return nil, fmt.Errorf("failed to get SecretStore %s in namespace %s: %v", secretStoreRef.Name, namespace, err)
		}
		return secretStore, nil

	case "ClusterSecretStore":
		// Get ClusterSecretStore
		clusterSecretStore := &api.ClusterSecretStore{}
		err := r.Get(context.Background(), client.ObjectKey{
			Name: secretStoreRef.Name,
		}, clusterSecretStore)

		if err != nil {
			return nil, fmt.Errorf("failed to get ClusterSecretStore %s: %v", secretStoreRef.Name, err)
		}

		// Validate if namespace is allowed to access this ClusterSecretStore
		if !utils.IsNamespaceAllowedForClusterSecretStore(clusterSecretStore, externalSecretNamespace, r.Get) {
			return nil, fmt.Errorf("namespace %s is not allowed to access ClusterSecretStore %s", externalSecretNamespace, secretStoreRef.Name)
		}

		// Convert ClusterSecretStore to SecretStore and return
		converted := &api.SecretStore{
			TypeMeta:   clusterSecretStore.TypeMeta,
			ObjectMeta: clusterSecretStore.ObjectMeta,
			Spec: api.SecretStoreSpec{
				KMS: clusterSecretStore.Spec.KMS,
				OOS: clusterSecretStore.Spec.OOS,
			},
			Status: api.SecretStoreStatus{
				Conditions:   clusterSecretStore.Status.Conditions,
				Capabilities: clusterSecretStore.Status.Capabilities,
			},
		}
		// Clear namespace since ClusterSecretStore is cluster-scoped
		converted.Namespace = ""
		return converted, nil

	default:
		return nil, fmt.Errorf("unsupported SecretStoreRef Kind: %s, must be SecretStore or ClusterSecretStore", kind)
	}
}

func (r *ExternalSecretReconciler) updateExternalSecretStatus(externalSec *api.ExternalSecret, dataErrorMap, extractDataErrorMap map[string]error) {
	externalSec.Status.DataSyncResults = make([]api.DataSyncResult, 0)

	// add Data error information
	for k, v := range dataErrorMap {
		result := api.DataSyncResult{
			ExternalSecretKey:   k,
			Status:              "Failed",
			Reason:              v.Error(),
			SynchronizationTime: metav1.Time{Time: time.Now()},
		}
		externalSec.Status.DataSyncResults = append(externalSec.Status.DataSyncResults, result)
	}

	// add Extract Data error information
	for k, v := range extractDataErrorMap {
		result := api.DataSyncResult{
			ExternalSecretKey:   k,
			Status:              "Failed",
			Reason:              v.Error(),
			SynchronizationTime: metav1.Time{Time: time.Now()},
		}
		externalSec.Status.DataSyncResults = append(externalSec.Status.DataSyncResults, result)
	}

	// if no errors, add a success entry
	if len(externalSec.Status.DataSyncResults) == 0 {
		result := api.DataSyncResult{
			Status:              "Succeeded",
			SynchronizationTime: metav1.Time{Time: time.Now()},
		}
		externalSec.Status.DataSyncResults = append(externalSec.Status.DataSyncResults, result)
	}

	// update status
	if err := r.Status().Update(r.Ctx, externalSec); err != nil {
		klog.Errorf("update external secret status error %v", err)
	}
}

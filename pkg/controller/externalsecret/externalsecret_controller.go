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
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
		return data, err
	}
	data = secret.Data
	return data, err
}

// Optimized updateSecret method with clear separation of concerns
func (r *ExternalSecretReconciler) updateSecret(externalSec *api.ExternalSecret, secretMap map[string][]byte, currentData map[string][]byte, metadataTargets map[string]map[string]string) error {
	// Validate input parameters
	if externalSec == nil {
		return fmt.Errorf("externalSec cannot be nil")
	}
	if secretMap == nil {
		secretMap = make(map[string][]byte)
	}
	if currentData == nil {
		currentData = make(map[string][]byte)
	}

	// Create secret operation handler with all required context
	handler := NewSimpleSecretOperationHandler(r.Client, r.CleanUpSecretOnFailure, r.Log)

	// Execute the complete secret operation with all policies and template processing
	err := handler.HandleSecretOperation(r.Ctx, externalSec, secretMap, currentData, metadataTargets)
	if err != nil {
		return err
	}

	return nil
}

// shouldWatch will return true if the ExternalSecret is in a watchable namespace
func (r *ExternalSecretReconciler) shouldWatch(externalSecNamespace string) bool {
	if len(r.WatchNamespaces) > 0 {
		return r.WatchNamespaces[externalSecNamespace]
	}
	return true
}

// isNamespaceTerminating checks if a namespace is in terminating state
func (r *ExternalSecretReconciler) isNamespaceTerminating(namespace string) (bool, error) {
	ns := &corev1.Namespace{}
	err := r.Get(r.Ctx, client.ObjectKey{Name: namespace}, ns)
	if err != nil {
		return false, err
	}

	// Check if namespace is terminating
	return ns.Status.Phase == corev1.NamespaceTerminating, nil
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

func (r *ExternalSecretReconciler) RequeueAfter(duration time.Duration) ctrl.Result {
	if r.DisablePolling {
		return ctrl.Result{Requeue: false}
	}
	return ctrl.Result{RequeueAfter: duration}
}

func (r *ExternalSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ExternalSecret", req.NamespacedName)

	externalSec := &api.ExternalSecret{}

	// only do not requeue when getting CR fails.
	err := r.Get(r.Ctx, req.NamespacedName, externalSec)
	if err != nil {
		// Only return error if it's not NotFound - NotFound is normal when resource is deleted
		return ctrl.Result{}, utils.IgnoreNotFoundError(err)
	}

	// Determine the actual secret name to use
	secretName := externalSec.Name
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Name != "" {
		secretName = externalSec.Spec.Target.Name
	}

	secretNamespace := externalSec.Namespace
	secretIndex := fmt.Sprintf("namespace/%s/%s", secretNamespace, secretName)
	log = log.WithValues("secret", secretIndex)
	r.Log.Info("externalSec info", "secretName", secretName, "secretNamespace", secretNamespace)

	rotationInterval := r.RotationInterval
	if externalSec.Spec.RotationInterval != nil {
		rotationInterval = externalSec.Spec.RotationInterval.Duration
	}

	// Handle deletion - resource lifecycle management
	if externalSec.GetDeletionTimestamp() != nil {
		r.updateResourceManagementStatus(externalSec, "operation", fmt.Errorf("external secret is being deleted"))
		return r.handleDeletion(ctx, log, externalSec, rotationInterval, secretName)
	}

	klog.Infof("reconcile external secret %v", secretIndex)

	// add Finalizer to external secret instance
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		if err := r.addFinalizer(log, externalSec); err != nil {
			r.updateResourceManagementStatus(externalSec, "finalizer", err)
			return r.RequeueAfter(r.ReconciliationPeriod), err
		}
	}

	if !r.shouldWatch(secretNamespace) {
		watchErr := fmt.Errorf("namespace %s is not in watched namespaces", secretNamespace)
		r.updateResourceManagementStatus(externalSec, "namespace_access", watchErr)
		return ctrl.Result{}, nil
	}

	// Check if namespace is terminating before proceeding
	if isTerminating, err := r.isNamespaceTerminating(secretNamespace); err != nil {
		r.updateResourceManagementStatus(externalSec, "namespace_check", err)
		return ctrl.Result{}, err
	} else if isTerminating {
		// Namespace is terminating, skip secret creation
		r.Log.Info("Skipping secret creation as namespace is terminating", "namespace", secretNamespace)
		r.updateResourceManagementStatus(externalSec, "namespace_terminating", fmt.Errorf("namespace %s is terminating", secretNamespace))
		return ctrl.Result{}, nil
	}

	// Delegate ALL data synchronization to syncIfNeedUpdate
	// It will populate dataErrorsMap and extractDataErrorsMap appropriately
	_, syncErr := r.syncIfNeedUpdate(externalSec)

	// syncIfNeedUpdate handles its own status updates for data-related operations
	// We only handle requeuing logic here
	if syncErr != nil {
		// Return only error to let controller-runtime handle exponential backoff
		// RequeueAfter result is ignored when error is non-nil
		return ctrl.Result{}, syncErr
	}

	log.Info("update secret store", "index", secretIndex)
	return r.RequeueAfter(rotationInterval), nil
}

// New helper method for resource management errors (not data sync errors)
func (r *ExternalSecretReconciler) updateResourceManagementStatus(externalSec *api.ExternalSecret, errorType string, err error) {
	// Create a temporary error map for resource management issues
	resourceErrors := map[string]error{errorType: err}
	r.updateExternalSecretStatus(externalSec, resourceErrors, make(map[string]error))
}

// handleDeletion handle resource deletion logic
func (r *ExternalSecretReconciler) handleDeletion(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret, rotationInterval time.Duration, secretName string) (ctrl.Result, error) {
	if !utils.Contains(externalSec.GetFinalizers(), secretFinalizer) {
		return r.RequeueAfter(rotationInterval), nil
	}

	// exec the clean work in secretFinalizer
	// do not delete Finalizer if clean failed, the clean work will exec in next reconcile
	if err := r.finalizeExternalSecret(ctx, log, externalSec, secretName); err != nil {
		log.Error(err, "failed to clean secret")
		return reconcile.Result{RequeueAfter: r.ReconciliationPeriod}, err
	}

	// remove secretFinalizer
	log.Info("removing finalizer", "currentFinalizers", externalSec.GetFinalizers())
	externalSec.SetFinalizers(utils.Remove(externalSec.GetFinalizers(), secretFinalizer))
	err := r.Update(ctx, externalSec)
	if err != nil {
		log.Error(err, "failed to update externalSec when clean finalizers")
		return r.RequeueAfter(r.ReconciliationPeriod), err
	}

	return r.RequeueAfter(rotationInterval), nil
}

func (r *ExternalSecretReconciler) finalizeExternalSecret(ctx context.Context, log logr.Logger, externalSec *api.ExternalSecret, secretName string) error {
	log.Info("Cleaning up secret for ExternalSecret", "externalSecret", externalSec.Name, "secret", secretName)

	// Get the secret
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Namespace: externalSec.Namespace,
		Name:      secretName,
	}, secret)

	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Secret already deleted", "namespace", externalSec.Namespace, "name", secretName)
			return nil
		}
		return fmt.Errorf("failed to get secret: %w", err)
	}

	// Original behavior: directly delete the secret (no owner reference is set)
	// This maintains backward compatibility with the original implementation
	err = r.Delete(ctx, secret)
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete secret: %w", err)
	}

	log.Info("Deleted secret (original orphan mode behavior)",
		"namespace", externalSec.Namespace, "name", secretName)
	return nil
}

func (r *ExternalSecretReconciler) addFinalizer(logger logr.Logger, es *api.ExternalSecret) error {
	logger.Info("Adding Finalizer for the externalsecret", "name", es.Name)
	es.SetFinalizers(append(es.GetFinalizers(), secretFinalizer))
	//update external secret instance
	err := r.Client.Update(context.Background(), es)
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

		// Get the secret with version information
		singleMap, err := secretClient.GetExternalSecret(&data, r.Client)
		if err != nil {
			errorsMap[data.Key] = fmt.Errorf("client %v get data error %v", clientName, err)
			continue
		}

		// Process each result from backend according to original JMESPath configuration
		for secretKey, secretData := range singleMap {
			// For JMESPath results, the secretKey is already the ObjectAlias
			// So we can use it directly as the final key
			finalKey := secretKey

			// Only fallback to data.Name if no JMESPath processing occurred
			// This would be the case for non-JMESPath data sources
			if len(data.JMESPath) == 0 {
				finalKey = data.Name
			}

			out[finalKey] = secretData
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

		// Get the secret with version information
		singleMap, err := secretClient.GetExternalSecretWithExtract(&data, r.Client)
		if err != nil {
			errorsMap[data.Extract.Key] = fmt.Errorf("client %s get data failed: %v", clientName, err)
			continue
		}

		// Process each result from backend according to original JMESPath configuration
		for secretKey, secretData := range singleMap {
			// Apply replace rules if any
			finalValue := secretData
			if len(data.ReplaceKey) > 0 {
				finalValueStr := string(secretData)
				for _, rule := range data.ReplaceKey {
					finalValueStr = strings.ReplaceAll(finalValueStr, rule.Source, rule.Target)
				}
				finalValue = []byte(finalValueStr)
			}

			// Determine the final key name based on the original JMESPath configuration
			finalKey := secretKey // default to backend key
			for _, jp := range data.Extract.JMESPath {
				// If the backend key matches the original JMESPath expression, use the alias
				if secretKey == jp.Path {
					if jp.ObjectAlias != "" {
						finalKey = jp.ObjectAlias
					}
					break
				}
			}

			out[finalKey] = finalValue
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

// syncIfNeedUpdate processes the external secret and determines if an update is needed
func (r *ExternalSecretReconciler) syncIfNeedUpdate(externalSec *api.ExternalSecret) (bool, error) {
	var templateResult *UnifiedTemplateResult
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
		// Provider configuration is a SYSTEM-LEVEL error, not data-source specific
		// Use resource management status update instead of polluting data error maps
		providerErr := fmt.Errorf("unsupported provider: %v, only support kms or oos", providerName)
		r.updateResourceManagementStatus(externalSec, "provider_configuration", providerErr)
		return false, providerErr
	}

	if err != nil {
		// Rate limiting is a SYSTEM-LEVEL error, not data-source specific
		r.updateResourceManagementStatus(externalSec, "rate_limit", err)
		return false, err
	}

	// Determine the actual secret name to use
	secretName := externalSec.Name
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Name != "" {
		secretName = externalSec.Spec.Target.Name
	}

	esIndex := fmt.Sprintf("%s/%s", externalSec.Namespace, secretName)
	log := r.Log.WithValues("secret", esIndex)
	provider := backend.GetProviderByName(providerName)
	if provider == nil {
		// Provider lookup failure is a SYSTEM-LEVEL error
		lookupErr := fmt.Errorf("provider %v not found", providerName)
		r.updateResourceManagementStatus(externalSec, "provider_lookup", lookupErr)
		return false, lookupErr
	}

	secretMap := make(map[string][]byte)
	dataErrorsMap := make(map[string]error)
	extractDataErrorsMap := make(map[string]error)
	var currentData map[string][]byte

	// Process ExternalSecret.Spec.Data EXCLUSIVELY - ONLY populate dataErrorsMap
	if len(externalSec.Spec.Data) != 0 {
		out, errorsMap := r.getExternalSecret(provider, externalSec.Spec.Data, externalSec.Namespace)
		// EXCLUSIVELY populate dataErrorsMap with Data-specific errors
		for k, v := range errorsMap {
			dataErrorsMap[k] = v
		}
		for k, v := range out {
			secretMap[k] = v
		}
	}

	// Process ExternalSecret.Spec.DataProcess EXCLUSIVELY - ONLY populate extractDataErrorsMap
	if len(externalSec.Spec.DataProcess) != 0 {
		out, errorsMap := r.getExternalSecretWithExtract(provider, externalSec.Spec.DataProcess, externalSec.Namespace)
		// EXCLUSIVELY populate extractDataErrorsMap with DataProcess-specific errors
		for k, v := range errorsMap {
			extractDataErrorsMap[k] = v
		}
		for k, v := range out {
			secretMap[k] = v
		}
	}

	// Update status with PURE data-source-specific errors
	r.updateExternalSecretStatus(externalSec, dataErrorsMap, extractDataErrorsMap)

	// Get current secret state
	var getCurrentDataErr error
	currentData, getCurrentDataErr = r.getCurrentData(externalSec.Namespace, secretName)
	if getCurrentDataErr != nil && !errors.IsNotFound(getCurrentDataErr) {
		// State retrieval is a SYSTEM-LEVEL error affecting comparison logic
		r.updateResourceManagementStatus(externalSec, "state_retrieval", getCurrentDataErr)
		return false, getCurrentDataErr
	}

	// If there are template processing requirements, process them now
	// This must happen AFTER getting current data but BEFORE checking update conditions
	templateProcessed := false                       // Track whether template processing occurred
	var metadataTargets map[string]map[string]string // TemplateFrom metadata targets
	if externalSec.Spec.Target != nil && externalSec.Spec.Target.Template != nil {
		tp := NewSimpleTemplateProcessor(r.Client)
		var err error
		templateResult, err = tp.ProcessAllTemplates(r.Ctx, externalSec, secretMap)
		if err != nil {
			// Fatal template processing error - mark ExternalSecret as Failed
			r.updateResourceManagementStatus(externalSec, "template_processing_fatal", err)
			return false, err
		}
		templateProcessed = true

		// Check if there were any non-fatal template processing errors and log them
		if len(templateResult.Stats.Errors) > 0 {
			// Log recoverable errors but don't fail the ExternalSecret
			for _, errMsg := range templateResult.Stats.Errors {
				r.Log.Info("template processing warning", "error", errMsg)
			}

			// Optionally update status to indicate warnings (but not failure)
			var warningMsg strings.Builder
			warningMsg.WriteString("template processing completed with warnings: ")
			for i, errMsg := range templateResult.Stats.Errors {
				if i > 0 {
					warningMsg.WriteString("; ")
				}
				warningMsg.WriteString(errMsg)
			}
			// Use a different error type that indicates warnings rather than failures
			r.updateResourceManagementStatus(externalSec, "template_processing_warnings", fmt.Errorf("%s", warningMsg.String()))
		}

		// Collect metadata targets from template result
		if len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0 {
			metadataTargets = make(map[string]map[string]string)
			if len(templateResult.Metadata.Annotations) > 0 {
				metadataTargets["annotations"] = templateResult.Metadata.Annotations
			}
			if len(templateResult.Metadata.Labels) > 0 {
				metadataTargets["labels"] = templateResult.Metadata.Labels
			}
		}

		// Use Data target for secret creation
		secretMap = templateResult.Data
	}

	// Check if there are any successful data sources or template processing results before proceeding with secret creation
	// Use templateProcessed flag and nil check to satisfy static analysis
	hasSuccessfulData := len(secretMap) > 0
	if templateProcessed && templateResult != nil {
		hasSuccessfulData = hasSuccessfulData || (len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0)
	}

	// Calculate total number of data sources configured
	totalDataSources := len(externalSec.Spec.Data) + len(externalSec.Spec.DataProcess)
	// Calculate total number of errors
	totalErrors := len(dataErrorsMap) + len(extractDataErrorsMap)
	// Check if all data sources have failed
	allDataSourcesFailed := totalDataSources > 0 && totalErrors > 0 && totalErrors >= totalDataSources

	// Check if update is needed
	// Consider template metadata targets as part of the update condition
	// Use templateProcessed flag and nil check to satisfy static analysis
	templateMetadataPresent := false
	if templateProcessed && templateResult != nil {
		templateMetadataPresent = len(templateResult.Metadata.Annotations) > 0 || len(templateResult.Metadata.Labels) > 0
	}
	eq := reflect.DeepEqual(secretMap, currentData) && !templateMetadataPresent

	if !eq {
		log.Info("found secret need to update", "hasSuccessfulData", hasSuccessfulData, "secretMapLength", len(secretMap), "allDataSourcesFailed", allDataSourcesFailed, "totalDataSources", totalDataSources, "totalErrors", totalErrors)

		// If there is no successful data but we still need to update (e.g., to delete the secret), proceed
		// Pass metadataTargets directly instead of using context
		if err := r.updateSecret(externalSec, secretMap, currentData, metadataTargets); err != nil {
			log.Error(err, "failed to update secret", "hasSuccessfulData", hasSuccessfulData, "secretMapLength", len(secretMap), "allDataSourcesFailed", allDataSourcesFailed)

			// Check if the error is due to empty data and handle accordingly
			if len(secretMap) == 0 {
				// This is the case where provider secrets are deleted/unavailable
				// CleanUpSecretOnFailure flag determines whether to clean up the secret
				// Original behavior:
				// - CleanUpSecretOnFailure=true: delete the secret
				// - CleanUpSecretOnFailure=false: retain the secret
				handler := NewSimpleSecretOperationHandler(r.Client, r.CleanUpSecretOnFailure, r.Log)

				if r.CleanUpSecretOnFailure {
					// When CleanUpSecretOnFailure is enabled, delete the secret
					delErr := handler.handleProviderDeletion(r.Ctx, externalSec, secretName)
					if delErr != nil {
						log.Error(delErr, "failed to delete secret when provider data unavailable", "allDataSourcesFailed", allDataSourcesFailed)
						return false, delErr
					}
					// Status already updated with errors by updateExternalSecretStatus above
					return true, nil
				} else {
					// When CleanUpSecretOnFailure is disabled, retain the secret (original behavior)
					log.Info("Retaining secret due to empty data (CleanUpSecretOnFailure disabled)",
						"namespace", externalSec.Namespace, "name", secretName)
					return false, nil
				}
			}

			// Update operation failure is a SYSTEM-LEVEL error
			r.updateResourceManagementStatus(externalSec, "update_operation", err)
			return false, err
		}
		log.Info("secret has sync from external backend", "secretMapLength", len(secretMap), "hasSuccessfulData", hasSuccessfulData, "allDataSourcesFailed", allDataSourcesFailed)
		return true, nil
	}

	// No update needed
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

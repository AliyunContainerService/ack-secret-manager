package secretstore

import (
	"context"
	"fmt"
	"reflect"
	"regexp"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/kms"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/oos"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	secretFinalizer        = "finalizer.ack.secrets-manager.alibabacloud.com"
	clusterSecretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"
)

// WrappedClient wraps both controller-runtime client and kubernetes client
type WrappedClient struct {
	client.Client
	KubeClient kubernetes.Interface
}

// GetKubeClient returns the kubernetes client interface
func (w *WrappedClient) GetKubeClient() kubernetes.Interface {
	return w.KubeClient
}

// StoreInterface defines the common interface for both SecretStore and ClusterSecretStore
type StoreInterface interface {
	client.Object
	GetName() string
	GetNamespace() string
	GetSpec() StoreSpecInterface
	GetStatus() StoreStatusInterface
	GetFinalizers() []string
	SetFinalizers([]string)
	GetGeneration() int64
	GetDeletionTimestamp() *metav1.Time
}

// StoreSpecInterface defines the common interface for both SecretStoreSpec and ClusterSecretStoreSpec
type StoreSpecInterface interface {
	GetKMS() *v1alpha1.KMSProvider
	GetOOS() *v1alpha1.OOSProvider
	GetConditions() []v1alpha1.ClusterSecretStoreCondition
	GetServiceAccountRef() *v1alpha1.ServiceAccountRef
	GetAccessKey() *v1alpha1.SecretRef
	GetAccessKeySecret() *v1alpha1.SecretRef
}

// StoreStatusInterface defines the common interface for both SecretStoreStatus and ClusterSecretStoreStatus
type StoreStatusInterface interface {
	GetConditions() []v1alpha1.SecretStoreStatusCondition
	GetCapabilities() v1alpha1.SecretStoreCapabilities
	SetCapabilities(v1alpha1.SecretStoreCapabilities)
	SetConditions(conditions []v1alpha1.SecretStoreStatusCondition)
}

// SecretStoreWrapper wraps SecretStore to implement StoreInterface
type SecretStoreWrapper struct {
	*v1alpha1.SecretStore
}

func (w *SecretStoreWrapper) GetSpec() StoreSpecInterface {
	return &SecretStoreSpecWrapper{&w.Spec}
}

func (w *SecretStoreWrapper) GetStatus() StoreStatusInterface {
	return &SecretStoreStatusWrapper{&w.Status}
}

// ClusterSecretStoreWrapper wraps ClusterSecretStore to implement StoreInterface
type ClusterSecretStoreWrapper struct {
	*v1alpha1.ClusterSecretStore
}

func (w *ClusterSecretStoreWrapper) GetSpec() StoreSpecInterface {
	return &ClusterSecretStoreSpecWrapper{&w.Spec}
}

func (w *ClusterSecretStoreWrapper) GetStatus() StoreStatusInterface {
	return &ClusterSecretStoreStatusWrapper{&w.Status}
}

// SecretStoreSpecWrapper wraps SecretStoreSpec to implement StoreSpecInterface
type SecretStoreSpecWrapper struct {
	*v1alpha1.SecretStoreSpec
}

func (w *SecretStoreSpecWrapper) GetKMS() *v1alpha1.KMSProvider {
	return w.KMS
}

func (w *SecretStoreSpecWrapper) GetOOS() *v1alpha1.OOSProvider {
	return w.OOS
}

func (w *SecretStoreSpecWrapper) GetServiceAccountRef() *v1alpha1.ServiceAccountRef {
	if w.KMS != nil && w.KMS.KMS != nil {
		return w.KMS.KMS.ServiceAccountRef
	} else if w.OOS != nil && w.OOS.OOS != nil {
		return w.OOS.OOS.ServiceAccountRef
	}
	return nil
}

func (w *SecretStoreSpecWrapper) GetAccessKey() *v1alpha1.SecretRef {
	if w.KMS != nil && w.KMS.KMS != nil {
		return w.KMS.KMS.AccessKey
	} else if w.OOS != nil && w.OOS.OOS != nil {
		return w.OOS.OOS.AccessKey
	}
	return nil
}

func (w *SecretStoreSpecWrapper) GetAccessKeySecret() *v1alpha1.SecretRef {
	if w.KMS != nil && w.KMS.KMS != nil {
		return w.KMS.KMS.AccessKeySecret
	} else if w.OOS != nil && w.OOS.OOS != nil {
		return w.OOS.OOS.AccessKeySecret
	}
	return nil
}

func (w *SecretStoreSpecWrapper) GetConditions() []v1alpha1.ClusterSecretStoreCondition {
	return nil
}

// ClusterSecretStoreSpecWrapper wraps ClusterSecretStoreSpec to implement StoreSpecInterface
type ClusterSecretStoreSpecWrapper struct {
	*v1alpha1.ClusterSecretStoreSpec
}

func (w *ClusterSecretStoreSpecWrapper) GetKMS() *v1alpha1.KMSProvider {
	return w.KMS
}

func (w *ClusterSecretStoreSpecWrapper) GetOOS() *v1alpha1.OOSProvider {
	return w.OOS
}

func (w *ClusterSecretStoreSpecWrapper) GetServiceAccountRef() *v1alpha1.ServiceAccountRef {
	if w.KMS != nil && w.KMS.KMS != nil {
		return w.KMS.KMS.ServiceAccountRef
	} else if w.OOS != nil && w.OOS.OOS != nil {
		return w.OOS.OOS.ServiceAccountRef
	}
	return nil
}

func (w *ClusterSecretStoreSpecWrapper) GetAccessKey() *v1alpha1.SecretRef {
	if w.KMS != nil && w.KMS.KMS != nil {
		return w.KMS.KMS.AccessKey
	} else if w.OOS != nil && w.OOS.OOS != nil {
		return w.OOS.OOS.AccessKey
	}
	return nil
}

func (w *ClusterSecretStoreSpecWrapper) GetAccessKeySecret() *v1alpha1.SecretRef {
	if w.KMS != nil && w.KMS.KMS != nil {
		return w.KMS.KMS.AccessKeySecret
	} else if w.OOS != nil && w.OOS.OOS != nil {
		return w.OOS.OOS.AccessKeySecret
	}
	return nil
}

func (w *ClusterSecretStoreSpecWrapper) GetConditions() []v1alpha1.ClusterSecretStoreCondition {
	return w.Conditions
}

// SecretStoreStatusWrapper wraps SecretStoreStatus to implement StoreStatusInterface
type SecretStoreStatusWrapper struct {
	*v1alpha1.SecretStoreStatus
}

func (w *SecretStoreStatusWrapper) GetConditions() []v1alpha1.SecretStoreStatusCondition {
	return w.Conditions
}

func (w *SecretStoreStatusWrapper) GetCapabilities() v1alpha1.SecretStoreCapabilities {
	return w.Capabilities
}

func (w *SecretStoreStatusWrapper) SetCapabilities(capabilities v1alpha1.SecretStoreCapabilities) {
	w.Capabilities = capabilities
}

func (w *SecretStoreStatusWrapper) SetConditions(conditions []v1alpha1.SecretStoreStatusCondition) {
	w.Conditions = conditions
}

// ClusterSecretStoreStatusWrapper wraps ClusterSecretStoreStatus to implement StoreStatusInterface
type ClusterSecretStoreStatusWrapper struct {
	*v1alpha1.ClusterSecretStoreStatus
}

func (w *ClusterSecretStoreStatusWrapper) GetConditions() []v1alpha1.SecretStoreStatusCondition {
	return w.Conditions
}

func (w *ClusterSecretStoreStatusWrapper) GetCapabilities() v1alpha1.SecretStoreCapabilities {
	return w.Capabilities
}

func (w *ClusterSecretStoreStatusWrapper) SetCapabilities(capabilities v1alpha1.SecretStoreCapabilities) {
	w.Capabilities = capabilities
}

func (w *ClusterSecretStoreStatusWrapper) SetConditions(conditions []v1alpha1.SecretStoreStatusCondition) {
	w.Conditions = conditions
}

// CommonReconciler contains common logic for both SecretStore and ClusterSecretStore controllers
type CommonReconciler struct {
	client.Client
	RestConfig                  *rest.Config
	EnableCrossNamespaceAuthRef bool
}

// validateStoreSpec validates the common part of store spec
func (r *CommonReconciler) validateStoreSpec(kms, oos interface{}, storeType string) error {
	// Cast to the correct types
	kmsProvider, kmsOk := kms.(*v1alpha1.KMSProvider)
	oosProvider, oosOk := oos.(*v1alpha1.OOSProvider)

	// Validate provider count (must be exactly one)
	providerCount := 0
	if kmsOk && kmsProvider != nil && kmsProvider.KMS != nil {
		providerCount++
	}
	if oosOk && oosProvider != nil && oosProvider.OOS != nil {
		providerCount++
	}

	if providerCount == 0 {
		return fmt.Errorf("no provider specified in %s spec", storeType)
	} else if providerCount > 1 {
		return fmt.Errorf("multiple providers specified in %s spec", storeType)
	}

	return nil
}

// ValidateSecretStoreSpec validates the SecretStore spec
func (r *CommonReconciler) validateSecretStoreSpec(store StoreInterface) error {
	spec := store.GetSpec()
	if err := r.validateStoreSpec(spec.GetKMS(), spec.GetOOS(), "SecretStore"); err != nil {
		return err
	}

	// Check cross namespace reference restrictions
	if !r.EnableCrossNamespaceAuthRef {
		// For SecretStore, if cross namespace references are disabled,
		// ensure ServiceAccountRef, AccessKey and AccessKeySecret are in the same namespace
		serviceAccountRef := spec.GetServiceAccountRef()
		if serviceAccountRef != nil && serviceAccountRef.Namespace != "" && serviceAccountRef.Namespace != store.GetNamespace() {
			return fmt.Errorf("cross namespace ServiceAccountRef is disabled, cannot reference ServiceAccount in namespace %s from SecretStore in namespace %s", serviceAccountRef.Namespace, store.GetNamespace())
		}

		accessKey := spec.GetAccessKey()
		if accessKey != nil && accessKey.Namespace != "" && accessKey.Namespace != store.GetNamespace() {
			return fmt.Errorf("cross namespace AccessKey is disabled, cannot reference Secret in namespace %s from SecretStore in namespace %s", accessKey.Namespace, store.GetNamespace())
		}

		accessKeySecret := spec.GetAccessKeySecret()
		if accessKeySecret != nil && accessKeySecret.Namespace != "" && accessKeySecret.Namespace != store.GetNamespace() {
			return fmt.Errorf("cross namespace AccessKeySecret is disabled, cannot reference Secret in namespace %s from SecretStore in namespace %s", accessKeySecret.Namespace, store.GetNamespace())
		}
	}

	return nil
}

// ValidateClusterSecretStoreSpec validates the ClusterSecretStore spec
func (r *CommonReconciler) validateClusterSecretStoreSpec(store StoreInterface) error {
	spec := store.GetSpec()
	if err := r.validateStoreSpec(spec.GetKMS(), spec.GetOOS(), "ClusterSecretStore"); err != nil {
		return err
	}

	serviceAccountRef := spec.GetServiceAccountRef()
	if serviceAccountRef != nil && serviceAccountRef.Namespace == "" {
		return fmt.Errorf("ServiceAccountRef.Namespace is required for ClusterSecretStore")
	}

	accessKey := spec.GetAccessKey()
	if accessKey != nil && accessKey.Namespace == "" {
		return fmt.Errorf("AccessKey.Namespace is required for ClusterSecretStore")
	}

	accessKeySecret := spec.GetAccessKeySecret()
	if accessKeySecret != nil && accessKeySecret.Namespace == "" {
		return fmt.Errorf("AccessKeySecret.Namespace is required for ClusterSecretStore")
	}

	conditions := spec.GetConditions()
	// Validate conditions if any
	for i, condition := range conditions {
		// Validate namespace selector if present
		if condition.NamespaceSelector != nil {
			_, err := metav1.LabelSelectorAsSelector(condition.NamespaceSelector)
			if err != nil {
				return fmt.Errorf("invalid label selector in condition %d: %w", i, err)
			}
		}

		// Validate namespace regexes if present
		for j, regex := range condition.NamespaceRegexes {
			_, err := regexp.Compile(regex)
			if err != nil {
				return fmt.Errorf("invalid regex %s in condition %d regex %d: %w", regex, i, j, err)
			}
		}
	}

	return nil
}

// NeedRecreateClient checks if we need to recreate the client
func (r *CommonReconciler) needRecreateClient(clientName string, generation int64, conditions []v1alpha1.SecretStoreStatusCondition, kmsProvider, oosProvider backend.Provider) bool {
	// Check if client exists
	var clientExists bool
	if kmsProvider != nil {
		client, _ := kmsProvider.GetClient(clientName)
		clientExists = client != nil
	} else if oosProvider != nil {
		client, _ := oosProvider.GetClient(clientName)
		clientExists = client != nil
	}

	// If client doesn't exist, we definitely need to create it
	if !clientExists {
		return true
	}

	// Check if generation has changed (primary indicator of spec changes)
	if len(conditions) > 0 && generation != conditions[0].ObservedGeneration {
		return true
	}

	// If no conditions exist, this is initial reconcile, so recreate
	if len(conditions) == 0 {
		return true
	}

	return false
}

// RecreateClient recreates the client for the SecretStore
func (r *CommonReconciler) recreateClient(ctx context.Context, log logr.Logger, clientName string, kmsProvider, oosProvider backend.Provider, store StoreInterface) error {
	// Clean up the old clients: the plain clientName client plus every
	// endpoint-specific composite ("clientName#endpoint") variant created
	// on-demand by the ExternalSecret controller. Composite variants are not
	// re-created here; they are rebuilt on demand in later ExternalSecret
	// reconciles with the refreshed store credentials.
	kmsProvider.DeletePrefixed(clientName)
	oosProvider.DeletePrefixed(clientName)

	// Create kubernetes.Interface from rest.Config for dynamic token acquisition
	var kubeClient kubernetes.Interface
	var err error
	if r.RestConfig != nil {
		kubeClient, err = kubernetes.NewForConfig(r.RestConfig)
		if err != nil {
			log.Error(err, "Failed to create kubernetes clientset from rest.Config")
			return err
		}
	}

	// Create a wrapper client that includes both controller-runtime client and kubernetes client
	wrapperClient := &WrappedClient{
		Client:     r.Client,
		KubeClient: kubeClient,
	}

	// Create the appropriate client based on provider type and store type.
	// The branch decision uses the same inner-field check as validateStoreSpec
	// (e.g. an empty `kms: {}` block must not select the KMS branch), so any
	// configuration that passed validation is guaranteed to enter exactly one
	// provider branch here.
	spec := store.GetSpec()
	kmsConfigured := spec.GetKMS() != nil && spec.GetKMS().KMS != nil
	oosConfigured := spec.GetOOS() != nil && spec.GetOOS().OOS != nil
	if kmsConfigured {
		if oosConfigured {
			klog.Warningf("both KMS and OOS providers are configured for store %s/%s, KMS takes precedence",
				store.GetNamespace(), store.GetName())
		}
		return r.createKMSClient(ctx, log, store, wrapperClient, clientName, kmsProvider)
	} else if oosConfigured {
		return r.createOOSClient(ctx, log, store, wrapperClient, clientName, oosProvider)
	}

	// validateStoreSpec rejects specs without a configured provider before
	// recreateClient is called, so reaching here indicates an unexpected state.
	return fmt.Errorf("no valid provider found for store %s/%s", store.GetNamespace(), store.GetName())
}

// createKMSClient creates a KMS client
func (r *CommonReconciler) createKMSClient(ctx context.Context, log logr.Logger, store StoreInterface, wrapperClient *WrappedClient, clientName string, kmsProvider backend.Provider) error {
	var secretClient backend.SecretClient
	var err error

	// Handle different store types
	switch s := store.(type) {
	case *SecretStoreWrapper:
		secretClient, err = kmsProvider.NewClient(ctx, s.SecretStore, wrapperClient, "")
	case *ClusterSecretStoreWrapper:
		// Create a temporary SecretStore for compatibility with existing provider code
		tempStore := &v1alpha1.SecretStore{
			TypeMeta:   s.TypeMeta,
			ObjectMeta: s.ObjectMeta,
			Spec: v1alpha1.SecretStoreSpec{
				KMS: s.Spec.KMS,
				OOS: s.Spec.OOS,
			},
		}
		secretClient, err = kmsProvider.NewClient(ctx, tempStore, wrapperClient, "")
	default:
		return fmt.Errorf("unsupported store type for KMS client creation")
	}

	if err != nil {
		log.Error(err, fmt.Sprintf("could not create kms client %s", clientName))
		return err
	}

	kmsClient, ok := secretClient.(*kms.KMSClient)
	if !ok {
		err := fmt.Errorf("unexpected client type for KMS provider")
		log.Error(err, fmt.Sprintf("could not create kms client %s", clientName))
		return err
	}

	kmsProvider.Register(kmsClient.GetName(), kmsClient)
	return nil
}

// createOOSClient creates an OOS client
func (r *CommonReconciler) createOOSClient(ctx context.Context, log logr.Logger, store StoreInterface, wrapperClient *WrappedClient, clientName string, oosProvider backend.Provider) error {
	var secretClient backend.SecretClient
	var err error

	// Handle different store types
	switch s := store.(type) {
	case *SecretStoreWrapper:
		secretClient, err = oosProvider.NewClient(ctx, s.SecretStore, wrapperClient, "")
	case *ClusterSecretStoreWrapper:
		// Create a temporary SecretStore for compatibility with existing provider code
		tempStore := &v1alpha1.SecretStore{
			TypeMeta:   s.TypeMeta,
			ObjectMeta: s.ObjectMeta,
			Spec: v1alpha1.SecretStoreSpec{
				KMS: s.Spec.KMS,
				OOS: s.Spec.OOS,
			},
		}
		secretClient, err = oosProvider.NewClient(ctx, tempStore, wrapperClient, "")
	default:
		return fmt.Errorf("unsupported store type for OOS client creation")
	}

	if err != nil {
		log.Error(err, fmt.Sprintf("could not create oos client %s", clientName))
		return err
	}

	oosClient, ok := secretClient.(*oos.OOSClient)
	if !ok {
		err := fmt.Errorf("unexpected client type for OOS provider")
		log.Error(err, fmt.Sprintf("could not create oos client %s", clientName))
		return err
	}

	oosProvider.Register(oosClient.GetName(), oosClient)
	return nil
}

// SetCondition sets a condition in the store status.
// LastTransitionTime is only refreshed when the condition actually transitions
// (Type/Status/Reason/Message changed); otherwise the original timestamp is preserved.
func (r *CommonReconciler) setCondition(store StoreInterface, condition v1alpha1.SecretStoreStatusCondition) {
	now := metav1.Now()
	condition.ObservedGeneration = store.GetGeneration()

	status := store.GetStatus()
	conditions := status.GetConditions()

	// Check if condition already exists
	for i, c := range conditions {
		if c.Type == condition.Type {
			if c.Status == condition.Status && c.Reason == condition.Reason && c.Message == condition.Message {
				// Preserve the original transition time when nothing changed
				condition.LastTransitionTime = c.LastTransitionTime
			} else {
				// Condition is transitioning, refresh the transition time
				condition.LastTransitionTime = now
			}
			conditions[i] = condition
			return
		}
	}

	// Add new condition with current time
	condition.LastTransitionTime = now
	conditions = append(conditions, condition)
	status.SetConditions(conditions)
}

// StatusEqual compares two SecretStoreStatus objects, ignoring LastTransitionTime
// so that unchanged conditions do not trigger redundant status writes.
func (r *CommonReconciler) statusEqual(old, new StoreStatusInterface) bool {
	if old.GetCapabilities() != new.GetCapabilities() {
		return false
	}

	originConditions := new.GetConditions()
	destConditions := old.GetConditions()

	if len(originConditions) != len(destConditions) {
		return false
	}

	// Compare conditions (assuming they are in the same order),
	// ignoring LastTransitionTime which is managed by setCondition
	for i := range originConditions {
		o := originConditions[i]
		d := destConditions[i]
		o.LastTransitionTime = metav1.Time{}
		d.LastTransitionTime = metav1.Time{}
		if !reflect.DeepEqual(o, d) {
			return false
		}
	}

	return true
}

// HandleDeletion handles the cleanup when SecretStore is marked for deletion
func (r *CommonReconciler) handleDeletion(log logr.Logger, finalizers []string, secretStore client.Object, clientName string, kmsProvider, oosProvider backend.Provider, updateFunc func(client.Object) error) (reconcile.Result, error) {
	log.Info("Store is marked to be deleted")
	var finalizerName string
	switch secretStore.(type) {
	case *v1alpha1.SecretStore:
		finalizerName = secretFinalizer
	case *v1alpha1.ClusterSecretStore:
		finalizerName = clusterSecretFinalizer
	}

	// Clean up the provider clients before removing the finalizer. ClientManager.DeletePrefixed
	// is an idempotent, void operation with no failure path, so this best-effort
	// cleanup guarantees that provider clients (plain and every composite
	// "clientName#endpoint" variant) do not leak on the deletion path.
	kmsProvider.DeletePrefixed(clientName)
	oosProvider.DeletePrefixed(clientName)

	if utils.Contains(finalizers, finalizerName) {
		// Remove finalizer
		log.Info("removing finalizer", "currentFinalizers", finalizers)
		newFinalizers := utils.Remove(finalizers, finalizerName)

		// Update the object with new finalizers
		secretStore.SetFinalizers(newFinalizers)
		err := updateFunc(secretStore)
		if err != nil {
			log.Error(err, "failed to update store when clean finalizers")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

// updateStatus updates the status of the store to indicate success.
// Returns (true, nil) when the status was written, (false, nil) when the
// status was already up-to-date (no write performed), and (false, err) on failure.
func (r *CommonReconciler) updateStatus(ctx context.Context, logger logr.Logger, store StoreInterface, condition v1alpha1.SecretStoreStatusCondition) (bool, error) {
	var oldStatus StoreStatusInterface
	switch s := store.(type) {
	case *SecretStoreWrapper:
		old := s.Status.DeepCopy()
		oldStatus = &SecretStoreStatusWrapper{SecretStoreStatus: old}
	case *ClusterSecretStoreWrapper:
		old := s.Status.DeepCopy()
		oldStatus = &ClusterSecretStoreStatusWrapper{ClusterSecretStoreStatus: old}
	}

	r.setCondition(store, condition)

	// Compare status
	statusEqual := r.statusEqual(oldStatus, store.GetStatus())
	// If there were no conditions before, force update to initialize status
	shouldUpdate := !statusEqual || len(oldStatus.GetConditions()) == 0

	if shouldUpdate {
		// Update the actual Kubernetes object status, not the wrapper
		var objKey client.ObjectKey

		switch s := store.(type) {
		case *SecretStoreWrapper:
			objKey = client.ObjectKey{
				Namespace: s.Namespace,
				Name:      s.Name,
			}
		case *ClusterSecretStoreWrapper:
			objKey = client.ObjectKey{
				Name: s.Name,
			}
		default:
			return false, fmt.Errorf("unknown store type: %T", store)
		}

		// Get a fresh copy of the object to avoid resource version conflicts
		var freshObj client.Object
		switch store.(type) {
		case *SecretStoreWrapper:
			freshObj = &v1alpha1.SecretStore{}
		case *ClusterSecretStoreWrapper:
			freshObj = &v1alpha1.ClusterSecretStore{}
		}

		if err := r.Get(ctx, objKey, freshObj); err != nil {
			logger.Error(err, "failed to get fresh object for status update")
			return false, err
		}

		// Update the status on the fresh object
		switch t := freshObj.(type) {
		case *v1alpha1.SecretStore:
			modifiedStore := store.(*SecretStoreWrapper).SecretStore
			t.Status = *modifiedStore.Status.DeepCopy()
		case *v1alpha1.ClusterSecretStore:
			modifiedStore := store.(*ClusterSecretStoreWrapper).ClusterSecretStore
			t.Status = *modifiedStore.Status.DeepCopy()
		}

		// Attempt to update the status subresource. On conflict errors we return
		// the error and let the workqueue retry with exponential backoff instead
		// of blocking the reconcile goroutine with a synchronous sleep.
		err := r.Status().Update(ctx, freshObj)
		if err == nil {
			logger.Info("successfully updated store status subresource")
			return true, nil
		}

		if errors.IsConflict(err) {
			logger.V(2).Info("conflict when updating status, will be retried by workqueue backoff", "error", err)
		} else {
			logger.Error(err, "failed to update store status")
		}
		return false, err
	}

	return false, nil
}

// updateStatusWithReady updates the status to indicate the store is ready
func (r *CommonReconciler) updateStatusWithReady(ctx context.Context, logger logr.Logger, store StoreInterface) (bool, error) {
	condition := v1alpha1.SecretStoreStatusCondition{
		Type:   v1alpha1.SecretStoreReady,
		Status: corev1.ConditionTrue,
		Reason: v1alpha1.ReasonStoreValid,
	}

	return r.updateStatus(ctx, logger, store, condition)
}

// updateStatusWithError updates the status with an error condition
func (r *CommonReconciler) updateStatusWithError(ctx context.Context, logger logr.Logger, store StoreInterface, reason, message string) error {
	condition := v1alpha1.SecretStoreStatusCondition{
		Type:    v1alpha1.SecretStoreReady,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}

	_, err := r.updateStatus(ctx, logger, store, condition)
	return err
}

// updateStatusWithReadyAndGeneration updates the status to indicate the store is ready
// with specified capabilities and records the observed generation
func (r *CommonReconciler) updateStatusWithReadyAndGeneration(ctx context.Context, logger logr.Logger, store StoreInterface, capabilities v1alpha1.SecretStoreCapabilities) error {
	condition := v1alpha1.SecretStoreStatusCondition{
		Type:               v1alpha1.SecretStoreReady,
		Status:             corev1.ConditionTrue,
		Reason:             v1alpha1.ReasonStoreValid,
		ObservedGeneration: store.GetGeneration(),
	}

	store.GetStatus().SetCapabilities(capabilities)
	_, err := r.updateStatus(ctx, logger, store, condition)
	return err
}

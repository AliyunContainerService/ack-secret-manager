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
	GetClientGeneration() int64
	SetClientGeneration(int64)
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

func (w *SecretStoreStatusWrapper) GetClientGeneration() int64 {
	return w.ClientGeneration
}

func (w *SecretStoreStatusWrapper) SetClientGeneration(generation int64) {
	w.ClientGeneration = generation
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

func (w *ClusterSecretStoreStatusWrapper) GetClientGeneration() int64 {
	return w.ClientGeneration
}

func (w *ClusterSecretStoreStatusWrapper) SetClientGeneration(generation int64) {
	w.ClientGeneration = generation
}

// CommonReconciler contains common logic for both SecretStore and ClusterSecretStore controllers
type CommonReconciler struct {
	client.Client
	RestConfig                  *rest.Config
	EnableCrossNamespaceAuthRef bool
}

// validateStoreSpec validates the common part of store spec
func (r *CommonReconciler) validateStoreSpec(kms, oos interface{}, storeType string) error {
	kmsProvider, kmsOk := kms.(*v1alpha1.KMSProvider)
	oosProvider, oosOk := oos.(*v1alpha1.OOSProvider)

	// Exactly one provider must be configured
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

// validateSecretStoreSpec validates the SecretStore spec
func (r *CommonReconciler) validateSecretStoreSpec(store StoreInterface) error {
	spec := store.GetSpec()
	if err := r.validateStoreSpec(spec.GetKMS(), spec.GetOOS(), "SecretStore"); err != nil {
		return err
	}

	// Cross-namespace references must stay in the store's namespace when disabled
	if !r.EnableCrossNamespaceAuthRef {
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

// validateClusterSecretStoreSpec validates the ClusterSecretStore spec
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
	for i, condition := range conditions {
		if condition.NamespaceSelector != nil {
			_, err := metav1.LabelSelectorAsSelector(condition.NamespaceSelector)
			if err != nil {
				return fmt.Errorf("invalid label selector in condition %d: %w", i, err)
			}
		}

		for j, regex := range condition.NamespaceRegexes {
			_, err := regexp.Compile(regex)
			if err != nil {
				return fmt.Errorf("invalid regex %s in condition %d regex %d: %w", regex, i, j, err)
			}
		}
	}

	return nil
}

// storeConfiguresKMS reports whether the store spec configures KMS by its
// inner auth block (an empty `kms: {}` block does not count), matching the
// branch decision in validateStoreSpec and recreateClient.
func storeConfiguresKMS(spec StoreSpecInterface) bool {
	return spec.GetKMS() != nil && spec.GetKMS().KMS != nil
}

// storeConfiguresOOS reports whether the store spec configures OOS by its
// inner auth block (an empty `oos: {}` block does not count), matching the
// branch decision in validateStoreSpec and recreateClient.
func storeConfiguresOOS(spec StoreSpecInterface) bool {
	return spec.GetOOS() != nil && spec.GetOOS().OOS != nil
}

// needRecreateClient checks whether the client must be recreated. The client
// registry is selected by the provider actually configured in the store spec
// (OOS clients live in OosClientMap, never KmsClientMap), so an OOS store
// with a registered client is not forced into a needless rebuild.
func (r *CommonReconciler) needRecreateClient(store StoreInterface, clientName string, generation int64, conditions []v1alpha1.SecretStoreStatusCondition, kmsProvider, oosProvider backend.Provider) bool {
	spec := store.GetSpec()
	var clientExists bool
	switch {
	case storeConfiguresKMS(spec):
		if kmsProvider != nil {
			client, _ := kmsProvider.GetClient(clientName)
			clientExists = client != nil
		}
	case storeConfiguresOOS(spec):
		if oosProvider != nil {
			client, _ := oosProvider.GetClient(clientName)
			clientExists = client != nil
		}
	}

	if !clientExists {
		return true
	}

	// No Ready condition yet: initial reconcile. The condition is located by
	// Type == SecretStoreReady so additional condition types can never break
	// the observed-generation comparison.
	observed, found := utils.ReadyConditionObservedGeneration(conditions)
	if !found {
		return true
	}

	// Generation change indicates a spec change
	if generation != observed {
		return true
	}

	return false
}

// recreateClient recreates the client for the store
func (r *CommonReconciler) recreateClient(ctx context.Context, log logr.Logger, clientName string, kmsProvider, oosProvider backend.Provider, store StoreInterface) error {
	// Clean up the plain clientName client plus every composite
	// ("clientName#endpoint") variant; composite variants are rebuilt on
	// demand in later ExternalSecret reconciles.
	kmsProvider.DeletePrefixed(clientName)
	oosProvider.DeletePrefixed(clientName)

	// kubeClient from rest.Config enables dynamic token acquisition
	var kubeClient kubernetes.Interface
	var err error
	if r.RestConfig != nil {
		kubeClient, err = kubernetes.NewForConfig(r.RestConfig)
		if err != nil {
			log.Error(err, "Failed to create kubernetes clientset from rest.Config")
			return err
		}
	}

	// Create a wrapper combining both clients
	wrapperClient := &backend.WrappedClient{
		Client:     r.Client,
		KubeClient: kubeClient,
	}

	// The branch decision uses the same inner-field check as validateStoreSpec
	// (an empty `kms: {}` block must not select the KMS branch), so any
	// validated configuration enters exactly one provider branch.
	spec := store.GetSpec()
	kmsConfigured := storeConfiguresKMS(spec)
	oosConfigured := storeConfiguresOOS(spec)
	if kmsConfigured {
		if oosConfigured {
			klog.Warningf("both KMS and OOS providers are configured for store %s/%s, KMS takes precedence",
				store.GetNamespace(), store.GetName())
		}
		return r.createKMSClient(ctx, log, store, wrapperClient, clientName, kmsProvider)
	} else if oosConfigured {
		return r.createOOSClient(ctx, log, store, wrapperClient, clientName, oosProvider)
	}

	// validateStoreSpec rejects provider-less specs before this point
	return fmt.Errorf("no valid provider found for store %s/%s", store.GetNamespace(), store.GetName())
}

// createKMSClient creates a KMS client
func (r *CommonReconciler) createKMSClient(ctx context.Context, log logr.Logger, store StoreInterface, wrapperClient *backend.WrappedClient, clientName string, kmsProvider backend.Provider) error {
	var secretClient backend.SecretClient
	var err error

	switch s := store.(type) {
	case *SecretStoreWrapper:
		secretClient, err = kmsProvider.NewClient(ctx, s.SecretStore, wrapperClient, "")
	case *ClusterSecretStoreWrapper:
		// Temporary SecretStore for compatibility with the provider interface
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
func (r *CommonReconciler) createOOSClient(ctx context.Context, log logr.Logger, store StoreInterface, wrapperClient *backend.WrappedClient, clientName string, oosProvider backend.Provider) error {
	var secretClient backend.SecretClient
	var err error

	switch s := store.(type) {
	case *SecretStoreWrapper:
		secretClient, err = oosProvider.NewClient(ctx, s.SecretStore, wrapperClient, "")
	case *ClusterSecretStoreWrapper:
		// Temporary SecretStore for compatibility with the provider interface
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

// setCondition sets a condition in the store status; LastTransitionTime is
// only refreshed when the condition actually transitions.
func (r *CommonReconciler) setCondition(store StoreInterface, condition v1alpha1.SecretStoreStatusCondition) {
	now := metav1.Now()
	condition.ObservedGeneration = store.GetGeneration()

	status := store.GetStatus()
	conditions := status.GetConditions()

	for i, c := range conditions {
		if c.Type == condition.Type {
			if c.Status == condition.Status && c.Reason == condition.Reason && c.Message == condition.Message {
				// Not transitioning: preserve the original timestamp
				condition.LastTransitionTime = c.LastTransitionTime
			} else {
				condition.LastTransitionTime = now
			}
			conditions[i] = condition
			return
		}
	}

	condition.LastTransitionTime = now
	conditions = append(conditions, condition)
	status.SetConditions(conditions)
}

// statusEqual compares two store statuses, ignoring LastTransitionTime so
// unchanged conditions do not trigger redundant status writes.
func (r *CommonReconciler) statusEqual(old, new StoreStatusInterface) bool {
	if old.GetCapabilities() != new.GetCapabilities() {
		return false
	}

	// ClientGeneration changes must always surface: it is the signal the
	// ExternalSecret reverse watch uses to detect client rebuilds on the
	// trigger-annotation path where metadata.generation stays unchanged.
	if old.GetClientGeneration() != new.GetClientGeneration() {
		return false
	}

	originConditions := new.GetConditions()
	destConditions := old.GetConditions()

	if len(originConditions) != len(destConditions) {
		return false
	}

	// Conditions are compared in order, ignoring LastTransitionTime
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

// handleDeletion handles the cleanup when the store is marked for deletion
func (r *CommonReconciler) handleDeletion(log logr.Logger, finalizers []string, secretStore client.Object, clientName string, kmsProvider, oosProvider backend.Provider, updateFunc func(client.Object) error) (reconcile.Result, error) {
	log.Info("Store is marked to be deleted")
	var finalizerName string
	switch secretStore.(type) {
	case *v1alpha1.SecretStore:
		finalizerName = secretFinalizer
	case *v1alpha1.ClusterSecretStore:
		finalizerName = clusterSecretFinalizer
	}

	// Clean up provider clients (plain and all composite variants) before
	// removing the finalizer; DeletePrefixed is idempotent with no failure
	// path, so clients cannot leak on the deletion path.
	kmsProvider.DeletePrefixed(clientName)
	oosProvider.DeletePrefixed(clientName)

	if utils.Contains(finalizers, finalizerName) {
		log.Info("removing finalizer", "currentFinalizers", finalizers)
		newFinalizers := utils.Remove(finalizers, finalizerName)

		secretStore.SetFinalizers(newFinalizers)
		err := updateFunc(secretStore)
		if err != nil {
			log.Error(err, "failed to update store when clean finalizers")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

// updateStatus updates the store status. Returns (true, nil) when the status
// was written, (false, nil) when already up-to-date, and (false, err) on failure.
// bumpClientGeneration marks a successful client recreation in this round.
// Invariant: the bump happens AFTER the old-status snapshot, callers persist
// the status write BEFORE clearing the trigger annotation (surfacing clearing
// failures as errors), and a failed write discards the in-memory bump. Hence
// a cleared annotation always implies the bump was persisted, the signal can
// never be lost on the annotation path, and every persisted recreation maps
// to exactly one increment.
func (r *CommonReconciler) updateStatus(ctx context.Context, logger logr.Logger, store StoreInterface, condition v1alpha1.SecretStoreStatusCondition, bumpClientGeneration bool) (bool, error) {
	var oldStatus StoreStatusInterface
	switch s := store.(type) {
	case *SecretStoreWrapper:
		old := s.Status.DeepCopy()
		oldStatus = &SecretStoreStatusWrapper{SecretStoreStatus: old}
	case *ClusterSecretStoreWrapper:
		old := s.Status.DeepCopy()
		oldStatus = &ClusterSecretStoreStatusWrapper{ClusterSecretStoreStatus: old}
	}

	if bumpClientGeneration {
		status := store.GetStatus()
		status.SetClientGeneration(status.GetClientGeneration() + 1)
		logger.Info("bumped store status clientGeneration after successful client recreation",
			"clientGeneration", status.GetClientGeneration())
	}

	r.setCondition(store, condition)

	statusEqual := r.statusEqual(oldStatus, store.GetStatus())
	// Force update when there were no conditions yet (status initialization)
	shouldUpdate := !statusEqual || len(oldStatus.GetConditions()) == 0

	if shouldUpdate {
		// Update the real object's status, not the wrapper
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

		// Fresh copy to avoid resource version conflicts
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

		switch t := freshObj.(type) {
		case *v1alpha1.SecretStore:
			modifiedStore := store.(*SecretStoreWrapper).SecretStore
			t.Status = *modifiedStore.Status.DeepCopy()
		case *v1alpha1.ClusterSecretStore:
			modifiedStore := store.(*ClusterSecretStoreWrapper).ClusterSecretStore
			t.Status = *modifiedStore.Status.DeepCopy()
		}

		// On conflict errors, let the workqueue retry with backoff instead of
		// blocking the reconcile goroutine.
		err := r.Status().Update(ctx, freshObj)
		if err == nil {
			logger.Info("successfully updated store status subresource")
			return true, nil
		}

		if errors.IsConflict(err) {
			logger.Info("conflict when updating status, will be retried by workqueue backoff", "error", err)
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

	return r.updateStatus(ctx, logger, store, condition, false)
}

// updateStatusWithError updates the status with an error condition
func (r *CommonReconciler) updateStatusWithError(ctx context.Context, logger logr.Logger, store StoreInterface, reason, message string) error {
	condition := v1alpha1.SecretStoreStatusCondition{
		Type:    v1alpha1.SecretStoreReady,
		Status:  corev1.ConditionFalse,
		Reason:  reason,
		Message: message,
	}

	_, err := r.updateStatus(ctx, logger, store, condition, false)
	return err
}

// updateStatusWithReadyAndGeneration updates the status to indicate the store
// is ready with the given capabilities, recording the observed generation.
// clientRecreated marks rounds that successfully rebuilt the backend client:
// status.clientGeneration is bumped so the ExternalSecret reverse watch
// observes the rebuild (see updateStatus).
func (r *CommonReconciler) updateStatusWithReadyAndGeneration(ctx context.Context, logger logr.Logger, store StoreInterface, capabilities v1alpha1.SecretStoreCapabilities, clientRecreated bool) error {
	condition := v1alpha1.SecretStoreStatusCondition{
		Type:               v1alpha1.SecretStoreReady,
		Status:             corev1.ConditionTrue,
		Reason:             v1alpha1.ReasonStoreValid,
		ObservedGeneration: store.GetGeneration(),
	}

	store.GetStatus().SetCapabilities(capabilities)
	_, err := r.updateStatus(ctx, logger, store, condition, clientRecreated)
	return err
}

package secret

import (
	"context"
	"reflect"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// SecretReconciler reconciles a Secret object for changes affecting SecretStore/ClusterSecretStore
type SecretReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
	// ProcessClusterSecretStore mirrors the --process-cluster-secret-store
	// flag: when false the ClusterSecretStore controller is disabled, so this
	// reconciler must not patch trigger annotations onto ClusterSecretStores
	// either (nobody would clear them, leaving referencing ExternalSecrets
	// stuck in the freshness-guard retry loop).
	ProcessClusterSecretStore bool
}

//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups=alibabacloud.com,resources=secretstores,verbs=get;list;update
//+kubebuilder:rbac:groups=alibabacloud.com,resources=clustersecretstores,verbs=get;list;update

func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("secret", req.NamespacedName)

	secret := &corev1.Secret{}
	err := r.Get(ctx, req.NamespacedName, secret)
	if err != nil {
		if errors.IsNotFound(err) {
			// The secret was deleted: reference matching only needs name/namespace,
			// so construct a synthetic object and still trigger referencing stores.
			log.Info("Secret not found, treating as a delete event for reference matching")
			secret = &corev1.Secret{}
			secret.Name = req.Name
			secret.Namespace = req.Namespace
		} else {
			return ctrl.Result{}, err
		}
	}

	// Find all stores referencing this secret
	secretStoreList := &api.SecretStoreList{}
	err = r.List(ctx, secretStoreList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, store := range secretStoreList.Items {
		if r.secretIsReferenced(secret, &store.Spec, store.Namespace) {
			// A non-empty trigger annotation already guarantees a pending
			// rebuild; re-patching a fresh value would force another full
			// rebuild+fan-out on every whole-loop retry.
			if store.Annotations[utils.TriggerReconcileAnnotation] != "" {
				log.Info("SecretStore already carries a pending trigger annotation, skipping redundant patch", "store", store.Name, "namespace", store.Namespace)
				continue
			}
			log.Info("Triggering reconcile for SecretStore", "store", store.Name, "namespace", store.Namespace)
			err := utils.PatchTriggerAnnotation(ctx, r.Client, &store)
			if err != nil {
				if errors.IsNotFound(err) {
					// The store was deleted between List and Patch: there is
					// nothing left to trigger, so skip without requeueing.
					log.Info("SecretStore already deleted, skipping trigger annotation patch", "store", store.Name, "namespace", store.Namespace)
					continue
				}
				log.Error(err, "Failed to trigger reconcile for SecretStore, will retry via workqueue", "store", store.Name, "namespace", store.Namespace)
				return ctrl.Result{}, err
			}
		}
	}

	if !r.ProcessClusterSecretStore {
		log.Info("ClusterSecretStore processing disabled, skipping trigger annotation patches for ClusterSecretStores")
		return ctrl.Result{}, nil
	}

	clusterSecretStoreList := &api.ClusterSecretStoreList{}
	err = r.List(ctx, clusterSecretStoreList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, store := range clusterSecretStoreList.Items {
		if r.secretIsReferencedByClusterStore(secret, &store.Spec) {
			// A non-empty trigger annotation already guarantees a pending
			// rebuild; re-patching a fresh value would force another full
			// rebuild+fan-out on every whole-loop retry.
			if store.Annotations[utils.TriggerReconcileAnnotation] != "" {
				log.Info("ClusterSecretStore already carries a pending trigger annotation, skipping redundant patch", "store", store.Name)
				continue
			}
			log.Info("Triggering reconcile for ClusterSecretStore", "store", store.Name)
			err := utils.PatchTriggerAnnotation(ctx, r.Client, &store)
			if err != nil {
				if errors.IsNotFound(err) {
					// The store was deleted between List and Patch: there is
					// nothing left to trigger, so skip without requeueing.
					log.Info("ClusterSecretStore already deleted, skipping trigger annotation patch", "store", store.Name)
					continue
				}
				log.Error(err, "Failed to trigger reconcile for ClusterSecretStore, will retry via workqueue", "store", store.Name)
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// secretIsReferenced reports whether the store spec references this secret;
// storeNamespace resolves references that omit the namespace field.
func (r *SecretReconciler) secretIsReferenced(secret *corev1.Secret, spec *api.SecretStoreSpec, storeNamespace string) bool {
	if spec.KMS != nil && spec.KMS.KMS != nil {
		if r.checkSecret(secret, spec.KMS.KMS.AccessKey, storeNamespace) ||
			r.checkSecret(secret, spec.KMS.KMS.AccessKeySecret, storeNamespace) {
			return true
		}
	}

	if spec.OOS != nil && spec.OOS.OOS != nil {
		if r.checkSecret(secret, spec.OOS.OOS.AccessKey, storeNamespace) ||
			r.checkSecret(secret, spec.OOS.OOS.AccessKeySecret, storeNamespace) {
			return true
		}
	}

	return false
}

// secretIsReferencedByClusterStore reports whether the cluster store spec
// references this secret. An empty reference namespace resolves to "" and
// never matches a namespaced secret.
func (r *SecretReconciler) secretIsReferencedByClusterStore(secret *corev1.Secret, spec *api.ClusterSecretStoreSpec) bool {
	if spec.KMS != nil && spec.KMS.KMS != nil {
		if r.checkSecret(secret, spec.KMS.KMS.AccessKey, "") ||
			r.checkSecret(secret, spec.KMS.KMS.AccessKeySecret, "") {
			return true
		}
	}

	if spec.OOS != nil && spec.OOS.OOS != nil {
		if r.checkSecret(secret, spec.OOS.OOS.AccessKey, "") ||
			r.checkSecret(secret, spec.OOS.OOS.AccessKeySecret, "") {
			return true
		}
	}

	return false
}

// checkSecret reports whether ref matches the secret; an omitted
// ref.Namespace defaults to storeNamespace, mirroring the auth chain.
func (r *SecretReconciler) checkSecret(secret *corev1.Secret, ref *api.SecretRef, storeNamespace string) bool {
	if ref == nil {
		return false
	}

	expectedNamespace := ref.Namespace
	if expectedNamespace == "" {
		expectedNamespace = storeNamespace
	}
	return secret.Name == ref.Name && secret.Namespace == expectedNamespace
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Secret{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				// only trigger on secret has data, to avoid triggering on irrelevant secret creations
				secret := e.Object.(*corev1.Secret)
				return len(secret.Data) > 0
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Only trigger on data changes
				oldSecret := e.ObjectOld.(*corev1.Secret)
				newSecret := e.ObjectNew.(*corev1.Secret)
				return !reflect.DeepEqual(oldSecret.Data, newSecret.Data)
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				return true
			},
		})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

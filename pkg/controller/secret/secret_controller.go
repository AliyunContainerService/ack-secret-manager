// pkg/controller/secret/secret_controller.go
package secret

import (
	"context"
	"fmt"
	"reflect"
	"time"

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
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/secretstore"
)

// SecretReconciler reconciles a Secret object for changes affecting SecretStore/ClusterSecretStore
type SecretReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
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
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Find all SecretStore and ClusterSecretStore objects that reference this secret
	secretStoreList := &api.SecretStoreList{}
	err = r.List(ctx, secretStoreList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	clusterSecretStoreList := &api.ClusterSecretStoreList{}
	err = r.List(ctx, clusterSecretStoreList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	// Trigger reconcile for each referencing store
	for _, store := range secretStoreList.Items {
		if r.secretIsReferenced(secret, &store.Spec) {
			log.Info("Triggering reconcile for SecretStore", "store", store.Name, "namespace", store.Namespace)
			err := r.triggerStoreReconcile(ctx, &store)
			if err != nil {
				log.Error(err, "Failed to trigger reconcile for SecretStore", "store", store.Name)
			}
		}
	}

	for _, store := range clusterSecretStoreList.Items {
		if r.secretIsReferencedByClusterStore(secret, &store.Spec) {
			log.Info("Triggering reconcile for ClusterSecretStore", "store", store.Name)
			err := r.triggerClusterStoreReconcile(ctx, &store)
			if err != nil {
				log.Error(err, "Failed to trigger reconcile for ClusterSecretStore", "store", store.Name)
			}
		}
	}

	return ctrl.Result{}, nil
}

// secretIsReferenced checks if a secret is referenced by the store spec
func (r *SecretReconciler) secretIsReferenced(secret *corev1.Secret, spec *api.SecretStoreSpec) bool {
	// Check KMS provider
	if spec.KMS != nil && spec.KMS.KMS != nil {
		if r.checkSecret(secret, spec.KMS.KMS.AccessKey) ||
			r.checkSecret(secret, spec.KMS.KMS.AccessKeySecret) {
			return true
		}
	}

	// Check OOS provider
	if spec.OOS != nil && spec.OOS.OOS != nil {
		if r.checkSecret(secret, spec.OOS.OOS.AccessKey) ||
			r.checkSecret(secret, spec.OOS.OOS.AccessKeySecret) {
			return true
		}
	}

	return false
}

// serviceAccountIsReferencedByClusterStore checks if a service account is referenced by the cluster store spec
func (r *SecretReconciler) secretIsReferencedByClusterStore(secret *corev1.Secret, spec *api.ClusterSecretStoreSpec) bool {
	// Check KMS provider
	if spec.KMS != nil && spec.KMS.KMS != nil {
		if r.checkSecret(secret, spec.KMS.KMS.AccessKey) ||
			r.checkSecret(secret, spec.KMS.KMS.AccessKeySecret) {
			return true
		}
	}

	// Check OOS provider
	if spec.OOS != nil && spec.OOS.OOS != nil {
		if r.checkSecret(secret, spec.OOS.OOS.AccessKey) ||
			r.checkSecret(secret, spec.OOS.OOS.AccessKeySecret) {
			return true
		}
	}

	return false
}

// checkSecret checks if a secret reference matches the given secret
func (r *SecretReconciler) checkSecret(secret *corev1.Secret, ref *api.SecretRef) bool {
	if ref == nil {
		return false
	}

	// For SecretStore, if ref.Namespace is not specified, it defaults to the store's namespace
	// But in practice, Secret always has a namespace specified when used with SecretStore
	return secret.Name == ref.Name && secret.Namespace == ref.Namespace
}

// triggerStoreReconcile triggers a reconcile for a SecretStore by updating its annotation
func (r *SecretReconciler) triggerStoreReconcile(ctx context.Context, store *api.SecretStore) error {
	updatedStore := store.DeepCopy()
	if updatedStore.Annotations == nil {
		updatedStore.Annotations = make(map[string]string)
	}
	updatedStore.Annotations[secretstore.TriggerReconcileAnnotation] = fmt.Sprintf("%d", time.Now().UnixNano())

	return r.Patch(ctx, updatedStore, client.MergeFrom(store))
}

// triggerClusterStoreReconcile triggers a reconcile for a ClusterSecretStore by updating its annotation
func (r *SecretReconciler) triggerClusterStoreReconcile(ctx context.Context, store *api.ClusterSecretStore) error {
	updatedStore := store.DeepCopy()
	if updatedStore.Annotations == nil {
		updatedStore.Annotations = make(map[string]string)
	}
	updatedStore.Annotations[secretstore.TriggerReconcileAnnotation] = fmt.Sprintf("%d", time.Now().UnixNano())

	return r.Patch(ctx, updatedStore, client.MergeFrom(store))
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

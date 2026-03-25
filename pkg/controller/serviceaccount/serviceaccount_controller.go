// pkg/controller/serviceaccount/serviceaccount_controller.go
package serviceaccount

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

const (
	// RoleARNAnnotation is the annotation key for Alibaba Cloud RAM role ARN
	RoleARNAnnotation = "ack.alibabacloud.com/role-arn"
)

// ServiceAccountReconciler reconciles a ServiceAccount object for changes affecting SecretStore/ClusterSecretStore
type ServiceAccountReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch
//+kubebuilder:rbac:groups=alibabacloud.com,resources=secretstores,verbs=get;list;update
//+kubebuilder:rbac:groups=alibabacloud.com,resources=clustersecretstores,verbs=get;list;update

func (r *ServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("serviceaccount", req.NamespacedName)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Get(ctx, req.NamespacedName, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Find all SecretStore and ClusterSecretStore objects that reference this service account
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
		if r.serviceAccountIsReferenced(serviceAccount, &store.Spec) {
			log.Info("Triggering reconcile for SecretStore", "store", store.Name, "namespace", store.Namespace)
			err := r.triggerStoreReconcile(ctx, &store)
			if err != nil {
				log.Error(err, "Failed to trigger reconcile for SecretStore", "store", store.Name)
			}
		}
	}

	for _, store := range clusterSecretStoreList.Items {
		if r.serviceAccountIsReferencedByClusterStore(serviceAccount, &store.Spec) {
			log.Info("Triggering reconcile for ClusterSecretStore", "store", store.Name)
			err := r.triggerClusterStoreReconcile(ctx, &store)
			if err != nil {
				log.Error(err, "Failed to trigger reconcile for ClusterSecretStore", "store", store.Name)
			}
		}
	}

	return ctrl.Result{}, nil
}

// serviceAccountIsReferenced checks if a service account is referenced by the store spec
func (r *ServiceAccountReconciler) serviceAccountIsReferenced(sa *corev1.ServiceAccount, spec *api.SecretStoreSpec) bool {
	// Check KMS provider
	if spec.KMS != nil && spec.KMS.KMS != nil {
		if r.checkServiceAccount(sa, spec.KMS.KMS.ServiceAccountRef) {
			return true
		}
	}

	// Check OOS provider
	if spec.OOS != nil && spec.OOS.OOS != nil {
		if r.checkServiceAccount(sa, spec.OOS.OOS.ServiceAccountRef) {
			return true
		}
	}

	return false
}

// serviceAccountIsReferencedByClusterStore checks if a service account is referenced by the cluster store spec
func (r *ServiceAccountReconciler) serviceAccountIsReferencedByClusterStore(sa *corev1.ServiceAccount, spec *api.ClusterSecretStoreSpec) bool {
	// Check KMS provider
	if spec.KMS != nil && spec.KMS.KMS != nil {
		if r.checkServiceAccount(sa, spec.KMS.KMS.ServiceAccountRef) {
			return true
		}
	}

	// Check OOS provider
	if spec.OOS != nil && spec.OOS.OOS != nil {
		if r.checkServiceAccount(sa, spec.OOS.OOS.ServiceAccountRef) {
			return true
		}
	}

	return false
}

// checkServiceAccount checks if a service account reference matches the given service account
func (r *ServiceAccountReconciler) checkServiceAccount(sa *corev1.ServiceAccount, ref *api.ServiceAccountRef) bool {
	if ref == nil {
		return false
	}

	// For SecretStore, if ref.Namespace is not specified, it defaults to the store's namespace
	// For ClusterSecretStore, ref.Namespace is always specified
	expectedNamespace := sa.Namespace
	if ref.Namespace != "" {
		expectedNamespace = ref.Namespace
	}

	return sa.Name == ref.Name && sa.Namespace == expectedNamespace
}

// triggerStoreReconcile triggers a reconcile for a SecretStore by updating its annotation
func (r *ServiceAccountReconciler) triggerStoreReconcile(ctx context.Context, store *api.SecretStore) error {
	updatedStore := store.DeepCopy()
	if updatedStore.Annotations == nil {
		updatedStore.Annotations = make(map[string]string)
	}
	updatedStore.Annotations[secretstore.TriggerReconcileAnnotation] = fmt.Sprintf("%d", time.Now().UnixNano())

	return r.Patch(ctx, updatedStore, client.MergeFrom(store))
}

// triggerClusterStoreReconcile triggers a reconcile for a ClusterSecretStore by updating its annotation
func (r *ServiceAccountReconciler) triggerClusterStoreReconcile(ctx context.Context, store *api.ClusterSecretStore) error {
	updatedStore := store.DeepCopy()
	if updatedStore.Annotations == nil {
		updatedStore.Annotations = make(map[string]string)
	}
	updatedStore.Annotations[secretstore.TriggerReconcileAnnotation] = fmt.Sprintf("%d", time.Now().UnixNano())

	return r.Patch(ctx, updatedStore, client.MergeFrom(store))
}

// hasRoleARNAnnotation checks if a ServiceAccount has the role-arn annotation
func (r *ServiceAccountReconciler) hasRoleARNAnnotation(sa *corev1.ServiceAccount) bool {
	if sa.Annotations == nil {
		return false
	}
	_, exists := sa.Annotations[RoleARNAnnotation]
	return exists
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceAccountReconciler) SetupWithManager(mgr ctrl.Manager, reconcileCount int) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ServiceAccount{}, builder.WithPredicates(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				// Only trigger on creation if the ServiceAccount has the role-arn annotation
				sa := e.Object.(*corev1.ServiceAccount)
				return r.hasRoleARNAnnotation(sa)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Only trigger on updates if the ServiceAccount has the role-arn annotation
				// and annotations have changed
				oldSA := e.ObjectOld.(*corev1.ServiceAccount)
				newSA := e.ObjectNew.(*corev1.ServiceAccount)

				// Check if either old or new ServiceAccount has the annotation
				hasAnnotation := r.hasRoleARNAnnotation(oldSA) || r.hasRoleARNAnnotation(newSA)
				annotationsChanged := !reflect.DeepEqual(oldSA.Annotations, newSA.Annotations)

				return hasAnnotation && annotationsChanged
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				// Always trigger on deletion if the ServiceAccount had the role-arn annotation
				sa := e.Object.(*corev1.ServiceAccount)
				return r.hasRoleARNAnnotation(sa)
			},
		})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

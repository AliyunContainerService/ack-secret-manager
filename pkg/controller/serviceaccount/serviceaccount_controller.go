package serviceaccount

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

const (
	// RoleARNAnnotation is the annotation key for Alibaba Cloud RAM role ARN
	RoleARNAnnotation = "ack.alibabacloud.com/role-arn"
)

// ServiceAccountReconciler reconciles a ServiceAccount object for changes affecting SecretStore/ClusterSecretStore
type ServiceAccountReconciler struct {
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

//+kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch
//+kubebuilder:rbac:groups=alibabacloud.com,resources=secretstores,verbs=get;list;update
//+kubebuilder:rbac:groups=alibabacloud.com,resources=clustersecretstores,verbs=get;list;update

func (r *ServiceAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("serviceaccount", req.NamespacedName)

	serviceAccount := &corev1.ServiceAccount{}
	err := r.Get(ctx, req.NamespacedName, serviceAccount)
	if err != nil {
		if errors.IsNotFound(err) {
			// The service account was deleted: reference matching only needs
			// name/namespace, so construct a synthetic object and still trigger
			// referencing stores.
			log.Info("ServiceAccount not found, treating as a delete event for reference matching")
			serviceAccount = &corev1.ServiceAccount{}
			serviceAccount.Name = req.Name
			serviceAccount.Namespace = req.Namespace
		} else {
			return ctrl.Result{}, err
		}
	}

	// Find all stores referencing this service account
	secretStoreList := &api.SecretStoreList{}
	err = r.List(ctx, secretStoreList, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	for _, store := range secretStoreList.Items {
		if r.serviceAccountIsReferenced(serviceAccount, &store.Spec, store.Namespace) {
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
		if r.serviceAccountIsReferencedByClusterStore(serviceAccount, &store.Spec) {
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

// serviceAccountIsReferenced reports whether the store spec references this
// service account; storeNamespace resolves references omitting the namespace.
func (r *ServiceAccountReconciler) serviceAccountIsReferenced(sa *corev1.ServiceAccount, spec *api.SecretStoreSpec, storeNamespace string) bool {
	if spec.KMS != nil && spec.KMS.KMS != nil {
		if r.checkServiceAccount(sa, spec.KMS.KMS.ServiceAccountRef, storeNamespace) {
			return true
		}
	}

	if spec.OOS != nil && spec.OOS.OOS != nil {
		if r.checkServiceAccount(sa, spec.OOS.OOS.ServiceAccountRef, storeNamespace) {
			return true
		}
	}

	return false
}

// serviceAccountIsReferencedByClusterStore reports whether the cluster store
// spec references this service account; an empty reference namespace resolves
// to "" and never matches a namespaced service account.
func (r *ServiceAccountReconciler) serviceAccountIsReferencedByClusterStore(sa *corev1.ServiceAccount, spec *api.ClusterSecretStoreSpec) bool {
	if spec.KMS != nil && spec.KMS.KMS != nil {
		if r.checkServiceAccount(sa, spec.KMS.KMS.ServiceAccountRef, "") {
			return true
		}
	}

	if spec.OOS != nil && spec.OOS.OOS != nil {
		if r.checkServiceAccount(sa, spec.OOS.OOS.ServiceAccountRef, "") {
			return true
		}
	}

	return false
}

// checkServiceAccount reports whether ref matches the service account; an
// omitted ref.Namespace defaults to storeNamespace, mirroring the auth chain.
func (r *ServiceAccountReconciler) checkServiceAccount(sa *corev1.ServiceAccount, ref *api.ServiceAccountRef, storeNamespace string) bool {
	if ref == nil {
		return false
	}

	expectedNamespace := ref.Namespace
	if expectedNamespace == "" {
		expectedNamespace = storeNamespace
	}

	return sa.Name == ref.Name && sa.Namespace == expectedNamespace
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
				// Trigger only when annotations changed on an SA carrying (or
				// previously carrying) the role-arn annotation
				oldSA := e.ObjectOld.(*corev1.ServiceAccount)
				newSA := e.ObjectNew.(*corev1.ServiceAccount)

				hasAnnotation := r.hasRoleARNAnnotation(oldSA) || r.hasRoleARNAnnotation(newSA)
				annotationsChanged := !reflect.DeepEqual(oldSA.Annotations, newSA.Annotations)

				return hasAnnotation && annotationsChanged
			},
			DeleteFunc: func(e event.DeleteEvent) bool {
				sa := e.Object.(*corev1.ServiceAccount)
				return r.hasRoleARNAnnotation(sa)
			},
		})).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: reconcileCount,
		}).
		Complete(r)
}

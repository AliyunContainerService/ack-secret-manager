package serviceaccount

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/controller/secretstore"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add api to scheme: %v", err)
	}
	return scheme
}

// kmsStoreSpec returns a SecretStoreSpec whose KMS serviceAccountRef omits the
// namespace field (defaults to the store's own namespace in the auth chain).
func kmsStoreSpec(saName string) api.SecretStoreSpec {
	return api.SecretStoreSpec{
		KMS: &api.KMSProvider{
			KMS: &api.KMSAuth{
				ServiceAccountRef: &api.ServiceAccountRef{Name: saName},
			},
		},
	}
}

func TestCheckServiceAccount(t *testing.T) {
	r := &ServiceAccountReconciler{}

	tests := []struct {
		name           string
		sa             *corev1.ServiceAccount
		ref            *api.ServiceAccountRef
		storeNamespace string
		want           bool
	}{
		{
			name:           "omitted namespace matches service account in store namespace",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns1"}},
			ref:            &api.ServiceAccountRef{Name: "rrsa-sa"},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "omitted namespace does not match service account in other namespace",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns2"}},
			ref:            &api.ServiceAccountRef{Name: "rrsa-sa"},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "explicit namespace matches",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns2"}},
			ref:            &api.ServiceAccountRef{Name: "rrsa-sa", Namespace: "ns2"},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "explicit namespace mismatch",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns1"}},
			ref:            &api.ServiceAccountRef{Name: "rrsa-sa", Namespace: "ns2"},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "name mismatch",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns1"}},
			ref:            &api.ServiceAccountRef{Name: "rrsa-sa"},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "nil ref",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns1"}},
			ref:            nil,
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "cluster store with omitted namespace never matches namespaced service account",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns1"}},
			ref:            &api.ServiceAccountRef{Name: "rrsa-sa"},
			storeNamespace: "",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.checkServiceAccount(tt.sa, tt.ref, tt.storeNamespace); got != tt.want {
				t.Errorf("checkServiceAccount() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReconcile_OmittedNamespaceMatchesSameNamespaceServiceAccount covers the
// case where a SecretStore omits the namespace of its serviceAccountRef and the
// referenced service account changes in the store's own namespace.
func TestReconcile_OmittedNamespaceMatchesSameNamespaceServiceAccount(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-a", Namespace: "ns1"},
		Spec:       kmsStoreSpec("rrsa-sa"),
	}
	// Same SA name in another namespace must NOT trigger store-a.
	unrelatedStore := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-b", Namespace: "ns2"},
		Spec:       kmsStoreSpec("rrsa-sa"),
	}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rrsa-sa",
			Namespace: "ns1",
			Annotations: map[string]string{
				RoleARNAnnotation: "acs:ram::123:role/test",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(store, unrelatedStore, sa).Build()
	r := &ServiceAccountReconciler{Client: cl, Scheme: scheme, Log: logr.Discard()}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "rrsa-sa"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	updated := &api.SecretStore{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "store-a"}, updated); err != nil {
		t.Fatalf("failed to get store-a: %v", err)
	}
	if _, ok := updated.Annotations[secretstore.TriggerReconcileAnnotation]; !ok {
		t.Errorf("store-a missing trigger annotation, annotations = %v", updated.Annotations)
	}

	// An SA event in ns1 must not trigger store-b (ns2) whose ref omits namespace.
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns2", Name: "store-b"}, updated); err != nil {
		t.Fatalf("failed to get store-b: %v", err)
	}
	if _, ok := updated.Annotations[secretstore.TriggerReconcileAnnotation]; ok {
		t.Errorf("store-b unexpectedly triggered by SA in another namespace, annotations = %v", updated.Annotations)
	}
}

// oosStoreSpec returns a SecretStoreSpec whose OOS serviceAccountRef omits
// the namespace field (defaults to the store's own namespace in the auth chain).
func oosStoreSpec(saName string) api.SecretStoreSpec {
	return api.SecretStoreSpec{
		OOS: &api.OOSProvider{
			OOS: &api.OOSAuth{
				ServiceAccountRef: &api.ServiceAccountRef{Name: saName},
			},
		},
	}
}

// TestServiceAccountIsReferenced_OOSProvider covers the OOS provider branch of
// serviceAccountIsReferenced, which existing KMS-only tests never reach.
func TestServiceAccountIsReferenced_OOSProvider(t *testing.T) {
	r := &ServiceAccountReconciler{}

	tests := []struct {
		name           string
		sa             *corev1.ServiceAccount
		spec           *api.SecretStoreSpec
		storeNamespace string
		want           bool
	}{
		{
			name:           "OOS serviceAccountRef matches SA in store namespace",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{ServiceAccountRef: &api.ServiceAccountRef{Name: "rrsa-sa"}}}},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "OOS serviceAccountRef explicit namespace matches",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns2"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{ServiceAccountRef: &api.ServiceAccountRef{Name: "rrsa-sa", Namespace: "ns2"}}}},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "OOS serviceAccountRef in different namespace does not match",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns2"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{ServiceAccountRef: &api.ServiceAccountRef{Name: "rrsa-sa"}}}},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "OOS serviceAccountRef name mismatch does not match",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{ServiceAccountRef: &api.ServiceAccountRef{Name: "rrsa-sa"}}}},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "OOS nil serviceAccountRef does not match",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{}}},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "empty outer OOS block does not match",
			sa:             &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "rrsa-sa", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{}},
			storeNamespace: "ns1",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.serviceAccountIsReferenced(tt.sa, tt.spec, tt.storeNamespace); got != tt.want {
				t.Errorf("serviceAccountIsReferenced() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestHasRoleARNAnnotation covers the annotation decision function that backs
// every predicate in SetupWithManager (Create/Update/Delete all gate on it).
// The predicate closures themselves are defined inline inside SetupWithManager
// and cannot be exercised without a live manager, so the shared decision
// function is covered exhaustively instead.
func TestHasRoleARNAnnotation(t *testing.T) {
	r := &ServiceAccountReconciler{}

	tests := []struct {
		name string
		sa   *corev1.ServiceAccount
		want bool
	}{
		{
			name: "nil annotations",
			sa:   &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "sa", Namespace: "ns1"}},
			want: false,
		},
		{
			name: "empty annotations",
			sa: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Name: "sa", Namespace: "ns1", Annotations: map[string]string{},
			}},
			want: false,
		},
		{
			name: "other annotations only",
			sa: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Name: "sa", Namespace: "ns1",
				Annotations: map[string]string{"other": "value"},
			}},
			want: false,
		},
		{
			name: "role-arn annotation present",
			sa: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Name: "sa", Namespace: "ns1",
				Annotations: map[string]string{RoleARNAnnotation: "acs:ram::123:role/test"},
			}},
			want: true,
		},
		{
			name: "role-arn annotation present with empty value still counts",
			sa: &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
				Name: "sa", Namespace: "ns1",
				Annotations: map[string]string{RoleARNAnnotation: ""},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.hasRoleARNAnnotation(tt.sa); got != tt.want {
				t.Errorf("hasRoleARNAnnotation() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReconcile_OOSProviderStoreTriggered covers the reconcile path for a
// SecretStore that references a service account via the OOS provider.
func TestReconcile_OOSProviderStoreTriggered(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-oos", Namespace: "ns1"},
		Spec:       oosStoreSpec("rrsa-sa"),
	}
	// Same OOS ref in another namespace must stay untouched.
	unrelatedStore := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-oos-b", Namespace: "ns2"},
		Spec:       oosStoreSpec("rrsa-sa"),
	}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rrsa-sa",
			Namespace: "ns1",
			Annotations: map[string]string{
				RoleARNAnnotation: "acs:ram::123:role/test",
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(store, unrelatedStore, sa).Build()
	r := &ServiceAccountReconciler{Client: cl, Scheme: scheme, Log: logr.Discard()}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "rrsa-sa"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	updated := &api.SecretStore{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "store-oos"}, updated); err != nil {
		t.Fatalf("failed to get store-oos: %v", err)
	}
	if _, ok := updated.Annotations[secretstore.TriggerReconcileAnnotation]; !ok {
		t.Errorf("store-oos missing trigger annotation, annotations = %v", updated.Annotations)
	}

	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns2", Name: "store-oos-b"}, updated); err != nil {
		t.Fatalf("failed to get store-oos-b: %v", err)
	}
	if _, ok := updated.Annotations[secretstore.TriggerReconcileAnnotation]; ok {
		t.Errorf("store-oos-b unexpectedly triggered, annotations = %v", updated.Annotations)
	}
}

// TestReconcile_DeletedServiceAccountTriggersStoreReconcile covers the delete
// path: the service account no longer exists, but stores referencing it must
// still be triggered.
func TestReconcile_DeletedServiceAccountTriggersStoreReconcile(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-a", Namespace: "ns1"},
		Spec:       kmsStoreSpec("rrsa-sa"),
	}
	clusterStore := &api.ClusterSecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-store"},
		Spec: api.ClusterSecretStoreSpec{
			KMS: &api.KMSProvider{
				KMS: &api.KMSAuth{
					ServiceAccountRef: &api.ServiceAccountRef{Name: "rrsa-sa", Namespace: "ns1"},
				},
			},
		},
	}
	// No ServiceAccount object: it has been deleted.
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(store, clusterStore).Build()
	r := &ServiceAccountReconciler{Client: cl, Scheme: scheme, Log: logr.Discard()}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "rrsa-sa"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	updated := &api.SecretStore{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "store-a"}, updated); err != nil {
		t.Fatalf("failed to get store-a: %v", err)
	}
	if _, ok := updated.Annotations[secretstore.TriggerReconcileAnnotation]; !ok {
		t.Errorf("store-a missing trigger annotation after SA deletion, annotations = %v", updated.Annotations)
	}

	updatedCluster := &api.ClusterSecretStore{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "cluster-store"}, updatedCluster); err != nil {
		t.Fatalf("failed to get cluster-store: %v", err)
	}
	if _, ok := updatedCluster.Annotations[secretstore.TriggerReconcileAnnotation]; !ok {
		t.Errorf("cluster-store missing trigger annotation after SA deletion, annotations = %v", updatedCluster.Annotations)
	}
}

// TestReconcile_DeletedUnreferencedServiceAccountTriggersNothing ensures a
// deleted service account that no store references does not trigger any store.
func TestReconcile_DeletedUnreferencedServiceAccountTriggersNothing(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-a", Namespace: "ns1"},
		Spec:       kmsStoreSpec("rrsa-sa"),
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(store).Build()
	r := &ServiceAccountReconciler{Client: cl, Scheme: scheme, Log: logr.Discard()}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "unrelated"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	updated := &api.SecretStore{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "store-a"}, updated); err != nil {
		t.Fatalf("failed to get store-a: %v", err)
	}
	if _, ok := updated.Annotations[secretstore.TriggerReconcileAnnotation]; ok {
		t.Errorf("store-a unexpectedly triggered, annotations = %v", updated.Annotations)
	}
}

package secret

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

// kmsStoreSpec returns a SecretStoreSpec whose KMS accessKey reference omits
// the namespace field (defaults to the store's own namespace in the auth chain).
func kmsStoreSpec(secretName string) api.SecretStoreSpec {
	return api.SecretStoreSpec{
		KMS: &api.KMSProvider{
			KMS: &api.KMSAuth{
				AccessKey: &api.SecretRef{
					Name: secretName,
					Key:  "accessKeyId",
				},
				AccessKeySecret: &api.SecretRef{
					Name: secretName + "-sk",
					Key:  "accessKeySecret",
				},
			},
		},
	}
}

func TestCheckSecret(t *testing.T) {
	r := &SecretReconciler{}

	tests := []struct {
		name           string
		secret         *corev1.Secret
		ref            *api.SecretRef
		storeNamespace string
		want           bool
	}{
		{
			name:           "omitted namespace matches secret in store namespace",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns1"}},
			ref:            &api.SecretRef{Name: "cred", Key: "k"},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "omitted namespace does not match secret in other namespace",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns2"}},
			ref:            &api.SecretRef{Name: "cred", Key: "k"},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "explicit namespace matches",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns2"}},
			ref:            &api.SecretRef{Name: "cred", Namespace: "ns2", Key: "k"},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "explicit namespace mismatch",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns1"}},
			ref:            &api.SecretRef{Name: "cred", Namespace: "ns2", Key: "k"},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "name mismatch",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns1"}},
			ref:            &api.SecretRef{Name: "cred", Key: "k"},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "nil ref",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns1"}},
			ref:            nil,
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "cluster store with omitted namespace never matches namespaced secret",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns1"}},
			ref:            &api.SecretRef{Name: "cred", Key: "k"},
			storeNamespace: "",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.checkSecret(tt.secret, tt.ref, tt.storeNamespace); got != tt.want {
				t.Errorf("checkSecret() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReconcile_OmittedNamespaceMatchesSameNamespaceSecret covers the case where
// a SecretStore omits the namespace of its credential secret reference and the
// referenced secret changes in the store's own namespace.
func TestReconcile_OmittedNamespaceMatchesSameNamespaceSecret(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-a", Namespace: "ns1"},
		Spec:       kmsStoreSpec("cred"),
	}
	// Unreferencing store in another namespace must stay untouched.
	unrelatedStore := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-b", Namespace: "ns2"},
		Spec:       kmsStoreSpec("cred"),
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns1"},
		Data:       map[string][]byte{"accessKeyId": []byte("ak")},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(store, unrelatedStore, secret).Build()
	r := &SecretReconciler{Client: cl, Scheme: scheme, Log: logr.Discard()}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "cred"},
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

	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns2", Name: "store-b"}, updated); err != nil {
		t.Fatalf("failed to get store-b: %v", err)
	}
	if _, ok := updated.Annotations[secretstore.TriggerReconcileAnnotation]; ok {
		t.Errorf("store-b unexpectedly triggered, annotations = %v", updated.Annotations)
	}
}

// oosStoreSpec returns a SecretStoreSpec whose OOS accessKey/accessKeySecret
// references omit the namespace field (defaults to the store's own namespace
// in the auth chain).
func oosStoreSpec(secretName string) api.SecretStoreSpec {
	return api.SecretStoreSpec{
		OOS: &api.OOSProvider{
			OOS: &api.OOSAuth{
				AccessKey: &api.SecretRef{
					Name: secretName,
					Key:  "accessKeyId",
				},
				AccessKeySecret: &api.SecretRef{
					Name: secretName + "-sk",
					Key:  "accessKeySecret",
				},
			},
		},
	}
}

// TestSecretIsReferenced_OOSProvider covers the OOS provider branch of
// secretIsReferenced, which existing KMS-only tests never reach.
func TestSecretIsReferenced_OOSProvider(t *testing.T) {
	r := &SecretReconciler{}

	tests := []struct {
		name           string
		secret         *corev1.Secret
		spec           *api.SecretStoreSpec
		storeNamespace string
		want           bool
	}{
		{
			name:           "OOS accessKey matches secret in store namespace",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{AccessKey: &api.SecretRef{Name: "cred", Key: "accessKeyId"}}}},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "OOS accessKeySecret matches secret in store namespace",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred-sk", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{AccessKeySecret: &api.SecretRef{Name: "cred-sk", Key: "accessKeySecret"}}}},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "OOS accessKey explicit namespace matches",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns2"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{AccessKey: &api.SecretRef{Name: "cred", Namespace: "ns2", Key: "accessKeyId"}}}},
			storeNamespace: "ns1",
			want:           true,
		},
		{
			name:           "OOS refs in different namespace do not match",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns2"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{AccessKey: &api.SecretRef{Name: "cred", Key: "accessKeyId"}, AccessKeySecret: &api.SecretRef{Name: "cred-sk", Key: "accessKeySecret"}}}},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "OOS ref name mismatch does not match",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{AccessKey: &api.SecretRef{Name: "cred", Key: "accessKeyId"}, AccessKeySecret: &api.SecretRef{Name: "cred-sk", Key: "accessKeySecret"}}}},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "OOS refs both nil does not match",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{OOS: &api.OOSAuth{}}},
			storeNamespace: "ns1",
			want:           false,
		},
		{
			name:           "empty outer OOS block does not match",
			secret:         &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cred", Namespace: "ns1"}},
			spec:           &api.SecretStoreSpec{OOS: &api.OOSProvider{}},
			storeNamespace: "ns1",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.secretIsReferenced(tt.secret, tt.spec, tt.storeNamespace); got != tt.want {
				t.Errorf("secretIsReferenced() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReconcile_OOSProviderStoreTriggered covers the reconcile path for a
// SecretStore that uses the OOS provider instead of KMS.
func TestReconcile_OOSProviderStoreTriggered(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-oos", Namespace: "ns1"},
		Spec:       oosStoreSpec("cred"),
	}
	// Same OOS refs in another namespace must stay untouched.
	unrelatedStore := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-oos-b", Namespace: "ns2"},
		Spec:       oosStoreSpec("cred"),
	}
	// The accessKeySecret reference is matched instead of accessKey here.
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "cred-sk", Namespace: "ns1"},
		Data:       map[string][]byte{"accessKeySecret": []byte("sk")},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(store, unrelatedStore, secret).Build()
	r := &SecretReconciler{Client: cl, Scheme: scheme, Log: logr.Discard()}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "cred-sk"},
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

// TestReconcile_DeletedSecretTriggersStoreReconcile covers the delete path:
// the secret no longer exists, but stores referencing it must still be triggered.
func TestReconcile_DeletedSecretTriggersStoreReconcile(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-a", Namespace: "ns1"},
		Spec:       kmsStoreSpec("cred"),
	}
	clusterStore := &api.ClusterSecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-store"},
		Spec: api.ClusterSecretStoreSpec{
			KMS: &api.KMSProvider{
				KMS: &api.KMSAuth{
					AccessKey: &api.SecretRef{Name: "cred", Namespace: "ns1", Key: "accessKeyId"},
				},
			},
		},
	}
	// No secret object: it has been deleted.
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(store, clusterStore).Build()
	r := &SecretReconciler{Client: cl, Scheme: scheme, Log: logr.Discard()}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "ns1", Name: "cred"},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}

	updated := &api.SecretStore{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns1", Name: "store-a"}, updated); err != nil {
		t.Fatalf("failed to get store-a: %v", err)
	}
	if _, ok := updated.Annotations[secretstore.TriggerReconcileAnnotation]; !ok {
		t.Errorf("store-a missing trigger annotation after secret deletion, annotations = %v", updated.Annotations)
	}

	updatedCluster := &api.ClusterSecretStore{}
	if err := cl.Get(context.Background(), types.NamespacedName{Name: "cluster-store"}, updatedCluster); err != nil {
		t.Fatalf("failed to get cluster-store: %v", err)
	}
	if _, ok := updatedCluster.Annotations[secretstore.TriggerReconcileAnnotation]; !ok {
		t.Errorf("cluster-store missing trigger annotation after secret deletion, annotations = %v", updatedCluster.Annotations)
	}
}

// TestReconcile_DeletedUnreferencedSecretTriggersNothing ensures a deleted secret
// that no store references does not trigger any store.
func TestReconcile_DeletedUnreferencedSecretTriggersNothing(t *testing.T) {
	scheme := newTestScheme(t)

	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "store-a", Namespace: "ns1"},
		Spec:       kmsStoreSpec("cred"),
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(store).Build()
	r := &SecretReconciler{Client: cl, Scheme: scheme, Log: logr.Discard()}

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

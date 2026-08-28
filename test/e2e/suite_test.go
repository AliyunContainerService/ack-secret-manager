// suite_test.go - E2E test suite
package e2e

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	k8sClient client.Client
	clientset *kubernetes.Clientset
	ctx       context.Context
	cancel    context.CancelFunc
)

const (
	ResourceSecretStore        = "SecretStore"
	ResourceClusterSecretStore = "ClusterSecretStore"

	// ackSecretManagerDeploymentName is the name of the ack-secret-manager Deployment.
	ackSecretManagerDeploymentName = "ack-secret-manager"
	// ackSecretManagerNamespace is the namespace where ack-secret-manager is deployed.
	ackSecretManagerNamespace = "kube-system"
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Set up context
	ctx, cancel = context.WithCancel(context.TODO())

	// Register built-in Kubernetes API types in the scheme
	scheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	// Register API types in the scheme
	err = api.AddToScheme(scheme)
	Expect(err).NotTo(HaveOccurred())

	// Get k8s config
	cfg, err := getKubeConfig()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	// Create client with the registered scheme
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Create typed clientset for Deployment patch operations
	clientset, err = kubernetes.NewForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
	Expect(clientset).NotTo(BeNil())

	// Ensure CRDs are installed
	ensureCRDsInstalled(ctx)

	// Set up global test resources using ResourceManager
	accountID := os.Getenv("ALIBABA_CLOUD_ACCOUNT_ID")
	clusterID := os.Getenv("CLUSTER_ID")

	if accountID == "" || clusterID == "" {
		Fail("Required environment variables for ResourceManager are not set: ALIBABA_CLOUD_ACCOUNT_ID, CLUSTER_ID")
	}

	GlobalResourceManager, err = NewResourceManager(accountID, clusterID)
	Expect(err).NotTo(HaveOccurred())

	// Configure target account (Account B) credentials for cross-account testing
	// These are optional - if not set, cross-account resource creation is skipped
	// gracefully and cross-account tests are Skipped
	remoteAccountID := os.Getenv("CROSS_ACCOUNT_ID")
	remoteAccessKeyID := os.Getenv("CROSS_ACCOUNT_ACCESS_KEY_ID")
	remoteAccessKeySecret := os.Getenv("CROSS_ACCOUNT_ACCESS_KEY_SECRET")
	remoteKMSKeyID := os.Getenv("CROSS_ACCOUNT_KMS_KEY_ID")
	remoteKMSInstanceID := os.Getenv("CROSS_ACCOUNT_KMS_INSTANCE_ID")

	// Backward-compatibility hint: the cross-account env vars were renamed
	// from REMOTE_* to CROSS_ACCOUNT_*. When the old names are still set but
	// the new CROSS_ACCOUNT_ID is absent, print a prominent warning so users
	// know their old settings are ignored; do not Fail.
	if remoteAccountID == "" && (os.Getenv("REMOTE_ACCOUNT_ID") != "" || os.Getenv("REMOTE_ACCESS_KEY_ID") != "") {
		GinkgoWriter.Printf("WARNING: cross-account env vars have been renamed from REMOTE_* to CROSS_ACCOUNT_* " +
			"(CROSS_ACCOUNT_ID, CROSS_ACCOUNT_ACCESS_KEY_ID, CROSS_ACCOUNT_ACCESS_KEY_SECRET, " +
			"CROSS_ACCOUNT_KMS_KEY_ID, CROSS_ACCOUNT_KMS_INSTANCE_ID). Detected REMOTE_* variables are set but " +
			"CROSS_ACCOUNT_ID is not, so the REMOTE_* values are IGNORED. Please migrate to the CROSS_ACCOUNT_* names.\n")
	}

	if remoteAccountID != "" && remoteAccessKeyID != "" && remoteAccessKeySecret != "" {
		By("configuring target account credentials for cross-account testing")
		err = GlobalResourceManager.SetRemoteAccountCredentials(remoteAccountID, remoteAccessKeyID, remoteAccessKeySecret, remoteKMSKeyID, remoteKMSInstanceID)
		Expect(err).NotTo(HaveOccurred())
	} else {
		By("cross-account credentials not configured, skipping cross-account resource creation (cross-account tests will be Skipped)")
	}

	err = GlobalResourceManager.SetupTestResources(ctx)
	Expect(err).NotTo(HaveOccurred())

	// Normalize the Deployment args baseline BEFORE any spec runs: a
	// hard-killed run (e.g. a CI timeout kill) can skip the per-spec
	// DeferCleanup restores and leave test-only flags such as
	// --disable-polling=true on the shared cluster. Without this step the
	// next run would capture the polluted state as its baseline (getDeploymentArgs)
	// and permanently cement the drift. This is the args counterpart of the
	// ensureRRSAEnabled self-healing below; regular configuration flags
	// (--cluster-id/--uid/--enable-*/...) are never touched.
	normalizeDeploymentArgsBaseline(ctx)

	// Ensure RRSA is enabled on the ack-secret-manager Deployment.
	// RRSA requires a projected OIDC token volume; without it, all tests that
	// rely on RRSA / OIDC authentication will fail with
	// "open /var/run/secrets/tokens/ack-secret-manager: no such file or directory".
	ensureRRSAEnabled(ctx)

	// The controller now runs with leader election (LeaderElectionReleaseOnCancel
	// migration): every reconcile path is gated behind the lease, so assert early
	// that leadership was actually acquired before any test starts exercising it.
	verifyLeaderElectionLease(ctx)
})

var _ = AfterSuite(func() {
	By("cleaning up global test resources")
	if GlobalResourceManager != nil {
		err := GlobalResourceManager.CleanupAllResources(ctx)
		Expect(err).NotTo(HaveOccurred())
	}

	// Remove the RRSA env vars from the Deployment BEFORE deleting the env
	// Secret below, so no secretKeyRef on the Deployment ever dangles.
	By("cleaning up RRSA env vars from the Deployment")
	cleanupDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)

	// Clean up the RRSA env Secret created in ensureRRSAEnabled
	By("cleaning up RRSA env Secret")
	_ = clientset.CoreV1().Secrets(ackSecretManagerNamespace).Delete(ctx, "ack-secret-manager-rrsa-env", metav1.DeleteOptions{})

	By("tearing down the test environment")
	cancel()
})

// Generate random string based on Ginkgo random seed
func getRandString() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, 8)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}
	return string(result)
}

// Get kubeconfig helper function
func getKubeConfig() (*rest.Config, error) {
	var kubeconfig string

	// Check environment variable
	if os.Getenv("KUBECONFIG") != "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	} else {
		// Default to ~/.kube/config
		kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}

	// If environment variable or default path exists, use file to load config
	if _, err := os.Stat(kubeconfig); err == nil {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}

	// If not running in cluster, try using InClusterConfig
	return rest.InClusterConfig()
}

// Ensure CRDs are installed
func ensureCRDsInstalled(ctx context.Context) {
	// Simple check to ensure CRDs exist, if not they need to be installed first
	err := k8sClient.List(ctx, &api.SecretStoreList{})
	Expect(err).NotTo(HaveOccurred(), "SecretStore CRD not found, please install CRDs first")

	err = k8sClient.List(ctx, &api.ExternalSecretList{})
	Expect(err).NotTo(HaveOccurred(), "ExternalSecret CRD not found, please install CRDs first")

	err = k8sClient.List(ctx, &api.ClusterSecretStoreList{})
	Expect(err).NotTo(HaveOccurred(), "ClusterSecretStore CRD not found, please install CRDs first")

	err = k8sClient.List(ctx, &api.ClusterExternalSecretList{})
	Expect(err).NotTo(HaveOccurred(), "ClusterExternalSecret CRD not found, please install CRDs first")
}

// Create test namespace helper function
func createTestNamespace(ctx context.Context, name string) *corev1.Namespace {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	return namespace
}

// Delete test namespace helper function.
// Acts as a safety net: actively cleans up any remaining ExternalSecrets, their
// synced Secrets, and SecretStores before deleting the namespace. This ensures
// cleanup even when a test fails before reaching its own CleanupExternalSecret/DeferCleanup calls.
// SecretStores carry a finalizer that blocks namespace cascade deletion when the
// controller cannot reconcile in time, so their finalizers are stripped explicitly.
func deleteTestNamespace(ctx context.Context, namespace *corev1.Namespace) {
	if namespace == nil {
		return
	}

	// Step 1: Best-effort cleanup of synced Secrets, ExternalSecrets, and SecretStores.
	// Delete synced Secrets, then delete ESes and try to remove their
	// finalizers. Also delete SecretStores and strip their finalizers so the
	// namespace cascade deletion is not blocked by the store finalizer when
	// the controller cannot reconcile in time.
	// If the controller is slow or the finalizer removal fails,
	// we proceed anyway — Step 4 force-finalizes the namespace if it gets
	// stuck in Terminating.
	esList := &api.ExternalSecretList{}
	if err := k8sClient.List(ctx, esList, client.InNamespace(namespace.Name)); err == nil {
		for i := range esList.Items {
			es := &esList.Items[i]
			syncedSecret := &corev1.Secret{}
			secretKey := types.NamespacedName{Name: es.Name, Namespace: es.Namespace}
			if err := k8sClient.Get(ctx, secretKey, syncedSecret); err == nil {
				if err := k8sClient.Delete(ctx, syncedSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete synced Secret %s: %v\n", secretKey, err)
				}
			}
			if err := k8sClient.Delete(ctx, es); err != nil && !k8serrors.IsNotFound(err) {
				GinkgoWriter.Printf("WARNING: failed to delete ExternalSecret %s/%s: %v\n", es.Namespace, es.Name, err)
			}
			// Try to remove finalizers via JSON Patch (no resourceVersion check).
			if len(es.Finalizers) > 0 {
				jsonPatch := []byte(`[{"op":"replace","path":"/metadata/finalizers","value":[]}]`)
				if err := k8sClient.Patch(ctx, es, client.RawPatch(types.JSONPatchType, jsonPatch)); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to strip finalizers from ExternalSecret %s/%s: %v\n", es.Namespace, es.Name, err)
				}
			}
		}
	}

	ssList := &api.SecretStoreList{}
	if err := k8sClient.List(ctx, ssList, client.InNamespace(namespace.Name)); err == nil {
		for i := range ssList.Items {
			ss := &ssList.Items[i]
			if err := k8sClient.Delete(ctx, ss); err != nil && !k8serrors.IsNotFound(err) {
				GinkgoWriter.Printf("WARNING: failed to delete SecretStore %s/%s: %v\n", ss.Namespace, ss.Name, err)
			}
			if len(ss.Finalizers) > 0 {
				jsonPatch := []byte(`[{"op":"replace","path":"/metadata/finalizers","value":[]}]`)
				if err := k8sClient.Patch(ctx, ss, client.RawPatch(types.JSONPatchType, jsonPatch)); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to strip finalizers from SecretStore %s/%s: %v\n", ss.Namespace, ss.Name, err)
				}
			}
		}
	}

	// Step 2: Brief wait for ES and SecretStore cleanup (best-effort, deliberately NOT
	// blocking: cleanup issues must not mask the test result).
	esCleaned := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		checkList := &api.ExternalSecretList{}
		if err := k8sClient.List(ctx, checkList, client.InNamespace(namespace.Name)); err != nil || len(checkList.Items) == 0 {
			esCleaned = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !esCleaned {
		GinkgoWriter.Printf("WARNING: ExternalSecrets in namespace %s not cleaned up in time; proceeding with namespace deletion\n", namespace.Name)
	}

	ssCleaned := false
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		checkList := &api.SecretStoreList{}
		if err := k8sClient.List(ctx, checkList, client.InNamespace(namespace.Name)); err != nil || len(checkList.Items) == 0 {
			ssCleaned = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !ssCleaned {
		GinkgoWriter.Printf("WARNING: SecretStores in namespace %s not cleaned up in time; proceeding with namespace deletion\n", namespace.Name)
	}

	// Step 3: Delete the namespace.
	if err := k8sClient.Delete(ctx, namespace); err != nil && !k8serrors.IsNotFound(err) {
		GinkgoWriter.Printf("WARNING: failed to delete namespace %s: %v\n", namespace.Name, err)
	}

	// Step 4: Wait briefly for the namespace to be deleted. If it gets stuck
	// in Terminating (due to spec.finalizers), force removal through the
	// finalize subresource — a plain Update is rejected for Terminating
	// namespaces and cannot modify namespace finalizers at all. Failure here
	// is a warning only, consistent with best-effort cleanup semantics.
	nsGone := false
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		ns := &corev1.Namespace{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: namespace.Name}, ns); err != nil {
			nsGone = true // namespace is gone
			break
		}
		if ns.Status.Phase == corev1.NamespaceTerminating && len(ns.Spec.Finalizers) > 0 {
			ns.Spec.Finalizers = nil
			if err := k8sClient.SubResource("finalize").Update(ctx, ns); err != nil {
				GinkgoWriter.Printf("WARNING: failed to force-finalize Terminating namespace %s: %v\n", namespace.Name, err)
			}
		}
		time.Sleep(2 * time.Second)
	}
	if !nsGone {
		GinkgoWriter.Printf("WARNING: namespace %s was not fully deleted during cleanup (may remain in Terminating)\n", namespace.Name)
	}
}

// CleanupExternalSecret helper function to properly delete ExternalSecret and wait for completion
func CleanupExternalSecret(ctx context.Context, externalSecret *api.ExternalSecret) {
	// Delete the ExternalSecret
	Expect(k8sClient.Delete(ctx, externalSecret)).To(Succeed())

	// Wait for ExternalSecret to be fully deleted
	Eventually(func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: externalSecret.Name, Namespace: externalSecret.Namespace}, &api.ExternalSecret{})
		return err != nil && k8serrors.IsNotFound(err)
	}, time.Second*10, time.Second*1).Should(BeTrue(), "ExternalSecret should be deleted before test ends")
}

// CleanupExternalSecretAndSyncedSecret thoroughly cleans up an ExternalSecret
// and all associated K8s Secrets: synced Secrets may persist after deletion
// (cleanupSecretOnFailure defaults to false), and a leftover ExternalSecret
// would make deleteTestNamespace hang. Deletes the synced Secrets and the
// ExternalSecret, waits for both; safe in DeferCleanup (tolerates missing).
func CleanupExternalSecretAndSyncedSecret(ctx context.Context, es *api.ExternalSecret) {
	if es == nil {
		return
	}

	// Step 1: Delete synced K8s Secrets explicitly.
	// The controller creates a Secret named after the ExternalSecret (or spec.target.name
	// when set). cleanupSecretOnFailure is false by default, so synced Secrets may persist.
	syncedSecretName := es.Name
	if es.Spec.Target != nil && es.Spec.Target.Name != "" {
		syncedSecretName = es.Spec.Target.Name
	}
	syncedSecret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: syncedSecretName, Namespace: es.Namespace}
	if err := k8sClient.Get(ctx, secretKey, syncedSecret); err == nil {
		_ = k8sClient.Delete(ctx, syncedSecret)
	}
	// Also try the ES-name-based Secret in case it differs from target name
	if syncedSecretName != es.Name {
		esSecret := &corev1.Secret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: es.Name, Namespace: es.Namespace}, esSecret); err == nil {
			_ = k8sClient.Delete(ctx, esSecret)
		}
	}

	// Step 2: Delete the ExternalSecret (tolerates already-deleted)
	if err := k8sClient.Delete(ctx, es); err != nil && !k8serrors.IsNotFound(err) {
		GinkgoWriter.Printf("WARNING: failed to delete ExternalSecret %s/%s: %v\n", es.Namespace, es.Name, err)
		return
	}

	// Step 3: Wait for ExternalSecret to be fully gone
	Eventually(func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: es.Name, Namespace: es.Namespace}, &api.ExternalSecret{})
		return err != nil && k8serrors.IsNotFound(err)
	}).WithTimeout(time.Second*30).WithPolling(time.Second*2).Should(BeTrue(),
		"ExternalSecret %s/%s should be fully deleted", es.Namespace, es.Name)
}

// Validate ExternalSecret status is succeeded and corresponding secret is created
func validateExternalSecretSucceededAndSecretCreated(ctx context.Context, namespace, name string, timeout time.Duration) {
	var lastCheckError string
	Eventually(func() bool {
		createdExternalSecret := &api.ExternalSecret{}
		err := k8sClient.Get(ctx, client.ObjectKey{
			Name:      name,
			Namespace: namespace,
		}, createdExternalSecret)
		if err != nil {
			lastCheckError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
			return false
		}

		// Check if there are sync results with Succeeded status. An empty
		// DataSyncResults slice would otherwise pass the loop vacuously.
		if len(createdExternalSecret.Status.DataSyncResults) == 0 {
			lastCheckError = "ExternalSecret has no DataSyncResults yet"
			return false
		}
		hasSucceeded := false
		for i, result := range createdExternalSecret.Status.DataSyncResults {
			switch result.Status {
			case "Succeeded":
				hasSucceeded = true
			case "Warning":
				// CSS disabled notice etc. — acceptable alongside Succeeded data syncs
				continue
			default:
				lastCheckError = fmt.Sprintf(
					"DataSyncResult[%d] should have status 'Succeeded', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
				return false
			}
		}
		if !hasSucceeded {
			lastCheckError = "no DataSyncResult has status 'Succeeded'"
			return false
		}

		// Check if corresponding secret was created
		secret := &corev1.Secret{}
		err = k8sClient.Get(ctx, client.ObjectKey{
			Name:      name,
			Namespace: namespace,
		}, secret)
		if err != nil {
			lastCheckError = fmt.Sprintf("Failed to get corresponding Secret: %v", err)
			return false
		}

		return true
	}).WithTimeout(timeout).WithPolling(time.Second*5).Should(BeTrue(),
		func() string {
			if lastCheckError != "" {
				return fmt.Sprintf("ExternalSecret should succeed and corresponding secret should be created, but: %s", lastCheckError)
			}
			return "ExternalSecret should succeed and corresponding secret should be created"
		})
}

// Validate parsed secret content: every expected key must exist in the
// synced Secret and its value must equal the expected value.
func validateParsedSecretContent(ctx context.Context, externalSecret *api.ExternalSecret, expectedValues map[string]string) {
	var lastCheckError string
	Eventually(func() bool {
		kubeSecret := &corev1.Secret{}
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      externalSecret.Name,
			Namespace: externalSecret.Namespace,
		}, kubeSecret)
		if err != nil {
			lastCheckError = fmt.Sprintf("Failed to get Kubernetes Secret: %v", err)
			return false
		}

		// Check if ALL expected keys exist with the expected values
		for key, expectedValue := range expectedValues {
			if kubeSecret.Data[key] == nil {
				lastCheckError = fmt.Sprintf("Missing expected key %q in secret", key)
				return false
			}
			if string(kubeSecret.Data[key]) != expectedValue {
				lastCheckError = fmt.Sprintf("Secret key %q has value %q, expected %q", key, string(kubeSecret.Data[key]), expectedValue)
				return false
			}
		}

		return true
	}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
		func() string {
			if lastCheckError != "" {
				return fmt.Sprintf("Parsed secret should contain all expected keys with expected values, but: %s", lastCheckError)
			}
			return "Parsed secret should contain all expected keys with expected values"
		})
}

// createAKSecret creates a Kubernetes Secret holding AK/SK credentials and
// returns it. Shared by all auth-related tests that need an accessKeyId /
// accessKeySecret Secret referenced by SecretStore authentication.
func createAKSecret(ctx context.Context, namespace, name, accessKeyID, accessKeySecret string) *corev1.Secret {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"accessKeyId":     []byte(accessKeyID),
			"accessKeySecret": []byte(accessKeySecret),
		},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	return secret
}

// createRRSASecretStore creates an OIDC/RRSA SecretStore in the given
// namespace and waits until it becomes Ready.
func createRRSASecretStore(ctx context.Context, namespace, name string) *api.SecretStore {
	store := &api.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: api.SecretStoreSpec{
			KMS: &api.KMSProvider{
				KMS: &api.KMSAuth{
					RAMRoleARN:      RAMRoleArnForRRSA,
					OIDCProviderARN: OIDCProviderARN,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, store)).To(Succeed())
	waitForSecretStoreReady(ctx, namespace, name)
	return store
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ACK Secret Manager E2E Suite")
}

// createRRSAServiceAccountForTest creates a dedicated RAM role whose trust
// policy targets the given ServiceAccount, then creates the ServiceAccount
// annotated with the role ARN. The RAM role cleanup is registered before the
// ServiceAccount is created so the role is always deleted, even when the
// ServiceAccount creation fails (cleanup failures only emit a WARNING,
// matching the suite's cleanup conventions). The ServiceAccount itself is
// removed by the namespace cascade deletion, so no separate cleanup is needed.
func createRRSAServiceAccountForTest(ctx context.Context, namespace, saName string) *corev1.ServiceAccount {
	roleArn, roleName, err := GlobalResourceManager.CreateRamRoleForServiceAccount(ctx, namespace, saName)
	Expect(err).NotTo(HaveOccurred(), "failed to create RAM role for ServiceAccount %s/%s", namespace, saName)

	// Register cleanup before creating the ServiceAccount so the RAM role is
	// never leaked even if the creation below fails.
	DeferCleanup(func() {
		if err := GlobalResourceManager.DeleteRamRole(roleName); err != nil {
			GinkgoWriter.Printf("WARNING: failed to delete RAM role %s: %v\n", roleName, err)
		}
	})

	serviceAccount := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: namespace,
			Annotations: map[string]string{
				ACKRRSAAnnotation: roleArn,
			},
		},
	}
	Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())
	return serviceAccount
}

// waitForClusterSecretStoreReady waits until a ClusterSecretStore becomes Ready.
func waitForClusterSecretStoreReady(ctx context.Context, name string) {
	var lastError string
	Eventually(func() bool {
		store := &api.ClusterSecretStore{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, store)
		if err != nil {
			lastError = fmt.Sprintf("Error getting ClusterSecretStore: %v", err)
			return false
		}
		if len(store.Status.Conditions) == 0 {
			lastError = "ClusterSecretStore has no status conditions"
			return false
		}
		for _, c := range store.Status.Conditions {
			// Deterministic failure: spec validation failed and cannot
			// self-heal, so fail fast instead of polling until timeout.
			// Other reasons (e.g. ClientCreationFailed) remain retryable.
			if c.Type == api.SecretStoreReady && c.Status == corev1.ConditionFalse && c.Reason == api.ReasonValidationFailed {
				Fail(fmt.Sprintf("ClusterSecretStore %s failed validation: reason=%s message=%s",
					name, c.Reason, c.Message))
			}
			if c.Type != api.SecretStoreReady || c.Status != corev1.ConditionTrue {
				lastError = fmt.Sprintf("SecretStoreReady condition type=%s status=%s reason=%s message=%s",
					c.Type, c.Status, c.Reason, c.Message)
				return false
			}
		}
		return true
	}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
		func() string {
			if lastError != "" {
				return fmt.Sprintf("ClusterSecretStore should eventually become ready, but: %s", lastError)
			}
			return "ClusterSecretStore should eventually become ready"
		})
}

// waitForSecretStoreReady waits until a SecretStore becomes Ready.
func waitForSecretStoreReady(ctx context.Context, namespace, name string) {
	var lastError string
	Eventually(func() bool {
		store := &api.SecretStore{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, store)
		if err != nil {
			lastError = fmt.Sprintf("Error getting SecretStore: %v", err)
			return false
		}
		if len(store.Status.Conditions) == 0 {
			lastError = "SecretStore has no status conditions"
			return false
		}
		for _, c := range store.Status.Conditions {
			// Deterministic failure: spec validation failed and cannot
			// self-heal, so fail fast instead of polling until timeout.
			// Other reasons (e.g. ClientCreationFailed) remain retryable.
			if c.Type == api.SecretStoreReady && c.Status == corev1.ConditionFalse && c.Reason == api.ReasonValidationFailed {
				Fail(fmt.Sprintf("SecretStore %s/%s failed validation: reason=%s message=%s",
					namespace, name, c.Reason, c.Message))
			}
			if c.Type != api.SecretStoreReady || c.Status != corev1.ConditionTrue {
				lastError = fmt.Sprintf("SecretStoreReady condition type=%s status=%s reason=%s message=%s",
					c.Type, c.Status, c.Reason, c.Message)
				return false
			}
		}
		return true
	}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(BeTrue(),
		func() string {
			if lastError != "" {
				return fmt.Sprintf("SecretStore should eventually become ready, but: %s", lastError)
			}
			return "SecretStore should eventually become ready"
		})
}

// waitForDeploymentRollout waits for a Deployment to complete its rolling update.
// It checks that the updated replicas match the desired replicas and that no pods
// are unavailable.
func waitForDeploymentRollout(ctx context.Context, namespace, name string) {
	Eventually(func() error {
		return checkDeploymentRolloutComplete(ctx, namespace, name)
	}).WithTimeout(rolloutWaitTimeout).WithPolling(rolloutPollInterval).Should(Succeed(),
		"Deployment %s/%s did not complete rollout within timeout", namespace, name)
}

// waitForDeploymentRolloutQuietly polls the Deployment rollout status like
// waitForDeploymentRollout but returns an error instead of failing the suite,
// so it can be used in AfterSuite cleanup without masking test results.
// It shares the exact same completion criteria (checkDeploymentRolloutComplete),
// timeout and polling interval as waitForDeploymentRollout.
func waitForDeploymentRolloutQuietly(ctx context.Context, namespace, name string) error {
	deadline := time.Now().Add(rolloutWaitTimeout)
	var lastErr error
	for {
		if lastErr = checkDeploymentRolloutComplete(ctx, namespace, name); lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rolloutPollInterval):
		}
	}
}

const (
	// rolloutWaitTimeout is the maximum time shared rollout waiters allow for
	// a Deployment rollout to complete.
	rolloutWaitTimeout = 120 * time.Second
	// rolloutPollInterval is the polling interval shared by rollout waiters.
	rolloutPollInterval = 5 * time.Second
)

// checkDeploymentRolloutComplete reports whether the Deployment has finished
// its rolling update: the latest generation has been observed, all desired
// replicas run the updated template, and no replica is unavailable.
func checkDeploymentRolloutComplete(ctx context.Context, namespace, name string) error {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if dep.Status.ObservedGeneration < dep.Generation {
		return fmt.Errorf("deployment %s/%s not yet observed (gen=%d, observed=%d)",
			namespace, name, dep.Generation, dep.Status.ObservedGeneration)
	}
	if dep.Status.UpdatedReplicas < *dep.Spec.Replicas {
		return fmt.Errorf("deployment %s/%s rolling update in progress (updated=%d/%d)",
			namespace, name, dep.Status.UpdatedReplicas, *dep.Spec.Replicas)
	}
	if dep.Status.UnavailableReplicas > 0 {
		return fmt.Errorf("deployment %s/%s has %d unavailable replicas",
			namespace, name, dep.Status.UnavailableReplicas)
	}
	return nil
}

// bumpRestartAnnotationAndUpdate sets the restartedAt annotation to force a
// rolling restart and updates the Deployment. It is the quiet counterpart of
// updateDeploymentAndRollout: it performs no Ginkgo assertions so it can be
// used in AfterSuite cleanup where failures must only warn, never fail the
// suite. Callers are expected to retry on conflict.
func bumpRestartAnnotationAndUpdate(ctx context.Context, namespace, name string, dep *appsv1.Deployment) error {
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err := clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	return err
}

// filterRRSAEnvVars strips the RRSA env vars (ALICLOUD_ROLE_ARN,
// ALICLOUD_OIDC_PROVIDER_ARN) from the Deployment's first container and
// reports whether anything was removed. Shared by cleanupDeploymentRRSAEnv
// (AfterSuite).
func filterRRSAEnvVars(dep *appsv1.Deployment) bool {
	rrsaEnvNames := map[string]bool{
		"ALICLOUD_ROLE_ARN":          true,
		"ALICLOUD_OIDC_PROVIDER_ARN": true,
	}
	filteredEnv := make([]corev1.EnvVar, 0, len(dep.Spec.Template.Spec.Containers[0].Env))
	removed := false
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		if rrsaEnvNames[env.Name] {
			removed = true
			continue
		}
		filteredEnv = append(filteredEnv, env)
	}
	dep.Spec.Template.Spec.Containers[0].Env = filteredEnv
	return removed
}

// filterAllAuthEnvVars strips ALL auth-related env vars (RRSA + AK) from the
// Deployment's first container and reports whether anything was removed.
// Used by tests that need to exhaust every ENV auth tier (WorkerRole tests,
// ENV client lazy-init tests).
func filterAllAuthEnvVars(dep *appsv1.Deployment) bool {
	authEnvNames := map[string]bool{
		"ALICLOUD_ROLE_ARN":          true,
		"ALICLOUD_OIDC_PROVIDER_ARN": true,
		"ACCESS_KEY_ID":              true,
		"SECRET_ACCESS_KEY":          true,
	}
	filteredEnv := make([]corev1.EnvVar, 0, len(dep.Spec.Template.Spec.Containers[0].Env))
	removed := false
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		if authEnvNames[env.Name] {
			removed = true
			continue
		}
		filteredEnv = append(filteredEnv, env)
	}
	if removed {
		dep.Spec.Template.Spec.Containers[0].Env = filteredEnv
	}
	return removed
}

// strippedAuthEnvVars stores the exact auth env vars (RRSA + AK) removed by
// the most recent removeRRSAEnvTemporarily call, so restoreRRSAEnvBaseline
// can put them back verbatim instead of reconstructing hardcoded values. The
// e2e suite is single-threaded, so one capture slot is sufficient.
var strippedAuthEnvVars []corev1.EnvVar

// restoreAKEnvVarsFromCapture re-adds exactly the auth env vars that
// removeRRSAEnvTemporarily stripped (verbatim, preserving their original
// ValueFrom), and only when they are still missing. Returns true when at
// least one env var was added. When nothing was stripped in this run it is a
// no-op, so a baseline without AK env vars is never injected with new ones.
func restoreAKEnvVarsFromCapture(dep *appsv1.Deployment) bool {
	if len(strippedAuthEnvVars) == 0 {
		return false
	}
	existing := make(map[string]bool, len(dep.Spec.Template.Spec.Containers[0].Env))
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		existing[env.Name] = true
	}
	added := false
	for _, env := range strippedAuthEnvVars {
		// RRSA env vars are restored by patchDeploymentEnvWithRRSA; only the
		// AK tier is handled here.
		if env.Name != "ACCESS_KEY_ID" && env.Name != "SECRET_ACCESS_KEY" {
			continue
		}
		if !existing[env.Name] {
			dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env, env)
			added = true
		}
	}
	return added
}

// updateDeploymentAndRollout fetches the Deployment, applies mutate to it,
// sets the restartedAt annotation to force a rolling restart, updates the
// Deployment and waits for the rollout to complete.
func updateDeploymentAndRollout(ctx context.Context, namespace, name string, mutate func(dep *appsv1.Deployment)) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get Deployment %s/%s", namespace, name)

	mutate(dep)

	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to update Deployment %s/%s", namespace, name)

	By(fmt.Sprintf("waiting for Deployment %s/%s rollout to complete", namespace, name))
	waitForDeploymentRollout(ctx, namespace, name)

	// Every restart must re-confirm leadership before subsequent specs
	// exercise the lease-gated reconcile paths.
	verifyLeaderElectionLease(ctx)
}

// getDeploymentArgs returns a copy of the current args of the first
// container of the ack-secret-manager Deployment. It is a read-only helper
// used to capture the args baseline BEFORE registering cleanup, so the
// DeferCleanup registration never depends on a (potentially failing) patch
// call. Fails when the Deployment cannot be retrieved.
func getDeploymentArgs(ctx context.Context) []string {
	dep, err := clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment for args baseline capture")

	args := make([]string, len(dep.Spec.Template.Spec.Containers[0].Args))
	copy(args, dep.Spec.Template.Spec.Containers[0].Args)
	return args
}

// patchDeploymentArgs patches the ack-secret-manager Deployment args: every
// arg matching one of removePrefixes is dropped and addArgs are appended,
// then the Deployment is restarted and the rollout awaited. Returns the
// original args so callers can restore them via restoreDeploymentArgs.
//
// Idempotent: when the current args already satisfy the desired state (no
// arg matches any removePrefix except when it is one of addArgs, and every
// addArg is present), the Update and rollout are skipped entirely.
func patchDeploymentArgs(ctx context.Context, removePrefixes []string, addArgs []string) (originalArgs []string) {
	dep, err := clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment for args patch")

	originalArgs = make([]string, len(dep.Spec.Template.Spec.Containers[0].Args))
	copy(originalArgs, dep.Spec.Template.Spec.Containers[0].Args)

	// Check whether the current args already satisfy the desired state.
	alreadySatisfied := true
	for _, arg := range originalArgs {
		for _, prefix := range removePrefixes {
			if !strings.HasPrefix(arg, prefix) {
				continue
			}
			// An arg that exactly matches one of addArgs is part of the
			// desired state, so it does not break satisfaction.
			isDesired := false
			for _, addArg := range addArgs {
				if arg == addArg {
					isDesired = true
					break
				}
			}
			if !isDesired {
				alreadySatisfied = false
				break
			}
		}
		if !alreadySatisfied {
			break
		}
	}
	if alreadySatisfied {
		for _, addArg := range addArgs {
			found := false
			for _, arg := range originalArgs {
				if arg == addArg {
					found = true
					break
				}
			}
			if !found {
				alreadySatisfied = false
				break
			}
		}
	}
	if alreadySatisfied {
		By("Deployment args already match the desired state, skipping patch and rollout")
		return originalArgs
	}

	updateDeploymentAndRollout(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName, func(dep *appsv1.Deployment) {
		newArgs := make([]string, 0, len(dep.Spec.Template.Spec.Containers[0].Args)+len(addArgs))
		for _, arg := range dep.Spec.Template.Spec.Containers[0].Args {
			removed := false
			for _, prefix := range removePrefixes {
				if strings.HasPrefix(arg, prefix) {
					removed = true
					break
				}
			}
			if !removed {
				newArgs = append(newArgs, arg)
			}
		}
		dep.Spec.Template.Spec.Containers[0].Args = append(newArgs, addArgs...)
	})

	return originalArgs
}

// testOnlyFlagPrefixes lists the Deployment args that only e2e specs ever
// set (via patchDeploymentArgs) to steer the operator under test; none of
// them belongs to a regular production configuration. Every prefix maps to
// a flag whose DEFAULT restores the expected baseline once the arg is
// dropped (cmd/manager/main.go). Deliberately NOT listed: regular
// configuration flags such as --cluster-id, --uid, --region, --backend and
// the --enable-* family, which must survive normalization untouched.
var testOnlyFlagPrefixes = []string{
	"--disable-polling",
	"--polling-interval",
	"--process-cluster-secret-store",
	"--process-cluster-external-secret",
	"--cleanup-secret-on-failure",
	"--watch-namespaces",
	"--exclude-namespaces",
	"--max-concurrent-kms-secret-pulls",
	"--max-concurrent-secret-pulls",
}

// normalizeDeploymentArgsBaseline removes test-only flags left behind on the
// ack-secret-manager Deployment (e.g. by a hard-killed previous run whose
// DeferCleanup restore never ran) so every suite start observes the true
// production args baseline. Idempotent: patchDeploymentArgs skips the
// Update/rollout entirely when no test-only flag is present.
func normalizeDeploymentArgsBaseline(ctx context.Context) {
	currentArgs := getDeploymentArgs(ctx)

	leftover := make([]string, 0)
	for _, arg := range currentArgs {
		for _, prefix := range testOnlyFlagPrefixes {
			if strings.HasPrefix(arg, prefix) {
				leftover = append(leftover, arg)
				break
			}
		}
	}
	if len(leftover) == 0 {
		By("Deployment args baseline is clean, no test-only flags to normalize")
		return
	}

	GinkgoWriter.Printf("WARNING: normalizing Deployment args baseline; removing test-only flag(s) left behind by a previous run: %v\n", leftover)
	By(fmt.Sprintf("normalizing Deployment args baseline (removing leftover test-only flags %v)", leftover))
	patchDeploymentArgs(ctx, testOnlyFlagPrefixes, nil)
}

// ensureRRSAEnvSecret ensures the RRSA env Secret exists with the correct data.
// This is a shared helper used by both ensureRRSAEnabled (BeforeSuite) and patchDeploymentRRSAEnv (per-test).
func ensureRRSAEnvSecret(ctx context.Context, namespace string) {
	if RAMRoleArnForRRSA == "" || OIDCProviderARN == "" {
		return
	}

	const rrsaEnvSecretName = "ack-secret-manager-rrsa-env"

	rrsaEnvSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rrsaEnvSecretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"rolearn":         []byte(RAMRoleArnForRRSA),
			"oidcproviderarn": []byte(OIDCProviderARN),
		},
	}

	existingSecret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, rrsaEnvSecretName, metav1.GetOptions{})
	if err != nil {
		By("creating Secret for RRSA env vars")
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, rrsaEnvSecret, metav1.CreateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to create RRSA env Secret")
	} else {
		By("updating existing Secret for RRSA env vars")
		existingSecret.Data = rrsaEnvSecret.Data
		_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, existingSecret, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to update RRSA env Secret")
	}
}

// patchDeploymentEnvWithRRSA patches the Deployment to reference the RRSA env Secret via secretKeyRef.
// Returns true if the Deployment was updated (needs rollout).
// This is a shared helper used by both ensureRRSAEnabled (BeforeSuite) and patchDeploymentRRSAEnv (per-test).
func patchDeploymentEnvWithRRSA(ctx context.Context, dep *appsv1.Deployment) bool {
	const rrsaEnvSecretName = "ack-secret-manager-rrsa-env"

	needsUpdate := false
	envVarKeys := map[string]string{
		"ALICLOUD_ROLE_ARN":          "rolearn",
		"ALICLOUD_OIDC_PROVIDER_ARN": "oidcproviderarn",
	}
	for envName, secretKey := range envVarKeys {
		found := false
		for i, env := range dep.Spec.Template.Spec.Containers[0].Env {
			if env.Name == envName {
				// Check if already using correct secretKeyRef
				if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil &&
					env.ValueFrom.SecretKeyRef.Name == rrsaEnvSecretName &&
					env.ValueFrom.SecretKeyRef.Key == secretKey {
					found = true
					break
				}
				// Replace existing env var
				By(fmt.Sprintf("replacing env var %s with secretKeyRef", envName))
				dep.Spec.Template.Spec.Containers[0].Env[i] = corev1.EnvVar{
					Name: envName,
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: rrsaEnvSecretName},
							Key:                  secretKey,
						},
					},
				}
				found = true
				needsUpdate = true
				break
			}
		}
		if !found {
			By(fmt.Sprintf("patching Deployment to add env var %s from Secret", envName))
			dep.Spec.Template.Spec.Containers[0].Env = append(
				dep.Spec.Template.Spec.Containers[0].Env,
				corev1.EnvVar{
					Name: envName,
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: rrsaEnvSecretName},
							Key:                  secretKey,
						},
					},
				},
			)
			needsUpdate = true
		}
	}
	return needsUpdate
}

// patchDeploymentRRSAEnv ensures the ack-secret-manager Deployment has RRSA env vars
// (ALICLOUD_ROLE_ARN, ALICLOUD_OIDC_PROVIDER_ARN) configured via secretKeyRef.
// This follows the production pattern where credentials are stored in a Secret
// and referenced via secretKeyRef instead of being set as plain-text values.
func patchDeploymentRRSAEnv(ctx context.Context, namespace, name string) {
	if RAMRoleArnForRRSA == "" || OIDCProviderARN == "" {
		GinkgoWriter.Printf("WARNING: RRSA env vars not configured (RAMRoleArnForRRSA=%q, OIDCProviderARN=%q), skipping patch\n",
			RAMRoleArnForRRSA, OIDCProviderARN)
		return
	}

	// Ensure the RRSA env Secret exists
	ensureRRSAEnvSecret(ctx, namespace)

	// Get the Deployment and check whether it already matches the desired state
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get Deployment for RRSA env patch")

	if !patchDeploymentEnvWithRRSA(ctx, dep) {
		By("Deployment already has correct RRSA env vars via secretKeyRef, skipping patch")
		return
	}

	updateDeploymentAndRollout(ctx, namespace, name, func(dep *appsv1.Deployment) {
		patchDeploymentEnvWithRRSA(ctx, dep)
	})
}

// restoreDeploymentRRSAEnv restores the ack-secret-manager Deployment to the
// RRSA env baseline established by BeforeSuite (ensureRRSAEnabled): the env
// Secret ack-secret-manager-rrsa-env exists and ALICLOUD_ROLE_ARN /
// ALICLOUD_OIDC_PROVIDER_ARN are present via secretKeyRef.
//
// IMPORTANT: this must NOT simply remove the RRSA env vars. BeforeSuite
// injects them globally, so removing them here would silently change the
// baseline for every subsequent test (and deleting the env Secret would even
// break pod startup because the remaining secretKeyRef env vars would dangle).
// Restoration therefore means "return to the BeforeSuite baseline state".
func restoreDeploymentRRSAEnv(ctx context.Context, namespace, name string) {
	restoreRRSAEnvBaseline(ctx, namespace, name)
}

// removeRRSAEnvTemporarily removes the RRSA env vars (ALICLOUD_ROLE_ARN,
// ALICLOUD_OIDC_PROVIDER_ARN) AND the AK env vars (ACCESS_KEY_ID,
// SECRET_ACCESS_KEY) from the Deployment and triggers a rollout.
// It is used by WorkerRole/ENV-client tests that need to exhaust every ENV
// auth tier: with the globally injected ENV OIDC credentials OR AK
// credentials present, an ExternalSecret without SecretStoreRef would be
// served by ENV RRSA (auth chain priority 2) or ENV AK (priority 3)
// instead of reaching the WorkerRole tier (priority 5) or producing the
// "no usable authentication tier" error.
//
// Callers MUST restore the baseline afterwards via restoreRRSAEnvBaseline
// (typically in DeferCleanup), otherwise subsequent tests lose ENV RRSA
// and ENV AK credentials.
func removeRRSAEnvTemporarily(ctx context.Context, namespace, name string) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get Deployment for RRSA env removal")

	// Capture the exact env vars about to be stripped so the restore path can
	// put them back verbatim (no hardcoded reconstruction).
	captured := []corev1.EnvVar{}
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		switch env.Name {
		case "ALICLOUD_ROLE_ARN", "ALICLOUD_OIDC_PROVIDER_ARN", "ACCESS_KEY_ID", "SECRET_ACCESS_KEY":
			captured = append(captured, env)
		}
	}
	if len(captured) == 0 {
		strippedAuthEnvVars = nil
		By("auth env vars already absent from Deployment, nothing to remove")
		return
	}
	strippedAuthEnvVars = captured

	updateDeploymentAndRollout(ctx, namespace, name, func(dep *appsv1.Deployment) {
		filterAllAuthEnvVars(dep)
	})
}

// cleanupDeploymentRRSAEnv removes the RRSA env vars (ALICLOUD_ROLE_ARN,
// ALICLOUD_OIDC_PROVIDER_ARN) from the ack-secret-manager Deployment as part
// of AfterSuite cleanup. It MUST run before the RRSA env Secret is deleted so
// that no secretKeyRef on the Deployment ever dangles (a dangling reference
// would leave restarted pods stuck in CreateContainerConfigError).
//
// Idempotent: when the two env vars are already absent, no update or rollout
// happens. All failures are logged as warnings and never fail the suite, so
// cleanup issues do not mask test results.
func cleanupDeploymentRRSAEnv(ctx context.Context, namespace, name string) {
	needsRollout := false
	cleanupOnce := func() error {
		dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if !filterRRSAEnvVars(dep) {
			needsRollout = false
			return nil
		}
		if err := bumpRestartAnnotationAndUpdate(ctx, namespace, name, dep); err != nil {
			return err
		}
		needsRollout = true
		return nil
	}

	if err := cleanupOnce(); err != nil {
		// Retry once (re-Get then Update) before giving up.
		if err = cleanupOnce(); err != nil {
			GinkgoWriter.Printf("WARNING: failed to remove RRSA env vars from Deployment %s/%s after retry; "+
				"the env vars may remain and their secretKeyRef will dangle once the RRSA env Secret is deleted: %v\n",
				namespace, name, err)
			return
		}
	}

	if !needsRollout {
		By("RRSA env vars already absent from Deployment, skipping cleanup rollout")
		return
	}

	By("waiting for Deployment rollout after removing RRSA env vars")
	if err := waitForDeploymentRolloutQuietly(ctx, namespace, name); err != nil {
		GinkgoWriter.Printf("WARNING: Deployment %s/%s rollout after RRSA env cleanup did not finish cleanly: %v\n",
			namespace, name, err)
	}
}

// restoreRRSAEnvBaseline restores the RRSA env baseline established by
// BeforeSuite (ensureRRSAEnabled): it re-ensures the RRSA env Secret and the
// secretKeyRef env vars, patching + rolling out only when the Deployment has
// drifted from the baseline. It also restores the AK env vars
// (ACCESS_KEY_ID, SECRET_ACCESS_KEY) that removeRRSAEnvTemporarily actually
// stripped, re-adding them verbatim from the captured originals. The env
// Secret is deliberately NOT deleted here (AfterSuite owns its final cleanup)
// so the secretKeyRef env vars always resolve and pods keep starting up.
func restoreRRSAEnvBaseline(ctx context.Context, namespace, name string) {
	if RAMRoleArnForRRSA == "" || OIDCProviderARN == "" {
		GinkgoWriter.Printf("WARNING: RRSA baseline not configured (RAMRoleArnForRRSA=%q, OIDCProviderARN=%q), skipping baseline restore\n",
			RAMRoleArnForRRSA, OIDCProviderARN)
		return
	}

	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to get Deployment %s/%s for RRSA env baseline restoration: %v\n", namespace, name, err)
		return
	}

	// Re-ensure the env Secret (it may have been absent) before referencing it.
	ensureRRSAEnvSecret(ctx, namespace)

	needsUpdate := patchDeploymentEnvWithRRSA(ctx, dep)
	// Restore only the AK env vars this suite actually stripped, verbatim.
	// When nothing was stripped (or the baseline has no AK env vars), this is
	// a no-op and nothing is injected.
	if restoreAKEnvVarsFromCapture(dep) {
		needsUpdate = true
	}
	if !needsUpdate {
		By("Deployment already matches the RRSA env baseline, skipping restore")
		return
	}

	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to restore Deployment %s/%s RRSA env baseline: %v\n", namespace, name, err)
		return
	}
	By("waiting for Deployment rollout after RRSA env baseline restore")
	waitForDeploymentRollout(ctx, namespace, name)
	// The baseline restore rollout restarted the controller; re-confirm leadership.
	verifyLeaderElectionLease(ctx)
}

// workerRoleEnabledInDeployment reports whether the ack-secret-manager
// Deployment runs with --enable-worker-role enabled. The flag defaults to
// false (see cmd/manager/main.go), so only an explicit
// --enable-worker-role=true enables the WorkerRole auth provider.
func workerRoleEnabledInDeployment(ctx context.Context, namespace, name string) bool {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to get Deployment %s/%s to inspect --enable-worker-role: %v\n", namespace, name, err)
		// Flag default is false; assume disabled when we cannot inspect.
		return false
	}
	for _, arg := range dep.Spec.Template.Spec.Containers[0].Args {
		if strings.HasPrefix(arg, "--enable-worker-role=") {
			return strings.TrimPrefix(arg, "--enable-worker-role=") == "true"
		}
	}
	return false
}

// restoreDeploymentEnv restores the original env vars of a Deployment's first container.
// Used to clean up after tests that patch the Deployment for WorkerRole + cross-account testing.
func restoreDeploymentEnv(ctx context.Context, namespace, name string, originalEnv []corev1.EnvVar) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to get Deployment %s/%s for restoration: %v\n", namespace, name, err)
		return
	}
	dep.Spec.Template.Spec.Containers[0].Env = originalEnv
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to restore Deployment %s/%s env vars: %v\n", namespace, name, err)
		return
	}
	waitForDeploymentRollout(ctx, namespace, name)
}

// patchDeploymentCrossNamespaceArgs patches the ack-secret-manager Deployment to set
// --enable-cross-namespace-secret-store and --enable-cross-namespace-auth-ref flags.
// Returns the original args so they can be restored later.
func patchDeploymentCrossNamespaceArgs(ctx context.Context, enableSecretStore, enableAuthRef bool) (originalArgs []string) {
	return patchDeploymentArgs(ctx,
		[]string{"--enable-cross-namespace-secret-store", "--enable-cross-namespace-auth-ref"},
		[]string{
			fmt.Sprintf("--enable-cross-namespace-secret-store=%v", enableSecretStore),
			fmt.Sprintf("--enable-cross-namespace-auth-ref=%v", enableAuthRef),
		})
}

// restoreDeploymentArgs restores the original args of the ack-secret-manager Deployment.
//
// Skips the Update and rollout when the current args already match the
// original baseline element by element. On Get or Update failure it retries
// once (re-Get then Update); a second failure escalates to Fail because a
// silent restore failure would leak the modified args baseline to every
// subsequent test.
func restoreDeploymentArgs(ctx context.Context, originalArgs []string) {
	namespace := ackSecretManagerNamespace
	deploymentName := ackSecretManagerDeploymentName

	argsMatch := func(currentArgs []string) bool {
		if len(currentArgs) != len(originalArgs) {
			return false
		}
		for i := range currentArgs {
			if currentArgs[i] != originalArgs[i] {
				return false
			}
		}
		return true
	}

	// restoreOnce re-Gets the Deployment and applies the original args.
	// Returns whether an Update was actually performed.
	restoreOnce := func() (bool, error) {
		dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("failed to get Deployment %s/%s for restoration: %v", namespace, deploymentName, err)
		}

		// Skip when the Deployment already matches the original baseline.
		if argsMatch(dep.Spec.Template.Spec.Containers[0].Args) {
			By("Deployment args already match the original baseline, skipping restore")
			return false, nil
		}

		dep.Spec.Template.Spec.Containers[0].Args = originalArgs
		if dep.Spec.Template.Annotations == nil {
			dep.Spec.Template.Annotations = make(map[string]string)
		}
		dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

		if _, err := clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
			return false, fmt.Errorf("failed to restore Deployment %s/%s args: %v", namespace, deploymentName, err)
		}
		return true, nil
	}

	updated, err := restoreOnce()
	if err != nil {
		// Retry once (re-Get then Update) before escalating.
		updated, err = restoreOnce()
		if err != nil {
			Fail(fmt.Sprintf("failed to restore Deployment %s/%s args after retry; leaking the modified args baseline would break subsequent tests: %v",
				namespace, deploymentName, err))
		}
	}

	if updated {
		By("waiting for Deployment rollout after restoring args")
		waitForDeploymentRollout(ctx, namespace, deploymentName)
		// The restore rollout restarted the controller; re-confirm leadership.
		verifyLeaderElectionLease(ctx)
	}
}

// ensureRRSAEnabled checks whether the ack-secret-manager Deployment already has
// the RRSA projected OIDC token volume mounted. If not, it patches the Deployment
// to add the volume and volumeMount, then waits for rollout to complete.
// It also ensures ALICLOUD_ROLE_ARN and ALICLOUD_OIDC_PROVIDER_ARN env vars are set
// so that ENV-based authentication (BuildAuthConfigFromEnv) works for tests that use
// ExternalSecret without SecretStoreRef.
// This prevents the common failure:
//
//	open /var/run/secrets/tokens/ack-secret-manager: no such file or directory
func ensureRRSAEnabled(ctx context.Context) {
	const (
		volumeName = "ack-secret-manager"
		mountPath  = "/var/run/secrets/tokens"
		tokenPath  = "ack-secret-manager"
		audience   = "sts.aliyuncs.com"
	)

	dep, err := clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment")

	// Check if the projected volume is already present
	needsUpdate := !hasRRSAProjectedVolume(dep, volumeName)
	if needsUpdate {
		By("patching Deployment to enable RRSA (projected OIDC token volume)")
	} else {
		By("RRSA projected volume already configured, skipping volume patch")
	}

	// Ensure RRSA env vars are set for ENV-based authentication using shared helpers.
	ensureRRSAEnvSecret(ctx, ackSecretManagerNamespace)
	if patchDeploymentEnvWithRRSA(ctx, dep) {
		needsUpdate = true
	}

	if needsUpdate {
		updateDeploymentAndRollout(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName, func(dep *appsv1.Deployment) {
			addRRSAProjectedVolume(dep, volumeName, mountPath, tokenPath, audience)
			patchDeploymentEnvWithRRSA(ctx, dep)
		})
	}
}

// hasRRSAProjectedVolume reports whether the Deployment already carries the
// RRSA projected OIDC token volume.
func hasRRSAProjectedVolume(dep *appsv1.Deployment, volumeName string) bool {
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == volumeName && v.Projected != nil {
			return true
		}
	}
	return false
}

const (
	// leaderElectionLeaseName mirrors the controller-runtime LeaderElectionID
	// configured in cmd/manager/main.go.
	leaderElectionLeaseName = "ack-secret-manager-lock"

	// leaderRenewFreshnessWindow is the maximum age a Lease renewTime may have
	// before the holder is considered dead. The controller-runtime renew loop
	// renews every few seconds (RetryPeriod=2s by default), so 30s leaves
	// ample headroom while still catching a stale lease left behind by a
	// leader that crashed without releasing it.
	leaderRenewFreshnessWindow = 30 * time.Second
)

// leaderElectionLeaseNamespace resolves the namespace holding the leader
// election Lease. It is taken from the Deployment's POD_NAMESPACE env var;
// since it is injected via the downward API (fieldRef: metadata.namespace),
// its spec value is empty and the Deployment namespace is the effective
// fallback.
func leaderElectionLeaseNamespace(ctx context.Context) string {
	leaseNamespace := ackSecretManagerNamespace
	dep, err := clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment for leader-election namespace detection")
	for _, container := range dep.Spec.Template.Spec.Containers {
		for _, envVar := range container.Env {
			if envVar.Name == "POD_NAMESPACE" && envVar.Value != "" {
				leaseNamespace = envVar.Value
			}
		}
	}
	return leaseNamespace
}

// runningControllerPodNames lists the names of the Running Pods selected by
// the ack-secret-manager Deployment's label selector, so a Lease holder can
// be checked against the live Pod population.
func runningControllerPodNames(ctx context.Context, namespace string) ([]string, error) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get Deployment %s/%s for Pod listing: %v", namespace, ackSecretManagerDeploymentName, err)
	}
	selector := labels.Set(dep.Spec.Selector.MatchLabels).String()
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("failed to list controller Pods in %s: %v", namespace, err)
	}
	names := make([]string, 0, len(pods.Items))
	for i := range pods.Items {
		if pods.Items[i].Status.Phase == corev1.PodRunning {
			names = append(names, pods.Items[i].Name)
		}
	}
	return names, nil
}

// leaseHolderPodName returns the Pod-name portion of a controller-runtime
// holderIdentity, which has the form "<pod-name>_<uuid>" (the UUID suffix
// never contains underscores, the Pod name may contain dashes).
func leaseHolderPodName(holder string) string {
	if idx := strings.LastIndex(holder, "_"); idx >= 0 {
		return holder[:idx]
	}
	return holder
}

// leaseHolderMatchesRunningPod reports whether the holderIdentity corresponds
// to one of the currently Running controller Pods (exclusive leadership by a
// live replica).
func leaseHolderMatchesRunningPod(holder string, podNames []string) bool {
	name := leaseHolderPodName(holder)
	for _, podName := range podNames {
		if podName == name {
			return true
		}
	}
	return false
}

// verifyLeaderElectionLease asserts that the controller-manager acquired
// leadership: the coordination.k8s.io Lease "ack-secret-manager-lock" exists
// in the leader-election namespace, carries a non-empty holderIdentity that
// corresponds to one of the Running controller Pods, and a recent renewTime.
// It is reusable: every Deployment restart (updateDeploymentAndRollout,
// restoreDeploymentArgs, restoreRRSAEnvBaseline) calls it to re-confirm
// leadership before subsequent specs exercise the lease-gated reconcile paths.
func verifyLeaderElectionLease(ctx context.Context) {
	By("verifying the leader election Lease exists, is actively renewed and held by a running Pod")

	leaseNamespace := leaderElectionLeaseNamespace(ctx)

	Eventually(func(g Gomega) {
		lease, err := clientset.CoordinationV1().Leases(leaseNamespace).Get(ctx, leaderElectionLeaseName, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred(), "Lease %s/%s should exist once leadership is acquired", leaseNamespace, leaderElectionLeaseName)
		g.Expect(lease.Spec.HolderIdentity).NotTo(BeNil(), "Lease holderIdentity must be set")
		holder := *lease.Spec.HolderIdentity
		g.Expect(holder).NotTo(BeEmpty(), "Lease holderIdentity must not be empty")
		g.Expect(lease.Spec.RenewTime).NotTo(BeNil(), "Lease renewTime must be set")
		g.Expect(time.Since(lease.Spec.RenewTime.Time)).To(BeNumerically("<", leaderRenewFreshnessWindow),
			"Lease renewTime should be fresh, indicating the holder is still renewing")

		podNames, err := runningControllerPodNames(ctx, ackSecretManagerNamespace)
		g.Expect(err).NotTo(HaveOccurred(), "controller Pods should be listable for the holderIdentity consistency check")
		g.Expect(leaseHolderMatchesRunningPod(holder, podNames)).To(BeTrue(),
			"Lease holderIdentity %q must correspond to one of the Running controller Pods %v", holder, podNames)
	}).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
		"leader election Lease %s/%s was not acquired and kept fresh by a running Pod", leaseNamespace, leaderElectionLeaseName)
}

// ptrToInt32 returns a pointer to the given int32 value, for patching
// pointer-typed Deployment fields such as Spec.Replicas.
func ptrToInt32(v int32) *int32 {
	return &v
}

// restoreDeploymentReplicas restores the Deployment's replica count to the
// captured baseline, waits for the resulting rollout and re-confirms
// leadership. Intended for DeferCleanup registration in specs that scale the
// controller Deployment, so the single-replica baseline (and a stable leader)
// is always restored even on failure paths.
func restoreDeploymentReplicas(ctx context.Context, namespace, deploymentName string, replicas int32) {
	By(fmt.Sprintf("restoring Deployment %s/%s replicas to %d", namespace, deploymentName, replicas))
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		Fail(fmt.Sprintf("failed to get Deployment %s/%s for replica restoration: %v", namespace, deploymentName, err))
	}
	if dep.Spec.Replicas != nil && *dep.Spec.Replicas == replicas {
		return
	}
	dep.Spec.Replicas = &replicas
	if _, err := clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{}); err != nil {
		Fail(fmt.Sprintf("failed to restore Deployment %s/%s replicas to %d; leaking the scaled replica count would break subsequent tests: %v",
			namespace, deploymentName, replicas, err))
	}
	waitForDeploymentRollout(ctx, namespace, deploymentName)
	// The scale change alters the live Pod population; re-confirm leadership.
	verifyLeaderElectionLease(ctx)
}

// addRRSAProjectedVolume adds the RRSA projected OIDC token volume and its
// volumeMount to the Deployment's first container (no-op when already present).
func addRRSAProjectedVolume(dep *appsv1.Deployment, volumeName, mountPath, tokenPath, audience string) {
	if hasRRSAProjectedVolume(dep, volumeName) {
		return
	}

	expSeconds := int64(7200)
	dep.Spec.Template.Spec.Volumes = append(dep.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name: volumeName,
			VolumeSource: corev1.VolumeSource{
				Projected: &corev1.ProjectedVolumeSource{
					Sources: []corev1.VolumeProjection{
						{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Path:              tokenPath,
								ExpirationSeconds: &expSeconds,
								Audience:          audience,
							},
						},
					},
				},
			},
		},
	)

	dep.Spec.Template.Spec.Containers[0].VolumeMounts = append(
		dep.Spec.Template.Spec.Containers[0].VolumeMounts,
		corev1.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		},
	)
}

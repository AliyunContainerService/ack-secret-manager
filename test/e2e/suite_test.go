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

	// Ensure RRSA is enabled on the ack-secret-manager Deployment.
	// RRSA requires a projected OIDC token volume; without it, all tests that
	// rely on RRSA / OIDC authentication will fail with
	// "open /var/run/secrets/tokens/ack-secret-manager: no such file or directory".
	ensureRRSAEnabled(ctx)
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
// Acts as a safety net: actively cleans up any remaining ExternalSecrets and their
// synced Secrets before deleting the namespace. This ensures cleanup even when a test
// fails before reaching its own CleanupExternalSecret/DeferCleanup calls.
func deleteTestNamespace(ctx context.Context, namespace *corev1.Namespace) {
	if namespace == nil {
		return
	}

	// Step 1: Actively delete any remaining ExternalSecrets in the namespace.
	// This is a safety net for tests that failed before reaching their own cleanup.
	esList := &api.ExternalSecretList{}
	if err := k8sClient.List(ctx, esList, client.InNamespace(namespace.Name)); err == nil {
		for i := range esList.Items {
			es := &esList.Items[i]
			// Delete synced Secret first (cleanupSecretOnFailure is false by default)
			syncedSecret := &corev1.Secret{}
			secretKey := types.NamespacedName{Name: es.Name, Namespace: es.Namespace}
			if err := k8sClient.Get(ctx, secretKey, syncedSecret); err == nil {
				_ = k8sClient.Delete(ctx, syncedSecret)
			}
			// Delete the ExternalSecret
			_ = k8sClient.Delete(ctx, es)
		}
	}

	// Step 2: Wait for all ExternalSecrets to be fully deleted
	Eventually(func() bool {
		checkList := &api.ExternalSecretList{}
		err := k8sClient.List(ctx, checkList, client.InNamespace(namespace.Name))
		if err != nil {
			return true // namespace may already be gone
		}
		return len(checkList.Items) == 0
	}, time.Second*30, time.Second*2).Should(BeTrue(), "All ExternalSecrets should be cleaned up before namespace deletion")

	// Step 3: Delete the namespace (cascades to all remaining Secrets, ConfigMaps, etc.)
	_ = k8sClient.Delete(ctx, namespace)
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
	// The controller creates a Secret with the same name as the ExternalSecret.
	// cleanupSecretOnFailure is false by default, so synced Secrets may persist.
	syncedSecret := &corev1.Secret{}
	secretKey := types.NamespacedName{Name: es.Name, Namespace: es.Namespace}
	if err := k8sClient.Get(ctx, secretKey, syncedSecret); err == nil {
		_ = k8sClient.Delete(ctx, syncedSecret)
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
		for i, result := range createdExternalSecret.Status.DataSyncResults {
			if result.Status != "Succeeded" {
				lastCheckError = fmt.Sprintf(
					"DataSyncResult[%d] should have status 'Succeeded', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
				return false
			}
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
// reports whether anything was removed. Shared by removeRRSAEnvTemporarily
// (per-test) and cleanupDeploymentRRSAEnv (AfterSuite).
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
// ALICLOUD_OIDC_PROVIDER_ARN) from the Deployment and triggers a rollout.
// It is used by WorkerRole tests: with the globally injected ENV OIDC
// credentials present, an ExternalSecret without SecretStoreRef would be
// served by ENV RRSA (auth chain priority 2) instead of WorkerRole
// (priority 5). Removing the two env vars forces the chain down to the
// WorkerRole provider.
//
// Callers MUST restore the baseline afterwards via restoreRRSAEnvBaseline
// (typically in DeferCleanup), otherwise subsequent tests lose ENV RRSA.
func removeRRSAEnvTemporarily(ctx context.Context, namespace, name string) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get Deployment for RRSA env removal")

	if !filterRRSAEnvVars(dep) {
		By("RRSA env vars already absent from Deployment, nothing to remove")
		return
	}

	updateDeploymentAndRollout(ctx, namespace, name, func(dep *appsv1.Deployment) {
		filterRRSAEnvVars(dep)
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
// drifted from the baseline. The env Secret is deliberately NOT deleted here
// (AfterSuite owns its final cleanup) so the secretKeyRef env vars always
// resolve and pods keep starting up.
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
}

// workerRoleEnabledInDeployment reports whether the ack-secret-manager
// Deployment runs with --enable-worker-role enabled. The flag defaults to
// true (see cmd/manager/main.go), so its absence means enabled; only an
// explicit --enable-worker-role=false disables the WorkerRole auth provider.
func workerRoleEnabledInDeployment(ctx context.Context, namespace, name string) bool {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to get Deployment %s/%s to inspect --enable-worker-role: %v\n", namespace, name, err)
		// Flag default is true; assume enabled when we cannot inspect.
		return true
	}
	for _, arg := range dep.Spec.Template.Spec.Containers[0].Args {
		if strings.HasPrefix(arg, "--enable-worker-role=") {
			return strings.TrimPrefix(arg, "--enable-worker-role=") == "true"
		}
	}
	return true
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

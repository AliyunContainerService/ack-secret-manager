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
		Fail("Required environment variables for ResourceManager are not set: ALIBABA_CLOUD_ACCOUNT_ID, CLUSTER_ID, ACK_WORKER_ROLE_NAME")
	}

	GlobalResourceManager, err = NewResourceManager(accountID, clusterID)
	Expect(err).NotTo(HaveOccurred())

	// Configure target account (Account B) credentials for cross-account testing
	// These are optional - if not set, cross-account tests will use same-account simulation
	remoteAccountID := os.Getenv("REMOTE_ACCOUNT_ID")
	remoteAccessKeyID := os.Getenv("REMOTE_ACCESS_KEY_ID")
	remoteAccessKeySecret := os.Getenv("REMOTE_ACCESS_KEY_SECRET")
	remoteKMSKeyID := os.Getenv("REMOTE_KMS_KEY_ID")
	remoteKMSInstanceID := os.Getenv("REMOTE_KMS_INSTANCE_ID")

	if remoteAccountID != "" && remoteAccessKeyID != "" && remoteAccessKeySecret != "" {
		By("configuring target account credentials for cross-account testing")
		err = GlobalResourceManager.SetRemoteAccountCredentials(remoteAccountID, remoteAccessKeyID, remoteAccessKeySecret, remoteKMSKeyID, remoteKMSInstanceID)
		Expect(err).NotTo(HaveOccurred())
	} else {
		By("cross-account credentials not configured, using same-account simulation for cross-account tests")
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

	// Clean up the RRSA env Secret created in ensureRRSAEnabled
	By("cleaning up RRSA env Secret")
	_ = clientset.CoreV1().Secrets("kube-system").Delete(ctx, "ack-secret-manager-rrsa-env", metav1.DeleteOptions{})

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

// CleanupExternalSecretAndSyncedSecret thoroughly cleans up an ExternalSecret and all
// associated K8s Secrets. This is critical because:
//  1. cleanupSecretOnFailure is false by default, so synced Secrets may persist after ExternalSecret deletion
//  2. The controller may not immediately clean up synced Secrets when ExternalSecret is deleted
//  3. If ExternalSecret is not cleaned up, deleteTestNamespace will hang waiting for it
//
// This function:
//   - Deletes all synced K8s Secrets (named after each DataSource entry)
//   - Deletes the ExternalSecret itself
//   - Waits for both to be fully gone
//
// Safe to call in DeferCleanup - tolerates already-deleted resources.
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

		// Check if there are sync results with Succeeded status
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

// Validate parsed secret content based on whether specific keys exist or if general data processing occurred
func validateParsedSecretContent(ctx context.Context, externalSecret *api.ExternalSecret, expectedKeyAliases []string) {
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

		// Check if ALL expected key aliases exist in the created Kubernetes secret
		missingKeys := []string{}
		for _, alias := range expectedKeyAliases {
			if kubeSecret.Data[alias] == nil {
				missingKeys = append(missingKeys, alias)
			}
		}

		if len(missingKeys) > 0 {
			lastCheckError = fmt.Sprintf("Missing expected keys in secret: %v", missingKeys)
			return false
		}

		// Only return true if ALL expected keys exist
		return true
	}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
		func() string {
			if lastCheckError != "" {
				return fmt.Sprintf("Parsed secret should contain all expected keys, but: %s", lastCheckError)
			}
			return "Parsed secret should contain all expected keys"
		})
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ACK Secret Manager E2E Suite")
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
	}).WithTimeout(time.Second*120).WithPolling(time.Second*5).Should(Succeed(),
		"Deployment %s/%s did not complete rollout within timeout", namespace, name)
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

	// Get and patch the Deployment
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get Deployment for RRSA env patch")

	needsUpdate := patchDeploymentEnvWithRRSA(ctx, dep)

	if needsUpdate {
		if dep.Spec.Template.Annotations == nil {
			dep.Spec.Template.Annotations = make(map[string]string)
		}
		dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
		_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to update Deployment with RRSA env vars")
		By("waiting for Deployment rollout after RRSA env patch")
		waitForDeploymentRollout(ctx, namespace, name)
	} else {
		By("Deployment already has correct RRSA env vars via secretKeyRef, skipping patch")
	}
}

// restoreDeploymentRRSAEnv removes RRSA env vars from the Deployment and triggers a rollout.
// Used to clean up after ENV-based authentication tests.
func restoreDeploymentRRSAEnv(ctx context.Context, namespace, name string) {
	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to get Deployment %s/%s for RRSA env restoration: %v\n", namespace, name, err)
		return
	}

	// Remove RRSA env vars
	rrsaEnvNames := map[string]bool{
		"ALICLOUD_ROLE_ARN":          true,
		"ALICLOUD_OIDC_PROVIDER_ARN": true,
	}
	var filteredEnv []corev1.EnvVar
	for _, env := range dep.Spec.Template.Spec.Containers[0].Env {
		if !rrsaEnvNames[env.Name] {
			filteredEnv = append(filteredEnv, env)
		}
	}
	dep.Spec.Template.Spec.Containers[0].Env = filteredEnv

	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to restore Deployment %s/%s RRSA env vars: %v\n", namespace, name, err)
		return
	}
	By("waiting for Deployment rollout after RRSA env cleanup")
	waitForDeploymentRollout(ctx, namespace, name)

	// Clean up the Secret
	_ = clientset.CoreV1().Secrets(namespace).Delete(ctx, "ack-secret-manager-rrsa-env", metav1.DeleteOptions{})
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
	const (
		deploymentName = "ack-secret-manager"
		namespace      = "kube-system"
	)

	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment for cross-namespace patch")

	originalArgs = make([]string, len(dep.Spec.Template.Spec.Containers[0].Args))
	copy(originalArgs, dep.Spec.Template.Spec.Containers[0].Args)

	newArgs := make([]string, 0, len(originalArgs))
	for _, arg := range originalArgs {
		if strings.HasPrefix(arg, "--enable-cross-namespace-secret-store") {
			continue
		}
		if strings.HasPrefix(arg, "--enable-cross-namespace-auth-ref") {
			continue
		}
		newArgs = append(newArgs, arg)
	}
	newArgs = append(newArgs,
		fmt.Sprintf("--enable-cross-namespace-secret-store=%v", enableSecretStore),
		fmt.Sprintf("--enable-cross-namespace-auth-ref=%v", enableAuthRef),
	)

	dep.Spec.Template.Spec.Containers[0].Args = newArgs
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to update Deployment cross-namespace args")

	By("waiting for Deployment rollout after cross-namespace args patch")
	waitForDeploymentRollout(ctx, namespace, deploymentName)

	return originalArgs
}

// restoreDeploymentArgs restores the original args of the ack-secret-manager Deployment.
func restoreDeploymentArgs(ctx context.Context, originalArgs []string) {
	const (
		deploymentName = "ack-secret-manager"
		namespace      = "kube-system"
	)

	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to get Deployment %s/%s for restoration: %v\n", namespace, deploymentName, err)
		return
	}

	dep.Spec.Template.Spec.Containers[0].Args = originalArgs
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = make(map[string]string)
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		GinkgoWriter.Printf("WARNING: failed to restore Deployment %s/%s args: %v\n", namespace, deploymentName, err)
		return
	}

	By("waiting for Deployment rollout after restoring args")
	waitForDeploymentRollout(ctx, namespace, deploymentName)
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
		deploymentName = "ack-secret-manager"
		namespace      = "kube-system"
		volumeName     = "ack-secret-manager"
		mountPath      = "/var/run/secrets/tokens"
		tokenPath      = "ack-secret-manager"
		audience       = "sts.aliyuncs.com"
	)

	dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment")

	needsUpdate := false

	// Check if the projected volume is already present
	hasVolume := false
	for _, v := range dep.Spec.Template.Spec.Volumes {
		if v.Name == volumeName && v.Projected != nil {
			hasVolume = true
			break
		}
	}

	if !hasVolume {
		By("patching Deployment to enable RRSA (projected OIDC token volume)")

		// Add projected volume with serviceAccountToken
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

		// Add volumeMount to the first container
		dep.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			dep.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      volumeName,
				MountPath: mountPath,
			},
		)
		needsUpdate = true
	} else {
		By("RRSA projected volume already configured, skipping volume patch")
	}

	// Ensure RRSA env vars are set for ENV-based authentication using shared helpers.
	ensureRRSAEnvSecret(ctx, namespace)
	if patchDeploymentEnvWithRRSA(ctx, dep) {
		needsUpdate = true
	}

	if needsUpdate {
		// Force rolling restart
		if dep.Spec.Template.Annotations == nil {
			dep.Spec.Template.Annotations = make(map[string]string)
		}
		dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

		_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred(), "failed to update Deployment with RRSA config")

		By("waiting for Deployment rollout after RRSA patch")
		waitForDeploymentRollout(ctx, namespace, deploymentName)
	}
}

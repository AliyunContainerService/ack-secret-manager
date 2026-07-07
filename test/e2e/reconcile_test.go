// reconcile_test.go - Reconcile E2E tests
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Reconcile E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-reconcile-"+getRandString())
	})

	AfterEach(func() {
		// Delete the test namespace
		// deleteTestNamespace(ctx, testNamespace)
	})

	Context("ExternalSecret reconciles when referenced SecretStore becomes available", func() {
		It("Should sync successfully after SecretStore is updated to become available", func() {
			// Create ExternalSecret referencing a non-existent SecretStore
			nonExistentStoreName := "non-existent-store"
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-reconcile-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "test-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      nonExistentStoreName,
								Namespace: testNamespace.Name,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Initially, ExternalSecret should fail to sync because SecretStore doesn't exist
			var lastCheckError string
			Consistently(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, createdExternalSecret)

				if err != nil {
					lastCheckError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
					return false
				}
				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					lastCheckError = "DataSyncResults is empty, waiting for sync results..."
					return false
				}

				// Check if there are sync failures due to missing SecretStore
				for i, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" {
						lastCheckError = fmt.Sprintf(
							"DataSyncResult[%d] should have status 'Failed', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
						return false
					}
				}
				lastCheckError = ""
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastCheckError != "" {
						return fmt.Sprintf("ExternalSecret should consistently fail to sync because SecretStore doesn't exist, but: %s", lastCheckError)
					}
					return "ExternalSecret should consistently fail to sync because SecretStore doesn't exist"
				})

			// Now create the SecretStore that the ExternalSecret references
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nonExistentStoreName,
					Namespace: testNamespace.Name,
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

			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())

			// Wait for SecretStore to be ready
			var lastSecretStoreError string
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secretStore.Name,
					Namespace: secretStore.Namespace,
				}, createdStore)
				if err != nil {
					lastSecretStoreError = fmt.Sprintf("Error getting SecretStore: %v", err)
					return false
				}

				if len(createdStore.Status.Conditions) == 0 {
					lastSecretStoreError = "SecretStore has no status conditions"
					return false
				}

				for _, condition := range createdStore.Status.Conditions {
					if condition.Type != api.SecretStoreReady || condition.Status != corev1.ConditionTrue {
						lastSecretStoreError = fmt.Sprintf("SecretStoreReady condition type is %s, expected Ready, status is %s, expected True, reason: %s, message: %s", condition.Type, string(condition.Status), condition.Reason, condition.Message)
						return false
					}
				}

				return true
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastSecretStoreError != "" {
						return fmt.Sprintf("SecretStore should eventually become ready, but: %s", lastSecretStoreError)
					}
					return "SecretStore should eventually become ready"
				})

			// Eventually, the ExternalSecret should reconcile and sync successfully now that SecretStore exists
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				// Handle complete cleanup to avoid race conditions with AfterEach
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
			})
			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("ExternalSecret reconciles when remote credential is updated", func() {
		It("Should update local secret when remote credential changes", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-update-store",
					Namespace: testNamespace.Name,
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

			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())

			// Wait for SecretStore to be ready
			var lastSecretStoreError string
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secretStore.Name,
					Namespace: secretStore.Namespace,
				}, createdStore)
				if err != nil {
					lastSecretStoreError = fmt.Sprintf("Error getting SecretStore: %v", err)
					return false
				}

				if len(createdStore.Status.Conditions) == 0 {
					lastSecretStoreError = "SecretStore has no status conditions"
					return false
				}

				for _, condition := range createdStore.Status.Conditions {
					if condition.Type != api.SecretStoreReady || condition.Status != corev1.ConditionTrue {
						lastSecretStoreError = fmt.Sprintf("SecretStoreReady condition type is %s, expected Ready, status is %s, expected True, reason: %s, message: %s", condition.Type, string(condition.Status), condition.Reason, condition.Message)
						return false
					}
				}

				return true
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastSecretStoreError != "" {
						return fmt.Sprintf("SecretStore should eventually become ready, but: %s", lastSecretStoreError)
					}
					return "SecretStore should eventually become ready"
				})

			// Create ExternalSecret to fetch data
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-update-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  CommonKMSSecretName,
							Name: "test-secret-key",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      secretStore.Name,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Validate ExternalSecret initially syncs successfully
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Get the initial synced secret
			initialSecret := &corev1.Secret{}
			var lastInitialSecretError string
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, initialSecret)
				if err != nil {
					lastInitialSecretError = fmt.Sprintf("Failed to get initial secret: %v", err)
					return false
				}
				lastInitialSecretError = ""
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastInitialSecretError != "" {
						return fmt.Sprintf("should get initial synced secret, but: %s", lastInitialSecretError)
					}
					return "should get initial synced secret"
				})

			initialData := make(map[string][]byte)
			for k, v := range initialSecret.Data {
				initialData[k] = make([]byte, len(v))
				copy(initialData[k], v)
			}

			// Change remote credential
			if GlobalResourceManager != nil {
				err := GlobalResourceManager.PutSecretValueForCommonKMSCredential(ctx, CommonKMSSecretName)
				if err != nil {
					fmt.Printf("Failed to update common KMS credential: %v\n", err)
				}
			} else {
				fmt.Printf("GlobalResourceManager is not initialized\n")
			}

			// Simulate a delay to allow for the next reconcile cycle to detect changes
			// (In practice, this would involve changing the actual remote credential)
			// For testing purposes, we'll wait and then re-check the secret
			time.Sleep(time.Second * 30)

			// After the reconcile interval, check if the secret has been updated
			var lastUpdateCheckError string
			Eventually(func() bool {
				currentSecret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, currentSecret)

				if err != nil {
					lastUpdateCheckError = fmt.Sprintf("Failed to get current secret: %v", err)
					return false
				}

				// Check if the secret has been updated in any way
				// Either by adding new keys, removing old keys, or changing values
				hasChanged := false

				// Check for value changes in existing keys
				for key, initialValue := range initialData {
					currentValue, exists := currentSecret.Data[key]
					if !exists {
						// A key disappeared, this is a change
						hasChanged = true
						break
					}

					// If values are different, it's a change
					if !bytes.Equal(initialValue, currentValue) {
						hasChanged = true
						break
					}
				}

				// If no differences found in existing keys, check for new keys
				if !hasChanged {
					for key := range currentSecret.Data {
						if _, exists := initialData[key]; !exists {
							// Found a new key that wasn't in the original, this is a change
							hasChanged = true
							break
						}
					}
				}

				if !hasChanged {
					lastUpdateCheckError = "secret has not been updated after remote credential change"
					return false
				}

				lastUpdateCheckError = ""
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*10).Should(BeTrue(),
				func() string {
					if lastUpdateCheckError != "" {
						return fmt.Sprintf("secret should be updated after remote credential change, but: %s", lastUpdateCheckError)
					}
					return "secret should be updated after remote credential change"
				})

			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				// Handle complete cleanup to avoid race conditions with AfterEach
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
			})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("ClusterExternalSecret creates ExternalSecrets in new namespaces", func() {
		It("Should create ExternalSecrets in newly created namespaces that match conditions", func() {
			// Create ClusterSecretStore
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster-store" + getRandString(),
				},
				Spec: api.ClusterSecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							RAMRoleARN:      RAMRoleArnForRRSA,
							OIDCProviderARN: OIDCProviderARN,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, clusterSecretStore)).To(Succeed())
			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())
			})

			// Wait for ClusterSecretStore to be ready
			var lastClusterStoreError string
			Eventually(func() bool {
				createdStore := &api.ClusterSecretStore{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: clusterSecretStore.Name,
				}, createdStore)
				if err != nil {
					lastClusterStoreError = fmt.Sprintf("Error getting ClusterSecretStore: %v", err)
					return false
				}

				if len(createdStore.Status.Conditions) == 0 {
					lastClusterStoreError = "ClusterSecretStore has no status conditions"
					return false
				}

				for _, condition := range createdStore.Status.Conditions {
					if condition.Type != api.SecretStoreReady || condition.Status != corev1.ConditionTrue {
						lastClusterStoreError = fmt.Sprintf("SecretStoreReady condition type is %s, expected Ready, status is %s, expected True, reason: %s, message: %s", condition.Type, string(condition.Status), condition.Reason, condition.Message)
						return false
					}
				}

				return true
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastClusterStoreError != "" {
						return fmt.Sprintf("ClusterSecretStore should eventually become ready, but: %s", lastClusterStoreError)
					}
					return "ClusterSecretStore should eventually become ready"
				})

			// Create ClusterExternalSecret with namespace selector
			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster-externalsecret" + getRandString(),
				},
				Spec: api.ClusterExternalSecretSpec{
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					NamespaceSelectors: []*metav1.LabelSelector{
						{
							MatchLabels: map[string]string{
								"environment": "test",
							},
						},
					},
					ExternalSecretSpec: api.ExternalSecretSpec{
						Provider: "kms",
						Data: []api.DataSource{
							{
								Key:       CommonKMSSecretName,
								Name:      "cluster-secret-key",
								VersionId: "v1",
								SecretStoreRef: &api.SecretStoreRef{
									Name: clusterSecretStore.Name,
									Kind: ResourceClusterSecretStore,
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())

			// Create a new namespace that matches the label selector
			newTestNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "new-test-namespace-" + getRandString(),
					Labels: map[string]string{
						"environment": "test",
					},
				},
			}

			Expect(k8sClient.Create(ctx, newTestNamespace)).To(Succeed())

			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				// 1. Delete the ClusterExternalSecret to trigger controller cleanup
				Expect(k8sClient.Delete(ctx, clusterExternalSecret)).To(Succeed())

				// 2. Wait for all ExternalSecrets to be cleaned up in both namespaces
				Eventually(func() bool {
					externalSecretList := &api.ExternalSecretList{}
					err := k8sClient.List(ctx, externalSecretList, client.InNamespace(testNamespace.Name))
					if err != nil {
						return false
					}

					return len(externalSecretList.Items) == 0
				}, time.Second*30, time.Second*2).Should(BeTrue(), "All ExternalSecrets should be cleaned up before namespace deletion")

				// 3. Now safely delete both namespaces
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
				Expect(k8sClient.Delete(ctx, newTestNamespace)).To(Succeed())

			})

			// Eventually, an ExternalSecret should be created in the new namespace
			var lastExternalSecretError string
			Eventually(func() bool {
				externalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: newTestNamespace.Name,
				}, externalSecret)

				// Return true if the ExternalSecret exists, false otherwise
				if err != nil {
					lastExternalSecretError = fmt.Sprintf("ExternalSecret was not created in new namespace %s: %v", newTestNamespace.Name, err)
					return false
				}
				lastExternalSecretError = ""
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastExternalSecretError != "" {
						return fmt.Sprintf("ExternalSecret should be created in new namespace that matches conditions, but: %s", lastExternalSecretError)
					}
					return "ExternalSecret should be created in new namespace that matches conditions"
				})
		})
	})

	Context("SecretStore reconciles when credentials secret is updated", func() {
		It("Should reconcile when accessKeyId/accessKeySecret in referenced secret is updated", func() {
			// Create Secret with invalid credentials first
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "aksk-secret-credentials-update",
					Namespace: testNamespace.Name,
				},
				Data: map[string][]byte{
					"accessKeyId":     []byte("InvalidAccessKeyId"),
					"accessKeySecret": []byte("InvalidAccessKeySecret"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-credentials-update-store",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							AccessKey: &api.SecretRef{
								Name:      secret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      secret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())

			// Wait for SecretStore to become ready but client is uninvalid due to wrong AK/SK
			var lastUnreadyError string
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secretStore.Name,
					Namespace: secretStore.Namespace,
				}, createdStore)
				if err != nil {
					lastUnreadyError = fmt.Sprintf("Error getting SecretStore: %v", err)
					return false
				}

				if len(createdStore.Status.Conditions) == 0 {
					lastUnreadyError = "SecretStore has no status conditions"
					return false
				}

				for _, condition := range createdStore.Status.Conditions {
					if condition.Type != api.SecretStoreReady || condition.Status != corev1.ConditionTrue {
						lastUnreadyError = fmt.Sprintf("SecretStoreReady condition type is %s, expected Ready, status is %s, expected True, reason: %s, message: %s", condition.Type, string(condition.Status), condition.Reason, condition.Message)
						return false
					}
				}

				return true
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastUnreadyError != "" {
						return fmt.Sprintf("SecretStore should become ready but client is uninvalid due to wrong AK/SK, but: %s", lastUnreadyError)
					}
					return "SecretStore should become ready but client is uninvalid due to wrong AK/SK"
				})

			// Create an ExternalSecret that uses this SecretStore
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cred-update-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "test-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      secretStore.Name,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Initially, ExternalSecret should fail to sync because of invalid credentials.
			// Use Eventually first to wait for the controller to process the ExternalSecret
			// and populate DataSyncResults with Failed status.
			var lastInitialFailError string
			Eventually(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, createdExternalSecret)

				if err != nil {
					lastInitialFailError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
					return false
				}
				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					lastInitialFailError = "DataSyncResults is empty, controller has not processed ExternalSecret yet"
					return false
				}

				// Check if there are sync failures due to invalid credentials
				for i, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" {
						lastInitialFailError = fmt.Sprintf(
							"DataSyncResult[%d] should have status 'Failed', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
						return false
					}
				}
				lastInitialFailError = ""
				return true
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastInitialFailError != "" {
						return fmt.Sprintf("ExternalSecret should fail to sync because of invalid credentials, but: %s", lastInitialFailError)
					}
					return "ExternalSecret should fail to sync because of invalid credentials"
				})

			// Now update the secret with valid credentials to trigger re-reconciliation
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secret.Name,
					Namespace: secret.Namespace,
				}, secret)
				if err != nil {
					return err
				}

				// Update with valid credentials to trigger reconcile
				secret.Data["accessKeyId"] = []byte(RAMUserAccessKeyID)
				secret.Data["accessKeySecret"] = []byte(RAMUserAccessKeySecret)
				return k8sClient.Update(ctx, secret)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"should update secret with valid credentials")

			// Wait for SecretStore reconcile after secret data update
			time.Sleep(time.Second * 30)

			// Eventually, the ExternalSecret should reconcile and sync successfully now that credentials are valid
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				// Handle complete cleanup to avoid race conditions with AfterEach
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
			})
			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("SecretStore reconciles when ServiceAccount annotations are updated", func() {
		It("Should reconcile when service account annotations are updated", func() {
			// Create SecretStore that references the service account
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa-update-store",
					Namespace: ServiceaccountNamespaceForSAAuth.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							ServiceAccountRef: &api.ServiceAccountRef{
								Name:      ServiceaccountNameForSAAuth,
								Namespace: ServiceaccountNamespaceForSAAuth.Name,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())

			// Wait for SecretStore to become ready
			var lastUnreadySAError string
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secretStore.Name,
					Namespace: secretStore.Namespace,
				}, createdStore)
				if err != nil {
					lastUnreadySAError = fmt.Sprintf("Error getting SecretStore: %v", err)
					return false
				}

				if len(createdStore.Status.Conditions) == 0 {
					lastUnreadySAError = "SecretStore has no status conditions"
					return false
				}

				for _, condition := range createdStore.Status.Conditions {
					if condition.Type != api.SecretStoreReady || condition.Status != corev1.ConditionTrue {
						lastUnreadySAError = fmt.Sprintf("SecretStoreReady condition type is %s, expected Ready, status is %s, expected True, reason: %s, message: %s", condition.Type, string(condition.Status), condition.Reason, condition.Message)
						return false
					}
				}

				return true
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastUnreadySAError != "" {
						return fmt.Sprintf("SecretStore should become ready, but: %s", lastUnreadySAError)
					}
					return "SecretStore should become ready"
				})

			// Create an ExternalSecret that uses this SecretStore
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa-update-externalsecret",
					Namespace: ServiceaccountNamespaceForSAAuth.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "test-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      secretStore.Name,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Eventually, the ExternalSecret should reconcile and sync successfully now that service account annotation is valid
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Update service account annotations with valid role ARN to trigger reconcile
			Eventually(func() error {
				serviceAccount := &corev1.ServiceAccount{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ServiceaccountNameForSAAuth,
					Namespace: ServiceaccountNamespaceForSAAuth.Name,
				}, serviceAccount)
				if err != nil {
					return err
				}

				// Update annotation with valid role ARN to trigger reconcile
				serviceAccount.Annotations[ACKRRSAAnnotation] = "arn:aws:iam::123456789012:role/invalid-role"
				return k8sClient.Update(ctx, serviceAccount)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"should update service account with valid role ARN")

			// wait for SecretStore reconcile after service account annotation update
			time.Sleep(time.Second * 30)

			// Initially, ExternalSecret should fail to sync because of invalid service account annotation
			var lastInitialSAFailError string
			Consistently(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, createdExternalSecret)

				if err != nil {
					lastInitialSAFailError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
					return false
				}

				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					return false
				}
				// Check if there are sync failures due to invalid service account annotation
				for i, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" {
						lastInitialSAFailError = fmt.Sprintf(
							"DataSyncResult[%d] should have status 'Failed', got '%s'.", i, result.Status)
						return false
					}
				}
				lastInitialSAFailError = ""
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastInitialSAFailError != "" {
						return fmt.Sprintf("ExternalSecret should consistently fail to sync because of invalid service account annotation, but: %s", lastInitialSAFailError)
					}
					return "ExternalSecret should consistently fail to sync because of invalid service account annotation"
				})

			// Update service account annotations with valid role ARN to trigger reconcile
			Eventually(func() error {
				serviceAccount := &corev1.ServiceAccount{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ServiceaccountNameForSAAuth,
					Namespace: ServiceaccountNamespaceForSAAuth.Name,
				}, serviceAccount)
				if err != nil {
					return err
				}

				// Update annotation with valid role ARN to trigger reconcile
				serviceAccount.Annotations[ACKRRSAAnnotation] = RAMRoleArnForSAAuth
				return k8sClient.Update(ctx, serviceAccount)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"should update service account with valid role ARN")

			// wait for SecretStore reconcile after service account annotation update
			time.Sleep(time.Second * 30)

			// Eventually, the ExternalSecret should reconcile and sync successfully now that service account annotation is valid
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				// Handle complete cleanup to avoid race conditions with AfterEach
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
			})
			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})
})

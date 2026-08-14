// reconcile_test.go - Reconcile E2E tests
package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
		// Safety net: delete the test namespace even if the test failed before
		// reaching its in-body DeferCleanup registration, so namespaces never leak.
		// Idempotent: tests whose DeferCleanup already deleted the namespace are tolerated.
		if testNamespace == nil {
			return
		}
		check := &corev1.Namespace{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: testNamespace.Name}, check); err != nil {
			if k8serrors.IsNotFound(err) {
				// Already cleaned up by the test body's DeferCleanup
				return
			}
			Expect(err).NotTo(HaveOccurred())
		}
		deleteTestNamespace(ctx, testNamespace)
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

			// Wait for the controller to reconcile and populate DataSyncResults with Failed status
			// Consistently alone would fail immediately because the controller needs time to process
			var lastCheckError string
			Eventually(func() bool {
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
						return fmt.Sprintf("ExternalSecret should fail to sync because SecretStore doesn't exist, but: %s", lastCheckError)
					}
					return "ExternalSecret should fail to sync because SecretStore doesn't exist"
				})

			// Now verify the Failed status persists consistently
			Consistently(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, createdExternalSecret)

				if err != nil {
					return false
				}
				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					return false
				}

				for _, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" {
						return false
					}
				}
				return true
			}).WithTimeout(time.Second*10).WithPolling(time.Second*2).Should(BeTrue(),
				"ExternalSecret should consistently remain in Failed state while SecretStore doesn't exist")

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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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

			// Create a KMS Secret dedicated to this test case so that appending a new
			// version never mutates the shared CommonKMSSecretName used by other specs
			// (Ginkgo may run specs in random order).
			Expect(GlobalResourceManager).NotTo(BeNil())
			dedicatedKMSSecretName, err := GlobalResourceManager.CreateKMSSecretForCredentialUpdate(ctx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				Expect(GlobalResourceManager.DeleteKMSSecret(dedicatedKMSSecretName)).To(Succeed())
			})

			// Wait for SecretStore to be ready
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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
							Key:  dedicatedKMSSecretName,
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

			// Change remote credential by appending a new version to the dedicated KMS secret
			Expect(GlobalResourceManager.PutSecretValueForKMSSecret(ctx, dedicatedKMSSecretName)).To(Succeed())

			// After the reconcile interval, the local secret should hold the exact v2 value
			// written by PutSecretValueForKMSSecret
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

				if string(currentSecret.Data["test-secret-key"]) != `{"key1":"value1","key2":"value2"}` {
					lastUpdateCheckError = fmt.Sprintf("secret data not updated to the v2 value yet, got: %q", string(currentSecret.Data["test-secret-key"]))
					return false
				}

				lastUpdateCheckError = ""
				return true
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastUpdateCheckError != "" {
						return fmt.Sprintf("secret should be updated to the v2 value after remote credential change, but: %s", lastUpdateCheckError)
					}
					return "secret should be updated to the v2 value after remote credential change"
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
					err := k8sClient.List(ctx, externalSecretList, client.InNamespace(newTestNamespace.Name))
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

			// Verify the child ExternalSecret actually syncs successfully in the new namespace,
			// not merely that it exists
			validateExternalSecretSucceededAndSecretCreated(ctx, newTestNamespace.Name, clusterExternalSecret.Name, time.Second*60)
		})
	})

	Context("SecretStore reconciles when credentials secret is updated", func() {
		It("Should reconcile when accessKeyId/accessKeySecret in referenced secret is updated", func() {
			// Create Secret with invalid credentials first
			secret := createAKSecret(ctx, testNamespace.Name, "aksk-secret-credentials-update", "InvalidAccessKeyId", "InvalidAccessKeySecret")

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

			// Wait for SecretStore to become ready but client is invalid due to wrong AK/SK
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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

			// Wait for the SecretStore to reconcile and become Ready again with the valid
			// credentials, instead of blindly sleeping
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Eventually, the ExternalSecret should reconcile and sync successfully now that credentials are valid
			var lastResyncError string
			Eventually(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, createdExternalSecret); err != nil {
					lastResyncError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
					return false
				}
				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					lastResyncError = "DataSyncResults is empty, controller has not reconciled yet"
					return false
				}
				for i, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Succeeded" {
						lastResyncError = fmt.Sprintf(
							"DataSyncResult[%d] should have status 'Succeeded', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
						return false
					}
				}
				syncedSecret := &corev1.Secret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, syncedSecret); err != nil {
					lastResyncError = fmt.Sprintf("Failed to get corresponding Secret: %v", err)
					return false
				}
				lastResyncError = ""
				return true
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastResyncError != "" {
						return fmt.Sprintf("ExternalSecret should reconcile and sync successfully after credentials are updated, but: %s", lastResyncError)
					}
					return "ExternalSecret should reconcile and sync successfully after credentials are updated"
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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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

			// Record the original ServiceAccount annotation and register an unconditional
			// restoration before any mutation happens: this ServiceAccount is shared by
			// other specs, so it must never be left in a broken state even if this spec
			// fails midway.
			originalSA := &corev1.ServiceAccount{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      ServiceaccountNameForSAAuth,
				Namespace: ServiceaccountNamespaceForSAAuth.Name,
			}, originalSA)).To(Succeed())
			originalAnnotation := originalSA.Annotations[ACKRRSAAnnotation]
			DeferCleanup(func() {
				// Restore the original annotation and wait until the restoration is effective
				Eventually(func() error {
					serviceAccount := &corev1.ServiceAccount{}
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      ServiceaccountNameForSAAuth,
						Namespace: ServiceaccountNamespaceForSAAuth.Name,
					}, serviceAccount)
					if err != nil {
						return err
					}

					serviceAccount.Annotations[ACKRRSAAnnotation] = originalAnnotation
					return k8sClient.Update(ctx, serviceAccount)
				}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
					"should restore original role ARN annotation on the shared service account")
			})

			// Eventually, the ExternalSecret should reconcile and sync successfully now that service account annotation is valid
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Update service account annotation to trigger reconcile
			Eventually(func() error {
				serviceAccount := &corev1.ServiceAccount{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ServiceaccountNameForSAAuth,
					Namespace: ServiceaccountNamespaceForSAAuth.Name,
				}, serviceAccount)
				if err != nil {
					return err
				}

				// Write an invalid role ARN to trigger sync failure
				serviceAccount.Annotations[ACKRRSAAnnotation] = "arn:aws:iam::123456789012:role/invalid-role"
				return k8sClient.Update(ctx, serviceAccount)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"should update service account annotation with invalid role ARN")

			// Wait for the ExternalSecret to reconcile into Failed state after the invalid
			// annotation is written, instead of blindly sleeping
			var lastReconcileFailError string
			Eventually(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, createdExternalSecret)

				if err != nil {
					lastReconcileFailError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
					return false
				}
				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					lastReconcileFailError = "DataSyncResults is empty, controller has not reconciled yet"
					return false
				}
				for i, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" {
						lastReconcileFailError = fmt.Sprintf(
							"DataSyncResult[%d] should have status 'Failed', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
						return false
					}
				}
				lastReconcileFailError = ""
				return true
			}).WithTimeout(time.Second*90).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastReconcileFailError != "" {
						return fmt.Sprintf("ExternalSecret should reconcile into Failed state after annotation becomes invalid, but: %s", lastReconcileFailError)
					}
					return "ExternalSecret should reconcile into Failed state after annotation becomes invalid"
				})

			// Verify the failure state persists consistently
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

			// Restore the valid role ARN to trigger reconcile again
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

			// Eventually, the ExternalSecret should reconcile and sync successfully again
			// now that the service account annotation is restored to the valid role ARN
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			// Clean up - this SecretStore lives in the shared ServiceaccountNamespaceForSAAuth
			// namespace, so it must be deleted explicitly together with the ExternalSecret
			// to avoid leaking into other specs. The testNamespace created by BeforeEach is
			// not used by this Context and is cleaned up by the Describe-level AfterEach.
			CleanupExternalSecret(ctx, externalSecret)
			Expect(k8sClient.Delete(ctx, secretStore)).To(Succeed())
		})
	})

	// Covers the ClusterExternalSecret cleanup contract implemented by
	// handleDeletion (deleting the CES reclaims all child ExternalSecrets) and
	// cleanupOrphanedExternalSecrets (a namespace that stops matching has its
	// orphaned child ExternalSecret deleted) in clusterexternalsecret_controller.go.
	//
	// Every spec below uses a randomly valued dedicated label so the CES only
	// matches the namespaces created inside the spec itself (the selector is
	// cluster-wide and must not collide with other specs' namespaces).
	Context("ClusterExternalSecret cleanup contract", func() {
		It("Should delete all child ExternalSecrets when the ClusterExternalSecret is deleted", func() {
			labelKey := "e2e-ces-cleanup-contract"
			labelValue := getRandString()

			By("creating two namespaces that match the ClusterExternalSecret selector")
			cesNamespace1 := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-reconcile-cleanup1-" + getRandString(),
					Labels: map[string]string{labelKey: labelValue},
				},
			}
			Expect(k8sClient.Create(ctx, cesNamespace1)).To(Succeed())
			cesNamespace2 := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-reconcile-cleanup2-" + getRandString(),
					Labels: map[string]string{labelKey: labelValue},
				},
			}
			Expect(k8sClient.Create(ctx, cesNamespace2)).To(Succeed())
			DeferCleanup(func() {
				// DeferCleanup runs LIFO, so this fires AFTER the CES cleanup
				// registered below has reclaimed the child ExternalSecrets.
				_ = k8sClient.Delete(ctx, cesNamespace1)
				_ = k8sClient.Delete(ctx, cesNamespace2)
			})

			By("creating a ClusterExternalSecret that matches both namespaces")
			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cleanup-delete-ces-" + getRandString(),
				},
				Spec: api.ClusterExternalSecretSpec{
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					ExternalSecretSpec: api.ExternalSecretSpec{
						Provider: "kms",
						Data: []api.DataSource{
							{
								Key:       CommonKMSSecretName,
								Name:      "test-secret-key",
								VersionId: "v1",
							},
						},
					},
					Conditions: []api.ClusterExternalSecretCondition{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{labelKey: labelValue},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())
			DeferCleanup(func() {
				// Idempotent: the spec body already deletes the CES on the happy path.
				if err := k8sClient.Delete(ctx, clusterExternalSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterExternalSecret %s: %v\n", clusterExternalSecret.Name, err)
					return
				}
				// Wait for all child ExternalSecrets to be reclaimed before the
				// namespaces above are deleted.
				Eventually(func() bool {
					es1 := &api.ExternalSecret{}
					err1 := k8sClient.Get(ctx, types.NamespacedName{
						Name: clusterExternalSecret.Name, Namespace: cesNamespace1.Name,
					}, es1)
					es2 := &api.ExternalSecret{}
					err2 := k8sClient.Get(ctx, types.NamespacedName{
						Name: clusterExternalSecret.Name, Namespace: cesNamespace2.Name,
					}, es2)
					return k8serrors.IsNotFound(err1) && k8serrors.IsNotFound(err2)
				}, time.Second*60, time.Second*2).Should(BeTrue(), "All child ExternalSecrets should be cleaned up before namespace deletion")
			})

			By("waiting for child ExternalSecrets to sync successfully in both namespaces")
			validateExternalSecretSucceededAndSecretCreated(ctx, cesNamespace1.Name, clusterExternalSecret.Name, time.Second*60)
			validateExternalSecretSucceededAndSecretCreated(ctx, cesNamespace2.Name, clusterExternalSecret.Name, time.Second*60)

			By("deleting the ClusterExternalSecret")
			Expect(k8sClient.Delete(ctx, clusterExternalSecret)).To(Succeed())

			By("waiting for the ClusterExternalSecret itself to disappear (finalizer removed)")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterExternalSecret.Name}, &api.ClusterExternalSecret{})
				return k8serrors.IsNotFound(err)
			}).WithTimeout(time.Second*60).WithPolling(time.Second*2).Should(BeTrue(),
				"ClusterExternalSecret should be fully deleted after handleDeletion removes the finalizer")

			By("waiting for all child ExternalSecrets to be reclaimed by handleDeletion")
			Eventually(func() bool {
				es1 := &api.ExternalSecret{}
				err1 := k8sClient.Get(ctx, types.NamespacedName{
					Name: clusterExternalSecret.Name, Namespace: cesNamespace1.Name,
				}, es1)
				es2 := &api.ExternalSecret{}
				err2 := k8sClient.Get(ctx, types.NamespacedName{
					Name: clusterExternalSecret.Name, Namespace: cesNamespace2.Name,
				}, es2)
				return k8serrors.IsNotFound(err1) && k8serrors.IsNotFound(err2)
			}).WithTimeout(time.Second*60).WithPolling(time.Second*2).Should(BeTrue(),
				"all child ExternalSecrets should be deleted when the ClusterExternalSecret is deleted")
		})

		It("Should delete the orphaned child ExternalSecret when a namespace no longer matches", func() {
			labelKey := "e2e-ces-cleanup-contract"
			labelValue := getRandString()

			By("creating two namespaces that match the ClusterExternalSecret selector")
			cesNamespace1 := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-reconcile-cleanup1-" + getRandString(),
					Labels: map[string]string{labelKey: labelValue},
				},
			}
			Expect(k8sClient.Create(ctx, cesNamespace1)).To(Succeed())
			cesNamespace2 := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-reconcile-cleanup2-" + getRandString(),
					Labels: map[string]string{labelKey: labelValue},
				},
			}
			Expect(k8sClient.Create(ctx, cesNamespace2)).To(Succeed())
			DeferCleanup(func() {
				// DeferCleanup runs LIFO, so this fires AFTER the CES cleanup
				// registered below has reclaimed the remaining child ExternalSecret.
				_ = k8sClient.Delete(ctx, cesNamespace1)
				_ = k8sClient.Delete(ctx, cesNamespace2)
			})

			By("creating a ClusterExternalSecret that matches both namespaces")
			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cleanup-orphan-ces-" + getRandString(),
				},
				Spec: api.ClusterExternalSecretSpec{
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					ExternalSecretSpec: api.ExternalSecretSpec{
						Provider: "kms",
						Data: []api.DataSource{
							{
								Key:       CommonKMSSecretName,
								Name:      "test-secret-key",
								VersionId: "v1",
							},
						},
					},
					Conditions: []api.ClusterExternalSecretCondition{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{labelKey: labelValue},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())
			DeferCleanup(func() {
				// Idempotent CES deletion + wait for the surviving child
				// ExternalSecret to be reclaimed before namespace deletion.
				if err := k8sClient.Delete(ctx, clusterExternalSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterExternalSecret %s: %v\n", clusterExternalSecret.Name, err)
					return
				}
				Eventually(func() bool {
					es1 := &api.ExternalSecret{}
					err1 := k8sClient.Get(ctx, types.NamespacedName{
						Name: clusterExternalSecret.Name, Namespace: cesNamespace1.Name,
					}, es1)
					es2 := &api.ExternalSecret{}
					err2 := k8sClient.Get(ctx, types.NamespacedName{
						Name: clusterExternalSecret.Name, Namespace: cesNamespace2.Name,
					}, es2)
					return k8serrors.IsNotFound(err1) && k8serrors.IsNotFound(err2)
				}, time.Second*60, time.Second*2).Should(BeTrue(), "All child ExternalSecrets should be cleaned up before namespace deletion")
			})

			By("waiting for child ExternalSecrets to sync successfully in both namespaces")
			validateExternalSecretSucceededAndSecretCreated(ctx, cesNamespace1.Name, clusterExternalSecret.Name, time.Second*60)
			validateExternalSecretSucceededAndSecretCreated(ctx, cesNamespace2.Name, clusterExternalSecret.Name, time.Second*60)

			By("removing the matching label from the first namespace")
			Eventually(func() error {
				ns := &corev1.Namespace{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: cesNamespace1.Name}, ns); err != nil {
					return err
				}
				delete(ns.Labels, labelKey)
				return k8sClient.Update(ctx, ns)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"should remove the matching label from namespace %s", cesNamespace1.Name)

			By("waiting for the orphaned child ExternalSecret to be deleted")
			// cleanupOrphanedExternalSecrets runs on the CES's next periodic
			// reconcile (driven by rotationInterval), so allow a generous window.
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: clusterExternalSecret.Name, Namespace: cesNamespace1.Name,
				}, &api.ExternalSecret{})
				return k8serrors.IsNotFound(err)
			}).WithTimeout(time.Second*120).WithPolling(time.Second*5).Should(BeTrue(),
				"orphaned child ExternalSecret should be deleted after the namespace stops matching")

			By("verifying the child ExternalSecret in the still-matching namespace is preserved")
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterExternalSecret.Name, Namespace: cesNamespace2.Name,
			}, &api.ExternalSecret{})).To(Succeed(),
				"child ExternalSecret in the still-matching namespace %s must not be deleted", cesNamespace2.Name)
		})
	})

	// Covers the fail-closed namespace matching contract of
	// IsNamespaceAllowedForClusterExternalSecret (pkg/utils/util.go): when
	// namespaceSelectors are configured but match no namespace, the CES must
	// NOT provision a child ExternalSecret anywhere (fail-closed), and its
	// status must surface the non-provisioning via FailedNamespaces and
	// Ready=False (handleNoMatchingNamespaces in clusterexternalsecret_controller.go).
	Context("ClusterExternalSecret with selectors matching no namespace", func() {
		It("Should not create child ExternalSecrets in non-matching namespaces and report Ready=False", func() {
			// Create ClusterSecretStore so the negative assertion is not
			// confounded by store-resolution failures (mirrors the positive
			// CES spec above).
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-failclosed-store-" + getRandString(),
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

			// Create a ClusterExternalSecret whose namespaceSelectors point at
			// a label that no namespace carries, so nothing may match.
			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-failclosed-ces-" + getRandString(),
				},
				Spec: api.ClusterExternalSecretSpec{
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					NamespaceSelectors: []*metav1.LabelSelector{
						{
							MatchLabels: map[string]string{
								"env": "nonexistent-label-xyz",
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
			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				// Idempotent: the spec body already deletes the CES on the happy path.
				if err := k8sClient.Delete(ctx, clusterExternalSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterExternalSecret %s: %v\n", clusterExternalSecret.Name, err)
				}
			})

			// Wait until the CES reconciles into a non-provisioning state:
			// Ready=False with FailedNamespaces populated. Waiting for the
			// status first guarantees the negative assertion below cannot pass
			// merely because the controller has not run yet.
			var lastFailClosedError string
			Eventually(func() bool {
				createdCES := &api.ClusterExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterExternalSecret.Name}, createdCES); err != nil {
					lastFailClosedError = fmt.Sprintf("Failed to get ClusterExternalSecret: %v", err)
					return false
				}
				if len(createdCES.Status.ProvisionedNamespaces) != 0 {
					lastFailClosedError = fmt.Sprintf("ProvisionedNamespaces should be empty, got %v", createdCES.Status.ProvisionedNamespaces)
					return false
				}
				if len(createdCES.Status.FailedNamespaces) == 0 {
					lastFailClosedError = "FailedNamespaces is empty, waiting for the controller to record non-matching namespaces"
					return false
				}
				for _, condition := range createdCES.Status.Conditions {
					if condition.Type == api.ClusterExternalSecretReady && condition.Status == corev1.ConditionFalse {
						lastFailClosedError = ""
						return true
					}
				}
				lastFailClosedError = "expected a Ready=False condition on the ClusterExternalSecret"
				return false
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastFailClosedError != "" {
						return fmt.Sprintf("ClusterExternalSecret should report Ready=False with FailedNamespaces when no namespace matches, but: %s", lastFailClosedError)
					}
					return "ClusterExternalSecret should report Ready=False with FailedNamespaces when no namespace matches"
				})

			// Reverse assertion: while the CES stays in the non-provisioning
			// state, no child ExternalSecret may ever appear in the (unlabeled)
			// test namespace.
			Consistently(func() bool {
				childExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: testNamespace.Name,
				}, childExternalSecret)
				if err == nil {
					return false
				}
				return k8serrors.IsNotFound(err)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				"no child ExternalSecret should be created in a namespace that does not match the namespaceSelectors")

			// Clean up the ClusterExternalSecret explicitly; the namespaces
			// hold no child ExternalSecrets and are cleaned by the Describe-level
			// AfterEach / the DeferCleanup registered above.
			Expect(k8sClient.Delete(ctx, clusterExternalSecret)).To(Succeed())
		})
	})
})

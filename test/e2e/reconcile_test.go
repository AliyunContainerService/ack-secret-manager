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
					// NotFound means the ES disappeared (real failure);
					// transient API errors are inconclusive, keep polling.
					return k8serrors.IsNotFound(err)
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

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
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

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
		})
	})

	Context("ExternalSecret periodic polling refreshes SynchronizationTime", func() {
		It("Should refresh SynchronizationTime when a periodic sync round writes new data", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-polling-refresh-store",
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

			// Dedicated KMS secret so staging a new version never touches the
			// shared CommonKMSSecretName used by other specs.
			Expect(GlobalResourceManager).NotTo(BeNil())
			dedicatedKMSSecretName, err := GlobalResourceManager.CreateKMSSecretForCredentialUpdate(ctx)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				Expect(GlobalResourceManager.DeleteKMSSecret(dedicatedKMSSecretName)).To(Succeed())
			})

			// Wait for SecretStore to be ready
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Short rotation so a periodic poll fires well inside the assertion window.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-polling-refresh-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							// No VersionId: the periodic poll fetches the latest
							// version, so the staged remote value becomes observable.
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

			By("waiting for the first successful sync (non-zero SynchronizationTime)")
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)
			baseline := recordExternalSecretSyncBaseline(ctx, externalSecret.Namespace, externalSecret.Name)

			By("staging a new remote version that only a fresh periodic sync can observe")
			// The changed data makes the next poll round actually write the Secret,
			// which forces the status write and refreshes SynchronizationTime
			// (unchanged-data polls are debounced by design and keep the old stamp).
			Expect(GlobalResourceManager.PutSecretValueForKMSSecret(ctx, dedicatedKMSSecretName)).To(Succeed())

			By("asserting SynchronizationTime advances within a few rotation periods")
			Eventually(func() bool {
				latest := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: externalSecret.Namespace,
					Name:      externalSecret.Name,
				}, latest); err != nil {
					return false
				}
				return latestSyncTimeOf(latest).Time.After(baseline.Time)
			}).WithTimeout(time.Second*90).WithPolling(time.Second*2).Should(BeTrue(),
				"SynchronizationTime of ExternalSecret %s/%s should advance after a periodic sync round writes new data",
				externalSecret.Namespace, externalSecret.Name)

			By("validating the synced Secret holds the staged v2 value written by the poll round")
			Eventually(func() string {
				currentSecret := &corev1.Secret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: externalSecret.Namespace,
					Name:      externalSecret.Name,
				}, currentSecret); err != nil {
					return ""
				}
				return string(currentSecret.Data["test-secret-key"])
			}).WithTimeout(time.Second*30).WithPolling(time.Second*2).Should(Equal(`{"key1":"value1","key2":"value2"}`),
				"synced Secret should hold the staged remote value after the periodic sync round")

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
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
				// The AfterEach safety net (deleteTestNamespace) runs BEFORE this
				// It-level DeferCleanup in Ginkgo v2, so the namespace is usually
				// already gone here — tolerate NotFound instead of failing.
				if err := k8sClient.Delete(ctx, testNamespace); err != nil && !k8serrors.IsNotFound(err) {
					Expect(err).NotTo(HaveOccurred())
				}
			})
			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
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
							OIDCProviderARN: OIDCProviderARN,
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
					// NotFound means the ES disappeared (real failure);
					// transient API errors are inconclusive, keep polling.
					lastInitialSAFailError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
					return k8serrors.IsNotFound(err)
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
			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			Expect(k8sClient.Delete(ctx, secretStore)).To(Succeed())
		})
	})

	// Covers the rewritten Secret/ServiceAccount controllers' ClusterSecretStore
	// reference scan (secret_controller.go / serviceaccount_controller.go): a
	// credential Secret or ServiceAccount change must patch the trigger
	// annotation onto every referencing ClusterSecretStore so its backend
	// client is rebuilt and referencing ExternalSecrets recover -- the exact
	// cluster-scoped counterpart of the two SecretStore Contexts above.
	//
	// Design principle: every ExternalSecret pins rotationInterval to 30m
	// (storeWatchLongInterval), so a recovery observed inside the short
	// assertion window can only come from the Secret/SA watch -> trigger
	// annotation -> client rebuild chain, never from the rotation poll.
	Context("Secret and ServiceAccount watches rebuild ClusterSecretStore clients", func() {
		It("Should recover a ClusterSecretStore-backed ExternalSecret when the referenced AK credential Secret is fixed", func() {
			By("creating an AK Secret with invalid credentials")
			akSecret := createAKSecret(ctx, testNamespace.Name, "css-cred-update-aksk-"+getRandString(),
				"InvalidAccessKeyId", "InvalidAccessKeySecret")

			By("creating a ClusterSecretStore that references the AK Secret")
			// ClusterSecretStore is cluster-scoped, so both SecretRefs must
			// carry an explicit namespace (validateClusterSecretStoreSpec).
			css := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-css-cred-update-" + getRandString(),
				},
				Spec: api.ClusterSecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							AccessKey: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, css)).To(Succeed())
			// Registered FIRST, runs LAST (LIFO): the cluster-scoped CSS is
			// never cascade-deleted with the namespace, so it must be removed
			// explicitly -- but only after the ExternalSecret is gone.
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, css); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", css.Name, err)
				}
			})
			// An invalid AK does not fail client creation itself, so the CSS
			// still becomes Ready (mirrors the SecretStore variant above).
			waitForClusterSecretStoreReady(ctx, css.Name)

			// ClientGeneration baseline recorded BEFORE the credential fix: the
			// recovery chain (Secret watch -> trigger annotation -> client
			// rebuild) must visibly bump it.
			genBefore := recordClusterSecretStoreClientGeneration(ctx, css.Name)

			By("creating an ExternalSecret with a 30m rotationInterval referencing the CSS")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-css-cred-update-es-" + getRandString(),
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: storeWatchLongInterval},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "test-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name: css.Name,
								Kind: ResourceClusterSecretStore,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			By("waiting for the ExternalSecret to fail because of the invalid credentials")
			expectExternalSecretAllResultsFailed(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)

			By("updating the AK Secret with valid credentials")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name: akSecret.Name, Namespace: akSecret.Namespace,
				}, akSecret); err != nil {
					return err
				}
				akSecret.Data["accessKeyId"] = []byte(RAMUserAccessKeyID)
				akSecret.Data["accessKeySecret"] = []byte(RAMUserAccessKeySecret)
				return k8sClient.Update(ctx, akSecret)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"should update the AK Secret with valid credentials")

			By("observing the ExternalSecret recover and the target Secret appear")
			// The 30m rotation rules out the poll: the recovery inside this
			// window can only come from the Secret watch -> CSS trigger
			// annotation -> client rebuild -> ES reverse watch chain.
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)

			By("observing the CSS controller bump Status.ClientGeneration for the rebuilt client")
			expectClusterSecretStoreClientGenerationAdvanced(ctx, css.Name, genBefore,
				"the AK credential Secret fix")
		})

		It("Should recover a ClusterSecretStore-backed ExternalSecret when the referenced ServiceAccount gains the RRSA annotation", func() {
			saName := "css-sa-update-sa-" + getRandString()

			By("creating a RAM role trusting the ServiceAccount")
			roleArn, roleName, err := GlobalResourceManager.CreateRamRoleForServiceAccount(ctx, testNamespace.Name, saName)
			Expect(err).NotTo(HaveOccurred(), "failed to create RAM role for ServiceAccount %s/%s", testNamespace.Name, saName)
			DeferCleanup(func() {
				if err := GlobalResourceManager.DeleteRamRole(roleName); err != nil {
					GinkgoWriter.Printf("WARNING: failed to delete RAM role %s: %v\n", roleName, err)
				}
			})

			By("creating the ServiceAccount WITHOUT the role-arn annotation")
			serviceAccount := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      saName,
					Namespace: testNamespace.Name,
				},
			}
			Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())

			By("creating a ClusterSecretStore that references the annotation-less ServiceAccount")
			css := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-css-sa-update-" + getRandString(),
				},
				Spec: api.ClusterSecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							ServiceAccountRef: &api.ServiceAccountRef{
								Name:      saName,
								Namespace: testNamespace.Name,
							},
							OIDCProviderARN: OIDCProviderARN,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, css)).To(Succeed())
			// Registered FIRST, runs LAST (LIFO): cluster-scoped CSS cleanup
			// after the ExternalSecret is reclaimed.
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, css); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", css.Name, err)
				}
			})

			By("creating an ExternalSecret with a 30m rotationInterval referencing the CSS")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-css-sa-update-es-" + getRandString(),
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: storeWatchLongInterval},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "test-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name: css.Name,
								Kind: ResourceClusterSecretStore,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			By("waiting for the ExternalSecret to fail while the ServiceAccount lacks the RRSA annotation")
			expectExternalSecretAllResultsFailed(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)

			// ClientGeneration baseline recorded BEFORE the annotation fix: the
			// recovery chain (SA watch -> trigger annotation -> client rebuild)
			// must visibly bump it.
			genBefore := recordClusterSecretStoreClientGeneration(ctx, css.Name)

			By("adding the role-arn annotation to the ServiceAccount")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name: saName, Namespace: testNamespace.Name,
				}, serviceAccount); err != nil {
					return err
				}
				if serviceAccount.Annotations == nil {
					serviceAccount.Annotations = make(map[string]string)
				}
				serviceAccount.Annotations[ACKRRSAAnnotation] = roleArn
				return k8sClient.Update(ctx, serviceAccount)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"should add the role-arn annotation to the ServiceAccount")

			By("observing the ExternalSecret recover and the target Secret appear")
			// The SA watch predicate fires because the annotation appears on
			// the new object; the 30m rotation rules out the poll, so the
			// recovery can only come from the SA watch -> trigger annotation
			// -> client rebuild chain.
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)

			By("observing the CSS controller bump Status.ClientGeneration for the rebuilt client")
			expectClusterSecretStoreClientGenerationAdvanced(ctx, css.Name, genBefore,
				"the ServiceAccount RRSA annotation fix")
		})
	})


	// Covers the --watch-namespaces/--exclude-namespaces flags of the
	// ExternalSecret controller (v0.6.7, community-convention scoping).
	// Semantics under test: a namespace listed in --exclude-namespaces is
	// dropped at the watch level by the blacklist predicate, so its
	// ExternalSecret events never reach the work queue; every other
	// namespace stays watchable. The excluded namespace must never have
	// its ExternalSecret synced while a non-excluded namespace keeps
	// syncing normally; the args are restored afterwards (patch/restore).
	Context("ExternalSecret controller honors --exclude-namespaces", func() {
		It("Should not sync ExternalSecrets in the excluded namespace while other namespaces sync normally", func() {
			By("creating the excluded namespace and a control namespace")
			excludedNamespace := createTestNamespace(ctx, "test-exclude-ns-"+getRandString())
			controlNamespace := createTestNamespace(ctx, "test-exclude-ctrl-"+getRandString())
			DeferCleanup(func() {
				deleteTestNamespace(ctx, controlNamespace)
			})
			DeferCleanup(func() {
				deleteTestNamespace(ctx, excludedNamespace)
			})

			By("patching --exclude-namespaces=<excluded namespace> onto the controller Deployment")
			// Register the restore BEFORE mutating (suite convention, same as
			// cluster_store_flag_test.go): a mid-spec failure still returns
			// the args to their pre-spec state.
			originalArgs := getDeploymentArgs(ctx)
			DeferCleanup(func() {
				By("restoring Deployment args after the --exclude-namespaces spec")
				restoreDeploymentArgs(ctx, originalArgs)
			})
			patchDeploymentArgs(ctx,
				[]string{"--watch-namespaces", "--exclude-namespaces"},
				[]string{"--exclude-namespaces=" + excludedNamespace.Name})

			By("creating identical ExternalSecrets in both namespaces")
			newExcludeProbeES := func(namespace, name string) *api.ExternalSecret {
				return &api.ExternalSecret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: api.ExternalSecretSpec{
						Provider:         "kms",
						RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
						Data: []api.DataSource{
							{
								Key:       CommonKMSSecretName,
								Name:      "exclude-probe-key",
								VersionId: "v1",
							},
						},
					},
				}
			}
			excludedES := newExcludeProbeES(excludedNamespace.Name, "test-exclude-probe-es")
			Expect(k8sClient.Create(ctx, excludedES)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, excludedES)
			})
			controlES := newExcludeProbeES(controlNamespace.Name, "test-exclude-probe-es")
			Expect(k8sClient.Create(ctx, controlES)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, controlES)
			})

			By("observing the control namespace ExternalSecret sync normally")
			validateExternalSecretSucceededAndSecretCreated(ctx, controlNamespace.Name, controlES.Name, time.Second*90)

			By("consistently observing the excluded namespace ExternalSecret is never synced")
			// The blacklist predicate keeps every event of the excluded
			// namespace out of the work queue, so no status write and no
			// synced Secret may ever appear. The check only fails on hard
			// evidence (a Succeeded result or an existing synced Secret),
			// tolerating the at-most-one status write an in-flight operator
			// restart could still land from the pre-patch pod.
			Consistently(func() bool {
				es := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: excludedNamespace.Name, Name: excludedES.Name,
				}, es); err != nil {
					// Transient API errors are not evidence of a sync.
					return true
				}
				for _, result := range es.Status.DataSyncResults {
					if result.Status == "Succeeded" {
						return false
					}
				}
				syncedSecret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: excludedNamespace.Name, Name: excludedES.Name,
				}, syncedSecret)
				if err == nil {
					return false
				}
				if k8serrors.IsNotFound(err) {
					return true
				}
				// Transient API errors are not evidence of a sync.
				return true
			}).WithTimeout(time.Second*45).WithPolling(time.Second*5).Should(BeTrue(),
				"the ExternalSecret in the excluded namespace must never sync while --exclude-namespaces is active")
		})
	})

	// Covers the include branch of the namespace scoping when
	// --watch-namespaces and --exclude-namespaces are combined WITHOUT
	// overlap. main.go maps every --watch-namespaces entry to true and
	// every --exclude-namespaces entry to false; a namespace listed in
	// BOTH flags makes the operator refuse to start (fail-fast conflict
	// detection), so the combination under test is disjoint. include mode
	// is active (a true entry exists): the manager cache is narrowed via
	// DefaultNamespaces to the include list, so (a) a namespace in the
	// include list syncs normally; (b) a namespace in the exclude list is
	// invisible to the controller; (c) an unlisted namespace is invisible
	// as well. For (b) and (c) no reconcile ever runs, hence no status is
	// written and no synced Secret is ever created.
	Context("ExternalSecret controller honors --watch-namespaces combined with --exclude-namespaces", func() {
		It("Should sync the included namespace while the excluded and unlisted namespaces stay unwatched", func() {
			By("creating included, excluded and unlisted probe namespaces")
			includedNamespace := createTestNamespace(ctx, "test-watch-inc-"+getRandString())
			excludedNamespace := createTestNamespace(ctx, "test-watch-excl-"+getRandString())
			unlistedNamespace := createTestNamespace(ctx, "test-watch-unlisted-"+getRandString())
			DeferCleanup(func() {
				deleteTestNamespace(ctx, unlistedNamespace)
			})
			DeferCleanup(func() {
				deleteTestNamespace(ctx, excludedNamespace)
			})
			DeferCleanup(func() {
				deleteTestNamespace(ctx, includedNamespace)
			})

			By("patching --watch-namespaces (included) together with --exclude-namespaces (excluded)")
			// Register the restore BEFORE mutating (suite convention): a
			// mid-spec failure still returns the args to their pre-spec state.
			originalArgs := getDeploymentArgs(ctx)
			DeferCleanup(func() {
				By("restoring Deployment args after the watch+exclude spec")
				restoreDeploymentArgs(ctx, originalArgs)
			})
			patchDeploymentArgs(ctx,
				[]string{"--watch-namespaces", "--exclude-namespaces"},
				[]string{
					"--watch-namespaces=" + includedNamespace.Name,
					"--exclude-namespaces=" + excludedNamespace.Name,
				})

			By("creating identical ExternalSecrets in all three namespaces")
			newWatchProbeES := func(namespace, name string) *api.ExternalSecret {
				return &api.ExternalSecret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: api.ExternalSecretSpec{
						Provider:         "kms",
						RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
						Data: []api.DataSource{
							{
								Key:       CommonKMSSecretName,
								Name:      "watch-probe-key",
								VersionId: "v1",
							},
						},
					},
				}
			}
			includedES := newWatchProbeES(includedNamespace.Name, "test-watch-probe-es")
			Expect(k8sClient.Create(ctx, includedES)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, includedES)
			})
			excludedES := newWatchProbeES(excludedNamespace.Name, "test-watch-probe-es")
			Expect(k8sClient.Create(ctx, excludedES)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, excludedES)
			})
			unlistedES := newWatchProbeES(unlistedNamespace.Name, "test-watch-probe-es")
			Expect(k8sClient.Create(ctx, unlistedES)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, unlistedES)
			})

			By("observing the included namespace ExternalSecret sync normally (include branch active)")
			// Establishing the positive leg first proves the controller is
			// live under the new args, so the negative legs below cannot pass
			// merely because reconciliation has not run yet.
			validateExternalSecretSucceededAndSecretCreated(ctx, includedNamespace.Name, includedES.Name, time.Second*90)

			By("consistently observing that neither unwatched namespace ever gets a synced Secret")
			// With the whitelist cache both the excluded and the unlisted
			// namespace fall outside the manager cache entirely: their ES
			// events never reach the controller, so the former reconcile
			// guard's "is not in watched namespaces" status is never written
			// either -- the only observable contract is the permanent absence
			// of a synced Secret.
			noSyncedSecret := func(namespace, name string) bool {
				es := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: namespace, Name: name,
				}, es); err == nil {
					for _, result := range es.Status.DataSyncResults {
						if result.Status == "Succeeded" {
							return false
						}
					}
				}
				syncedSecret := &corev1.Secret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: namespace, Name: name,
				}, syncedSecret); err == nil {
					return false
				}
				return true
			}
			Consistently(func() bool {
				return noSyncedSecret(excludedNamespace.Name, excludedES.Name) &&
					noSyncedSecret(unlistedNamespace.Name, unlistedES.Name)
			}).WithTimeout(time.Second*45).WithPolling(time.Second*5).Should(BeTrue(),
				"neither the excluded nor the unlisted namespace may ever sync while the watch+exclude combination is active")
		})
	})
})




// cssControllerDisabledStatusKey is the documented dataSyncResults entry the
// ExternalSecret controller persists while consuming a ClusterSecretStore
// with --process-cluster-secret-store=false (degraded mode: the cached
// client is reused and a Warning notice is recorded). It is NOT a data-key
// sync verdict, so "all results Failed" style assertions must exempt it.
const cssControllerDisabledStatusKey = "cluster_secret_store_controller_disabled"

// expectExternalSecretAllResultsFailed waits until the ExternalSecret exposes
// a non-empty DataSyncResults slice whose every entry is Failed. Unlike
// expectExternalSecretFailedWith it does not constrain the Reason text, so it
// stays robust against the exact backend error wording (invalid credentials,
// missing SA annotation, ...). The documented degraded-mode notice entry
// (cssControllerDisabledStatusKey, Status=Warning) is exempt: it coexists
// with the Failed data-key entries when the ClusterSecretStore controller is
// disabled, and skipping it keeps this assertion focused on the real sync
// verdicts.
func expectExternalSecretAllResultsFailed(ctx context.Context, namespace, name string, timeout time.Duration) {
	var lastCheckError string
	Eventually(func() bool {
		es := &api.ExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, es); err != nil {
			lastCheckError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
			return false
		}
		if len(es.Status.DataSyncResults) == 0 {
			lastCheckError = "DataSyncResults is empty, waiting for sync results..."
			return false
		}
		for i, result := range es.Status.DataSyncResults {
			if result.ExternalSecretKey == cssControllerDisabledStatusKey {
				// Documented degraded-mode notice, not a data-key verdict.
				continue
			}
			if result.Status != "Failed" {
				lastCheckError = fmt.Sprintf(
					"DataSyncResult[%d] should have status 'Failed', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
				return false
			}
		}
		lastCheckError = ""
		return true
	}).WithTimeout(timeout).WithPolling(time.Second*5).Should(BeTrue(),
		func() string {
			if lastCheckError != "" {
				return fmt.Sprintf("ExternalSecret %s/%s should have every DataSyncResult Failed, but: %s", namespace, name, lastCheckError)
			}
			return fmt.Sprintf("ExternalSecret %s/%s should have every DataSyncResult Failed", namespace, name)
		})
}



// cross_namespace_ref_test.go - Cross Namespace Reference E2E tests
package e2e

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Cross Namespace Reference E2E", func() {
	var (
		testNamespace   *corev1.Namespace
		testNamespace2  *corev1.Namespace
		ctx             = context.Background()
		originalDepArgs []string
	)

	BeforeEach(func() {
		// Patch deployment to disable cross-namespace references so the controller
		// rejects cross-namespace ServiceAccount / Secret / SecretStore refs.
		originalDepArgs = patchDeploymentCrossNamespaceArgs(ctx, false, false)

		testNamespace = createTestNamespace(ctx, "test-cross-ns-ref-"+getRandString())
		testNamespace2 = createTestNamespace(ctx, "test-cross-ns-ref2-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
		deleteTestNamespace(ctx, testNamespace2)

		// Restore original deployment args
		if len(originalDepArgs) > 0 {
			restoreDeploymentArgs(ctx, originalDepArgs)
		}
	})

	Context("ExternalSecret cross namespace reference to SecretStore", func() {
		It("Should not allow cross namespace reference to SecretStore", func() {
			// Create SecretStore in testNamespace2
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cross-ns-secretstore",
					Namespace: testNamespace2.Name,
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

			// Create ExternalSecret in testNamespace that references SecretStore in testNamespace2
			// This should fail because cross namespace reference is disabled
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cross-ns-externalsecret",
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
								Namespace: testNamespace2.Name, // Cross namespace reference
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Validate ExternalSecret status contains error information (because cross namespace reference is disabled)
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
				// Check if there are failed sync results
				for i, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" {
						lastCheckError = fmt.Sprintf(
							"DataSyncResult[%d] should have status 'Failed', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
						return false
					}
				}
				lastCheckError = ""
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*10).Should(BeTrue(),
				func() string {
					if lastCheckError != "" {
						return fmt.Sprintf("ExternalSecret should eventually fail to sync due to cross namespace reference, but: %s", lastCheckError)
					}
					return "ExternalSecret should eventually fail to sync due to cross namespace reference"
				})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("SecretStore cross namespace reference to ServiceAccount", func() {
		It("Should not allow cross namespace reference to ServiceAccount", func() {
			// Create ServiceAccount in testNamespace2
			serviceAccount := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cross-ns-serviceaccount",
					Namespace: testNamespace2.Name,
					Annotations: map[string]string{
						ACKRRSAAnnotation: RAMRoleArnForSAAuth,
					},
				},
			}
			Expect(k8sClient.Create(ctx, serviceAccount)).To(Succeed())

			// Create SecretStore in testNamespace that references ServiceAccount in testNamespace2
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cross-sa-secretstore",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							ServiceAccountRef: &api.ServiceAccountRef{
								Name:      serviceAccount.Name,
								Namespace: testNamespace2.Name, // Cross namespace reference
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())

			// Validate SecretStore status contains error information (because cross namespace reference is disabled)
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

				// Check if the store remains unready due to cross namespace reference issue
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionTrue {
						lastSecretStoreError = "SecretStore became ready despite cross namespace reference issue"
						return false
					}
				}
				return true
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastSecretStoreError != "" {
						return fmt.Sprintf("SecretStore should remain unready due to cross namespace reference, but: %s", lastSecretStoreError)
					}
					return "SecretStore should remain unready due to cross namespace reference"
				})
		})
	})

	Context("SecretStore cross namespace reference to authentication Secret", func() {
		It("Should not allow cross namespace reference to authentication Secret", func() {
			// Create Secret with AKSK authentication in testNamespace2 (different namespace)
			authSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "aksk-auth-secret",
					Namespace: testNamespace2.Name,
				},
				Data: map[string][]byte{
					"accessKeyId":     []byte(RAMUserAccessKeyID),
					"accessKeySecret": []byte(RAMUserAccessKeySecret),
				},
			}
			Expect(k8sClient.Create(ctx, authSecret)).To(Succeed())

			// Create SecretStore in testNamespace that references authentication Secret in testNamespace2
			// This should fail because cross namespace reference is disabled
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cross-secret-secretstore",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							AccessKey: &api.SecretRef{
								Name:      authSecret.Name,
								Namespace: testNamespace2.Name, // Cross namespace reference
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      authSecret.Name,
								Namespace: testNamespace2.Name, // Cross namespace reference
								Key:       "accessKeySecret",
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())

			// Validate SecretStore status contains error information (because cross namespace reference is disabled)
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
				// Check if the store remains unready due to cross namespace reference issue
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionTrue {
						lastSecretStoreError = "SecretStore became ready despite cross namespace reference issue"
						return false
					}
				}
				return true
			}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastSecretStoreError != "" {
						return fmt.Sprintf("SecretStore should remain unready due to cross namespace reference, but: %s", lastSecretStoreError)
					}
					return "SecretStore should remain unready due to cross namespace reference"
				})
		})
	})
})

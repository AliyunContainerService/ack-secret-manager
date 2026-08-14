// cross_namespace_ref_test.go - Cross Namespace Reference E2E tests
package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Cross Namespace Reference E2E", Ordered, func() {
	var (
		testNamespace   *corev1.Namespace
		testNamespace2  *corev1.Namespace
		ctx             = context.Background()
		originalDepArgs []string
	)

	// Ordered container: the Deployment is patched once in BeforeAll for all
	// enclosed specs (all specs use the same args) and restored afterwards,
	// so each spec no longer triggers two extra rollouts. Namespaces are
	// still created/deleted per spec to keep isolation unchanged.
	BeforeAll(func() {
		// Capture the args baseline and register the restore cleanup BEFORE
		// patching, so the cleanup is always registered even when the patch
		// below fails midway (otherwise the modified Deployment args would
		// leak into every subsequent suite). restoreDeploymentArgs is
		// idempotent: it skips the rollout when the baseline already matches,
		// so this is also safe when the patch itself is skipped as a no-op.
		originalDepArgs = getDeploymentArgs(ctx)
		DeferCleanup(func() {
			// Restore original deployment args
			if len(originalDepArgs) > 0 {
				restoreDeploymentArgs(ctx, originalDepArgs)
			}
		})
		// Patch deployment to disable cross-namespace references so the controller
		// rejects cross-namespace ServiceAccount / Secret / SecretStore refs.
		patchDeploymentCrossNamespaceArgs(ctx, false, false)
	})

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-cross-ns-ref-"+getRandString())
		testNamespace2 = createTestNamespace(ctx, "test-cross-ns-ref2-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
		deleteTestNamespace(ctx, testNamespace2)
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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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
				// Check if there are failed sync results, and lock the failure
				// reason to the cross-namespace rejection error emitted by the
				// ExternalSecret controller (see validateSecretStoreAccess /
				// getSecretStore), so the test cannot pass for unrelated errors.
				for i, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" {
						lastCheckError = fmt.Sprintf(
							"DataSyncResult[%d] should have status 'Failed', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
						return false
					}
					if !strings.Contains(result.Reason, "cross namespace SecretStore reference is disabled") {
						lastCheckError = fmt.Sprintf(
							"DataSyncResult[%d] should fail due to cross namespace SecretStore reference rejection, got Reason: '%s'", i, result.Reason)
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

				// Lock the failure reason to the cross-namespace ServiceAccountRef
				// rejection error emitted by the SecretStore controller
				// (validateStoreSpec), so the test cannot pass for unrelated errors.
				rejectionFound := false
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionFalse &&
						strings.Contains(condition.Message, "cross namespace ServiceAccountRef is disabled") {
						rejectionFound = true
						break
					}
				}
				if !rejectionFound {
					lastSecretStoreError = "SecretStore is unready but no condition carries the cross namespace ServiceAccountRef rejection message"
					return false
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
			authSecret := createAKSecret(ctx, testNamespace2.Name, "aksk-auth-secret", RAMUserAccessKeyID, RAMUserAccessKeySecret)

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

				// Lock the failure reason to the cross-namespace AccessKey/
				// AccessKeySecret rejection error emitted by the SecretStore
				// controller (validateSecretStoreSpec), so the test cannot pass
				// for unrelated errors.
				rejectionFound := false
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionFalse &&
						(strings.Contains(condition.Message, "cross namespace AccessKey is disabled") ||
							strings.Contains(condition.Message, "cross namespace AccessKeySecret is disabled")) {
						rejectionFound = true
						break
					}
				}
				if !rejectionFound {
					lastSecretStoreError = "SecretStore is unready but no condition carries the cross namespace AccessKey rejection message"
					return false
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

// Positive counterpart of the Describe above: with both cross-namespace flags
// enabled (--enable-cross-namespace-secret-store=true and
// --enable-cross-namespace-auth-ref=true), the exact same cross-namespace
// topologies that are rejected by default must sync successfully. The three
// Contexts below mirror the negative cases one to one (same cross-namespace
// layout, only the switches flipped on).
var _ = Describe("Cross Namespace Reference Enabled E2E", Ordered, func() {
	var (
		testNamespace   *corev1.Namespace
		testNamespace2  *corev1.Namespace
		ctx             = context.Background()
		originalDepArgs []string
	)

	// Ordered container: the Deployment is patched once in BeforeAll for all
	// enclosed specs (all specs use the same args) and restored afterwards,
	// so each spec no longer triggers two extra rollouts. Namespaces are
	// still created/deleted per spec to keep isolation unchanged.
	BeforeAll(func() {
		// Same baseline/restore pattern as the first Describe's BeforeAll above.
		originalDepArgs = getDeploymentArgs(ctx)
		DeferCleanup(func() {
			// Restore original deployment args
			if len(originalDepArgs) > 0 {
				restoreDeploymentArgs(ctx, originalDepArgs)
			}
		})
		// Patch deployment to enable cross-namespace references so the controller
		// accepts cross-namespace ServiceAccount / Secret / SecretStore refs.
		patchDeploymentCrossNamespaceArgs(ctx, true, true)
	})

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-cross-ns-ref-"+getRandString())
		testNamespace2 = createTestNamespace(ctx, "test-cross-ns-ref2-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
		deleteTestNamespace(ctx, testNamespace2)
	})

	Context("ExternalSecret cross namespace reference to SecretStore", func() {
		It("Should allow cross namespace reference to SecretStore when enabled", func() {
			// Create RRSA SecretStore in testNamespace2 and wait until Ready
			secretStore := createRRSASecretStore(ctx, testNamespace2.Name, "test-cross-ns-secretstore")

			// Create ExternalSecret in testNamespace that references SecretStore in testNamespace2
			// This should succeed because cross namespace reference is enabled
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
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			// Validate ExternalSecret syncs successfully across namespaces
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)
		})
	})

	Context("SecretStore cross namespace reference to ServiceAccount", func() {
		It("Should allow cross namespace reference to ServiceAccount when enabled", func() {
			// Create ServiceAccount in testNamespace2 backed by a dynamic RAM role
			// whose trust policy targets exactly this (namespace, ServiceAccount)
			// pair. The shared global SA cannot be used here: its role's trust
			// policy only trusts the global SA, so an STS AssumeRoleWithOIDC for
			// any other annotated SA fails with 401.
			serviceAccount := createRRSAServiceAccountForTest(ctx, testNamespace2.Name, "test-cross-ns-serviceaccount")

			// Create SecretStore in testNamespace that references ServiceAccount in testNamespace2
			// This should become Ready because cross namespace reference is enabled
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

			// Wait for SecretStore to be ready despite the cross namespace ServiceAccountRef
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Create ExternalSecret using the SecretStore to prove the cross namespace
			// ServiceAccount authentication actually works end-to-end
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cross-sa-externalsecret",
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
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)
		})
	})

	Context("SecretStore cross namespace reference to authentication Secret", func() {
		It("Should allow cross namespace reference to authentication Secret when enabled", func() {
			// Create Secret with AKSK authentication in testNamespace2 (different namespace)
			authSecret := createAKSecret(ctx, testNamespace2.Name, "aksk-auth-secret", RAMUserAccessKeyID, RAMUserAccessKeySecret)

			// Create SecretStore in testNamespace that references authentication Secret in testNamespace2
			// This should become Ready because cross namespace reference is enabled
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

			// Wait for SecretStore to be ready despite the cross namespace AccessKey refs
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Create ExternalSecret using the SecretStore to prove the cross namespace
			// AK credential actually works end-to-end
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cross-secret-externalsecret",
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
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)
		})
	})
})

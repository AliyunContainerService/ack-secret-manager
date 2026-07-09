// match_conditions_test.go - Match Conditions E2E tests
package e2e

import (
	"context"
	"fmt"
	"regexp"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Match Conditions E2E", func() {
	var (
		testNamespace       *corev1.Namespace
		testNamespace2      *corev1.Namespace
		ctx                 = context.Background()
		matchesRegexPattern = regexp.MustCompile("(test-match-conditions|test-match-conditions2).*")
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-match-conditions-"+getRandString())
		testNamespace2 = createTestNamespace(ctx, "test-match-conditions2-"+getRandString())
	})

	AfterEach(func() {
		// Do not call deleteTestNamespace here to avoid race conditions
		// All cleanup is handled in DeferCleanup to ensure proper ordering
	})

	Context("ClusterSecretStore namespace matching", func() {
		It("Should match namespaces based on conditions", func() {
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-namespace-match-store-" + getRandString(),
				},
				Spec: api.ClusterSecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							RAMRoleARN:      RAMRoleArnForRRSA,
							OIDCProviderARN: OIDCProviderARN,
						},
					},
					Conditions: []api.ClusterSecretStoreCondition{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"environment": "test",
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, clusterSecretStore)).To(Succeed())
			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				// Handle complete cleanup to avoid race conditions with AfterEach

				// 1. Delete the ClusterSecretStore
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())

				// 2. Now safely delete the namespace
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
				Expect(k8sClient.Delete(ctx, testNamespace2)).To(Succeed())
			})

			// Validate ClusterSecretStore status update
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

			// Create ExternalSecret using the store in a namespace without matching labels
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-namespace-match-externalsecret",
					Namespace: testNamespace.Name, // This namespace does not have the "environment: test" label yet
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
								Name: clusterSecretStore.Name,
								Kind: ResourceClusterSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Initially, ExternalSecret should fail to sync because the namespace doesn't match conditions
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
				// Check if there are sync failures due to namespace not matching conditions
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
						return fmt.Sprintf("ExternalSecret should consistently fail to sync because namespace doesn't match conditions, but: %s", lastCheckError)
					}
					return "ExternalSecret should consistently fail to sync because namespace doesn't match conditions"
				})

			// Now update test namespace to match conditions
			testNamespace.Labels = map[string]string{"environment": "test"}
			Expect(k8sClient.Update(ctx, testNamespace)).To(Succeed())

			// Wait for the ExternalSecret to sync successfully after namespace label update
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("ClusterExternalSecret namespace matching", func() {
		It("Should match namespaces based on regex", func() {
			// Create ClusterExternalSecret using the store
			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-regex-match-clusterexternalsecret" + getRandString(),
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
							NamespaceRegexes: []string{
								"test-match-conditions.*",
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())
			// Register cleanup for cluster-scoped resources
			DeferCleanup(func() {
				// Handle complete cleanup to avoid race conditions with AfterEach

				// 1. Wait for any ongoing controller operations to complete
				time.Sleep(time.Second * 3)

				// 2. Delete the ClusterExternalSecret to trigger controller cleanup
				Expect(k8sClient.Delete(ctx, clusterExternalSecret)).To(Succeed())

				// 3. Wait for all ExternalSecrets to be cleaned up in both namespaces
				Eventually(func() bool {
					externalSecretList := &api.ExternalSecretList{}
					err := k8sClient.List(ctx, externalSecretList, client.InNamespace(testNamespace.Name))
					if err != nil {
						return false
					}
					if len(externalSecretList.Items) > 0 {
						return false
					}

					externalSecretList2 := &api.ExternalSecretList{}
					err = k8sClient.List(ctx, externalSecretList2, client.InNamespace(testNamespace2.Name))
					if err != nil {
						return false
					}
					return len(externalSecretList2.Items) == 0
				}, time.Second*30, time.Second*2).Should(BeTrue(), "All ExternalSecrets should be cleaned up before namespace deletion")

				// 4. Now safely delete both namespaces
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
				Expect(k8sClient.Delete(ctx, testNamespace2)).To(Succeed())
			})

			// Look for ExternalSecrets that would have been created by the ClusterExternalSecret
			// These should be named after the ClusterExternalSecret and exist in matching namespaces
			externalSecretList := &api.ExternalSecretList{}
			Expect(k8sClient.List(ctx, externalSecretList)).To(Succeed())

			// Check that ExternalSecrets were created in namespaces that match the regex pattern
			for _, es := range externalSecretList.Items {
				// Check if this ExternalSecret was created by our ClusterExternalSecret
				// The ExternalSecret name should match the ClusterExternalSecret name
				if es.Name == clusterExternalSecret.Name {
					// Verify that the namespace matches the regex pattern
					// This regex matches both "test-match-conditions-" and "test-match-conditions2-"
					matchesRegex := matchesRegexPattern.MatchString(es.Namespace)

					Expect(matchesRegex).To(BeTrue(), "ExternalSecret %s found in namespace %s which doesn't match regex", es.Name, es.Namespace)
				}
			}
		})
	})
})

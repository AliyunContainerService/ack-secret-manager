// match_conditions_test.go - Match Conditions E2E tests
package e2e

import (
	"context"
	"fmt"
	"regexp"
	"strings"
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
		// Register namespace cleanup immediately after creation so the namespace
		// is deleted even when a later assertion fails before the spec's own
		// DeferCleanup is registered. deleteTestNamespace tolerates an
		// already-deleted namespace, so running it twice is harmless.
		DeferCleanup(deleteTestNamespace, ctx, testNamespace)
		testNamespace2 = createTestNamespace(ctx, "test-match-conditions2-"+getRandString())
		DeferCleanup(deleteTestNamespace, ctx, testNamespace2)
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
			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

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

			// Initially, ExternalSecret should fail to sync because the namespace doesn't match conditions.
			// Consistently alone would fail immediately because the controller needs time to process,
			// so first wait for the initial Failed result, then verify it persists.
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
				// Check if there are sync failures due to namespace not matching conditions.
				// The ExternalSecret controller rejects the ClusterSecretStore reference
				// with "namespace ... is not allowed to access ClusterSecretStore ..."
				// (see getSecretStore / validateSecretStoreAccess) when the namespace
				// labels don't satisfy the store conditions, and the error surfaces in
				// DataSyncResults.Reason. Asserting on that message locks the failure
				// reason to namespace mismatch instead of any unrelated sync error.
				for i, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" {
						lastCheckError = fmt.Sprintf(
							"DataSyncResult[%d] should have status 'Failed', got '%s'. Reason: '%s'", i, result.Status, result.Reason)
						return false
					}
					if !strings.Contains(result.Reason, "is not allowed to access ClusterSecretStore") {
						lastCheckError = fmt.Sprintf(
							"DataSyncResult[%d] should fail because namespace doesn't match ClusterSecretStore conditions, got Reason: '%s'", i, result.Reason)
						return false
					}
				}
				lastCheckError = ""
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastCheckError != "" {
						return fmt.Sprintf("ExternalSecret should fail to sync because namespace doesn't match conditions, but: %s", lastCheckError)
					}
					return "ExternalSecret should fail to sync because namespace doesn't match conditions"
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
					if !strings.Contains(result.Reason, "is not allowed to access ClusterSecretStore") {
						return false
					}
				}
				return true
			}).WithTimeout(time.Second*10).WithPolling(time.Second*2).Should(BeTrue(),
				"ExternalSecret should consistently remain in Failed state while namespace doesn't match conditions")

			// Now update test namespace to match conditions. Re-Get the latest
			// object before mutating so concurrent label changes are not
			// clobbered, and retry via Eventually on resourceVersion conflicts.
			Eventually(func() bool {
				ns := &corev1.Namespace{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: testNamespace.Name}, ns); err != nil {
					return false
				}
				if ns.Labels == nil {
					ns.Labels = map[string]string{}
				}
				ns.Labels["environment"] = "test"
				if err := k8sClient.Update(ctx, ns); err != nil {
					// ResourceVersion conflict (or transient error): retry with a fresh object.
					return false
				}
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*2).Should(BeTrue(),
				"namespace labels should eventually be updated to match ClusterSecretStore conditions")

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

				// 1. Delete the ClusterExternalSecret to trigger controller cleanup
				Expect(k8sClient.Delete(ctx, clusterExternalSecret)).To(Succeed())

				// 2. Wait for all ExternalSecrets to be cleaned up in both namespaces
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

				// 3. Now safely delete both namespaces
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
				Expect(k8sClient.Delete(ctx, testNamespace2)).To(Succeed())
			})

			// Wait for the ClusterExternalSecret controller to provision child
			// ExternalSecrets (named after the ClusterExternalSecret) in BOTH
			// matching namespaces. Without this wait the subsequent assertions
			// could run before the controller creates anything and pass vacuously.
			Eventually(func() bool {
				es1 := &api.ExternalSecret{}
				err1 := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: testNamespace.Name,
				}, es1)
				es2 := &api.ExternalSecret{}
				err2 := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: testNamespace2.Name,
				}, es2)
				return err1 == nil && err2 == nil
			}).WithTimeout(time.Second*60).WithPolling(time.Second*2).Should(BeTrue(),
				"ClusterExternalSecret should provision child ExternalSecrets in both matching namespaces %s and %s",
				testNamespace.Name, testNamespace2.Name)

			// Explicitly assert the two child ExternalSecrets exist in the
			// matching namespaces.
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterExternalSecret.Name, Namespace: testNamespace.Name,
			}, &api.ExternalSecret{})).To(Succeed(),
				"child ExternalSecret should exist in matching namespace %s", testNamespace.Name)
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterExternalSecret.Name, Namespace: testNamespace2.Name,
			}, &api.ExternalSecret{})).To(Succeed(),
				"child ExternalSecret should exist in matching namespace %s", testNamespace2.Name)

			// Exclusivity: no child ExternalSecret of this ClusterExternalSecret
			// may exist in any non-matching namespace (cluster-wide check).
			externalSecretList := &api.ExternalSecretList{}
			Expect(k8sClient.List(ctx, externalSecretList)).To(Succeed())

			for _, es := range externalSecretList.Items {
				if es.Name == clusterExternalSecret.Name {
					// The ExternalSecret name matches; its namespace MUST match
					// the regex pattern. This regex matches both
					// "test-match-conditions-" and "test-match-conditions2-".
					matchesRegex := matchesRegexPattern.MatchString(es.Namespace)
					Expect(matchesRegex).To(BeTrue(), "ExternalSecret %s found in namespace %s which doesn't match regex", es.Name, es.Namespace)
				}
			}
		})
	})
})

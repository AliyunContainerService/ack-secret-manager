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
		probeNamespace      *corev1.Namespace
		ctx                 = context.Background()
		matchesRegexPattern = regexp.MustCompile("(test-match-conditions|test-match-conditions2).*")
	)

	// labelNamespace adds the environment=test label to a namespace, retrying
	// on resourceVersion conflicts with a fresh object each attempt.
	labelNamespace := func(nsName string) {
		Eventually(func() bool {
			ns := &corev1.Namespace{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: nsName}, ns); err != nil {
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
			"namespace %s labels should eventually be updated", nsName)
	}

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-match-conditions-"+getRandString())
		// Register namespace cleanup immediately after creation so the namespace
		// is deleted even when a later assertion fails before the spec's own
		// DeferCleanup is registered. deleteTestNamespace tolerates an
		// already-deleted namespace, so running it twice is harmless.
		DeferCleanup(deleteTestNamespace, ctx, testNamespace)
		testNamespace2 = createTestNamespace(ctx, "test-match-conditions2-"+getRandString())
		DeferCleanup(deleteTestNamespace, ctx, testNamespace2)
		// Probe namespace used for NEGATIVE matching: it carries none of the
		// condition-matching properties (no environment=test label, and a name
		// that shares NO prefix with the CES regex conditions), so it must be
		// excluded by both stores below.
		probeNamespace = createTestNamespace(ctx, "x-match-conditions-probe-"+getRandString())
		DeferCleanup(deleteTestNamespace, ctx, probeNamespace)
	})

	Context("ClusterSecretStore namespace matching", func() {
		It("Should match namespaces based on conditions", func() {
			// Bidirectional design: the store condition is the environment=test
			// label selector. The positive side gains the label explicitly and
			// must become allowed; the negative probe namespace never gains it
			// and must stay denied.
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

				// 2. Now safely delete the namespaces
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
				Expect(k8sClient.Delete(ctx, testNamespace2)).To(Succeed())
				Expect(k8sClient.Delete(ctx, probeNamespace)).To(Succeed())
			})

			// Validate ClusterSecretStore status update
			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			// NEGATIVE side: ExternalSecret in the probe namespace, which does
			// not carry the environment=test label and never will in this spec.
			probeExternalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-namespace-match-probe-externalsecret",
					Namespace: probeNamespace.Name,
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

			Expect(k8sClient.Create(ctx, probeExternalSecret)).To(Succeed())

			// Initially, ExternalSecret should fail to sync because the namespace doesn't match conditions.
			// Consistently alone would fail immediately because the controller needs time to process,
			// so first wait for the initial Failed result, then verify it persists.
			var lastCheckError string
			Eventually(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      probeExternalSecret.Name,
					Namespace: probeExternalSecret.Namespace,
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

			// POSITIVE side: ExternalSecret in the test namespace, which also
			// lacks the label initially.
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

			// The positive-side namespace is denied as well until the label is
			// added: wait for the initial access-denied sync result before
			// flipping the label, so the success afterwards is attributable to
			// the label change.
			Eventually(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, createdExternalSecret); err != nil {
					return false
				}
				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					return false
				}
				for _, result := range createdExternalSecret.Status.DataSyncResults {
					if result.Status != "Failed" || !strings.Contains(result.Reason, "is not allowed to access ClusterSecretStore") {
						return false
					}
				}
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				"ExternalSecret should initially fail because its namespace does not match conditions yet")

			// Now update the POSITIVE namespace to match conditions. The probe
			// namespace intentionally never gains the label.
			labelNamespace(testNamespace.Name)

			// Wait for the ExternalSecret to sync successfully after namespace label update
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// The probe namespace must REMAIN denied even after the positive
			// side succeeded: the access decision is per-namespace, not global.
			Consistently(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      probeExternalSecret.Name,
					Namespace: probeExternalSecret.Namespace,
				}, createdExternalSecret)
				if err != nil {
					// Transient API error makes the outcome undeterminable; do
					// not fail the check on it and keep probing.
					return true
				}

				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					return true
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
				"ExternalSecret in the non-matching probe namespace should consistently remain denied by ClusterSecretStore conditions")

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
			CleanupExternalSecret(ctx, probeExternalSecret)
		})

		It("Should match namespace by substring semantics in NamespaceRegexes", func() {
			// Substring-semantics discriminator: the regex "regex-sub" carries
			// NO anchors and no ".*", so it can only match a namespace whose
			// name merely CONTAINS it (e.g. "test-regex-sub-<rand>"). Under
			// the legacy fully-anchored ^(?:regex)$ semantics it would match
			// nothing, so this spec distinguishes the two behaviors.
			matchNamespace := createTestNamespace(ctx, "test-regex-sub-"+getRandString())
			DeferCleanup(deleteTestNamespace, ctx, matchNamespace)
			// Probe namespace for the NEGATIVE side: its name intentionally
			// does NOT contain the substring "regex-sub" (note the shortened
			// "rx-sub" token), so the regex cannot match it under either
			// substring or anchored semantics.
			probeNS := createTestNamespace(ctx, "x-rx-sub-probe-"+getRandString())
			DeferCleanup(deleteTestNamespace, ctx, probeNS)

			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-regex-sub-store-" + getRandString(),
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
							NamespaceRegexes: []string{"regex-sub"},
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

				// 2. Now safely delete the namespaces
				Expect(k8sClient.Delete(ctx, matchNamespace)).To(Succeed())
				Expect(k8sClient.Delete(ctx, probeNS)).To(Succeed())
			})

			// Validate ClusterSecretStore status update
			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			// NEGATIVE side: ExternalSecret in the probe namespace, whose name
			// does not contain "regex-sub" and never will in this spec.
			probeExternalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-regex-sub-probe-externalsecret",
					Namespace: probeNS.Name,
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

			Expect(k8sClient.Create(ctx, probeExternalSecret)).To(Succeed())

			// Wait for the initial Failed result caused by the namespace not
			// matching the regex condition.
			var lastCheckError string
			Eventually(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      probeExternalSecret.Name,
					Namespace: probeExternalSecret.Namespace,
				}, createdExternalSecret)
				if err != nil {
					lastCheckError = fmt.Sprintf("Failed to get ExternalSecret: %v", err)
					return false
				}

				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					lastCheckError = "DataSyncResults is empty, waiting for sync results..."
					return false
				}
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
						return fmt.Sprintf("ExternalSecret should fail to sync because namespace doesn't match the substring regex, but: %s", lastCheckError)
					}
					return "ExternalSecret should fail to sync because namespace doesn't match the substring regex"
				})

			// POSITIVE side: ExternalSecret in the namespace whose name
			// contains "regex-sub" only as a substring.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-regex-sub-externalsecret",
					Namespace: matchNamespace.Name,
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

			// Under substring semantics the regex "regex-sub" matches this
			// namespace, so the sync must succeed and the Secret be created.
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// The probe namespace must REMAIN denied even after the positive
			// side succeeded: the access decision is per-namespace, not global.
			Consistently(func() bool {
				createdExternalSecret := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      probeExternalSecret.Name,
					Namespace: probeExternalSecret.Namespace,
				}, createdExternalSecret)
				if err != nil {
					// Transient API error makes the outcome undeterminable; do
					// not fail the check on it and keep probing.
					return true
				}

				if len(createdExternalSecret.Status.DataSyncResults) == 0 {
					return true
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
				"ExternalSecret in the non-matching probe namespace should consistently remain denied by the substring regex condition")

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
			CleanupExternalSecret(ctx, probeExternalSecret)
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

				// 2. Wait for all ExternalSecrets to be cleaned up in all namespaces
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
					if len(externalSecretList2.Items) > 0 {
						return false
					}

					externalSecretList3 := &api.ExternalSecretList{}
					err = k8sClient.List(ctx, externalSecretList3, client.InNamespace(probeNamespace.Name))
					if err != nil {
						return false
					}
					return len(externalSecretList3.Items) == 0
				}, time.Second*30, time.Second*2).Should(BeTrue(), "All ExternalSecrets should be cleaned up before namespace deletion")

				// 3. Now safely delete all namespaces
				Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
				Expect(k8sClient.Delete(ctx, testNamespace2)).To(Succeed())
				Expect(k8sClient.Delete(ctx, probeNamespace)).To(Succeed())
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

			// Non-matching discrimination: the probe namespace name does not
			// contain the substring "test-match-conditions", so the regex
			// test-match-conditions.* does not match it under any matching
			// strategy. The child ExternalSecret (named after the
			// ClusterExternalSecret) must never be provisioned there.
			Consistently(func() bool {
				probeES := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: probeNamespace.Name,
				}, probeES)
				if err == nil {
					// Child ES exists in the probe namespace: the regex
					// matched a namespace it should not have.
					return false
				}
				// A transient API error makes the outcome undeterminable; do
				// not fail the check on it and keep probing.
				return true
			}).WithTimeout(time.Second*10).WithPolling(time.Second*2).Should(BeTrue(),
				"ClusterExternalSecret must not provision a child ExternalSecret in the non-matching probe namespace %s",
				probeNamespace.Name)
		})

		It("Should provision child ExternalSecrets in substring-matching namespaces", func() {
			// Substring-semantics discriminator identical to the ClusterSecretStore
			// spec: "regex-sub" carries NO anchors and no ".*", so it can only
			// match namespaces whose names merely CONTAIN it. Under the legacy
			// fully-anchored ^(?:regex)$ semantics nothing would be provisioned.
			substringPattern := regexp.MustCompile("regex-sub")

			// Dedicated substring-matching namespaces, created inside this spec
			// with fresh random suffixes so they never collide with the
			// ClusterSecretStore substring spec's namespaces.
			matchNamespace := createTestNamespace(ctx, "test-regex-sub-"+getRandString())
			DeferCleanup(deleteTestNamespace, ctx, matchNamespace)
			matchNamespace2 := createTestNamespace(ctx, "test-regex-sub2-"+getRandString())
			DeferCleanup(deleteTestNamespace, ctx, matchNamespace2)
			// Probe namespace for the NEGATIVE side: its name intentionally does
			// NOT contain the substring "regex-sub" (note the shortened "rx-sub"
			// token), and shares no prefix with any other spec's regex.
			probeNS := createTestNamespace(ctx, "x-rx-sub-probe-"+getRandString())
			DeferCleanup(deleteTestNamespace, ctx, probeNS)

			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-regex-sub-clusterexternalsecret-" + getRandString(),
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
								"regex-sub",
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

				// 2. Wait for all ExternalSecrets to be cleaned up in all namespaces
				Eventually(func() bool {
					for _, ns := range []string{matchNamespace.Name, matchNamespace2.Name, probeNS.Name} {
						externalSecretList := &api.ExternalSecretList{}
						if err := k8sClient.List(ctx, externalSecretList, client.InNamespace(ns)); err != nil {
							return false
						}
						if len(externalSecretList.Items) > 0 {
							return false
						}
					}
					return true
				}, time.Second*30, time.Second*2).Should(BeTrue(), "All ExternalSecrets should be cleaned up before namespace deletion")

				// 3. Now safely delete all namespaces
				Expect(k8sClient.Delete(ctx, matchNamespace)).To(Succeed())
				Expect(k8sClient.Delete(ctx, matchNamespace2)).To(Succeed())
				Expect(k8sClient.Delete(ctx, probeNS)).To(Succeed())
			})

			// Wait for the ClusterExternalSecret controller to provision child
			// ExternalSecrets (named after the ClusterExternalSecret) in BOTH
			// substring-matching namespaces. Without this wait the subsequent
			// assertions could run before the controller creates anything and
			// pass vacuously.
			Eventually(func() bool {
				es1 := &api.ExternalSecret{}
				err1 := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: matchNamespace.Name,
				}, es1)
				es2 := &api.ExternalSecret{}
				err2 := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: matchNamespace2.Name,
				}, es2)
				return err1 == nil && err2 == nil
			}).WithTimeout(time.Second*60).WithPolling(time.Second*2).Should(BeTrue(),
				"ClusterExternalSecret should provision child ExternalSecrets in both substring-matching namespaces %s and %s",
				matchNamespace.Name, matchNamespace2.Name)

			// Explicitly assert the two child ExternalSecrets exist in the
			// matching namespaces.
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterExternalSecret.Name, Namespace: matchNamespace.Name,
			}, &api.ExternalSecret{})).To(Succeed(),
				"child ExternalSecret should exist in substring-matching namespace %s", matchNamespace.Name)
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterExternalSecret.Name, Namespace: matchNamespace2.Name,
			}, &api.ExternalSecret{})).To(Succeed(),
				"child ExternalSecret should exist in substring-matching namespace %s", matchNamespace2.Name)

			// Exclusivity: no child ExternalSecret of this ClusterExternalSecret
			// may exist in any non-matching namespace (cluster-wide check).
			externalSecretList := &api.ExternalSecretList{}
			Expect(k8sClient.List(ctx, externalSecretList)).To(Succeed())

			for _, es := range externalSecretList.Items {
				if es.Name == clusterExternalSecret.Name {
					// The ExternalSecret name matches; its namespace MUST contain
					// the "regex-sub" substring, i.e. match the condition regex
					// under substring semantics.
					Expect(substringPattern.MatchString(es.Namespace)).To(BeTrue(),
						"ExternalSecret %s found in namespace %s which doesn't match the substring regex", es.Name, es.Namespace)
				}
			}

			// Non-matching discrimination: the probe namespace name does not
			// contain the substring "regex-sub", so the regex does not match it
			// under any matching strategy. The child ExternalSecret (named after
			// the ClusterExternalSecret) must never be provisioned there.
			Consistently(func() bool {
				probeES := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: probeNS.Name,
				}, probeES)
				if err == nil {
					// Child ES exists in the probe namespace: the regex
					// matched a namespace it should not have.
					return false
				}
				// A transient API error makes the outcome undeterminable; do
				// not fail the check on it and keep probing.
				return true
			}).WithTimeout(time.Second*10).WithPolling(time.Second*2).Should(BeTrue(),
				"ClusterExternalSecret must not provision a child ExternalSecret in the non-matching probe namespace %s",
				probeNS.Name)
		})
	})
})

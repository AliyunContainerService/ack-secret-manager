// cluster_external_secret_test.go - ClusterExternalSecret E2E tests
//
// Covers provisioning lifecycle, namespace watch, cleanup contract,
// fail-closed namespace matching, deprecated NamespaceSelectors
// compatibility, and auto-disable under namespace scope.
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

var _ = Describe("ClusterExternalSecret E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-ces-"+getRandString())
	})

	AfterEach(func() {
		if testNamespace == nil {
			return
		}
		check := &corev1.Namespace{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: testNamespace.Name}, check); err != nil {
			if k8serrors.IsNotFound(err) {
				return
			}
			Expect(err).NotTo(HaveOccurred())
		}
		deleteTestNamespace(ctx, testNamespace)
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
			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			// Create ClusterExternalSecret with namespace selector
			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-cluster-externalsecret" + getRandString(),
				},
				Spec: api.ClusterExternalSecretSpec{
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Conditions: []api.ClusterExternalSecretCondition{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"environment": "test",
								},
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

				// 2. Wait for all ExternalSecrets to be cleaned up in both namespaces.
				// If the namespace is already gone (AfterEach runs before this
				// DeferCleanup in Ginkgo v2), the ExternalSecrets are gone too —
				// treat NotFound as success.
				Eventually(func() bool {
					externalSecretList := &api.ExternalSecretList{}
					err := k8sClient.List(ctx, externalSecretList, client.InNamespace(newTestNamespace.Name))
					if err != nil {
						return k8serrors.IsNotFound(err)
					}

					return len(externalSecretList.Items) == 0
				}, time.Second*30, time.Second*2).Should(BeTrue(), "All ExternalSecrets should be cleaned up before namespace deletion")

				// 3. Now safely delete both namespaces. The AfterEach safety net
				// (deleteTestNamespace) runs BEFORE this It-level DeferCleanup in
				// Ginkgo v2, so testNamespace may already be gone — tolerate
				// NotFound for both namespace deletions.
				for _, ns := range []*corev1.Namespace{testNamespace, newTestNamespace} {
					if err := k8sClient.Delete(ctx, ns); err != nil && !k8serrors.IsNotFound(err) {
						Expect(err).NotTo(HaveOccurred())
					}
				}

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

	// Deprecated-field compatibility: the main CES specs above use the
	// Conditions field, but shipped manifests still carry the deprecated
	// NamespaceSelectors field and must keep provisioning child
	// ExternalSecrets exactly as before.
	Context("ClusterExternalSecret deprecated NamespaceSelectors compatibility", func() {
		It("Should still provision child ExternalSecrets via the deprecated NamespaceSelectors field", func() {
			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-deprecated-ces-" + getRandString(),
				},
				Spec: api.ClusterExternalSecretSpec{
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					// Deprecated field, intentionally used to pin backward compatibility.
					NamespaceSelectors: []*metav1.LabelSelector{
						{
							MatchLabels: map[string]string{
								"environment": "deprecated-compat",
							},
						},
					},
					ExternalSecretSpec: api.ExternalSecretSpec{
						Provider: "kms",
						Data: []api.DataSource{
							{
								Key:       CommonKMSSecretName,
								Name:      "deprecated-compat-key",
								VersionId: "v1",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())

			// Namespace carrying the label the deprecated selector matches.
			matchedNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-deprecated-ns-" + getRandString(),
					Labels: map[string]string{
						"environment": "deprecated-compat",
					},
				},
			}
			Expect(k8sClient.Create(ctx, matchedNamespace)).To(Succeed())

			DeferCleanup(func() {
				// 1. Delete the CES so the controller reclaims the child ExternalSecret
				if err := k8sClient.Delete(ctx, clusterExternalSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterExternalSecret %s: %v\n", clusterExternalSecret.Name, err)
				}
				// 2. Wait for the child ExternalSecret to be reclaimed before the namespace goes
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name: clusterExternalSecret.Name, Namespace: matchedNamespace.Name,
					}, &api.ExternalSecret{})
					return k8serrors.IsNotFound(err)
				}, time.Second*30, time.Second*2).Should(BeTrue(), "child ExternalSecret should be cleaned up before namespace deletion")
				// 3. Delete the matched namespace
				Expect(k8sClient.Delete(ctx, matchedNamespace)).To(Succeed())
			})

			// A child ExternalSecret must appear in the matching namespace,
			// proving the deprecated field is still honored.
			var lastCompatError string
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: matchedNamespace.Name,
				}, &api.ExternalSecret{})
				if err != nil {
					lastCompatError = fmt.Sprintf("child ExternalSecret was not created in namespace %s: %v", matchedNamespace.Name, err)
					return false
				}
				lastCompatError = ""
				return true
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				func() string {
					if lastCompatError != "" {
						return fmt.Sprintf("deprecated NamespaceSelectors should still provision child ExternalSecrets, but: %s", lastCompatError)
					}
					return "deprecated NamespaceSelectors should still provision child ExternalSecrets"
				})
		})
	})

	// Covers the CES namespace watch (mapNamespaceToCES +
	// NamespaceWatchPredicate in clusterexternalsecret_controller.go):
	// namespaces that start matching AFTER the CES exists must be provisioned
	// immediately instead of waiting for the rotation poll.
	//
	// Design principle: the CES rotationInterval is pinned to 30m so a child
	// ExternalSecret appearing inside the short assertion window can only
	// come from the namespace watch.
	Context("ClusterExternalSecret watches namespaces for immediate provisioning", func() {
		It("Should provision a child ExternalSecret immediately when a namespace gains the matching label", func() {
			labelKey := "e2e-ces-ns-watch"
			labelValue := getRandString()

			By("creating a ClusterSecretStore for the ClusterExternalSecret")
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ces-watch-store-" + getRandString(),
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
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, clusterSecretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", clusterSecretStore.Name, err)
				}
			})
			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			By("creating a namespace WITHOUT the matching label")
			watchNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ces-watch-ns-" + getRandString(),
				},
			}
			Expect(k8sClient.Create(ctx, watchNamespace)).To(Succeed())
			DeferCleanup(func() {
				// Registered first, runs LAST (LIFO): the namespace is deleted
				// only after the CES cleanup below reclaimed the child ES.
				_ = k8sClient.Delete(ctx, watchNamespace)
			})

			By("creating a ClusterExternalSecret with a long rotationInterval")
			clusterExternalSecret := newLongRotationCES("test-ces-watch-label-"+getRandString(), labelKey, labelValue, clusterSecretStore.Name)
			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, clusterExternalSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterExternalSecret %s: %v\n", clusterExternalSecret.Name, err)
					return
				}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name: clusterExternalSecret.Name, Namespace: watchNamespace.Name,
					}, &api.ExternalSecret{})
					return k8serrors.IsNotFound(err)
				}, time.Second*60, time.Second*2).Should(BeTrue(),
					"child ExternalSecret should be reclaimed before namespace deletion")
			})

			By("waiting for the CES to finish its first reconcile without provisioning")
			waitForCESNonProvisioning(ctx, clusterExternalSecret.Name)

			By("consistently observing no child ExternalSecret in the unmatched namespace")
			Consistently(func() bool {
				// Transient API errors are not evidence of a child appearing;
				// only a confirmed existing child fails the assertion.
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: clusterExternalSecret.Name, Namespace: watchNamespace.Name,
				}, &api.ExternalSecret{})
				return err != nil
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				"no child ExternalSecret may appear while the namespace does not match")

			By("labeling the namespace so it matches the CES selector")
			Eventually(func() error {
				ns := &corev1.Namespace{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: watchNamespace.Name}, ns); err != nil {
					return err
				}
				if ns.Labels == nil {
					ns.Labels = make(map[string]string)
				}
				ns.Labels[labelKey] = labelValue
				return k8sClient.Update(ctx, ns)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"should add the matching label to namespace %s", watchNamespace.Name)

			By("waiting for the child ExternalSecret to appear within the short watch window")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: clusterExternalSecret.Name, Namespace: watchNamespace.Name,
				}, &api.ExternalSecret{})
				return err == nil
			}).WithTimeout(time.Second*60).WithPolling(time.Second*2).Should(BeTrue(),
				"the namespace watch should provision the child ExternalSecret immediately after relabeling")
			validateExternalSecretSucceededAndSecretCreated(ctx, watchNamespace.Name, clusterExternalSecret.Name, time.Second*60)

			By("verifying CES status reports the namespace as provisioned with no failures")
			Eventually(func() bool {
				ces := &api.ClusterExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterExternalSecret.Name}, ces); err != nil {
					return false
				}
				if len(ces.Status.FailedNamespaces) != 0 {
					return false
				}
				for _, ns := range ces.Status.ProvisionedNamespaces {
					if ns == watchNamespace.Name {
						return true
					}
				}
				return false
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"CES status should list %s in provisionedNamespaces with empty failedNamespaces", watchNamespace.Name)
		})

		It("Should provision a child ExternalSecret immediately when a matching namespace is created", func() {
			labelKey := "e2e-ces-ns-watch"
			labelValue := getRandString()

			By("creating a ClusterSecretStore for the ClusterExternalSecret")
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ces-watch-store2-" + getRandString(),
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
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, clusterSecretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", clusterSecretStore.Name, err)
				}
			})
			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			By("creating a ClusterExternalSecret with a long rotationInterval")
			clusterExternalSecret := newLongRotationCES("test-ces-watch-create-"+getRandString(), labelKey, labelValue, clusterSecretStore.Name)
			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())

			// The watch namespace is created inside the spec; its cleanup is
			// registered before the CES cleanup so the CES reclaims the child
			// ExternalSecret first (LIFO).
			watchNamespaceName := "test-ces-watch-ns2-" + getRandString()
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: watchNamespaceName}})
			})
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, clusterExternalSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterExternalSecret %s: %v\n", clusterExternalSecret.Name, err)
					return
				}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name: clusterExternalSecret.Name, Namespace: watchNamespaceName,
					}, &api.ExternalSecret{})
					return k8serrors.IsNotFound(err)
				}, time.Second*60, time.Second*2).Should(BeTrue(),
					"child ExternalSecret should be reclaimed before namespace deletion")
			})

			By("waiting for the CES to finish its first reconcile without provisioning")
			waitForCESNonProvisioning(ctx, clusterExternalSecret.Name)

			By("creating a namespace that already matches the CES selector")
			watchNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   watchNamespaceName,
					Labels: map[string]string{labelKey: labelValue},
				},
			}
			Expect(k8sClient.Create(ctx, watchNamespace)).To(Succeed())

			By("waiting for the child ExternalSecret to appear within the short watch window")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: clusterExternalSecret.Name, Namespace: watchNamespaceName,
				}, &api.ExternalSecret{})
				return err == nil
			}).WithTimeout(time.Second*60).WithPolling(time.Second*2).Should(BeTrue(),
				"the namespace watch should provision the child ExternalSecret immediately after namespace creation")
			validateExternalSecretSucceededAndSecretCreated(ctx, watchNamespaceName, clusterExternalSecret.Name, time.Second*60)

			By("verifying CES status reports the namespace as provisioned with no failures")
			Eventually(func() bool {
				ces := &api.ClusterExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterExternalSecret.Name}, ces); err != nil {
					return false
				}
				if len(ces.Status.FailedNamespaces) != 0 {
					return false
				}
				for _, ns := range ces.Status.ProvisionedNamespaces {
					if ns == watchNamespaceName {
						return true
					}
				}
				return false
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"CES status should list %s in provisionedNamespaces with empty failedNamespaces", watchNamespaceName)
		})

		// Covers the namespace DELETION event path of the CES namespace watch:
		// when a provisioned namespace is deleted, the child ExternalSecret
		// must disappear and status.provisionedNamespaces must drop the entry
		// within the short watch window (30m rotation rules out the poll).
		It("Should clean up the child ExternalSecret and the provisionedNamespaces ledger when a provisioned namespace is deleted", func() {
			labelKey := "e2e-ces-ns-delete"
			labelValue := getRandString()

			By("creating a ClusterSecretStore for the ClusterExternalSecret")
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ces-delete-store-" + getRandString(),
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
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, clusterSecretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", clusterSecretStore.Name, err)
				}
			})
			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			By("creating two namespaces that match the CES selector")
			nsToDelete := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-ces-del-ns1-" + getRandString(),
					Labels: map[string]string{labelKey: labelValue},
				},
			}
			Expect(k8sClient.Create(ctx, nsToDelete)).To(Succeed())
			nsToKeep := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-ces-del-ns2-" + getRandString(),
					Labels: map[string]string{labelKey: labelValue},
				},
			}
			Expect(k8sClient.Create(ctx, nsToKeep)).To(Succeed())
			DeferCleanup(func() {
				// Registered first, runs LAST (LIFO): the namespaces are deleted
				// only after the CES cleanup below reclaimed the surviving
				// child ExternalSecret. Idempotent: the spec body deletes
				// nsToDelete itself on the happy path.
				_ = k8sClient.Delete(ctx, nsToDelete)
				_ = k8sClient.Delete(ctx, nsToKeep)
			})

			By("creating a ClusterExternalSecret with a long rotationInterval")
			clusterExternalSecret := newLongRotationCES("test-ces-del-"+getRandString(), labelKey, labelValue, clusterSecretStore.Name)
			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, clusterExternalSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterExternalSecret %s: %v\n", clusterExternalSecret.Name, err)
					return
				}
				Eventually(func() bool {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name: clusterExternalSecret.Name, Namespace: nsToKeep.Name,
					}, &api.ExternalSecret{})
					return k8serrors.IsNotFound(err)
				}, time.Second*60, time.Second*2).Should(BeTrue(),
					"surviving child ExternalSecret should be reclaimed before namespace deletion")
			})

			By("waiting for both namespaces to be provisioned and synced")
			validateExternalSecretSucceededAndSecretCreated(ctx, nsToDelete.Name, clusterExternalSecret.Name, time.Second*60)
			validateExternalSecretSucceededAndSecretCreated(ctx, nsToKeep.Name, clusterExternalSecret.Name, time.Second*60)

			By("deleting one of the provisioned namespaces")
			Expect(k8sClient.Delete(ctx, nsToDelete)).To(Succeed())

			By("observing the provisionedNamespaces ledger drop the deleted namespace within the short watch window")
			Eventually(func() bool {
				ces := &api.ClusterExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterExternalSecret.Name}, ces); err != nil {
					return false
				}
				deletedPresent := false
				keptPresent := false
				for _, ns := range ces.Status.ProvisionedNamespaces {
					if ns == nsToDelete.Name {
						deletedPresent = true
					}
					if ns == nsToKeep.Name {
						keptPresent = true
					}
				}
				return !deletedPresent && keptPresent
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"the namespace deletion event should remove %s from provisionedNamespaces while keeping %s", nsToDelete.Name, nsToKeep.Name)

			By("verifying the child ExternalSecret of the surviving namespace is preserved")
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: clusterExternalSecret.Name, Namespace: nsToKeep.Name,
			}, &api.ExternalSecret{})).To(Succeed(),
				"child ExternalSecret in the surviving namespace %s must not be affected", nsToKeep.Name)
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
			// Orphan cleanup is not triggered by the label removal itself: the
			// CES namespace watch only enqueues CESes that still match the
			// namespace, so cleanup actually happens on the periodic rotation
			// poll (10s here). The 60s window covers several poll cycles while
			// keeping headroom against flakes.
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name: clusterExternalSecret.Name, Namespace: cesNamespace1.Name,
				}, &api.ExternalSecret{})
				return k8serrors.IsNotFound(err)
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
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

			// Create a ClusterExternalSecret whose Conditions namespaceSelector
			// points at a label that no namespace carries, so nothing may match.
			clusterExternalSecret := &api.ClusterExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-failclosed-ces-" + getRandString(),
				},
				Spec: api.ClusterExternalSecretSpec{
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Conditions: []api.ClusterExternalSecretCondition{
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"env": "nonexistent-label-xyz",
								},
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
				"no child ExternalSecret should be created in a namespace that does not match the Conditions namespace selector")

			// Clean up the ClusterExternalSecret explicitly; the namespaces
			// hold no child ExternalSecrets and are cleaned by the Describe-level
			// AfterEach / the DeferCleanup registered above.
			Expect(k8sClient.Delete(ctx, clusterExternalSecret)).To(Succeed())
		})
	})

	// Covers the community-convention auto-disable: once any namespace scope
	// is configured (watch and/or exclude), main.go forces both cluster-level
	// controllers off -- the ClusterExternalSecret controller is never
	// started, so a CES matching a labeled namespace must never provision a
	// child ExternalSecret and its status must stay untouched. The spec uses
	// --exclude-namespaces (NOT --watch-namespaces) on purpose: exclude-only
	// scoping keeps the manager cache cluster-wide, so the test infrastructure
	// (leader-election lease, Deployment namespace) is unaffected.
	Context("ClusterExternalSecret controller auto-disabled under namespace scope", func() {
		It("Should never provision child ExternalSecrets while --exclude-namespaces is active", func() {
			labelKey := "e2e-ces-scoped"
			labelValue := getRandString()

			By("creating an excluded namespace and a labeled namespace matching the CES selector")
			excludedNamespace := createTestNamespace(ctx, "test-ces-scoped-excl-"+getRandString())
			DeferCleanup(func() {
				deleteTestNamespace(ctx, excludedNamespace)
			})
			targetNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-ces-scoped-target-" + getRandString(),
					Labels: map[string]string{labelKey: labelValue},
				},
			}
			Expect(k8sClient.Create(ctx, targetNamespace)).To(Succeed())
			// Both namespace cleanups are registered first, so they run LAST
			// (LIFO): the namespaces are deleted only after the CES/store
			// cleanups and the args restore below; the target namespace goes
			// away before the excluded one.
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, targetNamespace)
			})

			By("creating a ClusterSecretStore while the cluster-scoped controllers still run")
			// The store must be created AND reach Ready BEFORE the Deployment
			// args patch: scoped mode auto-disables the ClusterSecretStore
			// controller, so afterwards no status would ever be written and
			// waitForClusterSecretStoreReady would time out for the full two
			// minutes. Establishing the reference chain under the pre-spec
			// configuration keeps the later CES observation meaningful.
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ces-scoped-store-" + getRandString(),
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
			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			By("patching --exclude-namespaces=<excluded namespace> onto the controller Deployment")
			// Register the restore BEFORE mutating (suite convention): a
			// mid-spec failure still returns the args to their pre-spec
			// state. Runs after the CES/store cleanups (LIFO), so resource
			// cleanup happens first and namespace deletion last.
			originalArgs := getDeploymentArgs(ctx)
			DeferCleanup(func() {
				By("restoring Deployment args after the CES scoped-auto-disable spec")
				restoreDeploymentArgs(ctx, originalArgs)
			})
			// Registered after the args restore so the LIFO run order deletes
			// the store BEFORE the args are restored (resource cleanup first).
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, clusterSecretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterSecretStore %s: %v\n", clusterSecretStore.Name, err)
				}
			})
			patchDeploymentArgs(ctx,
				[]string{"--watch-namespaces", "--exclude-namespaces"},
				[]string{"--exclude-namespaces=" + excludedNamespace.Name})

			By("creating a ClusterExternalSecret whose selector matches the labeled namespace")
			clusterExternalSecret := newLongRotationCES("test-ces-scoped-"+getRandString(), labelKey, labelValue, clusterSecretStore.Name)
			Expect(k8sClient.Create(ctx, clusterExternalSecret)).To(Succeed())
			// Registered last, so the CES is deleted FIRST during cleanup.
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, clusterExternalSecret); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete ClusterExternalSecret %s: %v\n", clusterExternalSecret.Name, err)
				}
			})

			By("consistently observing that no child ExternalSecret is ever provisioned and the CES status stays untouched")
			// The controller is never started under a namespace scope, so the
			// CES is never reconciled: no child ExternalSecret may appear in
			// the matching namespace and the status subresource must stay
			// empty. Transient API errors are not evidence of provisioning.
			Consistently(func() bool {
				childExternalSecret := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      clusterExternalSecret.Name,
					Namespace: targetNamespace.Name,
				}, childExternalSecret); err == nil {
					return false
				}
				ces := &api.ClusterExternalSecret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: clusterExternalSecret.Name}, ces); err != nil {
					return true
				}
				return len(ces.Status.ProvisionedNamespaces) == 0 &&
					len(ces.Status.FailedNamespaces) == 0 &&
					len(ces.Status.Conditions) == 0
			}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
				"with the ClusterExternalSecret controller auto-disabled under a namespace scope, no child ExternalSecret may be provisioned")
		})
	})
})

// newLongRotationCES builds a ClusterExternalSecret with a 30m
// rotationInterval matching namespaces via conditions[labelKey=labelValue];
// the long rotation guarantees provisioning inside short assertion windows
// can only come from the namespace watch, not the periodic poll.
func newLongRotationCES(name, labelKey, labelValue, clusterStoreName string) *api.ClusterExternalSecret {
	return &api.ClusterExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: api.ClusterExternalSecretSpec{
			RotationInterval: &metav1.Duration{Duration: 30 * time.Minute},
			ExternalSecretSpec: api.ExternalSecretSpec{
				Provider: "kms",
				Data: []api.DataSource{
					{
						Key:       CommonKMSSecretName,
						Name:      "cluster-secret-key",
						VersionId: "v1",
						SecretStoreRef: &api.SecretStoreRef{
							Name: clusterStoreName,
							Kind: ResourceClusterSecretStore,
						},
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
}

// waitForCESNonProvisioning waits until the ClusterExternalSecret finished a
// reconcile round that provisioned nothing (Ready=False, empty
// provisionedNamespaces). Establishing this state first guarantees that the
// controller observed the cluster BEFORE any negative/positive provisioning
// assertion runs.
func waitForCESNonProvisioning(ctx context.Context, cesName string) {
	Eventually(func() bool {
		ces := &api.ClusterExternalSecret{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: cesName}, ces); err != nil {
			return false
		}
		if len(ces.Status.ProvisionedNamespaces) != 0 {
			return false
		}
		for _, condition := range ces.Status.Conditions {
			if condition.Type == api.ClusterExternalSecretReady && condition.Status == corev1.ConditionFalse {
				return true
			}
		}
		return false
	}).WithTimeout(time.Second*60).WithPolling(time.Second*5).Should(BeTrue(),
		"ClusterExternalSecret %s should report Ready=False without provisioning while no namespace matches", cesName)
}

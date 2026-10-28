// test/e2e/provider_test.go - Test ExternalSecret providers (OOS)
// Note: the KMS provider sync path is covered by data_fetch_test.go
// ("Should fetch normal data"), so no dedicated KMS spec is kept here.
package e2e

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Provider E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-provider-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	Context("ExternalSecret with OOS provider", func() {
		It("should sync secret data from OOS", func() {
			secretStore := createRRSAOOSSecretStore(ctx, testNamespace.Name, "oos-provider-store")
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, secretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete SecretStore %s/%s: %v\n", secretStore.Namespace, secretStore.Name, err)
				}
			})

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "oos-provider-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "oos",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  CommonOOSSecretParameterName,
							Name: "oos-secret-key",
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

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, shortSyncTimeout)

			syncedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: externalSecret.Namespace,
				Name:      externalSecret.Name,
			}, syncedSecret)).To(Succeed())
			Expect(string(syncedSecret.Data["oos-secret-key"])).To(Equal(CommonOOSSecretParameterValue),
				"synced Secret content should match the preset OOS parameter value")
		})

		It("should fail when the referenced OOS parameter does not exist", func() {
			secretStore := createRRSAOOSSecretStore(ctx, testNamespace.Name, "oos-missing-store")
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, secretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete SecretStore %s/%s: %v\n", secretStore.Namespace, secretStore.Name, err)
				}
			})

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "oos-missing-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "oos",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  TestResourcePrefix + "nonexistent-oos-param-" + getRandString(),
							Name: "oos-secret-key",
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

			expectExternalSecretFailedWith(ctx, externalSecret.Namespace, externalSecret.Name,
				"does not exist", standardSyncTimeout)
		})

		It("should resync secret data when the OOS parameter value changes", func() {
			secretStore := createRRSAOOSSecretStore(ctx, testNamespace.Name, "oos-update-store")
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, secretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete SecretStore %s/%s: %v\n", secretStore.Namespace, secretStore.Name, err)
				}
			})

			oosParamName, err := GlobalResourceManager.CreateOOSSecretParameterForUpdate(ctx, "oos-update-v1")
			Expect(err).NotTo(HaveOccurred(), "failed to create dedicated OOS parameter for update test")
			DeferCleanup(func() {
				if err := GlobalResourceManager.DeleteOOSSecretParameter(oosParamName); err != nil {
					GinkgoWriter.Printf("WARNING: failed to delete dedicated OOS parameter %s: %v\n", oosParamName, err)
				}
			})

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "oos-update-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "oos",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  oosParamName,
							Name: "oos-secret-key",
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

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, standardSyncTimeout)

			By("staging a new value for the OOS parameter")
			Expect(GlobalResourceManager.PutOOSParameterValue(ctx, oosParamName, "oos-update-v2")).To(Succeed())

			Eventually(func() bool {
				secret := &corev1.Secret{}
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: externalSecret.Namespace,
					Name:      externalSecret.Name,
				}, secret); err != nil {
					return false
				}
				return string(secret.Data["oos-secret-key"]) == "oos-update-v2"
			}).WithTimeout(standardSyncTimeout).WithPolling(time.Second*5).Should(BeTrue(),
				"synced Secret should reflect the updated OOS parameter value")
		})

		It("should process OOS data through DataProcess extract and ReplaceKey", func() {
			secretStore := createRRSAOOSSecretStore(ctx, testNamespace.Name, "oos-dataprocess-store")
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, secretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete SecretStore %s/%s: %v\n", secretStore.Namespace, secretStore.Name, err)
				}
			})

			oosParamName, err := GlobalResourceManager.CreateOOSSecretParameterForUpdate(ctx, `{"name":"alice","age":"30"}`)
			Expect(err).NotTo(HaveOccurred(), "failed to create JSON OOS parameter for DataProcess test")
			DeferCleanup(func() {
				if err := GlobalResourceManager.DeleteOOSSecretParameter(oosParamName); err != nil {
					GinkgoWriter.Printf("WARNING: failed to delete dedicated OOS parameter %s: %v\n", oosParamName, err)
				}
			})

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "oos-dataprocess-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "oos",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					DataProcess: []api.DataProcess{
						{
							Extract: &api.DataSource{
								Key:  oosParamName,
								Name: "json-auto-secret-key",
								SecretStoreRef: &api.SecretStoreRef{
									Name:      secretStore.Name,
									Namespace: secretStore.Namespace,
									Kind:      ResourceSecretStore,
								},
							},
							ReplaceKey: []api.ReplaceRule{
								{Source: "^n.*e$", Target: "namekey"},
								{Source: "^a.*e$", Target: "agekey"},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, standardSyncTimeout)

			syncedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: externalSecret.Namespace,
				Name:      externalSecret.Name,
			}, syncedSecret)).To(Succeed())
			Expect(syncedSecret.Data).To(HaveKey("namekey"))
			Expect(syncedSecret.Data).To(HaveKey("agekey"))
			Expect(string(syncedSecret.Data["namekey"])).To(Equal("alice"))
			Expect(string(syncedSecret.Data["agekey"])).To(Equal("30"))
		})

		It("should process OOS data through a Go template", func() {
			secretStore := createRRSAOOSSecretStore(ctx, testNamespace.Name, "oos-template-store")
			DeferCleanup(func() {
				if err := k8sClient.Delete(ctx, secretStore); err != nil && !k8serrors.IsNotFound(err) {
					GinkgoWriter.Printf("WARNING: failed to delete SecretStore %s/%s: %v\n", secretStore.Namespace, secretStore.Name, err)
				}
			})

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "oos-template-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "oos",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Go template field access does not allow '-' in identifiers;
								// use index to read the data-source key with a hyphenated name.
								"rendered": "{{ index . \"oos-secret-key\" | upper }}",
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  CommonOOSSecretParameterName,
							Name: "oos-secret-key",
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

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, standardSyncTimeout)

			syncedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: externalSecret.Namespace,
				Name:      externalSecret.Name,
			}, syncedSecret)).To(Succeed())
			Expect(string(syncedSecret.Data["rendered"])).To(Equal(strings.ToUpper(CommonOOSSecretParameterValue)))
		})
	})
})

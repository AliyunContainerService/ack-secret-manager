// data_fetch_test.go - Data Fetch E2E tests
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

var _ = Describe("Data Fetch E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-datafetch-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	Context("Normal data fetch", func() {
		It("Should fetch normal data", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-normal-data-store",
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

			// Create ExternalSecret to fetch normal data
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-normal-data-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "normal-secret-key",
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

			// Validate ExternalSecret status update
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("JSON key parsing", func() {
		It("Should parse JSON key", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-json-key-store",
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

			// Create ExternalSecret using JMESPath to parse JSON
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-json-key-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       JsonKMSSecretName,
							Name:      "json-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      secretStore.Name,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
							JMESPath: []api.JMESPathObject{
								{
									Path:        "name",
									ObjectAlias: "myname",
								},
								{
									Path:        "friends[0].name",
									ObjectAlias: "friendname",
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Validate ExternalSecret status update and created Kubernetes Secret
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Verify the created Kubernetes Secret contains the expected data from JSON parsing
			validateParsedSecretContent(ctx, externalSecret, []string{"myname", "friendname"})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("JSON auto parsing", func() {
		It("Should auto parse JSON", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-json-auto-store",
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

			// Create ExternalSecret using data processing functionality
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-json-auto-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					DataProcess: []api.DataProcess{
						{
							Extract: &api.DataSource{
								Key:       JsonKMSSecretName,
								Name:      "json-auto-secret-key",
								VersionId: "v1",
								SecretStoreRef: &api.SecretStoreRef{
									Name:      secretStore.Name,
									Namespace: secretStore.Namespace,
									Kind:      ResourceSecretStore,
								},
							},
							ReplaceKey: []api.ReplaceRule{
								{
									Source: "^n.*e$",
									Target: "namekey",
								},
								{
									Source: "^a.*e$",
									Target: "agekey",
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Validate ExternalSecret status update and created Kubernetes Secret
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Verify the created Kubernetes Secret contains the expected data from JSON auto parsing
			validateParsedSecretContent(ctx, externalSecret, []string{"namekey", "agekey"})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("YAML key parsing", func() {
		It("Should parse YAML key", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-yaml-key-store",
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

			// Create ExternalSecret using JMESPath to parse YAML
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-yaml-key-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       YamlKMSSecretName,
							Name:      "yaml-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      secretStore.Name,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
							JMESPath: []api.JMESPathObject{
								{
									Path:        "name",
									ObjectAlias: "myname",
								},
								{
									Path:        "friends[0].name",
									ObjectAlias: "friendname",
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Validate ExternalSecret status update and created Kubernetes Secret
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Verify the created Kubernetes Secret contains the expected data from YAML parsing
			validateParsedSecretContent(ctx, externalSecret, []string{"myname", "friendname"})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("YAML auto parsing", func() {
		It("Should auto parse YAML", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-yaml-auto-store",
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

			// Create ExternalSecret using data processing functionality to parse YAML
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-yaml-auto-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					DataProcess: []api.DataProcess{
						{
							Extract: &api.DataSource{
								Key:       YamlKMSSecretName,
								Name:      "yaml-auto-secret-key",
								VersionId: "v1",
								SecretStoreRef: &api.SecretStoreRef{
									Name:      secretStore.Name,
									Namespace: secretStore.Namespace,
									Kind:      ResourceSecretStore,
								},
							},
							ReplaceKey: []api.ReplaceRule{
								{
									Source: "^n.*e$",
									Target: "namekey",
								},
								{
									Source: "^a.*e$",
									Target: "agekey",
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Validate ExternalSecret status update and created Kubernetes Secret
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Verify the created Kubernetes Secret contains the expected data from YAML auto parsing
			validateParsedSecretContent(ctx, externalSecret, []string{"namekey", "agekey"})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})
})

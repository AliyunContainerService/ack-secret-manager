// gateway_test.go - KMS Gateway E2E tests
package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Gateway E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-gateway-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	Context("Dedicated gateway", func() {
		It("Should work with dedicated gateway", func() {
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-dedicated-gateway-store-" + getRandString(),
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
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())
			})

			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			// Create ExternalSecret using the store with dedicated gateway
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-dedicated-gateway-externalsecret",
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
								Name: clusterSecretStore.Name,
								Kind: ResourceClusterSecretStore,
							},
							KmsEndpoint: DedicatedKMSEndpoint,
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

	Context("Shared gateway", func() {
		It("Should work with shared gateway", func() {
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-shared-gateway-store-" + getRandString(),
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
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())
			})

			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			// Create ExternalSecret using the store with shared gateway
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-shared-gateway-externalsecret",
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
								Name: clusterSecretStore.Name,
								Kind: ResourceClusterSecretStore,
							},
							KmsEndpoint: SharedKMSEndpoint,
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

	// Test: ENV authentication with custom endpoint (this was the bug scenario)
	Context("ENV authentication with custom endpoint", func() {
		It("Should successfully sync secret from custom KMS endpoint without SecretStore", func() {
			By("patching Deployment to enable ENV-based RRSA authentication")
			patchDeploymentRRSAEnv(ctx, "kube-system", "ack-secret-manager")
			DeferCleanup(func() {
				By("cleaning up RRSA env vars from Deployment")
				restoreDeploymentRRSAEnv(ctx, "kube-system", "ack-secret-manager")
			})

			By("creating ExternalSecret with custom kmsEndpoint (no SecretStoreRef)")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env-custom-endpoint",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:         CommonKMSSecretName,
							Name:        "test-env-secret",
							KmsEndpoint: DedicatedKMSEndpoint, // Custom endpoint without SecretStore
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	// Test: Multiple data items with different endpoints
	Context("Multiple data items with different endpoints", func() {
		It("Should successfully sync secrets from different endpoints in one ExternalSecret", func() {
			By("patching Deployment to enable ENV-based RRSA authentication")
			patchDeploymentRRSAEnv(ctx, "kube-system", "ack-secret-manager")
			DeferCleanup(func() {
				By("cleaning up RRSA env vars from Deployment")
				restoreDeploymentRRSAEnv(ctx, "kube-system", "ack-secret-manager")
			})

			By("creating ExternalSecret with multiple endpoints")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-multi-endpoint",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:         CommonKMSSecretName,
							Name:        "secret-vpc",
							KmsEndpoint: SharedKMSEndpoint, // VPC endpoint
						},
						{
							Key:         CommonKMSSecretName,
							Name:        "secret-dedicated",
							KmsEndpoint: DedicatedKMSEndpoint, // Dedicated endpoint
						},
						{
							Key:  CommonKMSSecretName,
							Name: "secret-default",
							// No kmsEndpoint, use default
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Controller creates ONE K8s Secret named after the ExternalSecret,
			// with DataSource.Name as keys in the Secret data.
			Eventually(func() bool {
				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace.Name,
					Name:      externalSecret.Name,
				}, secret)
				if err != nil {
					return false
				}
				return len(secret.Data["secret-vpc"]) > 0 && len(secret.Data["secret-dedicated"]) > 0 && len(secret.Data["secret-default"]) > 0
			}, 90*time.Second, 2*time.Second).Should(BeTrue(), "All secrets should be created from different endpoints")

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)

			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	// Test: dataProcess with custom endpoint
	Context("dataProcess with custom endpoint", func() {
		It("Should successfully sync and process secret from custom endpoint", func() {
			By("patching Deployment to enable ENV-based RRSA authentication")
			patchDeploymentRRSAEnv(ctx, "kube-system", "ack-secret-manager")
			DeferCleanup(func() {
				By("cleaning up RRSA env vars from Deployment")
				restoreDeploymentRRSAEnv(ctx, "kube-system", "ack-secret-manager")
			})

			By("creating ExternalSecret with dataProcess and custom endpoint")
			// Use JsonKMSSecretName which contains structured JSON data:
			// {"name":"xiaoming","age":10,"friends":[...]}
			// dataProcess.ReplaceKey requires map-formatted data, not plain text.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-dataprocess-endpoint",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					DataProcess: []api.DataProcess{
						{
							Extract: &api.DataSource{
								Key:         JsonKMSSecretName,
								KmsEndpoint: DedicatedKMSEndpoint,
							},
							ReplaceKey: []api.ReplaceRule{
								{
									Source: "^n.*e$", // matches "name"
									Target: "namekey",
								},
								{
									Source: "^a.*e$", // matches "age"
									Target: "agekey",
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	// Test: ServiceAccount RRSA with custom endpoint (combination test)
	Context("ServiceAccount RRSA with custom KMS endpoint", func() {
		It("Should successfully sync secret using ServiceAccount RRSA with dedicated endpoint", func() {
			By("creating a SecretStore with ServiceAccountRef")
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa-endpoint-store",
					Namespace: testNamespace.Name,
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
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, secretStore)).To(Succeed())
			})

			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			By("creating ExternalSecret with ServiceAccountRef and custom kmsEndpoint")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa-endpoint-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  CommonKMSSecretName,
							Name: "test-sa-endpoint-secret",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      secretStore.Name,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
							KmsEndpoint: DedicatedKMSEndpoint, // Custom endpoint with ServiceAccount RRSA
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)

			CleanupExternalSecret(ctx, externalSecret)
		})
	})
})

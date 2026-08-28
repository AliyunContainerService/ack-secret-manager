// gateway_test.go - KMS Gateway E2E tests
package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
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
			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
		})
	})

	// Test: ENV authentication with custom endpoint (this was the bug scenario)
	Context("ENV authentication with custom endpoint", func() {
		It("Should successfully sync secret from custom KMS endpoint without SecretStore", func() {
			By("patching Deployment to enable ENV-based RRSA authentication")
			patchDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			DeferCleanup(func() {
				By("restoring the RRSA env baseline on the Deployment")
				restoreDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
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

			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
		})
	})

	// Test: composite endpoint client lifecycle GC. When spec.data[].kmsEndpoint
	// changes on an existing ExternalSecret, the controller must GC the stale
	// "clientName#endpoint" composite client and rebuild one for the new
	// endpoint; the re-sync must succeed and the final deletion must complete
	// without hanging.
	Context("Endpoint change triggers composite client rebuild", func() {
		It("Should re-sync successfully after kmsEndpoint is updated on an existing ExternalSecret", func() {
			By("patching Deployment to enable ENV-based RRSA authentication")
			patchDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			DeferCleanup(func() {
				By("restoring the RRSA env baseline on the Deployment")
				restoreDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			})

			// Create a dedicated KMS secret so we can stage a new version
			// before the endpoint change; the fingerprint debounce requires
			// a data change for SynchronizationTime to advance.
			By("creating a dedicated KMS secret for the endpoint change spec")
			endpointKMSName, err := GlobalResourceManager.CreateKMSSecretForCredentialUpdate(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to create dedicated KMS secret for endpoint change spec")
			DeferCleanup(func() {
				if delErr := GlobalResourceManager.DeleteKMSSecret(endpointKMSName); delErr != nil {
					GinkgoWriter.Printf("WARNING: failed to delete dedicated KMS secret %s: %v\n", endpointKMSName, delErr)
				}
			})

			By("creating ExternalSecret with the dedicated KMS endpoint")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-endpoint-change-" + getRandString(),
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							// No VersionId: always fetch the latest version so
							// a staged remote value becomes observable on the
							// next sync round.
							Key:         endpointKMSName,
							Name:        "endpoint-change-secret",
							KmsEndpoint: DedicatedKMSEndpoint,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			By("staging a new remote value that only a fresh sync can observe")
			stagedEndpointValue := CredentialUpdateKMSSecretInitialValue + "-endpoint-changed"
			Expect(GlobalResourceManager.PutKMSSecretVersion(ctx, endpointKMSName, "e2e-endpoint-change", stagedEndpointValue)).To(Succeed(),
				"failed to stage remote KMS version for endpoint change spec")

			By("capturing the last sync time before the endpoint change")
			esKey := types.NamespacedName{Namespace: externalSecret.Namespace, Name: externalSecret.Name}
			before := &api.ExternalSecret{}
			Expect(k8sClient.Get(ctx, esKey, before)).To(Succeed())
			Expect(before.Status.DataSyncResults).NotTo(BeEmpty())
			lastSyncTime := before.Status.DataSyncResults[0].SynchronizationTime

			By("updating kmsEndpoint to the shared KMS endpoint (stale composite client must be GC-ed)")
			Eventually(func() error {
				latest := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, esKey, latest); err != nil {
					return err
				}
				latest.Spec.Data[0].KmsEndpoint = SharedKMSEndpoint
				return k8sClient.Update(ctx, latest)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*2).Should(Succeed(),
				"the ExternalSecret kmsEndpoint update should be applied")

			By("waiting for a fresh successful sync served by the rebuilt composite client")
			Eventually(func() bool {
				latest := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, esKey, latest); err != nil {
					GinkgoWriter.Printf("[DIAG] endpoint-change ES get error: %v\n", err)
					return false
				}
				if len(latest.Status.DataSyncResults) == 0 {
					GinkgoWriter.Printf("[DIAG] endpoint-change ES has no DataSyncResults yet\n")
					return false
				}
				for i, result := range latest.Status.DataSyncResults {
					if result.Status != "Succeeded" {
						GinkgoWriter.Printf("[DIAG] endpoint-change DataSyncResult[%d] status=%s reason=%s syncTime=%v\n",
							i, result.Status, result.Reason, result.SynchronizationTime.Time)
						return false
					}
					// A sync time strictly after the pre-change baseline proves the
					// result comes from a sync performed with the NEW endpoint,
					// not from the stale result of the old composite client.
					if !result.SynchronizationTime.After(lastSyncTime.Time) {
						GinkgoWriter.Printf("[DIAG] endpoint-change DataSyncResult[%d] syncTime %v not after baseline %v\n",
							i, result.SynchronizationTime.Time, lastSyncTime.Time)
						return false
					}
				}
				return true
			}).WithTimeout(time.Second*120).WithPolling(time.Second*5).Should(BeTrue(),
				"ExternalSecret should re-sync successfully after the kmsEndpoint change")

			By("validating the synced Secret carries the staged value via the rebuilt composite client")
			syncedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNamespace.Name,
				Name:      externalSecret.Name,
			}, syncedSecret)).To(Succeed())
			Expect(string(syncedSecret.Data["endpoint-change-secret"])).To(Equal(stagedEndpointValue),
				"synced Secret content should match the staged value after the endpoint change")
		})
	})

	// Test: invalid kmsEndpoint is rejected fail-closed (RN 0.6.4 SSRF guard).
	// validateKmsEndpoint (pkg/backend/provider/kms/endpoint_validation.go)
	// rejects every endpoint that is not a bare lowercase KMS hostname
	// before the KMS client is constructed (client_provider.go
	// newClientWithAuth): credential resolution (auth.GetAuthCred) runs first
	// and may itself perform network calls, but no KMS client is ever built
	// and no data-plane request reaches the invalid endpoint, so the spec
	// needs no reachable backend: every data key must surface a Failed sync
	// status and no cluster Secret may be written.
	Context("Invalid kmsEndpoint rejected fail-closed", func() {
		It("Should fail the sync and write no Secret for malformed and SSRF-class endpoints", func() {
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-invalid-endpoint-store-" + getRandString(),
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

			By("creating an ExternalSecret whose data keys all carry invalid endpoints")
			// All three endpoint classes rejected by the unit-level table
			// (endpoint_validation_test.go): an SSRF metadata IP with an http
			// scheme, a bare SSRF metadata IP literal, and a syntactically
			// near-valid gateway hostname carrying a port.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-invalid-endpoint-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
							Name:      "ssrf-metadata-ip-with-scheme",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name: clusterSecretStore.Name,
								Kind: ResourceClusterSecretStore,
							},
							KmsEndpoint: "http://169.254.169.254",
						},
						{
							Key:       CommonKMSSecretName,
							Name:      "ssrf-metadata-ip-literal",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name: clusterSecretStore.Name,
								Kind: ResourceClusterSecretStore,
							},
							KmsEndpoint: "169.254.169.254",
						},
						{
							Key:       CommonKMSSecretName,
							Name:      "malformed-gateway-port",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name: clusterSecretStore.Name,
								Kind: ResourceClusterSecretStore,
							},
							KmsEndpoint: "kms-vpc.cn-hangzhou.aliyuncs.com:443",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			By("observing every data key fail with the endpoint validation wording")
			// The validation error ("KMS endpoint %q does not match any allowed
			// KMS endpoint pattern ...") is returned before the KMS client is
			// constructed, so it surfaces verbatim in the per-key Failed DataSyncResults.
			expectExternalSecretFailedWith(ctx, externalSecret.Namespace, externalSecret.Name,
				"does not match any allowed KMS endpoint pattern", time.Second*60)

			By("consistently observing that no cluster Secret is written (fail-closed)")
			Consistently(func() bool {
				syncedSecret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: externalSecret.Namespace,
					Name:      externalSecret.Name,
				}, syncedSecret)
				if err != nil {
					// Transient API errors are not evidence of a Secret appearing.
					return k8serrors.IsNotFound(err)
				}
				return false
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(BeTrue(),
				"no cluster Secret may be written when every endpoint is rejected before the KMS client is constructed")
		})
	})

	// Test: Multiple data items with different endpoints
	Context("Multiple data items with different endpoints", func() {
		It("Should successfully sync secrets from different endpoints in one ExternalSecret", func() {
			By("patching Deployment to enable ENV-based RRSA authentication")
			patchDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			DeferCleanup(func() {
				By("restoring the RRSA env baseline on the Deployment")
				restoreDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
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
							Name:        "secret-shared",
							KmsEndpoint: SharedKMSEndpoint, // Shared (public) KMS endpoint
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
			// with DataSource.Name as keys in the Secret data. All three data
			// sources read the same KMS secret, so each key must carry exactly
			// the preset KMS value (not just non-empty bytes), which proves each
			// endpoint actually fetched the source credential.
			Eventually(func() bool {
				secret := &corev1.Secret{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: testNamespace.Name,
					Name:      externalSecret.Name,
				}, secret)
				if err != nil {
					return false
				}
				return string(secret.Data["secret-shared"]) == CommonKMSSecretValue &&
					string(secret.Data["secret-dedicated"]) == CommonKMSSecretValue &&
					string(secret.Data["secret-default"]) == CommonKMSSecretValue
			}, 90*time.Second, 2*time.Second).Should(BeTrue(),
				"All keys should be synced from different endpoints with the exact preset KMS value")

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*90)

			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
		})
	})

	// Test: dataProcess with custom endpoint
	Context("dataProcess with custom endpoint", func() {
		It("Should successfully sync and process secret from custom endpoint", func() {
			By("patching Deployment to enable ENV-based RRSA authentication")
			patchDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			DeferCleanup(func() {
				By("restoring the RRSA env baseline on the Deployment")
				restoreDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
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

			By("validating dataProcess.ReplaceKey rewrote the extracted keys")
			// ReplaceKey semantics (see common.ProcessExtractedExternalSecretData
			// and utils.RewriteRegexp): each rule is a regexp applied to the KEYS
			// of the extracted JSON/YAML map; a fully matching key is renamed to
			// the target while its VALUE is preserved, and the original key
			// disappears. Source secret JsonKMSSecretName is
			// {"name":"xiaoming","age":10,"friends":[...]}.
			syncedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNamespace.Name,
				Name:      externalSecret.Name,
			}, syncedSecret)).To(Succeed(), "synced Secret should exist for dataProcess validation")

			Expect(syncedSecret.Data).To(HaveKey("namekey"),
				"key 'name' should be renamed to 'namekey' by ReplaceKey rule")
			Expect(string(syncedSecret.Data["namekey"])).To(Equal("xiaoming"),
				"renamed key 'namekey' should preserve the original value of 'name'")
			Expect(syncedSecret.Data).To(HaveKey("agekey"),
				"key 'age' should be renamed to 'agekey' by ReplaceKey rule")
			Expect(string(syncedSecret.Data["agekey"])).To(Equal("10"),
				"renamed key 'agekey' should preserve the original value of 'age'")
			Expect(syncedSecret.Data).NotTo(HaveKey("name"),
				"original key 'name' should be replaced and no longer exist")
			Expect(syncedSecret.Data).NotTo(HaveKey("age"),
				"original key 'age' should be replaced and no longer exist")

			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
		})
	})

	// Test: ServiceAccount RRSA with custom endpoint (combination test)
	Context("ServiceAccount RRSA with custom KMS endpoint", func() {
		It("Should successfully sync secret using ServiceAccount RRSA with dedicated endpoint", func() {
			By("creating a local ServiceAccount backed by a dynamic RRSA role")
			// The ServiceAccount must live in the SAME namespace as the SecretStore
			// and own its own dynamic RRSA role; see authentication_test.go for the
			// full rationale.
			serviceAccount := createRRSAServiceAccountForTest(ctx, testNamespace.Name, "test-sa-endpoint-sa-"+getRandString())

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
								Name:      serviceAccount.Name,
								Namespace: testNamespace.Name,
							},
							OIDCProviderARN: OIDCProviderARN,
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

			CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
		})
	})
})

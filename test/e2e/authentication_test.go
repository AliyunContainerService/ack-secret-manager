// authentication_test.go - Authentication E2E tests
package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Authentication E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-auth-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	Context("WorkerRole authentication", func() {
		It("Should validate WorkerRole authentication", func() {
			// Temporarily remove the globally injected ENV RRSA vars so WorkerRole
			// (priority 5) is genuinely exercised; baseline restored via
			// DeferCleanup (see removeRRSAEnvTemporarily).
			if !workerRoleEnabledInDeployment(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName) {
				Skip("WorkerRole authentication is disabled on the ack-secret-manager Deployment (--enable-worker-role=false); the WorkerRole auth provider is not part of the auth chain, so this test cannot validate it")
			}

			By("temporarily removing RRSA env vars so the auth chain falls through to WorkerRole")
			// Register the restore cleanup BEFORE mutating the Deployment (same
			// "register cleanup first" pattern as the AK cases below): if the
			// removal or any later step fails mid-way, the RRSA env baseline is
			// still guaranteed to be restored, keeping later test cases clean.
			DeferCleanup(func() {
				By("restoring RRSA env baseline on the Deployment")
				restoreRRSAEnvBaseline(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			})
			removeRRSAEnvTemporarily(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)

			// WorkerRole authentication is used when ExternalSecret doesn't reference a SecretStore
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workerrole-externalsecret",
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

	Context("RamRole authentication", func() {
		It("Should validate RamRole authentication", func() {
			// Create Secret with AKSK
			secret := createAKSecret(ctx, testNamespace.Name, "secret-"+getRandString(), RAMUserAccessKeyIDForRolePlay, RAMUserAccessKeySecretForRolePlay)

			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-ramrole-store-" + getRandString(),
				},
				Spec: api.ClusterSecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							AccessKey: &api.SecretRef{
								Name:      secret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      secret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
							},
							RAMRoleARN:         RAMRoleArnForRolePlay,
							RAMRoleSessionName: "ack-secret-manager-role-play",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, clusterSecretStore)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())
			})

			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			// Create ExternalSecret using the ClusterSecretStore
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ramrole-externalsecret",
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

	Context("AKSK authentication", func() {
		It("Should validate AKSK authentication", func() {
			// Create Secret with AKSK
			secret := createAKSecret(ctx, testNamespace.Name, "aksk-secret-"+getRandString(), RAMUserAccessKeyID, RAMUserAccessKeySecret)

			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-aksk-store",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							AccessKey: &api.SecretRef{
								Name:      secret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      secret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())

			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Create ExternalSecret using the SecretStore
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-aksk-externalsecret",
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

			// Validate ExternalSecret status update
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("OIDC authentication", func() {
		It("Should validate OIDC authentication", func() {
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-oidc-store-" + getRandString(),
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

			// Create ExternalSecret using the ClusterSecretStore
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-oidc-externalsecret",
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

	Context("ServiceAccount authentication", func() {
		It("Should validate ServiceAccount authentication", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa-store",
					Namespace: ServiceaccountNamespaceForSAAuth.Name,
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

			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Create ExternalSecret using the SecretStore
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-sa-externalsecret",
					Namespace: ServiceaccountNamespaceForSAAuth.Name,
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

			// Validate ExternalSecret status update
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	// Test: Environment variable RRSA authentication (Priority 1b). The cleanup
	// MUST restore the BeforeSuite ENV baseline instead of removing the globally
	// injected vars: removing them would silently break ENV RRSA for all
	// subsequent tests (restoreDeploymentRRSAEnv is baseline-aware).
	Context("Environment variable RRSA authentication", func() {
		It("Should successfully sync secret using OIDC credentials from environment variables", func() {
			By("patching Deployment to enable ENV-based RRSA authentication (idempotent: BeforeSuite already injects the baseline)")
			patchDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			DeferCleanup(func() {
				By("restoring the RRSA env baseline on the Deployment (NOT removing the globally injected env vars)")
				restoreDeploymentRRSAEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			})

			By("creating ExternalSecret with ENV-based RRSA authentication (no SecretStoreRef)")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env-rrsa-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					// No SecretStoreRef - will use environment variable credentials
					Data: []api.DataSource{
						{
							Key:  CommonKMSSecretName,
							Name: "test-env-rrsa-secret",
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	// Test: Authentication priority - ServiceAccount RRSA vs AK.
	// Negative-discrimination design: the AK belongs to a RAM user with NO KMS
	// permission, so a mistakenly selected AK path fails the sync (permanent
	// 403, not retried); a successful sync proves ServiceAccount RRSA took
	// precedence. WARNING: do NOT replace this AK with the privileged global AK
	// (RAMUserAccessKeyID/RAMUserAccessKeySecret), or the test loses its
	// discriminating power and silently passes on any authentication path.
	Context("Authentication priority - ServiceAccount RRSA takes precedence over AK", func() {
		It("Should use ServiceAccount RRSA when both SA and AK are configured", func() {
			By("creating a RAM user AK without KMS permission")
			noKmsAccessKeyID, noKmsAccessKeySecret, noKmsUserName, err := GlobalResourceManager.CreateRamUserWithoutKMSPermission(ctx)
			// Register cleanup BEFORE the assertion so it also runs when creation
			// partially fails (e.g. user created but access key creation failed).
			DeferCleanup(func() {
				if noKmsUserName != "" {
					if cleanupErr := GlobalResourceManager.DeleteRamUserAndAccessKey(noKmsUserName, noKmsAccessKeyID); cleanupErr != nil {
						GinkgoWriter.Printf("WARNING: failed to cleanup RAM user %s (AK %s): %v\n", noKmsUserName, noKmsAccessKeyID, cleanupErr)
					}
				}
			})
			Expect(err).NotTo(HaveOccurred())

			By("creating a Secret with AK/SK credentials")
			akSecret := createAKSecret(ctx, testNamespace.Name, "test-ak-secret", noKmsAccessKeyID, noKmsAccessKeySecret)
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, akSecret)
			})

			By("creating a local ServiceAccount backed by a dynamic RRSA role")
			// The ServiceAccount must live in the SAME namespace as the SecretStore
			// (default --enable-cross-namespace-auth-ref=false rejects cross-
			// namespace refs with ValidationFailed). Its dynamic RAM role's trust
			// policy targets exactly this (namespace, ServiceAccount) pair, so the
			// OIDC AssumeRole succeeds (the shared global SA's role only trusts the
			// global SA and would fail with STS 401).
			serviceAccount := createRRSAServiceAccountForTest(ctx, testNamespace.Name, "test-priority-sa-"+getRandString())

			By("creating SecretStore with both ServiceAccountRef and AccessKey")
			// ServiceAccountRef should take precedence over AccessKey
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-priority-store",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							ServiceAccountRef: &api.ServiceAccountRef{
								Name:      serviceAccount.Name,
								Namespace: testNamespace.Name,
							},
							// Also configure AK (should be ignored due to SA priority)
							AccessKey: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
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

			By("creating ExternalSecret that references the SecretStore")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-priority-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  CommonKMSSecretName,
							Name: "test-priority-secret",
							SecretStoreRef: &api.SecretStoreRef{
								Name: secretStore.Name,
								Kind: ResourceSecretStore,
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)

			// Clean up
			CleanupExternalSecret(ctx, externalSecret)
		})

		// Reverse-direction priority test: rules out two alternative explanations
		// of the positive test -- (a) the chain falls back to another path on
		// failure; (b) a path is picked at random. The SecretStore carries a
		// VALID, KMS-privileged AK next to a ServiceAccountRef whose RRSA role
		// does not exist; serviceAccountRef is exclusive (the AK is never read,
		// and SA failure is not a fallback trigger). If the AK were used the
		// sync would SUCCEED, so a persistently FAILED sync with an
		// OIDC/AssumeRole-flavored error proves the SA path was exclusively
		// selected with no AK fallback.
		It("ServiceAccount RRSA is exclusively selected - no fallback to AK on failure", func() {
			By("creating a dedicated ServiceAccount whose RRSA role ARN does not exist")
			// Invalid ARN format follows the pattern used in reconcile_test.go
			// (independent name; the shared SA for SA auth is NOT touched).
			const nonExistentRoleArn = "arn:aws:iam::123456789012:role/nonexistent-rrsa-role-for-priority-test"
			dedicatedSA := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-priority-nofallback-sa",
					Namespace: testNamespace.Name,
					Annotations: map[string]string{
						ACKRRSAAnnotation: nonExistentRoleArn,
					},
				},
			}
			Expect(k8sClient.Create(ctx, dedicatedSA)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, dedicatedSA)
			})

			By("creating a Secret with the VALID global AK/SK credentials (has KMS permission)")
			akSecret := createAKSecret(ctx, testNamespace.Name, "test-priority-nofallback-ak-secret", RAMUserAccessKeyID, RAMUserAccessKeySecret)
			DeferCleanup(func() {
				// Global credentials - only the K8s Secret needs deletion, no cloud-side cleanup.
				_ = k8sClient.Delete(ctx, akSecret)
			})

			By("creating SecretStore with both ServiceAccountRef (broken RRSA) and valid AK")
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-priority-nofallback-store",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							ServiceAccountRef: &api.ServiceAccountRef{
								Name:      dedicatedSA.Name,
								Namespace: testNamespace.Name,
							},
							// Valid AK present: if any fallback/random selection reached
							// it, the sync would succeed and this test would fail.
							AccessKey: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, secretStore)
			})

			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			By("creating ExternalSecret that references the SecretStore")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-priority-nofallback-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  CommonKMSSecretName,
							Name: "test-priority-nofallback-secret",
							SecretStoreRef: &api.SecretStoreRef{
								Name: secretStore.Name,
								Kind: ResourceSecretStore,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			By("waiting for the sync to fail on the SA/OIDC path")
			// The dynamic token is issued successfully, but AssumeRoleWithOIDC
			// fails because the RAM role does not exist / does not trust the
			// cluster. That STS error is permanent (not throttling/5xx/network,
			// see utils.JudgeNeedRetry), so it is surfaced immediately without
			// exhausting retries.
			var failedReason string
			Eventually(func() bool {
				es := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, es); err != nil {
					return false
				}
				if len(es.Status.DataSyncResults) == 0 {
					return false
				}
				for _, result := range es.Status.DataSyncResults {
					if result.Status != "Failed" {
						return false
					}
					if result.Reason == "" {
						return false
					}
					failedReason = result.Reason
				}
				return true
			}).WithTimeout(time.Second*120).WithPolling(time.Second*5).Should(BeTrue(),
				"ExternalSecret should report a Failed DataSyncResult with a non-empty Reason because the SA RRSA role does not exist")

			By("verifying the failure originates from the OIDC/AssumeRole (SA) path")
			// auth.go wraps the STS failure as "failed to assume role with oidc: ..."
			// (pkg/backend/auth/auth.go), so the Reason must carry the OIDC wording.
			reasonLower := strings.ToLower(failedReason)
			hasOidcKeyword := strings.Contains(reasonLower, "oidc") ||
				strings.Contains(reasonLower, "assume role with oidc")
			Expect(hasOidcKeyword).To(BeTrue(),
				fmt.Sprintf("failure Reason should point to the OIDC/AssumeRole (ServiceAccount) path, got: %q", failedReason))

			By("verifying the sync never succeeds and no Secret is ever created (no AK fallback)")
			// If the chain fell back to the valid AK, or randomly selected the AK
			// path, the sync would succeed within this window and both assertions
			// below would fail.
			Consistently(func() bool {
				es := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, es); err != nil {
					// Transient apiserver error: this round is not conclusive, keep polling.
					return true
				}
				for _, result := range es.Status.DataSyncResults {
					if result.Status == "Succeeded" {
						return false
					}
				}
				syncedSecret := &corev1.Secret{}
				err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, syncedSecret)
				if err == nil {
					// Secret exists -> some path synced successfully -> fallback happened.
					return false
				}
				if k8serrors.IsNotFound(err) {
					return true
				}
				// Transient apiserver error (timeout/5xx): not conclusive, keep polling.
				return true
			}).WithTimeout(time.Second*20).WithPolling(time.Second*5).Should(BeTrue(),
				"sync must stay Failed and the target Secret must not exist; a Succeeded status or an existing Secret would prove a fallback/random selection to the valid AK")

		})

		// Test: oidcTierAllowed contract (pkg/backend/auth/auth.go). When the
		// SecretStore carries complete AK+SK+roleArn and oidcProviderARN is left
		// unset (auto-derived), AK AssumeRole must take precedence over
		// file-based RRSA; the sync must succeed instead of failing on a
		// missing OIDC token file. The role-play RAM role (dual trust policy,
		// created by CreateRamRoleUserForRolePlay) is reused, and the role-play
		// user only holds sts:AssumeRole on that role (no direct KMS
		// permission), so a successful sync additionally proves the credentials
		// came from assuming the role rather than from the bare AK.
		It("Should use AK AssumeRole over auto-derived OIDC RRSA when AK+SK+roleArn are complete", func() {
			By("creating a Secret with the role-play RAM user AK/SK")
			akSecret := createAKSecret(ctx, testNamespace.Name, "test-akassume-ak-secret-"+getRandString(),
				RAMUserAccessKeyIDForRolePlay, RAMUserAccessKeySecretForRolePlay)
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, akSecret)
			})

			By("creating a SecretStore with complete AK+SK+roleArn and NO explicit oidcProviderARN")
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-akassume-store-" + getRandString(),
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							AccessKey: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
							},
							RAMRoleARN: RAMRoleArnForRolePlay,
							// OIDCProviderARN deliberately omitted: it is auto-derived,
							// and under this combination AK AssumeRole must win.
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, secretStore)
			})

			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			By("creating ExternalSecret that references the SecretStore")
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-akassume-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  CommonKMSSecretName,
							Name: "test-akassume-secret",
							SecretStoreRef: &api.SecretStoreRef{
								Name: secretStore.Name,
								Kind: ResourceSecretStore,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			// A successful sync proves the AK AssumeRole path was selected:
			// the role-play user has no direct KMS permission, and the AK
			// AssumeRole tier must preempt the auto-derived OIDC RRSA tier.
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*60)
		})
	})

	// Test: ENV credentials vs SecretStore authentication are mutually exclusive
	// (no stacked fallback per docs/auth_guide.md). BeforeSuite keeps the ENV
	// RRSA baseline live, which makes the specs discriminating: they would fail
	// if the controller fell back to the working ENV credentials when the
	// storeRef path cannot serve the request.
	Context("ENV and SecretStore authentication are mutually exclusive", func() {
		It("Should sync successfully via storeRef while ENV RRSA credentials are present", func() {
			By("creating a Secret with valid AK/SK credentials")
			akSecret := createAKSecret(ctx, testNamespace.Name, "test-env-storeref-ak-secret", RAMUserAccessKeyID, RAMUserAccessKeySecret)
			DeferCleanup(func() {
				// Global credentials - only the K8s Secret needs deletion, no cloud-side cleanup.
				_ = k8sClient.Delete(ctx, akSecret)
			})

			By("creating a SecretStore backed by the valid AK credentials")
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env-storeref-store",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							AccessKey: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, secretStore)
			})

			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			By("creating ExternalSecret that references the SecretStore")
			// The ENV RRSA baseline is present all along (injected by BeforeSuite);
			// the storeRef path must still work normally side by side with it.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env-storeref-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  CommonKMSSecretName,
							Name: "test-env-storeref-secret",
							SecretStoreRef: &api.SecretStoreRef{
								Name: secretStore.Name,
								Kind: ResourceSecretStore,
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

		It("ENV RRSA is never used as a fallback when storeRef credentials fail", func() {
			By("creating a RAM user AK without KMS permission")
			noKmsAccessKeyID, noKmsAccessKeySecret, noKmsUserName, err := GlobalResourceManager.CreateRamUserWithoutKMSPermission(ctx)
			// Register cleanup BEFORE the assertion so it also runs when creation
			// partially fails (e.g. user created but access key creation failed).
			DeferCleanup(func() {
				if noKmsUserName != "" {
					if cleanupErr := GlobalResourceManager.DeleteRamUserAndAccessKey(noKmsUserName, noKmsAccessKeyID); cleanupErr != nil {
						GinkgoWriter.Printf("WARNING: failed to cleanup RAM user %s (AK %s): %v\n", noKmsUserName, noKmsAccessKeyID, cleanupErr)
					}
				}
			})
			Expect(err).NotTo(HaveOccurred())

			By("creating a Secret with the no-KMS-permission AK/SK credentials")
			akSecret := createAKSecret(ctx, testNamespace.Name, "test-env-nofallback-ak-secret", noKmsAccessKeyID, noKmsAccessKeySecret)
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, akSecret)
			})

			By("creating a SecretStore backed by the no-KMS-permission AK credentials")
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env-nofallback-store",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							AccessKey: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeyId",
							},
							AccessKeySecret: &api.SecretRef{
								Name:      akSecret.Name,
								Namespace: testNamespace.Name,
								Key:       "accessKeySecret",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, secretStore)
			})

			// SecretStore readiness does NOT remotely validate the AK (the
			// AccessKeyProvider issues credentials locally), so the store becomes
			// Ready even though every KMS call authenticated by this AK fails
			// with a permanent 403 permission error.
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			By("creating ExternalSecret that references the SecretStore")
			// Discrimination logic: the Deployment also holds the WORKING ENV RRSA
			// baseline injected by BeforeSuite. If the controller fell back to ENV
			// when the storeRef path fails, the sync would SUCCEED and both
			// assertions below would fail.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env-nofallback-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  CommonKMSSecretName,
							Name: "test-env-nofallback-secret",
							SecretStoreRef: &api.SecretStoreRef{
								Name: secretStore.Name,
								Kind: ResourceSecretStore,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, externalSecret)
			})

			By("waiting for the sync to fail on the storeRef path")
			// The no-KMS-permission AK yields a permanent 403 on GetSecretValue
			// (permanent errors are not retried, see fetch_retry.go), so the
			// Failed result is surfaced immediately without exhausting retries.
			Eventually(func() bool {
				es := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, es); err != nil {
					return false
				}
				if len(es.Status.DataSyncResults) == 0 {
					return false
				}
				for _, result := range es.Status.DataSyncResults {
					if result.Status != "Failed" {
						return false
					}
					if result.Reason == "" {
						return false
					}
				}
				return true
			}).WithTimeout(time.Second*120).WithPolling(time.Second*5).Should(BeTrue(),
				"ExternalSecret should report a Failed DataSyncResult with a non-empty Reason because the storeRef AK has no KMS permission")

			By("verifying the sync never succeeds and no Secret is ever created (no ENV fallback)")
			// If the controller stacked the ENV RRSA credentials on top of the
			// storeRef path (or fell back to them), the sync would succeed within
			// this window and both assertions below would fail.
			Consistently(func() bool {
				es := &api.ExternalSecret{}
				if err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, es); err != nil {
					// Transient apiserver error: this round is not conclusive, keep polling.
					return true
				}
				for _, result := range es.Status.DataSyncResults {
					if result.Status == "Succeeded" {
						return false
					}
				}
				syncedSecret := &corev1.Secret{}
				err := k8sClient.Get(ctx, client.ObjectKey{
					Name:      externalSecret.Name,
					Namespace: externalSecret.Namespace,
				}, syncedSecret)
				if err == nil {
					// Secret exists -> some path synced successfully -> ENV fallback happened.
					return false
				}
				if k8serrors.IsNotFound(err) {
					return true
				}
				// Transient apiserver error (timeout/5xx): not conclusive, keep polling.
				return true
			}).WithTimeout(time.Second*20).WithPolling(time.Second*5).Should(BeTrue(),
				"sync must stay Failed and the target Secret must not exist; a Succeeded status or an existing Secret would prove a fallback to the ENV RRSA credentials")
		})
	})
})

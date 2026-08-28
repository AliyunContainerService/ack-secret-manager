// cross_account_sync_test.go - Cross-account sync E2E tests
// Tests each authentication method combined with cross-account sync via remoteRamRoleARN.
// All tests share the same target account resources (RAMRoleArnForCrossAccount, CrossAccountKMSSecretName)
// but each test uses an independent auth chain to ensure no interference between authentication methods.
//
// Test matrix:
//   - ServiceAccount RRSA + cross-account sync
//   - OIDC RRSA + cross-account sync
//   - AK AssumeRole + cross-account sync
//   - AK basic + cross-account sync
//   - WorkerRole (env-based) + cross-account sync (patches Deployment)
package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

var _ = Describe("Cross-account Sync E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		// Cross-account resources are only created when CROSS_ACCOUNT_ID,
		// CROSS_ACCOUNT_ACCESS_KEY_ID and CROSS_ACCOUNT_ACCESS_KEY_SECRET are configured
		// (see ResourceManager.CreateRamRoleForCrossAccount / CreateRemoteKMSCredential,
		// which skip gracefully otherwise). Without them both variables stay
		// empty and every spec in this suite must be Skipped.
		if CrossAccountKMSSecretName == "" || RAMRoleArnForCrossAccount == "" {
			Skip("cross-account resources not configured (created by GlobalResourceManager.SetupTestResources; true cross-account mode requires CROSS_ACCOUNT_ID, CROSS_ACCOUNT_ACCESS_KEY_ID and CROSS_ACCOUNT_ACCESS_KEY_SECRET env vars)")
		}
		testNamespace = createTestNamespace(ctx, "test-crossacct-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	// createCrossAccountExternalSecret creates an ExternalSecret targeting the cross-account KMS secret
	createCrossAccountExternalSecret := func(namespace, storeName, storeKind, secretName string) *api.ExternalSecret {
		es := &api.ExternalSecret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cross-acct-" + getRandString(),
				Namespace: namespace,
			},
			Spec: api.ExternalSecretSpec{
				Provider:         "kms",
				RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
				Data: []api.DataSource{
					{
						Key:       CrossAccountKMSSecretName,
						Name:      secretName,
						VersionId: "v1",
						SecretStoreRef: &api.SecretStoreRef{
							Name: storeName,
							Kind: storeKind,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, es)).To(Succeed())
		return es
	}

	// crossAccountSecretPayload is the exact SecretData stored in the remote
	// KMS secret by ResourceManager.CreateRemoteKMSCredential (see
	// resource_manager_test.go). Asserting it proves the synced Secret carries
	// the real cross-account payload, not just that a Secret object exists.
	const crossAccountSecretPayload = "cross-account-test-secret-data"

	// assertCrossAccountSecretContent verifies the synced Secret (named after
	// the ExternalSecret) carries the expected payload under the data source key.
	assertCrossAccountSecretContent := func(es *api.ExternalSecret, dataKey string) {
		syncedSecret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Namespace: es.Namespace,
			Name:      es.Name,
		}, syncedSecret)).To(Succeed(), "synced Secret should exist for content validation")
		Expect(syncedSecret.Data).To(HaveKey(dataKey), "synced Secret should contain the data source key")
		Expect(string(syncedSecret.Data[dataKey])).To(Equal(crossAccountSecretPayload),
			"synced Secret content should equal the remote KMS secret payload")
	}

	// Context 1: ServiceAccount RRSA + cross-account
	// Auth chain: SA annotation → STS AssumeRoleWithOIDC → temp creds → AssumeRole(RemoteRAMRoleARN) → final creds
	Context("ServiceAccount RRSA + cross-account sync", func() {
		It("Should sync secret using SA RRSA auth with remoteRamRoleARN", func() {
			By("creating ClusterSecretStore with ServiceAccountRef + remoteRamRoleARN")
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "crossacct-sa-store-" + getRandString(),
				},
				Spec: api.ClusterSecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							ServiceAccountRef: &api.ServiceAccountRef{
								Name:      ServiceaccountNameForSAAuth,
								Namespace: ServiceaccountNamespaceForSAAuth.Name,
							},
							OIDCProviderARN:          OIDCProviderARN,
							RemoteRAMRoleARN:         RAMRoleArnForCrossAccount,
							RemoteRAMRoleSessionName: "crossacct-sa-session",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, clusterSecretStore)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())
			})

			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			By("creating ExternalSecret targeting cross-account KMS secret")
			es := createCrossAccountExternalSecret(testNamespace.Name, clusterSecretStore.Name, ResourceClusterSecretStore, "crossacct-sa-secret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via SA RRSA + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)

			By("verifying the synced Secret carries the cross-account payload")
			assertCrossAccountSecretContent(es, "crossacct-sa-secret")
		})
	})

	// Context 2: OIDC (RRSA) + cross-account
	// Auth chain: OIDC token → AssumeRoleWithOIDC → temp creds → AssumeRole(RemoteRAMRoleARN) → final creds
	Context("OIDC RRSA + cross-account sync", func() {
		It("Should sync secret using OIDC auth with remoteRamRoleARN", func() {
			By("creating ClusterSecretStore with OIDC + remoteRamRoleARN")
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "crossacct-oidc-store-" + getRandString(),
				},
				Spec: api.ClusterSecretStoreSpec{
					KMS: &api.KMSProvider{
						KMS: &api.KMSAuth{
							RAMRoleARN:               RAMRoleArnForRRSA,
							OIDCProviderARN:          OIDCProviderARN,
							RemoteRAMRoleARN:         RAMRoleArnForCrossAccount,
							RemoteRAMRoleSessionName: "crossacct-oidc-session",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, clusterSecretStore)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())
			})

			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			By("creating ExternalSecret targeting cross-account KMS secret")
			es := createCrossAccountExternalSecret(testNamespace.Name, clusterSecretStore.Name, ResourceClusterSecretStore, "crossacct-oidc-secret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via OIDC + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)

			By("verifying the synced Secret carries the cross-account payload")
			assertCrossAccountSecretContent(es, "crossacct-oidc-secret")
		})
	})

	// Context 3: AK AssumeRole + cross-account
	// Auth chain: AK → AssumeRole(RAMRoleARN) → temp creds → AssumeRole(RemoteRAMRoleARN) → final creds
	Context("AK AssumeRole + cross-account sync", func() {
		It("Should sync secret using AK AssumeRole auth with remoteRamRoleARN", func() {
			By("creating Secret with AK/SK for role play")
			secret := createAKSecret(ctx, testNamespace.Name, "crossacct-ak-role-secret-"+getRandString(),
				RAMUserAccessKeyIDForRolePlay, RAMUserAccessKeySecretForRolePlay)

			By("creating ClusterSecretStore with AK AssumeRole + remoteRamRoleARN")
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "crossacct-ak-role-store-" + getRandString(),
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
							RAMRoleARN:               RAMRoleArnForRolePlay,
							RAMRoleSessionName:       "crossacct-ak-role-session",
							RemoteRAMRoleARN:         RAMRoleArnForCrossAccount,
							RemoteRAMRoleSessionName: "crossacct-ak-role-remote-session",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, clusterSecretStore)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())
			})

			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			By("creating ExternalSecret targeting cross-account KMS secret")
			es := createCrossAccountExternalSecret(testNamespace.Name, clusterSecretStore.Name, ResourceClusterSecretStore, "crossacct-ak-role-secret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via AK AssumeRole + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)

			By("verifying the synced Secret carries the cross-account payload")
			assertCrossAccountSecretContent(es, "crossacct-ak-role-secret")
		})
	})

	// Context 4: AK basic (no AssumeRole) + cross-account
	// Auth chain: AK (basic creds, no local AssumeRole) -> AssumeRole(RemoteRAMRoleARN) -> final creds -> KMS access in the target account
	// Note: AK basic + cross-account requires the AK user has sts:AssumeRole permission for the remote role
	Context("AK basic + cross-account sync", func() {
		It("Should sync secret using AK basic auth with remoteRamRoleARN", func() {
			By("creating Secret with AK/SK for basic auth user")
			secret := createAKSecret(ctx, testNamespace.Name, "crossacct-ak-basic-secret-"+getRandString(),
				RAMUserAccessKeyID, RAMUserAccessKeySecret)

			By("creating ClusterSecretStore with AK basic + remoteRamRoleARN")
			clusterSecretStore := &api.ClusterSecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name: "crossacct-ak-basic-store-" + getRandString(),
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
							// No RAMRoleARN - direct AK basic auth
							RemoteRAMRoleARN:         RAMRoleArnForCrossAccount,
							RemoteRAMRoleSessionName: "crossacct-ak-basic-session",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, clusterSecretStore)).To(Succeed())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, clusterSecretStore)).To(Succeed())
			})

			waitForClusterSecretStoreReady(ctx, clusterSecretStore.Name)

			By("creating ExternalSecret targeting cross-account KMS secret")
			es := createCrossAccountExternalSecret(testNamespace.Name, clusterSecretStore.Name, ResourceClusterSecretStore, "crossacct-ak-basic-secret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via AK basic + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)

			By("verifying the synced Secret carries the cross-account payload")
			assertCrossAccountSecretContent(es, "crossacct-ak-basic-secret")
		})
	})

	// WorkerRole + cross-account sync
	// This test patches the ack-secret-manager Deployment to add ALICLOUD_REMOTE_ROLE_ARN
	// and ALICLOUD_REMOTE_ROLE_SESSION_NAME env vars, enabling cross-account access via
	// the WorkerRole auth chain (priority 5, env-based fallback).
	// After the test, the original Deployment spec is restored.
	Context("WorkerRole (env-based) + cross-account sync", func() {
		It("should sync cross-account secret using WorkerRole + remote role assumption", func() {
			if !workerRoleEnabledInDeployment(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName) {
				Skip("WorkerRole authentication is disabled on the ack-secret-manager Deployment (--enable-worker-role=false); the WorkerRole auth provider is not part of the auth chain, so this test cannot validate it")
			}

			// The globally injected ENV RRSA vars are temporarily removed below so
			// WorkerRole (priority 5) is genuinely exercised; the baseline is
			// restored afterwards via DeferCleanup.
			By("getting the current ack-secret-manager Deployment")
			deployment, err := clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(
				ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment")

			// Snapshot the FULL baseline (including the RRSA env vars injected by
			// BeforeSuite) BEFORE removing them: after the test, restoreDeploymentEnv
			// reinstates the complete baseline in a single rollout, and
			// restoreRRSAEnvBaseline degrades to an idempotent no-op (needsUpdate=false).
			originalEnv := make([]corev1.EnvVar, len(deployment.Spec.Template.Spec.Containers[0].Env))
			copy(originalEnv, deployment.Spec.Template.Spec.Containers[0].Env)

			// Register the restore BEFORE the mutation: removeRRSAEnvTemporarily may
			// hard-fail mid-way (e.g. rollout timeout after a successful Update), and a
			// DeferCleanup registered after it would never run, permanently stripping
			// the RRSA env baseline. restoreRRSAEnvBaseline is idempotent (no-op when
			// the baseline is unchanged), so early registration is safe.
			DeferCleanup(func() {
				By("restoring RRSA env baseline on the Deployment")
				restoreRRSAEnvBaseline(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)
			})
			By("temporarily removing RRSA env vars so the auth chain falls through to WorkerRole")
			removeRRSAEnvTemporarily(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)

			// Re-fetch the Deployment: the env removal above changed the pod template.
			deployment, err = clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(
				ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment after RRSA env removal")

			// Check if ALICLOUD_REMOTE_ROLE_ARN is already set (from Helm values)
			var hasRemoteRoleARN bool
			for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
				if env.Name == "ALICLOUD_REMOTE_ROLE_ARN" && env.Value != "" {
					hasRemoteRoleARN = true
					break
				}
			}

			if !hasRemoteRoleARN {
				By("patching Deployment to add ALICLOUD_REMOTE_ROLE_ARN and ALICLOUD_REMOTE_ROLE_SESSION_NAME")

				// Register cleanup BEFORE the mutation: a hard failure during Update or
				// rollout must not leave the patched env vars without a restore.
				// restoreDeploymentEnv reinstates the full baseline and is safe even
				// if the Update below never happened.
				DeferCleanup(func() {
					By("restoring original Deployment env vars")
					restoreDeploymentEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName, originalEnv)
				})

				// Add remote role env vars to the container and roll out
				updateDeploymentAndRollout(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName, func(dep *appsv1.Deployment) {
					dep.Spec.Template.Spec.Containers[0].Env = append(dep.Spec.Template.Spec.Containers[0].Env,
						corev1.EnvVar{Name: "ALICLOUD_REMOTE_ROLE_ARN", Value: RAMRoleArnForCrossAccount},
						corev1.EnvVar{Name: "ALICLOUD_REMOTE_ROLE_SESSION_NAME", Value: "cross-account-e2e-test"},
					)
				})
			} else {
				By("ALICLOUD_REMOTE_ROLE_ARN already configured in Deployment, skipping patch")
			}

			By("creating ExternalSecret without SecretStoreRef (WorkerRole fallback + cross-account)")
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "wr-crossacct-secret-" + getRandString(),
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  CrossAccountKMSSecretName,
							Name: "crossacct-wr-secret",
						},
					},
					// No SecretStoreRef → WorkerRole auth chain (priority 5)
					// Combined with ALICLOUD_REMOTE_ROLE_ARN env var → cross-account AssumeRole
				},
			}
			err = k8sClient.Create(ctx, es)
			Expect(err).NotTo(HaveOccurred(), "failed to create ExternalSecret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via WorkerRole + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)
		})
	})
})

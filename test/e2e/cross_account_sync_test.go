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
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

const (
	// ackSecretManagerDeploymentName is the name of the ack-secret-manager Deployment.
	ackSecretManagerDeploymentName = "ack-secret-manager"
	// ackSecretManagerNamespace is the namespace where ack-secret-manager is deployed.
	ackSecretManagerNamespace = "kube-system"
)

var _ = Describe("Cross-account Sync E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-crossacct-"+getRandString())
	})

	AfterEach(func() {
		deleteTestNamespace(ctx, testNamespace)
	})

	// validateClusterSecretStoreReady is a helper to wait for ClusterSecretStore to become ready
	validateClusterSecretStoreReady := func(store *api.ClusterSecretStore) {
		var lastStoreError string
		Eventually(func() bool {
			createdStore := &api.ClusterSecretStore{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name: store.Name,
			}, createdStore)
			if err != nil {
				lastStoreError = fmt.Sprintf("Error getting ClusterSecretStore: %v", err)
				return false
			}
			if len(createdStore.Status.Conditions) == 0 {
				lastStoreError = "ClusterSecretStore has no status conditions"
				return false
			}
			for _, condition := range createdStore.Status.Conditions {
				if condition.Type != api.SecretStoreReady || condition.Status != corev1.ConditionTrue {
					lastStoreError = fmt.Sprintf("SecretStoreReady condition type is %s, expected Ready, status is %s, expected True, reason: %s, message: %s", condition.Type, string(condition.Status), condition.Reason, condition.Message)
					return false
				}
			}
			return true
		}).WithTimeout(time.Minute*2).WithPolling(time.Second*5).Should(BeTrue(),
			func() string {
				if lastStoreError != "" {
					return fmt.Sprintf("ClusterSecretStore should eventually become ready, but: %s", lastStoreError)
				}
				return "ClusterSecretStore should eventually become ready"
			})
	}

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

			validateClusterSecretStoreReady(clusterSecretStore)

			By("creating ExternalSecret targeting cross-account KMS secret")
			es := createCrossAccountExternalSecret(testNamespace.Name, clusterSecretStore.Name, ResourceClusterSecretStore, "crossacct-sa-secret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via SA RRSA + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)
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

			validateClusterSecretStoreReady(clusterSecretStore)

			By("creating ExternalSecret targeting cross-account KMS secret")
			es := createCrossAccountExternalSecret(testNamespace.Name, clusterSecretStore.Name, ResourceClusterSecretStore, "crossacct-oidc-secret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via OIDC + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)
		})
	})

	// Context 3: AK AssumeRole + cross-account
	// Auth chain: AK → AssumeRole(RAMRoleARN) → temp creds → AssumeRole(RemoteRAMRoleARN) → final creds
	Context("AK AssumeRole + cross-account sync", func() {
		It("Should sync secret using AK AssumeRole auth with remoteRamRoleARN", func() {
			By("creating Secret with AK/SK for role play")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "crossacct-ak-role-secret-" + getRandString(),
					Namespace: testNamespace.Name,
				},
				Data: map[string][]byte{
					"accessKeyId":     []byte(RAMUserAccessKeyIDForRolePlay),
					"accessKeySecret": []byte(RAMUserAccessKeySecretForRolePlay),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

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

			validateClusterSecretStoreReady(clusterSecretStore)

			By("creating ExternalSecret targeting cross-account KMS secret")
			es := createCrossAccountExternalSecret(testNamespace.Name, clusterSecretStore.Name, ResourceClusterSecretStore, "crossacct-ak-role-secret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via AK AssumeRole + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)
		})
	})

	// Context 4: AK basic (no AssumeRole) + cross-account
	// Auth chain: AK → direct KMS access (local account) → AssumeRole(RemoteRAMRoleARN) → final creds
	// Note: AK basic + cross-account requires the AK user has sts:AssumeRole permission for the remote role
	Context("AK basic + cross-account sync", func() {
		It("Should sync secret using AK basic auth with remoteRamRoleARN", func() {
			By("creating Secret with AK/SK for basic auth user")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "crossacct-ak-basic-secret-" + getRandString(),
					Namespace: testNamespace.Name,
				},
				Data: map[string][]byte{
					"accessKeyId":     []byte(RAMUserAccessKeyID),
					"accessKeySecret": []byte(RAMUserAccessKeySecret),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

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

			validateClusterSecretStoreReady(clusterSecretStore)

			By("creating ExternalSecret targeting cross-account KMS secret")
			es := createCrossAccountExternalSecret(testNamespace.Name, clusterSecretStore.Name, ResourceClusterSecretStore, "crossacct-ak-basic-secret")
			DeferCleanup(func() {
				CleanupExternalSecretAndSyncedSecret(ctx, es)
			})

			By("waiting for the secret to be synced via AK basic + cross-account")
			validateExternalSecretSucceededAndSecretCreated(ctx, es.Namespace, es.Name, time.Second*60)
		})
	})

	// WorkerRole + cross-account sync
	// This test patches the ack-secret-manager Deployment to add ALICLOUD_REMOTE_ROLE_ARN
	// and ALICLOUD_REMOTE_ROLE_SESSION_NAME env vars, enabling cross-account access via
	// the WorkerRole auth chain (priority 5, env-based fallback).
	// After the test, the original Deployment spec is restored.
	Context("WorkerRole (env-based) + cross-account sync", func() {
		It("should sync cross-account secret using WorkerRole + remote role assumption", func() {
			ctx := context.TODO()

			if CrossAccountKMSSecretName == "" || RAMRoleArnForCrossAccount == "" {
				Skip("cross-account resources not configured (CROSS_ACCOUNT_KMS_SECRET_NAME, RAM_ROLE_ARN_FOR_CROSS_ACCOUNT)")
			}

			By("getting the current ack-secret-manager Deployment")
			deployment, err := clientset.AppsV1().Deployments(ackSecretManagerNamespace).Get(
				ctx, ackSecretManagerDeploymentName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "failed to get ack-secret-manager Deployment")

			// Save original env vars for restoration after test
			originalEnv := make([]corev1.EnvVar, len(deployment.Spec.Template.Spec.Containers[0].Env))
			copy(originalEnv, deployment.Spec.Template.Spec.Containers[0].Env)

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
				// Add remote role env vars to the container
				newEnv := append(deployment.Spec.Template.Spec.Containers[0].Env,
					corev1.EnvVar{Name: "ALICLOUD_REMOTE_ROLE_ARN", Value: RAMRoleArnForCrossAccount},
					corev1.EnvVar{Name: "ALICLOUD_REMOTE_ROLE_SESSION_NAME", Value: "cross-account-e2e-test"},
				)
				deployment.Spec.Template.Spec.Containers[0].Env = newEnv

				// Add restart annotation to force rolling restart
				if deployment.Spec.Template.Annotations == nil {
					deployment.Spec.Template.Annotations = make(map[string]string)
				}
				deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

				_, err = clientset.AppsV1().Deployments(ackSecretManagerNamespace).Update(
					ctx, deployment, metav1.UpdateOptions{})
				Expect(err).NotTo(HaveOccurred(), "failed to update Deployment with remote role env vars")

				By("waiting for Deployment rollout to complete")
				waitForDeploymentRollout(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName)

				// Register cleanup to restore original Deployment
				DeferCleanup(func() {
					By("restoring original Deployment env vars")
					restoreDeploymentEnv(ctx, ackSecretManagerNamespace, ackSecretManagerDeploymentName, originalEnv)
				})
			} else {
				By("ALICLOUD_REMOTE_ROLE_ARN already configured in Deployment, skipping patch")
			}

			By("creating ExternalSecret without SecretStoreRef (WorkerRole fallback + cross-account)")
			es := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("wr-crossacct-secret-%s", strings.ToLower(ResourceClusterSecretStore)),
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
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

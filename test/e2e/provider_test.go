// test/e2e/provider_test.go - Test ExternalSecret providers (OOS)
// Note: the KMS provider sync path is covered by data_fetch_test.go
// ("Should fetch normal data"), so no dedicated KMS spec is kept here.
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
			// Create SecretStore for OOS
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "oos-provider-store",
					Namespace: testNamespace.Name,
				},
				Spec: api.SecretStoreSpec{
					OOS: &api.OOSProvider{
						OOS: &api.OOSAuth{
							RAMRoleARN:      RAMRoleArnForRRSA,
							OIDCProviderARN: OIDCProviderARN,
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, secretStore)).To(Succeed())

			// Wait for SecretStore to be ready
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Create ExternalSecret using OOS provider
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

			// Validate ExternalSecret syncs successfully
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// Validate the synced Secret content matches the preset OOS
			// encrypted parameter value created by ResourceManager, proving the
			// OOS provider fetched the actual source data (not just any bytes).
			syncedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: externalSecret.Namespace,
				Name:      externalSecret.Name,
			}, syncedSecret)).To(Succeed())
			Expect(string(syncedSecret.Data["oos-secret-key"])).To(Equal(CommonOOSSecretParameterValue),
				"synced Secret content should match the preset OOS parameter value")

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})
})

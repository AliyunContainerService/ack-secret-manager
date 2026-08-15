// data_fetch_test.go - Data Fetch E2E tests
package e2e

import (
	"context"
	"encoding/json"
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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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

			// Validate the synced Secret content matches the source KMS
			// credential value preset by ResourceManager, proving the fetched
			// data is the actual secret (not just any non-empty bytes).
			syncedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: externalSecret.Namespace,
				Name:      externalSecret.Name,
			}, syncedSecret)).To(Succeed())
			Expect(string(syncedSecret.Data["normal-secret-key"])).To(Equal(CommonKMSSecretValue),
				"synced Secret content should match the source KMS credential value")

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("Name fallback to key", func() {
		It("Should use data key as Secret data key when name is omitted", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-name-fallback-store",
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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Create ExternalSecret whose data entry sets only Key and omits
			// Name, exercising the documented fallback: the target Secret data
			// key must equal the Key value instead of an empty key (which the
			// API server would reject, making the whole sync fail).
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-name-fallback-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       CommonKMSSecretName,
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

			// Validate ExternalSecret status update and created Kubernetes Secret
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			// The synced Secret must carry the data under the Key value and
			// must never contain an empty key.
			syncedSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Namespace: externalSecret.Namespace,
				Name:      externalSecret.Name,
			}, syncedSecret)).To(Succeed())
			Expect(syncedSecret.Data).To(HaveKey(CommonKMSSecretName),
				"synced Secret should contain a data key named after the data entry key when name is omitted")
			Expect(string(syncedSecret.Data[CommonKMSSecretName])).To(Equal(CommonKMSSecretValue),
				"synced Secret content should match the source KMS credential value")
			Expect(syncedSecret.Data).NotTo(HaveKey(""),
				"synced Secret must not contain an empty data key")

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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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
			// (JsonKMSSecretName preset: name=xiaoming, friends[0].name=xiaohong)
			validateParsedSecretContent(ctx, externalSecret, map[string]string{
				"myname":     "xiaoming",
				"friendname": "xiaohong",
			})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	// Contract: GetJsonSecrets (pkg/utils/util.go) must serialize complex
	// (map/slice) jmesPath results as compact JSON strings. Assertions use
	// json.Valid + structural equality instead of exact whitespace matching so
	// the test stays robust against formatting details.
	Context("JSON complex value via JMESPath", func() {
		It("Should serialize map and slice jmesPath results as compact JSON", func() {
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-json-complex-store",
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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

			// Extract complex values from JsonKMSSecretName
			// ({"name":"xiaoming","age":10,"friends":[...]}): "friends" yields a
			// slice and "@" yields the whole document map; both must land in the
			// target Secret as compact JSON strings.
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-json-complex-externalsecret",
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:       JsonKMSSecretName,
							Name:      "json-complex-secret-key",
							VersionId: "v1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      secretStore.Name,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
							JMESPath: []api.JMESPathObject{
								{
									Path:        "friends",
									ObjectAlias: "friends-json",
								},
								{
									Path:        "@",
									ObjectAlias: "whole-doc-json",
								},
							},
						},
					},
				},
			}

			Expect(k8sClient.Create(ctx, externalSecret)).To(Succeed())

			// Validate ExternalSecret status update and created Kubernetes Secret
			validateExternalSecretSucceededAndSecretCreated(ctx, externalSecret.Namespace, externalSecret.Name, time.Second*30)

			syncedSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Namespace: externalSecret.Namespace,
					Name:      externalSecret.Name,
				}, syncedSecret)
			}).WithTimeout(time.Second*30).WithPolling(time.Second*5).Should(Succeed(),
				"synced Secret should exist for complex-value validation")

			// The slice result must be valid JSON and structurally equal to the
			// preset friends array of JsonKMSSecretName.
			friendsRaw, ok := syncedSecret.Data["friends-json"]
			Expect(ok).To(BeTrue(), "synced Secret should contain the 'friends-json' key")
			Expect(json.Valid(friendsRaw)).To(BeTrue(),
				"slice jmesPath result should be serialized as valid JSON")
			var friends []map[string]interface{}
			Expect(json.Unmarshal(friendsRaw, &friends)).To(Succeed(),
				"slice jmesPath result should deserialize into a JSON array")
			Expect(friends).To(Equal([]map[string]interface{}{
				{"name": "xiaohong", "age": float64(11)},
				{"name": "xiaoli", "age": float64(12)},
			}), "slice jmesPath result should match the preset friends array")

			// The whole-document map result must also be valid compact JSON and
			// structurally equal to the preset document.
			docRaw, ok := syncedSecret.Data["whole-doc-json"]
			Expect(ok).To(BeTrue(), "synced Secret should contain the 'whole-doc-json' key")
			Expect(json.Valid(docRaw)).To(BeTrue(),
				"map jmesPath result should be serialized as valid JSON")
			var doc map[string]interface{}
			Expect(json.Unmarshal(docRaw, &doc)).To(Succeed(),
				"map jmesPath result should deserialize into a JSON object")
			Expect(doc).To(Equal(map[string]interface{}{
				"name": "xiaoming",
				"age":  float64(10),
				"friends": []interface{}{
					map[string]interface{}{"name": "xiaohong", "age": float64(11)},
					map[string]interface{}{"name": "xiaoli", "age": float64(12)},
				},
			}), "map jmesPath result should match the preset JSON document")

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("JSON key rename via ReplaceKey", func() {
		It("Should rename JSON keys via DataProcess ReplaceKey regex rules", func() {
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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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

			// Verify the ReplaceKey regex rules renamed the parsed JSON top-level
			// keys (name -> namekey, age -> agekey) while preserving the values
			// (JsonKMSSecretName preset: name=xiaoming, age=10).
			validateParsedSecretContent(ctx, externalSecret, map[string]string{
				"namekey": "xiaoming",
				"agekey":  "10",
			})

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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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
			// (YamlKMSSecretName preset: name=xiaoming, friends[0].name=xiaohong)
			validateParsedSecretContent(ctx, externalSecret, map[string]string{
				"myname":     "xiaoming",
				"friendname": "xiaohong",
			})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})

	Context("YAML key rename via ReplaceKey", func() {
		It("Should rename YAML keys via DataProcess ReplaceKey regex rules", func() {
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
			waitForSecretStoreReady(ctx, secretStore.Namespace, secretStore.Name)

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

			// Verify the ReplaceKey regex rules renamed the parsed YAML top-level
			// keys (name -> namekey, age -> agekey). YAML values are re-serialized
			// with yaml.v3, so the integer age=10 carries a trailing newline.
			validateParsedSecretContent(ctx, externalSecret, map[string]string{
				"namekey": "xiaoming",
				"agekey":  "10\n",
			})

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			CleanupExternalSecret(ctx, externalSecret)
		})
	})
})

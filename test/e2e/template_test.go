// Copyright © 2025 Alibaba Cloud. All rights reserved.

package e2e

import (
	"context"
	"encoding/json"
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

var _ = Describe("Template Processing E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	// Helper function to create string pointer
	stringPtr := func(s string) *string {
		return &s
	}

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-template-"+getRandString())
	})

	AfterEach(func() {
		// Delete the test namespace
		deleteTestNamespace(ctx, testNamespace)
	})

	// createTemplateStore creates a standard KMS/RRSA SecretStore and registers
	// cleanup via DeferCleanup. Returns the store so callers can reference its
	// Name in ExternalSecret specs.
	createTemplateStore := func(namespace string) *api.SecretStore {
		storeName := "template-store-" + getRandString()
		store := &api.SecretStore{
			ObjectMeta: metav1.ObjectMeta{
				Name:      storeName,
				Namespace: namespace,
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
		Expect(k8sClient.Create(ctx, store)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, store)
		})
		waitForSecretStoreReady(ctx, namespace, storeName)
		return store
	}

	// syncTemplateExternalSecret creates the ExternalSecret, registers cleanup
	// via DeferCleanup, waits for ES status Succeeded, then reads and returns
	// the synced Secret. The Secret name is derived from Target.Name (if set)
	// or falls back to the ES name.
	syncTemplateExternalSecret := func(es *api.ExternalSecret) *corev1.Secret {
		Expect(k8sClient.Create(ctx, es)).To(Succeed())
		DeferCleanup(func() {
			CleanupExternalSecretAndSyncedSecret(ctx, es)
		})

		// Wait for status Succeeded before reading the Secret
		waitForExternalSecretSucceeded(ctx, es.Namespace, es.Name, 60*time.Second)

		// Determine the Secret name
		secretName := es.Name
		if es.Spec.Target != nil && es.Spec.Target.Name != "" {
			secretName = es.Spec.Target.Name
		}
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: secretName, Namespace: es.Namespace,
		}, secret)).To(Succeed())
		return secret
	}

	Describe("Basic Template Processing", func() {
		It("Should process template with no template specified (raw data)", func() {
			store := createTemplateStore(testNamespace.Name)

			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "basic-externalsecret-" + getRandString(),
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "key1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: "basic-secret-" + getRandString(),
					},
				},
			}
			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data: Data["key1"] holds the full raw secret content
			Expect(secret.Data).To(HaveKey("key1"))
			Expect(string(secret.Data["key1"])).To(ContainSubstring("value1"))
		})

		It("Should process simple Go template with key-value data", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with simple Go template using key-value data
			esName := "simple-go-template-externalsecret-" + getRandString()
			secretTargetName := "simple-go-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Test 1: Direct field access
								"appName": `{{ .appName }}`,

								// Test 2: Conditional statement
								"enabled": `{{ if .enabled }}yes{{ else }}no{{ end }}`,

								// Test 3: Sprig function - upper case
								"environment": `{{ .environment | upper }}`,

								// Test 4: Multiple fields concatenation
								"info": `App: {{ .appName }}, Env: {{ .environment }}`,

								// Test 5: Range over all keys
								"all-keys": `{{ range $key, $value := . }}{{$key}},{{ end }}`,

								// Test 6: With statement
								"replicas": `{{ with .replicas }}Replicas: {{ . }}{{ end }}`,
							},
						},
					},
					DataProcess: []api.DataProcess{
						{
							Extract: &api.DataSource{
								Key:  GoTemplateSecretName,
								Name: "data",
								SecretStoreRef: &api.SecretStoreRef{
									Name:      store.Name,
									Namespace: store.Namespace,
									Kind:      ResourceSecretStore,
								},
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data
			// Test 1: Direct field access
			Expect(secret.Data).To(HaveKey("appName"))
			Expect(string(secret.Data["appName"])).To(Equal("myapp"))

			// Test 2: Conditional statement
			Expect(secret.Data).To(HaveKey("enabled"))
			Expect(string(secret.Data["enabled"])).To(Equal("yes"))

			// Test 3: Sprig function - upper case
			Expect(secret.Data).To(HaveKey("environment"))
			Expect(string(secret.Data["environment"])).To(Equal("PRODUCTION"))

			// Test 4: Multiple fields concatenation
			Expect(secret.Data).To(HaveKey("info"))
			userInfo := string(secret.Data["info"])
			Expect(userInfo).To(ContainSubstring("App: myapp"))
			Expect(userInfo).To(ContainSubstring("Env: production"))

			// Test 5: Range over all keys
			Expect(secret.Data).To(HaveKey("all-keys"))
			allKeys := string(secret.Data["all-keys"])
			Expect(allKeys).To(ContainSubstring("appName,"))
			Expect(allKeys).To(ContainSubstring("database,"))
			Expect(allKeys).To(ContainSubstring("enabled,"))
			Expect(allKeys).To(ContainSubstring("environment,"))
			Expect(allKeys).To(ContainSubstring("features,"))
			Expect(allKeys).To(ContainSubstring("replicas,"))
			Expect(allKeys).To(ContainSubstring("status,"))

			// Test 6: With statement
			Expect(secret.Data).To(HaveKey("replicas"))
			deptInfo := string(secret.Data["replicas"])
			Expect(deptInfo).To(ContainSubstring("Replicas: 3"))

		})

		It("Should process Go template with structured JSON data", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create a simple ExternalSecret with template that tests various Go template syntaxes
			// Note: GoTemplateSecretName contains a single JSON object, accessed via .data field
			esName := "json-template-externalsecret-" + getRandString()
			secretTargetName := "json-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Test 1: Access the raw data string
								"raw-data": `{{ .data }}`,

								// Test 2: Parse JSON and access top-level field
								"app-name": `{{ $d := .data | fromJson }}{{ $d.appName }}`,

								// Test 3: Conditional with parsed JSON field
								"is-enabled": `{{ $d := .data | fromJson }}{{ if eq $d.status "enabled" }}true{{ else }}false{{ end }}`,

								// Test 4: Access nested database.host field
								"db-host": `{{ $d := .data | fromJson }}{{ $d.database.host }}`,

								// Test 5: Full parsed JSON object
								"parsed-data": `{{ .data | fromJson | toJson }}`,

								// Test 6: Sprig function pipeline with JSON parsing
								"db-name-upper": `{{ $d := .data | fromJson }}{{ $d.database.name | upper }}`,

								// Test 7: Conditional with boolean field
								"enabled-status": `{{ $d := .data | fromJson }}{{ if $d.enabled }}yes{{ else }}no{{ end }}`,

								// Test 8: Multiple conditions and field access
								"environment-info": `{{ $d := .data | fromJson }}{{ if eq $d.status "enabled" }}Active{{ else }}Inactive{{ end }}-{{ $d.environment }}`,

								// Test 9: Numeric field
								"replica-count": `{{ $d := .data | fromJson }}{{ $d.replicas }}`,

								// Test 10: Array access with index
								"first-feature": `{{ $d := .data | fromJson }}{{ index $d.features 0 }}`,
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  GoTemplateSecretName,
							Name: "data", // The entire JSON object is stored in the "data" key
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data
			// Test 1: Raw data
			Expect(secret.Data).To(HaveKey("raw-data"))
			Expect(string(secret.Data["raw-data"])).To(ContainSubstring("myapp"))

			// Test 2: App name from parsed JSON
			Expect(secret.Data).To(HaveKey("app-name"))
			Expect(string(secret.Data["app-name"])).To(Equal("myapp"))

			// Test 3: Conditional statement
			Expect(secret.Data).To(HaveKey("is-enabled"))
			Expect(string(secret.Data["is-enabled"])).To(Equal("true"))

			// Test 4: Nested field access
			Expect(secret.Data).To(HaveKey("db-host"))
			Expect(string(secret.Data["db-host"])).To(Equal("db.example.com"))

			// Test 5: Parsed data
			Expect(secret.Data).To(HaveKey("parsed-data"))
			Expect(string(secret.Data["parsed-data"])).To(ContainSubstring("myapp"))

			// Test 6: Sprig function pipeline
			Expect(secret.Data).To(HaveKey("db-name-upper"))
			Expect(string(secret.Data["db-name-upper"])).To(Equal("MYDATABASE"))

			// Test 7: Boolean conditional
			Expect(secret.Data).To(HaveKey("enabled-status"))
			Expect(string(secret.Data["enabled-status"])).To(Equal("yes"))

			// Test 8: Multiple conditions
			Expect(secret.Data).To(HaveKey("environment-info"))
			Expect(string(secret.Data["environment-info"])).To(ContainSubstring("Active"))
			Expect(string(secret.Data["environment-info"])).To(ContainSubstring("production"))

			// Test 9: Numeric field
			Expect(secret.Data).To(HaveKey("replica-count"))
			Expect(string(secret.Data["replica-count"])).To(Equal("3"))

			// Test 10: Array access
			Expect(secret.Data).To(HaveKey("first-feature"))
			Expect(string(secret.Data["first-feature"])).To(Equal("auth"))

		})

		It("Should process Go template with range and with statements", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with range and with templates
			// Using GoTemplateSecretName which contains structured JSON data in the "data" field
			esName := "range-with-template-externalsecret-" + getRandString()
			secretTargetName := "range-with-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Test 1: Range over features array (parsed from JSON)
								"features-list": `{{ $d := .data | fromJson }}{{ range $i, $feature := $d.features }}{{$feature}}{{ if lt $i (sub (len $d.features) 1) }},{{ end }}{{ end }}`,

								// Test 2: With statement for context switching on nested object
								"app-info": `{{ $d := .data | fromJson }}{{ with $d.database }}Host: {{ .host }}, Port: {{ .port }}, DB: {{ .name }}{{ end }}`,

								// Test 3: With statement with conditional
								"db-status": `{{ $d := .data | fromJson }}{{ with $d.database }}{{ if .port }}Online{{ else }}Offline{{ end }}{{ end }}`,

								// Test 4: Range with index
								"indexed-features": `{{ $d := .data | fromJson }}{{ range $i, $feature := $d.features }}[{{$i}}]{{$feature}};{{ end }}`,

								// Test 5: Complex nested access
								"config-summary": `{{ $d := .data | fromJson }}App: {{ $d.appName }}, Env: {{ $d.environment }}, Replicas: {{ $d.replicas }}, Status: {{ if eq $d.status "enabled" }}Active{{ else }}Inactive{{ end }}`,

								// Test 6: Range over object keys
								"all-keys": `{{ $d := .data | fromJson }}{{ range $key, $value := $d }}{{$key}},{{ end }}`,
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  GoTemplateSecretName,
							Name: "data",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data
			// Test 1: Features list
			Expect(secret.Data).To(HaveKey("features-list"))
			featuresList := string(secret.Data["features-list"])
			Expect(featuresList).To(ContainSubstring("auth"))
			Expect(featuresList).To(ContainSubstring("logging"))
			Expect(featuresList).To(ContainSubstring("monitoring"))

			// Test 2: App info with with statement
			Expect(secret.Data).To(HaveKey("app-info"))
			appInfo := string(secret.Data["app-info"])
			Expect(appInfo).To(ContainSubstring("Host: db.example.com"))
			Expect(appInfo).To(ContainSubstring("Port: 5432"))
			Expect(appInfo).To(ContainSubstring("DB: mydatabase"))

			// Test 3: DB status conditional
			Expect(secret.Data).To(HaveKey("db-status"))
			Expect(string(secret.Data["db-status"])).To(Equal("Online"))

			// Test 4: Indexed features
			Expect(secret.Data).To(HaveKey("indexed-features"))
			indexedFeatures := string(secret.Data["indexed-features"])
			Expect(indexedFeatures).To(ContainSubstring("[0]auth"))
			Expect(indexedFeatures).To(ContainSubstring("[1]logging"))
			Expect(indexedFeatures).To(ContainSubstring("[2]monitoring"))

			// Test 5: Config summary
			Expect(secret.Data).To(HaveKey("config-summary"))
			configSummary := string(secret.Data["config-summary"])
			Expect(configSummary).To(ContainSubstring("App: myapp"))
			Expect(configSummary).To(ContainSubstring("Env: production"))
			Expect(configSummary).To(ContainSubstring("Replicas: 3"))
			Expect(configSummary).To(ContainSubstring("Status: Active"))

			// Test 6: All keys
			Expect(secret.Data).To(HaveKey("all-keys"))
			allKeys := string(secret.Data["all-keys"])
			Expect(allKeys).To(ContainSubstring("appName,"))
			Expect(allKeys).To(ContainSubstring("environment,"))
			Expect(allKeys).To(ContainSubstring("database,"))

		})

		It("Should apply enabled Sprig function and preserve original value", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create an ExternalSecret verifying the enabled Sprig upper function and the
			// pass-through behavior for values where the disabled reverse function is not applied
			esName := "sprig-template-externalsecret-" + getRandString()
			secretTargetName := "sprig-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"uppercase-name": `{{ (parseKeyValue .name).name | upper }}`,
								// No reverse function is applied here (reverse is disabled): this template
								// verifies the original value passes through unchanged
								"passthrough-name": `{{ (parseKeyValue .name).name }}`,
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "name",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			secret := syncTemplateExternalSecret(externalSecret)

			// SimpleTemplateSecretName contains name=test-app: the enabled Sprig upper
			// function turns it into TEST-APP, while the pass-through template keeps
			// the original value because the disabled reverse function is not applied
			Expect(string(secret.Data["uppercase-name"])).To(Equal("TEST-APP"))
			Expect(string(secret.Data["passthrough-name"])).To(Equal("test-app"))
		})
	})

	Describe("TemplateFrom Processing", func() {
		It("Should process template from ConfigMap", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create a ConfigMap with template content
			// TemplateScopeKeysAndValues parses the template OUTPUT (not input)
			// The template uses parseKeyValue to parse the INPUT data from KMS (which is key=value format)
			// Then outputs: DB_KEY1=value1\nDB_KEY2=value2
			// Finally TemplateScopeKeysAndValues parses this into separate secret keys
			cmName := "template-configmap-" + getRandString()
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: testNamespace.Name,
				},
				Data: map[string]string{
					// Template parses input .data (key=value format) and outputs key=value pairs
					// TemplateScopeKeysAndValues then parses the output into individual keys
					"db-config": `DB_KEY1={{ (parseKeyValue .data).key1 }}
DB_KEY2={{ (parseKeyValue .data).key2 }}`,
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			// Create an ExternalSecret that uses the ConfigMap template
			esName := "cm-template-externalsecret-" + getRandString()
			secretTargetName := "cm-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									ConfigMap: &api.TemplateRef{
										Name: cmName,
										Items: []api.TemplateRefItem{
											{
												Key:        "db-config",
												TemplateAs: api.TemplateScopeKeysAndValues,
											},
										},
									},
								},
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "data",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data
			Expect(secret.Data).To(HaveKey("DB_KEY1"))
			// SimpleTemplateSecretName contains key1=value1, should be processed
			Expect(string(secret.Data["DB_KEY1"])).To(Equal("value1"))
			Expect(secret.Data).To(HaveKey("DB_KEY2"))
			// SimpleTemplateSecretName contains key2=value2, should be processed
			Expect(string(secret.Data["DB_KEY2"])).To(Equal("value2"))

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
		})

		It("Should process template from Secret", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create a Secret with template content
			templateSecretName := "template-secret-" + getRandString()
			templateSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      templateSecretName,
					Namespace: testNamespace.Name,
				},
				Data: map[string][]byte{
					"api-config": []byte(`API_KEY={{ (parseKeyValue .data).key1 }}
CLIENT_SECRET={{ (parseKeyValue .data).status }}`),
				},
			}
			Expect(k8sClient.Create(ctx, templateSecret)).To(Succeed())

			// Create an ExternalSecret that uses the Secret template
			esName := "secret-template-externalsecret-" + getRandString()
			secretTargetName := "secret-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									Secret: &api.TemplateRef{
										Name: templateSecretName,
										Items: []api.TemplateRefItem{
											{
												Key:        "api-config",
												TemplateAs: api.TemplateScopeKeysAndValues,
											},
										},
									},
								},
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "data",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data
			Expect(secret.Data).To(HaveKey("API_KEY"))
			// SimpleTemplateSecretName contains key1=value1, should be processed
			Expect(string(secret.Data["API_KEY"])).To(Equal("value1"))
			Expect(secret.Data).To(HaveKey("CLIENT_SECRET"))
			// SimpleTemplateSecretName contains status=enabled, should be processed
			Expect(string(secret.Data["CLIENT_SECRET"])).To(Equal("enabled"))

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace
			Expect(k8sClient.Delete(ctx, templateSecret)).To(Succeed())

		})
	})

	Describe("Template Metadata Processing", func() {
		It("Should process template metadata", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create an ExternalSecret with template metadata
			esName := "metadata-template-externalsecret-" + getRandString()
			secretTargetName := "metadata-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Metadata: &api.ExternalSecretTemplateMetadata{
								Labels: map[string]string{
									"app":         `{{ (.app_name | fromJson).appName }}`,
									"environment": `{{ (.env | fromJson).environment }}`,
								},
								Annotations: map[string]string{
									"description": `Application configuration for {{ (.app_name | fromJson).appName }}`,
									"version":     `{{ (.version | fromJson).version }}`,
								},
							},
							Data: map[string]string{
								"app-config": `{{ (.config | fromJson).config }}`,
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  GoTemplateSecretName,
							Name: "app_name",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  GoTemplateSecretName,
							Name: "env",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  GoTemplateSecretName,
							Name: "version",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  GoTemplateSecretName,
							Name: "config",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret metadata holds the exact values rendered from GoTemplateSecretName
			Expect(secret.Labels["app"]).To(Equal("myapp"))
			Expect(secret.Labels["environment"]).To(Equal("production"))
			Expect(secret.Annotations["description"]).To(ContainSubstring("myapp"))
			Expect(secret.Annotations["version"]).To(Equal("v1.2.3"))
			Expect(secret.Data).To(HaveKey("app-config"))

		})
	})

	Describe("Edge Cases and Error Handling", func() {
		It("Should handle empty template gracefully", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create an ExternalSecret with empty template
			esName := "empty-template-externalsecret-" + getRandString()
			secretTargetName := "empty-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{}, // Empty template data
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName, // Using the correct field and value from resource manager
							Name: "key1",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// With empty template data, the raw data should be preserved
			// Empty Template.Data{} means "don't use template processing", not "clear all data"
			Expect(secret).ToNot(BeNil())
			// The secret should contain the data from the data source
			Expect(secret.Data).To(HaveKey("key1"))
			Expect(secret.Data["key1"]).ToNot(BeEmpty())

		})

		It("Should handle template syntax errors appropriately", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create an ExternalSecret with invalid template syntax
			esName := "syntax-error-externalsecret-" + getRandString()
			secretTargetName := "syntax-error-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"bad-template": `{{ (parseKeyValue .key) | invalidFunction }}`, // This will cause a template error
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName, // Using the correct field and value from resource manager
							Name: "key",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
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

			// Wait and check that the ExternalSecret eventually reports a failure whose reason
			// proves it came from the template parse-error path. Fatal parse errors are recorded
			// as a Failed DataSyncResult with key "template_processing_fatal" and a Reason
			// carrying the parse failure (see ProcessAllTemplates/executeTemplate in template_processor.go).
			Eventually(func() bool {
				updatedES := &api.ExternalSecret{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: esName, Namespace: testNamespace.Name}, updatedES)
				if err != nil {
					return false
				}

				// The template references the undefined function invalidFunction, so execution
				// must fail during parsing; pin the failure to that exact error path.
				for _, result := range updatedES.Status.DataSyncResults {
					if result.Status == "Failed" &&
						result.ExternalSecretKey == "template_processing_fatal" &&
						strings.Contains(result.Reason, "parse failed") &&
						strings.Contains(result.Reason, "invalidFunction") {
						return true
					}
				}
				return false
			}).WithTimeout(60 * time.Second).WithPolling(5 * time.Second).Should(BeTrue())

		})

		It("Should handle missing data keys gracefully", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create an ExternalSecret that references a non-existent data key in the template
			esName := "missing-key-externalsecret-" + getRandString()
			secretTargetName := "missing-key-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Access existing variable but non-existent field within it
								// Use default "" to handle missing field gracefully (returns empty string instead of "<no value>")
								"missing-data": `{{ (parseKeyValue .data).nonexistent_field | default "" }}`,
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "data", // Load entire key=value content as .data
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// The missing key should result in empty string
			// When accessing a non-existent key in a map, Go template returns the zero value
			// Using default "" ensures we get empty string instead of "<no value>"
			Expect(secret.Data).To(HaveKey("missing-data"))
			Expect(string(secret.Data["missing-data"])).To(Equal(""))

		})
	})

	Describe("Performance and Scalability", func() {
		It("Should handle large template processing efficiently", func() {
			startTime := time.Now()

			store := createTemplateStore(testNamespace.Name)

			// Create a template with many fields to test performance
			largeTemplateData := make(map[string]string)
			for i := 0; i < 50; i++ {
				largeTemplateData[fmt.Sprintf("field_%d", i)] = fmt.Sprintf(`{{ .value_%d }}`, i)
			}

			esName := "large-template-externalsecret-" + getRandString()
			secretTargetName := "large-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: largeTemplateData,
						},
					},
				},
			}

			// Add many data entries
			for i := 0; i < 50; i++ {
				externalSecret.Spec.Data = append(externalSecret.Spec.Data, api.DataSource{
					Key:  SimpleTemplateSecretName,
					Name: fmt.Sprintf("value_%d", i),
					SecretStoreRef: &api.SecretStoreRef{
						Name:      store.Name,
						Namespace: store.Namespace,
						Kind:      ResourceSecretStore,
					},
				})
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify that all fields were processed
			for i := 0; i < 50; i++ {
				fieldName := fmt.Sprintf("field_%d", i)
				Expect(secret.Data).To(HaveKey(fieldName))
			}

			duration := time.Since(startTime)
			GinkgoWriter.Printf("Large template processing took: %v\n", duration)

			// Performance check - should complete within reasonable time
			Expect(duration.Seconds()).To(BeNumerically("<", 60.0))

		})
	})

	Describe("Template Scope Testing", func() {
		It("Should process template with KeysAndValues scope", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create a ConfigMap with template that demonstrates KeysAndValues scope functionality
			// This template will generate key-value pairs that get parsed into separate secret entries
			cmName := "keys-values-configmap-" + getRandString()
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: testNamespace.Name,
				},
				Data: map[string]string{
					// SimpleTemplateSecretName content: key1=value1, key2=value2, status=enabled, name=test-app
					"user-config": `username={{ (parseKeyValue .username).name }}
email={{ (parseKeyValue .email).status }}`,
					// GoTemplateSecretName JSON contains an "environment" field with value "production"
					"app-config": `app_name=myapp
app_version=1.0
environment={{ (.env | fromJson).environment }}`,
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			// Create ExternalSecret with KeysAndValues template scope
			esName := "keys-values-externalsecret-" + getRandString()
			secretTargetName := "keys-values-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									ConfigMap: &api.TemplateRef{
										Name: cmName,
										Items: []api.TemplateRefItem{
											{
												Key:        "user-config",
												TemplateAs: api.TemplateScopeKeysAndValues,
											},
											{
												Key:        "app-config",
												TemplateAs: api.TemplateScopeKeysAndValues,
											},
										},
									},
									Target: "Data",
								},
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "username",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  SimpleTemplateSecretName,
							Name: "email",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  GoTemplateSecretName,
							Name: "env",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify all expected keys hold the exact values rendered from the known source data
			Expect(string(secret.Data["username"])).To(Equal("test-app"))
			Expect(string(secret.Data["email"])).To(Equal("enabled"))
			Expect(string(secret.Data["app_name"])).To(Equal("myapp"))
			Expect(string(secret.Data["app_version"])).To(Equal("1.0"))
			Expect(string(secret.Data["environment"])).To(Equal("production"))

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace

			Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
		})
	})

	Describe("Literal Template Testing", func() {
		It("Should process literal template from TemplateFrom", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with literal template
			esName := "literal-template-externalsecret-" + getRandString()
			secretTargetName := "literal-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									Literal: stringPtr(`ENV_{{ (parseKeyValue .status).status | upper }}`),
									Target:  "Data",
								},
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "status",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data contains the processed literal template
			Expect(secret.Data).To(HaveKey("literal"))
			// Should contain "ENV_ENABLED" since SimpleTemplateSecretName has status=enabled
			Expect(string(secret.Data["literal"])).To(ContainSubstring("ENV_ENABLED"))

		})
	})

	Describe("Template Target Testing", func() {
		It("Should process template targeting Annotations", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create a ConfigMap with annotation template
			cmName := "annotation-configmap-" + getRandString()
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: testNamespace.Name,
				},
				Data: map[string]string{
					"app-version": `app-version=v1.2.3`,
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			// Create ExternalSecret targeting Annotations
			esName := "annotation-target-externalsecret-" + getRandString()
			secretTargetName := "annotation-target-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									ConfigMap: &api.TemplateRef{
										Name: cmName,
										Items: []api.TemplateRefItem{
											{
												Key:        "app-version",
												TemplateAs: api.TemplateScopeKeysAndValues,
											},
										},
									},
									Target: "Annotations",
								},
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  GoTemplateSecretName,
							Name: "version",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the annotation contains the processed template
			Expect(secret.Annotations).To(HaveKey("app-version"))
			Expect(secret.Annotations["app-version"]).To(Equal("v1.2.3"))

			// O-1 fix: a metadata-only templateFrom under the default Replace
			// merge policy must NOT clear the raw data keys.
			Expect(secret.Data).To(HaveKey("version"))

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace

			Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
		})

		It("Should process template targeting Labels", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create a ConfigMap with label template
			cmName := "label-configmap-" + getRandString()
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: testNamespace.Name,
				},
				Data: map[string]string{
					"env-type": `env-type=TEST_ENV`,
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).To(Succeed())

			// Create ExternalSecret targeting Labels
			esName := "label-target-externalsecret-" + getRandString()
			secretTargetName := "label-target-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									ConfigMap: &api.TemplateRef{
										Name: cmName,
										Items: []api.TemplateRefItem{
											{
												Key:        "env-type",
												TemplateAs: api.TemplateScopeKeysAndValues,
											},
										},
									},
									Target: "Labels",
								},
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  GoTemplateSecretName,
							Name: "env",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the label contains the processed template
			Expect(secret.Labels).To(HaveKey("env-type"))
			Expect(secret.Labels["env-type"]).To(Equal("TEST_ENV"))

			// O-1 fix: a metadata-only templateFrom under the default Replace
			// merge policy must NOT clear the raw data keys.
			Expect(secret.Data).To(HaveKey("env"))

			// Clean up - delete resources explicitly before namespace cleanup
			// This prevents controller from trying to create resources in terminating namespace

			Expect(k8sClient.Delete(ctx, configMap)).To(Succeed())
		})
	})

	Describe("Array/Slice Operations", func() {
		It("Should process array/slice operations in templates", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with array/slice template operations
			esName := "array-slice-template-externalsecret-" + getRandString()
			secretTargetName := "array-slice-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"first-user":   `{{ index (.data | fromJson).users 0 | mustToPrettyJson }}`,
								"user-count":   `{{ len ((.data | fromJson).users) }}`,
								"first-tag":    `{{ index (.data | fromJson).tags 0 }}`,
								"ports-joined": `{{ join "," ((.data | fromJson).ports) }}`,
								"active-users": `{{ range (.data | fromJson).users }}{{ if .active }}{{ .name }},{{ end }}{{ end }}`,
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  GoTemplateSecretName,
							Name: "data",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			secret := syncTemplateExternalSecret(externalSecret)

			Expect(string(secret.Data["user-count"])).To(Equal("3"))
			Expect(string(secret.Data["first-tag"])).To(Equal("web"))
			Expect(string(secret.Data["ports-joined"])).To(Equal("8080,8081,8082"))
			Expect(string(secret.Data["first-user"])).To(ContainSubstring("alice"))
			Expect(string(secret.Data["active-users"])).To(Equal("alice,charlie,"))
		})
	})

	Describe("Default Value Operations", func() {
		It("Should process default value operations in templates", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with default value template operations
			esName := "default-value-template-externalsecret-" + getRandString()
			secretTargetName := "default-value-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Test default when key doesn't exist
								"with-default": `{{ (parseKeyValue .data).key_missing_should_use_default | default "default_fallback" }}`,
								// Test default when key exists but is empty
								"empty-with-default": `{{ (parseKeyValue .data).key_empty | default "empty_fallback" }}`,
								// Test that existing value is used without default
								"present-no-default": `{{ (parseKeyValue .data).key1 | default "should_not_appear" }}`,
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "data",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data contains expected default value operations results
			Expect(secret.Data).To(HaveKey("with-default"))
			Expect(secret.Data).To(HaveKey("empty-with-default"))
			Expect(secret.Data).To(HaveKey("present-no-default"))

			// Verify default fallback behavior
			// key_missing_should_use_default doesn't exist, should use default_fallback
			Expect(string(secret.Data["with-default"])).To(Equal("default_fallback"))
			// key_empty doesn't exist in SimpleTemplateSecretName, so it will use the default
			Expect(string(secret.Data["empty-with-default"])).To(Equal("empty_fallback"))
			// key1 exists in SimpleTemplateSecretName with value=value1, so default should NOT be used
			Expect(string(secret.Data["present-no-default"])).To(Equal("value1"))

		})
	})

	Describe("Type Conversion Operations", func() {
		It("Should process type conversion operations in templates", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with type conversion template operations
			esName := "type-conversion-template-externalsecret-" + getRandString()
			secretTargetName := "type-conversion-template-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// String to int conversion
								"string-to-int": `{{ (.data | fromJson).string_number | atoi }}`,
								// Boolean string comparisons - check if value equals "true" or "yes"
								"string-to-bool-true": `{{ eq (((.data | fromJson).string_boolean_true) | lower) "true" }}`,
								"string-to-bool-yes":  `{{ eq (((.data | fromJson).string_boolean_yes) | lower) "yes" }}`,
								// These return false because "false" != "true" and "no" != "yes"
								"string-to-bool-false": `{{ eq (((.data | fromJson).string_boolean_false) | lower) "true" }}`,
								"string-to-bool-no":    `{{ eq (((.data | fromJson).string_boolean_no) | lower) "yes" }}`,
								// Array operations
								"array-length": `{{ len ((.data | fromJson).array_of_numbers) }}`,
								"array-first":  `{{ index ((.data | fromJson).array_of_numbers) 0 }}`,
								// JSON object access - mixed_array[1] is {"key": "two"}
								"json-object-access": `{{ (index ((.data | fromJson).mixed_array) 1).key }}`,
								"json-object-value":  `{{ ((.data | fromJson).json_object).value }}`,
								"json-object-nested": `{{ (((.data | fromJson).json_object).nested).inner }}`,
							},
						},
					},
					Data: []api.DataSource{
						{
							Key:  GoTemplateSecretName,
							Name: "data",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the secret data contains expected type conversion operations results
			Expect(secret.Data).To(HaveKey("string-to-int"))
			Expect(secret.Data).To(HaveKey("string-to-bool-true"))
			Expect(secret.Data).To(HaveKey("string-to-bool-yes"))
			Expect(secret.Data).To(HaveKey("string-to-bool-false"))
			Expect(secret.Data).To(HaveKey("string-to-bool-no"))
			Expect(secret.Data).To(HaveKey("array-length"))
			Expect(secret.Data).To(HaveKey("array-first"))
			Expect(secret.Data).To(HaveKey("json-object-access"))
			Expect(secret.Data).To(HaveKey("json-object-value"))
			Expect(secret.Data).To(HaveKey("json-object-nested"))

			// Verify specific values
			Expect(string(secret.Data["string-to-int"])).To(Equal("123"))
			Expect(string(secret.Data["string-to-bool-true"])).To(Equal("true"))
			Expect(string(secret.Data["string-to-bool-yes"])).To(Equal("true"))
			Expect(string(secret.Data["string-to-bool-false"])).To(Equal("false"))
			Expect(string(secret.Data["string-to-bool-no"])).To(Equal("false"))
			Expect(string(secret.Data["array-length"])).To(Equal("5"))
			Expect(string(secret.Data["array-first"])).To(Equal("1"))
			Expect(string(secret.Data["json-object-access"])).To(Equal("two"))
			Expect(string(secret.Data["json-object-value"])).To(Equal("two"))
			Expect(string(secret.Data["json-object-nested"])).To(Equal("inner-value"))

		})
	})

	Describe("Template Merge Policy Tests", func() {
		It("Should use Replace policy to clear original data", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with Replace policy
			esName := "merge-replace-example-" + getRandString()
			secretTargetName := "merge-replace-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  AdvancedDatabaseCredsSecret,
							Name: "dbcreds",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							MergePolicy: api.MergePolicyReplace, // Explicitly set Replace
							Data: map[string]string{
								"DATABASE_URL": `postgresql://{{ (jsonPath .dbcreds "password") }}:5432/mydb`,
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			secret := syncTemplateExternalSecret(externalSecret)

			// Verify: Only DATABASE_URL should exist, original data should be cleared
			Expect(secret.Data).To(HaveKey("DATABASE_URL"))
			// AdvancedDatabaseCredsSecret contains: {"user": "dbuser", "password": "dbpass123", "host": "postgres.internal", "port": "5432", "name": "users_db"}
			// The template uses jsonPath to extract password field
			Expect(string(secret.Data["DATABASE_URL"])).To(ContainSubstring("dbpass123"))
			Expect(string(secret.Data["DATABASE_URL"])).To(ContainSubstring(":5432/mydb"))

			// Original keys should NOT exist (Replace policy)
			Expect(secret.Data).ToNot(HaveKey("password"))
			Expect(secret.Data).ToNot(HaveKey("host"))

		})

		It("Should use Merge policy to preserve original data and override same keys", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with Merge policy
			esName := "merge-policy-example-" + getRandString()
			secretTargetName := "merge-policy-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  AdvancedDatabaseCredsSecret,
							Name: "dbcreds",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							MergePolicy: api.MergePolicyMerge,
							Data: map[string]string{
								"host":    "myhost",
								"new_key": "new_value",
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			secret := syncTemplateExternalSecret(externalSecret)

			// Verify: Both original and new keys should exist
			Expect(secret.Data).To(HaveKey("host"))
			Expect(string(secret.Data["host"])).To(Equal("myhost"))

			// AdvancedDatabaseCredsSecret contains the full JSON as dbcreds
			// With Merge policy, the original dbcreds should still exist
			Expect(secret.Data).To(HaveKey("dbcreds"))

			Expect(secret.Data).To(HaveKey("new_key"))
			Expect(string(secret.Data["new_key"])).To(Equal("new_value"))

		})
	})

	Describe("Custom Template Functions", func() {
		It("Should test bcrypt password hashing function", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with bcrypt template
			esName := "bcrypt-test-" + getRandString()
			secretTargetName := "bcrypt-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "password",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"hashed-password": `{{ bcrypt .password }}`,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify the hashed password starts with $2a$ (bcrypt format)
			Expect(secret.Data).To(HaveKey("hashed-password"))
			hashedPassword := string(secret.Data["hashed-password"])
			Expect(hashedPassword).To(MatchRegexp(`^\$2[ab]?\$\d+\$`))
			Expect(len(hashedPassword)).To(BeNumerically(">", 50))

		})

		It("Should test htpasswd function for HTTP Basic Auth", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with htpasswd template using JSON credentials
			esName := "htpasswd-test-" + getRandString()
			secretTargetName := "htpasswd-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  AuthCredentialsSecretName,
							Name: "credentials",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"htpasswd-file": `{{ htpasswd (.credentials | fromJson).username (.credentials | fromJson).password }}`,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify htpasswd format: username:$2a$...
			Expect(secret.Data).To(HaveKey("htpasswd-file"))
			htpasswdContent := string(secret.Data["htpasswd-file"])
			Expect(htpasswdContent).To(MatchRegexp(`^[\w-]+:\$2[ab]?\$\d+\$.+\n$`))

		})

		It("Should test jsonPath function for JSON extraction", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with jsonPath template
			esName := "jsonpath-test-" + getRandString()
			secretTargetName := "jsonpath-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  GoTemplateSecretName,
							Name: "data",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"first-user-name": `{{ jsonPath .data "users.0.name" }}`,
								"first-user-role": `{{ jsonPath .data "users.0.role" }}`,
								"first-tag":       `{{ jsonPath .data "tags.0" }}`,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify jsonPath extraction works (actual values depend on GoTemplateSecretName structure)
			// GoTemplateSecretName contains JSON with users array and tags array
			Expect(secret.Data).To(HaveKey("first-user-name"))
			Expect(secret.Data).To(HaveKey("first-user-role"))
			Expect(secret.Data).To(HaveKey("first-tag"))

			// Verify the values match expected data from GoTemplateSecretName
			// users[0] is {"name": "alice", "age": 30, "active": true, "role": "admin"}
			Expect(string(secret.Data["first-user-name"])).To(Equal("alice"))
			Expect(string(secret.Data["first-user-role"])).To(Equal("admin"))
			// tags[0] is "web"
			Expect(string(secret.Data["first-tag"])).To(Equal("web"))

		})

		It("Should test mergeJson function for deep merging", func() {
			store := createTemplateStore(testNamespace.Name)

			// Create ExternalSecret with mergeJson template using two separate JSON secrets
			esName := "mergejson-test-" + getRandString()
			secretTargetName := "mergejson-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  MergeJsonBaseSecretName,
							Name: "base",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  MergeJsonOverrideSecretName,
							Name: "override",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"merged": `{{ mergeJson .base .override }}`,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// mergeJson semantics (see deepMerge in template_processor.go): deep merge where
			// base-only keys are preserved, override-only keys are added and conflicting keys
			// take the override value.
			getNested := func(m map[string]interface{}, keys ...string) (interface{}, bool) {
				var cur interface{} = m
				for _, k := range keys {
					sub, ok := cur.(map[string]interface{})
					if !ok {
						return nil, false
					}
					cur, ok = sub[k]
					if !ok {
						return nil, false
					}
				}
				return cur, true
			}
			strEq := func(m map[string]interface{}, want string, keys ...string) bool {
				v, ok := getNested(m, keys...)
				return ok && v == want
			}
			numEq := func(m map[string]interface{}, want float64, keys ...string) bool {
				v, ok := getNested(m, keys...)
				f, isNum := v.(float64)
				return ok && isNum && f == want
			}
			boolEq := func(m map[string]interface{}, want bool, keys ...string) bool {
				v, ok := getNested(m, keys...)
				b, isBool := v.(bool)
				return ok && isBool && b == want
			}

			mergedRaw := secret.Data["merged"]
			Expect(mergedRaw).NotTo(BeEmpty())
			var merged map[string]interface{}
			Expect(json.Unmarshal(mergedRaw, &merged)).To(Succeed())
			Expect(strEq(merged, "myapp", "app", "name")).To(BeTrue())            // base-only key preserved
			Expect(strEq(merged, "2.0.0", "app", "version")).To(BeTrue())          // conflicting key takes override
			Expect(strEq(merged, "production", "app", "environment")).To(BeTrue()) // override-only key merged in
			Expect(strEq(merged, "prod-db.example.com", "database", "host")).To(BeTrue())
			Expect(numEq(merged, 5432, "database", "port")).To(BeTrue())
			Expect(strEq(merged, "devdb", "database", "name")).To(BeTrue())
			Expect(strEq(merged, "secret123", "database", "password")).To(BeTrue())
			Expect(strEq(merged, "debug", "logging", "level")).To(BeTrue())
			Expect(strEq(merged, "json", "logging", "format")).To(BeTrue())
			Expect(boolEq(merged, true, "cache", "enabled")).To(BeTrue())
			Expect(numEq(merged, 3600, "cache", "ttl")).To(BeTrue())

		})

		It("Should test parseKeyValue function for key=value parsing", func() {
			store := createTemplateStore(testNamespace.Name)

			esName := "parsekv-test-" + getRandString()
			secretTargetName := "parsekv-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "config",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Query keys that actually exist in SimpleTemplateSecretName
								// (key1=value1, key2=value2, status=enabled, name=test-app)
								"parsed-key1":   `{{ (parseKeyValue .config).key1 }}`,
								"parsed-status": `{{ (parseKeyValue .config).status }}`,
								"parsed-name":   `{{ (parseKeyValue .config).name }}`,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify parseKeyValue correctly splits key=value pairs
			Expect(string(secret.Data["parsed-key1"])).To(Equal("value1"))
			Expect(string(secret.Data["parsed-status"])).To(Equal("enabled"))
			Expect(string(secret.Data["parsed-name"])).To(Equal("test-app"))
		})

		It("Should test toLines function for splitting strings into arrays", func() {
			store := createTemplateStore(testNamespace.Name)

			esName := "tolines-test-" + getRandString()
			secretTargetName := "tolines-secret-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider: "kms",
					Data: []api.DataSource{
						{
							Key:  SimpleTemplateSecretName,
							Name: "multiline",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      store.Name,
								Namespace: store.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								"line-count": `{{ toLines .multiline | len }}`,
								"first-line": `{{ index (toLines .multiline) 0 }}`,
							},
						},
					},
				},
			}

			secret := syncTemplateExternalSecret(externalSecret)

			// Verify toLines function works (actual values depend on SimpleTemplateSecretName content)
			// SimpleTemplateSecretName contains: key1=value1\nkey2=value2\nstatus=enabled\nname=test-app (4 lines)
			Expect(secret.Data).To(HaveKey("line-count"))
			Expect(secret.Data).To(HaveKey("first-line"))

			// Verify line count (should be 4 for the 4 key-value pairs)
			Expect(string(secret.Data["line-count"])).To(Equal("4"))
			Expect(string(secret.Data["first-line"])).To(ContainSubstring("key1="))

		})
	})
})

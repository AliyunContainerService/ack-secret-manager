// Copyright © 2025 Alibaba Cloud. All rights reserved.

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

var _ = Describe("Advanced Template Processing E2E", func() {
	var (
		testNamespace *corev1.Namespace
		ctx           = context.Background()
	)

	BeforeEach(func() {
		testNamespace = createTestNamespace(ctx, "test-advanced-template-"+getRandString())
	})

	AfterEach(func() {
		// Delete the test namespace
		deleteTestNamespace(ctx, testNamespace)
	})

	Describe("Microservice Configuration Generation", func() {
		It("Should generate microservice configuration from multiple data sources", func() {
			// Create SecretStore first
			storeName := "microservice-store-" + getRandString()
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      storeName,
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
			Expect(k8sClient.Create(context.Background(), secretStore)).To(Succeed())

			// Wait for SecretStore to be ready
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(context.Background(), types.NamespacedName{
					Name:      storeName,
					Namespace: testNamespace.Name,
				}, createdStore)
				if err != nil {
					return false
				}
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Create ConfigMap with template
			cmName := "service-template-" + getRandString()
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: testNamespace.Name,
				},
				Data: map[string]string{
					"service-config": `# 服务基本信息
SERVICE_NAME={{ jsonPath .service "name" }}
SERVICE_VERSION={{ jsonPath .service "version" }}

# 环境配置
ENVIRONMENT={{ .environment | upper }}
DEBUG={{ if eq .environment "dev" }}true{{ else }}false{{ end }}

# 数据库配置
DATABASE_URL=postgresql://{{ jsonPath .db "user" }}:{{ jsonPath .db "password" }}@{{ jsonPath .db "host" }}:{{ jsonPath .db "port" }}/{{ jsonPath .db "name" }}

# 缓存配置
REDIS_URL=redis://{{ jsonPath .redis "host" }}:{{ jsonPath .redis "port" }}/{{ jsonPath .redis "db" | default "0" }}

# 日志配置
LOG_LEVEL={{ jsonPath .log "level" | default "info" }}
LOG_FORMAT={{ jsonPath .log "format" | default "json" }}`,
				},
			}
			Expect(k8sClient.Create(context.Background(), configMap)).To(Succeed())

			// Create ExternalSecret
			esName := "microservice-config-example-" + getRandString()
			secretTargetName := "service-configuration-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  AdvancedServiceMetadataSecret,
							Name: "service",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedEnvironmentConfigSecret,
							Name: "environment",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedDatabaseCredsSecret,
							Name: "db",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedRedisConfigSecret,
							Name: "redis",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedLoggingConfigSecret,
							Name: "log",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									ConfigMap: &api.TemplateRef{
										Name: cmName,
										Items: []api.TemplateRefItem{
											{
												Key:        "service-config",
												TemplateAs: api.TemplateScopeKeysAndValues,
											},
										},
									},
									Target: "Data",
								},
							},
							Metadata: &api.ExternalSecretTemplateMetadata{
								Labels: map[string]string{
									"app":         `{{ jsonPath .service "name" }}`,
									"version":     `{{ jsonPath .service "version" }}`,
									"environment": `{{ .environment }}`,
								},
								Annotations: map[string]string{
									"last-updated":  `{{ now | date "2006-01-02T15:04:05Z07:00" }}`,
									"config-source": "kms",
								},
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			Expect(k8sClient.Create(context.Background(), externalSecret)).To(Succeed())

			// Wait for the corresponding secret to be created
			secret := &corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: secretTargetName, Namespace: testNamespace.Name}, secret)
				return err == nil
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Verify the secret data
			Expect(secret.Data).To(HaveKey("SERVICE_NAME"))
			Expect(string(secret.Data["SERVICE_NAME"])).To(Equal("user-service"))

			Expect(secret.Data).To(HaveKey("SERVICE_VERSION"))
			Expect(string(secret.Data["SERVICE_VERSION"])).To(Equal("v1.2.3"))

			Expect(secret.Data).To(HaveKey("ENVIRONMENT"))
			Expect(string(secret.Data["ENVIRONMENT"])).To(Equal("DEV"))

			Expect(secret.Data).To(HaveKey("DEBUG"))
			Expect(string(secret.Data["DEBUG"])).To(Equal("true"))

			Expect(secret.Data).To(HaveKey("DATABASE_URL"))
			Expect(string(secret.Data["DATABASE_URL"])).To(Equal("postgresql://dbuser:dbpass123@postgres.internal:5432/users_db"))

			Expect(secret.Data).To(HaveKey("REDIS_URL"))
			Expect(string(secret.Data["REDIS_URL"])).To(Equal("redis://redis.cache.internal:6379/1"))

			Expect(secret.Data).To(HaveKey("LOG_LEVEL"))
			Expect(string(secret.Data["LOG_LEVEL"])).To(Equal("debug"))

			Expect(secret.Data).To(HaveKey("LOG_FORMAT"))
			Expect(string(secret.Data["LOG_FORMAT"])).To(Equal("text"))

			// Verify metadata
			Expect(secret.Labels).To(HaveKey("app"))
			Expect(secret.Labels["app"]).To(Equal("user-service"))
			Expect(secret.Labels).To(HaveKey("version"))
			Expect(secret.Labels["version"]).To(Equal("v1.2.3"))
			Expect(secret.Labels).To(HaveKey("environment"))
			Expect(secret.Labels["environment"]).To(Equal("dev"))

			Expect(secret.Annotations).To(HaveKey("config-source"))
			Expect(secret.Annotations["config-source"]).To(Equal("kms"))

			// Clean up
			CleanupExternalSecret(context.Background(), externalSecret)
			Expect(k8sClient.Delete(context.Background(), configMap)).To(Succeed())
			Expect(k8sClient.Delete(context.Background(), secretStore)).To(Succeed())
		})
	})

	Describe("Kubernetes Resource Manifest Generation", func() {
		It("Should generate Kubernetes Deployment and Service manifests", func() {
			// Create SecretStore first
			storeName := "k8s-manifest-store-" + getRandString()
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      storeName,
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
			Expect(k8sClient.Create(context.Background(), secretStore)).To(Succeed())

			// Wait for SecretStore to be ready
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(context.Background(), types.NamespacedName{
					Name:      storeName,
					Namespace: testNamespace.Name,
				}, createdStore)
				if err != nil {
					return false
				}
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Create ExternalSecret for Kubernetes manifests
			esName := "k8s-manifest-example-" + getRandString()
			secretTargetName := "k8s-manifests-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  AdvancedAppNameSecret,
							Name: "app_name",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedAppVersionSecret,
							Name: "version",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedReplicasSecret,
							Name: "replicas",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedImageRepoSecret,
							Name: "image_repo",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedImageTagSecret,
							Name: "image_tag",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedPortsSecret,
							Name: "ports",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Deployment manifest
								"deployment_yaml": `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .app_name }}
  labels:
    app: {{ .app_name }}
spec:
  replicas: {{ .replicas | default "3" | int }}
  selector:
    matchLabels:
      app: {{ .app_name }}
  template:
    metadata:
      labels:
        app: {{ .app_name }}
    spec:
      containers:
      - name: {{ .app_name }}
        image: {{ .image_repo }}:{{ .image_tag }}
        ports:
        {{ range .ports | fromJson }}
        - containerPort: {{ . }}
        {{ end }}`,

								// Service manifest
								"service_yaml": `apiVersion: v1
kind: Service
metadata:
  name: {{ .app_name }}-service
  labels:
    app: {{ .app_name }}
spec:
  selector:
    app: {{ .app_name }}
  ports:
  {{ range .ports | fromJson }}
  - protocol: TCP
    port: {{ . }}
    targetPort: {{ . }}
  {{ end }}
  type: ClusterIP`,
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			Expect(k8sClient.Create(context.Background(), externalSecret)).To(Succeed())

			// Wait for the corresponding secret to be created
			secret := &corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: secretTargetName, Namespace: testNamespace.Name}, secret)
				return err == nil
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Verify the generated manifests
			Expect(secret.Data).To(HaveKey("deployment_yaml"))
			deploymentYaml := string(secret.Data["deployment_yaml"])
			Expect(deploymentYaml).To(ContainSubstring("name: my-web-app"))
			Expect(deploymentYaml).To(ContainSubstring("replicas: 5"))
			Expect(deploymentYaml).To(ContainSubstring("image: registry.aliyuncs.com/mycompany:latest"))
			Expect(deploymentYaml).To(ContainSubstring("containerPort: 8080"))
			Expect(deploymentYaml).To(ContainSubstring("containerPort: 8443"))
			Expect(deploymentYaml).To(ContainSubstring("containerPort: 9090"))

			Expect(secret.Data).To(HaveKey("service_yaml"))
			serviceYaml := string(secret.Data["service_yaml"])
			Expect(serviceYaml).To(ContainSubstring("name: my-web-app-service"))
			Expect(serviceYaml).To(ContainSubstring("port: 8080"))
			Expect(serviceYaml).To(ContainSubstring("port: 8443"))
			Expect(serviceYaml).To(ContainSubstring("port: 9090"))
			Expect(serviceYaml).To(ContainSubstring("targetPort: 8080"))
			Expect(serviceYaml).To(ContainSubstring("targetPort: 8443"))
			Expect(serviceYaml).To(ContainSubstring("targetPort: 9090"))

			// Clean up
			CleanupExternalSecret(context.Background(), externalSecret)
			Expect(k8sClient.Delete(context.Background(), secretStore)).To(Succeed())
		})
	})

	Describe("Certificate and Key Handling", func() {
		It("Should handle certificate and key processing correctly", func() {
			// Create SecretStore first
			storeName := "cert-store-" + getRandString()
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      storeName,
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
			Expect(k8sClient.Create(context.Background(), secretStore)).To(Succeed())

			// Wait for SecretStore to be ready
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(context.Background(), types.NamespacedName{
					Name:      storeName,
					Namespace: testNamespace.Name,
				}, createdStore)
				if err != nil {
					return false
				}
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Create ExternalSecret for certificates
			esName := "certificate-example-" + getRandString()
			secretTargetName := "certificates-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  AdvancedPrivateKeySecret,
							Name: "private_key",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedCertificateSecret,
							Name: "certificate",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedCABundleSecret,
							Name: "ca_bundle",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedKeystorePasswordSecret,
							Name: "keystore_password",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// PEM format handling
								"tls.crt": `{{ .certificate }}`,
								"tls.key": `{{ .private_key }}`,
								"ca.crt":  `{{ .ca_bundle }}`,

								// Combined certificate chain
								"fullchain.pem": `{{ .certificate }}

{{ .ca_bundle }}`,

								// Keystore password with default
								"keystore_password": `{{ .keystore_password | default "changeit" }}`,

								// Application specific format
								"nginx_ssl_cert": `ssl_certificate /etc/ssl/certs/tls.crt;
ssl_certificate_key /etc/ssl/private/tls.key;
ssl_trusted_certificate /etc/ssl/certs/ca.crt;`,
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			Expect(k8sClient.Create(context.Background(), externalSecret)).To(Succeed())

			// Wait for the corresponding secret to be created
			secret := &corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: secretTargetName, Namespace: testNamespace.Name}, secret)
				return err == nil
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Verify certificate handling
			Expect(secret.Data).To(HaveKey("tls.crt"))
			Expect(string(secret.Data["tls.crt"])).To(ContainSubstring("-----BEGIN CERTIFICATE-----"))

			Expect(secret.Data).To(HaveKey("tls.key"))
			Expect(string(secret.Data["tls.key"])).To(ContainSubstring("-----BEGIN PRIVATE KEY-----"))

			Expect(secret.Data).To(HaveKey("ca.crt"))
			Expect(string(secret.Data["ca.crt"])).To(ContainSubstring("-----BEGIN CERTIFICATE-----"))

			Expect(secret.Data).To(HaveKey("fullchain.pem"))
			fullchain := string(secret.Data["fullchain.pem"])
			Expect(fullchain).To(ContainSubstring("-----BEGIN CERTIFICATE-----"))
			Expect(fullchain).To(ContainSubstring("\n\n-----BEGIN CERTIFICATE-----")) // Two certificates separated by empty line

			Expect(secret.Data).To(HaveKey("keystore_password"))
			Expect(string(secret.Data["keystore_password"])).To(Equal("mySecurePass123"))

			Expect(secret.Data).To(HaveKey("nginx_ssl_cert"))
			Expect(string(secret.Data["nginx_ssl_cert"])).To(ContainSubstring("ssl_certificate /etc/ssl/certs/tls.crt;"))

			// Clean up
			CleanupExternalSecret(context.Background(), externalSecret)
			Expect(k8sClient.Delete(context.Background(), secretStore)).To(Succeed())
		})
	})

	Describe("Multi-Environment Configuration Management", func() {
		It("Should dynamically select configuration based on environment", func() {
			// Create SecretStore first
			storeName := "multi-env-store-" + getRandString()
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      storeName,
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
			Expect(k8sClient.Create(context.Background(), secretStore)).To(Succeed())

			// Wait for SecretStore to be ready
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(context.Background(), types.NamespacedName{
					Name:      storeName,
					Namespace: testNamespace.Name,
				}, createdStore)
				if err != nil {
					return false
				}
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Create ConfigMap with environment templates
			cmName := "env-templates-" + getRandString()
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmName,
					Namespace: testNamespace.Name,
				},
				Data: map[string]string{
					"dev-config": `LOG_LEVEL=DEBUG
DATABASE_POOL_SIZE=5
CACHE_TTL=300
ENABLE_PROFILING=true`,

					"staging-config": `LOG_LEVEL=INFO
DATABASE_POOL_SIZE=20
CACHE_TTL=600
ENABLE_PROFILING=false`,

					"prod-config": `LOG_LEVEL=WARN
DATABASE_POOL_SIZE=50
CACHE_TTL=1800
ENABLE_PROFILING=false`,
				},
			}
			Expect(k8sClient.Create(context.Background(), configMap)).To(Succeed())

			// Create ExternalSecret with dynamic template selection
			esName := "multi-env-example-" + getRandString()
			secretTargetName := "multi-env-config-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  AdvancedCurrentEnvironmentSecret,
							Name: "environment",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							TemplateFrom: []api.TemplateFrom{
								{
									ConfigMap: &api.TemplateRef{
										Name: cmName,
										Items: []api.TemplateRefItem{
											{
												Key:        "staging-config",
												TemplateAs: api.TemplateScopeKeysAndValues,
											},
										},
									},
									Target: "Data",
								},
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			Expect(k8sClient.Create(context.Background(), externalSecret)).To(Succeed())

			// Wait for the corresponding secret to be created
			secret := &corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: secretTargetName, Namespace: testNamespace.Name}, secret)
				return err == nil
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Verify staging configuration was selected (based on current-environment = staging)
			Expect(secret.Data).To(HaveKey("LOG_LEVEL"))
			Expect(string(secret.Data["LOG_LEVEL"])).To(Equal("INFO"))

			Expect(secret.Data).To(HaveKey("DATABASE_POOL_SIZE"))
			Expect(string(secret.Data["DATABASE_POOL_SIZE"])).To(Equal("20"))

			Expect(secret.Data).To(HaveKey("CACHE_TTL"))
			Expect(string(secret.Data["CACHE_TTL"])).To(Equal("600"))

			Expect(secret.Data).To(HaveKey("ENABLE_PROFILING"))
			Expect(string(secret.Data["ENABLE_PROFILING"])).To(Equal("false"))

			// Clean up
			CleanupExternalSecret(context.Background(), externalSecret)
			Expect(k8sClient.Delete(context.Background(), configMap)).To(Succeed())
			Expect(k8sClient.Delete(context.Background(), secretStore)).To(Succeed())
		})
	})

	Describe("Dynamic Port Configuration", func() {
		It("Should generate dynamic port mappings and configurations", func() {
			// Create SecretStore first
			storeName := "dynamic-port-store-" + getRandString()
			secretStore := &api.SecretStore{
				ObjectMeta: metav1.ObjectMeta{
					Name:      storeName,
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
			Expect(k8sClient.Create(context.Background(), secretStore)).To(Succeed())

			// Wait for SecretStore to be ready
			Eventually(func() bool {
				createdStore := &api.SecretStore{}
				err := k8sClient.Get(context.Background(), types.NamespacedName{
					Name:      storeName,
					Namespace: testNamespace.Name,
				}, createdStore)
				if err != nil {
					return false
				}
				for _, condition := range createdStore.Status.Conditions {
					if condition.Type == api.SecretStoreReady && condition.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Create ExternalSecret for dynamic port configuration
			esName := "dynamic-ports-example-" + getRandString()
			secretTargetName := "dynamic-port-config-" + getRandString()
			externalSecret := &api.ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      esName,
					Namespace: testNamespace.Name,
				},
				Spec: api.ExternalSecretSpec{
					Provider:         "kms",
					RotationInterval: &metav1.Duration{Duration: 10 * time.Second},
					Data: []api.DataSource{
						{
							Key:  AdvancedPortsSecret,
							Name: "ports",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
						{
							Key:  AdvancedServiceNameSecret,
							Name: "service_name",
							SecretStoreRef: &api.SecretStoreRef{
								Name:      storeName,
								Namespace: secretStore.Namespace,
								Kind:      ResourceSecretStore,
							},
						},
					},
					Target: &api.ExternalSecretTarget{
						Name: secretTargetName,
						Template: &api.ExternalSecretTemplate{
							Data: map[string]string{
								// Generate port mapping configuration
								"port_mappings": `{{ range .ports | fromJson }}
- containerPort: {{ . }}
  hostPort: {{ add . 10000 }}
{{ end }}`,

								// Generate health check configuration
								"health_check_ports": `{{ range .ports | fromJson }}
{{ if eq (printf "%.0f" .) "8080" }}health_port: {{ . }}{{ end }}
{{ end }}`,

								// Generate service discovery tags
								"discovery_tags": `service={{ .service_name }}
{{ range .ports | fromJson }}
port_{{ . }}=enabled
{{ end }}`,
							},
						},
					},
				},
			}

			// Create the ExternalSecret
			Expect(k8sClient.Create(context.Background(), externalSecret)).To(Succeed())

			// Wait for the corresponding secret to be created
			secret := &corev1.Secret{}
			Eventually(func() bool {
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: secretTargetName, Namespace: testNamespace.Name}, secret)
				return err == nil
			}, time.Second*30, time.Second*5).Should(BeTrue())

			// Verify dynamic port configurations
			Expect(secret.Data).To(HaveKey("port_mappings"))
			portMappings := string(secret.Data["port_mappings"])
			Expect(portMappings).To(ContainSubstring("containerPort: 8080"))
			Expect(portMappings).To(ContainSubstring("hostPort: 18080"))
			Expect(portMappings).To(ContainSubstring("containerPort: 8443"))
			Expect(portMappings).To(ContainSubstring("hostPort: 18443"))
			Expect(portMappings).To(ContainSubstring("containerPort: 9090"))
			Expect(portMappings).To(ContainSubstring("hostPort: 19090"))

			Expect(secret.Data).To(HaveKey("health_check_ports"))
			healthCheck := string(secret.Data["health_check_ports"])
			Expect(healthCheck).To(ContainSubstring("health_port: 8080"))
			// Should not contain other ports for health check
			Expect(healthCheck).ToNot(ContainSubstring("health_port: 8443"))
			Expect(healthCheck).ToNot(ContainSubstring("health_port: 9090"))

			Expect(secret.Data).To(HaveKey("discovery_tags"))
			discoveryTags := string(secret.Data["discovery_tags"])
			Expect(discoveryTags).To(ContainSubstring("service=payment-service"))
			Expect(discoveryTags).To(ContainSubstring("port_8080=enabled"))
			Expect(discoveryTags).To(ContainSubstring("port_8443=enabled"))
			Expect(discoveryTags).To(ContainSubstring("port_9090=enabled"))

			// Clean up
			CleanupExternalSecret(context.Background(), externalSecret)
			Expect(k8sClient.Delete(context.Background(), secretStore)).To(Succeed())
		})
	})
})

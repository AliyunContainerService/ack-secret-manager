# ACK Secret Manager Examples

> This directory contains complete usage examples for ACK Secret Manager, organized by feature category, covering authentication, CRD resources, advanced features, template processing, and best practices.
>
> All examples use a comment-driven style. Prerequisites, verification steps, notes, etc. are all provided as YAML comments and can be applied directly with `kubectl apply -f`.
>
> **Note**: The examples below use various command-line tools, including [aliyun CLI](https://github.com/aliyun/aliyun-cli), [ack-ram-tool](https://github.com/AliyunContainerService/ack-ram-tool), `kubectl`, `helm`, etc. You can also perform these operations via the [Alibaba Cloud Console](https://home.console.aliyun.com/) or by calling Alibaba Cloud OpenAPIs. Make sure each tool is installed and configured before use — for example, run `aliyun configure` to set up credentials and region for aliyun CLI, and configure kubeconfig for `kubectl` to connect to your cluster.

## Examples List

### Authentication Examples (`auth/`)

| File | Description | Related Documentation |
|------|-------------|----------------------|
| [auth-01-serviceaccount-rrsa.yaml](auth/auth-01-serviceaccount-rrsa.yaml) | ServiceAccount RRSA authentication (recommended) | [Auth Guide §1](../docs/auth_guide.md#1-serviceaccount-rrsa-authentication-recommended) |
| [auth-02-rrsa.yaml](auth/auth-02-rrsa.yaml) | RRSA authentication (environment variables + SecretStore) | [Auth Guide §2](../docs/auth_guide.md#2-rrsa-authentication) |
| [auth-03-ak-assume-role.yaml](auth/auth-03-ak-assume-role.yaml) | AK AssumeRole authentication | [Auth Guide §3](../docs/auth_guide.md#3-ak-assume-role-authentication) |
| [auth-04-ak-basic.yaml](auth/auth-04-ak-basic.yaml) | AK basic authentication | [Auth Guide §4](../docs/auth_guide.md#4-ak-authentication) |
| [auth-05-worker-role.yaml](auth/auth-05-worker-role.yaml) | WorkerRole authentication | [Auth Guide §5](../docs/auth_guide.md#5-workerrole-authentication) |

### CRD Resource Examples (`crd/`)

| File | Description | Related Documentation |
|------|-------------|----------------------|
| [crd-01-secretstore.yaml](crd/crd-01-secretstore.yaml) | SecretStore configuration example | [CRD Guide §SecretStore](../docs/crd_resources_guide.md#secretstore) |
| [crd-02-cluster-secret-store.yaml](crd/crd-02-cluster-secret-store.yaml) | ClusterSecretStore + conditions | [CRD Guide §ClusterSecretStore](../docs/crd_resources_guide.md#clustersecretstore) |
| [crd-03-externalsecret-basic.yaml](crd/crd-03-externalsecret-basic.yaml) | Basic ExternalSecret | [CRD Guide §ExternalSecret](../docs/crd_resources_guide.md#externalsecret) |
| [crd-04-externalsecret-multi-key.yaml](crd/crd-04-externalsecret-multi-key.yaml) | Multi-key configuration + JMESPath | [CRD Guide §ExternalSecret](../docs/crd_resources_guide.md#externalsecret) |
| [crd-05-cluster-external-secret.yaml](crd/crd-05-cluster-external-secret.yaml) | ClusterExternalSecret | [CRD Guide §ClusterExternalSecret](../docs/crd_resources_guide.md#clusterexternalsecret) |

### Advanced Feature Examples (`advanced/`)

| File | Description | Related Documentation |
|------|-------------|----------------------|
| [advanced-01-jmespath-parsing.yaml](advanced/advanced-01-jmespath-parsing.yaml) | JMESPath field extraction + dataProcess.extract auto-parsing | [Advanced Usage §JSON/YAML](../docs/advanced_usage.md#jsonyaml-credential-parsing) |
| [advanced-02-cross-account.yaml](advanced/advanced-02-cross-account.yaml) | Cross-account authentication (all methods) | [Advanced Usage §Cross-Account](../docs/advanced_usage.md#cross-account-synchronization) |
| [advanced-03-kms-endpoint.yaml](advanced/advanced-03-kms-endpoint.yaml) | KMS Endpoint configuration (including dedicated instances) | [Advanced Usage §kmsEndpoint](../docs/advanced_usage.md#kmsendpoint-configuration) |
| [advanced-04-credential-rotation.yaml](advanced/advanced-04-credential-rotation.yaml) | Credential rotation configuration (credential-level + global) | [Advanced Usage §Credential Rotation](../docs/advanced_usage.md#credential-rotation) |
| [advanced-05-oos-parameter.yaml](advanced/advanced-05-oos-parameter.yaml) | OOS parameter synchronization | [Advanced Usage §Multi-DataSource](../docs/advanced_usage.md#multi-data-source-support) |

### Template Examples (`template/`)

| File | Description | Related Documentation |
|------|-------------|----------------------|
| [template-01-basic.yaml](template/template-01-basic.yaml) | Basic inline templates | [Template Guide](../docs/template_processing_guide.md) |
| [template-02-template-from.yaml](template/template-02-template-from.yaml) | templateFrom usage | [Template Guide](../docs/template_processing_guide.md) |
| [template-03-merge-policy.yaml](template/template-03-merge-policy.yaml) | mergePolicy usage | [Template Guide](../docs/template_processing_guide.md) |
| [template-04-advanced-scenarios.yaml](template/template-04-advanced-scenarios.yaml) | Advanced scenarios (microservice config, TLS certificates, multi-environment management) | [Template Guide](../docs/template_processing_guide.md) |

### Best Practice Examples (`best-practices/`)

| File | Description | Related Documentation |
|------|-------------|----------------------|
| [best-practices-01-multi-tenant.yaml](best-practices/best-practices-01-multi-tenant.yaml) | Multi-tenant scenario | [Auth Guide §Best Practices](../docs/auth_guide.md) |
| [best-practices-02-production.yaml](best-practices/best-practices-02-production.yaml) | Production environment configuration | [Auth Guide §Best Practices](../docs/auth_guide.md) |

## Quick Start

### Before You Begin

1. **Replace placeholders**: All `<accountId>`, `<clusterId>`, `<region>`, etc. must be replaced with actual values
2. **RAM Role**: Ensure the RAM Role has been created with the correct trust policy configured
3. **Permissions**: The RAM Role must have KMS/OOS access permissions
4. **Namespace**: Adjust namespace names according to your actual environment

### Scenario 1: Single Namespace Application (Recommended)

```bash
# Using ServiceAccount RRSA
kubectl apply -f auth/auth-01-serviceaccount-rrsa.yaml

# Verify
kubectl get externalsecret app-secret -n production
kubectl get secret app-secret -n production -o yaml
```

### Scenario 2: Multi-Namespace Shared Authentication

```bash
# Using ClusterSecretStore
kubectl apply -f crd/crd-02-cluster-secret-store.yaml

# Create ExternalSecret in different namespaces
kubectl apply -f crd/crd-03-externalsecret-basic.yaml -n production
kubectl apply -f crd/crd-03-externalsecret-basic.yaml -n staging
```

### Scenario 3: Automatic Sync to Multiple Namespaces

```bash
# Using ClusterExternalSecret
kubectl apply -f crd/crd-05-cluster-external-secret.yaml

# Automatically creates ExternalSecrets in matching namespaces
kubectl get externalsecret db-secret -A
```

### Scenario 4: Cross-Account Access

```bash
# Configure cross-account authentication
kubectl apply -f advanced/advanced-02-cross-account.yaml
```

### Scenario 5: Template Data Transformation

```bash
# Using inline templates
kubectl apply -f template/template-01-basic.yaml
```

## Full Documentation

| Documentation | Description |
|---------------|-------------|
| [Authentication Guide](../docs/auth_guide.md) | Detailed configuration for 5 authentication methods |
| [CRD Resource Guide](../docs/crd_resources_guide.md) | Description of 4 CRD resources |
| [Advanced Usage Guide](../docs/advanced_usage.md) | Credential parsing, cross-account sync, kmsEndpoint, credential rotation, multi-data-source support |
| [Template Processing Guide](../docs/template_processing_guide.md) | Detailed template feature documentation |

## Security Recommendations

1. **Production**: Use ServiceAccount RRSA authentication
2. **Least Privilege**: Grant only necessary permissions to RAM Roles
3. **Avoid AK**: Do not use plain AccessKey authentication
4. **Access Control**: Configure conditions on ClusterSecretStore
5. **Regular Audits**: Periodically review RAM Role permissions and ExternalSecret statuses

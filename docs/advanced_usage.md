# ACK Secret Manager Advanced Usage Guide

> This document describes the advanced features of ack-secret-manager, including JSON/YAML credential parsing, cross-account sync, kmsEndpoint configuration, credential rotation, and multi-data source support.

## Table of Contents

- [JSON/YAML Credential Parsing](#jsonyaml-credential-parsing)
- [Cross-Account Sync](#cross-account-sync)
- [kmsEndpoint Configuration](#kmsendpoint-configuration)
- [Credential Rotation](#credential-rotation)
- [Multi-Data Source Support](#multi-data-source-support)

## JSON/YAML Credential Parsing

### Feature Description

Use the JMESPath query language to parse JSON-formatted KMS credentials and extract specific fields. Two parsing methods are available:

| Method | Configuration Field | Use Case | Description |
| ------ | ------------------- | -------- | ----------- |
| Field extraction | `data[].jmesPath` | Known JSON structure, extract specific fields | Precise extraction via JMESPath expressions |
| Auto-parsing | `dataProcess[].extract` | Unknown JSON structure, full parsing | Auto-flatten all fields into key-value pairs |

### data[].jmesPath — Field Extraction

Use when the KMS credential JSON structure is known and you only need specific fields.

**Field Description**:

| Field | Required | Description |
| ----- | -------- | ----------- |
| `jmesPath[].path` | Yes | Parse specific fields in JSON based on [JMESPath](https://jmespath.org/specification.html) specification |
| `jmesPath[].objectAlias` | Yes | The key name in the Kubernetes secret where the parsed field will be synced |

**Common JMESPath Expressions**:

| Expression | Description |
| ---------- | ----------- |
| `name` | Basic field access |
| `friends[0].name` | Array index |
| `friends[*].name` | Array traversal |
| `a.b.c.d` | Nested access |

### dataProcess[].extract — Auto-Parsing

Use when the KMS credential JSON structure is unknown and you need full parsing. Automatically flattens JSON/YAML credentials into key-value pairs stored in K8s Secret.

**Field Description**:

| Field | Required | Default Behavior | Description |
| ----- | -------- | ---------------- | ----------- |
| `extract.key` | Yes | — | KMS credential name |
| `extract.name` | Yes | — | ExternalSecret name, also the generated K8s Secret name |
| `extract.versionId` | No | Fetches latest version | KMS credential version |
| `extract.kmsEndpoint` | No | Uses global configuration | KMS service address |
| `extract.secretStoreRef` | No | Uses default values | SecretStore reference |
| `replaceRule[].source` | No | No replacement | Regex pattern to match keys that need renaming |
| `replaceRule[].target` | No | — | Replacement key name |

> **Notes**:
> - `extract` flattens all fields in the JSON credential into key-value pairs stored directly in the K8s Secret, no need to know the JSON structure in advance
> - `replaceRule` solves the problem where KMS credential keys contain illegal characters (e.g., `.`, `/`) that prevent writing to K8s Secret, supports batch key renaming via regex matching
>
> **replaceRule Example**: KMS credential contains key `db.password`, K8s Secret does not allow `.` as a key:
> ```yaml
> replaceRule:
>   - source: "\."    # Match all dots
>     target: "-"     # Replace with hyphen, db.password → db-password
> ```

### Complete Example

See [examples/advanced/advanced-01-jmespath-parsing.yaml](../examples/advanced/advanced-01-jmespath-parsing.yaml)

## Cross-Account Sync

### Feature Description

Access KMS credentials from other Alibaba Cloud accounts in the current account.

> **Note**: All authentication methods (ServiceAccount RRSA / RRSA / AK AssumeRole / AK / WorkerRole) support cross-account sync.

### How It Works

Overlay an additional AssumeRole layer via `remoteRamRoleARN` on top of the current account authentication to access the target account's credentials:

```
Current account authentication (any method) → Get temporary credentials → AssumeRole(remoteRamRoleARN) → Target account credentials
```

### Configuration

**SecretStore method** (for ServiceAccount RRSA / RRSA / AK AssumeRole / AK):

Add `remoteRamRoleARN` and `remoteRamRoleSessionName` in the SecretStore's `KMSAuth`, independent of the base authentication method.

**Environment variable method** (for RRSA / AK AssumeRole / AK / WorkerRole):

Configure via `ALICLOUD_REMOTE_ROLE_ARN` and `ALICLOUD_REMOTE_ROLE_SESSION_NAME` environment variables.

### Prerequisites

1. **Target Account**: Create a RAM Role with a trust policy that allows the current account to assume it, and has KMS access permissions
2. **Current Account**: Any authentication method has been configured, and has permissions to assume the target account's RAM Role

### Complete Example

See [examples/advanced/advanced-02-cross-account.yaml](../examples/advanced/advanced-02-cross-account.yaml)

## kmsEndpoint Configuration

### Feature Description

Accessing KMS services to fetch credentials supports two methods: dedicated instance gateway and shared gateway. Refer to [Differences between Shared Gateway and Dedicated Gateway](https://www.alibabacloud.com/help/zh/kms/key-management-service/developer-reference/classic-kms-sdkclassic-kms-sdk/#d61514b089my8).

### KMS Endpoint Priority

| Type | Configuration Field | Purpose | Priority | Description |
| ---- | ------------------- | ------- | -------- | ----------- |
| Credential-level | `ExternalSecret.spec.data.kmsEndpoint` | Specify endpoint per credential | Highest | Overrides global and default configurations |
| Global | `command.kmsEndpoint` (startup parameter) | Used for all KMS requests | Medium | Provides endpoint address beyond default |
| Default | None | Used when no endpoint is configured | Lowest | `kms-vpc.{region}.aliyuncs.com` |

### KMS Endpoint Address Description

| Gateway Type | Domain Type | Endpoint Address | Usage Notes |
| ------------ | ----------- | ---------------- | ----------- |
| Dedicated | KMS Private Domain | `{kms-instance-id}.cryptoservice.kms.aliyuncs.com` | KMS instance and cluster must be in the same region/VPC, instance version 3.0+ |
| Shared | VPC Domain | `kms-vpc.{region}.aliyuncs.com` | KMS credential and cluster in the same region (default) |
| Shared | Public | `kms.{region}.aliyuncs.com` | Cluster must have public network access |

### Configuration Location

- **Global configuration**: Set via `command.kmsEndpoint` in Helm `values.yaml`, applies to all credentials
- **Credential-level configuration**: Set via `ExternalSecret.spec.data[].kmsEndpoint`, applies only to that credential, overrides global configuration

### Complete Example

See [examples/advanced/advanced-03-kms-endpoint.yaml](../examples/advanced/advanced-03-kms-endpoint.yaml)

## Credential Rotation

### Feature Description

ack-secret-manager supports automatic credential rotation. The controller periodically re-fetches the latest credentials from the backend (KMS/OOS) and syncs them to K8s Secrets, ensuring applications always use the latest credential content.

### Configuration

| Configuration Level | Configuration Field | Description |
| ------------------- | ------------------- | ----------- |
| Per-secret | `ExternalSecret.spec.rotationInterval` | Rotation interval for a single ExternalSecret, takes priority over global configuration |
| Global | `command.pollingInterval` (startup parameter) | Default rotation interval for all ExternalSecrets |

> **Note**: When an ExternalSecret has `rotationInterval` configured, that value is used; otherwise the global `pollingInterval` is used.

### Example

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: rotation-demo
spec:
  provider: kms
  data:
    - key: my-secret
      name: my-secret
      versionId: v1
  rotationInterval: 60s  # Re-fetch credential from backend every 60 seconds
```

See [examples/advanced/advanced-04-credential-rotation.yaml](../examples/advanced/advanced-04-credential-rotation.yaml)

## Multi-Data Source Support

### Feature Description

ack-secret-manager supports syncing two types of data sources:

| Data Source | Description | Provider Value | Authentication Field |
| ----------- | ----------- | -------------- | -------------------- |
| KMS Secrets Manager | Synchronize secret credentials from KMS Secrets Manager | `kms` (default) | `KMSAuth` |
| OOS Encryption Parameter | Synchronize OOS encryption parameters | `oos` | `OOSAuth` |

### KMS vs OOS Configuration Differences

| Configuration Item | KMS Secrets Manager | OOS Encryption Parameter |
| ------------------ | ------------------- | ------------------------ |
| SecretStore authentication | `spec.KMS.KMSAuth` | `spec.OOS.OOSAuth` |
| ExternalSecret provider | `kms` | `oos` |
| `versionId` field | Required | Not required |
| `kmsEndpoint` field | Configurable | Not applicable |

> **Note**: The authentication fields (`KMSAuth` / `OOSAuth`) for KMS and OOS have the same structure and support the same authentication methods.

### OOS Example

**SecretStore**:

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: SecretStore
metadata:
  name: oos-store
spec:
  OOS:
    OOSAuth:
      oidcProviderARN: "acs:ram::{accountID}:oidc-provider/ack-rrsa-{clusterID}"
      ramRoleARN: "acs:ram::{accountID}:role/{roleName}"
```

**ExternalSecret**:

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: oos-demo
spec:
  provider: oos  # Set to oos
  data:
    - key: my-oos-parameter  # OOS encryption parameter name
      name: my-secret
      secretStoreRef:
        name: oos-store
        namespace: default
```

See [examples/advanced/advanced-05-oos-parameter.yaml](../examples/advanced/advanced-05-oos-parameter.yaml)

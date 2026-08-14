# ACK Secret Manager CRD Resources Guide

> This document provides detailed information about the 4 CRD resources in ack-secret-manager, including resource overview, cross-namespace control mechanisms, recommended usage patterns, and a complete field parameter reference.

## Table of Contents

- [CRD Overview](#crd-overview)
- [Cross-Namespace Control Mechanisms](#cross-namespace-control-mechanisms)
- [Recommended Usage](#recommended-usage)
- [SecretStore](#secretstore)
- [ClusterSecretStore](#clustersecretstore)
- [ExternalSecret](#externalsecret)
- [ClusterExternalSecret](#clusterexternalsecret)
- [CRD Field Parameters Reference](#crd-field-parameters-reference)

## CRD Overview

ack-secret-manager involves 4 types of CRDs, divided into two categories:

### Authentication CRDs (Store Access Credentials)

| CRD | Scope | Description |
| --- | ----- | ----------- |
| **SecretStore** | Namespace | Stores authentication info, referenced by ExternalSecrets in the same namespace by default, also supports cross-namespace references (disabled by default) |
| **ClusterSecretStore** | Cluster | Stores authentication info, can be referenced by ExternalSecrets in any namespace |

### Data CRDs (Define Sync Rules)

| CRD | Scope | Description |
| --- | ----- | ----------- |
| **ExternalSecret** | Namespace | Defines which credentials to sync, creates corresponding Kubernetes Secrets |
| **ClusterExternalSecret** | Cluster | Automatically creates ExternalSecrets in multiple matching namespaces |

**Design Philosophy**: Separation of permissions and data for enhanced flexibility.

### SecretStore vs ClusterSecretStore

| Feature | SecretStore | ClusterSecretStore |
| ------- | ----------- | ------------------ |
| **Resource Level** | Namespace | Cluster |
| **Scope** | Same namespace by default, also supports cross-namespace references (disabled by default) | Entire cluster |
| **Access Control** | None | Restricts accessible namespaces via `spec.conditions` |
| **Use Cases** | Multi-tenant isolation, environment separation | Global sharing, centralized management |

### ExternalSecret vs ClusterExternalSecret

Both require users to manually create the resources themselves. The key difference is: ClusterExternalSecret automatically creates and maintains ExternalSecret instances in matching namespaces based on `spec.conditions`.

| Feature | ExternalSecret | ClusterExternalSecret |
| ------- | -------------- | --------------------- |
| **Resource Level** | Namespace | Cluster |
| **Sync Target** | Defines and syncs credentials in a single namespace | Automatically creates ExternalSecrets in matching namespaces, which then sync credentials |
| **New Matching Namespace** | Requires manual creation of new ExternalSecret | Automatically creates corresponding ExternalSecret |
| **Configuration Consistency** | May be inconsistent across multiple ExternalSecrets | Unified management by ClusterExternalSecret ensures consistency |
| **Use Cases** | Few namespaces | Many/dynamic namespaces |

## Cross-Namespace Control Mechanisms

To enhance security and flexibility, ack-secret-manager provides multiple cross-namespace control mechanisms:

> **Breaking Change (v0.6.4)**: For security hardening, both `enableCrossNamespaceSecretStore` and `enableCrossNamespaceAuthRef` default values changed from `true` to `false`. If your deployment uses cross-namespace references, you must explicitly set these parameters to `true` in `values.yaml` when upgrading, otherwise cross-namespace references will be rejected.

### ExternalSecret Referencing SecretStore Control

- Controlled by `command.enableCrossNamespaceSecretStore` parameter to determine if ExternalSecret can reference SecretStore across namespaces
- Default value is `false`, disallowing cross-namespace references by default
- When set to `true`, ExternalSecret can reference SecretStore across namespaces

**Reference Rules When Using SecretStore**:

| Referrer | Target Resource | Namespace Field | Control Parameter | When Namespace Not Set | When Namespace Is Set |
| -------- | --------------- | --------------- | ----------------- | ---------------------- | --------------------- |
| ExternalSecret | SecretStore | `secretStoreRef.namespace` | `command.enableCrossNamespaceSecretStore` | References SecretStore in same namespace | References SecretStore in specified namespace (requires parameter to be `true`) |

> When the control parameter is set to `false`, even if the namespace field is set, it can only reference resources in the same namespace.

**Reference Rules When Using ClusterSecretStore**:

| Referrer | Target Resource | Namespace Field | Description |
| -------- | --------------- | --------------- | ----------- |
| ExternalSecret | ClusterSecretStore | None (cluster-scoped resource) | Can be referenced from any namespace, must satisfy `conditions` configuration |

### SecretStore Referencing Authentication Resources Control

- Controlled by `command.enableCrossNamespaceAuthRef` parameter to determine if SecretStore can reference authentication resources (ServiceAccount, AccessKey Secret) across namespaces
- Default value is `false`, disallowing cross-namespace references by default
- When set to `true`, SecretStore can reference authentication resources across namespaces

**When Using SecretStore**:

| Referrer | Target Resource | Namespace Field | Control Parameter | When Namespace Not Set | When Namespace Is Set |
| -------- | --------------- | --------------- | ----------------- | ---------------------- | --------------------- |
| SecretStore | ServiceAccount | `serviceAccountRef.namespace` | `command.enableCrossNamespaceAuthRef` | References ServiceAccount in same namespace | References ServiceAccount in specified namespace (requires parameter to be `true`) |
| SecretStore | Secret | `secretRef.namespace` | `command.enableCrossNamespaceAuthRef` | References Secret in same namespace | References Secret in specified namespace (requires parameter to be `true`) |

**When Using ClusterSecretStore**:

| Referrer | Target Resource | Namespace Field | Description |
| -------- | --------------- | --------------- | ----------- |
| ClusterSecretStore | ServiceAccount | `serviceAccountRef.namespace` | **Required**, can point to any namespace |
| ClusterSecretStore | Secret | `secretRef.namespace` | **Required**, can point to any namespace |

### ClusterExternalSecret Namespace Matching Control

- ClusterExternalSecret uses the `spec.conditions` field to define namespace selection criteria, creating ExternalSecret resources in matching namespaces
- Supports three matching methods, conditions are OR-ed:
  1. `namespaceSelector`: Uses label selector to match allowed namespaces
  2. `namespaces`: Explicitly lists allowed namespace names
  3. `namespaceRegexes`: Uses regex patterns to match allowed namespace names

> **Note**: A regex in `namespaceRegexes` must match the entire namespace name (e.g., to match namespaces with the `prod-` prefix, use `prod-.*` rather than `prod-`). An invalid regex or labelSelector rejects all namespaces.

### ClusterSecretStore Access Control

- ClusterSecretStore uses the `spec.conditions` field to define which namespaces are allowed to access this resource
- Supports three access control methods, conditions are OR-ed:
  1. `namespaceSelector`: Uses label selector to match allowed namespaces
  2. `namespaces`: Explicitly lists allowed namespace names
  3. `namespaceRegexes`: Uses regex patterns to match allowed namespace names

> **Note**: A regex in `namespaceRegexes` must match the entire namespace name (e.g., to match namespaces with the `prod-` prefix, use `prod-.*` rather than `prod-`). An invalid regex or labelSelector rejects all namespaces.

## Recommended Usage

### Recommended Approach for Cross-Namespace Access

For scenarios requiring cross-namespace access, the following combinations are recommended:

1. **ClusterSecretStore + ExternalSecret**: When multiple namespaces need to use the same authentication configuration
2. **ClusterSecretStore + ClusterExternalSecret**: When ExternalSecrets with the same configuration need to be automatically created in multiple namespaces

### Security Best Practices

1. **Principle of Least Privilege**:
   - Keep `command.enableCrossNamespaceSecretStore` and `command.enableCrossNamespaceAuthRef` at their default value of `false` unless cross-namespace access is explicitly required
   - Prefer namespace-level resources (SecretStore and ExternalSecret)
2. **Access Control Configuration**:
   - When using ClusterSecretStore, explicitly configure `spec.conditions` to restrict accessible namespaces
   - Avoid creating ClusterSecretStores without access restrictions
3. **Authentication Method Selection**:
   - Prefer RRSA or ServiceAccount for authentication, avoid exposing AccessKey directly in configurations
   - Separate authentication configuration from data configuration for improved security
4. **Use ClusterExternalSecret Only When Necessary** to reduce the risk of Secrets leaking across namespaces:
   - If your business needs to sync Secrets across multiple namespaces, use `spec.conditions` to precisely control the scope of ExternalSecret creation

## SecretStore

### Features and Use Cases

SecretStore is a namespace-level resource used to define access credentials (such as RRSA, AK configurations, etc.). It is referenced by ExternalSecrets in the same namespace by default, but also supports cross-namespace references (disabled by default, see [Cross-Namespace Control Mechanisms](#cross-namespace-control-mechanisms)). Suitable for multi-tenant isolation, environment separation, and similar scenarios.

### Configuration Notes

SecretStore supports all authentication methods (ServiceAccount RRSA, RRSA, AK AssumeRole, AK, cross-account). For detailed configuration, refer to the [Authentication Guide](auth_guide.md).

Key configuration points:
- **ServiceAccount RRSA**: Reference a ServiceAccount in the same namespace via `serviceAccountRef.name`, or cross-namespace via `serviceAccountRef.namespace`
- **AK Authentication**: Reference a Kubernetes Secret containing AK info via `accessKey` / `accessKeySecret`
- **Cross-Account**: Add `remoteRamRoleARN` + `remoteRamRoleSessionName` on top of any authentication method

### Complete Examples

See [examples/crd/crd-01-secretstore.yaml](../examples/crd/crd-01-secretstore.yaml)

## ClusterSecretStore

### Features and Use Cases

ClusterSecretStore is a cluster-level resource with the same functionality as SecretStore, but can be referenced by ExternalSecrets in any namespace in the cluster. It supports access control configuration via `spec.conditions`. Suitable for global shared authentication, centralized management, and similar scenarios.

### Access Control

Control which namespaces can use this ClusterSecretStore via `spec.conditions`, supporting three methods:

1. **namespaces**: Explicitly list allowed namespace names
2. **namespaceSelector**: Use label selectors to match namespaces
3. **namespaceRegexes**: Use regex patterns to match namespace names

> **Note**: A regex in `namespaceRegexes` must match the entire namespace name (e.g., to match namespaces with the `prod-` prefix, use `prod-.*` rather than `prod-`). An invalid regex or labelSelector rejects all namespaces.

### Configuration Notes

ClusterSecretStore authentication configuration is the same as SecretStore, with additional notes:
- When referencing ServiceAccount or Secret, the `namespace` field is **required** (can point to any namespace)
- It is recommended to configure `conditions` to restrict accessible namespaces, avoiding unrestricted ClusterSecretStores

### Complete Examples

See [examples/crd/crd-02-cluster-secret-store.yaml](../examples/crd/crd-02-cluster-secret-store.yaml)

## ExternalSecret

### Features and Use Cases

ExternalSecret is a namespace-level resource used to define the basic information of credentials to be synced (such as credential name, version, etc.) and to specify a SecretStore. The controller automatically fetches credentials from the backend and creates corresponding Kubernetes Secrets based on the configuration. Suitable for single-namespace credential sync scenarios.

### Configuration Notes

Key ExternalSecret configuration fields (indented fields belong to the level above them):
- `provider`: Target cloud service (`kms` or `oos`, default `kms`)
- `rotationInterval`: Sync interval
- `dataProcess`: Auto-parse JSON/YAML credentials (optional)
- `data[]`: List of credentials to sync. Fields under each entry:
  - `key`: Credential name in the backend (KMS credential name)
  - `name`: Key in the K8s Secret
  - `versionId`: Credential version to sync
  - `secretStoreRef`: Reference to SecretStore or ClusterSecretStore, configured per `data[]` entry (must specify `kind: ClusterSecretStore` when referencing ClusterSecretStore)
  - `kmsEndpoint`: Credential-level KMS Endpoint (optional, overrides global configuration)
  - `jmesPath`: Parse specific fields in JSON/YAML credentials (optional)

### Multi-Key Configuration

A single ExternalSecret can configure multiple credentials via the `data[]` array. All sync results are written into the **same** Kubernetes Secret. Each entry's `key` specifies the credential name in the backend (KMS/OOS), and `name` specifies the key name in the K8s Secret `data`.

**Configuration Example**:

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: my-app-secrets
  namespace: production
spec:
  provider: kms
  data:
    - key: db-password      # Credential name in KMS
      name: database-password # Key written to K8s Secret
      secretStoreRef:       # Each data[] entry configures its own SecretStore reference
        name: my-store
        kind: SecretStore
    - key: api-key
      name: api-key
      secretStoreRef:
        name: my-store
    - key: tls-cert
      name: tls-certificate
      secretStoreRef:
        name: my-store
```

The above configuration generates a K8s Secret named `my-app-secrets` with three keys in `data`:

```
K8s Secret (production/my-app-secrets)
  data:
    database-password: <content of db-password credential>
    api-key:           <content of api-key credential>
    tls-certificate:   <content of tls-cert credential>
```

> **Notes**:
> - `key` is the credential identifier in the backend (KMS/OOS), `name` is the key name written to K8s Secret `data`. They can be different
> - If `name` is omitted, the `key` value is used as the Secret data key (e.g., `key: api-key` without `name` results in Secret data key `api-key`)
> - Each `data[]` entry independently configures its own `secretStoreRef`, `kmsEndpoint`, `versionId`, etc.; `secretStoreRef` is a per-entry field (there is no spec-level `secretStoreRef`)
> - Suitable for scenarios where multiple related credentials need to be centrally managed in the same K8s Secret (e.g., database connection info, TLS certificates)

> **Status Semantics (v0.6.6)**: `status.dataSyncResults` is updated when the sync result semantics (per-key status/reason, or the overall success state) change, and the sync timestamp is forcibly refreshed after the controller actually writes to (or deletes) the target Kubernetes Secret; `synchronizationTime` records when the currently reported result occurred and is not refreshed every reconcile round: on steady-state polling rounds where the fetched data is unchanged and no Secret write occurs, it keeps the time of the last successful synchronization. Do not use it as a liveness heartbeat. For how failures affect the cluster Secret, see [Advanced Usage Guide - Sync Failure Handling Semantics](advanced_usage.md#sync-failure-handling-semantics).

> **Deletion Behavior**: When an ExternalSecret is deleted, the finalizer unconditionally deletes the target Secret (`cleanupSecretOnFailure` is not consulted); if `target.name` points to a manually maintained Secret with the same name, that Secret is deleted as well.

### Complete Examples

See:
- [examples/crd/crd-03-externalsecret-basic.yaml](../examples/crd/crd-03-externalsecret-basic.yaml) — Basic example
- [examples/crd/crd-04-externalsecret-multi-key.yaml](../examples/crd/crd-04-externalsecret-multi-key.yaml) — Multi-key configuration

## ClusterExternalSecret

### Features and Use Cases

ClusterExternalSecret is a cluster-level resource used to manage and coordinate ExternalSecrets across multiple namespaces. It automatically creates ExternalSecrets in matching namespaces. Suitable for scenarios with a large number of or dynamic namespaces requiring credential sync.

### How It Works

```
ClusterExternalSecret Created
  ↓
Controller periodically checks conditions
  ↓
Matching namespaces (via namespaces / namespaceSelector / namespaceRegexes)
  ↓
Create ExternalSecret in each matching namespace
  ↓
ExternalSecret Controller syncs KMS credentials
  ↓
Generate Kubernetes Secret
```

### Configuration Notes

Key ClusterExternalSecret configuration fields:
- `externalSecretSpec`: Specification of the ExternalSecret to be created
- `externalSecretName`: Name of the ExternalSecret to create (defaults to ClusterExternalSecret's name)
- `externalSecretMetadata`: Add labels and annotations to created ExternalSecrets
- `conditions`: Define which namespaces to create ExternalSecrets in
- `rotationInterval`: Interval for checking namespace matches

### Notes

- **Deletion Behavior**: When deleting a ClusterExternalSecret, it will automatically delete the ExternalSecrets it created in each namespace
- **Naming Conflicts**: If an ExternalSecret with the same name already exists in a namespace, it will be taken over and overwritten. It is recommended to use a unique `externalSecretName`

### Complete Examples

See [examples/crd/crd-05-cluster-external-secret.yaml](../examples/crd/crd-05-cluster-external-secret.yaml)

## CRD Field Parameters Reference

### SecretStore

**spec**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| KMS | Connect to KMS Secrets Manager to get secrets | No |
| OOS | Connect to OOS to get encrypted parameters | No |

**KMS**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| KMSAuth | Credentials for accessing KMS | No |

**OOS**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| OOSAuth | Credentials for accessing OOS | No |

**KMSAuth / OOSAuth**

| CRD Field | Description | Required | Default Behavior |
| --------- | ----------- | -------- | ---------------- |
| accessKey | Alibaba Cloud AccessKey authentication config | No | When not set, another enabled (prerequisites met) authentication method is selected by priority (RRSA → WorkerRole) |
| accessKeySecret | Alibaba Cloud AccessKey authentication config | No | Used together with accessKey |
| ramRoleARN | RAM Role ARN for RRSA or AK AssumeRole authentication | No | When not set, role-based authentication is not used |
| ramRoleSessionName | Role session name for STS AssumeRole | No | When not set, uses component default session name |
| oidcProviderARN | OIDC Provider ARN for RRSA authentication | No | When not set, OIDC authentication is not used |
| oidcTokenFilePath | OIDC Token file path | No | When not set, uses default path `/var/run/secrets/tokens/ack-secret-manager` |
| remoteRamRoleARN | Cross-account RAM Role ARN for cross-account sync | No | When not set, no cross-account AssumeRole is performed |
| remoteRamRoleSessionName | Cross-account role session name | No | When not set, uses component default session name |
| serviceAccountRef | Referenced ServiceAccount information for ServiceAccount RRSA authentication | No | When not set, ServiceAccount-level authentication is not used |

**Alibaba Cloud AccessKey Authentication Configuration**

Alibaba Cloud AccessKey is the most important identity credential for users to access cloud resources. It cannot be directly configured in SecretStore fields and must be referenced through Kubernetes Secrets. The reference configuration fields are described below:

> AccessKey leakage can lead to serious security risks. It is recommended to use RRSA or other Alibaba Cloud authentication methods.

| Field | Description | Required | Default Behavior |
| ----- | ----------- | -------- | ---------------- |
| name | Name of the Kubernetes Secret | Yes | — |
| namespace | Namespace where the Kubernetes Secret is located | No | For SecretStore, defaults to same namespace (cross-namespace reference controlled by `command.enableCrossNamespaceAuthRef`); required for ClusterSecretStore |
| key | Key specified in the Kubernetes Secret data | Yes | — |

**ServiceAccountRef**

| CRD Field | Description | Required | Default Behavior |
| --------- | ----------- | -------- | ---------------- |
| name | ServiceAccount name | Yes | — |
| namespace | ServiceAccount namespace | No | For SecretStore, defaults to same namespace (cross-namespace reference controlled by `command.enableCrossNamespaceAuthRef`); required for ClusterSecretStore |
| audiences | Audience array for the aud field in ServiceAccount token | No | When not set, defaults to `["sts.aliyuncs.com"]` |

### ClusterSecretStore

ClusterSecretStore is a cluster-level SecretStore resource that can be referenced by ExternalSecrets in any namespace in the cluster. In addition to all the features of SecretStore, it also adds access control configuration to restrict which namespaces can access this resource.

**spec**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| conditions | Defines the namespace conditions for accessing this resource. No restrictions when not configured; takes effect when configured | No |
| KMS | Connect to KMS Secrets Manager to get secrets | No |
| OOS | Connect to OOS to get encrypted parameters | No |

**conditions**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| namespaceSelector | Uses label selector to match allowed namespaces | No |
| namespaces | Explicitly lists allowed namespace names | No |
| namespaceRegexes | Uses regex patterns to match allowed namespace names | No |

> The authentication fields under KMS/OOS (KMSAuth/OOSAuth) are the same as SecretStore, please refer to the SecretStore parameter description above.
>
> **Regex matching note**: A regex in `namespaceRegexes` must match the entire namespace name (e.g., to match namespaces with the `prod-` prefix, use `prod-.*` rather than `prod-`). An invalid regex or labelSelector rejects all namespaces.

### ExternalSecret

**spec**

| CRD Field | Description | Required | Default Behavior |
| --------- | ----------- | -------- | ---------------- |
| provider | Target cloud service for fetching secrets | No | Defaults to `kms` when not set |
| data | Data sources (identifiers for target data) | No | — |
| dataProcess | Data sources requiring special processing | No | — |
| type | Kubernetes secret type | No | Defaults to `Opaque` when not set |
| target | Configuration for the Kubernetes Secret to be created | No | When not set, Secret name is the same as ExternalSecret name |
| rotationInterval | Time interval for the controller to re-reconcile the object | No | When not set, uses global `--polling-interval` (default 120s) |

**target**

| CRD Field | Description | Required | Default Behavior |
| --------- | ----------- | -------- | ---------------- |
| name | Name of the target Kubernetes Secret | No | Defaults to ExternalSecret name when not set |
| template | Template configuration for transforming secret data | No | When not set, raw credential data is used directly |

**template**

| CRD Field | Description | Required | Default |
| --------- | ----------- | -------- | ------- |
| data | Inline template definition | No | |
| templateFrom | External template reference (ConfigMap/Secret) | No | |
| metadata | Secret metadata template (labels/annotations) | No | |
| type | Type of the target secret | No | |
| mergePolicy | Merge policy for template results with original data | No | Replace |

**metadata**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| annotations | Annotations to apply to the secret | No |
| labels | Labels to apply to the secret | No |

**templateFrom**

| CRD Field | Description | Required | Default |
| --------- | ----------- | -------- | ------- |
| configMap | ConfigMap reference containing the template | No | |
| secret | Secret reference containing the template | No | |
| literal | Inline literal template string | No | |
| target | Target location for template results (Data/Annotations/Labels) | No | Data |

**configMap/secret**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| name | Name of the ConfigMap/Secret resource | Yes |
| items | List of keys in ConfigMap/Secret used as templates | Yes |

**items**

| CRD Field | Description | Required | Default |
| --------- | ----------- | -------- | ------- |
| key | Key in ConfigMap/Secret | Yes | |
| templateAs | How to interpret the template key | No | Values |

**templateAs**

| Value | Description |
| ----- | ----------- |
| Values | Treat template as values only |
| KeysAndValues | Treat template as key-value pairs |

**templateFrom.target**

| Value | Description |
| ----- | ----------- |
| Data | Store template results in Secret data |
| Annotations | Store template results in Secret annotations |
| Labels | Store template results in Secret labels |

**mergePolicy**

| Value | Description |
| ----- | ----------- |
| Replace | Completely use template field names (clear existing field names in the secret). Exception: when all `templateFrom` entries target only Labels/Annotations, the Secret `data` is not cleared — see [Template Processing Guide - Merge Policy](template_processing_guide.md#merge-policy-mergepolicy) |
| Merge | Merge template field names with existing field names in the secret. If field names overlap, template values take precedence |

**data (Data sources that don't require special processing)**

| CRD Field | Description | Required | Default Behavior |
| --------- | ----------- | -------- | ---------------- |
| key | Unique identifier for the target credential (KMS credential name or OOS parameter name) | Yes | — |
| name | Corresponding key name in K8s Secret data | No | Defaults to the `key` value when not set |
| versionStage | Target credential version stage (e.g., `ACSCurrent`, `ACSPrevious`) | No | When not set, fetches the latest version (`ACSCurrent`) |
| versionId | Target credential version ID; not required when provider is `oos` | No | When not set, fetches the latest version |
| jmesPath | When target credential is JSON/YAML format, extract specific fields via JMESPath expression | No | When not set, syncs the entire credential content |
| secretStoreRef | SecretStore reference for this credential; each `data[]` entry specifies its own SecretStore via this field | No | When not set, this data source uses environment-variable or WorkerRole authentication (see the Authentication Guide) |
| kmsEndpoint | KMS Endpoint address for this credential, can override global configuration | No | When not set, uses global `command.kmsEndpoint` → default `kms-vpc.{region}.aliyuncs.com` |

**dataProcess (Data sources requiring special processing)**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| extract | Parse json/yaml for the target secret, without requiring users to specify json/yaml keys | No |
| replaceRule | Replace keys of json/yaml parsed secrets according to specific rules, to prevent illegal keys from being stored in k8s secret | No |

**replaceRule (Used for Secret Key content replacement)**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| target | String to use for replacement | Yes |
| source | String to be replaced, can be a regex | Yes |

**jmesPath**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| path | JMES expression for specifying json/yaml key | Yes |
| objectAlias | Corresponding data key in k8s secret | Yes |

**secretStoreRef**

| CRD Field | Description | Required | Default Behavior |
| --------- | ----------- | -------- | ---------------- |
| name | Name of the SecretStore or ClusterSecretStore to reference | Yes | — |
| namespace | Namespace of the SecretStore | No | When not set, defaults to the ExternalSecret's namespace (cross-namespace reference controlled by `command.enableCrossNamespaceSecretStore`) |
| kind | Specify SecretStore or ClusterSecretStore | No | Defaults to `SecretStore` when not set |

### ClusterExternalSecret

ClusterExternalSecret is a resource for managing and coordinating ExternalSecrets across multiple namespaces. It automatically creates ExternalSecrets in matching namespaces. When an ExternalSecret to be created already exists in a matching namespace, ClusterExternalSecret completely updates the existing ExternalSecret resource with the new specification.

**spec**

| CRD Field | Description | Required | Default Behavior |
| --------- | ----------- | -------- | ---------------- |
| externalSecretSpec | Specification of the ExternalSecret to be created | Yes | — |
| externalSecretName | Name of the ExternalSecret to be created | No | Defaults to the ClusterExternalSecret's name |
| externalSecretMetadata | Metadata of the ExternalSecret to be created | No | When not set, no additional metadata is added |
| namespaceSelectors | Uses label selector to match allowed namespaces (deprecated) | No | Use `conditions` instead |
| conditions | List of conditions for selecting target namespaces | No | When both `conditions` and `namespaceSelectors` are unset, ExternalSecrets are created in all namespaces; once either is configured, namespaces that match no condition are excluded (fail-closed) |
| rotationInterval | Time interval for the controller to check namespace labels and reconcile objects | No | When not set, uses global `--polling-interval` (default 120s) |

**externalSecretMetadata**

The externalSecretMetadata field allows you to automatically add additional metadata to ExternalSecret resources created by ClusterExternalSecret:

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| annotations | Annotations for the ExternalSecret to be created | No |
| labels | Labels for the ExternalSecret to be created | No |

**conditions**

| CRD Field | Description | Required |
| --------- | ----------- | -------- |
| namespaceSelector | Uses label selector to match allowed namespaces | No |
| namespaces | Explicitly lists allowed namespace names | No |
| namespaceRegexes | Uses regex patterns to match allowed namespace names | No |

> **Regex matching note**: A regex in `namespaceRegexes` must match the entire namespace name (e.g., to match namespaces with the `prod-` prefix, use `prod-.*` rather than `prod-`). An invalid regex or labelSelector rejects all namespaces.

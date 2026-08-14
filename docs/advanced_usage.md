# ACK Secret Manager Advanced Usage Guide

> This document describes the advanced features of ack-secret-manager, including JSON/YAML credential parsing, cross-account sync, kmsEndpoint configuration, credential rotation, multi-data source support, and sync failure handling semantics.

## Table of Contents

- [JSON/YAML Credential Parsing](#jsonyaml-credential-parsing)
- [Cross-Account Sync](#cross-account-sync)
- [kmsEndpoint Configuration](#kmsendpoint-configuration)
- [Credential Rotation](#credential-rotation)
- [Multi-Data Source Support](#multi-data-source-support)
- [Sync Failure Handling Semantics](#sync-failure-handling-semantics)

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
| `extract.name` | No | Not used by the controller | Optional field; only takes effect in the `spec.data[].name` non-jmesPath scenario, the controller does not use this field on the extract path; Secret data keys default to the top-level keys of the parsed JSON/YAML, and when `extract.jmesPath` is configured, keys are determined by each entry's `objectAlias`; keys can be renamed via `replaceRule` |
| `extract.versionId` | No | Fetches latest version | KMS credential version |
| `extract.kmsEndpoint` | No | Uses global configuration | KMS service address |
| `extract.secretStoreRef` | No | Uses default values | SecretStore reference |
| `replaceRule[].source` | No | No replacement | Regex pattern to match keys that need renaming |
| `replaceRule[].target` | No | — | Replacement key name |

> **Notes**:
> - `extract` flattens all fields in the JSON credential into key-value pairs stored directly in the K8s Secret, no need to know the JSON structure in advance
> - `replaceRule` solves the problem where KMS credential keys contain illegal characters (e.g., `.`, `/`) that prevent writing to K8s Secret, supports batch key renaming via regex matching
> - On the extract path, in addition to renaming keys (regex matching), `replaceRule` also performs literal replacement on the values of Secret data
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

> **Note**: `remoteRamRoleSessionName` and `ramRoleSessionName` are both optional; when omitted, the component default session name `ack-secret-manager` is used.

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

## Sync Failure Handling Semantics

### Feature Description

When fetching credentials from backend data sources (KMS/OOS), some or all data sources may fail due to transient errors (rate limiting, 5xx responses, network jitter, etc.) or persistent errors (credentials not found, insufficient permissions, etc.). Starting from v0.6.5, the controller protects the cluster Secret based on the failure scope and whether template processing is configured, avoiding writing incomplete or incorrect data, and also avoiding accidental deletion or clearing of still-valid Secrets.

Transient errors are retried in a unified manner before being reported as failures: bounded retry count, exponential backoff with jitter, and retries can be canceled at any time during graceful shutdown of the controller. Each data source entry makes at most 3 cloud API calls on a transient failure (backoff intervals of roughly 2s/4s with ±20% jitter); the retry count is currently not configurable. Failures visible in `status.dataSyncResults` are all final errors after retries are exhausted.

### Failure Handling Truth Table

| Failure Scope | Template Processing | cleanupSecretOnFailure | Handling Behavior |
| ------------- | ------------------- | ---------------------- | ----------------- |
| All succeeded | Any | Any | Normal write, behavior unchanged |
| Partial failure | Not configured | Any | **Merge write — only the successful part is updated**: successful keys are written with fresh values; failed keys retain their previous values in the Secret |
| Partial failure | Configured | Any | **Fail-closed**: Secret write is skipped entirely, the previous Secret is retained |
| Total failure | Any | `false` (default) | Secret write is skipped, the previous Secret is retained |
| Total failure | Any | `true` | The cluster Secret is deleted |

**Key points**:

- **Partial failure** means some data sources succeeded and some failed; **total failure** means all configured data sources (`spec.data[]` + `spec.dataProcess[]`) failed.
- In plain terms, on a partial failure the controller **only updates the part that was fetched successfully**: each successful key is written with its fresh value, while each failed key keeps its previous value in the Secret, so failed keys are never lost or overwritten.
- The merge write derives the retention key set using exactly the same key-naming rules as the success path (`name`, or the `key` fallback, or `jmesPath` alias).
- **Conservative retention for failed `dataProcess[].extract` entries**: when a failed entry is `dataProcess[].extract`, the controller cannot statically infer which keys it "would have produced", so it conservatively retains **all** keys in the Secret that are not covered by successful entries. This conservative superset may include stale keys of entries that have already been removed from the spec — such keys have no corresponding failure record in `status.dataSyncResults` because the entry no longer exists. This only occurs when an entry removal and an extract failure happen to overlap in time, and the leftover keys are cleared automatically on the next fully successful round. If the failing entry persists, the retained keys stay retained — this is expected behavior.
- When the Secret does not exist yet, there are no previous values to retain: a partial-failure round only writes the successful keys.
- **Duplicate backend key exemption**: when the same backend key is referenced by multiple spec entries and, in one round, one of them succeeds while another fails, the controller treats the data as available for this round: the failure is exempt from the failure statistics and is not reported as `Failed` in `status.dataSyncResults` (no duplicate failure reporting). The write/deletion contracts above are not affected by this exemption.
- The deletion on total failure with `cleanupSecretOnFailure=true` also applies to templated ExternalSecrets; template-rendered static content is never written in that case. On partial failures the Secret is never deleted, regardless of this flag.
- `cleanupSecretOnFailure` defaults to `false` (flag `--cleanup-secret-on-failure`, Helm value `command.cleanupSecretOnFailure`).
- **Zero-output guard (fail-closed)**: an error-free round that produces 0 keys — e.g. a `dataProcess[].extract` whose backend document has been emptied (`{}` or empty string), or an ExternalSecret declaring neither data sources nor a template — does NOT write an empty dataset: doing so would delete the existing Secret with `cleanupSecretOnFailure=true` or clear its Data otherwise. The write is skipped, the deletion is withheld, and `status.dataSyncResults` reports a `zero_output_guard` entry. Consequently, intentionally emptying a backend document no longer clears/deletes the cluster Secret; the Secret is retained until a round produces keys again. The guard fires only on error-free rounds, so it never preempts the failure contracts above. A **post-template counterpart** covers the same gap after rendering: when every data source succeeded and the source data was non-empty, but template rendering produces 0 data keys (Replace mode with every inline data template failing execution, or a Data-targeted `templateFrom` rendering zero valid keys), the write is skipped and deletion withheld as well, reporting a `template_zero_output_guard` entry.
- **Known limitations of the zero-output protection**: (a) when only SOME entries among multiple `dataProcess[].extract` produce zero keys, their keys are removed as part of the normal all-success semantics — this is not guarded; (b) when template rendering partially fails in Replace mode (some data templates fail execution while others succeed), the reduced dataset containing only the successfully rendered keys is written — this preserves the established non-fatal semantics for template execution errors and is only surfaced as a warning in the controller logs.

### Behavior Changes vs. Previous Versions

| Scenario | Behavior before v0.6.5 | Behavior since v0.6.5 |
| -------- | ---------------------- | --------------------- |
| Partial failure without template | The Secret was overwritten wholesale with only the successfully fetched values (the previous values of failed keys were lost) | Merge write — **only the successful part is updated**: successful keys get fresh values, failed keys retain their previous values |
| Partial failure with template | Partial data could be rendered into an incorrect Secret | Fail-closed: write skipped, previous Secret retained |
| Total failure with `cleanupSecretOnFailure=false` | Secret was cleared (written with empty data) | Previous Secret is retained |
| Total failure with `cleanupSecretOnFailure=true` | Secret deleted | Secret deleted (now also applies to templated ExternalSecrets) |
| All succeeded but 0 keys produced (e.g. backend document emptied) | The Secret was silently cleared (or deleted with `cleanupSecretOnFailure=true`) | Fail-closed: write skipped, existing Secret retained, `zero_output_guard` reported |

### Failure Visibility and Status Semantics

- Sync failures are recorded in `status.dataSyncResults` (key, status, reason). Failures do not change the polling frequency, nor do they speed up or slow down subsequent sync polling.
- When a sync round produces no data at all and the write is skipped, `status.dataSyncResults` shows an entry with Status `Failed` whose key is a synthetic identifier (not a backend secret key); the Reason explains why the write was skipped.
- For retryable transient errors (5xx / 429 / request timeout / connection reset), the failure result may take several seconds (roughly 3–10 seconds) to surface in `status`; this is expected behavior of the retry mechanism.
- When automatic polling is disabled (`--disable-polling` / `command.disablePolling`), a single sync failure is not retried automatically; sync is only attempted again when the spec of that ExternalSecret changes. Transient retries within a single sync are unaffected by this.
- `status.dataSyncResults` is updated when the sync result semantics (per-key status/reason, or the overall success state) change, and the sync timestamp is forcibly refreshed after the controller actually writes to (or deletes) the target Secret. `synchronizationTime` reflects when the currently reported result was recorded and is **not** refreshed on every sync polling round: on steady-state polling rounds where the fetched data is unchanged and no Secret write occurs, it keeps the time of the last successful synchronization — do not use it as a liveness heartbeat.

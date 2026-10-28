# E2E Test Suite

End-to-end tests for ACK Secret Manager. The suite uses **Ginkgo v2 + Gomega**
and requires a real Alibaba Cloud ACK cluster to run.

## File Categories

### Infrastructure files (no Describe/It blocks)

| File | Role |
|------|------|
| `suite_test.go` | Ginkgo bootstrap (`TestE2E`), `BeforeSuite`/`AfterSuite`, global variables, and shared low-level helpers (namespace lifecycle, store readiness waiters, cleanup utilities). |
| `resource_manager_test.go` | Cloud-resource fixture management — KMS secrets, OOS parameters, RAM roles/policies, ACK node-pool labels. |
| `helpers_test.go` | Reusable assertion/wait helpers shared across spec files (sync-time comparison, ExternalSecret status checks, SecretStore mutation utilities, ClientGeneration assertions). |

### Spec files (contain Describe/It blocks)

| File | Coverage area |
|------|---------------|
| `template_test.go` | Template processing (Go templates, Sprig functions, TemplateFrom, custom functions). |
| `advanced_template_test.go` | Advanced template scenarios (JMESPath, merge policies, multi-key templates). |
| `reconcile_test.go` | ExternalSecret reconcile lifecycle, SecretStore credential/SA watches, namespace scoping. |
| `cluster_external_secret_test.go` | ClusterExternalSecret provisioning, namespace watch, cleanup contract, fail-closed matching, auto-disable. |
| `cross_namespace_ref_test.go` | Cross-namespace SecretStore references and `--enable-cross-namespace-*` flag gating. |
| `match_conditions_test.go` | Namespace matching conditions (label selectors, regex, substring). |
| `data_fetch_test.go` | Secret data fetching, key remapping, and multi-key retrieval. |
| `gateway_test.go` | Secret gateway integration. |
| `provider_test.go` | Provider-specific scenarios (KMS, OOS). |
| `store_watch_test.go` | SecretStore/ClusterSecretStore spec-change watch and reverse-watch chain. |
| `cross_account_sync_test.go` | Cross-account secret synchronization. |
| `partial_failure_test.go` | Partial failure handling. |
| `authentication_test.go` | Authentication methods (RRSA, AK/SK, ServiceAccount, WorkerRole). |
| `polling_flags_test.go` | Polling interval flag behavior. |
| `cluster_store_flag_test.go` | `--process-cluster-secret-store` flag behavior. |
| `key_conflict_test.go` | Key conflict detection and resolution across multiple data entries. |
| `leader_election_test.go` | Leader election and high-availability behavior. |

## Running

```bash
# Compile-only check (no cluster required):
go vet ./test/e2e/...

# Full run (requires cluster connectivity + env vars):
make test-e2e

# Template processing subset only:
make test-e2e-template

# Or directly with Ginkgo CLI:
ginkgo -v -timeout 3h ./test/e2e
```

## Environment Variables

All variables are available in the template file
[e2e-env.sh.template](e2e-env.sh.template). Copy it to `e2e-env.sh`, fill in the
values, then source it before running tests:

```bash
cp test/e2e/e2e-env.sh.template test/e2e/e2e-env.sh
# edit e2e-env.sh
source test/e2e/e2e-env.sh
make test-e2e
```

### Required variables

| Variable | Purpose | Missing behavior |
|----------|---------|------------------|
| `ALIBABA_CLOUD_ACCOUNT_ID` | Alibaba Cloud main account ID — used to compose RAM role ARNs, OIDC Provider ARNs, etc. | BeforeSuite fails |
| `CLUSTER_ID` | ACK cluster ID — used for WorkerRole/VPC lookup and OIDC Provider name `ack-rrsa-{clusterID}` | BeforeSuite fails |
| `REGION` | Region for KMS/OOS/CS endpoints | BeforeSuite fails |
| `KMS_KEY_ID` | KMS symmetric key ID for creating test credentials | BeforeSuite fails |
| `KMS_INSTANCE_ID` | KMS software secret instance ID for dedicated gateway endpoints | BeforeSuite fails |

### Optional variables

| Variable | Purpose | Missing behavior |
|----------|---------|------------------|
| `KUBECONFIG` | Cluster access config | Falls back to `~/.kube/config`, then InClusterConfig |
| `ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET` | OpenAPI credentials for the test process itself (RAM/KMS/OOS/CS calls) | Resolved via credentials-go default chain |
| `CROSS_ACCOUNT_ID`, `CROSS_ACCOUNT_ACCESS_KEY_ID`, `CROSS_ACCOUNT_ACCESS_KEY_SECRET` | Cross-account target (all three must be set together) | Cross-account tests are skipped |
| `CROSS_ACCOUNT_KMS_KEY_ID` / `CROSS_ACCOUNT_KMS_INSTANCE_ID` | Required in true cross-account mode for creating KMS secrets in the target account | BeforeSuite fails when `CROSS_ACCOUNT_*` is set but these are missing |

> **Credential chain note:** The test process uses credentials-go
> (`credential.NewCredential(nil)`) with the following lookup order: AK/STS env
> vars → OIDC env vars → aliyun CLI config `~/.aliyun/config.json` →
> `~/.alibabacloud/credentials` → ECS instance metadata RAM Role → credential
> URI. Set `ALIBABA_CLOUD_CLI_PROFILE_DISABLED=true` to skip the CLI profile if
> needed. The credentials must have KMS (`CreateSecret`/`PutSecretValue`/
> `DeleteSecret`/`UpdateKmsInstanceBindVpc`), OOS, RAM management, and CS
> `DescribeClusterDetail` permissions.

## Prerequisites

### Cluster & deployment

- **Real ACK cluster required** — tests depend on RRSA/OIDC, WorkerRole, and node
  metadata services; local clusters (kind/minikube) are not supported.
- **`kube-system/ack-secret-manager` Deployment must be deployed** — the suite
  only Gets the Deployment; it does not install it. Missing → Fail.
- **4 CRDs must be pre-installed**: `SecretStore`, `ExternalSecret`,
  `ClusterSecretStore`, `ClusterExternalSecret`. Missing → Fail with
  `please install CRDs first`.
- Default Deployment parameters are sufficient. The suite auto-patches the
  Deployment to inject the RRSA projected serviceAccountToken volume and cleans
  up in AfterSuite. Some tests (e.g. `cross_namespace_ref_test.go`) temporarily
  patch additional flags and restore them afterwards.

### Cloud resources

- **KMS software secret instance and a symmetric key** must exist beforehand
  (`KMS_INSTANCE_ID` / `KMS_KEY_ID`).

  > **Warning:** Tests modify the VPC binding of the KMS instance and clear it
  > on teardown. **Do not use a production KMS instance that is actively bound.**

- The following resources are **auto-created and auto-cleaned** (prefix
  `acksm-test-`): ~26 KMS secrets, 1 OOS encrypted parameter, multiple RAM
  users/roles/policies, and temporary KMS/OOS/STS permissions appended to the
  cluster WorkerRole.

### Authentication prerequisites

| Auth method | Prerequisite |
|-------------|-------------|
| ServiceAccount RRSA | Cluster must have RRSA enabled (OIDC Provider `ack-rrsa-{CLUSTER_ID}` exists) |
| ENV RRSA | Same as above |
| AK Basic | No extra prerequisite — test creates RAM user automatically |
| AK + AssumeRole | No extra prerequisite — test creates RAM user and role automatically |
| WorkerRole | Real ACK cluster WorkerRole + node metadata service; Deployment needs `--enable-worker-role=true` (default off) |
| Cross-account | Set `CROSS_ACCOUNT_*` vars; true cross-account mode also needs `CROSS_ACCOUNT_KMS_*`. Tests skip when unset, BeforeSuite fails when partially set |

## Toolchain

| Tool | Requirement | Notes |
|------|-------------|-------|
| Go | See [go.mod](../../go.mod) | |
| kubectl / helm | Optional | Only for pre-deploying the controller and CRDs; not directly used by test code |

Run `go mod download` before the first execution to warm up dependencies.

## Pre-run Checklist

- [ ] Go version meets requirement (`go version`)
- [ ] kubeconfig can access the target ACK cluster
- [ ] `kube-system/ack-secret-manager` Deployment is deployed and running
- [ ] 4 CRDs installed (SecretStore / ExternalSecret / ClusterSecretStore / ClusterExternalSecret)
- [ ] Cluster has RRSA enabled (OIDC Provider `ack-rrsa-{CLUSTER_ID}` exists)
- [ ] Required env vars set: `ALIBABA_CLOUD_ACCOUNT_ID`, `CLUSTER_ID`, `REGION`
- [ ] Required KMS vars set: `KMS_KEY_ID`, `KMS_INSTANCE_ID` (use a dedicated test instance, not production)
- [ ] OpenAPI credentials available (AK env vars or credentials-go default chain with sufficient permissions)
- [ ] (Optional) Cross-account vars configured for cross-account tests

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
| `reconcile_test.go` | ExternalSecret reconcile lifecycle, SecretStore credential/SA watches, namespace scoping. |
| `cluster_external_secret_test.go` | ClusterExternalSecret provisioning, namespace watch, cleanup contract, fail-closed matching, auto-disable. |
| `match_conditions_test.go` | Namespace matching conditions (label selectors, regex, substring). |
| `gateway_test.go` | Secret gateway integration. |
| `provider_test.go` | Provider-specific scenarios (KMS, OOS). |
| `store_watch_test.go` | SecretStore/ClusterSecretStore spec-change watch and reverse-watch chain. |
| `cross_account_sync_test.go` | Cross-account secret synchronization. |
| `partial_failure_test.go` | Partial failure handling. |
| `authentication_test.go` | Authentication methods (RRSA, AK/SK, ServiceAccount). |
| `polling_flags_test.go` | Polling interval flag behavior. |
| `cluster_store_flag_test.go` | `--process-cluster-secret-store` flag behavior. |

## Running

```bash
# Compile-only check (no cluster required):
go vet ./test/e2e/...

# Full run (requires cluster connectivity + env vars):
make test-e2e-template
```

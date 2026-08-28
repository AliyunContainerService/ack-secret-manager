# ACK Secret Manager Authentication Guide

> This document describes the various authentication methods supported by ack-secret-manager and their configuration.

## Table of Contents

- [Authentication Methods Overview](#authentication-methods-overview)
- [Authentication Priority](#authentication-priority)
- [Namespace Rules](#namespace-rules)
- [ServiceAccount RRSA Authentication](#1-serviceaccount-rrsa-authentication-recommended)
- [RRSA Authentication](#2-rrsa-authentication)
- [AK AssumeRole Authentication](#3-ak-assumerole-authentication)
- [AK Authentication](#4-ak-authentication)
- [WorkerRole Authentication](#5-workerrole-authentication)
- [Best Practices](#authentication-best-practices)

## Authentication Methods Overview

ack-secret-manager supports the following 5 authentication methods:

| Method              | Configuration        | Use Case                  | Security   | Complexity |
| ------------------- | -------------------- | ------------------------- | ---------- | ---------- |
| ServiceAccount RRSA | SecretStore          | Multi-tenant, production (recommended) | ⭐⭐⭐⭐⭐ | Medium     |
| RRSA                | Env Vars / SecretStore | Cluster-wide, quick deploy | ⭐⭐⭐⭐   | Low        |
| AK AssumeRole       | Env Vars / SecretStore | Temporary credentials, permission narrowing | ⭐⭐⭐     | Medium     |
| AK                  | Env Vars / SecretStore | Test/dev (not recommended for production) | ⭐         | Low        |
| WorkerRole          | Explicit opt-in (`--enable-worker-role=true`) | Simple deployment      | ⭐⭐⭐     | Low        |

> **Notes**:
>
> - **Environment Variables** method: Authentication parameters are injected through Helm values or helm --set, applied at the component level, ExternalSecret doesn't need to specify `secretStoreRef`
> - **SecretStore** method: Configured through SecretStore/ClusterSecretStore CRD, ExternalSecret references via `secretStoreRef`
> - **ServiceAccount RRSA** only supports SecretStore method (referenced via `serviceAccountRef`)
> - **WorkerRole** uses the node ECS's RAM Role; it must be explicitly enabled (`--enable-worker-role=true` / `command.enableWorkerRole=true`). When it is disabled and no other authentication tier is usable, synchronization fails fail-closed (error: "no usable authentication tier")

## Authentication Priority

Whether using environment variables or SecretStore, the authentication chain priority is the same. When multiple authentication methods are configured simultaneously, ack-secret-manager selects them in the following priority:

```
Priority 1 (highest): ServiceAccount RRSA (SecretStore serviceAccountRef)
    ↓
Priority 2: RRSA (OIDC Token)
    ↓
Priority 3: AK AssumeRole (AK + AssumeRole)
    ↓
Priority 4: AK (Pure AccessKey)
    ↓
Priority 5: WorkerRole (ECS RAM Role)
```

| Priority | Method | Config Method | Trigger Condition | Description |
| ---------- | -------------- | ---------------------- | ------------------------------------------------------------ | ------------------------------------------------------------ |
| 1 (highest) | ServiceAccount RRSA | SecretStore | SecretStore configured with `serviceAccountRef` | Controller reads target ServiceAccount annotations, obtains credentials via OIDC dynamic token, highest security |
| 2 | RRSA | Env Vars / SecretStore | `ramRoleARN` + `oidcProviderARN` configured | Uses component-level OIDC token for temporary credentials |
| 3 | AK AssumeRole | Env Vars / SecretStore | `accessKey` + `accessKeySecret` + `ramRoleARN` configured (`ramRoleSessionName` is optional) | Uses AK to log in, then AssumeRole for temporary credentials |
| 4 | Pure AK | Env Vars / SecretStore | Only `accessKey` + `accessKeySecret` configured | Uses static AK/SK directly |
| 5 (lowest) | WorkerRole | Explicit opt-in | Explicitly enabled via `--enable-worker-role=true` / `command.enableWorkerRole=true`; when disabled and no other usable tier exists, fails fail-closed (error: "no usable authentication tier") | Uses the node ECS instance's RAM Role |

**Important Notes**:

- The authentication chain **selects an authentication tier** in priority order: it only moves to the next tier when the current tier's prerequisites are missing (the tier is not enabled); a credential retrieval failure (e.g., missing token file, STS error) does NOT fall back to the next tier — the chain stays on the failed tier and reports an error
- **Once credentials are successfully retrieved, even if they lack permissions for the target API (e.g., KMS 403), the chain will NOT fall back to the next authentication method**
- When multiple authentication fields are configured simultaneously (e.g., both `RRSA` and `AK`), only **one** method takes effect based on the priority table above, i.e., `RRSA` authentication will be used
- ServiceAccount RRSA uses controller-driven dynamic tokens per referenced ServiceAccount, providing ServiceAccount-level fine-grained permission isolation, highest security

### Mutual Exclusion Between Environment Variables and SecretStore

Each `DataSource` in an ExternalSecret determines its authentication path based on whether `secretStoreRef` is configured. **The two paths are mutually exclusive — there is no priority stacking or fallback behavior**:

| `secretStoreRef` | Authentication Path | Description |
| ----------------- | ------------------- | ----------- |
| Not configured (nil) | Environment variable authentication | Uses the lazily registered (on first use) global ENV client, all DataSources share the same credentials |
| Configured | SecretStore authentication | Uses the authentication method configured in SecretStore, environment variables are completely ignored |

> **Example**: Even if the Deployment is configured with RRSA environment variables (`ALICLOUD_ROLE_ARN`) and a SecretStore is configured with AK authentication, as long as the ExternalSecret's `DataSource` references that SecretStore, the SecretStore's AK authentication takes effect and the environment variables are not used.

## Namespace Rules

By default, both `command.enableCrossNamespaceSecretStore` and `command.enableCrossNamespaceAuthRef` are set to `false`, **disabling cross-namespace references by default** for security. To enable cross-namespace references, set the corresponding parameter to `true`.

**When Using SecretStore**:

| Referrer           | Target Resource    | Namespace Field               | Control Parameter                         | When Namespace Not Set               | When Namespace Set                                         |
| ------------------ | ------------------ | ----------------------------- | ----------------------------------------- | ------------------------------------ | ---------------------------------------------------------- |
| ExternalSecret     | SecretStore        | `secretStoreRef.namespace`    | `command.enableCrossNamespaceSecretStore` | Reference SecretStore in same namespace | Reference SecretStore in specified namespace (requires `true`) |
| SecretStore        | ServiceAccount     | `serviceAccountRef.namespace` | `command.enableCrossNamespaceAuthRef`     | Reference ServiceAccount in same namespace | Reference ServiceAccount in specified namespace (requires `true`) |
| SecretStore        | Secret             | `secretRef.namespace`         | `command.enableCrossNamespaceAuthRef`     | Reference Secret in same namespace     | Reference Secret in specified namespace (requires `true`)  |

> When the control parameter is set to `false`, even if the namespace field is set, only same-namespace resources can be referenced.

**When Using ClusterSecretStore**:

| Referrer           | Target Resource    | Namespace Field               | Description                                    |
| ------------------ | ------------------ | ----------------------------- | ---------------------------------------------- |
| ExternalSecret     | ClusterSecretStore | None (cluster-scoped resource) | Can be referenced from any namespace, must satisfy `conditions` configuration |
| ClusterSecretStore | ServiceAccount     | `serviceAccountRef.namespace` | **Required**, can point to any namespace       |
| ClusterSecretStore | Secret             | `secretRef.namespace`         | **Required**, can point to any namespace       |

## 1. ServiceAccount RRSA Authentication (Recommended)

### 1.1 Use Cases

- ✅ Multi-tenant applications (independent permissions per tenant)
- ✅ Environment separation (dev/staging/prod)
- ✅ Compliance requirements (ServiceAccount-level access control)
- ✅ Production workloads

### 1.2 Configuration Steps

1. **Enable RRSA**: Refer to [ACK RRSA Documentation](https://help.aliyun.com/document_detail/356611.html) to enable the cluster RRSA feature
2. **Create RAM Role**: Type **Identity Provider**, trust policy `oidc:sub` must match the ServiceAccount full path (`system:serviceaccount:<namespace>:<name>`)
3. **Authorize RAM Role** with KMS access permissions (`kms:GetSecretValue`, `kms:Decrypt`)
4. **Create ServiceAccount**: Add annotation `ack.alibabacloud.com/role-arn` pointing to the RAM Role ARN
5. **Create SecretStore**: Configure `serviceAccountRef` to reference the ServiceAccount
6. **Create ExternalSecret**: Reference the SecretStore via `secretStoreRef`

> **Key Points**:
> - `oidc:iss` region must match the cluster region (e.g., `cn-hangzhou`, `cn-beijing`)
> - `oidc:aud` is fixed to `sts.aliyuncs.com`
> - `oidc:sub` must exactly match the ServiceAccount full path

### 1.3 Complete Example

Refer to [examples/auth/auth-01-serviceaccount-rrsa.yaml](../examples/auth/auth-01-serviceaccount-rrsa.yaml)

## 2. RRSA Authentication

RRSA (RAM Roles for Service Accounts) obtains temporary credentials through OIDC Token. Unlike ServiceAccount RRSA, RRSA is configured at the **component level**, and all ExternalSecrets share the same credentials.

### 2.1 Use Cases

- ✅ All applications in the cluster share the same KMS permissions
- ✅ Quick deployment, no need to configure per namespace
- ⚠️ Not suitable for multi-tenant scenarios (coarse permission isolation)

### 2.2 Configuration Steps

1. **Enable RRSA**: Refer to [ACK RRSA Documentation](https://help.aliyun.com/document_detail/356611.html)
2. **Create RAM Role**: Type **Identity Provider**, `oidc:sub` points to the component ServiceAccount (default `system:serviceaccount:kube-system:ack-secret-manager`)
3. **Authorize RAM Role** with KMS access permissions
4. **Create Secret**: containing `rolearn` and `oidcproviderarn`
5. **Configure authentication** (choose one):
   - **Method A (Environment Variables)**: Inject `ALICLOUD_ROLE_ARN` and `ALICLOUD_OIDC_PROVIDER_ARN` via `envVarsFromSecret` in Helm values, ExternalSecret doesn't need `secretStoreRef`
   - **Method B (SecretStore)**: Create SecretStore with `ramRoleARN` + `oidcProviderARN`, ExternalSecret references via `secretStoreRef`

### 2.3 Complete Example

Refer to [examples/auth/auth-02-rrsa.yaml](../examples/auth/auth-02-rrsa.yaml)

## 3. AK AssumeRole Authentication

AK AssumeRole uses AccessKey + AssumeRole to obtain temporary credentials, more secure than pure AK (temporary credentials are automatically refreshed).

### 3.1 Use Cases

- ✅ Security scenarios requiring temporary credentials
- ✅ Permission narrowing (limit access scope through Role)

### 3.2 Configuration Steps

1. **Create RAM User**: Check **OpenAPI Call Access**, obtain AccessKey
2. **Create RAM Role**: Type **Alibaba Cloud Account**, trust policy allows RAM User to assume
3. **Authorize RAM Role** with KMS access permissions
4. **Authorize RAM User** with AssumeRole permission
5. **Create Secret**: containing `id`, `secret`, and `rolearn`
6. **Configure authentication** (choose one):
   - **Method A (Environment Variables)**: Inject `ACCESS_KEY_ID`, `SECRET_ACCESS_KEY`, and `ALICLOUD_ROLE_ARN` via `envVarsFromSecret` in Helm values, ExternalSecret doesn't need `secretStoreRef`
   - **Method B (SecretStore)**: Create SecretStore with `accessKey` + `accessKeySecret` + `ramRoleARN` (+ optional `ramRoleSessionName`; when omitted, the component default session name `ack-secret-manager` is used), ExternalSecret references via `secretStoreRef`

### 3.3 Complete Example

Refer to [examples/auth/auth-03-ak-assume-role.yaml](../examples/auth/auth-03-ak-assume-role.yaml)

## 4. AK Authentication

### 4.1 ⚠️ Not Recommended for Production

**Use case**: Test and development environments only

### 4.2 Configuration Steps

1. **Create RAM User**: Check **OpenAPI Call Access**, obtain AccessKey
2. **Authorize RAM User** with KMS access permissions
3. **Create Secret**: containing `id` and `secret`
4. **Configure authentication** (choose one):
   - **Method A (Environment Variables)**: Inject `ACCESS_KEY_ID` and `SECRET_ACCESS_KEY` via `envVarsFromSecret` in Helm values, ExternalSecret doesn't need `secretStoreRef`
   - **Method B (SecretStore)**: Create SecretStore with `accessKey` + `accessKeySecret`, ExternalSecret references via `secretStoreRef`

### 4.3 Complete Example

Refer to [examples/auth/auth-04-ak-basic.yaml](../examples/auth/auth-04-ak-basic.yaml)

## 5. WorkerRole Authentication

### 5.1 Use Cases

- ✅ Simple deployment scenarios
- ✅ No fine-grained access control needed

### 5.2 Configuration Method

1. Log in to the Container Service Console
2. Cluster Details -> Cluster Info -> Basic Info -> Cluster Resources -> Worker RAM Role
3. Add KMS access policy (`kms:GetSecretValue`, `kms:Decrypt`)

### 5.3 command.enableWorkerRole Configuration

`command.enableWorkerRole` controls whether to enable WorkerRole (ECS RAM Role) authentication. The default value is `false`. For ACK clusters (managed, dedicated, edge), set it to `true` explicitly to enable node RAM Role authentication:

| Cluster Type        | Recommended command.enableWorkerRole |
| ------------------- | ------------------------------------ |
| ACK Managed Cluster | true                                 |
| ACK Dedicated Cluster | true                               |
| ACK Edge Cluster    | true                                 |
| Other Clusters      | false                                |

When `enableWorkerRole` is `true`, the component uses the node ECS instance's RAM Role to obtain temporary credentials, no additional authentication configuration needed (the role needs KMS access permissions).

### 5.4 Using ExternalSecret

When using WorkerRole authentication, **no need to create SecretStore** or configure `secretStoreRef`.

### 5.5 Complete Example

Refer to [examples/auth/auth-05-worker-role.yaml](../examples/auth/auth-05-worker-role.yaml)

## Authentication Best Practices

### Security Recommendations

1. ✅ **Prefer ServiceAccount RRSA**: Provides ServiceAccount-level permission isolation, different tenants can reference different ServiceAccounts
2. ✅ **Least Privilege**: RAM Roles should only be granted necessary KMS permissions
3. ✅ **Avoid Hardcoded AK**: Don't use pure AccessKey in production
4. ✅ **Regular Rotation**: Configure reasonable `rotationInterval`

### Multi-Tenant Scenario

```
Each tenant configured independently:
- Independent ServiceAccount (different namespaces)
- Independent SecretStore
- Independent RAM Role (different permission scopes)
```

### Cluster-Wide Scenario

```
Use environment variable RRSA:
- Component-level unified authentication
- All ExternalSecrets share permissions
- Simple configuration but coarse permission isolation
```

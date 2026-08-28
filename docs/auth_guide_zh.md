# ACK Secret Manager 认证配置指南

> 本文档详细说明 ack-secret-manager 支持的各种认证方式及其配置方法。

## 目录

- [认证方式概述](#认证方式概述)
- [认证优先级](#认证优先级)
- [Namespace 规则](#namespace-规则)
- [ServiceAccount RRSA 认证](#1-serviceaccount-rrsa-认证推荐)
- [RRSA 认证](#2-rrsa-认证)
- [AK 扮演认证](#3-ak-扮演认证)
- [AK 认证](#4-ak-认证)
- [WorkerRole 认证](#5-workerrole-认证)
- [认证配置最佳实践](#认证配置最佳实践)

## 认证方式概述

ack-secret-manager 支持以下 5 种认证方式：

| 认证方式            | 配置方式               | 适用场景                 | 安全性     | 配置复杂度 |
| ------------------- | ---------------------- | ------------------------ | ---------- | ---------- |
| ServiceAccount RRSA | SecretStore            | 多租户、生产环境（推荐） | ⭐⭐⭐⭐⭐ | 中         |
| RRSA                | 环境变量 / SecretStore | 集群级共享、快速部署     | ⭐⭐⭐⭐   | 低         |
| AK 扮演             | 环境变量 / SecretStore | 临时凭据、权限收窄       | ⭐⭐⭐     | 中         |
| AK                  | 环境变量 / SecretStore | 测试/开发（不推荐生产）  | ⭐         | 低         |
| WorkerRole          | 显式启用（`--enable-worker-role=true`） | 简单部署场景             | ⭐⭐⭐     | 低         |

> **说明**：
>
> - **环境变量**方式：通过 Helm values 或 helm --set 注入认证参数，组件级别统一生效，ExternalSecret 无需指定 `secretStoreRef`
> - **SecretStore** 方式：通过 SecretStore/ClusterSecretStore CRD 配置，ExternalSecret 通过 `secretStoreRef` 引用
> - **ServiceAccount RRSA** 仅支持 SecretStore 方式（通过 `serviceAccountRef` 引用）
> - **WorkerRole** 使用节点 ECS 的 RAM Role；需显式启用（`--enable-worker-role=true` / `command.enableWorkerRole=true`），未启用且无其他可用认证时按 fail-closed 失败（报错 "no usable authentication tier"）

## 认证优先级

无论是环境变量方式还是 SecretStore 方式，认证链优先级相同。当同时配置多种认证方式时，按以下优先级选择：

```
优先级 1 (最高): ServiceAccount RRSA (SecretStore serviceAccountRef)
    ↓
优先级 2: RRSA (OIDC Token)
    ↓
优先级 3: AK 扮演 (AK + AssumeRole)
    ↓
优先级 4: AK (纯 AccessKey)
    ↓
优先级 5: WorkerRole (ECS RAM Role)
```

| 优先级 | 认证方式 | 支持的配置方式 | 触发条件 | 说明 |
| -------- | -------------- | ---------------------- | ------------------------------------------------------------ | ------------------------------------------------------------ |
| 1（最高） | ServiceAccount RRSA | SecretStore | SecretStore 配置了 `serviceAccountRef` | 控制器读取目标 ServiceAccount 注解，通过 OIDC 动态 Token 获取凭据，安全性最高 |
| 2 | RRSA | 环境变量 / SecretStore | 配置了 `ramRoleARN` + `oidcProviderARN` | 使用组件级 OIDC Token 获取临时凭据 |
| 3 | AK 扮演 | 环境变量 / SecretStore | 配置了 `accessKey` + `accessKeySecret` + `ramRoleARN`（`ramRoleSessionName` 可选） | 使用 AK 登录后 AssumeRole 获取临时凭据 |
| 4 | 纯 AK | 环境变量 / SecretStore | 仅配置 `accessKey` + `accessKeySecret` | 直接使用静态 AK/SK |
| 5（最低） | WorkerRole | 显式启用 | 通过 `--enable-worker-role=true` / `command.enableWorkerRole=true` 显式启用；未启用且无其他可用认证时按 fail-closed 失败（报错 "no usable authentication tier"） | 使用节点 ECS 的 RAM Role |

**重要说明**：

- 认证链按优先级依次**选择认证档**：仅当某档前置条件缺失（该档未启用）时才跳到下一档；凭据获取失败（如 token 文件缺失、STS 报错）不会回退到下一档，认证链将停留在该失败档并报错
- **一旦凭据获取成功，即使该凭据没有目标 API 的权限（如 KMS 403），也不会回退到认证链的下一种认证**
- 当同时配置了多种认证字段时（例如同时配置 `RRSA` 和 `AK`），按上表优先级选择**唯一生效**的认证方式即会使用`RRSA`认证
- ServiceAccount RRSA 由控制器读取目标 ServiceAccount 注解，通过 OIDC 动态 Token 获取凭据，提供 ServiceAccount 维度的细粒度权限隔离，安全性最高

### 环境变量与 SecretStore 的互斥关系

ExternalSecret 的每个 `DataSource` 根据是否配置 `secretStoreRef` 决定认证路径，**两者互斥，不存在优先级叠加或 fallback 行为**：

| `secretStoreRef` | 认证路径 | 说明 |
| ----------------- | -------- | ---- |
| 未配置（nil）     | 环境变量认证 | 使用首次使用时惰性注册的全局 ENV client，所有 DataSource 共享同一凭据 |
| 已配置            | SecretStore 认证 | 使用 SecretStore 中配置的认证方式，环境变量被完全忽略 |

> **示例**：即使 Deployment 配置了 RRSA 环境变量（`ALICLOUD_ROLE_ARN`），同时 SecretStore 配置了 AK 认证，只要 ExternalSecret 的 `DataSource` 引用了该 SecretStore，最终生效的是 SecretStore 的 AK 认证，环境变量不参与。

## Namespace 规则

默认情况下，`command.enableCrossNamespaceSecretStore` 和 `command.enableCrossNamespaceAuthRef` 均为 `false`，**默认禁止跨 namespace 引用**。如需开启跨 namespace 引用，请将对应参数设置为 `true`。

**使用 SecretStore 时**：

| 引用方         | 目标资源       | Namespace 字段                | 控制参数                                  | Namespace 字段未设置时             | Namespace 字段设置时                                   |
| -------------- | -------------- | ----------------------------- | ----------------------------------------- | ---------------------------------- | ------------------------------------------------------ |
| ExternalSecret | SecretStore    | `secretStoreRef.namespace`    | `command.enableCrossNamespaceSecretStore` | 引用同 namespace 的 SecretStore    | 引用指定 namespace 的 SecretStore（需参数为`true`）    |
| SecretStore    | ServiceAccount | `serviceAccountRef.namespace` | `command.enableCrossNamespaceAuthRef`     | 引用同 namespace 的 ServiceAccount | 引用指定 namespace 的 ServiceAccount（需参数为`true`） |
| SecretStore    | Secret         | `secretRef.namespace`         | `command.enableCrossNamespaceAuthRef`     | 引用同 namespace 的 Secret         | 引用指定 namespace 的 Secret（需参数为`true`）         |

> 当控制参数设置为 `false` 时，即使设置了 namespace 字段，也只能引用同 namespace 的资源。

**使用 ClusterSecretStore 时**：

| 引用方             | 目标资源           | Namespace 字段                | 说明                                           |
| ------------------ | ------------------ | ----------------------------- | ---------------------------------------------- |
| ExternalSecret     | ClusterSecretStore | 无（集群级别资源）            | 任意 namespace 可引用，需满足`conditions` 配置 |
| ClusterSecretStore | ServiceAccount     | `serviceAccountRef.namespace` | **必填**，可指向任意 namespace                 |
| ClusterSecretStore | Secret             | `secretRef.namespace`         | **必填**，可指向任意 namespace                 |

## 1. ServiceAccount RRSA 认证（推荐）

### 1.1 适用场景

- ✅ 多租户应用（每个租户独立权限）
- ✅ 环境分离（dev/staging/prod）
- ✅ 合规要求（ServiceAccount 级别访问控制）
- ✅ 生产环境工作负载

### 1.2 配置步骤

1. **启用 RRSA**：参考 [ACK RRSA 文档](https://help.aliyun.com/document_detail/356611.html) 启用集群 RRSA 功能
2. **创建 RAM Role**：类型选择**身份提供商**，信任策略中 `oidc:sub` 必须与 ServiceAccount 完整路径匹配（`system:serviceaccount:<namespace>:<name>`）
3. **为 RAM Role 授权** KMS 访问权限（`kms:GetSecretValue`、`kms:Decrypt`）
4. **创建 ServiceAccount**：添加注解 `ack.alibabacloud.com/role-arn` 指向 RAM Role ARN
5. **创建 SecretStore**：配置 `serviceAccountRef` 引用上述 ServiceAccount
6. **创建 ExternalSecret**：通过 `secretStoreRef` 引用 SecretStore

> **关键**：
> - `oidc:iss` 中的 region 需与集群所在地域匹配（如 `cn-hangzhou`、`cn-beijing`）
> - `oidc:aud` 固定为 `sts.aliyuncs.com`
> - `oidc:sub` 必须与 ServiceAccount 的完整路径完全匹配

### 1.3 完整示例

请参考 [examples/auth/auth-01-serviceaccount-rrsa.yaml](../examples/auth/auth-01-serviceaccount-rrsa.yaml)

## 2. RRSA 认证

RRSA（RAM Roles for Service Accounts）通过 OIDC Token 获取临时凭据。与 ServiceAccount RRSA 不同，RRSA 在**组件级别**配置，所有 ExternalSecret 共享同一凭据。

### 2.1 适用场景

- ✅ 集群所有应用共享相同 KMS 权限
- ✅ 快速部署，无需为每个 namespace 配置
- ⚠️ 不适合多租户场景（权限隔离粒度粗）

### 2.2 配置步骤

1. **启用 RRSA**：参考 [ACK RRSA 文档](https://help.aliyun.com/document_detail/356611.html) 启用集群 RRSA 功能
2. **创建 RAM Role**：类型选择**身份提供商**，信任策略中 `oidc:sub` 指向组件 ServiceAccount（默认 `system:serviceaccount:kube-system:ack-secret-manager`）
3. **为 RAM Role 授权** KMS 访问权限
4. **创建 Secret**：包含 `rolearn` 和 `oidcproviderarn`
5. **配置认证**（两种方式任选）：
   - **方式 A（环境变量）**：在 Helm values 中通过 `envVarsFromSecret` 注入 `ALICLOUD_ROLE_ARN` 和 `ALICLOUD_OIDC_PROVIDER_ARN`，ExternalSecret 无需 `secretStoreRef`
   - **方式 B（SecretStore）**：创建 SecretStore 配置 `ramRoleARN` + `oidcProviderARN`，ExternalSecret 通过 `secretStoreRef` 引用

### 2.3 完整示例

请参考 [examples/auth/auth-02-rrsa.yaml](../examples/auth/auth-02-rrsa.yaml)

## 3. AK 扮演认证

AK 扮演使用 AccessKey + AssumeRole 获取临时凭据，相比纯 AK 更安全（临时凭据自动刷新）。

### 3.1 适用场景

- ✅ 需要临时凭据的安全场景
- ✅ 权限收窄（通过 Role 限制访问范围）

### 3.2 配置步骤

1. **创建 RAM User**：勾选 **OpenAPI 调用访问**，获取 AccessKey
2. **创建 RAM Role**：类型选择**阿里云账号**，信任策略允许 RAM User 扮演
3. **为 RAM Role 授权** KMS 访问权限
4. **为 RAM User 授权** AssumeRole 权限
5. **创建 Secret**：包含 `id`、`secret` 和 `rolearn`
6. **配置认证**（两种方式任选）：
   - **方式 A（环境变量）**：在 Helm values 中通过 `envVarsFromSecret` 注入 `ACCESS_KEY_ID`、`SECRET_ACCESS_KEY` 和 `ALICLOUD_ROLE_ARN`，ExternalSecret 无需 `secretStoreRef`
   - **方式 B（SecretStore）**：创建 SecretStore 配置 `accessKey` + `accessKeySecret` + `ramRoleARN`（`ramRoleSessionName` 可选，不填时使用组件默认会话名 `ack-secret-manager`），ExternalSecret 通过 `secretStoreRef` 引用

### 3.3 完整示例

请参考 [examples/auth/auth-03-ak-assume-role.yaml](../examples/auth/auth-03-ak-assume-role.yaml)

## 4. AK 认证

### 4.1 ⚠️ 不推荐用于生产环境

**使用场景**：仅限测试和开发环境

### 4.2 配置步骤

1. **创建 RAM User**：勾选 **OpenAPI 调用访问**，获取 AccessKey
2. **为 RAM User 授权** KMS 访问权限
3. **创建 Secret**：包含 `id` 和 `secret`
4. **配置认证**（两种方式任选）：
   - **方式 A（环境变量）**：在 Helm values 中通过 `envVarsFromSecret` 注入 `ACCESS_KEY_ID` 和 `SECRET_ACCESS_KEY`，ExternalSecret 无需 `secretStoreRef`
   - **方式 B（SecretStore）**：创建 SecretStore 配置 `accessKey` + `accessKeySecret`，ExternalSecret 通过 `secretStoreRef` 引用

### 4.3 完整示例

请参考 [examples/auth/auth-04-ak-basic.yaml](../examples/auth/auth-04-ak-basic.yaml)

## 5. WorkerRole 认证

### 5.1 适用场景

- ✅ 简单部署场景
- ✅ 不需要细粒度权限控制

### 5.2 配置方法

1. 登录容器服务控制台
2. 集群详情 -> 集群信息 -> 基本信息 -> 集群资源 -> Worker RAM 角色
3. 添加 KMS 访问权限策略（`kms:GetSecretValue`、`kms:Decrypt`）

### 5.3 command.enableWorkerRole 配置

`command.enableWorkerRole` 用于控制是否启用 WorkerRole（ECS RAM Role）认证方式，默认值为 `false`。对于 ACK 集群（托管、专有、Edge），需要显式设置为 `true` 以启用节点 RAM 角色认证：

| 集群类型      | 建议 command.enableWorkerRole |
| ------------- | ------------------------ |
| ACK 托管集群  | true                     |
| ACK 专有集群  | true                     |
| ACK Edge 集群 | true                     |
| 其它集群      | false                    |

当 `enableWorkerRole` 为 `true` 时，组件会使用节点 ECS 实例的 RAM 角色获取临时凭据，无需额外配置认证信息（该角色需要具有 KMS 访问权限）。

### 5.4 使用 ExternalSecret

使用 WorkerRole 认证时，**无需创建 SecretStore** 和配置 `secretStoreRef`。

### 5.5 完整示例

请参考 [examples/auth/auth-05-worker-role.yaml](../examples/auth/auth-05-worker-role.yaml)

## 认证配置最佳实践

### 安全建议

1. ✅ **优先使用 ServiceAccount RRSA**: 提供 ServiceAccount 级别的权限隔离，不同租户可引用不同 ServiceAccount
2. ✅ **最小权限原则**: RAM Role 只授予必要的 KMS 权限
3. ✅ **避免硬编码 AK**: 不要在生产环境使用纯 AccessKey
4. ✅ **定期轮换**: 配置合理的 `rotationInterval`

### 多租户场景

```
每个租户独立配置：
- 独立的 ServiceAccount（不同 namespace）
- 独立的 SecretStore
- 独立的 RAM Role（不同的权限范围）
```

### 集群共享场景

```
使用环境变量 RRSA：
- 组件级别统一认证
- 所有 ExternalSecret 共享权限
- 配置简单但权限隔离粗
```

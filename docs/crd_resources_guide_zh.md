# ACK Secret Manager CRD 资源详解

> 本文档详细说明 ack-secret-manager 的 4 个 CRD 资源，包括资源概述、跨命名空间控制机制、推荐使用方式及各资源字段参数参考。

## 目录

- [CRD 概述](#crd-概述)
- [跨命名空间控制机制](#跨命名空间控制机制)
- [推荐使用方式](#推荐使用方式)
- [SecretStore](#secretstore)
- [ClusterSecretStore](#clustersecretstore)
- [ExternalSecret](#externalsecret)
- [ClusterExternalSecret](#clusterexternalsecret)
- [CRD 字段参数说明](#crd-字段参数说明)

## CRD 概述

ack-secret-manager 涉及 4 种 CRD，分为两类：

### 认证类 CRD（存储访问凭据）

| CRD | 作用范围 | 说明 |
| --- | -------- | ---- |
| **SecretStore** | 命名空间级 | 存储认证信息，默认被同 namespace 的 ExternalSecret 引用，也支持跨 namespace 引用（默认允许） |
| **ClusterSecretStore** | 集群级 | 存储认证信息，可被任意 namespace 的 ExternalSecret 引用 |

### 数据类 CRD（定义同步规则）

| CRD | 作用范围 | 说明 |
| --- | -------- | ---- |
| **ExternalSecret** | 命名空间级 | 定义同步哪些凭据，创建对应的 Kubernetes Secret |
| **ClusterExternalSecret** | 集群级 | 自动在多个匹配的 namespace 创建 ExternalSecret |

**设计理念**：权限与数据分离，增强使用灵活性。

### SecretStore vs ClusterSecretStore

| 特性 | SecretStore | ClusterSecretStore |
| ---- | ----------- | ------------------ |
| **资源级别** | Namespace 级别 | 集群级别 |
| **作用范围** | 默认同 namespace，也可被跨 namespace 引用（默认允许） | 整个集群 |
| **访问控制** | 无 | 通过 `spec.conditions` 限制可访问的命名空间 |
| **适用场景** | 多租户隔离、环境分离 | 全局共享、集中管理 |

### ExternalSecret vs ClusterExternalSecret

两者都需要用户手动创建资源本身，核心区别在于：ClusterExternalSecret 会根据 `spec.conditions` 自动在匹配的 namespace 中创建并维护 ExternalSecret 实例。

| 特性 | ExternalSecret | ClusterExternalSecret |
| ---- | -------------- | --------------------- |
| **资源级别** | Namespace 级别 | 集群级别 |
| **同步目标** | 自身定义并同步单个 namespace 的凭据 | 自动在匹配的 namespace 中创建 ExternalSecret，由这些 ExternalSecret 完成凭据同步 |
| **新增匹配 namespace** | 需手动创建新的 ExternalSecret | 自动创建对应的 ExternalSecret |
| **配置一致性** | 多个 ExternalSecret 之间可能不一致 | 由 ClusterExternalSecret 统一管理，保证一致 |
| **适用场景** | 少量 namespace | 大量/动态 namespace |

## 跨命名空间控制机制

为了增强安全性和灵活性，ack-secret-manager 提供了多种跨命名空间控制机制：

### ExternalSecret 引用 SecretStore 控制

- 通过 `command.enableCrossNamespaceSecretStore` 参数控制 ExternalSecret 是否可以跨命名空间引用 SecretStore
- 默认值为 `true`，即允许跨命名空间引用
- 设置为 `false` 时，ExternalSecret 只能引用同命名空间的 SecretStore

**使用 SecretStore 时的引用规则**：

| 引用方 | 目标资源 | Namespace 字段 | 控制参数 | Namespace 字段未设置时 | Namespace 字段设置时 |
| ------ | -------- | -------------- | -------- | ---------------------- | -------------------- |
| ExternalSecret | SecretStore | `secretStoreRef.namespace` | `command.enableCrossNamespaceSecretStore` | 引用同 namespace 的 SecretStore | 引用指定 namespace 的 SecretStore（需参数为 `true`） |

> 当控制参数设置为 `false` 时，即使设置了 namespace 字段，也只能引用同 namespace 的资源。

**使用 ClusterSecretStore 时的引用规则**：

| 引用方 | 目标资源 | Namespace 字段 | 说明 |
| ------ | -------- | -------------- | ---- |
| ExternalSecret | ClusterSecretStore | 无（集群级别资源） | 任意 namespace 可引用，需满足 `conditions` 配置 |

### SecretStore 引用认证资源控制

- 通过 `command.enableCrossNamespaceAuthRef` 参数控制 SecretStore 是否可以跨命名空间引用认证资源（ServiceAccount、AccessKey Secret）
- 默认值为 `true`，即允许跨命名空间引用
- 设置为 `false` 时，SecretStore 只能引用同命名空间的认证资源

**使用 SecretStore 时**：

| 引用方 | 目标资源 | Namespace 字段 | 控制参数 | Namespace 字段未设置时 | Namespace 字段设置时 |
| ------ | -------- | -------------- | -------- | ---------------------- | -------------------- |
| SecretStore | ServiceAccount | `serviceAccountRef.namespace` | `command.enableCrossNamespaceAuthRef` | 引用同 namespace 的 ServiceAccount | 引用指定 namespace 的 ServiceAccount（需参数为 `true`） |
| SecretStore | Secret | `secretRef.namespace` | `command.enableCrossNamespaceAuthRef` | 引用同 namespace 的 Secret | 引用指定 namespace 的 Secret（需参数为 `true`） |

**使用 ClusterSecretStore 时**：

| 引用方 | 目标资源 | Namespace 字段 | 说明 |
| ------ | -------- | -------------- | ---- |
| ClusterSecretStore | ServiceAccount | `serviceAccountRef.namespace` | **必填**，可指向任意 namespace |
| ClusterSecretStore | Secret | `secretRef.namespace` | **必填**，可指向任意 namespace |

### ClusterExternalSecret 匹配命名空间控制

- ClusterExternalSecret 通过 `spec.conditions` 字段定义命名空间的选择条件，以在符合条件的命名空间中创建 ExternalSecret 资源
- 支持三种匹配方式，条件之间是或的关系：
  1. `namespaceSelector`：使用标签选择器匹配允许访问的命名空间
  2. `namespaces`：明确列出允许访问的命名空间名称列表
  3. `namespaceRegexes`：使用正则表达式匹配允许访问的命名空间名称列表

### ClusterSecretStore 访问控制

- ClusterSecretStore 通过 `spec.conditions` 字段定义允许访问该资源的命名空间条件
- 支持三种访问控制方式，条件之间是或的关系：
  1. `namespaceSelector`：使用标签选择器匹配允许访问的命名空间
  2. `namespaces`：明确列出允许访问的命名空间名称列表
  3. `namespaceRegexes`：使用正则表达式匹配允许访问的命名空间名称列表

## 推荐使用方式

### 跨命名空间访问推荐方案

对于需要跨命名空间访问的场景，推荐使用以下组合：

1. **ClusterSecretStore + ExternalSecret**：当多个命名空间需要使用相同的认证配置时
2. **ClusterSecretStore + ClusterExternalSecret**：当需要在多个命名空间中自动创建相同配置的 ExternalSecret 时

### 安全最佳实践

1. **最小权限原则**：
   - 在不需要跨命名空间访问的场景中，将 `command.enableCrossNamespaceSecretStore` 和 `command.enableCrossNamespaceAuthRef` 设置为 false
   - 优先使用命名空间级别的资源（SecretStore 和 ExternalSecret）
2. **访问控制配置**：
   - 使用 ClusterSecretStore 时，明确配置 `spec.conditions` 来限制可访问的命名空间
   - 避免创建无访问限制的 ClusterSecretStore
3. **认证方式选择**：
   - 优先使用 RRSA 或 ServiceAccount 方式进行认证，避免在配置中直接暴露 AccessKey
   - 将认证配置与数据配置分离，提高安全性
4. **非必要不使用 ClusterExternalSecret**，以减少 Secrets 在不同命名空间中的泄露风险：
   - 如果业务需要在多个命名空间中同步 Secrets 实例，可以利用 `spec.conditions` 精确控制 ExternalSecret 的创建范围

## SecretStore

### 功能与适用场景

SecretStore 是命名空间级别资源，用于定义访问凭据（如 RRSA、AK 配置等）。默认被同命名空间的 ExternalSecret 引用，也支持跨命名空间引用（默认允许，详见[跨命名空间控制机制](#跨命名空间控制机制)）。适用于多租户隔离、环境分离等场景。

### 配置说明

SecretStore 支持以下认证方式（ServiceAccount RRSA、RRSA、AK 扮演、AK），具体配置请参考 [认证配置指南](auth_guide_zh.md)。

关键配置要点：
- **ServiceAccount RRSA**：通过 `serviceAccountRef.name` 引用同 namespace 的 ServiceAccount，可通过 `serviceAccountRef.namespace` 跨 namespace 引用
- **AK 认证**：通过 `accessKey` / `accessKeySecret` 引用包含 AK 信息的 Kubernetes Secret
- **跨账号**：在任意认证方式基础上添加 `remoteRamRoleARN` + `remoteRamRoleSessionName`

### 完整示例

请参考 [examples/crd/crd-01-secretstore.yaml](../examples/crd/crd-01-secretstore.yaml)

## ClusterSecretStore

### 功能与适用场景

ClusterSecretStore 是集群级别资源，功能与 SecretStore 相同，但可被集群中任意命名空间的 ExternalSecret 引用，并支持通过 `spec.conditions` 配置访问控制。适用于全局共享认证、集中管理等场景。

### 访问控制

通过 `spec.conditions` 控制哪些 namespace 可以使用该 ClusterSecretStore，支持三种方式：

1. **namespaces**：明确列出允许的 namespace 名称列表
2. **namespaceSelector**：使用标签选择器匹配 namespace
3. **namespaceRegexes**：使用正则表达式匹配 namespace 名称

### 配置说明

ClusterSecretStore 的认证配置与 SecretStore 相同，额外需要注意：
- ClusterSecretStore 引用 ServiceAccount 或 Secret 时，`namespace` 字段**必填**（可指向任意 namespace）
- 推荐配置 `conditions` 限制可访问的 namespace，避免创建无访问限制的 ClusterSecretStore

### 完整示例

请参考 [examples/crd/crd-02-cluster-secret-store.yaml](../examples/crd/crd-02-cluster-secret-store.yaml)

## ExternalSecret

### 功能与适用场景

ExternalSecret 是命名空间级别资源，用于定义需要同步的凭据基础信息（如凭据名称、版本等）以及指定 SecretStore。控制器会根据配置自动从后端拉取凭据并创建对应的 Kubernetes Secret。适用于单命名空间的凭据同步场景。

### 配置说明

ExternalSecret 的关键配置字段：
- `provider`：目标云服务（`kms` 或 `oos`，默认 `kms`）
- `data[]`：定义同步的凭据列表，每个凭据指定 `key`（KMS 凭据名称）、`name`（K8s Secret 中的 key）、`versionId` 等
- `secretStoreRef`：引用的 SecretStore 或 ClusterSecretStore（引用 ClusterSecretStore 时需指定 `kind: ClusterSecretStore`）
- `rotationInterval`：同步间隔
- `kmsEndpoint`：凭据级 KMS Endpoint（可选，覆盖全局配置）
- `jmesPath`：解析 JSON/YAML 凭据中的特定字段（可选）
- `dataProcess`：自动解析 JSON/YAML 凭据（可选）

### 多 Key 配置说明

一个 ExternalSecret 可以通过 `data[]` 数组配置多个凭据，所有凭据的同步结果会写入**同一个** Kubernetes Secret 中。`data[]` 中每个条目的 `key` 指定后端（KMS/OOS）中的凭据名称，`name` 指定同步到 K8s Secret `data` 中的 key 名称。

**配置示例**：

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: my-app-secrets
  namespace: production
spec:
  provider: kms
  secretStoreRef:
    name: my-store
    kind: SecretStore
  data:
    - key: db-password      # KMS 中的凭据名称
      name: database-password # 写入 K8s Secret 的 key
    - key: api-key
      name: api-key
    - key: tls-cert
      name: tls-certificate
```

上述配置将生成一个名为 `my-app-secrets` 的 K8s Secret，其 `data` 包含三个 key：

```
K8s Secret (production/my-app-secrets)
  data:
    database-password: <db-password 的凭据内容>
    api-key:           <api-key 的凭据内容>
    tls-certificate:   <tls-cert 的凭据内容>
```

> **说明**：
> - `key` 是后端（KMS/OOS）中凭据的标识符，`name` 是最终写入 K8s Secret `data` 中的 key 名称，两者可以不同
> - 如果省略 `name`，默认使用 `key` 的值作为 Secret data 的 key（例如 `key: api-key` 未指定 `name` 时，Secret data 中的 key 也是 `api-key`）
> - 每个 `data[]` 条目可以独立配置 `secretStoreRef`、`kmsEndpoint`、`versionId` 等，未配置时使用默认值
> - 适用于需要将多个相关凭据集中管理到同一个 K8s Secret 的场景（如数据库连接信息、TLS 证书等）

### 完整示例

请参考：
- [examples/crd/crd-03-externalsecret-basic.yaml](../examples/crd/crd-03-externalsecret-basic.yaml) — 基础示例
- [examples/crd/crd-04-externalsecret-multi-key.yaml](../examples/crd/crd-04-externalsecret-multi-key.yaml) — 多 Key 配置

## ClusterExternalSecret

### 功能与适用场景

ClusterExternalSecret 是集群级别资源，用于管理和协调多个命名空间下的 ExternalSecret，能够在匹配的命名空间中自动创建 ExternalSecret。适用于大量或动态 namespace 的凭据同步场景。

### 工作原理

```
ClusterExternalSecret 创建
  ↓
Controller 定期检查 conditions
  ↓
匹配 namespace（通过 namespaces / namespaceSelector / namespaceRegexes）
  ↓
在每个匹配的 namespace 创建 ExternalSecret
  ↓
ExternalSecret Controller 同步 KMS 凭据
  ↓
生成 Kubernetes Secret
```

### 配置说明

ClusterExternalSecret 的关键配置字段：
- `externalSecretSpec`：要创建的 ExternalSecret 规格定义
- `externalSecretName`：创建的 ExternalSecret 名称（默认为 ClusterExternalSecret 的名称）
- `externalSecretMetadata`：为创建的 ExternalSecret 添加标签和注解
- `conditions`：定义在哪些 namespace 创建 ExternalSecret
- `rotationInterval`：检查 namespace 匹配的间隔

### 注意事项

- **删除行为**：删除 ClusterExternalSecret 时，会自动删除其在各 namespace 中创建的 ExternalSecret
- **命名冲突**：若 namespace 中已存在同名 ExternalSecret，会被接管并覆盖，建议使用唯一的 `externalSecretName`

### 完整示例

请参考 [examples/crd/crd-05-cluster-external-secret.yaml](../examples/crd/crd-05-cluster-external-secret.yaml)

## CRD 字段参数说明

### SecretStore

**spec**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| KMS | 连接KMS凭据管家服务获取密钥 | 否 |
| OOS | 连接OOS服务获取加密参数 | 否 |

**KMS**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| KMSAuth | 访问 KMS 所需凭证(密钥管理服务) | 否 |

**OOS**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| OOSAuth | 访问 OOS 所需凭证(管理加密参数) | 否 |

**KMSAuth / OOSAuth**

| crd 字段 | 描述 | 是否必选 | 默认行为 |
| -------- | ---- | -------- | -------- |
| accessKey | 参考如下阿里云AccessKey认证配置方式 | 否 | 不填时依次按优先级尝试其他认证方式（RRSA → AK 扮演 → WorkerRole） |
| accessKeySecret | 参考如下阿里云AccessKey认证配置方式 | 否 | 与 accessKey 配合使用 |
| ramRoleARN | RAM 角色 ARN，用于 RRSA 或 AK 扮演认证 | 否 | 不填时不使用角色认证 |
| ramRoleSessionName | 角色会话名，用于 STS AssumeRole | 否 | 不填时使用组件默认会话名 |
| oidcProviderARN | OIDC 提供商 ARN，用于 RRSA 认证 | 否 | 不填时不使用 OIDC 认证 |
| oidcTokenFilePath | OIDC Token 文件路径 | 否 | 不填时使用默认路径 `/var/run/secrets/tokens/ack-secret-manager` |
| remoteRamRoleARN | 跨账号 RAM 角色 ARN，用于跨账号同步 | 否 | 不填时不进行跨账号 AssumeRole |
| remoteRamRoleSessionName | 跨账号角色会话名 | 否 | 不填时使用组件默认会话名 |
| serviceAccountRef | 引用的 ServiceAccount 信息，用于 ServiceAccount RRSA 认证 | 否 | 不填时不使用 ServiceAccount 级别认证 |

**阿里云AccessKey认证配置方式**

阿里云AccessKey是用户访问云上资源最重要的身份凭据，不支持在SecretStore字段中直接配置，需要配置在Kubernetes Secrets实例中引用，相关引用配置字段说明如下：

> 阿里云AccessKey泄露会导致严重的安全风险，推荐您使用RRSA或其他阿里云认证方式

| 字段 | 描述 | 是否必选 | 默认行为 |
| ---- | ---- | -------- | -------- |
| name | Kubernetes Secrets名称 | 是 | — |
| namespace | Kubernetes Secrets所在namespace | 否 | SecretStore 时默认引用同 namespace，支持跨 namespace 引用（由 `command.enableCrossNamespaceAuthRef` 控制）；ClusterSecretStore 时必填 |
| key | Kubernetes Secrets 数据中指定的Key | 是 | — |

**ServiceAccountRef**

| crd 字段 | 描述 | 是否必选 | 默认行为 |
| -------- | ---- | -------- | -------- |
| name | ServiceAccount 名称 | 是 | — |
| namespace | ServiceAccount 命名空间 | 否 | SecretStore 时默认引用同 namespace，支持跨 namespace 引用（由 `command.enableCrossNamespaceAuthRef` 控制）；ClusterSecretStore 时必填 |
| audiences | Audience 数组，用于ServiceAccount token的aud字段 | 否 | 不填时默认使用 `["sts.aliyuncs.com"]` |

### ClusterSecretStore

ClusterSecretStore 是集群级别的 SecretStore 资源，可被集群中的任意命名空间下的 ExternalSecret 引用。它除了具备 SecretStore 的所有功能外，还增加了访问控制配置，可以限制该资源被访问的命名空间。

**spec**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| conditions | 定义允许访问该资源的命名空间条件。未配置时不限制访问，配置后生效 | 否 |
| KMS | 连接KMS凭据管家服务获取密钥 | 否 |
| OOS | 连接OOS服务获取加密参数 | 否 |

**conditions**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| namespaceSelector | 使用标签选择器匹配允许访问的命名空间 | 否 |
| namespaces | 明确列出允许访问的命名空间名称列表 | 否 |
| namespaceRegexes | 使用正则表达式匹配允许访问的命名空间名称列表 | 否 |

> KMS/OOS 下的认证字段（KMSAuth/OOSAuth）与 SecretStore 相同，请参考上方 SecretStore 参数说明。

### ExternalSecret

**spec**

| crd 字段 | 描述 | 是否必选 | 默认行为 |
| -------- | ---- | -------- | -------- |
| provider | 获取 secret 的目标云服务 | 否 | 不填时默认为 `kms` |
| data | 数据源（目标数据的标识） | 否 | — |
| dataProcess | 需要进行特殊加工的数据源 | 否 | — |
| type | k8s secret 类型 | 否 | 不填时默认为 `Opaque` |
| target | 定义将要创建的 Kubernetes Secret 的配置 | 否 | 不填时 Secret 名称与 ExternalSecret 名称相同 |
| rotationInterval | 控制器重新协调对象的时间间隔 | 否 | 不填时使用全局 `--polling-interval`（默认 120s） |

**target**

| crd 字段 | 描述 | 是否必选 | 默认行为 |
| -------- | ---- | -------- | -------- |
| name | 目标 Kubernetes Secret 的名称 | 否 | 不填时默认为 ExternalSecret 的名称 |
| template | 用于转换密钥数据的模板配置 | 否 | 不填时直接使用原始凭据数据 |

**template**

| crd 字段 | 描述 | 是否必选 | 默认值 |
| -------- | ---- | -------- | ------ |
| data | 内联模板定义 | 否 | |
| templateFrom | 外部模板引用（ConfigMap/Secret） | 否 | |
| metadata | Secret 元数据模板（标签/注解） | 否 | |
| type | 目标 secret 的类型 | 否 | |
| mergePolicy | 模板结果与原始数据的合并策略 | 否 | Replace |

**metadata**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| annotations | 应用于 secret 的注解 | 否 |
| labels | 应用于 secret 的标签 | 否 |

**templateFrom**

| crd 字段 | 描述 | 是否必选 | 默认值 |
| -------- | ---- | -------- | ------ |
| configMap | 包含模板的 ConfigMap 引用 | 否 | |
| secret | 包含模板的 Secret 引用 | 否 | |
| literal | 内联字面量模板字符串 | 否 | |
| target | 模板结果的目标位置（Data/Annotations/Labels） | 否 | Data |

**configMap/secret**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| name | ConfigMap/Secret 资源的名称 | 是 |
| items | ConfigMap/Secret 中用作模板的键列表 | 是 |

**items**

| crd 字段 | 描述 | 是否必选 | 默认值 |
| -------- | ---- | -------- | ------ |
| key | ConfigMap/Secret 中的键 | 是 | |
| templateAs | 模板键的解释方式 | 否 | Values |

**templateAs**

| 值 | 描述 |
| -- | ---- |
| Values | 仅将模板作为值处理 |
| KeysAndValues | 将模板作为键值对处理 |

**templateFrom.target**

| 值 | 描述 |
| -- | ---- |
| Data | 将模板结果存入 Secret data |
| Annotations | 将模板结果存入 Secret annotations |
| Labels | 将模板结果存入 Secret labels |

**mergePolicy**

| 值 | 描述 |
| -- | ---- |
| Replace | 完全使用模板字段名（清空现有secret中的字段名） |
| Merge | 将模板字段名与现有secret中的字段名合并, 模板中包含的字段名和现有secret中的字段名相同，以模板值为准 |

**data（无需经过特殊处理的数据源）**

| crd 字段 | 描述 | 是否必选 | 默认行为 |
| -------- | ---- | -------- | -------- |
| key | 目标凭据的唯一标识（KMS 凭据名称或 OOS 参数名称） | 是 | — |
| name | 同步到 K8s Secret data 中对应的 key 名称 | 是 | 未配置时默认使用 `key` 的值 |
| versionStage | 目标凭据的版本状态（如 `ACSCurrent`、`ACSPrevious`） | 否 | 不填时拉取最新版本（`ACSCurrent`） |
| versionId | 目标凭据的版本号；当 provider 为 `oos` 时不需要指定 | 否 | 不填时拉取最新版本 |
| jmesPath | 当目标凭据为 JSON/YAML 格式时，通过 JMESPath 表达式提取特定字段 | 否 | 不填时同步整个凭据内容 |
| secretStoreRef | 该凭据引用的 SecretStore 信息，可覆盖 ExternalSecret 顶层配置 | 否 | 不填时使用默认值 |
| kmsEndpoint | 该凭据使用的 KMS Endpoint 地址，可覆盖全局配置 | 否 | 不填时依次使用全局 `command.kmsEndpoint` → 默认 `kms-vpc.{region}.aliyuncs.com` |

**dataProcess（需要进行特殊处理的数据源）**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| extract | 针对目标 secret 进行 json/yaml 解析，不需要用户指定 json/yaml key | 否 |
| replaceRule | 根据特定规则替换经过 json/yaml 解析的 secret 的 key，防止非法 key 不能存入 k8s secret | 否 |

**replaceRule（用于进行 Secret Key 内容替换）**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| target | 用于替换的字符串 | 是 |
| source | 需要被替换的字符串，可以为正则表达式 | 是 |

**jmesPath**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| path | jmes 表达式，用户指定 json/yaml key | 是 |
| objectAlias | 存入 k8s secret 对应的 data key | 是 |

**secretStoreRef**

| crd 字段 | 描述 | 是否必选 | 默认行为 |
| -------- | ---- | -------- | -------- |
| name | 指定引用的 SecretStore 或 ClusterSecretStore name | 是 | — |
| namespace | 指定 SecretStore 的 namespace | 否 | 不填时默认引用 ExternalSecret 所在的 namespace，支持跨 namespace 引用（由 `command.enableCrossNamespaceSecretStore` 控制） |
| kind | 指定引用 SecretStore 或 ClusterSecretStore | 否 | 不填时默认为 `SecretStore` |

### ClusterExternalSecret

ClusterExternalSecret 是管理和协调多个命名空间下 ExternalSecret 的资源，能够在匹配的命名空间中自动创建 ExternalSecret，当匹配的命名空间已存在需创建的 ExternalSecret 时，ClusterExternalSecret 用新的规范完全更新现有的 ExternalSecret 资源。

**spec**

| crd 字段 | 描述 | 是否必选 | 默认行为 |
| -------- | ---- | -------- | -------- |
| externalSecretSpec | 要创建的 ExternalSecret 的规格定义 | 是 | — |
| externalSecretName | 要创建的 ExternalSecret 的名称 | 否 | 不填时默认为 ClusterExternalSecret 的名称 |
| externalSecretMetadata | 要创建的 ExternalSecret 的元数据 | 否 | 不填时不添加额外元数据 |
| namespaceSelectors | 使用标签选择器匹配允许访问的命名空间(已废弃) | 否 | 请使用 `conditions` 代替 |
| conditions | 用于选择目标命名空间的条件列表 | 否 | 不填时不创建任何 ExternalSecret |
| rotationInterval | 控制器检查命名空间标签和协调对象的时间间隔 | 否 | 不填时使用全局 `--polling-interval`（默认 120s） |

**externalSecretMetadata**

externalSecretMetadata 字段允许您自动为 ClusterExternalSecret 创建的 ExternalSecret 资源添加额外的元数据：

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| annotations | 要创建的 ExternalSecret 的注解 | 否 |
| labels | 要创建的 ExternalSecret 的标签 | 否 |

**conditions**

| crd 字段 | 描述 | 是否必选 |
| -------- | ---- | -------- |
| namespaceSelector | 使用标签选择器匹配允许访问的命名空间 | 否 |
| namespaces | 明确列出允许访问的命名空间名称列表 | 否 |
| namespaceRegexes | 使用正则表达式匹配允许访问的命名空间名称列表 | 否 |

# ACK Secret Manager 高级用法指南

> 本文档说明 ack-secret-manager 的高级功能，包括 JSON/YAML 凭据解析、跨账号同步、kmsEndpoint 配置、凭据轮转、多数据源支持。

## 目录

- [JSON/YAML 凭据解析](#jsonyaml-凭据解析)
- [跨账号同步](#跨账号同步)
- [kmsEndpoint 配置](#kmsendpoint-配置)
- [凭据轮转](#凭据轮转)
- [多数据源支持](#多数据源支持)

## JSON/YAML 凭据解析

### 功能说明

使用 JMESPath 查询语言解析 JSON 格式的 KMS 凭据，提取特定字段。提供两种解析方式：

| 方式         | 配置字段                | 适用场景                     | 说明                         |
| ------------ | ----------------------- | ---------------------------- | ---------------------------- |
| 指定字段提取 | `data[].jmesPath`       | 已知 JSON 结构，提取特定字段 | 通过 JMESPath 表达式精确提取 |
| 自动解析     | `dataProcess[].extract` | 不确定 JSON 结构，全量解析   | 自动展平所有字段为 key-value |

### data[].jmesPath — 指定字段提取

当已知 KMS 凭据的 JSON 结构，只需提取特定字段时使用。

**字段说明**：

| 字段                     | 必填 | 说明                                                                               |
| ------------------------ | ---- | ---------------------------------------------------------------------------------- |
| `jmesPath[].path`        | 是   | 基于[JMESPath](https://jmespath.org/specification.html) 规范解析 JSON 中的指定字段 |
| `jmesPath[].objectAlias` | 是   | 解析出的字段同步到 K8s Secret 中的 key 名称                                        |

**常用 JMESPath 表达式**：

| 表达式            | 说明         |
| ----------------- | ------------ |
| `name`            | 基础字段访问 |
| `friends[0].name` | 数组索引     |
| `friends[*].name` | 数组遍历     |
| `a.b.c.d`         | 嵌套访问     |

### dataProcess[].extract — 自动解析

当不确定 KMS 凭据的 JSON 结构，需要全量解析时使用。自动将 JSON/YAML 凭据展平为 key-value 对存入 K8s Secret。

**字段说明**：

| 字段                     | 必填 | 默认行为     | 说明                           |
| ------------------------ | ---- | ------------ | ------------------------------ |
| `extract.key`            | 是   | —            | KMS 凭据名称                   |
| `extract.name`           | 是   | —            | ExternalSecret 名称，也是生成的 K8s Secret 名称 |
| `extract.versionId`      | 否   | 拉取最新版本 | KMS 凭据版本                   |
| `extract.kmsEndpoint`    | 否   | 使用全局配置 | KMS 服务地址                   |
| `extract.secretStoreRef` | 否   | 使用默认值   | SecretStore 引用               |
| `replaceRule[].source`   | 否   | 不替换       | 正则表达式，匹配需要替换的 key |
| `replaceRule[].target`   | 否   | —            | 替换后的 key 名称              |

> **说明**：
> - `extract` 会将 JSON 凭据中的所有字段展平为 key-value 对，直接存入 K8s Secret，无需用户预先知道 JSON 结构
> - `replaceRule` 用于解决 KMS 凭据中的 key 包含非法字符（如 `.`、`/`）导致无法写入 K8s Secret 的问题，可通过正则匹配批量重命名 key
>
> **replaceRule 示例**：KMS 凭据中包含 key `db.password`，K8s Secret 不允许 `.` 作为 key：
> ```yaml
> replaceRule:
>   - source: "\."    # 匹配所有点号
>     target: "-"     # 替换为连字符，db.password → db-password
> ```

### 完整示例

请参考 [examples/advanced/advanced-01-jmespath-parsing.yaml](../examples/advanced/advanced-01-jmespath-parsing.yaml)

## 跨账号同步

### 功能说明

在当前账号访问其他阿里云账号的 KMS 凭据。

> **说明**：所有认证方式（ServiceAccount RRSA / RRSA / AK 扮演 / AK / WorkerRole）都支持跨账号同步。

### 跨账号原理

在当前账号认证之上，叠加一层 `remoteRamRoleARN` 的 AssumeRole，获取目标账号的凭据访问权限：

```
当前账号认证（任意方式）→ 获取临时凭据 → AssumeRole(remoteRamRoleARN) → 目标账号凭据
```

### 配置方式

**SecretStore 方式**（适用于 ServiceAccount RRSA / RRSA / AK 扮演 / AK）：

在 SecretStore 的 `KMSAuth` 中添加 `remoteRamRoleARN` 和 `remoteRamRoleSessionName`，与基础认证方式无关。

**环境变量方式**（适用于 RRSA / AK 扮演 / AK / WorkerRole）：

通过 `ALICLOUD_REMOTE_ROLE_ARN` 和 `ALICLOUD_REMOTE_ROLE_SESSION_NAME` 环境变量配置。

### 前提条件

1. **目标账号**：创建 RAM Role，信任策略允许当前账号扮演，且有 KMS 访问权限
2. **当前账号**：任意认证方式已配置，且有扮演目标账号 RAM Role 的权限

### 完整示例

请参考 [examples/advanced/advanced-02-cross-account.yaml](../examples/advanced/advanced-02-cross-account.yaml)

## kmsEndpoint 配置

### 功能说明

访问 KMS 服务获取凭据支持专属实例网关和共享网关两种方式，可参考 [共享网关和专属网关的差异](https://www.alibabacloud.com/help/zh/kms/key-management-service/developer-reference/classic-kms-sdkclassic-kms-sdk/#d61514b089my8)。

### KMS Endpoint 优先级说明

| 类型       | 配置字段                               | 用途                             | 优先级 | 说明                             |
| ---------- | -------------------------------------- | -------------------------------- | ------ | -------------------------------- |
| 凭据级配置 | `ExternalSecret.spec.data.kmsEndpoint` | 为每个 KMS 凭据单独指定 Endpoint | 最高   | 覆盖全局配置和默认配置           |
| 全局配置   | `command.kmsEndpoint`（启动参数）      | 用于所有 KMS 请求                | 中     | 提供默认配置以外的 Endpoint 地址 |
| 默认配置   | 无                                     | 未配置任何 Endpoint 时使用       | 最低   | `kms-vpc.{region}.aliyuncs.com`  |

### KMS Endpoint 地址说明

| 网关类型 | 域名类型     | Endpoint 地址                                      | 使用说明                                              |
| -------- | ------------ | -------------------------------------------------- | ----------------------------------------------------- |
| 专属网关 | KMS 私网域名 | `{kms-instance-id}.cryptoservice.kms.aliyuncs.com` | KMS 凭据所属实例和集群 Region/VPC 相同，实例版本 3.0+ |
| 共享网关 | VPC 域名     | `kms-vpc.{region}.aliyuncs.com`                    | KMS 凭据和集群 Region 相同（默认行为）                |
| 共享网关 | 公网         | `kms.{region}.aliyuncs.com`                        | 集群具有公网访问能力                                  |

### 配置位置

- **全局配置**：通过 Helm `values.yaml` 的 `command.kmsEndpoint` 设置，对所有凭据生效
- **凭据级配置**：通过 `ExternalSecret.spec.data[].kmsEndpoint` 设置，仅该凭据生效，覆盖全局配置

### 完整示例

请参考 [examples/advanced/advanced-03-kms-endpoint.yaml](../examples/advanced/advanced-03-kms-endpoint.yaml)

## 凭据轮转

### 功能说明

ack-secret-manager 支持自动轮转凭据，控制器会按照配置的间隔定期从后端（KMS/OOS）重新拉取最新凭据并同步到 K8s Secret，确保应用始终使用最新的凭据内容。

### 配置方式

| 配置层级 | 配置字段                               | 说明                                               |
| -------- | -------------------------------------- | -------------------------------------------------- |
| 凭据级   | `ExternalSecret.spec.rotationInterval` | 单个 ExternalSecret 的轮转间隔，优先级高于全局配置 |
| 全局     | `command.pollingInterval`（启动参数）  | 所有 ExternalSecret 的默认轮转间隔                 |

> **说明**：当 ExternalSecret 配置了 `rotationInterval` 时，使用该值；否则使用全局的 `pollingInterval`。

### 示例

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
  rotationInterval: 60s  # 每 60 秒从后端重新拉取凭据
```

完整示例请参考 [examples/advanced/advanced-04-credential-rotation.yaml](../examples/advanced/advanced-04-credential-rotation.yaml)

## 多数据源支持

### 功能说明

ack-secret-manager 支持同步两种数据源：

| 数据源       | 说明                                  | provider 值   | 认证字段  |
| ------------ | ------------------------------------- | ------------- | --------- |
| KMS 凭据管家 | 同步 KMS Secrets Manager 中的密钥凭据 | `kms`（默认） | `KMSAuth` |
| OOS 加密参数 | 同步 OOS 加密参数                     | `oos`         | `OOSAuth` |

### KMS 与 OOS 配置差异

| 配置项                  | KMS 凭据管家       | OOS 加密参数       |
| ----------------------- | ------------------ | ------------------ |
| SecretStore 认证        | `spec.KMS.KMSAuth` | `spec.OOS.OOSAuth` |
| ExternalSecret provider | `kms`              | `oos`              |
| `versionId` 字段        | 需要指定           | 不需要             |
| `kmsEndpoint` 字段      | 可配置             | 不适用             |

> **说明**：KMS 和 OOS 的认证字段（`KMSAuth` / `OOSAuth`）结构相同，支持相同的认证方式。

### OOS 示例

**SecretStore**：

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

**ExternalSecret**：

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: oos-demo
spec:
  provider: oos  # 指定为 oos
  data:
    - key: my-oos-parameter  # OOS 加密参数名称
      name: my-secret
      secretStoreRef:
        name: oos-store
        namespace: default
```

完整示例请参考 [examples/advanced/advanced-05-oos-parameter.yaml](../examples/advanced/advanced-05-oos-parameter.yaml)

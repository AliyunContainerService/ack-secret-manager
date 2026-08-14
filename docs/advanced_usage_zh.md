# ACK Secret Manager 高级用法指南

> 本文档说明 ack-secret-manager 的高级功能，包括 JSON/YAML 凭据解析、跨账号同步、kmsEndpoint 配置、凭据轮转、多数据源支持、同步失败处理语义。

## 目录

- [JSON/YAML 凭据解析](#jsonyaml-凭据解析)
- [跨账号同步](#跨账号同步)
- [kmsEndpoint 配置](#kmsendpoint-配置)
- [凭据轮转](#凭据轮转)
- [多数据源支持](#多数据源支持)
- [同步失败处理语义](#同步失败处理语义)

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
| `extract.name`           | 否   | 控制器不使用 | 可选字段；仅在 `spec.data[].name` 非 jmesPath 场景生效，extract 路径下控制器不使用该字段；Secret data 的 key 默认为解析后 JSON/YAML 的顶层 key，配置 `extract.jmesPath` 时由各条目的 `objectAlias` 决定，可通过 `replaceRule` 重命名 |
| `extract.versionId`      | 否   | 拉取最新版本 | KMS 凭据版本                   |
| `extract.kmsEndpoint`    | 否   | 使用全局配置 | KMS 服务地址                   |
| `extract.secretStoreRef` | 否   | 使用默认值   | SecretStore 引用               |
| `replaceRule[].source`   | 否   | 不替换       | 正则表达式，匹配需要替换的 key |
| `replaceRule[].target`   | 否   | —            | 替换后的 key 名称              |

> **说明**：
> - `extract` 会将 JSON 凭据中的所有字段展平为 key-value 对，直接存入 K8s Secret，无需用户预先知道 JSON 结构
> - `replaceRule` 用于解决 KMS 凭据中的 key 包含非法字符（如 `.`、`/`）导致无法写入 K8s Secret 的问题，可通过正则匹配批量重命名 key
> - 在 extract 路径下，`replaceRule` 除重命名 key（正则匹配）外，还会对 Secret data 的值执行字面量替换
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

> **说明**：`remoteRamRoleSessionName` 与 `ramRoleSessionName` 均可省略，省略时使用组件默认会话名 `ack-secret-manager`。

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

## 同步失败处理语义

### 功能说明

从后端数据源（KMS/OOS）拉取凭据时，部分或全部数据源可能因瞬态错误（限流、5xx 响应、网络抖动等）或持久性错误（凭据不存在、权限不足等）而失败。自 v0.6.5 起，控制器会根据失败范围与是否配置了模板解析来保护集群 Secret，既避免写入不完整或错误的数据，也避免误删或误清空仍然有效的 Secret。

瞬态错误在被报告为失败前会统一重试：重试次数有界、采用带抖动的指数退避，且在控制器优雅停机期间可随时取消。每个数据源条目在瞬态失败时最多发起 3 次云端 API 调用（重试间隔约 2s/4s，带 ±20% 抖动），重试次数当前不可通过参数配置。`status.dataSyncResults` 中可见的失败均为重试耗尽后的最终错误。

### 失败处理真值表

| 失败范围 | 模板解析 | cleanupSecretOnFailure | 处理行为 |
| -------- | -------- | ---------------------- | -------- |
| 全部成功 | 任意 | 任意 | 正常写入，行为不变 |
| 部分失败 | 未配置 | 任意 | **合并写入——仅更新获取成功的部分**：成功键写入新值，失败键保留 Secret 中的旧值 |
| 部分失败 | 已配置 | 任意 | **fail-closed**：整体跳过 Secret 写入，保留旧 Secret |
| 全量失败 | 任意 | `false`（默认） | 跳过 Secret 写入，保留旧 Secret |
| 全量失败 | 任意 | `true` | 删除集群 Secret |

**要点说明**：

- **部分失败**指部分数据源成功、部分失败；**全量失败**指所有已配置的数据源（`spec.data[]` + `spec.dataProcess[]`）均失败。
- 通俗地说，凭据获取部分失败时，控制器**仅更新获取成功的部分**：每个成功的键写入新值，每个失败的键保留 Secret 中的旧值，失败键的旧值不会丢失或被覆盖。
- 合并写入的保留键集合与成功路径采用完全一致的键命名规则推导（`name`，或 `key` 回退，或 `jmesPath` 别名）。
- **失败的 `dataProcess[].extract` 条目的保守保留**：当失败条目是 `dataProcess[].extract` 时，控制器无法静态推断它“本应产出哪些键”，只能保守保留 Secret 中所有未被成功条目覆盖的旧键。该保守超集中可能包含用户已从 spec 删除的条目的旧键——这些键在 `status.dataSyncResults` 中没有对应的失败记录，因为条目已不存在。该现象需要“删条目”与“extract 失败”恰好时间重叠才会出现，且残留键会在下一个全量成功的轮次中自动清除。若失败条目持续存在，则保留键持续保留，属预期行为。
- 当 Secret 尚不存在时没有旧值可保留：部分失败轮次仅写入成功键。
- **重复后端键豁免**：同一后端键被多个 spec 条目引用，且同一轮次中一个成功、另一个失败时，控制器按“本轮数据可用”豁免该失败：不计入失败统计，也不在 `status.dataSyncResults` 中重复上报 `Failed`。上述写入/删除契约不受该豁免影响。
- 全量失败且 `cleanupSecretOnFailure=true` 时的删除同样适用于配置了模板的 ExternalSecret，且此时模板渲染出的静态内容永远不会被写入；部分失败时无论该开关如何取值都不会删除 Secret。
- `cleanupSecretOnFailure` 默认为 `false`（启动参数 `--cleanup-secret-on-failure`，Helm 配置项 `command.cleanupSecretOnFailure`）。
- **零产出守卫（fail-closed）**：无失败但产出 0 个 key 的轮次——如 `dataProcess[].extract` 引用的后端文档被清空（`{}` 或空字符串），或未声明任何数据源且未配置模板的 ExternalSecret——不会写入空数据集：否则会删除已有 Secret（`cleanupSecretOnFailure=true`）或清空其 Data。此时跳过写入、不触发删除，并在 `status.dataSyncResults` 中上报 `zero_output_guard` 条目。因此故意清空后端文档不再导致集群 Secret 被清空/删除，Secret 会保留到下一个产出 key 的轮次。该守卫仅在无失败轮次触发，不会抢占上述任何失败契约。**模板后零产出守卫**覆盖渲染之后的同类缺口：所有数据源成功且源数据非空，但模板渲染产出 0 个数据键时（Replace 模式下内联数据模板全部执行失败，或 Data 目标的 `templateFrom` 渲染出零个有效键），同样跳过写入、扣住删除，并上报 `template_zero_output_guard` 条目。
- **零产出保护的已知限制**：（a）多个 `dataProcess[].extract` 中仅部分条目产出零键时，这些条目的键会按全成功语义正常移除，不受守卫保护；（b）Replace 模式下模板渲染部分失败（部分数据模板执行失败、部分成功）时，写入仅含成功渲染键的残缺数据集——这保留了模板执行错误非致命的既有语义，仅在控制器日志中以 warning 提示。

### 与旧版本的行为差异

| 场景 | v0.6.5 之前的行为 | v0.6.5 起的行为 |
| ---- | ---------------- | --------------- |
| 部分失败（未配置模板） | Secret 被已获取到的成功值整体覆盖更新（失败键的旧值丢失） | 合并写入——**仅更新获取成功的部分**：成功键写入新值，失败键保留旧值 |
| 部分失败（已配置模板） | 部分数据可能被渲染为错误的 Secret | fail-closed：跳过写入，保留旧 Secret |
| 全量失败且 `cleanupSecretOnFailure=false` | Secret 被清空（写入空数据） | 保留旧 Secret |
| 全量失败且 `cleanupSecretOnFailure=true` | 删除 Secret | 删除 Secret（现同样适用于配置了模板的 ExternalSecret） |
| 全部成功但产出 0 个 key（如后端文档被清空） | Secret 被静默清空（`cleanupSecretOnFailure=true` 时被删除） | fail-closed：跳过写入，保留已有 Secret，上报 `zero_output_guard` |

### 失败可见性与状态语义

- 同步失败记录在 `status.dataSyncResults` 中（键、状态、原因）。失败不会改变轮询频率，也不会加快或减慢后续的同步轮询。
- 当某轮同步未产出任何数据而跳过写入时，`status.dataSyncResults` 中会出现一条 Status 为 `Failed` 的条目，其 key 为合成标识（非后端密钥名），Reason 说明跳过写入的原因。
- 对于可重试的瞬态错误（5xx/429/请求超时/连接重置），失败结果可能延迟数秒（约 3~10 秒）才反映到 `status`，这是重试机制的预期行为。
- 关闭自动轮询（`--disable-polling` / `command.disablePolling`）后，单次同步失败不会自动重试；只有当该 ExternalSecret 的 spec 发生变更时才会再次尝试同步。单次同步过程中的瞬态重试不受此影响。
- `status.dataSyncResults` 在同步结果语义（各键的状态/原因，或整体成功状态）变化时更新，并在控制器实际写入（或删除）目标 Secret 后强制刷新同步时间戳；`synchronizationTime` 表示当前上报结果的记录时间，**不会**每次同步轮询刷新：稳态轮询轮次（拉取到的数据无变化、未写入 Secret）中，时间戳保持为上次成功同步的时间，请勿将其用作活性心跳。

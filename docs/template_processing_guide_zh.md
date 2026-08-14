# ACK Secret Manager 模板解析指南

> 本文档说明 ack-secret-manager 的模板解析功能，包括模板语法、自定义函数、使用方式和常见问题排查。

## 目录

- [模板解析简介](#模板解析简介)
- [模板配置字段](#模板配置字段)
- [模板语法与函数](#模板语法与函数)
- [模板使用方式](#模板使用方式)
- [常见问题与排查](#常见问题与排查)

## 模板解析简介

### 简单理解

模板解析是在创建 Kubernetes Secret 之前，对从云服务商（如阿里云 KMS）获取的原始密钥数据进行自动转换和处理的功能。

**举个例子**：
- 原始数据：KMS 中存储的密码 `mypassword123`
- 处理后：生成 `DATABASE_PASSWORD=mypassword123` 格式的环境变量

### 核心价值

1. **自动化转换**：避免手动编辑 Secret 的繁琐操作
2. **格式标准化**：统一转换为应用程序需要的格式
3. **环境适配**：根据不同环境生成相应的配置
4. **数据整合**：将多个数据源组合成完整的配置

### 基础概念

- **数据源**：来自 KMS/OOS 等云服务的原始密钥数据
- **模板**：定义如何转换和处理这些数据的规则
- **目标 Secret**：最终生成的 Kubernetes Secret 资源

### 模板处理流程

```
原始数据获取 → 执行内联模板 → 执行 TemplateFrom 引用 → 验证结果 → 按目标分类 → 创建/更新 Secret
```

## 模板配置字段

模板相关配置位于 ExternalSecret 的 `spec.target.template` 下，完整的 CRD 字段定义请参考 [CRD 资源详解 - ExternalSecret](crd_resources_guide_zh.md#externalsecret) 部分。

主要字段概览：

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `data` | `map[string]string` | 内联模板，定义 Secret data 的 key-value |
| `templateFrom` | `[]TemplateFrom` | 从外部资源（ConfigMap/Secret/Literal）引用模板 |
| `metadata` | `ExternalSecretTemplateMetadata` | 模板化 annotations 和 labels |
| `type` | `SecretType` | 目标 Secret 类型（如 `kubernetes.io/tls`） |
| `mergePolicy` | `Replace`/`Merge` | 模板结果与原始数据的合并策略 |

## 模板语法与函数

### Go 模板基础语法

**基础操作**：
- 直接字段访问：`.key`
- 管道操作：`{{ .key | func }}`

**控制结构**：
- 条件语句：`{{ if condition }}...{{ else }}...{{ end }}`
- 循环语句：`{{ range $key, $value := . }}...{{ end }}`
- 上下文切换：`{{ with expression }}...{{ end }}`

**高级特性**：
- 变量声明：`{{ $var := value }}`
- 数组操作：`{{ index . "key" }}`、`{{ len . }}`
- 模板注释：`{{/* comment */}}`
- 空白控制：`{{- content -}}`

### 模板数据访问方式

在模板中访问从 KMS/OOS 获取的凭据数据，支持以下方式：

| 方式 | 语法 | 适用场景 |
| ---- | ---- | -------- |
| 直接访问 | `{{ .key }}` | 访问顶层字段 |
| index 函数 | `{{ index . "key" }}` | 键名包含特殊字符时 |
| jsonPath | `{{ jsonPath .config "path.to.field" }}` | 访问嵌套 JSON 字段（推荐） |
| fromJson + index | `{{ index (.config \| fromJson) "field" }}` | 先解析 JSON 再访问 |

**示例对比**：

```yaml
# 假设 KMS 凭据内容: {"database": {"host": "db.example.com", "port": 5432}}
# 凭据名称为 db_config

target:
  template:
    data:
      # 方式 1: 直接访问（适用于简单字段）
      simple_field: "{{ .db_config }}"
      
      # 方式 2: jsonPath（推荐，适用于嵌套 JSON）
      db_host: "{{ jsonPath .db_config \"database.host\" }}"
      db_port: "{{ jsonPath .db_config \"database.port\" }}"
      
      # 方式 3: fromJson + index
      db_host_alt: "{{ index (.db_config | fromJson) \"database\" | index \"host\" }}"
```

> 对于嵌套 JSON 数据，推荐使用 `jsonPath` 方式，语法简洁且可读性更好。

### Sprig 函数支持

ACK Secret Manager 基于 [Sprig v3](https://github.com/Masterminds/sprig/) 提供超过 70 个模板函数支持。以下是核心函数分类：

**字符串处理**：`upper`/`lower`/`title`、`trim`/`trimAll`/`trimPrefix`/`trimSuffix`、`replace`、`repeat`、`substr`、`contains`/`hasPrefix`/`hasSuffix`、`quote`/`squote`、`cat`、`nospace`、`trunc`/`abbrev`、`snakecase`/`camelcase`/`kebabcase`

**数学运算**：`add`/`sub`/`mul`/`div`、`addf`/`subf`/`mulf`/`divf`、`max`/`min`、`ceil`/`floor`/`round`、`mod`

**类型转换**：`atoi`、`int`/`int64`/`float64`、`toString`/`toStrings`

**默认值和条件**：`default`、`empty`、`coalesce`、`ternary`

**JSON 处理**：`fromJson`/`mustFromJson`、`toJson`/`mustToJson`、`toPrettyJson`、`toRawJson`

**编解码**：`b64enc`/`b64dec`、`b32enc`/`b32dec`、`sha256sum`/`sha1sum`

**集合操作**：`list`/`dict`、`get`/`set`/`hasKey`、`keys`/`values`、`first`/`last`、`uniq`/`without`

**日期时间**：`now`、`date`/`dateInZone`、`dateModify`

**正则表达式**：`regexMatch`/`regexFind`/`regexReplaceAll`、`regexSplit`

> 所有函数均来自标准 Sprig v3 库，支持管道操作和函数组合使用。

### 自定义函数

为了补充 Sprig 函数的不足，实现了以下实用函数：

#### 密码哈希函数

| 函数 | 说明 | 用法示例 |
| ---- | ---- | -------- |
| `bcrypt` | Bcrypt 密码哈希 | `{{ bcrypt "password123" }}` 或 `{{ bcrypt "password123" 12 }}`（自定义成本） |
| `htpasswd` | Htpasswd 格式（HTTP 基本认证） | `{{ htpasswd "admin" "password123" }}` |

#### JSON 处理函数

| 函数 | 说明 | 用法示例 |
| ---- | ---- | -------- |
| `jsonPath` | JSON 路径查询（点号表示法） | `{{ jsonPath .config "database.host" }}`、`{{ jsonPath .users "0.name" }}` |
| `mergeJson` | 深度合并两个 JSON 对象 | `{{ mergeJson .base .override }}` |
| `parseKeyValue` | Key=Value 格式解析为字典 | `{{ (parseKeyValue .data).KEY1 }}` |
| `toLines` | 多行字符串分割为数组 | `{{ range toLines .hosts }}` |

#### parseKeyValue 使用说明

`parseKeyValue` 将 `key=value` 格式的字符串解析为 `map[string]string`，需通过 `.key` 语法或 `index` 函数访问：

**正确用法**：
- `{{ $kv := parseKeyValue .data }}` 然后 `{{ index $kv "KEY1" }}`
- `{{ (parseKeyValue .data).KEY1 }}` — 链式调用访问特定键
- 配合 `templateAs: KeysAndValues` 使用，输出会被再次解析为独立的 Secret keys

**错误用法**：
- `{{ parseKeyValue .data }}` — 会输出 map 字符串表示，不是期望结果
- `{{ range parseKeyValue .data }}` — map 不能直接用于 range

**工作原理**：
1. KMS 返回数据为字符串 `"key1=value1\nkey2=value2"`
2. `parseKeyValue` 解析为 `map[string]string{"key1": "value1", "key2": "value2"}`
3. 模板通过 `.key1` 或 `index` 访问 map 字段

> 所有自定义函数都采用"安全失败"策略，出错时返回空值或原值，不会导致模板执行中断。

### KeysAndValues 工作流程

当使用 `templateAs: KeysAndValues` 时，数据经过**两层解析**：

1. **第一层（模板内）**：模板引擎执行，输出 `key=value` 格式字符串
2. **第二层（模板外）**：`KeysAndValues` 将输出按行分割，提取 `key=value` 对为独立的 Secret keys

**示例**：KMS 原始数据 `host=db.example.com\nport=5432`
- 模板输出：`DB_HOST=db.example.com\nDB_PORT=5432`
- KeysAndValues 解析后：`DB_HOST` → `"db.example.com"`，`DB_PORT` → `"5432"`

**解析行为**：
- 每行一个 key=value 对，用换行符分隔
- 行首尾空格以及 key/value 两侧的空格会自动去除（如 `KEY1= value1` 可正常解析），空行会被跳过
- 不包含 `=` 分隔符的行会被忽略

## 模板使用方式

### 内联模板（Inline Templates）

直接在 ExternalSecret 的 `target.template.data` 中编写 Go 模板表达式，支持：
- 字符串处理（大小写转换、拼接、截断等）
- 条件逻辑（根据环境生成不同配置）
- 函数调用（Sprig 函数和自定义函数）
- JSON 格式化（使用 `fromJson`/`toJson` 处理嵌套结构）

完整示例请参考 [examples/template/template-01-basic.yaml](../examples/template/template-01-basic.yaml)

### TemplateFrom 引用

从外部资源引用模板，实现模板复用。支持三种引用来源：

| 引用方式 | 配置字段 | 说明 |
| -------- | -------- | ---- |
| ConfigMap | `templateFrom[].configMap` | 从 ConfigMap 的 key 中读取模板内容 |
| Secret | `templateFrom[].secret` | 从 Secret 的 key 中读取模板内容 |
| 字面量 | `templateFrom[].literal` | 直接内联模板字符串 |

> **`literal` 说明**：`templateFrom[].literal` 条目的渲染结果固定写入名为 `literal` 的 key；配置多个 `literal` 条目时会互相覆盖（`literal` key 下最终只保留其中一个条目的渲染结果）。如需产出多个 key，请改用 `templateFrom[].configMap`/`secret` 配合 `templateAs: KeysAndValues`，或使用内联 `template.data`。

**模板作用域（templateAs）**：

| 值 | 说明 |
| -- | ---- |
| `Values`（默认） | 仅处理模板值，整个输出作为单个 Secret key 的值 |
| `KeysAndValues` | 先执行模板，再按 `key=value` 解析为多个独立的 Secret keys |

**目标位置（target）**：

| 值 | 说明 |
| -- | ---- |
| `Data`（默认） | 将模板结果存入 Secret data |
| `Annotations` | 将模板结果存入 Secret annotations |
| `Labels` | 将模板结果存入 Secret labels |

完整示例请参考 [examples/template/template-02-template-from.yaml](../examples/template/template-02-template-from.yaml)

### 合并策略（mergePolicy）

控制模板结果与原始 `data` 数据的合并方式：

| 策略 | 原始 data | 模板 data | 适用场景 |
| ---- | --------- | --------- | -------- |
| `Replace`（默认） | 被清空 | 完全替换 | 只需要模板输出的数据 |
| `Merge` | 保留 | 覆盖同名 key，新增新 key | 需要保留部分原始数据 |

完整示例请参考 [examples/template/template-03-merge-policy.yaml](../examples/template/template-03-merge-policy.yaml)

### 模板验证规则

模板处理器在生成最终 Secret 前会验证输出格式的合法性：

| 目标位置 | 验证规则 | 不合法处理 |
| -------- | -------- | -------- |
| Secret data keys | 必须符合 Kubernetes 命名规范 | 自动过滤 |
| Annotations keys | 必须符合 DNS subdomain 格式 | 自动过滤 |
| Labels keys | 必须符合 DNS label 格式 | 自动过滤 |

不合法的 key 会被自动过滤，不会导致 Secret 创建失败。

> 更多生产场景示例（微服务配置、TLS 证书、多环境管理等）请参考 [examples/template/template-04-advanced-scenarios.yaml](../examples/template/template-04-advanced-scenarios.yaml)

### 数据拉取失败时的行为

配置了模板解析时，若数据拉取失败，控制器自 v0.6.5 起按以下两层语义处理：

1. **跳过写入（fail-closed）是默认保护行为，不受 `cleanupSecretOnFailure` 影响**：模板可能引用或遍历任意同步键，使用部分数据渲染可能产生错误的 Secret，因此失败轮次跳过 Secret 写入、保留旧 Secret。
2. **删除行为由 `cleanupSecretOnFailure` 单独控制**：仅当所有数据源全量失败且 `cleanupSecretOnFailure=true` 时，删除契约优先于跳过写入，控制器会删除集群 Secret；此时即使模板能渲染出静态内容，也不会被写入。

| 失败范围 | cleanupSecretOnFailure | 处理行为 |
| -------- | ---------------------- | -------- |
| 部分失败（部分数据源成功） | 任意 | 整体跳过 Secret 写入，保留旧 Secret |
| 全量失败（所有数据源失败） | `false`（默认） | 跳过 Secret 写入，保留旧 Secret |
| 全量失败（所有数据源失败） | `true` | 删除集群 Secret；模板渲染出的静态内容永远不会被写入 |

**说明**：

- 即使模板只包含静态内容、全量失败时也能渲染成功，只要未满足上述删除条件，失败轮次同样不会写入任何模板输出——失败可见性由 `status.dataSyncResults` 与控制器日志承载。
- 部分失败时无论 `cleanupSecretOnFailure` 如何取值都不会删除 Secret。
- 所有数据源恢复后，下一个成功轮次会正常渲染并写入模板。
- 完整的失败处理矩阵（含未配置模板的 ExternalSecret）请参考[高级用法指南 - 同步失败处理语义](advanced_usage_zh.md#同步失败处理语义)

> 模板致命错误（如模板语法解析错误）在数据源全部成功的轮次会产生 `template_processing_fatal` 并零写入；若同一轮数据源也存在失败，数据源失败契约保持优先，模板错误降级为 `template_processing_errors` 报告。

> **零产出守卫（fail-closed）**：数据源全部成功但轮次产出 0 个 key 时（如 `dataProcess[].extract` 引用的后端文档被清空为 `{}`/空字符串），控制器跳过 Secret 写入、不触发 cleanup 删除，保留已有 Secret，并在 `status.dataSyncResults` 中上报 `zero_output_guard` 条目。该判定基于模板处理前的数据，模板静态内容不会掩盖零产出信号；故意清空后端文档不再导致 Secret 被清空/删除。守卫触发轮次，模板元数据（Labels/Annotations）更新同样被推迟。**模板后零产出守卫**覆盖渲染阶段的同类缺口：所有数据源成功且源数据非空，但模板渲染产出 0 个数据键时（Replace 模式下内联数据模板全部执行失败，或 Data 目标的 `templateFrom` 渲染出零个有效键），同样跳过写入、扣住删除，并上报 `template_zero_output_guard` 条目；仅元数据目标的 `templateFrom` 不会误触发，因为原始数据会被保留。详见[高级用法指南 - 同步失败处理语义](advanced_usage_zh.md#同步失败处理语义)。

## 常见问题与排查

### Q1: 模板输出为空或不符合预期

**可能原因**：JSON 路径不正确、数据类型不匹配、键不存在、`parseKeyValue` 使用方式错误

**排查步骤**：
1. 查看 ExternalSecret 配置和生成的 Secret：`kubectl get externalsecret <name> -o yaml` / `kubectl get secret <name> -o yaml`
2. 查看控制器日志：`kubectl logs -l app=ack-secret-manager --tail=100`
3. 简化模板测试：先用最简单的 `{{ .key }}` 验证数据是否存在，再添加复杂逻辑
4. 验证 `parseKeyValue` 使用：必须通过 `{{ (parseKeyValue .data).key }}` 访问具体字段，不能直接输出 map

### Q2: jsonPath 返回空值

**可能原因**：路径拼写错误、大小写不匹配、数组索引越界、数据结构不匹配

**调试技巧**：先输出整个 JSON 对象查看结构 `{{ .data }}`，再逐步深入访问

### Q3: KeysAndValues 解析失败

**现象**：模板执行成功，但 Secret 中没有预期的 keys

**可能原因**：
- 模板输出不是 `key=value` 格式（如某行不包含 `=` 分隔符，该行会被忽略）
- 缺少换行符分隔

> 说明：行首尾空格以及 key/value 两侧的空格不会导致解析失败，会自动去除。

**验证方法**：`kubectl describe externalsecret <name>` 查看模板实际输出

### Q4: 默认值不生效

**可能原因**：字段实际存在但值为空字符串（`default` 函数对空字符串不生效）、类型不匹配

**解决方案**：使用 `{{ if .port }}{{ .port }}{{ else }}8080{{ end }}` 或 `{{ coalesce .port 8080 }}`

### Q5: 特殊字符导致模板解析失败

**问题**：密码中包含 `$`、`{`、`}` 等特殊字符

**解决方案**：使用 `{{ .password | quote }}` 添加引号保护，或 `{{ printf "%s" .password }}`

### Q6: 循环引用或递归过深

**现象**：控制器崩溃或报 stack overflow

**原因**：`mergeJson` 深度合并时遇到循环引用。已自动检测循环引用，如遇到此问题请检查数据结构。

### 通用调试技巧

1. **查看控制器日志**：`kubectl logs -l app=ack-secret-manager --tail=100` 查看模板处理详情与错误（组件未提供额外的日志级别开关）
2. **分步验证**：不要一次性写复杂的模板，逐步验证每个步骤的输出
3. **参考测试用例**：查看 `test/e2e/template_test.go` 和 `test/e2e/advanced_template_test.go` 获取更多正确使用的示例

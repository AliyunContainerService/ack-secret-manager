# ACK Secret Manager 模板解析完整指南

## 1. 模板解析简介

### 简单理解

模板解析是在创建Kubernetes Secret之前，对从云服务商（如阿里云KMS）获取的原始密钥数据进行自动转换和处理的功能。

**举个例子**：

- 原始数据：KMS中存储的密码 `mypassword123`
- 处理后：生成 `DATABASE_PASSWORD=mypassword123` 格式的环境变量

### 核心价值

1. **自动化转换**：避免手动编辑Secret的繁琐操作
2. **格式标准化**：统一转换为应用程序需要的格式
3. **环境适配**：根据不同环境生成相应的配置
4. **数据整合**：将多个数据源组合成完整的配置

### 基础概念

- **数据源**：来自KMS/OOS等云服务的原始密钥数据
- **模板**：定义如何转换和处理这些数据的规则
- **目标Secret**：最终生成的Kubernetes Secret资源

## 2. 模板解析字段定义详解

### 2.1 ExternalSecretSpec 结构

```go
type ExternalSecretSpec struct {
    Provider         string               `json:"provider,omitempty"`
    Data             []DataSource         `json:"data,omitempty"`
    DataProcess      []DataProcess        `json:"dataProcess,omitempty"`
    Type             string               `json:"type,omitempty"`
    Target           ExternalSecretTarget `json:"target,omitempty"`
    RotationInterval *metav1.Duration     `json:"rotationInterval,omitempty"`
}
```

### 2.2 ExternalSecretTarget 结构

```go
type ExternalSecretTarget struct {
    Name            string               `json:"name,omitempty"`
    Template        *ExternalSecretTemplate `json:"template,omitempty"`
}
```

### 2.3 ExternalSecretTemplate 结构

```go
type ExternalSecretTemplate struct {
    Data     map[string]string              `json:"data,omitempty"`
    TemplateFrom []TemplateFrom             `json:"templateFrom,omitempty"`
    Metadata *ExternalSecretTemplateMetadata `json:"metadata,omitempty"`
    Type     corev1.SecretType              `json:"type,omitempty"`
    MergePolicy TemplateMergePolicy         `json:"mergePolicy,omitempty"`
}
```

### 2.4 TemplateFrom 结构

```go
type TemplateFrom struct {
    ConfigMap *TemplateRef `json:"configMap,omitempty"`
    Secret    *TemplateRef `json:"secret,omitempty"`
    Target    TemplateTarget `json:"target,omitempty"`  // Data, Annotations, Labels
    Literal   *string      `json:"literal,omitempty"`
}
```

### 2.5 TemplateRef 结构

```go
type TemplateRef struct {
    Name  string            `json:"name"`
    Items []TemplateRefItem `json:"items"`
}

type TemplateRefItem struct {
    Key        string       `json:"key"`
    TemplateAs TemplateScope `json:"templateAs,omitempty"`  // Values, KeysAndValues
}
```

### 2.6 策略枚举类型

```go
// TemplateMergePolicy 定义模板结果与原始数据的合并策略
type TemplateMergePolicy string
const (
    MergePolicyReplace TemplateMergePolicy = "Replace"  // 替换模式（默认值）
    MergePolicyMerge   TemplateMergePolicy = "Merge"    // 合并模式
)

// TemplateTarget 定义模板结果的目标字段
type TemplateTarget string
const (
    TemplateTargetData        TemplateTarget = "Data"        // 存入 Secret data（默认值）
    TemplateTargetAnnotations TemplateTarget = "Annotations" // 存入 Secret annotations
    TemplateTargetLabels      TemplateTarget = "Labels"      // 存入 Secret labels
)

// TemplateScope 定义模板键的解释方式
type TemplateScope string
const (
    TemplateScopeValues        TemplateScope = "Values"        // 仅处理值
    TemplateScopeKeysAndValues TemplateScope = "KeysAndValues" // 处理键值对
)
```

### 2.7 TemplateScopeKeysAndValues 工作流程

当使用 `TemplateScope: KeysAndValues` 时，数据会经过**两层解析**过程：

```
┌─────────────────────────────────────────────────────────────┐
│ 第 0 层：KMS 中的原始数据                                      │
│ "host=db.example.com\nport=5432\nuser=admin"                │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ 第 1 层：DataSource 加载                                       │
│ .data = "host=db.example.com\nport=5432\nuser=admin"        │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ 第 2 层：模板引擎执行 parseKeyValue                            │
│ {{ (parseKeyValue .data).host }} → "db.example.com"         │
│ {{ (parseKeyValue .data).port }} → "5432"                   │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ 第 3 层：模板输出 key=value 格式字符串                          │
│ "DB_HOST=db.example.com\nDB_PORT=5432"                      │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ 第 4 层：TemplateScopeKeysAndValues 解析                       │
│ 将输出按行分割，提取 key=value 对                               │
│ DB_HOST → "db.example.com"                                  │
│ DB_PORT → "5432"                                            │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ 第 5 层：最终生成的 Secret                                     │
│ data:                                                       │
│   DB_HOST: "db.example.com"                                 │
│   DB_PORT: "5432"                                           │
└─────────────────────────────────────────────────────────────┘
```

**💡 关键点**:

1. **第一层解析（模板内）**: `parseKeyValue` 将输入数据从 key=value 字符串转为 map
2. **第二层解析（模板外）**: `TemplateScopeKeysAndValues` 将模板输出从 key=value 字符串转为独立的 Secret keys

**示例配置**:

```yaml
# ConfigMap 中的模板
data:
  db-config: |
    DB_HOST={{ (parseKeyValue .data).host }}
    DB_PORT={{ (parseKeyValue .data).port }}

# ExternalSecret 引用
spec:
  target:
    template:
      templateFrom:
      - configMap:
          name: db-template
          items:
          - key: db-config
            templateAs: KeysAndValues  # 触发第二层解析
```

**常见错误**:

❌ **错误**: 在模板中直接使用 `{{ parseKeyValue .data }}`
✅ **正确**: 必须访问具体的键 `{{ (parseKeyValue .data).host }}`

❌ **错误**: 期望 `.host` 直接可用
✅ **正确**: 需要使用 `{{ (parseKeyValue .data).host }}` 或 `{{ index (parseKeyValue .data) "host" }}`

## 3. 模板解析实现逻辑

### 3.1 支持的模板功能

#### 3.1.1 Go 模板基础语法

**基础操作**：

- 直接字段访问：`.key`
- 管道操作：`{{ .key | func }}`

**控制结构**：

- 条件语句：`{{ if condition }}...{{ else }}...{{ end }}`
- 循环语句：`{{ range $key, $value := . }}...{{ end }}`
- 上下文切换：`{{ with expression }}...{{ end }}`

**高级特性**：

- 变量声明：`{{ $var := value }}`
- 数组操作：`{{ index . "key" }}`, `{{ len . }}`
- 模板注释：`{{/* comment */}}`
- 空白控制：`{{- content -}}`

#### 3.1.2 Sprig 函数支持

ACK Secret Manager 基于 [Sprig v3](https://github.com/Masterminds/sprig/) 提供超过70个模板函数支持。以下是核心函数分类：

##### 字符串处理函数

- `upper`/`lower`/`title` - 大小写转换
- `trim`/`trimAll`/`trimPrefix`/`trimSuffix` - 字符串修剪
- `replace` - 字符串替换
- `repeat` - 字符串重复
- `substr` - 子字符串提取
- `contains`/`hasPrefix`/`hasSuffix` - 字符串包含检查
- `quote`/`squote` - 添加引号
- `cat` - 字符串连接
- `nospace` - 移除空格
- `trunc`/`abbrev` - 字符串截断
- `snakecase`/`camelcase`/`kebabcase` - 命名格式转换

##### 数学运算函数

- `add`/`sub`/`mul`/`div` - 基本算术运算
- `addf`/`subf`/`mulf`/`divf` - 浮点数运算
- `max`/`min` - 最值计算
- `ceil`/`floor`/`round` - 数值取整
- `mod` - 取模运算

##### 类型转换函数

- `atoi` - 字符串转整数
- `int`/`int64`/`float64` - 数值类型转换
- `toString`/`toStrings` - 转换为字符串

##### 默认值和条件函数

- `default` - 设置默认值
- `empty` - 检查空值
- `coalesce` - 返回首个非空值
- `ternary` - 三元条件操作

##### JSON处理函数

- `fromJson`/`mustFromJson` - JSON解析
- `toJson`/`mustToJson` - JSON序列化
- `toPrettyJson`/`mustToPrettyJson` - 格式化JSON
- `toRawJson`/`mustToRawJson` - 原始JSON输出

##### 编码解码函数

- `b64enc`/`b64dec` - Base64编解码
- `b32enc`/`b32dec` - Base32编解码
- `sha256sum`/`sha1sum` - 哈希计算

##### 集合操作函数

- `list`/`dict` - 创建集合
- `get`/`set`/`hasKey` - 集合操作
- `keys`/`values` - 获取键值
- `first`/`last` - 集合元素访问
- `uniq`/`without` - 集合去重和过滤

##### 日期时间函数

- `now` - 当前时间
- `date`/`dateInZone` - 日期格式化
- `dateModify` - 日期修改

##### 正则表达式函数

- `regexMatch`/`regexFind`/`regexReplaceAll` - 正则操作
- `regexSplit` - 正则分割

注意：所有函数均来自标准Sprig v3库，支持管道操作和函数组合使用。

#### 3.1.3 自定义函数扩展

为了补充 Sprig 函数的不足，我们实现了以下实用函数：

##### 密码哈希函数

**bcrypt** - Bcrypt 密码哈希

```yaml
# 使用默认成本 (cost=10)
{{ bcrypt "password123" }}

# 使用自定义成本
{{ bcrypt "password123" 12 }}
```

输出示例：$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy

**htpasswd** - Htpasswd 格式（用于 HTTP 基本认证）

```yaml
{{ htpasswd "admin" "password123" }}
```

输出示例：admin:$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy

##### JSON 处理函数

**jsonPath** - JSON 路径查询（点号表示法）

```yaml
# 嵌套字段访问
{{ jsonPath .config "database.host" }}

# 数组索引访问
{{ jsonPath .users "0.name" }}

# 深层嵌套访问
{{ jsonPath .data "a.b.c.d" }}
```

优势：

- ✅ 比多层 index 嵌套更简洁
- ✅ 支持数组索引访问
- ✅ 错误处理更安全

**mergeJson** - 深度合并两个 JSON 对象

```yaml
{{ mergeJson .base .override }}
```

示例：

```yaml
base: '{"a": 1, "b": 2}'
override: '{"b": 3, "c": 4}'
结果：'{"a":1,"b":3,"c":4}'
```

##### 字符串处理函数

**parseKeyValue** - Key=Value 格式解析为字典

```yaml
{{ $kv := parseKeyValue .envFile }}
{{ index $kv "DATABASE_URL" }}
```

输入示例：

```plaintext
DATABASE_URL=postgres://localhost/db
DEBUG=true
# Comment
API_KEY=secret123
```

**⚠️ 重要使用说明**

`parseKeyValue` 返回的是 Go 语言的 `map[string]string` 对象，在模板中需要通过 `.key` 语法或 `index` 函数访问，而不是直接展开。

❌ **错误用法**:

```yaml
# 这会输出 "map[KEY1:value1 KEY2:value2]" 字符串，不是期望的结果
{{ parseKeyValue .data }}

# 这也会导致编译错误
{{ range parseKeyValue .data }}  # map 不能直接用于 range
```

✅ **正确用法**:

```yaml
# 方式 1: 使用变量存储
{{ $kv := parseKeyValue .data }}
{{ index $kv "KEY1" }}

# 方式 2: 链式调用访问特定键
{{ (parseKeyValue .data).KEY1 }}

# 方式 3: 在 TemplateScopeKeysAndValues 中使用
# ConfigMap 模板内容:
DB_HOST={{ (parseKeyValue .data).host }}
DB_PORT={{ (parseKeyValue .data).port }}
# 输出会被 TemplateScopeKeysAndValues 再次解析为独立的 key
```

**💡 工作原理**:

```
1. KMS 返回的数据是字符串格式："key1=value1\nkey2=value2"
   ↓
2. parseKeyValue 将其解析为 map[string]string
   {"key1": "value1", "key2": "value2"}
   ↓
3. 模板通过 .key1 或 index 访问 map 的字段
   {{ (parseKeyValue .data).key1 }} → "value1"
```

**典型应用场景**:

```yaml
# 场景 1: 解析.env 文件
{{ $env := parseKeyValue .envFile }}
DATABASE_URL: {{ index $env "DATABASE_URL" }}
API_KEY: {{ index $env "API_KEY" }}

# 场景 2: 在 TemplateScopeKeysAndValues 中使用
# ConfigMap db-template:
DB_HOST={{ (parseKeyValue .db_config).host }}
DB_PORT={{ (parseKeyValue .db_config).port }}

# ExternalSecret:
spec:
  target:
    template:
      templateFrom:
      - configMap:
          name: db-template
          items:
          - key: db-template
            templateAs: KeysAndValues  # 自动解析为 DB_HOST 和 DB_PORT 两个独立的 key
```

**toLines** - 多行字符串分割为数组

```yaml
{{ range toLines .hosts }}
- host: {{ . }}
{{ end }}
```

注意：所有自定义函数都采用"安全失败"策略，出错时返回空值或原值，不会导致模板执行中断

### 3.2 模板处理流程

```
原始数据获取 → 执行内联模板 → 执行 TemplateFrom 引用 → 验证结果 → 按目标分类 → 创建/更新 Secret
```

## 4. 模板解析使用方式

### 4.1 内联模板（Inline Templates）

直接在 ExternalSecret 定义中编写模板：

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: inline-template-example
spec:
  provider: kms
  data:
    - key: database-config
      name: db_config
    - key: app-settings
      name: app_settings
  target:
    template:
      data:
        # 条件处理
        is_production: |
          {{ if eq .environment "prod" }}true{{ else }}false{{ end }}
  
        # 数据转换和格式化
        database_url: |
          postgresql://{{ .db_user }}:{{ .db_password }}@{{ .db_host }}:{{ .db_port }}/{{ .db_name }}
  
        # 使用 Sprig 函数
        config_json: |
          {
            "appName": "{{ .app_name | quote }}",
            "version": "{{ .version | default "1.0.0" }}",
            "debug": {{ .debug_mode | default "false" }}
          }
  
        # 使用 jsonPath 访问嵌套 JSON
        db_host: |
          {{ jsonPath .db_config "host" }}
        db_port: |
          {{ jsonPath .db_config "port" }}

        # 数组操作
        server_ports: |
          {{ range .ports | fromJson }}{{ . }} {{ end }}
```

### 4.2 TemplateFrom 引用

从外部资源引用模板：

#### 4.2.1 ConfigMap 引用

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: db-template-config
data:
  db-connection: |
    host={{ .db_host }}
    port={{ .db_port }}
    database={{ .db_name }}
    username={{ .db_user }}
    password={{ .db_password }}

---
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: configmap-template-example
spec:
  provider: kms
  data:
    - key: database-credentials
      name: db_creds
  target:
    template:
      templateFrom:
        - configMap:
            name: db-template-config
            items:
              - key: db-connection
                templateAs: KeysAndValues
          target: Data
```

#### 4.2.2 Secret 引用

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: api-template-secret
type: Opaque
data:
  api-config: YXBpX2tleT17eyAuR2V0RGF0YSAiYXBpX2tleSIgfX0KY2xpZW50X3NlY3JldD17eyAuR2V0RGF0YSAiY2xpZW50X3NlY3JldCIgfX0=

---
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: secret-template-example
spec:
  provider: kms
  data:
    - key: api-credentials
      name: api_creds
  target:
    template:
      templateFrom:
        - secret:
            name: api-template-secret
            items:
              - key: api-config
                templateAs: KeysAndValues
          target: Data
```

#### 4.2.3 字面量模板

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: literal-template-example
spec:
  provider: kms
  data:
    - key: app-status
      name: status
  target:
    template:
      templateFrom:
        - literal: |
            APP_ENV={{ .status" | upper }}
            APP_READY={{ if eq .status "ready" }}true{{ else }}false{{ end }}
          target: Data
```

### 4.3 目标配置（Target Configuration）

#### 4.3.1 Data 目标（默认）

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: data-target-example
spec:
  provider: kms
  target:
    template:
      templateFrom:
        - configMap:
            name: app-config
            items:
              - key: app-settings
          target: Data  # 默认值，可省略
```

#### 4.3.2 Annotations 目标

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: annotations-target-example
spec:
  provider: kms
  target:
    template:
      templateFrom:
        - configMap:
            name: app-metadata
            items:
              - key: app-info
                templateAs: KeysAndValues
          target: Annotations
```

#### 4.3.3 Labels 目标

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: labels-target-example
spec:
  provider: kms
  target:
    template:
      templateFrom:
        - configMap:
            name: app-labels
            items:
              - key: env-labels
                templateAs: KeysAndValues
          target: Labels
```

### 4.4 模板作用域（Template Scope）

#### 4.4.1 Values 作用域（默认）

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: values-template
data:
  greeting: Hello {{ .username }}!

---
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: values-scope-example
spec:
  provider: kms
  target:
    template:
      templateFrom:
        - configMap:
            name: values-template
            items:
              - key: greeting
                templateAs: Values  # 默认值，处理整个值作为单个密钥
```

#### 4.4.2 KeysAndValues 作用域

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: kv-template
data:
  db-config: |
    DB_HOST={{ .host }}
    DB_PORT={{ .port }}
    DB_NAME={{ .database }}

---
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: kv-scope-example
spec:
  provider: kms
  target:
    template:
      templateFrom:
        - configMap:
            name: kv-template
            items:
              - key: db-config
                templateAs: KeysAndValues  # 解析为多个键值对
```

### 4.5 合并策略

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: merge-policy-replace-example
spec:
  provider: kms
  data:
    - key: db-password
      name: password
    - key: db-host
      name: host
  target:
    template:
      mergePolicy: Replace  # 默认值，可省略
      data:
        DATABASE_URL: "postgresql://{{ .host }}:5432/mydb"
```

可用策略：

- `Replace`：清空原始数据，完全使用模板生成的数据
- `Merge`：保留原始数据，用模板结果覆盖同名 keys

执行结果：

- Secret 中只有 DATABASE_URL 一个 key
- 原始的 password 和 host 被清空

## 5. 模板解析使用示例

### 5.1 基础示例

#### 5.1.1 简单数据转换

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: basic-transform-example
spec:
  provider: kms
  data:
    - key: user-credentials
      name: credentials
  target:
    template:
      data:
        # 基本字符串处理
        username_upper: {{ .credentials.username | upper }}
        email_normalized: {{ .credentials.email | lower | trim }}
  
        # 条件逻辑
        is_admin: |
          {{ if eq .credentials.role "admin" }}true{{ else }}false{{ end }}
  
        # 默认值处理
        timeout: {{ .credentials.timeout | default "30" }}
  
        # JSON 格式化
        user_profile: |
          {
            "username": "{{ .credentials.username }}",
            "email": "{{ .credentials.email }}",
            "role": "{{ .credentials.role }}"
          }
```

#### 5.1.2 数据库连接字符串生成

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: database-connection-example
spec:
  provider: kms
  data:
    - key: db-host
      name: host
    - key: db-port
      name: port
    - key: db-name
      name: database
    - key: db-user
      name: username
    - key: db-password
      name: password
  target:
    template:
      data:
        # PostgreSQL 连接字符串
        postgres_url: |
          postgresql://{{ .username }}:{{ .password }}@{{ .host }}:{{ .port }}/{{ .database }}
  
        # MySQL 连接字符串
        mysql_url: |
          mysql://{{ .username }}:{{ .password }}@tcp({{ .host }}:{{ .port }})/{{ .database }}
  
        # 连接池配置
        pool_config: |
          {
            "max_connections": {{ .max_connections | default "10" | int }},
            "min_connections": {{ .min_connections | default "2" | int }},
            "max_idle_time": "{{ .max_idle_time | default "30m" }}"
          }
```

### 5.2 高级示例

#### 5.2.1 微服务配置生成

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: service-template
data:
  service-config: |
# 服务基本信息
    SERVICE_NAME={{ index .service | fromJson) "name" }}
    SERVICE_VERSION={{ index .service | fromJson) "name" }}

    # 环境配置
    ENVIRONMENT={{ .environment | upper }}
    DEBUG={{ if eq .environment "dev" }}true{{ else }}false{{ end }}

    # 数据库配置
    DATABASE_URL=postgresql://{{ index .db | fromJson "user" }}:{{ index .db | fromJson "password" }}@{{ index .db | fromJson "host" }}:{{ index .db | fromJson "port" }}/{{ index .db | fromJson "name" }}

    # 缓存配置
    REDIS_URL=redis://{{ index .redis | fromJson "host" }}:{{ index .redis | fromJson "port" }}/{{ index .redis | fromJson "db" | default "0" }}

    # 日志配置
    LOG_LEVEL={{ index .log | fromJson "level" | default "info" }}
    LOG_FORMAT={{ index .log | fromJson "format" | default "json" }}

---
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: microservice-config-example
spec:
  provider: kms
  data:
    - key: service-metadata
      name: service
    - key: environment-config
      name: environment
    - key: database-creds
      name: db
    - key: redis-config
      name: redis
    - key: logging-config
      name: log
  target:
    name: service-configuration
    template:
      templateFrom:
        - configMap:
            name: service-template
            items:
              - key: service-config
                templateAs: KeysAndValues
          target: Data
      metadata:
        labels:
          app: {{ index .service | fromJson) "name" }}
          version: {{ index .service | fromJson) "version" }}
          environment: {{ .environment }}
        annotations:
          last-updated: {{ now | date "2006-01-02T15:04:05Z07:00" }}
          config-source: kms
```

#### 5.2.2 Kubernetes 资源清单生成

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: k8s-manifest-example
spec:
  provider: kms
  data:
    - key: app-name
      name: app_name
    - key: app-version
      name: version
    - key: replicas
      name: replicas
    - key: image-repository
      name: image_repo
    - key: image-tag
      name: image_tag
    - key: ports
      name: ports
  target:
    template:
      data:
        # Deployment 清单
        deployment_yaml: |
          apiVersion: apps/v1
          kind: Deployment
          metadata:
            name: {{ .app_name }}
            labels:
              app: {{ .app_name }}
          spec:
            replicas: {{ .replicas | default "3" | int }}
            selector:
              matchLabels:
                app: {{ .app_name }}
            template:
              metadata:
                labels:
                  app: {{ .app_name }}
              spec:
                containers:
                - name: {{ .app_name }}
                  image: {{ .image_repo }}:{{ .image_tag }}
                  ports:
                  {{ range .ports | fromJson }}
                  - containerPort: {{ . }}
                  {{ end }}
  
        # Service 清单
        service_yaml: |
          apiVersion: v1
          kind: Service

#### 5.2.3 证书和密钥处理

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: certificate-example
spec:
  provider: kms
  data:
    - key: tls-private-key
      name: private_key
    - key: tls-certificate
      name: certificate
    - key: ca-bundle
      name: ca_bundle
  target:
    template:
      data:
        # PEM 格式处理
        tls.crt: |
          {{ .certificate }}
        tls.key: |
          {{ .private_key }}
        ca.crt: |
          {{ .ca_bundle }}
  
        # 组合证书链
        fullchain.pem: |
          {{ .certificate }}
  
          {{ .ca_bundle }}
  
        # JKS 格式（需要额外工具）
        keystore_password: {{ .keystore_password | default "changeit" }}
  
        # 应用特定格式
        nginx_ssl_cert: |
          ssl_certificate /etc/ssl/certs/tls.crt;
          ssl_certificate_key /etc/ssl/private/tls.key;
          ssl_trusted_certificate /etc/ssl/certs/ca.crt;
```

### 5.3 实际应用场景示例

#### 5.3.1 多环境配置管理

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: env-templates
data:
  dev-config: |
    LOG_LEVEL=DEBUG
    DATABASE_POOL_SIZE=5
    CACHE_TTL=300
    ENABLE_PROFILING=true
  
  staging-config: |
    LOG_LEVEL=INFO
    DATABASE_POOL_SIZE=20
    CACHE_TTL=600
    ENABLE_PROFILING=false
  
  prod-config: |
    LOG_LEVEL=WARN
    DATABASE_POOL_SIZE=50
    CACHE_TTL=1800
    ENABLE_PROFILING=false

---
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: multi-env-example
spec:
  provider: kms
  data:
    - key: current-environment
      name: environment
  target:
    template:
      templateFrom:
        - configMap:
            name: env-templates
            items:
              - key: "staging-config"
                templateAs: KeysAndValues
          target: Data
```

#### 5.3.2 动态端口配置

```yaml
apiVersion: alibabacloud.com/v1alpha1
kind: ExternalSecret
metadata:
  name: dynamic-ports-example
spec:
  provider: kms
  data:
    - key: service-ports
      name: ports
    - key: service-name
      name: service_name
  target:
    template:
      data:
        # 生成端口映射配置
        port_mappings: |
          {{ range .ports | fromJson }}
          - containerPort: {{ . }}
            hostPort: {{ add . 10000 }}
          {{ end }}
  
        # 生成健康检查配置
        health_check_ports: |
          {{ range .ports | fromJson }}
          {{ if eq . 8080 }}health_port: {{ . }}{{ end }}
          {{ end }}
  
        # 生成服务发现标签
        discovery_tags: |
          service={{ .service_name }}
          {{ range .ports | fromJson }}
          port_{{ . }}=enabled
          {{ end }}
```

## 6. 常见问题与排查

### Q1: 模板输出为空或不符合预期

**可能原因**:

- JSON 路径不正确
- 数据类型不匹配
- 键不存在
- `parseKeyValue` 使用方式错误

**排查步骤**:

1. **检查原始数据**:

   ```bash
   # 查看 ExternalSecret 配置
   kubectl get externalsecret <name> -o yaml

   # 查看生成的 Secret
   kubectl get secret <target-secret-name> -o yaml
   ```
2. **查看控制器日志**:

   ```bash
   kubectl logs -l app=ack-secret-manager --tail=100
   ```

   查找包含 `jsonPath`、`template` 等关键词的日志
3. **简化模板测试**:

   ```yaml
   # 先测试最简单的字段访问
   data:
     test: "{{ .key }}"

   # 确认数据存在后再添加复杂逻辑
   data:
     test: "{{ (parseKeyValue .data).key }}"
   ```
4. **验证 parseKeyValue 使用**:

   ```yaml
   # ❌ 错误：直接输出 map
   {{ parseKeyValue .data }}

   # ✅ 正确：访问具体字段
   {{ (parseKeyValue .data).key }}
   {{ index (parseKeyValue .data) "key" }}
   ```

### Q2: jsonPath 返回空值

**可能原因**:

- 路径拼写错误
- 大小写不匹配
- 数组索引越界
- 数据结构不匹配

**示例**:

```yaml
# 假设 KMS 中的数据是:
{ "users": [{ "name": "alice" }] }

# ❌ 错误：路径大小写不匹配
{{ jsonPath .data "Users.0.Name" }}

# ✅ 正确：
{{ jsonPath .data "users.0.name" }}

# ❌ 错误：索引越界
{{ jsonPath .data "users.5.name" }}

# ✅ 正确：
{{ jsonPath .data "users.0.name" }}
```

**调试技巧**:

```yaml
# 先输出整个 JSON 对象查看结构
debug-json: "{{ .data }}"

# 逐步深入
debug-users: "{{ (.data | fromJson).users }}"
debug-first-user: "{{ index ((.data | fromJson).users) 0 }}"
debug-name: "{{ (index ((.data | fromJson).users) 0).name }}"
```

### Q3: TemplateScopeKeysAndValues 解析失败

**现象**: 模板执行成功，但生成的 Secret 中没有预期的 keys

**可能原因**:

- 模板输出格式不是严格的 `key=value` 格式
- 行首行尾有空格
- 缺少换行符分隔

**示例**:

```yaml
# ConfigMap 中的模板
data:
  config: |
    # ❌ 错误：有多余的空格
    KEY1= value1
    KEY2 =value2
  
    # ✅ 正确：紧凑格式
    KEY1=value1
    KEY2=value2

# ExternalSecret 配置
spec:
  target:
    template:
      templateFrom:
      - configMap:
          name: config
          items:
          - key: config
            templateAs: KeysAndValues  # 会解析为两个独立的 key
```

**验证方法**:

```bash
# 查看模板实际输出
kubectl describe externalsecret <name>
```

### Q4: 默认值不生效

**现象**: 使用了 `default` 函数，但没有得到预期的默认值

**可能原因**:

- 字段实际存在但值为空字符串
- 类型不匹配（数字 vs 字符串）

**示例**:

```yaml
# 假设数据：{"port": 0}

# ❌ 错误：0 会被认为是"空值"
port: "{{ .port | default 8080 }}"  # 结果：8080

# ✅ 正确：先检查是否存在
port: "{{ if .port }}{{ .port }}{{ else }}8080{{ end }}"

# 或者使用 coalesce（返回第一个非空值）
port: "{{ coalesce .port 8080 }}"
```

### Q5: 特殊字符导致模板解析失败

**问题**: 密码中包含 `$`、`{`、`}` 等特殊字符

**解决方案**:

```yaml
# 使用 raw string 或转义
password: {{ .password | quote }}  # 添加引号保护

# 或使用 printf
password: {{ printf "%s" .password }}
```

### Q6: 循环引用或递归过深

**现象**: 控制器崩溃或报 stack overflow

**原因**: `mergeJson` 深度合并时遇到循环引用

**解决**: 已自动检测循环引用，但如果遇到此问题，请检查数据结构是否合理。

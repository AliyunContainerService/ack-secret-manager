# ACK Secret Manager Template Processing Complete Guide

## 1. Introduction to Template Processing

### Simple Explanation

Template processing is a feature that automatically transforms and processes raw secret data obtained from cloud service providers (such as Alibaba Cloud KMS) before creating Kubernetes Secrets.

**Example**:

- Raw data: Password `mypassword123` stored in KMS
- Processed result: Environment variable in `DATABASE_PASSWORD=mypassword123` format

### Core Value

1. **Automated Transformation**: Avoid tedious manual editing of Secrets
2. **Format Standardization**: Uniformly convert to formats required by applications
3. **Environment Adaptation**: Generate corresponding configurations for different environments
4. **Data Integration**: Combine multiple data sources into complete configurations

### Basic Concepts

- **Data Source**: Raw secret data from KMS/OOS and other cloud services
- **Template**: Rules defining how to transform and process this data
- **Target Secret**: The final generated Kubernetes Secret resource

## 2. Detailed Template Processing Field Definitions

### 2.1 ExternalSecretSpec Structure

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

### 2.2 ExternalSecretTarget Structure

```go
type ExternalSecretTarget struct {
    Name            string               `json:"name,omitempty"`
    Template        *ExternalSecretTemplate `json:"template,omitempty"`
}
```

### 2.3 ExternalSecretTemplate Structure

```go
type ExternalSecretTemplate struct {
    Data     map[string]string              `json:"data,omitempty"`
    TemplateFrom []TemplateFrom             `json:"templateFrom,omitempty"`
    Metadata *ExternalSecretTemplateMetadata `json:"metadata,omitempty"`
    Type     corev1.SecretType              `json:"type,omitempty"`
    MergePolicy TemplateMergePolicy         `json:"mergePolicy,omitempty"`
}
```

### 2.4 TemplateFrom Structure

```go
type TemplateFrom struct {
    ConfigMap *TemplateRef `json:"configMap,omitempty"`
    Secret    *TemplateRef `json:"secret,omitempty"`
    Target    TemplateTarget `json:"target,omitempty"`  // Data, Annotations, Labels
    Literal   *string      `json:"literal,omitempty"`
}
```

### 2.5 TemplateRef Structure

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

### 2.6 Policy Enumeration Types

```go
// TemplateMergePolicy defines the merge policy for template results with original data
type TemplateMergePolicy string
const (
    MergePolicyReplace TemplateMergePolicy = "Replace"  // Replace mode (default)
    MergePolicyMerge   TemplateMergePolicy = "Merge"    // Merge mode
)

// TemplateTarget defines the target field for template results
type TemplateTarget string
const (
    TemplateTargetData        TemplateTarget = "Data"        // Store in Secret data (default)
    TemplateTargetAnnotations TemplateTarget = "Annotations" // Store in Secret annotations
    TemplateTargetLabels      TemplateTarget = "Labels"      // Store in Secret labels
)

// TemplateScope defines how template keys are interpreted
type TemplateScope string
const (
    TemplateScopeValues        TemplateScope = "Values"        // Process values only
    TemplateScopeKeysAndValues TemplateScope = "KeysAndValues" // Process key-value pairs
)
```

### 2.7 TemplateScopeKeysAndValues Workflow

When using `TemplateScope: KeysAndValues`, data undergoes a **two-layer parsing** process:

```
┌─────────────────────────────────────────────────────────────┐
│ Layer 0: Raw data in KMS                                     │
│ "host=db.example.com\nport=5432\nuser=admin"                │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 1: DataSource Loading                                  │
│ .data = "host=db.example.com\nport=5432\nuser=admin"        │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 2: Template Engine executes parseKeyValue              │
│ {{ (parseKeyValue .data).host }} → "db.example.com"         │
│ {{ (parseKeyValue .data).port }} → "5432"                   │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 3: Template outputs key=value format string            │
│ "DB_HOST=db.example.com\nDB_PORT=5432"                      │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 4: TemplateScopeKeysAndValues Parsing                  │
│ Split output by lines, extract key=value pairs               │
│ DB_HOST → "db.example.com"                                  │
│ DB_PORT → "5432"                                            │
└────────────────────┬────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 5: Final Generated Secret                              │
│ data:                                                       │
│   DB_HOST: "db.example.com"                                 │
│   DB_PORT: "5432"                                           │
└─────────────────────────────────────────────────────────────┘
```

**💡 Key Points**:

1. **First layer parsing (inside template)**: `parseKeyValue` converts input data from key=value string to map
2. **Second layer parsing (outside template)**: `TemplateScopeKeysAndValues` converts template output from key=value string to independent Secret keys

**Example Configuration**:

```yaml
# Template in ConfigMap
data:
  db-config: |
    DB_HOST={{ (parseKeyValue .data).host }}
    DB_PORT={{ (parseKeyValue .data).port }}

# ExternalSecret reference
spec:
  target:
    template:
      templateFrom:
      - configMap:
          name: db-template
          items:
          - key: db-config
            templateAs: KeysAndValues  # Trigger second layer parsing
```

**Common Mistakes**:

❌ **Wrong**: Using `{{ parseKeyValue .data }}` directly in template
✅ **Correct**: Must access specific keys `{{ (parseKeyValue .data).host }}`

❌ **Wrong**: Expecting `.host` to be directly available
✅ **Correct**: Need to use `{{ (parseKeyValue .data).host }}` or `{{ index (parseKeyValue .data) "host" }}`

## 3. Template Processing Implementation Logic

### 3.1 Supported Template Features

#### 3.1.1 Go Template Basic Syntax

**Basic Operations**:

- Direct field access: `.key`
- Pipeline operation: `{{ .key | func }}`

**Control Structures**:

- Conditional statements: `{{ if condition }}...{{ else }}...{{ end }}`
- Loop statements: `{{ range $key, $value := . }}...{{ end }}`
- Context switching: `{{ with expression }}...{{ end }}`

**Advanced Features**:

- Variable declaration: `{{ $var := value }}`
- Array operations: `{{ index . "key" }}`, `{{ len . }}`
- Template comments: `{{/* comment */}}`
- Whitespace control: `{{- content -}}`

#### 3.1.2 Sprig Function Support

ACK Secret Manager provides over 70 template functions based on [Sprig v3](https://github.com/Masterminds/sprig/). Here are the core function categories:

##### String Processing Functions

- `upper`/`lower`/`title` - Case conversion
- `trim`/`trimAll`/`trimPrefix`/`trimSuffix` - String trimming
- `replace` - String replacement
- `repeat` - String repetition
- `substr` - Substring extraction
- `contains`/`hasPrefix`/`hasSuffix` - String containment check
- `quote`/`squote` - Add quotes
- `cat` - String concatenation
- `nospace` - Remove spaces
- `trunc`/`abbrev` - String truncation
- `snakecase`/`camelcase`/`kebabcase` - Naming format conversion

##### Mathematical Functions

- `add`/`sub`/`mul`/`div` - Basic arithmetic operations
- `addf`/`subf`/`mulf`/`divf` - Floating point operations
- `max`/`min` - Maximum/minimum calculation
- `ceil`/`floor`/`round` - Number rounding
- `mod` - Modulo operation

##### Type Conversion Functions

- `atoi` - String to integer
- `int`/`int64`/`float64` - Numeric type conversion
- `toString`/`toStrings` - Convert to string

##### Default and Conditional Functions

- `default` - Set default value
- `empty` - Check for empty value
- `coalesce` - Return first non-empty value
- `ternary` - Ternary conditional operation

##### JSON Processing Functions

- `fromJson`/`mustFromJson` - JSON parsing
- `toJson`/`mustToJson` - JSON serialization
- `toPrettyJson`/`mustToPrettyJson` - Formatted JSON
- `toRawJson`/`mustToRawJson` - Raw JSON output

##### Encoding/Decoding Functions

- `b64enc`/`b64dec` - Base64 encoding/decoding
- `b32enc`/`b32dec` - Base32 encoding/decoding
- `sha256sum`/`sha1sum` - Hash calculation

##### Collection Operations Functions

- `list`/`dict` - Create collections
- `get`/`set`/`hasKey` - Collection operations
- `keys`/`values` - Get keys/values
- `first`/`last` - Collection element access
- `uniq`/`without` - Collection deduplication and filtering

##### Date/Time Functions

- `now` - Current time
- `date`/`dateInZone` - Date formatting
- `dateModify` - Date modification

##### Regular Expression Functions

- `regexMatch`/`regexFind`/`regexReplaceAll` - Regex operations
- `regexSplit` - Regex splitting

Note: All functions come from the standard Sprig v3 library and support pipeline operations and function composition.

#### 3.1.3 Custom Function Extensions

To supplement Sprig functions, we have implemented the following utility functions:

##### Password Hash Functions

**bcrypt** - Bcrypt password hashing

```yaml
# Use default cost (cost=10)
{{ bcrypt "password123" }}

# Use custom cost
{{ bcrypt "password123" 12 }}
```

Output example: $2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy

**htpasswd** - Htpasswd format (for HTTP basic authentication)

```yaml
{{ htpasswd "admin" "password123" }}
```

Output example: admin:$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy

##### JSON Processing Functions

**jsonPath** - JSON path query (dot notation)

```yaml
# Nested field access
{{ jsonPath .config "database.host" }}

# Array index access
{{ jsonPath .users "0.name" }}

# Deep nested access
{{ jsonPath .data "a.b.c.d" }}
```

Advantages:

- ✅ More concise than nested index calls
- ✅ Supports array index access
- ✅ Safer error handling

**mergeJson** - Deep merge two JSON objects

```yaml
{{ mergeJson .base .override }}
```

Example:

```yaml
base: '{"a": 1, "b": 2}'
override: '{"b": 3, "c": 4}'
Result: '{"a":1,"b":3,"c":4}'
```

##### String Processing Functions

**parseKeyValue** - Parse Key=Value format to dictionary

```yaml
{{ $kv := parseKeyValue .envFile }}
{{ index $kv "DATABASE_URL" }}
```

Input example:

```plaintext
DATABASE_URL=postgres://localhost/db
DEBUG=true
# Comment
API_KEY=secret123
```

**⚠️ Important Usage Notes**

`parseKeyValue` returns a Go language `map[string]string` object. In templates, you need to access it using `.key` syntax or `index` function, not by direct expansion.

❌ **Wrong Usage**:

```yaml
# This will output "map[KEY1:value1 KEY2:value2]" string, not the expected result
{{ parseKeyValue .data }}

# This will also cause compilation error
{{ range parseKeyValue .data }}  # map cannot be used directly in range
```

✅ **Correct Usage**:

```yaml
# Method 1: Store in variable
{{ $kv := parseKeyValue .data }}
{{ index $kv "KEY1" }}

# Method 2: Chain call to access specific key
{{ (parseKeyValue .data).KEY1 }}

# Method 3: Use in TemplateScopeKeysAndValues
# ConfigMap template content:
DB_HOST={{ (parseKeyValue .data).host }}
DB_PORT={{ (parseKeyValue .data).port }}
# Output will be parsed by TemplateScopeKeysAndValues again into independent keys
```

**💡 Working Principle**:

```
1. Data returned by KMS is string format: "key1=value1\nkey2=value2"
   ↓
2. parseKeyValue parses it into map[string]string
   {"key1": "value1", "key2": "value2"}
   ↓
3. Template accesses map fields through .key1 or index
   {{ (parseKeyValue .data).key1 }} → "value1"
```

**Typical Application Scenarios**:

```yaml
# Scenario 1: Parse .env file
{{ $env := parseKeyValue .envFile }}
DATABASE_URL: {{ index $env "DATABASE_URL" }}
API_KEY: {{ index $env "API_KEY" }}

# Scenario 2: Use in TemplateScopeKeysAndValues
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
            templateAs: KeysAndValues  # Automatically parsed as DB_HOST and DB_PORT two independent keys
```

**toLines** - Split multi-line string to array

```yaml
{{ range toLines .hosts }}
- host: {{ . }}
{{ end }}
```

Note: All custom functions adopt a "fail-safe" strategy, returning empty values or original values on error without interrupting template execution.

### 3.2 Template Processing Flow

```
Raw Data Retrieval → Execute Inline Templates → Execute TemplateFrom References → Validate Results → Classify by Target → Create/Update Secret
```

## 4. Template Processing Usage Methods

### 4.1 Inline Templates

Write templates directly in ExternalSecret definitions:

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
        # Conditional processing
        is_production: |
          {{ if eq .environment "prod" }}true{{ else }}false{{ end }}
  
        # Data transformation and formatting
        database_url: |
          postgresql://{{ .db_user }}:{{ .db_password }}@{{ .db_host }}:{{ .db_port }}/{{ .db_name }}
  
        # Use Sprig functions
        config_json: |
          {
            "appName": "{{ .app_name | quote }}",
            "version": "{{ .version | default "1.0.0" }}",
            "debug": {{ .debug_mode | default "false" }}
          }
  
        # Use jsonPath to access nested JSON
        db_host: |
          {{ jsonPath .db_config "host" }}
        db_port: |
          {{ jsonPath .db_config "port" }}

        # Array operations
        server_ports: |
          {{ range .ports | fromJson }}{{ . }} {{ end }}
```

### 4.2 TemplateFrom References

Reference templates from external resources:

#### 4.2.1 ConfigMap Reference

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

#### 4.2.2 Secret Reference

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

#### 4.2.3 Literal Template

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
            APP_ENV={{ .status | upper }}
            APP_READY={{ if eq .status "ready" }}true{{ else }}false{{ end }}
          target: Data
```

### 4.3 Target Configuration

#### 4.3.1 Data Target (Default)

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
          target: Data  # Default value, can be omitted
```

#### 4.3.2 Annotations Target

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

#### 4.3.3 Labels Target

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

### 4.4 Template Scope

#### 4.4.1 Values Scope (Default)

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
                templateAs: Values  # Default value, process entire value as single key
```

#### 4.4.2 KeysAndValues Scope

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
                templateAs: KeysAndValues  # Parse into multiple key-value pairs
```

### 4.5 Merge Policy

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
      mergePolicy: Replace  # Default value, can be omitted
      data:
        DATABASE_URL: "postgresql://{{ .host }}:5432/mydb"
```

Available policies:

- `Replace`: Clear original data, use only template-generated data
- `Merge`: Keep original data, override keys with same name using template results

Execution result:

- Secret contains only one key: DATABASE_URL
- Original password and host are cleared

## 5. Template Processing Usage Examples

### 5.1 Basic Examples

#### 5.1.1 Simple Data Transformation

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
        # Basic string processing
        username_upper: {{ .credentials.username | upper }}
        email_normalized: {{ .credentials.email | lower | trim }}
  
        # Conditional logic
        is_admin: |
          {{ if eq .credentials.role "admin" }}true{{ else }}false{{ end }}
  
        # Default value handling
        timeout: {{ .credentials.timeout | default "30" }}
  
        # JSON formatting
        user_profile: |
          {
            "username": "{{ .credentials.username }}",
            "email": "{{ .credentials.email }}",
            "role": "{{ .credentials.role }}"
          }
```

#### 5.1.2 Database Connection String Generation

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
        # PostgreSQL connection string
        postgres_url: |
          postgresql://{{ .username }}:{{ .password }}@{{ .host }}:{{ .port }}/{{ .database }}
  
        # MySQL connection string
        mysql_url: |
          mysql://{{ .username }}:{{ .password }}@tcp({{ .host }}:{{ .port }})/{{ .database }}
  
        # Connection pool configuration
        pool_config: |
          {
            "max_connections": {{ .max_connections | default "10" | int }},
            "min_connections": {{ .min_connections | default "2" | int }},
            "max_idle_time": "{{ .max_idle_time | default "30m" }}"
          }
```

### 5.2 Advanced Examples

#### 5.2.1 Microservice Configuration Generation

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: service-template
data:
  service-config: |
    # Service basic information
    SERVICE_NAME={{ index (.service | fromJson) "name" }}
    SERVICE_VERSION={{ index (.service | fromJson) "version" }}

    # Environment configuration
    ENVIRONMENT={{ .environment | upper }}
    DEBUG={{ if eq .environment "dev" }}true{{ else }}false{{ end }}

    # Database configuration
    DATABASE_URL=postgresql://{{ index (.db | fromJson) "user" }}:{{ index (.db | fromJson) "password" }}@{{ index (.db | fromJson) "host" }}:{{ index (.db | fromJson) "port" }}/{{ index (.db | fromJson) "name" }}

    # Cache configuration
    REDIS_URL=redis://{{ index (.redis | fromJson) "host" }}:{{ index (.redis | fromJson) "port" }}/{{ index (.redis | fromJson) "db" | default "0" }}

    # Log configuration
    LOG_LEVEL={{ index (.log | fromJson) "level" | default "info" }}
    LOG_FORMAT={{ index (.log | fromJson) "format" | default "json" }}

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
          app: {{ index (.service | fromJson) "name" }}
          version: {{ index (.service | fromJson) "version" }}
          environment: {{ .environment }}
        annotations:
          last-updated: {{ now | date "2006-01-02T15:04:05Z07:00" }}
          config-source: kms
```

#### 5.2.2 Kubernetes Resource Manifest Generation

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
        # Deployment manifest
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
  
        # Service manifest
        service_yaml: |
          apiVersion: v1
          kind: Service
          metadata:
            name: {{ .app_name }}-service
            labels:
              app: {{ .app_name }}
          spec:
            selector:
              app: {{ .app_name }}
            ports:
            {{ range .ports | fromJson }}
            - protocol: TCP
              port: {{ . }}
              targetPort: {{ . }}
            {{ end }}
            type: ClusterIP
```

#### 5.2.3 Certificate and Key Handling

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
        # PEM format processing
        tls.crt: |
          {{ .certificate }}
        tls.key: |
          {{ .private_key }}
        ca.crt: |
          {{ .ca_bundle }}
  
        # Combined certificate chain
        fullchain.pem: |
          {{ .certificate }}
  
          {{ .ca_bundle }}
  
        # JKS format (requires additional tools)
        keystore_password: {{ .keystore_password | default "changeit" }}
  
        # Application-specific format
        nginx_ssl_cert: |
          ssl_certificate /etc/ssl/certs/tls.crt;
          ssl_certificate_key /etc/ssl/private/tls.key;
          ssl_trusted_certificate /etc/ssl/certs/ca.crt;
```

### 5.3 Real-World Application Scenarios

#### 5.3.1 Multi-Environment Configuration Management

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

#### 5.3.2 Dynamic Port Configuration

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
        # Generate port mapping configuration
        port_mappings: |
          {{ range .ports | fromJson }}
          - containerPort: {{ . }}
            hostPort: {{ add . 10000 }}
          {{ end }}
  
        # Generate health check configuration
        health_check_ports: |
          {{ range .ports | fromJson }}
          {{ if eq . 8080 }}health_port: {{ . }}{{ end }}
          {{ end }}
  
        # Generate service discovery labels
        discovery_tags: |
          service={{ .service_name }}
          {{ range .ports | fromJson }}
          port_{{ . }}=enabled
          {{ end }}
```

## 6. Common Issues and Troubleshooting

### Q1: Template output is empty or unexpected

**Possible Causes**:

- Incorrect JSON path
- Data type mismatch
- Key does not exist
- Incorrect `parseKeyValue` usage

**Troubleshooting Steps**:

1. **Check raw data**:

   ```bash
   # View ExternalSecret configuration
   kubectl get externalsecret <name> -o yaml

   # View generated Secret
   kubectl get secret <target-secret-name> -o yaml
   ```
2. **View controller logs**:

   ```bash
   kubectl logs -l app=ack-secret-manager --tail=100
   ```

   Look for logs containing keywords like `jsonPath`, `template`, etc.
3. **Simplify template testing**:

   ```yaml
   # Test simplest field access first
   data:
     test: "{{ .key }}"

   # Add complex logic after confirming data exists
   data:
     test: "{{ (parseKeyValue .data).key }}"
   ```
4. **Verify parseKeyValue usage**:

   ```yaml
   # ❌ Wrong: Directly output map
   {{ parseKeyValue .data }}

   # ✅ Correct: Access specific fields
   {{ (parseKeyValue .data).key }}
   {{ index (parseKeyValue .data) "key" }}
   ```

### Q2: jsonPath returns empty value

**Possible Causes**:

- Path spelling error
- Case mismatch
- Array index out of bounds
- Data structure mismatch

**Example**:

```yaml
# Assuming data in KMS is:
{ "users": [{ "name": "alice" }] }

# ❌ Wrong: Path case mismatch
{{ jsonPath .data "Users.0.Name" }}

# ✅ Correct:
{{ jsonPath .data "users.0.name" }}

# ❌ Wrong: Index out of bounds
{{ jsonPath .data "users.5.name" }}

# ✅ Correct:
{{ jsonPath .data "users.0.name" }}
```

**Debugging Tips**:

```yaml
# Output entire JSON object first to check structure
debug-json: "{{ .data }}"

# Step by step
debug-users: "{{ (.data | fromJson).users }}"
debug-first-user: "{{ index ((.data | fromJson).users) 0 }}"
debug-name: "{{ (index ((.data | fromJson).users) 0).name }}"
```

### Q3: TemplateScopeKeysAndValues parsing fails

**Symptom**: Template executes successfully, but generated Secret lacks expected keys

**Possible Causes**:

- Template output format is not strict `key=value` format
- Leading/trailing spaces in lines
- Missing newline separators

**Example**:

```yaml
# Template in ConfigMap
data:
  config: |
    # ❌ Wrong: Extra spaces
    KEY1= value1
    KEY2 =value2
  
    # ✅ Correct: Compact format
    KEY1=value1
    KEY2=value2

# ExternalSecret configuration
spec:
  target:
    template:
      templateFrom:
      - configMap:
          name: config
          items:
          - key: config
            templateAs: KeysAndValues  # Will parse as two independent keys
```

**Verification Method**:

```bash
# View actual template output
kubectl describe externalsecret <name>
```

### Q4: Default values not working

**Symptom**: Used `default` function but didn't get expected default value

**Possible Causes**:

- Field actually exists but value is empty string
- Type mismatch (number vs string)

**Example**:

```yaml
# Assuming data: {"port": 0}

# ❌ Wrong: 0 will be considered "empty value"
port: "{{ .port | default 8080 }}"  # Result: 8080

# ✅ Correct: Check existence first
port: "{{ if .port }}{{ .port }}{{ else }}8080{{ end }}"

# Or use coalesce (returns first non-empty value)
port: "{{ coalesce .port 8080 }}"
```

### Q5: Special characters cause template parsing failure

**Problem**: Password contains special characters like `$`, `{`, `}`

**Solution**:

```yaml
# Use raw string or escaping
password: {{ .password | quote }}  # Add quotes for protection

# Or use printf
password: {{ printf "%s" .password }}
```

### Q6: Circular reference or excessive recursion

**Symptom**: Controller crashes or reports stack overflow

**Cause**: Circular reference encountered during `mergeJson` deep merge

**Solution**: Circular references are automatically detected, but if you encounter this issue, please check if the data structure is reasonable.

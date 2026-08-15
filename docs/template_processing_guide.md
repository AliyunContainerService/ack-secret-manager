# ACK Secret Manager Template Processing Guide

> This document describes the template processing features of ack-secret-manager, including template syntax, custom functions, usage patterns, and common troubleshooting.

## Table of Contents

- [Template Processing Overview](#template-processing-overview)
- [Template Configuration Fields](#template-configuration-fields)
- [Template Syntax and Functions](#template-syntax-and-functions)
- [Template Usage Patterns](#template-usage-patterns)
- [Troubleshooting](#troubleshooting)

## Template Processing Overview

### What Is Template Processing?

Template processing is a feature that automatically transforms and processes raw secret data fetched from cloud providers (such as Alibaba Cloud KMS) before creating Kubernetes Secrets.

**Example**:
- Raw data: Password `mypassword123` stored in KMS
- After processing: Generates `DATABASE_PASSWORD=mypassword123` formatted environment variable

### Core Benefits

1. **Automated Transformation**: Avoid manual editing of Secrets
2. **Format Standardization**: Uniformly convert to the format your application needs
3. **Environment Adaptation**: Generate configurations based on different environments
4. **Data Integration**: Combine multiple data sources into complete configurations

### Basic Concepts

- **Data Source**: Raw secret data from KMS/OOS and other cloud services
- **Template**: Rules that define how to transform and process data
- **Target Secret**: The final Kubernetes Secret resource generated

### Template Processing Flow

```
Raw data fetch → Execute inline templates → Execute TemplateFrom references → Validate results → Categorize by target → Create/update Secret
```

## Template Configuration Fields

Template-related configuration is located under `spec.target.template` in ExternalSecret. For complete CRD field definitions, refer to [CRD Resources Guide - ExternalSecret](crd_resources_guide.md#externalsecret).

Field overview:

| Field | Type | Description |
| ----- | ---- | ----------- |
| `data` | `map[string]string` | Inline templates defining Secret data key-values |
| `templateFrom` | `[]TemplateFrom` | Reference templates from external resources (ConfigMap/Secret/Literal) |
| `metadata` | `ExternalSecretTemplateMetadata` | Templated annotations and labels |
| `type` | `SecretType` | Target Secret type (e.g., `kubernetes.io/tls`) |
| `mergePolicy` | `Replace`/`Merge` | Merge strategy between template results and original data |

## Template Syntax and Functions

### Go Template Basic Syntax

**Basic Operations**:
- Direct field access: `.key`
- Pipeline operations: `{{ .key | func }}`

**Control Structures**:
- Conditional: `{{ if condition }}...{{ else }}...{{ end }}`
- Loop: `{{ range $key, $value := . }}...{{ end }}`
- Context switching: `{{ with expression }}...{{ end }}`

**Advanced Features**:
- Variable declaration: `{{ $var := value }}`
- Array operations: `{{ index . "key" }}`, `{{ len . }}`
- Template comments: `{{/* comment */}}`
- Whitespace control: `{{- content -}}`

### Template Data Access Methods

Access credential data retrieved from KMS/OOS in templates using the following methods:

| Method | Syntax | Use Case |
| ------ | ------ | -------- |
| Direct access | `{{ .key }}` | Access top-level fields |
| index function | `{{ index . "key" }}` | When key names contain special characters |
| jsonPath | `{{ jsonPath .config "path.to.field" }}` | Access nested JSON fields (recommended) |
| fromJson + index | `{{ index (.config \| fromJson) "field" }}` | Parse JSON first, then access |

**Example Comparison**:

```yaml
# Assume KMS credential content: {"database": {"host": "db.example.com", "port": 5432}}
# Credential name is db_config

target:
  template:
    data:
      # Method 1: Direct access (for simple fields)
      simple_field: "{{ .db_config }}"
      
      # Method 2: jsonPath (recommended for nested JSON)
      db_host: "{{ jsonPath .db_config \"database.host\" }}"
      db_port: "{{ jsonPath .db_config \"database.port\" }}"
      
      # Method 3: fromJson + index
      db_host_alt: "{{ index (.db_config | fromJson) \"database\" | index \"host\" }}"
```

> For nested JSON data, `jsonPath` is recommended for its concise and readable syntax.

### Sprig Function Support

ACK Secret Manager provides over 70 template functions based on [Sprig v3](https://github.com/Masterminds/sprig/). Core function categories:

**String Processing**: `upper`/`lower`/`title`, `trim`/`trimAll`/`trimPrefix`/`trimSuffix`, `replace`, `repeat`, `substr`, `contains`/`hasPrefix`/`hasSuffix`, `quote`/`squote`, `cat`, `nospace`, `trunc`/`abbrev`, `snakecase`/`camelcase`/`kebabcase`

**Math Operations**: `add`/`sub`/`mul`/`div`, `addf`/`subf`/`mulf`/`divf`, `max`/`min`, `ceil`/`floor`/`round`, `mod`

**Type Conversion**: `atoi`, `int`/`int64`/`float64`, `toString`/`toStrings`

**Default & Conditional**: `default`, `empty`, `coalesce`, `ternary`

**JSON Processing**: `fromJson`/`mustFromJson`, `toJson`/`mustToJson`, `toPrettyJson`, `toRawJson`

**Encoding/Decoding**: `b64enc`/`b64dec`, `b32enc`/`b32dec`, `sha256sum`/`sha1sum`

**Collection Operations**: `list`/`dict`, `get`/`set`/`hasKey`, `keys`/`values`, `first`/`last`, `uniq`/`without`

**Date/Time**: `now`, `date`/`dateInZone`, `dateModify`

**Regular Expressions**: `regexMatch`/`regexFind`/`regexReplaceAll`, `regexSplit`

> All functions come from the standard Sprig v3 library and support pipeline operations and function composition.

### Custom Functions

To complement Sprig functions, the following utility functions are implemented:

#### Password Hash Functions

| Function | Description | Usage Example |
| -------- | ----------- | ------------- |
| `bcrypt` | Bcrypt password hash | `{{ bcrypt "password123" }}` or `{{ bcrypt "password123" 12 }}` (custom cost) |
| `htpasswd` | Htpasswd format (HTTP basic auth) | `{{ htpasswd "admin" "password123" }}` |

#### JSON Processing Functions

| Function | Description | Usage Example |
| -------- | ----------- | ------------- |
| `jsonPath` | JSON path query (dot notation) | `{{ jsonPath .config "database.host" }}`, `{{ jsonPath .users "0.name" }}` |
| `mergeJson` | Deep merge two JSON objects | `{{ mergeJson .base .override }}` |
| `parseKeyValue` | Parse Key=Value format into dictionary | `{{ (parseKeyValue .data).KEY1 }}` |
| `toLines` | Split multi-line string into array | `{{ range toLines .hosts }}` |

#### parseKeyValue Usage Notes

`parseKeyValue` parses `key=value` formatted strings into `map[string]string`. Access values via `.key` syntax or `index` function:

**Correct Usage**:
- `{{ $kv := parseKeyValue .data }}` then `{{ index $kv "KEY1" }}`
- `{{ (parseKeyValue .data).KEY1 }}` — chained call to access specific keys
- Use with `templateAs: KeysAndValues`, output will be parsed again into independent Secret keys

**Incorrect Usage**:
- `{{ parseKeyValue .data }}` — outputs map string representation, not expected results
- `{{ range parseKeyValue .data }}` — map cannot be used directly with range

**How It Works**:
1. KMS returns data as string `"key1=value1\nkey2=value2"`
2. `parseKeyValue` parses into `map[string]string{"key1": "value1", "key2": "value2"}`
3. Template accesses map fields via `.key1` or `index`

> All custom functions use a "safe failure" strategy — returning empty values or original values on error, without interrupting template execution.

### KeysAndValues Workflow

When using `templateAs: KeysAndValues`, data goes through **two layers of parsing**:

1. **First Layer (inside template)**: Template engine executes, outputs `key=value` formatted strings
2. **Second Layer (outside template)**: `KeysAndValues` splits output by lines, extracting `key=value` pairs as independent Secret keys

**Example**: KMS raw data `host=db.example.com\nport=5432`
- Template output: `DB_HOST=db.example.com\nDB_PORT=5432`
- After KeysAndValues parsing: `DB_HOST` → `"db.example.com"`, `DB_PORT` → `"5432"`

**Parsing Behavior**:
- One key=value pair per line, separated by newlines
- Leading/trailing whitespace on each line and around the key and value is trimmed automatically (e.g., `KEY1= value1` is parsed normally); empty lines are skipped
- Lines without an `=` separator are ignored

## Template Usage Patterns

### Inline Templates

Write Go template expressions directly in ExternalSecret's `target.template.data`, supporting:
- String processing (case conversion, concatenation, truncation, etc.)
- Conditional logic (generate different configurations based on environment)
- Function calls (Sprig functions and custom functions)
- JSON formatting (handle nested structures with `fromJson`/`toJson`)

See [examples/template/template-01-basic.yaml](../examples/template/template-01-basic.yaml) for complete examples

### TemplateFrom References

Reference templates from external resources for template reuse. Supports three reference sources:

| Reference Type | Configuration Field | Description |
| -------------- | ------------------- | ----------- |
| ConfigMap | `templateFrom[].configMap` | Read template content from a ConfigMap key |
| Secret | `templateFrom[].secret` | Read template content from a Secret key |
| Literal | `templateFrom[].literal` | Inline template string |

> **Note on `literal`**: the rendered result of a `templateFrom[].literal` entry is always written to a fixed key named `literal`; when multiple `literal` entries are configured, their results overwrite each other (only the rendered result of one is kept under the `literal` key). If you need multiple keys, use `templateFrom[].configMap`/`secret` with `templateAs: KeysAndValues` or inline `template.data` instead.

**Template Scope (templateAs)**:

| Value | Description |
| ----- | ----------- |
| `Values` (default) | Only process template values, entire output as a single Secret key's value |
| `KeysAndValues` | Execute template first, then parse `key=value` into multiple independent Secret keys |

**Target Location (target)**:

| Value | Description |
| ----- | ----------- |
| `Data` (default) | Store template results in Secret data |
| `Annotations` | Store template results in Secret annotations |
| `Labels` | Store template results in Secret labels |

See [examples/template/template-02-template-from.yaml](../examples/template/template-02-template-from.yaml) for complete examples

### Merge Policy (mergePolicy)

Controls how template results merge with original `data`:

| Policy | Original Data | Template Data | Use Case |
| ------ | ------------- | ------------- | -------- |
| `Replace` (default) | Cleared | Completely replaced | Only need template output |
| `Merge` | Preserved | Override same-name keys, add new keys | Need to keep some original data |

See [examples/template/template-03-merge-policy.yaml](../examples/template/template-03-merge-policy.yaml) for complete examples

### Template Validation Rules

The template processor validates output format legality before generating the final Secret:

| Target Location | Validation Rule | Invalid Handling |
| --------------- | --------------- | ---------------- |
| Secret data keys | Must follow Kubernetes naming conventions | Automatically filtered |
| Annotations keys | Must follow DNS subdomain format | Automatically filtered |
| Labels keys | Must follow DNS label format | Automatically filtered |

Invalid keys are automatically filtered and will not cause Secret creation failures.

> For more production scenario examples (microservice configuration, TLS certificates, multi-environment management, etc.), see [examples/template/template-04-advanced-scenarios.yaml](../examples/template/template-04-advanced-scenarios.yaml)

### Behavior on Data Fetch Failure

When template processing is configured, the controller handles data fetch failures with the following two-layer semantics (since v0.6.5):

1. **Skipping the write (fail-closed) is the default protection and is independent of `cleanupSecretOnFailure`**: templates may reference or iterate over any synced key, so rendering with partial data could produce an incorrect Secret. The Secret write is therefore skipped on failure rounds and the previous Secret is retained.
2. **Deletion is controlled separately by `cleanupSecretOnFailure`**: only when all data sources fail and `cleanupSecretOnFailure=true`, the deletion contract takes precedence over skipping the write and the controller deletes the cluster Secret; template-rendered static content is never written in that case.

| Failure Scope | cleanupSecretOnFailure | Handling Behavior |
| ------------- | ---------------------- | ----------------- |
| Partial failure (some data sources succeeded) | Any | Secret write skipped entirely, previous Secret retained |
| Total failure (all data sources failed) | `false` (default) | Secret write skipped, previous Secret retained |
| Total failure (all data sources failed) | `true` | Cluster Secret deleted; template-rendered static content is never written |

**Notes**:

- Even if a template contains only static content and could render successfully on total failure, no template output is written during failure rounds unless the deletion condition above is met — failure visibility is carried by `status.dataSyncResults` and controller logs instead.
- Partial failures never delete the Secret, regardless of `cleanupSecretOnFailure`.
- Once all data sources recover, the next successful round renders and writes the template normally.
- For the complete failure handling matrix (including ExternalSecrets without templates), see [Advanced Usage Guide - Sync Failure Handling Semantics](advanced_usage.md#sync-failure-handling-semantics)

> A fatal template error (e.g. a template parse error) on a round where all data sources succeeded results in `template_processing_fatal` with zero writes. When data sources also failed in the same round, the data-source failure contracts keep precedence and the template error is reported as `template_processing_errors` instead.

> **Zero-output guard (fail-closed)**: when all data sources succeed but the round produces 0 keys (e.g. a backend document referenced by `dataProcess[].extract` has been emptied to `{}`/empty string), the controller skips the Secret write, withholds cleanup deletion, retains the existing Secret, and reports a `zero_output_guard` entry in `status.dataSyncResults`. The decision is based on the pre-template data, so template static content cannot mask the zero-output signal; intentionally emptying the backend document no longer clears/deletes the Secret. On guard rounds, template metadata (Labels/Annotations) updates are deferred as well. A **post-template counterpart** covers the rendering stage: when all data sources succeeded and the source data was non-empty, but template rendering produces 0 data keys (Replace mode with every inline data template failing execution, or a Data-targeted `templateFrom` rendering zero valid keys), the write is skipped and deletion withheld as well, reporting a `template_zero_output_guard` entry; a metadata-only `templateFrom` never triggers it because the raw data is preserved. See [Advanced Usage Guide - Sync Failure Handling Semantics](advanced_usage.md#sync-failure-handling-semantics).

## Troubleshooting

### Q1: Template output is empty or unexpected

**Possible Causes**: Incorrect JSON path, data type mismatch, key doesn't exist, incorrect `parseKeyValue` usage

**Troubleshooting Steps**:
1. Check ExternalSecret config and generated Secret: `kubectl get externalsecret <name> -o yaml` / `kubectl get secret <name> -o yaml`
2. Check controller logs: `kubectl logs -l app=ack-secret-manager --tail=100`
3. Simplify template testing: Start with the simplest `{{ .key }}` to verify data exists, then add complex logic
4. Verify `parseKeyValue` usage: Must access specific fields via `{{ (parseKeyValue .data).key }}`, cannot output map directly

### Q2: jsonPath returns empty

**Possible Causes**: Path typo, case mismatch, array index out of bounds, data structure mismatch

**Debugging Tip**: Output the entire JSON object first with `{{ .data }}`, then progressively access deeper

### Q3: KeysAndValues parsing fails

**Symptom**: Template executes successfully, but Secret doesn't have expected keys

**Possible Causes**:
- Template output is not in `key=value` format (e.g., a line without any `=` separator is ignored)
- Missing newline separators

> Note: leading/trailing whitespace on lines and around keys/values does not cause parsing failures — it is trimmed automatically.

**Verification**: `kubectl describe externalsecret <name>` to check actual template output

### Q4: Default values don't work

**Possible Causes**: Field exists but value is empty string (`default` doesn't work on empty strings), type mismatch

**Solution**: Use `{{ if .port }}{{ .port }}{{ else }}8080{{ end }}` or `{{ coalesce .port 8080 }}`

### Q5: Special characters cause template parsing failure

**Problem**: Passwords contain `$`, `{`, `}` and other special characters

**Solution**: Use `{{ .password | quote }}` to add quote protection, or `{{ printf "%s" .password }}`

### Q6: Circular reference or recursion too deep

**Symptom**: Controller crashes or reports stack overflow

**Cause**: `mergeJson` encounters circular references during deep merge. Circular references are auto-detected; if encountered, check data structure.

### General Debugging Tips

1. **Check controller logs**: `kubectl logs -l app=ack-secret-manager --tail=100` to view template processing details and errors (the component does not provide an additional verbosity flag)
2. **Step-by-step verification**: Don't write complex templates all at once; verify each step's output progressively
3. **Reference test cases**: Check `test/e2e/template_test.go` and `test/e2e/advanced_template_test.go` for more correct usage examples

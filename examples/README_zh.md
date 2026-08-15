# ACK Secret Manager 示例

> 本目录包含 ACK Secret Manager 的完整使用示例，按功能分类组织，涵盖认证配置、CRD 资源、高级功能、模板处理和最佳实践。
>
> 所有示例采用注释驱动风格，前提条件、验证步骤、注意事项等均以 YAML 注释形式提供，可直接使用 `kubectl apply -f` 应用。
>
> **注意**：以下示例使用了多种命令行工具完成操作，包括 [aliyun CLI](https://github.com/aliyun/aliyun-cli)、[ack-ram-tool](https://github.com/AliyunContainerService/ack-ram-tool)、`kubectl`、`helm` 等。您也可以通过[阿里云控制台](https://home.console.aliyun.com/)图形化操作，或调用阿里云 OpenAPI 完成。使用前请确保各工具已安装并完成配置，例如 aliyun CLI 需执行 `aliyun configure` 配置凭证和地域，`kubectl` 需配置 kubeconfig 以连接集群。

## 示例列表

### 认证示例（`auth/`）

| 文件 | 说明 | 相关文档 |
|------|------|----------|
| [auth-01-serviceaccount-rrsa.yaml](auth/auth-01-serviceaccount-rrsa.yaml) | ServiceAccount RRSA 认证（推荐） | [认证指南 §1](../docs/auth_guide_zh.md#1-serviceaccount-rrsa-认证推荐) |
| [auth-02-rrsa.yaml](auth/auth-02-rrsa.yaml) | RRSA 认证（环境变量 + SecretStore） | [认证指南 §2](../docs/auth_guide_zh.md#2-rrsa-认证) |
| [auth-03-ak-assume-role.yaml](auth/auth-03-ak-assume-role.yaml) | AK 扮演认证 | [认证指南 §3](../docs/auth_guide_zh.md#3-ak-扮演认证) |
| [auth-04-ak-basic.yaml](auth/auth-04-ak-basic.yaml) | AK 基础认证 | [认证指南 §4](../docs/auth_guide_zh.md#4-ak-认证) |
| [auth-05-worker-role.yaml](auth/auth-05-worker-role.yaml) | WorkerRole 认证 | [认证指南 §5](../docs/auth_guide_zh.md#5-workerrole-认证) |

### CRD 资源示例（`crd/`）

| 文件 | 说明 | 相关文档 |
|------|------|----------|
| [crd-01-secretstore.yaml](crd/crd-01-secretstore.yaml) | SecretStore 配置示例 | [CRD 指南 §SecretStore](../docs/crd_resources_guide_zh.md#secretstore) |
| [crd-02-cluster-secret-store.yaml](crd/crd-02-cluster-secret-store.yaml) | ClusterSecretStore + conditions | [CRD 指南 §ClusterSecretStore](../docs/crd_resources_guide_zh.md#clustersecretstore) |
| [crd-03-externalsecret-basic.yaml](crd/crd-03-externalsecret-basic.yaml) | 基础 ExternalSecret | [CRD 指南 §ExternalSecret](../docs/crd_resources_guide_zh.md#externalsecret) |
| [crd-04-externalsecret-multi-key.yaml](crd/crd-04-externalsecret-multi-key.yaml) | 多凭据配置 + JMESPath | [CRD 指南 §ExternalSecret](../docs/crd_resources_guide_zh.md#externalsecret) |
| [crd-05-cluster-external-secret.yaml](crd/crd-05-cluster-external-secret.yaml) | ClusterExternalSecret | [CRD 指南 §ClusterExternalSecret](../docs/crd_resources_guide_zh.md#clusterexternalsecret) |

### 高级功能示例（`advanced/`）

| 文件 | 说明 | 相关文档 |
|------|------|----------|
| [advanced-01-jmespath-parsing.yaml](advanced/advanced-01-jmespath-parsing.yaml) | JMESPath 字段提取 + dataProcess.extract 自动解析 | [高级用法 §JSON/YAML](../docs/advanced_usage_zh.md#jsonyaml-凭据解析) |
| [advanced-02-cross-account.yaml](advanced/advanced-02-cross-account.yaml) | 跨账号同步（全部方式） | [高级用法 §跨账号](../docs/advanced_usage_zh.md#跨账号同步) |
| [advanced-03-kms-endpoint.yaml](advanced/advanced-03-kms-endpoint.yaml) | KMS Endpoint 配置（含专属实例） | [高级用法 §kmsEndpoint](../docs/advanced_usage_zh.md#kmsendpoint-配置) |
| [advanced-04-credential-rotation.yaml](advanced/advanced-04-credential-rotation.yaml) | 凭据轮转配置（凭据级 + 全局） | [高级用法 §凭据轮转](../docs/advanced_usage_zh.md#凭据轮转) |
| [advanced-05-oos-parameter.yaml](advanced/advanced-05-oos-parameter.yaml) | OOS 加密参数同步 | [高级用法 §多数据源](../docs/advanced_usage_zh.md#多数据源支持) |

### 模板示例（`template/`）

| 文件 | 说明 | 相关文档 |
|------|------|----------|
| [template-01-basic.yaml](template/template-01-basic.yaml) | 基础内联模板 | [模板指南](../docs/template_processing_guide_zh.md) |
| [template-02-template-from.yaml](template/template-02-template-from.yaml) | templateFrom 用法 | [模板指南](../docs/template_processing_guide_zh.md) |
| [template-03-merge-policy.yaml](template/template-03-merge-policy.yaml) | mergePolicy 用法 | [模板指南](../docs/template_processing_guide_zh.md) |
| [template-04-advanced-scenarios.yaml](template/template-04-advanced-scenarios.yaml) | 高级场景（微服务配置、TLS 证书、多环境管理） | [模板指南](../docs/template_processing_guide_zh.md) |

### 最佳实践示例（`best-practices/`）

| 文件 | 说明 | 相关文档 |
|------|------|----------|
| [best-practices-01-multi-tenant.yaml](best-practices/best-practices-01-multi-tenant.yaml) | 多租户场景 | [认证指南 §最佳实践](../docs/auth_guide_zh.md) |
| [best-practices-02-production.yaml](best-practices/best-practices-02-production.yaml) | 生产环境配置 | [认证指南 §最佳实践](../docs/auth_guide_zh.md) |

## 快速开始

### 使用前须知

1. **替换占位符**：所有 `<accountId>`、`<clusterId>`、`<region>` 等必须替换为实际值
2. **RAM Role**：确保已创建 RAM Role 并配置正确的信任策略
3. **权限**：RAM Role 需具备 KMS/OOS 访问权限
4. **Namespace**：根据实际环境调整命名空间名称

### 场景一：单命名空间应用（推荐）

```bash
# 使用 ServiceAccount RRSA
kubectl apply -f auth/auth-01-serviceaccount-rrsa.yaml

# 验证
kubectl get externalsecret app-secret -n production
kubectl get secret app-secret -n production -o yaml
```

### 场景二：多命名空间共享认证

```bash
# 使用 ClusterSecretStore
kubectl apply -f crd/crd-02-cluster-secret-store.yaml

# 在不同命名空间创建 ExternalSecret
kubectl apply -f crd/crd-03-externalsecret-basic.yaml -n production
kubectl apply -f crd/crd-03-externalsecret-basic.yaml -n staging
```

### 场景三：自动同步到多个命名空间

```bash
# 使用 ClusterExternalSecret
kubectl apply -f crd/crd-05-cluster-external-secret.yaml

# 自动在匹配的命名空间创建 ExternalSecret
kubectl get externalsecret db-secret -A
```

### 场景四：跨账号访问

```bash
# 配置跨账号认证
kubectl apply -f advanced/advanced-02-cross-account.yaml
```

### 场景五：模板数据转换

```bash
# 使用内联模板
kubectl apply -f template/template-01-basic.yaml
```

## 完整文档

| 文档 | 说明 |
|------|------|
| [认证配置指南](../docs/auth_guide_zh.md) | 5 种认证方式的详细配置 |
| [CRD 资源指南](../docs/crd_resources_guide_zh.md) | 4 种 CRD 资源的详细说明 |
| [高级用法指南](../docs/advanced_usage_zh.md) | 凭据解析、跨账号同步、kmsEndpoint、凭据轮转、多数据源支持 |
| [模板处理指南](../docs/template_processing_guide_zh.md) | 模板功能的详细文档 |

## 安全建议

1. **生产环境**：使用 ServiceAccount RRSA 认证
2. **最小权限**：仅授予 RAM Role 必要的权限
3. **避免 AK**：不要使用明文 AccessKey 认证
4. **访问控制**：在 ClusterSecretStore 上配置 conditions 限制访问范围
5. **定期审计**：定期检查 RAM Role 权限和 ExternalSecret 状态

# ACK Secret Manager

[English](./README_EN.md) | 简体中文

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager) 可以帮助您将存储在[阿里云KMS凭据管家](https://www.alibabacloud.com/help/zh/doc-detail/152001.html) 中的密钥凭据或[阿里云OOS加密参数](https://www.alibabacloud.com/help/zh/oos/getting-started/manage-encryption-parameters), 以K8s原生Secret对象的形式导入到集群中并实现密钥数据的自动同步，您可以在应用Pod中以挂载Secret等形式将存储在凭据管家或加密参数中的密文引入到应用程序中使用，避免敏感数据在应用开发构建流程中的传播和泄露。

> **⚠️ 不兼容变更（自 v0.6.4 起）**：出于安全加固，`enableCrossNamespaceSecretStore` 与 `enableCrossNamespaceAuthRef` 的默认值由 `true` 改为 `false`。升级后，跨命名空间引用（ExternalSecret → SecretStore、SecretStore → ServiceAccount/AccessKey Secret）默认将被拒绝。若您的部署依赖跨命名空间引用，请在 `values.yaml` 中显式将 `command.enableCrossNamespaceSecretStore` 和/或 `command.enableCrossNamespaceAuthRef` 设置为 `true` 以恢复旧行为。详见 [Release Note](#release-note)。

## 📚 文档导航

| 文档 | 说明 |
|------|------|
| [📋 CRD 资源详解](../../docs/crd_resources_guide_zh.md) | 4 种 CRD 概述、跨命名空间控制、推荐用法、配置示例、字段说明 |
| [🔐 认证配置指南](../../docs/auth_guide_zh.md) | 认证方式、认证优先级、Namespace 规则 |
| [🚀 高级用法指南](../../docs/advanced_usage_zh.md) | 凭据解析、跨账号同步、kmsEndpoint 配置、凭据轮转、多数据源支持 |
| [📝 模板解析指南](../../docs/template_processing_guide_zh.md) | 模板语法、模板引用、自定义函数 |
| [👤 ServiceAccountRef](../../docs/serviceAccountRef_zh.md) | 多租户 ServiceAccount 级别 RRSA 授权 |
| [📦 使用示例](../../examples/README_zh.md) | 按分类组织的 YAML 示例 |

## 安装

1. 请确保组件使用的凭据有足够的权限访问需要同步的阿里云服务，可以使用如下两种配置方式，推荐使用RRSA方式，实现Pod维度的授权。

    - 通过 [RRSA方式](https://help.aliyun.com/document_detail/356611.html) 实现Pod维度的授权（推荐）

        * [启用RRSA功能](https://help.aliyun.com/document_detail/356611.html#section-ywl-59g-j8h)
        * [使用RRSA功能](https://help.aliyun.com/document_detail/356611.html#section-rmr-eeh-878) ：为指定的 serviceaccount 创建对应的 RAM 角色，为 RAM 角色设置信任策略，并为 RAM 角色授权
        * 通过配置在SecretStore实例中的`ramRoleARN`和`oidcProviderARN`，实现以无AK的方式将获取KMS凭据权限绑定在ack-secret-manager组件使用的指定ServiceAccount上。
        * 在多租场景下，通过在租户命名空间的SecretStore中使用`serviceAccountRef`认证方式，可以将获取KMS凭据权限细粒度控制在ServiceAccount维度。  关于SecretStore的配置方式请参考[使用serviceAccountRef实现认证](../../docs/serviceAccountRef_zh.md)。

    - 在集群对应的 WorkerRole 中添加权限

        - 登录容器服务控制台

        - 选择对应集群进入到集群详情页

        - 在集群信息中选择**集群资源**页，点击Worker RAM角色中对应的命名为**KubernetesWorkerRole-xxxxxxxxxxxxxxx** 的角色名称，会自动导航到RAM角色对应的控制台页面

        - 点击添加权限按钮，创建自定义权限策略，策略内容如下（仅授权同步服务需要的RAM策略即可，保证最小权限原则）：

      > **注意**：仅授予访问 KMS 凭据管家所需的最小权限，遵循最小权限原则。

          ```json
          {
              "Action": [
                // 阿里云KMS凭据管家所需权限
                "kms:GetSecretValue",
                "kms:Decrypt",
          
                // 阿里云OOS加密参数所需权限
                "oos:GetSecretParameter",
                "kms:GetSecretValue"
              ],
              "Resource": [
                  "*"
              ],
              "Effect": "Allow"
          }
          ```

        - 绑定上面创建的自定义策略给集群对应的WorkerRole

    - 关于所有支持的认证配置方式，请参考[官方文档](https://www.alibabacloud.com/help/zh/ack/ack-managed-and-ack-dedicated/security-and-compliance/use-ack-secret-manager-to-import-alibaba-cloud-kms-service-credentials)。

    > 💡 **认证配置指南**：5 种认证方式、配置步骤、优先级、Namespace 规则，请参考 [认证配置指南](../../docs/auth_guide_zh.md)

2. 登录到容器服务控制台

    * 在左侧导航栏选择**市场** -> **应用市场**，在搜索栏中输入ack-secret-manager，选择进入到应用页面；
    * 选择需要安装的目标集群和命名空间、发布名称；
    * 在参数配置页面进行自定义参数配置，包括 values.yaml 中的 `rrsa.enable`以及配置 `envVarsFromSecret` 中的相关参数，参数说明参见下方的**配置说明**；
    * 点击**确定**按钮完成安装。

## 更新

1. 登录到容器服务控制台；
2. 选择目标集群点击进入到集群详情页面；
3. 在左侧的导航栏选择应用-> Helm，找到 ack-secret-manager 对应的**更新**，修改配置后点击**确定**按钮完成安装。

## 卸载

1. 登录到容器服务控制台；
2. 选择目标集群点击进入到集群详情页面；
3. 在左侧的导航栏选择应用-> Helm，找到 ack-secret-manager 对应的发布，点击操作拦中的删除按钮进行删除。

## 配置说明

> **CRD 安装说明**：核心 CRD（`externalsecrets`、`secretstores`）随 Chart 无条件安装；`crds.createClusterSecretStore` 和 `crds.createClusterExternalSecret` 开关仅控制集群级 CRD（`clustersecretstores`、`clusterexternalsecrets`）是否安装。

| **参数**                                            | **说明**                                                                                                                                  | **默认值**             |
| --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- |
| crds.createClusterSecretStore                       | 是否安装 ClusterSecretStore CRD                                                                                                       | true                   |
| crds.createClusterExternalSecret                    | 是否安装 ClusterExternalSecret CRD                                                                                                    | true                   |
| env.WATCH_NAMESPACE                                 | 指定组件watch的namespace（默认空值代表watch所有命名空间）                                                                                     |                       |
| envVarsFromSecret.ACCESS_KEY_ID                     | 可以通过设置ACCESS_KEY_ID变量指定凭证AK构建SDK client，需要定义在名称为alibaba-credentials的secret实例中                                       |                        |
| envVarsFromSecret.SECRET_ACCESS_KEY                 | 可以通过设置SECRET_ACCESS_KEY变量指定凭证SK构建SDK client，需要定义在名称为alibaba-credentials的secret实例中                                   |                        |
| envVarsFromSecret.ALICLOUD_ROLE_ARN                 | 可以通过设置ALICLOUD_ROLE_ARN变量指定RAM角色ARN用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中                           |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME        | 可以通过设置ALICLOUD_ROLE_SESSION_NAME变量指定RAM角色session name用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中         |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION  | 可以通过设置ALICLOUD_ROLE_SESSION_EXPIRATION变量指定RAM角色session过期时长用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret. ALICLOUD_OIDC_PROVIDER_ARN       | 可以通过设置ALICLOUD_OIDC_PROVIDER_ARN变量指定RAM OIDC供应商的ARN用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中         |                         |
| envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE          | 可以通过设置ALICLOUD_OIDC_TOKEN_FILE变量指定pod内oidc token文件路径用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中       |                         |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_ARN          | 可以通过设置 ALICLOUD_REMOTE_ROLE_ARN 变量指定另一个账号的 RAM Role Arn，用于在跨账号拉取凭据数据时进行角色扮演                                 |                        |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_SESSION_NAME | 可以通过设置 ALICLOUD_REMOTE_ROLE_SESSION_NAME 变量指定 RAM Role Session Name，于在跨账号拉取凭据数据时进行角色扮演                            |                        |
| rrsa.enable                                         | 是否启用RRSA特性，默认为false，启用后需要配置envVarsFromSecret中的ALICLOUD_ROLE_ARN和 ALICLOUD_OIDC_PROVIDER_ARN参数                          | false                  |
| command.reconcilePeriod                             | 控制器重新协调externalSecret实例的间隔时间，默认5秒                                                                                          | 5s                     |
| command.reconcileCount                              | 指定并发协调externalSecret实例的worker数量，默认是1                                                                                         | 1                      |
| command.tokenRotationPeriod                         | 检查 client访问STS token是否过期的轮询时间                                                                                                  | 120s                   |
| command.region                                      | 从指定region拉取secret凭据                                                                                                                 |                        |
| command.kmsEndpoint                                 | 从指定endpoint拉取secret凭据                                                                                                               |                        |
| command.disablePolling                              | 关闭从后端自动同步拉取最新的凭据内容，默认false                                                                                              | false                  |
| command.pollingInterval                             | 从后端同步存量secret实例的间隔时间                                                                                                          | 120s                   |
| command.maxConcurrentSecretPulls                    | 已弃用                                                                                                                                    | -                      |
| command.maxConcurrentKmsSecretPulls                 | kms secret 每秒同步的最大并发数量                                                                                                          | 10                     |
| command.maxConcurrentOosSecretPulls                 | oos secret 每秒同步的最大并发数量                                                                                                          | 10                     |
| command.cleanupSecretOnFailure                      | 当所有数据源拉取失败（无可用数据）且该开关开启时，删除对应集群 Secret（含模板场景）；部分失败时不删除，按合并/fail-closed 策略处理                                                                                                       | false                  |
| command.processClusterSecretStore                   | 是否处理 ClusterSecretStore 资源                                                                                                           | true                   |
| command.processClusterExternalSecret                | 是否处理 ClusterExternalSecret 资源                                                                                                        | true                   |
| command.enableCrossNamespaceSecretStore             | 是否启用跨命名空间的 SecretStore 引用                                                                                                      | false                  |
| command.enableCrossNamespaceAuthRef                 | 是否允许 SecretStore 跨命名空间引用认证资源（ServiceAccount、AccessKey Secret）                                                            | false                  |
| command.enableWorkerRole                            | 是否启用 WorkerRole（ECS RAM Role）认证                                                                                                  | true                   |
| command.aliuid                                      | 用于拼接OIDC身份提供商ARN的阿里云账号                                                                                                       |                        |
| command.clusterId                                   | 用于拼接OIDC身份提供商ARN的阿里云容器服务集群ID                                                                                              |                        |
| image.repository                                    | 指定的ack-secret-manager 镜像仓库名称                                                                                                      | registry-cn-hangzhou.ack.aliyuncs.com/acs/ack-secret-manager |
| image.tag                                           | 指定的ack-secret-manager 镜像tag                                                                                                          | v0.6.6                 |
| image.pullPolicy                                    | 镜像拉取策略，默认为IfNotPresent                                                                                                           | IfNotPresent           |
| cleanupImage.repository                             | 卸载前清理 Job（helm pre-delete hook）使用的外部预构建镜像仓库名称                                                                        | registry-cn-hangzhou.ack.aliyuncs.com/acs/ack-secret-manager-cleanup |
| cleanupImage.tag                                    | 清理 Job 镜像tag                                                                                                                        | v0.4.1                 |
| cleanupImage.pullPolicy                             | 清理 Job 镜像拉取策略                                                                                                                   | IfNotPresent           |
| nameOverride                                        | 覆盖应用名称                                                                                                                              | nil                    |
| fullnameOverride                                    | 覆盖应用全名                                                                                                                              | nil                    |
| rbac.create                                         | 是否创建并使用RBAC资源，默认为true                                                                                                         | true                   |
| securityContext.fsGroup                             | 指定应用的security context配置                                                                                                            | {}                     |
| serviceAccount.create                               | 是否创建serviceaccount                                                                                                                    | true                   |
| serviceAccount.name                                 | 指定创建serviceaccount的名称                                                                                                              | 自动生成                |
| serviceAccount.annotations                          | 指定添加serviceaccount annotation标签                                                                                                     | nil                    |
| podAnnotations                                      | 指定添加到pod中的annotation标签                                                                                                           | {}                     |
| podLabels                                           | 指定添加到pod中的Label标签                                                                                                                | {}                     |
| replicaCount                                        | 控制器副本个数                                                                                                                            | 2                      |
| nodeSelector                                        | 指定的nodeSelector标签                                                                                                                    | {}                     |
| tolerations                                         | 指定的污点容忍配置                                                                                                                        | []                      |
| affinity                                            | 指定的Pod亲和性配置                                                                                                                       | {}                     |
| resources                                           | 指定的Pod requests和limits配置                                                                                                            | {}                     |

## 使用说明

下文会在阿里云 KMS 凭据管家中添加一个测试凭据进行凭据同步，并展示部分扩展功能。

提示：其他阿里云服务的密文同步方式与KMS Secrets Manager的密文同步方式相同。请执行下列步骤：

1. 参照步骤1的流程为您想要的**产品添加密文**。
2. 以下表中提供的对应字段配置说明**更新下面 `SecretStore`和`ExternalSecret`示例中的配置字段**。

| 配置字段                               | 阿里云 KMS 凭据                | 阿里云 OOS 加密参数               |
| -------------------------------------- | ------------------------------ | --------------------------------- |
| **SecretStore 认证方式关键字**   | SecretStore.Spec.KMS.KMSAuth   | SecretStore.Spec.OOS.OOSAuth      |
| **ExternalSecret.spec.provider** | kms                            | oos                               |
| **ExternalSecret.spec.data.key** | <KMS 凭据名称>(如下文的 test1) | <OOS加密参数名称>(如下文的 test2) |

### 1. 创建密文

当前支持同步 KMS 凭据和 OOS 加密参数，下面分别是两种密文的创建方式参考

- 在KMS凭据管家中添加如下凭证，详细流程请参考[管理通用凭据](https://www.alibabacloud.com/help/zh/doc-detail/152003.html)

  ```txt
  SecretName: test1
  SecretData: {"name":"tom","age":"14","friends":[{"name":"lili"},{"name":"edf"}]} 
  VersionId: v1
  ```
- 在OOS加密参数中添加如下参数，详细流程请参考[管理加密参数](https://www.alibabacloud.com/help/zh/oos/developer-reference/api-oos-2019-06-01-createsecretparameter)

  ```txt
  Name: test2
  Value: {"name":"tom","age":"14","friends":[{"name":"lili"},{"name":"edf"}]} 
  ```

### 2. 创建SecretStore & ExternalSecret

前提：给集群开启 RRSA，并且正确配置相关 RAM Role 权限

本示例包含了对模板中一些配置字段的说明, 以下的示例中不再重复说明

- 创建文件 `hello-service-secret-store.yml`如下并保存为测试模板, 其可以创建名称为 `scdemo` 的 SecretStore 的测试实例, 需对部分字段进行替换
  ```yaml
  apiVersion: 'alibabacloud.com/v1alpha1'
  kind: SecretStore
  metadata:
    name: scdemo
  spec:
    KMS: # 同步kms凭据时，指定为KMS, 同步oos加密参数时，指定为OOS
      KMSAuth: # 同步kms凭据时，指定为KMSAuth, 同步oos加密参数时，指定为OOSAuth
        oidcProviderARN: "acs:ram::{accountID}:oidc-provider/ack-rrsa-{clusterID}"
        ramRoleARN: "acs:ram::{accountID}:role/{roleName}"
  ```
- 创建文件 `hello-service-external-secret.yml`如下并保存为测试模板, 其可以创建名称为 `esdemo` 的ExternalSecret的测试实例：
  ```yaml
  apiVersion: 'alibabacloud.com/v1alpha1'
  kind: ExternalSecret
  metadata:
    name: esdemo
  spec:
    provider: kms # 当前支持kms和oos, 默认为kms, 同步kms凭据时可不指定该字段
    data: #无需特殊处理的数据源
      - key: test1 # 需要同步的秘钥名称, 当前支持kms凭据和oos加密参数, 本示例为kms凭据名称
        name: test1 # 存入secret字段
        versionId: v1 #kms凭据版本, 当provider为非kms 时, 则不需要指定该字段 
        kmsEndpoint: kms.cn-hangzhou.aliyuncs.com #KMS服务地址, 当provider为非kms 时, 则不需要指定该字段, 详见高级用法指南
        secretStoreRef: # 使用WorkerRole方式认证时, 则不需要指定该属性
          name: scdemo
          namespace: default
  ```
- 执行命令创建secretstore测试实例(使用WorkerRole方式认证时, 则不需要创建该实例)：
  ```sh
  kubectl apply -f hello-service-secret-store.yml
  ```
- 执行命令创建externalsecret测试实例：
  ```sh
  kubectl apply -f hello-service-external-secret.yml
  ```
- 查看目标secret是否创建成功：
  ```sh
  kubectl get secret esdemo -oyaml
  ```
- 如果创建成功，查看secret内容如下：
  ```yaml
  apiVersion: v1
  data:
    test1: eyJuYW1lIjoidG9tIiwiYWdlIjoiMTQiLCJmcmllbmRzIjpbeyJuYW1lIjoibGlsaSJ9LHsibmFtZSI6ImVkZiJ9XX0=
  kind: Secret
  metadata:
    name: esdemo
    namespace: default
  type: Opaque
  ```
- 在没有关闭自动同步配置的前提下，可以修改KMS凭据管家中的密钥内容，等待片刻后查看目标secret是否已经完成同步

### 3. 认证配置

ack-secret-manager 支持多种认证方式，包括 ServiceAccount RRSA、环境变量 RRSA/OIDC、AK+AssumeRole、纯 AccessKey、ECS WorkerRole 等。

- 认证方式详细说明（含认证优先级、配置示例、跨账号认证）：[认证配置指南](../../docs/auth_guide_zh.md)

### 4. 高级功能

ack-secret-manager 提供了多种高级功能，详细使用说明请参考对应文档：

| 功能 | 说明 | 文档 |
|------|------|------|
| JSON/YAML 凭据解析 | 使用 `jmesPath` 解析提取 JSON/YAML 凭据字段，使用 `dataProcess` 自解析凭据 | [高级用法指南](../../docs/advanced_usage_zh.md#jsonyaml-凭据解析) |
| 跨账号同步 | 通过 `remoteRamRoleARN` 实现跨阿里云账号凭据同步 | [高级用法指南](../../docs/advanced_usage_zh.md#跨账号同步) |
| kmsEndpoint 配置 | 配置专属网关、VPC endpoint、公网 endpoint 等 | [高级用法指南](../../docs/advanced_usage_zh.md#kmsendpoint-配置) |
| 凭据轮转 | 可配置同步间隔，持续获取最新凭据版本 | [高级用法指南](../../docs/advanced_usage_zh.md#凭据轮转) |
| 多数据源支持 | 同时支持 KMS 凭据管家和 OOS 加密参数两种数据源 | [高级用法指南](../../docs/advanced_usage_zh.md#多数据源支持) |
| 同步失败处理语义 | 同步失败对集群 Secret 的影响：部分失败合并写入、模板 ExternalSecret fail-closed、零产出保护 | [高级用法指南](../../docs/advanced_usage_zh.md#同步失败处理语义) |

### 5. 模板解析

ack-secret-manager 支持高级模板解析功能，包括 Go 模板语法、70+ Sprig 函数、`templateFrom` 外部模板引用、`mergePolicy` 合并策略、元数据模板等。详细说明请参考：[模板解析指南](../../docs/template_processing_guide_zh.md)

### 6. CRD 资源详解

ack-secret-manager 涉及四种 CRD 资源，各 CRD 概述、跨命名空间控制机制、安全最佳实践、配置示例、字段说明请参考：[CRD 资源详解](../../docs/crd_resources_guide_zh.md)

| CRD | 作用范围 | 说明 |
|-----|---------|------|
| **SecretStore** | 命名空间级 | 存储认证信息（RRSA、AK、ServiceAccountRef 等） |
| **ClusterSecretStore** | 集群级 | 集群级认证资源，支持访问控制，可被任意命名空间引用 |
| **ExternalSecret** | 命名空间级 | 定义需要同步的凭据信息（凭据名称、版本、数据源等） |
| **ClusterExternalSecret** | 集群级 | 在匹配的命名空间中自动创建 ExternalSecret |

## 安全

对于发现的安全漏洞，请发送邮件至**kubernetes-security@service.aliyun.com**，您可在[SECURITY.md](https://github.com/AliyunContainerService/ack-secret-manager/blob/master/SECURITY.md)文件中找到更多信息。

## Release Note

| 版本号    | 变更时间       | 变更内容                                                                                                                                                |
| -------- | ------------ |-----------------------------------------------------------------------------------------------------------------------------------------------------|
| `0.4.0`  | 2022年12月22日 | 支持基于JMES解析提取JSON格式的密文字段                                                                                                                             |
| `0.5.0`  | 2023年10月10日 | 1.支持专属版 KMS 凭据同步<br />2.多阿里云访问凭据管理<br />3.凭据自解析与键规则替换<br />4.支持 KMS 跨账号凭据同步                                                                         |
| `0.5.1`  | 2023年10月18日 | 部分功能与性能优化                                                                                                                                           |
| `0.5.2`  | 2024年8月1日   | 大规模资源同步并发优化                                                                                                                                         |
| `0.5.3`  | 2024年10月10日 | 支持资源同步秒级限流，修复部分软件包cve                                                                                                                               |
| `0.5.4`  | 2024年10月31日 | 支持同步 OOS 加密参数                                                                                                                                       |
| `0.5.5`  | 2024年11月22日 | 支持同步 binary 类型的凭据                                                                                                                                   |
| `0.5.6`  | 2025年3月3日   | 支持Acs集群                                                                                                                                             |
| `0.5.7`  | 2025年3月26日  | 支持解析提取YAML格式的密文字段                                                                                                                                   |
| `0.5.8`  | 2025年3月28日  | 1.支持多架构部署<br />2.支持配置 Endpoint 获取 KMS 凭据                                                                                                            |
| `0.5.11` | 2025年6月17日  | 支持配置是否在KMS凭据或OOS加密参数同步失败时删除集群secret                                                                                                                 |
| `0.5.12` | 2025年6月25日  | 支持使用ExternalSecret配置KMS实例维度的endpoint地址对接多KMS实例                                                                                                      |
| `0.6.0`  | 2025年12月5日  | 1.支持ClusterSecretStore资源<br />2.支持ClusterExternalSecret资源<br />3.支持禁止ExternalSecret跨命名空间引用SecretStore资源<br /> 4.支持SecretStore引用ServiceAccount配置认证信息 |
| `0.6.1`  | 2026年2月28日 | 1.修改ClusterExternalSecret资源命名空间匹配添加<br />2.增加secret和serviceaccount控制器以Reconcile ClusterSecretStore和SecretStore资源<br />3.优化serviceaccount认证时token刷新时间 |
| `0.6.2`  | 2026年3月2日  | 支持同步外部secret时，使用高级模板解析功能在创建 Kubernetes Secret 之前转换和自定义密钥数据 |
| `0.6.3`  | 2026年6月30日  | 功能修复与文档优化 |
| `0.6.4`  | 2026年7月30日  | **不兼容变更**（安全加固）：<br />1.跨命名空间引用默认禁止（`enableCrossNamespaceSecretStore` 和 `enableCrossNamespaceAuthRef` 默认值从 `true` 改为 `false`），如需跨命名空间引用请显式设置为 `true`<br />2.新增 KMS Endpoint SSRF 验证 |
| `0.6.5`  | 2026年8月10日  | 1.同步失败处理：当部分数据源凭据获取失败或凭据解析失败时，Secret 采用合并写入——成功拉取的 key 更新为新值，失败的 key 保留 Secret 中的旧值；配置了模板的 ExternalSecret 采用 fail-closed（跳过写入，保留旧值）。 <br />2. 其它功能优化|
| `0.6.6`  | 2026年8月14日  | 部分功能优化 |

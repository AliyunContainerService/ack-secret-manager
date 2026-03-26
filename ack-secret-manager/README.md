# ACK Secret Manager

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager) 可以帮助您将存储在[阿里云KMS凭据管家](https://www.alibabacloud.com/help/zh/doc-detail/152001.html) 中的密钥凭据或[阿里云OOS加密参数](https://www.alibabacloud.com/help/zh/oos/getting-started/manage-encryption-parameters), 以K8s原生Secret对象的形式导入到集群中并实现密钥数据的自动同步，您可以在应用Pod中以挂载Secret等形式将存储在凭据管家或加密参数中的密文引入到应用程序中使用，避免敏感数据在应用开发构建流程中的传播和泄露。

## 安装

1. 请确保组件使用的凭据有足够的权限访问需要同步的阿里云服务，可以使用如下两种配置方式，推荐使用RRSA方式，实现Pod维度的授权。
    - 通过 [RRSA方式](https://help.aliyun.com/document_detail/356611.html) 实现Pod维度的授权（推荐）
        * [启用RRSA功能](https://help.aliyun.com/document_detail/356611.html#section-ywl-59g-j8h)
        * [使用RRSA功能](https://help.aliyun.com/document_detail/356611.html#section-rmr-eeh-878) ：为指定的 serviceaccount 创建对应的 RAM 角色，为 RAM 角色设置信任策略，并为 RAM 角色授权
        * 通过配置在SecretStore实例中的`ramRoleARN`和`oidcProviderARN`，实现以无AK的方式将获取KMS凭据权限绑定在ack-secret-manager组件使用的指定ServiceAccount上。
        * 在多租场景下，通过在租户命名空间的SecretStore中使用`serviceAccountRef`认证方式，可以将获取KMS凭据权限细粒度控制在ServiceAccount维度。  关于SecretStore的配置方式请参考[使用serviceAccountRef实现认证](#serviceAccountRef-auth)。

    - 在集群对应的 WorkerRole 中添加权限

        - 登录容器服务控制台

        - 选择对应集群进入到集群详情页

        - 在集群信息中选择**集群资源**页，点击Worker RAM角色中对应的命名为**KubernetesWorkerRole-xxxxxxxxxxxxxxx** 的角色名称，会自动导航到RAM角色对应的控制台页面

        - 点击添加权限按钮，创建自定义权限策略，策略内容如下（仅授权同步服务需要的RAM策略即可，保证最小权限原则）：

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

   ack-secret-manager支持的所有认证信息配置方式请参考[官方文档](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/security-and-compliance/use-ack-secret-manager-to-import-alibaba-cloud-kms-service-credentials)。

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

| **参数**                                            | **说明**                                                                                                                                  | **默认值**             |
| --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- |
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
| command.cleanupSecretOnFailure                      | kms 凭据同步失败时是否删除集群 Secret                                                                                                       | false                  |
| command.processClusterSecretStore                   | 是否处理 ClusterSecretStore 资源                                                                                                           | true                   |
| command.processClusterExternalSecret                | 是否处理 ClusterExternalSecret 资源                                                                                                        | true                   |
| command.enableCrossNamespaceSecretStore             | 是否启用跨命名空间的 SecretStore 引用                                                                                                      | true                   |
| command.enableCrossNamespaceAuthRef                 | 是否允许 SecretStore 跨命名空间引用认证资源（ServiceAccount、AccessKey Secret）                                                            | true                   |
| command.aliuiid                                     | 用于拼接OIDC身份提供商ARN的阿里云账号                                                                                                       |                        |
| command.clusterId                                   | 用于拼接OIDC身份提供商ARN的阿里云容器服务集群ID                                                                                              |                        |
| image.repository                                    | 指定的ack-secret-manager 镜像仓库名称                                                                                                      | acs/ack-secret-manager |
| image.tag                                           | 指定的ack-secret-manager 镜像tag                                                                                                          | v0.5.0                 |
| image.pullPolicy                                    | 镜像拉取策略，默认为Always                                                                                                                 | Always                 |
| nameOverride                                        | 覆盖应用名称                                                                                                                              | nil                    |
| fullnameOverride                                    | 覆盖应用全名                                                                                                                              | nil                    |
| rbac.create                                         | 是否创建并使用RBAC资源，默认为true                                                                                                         | true                   |
| securityContext.fsGroup                             | 指定应用的security context配置                                                                                                            | {}                     |
| serviceAccount.create                               | 是否创建serviceaccount                                                                                                                    | true                   |
| serviceAccount.name                                 | 指定创建serviceaccount的名称                                                                                                              | 自动生成                |
| serviceAccount.annotations                          | 指定添加serviceaccount annotation标签                                                                                                     | nil                    |
| podAnnotations                                      | 指定添加到pod中的annotation标签                                                                                                           | {}                     |
| podLabels                                           | 指定添加到pod中的Label标签                                                                                                                | {}                     |
| replicaCount                                        | 控制器副本个数                                                                                                                            | 1                      |
| nodeSelector                                        | 指定的nodeSelector标签                                                                                                                    | {}                     |
| tolerations                                         | 指定的污点容忍配置                                                                                                                        | []                      |
| affinity                                            | 指定的Pod亲和性配置                                                                                                                       | {}                      |
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

ack-secret-manager 涉及了四种 CRD，分别为 SecretStore、ClusterSecretStore 用于存放访问凭据（例如 RRSA ，ClientKey，AK 配置等），ExternalSecret、ClusterExternalSecret 用于存放需要同步的凭据基础信息（如凭据名称，版本等）以及指定 SecretStore，保证了权限与数据分离，增强使用灵活性。具体介绍见下方 **CRD 配置介绍**

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
        kmsEndpoint: kms.cn-hangzhou.aliyuncs.com #KMS服务地址, 当provider为非kms 时, 则不需要指定该字段, 字段配置方式参数使用说明的第6部分
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

### 3. JSON/YAML 凭据解析

**data**

- 如果您希望解析一个 JSON/YAML 格式的 secret 并将其中指定的 key-value 对同步到 k8s secret 中，可以使用 `jmesPath`字段。以下是一个使用 `jmesPath` 字段的样例，我们将其部署在集群中
  ```yaml
  apiVersion: 'alibabacloud.com/v1alpha1'
  kind: ExternalSecret
  metadata:
    name: es-json-demo
  spec:
    provider: kms
    data:
      - key: test1
        name: test1
        versionId: v1
        kmsEndpoint: kms.cn-hangzhou.aliyuncs.com #KMS服务地址, 当provider为非kms 时, 则不需要指定该字段, 字段配置方式参数使用说明的第6部分
        secretStoreRef:
          name: scdemo
          namespace: default
        jmesPath: # 解析 json/yaml 凭据中的部分字段
          - path: "name"
            objectAlias: "name"
          - path: "friends[0].name"
            objectAlias: "friendname"
  ```
- 当您使用 `jmesPath`字段时，必需指定下面两个子字段：
    - `path`: 必需项，基于 [JMES path](https://jmespath.org/specification.html) 规范解析 json/yaml 中的指定字段
    - `objectAlias`: 必需项，用于指定解析出的字段同步到 k8s secret 中的 key 名称
- 部署后检查 secret 是否创建成功
  ```sh
  kubectl get secret es-json-demo -oyaml
  ```
- 同步成功即可看到如下结果
  ```yaml
  apiVersion: v1
  data:
    friendname: bGlsaQ==
    name: dG9t
  kind: Secret
  metadata:
    name: es-json-demo
    namespace: default
  type: Opaque
  ```

**dataProcess**

- 如果您想将 JSON/YAML 凭据解析后再存放入 secret 中，但又不知道凭据的具体结构，可以采用自解析功能，即 dataProcess.Extract 字段。并且可以针对解析后的字段键进行规则替换，即 dataProcss.replaceRule 字段，防止不规则的 secret data key 导致无法创建 secret，以下为样例 ExternalSecret
  ```yaml
  apiVersion: 'alibabacloud.com/v1alpha1'
  kind: ExternalSecret
  metadata:
    name: extract-secret
  spec:
    provider: kms
    dataProcess:
      - extract:
          key: test1
          name: extract
          versionId: v1
          kmsEndpoint: kms.cn-hangzhou.aliyuncs.com #KMS服务地址, 当provider为非kms 时, 则不需要指定该字段, 字段配置方式参数使用说明的第6部分
          secretStoreRef:
            name: scdemo
            namespace: default
        replaceRule:
          - source: "^n.*e$" #替换 以n开头以e结尾 的 key 为 alibabacloud
            target: "alibabacloud"
          - source: "^f.*s$"
            target: "ack"
  ```
- 同步成功即可看到如下结果，JSON/YAML 凭据被解析为三部分，且各自的键根据 replaceRule 规则进行了替换
  ```yaml
  apiVersion: v1
  data:
    ack: W3sibmFtZSI6ImxpbGkifSx7Im5hbWUiOiJlZGYifV0=
    age: IjE0Ig==
    alibabacloud: InRvbSI=
  kind: Secret
  metadata:
    name: extract-secret
    namespace: default
  type: Opaque
  ```

### 4. 当前支持跨账号同步凭据，在 `SecretStore.Spec.KMS.KMSAuth` 中配置 `remoteRamRoleArn`，`remoteRamRoleSessionName` 即可，以下为样例 SecretStore

   ```yaml
   apiVersion: 'alibabacloud.com/v1alpha1'
   kind: SecretStore
   metadata:
     name: scdemo
   spec:
     KMS:
       KMSAuth:
         oidcProviderARN: "acs:ram::{accountID}:oidc-provider/ack-rrsa-{clusterID}"  #替换指定RAM OIDC provdider实例的账号ID和集群ID字段
         ramRoleARN: "acs:ram::{accountID}:role/{roleName}"    #替换RAM角色的ARN
         remoteRamRoleArn: "acs:ram::{accountID}:role/{roleName}"   #替换为指定跨账号RAM角色的ARN
         remoteRamRoleSessionName: ""
   ```

### 5. command.enableWorkerRole 配置介绍

command.enableWorkerRole 配置与集群类型有关，以下是对应关系：

| 集群类型      | command.enableWorkerRole |
| ------------ | ------------------------ |
| ACK 托管集群  | true                     |
| ACK 专有集群  | true                     |
| ACK Edge 集群 | true                     |
| 其它集群      | false                    |

### 6. kmsEndpoint 配置说明

访问 KMS 服务获取凭据当前支持专属实例网关和共享网关两种方式，需要参考以下说明进行endpoint配置(关于专属网关访问和共享网关访问的更多差异，请参见[共享网关和专属网关的差异](https://www.alibabacloud.com/help/zh/kms/key-management-service/developer-reference/classic-kms-sdkclassic-kms-sdk/#d61514b089my8))：

**KMS Endpoint 优先级说明**

| 类型     | 配置字段                              | 用途                                     | 优先级 | 说明                                                                                        |
| -------- | ------------------------------------ | ---------------------------------------- | ----- |-------------------------------------------------------------------------------------------- |
|凭据级配置 | ExternalSecret.spec.data.kmsEndpoint | 为需要导入的每个KMS凭据单独指定Endpoint地址 | 最高  | 针对单个凭据优先使用该配置，会覆盖全局配置和默认配置                                             |
|全局配置   | command.kmsEndpoint（启动参数）       | 用于所有KMS请求                           | 中    | 提供了凭据级配置以外的其他KMS凭据使用的Endpoint地址                                             |
|默认配置   | 无                                   | 当未明确配置Endpoint地址时使用             | 最低  | 默认使用的KMS Endpoint地址 kms-vpc.{region}.aliyuncs.com, 替换 {region} 为 KMS凭据所在的Region |

```
apiVersion: "alibabacloud.com/v1alpha1"
kind: ExternalSecret
metadata:
  name: esdemo
spec:
  provider: kms
  data:
    - key: test-hangzhou # 该凭据同步时实际使用的 Endpoint 地址：全局配置存在时使用全局配置，否则为默认配置地址：kms-vpc.{region}.aliyuncs.com
      name: hangzhou-vpc
      versionId: v1
    - key: test-hangzhou # 该凭据同步时实际使用的 Endpoint 地址：字段 kmsEndpoint 指定的 kms.cn-hangzhou.aliyuncs.com
      name: hangzhou-public
      versionId: v1
      kmsEndpoint: kms.cn-hangzhou.aliyuncs.com
```

**KMS Endpoint 地址说明**

| 网关类型     | 域名类型    | Endpoint 地址                                     | 使用说明                                                                                                                                |
| ----------- | ----------- | ------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------- |
| 专属网关     | KMS私网域名 | {kms-instance-id}.cryptoservice.kms.aliyuncs.com | 1. 要求 KMS 凭据所属实例和集群 Region 相同且VPC 相同<br />2. 替换 {kms-instance-id} 为KMS凭据所属实例ID<br />3. KMS 凭据所属实例版本 3.0 以上 |
| 共享网关     | VPC域名     | kms-vpc.{region}.aliyuncs.com                    | 1. 要求 KMS 凭据和集群 Region 相同<br />2. 替换 {region} 为 KMS 凭据所在的region<br />3. 应用默认配置，使用此地址时无需配置                  |
| 共享网关     | 公网        | kms.{region}.aliyuncs.com                        | 1. 替换 {region} 为 KMS 凭据所在的 region<br />2. 集群具有公网访问能力                                                                    |

### 7. 使用serviceAccountRef实现认证**{#serviceAccountRef-auth}**

在多租场景中，不同租户的用户希望使用各自命名空间的ServiceAccount绑定不同的RAM角色，并授权角色只能访问自己权限范围内KMS凭据管家中的指定凭据。

ack-secret-manager支持在租户命名空间的SecretStore中使用`serviceAccountRef`认证方式，将获取KMS凭据权限细粒度控制在ServiceAccount维度。

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  annotations:
    ack.alibabacloud.com/role-arn: acs:ram::<account-id>:role/<role-name>   #注意配置该annotation并填写该ServiceAccount绑定的RAM角色ARN
  name: test-serviceaccount-auth
  namespace: test

---
apiVersion: 'alibabacloud.com/v1alpha1'
kind: SecretStore
metadata:
  name: alibaba-credentials
  namespace: test
spec:
  KMS:
    KMSAuth:
      serviceAccountRef:
        name: test-serviceaccount-auth

---
apiVersion: "alibabacloud.com/v1alpha1"
kind: ExternalSecret
metadata:
  name: kms
  namespace: test
spec:
  provider: kms
  data:
    - key: test-json1
      name: test-json1
      versionId: v2
      secretStoreRef:
        name: alibaba-credentials
        kind: SecretStore
```

### 8. 模板解析功能

ack-secret-manager 支持高级模板解析功能，允许您在同步密钥凭据到 Kubernetes Secret 之前转换和自定义密钥数据。

#### 模板字段概览

`ExternalSecret.spec.target.template` 字段支持以下配置：

**内联模板 (`template.data`)**
直接在 ExternalSecret 规范中定义模板转换：

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: template-example
spec:
  provider: kms
  data:
    - key: my-secret
      name: raw-data
  target:
    template:
      data:
        # 使用 Go 模板和 Sprig 函数转换数据
        formatted-config: |
          {
            "database": {
              "host": "{{ .raw-data | quote }}",
              "port": {{ "5432" | int }}
            }
          }
        # 条件处理
        is-active: |
          {{ if eq .status "active" }}true{{ else }}false{{ end }}
```

**模板引用 (`templateFrom`)**
引用现有的 ConfigMap 或 Secret 中的模板：

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: template-from-example
spec:
  provider: kms
  data:
    - key: db-password
      name: password
    - key: db-host
      name: host
  target:
    template:
      templateFrom:
        # 引用 ConfigMap 模板
        - configMap:
            name: app-config-templates
            items:
              - key: database-config
                templateAs: Values  # 仅作为值处理
          target: Data
        
        # 引用 Secret 模板
        - secret:
            name: secure-templates
            items:
              - key: connection-string
                templateAs: KeysAndValues  # 作为键值对处理
          target: Data
        
        # 内联字面量模板
        - literal: |
            APP_ENV={{ .environment | default "production" }}
          target: Data
```

**模板目标**
控制模板结果的放置位置：

- `Data` (默认): 将结果放在 Secret data 中
- `Annotations`: 将结果放在 Secret 注解中
- `Labels`: 将结果放在 Secret 标签中

**模板作用域**
定义模板键的解释方式：

- `Values`: 仅将模板作为值处理
- `KeysAndValues`: 将模板作为键值对处理（换行分隔）

**支持的模板函数**

模板处理器包含 [Sprig 函数](http://masterminds.github.io/sprig/) 用于高级处理：

```yaml
# 字符串操作
uppercase: {{ .username | upper }}
lowercase: {{ .service | lower }}

# 默认值
port: {{ .port | default "8080" }}

# 类型转换
timeout: {{ "30" | int }}

# JSON 操作
config-json: {{ .config | fromJson | toJson }}

# 条件逻辑
is-prod: {{ if eq .env "prod" }}true{{ else }}false{{ end }}

# 字符串格式化
connection-url: postgresql://{{ .user }}:{{ .password }}@{{ .host }}:{{ .port }}/{{ .db }}

# Bcrypt 密码哈希
hashed-password: {{ bcrypt .password }}

# Htpasswd 格式（用于基本认证）
htpasswd-auth: {{ htpasswd "admin" .password }}

# JSON 路径查询（推荐用于嵌套访问）
db-host: {{ jsonPath .config "database.host" }}

# JSON 合并
merged: {{ mergeJson .default .override }}

# 正则替换
cleaned: {{ regexReplace .text "\\d+" "" }}

# Base64 安全解码
decoded: {{ b64decSafe .encoded }}

# Key=Value 解析
{{ $kv := parseKeyValue .env }}
DB_URL: {{ index $kv "DATABASE_URL" }}
```

#### 模板上下文访问

在模板中使用以下方式访问密钥数据：
- .KEY: 直接访问密钥数据（标准 Go 模板语法）
- index . "KEY": 使用 index 函数访问
- jsonPath .jsonKey "path.to.field": 从嵌套 JSON 中提取（推荐）

示例：
```yaml
target:
  template:
    data:
      # 标准字段访问
      username: {{ .username }}
      password: {{ .password }}
      
      # 嵌套 JSON 访问（推荐方式）
      db-host: {{ jsonPath .dbConfig "host" }}
      db-port: {{ jsonPath .dbConfig "port" }}
      
      # 或使用 fromJson + index
      db-user: {{ index (.dbConfig | fromJson) "user" }}
      
      # 条件处理
      {{ if eq .environment "production" }}
      log-level: error
      {{ else }}
      log-level: debug
      {{ end }}
      
      # 循环遍历
      {{ range .ports | fromJson }}
      - port: {{ . }}
      {{ end }}
      
      # 使用 bcrypt 加密密码
      hashed-pwd: {{ bcrypt .password }}
      
      # 生成 htpasswd 认证文件
      auth: |
        {{- $creds := list }}
        {{- range $user, $pw := . }}
        {{- $creds = append $creds (htpasswd $user $pw) }}
        {{- end }}
        {{ $creds | join "\n" }}
```

#### 模板验证

处理器验证输出格式：

- Secret 数据键: 必须遵循 Kubernetes 命名约定
- 注解键: 必须遵循 DNS 子域名格式
- 标签键: 必须遵循 DNS 标签格式

无效键会被自动过滤以防止 Secret 创建失败。

### 9. 新增 **ClusterExternalSecret** 和 **ClusterSecretStore** 资源

ack-secret-manager 新增了 ClusterExternalSecret 和 ClusterSecretStore 资源，以下是资源说明及示例

**ClusterExternalSecret**

集群级别资源，用于管理和协调多个命名空间下的ExternalSecret，支持使用 `spec.conditions` 配置匹配的命名空间，在匹配的命名空间中自动创建ExternalSecret

```yaml
apiVersion: "alibabacloud.com/v1alpha1"
kind: ClusterExternalSecret
metadata:
  name: cluster-kms
spec:
  externalSecretSpec:
    provider: kms
    data:
      - key: test
        name: test
        versionId: v1
        secretStoreRef:
          name: alibaba-credentials
          kind: ClusterSecretStore
  externalSecretName: kms
  externalSecretMetadata:
    labels:
      app: "my-app"
      team: "backend"
    annotations:
      annotation-key1: "annotation-value1"
      annotation-key2: "annotation-value2"
  conditions:
  - namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: default
      matchExpressions: 
      - key: environment
        operator: In
        values:
        - dev
  - namespaces:
    - test
  - namespaceRegexes:
    - "dev-.*"
  rotationInterval: 10s
```

**ClusterSecretStore**

集群级别资源，功能与SecretStore相同，但可被集群中任意命名空间的ExternalSecret引用，并支持使用 `sepc.conditions` 配置访问控制

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ClusterSecretStore
metadata:
  name: alibaba-credentials
spec:
  conditions: 
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: test
        matchExpressions: 
        - key: environment
          operator: In
          values:
          - dev
    - namespaces:
      - default
    - namespaceRegexes:
      - "kube-.*"
  KMS:
    KMSAuth:
      oidcProviderARN: acs:ram::<role-name>:oidc-provider/ack-rrsa-<cluster-id>
      serviceAccountRef:
        name: test-serviceaccount-auth
        namespace: test
```

更详细的 CRD 资源说明请参考以下 CRD 资源详解 和 CRD 配置介绍

## CRD 资源详解

### CRD 资源概述

当前提供了四种自定义资源定义(CRD)，分为两类：

**认证资源配置类**

1. **SecretStore**: 命名空间级别资源，用于定义访问凭据（如RRSA、ClientKey、AK配置等）
2. **ClusterSecretStore**: 集群级别资源，功能与SecretStore相同，但可被集群中任意命名空间的ExternalSecret引用，并支持访问控制配置

**数据同步配置类**

1. **ExternalSecret**: 命名空间级别资源，用于定义需要同步的凭据基础信息（如凭据名称、版本等）以及指定SecretStore
2. **ClusterExternalSecret**: 集群级别资源，用于管理和协调多个命名空间下的ExternalSecret，能够在匹配的命名空间中自动创建ExternalSecret

### 跨命名空间控制机制

为了增强安全性和灵活性，ack-secret-manager提供了多种跨命名空间控制机制：

**ExternalSecret 引用 SecretStore 控制**

* 通过 `command.enableCrossNamespaceSecretStore`参数控制ExternalSecret是否可以跨命名空间引用SecretStore
* 默认值为true，即允许跨命名空间引用
* 设置为false时，ExternalSecret只能引用同命名空间的SecretStore

**SecretStore 引用认证资源控制**

* 通过 `command.enableCrossNamespaceAuthRef`参数控制SecretStore是否可以跨命名空间引用认证资源（ServiceAccount、AccessKey Secret）
* 默认值为true，即允许跨命名空间引用
* 设置为false时，SecretStore只能引用同命名空间的认证资源

**ClusterExternalSecret 匹配命名空间控制**

* ClusterExternalSecret 通过 `spec.conditions` 字段定义命名空间的选择条件，以在符合条件的命名空间中创建 ExternalSecret 资源
* 支持三种匹配方式，条件之间是或的关系：

  1. namespaceSelector: 使用标签选择器匹配允许访问的命名空间
  2. namespaces: 明确列出允许访问的命名空间名称列表
  3. namespaceRegexes: 使用正则表达式匹配允许访问的命名空间名称列表

**ClusterSecretStore 访问控制**

* ClusterSecretStore 通过 `spec.conditions` 字段定义允许访问该资源的命名空间条件
* 支持三种访问控制方式，条件之间是或的关系：

    1. namespaceSelector: 使用标签选择器匹配允许访问的命名空间
    2. namespaces: 明确列出允许访问的命名空间名称列表
    3. namespaceRegexes: 使用正则表达式匹配允许访问的命名空间名称列表

### 推荐使用方式

**跨命名空间访问推荐方案**

对于需要跨命名空间访问的场景，推荐使用以下组合：

1. ClusterSecretStore + ExternalSecret: 当多个命名空间需要使用相同的认证配置时
2. ClusterSecretStore + ClusterExternalSecret: 当需要在多个命名空间中自动创建相同配置的ExternalSecret时

**安全最佳实践**

1. 最小权限原则:

    - 在不需要跨命名空间访问的场景中，将 `command.enableCrossNamespaceSecretStore` 和 `command.enableCrossNamespaceAuthRef` 设置为 false
    - 优先使用命名空间级别的资源（SecretStore 和 ExternalSecret）
2. 访问控制配置:

    - 使用 ClusterSecretStore 时，明确配置 `spec.conditions` 来限制可访问的命名空间
    - 避免创建无访问限制的 ClusterSecretStore
3. 认证方式选择:

    - 优先使用 RRSA  或 ServiceAccount 方式进行认证，避免在配置中直接暴露 AccessKey
    - 将认证配置与数据配置分离，提高安全性
4. 非必要不使用 ClusterExternalSecret，以减少Secrets在不同命名空间中的泄露风险:

    - 如果业务需要在多个命名空间中同步Secrets实例，可以利用 `spec.conditions` 精确控制 ExternalSecret 的创建范围

## CRD 配置介绍

### ExternalSecret

**spec**

| crd 字段         | 描述                                    | 是否必选 |
| ---------------- | -------------------------------------- | -------- |
| provider         | 获取 secret 的目标云服务（如 KMS 、OOS等 | 否       |
| data             | 数据源（目标数据的标识）                 | 否       |
| dataProcess      | 需要进行特殊加工的数据源（目标数据的标识） | 否       |
| type             | k8s secret 类型（Opaque等）             | 否       |
| target           | 定义将要创建的 Kubernetes Secret 的配置  | 否       |
| rotationInterval | 控制器重新协调对象的时间间隔             | 否       |

**target**

| crd 字段         | 描述                               | 是否必选 | 默认值 |
| ---------------- | ---------------------------------- | -------- | ------ |
| name             | 目标 Kubernetes Secret 的名称       | 否       |        |
| template         | 用于转换密钥数据的模板配置           | 否       |        |

**template**

| crd 字段     | 描述                                | 是否必选 | 默认值 |
| ------------ | ----------------------------------- | -------- | ------ |
| data         | 内联模板定义                         | 否       |        |
| templateFrom | 外部模板引用（ConfigMap/Secret）     | 否       |        |
| metadata     | Secret 元数据模板（标签/注解）       | 否       |        |
| type         | 目标 secret 的类型                   | 否       |        |
| mergePolicy  | 模板结果与原始数据的合并策略          | 否       | Replace|

**metadata**

| crd 字段    | 描述                    | 是否必选 |
| ----------- | ----------------------- | -------- |
| annotations | 应用于 secret 的注解     | 否       |
| labels      | 应用于 secret 的标签     | 否       |

**templateFrom**

| crd 字段   | 描述                                           | 是否必选 | 默认值 |
| ---------- | ---------------------------------------------- | -------- | ------ |
| configMap  | 包含模板的 ConfigMap 引用                       | 否       |        |
| secret     | 包含模板的 Secret 引用                          | 否       |        |
| literal    | 内联字面量模板字符串                            | 否       |        |
| target     | 模板结果的目标位置（Data/Annotations/Labels）   | 否       | Data   |

**configMap/secret**

| crd 字段 | 描述                                 | 是否必选 |
| -------- | ------------------------------------ | -------- |
| name     | ConfigMap/Secret 资源的名称          | 是       |
| items    | ConfigMap/Secret 中用作模板的键列表   | 是       |

**items**

| crd 字段    | 描述                        | 是否必选 | 默认值 |
| ----------- | --------------------------- | -------- | ------ |
| key         | ConfigMap/Secret 中的键      | 是       |        |
| templateAs  | 模板键的解释方式             | 否       | Values |

**templateAs**

| 值              | 描述                        |
| --------------- | --------------------------- |
| Values          | 仅将模板作为值处理           |
| KeysAndValues   | 将模板作为键值对处理         |

**templateFrom.target**

| 值              | 描述                             |
| --------------- | -------------------------------- |
| Data            | 将模板结果存入 Secret data        | 
| Annotations     | 将模板结果存入 Secret annotations |
| Labels          | 将模板结果存入 Secret labels      |

**mergePolicy**

| 值         | 描述                                                                                       |
| ---------- | ------------------------------------------------------------------------------------------ |
| Replace    | 完全使用模板字段名（清空现有secret中的字段名）                                                 |
| Merge      | 将模板字段名与现有secret中的字段名合并, 模板中包含的字段名和现有secret中的字段名相同，以模板值为准 |

**data（无需经过特殊处理的数据源）**

| crd 字段       | 描述                                                                         | 是否必选 |
| -------------- | --------------------------------------------------------------------------- | -------- |
| key            | 目标 secret 的唯一标识（例如 KMS 凭据的 key）                                  | 是       |
| name           | 在集群 secret data 中对应的 key                                               | 否       |
| versionStage   | 目标 secret 版本状态                                                          | 否       |
| versionId      | 目标 secret 版本号, 当 provider 是 oos 时, 则不需要指定该字段                   | 否       |
| jmesPath       | 如果目标 secret 为 json/yaml 类型，可指定获取 json/yaml 中特定 key 对应的 value | 否       |
| secretStoreRef | 引用的 SecretStore 信息                                                       | 否       |
| kmsEndpoint    | KMS 服务 endpoint 地址                                                        | 否       |

**dataProcess（需要进行特殊处理的数据源）**

| crd 字段    | 描述                                                                               | 是否必选 |
| ----------- | --------------------------------------------------------------------------------- | -------- |
| extract     | 针对目标 secret 进行 json/yaml 解析，不需要用户指定 json/yaml key                    | 否       |
| replaceRule | 根据特定规则替换经过 json/yaml 解析的 secret 的 key，防止非法 key 不能存入 k8s secret | 否       |

**replaceRule（用于进行 Secret Key 内容替换）**

| crd 字段 | 描述                               | 是否必选 |
| -------- | ----------------------------------| -------- |
| target   | 用于替换的字符串                   | 是       |
| source   | 需要被替换的字符串，可以为正则表达式 | 是       |

**jmesPath**

| crd 字段    | 描述                                | 是否必选 |
| ----------- | ---------------------------------- | -------- |
| path        | jmes 表达式，用户指定 json/yaml key | 是        |
| objectAlias | 存入 k8s secret 对应的 data key     | 是       |

**secretStoreRef**

| crd 字段  | 描述                                              | 是否必选 |
| --------- | ------------------------------------------------ | ---------------------- |
| name      | 指定引用的 SecretStore 或 ClusterSecretStore name | 是                     |
| namespace | 指定的 SecretStore namespace                      | 否                    |
| kind      | 指定引用 SecretStore 或 ClusterSecretStore        | 否，默认为 SecretStore |

### SecretStore

**spec**

| crd 字段 | 描述                       | 是否必选 |
| -------- | --------------------------| -------- |
| KMS      | 连接KMS凭据管家服务获取密钥 | 否       |
| OOS      | 连接OOS服务获取加密参数     | 否       |

**KMS**

| crd 字段 | 描述                           | 是否必选 |
| -------- | ----------------------------- | -------- |
| KMSAuth  | 访问 KMS 所需凭证(密钥管理服务) | 否       |

**OOS**

| crd 字段 | 描述                           | 是否必选 |
| -------- | ----------------------------- | -------- |
| OOSAuth  | 访问 OOS 所需凭证(管理加密参数) | 否       |

**KMSAuth**

| crd 字段                 | 描述                                | 是否必选 |
| ------------------------ | ---------------------------------- | -------- |
| accessKey                | 参考如下阿里云AccessKey认证配置方式   | 否       |
| accessKeySecret          | 参考如下阿里云AccessKey认证配置方式   | 否       |
| ramRoleARN               | RAM 角色 ARN                        | 否       |
| ramRoleSessionName       | 角色会话名                          | 否       |
| oidcProviderARN          | OIDC 提供商 ARN                     | 否       |
| oidcTokenFilePath        | OIDC Token文件路径                  | 否       |
| remoteRamRoleArn         | 跨账号 RAM 角色 ARN                 | 否       |
| remoteRamRoleSessionName | 跨账号RAM角色 session name          | 否       |
| serviceAccountRef        | 引用的 ServiceAccount 信息          | 否       |

**OOSAuth**

| crd 字段                 | 描述                                | 是否必选 |
| ------------------------ | ---------------------------------- | ------- |
| accessKey                | 参考如下阿里云AccessKey认证配置方式   | 否      |
| accessKeySecret          | 参考如下阿里云AccessKey认证配置方式   | 否      |
| ramRoleARN               | RAM 角色 ARN                        | 否      |
| ramRoleSessionName       | 角色会话名                          | 否      |
| oidcProviderARN          | OIDC 提供商 ARN                     | 否      |
| oidcTokenFilePath        | OIDC Token文件路径                  | 否      |
| remoteRamRoleArn         | 跨账号 RAM 角色 ARN                 | 否      |
| remoteRamRoleSessionName | 跨账号 RAM 角色 session name        | 否      |
| serviceAccountRef        | 引用的 ServiceAccount 信息          | 否       |

**阿里云AccessKey认证配置方式**

阿里云AccessKey是用户访问云上资源最重要的身份凭据，不支持在SecretStore字段中直接配置，需要配置在Kubernetes Secrets实例中引用，相关引用配置字段说明如下：

> 阿里云AccessKey泄露会导致严重的安全风险，推荐您使用RRSA或其他阿里云认证方式

| 字段      | 描述                               | 是否必选 |
| --------- | --------------------------------- | -------- |
| name      | Kubernetes Secrets名称             | 是      |
| namespace | Kubernetes Secrets所在namaspace    | 是      |
| key       | Kubernetes Secrets 数据中指定的Key  | 是      |

**ServiceAccountRef**

| crd 字段  | 描述	                                             | 是否必选 |
| --------- | ------------------------------------------------- | -------- |
| name	    | ServiceAccount 名称	                              | 是       |
| namespace	| ServiceAccount 命名空间（仅ClusterSecretStore需要）| 否       |
| audiences	| Audience 数组，用于ServiceAccount token的aud字段	 | 否       |

### ClusterSecretStore

ClusterSecretStore 是集群级别的 SecretStore 资源，可被集群中的任意命名空间下的 ExternalSecret 引用。它除了具备 SecretStore 的所有功能外，还增加了访问控制配置，可以限制该资源被访问的命名空间。

**spec**

| crd 字段   | 描述	                          | 是否必选 |
| ---------- | ----------------------------- | ------- |
| conditions | 定义允许访问该资源的命名空间条件 | 是      |
| KMS        | 连接KMS凭据管家服务获取密钥     | 否      |
| OOS	       | 连接OOS服务获取加密参数		     | 否      |

**conditions**

| crd 字段          | 描述                                     | 是否必选 |
| ----------------- | ----------------------------------------| -------- |
| namespaceSelector | 使用标签选择器匹配允许访问的命名空间        | 是       |
| namespaces	      | 明确列出允许访问的命名空间名称列表          | 否       |
| namespaceRegexes	| 使用正则表达式匹配允许访问的命名空间名称列表 | 否       |

### ClusterExternalSecret

ClusterExternalSecret 是管理和协调多个命名空间下 ExternalSecret 的资源，能够在匹配的命名空间中自动创建 ExternalSecret，当匹配的命名空间匹配已存在需创建的 ExternalSecret 时，ClusterExternalSecret 用新的规范完全更新现有的 ExternalSecret 资源。

**spec**

| crd 字段               | 描述                                                                                | 是否必选 |
| ---------------------- | -----------------------------------------------------------------------------------| -------- |
| externalSecretSpec     | 要创建的 ExternalSecret 的规格定义                                                   | 是       |
| externalSecretName	   | 要创建的 ExternalSecret 的名称，默认是 ClusterExternalSecret 的名称                   | 否       |
| externalSecretMetadata | 要创建的 ExternalSecret 的元数据                                                     | 否       |
| namespaceSelectors     | 使用标签选择器匹配允许访问的命名空间(已废弃)                                           | 否       |
| conditions             | 用于选择目标命名空间的条件列表，支持 namespaceSelector、namespaces 和 namespaceRegexes | 否       |
| rotationInterval       | 控制器检查命名空间标签和协调对象的时间间隔                                             | 否       |

**externalSecretMetadata**

externalSecretMetadata 字段允许您自动为 ClusterExternalSecret 创建的 ExternalSecret 资源添加额外的元数据：

| crd 字段    | 描述                           | 是否必选 |
| ----------- | ------------------------------| -------- |
| annotations | 要创建的 ExternalSecret 的注解 | 否       |
| labels      | 要创建的 ExternalSecret 的标签 | 否       |

**conditions**

| crd 字段          | 描述                                     | 是否必选 |
| ----------------- | ----------------------------------------| -------- |
| namespaceSelector | 使用标签选择器匹配允许访问的命名空间        | 是       |
| namespaces	      | 明确列出允许访问的命名空间名称列表          | 否       |
| namespaceRegexes	| 使用正则表达式匹配允许访问的命名空间名称列表 | 否       |

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

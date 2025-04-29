# ACK Secret Manager

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager) 可以帮助您将存储在[阿里云KMS凭据管家](https://www.alibabacloud.com/help/zh/doc-detail/152001.html) 中的密钥凭据或[阿里云OOS加密参数](https://www.alibabacloud.com/help/zh/oos/getting-started/manage-encryption-parameters), 以K8s原生Secret对象的形式导入到集群中并实现密钥数据的自动同步，您可以在应用Pod中以挂载Secret等形式将存储在凭据管家或加密参数中的密文引入到应用程序中使用，避免敏感数据在应用开发构建流程中的传播和泄露。

## Helm CLI

### 先决条件

- Helm >= 3
- Kubernetes >= 1.16

### 安装

1. 拉取代码

```bash
git clone https://github.com/AliyunContainerService/ack-secret-manager.git
cd ack-secret-manager
```

2. 安装 ack-secret-manager

```bash
helm install ack-secret-manager ./charts/ack-secret-manager \
  --namespace kube-system \
  --set image.repository=registry-cn-hangzhou.ack.aliyuncs.com/acs/ack-secret-manager \
  --set command.region=cn-hangzhou \
  --set command.enableWorkerRole=false \
  --set command.kmsEnpoint=kms.cn-zhangzhou.aliyuncs.com
```

说明

- 指定参数 image.repository 为可拉取的 ack-secret-manager 镜像仓库地址，默认为 registry-cn-hangzhou.ack.aliyuncs.com/acs/ack-secret-manager
- 指定参数 command.region 为集群所在区域
- 自版本 0.5.6 开始，增加参数 command.enableWorkerRole，默认为 true，配置方式参考 [使用说明](https://github.com/AliyunContainerService/ack-secret-manager/blob/master/README-zh_CN.md#使用说明) 第5部分
- 自版本 0.5.8 开始，增加参数 command.kmsEnpoint 配置 KMS 服务地址，默认为空，配置方式参考 [使用说明](https://github.com/AliyunContainerService/ack-secret-manager/blob/master/README-zh_CN.md#使用说明) 第6部分

### 更新

1. 修改参数
   执行如下命令进行更新需要修改的参数

```bash
helm upgrade ack-secret-manager ./charts/ack-secret-manager \
    --namespace kube-system \
    --set command.maxConcurrentKmsSecretPulls=5
```

### 卸载

1. 卸载 ack-secret-manager

```bash
helm -n kube-system uninstall ack-secret-manager
```

## 阿里云容器服务控制台

### 安装

1. 请确保组件使用的凭据有足够的权限访问需要同步的阿里云服务，可以使用如下两种配置方式，推荐使用RRSA方式，实现Pod维度的授权
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
   - 通过 [RRSA方式](https://help.aliyun.com/document_detail/356611.html) 实现Pod维度的授权
     * [启用RRSA功能](https://help.aliyun.com/document_detail/356611.html#section-ywl-59g-j8h)
     * [使用RRSA功能](https://help.aliyun.com/document_detail/356611.html#section-rmr-eeh-878) ：为指定的 serviceaccount 创建对应的 RAM 角色，为 RAM 角色设置信任策略，并为 RAM 角色授权
2. 登录到容器服务控制台
   * 在左侧导航栏选择**市场** -> **应用市场**，在搜索栏中输入ack-secret-manager，选择进入到应用页面；
   * 选择需要安装的目标集群和命名空间、发布名称；
   * 在参数配置页面进行自定义参数配置，包括 values.yaml 中的 `rrsa.enable`以及配置 `envVarsFromSecret` 中的相关参数，参数说明参见下方的**配置说明**；
   * 点击**确定**按钮完成安装。

### 更新

1. 登录到容器服务控制台；
2. 选择目标集群点击进入到集群详情页面；
3. 在左侧的导航栏选择应用-> Helm，找到 ack-secret-manager 对应的**更新**，修改配置后点击**确定**按钮完成安装。

### 卸载

1. 登录到容器服务控制台；
2. 选择目标集群点击进入到集群详情页面；
3. 在左侧的导航栏选择应用-> Helm，找到 ack-secret-manager 对应的发布，点击操作拦中的删除按钮进行删除。

## 配置说明

| **参数**                                      | **说明**                                                                                                                                  | **默认值**       |
| --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------- |
| env.WATCH_NAMESPACE                                 | 指定组件watch的namespace（默认空值代表watch所有命名空间）                                                                                       |                        |
| envVarsFromSecret.ACCESS_KEY_ID                     | 可以通过设置ACCESS_KEY_ID变量指定凭证AK构建SDK client，需要定义在名称为alibaba-credentials的secret实例中                                        |                        |
| envVarsFromSecret.SECRET_ACCESS_KEY                 | 可以通过设置SECRET_ACCESS_KEY变量指定凭证SK构建SDK client，需要定义在名称为alibaba-credentials的secret实例中                                    |                        |
| envVarsFromSecret.ALICLOUD_ROLE_ARN                 | 可以通过设置ALICLOUD_ROLE_ARN变量指定RAM角色ARN用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中                            |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME        | 可以通过设置ALICLOUD_ROLE_SESSION_NAME变量指定RAM角色session name用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中          |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION  | 可以通过设置ALICLOUD_ROLE_SESSION_EXPIRATION变量指定RAM角色session过期时长用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret. ALICLOUD_OIDC_PROVIDER_ARN       | 可以通过设置ALICLOUD_OIDC_PROVIDER_ARN变量指定RAM OIDC供应商的ARN用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中          |                        |
| envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE          | 可以通过设置ALICLOUD_OIDC_TOKEN_FILE变量指定pod内oidc token文件路径用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中        |                        |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_ARN          | 可以通过设置 ALICLOUD_REMOTE_ROLE_ARN 变量指定另一个账号的 RAM Role Arn，用于在跨账号拉取凭据数据时进行角色扮演                                 |                        |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_SESSION_NAME | 可以通过设置 ALICLOUD_REMOTE_ROLE_SESSION_NAME 变量指定 RAM Role Session Name，于在跨账号拉取凭据数据时进行角色扮演                             |                        |
| rrsa.enable                                         | 是否启用RRSA特性，默认为false，启用后需要配置envVarsFromSecret中的ALICLOUD_ROLE_ARN和 ALICLOUD_OIDC_PROVIDER_ARN参数                            | false                  |
| command.reconcilePeriod                             | 控制器重新协调externalSecret实例的间隔时间，默认5秒                                                                                             | 5s                     |
| command.reconcileCount                              | 指定并发协调externalSecret实例的worker数量，默认是1                                                                                             | 1                      |
| command.tokenRotationPeriod                         | 检查 client访问STS token是否过期的轮询时间                                                                                                      | 120s                   |
| command.region                                      | 从指定region拉取secret凭据                                                                                                                      |                        |
| command.kmsEndpoint                                 | 从指定endpoint拉取secret凭据                                                                                                                    |                        |
| command.disablePolling                              | 关闭从后端自动同步拉取最新的凭据内容，默认false                                                                                                 | false                  |
| command.pollingInterval                             | 从后端同步存量secret实例的间隔时间                                                                                                              | 120s                   |
| command.maxConcurrentSecretPulls                    | 已弃用                                                                                                                                          | -                      |
| command.maxConcurrentKmsSecretPulls                 | kms secret 每秒同步的最大并发数量                                                                                                               | 10                     |
| command.maxConcurrentOosSecretPulls                 | oos secret 每秒同步的最大并发数量                                                                                                               | 10                     |
| image.repository                                    | 指定的ack-secret-manager 镜像仓库名称                                                                                                           | acs/ack-secret-manager |
| image.tag                                           | 指定的ack-secret-manager 镜像tag                                                                                                                | v0.5.0                 |
| image.pullPolicy                                    | 镜像拉取策略，默认为Always                                                                                                                      | Always                 |
| nameOverride                                        | 覆盖应用名称                                                                                                                                    | nil                    |
| fullnameOverride                                    | 覆盖应用全名                                                                                                                                    | nil                    |
| rbac.create                                         | 是否创建并使用RBAC资源，默认为true                                                                                                              | true                   |
| securityContext.fsGroup                             | 指定应用的security context配置                                                                                                                  | {}                     |
| serviceAccount.create                               | 是否创建serviceaccount                                                                                                                          | true                   |
| serviceAccount.name                                 | 指定创建serviceaccount的名称                                                                                                                    | 自动生成               |
| serviceAccount.annotations                          | 指定添加serviceaccount annotation标签                                                                                                           | nil                    |
| podAnnotations                                      | 指定添加到pod中的annotation标签                                                                                                                 | {}                     |
| podLabels                                           | 指定添加到pod中的Label标签                                                                                                                      | {}                     |
| replicaCount                                        | 控制器副本个数                                                                                                                                  | 1                      |
| nodeSelector                                        | 指定的nodeSelector标签                                                                                                                          | {}                     |
| tolerations                                         | 指定的污点容忍配置                                                                                                                              | []                     |
| affinity                                            | 指定的Pod亲和性配置                                                                                                                             | {}                     |
| resources                                           | 指定的Pod requests和limits配置                                                                                                                  | {}                     |

## 使用说明

下文会在阿里云 KMS 凭据管家中添加一个测试凭据进行凭据同步，并展示部分扩展功能。

| 配置字段                               | 阿里云 KMS 凭据                | 阿里云 OOS 加密参数               |
| -------------------------------------- | ------------------------------ | --------------------------------- |
| **SecretStore 认证方式关键字**   | SecretStore.Spec.KMS.KMSAuth   | SecretStore.Spec.OOS.OOSAuth      |
| **ExternalSecret.spec.provider** | kms                            | oos                               |
| **ExternalSecret.spec.data.key** | <KMS 凭据名称>(如下文的 test1) | <OOS加密参数名称>(如下文的 test2) |

ack-secret-manager 涉及了两种 CRD，SecretStore 用于存放访问凭据（例如 RRSA ，ClientKey，AK 配置等），ExternalSecret 用于存放需要同步的凭据基础信息（如凭据名称，版本等）以及指定 SecretStore，保证了权限与数据分离，增强使用灵活性。具体介绍见下方 **CRD 配置介绍**

1. 创建密文
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
2. 创建SecretStore & ExternalSecret

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
3. JSON/YAML 凭据解析

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
4. 当前支持跨账号同步凭据，在 `SecretStore.Spec.KMS.KMSAuth` 中配置 `remoteRamRoleArn`，`remoteRamRoleSessionName` 即可，以下为样例 SecretStore

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
5. command.enableWorkerRole 配置介绍
   command.enableWorkerRole 配置与集群类型有关，以下是对应关系：

   | 集群类型      | command.enableWorkerRole |
   | ------------- | ------------------------ |
   | ACK 托管集群  | true                     |
   | ACK 专有集群  | true                     |
   | ACK Edge 集群 | true                     |
   | 其它集群      | false                    |
6. command.kmsEndpoint 配置介绍
   KMS 当前支持专属网关和共享网关两种访问方式，应用当前支持这两种方式，不同方式需要配置不同的 Endpoint，以下是不同网关的 Endpoint 地址配置方式：

   | 网关类型     | Endpoint 地址                                    | 使用说明                                                                                                                            |
   | ------------ | ------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
   | 专属网关     | {kms-instance-id}.cryptoservice.kms.aliyuncs.com | 1. 要求 KMS 实例和集群 Region 相同且VPC 相同<br />2. 替换 {kms-instance-id} 为实际的 KMS 实例 ID<br />3. KMS 实例版本 3.0 以上      |
   | VPC共享网关  | kms-vpc.{region}.aliyuncs.com                    | 1. 要求 KMS 实例和集群 Region 相同<br />2. 应用 Endpoint 默认配置，使用此地址无需配置<br />3. 替换 {region} 为 KMS 实例所在的region |
   | 公网共享网关 | kms.{region}.aliyuncs.com                        | 1. 替换 {region} 为 KMS 实例所在的 region<br />2. 集群具有公网访问能力                                                              |

## CRD 配置介绍

### ExternalSecret

**spec**

| crd 字段    | 描述                                       | 是否必选 |
| ----------- | ------------------------------------------ | -------- |
| provider    | 获取 secret 的目标云服务（如 KMS 、OOS等） | 否       |
| data        | 数据源（目标数据的标识）                   | 否       |
| dataProcess | 需要进行特殊加工的数据源（目标数据的标识） | 否       |
| type        | k8s secret 类型（Opaque等）                | 否       |

**data（无需经过特殊处理的数据源）**

| crd 字段       | 描述                                                                            | 是否必选 |
| -------------- | ------------------------------------------------------------------------------- | -------- |
| key            | 目标 secret 的唯一标识（例如 KMS 凭据的 key）                                   | 是       |
| name           | 在集群 secret data 中对应的 key                                                 | 否       |
| versionStage   | 目标 secret 版本状态                                                            | 否       |
| versionId      | 目标 secret 版本号, 当 provider 是 oos 时, 则不需要指定该字段                   | 否       |
| jmesPath       | 如果目标 secret 为 json/yaml 类型，可指定获取 json/yaml 中特定 key 对应的 value | 否       |
| secretStoreRef | 引用的 SecretStore 信息                                                         | 否       |

**dataProcess（需要进行特殊处理的数据源）**

| crd 字段    | 描述                                                                                  | 是否必选 |
| ----------- | ------------------------------------------------------------------------------------- | -------- |
| extract     | 针对目标 secret 进行 json/yaml 解析，不需要用户指定 json/yaml key                     | 否       |
| replaceRule | 根据特定规则替换经过 json/yaml 解析的 secret 的 key，防止非法 key 不能存入 k8s secret | 否       |

**replaceRule（用于进行 Secret Key 内容替换）**

| crd 字段 | 描述                                 | 是否必选 |
| -------- | ------------------------------------ | -------- |
| target   | 用于替换的字符串                     | 是       |
| source   | 需要被替换的字符串，可以为正则表达式 | 是       |

**jmesPath**

| crd 字段    | 描述                                | 是否必选 |
| ----------- | ----------------------------------- | -------- |
| path        | jmes 表达式，用户指定 json/yaml key | 是       |
| objectAlias | 存入 k8s secret 对应的 data key     | 是       |

**secretStoreRef**

| crd 字段  | 描述                         | 是否必选 |
| --------- | ---------------------------- | -------- |
| name      | 指定的 SecretStore name      | 是       |
| namespace | 指定的 SecretStore namespace | 是       |

### SecretStore

**spec**

| crd 字段 | 描述                        | 是否必选 |
| -------- | --------------------------- | -------- |
| KMS      | 连接KMS凭据管家服务获取密钥 | 否       |
| OOS      | 连接OOS服务获取加密参数     | 否       |

**KMS**

| crd 字段 | 描述                            | 是否必选 |
| -------- | ------------------------------- | -------- |
| KMSAuth  | 访问 KMS 所需凭证(密钥管理服务) | 否       |

**OOS**

| crd 字段 | 描述                             | 是否必选 |
| -------- | -------------------------------- | -------- |
| OOSAuth  | 访问 OOS 所需凭证(管理加密参数) | 否       |

**KMSAuth**

| crd 字段                 | 描述                                | 是否必选 |
| ------------------------ | ----------------------------------- | -------- |
| accessKey                | 参考如下阿里云AccessKey认证配置方式 | 否       |
| accessKeySecret          | 参考如下阿里云AccessKey认证配置方式 | 否       |
| ramRoleARN               | RAM 角色 ARN                        | 否       |
| ramRoleSessionName       | 角色会话名                          | 否       |
| oidcProviderARN          | OIDC 提供商 ARN                     | 否       |
| oidcTokenFilePath        | OIDC Token文件路径                  | 否       |
| remoteRamRoleArn         | 跨账号 RAM 角色 ARN                 | 否       |
| remoteRamRoleSessionName | 跨账号RAM角色 session name          | 否       |

**OOSAuth**

| crd 字段                 | 描述                                | 是否必选 |
| ------------------------ | ----------------------------------- | -------- |
| accessKey                | 参考如下阿里云AccessKey认证配置方式 | 否       |
| accessKeySecret          | 参考如下阿里云AccessKey认证配置方式 | 否       |
| ramRoleARN               | RAM 角色 ARN                        | 否       |
| ramRoleSessionName       | 角色会话名                          | 否       |
| oidcProviderARN          | OIDC 提供商 ARN                     | 否       |
| oidcTokenFilePath        | OIDC Token文件路径                  | 否       |
| remoteRamRoleArn         | 跨账号 RAM 角色 ARN                 | 否       |
| remoteRamRoleSessionName | 跨账号 RAM 角色 session name        | 否       |

**阿里云AccessKey认证配置方式**

阿里云AccessKey是用户访问云上资源最重要的身份凭据，不支持在SecretStore字段中直接配置，需要配置在Kubernetes Secrets实例中引用，相关引用配置字段说明如下：

> 阿里云AccessKey泄露会导致严重的安全风险，推荐您使用RRSA或其他阿里云认证方式

| 字段      | 描述                               | 是否必选 |
| --------- | ---------------------------------- | -------- |
| name      | Kubernetes Secrets名称             | 是       |
| namespace | Kubernetes Secrets所在namaspace    | 是       |
| key       | Kubernetes Secrets 数据中指定的Key | 是       |

## 安全

对于发现的安全漏洞，请发送邮件至**kubernetes-security@service.aliyun.com**，您可在[SECURITY.md](./SECURITY.md)文件中找到更多信息。

## Release Note

| 版本号    | 变更时间       | 变更内容                                                                                                           |
| --------- | -------------- | ------------------------------------------------------------------------------------------------------------------ |
| `0.4.0` | 2022年12月22日 | 支持基于JMES解析提取JSON格式的密文字段                                                                             |
| `0.5.0` | 2023年10月10日 | 1.支持专属版 KMS 凭据同步<br />2.多阿里云访问凭据管理<br />3.凭据自解析与键规则替换<br />4.支持 KMS 跨账号凭据同步 |
| `0.5.1` | 2023年10月18日 | 部分功能与性能优化                                                                                                 |
| `0.5.2` | 2024年8月1日   | 大规模资源同步并发优化                                                                                             |
| `0.5.3` | 2024年10月10日 | 支持资源同步秒级限流，修复部分软件包cve                                                                            |
| `0.5.4` | 2024年10月31日 | 支持同步 OOS 加密参数                                                                                              |
| `0.5.5` | 2024年11月22日 | 支持同步 binary 类型的凭据                                                                                         |
| `0.5.6` | 2025年3月3日   | 支持Acs集群                                                                                                        |
| `0.5.7` | 2025年3月26日  | 支持解析提取YAML格式的密文字段                                                                                     |
| `0.5.8` | 2025年3月28日  | 1.支持多架构部署<br />2.支持配置 Endpoint 获取 KMS 凭据                                                            |

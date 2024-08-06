

# ACK Secret Manager

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager) 可以帮助您将存储在[阿里云KMS凭据管家](https://www.alibabacloud.com/help/zh/doc-detail/152001.html) 中的密钥凭据以K8s原生Secret对象的形式导入到集群中并实现密钥数据的自动同步，您可以在应用Pod中以挂载Secret等形式将存储在凭据管家中的密文引入到应用程序中使用，避免敏感数据在应用开发构建流程中的传播和泄露。



## 安装

确保当前账号有足够的权限访问阿里云凭据管家服务，ack-secret-manager 支持共享网关和专属网关两种方式同步凭据管家的凭据。

1. 设置共享网关访问权限，这里有两种方式：

    - 在集群对应的 WorkerRole 中添加权限

        - 登录容器服务控制台

        - 选择对应集群进入到集群详情页

        - 在集群信息中选择**集群资源**页，点击Worker RAM角色中对应的命名为**KubernetesWorkerRole-xxxxxxxxxxxxxxx** 的角色名称，会自动导航到RAM角色对应的控制台页面

        - 点击添加权限按钮，创建自定义权限策略，策略内容如下：

          ```json
          {
              "Action": [
                 "kms:GetSecretValue",
                 "kms:Decrypt"
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

2. 设置专属网关访问权限，详情见[通过应用接入点访问KMS实例](https://help.aliyun.com/document_detail/604467.html?spm=a2c4g.2252257.0.0.7f047495H2lmEh)

3. 登录到容器服务控制台

    * 在左侧导航栏选择**市场** -> **应用市场**，在搜索栏中输入ack-secret-manager，选择进入到应用页面；

    * 选择需要安装的目标集群和命名空间、发布名称；

    * 在参数配置页面进行自定义参数配置，包括 values.yaml 中的`rrsa.enable`以及配置 `envVarsFromSecret` 中的相关参数，参数说明参见下方的**配置说明**；

    * 点击**确定**按钮完成安装。

## 升级

1. 登录到容器服务控制台；
2. 选择目标集群点击进入到集群详情页面；
3. 在左侧的导航栏选择应用-> Helm，找到 ack-secret-manager 对应的**更新**，修改配置后点击**确定**按钮完成安装。

## 卸载

1. 登录到容器服务控制台；
2. 选择目标集群点击进入到集群详情页面；
3. 在左侧的导航栏选择应用-> Helm，找到 ack-secret-manager 对应的发布，点击操作拦中的删除按钮进行删除。

## 配置说明

| **参数**                                            | **说明**                                                     | **默认值**             |
| --------------------------------------------------- | ------------------------------------------------------------ | ---------------------- |
| env.WATCH_NAMESPACE                                 | 指定组件watch的namespace（默认空值代表watch所有命名空间）    |                        |
| envVarsFromSecret.ACCESS_KEY_ID                     | 可以通过设置ACCESS_KEY_ID变量指定凭证AK构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.SECRET_ACCESS_KEY                 | 可以通过设置SECRET_ACCESS_KEY变量指定凭证SK构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_ROLE_ARN                 | 可以通过设置ALICLOUD_ROLE_ARN变量指定RAM角色ARN用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME        | 可以通过设置ALICLOUD_ROLE_SESSION_NAME变量指定RAM角色session name用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION  | 可以通过设置ALICLOUD_ROLE_SESSION_EXPIRATION变量指定RAM角色session过期时长用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret. ALICLOUD_OIDC_PROVIDER_ARN       | 可以通过设置ALICLOUD_OIDC_PROVIDER_ARN变量指定RAM OIDC供应商的ARN用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE          | 可以通过设置ALICLOUD_OIDC_TOKEN_FILE变量指定pod内oidc token文件路径用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_ARN          | 可以通过设置 ALICLOUD_REMOTE_ROLE_ARN 变量指定另一个账号的 RAM Role Arn，用于在跨账号拉取凭据数据时进行角色扮演 |                        |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_SESSION_NAME | 可以通过设置 ALICLOUD_REMOTE_ROLE_SESSION_NAME 变量指定 RAM Role Session Name，于在跨账号拉取凭据数据时进行角色扮演 |                        |
| rrsa.enable                                         | 是否启用RRSA特性，默认为false，启用后需要配置envVarsFromSecret中的ALICLOUD_ROLE_ARN和 ALICLOUD_OIDC_PROVIDER_ARN参数 | false                  |
| command.backend                                     | 对接的外部密钥管理系统后端，当前仅支持阿里云凭据管家，配置为alicloud-kms | alicloud-kms           |
| command.reconcilePeriod                             | 控制器重新协调externalSecret实例的间隔时间，默认5秒          | 5s                     |
| command.reconcileCount                              | 指定并发协调externalSecret实例的worker数量，默认是1          | 1                      |
| command.tokenRotationPeriod                         | 检查KMS client访问STS token是否过期的轮询时间                | 120s                   |
| command.region                                      | 从指定region拉取secret凭据                                   |                        |
| command.disablePolling                              | 关闭从KMS后端自动同步拉取最新的凭据内容，默认false           | false                  |
| command.pollingInterval                             | 从KMS后端同步存量secret实例的间隔时间                        | 120s                   |
| command.maxConcurrentSecretPulls                    | secret 同步的最大并发数量                                    | 5                      |
| image.repository                                    | 指定的ack-secret-manager 镜像仓库名称                        | acs/ack-secret-manager |
| image.tag                                           | 指定的ack-secret-manager 镜像tag                             | v0.5.0                 |
| image.pullPolicy                                    | 镜像拉取策略，默认为Always                                   | Always                 |
| nameOverride                                        | 覆盖应用名称                                                 | nil                    |
| fullnameOverride                                    | 覆盖应用全名                                                 | nil                    |
| rbac.create                                         | 是否创建并使用RBAC资源，默认为true                           | true                   |
| securityContext.fsGroup                             | 指定应用的security context配置                               | {}                     |
| serviceAccount.create                               | 是否创建serviceaccount                                       | true                   |
| serviceAccount.name                                 | 指定创建serviceaccount的名称                                 | 自动生成               |
| serviceAccount.annotations                          | 指定添加serviceaccount annotation标签                        | nil                    |
| podAnnotations                                      | 指定添加到pod中的annotation标签                              | {}                     |
| podLabels                                           | 指定添加到pod中的Label标签                                   | {}                     |
| replicaCount                                        | 控制器副本个数                                               | 1                      |
| nodeSelector                                        | 指定的nodeSelector标签                                       | {}                     |
| tolerations                                         | 指定的污点容忍配置                                           | []                     |
| affinity                                            | 指定的Pod亲和性配置                                          | {}                     |
| resources                                           | 指定的Pod requests和limits配置                               | {}                     |



## 使用说明

下文会在阿里云 KMS 凭据管家中添加一个测试凭据，并分别通过专属网关以及共享网关两种方式进行凭据同步，并展示部分扩展功能

ack-secret-manager 涉及了两种 CRD，SecretStore 用于存放访问凭据（例如 RRSA ，ClientKey，AK 配置等），ExternalSecret 用于存放需要同步的凭据基础信息（如凭据名称，版本等）以及指定 SecretStore，保证了权限与数据分离，增强使用灵活性。具体介绍见下方 **CRD 配置介绍**

1. 创建凭据

   在KMS凭据管家中添加如下凭证，详细流程请参考[管理通用凭据](https://www.alibabacloud.com/help/zh/doc-detail/152003.html)

   ```txt
   SecretName: test1
   SecretData: {"name":"tom","age":"14","friends":[{"name":"lili"},{"name":"edf"}]} 
   VersionId: v1
   ```

2. 共享网关

   前提：给集群开启 RRSA，并且正确配置相关 RAM Role 权限

    - 创建 SecretStore 的测试实例，测试模板如下，需对部分字段进行替换

      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: SecretStore
      metadata:
        name: scdemo
      spec:
        KMS:
          KMSAuth:
            oidcProviderARN: "acs:ram::{accountID}:oidc-provider/ack-rrsa-{clusterID}"
            ramRoleARN: "acs:ram::{accountID}:role/{roleName}"
      ```

    - 创建ExternalSecret的测试实例，测试模板如下：

      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: ExternalSecret
      metadata:
        name: esdemo
      spec:
        data: #无需特殊处理的数据源
          - key: 	test1 # kms 凭据名称
            name: test1 # 存入 secret 字段
            versionId: v1 #kms 凭据版本
            secretStoreRef:
              name: scdemo
              namespace: default
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
        creationTimestamp: "2023-10-09T13:03:09Z"
        labels:
          lastUpdatedAt: 2023-10-09T13.03.09Z
        name: esdemo
        namespace: default
        resourceVersion: "7311947"
        uid: 163c6a33-0bee-40b6-8ffe-6897277036cd
      type: Opaque
      ```

    - 在没有关闭自动同步配置的前提下，可以修改KMS凭据管家中的密钥内容，等待片刻后查看目标secret是否已经完成同步

    - 如果您希望解析一个 JSON 格式的 secret 并将其中指定的 key-value 对同步到 k8s secret 中，可以使用`jmesPath`字段。以下是一个使用 `jmesPath` 字段的样例，我们将其部署在集群中

      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: ExternalSecret
      metadata:
        name: es-json-demo
      spec:
        data: #无需特殊处理的数据源
          - key: 	test1 # kms 凭据名称
            name: test1 # 存入 secret 字段
            versionId: v1 #kms 凭据版本
            secretStoreRef:
              name: scdemo
              namespace: default
            jmesPath: # 解析 json 串中的部分字段
              - path: "name"
                objectAlias: "name"
              - path: "friends[0].name"
                objectAlias: "friendname"
      ```

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
        creationTimestamp: "2023-10-09T13:11:05Z"
        labels:
          lastUpdatedAt: 2023-10-09T13.11.05Z
        name: es-json-demo
        namespace: default
        resourceVersion: "7313940"
        uid: 18d84558-f526-4ff5-ab9a-720ec1861c30
      type: Opaque
      ```

    - 当您使用`jmesPath`字段时，必需指定下面两个子字段：

        - `path`: 必需项，基于 [JMES path](https://jmespath.org/specification.html) 规范解析 json 中的指定字段

        - `objectAlias`: 必需项，用于指定解析出的字段同步到 k8s secret 中的 key 名称

    - 共享网关当前支持跨账号同步凭据，在 `SecretStore.Spec.KMS.KMSAuth` 中配置 `remoteRamRoleArn`，`remoteRamRoleSessionName` 即可，以下为样例 SecretStore

      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: SecretStore
      metadata:
        name: scdemo
      spec:
        KMS:
          KMSAuth:
            oidcProviderARN: "acs:ram::{accountID}:oidc-provider/ack-rrsa-{clusterID}"
            ramRoleARN: "acs:ram::{accountID}:role/{roleName}"
            remoteRamRoleArn: "acs:ram::{accountID}:role/{roleName}"
            remoteRamRoleSessionName: ""
      ```



3. 专属网关

   前提：已在 KMS 控制台创建应用接入点，并在应用接入点中创建 ClientKey，ClientKey 正确配置了关于凭据 test1 的权限

    - 在集群中创建 ClientKeyContent，Password 等相关字段的 secret

      ```yaml
      apiVersion: v1
      data:
        clientkey: {{ Client Key File Content }}
        password: {{ Password }}
      kind: Secret
      metadata:
        name: clientkey
        namespace: kube-system
      type: Opaque
      ```

    - 创建 SecretStore 实例

      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: SecretStore
      metadata:
        name: dkms-client
      spec:
        KMS:
          dedicatedKMSAuth:
            protocol: "https"
            endpoint: {{ kms Instance ID }}
            ignoreSSL: true
            clientKeyContent:
              name: clientkey
              namespace: kube-system
              key: clientkey
            password:
              name: clientkey
              namespace: kube-system
              key: password
      ```

    - 创建 ExternalSecret 实例，SecretStoreRef 选择 default/dkms-client

      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: ExternalSecret
      metadata:
        name: es-dkms-demo
      spec:
        data: #无需特殊处理的数据源
          - key: 	test1 # kms 凭据名称
            name: dkms # 存入 secret 字段
            versionId: v1 #kms 凭据版本
            secretStoreRef:
              name: dkms-client
              namespace: default
      ```

    - 同步成功即可看到如下结果

      ```yaml
      apiVersion: v1
      data:
        dkms: eyJuYW1lIjoidG9tIiwiYWdlIjoiMTQiLCJmcmllbmRzIjpbeyJuYW1lIjoibGlsaSJ9LHsibmFtZSI6ImVkZiJ9XX0g
      kind: Secret
      metadata:
        creationTimestamp: "2023-10-09T13:59:24Z"
        labels:
          lastUpdatedAt: 2023-10-09T13.59.24Z
        name: es-dkms-demo
        namespace: default
        resourceVersion: "7326124"
        uid: 4773959c-d90a-44c6-bf88-094855802683
      type: Opaque
      ```

    - 如果您想将 JSON 凭据解析后再存放入 secret 中，但又不知道凭据的具体结构，可以采用自解析功能，即 dataProcess.Extract 字段。并且可以针对解析后的字段键进行规则替换，即 dataProcss.replaceRule 字段，防止不规则的 secret data key 导致无法创建 secret，以下为样例 ExternalSecret

      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: ExternalSecret
      metadata:
        name: extract-secret
      spec:
        dataProcess:
          - extract:
              key: test1
              name: extract
              versionId: v1
              secretStoreRef:
                name: dkms-client
                namespace: default
            replaceRule:
              - source: "^n.*e$" #替换 以n开头以e结尾 的 key 为 alibabacloud
                target: "alibabacloud"
              - source: "^f.*s$"
                target: "ack"
      ```

    - 同步成功即可看到如下结果，JSON 凭据被解析为三部分，且各自的键根据 replaceRule 规则进行了替换

      ```yaml
      apiVersion: v1
      data:
        ack: W3sibmFtZSI6ImxpbGkifSx7Im5hbWUiOiJlZGYifV0=
        age: IjE0Ig==
        alibabacloud: InRvbSI=
      kind: Secret
      metadata:
        creationTimestamp: "2023-10-09T14:07:35Z"
        labels:
          lastUpdatedAt: 2023-10-09T14.07.35Z
        name: extract-secret
        namespace: default
        resourceVersion: "7328187"
        uid: a3ab5278-02bd-4544-bae7-b502acccfe3c
      type: Opaque
      ```

## CRD 配置介绍

### ExternalSecret

**spec**

| crd 字段    | 描述                                       | 是否必选 |
| ----------- | ------------------------------------------ | -------- |
| provider    | 获取 secret 的目标云产品（如 KMS 等）      | 否       |
| data        | 数据源（目标数据的标识）                   | 否       |
| dataProcess | 需要进行特殊加工的数据源（目标数据的标识） | 否       |
| type        | k8s secret 类型（Opaque等）                | 否       |

**data（无需经过特殊处理的数据源）**

| crd 字段       | 描述                                                         | 是否必选 |
| -------------- | ------------------------------------------------------------ | -------- |
| key            | 目标 secret 的唯一标识（例如 KMS 凭据的 key）                | 是       |
| name           | 在集群 secret data 中对应的 key                              | 否       |
| versionStage   | 目标 secret 版本状态                                         | 否       |
| versionId      | 目标 secret 版本号                                           | 否       |
| jmesPath       | 如果目标 secret 为 json 类型，可指定获取 json 中特定 key 对应的 value | 否       |
| secretStoreRef | 引用的 SecretStore 信息                                      | 否       |

**dataProcess（需要进行特殊处理的数据源）**

| crd 字段    | 描述                                                         | 是否必选 |
| ----------- | ------------------------------------------------------------ | -------- |
| extract     | 针对目标 secret 进行 json 解析，不需要用户指定 json key      | 否       |
| replaceRule | 根据特定规则替换经过 json 解析的 secret 的 key，防止非法 key 不能存入 k8s secret | 否       |

**replaceRule（用于进行 Secret Key 内容替换）**

| crd 字段 | 描述                                 | 是否必选 |
| -------- | ------------------------------------ | -------- |
| target   | 用于替换的字符串                     | 是       |
| source   | 需要被替换的字符串，可以为正则表达式 | 是       |

**jmesPath**

| crd 字段    | 描述                            | 是否必选 |
| ----------- | ------------------------------- | -------- |
| path        | jmes 表达式，用户指定 json key  | 是       |
| objectAlias | 存入 k8s secret 对应的 data key | 是       |

**secretStoreRef**

| crd 字段  | 描述                         | 是否必选 |
| --------- | ---------------------------- | -------- |
| name      | 指定的 SecretStore name      | 是       |
| namespace | 指定的 SecretStore namespace | 是       |

### SecretStore

**spec**

| crd 字段 | 描述                | 是否必选 |
| -------- | ------------------- | -------- |
| KMS      | 代表目标云产品为KMS | 否       |

**KMS**

| crd 字段         | 描述                        | 是否必选 |
| ---------------- | --------------------------- | -------- |
| KMSAuth          | 共享网关下访问 KMS 所需凭证 | 否       |
| dedicatedKMSAuth | 专属网关下访问 KMS 所需凭证 | 否       |

**KMSAuth**

| crd 字段                 | 描述                         | 是否必选 |
| ------------------------ | ---------------------------- | -------- |
| accessKey                | 用户AK                       | 否       |
| accessKeySecret          | 用户SK                       | 否       |
| ramRoleARN               | ram 角色 arn                 | 否       |
| ramRoleSessionName       | 角色会话名                   | 否       |
| oidcProviderARN          | oidc 提供商 arn              | 否       |
| oidcTokenFilePath        | 暂时无用                     | 否       |
| remoteRamRoleArn         | 跨账号 ram 角色 arn          | 否       |
| remoteRamRoleSessionName | 跨账号 ram 角色 session name | 否       |

**dedicatedKMSAuth**

| crd 字段         | 描述                          | 是否必选 |
| ---------------- | ----------------------------- | -------- |
| protocol         | 传输协议（https）             | 是       |
| endpoint         | kms 实例 ID                   | 是       |
| ca               | 用户根CA,base64编码           | 否       |
| ignoreSSL        | 是否忽略 ssl 认证             | 否       |
| clientKeyContent | 专属网关 client key file 内容 | 是       |
| password         | client key 加密口令           | 是       |

**SecretRef（敏感访问凭据存放在 K8S secret 中）**

| crd 字段  | 描述                     | 是否必选 |
| --------- | ------------------------ | -------- |
| name      | k8s secret 名称          | 是       |
| namespace | k8s secret 所在namaspace | 是       |
| key       | k8s secret 数据对应的key | 是       |

## Release Note

| 版本号  | 变更时间       | 变更内容                                                     |
| ------- | -------------- | ------------------------------------------------------------ |
| `0.4.0` | 2022年12月22日 | 支持基于JMES解析提取JSON格式的密文字段                       |
| `0.5.0` | 2023年10月10日 | 1.支持专属版 KMS 凭据同步<br />2.多阿里云访问凭据管理<br />3.凭据自解析与键规则替换<br />4.共享版 KMS 跨账号凭据同步 |
| `0.5.1` | 2023年10月18日 | 部分功能与性能优化                                           |
| `0.5.2` | 2024年8月1日   | 大规模资源同步并发优化                                       |


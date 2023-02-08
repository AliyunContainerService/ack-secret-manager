

# ACK Secret Manager

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager) 可以帮助您将存储在[阿里云KMS凭据管家](https://www.alibabacloud.com/help/zh/doc-detail/152001.html) 中的密钥凭据以K8s原生Secret对象的形式导入到集群中并实现密钥数据的自动同步，您可以在应用Pod中以挂载Secret等形式将存储在凭据管家中的密文引入到应用程序中使用，避免敏感数据在应用开发构建流程中的传播和泄露。



## 安装

1. 当前账号有足够的阿里云凭据管家接口权限，以便您在KMS凭据管家服务中完成对密文数据的管理，权限策略说明参见[使用RAM实现对资源的访问控制](https://www.alibabacloud.com/help/zh/doc-detail/28953.html)

2. 授予ack-secret-manager获取凭据密文的权限，这里有两种方式：
   a  在集群对应的WorkerRole中添加权限
   * 登录容器服务控制台
   * 选择对应集群进入到集群详情页
   * 在集群信息中选择**集群资源**页，点击Worker RAM角色中对应的命名为			**KubernetesWorkerRole-xxxxxxxxxxxxxxx** 的角色名称，会自动导航到RAM角色对应的控制台页面
   * 点击添加权限按钮，创建自定义权限策略，策略内容如下：
   ```json
   {
   	"Action": [
   		"kms:GetSecretValue"
   	],
   	"Resource": [
   		"*"
   	],
   	"Effect": "Allow"
   }
   ```
   * 绑定上面创建的自定义策略给集群对应的WorkerRole

   b 对于1.22集群，可以通过[RRSA方式](https://help.aliyun.com/document_detail/356611.html) 实现Pod维度的授权（**注意：该方式仅支持1.22版本的ACK标准版和Pro版集群**）

   * [启用RRSA功能](https://help.aliyun.com/document_detail/356611.html#section-ywl-59g-j8h)
   * [使用RRSA功能](https://help.aliyun.com/document_detail/356611.html#section-rmr-eeh-878) ：包括为指定的serviceaccount创建对应的RAM角色，角色的信任策略以及为角色授权。

3. 登录到容器服务控制台

   * 在左侧导航栏选择**市场** -> **应用市场**，在搜索栏中输入ack-secret-manager，选择进入到应用页面；
   * 选择需要安装的目标集群和命名空间、发布名称；
   * 在参数配置页面进行自定义参数配置，包括values.yaml中的`rrsa.enable`以及配置envVarsFromSecret中的`ALICLOUD_ROLE_ARN`和 `ALICLOUD_OIDC_PROVIDER_ARN`参数,  参数说明参见下方的配置说明；
   * 点击**确定**按钮完成安装。


## 卸载

1. 登录到容器服务控制台；
2. 选择目标集群点击进入到集群详情页面；
3. 在左侧的导航栏选择应用-> Helm，找到ack-secret-manager对应的发布，点击操作拦中的删除按钮进行删除。



## 配置说明

| **参数**                                           | **说明**                                                     | **默认值**                |
| -------------------------------------------------- | ------------------------------------------------------------ |------------------------|
| env.WATCH_NAMESPACE                                | 指定组件watch的namespace（默认空值代表watch所有命名空间）    |                        |
| envVarsFromSecret.ACCESS_KEY_ID                    | 可以通过设置ACCESS_KEY_ID变量指定凭证AK构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.SECRET_ACCESS_KEY                | 可以通过设置SECRET_ACCESS_KEY变量指定凭证SK构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_ROLE_ARN                | 可以通过设置ALICLOUD_ROLE_ARN变量指定RAM角色ARN用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME       | 可以通过设置ALICLOUD_ROLE_SESSION_NAME变量指定RAM角色session name用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION | 可以通过设置ALICLOUD_ROLE_SESSION_EXPIRATION变量指定RAM角色session过期时长用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret. ALICLOUD_OIDC_PROVIDER_ARN      | 可以通过设置ALICLOUD_OIDC_PROVIDER_ARN变量指定RAM OIDC供应商的ARN用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE         | 可以通过设置ALICLOUD_OIDC_TOKEN_FILE变量指定pod内oidc token文件路径用于构建SDK client，需要定义在名称为alibaba-credentials的secret实例中 |                        |
| rrsa.enable                                        | 是否启用RRSA特性，默认为false，启用后需要配置envVarsFromSecret中的ALICLOUD_ROLE_ARN和 ALICLOUD_OIDC_PROVIDER_ARN参数 | false                  |
| command.backend                                    | 对接的外部密钥管理系统后端，当前仅支持阿里云凭据管家，配置为alicloud-kms | alicloud-kms           |
| command.reconcilePeriod                            | 控制器重新协调externalSecret实例的间隔时间，默认5秒          | 5s                     |
| command.reconcileCount                             | 指定并发协调externalSecret实例的worker数量，默认是1          | 1                      |
| command.tokenRotationPeriod                        | 检查KMS client访问STS token是否过期的轮询时间                | 120s                   |
| command.region                                     | 从指定region拉取secret凭据                                   |                        |
| command.disablePolling                             | 关闭从KMS后端自动同步拉取最新的凭据内容，默认false           | false                  |
| command.pollingInterval                            | 从KMS后端同步存量secret实例的间隔时间                        | 120s                   |
| image.repository                                   | 指定的ack-secret-manager 镜像仓库名称                        | acs/ack-secret-manager |
| image.tag                                          | 指定的ack-secret-manager 镜像tag                             | v0.3.0                 |
| image.pullPolicy                                   | 镜像拉取策略，默认为Always                                   | Always                 |
| nameOverride                                       | 覆盖应用名称                                                 | nil                    |
| fullnameOverride                                   | 覆盖应用全名                                                 | nil                    |
| rbac.create                                        | 是否创建并使用RBAC资源，默认为true                           | true                   |
| securityContext.fsGroup                            | 指定应用的security context配置                               | {}                     |
| serviceAccount.create                              | 是否创建serviceaccount                                       | true                   |
| serviceAccount.name                                | 指定创建serviceaccount的名称                                 | 自动生成                   |
| serviceAccount.annotations                         | 指定添加serviceaccount annotation标签                        | nil                    |
| podAnnotations                                     | 指定添加到pod中的annotation标签                              | {}                     |
| podLabels                                          | 指定添加到pod中的Label标签                                   | {}                     |
| replicaCount                                       | 控制器副本个数                                               | 1                      |
| nodeSelector                                       | 指定的nodeSelector标签                                       | {}                     |
| tolerations                                        | 指定的污点容忍配置                                           | []                     |
| affinity                                           | 指定的Pod亲和性配置                                          | {}                     |
| resources                                          | 指定的Pod requests和limits配置                               | {}                     |



## 使用说明



下文会通过在阿里云KMS凭据管家中添加一个测试凭据，并在目标集群中创建一个ExternalSecret实例来展示从凭据管家中导入密钥到集群中创建并同步Secret的过程：



1. 在KMS凭据管家中添加凭证，可通过下面的阿里云CLI工具命令行完成，详细流程请参考[管理通用凭据](https://www.alibabacloud.com/help/zh/doc-detail/152003.html)

```sh
aliyun kms CreateSecret --SecretName test --SecretData 1234 --VersionId v1
```

2.	创建ExternalSecret的测试实例，测试模板如下：

```yml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: hello-service
spec:
  data:
    - key: test
      name: password
      versionStage: ACSCurrent
```

执行命令创建externalsecret测试实例：

```sh
kubectl apply -f hello-service-external-secret.yml
```

3.  查看目标secret是否创建成功：

```sh
kubectl get secret hello-service -oyaml
```

如果创建成功，查看secret内容如下：

```yml
apiVersion: v1
kind: Secret
metadata:
  name: hello-service
type: Opaque
data:
  password: MTIzNA==
```


4. 在没有关闭自动同步配置的前提下，可以修改KMS凭据管家中的密钥内容，等待片刻后查看目标secret是否已经完成同步

5. 如果您希望解析一个JSON格式的secret并将其中指定的key-value对同步到k8s secret中，可以使用`jmesPath`字段。示例：假如您在KMS Secrets Manager中有如下JSON格式的secret：
```
{
	"username": "testuser",
	"password": "testpassword"
}
```

为了解析其中的username和password键值对并将其独立同步到k8s secrets中，可以使用如下的jmesPath字段配置：

```
  data:
    - key: testJson
      name: password
      jmesPath:
      - path: "username"
        objectAlias: "MySecretUsername"
      - path: "password"
        objectAlias: "MySecretPassword"
```

If you use the jmesPath field, you must provide the following two sub-fields:

当您使用jmesPath字段时，必需指定下面两个子字段：

- path: 必需项，基于 [JMES path](https://jmespath.org/specification.html) 规范解析json中的指定字段
- objectAlias: 必需项，用于指定解析出的字段同步到k8s secret中的key名称

同步后的secret实例如下:

```yml
apiVersion: v1
kind: Secret
metadata:
  name: hello-service
type: Opaque
data:
  MySecretPassword: dGVzdFBhc3N3b3Jk
  MySecretUsername: dGVzdFVzZXI=
```
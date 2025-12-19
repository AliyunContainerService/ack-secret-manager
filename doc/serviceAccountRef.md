

# serviceAccountRef认证:  ServiceAccount维度的细粒度RRSA授权

## 背景

在多租场景中，不同租户的用户希望使用各自命名空间的ServiceAccount绑定不同的RAM角色，并授权角色只能访问自己权限范围内KMS凭据管家中的指定凭据。

ack-secret-manager支持在租户命名空间的SecretStore中使用`serviceAccountRef`认证方式，将获取KMS凭据权限细粒度控制在ServiceAccount维度。

![image.png](./serviceAccountRef.png)


## 配置组件认证信息

基于Namespace维度下不同ServiceAccount的RRSA授权实现多租场景下的凭据管家访问权限隔离。

RRSA适用于1.22及以上版本的ACK托管集群和ACK Serverless集群。相比其他授权方式，RRSA授权方式可以实现Pod维度的权限隔离，还可以避免直接使用AK、SK引起的凭据泄露风险。

1. 在[容器服务管理控制台](https://cs.console.aliyun.com/)开启集群的RRSA功能，用于创建集群的身份提供商信息**。**具体操作，请参见[启用RRSA功能](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services#section-ywl-59g-j8h)。
2. 在指定命名空间下创建访问指定KMS凭据管家的独立ServiceAccount，注意ServiceAccount需要添加键值为`ack.alibabacloud.com/role-arn`的指定annotation，值为该ServiceAccount绑定的目标RAM角色ARN。

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  annotations:
    ack.alibabacloud.com/role-arn: acs:ram::<aliuid>:role/<role-name>
  name: <sa-name>
  namespace: <namespace>
```


3. 为不同的ServiceAccount创建可信实体为身份提供商的RAM角色。选择信任主体类型为身份提供商，添加主体时主要参数设置如下，具体操作，请参见创建OIDC身份提供商的RAM角色。

| **配置项**         | **描述**                                                     |
| ------------------ | ------------------------------------------------------------ |
| **身份提供商类型** | **OIDC**。                                                   |
| **身份提供商**     | 选择 `ack-rrsa-<cluster_id>`。其中，`<cluster_id>` 为集群 ID。 |
| **条件**           | - `oidc:iss`：保持默认。<br>- `oidc:aud`：保持默认。<br>- `oidc:sub`：需手动添加该条件。<br>　　- **条件键**：选择 `oidc:sub`。<br>　　- **运算符**：选择 `StringEquals`。<br>　　- **条件值**：输入 `system:serviceaccount:<namespace>:<serviceAccountName>`。<br>　　　　- `<namespace>` 为指定 ServiceAccount 的命名空间。<br>　　　　- `<serviceAccountName>` 为服务账户的名称。<br><br>**说明**：这里的 `namespace` 和 `serviceAccountName` 是指不同租户用于访问 KMS 凭据管家的独立 ServiceAccount。 |

4. 创建自定义权限策略并为上一步创建的RAM角色授权。
    - 创建指定导入KMS凭据时所需的权限策略。策略内容如下。具体操作，请参见[创建自定义权限策略](https://help.aliyun.com/zh/ram/create-a-custom-policy)。
   ```json
   {
    "Version": "1",
    "Statement": [
    {
      "Action": [
        "kms:GetSecretValue",
        "kms:Decrypt"
      ],
      "Resource": "acs:kms:<regionId>:<aliuid>:secret/xxxx",  #指定的KMS凭据ARN
      "Effect": "Allow"
    }
    ]
   }
	```
    -  为上一步创建的RAM角色授权。具体操作，请参见[为RAM角色授权](https://help.aliyun.com/zh/ram/user-guide/grant-permissions-to-a-ram-role#task-187801)。

5. 使用`serviceAccountRef`认证方式部署自定义资源SecretStore。

- 基于以下内容，替换相关字段后，创建secretstore-rrsa.yaml文件。

    - `<name>`：替换为指定的SecretStore实例名称。

    - `<namespace>`：替换为指定的集群命名空间名称。

    - `<sa-name>`：替换为[步骤2](https://help.aliyun.com/zh/ack/ack-managed-and-ack-dedicated/security-and-compliance/use-ack-secret-manager-to-import-alibaba-cloud-kms-service-credentials?spm=a2c4g.11186623.help-menu-85222.d_4_4_5_0.6d61acb8srf5b2#00f1aa9e28ms7)中创建的ServiceAccount实例名称。

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: SecretStore
metadata:
  name: <name>
  namespace: <namespace>
spec:
  KMS:
    KMSAuth:
      serviceAccountRef:
        name: <sa-name>
```

- 执行以下命令，部署SecretStore。

  ```yaml
  kubectl apply -f secretstore-rrsa.yaml
  ```






## 部署ExternalSecrets

- 按上述步骤分别创建命名空间team-a和team-b，并分别在命名空间中创建对应的ServiceAccount。
- 在KMS凭据管家中名称为team-a和team-b的通用凭据实例，模拟不同用户的密钥实例。
- 参考上述组件认证配置信息中的步骤，创建不同用户的RAM角色，将信任策略主体中的oidc:sub条件信息配置为对应Namespace中的ServiceAccount名称。创建完成后授权RAM角色访问指定KMS凭据权限。
- 为命名空间team-a和team-b中的ServiceAccount设置annotation，键值为`ack.alibabacloud.com/role-arn`，对应值为上述RAM角色ARN。
- 在命名空间team-a和team-b中创建SecretStore实例，serviceAccountRef字段指定为命名空间中的ServiceAccount实例。
- 分别在命名空间team-a和team-b中创建externalsecret实例，并使用各自的secretstore获取权限范围内的KMS凭据，验证是否在各自命名空间中成功创建secret实例。

```yaml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: team-a
  namespace: team-a
spec:
  provider: kms
  data:
    - key: team-a
      name: test
      versionStage: ACSCurrent
      secretStoreRef:
        name: team-a
        namespace: team-a
---
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: team-b
  namespace: team-b
spec:
  provider: kms
  data:
    - key: team-b
      name: test
      versionStage: ACSCurrent
      secretStoreRef:
        name: team-b
        namespace: team-b
```



- 尝试使用team-b的secretstore访问team-a中的KMS凭据

  ```yaml
  
  apiVersion: 'alibabacloud.com/v1alpha1'
  kind: ExternalSecret
  metadata:
    name: team-b-bad
    namespace: team-b
  spec:
    provider: kms
    data:
      - key: team-a  #team-a的凭据
        name: test
        versionStage: ACSCurrent
        secretStoreRef:
          name: team-b
          namespace: team-b
  ```

- 执行`kubectl get externalsecrets -nteam-b team-b-bad -oyaml`查看externalsecrets team-b-bad实例的status字段中是否有403的报错信息：

  ```yaml
  client namespace/team-b/team-b get data error SDKError:
           StatusCode: 403
           Code: Forbidden.NoPermission
           Message: code: 403, This operation for acs:kms:cn-hangzhou:xxxxx:secret/team-a is forbidden by permission system. request id: ...
  ```

  









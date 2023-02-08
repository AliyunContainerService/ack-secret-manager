# ACK Secret Manager

English | [简体中文](./README-zh_CN.md)

ACK Secret Manager allows you to use external secret management systems (*e.g.*, [Alibaba Cloud Secrets Manager](https://www.alibabacloud.com/help/doc-detail/152003.htm?spm=a2c63.p38356.b99.35.21571e37lyf0t2) to securely add secrets in Kubernetes.

ACK Secret Manager provide the same use experience as [kubernetes-external-secrets](https://github.com/godaddy/kubernetes-external-secrets) which provide the same ease of use as native Secret objects and provide access to secrets stored externally. In ACK Secret Manager，it also adding an ExternalSecret object to the Kubernetes API that allows developers to inject the external secret from [Alibaba Cloud Secrets Manager](https://help.aliyun.com/document_detail/152001.html?spm=a2c4g.11174283.6.578.4e0f7c681F2t9V) into a Pod using a declarative API similar to the native Secret one.

## Installing the Chart

**1.** You are the authorized user of [Alibaba Cloud Secrets Manager](https://www.alibabacloud.com/help/doc-detail/152003.htm?spm=a2c63.p38356.b99.35.21571e37lyf0t2)

**2.** Grant ack-secret-manager the permission of get credentials in KMS secret manager, here are two ways:

a.  Attach KMS RAM policy on target worker role
* access the target cluster's detail page in [Container Service console](https://cs.console.aliyun.com/)
* click the target ram role named **KubernetesWorkerRole-xxxxxxxxxxxxxxx** and access into RAM Roles page
* add kms RAM policy below into the policy bind to the worker role.
  ```
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

b.  For ACK/ASK 1.22 clusters, fine-grained ram role for service account could be implemented via [RRSA method](https://help.aliyun.com/document_detail/356611.html) (**Note: This method only supports 1.22 version of ACK Standard and Pro clusters**)
* [Enable RRSA function](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-ywl-59g-j8h)
* [Use RRSA function](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-rmr-eeh-878) : including creating the corresponding RAM role for the specified serviceaccount, the trust policy for the role, and binding the permission to the role.

**3.** Log on to the Container Service console.

* In the left-side navigation page, choose **Marketplace > App Catalog**. Select the **ack-secret-manager** application, click it and access into application page, then modify the chart configuration values in **Parameters** tab, please find parameters description below
* Select the target cluster and click **Create** button to add the component.
* Input the customized parameters in the parameter configuration page, including `rrsa.enable` in values.yaml and `ALICLOUD_ROLE_ARN` and `ALICLOUD_OIDC_PROVIDER_ARN` parameters, see the configuration notes below for parameter descriptions.
* Click the **OK** button to complete the installation.

## Uninstalling the Chart

1. Log on to the [Container Service console](https://cs.console.aliyun.com/) .
2. In the left-side      navigation pane, choose **Applications      > Publish** and click the **Releases**      tab. On the ** Releases** tab page,      locate the row that named **ack-secret-manager**, and click **Delete**.


## Configuration

The following table lists the configurable parameters of the `ack-secret-manager` chart and their default values.

| Parameter                            | Description                                                  | Default                  |
| ------------------------------------ | ------------------------------------------------------------ |--------------------------|
| `env.WATCH_NAMESPACE`                     | Set the namespaces operator watch（empty value means all-namespaces）                    |                          |
| `envVarsFromSecret.ACCESS_KEY_ID`     | Set the ACCESS_KEY_ID variable to specify the credential RAM AK for building SDK client, which needs to be defined in the secret named **alibaba-credentials** |                          |
| `envVarsFromSecret.SECRET_ACCESS_KEY`     | Set the SECRET_ACCESS_KEY variable to specify the credential RAM SK for building SDK client, which needs to be defined in the secret named **alibaba-credentials** |                          |
| `envVarsFromSecret.ALICLOUD_ROLE_ARN`     | Set the ALICLOUD_ROLE_ARN variable to specify the RAM role ARN for building SDK client, which needs to be defined in the secret named **alibaba-credentials** |                          |
| `envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME`     | Set the ALICLOUD_ROLE_SESSION_NAME variable to specify the RAM role session name for building SDK client, which needs to be defined in the secret named **alibaba-credentials** |                          |
| `envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION`     | Set the ALICLOUD_ROLE_SESSION_NAME variable to specify the RAM role session expiration for building SDK client, which needs to be defined in the secret named **alibaba-credentials** |                          |
| `envVarsFromSecret. ALICLOUD_OIDC_PROVIDER_ARN`        | Set the ALICLOUD_OIDC_PROVIDER_ARN variable to specify the RAM OIDC  provider arn for building SDK client, which needs to be defined in the secret named **alibaba-credentials** |                          |
| `envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE` | Set the ALICLOUD_OIDC_TOKEN_FILE variable to specify the serviceaccount OIDC token file path for building SDK client, which needs to be defined in the secret named **alibaba-credentials** |                          |
| `rrsa.enable` | Enable RRSA feature, default is false，when enalbe, you need to configure the parametes of  `ALICLOUD_ROLE_ARN` and `ALICLOUD_OIDC_PROVIDER_ARN`  in `envVarsFromSecret` | false                    |
| `command.backend`                           | Set the secret management backend, only alicloud-kms supported                              | `alicloud-kms`           |
| `command.reconcilePeriod`                        | How often the controller will re-queue externalsecret events           | `5s`                     |
| `command.reconcileCount`           | Specify the max concurrency reconcile work at the same time  | `1`                      |
| `command.tokenRotationPeriod`   | Polling interval to check kms client sts token expiration time.           | `120s`                   |
| `command.region `                          | The region id where you want to pull the secret from             |                          |
| `command.disablePolling `     | Disable auto polling external secret from kms.     | false                    |
| `command.pollingInterval `     | How often the controller will sync existing secret from kms.     | `120s`                   |
| `image.repository`                   | ack-secret-manager Image name                       | `acs/ack-secret-manager` |
| `image.tag`                          | ack-secret-manager Image tag | `v0.4.0`                 |
| `image.pullPolicy`                   | Image pull policy                                            | `Always`                 |
| `nameOverride`                   | Override the name of app                                            | `nil`                    |
| `fullnameOverride`                   | Override the full name of app                                            | `nil`                    |
| `rbac.create`                        | Create & use RBAC resources                                  | `true`                   |
| `securityContext.fsGroup`            | Security context for the container                           | `{}`                     |
| `serviceAccount.create`              | Whether a new service account name should be created.        | `true`                   |
| `serviceAccount.name`                | Service account to be used.                                  | automatically generated  |
| `serviceAccount.annotations`         | Annotations to be added to service account                   | `nil`                    |
| `podAnnotations`                     | Annotations to be added to pods                              | `{}`                     |
| `podLabels`                          | Additional labels to be added to pods                        | `{}`                     |
| `replicaCount`                       | Number of replicas                                           | `1`                      |
| `nodeSelector`                       | node labels for pod assignment                               | `{}`                     |
| `tolerations`                        | List of node taints to tolerate (requires Kubernetes >= 1.6) | `[]`                     |
| `affinity`                           | Affinity for pod assignment                                  | `{}`                     |
| `resources`                          | Pod resource requests & limits                               | `{}`                     |


> **Tip**: You can find the ack-secret-manager release in ACK  edit the param at the **Parameters** tab in    or use the default [values.yaml](https://github.com/AliyunContainerService/ack-secret-manager/blob/master/charts/ack-secret-manager/values.yaml)

## Add a secret

Add your secret data to your Secret Manager

```
aliyun kms CreateSecret --SecretName test --SecretData 1234 --VersionId v1

```

and then create a `hello-service-external-secret.yml` file:

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

Save the file and run:

```sh
kubectl apply -f hello-service-external-secret.yml
```

Wait a few minutes and verify that the associated `Secret` has been created:

```sh
kubectl get secret hello-service -oyaml
```

The `Secret` created by the controller should look like:

```yml
apiVersion: v1
kind: Secret
metadata:
  name: hello-service
type: Opaque
data:
  password: MTIzNA==
```
If the flag `disablePolling` is not set to `true`, the controller would auto polling the secret from KMS backend with the interval set in `pollingInterval`

#### jmes support

To set the specific key-value pairs extract from a JSON-formatted secret. You can use `jmesPath` field to mount key-value pairs from a properly formatted secret value as individual secrets. For example: Consider you have a secret "testJson" in KMS Secrets Manager with JSON content as follows:

```
{
	"username": "testuser",
	"password": "testpassword"
}
```

To fetch the username and password key pairs of this secret as individual secrets, use the jmesPath field as follows:

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

- path: This required field is the [JMES path](https://jmespath.org/specification.html) to use for retrieval
- objectAlias: This required field specifies the key name of the extracted value in the synced k8s secrets

The `Secret` created by the controller should look like:

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

## Release Note
| Version | Date       | Changes                                                                                       |
|---------|------------|-----------------------------------------------------------------------------------------------|
| `0.4.0` | 2022/12/22 | Support sync specific key-value pairs extract from a JSON-formatted secret based on JMES path |
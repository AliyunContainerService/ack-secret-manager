# ACK Secret Manager

[Ack Secret Manager](https://github.com/acs/ack-secret-manager) allows you to use external secret management systems (*e.g.*, [Alibaba Cloud Secrets Manager](https://help.aliyun.com/document_detail/152001.html?spm=a2c4g.11174283.6.578.4e0f7c681F2t9V) to securely add secrets in Kubernetes. 

ACK Secret Manager provide the same use experience as [kubernetes-external-secrets](https://github.com/godaddy/kubernetes-external-secrets) which provide the same ease of use as native Secret objects and provide access to secrets stored externally. In ACK Secret Manager，it also adding an ExternalSecret object to the Kubernetes API that allows developers to inject the external secret from [Alibaba Cloud Secrets Manager](https://help.aliyun.com/document_detail/152001.html?spm=a2c4g.11174283.6.578.4e0f7c681F2t9V) into a Pod using a declarative API similar to the native Secret one.


## Prerequisites

* Kubernetes 1.12+

## Installing the Chart

**1.** You are the authorized user of [Alibaba Cloud Secrets Manager](https://www.alibabacloud.com/help/doc-detail/152001.htm?spm=a2c63.p38356.b99.33.120e4107vpBRBO)
 
**2.** Attach KMS RAM policy on target worker role

 - access the target cluster's detail page in [Container Service console](https://cs.console.aliyun.com/)
 - click the target ram role named **KubernetesWorkerRole-xxxxxxxxxxxxxxx** and access into RAM Roles page
 - add kms RAM policy below into the policy bind to the worker role.
 
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

**3.** * Log on to the      Container Service console.

* In the left-side navigation page, choose **Marketplace > App Catalog**. Select the **ack-secret-manager** application, click it and access into application page, then choose **Create** to add the component.

## Uninstalling the Chart

1. Log on to the [Container Service console](https://cs.console.aliyun.com/).
2. In the left-side      navigation pane, choose **Applications      > Publish** and click the **Releases**      tab. On the ** Releases** tab page,      locate the row that named **ack-secret-manager**, and click **Delete**.


## Configuration

The following table lists the configurable parameters of the `ack-secret-manager` chart and their default values.

| Parameter                            | Description                                                  | Default                                                 |
| ------------------------------------ | ------------------------------------------------------------ | ------------------------------------------------------- |
| `env.WATCH_NAMESPACE`                     | Set the namespaces operator watch（empty value means all-namespaces）                    |                                           |
| `envVarsFromSecret.ACCESS_KEY_ID`     | Set ACCESS_KEY_ID (from secret alibaba-credentials) in Deployment Pod      |                                                         |
| `envVarsFromSecret.SECRET_ACCESS_KEY`     | Set SECRET_ACCESS_KEY (from secret alibaba-credentials) in Deployment Pod      |                                                         |
| `envVarsFromSecret.ALICLOUD_ROLE_ARN`     | Set ALICLOUD_ROLE_ARN (from secret alibaba-credentials) in Deployment Pod      |                                                         |
| `envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME`     | Set ALICLOUD_ROLE_SESSION_NAME (from secret alibaba-credentials) in Deployment Pod      |                                                         |
| `envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION`     | Set ALICLOUD_ROLE_SESSION_EXPIRATION (from secret alibaba-credentials) in Deployment Pod      |                                                         |
| `command.backend`                           | Set the secret management backend, only alicloud-kms supported                              | `alicloud-kms`                                                  |
| `command.reconcilePeriod`                        | How often the controller will re-queue externalsecret events           | `5s`                                                  |
| `command.reconcileCount`           | Specify the max concurrency reconcile work at the same time  | `1`          |
| `command.tokenRotationPeriod`   | Polling interval to check token expiration time.           | `120s`                                                 |
| `command.region `                          | Disables backend polling and only updates secrets when ExternalSecret is modified, setting this to any value will disable polling               | `cn-hangzhou`                                  |
| `command.enableLeaderElection `     | Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.     |   true                                                      |
| `image.repository`                   | ack-secret-manager Image name                       | `acs/ack-secret-manager`                   |
| `image.tag`                          | ack-secret-manager Image tag | `v0.0.1`                                                 |
| `image.pullPolicy`                   | Image pull policy                                            | `Always`                                          |
| `nameOverride`                   | Override the name of app                                            | `nil`                                          |
| `fullnameOverride`                   | Override the full name of app                                            | `nil`                                          |
| `rbac.create`                        | Create & use RBAC resources                                  | `true`                                                  |
| `securityContext.fsGroup`            | Security context for the container                           | `{}`                                                    |
| `serviceAccount.create`              | Whether a new service account name should be created.        | `true`                                                  |
| `serviceAccount.name`                | Service account to be used.                                  | automatically generated                                 |
| `serviceAccount.annotations`         | Annotations to be added to service account                   | `nil`                                                   |
| `podAnnotations`                     | Annotations to be added to pods                              | `{}`                                                    |
| `podLabels`                          | Additional labels to be added to pods                        | `{}`                                                    |
| `replicaCount`                       | Number of replicas                                           | `1`                                                     |
| `nodeSelector`                       | node labels for pod assignment                               | `{}`                                                    |
| `tolerations`                        | List of node taints to tolerate (requires Kubernetes >= 1.6) | `[]`                                                    |
| `affinity`                           | Affinity for pod assignment                                  | `{}`                                                    |
| `resources`                          | Pod resource requests & limits                               | `{}`                                                    |


> **Tip**: You can find the ack-secret-manager release in ACK  edit the param at the **Parameters** tab in    or use the default [values.yaml](https://github.com/AliyunContainerService/ack-secret-manager/blob/master/charts/ack-secret-manager/values.yaml)

## Add a secret

Add your secret data to your Secret Manager 

```
aliyun kms CreateSecret --SecretName test --SecretData 1234 --VersionId v1

```

> **Tip**: In current Secret Manager still in public preview status, it will open to use soon, thanks for your patient.

and then create a `hello-service-external-secret.yml` file:

```yml
apiVersion: 'alibabacloud.com/v1alpha1'
kind: ExternalSecret
metadata:
  name: hello-service
spec:
  backendType: alicloud-kms
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

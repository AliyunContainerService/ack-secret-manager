# ACK Secret Manager

English | [简体中文](./README-zh_CN.md)

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager) can help you import key credentials stored in [Alibaba Cloud KMS  Secrets Manager](https://www.alibabacloud.com/help/en/key-management-service) or [Alibaba Cloud OOS Encryption Parameter ](https://www.alibabacloud.com/help/en/oos/getting-started/manage-encryption-parameters)into the cluster in the form of Kubernetes native Secret objects and achieve automatic synchronization of key data, you can introduce the ciphertext stored in the Secrets Manager or Encryption Parameter into the application in the form of mounting Secret in the application Pod, to avoid the spread of sensitive data in the application development and construction process and leaks.

## Install

1. Make sure that the credentials used by the ack-secret-manager has sufficient permissions to access the Alibaba Cloud product service. You can use the following two configuration methods, and we recommend you to use the second **RRSA** method to achieve authorization in the Pod level.

   - Add permissions to the WorkerRole corresponding to the cluster
     - Log in to the Container Service console
     - Select the cluster to enter the cluster details page
     - Navigate to the **Cluster Resources** page in the cluster information. Once there, click on the Worker RAM role with the corresponding name **KubernetesWorkerRole-xxxxxxxxxxxxxxx**. This will automatically take you to the console page associated with the RAM role.
     - Add kms RAM policy below into the policy bind to the worker role(Only authorize the RAM policy needed for synchronization services, ensuring the principle of minimum permissions.)
       ```json
       {
           "Action": [
             // Permission required to access Alibaba Cloud KMS Secrets Manager
             "kms:GetSecretValue",
             "kms:Decrypt", 

             // Permission required to access Alibaba Cloud OOS Secret Parameters
             "oos:GetSecretParameter",
             "kms:GetSecretValue"
           ],
           "Resource": [
               "*"
           ],
           "Effect": "Allow"
       }
       ```
   - Implement Pod dimension authorization through [RRSA method](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services)
     * [Enable RRSA functionality](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-ywl-59g-j8h)
     * [Use RRSA function](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-rmr-eeh-878): Create the corresponding RAM role for the specified serviceaccount, set the trust policy for the RAM role, and authorize the RAM role
2. Log in to the Container Service console

   * Select **Marketplace** -> **Marketplace** in the left navigation bar, enter **ack-secret-manager** in the search bar, and select to enter the application page;
   * Select the target cluster, namespace, and release name to be installed;
   * Configure custom parameters on the parameter configuration page, including `rrsa.enable` in values.yaml and related parameters in `envVarsFromSecret`. For parameter descriptions, see the **configuration instructions** below;
   * Click the **OK** button to complete the installation.

## Upgrade

1. Log in to the Container Service console;
2. Select the target cluster and click to enter the cluster details page;
3. Select **Applications** -> **Helm** in the navigation bar on the left, find the **Update** button corresponding to ack-secret-manager, modify the configuration and click the **OK** button to complete the installation.

## Uninstall

1. Log in to the Container Service console;
2. Select the target cluster and click to enter the cluster details page;
3. Select **Applications** -> **Helm** in the navigation bar on the left, find the **Delete** button corresponding to ack-secret-manager, and click the **Delete** button in the operation bar to delete it.

## Configuration instructions

| **parameter**                                 | **introduction**                                                                                                                                                                                            | **default value** |
| --------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------- |
| env.WATCH_NAMESPACE                                 | Specify the namespace of the component watch (the default empty value represents all namespaces of watch)                                                                                                         |                         |
| envVarsFromSecret.ACCESS_KEY_ID                     | You can build the SDK client by specifying the credential AK by setting the ACCESS_KEY_ID variable, which needs to be defined in the secret instance named alibaba-credentials                                    |                         |
| envVarsFromSecret.SECRET_ACCESS_KEY                 | You can build the SDK client by specifying the credential SK by setting the SECRET_ACCESS_KEY variable, which needs to be defined in the secret instance named alibaba-credentials                                |                         |
| envVarsFromSecret.ALICLOUD_ROLE_ARN                 | You can specify the RAM role ARN used to build the SDK client by setting the ALICLOUD_ROLE_ARN variable, which needs to be defined in the secret instance named alibaba-credentials                               |                         |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME        | You can specify the RAM role session name by setting the ALICLOUD_ROLE_SESSION_NAME variable to build the SDK client, which needs to be defined in the secret instance named alibaba-credentials                  |                         |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION  | You can specify the RAM role session expiration length by setting the ALICLOUD_ROLE_SESSION_EXPIRATION variable to build the SDK client. It needs to be defined in the secret instance named alibaba-credentials. |                         |
| envVarsFromSecret. ALICLOUD_OIDC_PROVIDER_ARN       | You can specify the ARN of the RAM OIDC provider by setting the ALICLOUD_OIDC_PROVIDER_ARN variable to build the SDK client, which needs to be defined in the secret instance named alibaba-credentials           |                         |
| envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE          | You can specify the oidc token file path in the pod by setting the ALICLOUD_OIDC_TOKEN_FILE variable to build the SDK client. It needs to be defined in the secret instance named alibaba-credentials.            |                         |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_ARN          | You can specify the RAM Role Arn of another account by setting the ALICLOUD_REMOTE_ROLE_ARN variable for role playing when pulling credential data across accounts.                                               |                         |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_SESSION_NAME | You can specify the RAM Role Session Name by setting the ALICLOUD_REMOTE_ROLE_SESSION_NAME variable to perform role play when pulling credential data across accounts.                                            |                         |
| rrsa.enable                                         | Whether to enable the RRSA feature, the default is false. After enabling, you need to configure the ALICLOUD_ROLE_ARN and ALICLOUD_OIDC_PROVIDER_ARN parameters in envVarsFromSecret.                             | false                   |
| command.reconcilePeriod                             | The interval for the controller to re-coordinate the externalSecret instance, the default is 5 seconds                                                                                                            | 5s                      |
| command.reconcileCount                              | Specify the number of workers to concurrently coordinate the externalSecret instance. The default is 1                                                                                                            | 1                       |
| command.tokenRotationPeriod                         | Polling time to check whether the client access STS token has expired                                                                                                                                             | 120s                    |
| command.region                                      | Pull secret credentials from the specified region                                                                                                                                                                 |                         |
| command.disablePolling                              | Turn off automatic synchronization of pulling the latest credential content from the backend, default false                                                                                                       | false                   |
| command.pollingInterval                             | The interval for synchronizing existing secret instances from the backend                                                                                                                                         | 120s                    |
| command.maxConcurrentSecretPulls                    | Deprecated                                                                                                                                                                                                        | -                       |
| command.maxConcurrentKmsSecretPulls                 | Maximum concurrent synchronization per second of kms secrets                                                                                                                                                      | 10                      |
| command.maxConcurrentOosSecretPulls                 | Maximum concurrent synchronization per second of oos secrets                                                                                                                                                      | 10                      |
| image.repository                                    | Specified ack-secret-manager mirror warehouse name                                                                                                                                                                | acs/ack-secret-manager  |
| image.tag                                           | Specified ack-secret-manager image tag                                                                                                                                                                            | v0.5.0                  |
| image.pullPolicy                                    | Image pull strategy, default is Always                                                                                                                                                                            | Always                  |
| nameOverride                                        | Override app name                                                                                                                                                                                                 | nil                     |
| fullnameOverride                                    | Override application full name                                                                                                                                                                                    | nil                     |
| rbac.create                                         | Whether to create and use RBAC resources, the default is true                                                                                                                                                     | true                    |
| securityContext.fsGroup                             | Specify the security context configuration of the application                                                                                                                                                     | {}                      |
| serviceAccount.create                               | Whether to create serviceaccount                                                                                                                                                                                  | true                    |
| serviceAccount.name                                 | Specify the name of the created serviceaccount                                                                                                                                                                    | Automatic generated     |
| serviceAccount.annotations                          | Specify adding the serviceaccount annotation tag                                                                                                                                                                  | nil                     |
| podAnnotations                                      | Specify the annotation label added to the pod                                                                                                                                                                     | {}                      |
| podLabels                                           | Specify the Label added to the pod                                                                                                                                                                                | {}                      |
| replicaCount                                        | Number of controller copies                                                                                                                                                                                       | 1                       |
| nodeSelector                                        | The specified nodeSelector tag                                                                                                                                                                                    | {}                      |
| tolerations                                         | Specified taint tolerance configuration                                                                                                                                                                           | []                      |
| affinity                                            | Specified Pod affinity configuration                                                                                                                                                                              | {}                      |
| resources                                           | Specified Pod requests and limits configuration                                                                                                                                                                   | {}                      |

## Instructions for use

The following will add a test credential in Alibaba Cloud KMS Secrets Manager for synchronize the credentials, and demonstrate some extended functions.

| Configuration                             | Alibaba KMS Credential                       | Alibaba Cloud OOS  Secret Parameter               |
| ----------------------------------------------- | -------------------------------------------- | ------------------------------------------------- |
| **SecretStore keyword authentication method** | SecretStore.Spec.KMS.KMSAuth                 | SecretStore.Spec.OOS.OOSAuth                      |
| **ExternalSecret.spec.provider**        | kms                                          | oos                                               |
| **ExternalSecret.spec.data.key**        | `<KMS  credential name>`(as follows test1) | `<OOS encryption parameter name>`(as follows test2) |

ack-secret-manager involves two CRDs. SecretStore is used to store access credentials (such as RRSA configuration, ClientKey, AK configuration, etc.), and ExternalSecret is used to store basic credential information that needs to be synchronized (such as credential name, version, etc.) and specify the SecretStore. It ensures the separation of permissions and data and enhances the flexibility of use. See below for details **CRD configuration introduction**

1. Create ciphertexts
   Currently supports synchronization of KMS credentials and OOS encryption parameters. The following are references to how to create two ciphertexts.

    - Add the following credential in the KMS Secrets Manager. For detailed procedures, please refer to [Manage Common Credentials](https://www.alibabacloud.com/help/en/key-management-service/latest/manage-generic-secrets)

      ```txt
      SecretName: test1
      SecretData: {"name":"tom","age":"14","friends":[{"name":"lili"},{"name":"edf"}]} 
      VersionId: v1
      ```
    - Add the following parameter to the OOS Encryption Parameters. Please refer to the detailed process[CreateSecretParameter](https://www.alibabacloud.com/help/en/oos/developer-reference/api-oos-2019-06-01-createsecretparameter)

      ```txt
      Name: test2
      Value: {"name":"tom","age":"14","friends":[{"name":"lili"},{"name":"edf"}]} 
      ```
2. Create SecretStore & ExternalSecret

   Prerequisite: Enable RRSA for the cluster and properly configure the relevant RAM Role permissions

   This example includes descriptions of some configuration fields in the template, which will not be repeated in the following example.

    - Create a test instance named `scdemo` of SecretStore, and saved as a file `hello-service-secret-store.yml`. The sample template is as follows, with some fields need to be replaced.
      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: SecretStore
      metadata:
        name: scdemo
      spec:
        KMS: # When synchronizing KMS Secret Manager credentials, specify as KMS, and OOS when synchronizing OOS encryption parameters
          KMSAuth: # KMSAuth or OOSAuth
            oidcProviderARN: "acs:ram::{accountID}:oidc-provider/ack-rrsa-{clusterID}"
            ramRoleARN: "acs:ram::{accountID}:role/{roleName}"
      ```
    - Create a test instance named `esdemo` of ExternalSecret, and saved as a file `hello-service-external-secret.yml`. The test template is as follows:
      ```yaml
      apiVersion: 'alibabacloud.com/v1alpha1'
      kind: ExternalSecret
      metadata:
        name: esdemo
      spec:
        provider: kms  # Currently supports kms and oos, the default value is kms when this field is not specified
        data:
          - key: test1 # Key name to be synchronized, currently supports kms credentials and oos encryption parameters, this example is kms credential name
            name: test1 # the secret name in target Kubernetes cluster
            versionId: v1 # kms credential version, this field does not need to be configured when the provider is not kms
            secretStoreRef: # no need to be specified when using worker node RAM Role authentication
              name: scdemo
              namespace: default
      ```
    - Execute the command to create an secretstore test instance(This instance does not need to be created when using Worker Role authentication):
      ```sh
      kubectl apply -f hello-service-secret-store.yml
      ```
    - Execute the command to create an externalsecret test instance:
      ```sh
      kubectl apply -f hello-service-external-secret.yml
      ```
    - Check whether the target secret is created successfully:
      ```sh
      kubectl get secret esdemo -oyaml
      ```
    - If the creation is successful, view the secret content as follows:
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
    - Without turning off the automatic synchronization configuration, you can modify the key content in the KMS Secrets Manager and wait for a while to check whether the target secret has been synchronized.
3. JSON-formatted support

   **data**

   - If you want to parse a JSON-formatted secret and synchronize the key-value pairs specified in it to the k8s secret, you can use the `jmesPath` field. The following is an example using the `jmesPath` field, which we deploy in the cluster
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
           jmesPath: # Parse some fields in json string
             - path: "name"
               objectAlias: "name"
             - path: "friends[0].name"
               objectAlias: "friendname"
     ```
   - After deployment, check whether the secret is created successfully
     ```sh
     kubectl get secret es-json-demo -oyaml
     ```
   - If the synchronization is successful, you will see the following results:
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
   - When you use the `jmesPath` field, you must specify the following two subfields:
     - `path`: Required, parses the specified field in json based on the [JMES path](https://jmespath.org/specification.html) specification
     - `objectAlias`: Required, used to specify the parsed field to be synchronized to the key name in the k8s secret

   **dataProcess**

    - If you want to parse JSON credentials and store them in a secret but don't know the specific structure of the credentials, you can use the self-extraction feature, i.e.  `dataProcess.Extract` field. And you can replace the parsed field key with a rule, i.e., `dataProcss.replaceRule` field, to prevent the secret data key from being irregular, resulting in failure to create a secret, the following is a sample ExternalSecret:
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
                name: dkms-client
                namespace: default
            replaceRule:
              - source: "^n.*e$" # To replace keys that start with 'n' and end with 'e' with 'alibabacloud'
                target: "alibabacloud"
              - source: "^f.*s$"
                target: "ack"
      ```
    - Once the synchronization is successful, you will be able to see the following results. The JSON credentials are parsed into three parts, and their respective keys are replaced according to the replaceRule rules.
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
4. Currently supports cross-account synchronization of credentials. Just configure `remoteRamRoleArn` and `remoteRamRoleSessionName` in SecretStore.Spec.KMS.KMSAuth. The following is a sample SecretStore

   ```yaml
   apiVersion: 'alibabacloud.com/v1alpha1'
   kind: SecretStore
   metadata:
     name: scdemo
   spec:
     KMS:
       KMSAuth:
         oidcProviderARN: "acs:ram::{accountID}:oidc-provider/ack-rrsa-{clusterID}" #Replace the accountID and clusterID fields for the specified RAM OIDC provdider instance
         ramRoleARN: "acs:ram::{accountID}:role/{roleName}"  
         remoteRamRoleArn: "acs:ram::{accountID}:role/{roleName}"  #Replace with the ARN of the specified cross-account RAM role
         remoteRamRoleSessionName: ""
   ```

## CRD configuration introduction

### ExternalSecret

**spec**

| Parameter   | Description                                                  | Required |
| ----------- | ------------------------------------------------------------ | -------- |
| provider    | The target cloud products for syncing credentials, such as KMS, OOS | no       |
| data        | Data source (identifier for the target data)                 | no       |
| dataProcess | Data source requiring special processing (identifier for the target data) | no       |
| type        | Kubernetes Secret types (Opaque, etc.)                       | no       |

**data**

| Parameter      | Description                                                  | Required |
| -------------- | ------------------------------------------------------------ | -------- |
| key            | The unique identifier for the target credential, such as the key for KMS credentials | yes      |
| name           | The corresponding key for the credentials in the secret data of the cluster | no       |
| versionStage   | The version stage of the target credential                   | no       |
| versionId      | The version Id of the target credential, this field is not required when provider is oos | no       |
| jmesPath       | If the target credential is in JSON format, you can specify to retrieve the value corresponding to a specific key in the JSON | no       |
| secretStoreRef | Information of the referenced SecretStore                    | no       |

**dataProcess（Data source requiring special processing.）**

| Parameter   | Description                                                  | Required |
| ----------- | ------------------------------------------------------------ | -------- |
| extract     | Parsing JSON for the target credential without requiring the user to specify the JSON key | no       |
| replaceRule | Replacing keys of the parsed secret based on specific rules to prevent illegal keys from being stored in the Kubernetes Secret | no       |

**replaceRule（The content replacement used for the Secret Key.）**

| Parameter | Description                                                  | Required |
| --------- | ------------------------------------------------------------ | -------- |
| target    | The string used for replacement                              | yes      |
| source    | The string that needs to be replaced, which can be a regular expression | yes      |

**jmesPath**

| Parameter   | Description                                                  | Required |
| ----------- | ------------------------------------------------------------ | -------- |
| path        | JMESPath expression that allows users to specify the JSON key | yes      |
| objectAlias | The data key corresponding to the Kubernetes Secret where the data will be stored | yes      |

**secretStoreRef**

| Parameter | Description                         | Required |
| --------- | ----------------------------------- | -------- |
| name      | The specified SecretStore name      | yes      |
| namespace | The specified SecretStore namespace | Yes      |

### SecretStore

**spec**

| Parameter | Description                                               | Required |
| --------- |-----------------------------------------------------------| -------- |
| KMS       | Get credentials from KMS Secret Manager                   | no       |
| OOS       | Get credentials from CloudOps Orchestration Service (OOS) | no       |

**KMS**

| parameter | Description                                | Required |
| --------- |--------------------------------------------| -------- |
| KMSAuth   | Credentials required to access KMS service | no       |

**OOS**

| Parameter | Description                                | Required |
| --------- |--------------------------------------------| -------- |
| OOSAuth   | Credentials required to access OOS service | no       |

**KMSAuth**

| Parameter                | Description                                                  | Required |
| ------------------------ | ------------------------------------------------------------ | -------- |
| accessKey                | Refer to the following Alibaba Cloud AccessKey authentication configuration | no       |
| accessKeySecret          | Refer to the following Alibaba Cloud AccessKey authentication configuration | no       |
| ramRoleARN               | Ram role arn                                                 | no       |
| ramRoleSessionName       | Role session name                                            | no       |
| oidcProviderARN          | OIDC provider arn                                            | no       |
| oidcTokenFilePath        | OIDC token file path                                         | no       |
| remoteRamRoleArn         | Cross-account ram role are                                   | no       |
| remoteRamRoleSessionName | Cross-account ram role session name                          | no       |

**OOSAuth**

| Parameter                | Description                                                  | Required |
| ------------------------ | ------------------------------------------------------------ | -------- |
| accessKey                | Refer to the following Alibaba Cloud AccessKey authentication configuration | no       |
| accessKeySecret          | Refer to the following Alibaba Cloud AccessKey authentication configuration | no       |
| ramRoleARN               | Ram role arn                                                 | no       |
| ramRoleSessionName       | Role session name                                            | no       |
| oidcProviderARN          | OIDC provider arn                                            | no       |
| oidcTokenFilePath        | OIDC token file path                                         | no       |
| remoteRamRoleArn         | Cross-account ram role are                                   | no       |
| remoteRamRoleSessionName | Cross-account ram role session name                          | no       |



**Alibaba Cloud AccessKey authentication configuration**

Alibaba Cloud AccessKey is the most important identity credentials for users to access cloud resources, it does not support direct configuration in the SecretStore field, it needs to reference in the Kubernetes Secrets instance, and the related configuration fields are described as follows：

> The leakage of Alibaba Cloud AccessKey can lead to serious security risks. It is recommended that you use RRSA or other Alibaba Cloud authentication methods.

| parameter | Description                  | required |
| --------- | ---------------------------- | -------- |
| name      | Kubernetes Secrets name      | Yes      |
| namespace | Kubernetes Secrets namaspace | Yes      |
| key       | Kubernetes Secrets data key  | Yes      |

## Security

Please report vulnerabilities by email to **kubernetes-security@service.aliyun.com**. Also see our [SECURITY.md](./SECURITY.md) file for details.

## Release Note

| Version   | Date       | Changes                                                                                                                                                                                                                     |
| --------- | ---------- |-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `0.4.0` | 2022/12/22 | Support sync specific key-value pairs extract from a JSON-formatted secret based on JMES path                                                                                                                               |
| `0.5.0` | 2023/10/10 | 1.dedicated KMS credential synchronization<br />2.multiple Alibaba Cloud access credentials management,<br />3.self-resolving credentials and key rule replacement<br />4. Cross-account KMS credential synchronization.    |
| `0.5.1` | 2023/10/18 | Function and performance optimization                                                                                                                                                                                       |
| `0.5.2` | 2024/08/01 | Large-scale resource synchronization concurrency optimization                                                                                                                                                               |
| `0.5.3` | 2024/10/10 | Supports resource synchronization with second-level rate limiting, fixes for certain software package CVEs                                                                                                                  |
| `0.5.4` | 2024/10/31 | Supports OOS secret parameter synchronization                                                                                                                                                                               |
| `0.5.5` | 2024/11/22 | Supports synchronization of binary-type secrets                                                                                                                                                                             |


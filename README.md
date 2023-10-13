# ACK Secret Manager

English | [简体中文](./README-zh_CN.md)

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager) can help you import key credentials stored in [Alibaba Cloud KMS  Secrets Manager](https://www.alibabacloud.com/help/en/key-management-service) into the cluster in the form of Kubernetes native Secret objects and achieve automatic synchronization of key data, you can introduce the ciphertext stored in the Secrets Manager into the application in the form of mounting Secret in the application Pod to avoid the spread of sensitive data in the application development and construction process and leaks.



## Install

Make sure that the current account has sufficient permissions to access the Alibaba Cloud KMS Secrets Manager. ack-secret-manager supports two methods: shared KMS and dedicated KMS to synchronize the Secrets Manager's credentials.

1. There are two ways to set shared KMS access permissions:

   - Add permissions to the WorkerRole corresponding to the cluster

     - Log in to the Container Service console

     - Select the cluster to enter the cluster details page

     - Navigate to the **Cluster Resources** page in the cluster information. Once there, click on the Worker RAM role with the corresponding name **KubernetesWorkerRole-xxxxxxxxxxxxxxx**. This will automatically take you to the console page associated with the RAM role.

     - Add kms RAM policy below into the policy bind to the worker role

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

   - Implement Pod dimension authorization through [RRSA method](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services)

     * [Enable RRSA functionality](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-ywl-59g-j8h)

     * [Use RRSA function](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-rmr-eeh-878): Create the corresponding RAM role for the specified serviceaccount, set the trust policy for the RAM role, and authorize the RAM role

2. Set dedicated KMS access permissions. For details, see [Access a KMS instance by using an AAP](https://www.alibabacloud.com/help/en/key-management-service/latest/aap?spm=a2c63.l28256.0.0.2d881b76SOifvo)

3. Log in to the Container Service console

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

| **parameter**                                       | **introduction**                                             | **default value**      |
| --------------------------------------------------- | ------------------------------------------------------------ | ---------------------- |
| env.WATCH_NAMESPACE                                 | Specify the namespace of the component watch (the default empty value represents all namespaces of watch) |                        |
| envVarsFromSecret.ACCESS_KEY_ID                     | You can build the SDK client by specifying the credential AK by setting the ACCESS_KEY_ID variable, which needs to be defined in the secret instance named alibaba-credentials |                        |
| envVarsFromSecret.SECRET_ACCESS_KEY                 | You can build the SDK client by specifying the credential SK by setting the SECRET_ACCESS_KEY variable, which needs to be defined in the secret instance named alibaba-credentials |                        |
| envVarsFromSecret.ALICLOUD_ROLE_ARN                 | You can specify the RAM role ARN used to build the SDK client by setting the ALICLOUD_ROLE_ARN variable, which needs to be defined in the secret instance named alibaba-credentials |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME        | You can specify the RAM role session name by setting the ALICLOUD_ROLE_SESSION_NAME variable to build the SDK client, which needs to be defined in the secret instance named alibaba-credentials |                        |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION  | You can specify the RAM role session expiration length by setting the ALICLOUD_ROLE_SESSION_EXPIRATION variable to build the SDK client. It needs to be defined in the secret instance named alibaba-credentials. |                        |
| envVarsFromSecret. ALICLOUD_OIDC_PROVIDER_ARN       | You can specify the ARN of the RAM OIDC provider by setting the ALICLOUD_OIDC_PROVIDER_ARN variable to build the SDK client, which needs to be defined in the secret instance named alibaba-credentials |                        |
| envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE          | You can specify the oidc token file path in the pod by setting the ALICLOUD_OIDC_TOKEN_FILE variable to build the SDK client. It needs to be defined in the secret instance named alibaba-credentials. |                        |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_ARN          | You can specify the RAM Role Arn of another account by setting the ALICLOUD_REMOTE_ROLE_ARN variable for role playing when pulling credential data across accounts. |                        |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_SESSION_NAME | You can specify the RAM Role Session Name by setting the ALICLOUD_REMOTE_ROLE_SESSION_NAME variable to perform role play when pulling credential data across accounts. |                        |
| rrsa.enable                                         | Whether to enable the RRSA feature, the default is false. After enabling, you need to configure the ALICLOUD_ROLE_ARN and ALICLOUD_OIDC_PROVIDER_ARN parameters in envVarsFromSecret. | false                  |
| command.backend                                     | The docked external key management system backend currently only supports Alibaba Cloud Secrets Manager, configured as alicloud-kms | alicloud-kms           |
| command.reconcilePeriod                             | The interval for the controller to re-coordinate the externalSecret instance, the default is 5 seconds | 5s                     |
| command.reconcileCount                              | Specify the number of workers to concurrently coordinate the externalSecret instance. The default is 1 | 1                      |
| command.tokenRotationPeriod                         | Polling time to check whether the KMS client access STS token has expired | 120s                   |
| command.region                                      | Pull secret credentials from the specified region            |                        |
| command.disablePolling                              | Turn off automatic synchronization of pulling the latest credential content from the KMS backend, default false | false                  |
| command.pollingInterval                             | The interval for synchronizing existing secret instances from the KMS backend | 120s                   |
| image.repository                                    | Specified ack-secret-manager mirror warehouse name           | acs/ack-secret-manager |
| image.tag                                           | Specified ack-secret-manager image tag                       | v0.5.0                 |
| image.pullPolicy                                    | Image pull strategy, default is Always                       | Always                 |
| nameOverride                                        | Override app name                                            | nil                    |
| fullnameOverride                                    | Override application full name                               | nil                    |
| rbac.create                                         | Whether to create and use RBAC resources, the default is true | true                   |
| securityContext.fsGroup                             | Specify the security context configuration of the application | {}                     |
| serviceAccount.create                               | Whether to create serviceaccount                             | true                   |
| serviceAccount.name                                 | Specify the name of the created serviceaccount               | Automatic generated    |
| serviceAccount.annotations                          | Specify adding the serviceaccount annotation tag             | nil                    |
| podAnnotations                                      | Specify the annotation label added to the pod                | {}                     |
| podLabels                                           | Specify the Label added to the pod                           | {}                     |
| replicaCount                                        | Number of controller copies                                  | 1                      |
| nodeSelector                                        | The specified nodeSelector tag                               | {}                     |
| tolerations                                         | Specified taint tolerance configuration                      | []                     |
| affinity                                            | Specified Pod affinity configuration                         | {}                     |
| resources                                           | Specified Pod requests and limits configuration              | {}                     |



## Instructions for use

The following will add a test credential in Alibaba Cloud KMS Secrets Manager, synchronize the credentials through dedicated KMS and shared KMS, and demonstrate some extended functions.

ack-secret-manager involves two CRDs. SecretStore is used to store access credentials (such as RRSA configuration, ClientKey, AK configuration, etc.), and ExternalSecret is used to store basic credential information that needs to be synchronized (such as credential name, version, etc.) and specify the SecretStore. It ensures the separation of permissions and data and enhances the flexibility of use. See below for details **CRD configuration introduction**

1. Create credentials

   Add the following credentials in the KMS Secrets Manager. For detailed procedures, please refer to [Manage Common Credentials](https://www.alibabacloud.com/help/en/key-management-service/latest/manage-generic-secrets)

   ```txt
   SecretName: test1
   SecretData: {"name":"tom","age":"14","friends":[{"name":"lili"},{"name":"edf"}]} 
   VersionId: v1
   ```

2. Shared KMS

   Prerequisite: Enable RRSA for the cluster and correctly configure the relevant RAM Role permissions

   - Create a test instance of SecretStore. The test template is as follows. Some fields need to be replaced.

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

   - Create a test instance of ExternalSecret. The test template is as follows:

     ```yaml
     apiVersion: 'alibabacloud.com/v1alpha1'
     kind: ExternalSecret
     metadata:
       name: esdemo
     spec:
       data: #Data sources that require no special processing
         - key: 	test1 # kms credential name
           name: test1 # Fields stored in secret
           versionId: v1 # kms credential version
           secretStoreRef:
             name: scdemo
             namespace: default
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
       creationTimestamp: "2023-10-09T13:03:09Z"
       labels:
         lastUpdatedAt: 2023-10-09T13.03.09Z
       name: esdemo
       namespace: default
       resourceVersion: "7311947"
       uid: 163c6a33-0bee-40b6-8ffe-6897277036cd
     type: Opaque
     ```

   - Without turning off the automatic synchronization configuration, you can modify the key content in the KMS Secrets Manager and wait for a while to check whether the target secret has been synchronized.

   - If you want to parse a JSON-formatted secret and synchronize the key-value pairs specified in it to the k8s secret, you can use the `jmesPath` field. The following is an example using the `jmesPath` field, which we deploy in the cluster

     ```yaml
     apiVersion: 'alibabacloud.com/v1alpha1'
     kind: ExternalSecret
     metadata:
       name: es-json-demo
     spec:
       data: 
         - key: 	test1 
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
       creationTimestamp: "2023-10-09T13:11:05Z"
       labels:
         lastUpdatedAt: 2023-10-09T13.11.05Z
       name: es-json-demo
       namespace: default
       resourceVersion: "7313940"
       uid: 18d84558-f526-4ff5-ab9a-720ec1861c30
     type: Opaque
     ```

   - When you use the `jmesPath` field, you must specify the following two subfields:

     - `path`: Required, parses the specified field in json based on the [JMES path](https://jmespath.org/specification.html) specification

     - `objectAlias`: Required, used to specify the parsed field to be synchronized to the key name in the k8s secret

   - The shared KMS currently supports cross-account synchronization of credentials. Just configure `remoteRamRoleArn` and `remoteRamRoleSessionName` in `SecretStore.Spec.KMS.KMSAuth`. The following is a sample SecretStore

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
           remoteRamRoleSessionName: "
     ```

3. Dedicated KMS

   Prerequisite: An AAP (application access point) has been created in the KMS console, and a ClientKey has been created within the access point, correctly configured with permissions for the credential "test1".

   - Create a secret in the cluster containing the relevant fields such as ClientKeyContent, Password, etc.

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

   - Create a SecretStore instance

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

   - Create an ExternalSecret instance and select default/dkms-client as the SecretStoreRef.

     ```yaml
     apiVersion: 'alibabacloud.com/v1alpha1'
     kind: ExternalSecret
     metadata:
       name: es-dkms-demo
     spec:
       data: 
         - key: 	test1 
           name: dkms
           versionId: v1 
           secretStoreRef:
             name: dkms-client
             namespace: default
     ```

   - Once the synchronization is successful, you will be able to see the following results.

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

   - If you want to parse JSON credentials and store them in a secret but don't know the specific structure of the credentials, you can use the self-extraction feature, which is the `dataProcess.Extract` field. You can also perform rule-based replacements on the parsed field keys using the `dataProcess.replaceRule` field to prevent irregular secret data keys from causing issues when creating a secret. The following is an example of an ExternalSecret:

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
       creationTimestamp: "2023-10-09T14:07:35Z"
       labels:
         lastUpdatedAt: 2023-10-09T14.07.35Z
       name: extract-secret
       namespace: default
       resourceVersion: "7328187"
       uid: a3ab5278-02bd-4544-bae7-b502acccfe3c
     type: Opaque
     ```

## CRD configuration introduction

### ExternalSecret

**spec**

| parameter   | description                                                  | required |
| ----------- | ------------------------------------------------------------ | -------- |
| provider    | The target cloud products for syncing credentials, such as KMS | no       |
| data        | Data source (identifier for the target data)                 | no       |
| dataProcess | Data source requiring special processing (identifier for the target data) | no       |
| type        | Kubernetes Secret types (Opaque, etc.)                       | no       |

**data**

| parameter      | description                                                  | required |
| -------------- | ------------------------------------------------------------ | -------- |
| key            | The unique identifier for the target credential, such as the key for KMS credentials | yes      |
| name           | The corresponding key for the credentials in the secret data of the cluster | no       |
| versionStage   | The version stage of the target credential                   | no       |
| versionId      | The version Id of the target credential                      | no       |
| jmesPath       | If the target credential is in JSON format, you can specify to retrieve the value corresponding to a specific key in the JSON | no       |
| secretStoreRef | Information of the referenced SecretStore                    | no       |

**dataProcess（Data source requiring special processing.）**

| parameter   | description                                                  | required |
| ----------- | ------------------------------------------------------------ | -------- |
| extract     | Parsing JSON for the target credential without requiring the user to specify the JSON key | no       |
| replaceRule | Replacing keys of the parsed secret based on specific rules to prevent illegal keys from being stored in the Kubernetes Secret | no       |

**replaceRule（The content replacement used for the Secret Key.）**

| parameter | description                                                  | required |
| --------- | ------------------------------------------------------------ | -------- |
| target    | The string used for replacement                              | yes      |
| source    | The string that needs to be replaced, which can be a regular expression | yes      |

**jmesPath**

| parameter   | description                                                  | required |
| ----------- | ------------------------------------------------------------ | -------- |
| path        | JMESPath expression that allows users to specify the JSON key | yes      |
| objectAlias | The data key corresponding to the Kubernetes Secret where the data will be stored | yes      |

**secretStoreRef**

| parameter | description                         | required |
| --------- | ----------------------------------- | -------- |
| name      | The specified SecretStore name      | yes      |
| namespace | The specified SecretStore namespace | Yes      |

### SecretStore

**spec**

| parameter | description                                                  | required |
| --------- | ------------------------------------------------------------ | -------- |
| KMS       | Representing the target cloud product as KMS (Key Management Service) | no       |

**KMS**

| parameter        | description                                                  | required |
| ---------------- | ------------------------------------------------------------ | -------- |
| KMSAuth          | Credentials required to access KMS (Key Management Service) under a shared KMS | no       |
| dedicatedKMSAuth | Credentials required to access KMS (Key Management Service) under a dedicated KMS | no       |

**KMSAuth**

| parameter                | description                         | required |
| ------------------------ | ----------------------------------- | -------- |
| accessKey                | AccessKey                           | no       |
| accessKeySecret          | AccessKey Secret                    | no       |
| ramRoleARN               | Ram role arn                        | no       |
| ramRoleSessionName       | Role session name                   | no       |
| oidcProviderARN          | OIDC provider arn                   | no       |
| oidcTokenFilePath        |                                     | no       |
| remoteRamRoleArn         | Cross-account ram role are          | no       |
| remoteRamRoleSessionName | Cross-account ram role session name | no       |

**dedicatedKMSAuth**

| parameter        | description                              | required |
| ---------------- | ---------------------------------------- | -------- |
| protocol         | https                                    | yes      |
| endpoint         | Kms instance ID                          | yes      |
| ca               | User root CA certificate, base64 encoded | no       |
| ignoreSSL        | Whether to ignore ssl authentication     | no       |
| clientKeyContent | dedicated KMS client key file content    | yes      |
| password         | client key password                      | yes      |

**SecretRef（sensitive access credentials are stored in K8S secret）**

| parameter | description          | required |
| --------- | -------------------- | -------- |
| name      | k8s secret name      | yes      |
| namespace | k8s secret namaspace | yes      |
| key       | k8s secret key       | Yes      |

## Release Note

| Version | Date       | Changes                                                      |
| ------- | ---------- | ------------------------------------------------------------ |
| `0.4.0` | 2022/12/22 | Support sync specific key-value pairs extract from a JSON-formatted secret based on JMES path |
| `0.5.0` | 2023/10/10 | 1.dedicated KMS credential synchronization<br />2.multiple Alibaba Cloud access credentials management,<br />3.self-resolving credentials and key rule replacement<br />4.shared KMS cross-account credential synchronization. |
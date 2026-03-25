# ACK Secret Manager

English | [Simplified Chinese](./README-zh_CN.md)

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager)   enables you to securely import credentials stored in [Alibaba Cloud KMS Secrets Manager](https://www.alibabacloud.com/help/en/key-management-service) or [Alibaba Cloud OOS Encryption Parameter ](https://www.alibabacloud.com/help/en/oos/getting-started/manage-encryption-parameters)  into your Kubernetes cluster as native Secret objects. This ensures automatic synchronization of encrypted data while allowing applications to safely consume secrets by mounting them in Pod configurations. It helps mitigate sensitive data exposure risks in the development and deployment stages of the supply chain, ensuring compliance with security best practices.

## Install

1. Make sure that the credentials used by the ack-secret-manager has sufficient permissions to access the Alibaba Cloud product service. You can use the following two configuration methods, and we recommend you to use the second **RRSA** method to achieve authorization in the Pod level.

    - Implement pod-level authorization using [RRSA method](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services)

        * [Enable RRSA](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-ywl-59g-j8h)
        * [Use RRSA](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-rmr-eeh-878): Create a corresponding RAM role for the specified ServiceAccount, configure a trust policy for the RAM role, and grant necessary permissions to the RAM role.

        - By configuring `ramRoleARN` and `oidcProviderARN` in SecretStore, you can bind KMS secret retrieval permissions to the specific ServiceAccount used by the `ack-secret-manager` component **without using AccessKey (AK)**.
        - In multi-tenant scenarios, using the `serviceAccountRef` authentication method in the tenant namespace’s SecretStore enables **fine-grained, ServiceAccount-level control** over KMS secret retrieval permissions. For details on SecretStore configuration, see [Authentication using serviceAccountRef](#serviceAccountRef-auth).

    - Add permissions to the Worker RAM role associated with the cluster

        - Log in to the **Container Service for Kubernetes (ACK) console**.
        - Select your target cluster to open its **Cluster Details** page.
        - Go to the **Cluster Resources** section. Locate and click the Worker RAM role named **`KubernetesWorkerRole-xxxxxxxxxxxxxxx`**. This will redirect you to the corresponding RAM role page in the RAM console.
        - Attach the following KMS RAM policy to the role:

      > **Note**: Only grant the minimal permissions required for KMS Secret Manager, adhering to the principle of least privilege.

      ```json
      {
          "Action": [
            // Permission required to access Alibaba Cloud KMS Secrets Manager
            "kms:GetSecretValue",
            "kms:Decrypt", 
      
            // Permission required to access Alibaba Cloud OOS Encryption Parameter
            "oos:GetSecretParameter",
            "kms:GetSecretValue"
          ],
          "Resource": [
              "*"
          ],
          "Effect": "Allow"
      }
      ```

    - For all supported authentication configuration methods in ack-secret-manager, please refer to the [official documentation](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/security-and-compliance/use-ack-secret-manager-to-import-alibaba-cloud-kms-service-credentials).

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

| **parameter**                                       | **introduction**                                                                                                                                          | **default value** |
| --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------- |
| env.WATCH_NAMESPACE                                 | Specify the namespace of the component watch (the default empty value represents all namespaces of watch)                                                 |                   |
| envVarsFromSecret.ACCESS_KEY_ID                     | You can build the SDK client by specifying the credential AK by setting the ACCESS_KEY_ID variable, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.SECRET_ACCESS_KEY                 | You can build the SDK client by specifying the credential SK by setting the SECRET_ACCESS_KEY variable, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.ALICLOUD_ROLE_ARN                 | You can specify the RAM role ARN used to build the SDK client by setting the ALICLOUD_ROLE_ARN variable, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME        | You can specify the RAM role session name by setting the ALICLOUD_ROLE_SESSION_NAME variable to build the SDK client, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION  | You can specify the RAM role session expiration length by setting the ALICLOUD_ROLE_SESSION_EXPIRATION variable to build the SDK client. It needs to be defined in the secret instance named alibaba-credentials. | |
| envVarsFromSecret. ALICLOUD_OIDC_PROVIDER_ARN       | You can specify the ARN of the RAM OIDC provider by setting the ALICLOUD_OIDC_PROVIDER_ARN variable to build the SDK client, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE          | You can specify the oidc token file path in the pod by setting the ALICLOUD_OIDC_TOKEN_FILE variable to build the SDK client. It needs to be defined in the secret instance named alibaba-credentials. | |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_ARN          | You can specify the RAM Role Arn of another account by setting the ALICLOUD_REMOTE_ROLE_ARN variable for role playing when pulling credential data across accounts. |         |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_SESSION_NAME | You can specify the RAM Role Session Name by setting the ALICLOUD_REMOTE_ROLE_SESSION_NAME variable to perform role play when pulling credential data across accounts. |      |
| rrsa.enable                                         | Whether to enable the RRSA feature, the default is false. After enabling, you need to configure the ALICLOUD_ROLE_ARN and ALICLOUD_OIDC_PROVIDER_ARN parameters in envVarsFromSecret. | false |
| command.reconcilePeriod                             | The interval for the controller to re-coordinate the externalSecret instance, the default is 5 seconds                                                              | 5s      |
| command.reconcileCount                              | Specify the number of workers to concurrently coordinate the externalSecret instance. The default is 1                                                              | 1       |
| command.tokenRotationPeriod                         | Polling time to check whether the client access STS token has expired                                                                                               | 120s    |
| command.region                                      | Pull secret credentials from the specified region                                                                                                                   |         |
| command.kmsEndpoint                                 | Pull secret credentials from the specified endpoint                                                                                                                 |         |
| command.disablePolling                              | Turn off automatic synchronization of pulling the latest credential content from the backend, default false                                                         | false   |
| command.pollingInterval                             | The interval for synchronizing existing secret instances from the backend                                                                                           | 120s    |
| command.maxConcurrentSecretPulls                    | Deprecated                                                                                                                                                          | -       |
| command.maxConcurrentKmsSecretPulls                 | Maximum concurrent synchronization per second of kms secrets                                                                                                        | 10      |
| command.maxConcurrentOosSecretPulls                 | Maximum concurrent synchronization per second of oos secrets                                                                                                        | 10      |
| command.cleanupSecretOnFailure                      | Remove cluster secret on failed KMS secret sync                                                                                                                     | false   |
| command.processClusterSecretStore                   | Enable processing of ClusterSecretStore resources                                                                                                                   | true    |
| command.processClusterExternalSecret                | Enable processing of ClusterExternalSecret resources                                                                                                                | true    |
| command.enableCrossNamespaceSecretStore             | Enable cross namespace SecretStore reference in ExternalSecret                                                                                                      | true    |
| command.enableCrossNamespaceAuthRef                 | Enable cross namespace reference for authentication resources (ServiceAccount, AccessKey Secret) in SecretStore                                                      | true    |
| command.aliuid                                      | Alibaba Cloud account for splicing OIDC identity provider ARN                                                                                               |         |
| command.clusterId                                   | Aliyun Container Service Cluster ID for splicing OIDC identity provider ARN                                                                                               |         |
| image.repository                                    | Specified ack-secret-manager mirror warehouse name                                                                                                                  | acs/ack-secret-manager |
| image.tag                                           | Specified ack-secret-manager image tag                                                                                                                              | v0.5.0  |
| image.pullPolicy                                    | Image pull strategy, default is Always                                                                                                                              | Always  |
| nameOverride                                        | Override app name                                                                                                                                                   | nil     |
| fullnameOverride                                    | Override application full name                                                                                                                                      | nil     |
| rbac.create                                         | Whether to create and use RBAC resources, the default is true                                                                                                       | true    |
| securityContext.fsGroup                             | Specify the security context configuration of the application                                                                                                       | {}      |
| serviceAccount.create                               | Whether to create serviceaccount                                                                                                                                    | true    |
| serviceAccount.name                                 | Specify the name of the created serviceaccount                                                                                                                      | Automatic generated |
| serviceAccount.annotations                          | Specify adding the serviceaccount annotation tag                                                                                                                    | nil     |
| podAnnotations                                      | Specify the annotation label added to the pod                                                                                                                       | {}      |
| podLabels                                           | Specify the Label added to the pod                                                                                                                                  | {}      |
| replicaCount                                        | Number of controller copies                                                                                                                                         | 1       |
| nodeSelector                                        | The specified nodeSelector tag                                                                                                                                      | {}      |
| tolerations                                         | Specified taint tolerance configuration                                                                                                                             | []      |
| affinity                                            | Specified Pod affinity configuration                                                                                                                                | {}      |
| resources                                           | Specified Pod requests and limits configuration                                                                                                                     | {}      |

## Usage Instructions

This section adds a test credential in KMS Secrets Manager for synchronization and shows extended capabilities.

Tip: The ciphertext synchronization method for other Alibaba Cloud service mirrors that of KMS Secrets Manager. Follow these steps:

1. **Add the ciphertext for your desired product** by referencing the process in Step 1.
2. **Update the configuration fields** in the `SecretStore` and `ExternalSecret` examples below to match the corresponding values in the table provided.

| Configuration                                 | Alibaba KMS Secrets Manager                | Alibaba Cloud OOS Encryption Parameter              |
| --------------------------------------------- | ------------------------------------------ | --------------------------------------------------- |
| **SecretStore keyword authentication method** | SecretStore.Spec.KMS.KMSAuth               | SecretStore.Spec.OOS.OOSAuth                        |
| **ExternalSecret.spec.provider**              | kms                                        | oos                                                 |
| **ExternalSecret.spec.data.key**              | `<KMS  credential name>`(as follows test1) | `<OOS encryption parameter name>`(as follows test2) |

ack-secret-manager involves four CRDs, SecretStore and ClusterSecretStore are used to store access credentials (such as RRSA configuration, ClientKey, AK configuration, etc.), ExternalSecret and ClusterExternalSecret are used to store basic credential information that needs to be synchronized (such as credential name, version, etc.) and specify the SecretStore. It ensures the separation of permissions and data and enhances the flexibility of use. See below for details **CRD configuration introduction**

### 1. Create ciphertexts
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

### 2. Create SecretStore & ExternalSecret

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
        kmsEndpoint: kms.cn-hangzhou.aliyuncs.com # KMS service endpoint, field should only be configured when kms is the selected provider. For configuration details, see Part 6 of the usage instructions
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

### 3. JSON/YAML-formatted support

**data**

- If you want to parse a JSON/YAML-formatted secret and synchronize the key-value pairs specified in it to the k8s secret, you can use the `jmesPath` field. The following is an example using the `jmesPath` field, which we deploy in the cluster
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
        kmsEndpoint: kms.cn-hangzhou.aliyuncs.com # KMS service endpoint, field should only be configured when kms is the selected provider. For configuration details, see Part 6 of the usage instructions
        secretStoreRef:
          name: scdemo
          namespace: default
        jmesPath: # Parse some fields in json/yaml string
          - path: "name"
            objectAlias: "name"
          - path: "friends[0].name"
            objectAlias: "friendname"
  ```
- When you use the `jmesPath` field, you must specify the following two subfields:
    - `path`: Required, parses the specified field in json/yaml based on the [JMES path](https://jmespath.org/specification.html) specification
    - `objectAlias`: Required, used to specify the parsed field to be synchronized to the key name in the k8s secret
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

**dataProcess**

- If you want to parse JSON/YAML credentials and store them in a secret but don't know the specific structure of the credentials, you can use the self-extraction feature, i.e.  `dataProcess.Extract` field. And you can replace the parsed field key with a rule, i.e., `dataProcss.replaceRule` field, to prevent the secret data key from being irregular, resulting in failure to create a secret, the following is a sample ExternalSecret:
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
          kmsEndpoint: kms.cn-hangzhou.aliyuncs.com # KMS service endpoint, field should only be configured when kms is the selected provider. For configuration details, see Part 6 of the usage instructions
          secretStoreRef:
            name: scdemo
            namespace: default
        replaceRule:
          - source: "^n.*e$" # To replace keys that start with 'n' and end with 'e' with 'alibabacloud'
            target: "alibabacloud"
          - source: "^f.*s$"
            target: "ack"
  ```
- Once the synchronization is successful, you will be able to see the following results. The JSON/YAML credentials are parsed into three parts, and their respective keys are replaced according to the replaceRule rules.
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

### 4. Currently supports cross-account credential synchronization. To configure, specify the `remoteRamRoleArn` and `remoteRamRoleSessionName` fields in `SecretStore.Spec.KMS.KMSAuth`. Below is an example configuration:

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

### 5. command.enableWorkerRole Configuration Introduction

The configuration of `command.enableWorkerRole` is related to the cluster type, and the corresponding relationship is as follows:

| Cluster Type          | command.enableWorkerRole |
| --------------------- | ------------------------ |
| ACK Managed Cluster   | true                     |
| ACK Dedicated Cluster | true                     |
| ACK Edge Cluster      | true                     |
| Other Cluster         | false                    |

### 6. KMS Endpoint Configuration Reference

The KMS service currently supports two access methods: **dedicated instance gateway** and **public gateway**, requiring the following endpoint configurations(For more differences between dedicated gateway access and shared gateway access, please refer to [Differences between shared and dedicated gateways for accessing KMS](https://www.alibabacloud.com/help/en/kms/key-management-service/developer-reference/classic-kms-sdkclassic-kms-sdk/#26484656d84ey)):

**KMS Endpoint Priority Reference**

| Type                      | Configuration Field                   | Purpose                                                     | Priority | Reference                                                            |
| ------------------------- | ------------------------------------- | ----------------------------------------------------------- | -------- |-------------------------------------------------------------- |
|Secret-Level Configuration | ExternalSecret.spec.data.kmsEndpoint  | Specify the endpoint address for individual KMS secret that needs to be imported | Highest  | For individual KMS secret, this configuration takes precedence and will override both global and default configurations.|
|Global Configuration       | command.kmsEndpoint(launch parameter) | Applies to all KMS requests                                 | Medium   | Provides the endpoint address for KMS secrets when Secret-Level Configuration are not provided.|
|Default Configuration      | None                                  | Used when no explicit configuration is provided             | Lowest   | The default address is kms-vpc.{region}.aliyuncs.com, replace `{region}` with the KMS credential's region.|

```
apiVersion: "alibabacloud.com/v1alpha1"
kind: ExternalSecret
metadata:
  name: esdemo
spec:
  provider: kms
  data:
    - key: test-hangzhou # Actual endpoint address: Use the global configuration if it exists. Otherwise, use the default configuration: kms-vpc.{region}.aliyuncs.com.
      name: hangzhou-vpc
      versionId: v1
    - key: test-hangzhou # Actual endpoint address: The endpoint specified by the kmsEndpoint field is kms.cn-hangzhou.aliyuncs.com
      name: hangzhou-public
      versionId: v1
      kmsEndpoint: kms.cn-hangzhou.aliyuncs.com
```

**KMS Endpoint Address Reference**

| Gateway Type      | Domain Type        | Endpoint Address                                 | Usage Instructions                                                                                                             |
| ----------------- | ------------------ | ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------ |
| Dedicated Gateway | KMS Private Domain | {kms-instance-id}.cryptoservice.kms.aliyuncs.com | 1. The KMS secret instance and cluster are in the same region and the same VPC.<br />2. Replace {kms-instance-id} with the ID of KMS secret instance.<br />3. The version of the KMS secret instance must be 3.0 or later. |
| Shared Gateway    | VPC Domain         | kms-vpc.{region}.aliyuncs.com                    | 1. The KMS secret and cluster are in the same region.<br />2. Replace {region} with the region where the KMS secret is located.<br />3. This address can be used with the default configuration without additional setup. |
| Shared Gateway    | Public Network     | kms.{region}.aliyuncs.com                        | 1. Replace {region} with the region where the KMS secret is located.<br />2. The cluster can access public network. |

### 7. Authentication Using `serviceAccountRef` {#serviceAccountRef-auth}

In multi-tenant scenarios, users from different tenants want to bind distinct RAM roles to their own namespace-scoped ServiceAccounts, with each role authorized to access only specific credentials within Alibaba Cloud KMS Secrets Manager that fall under their permitted scope.

`ack-secret-manager` supports the `serviceAccountRef` authentication method in tenant namespaces’ `SecretStore` resources, enabling fine-grained, ServiceAccount-level control over KMS credential access permissions.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  annotations:
    ack.alibabacloud.com/role-arn: acs:ram::<account-id>:role/<role-name>   # Note: Configure this annotation with the ARN of the RAM role bound to the ServiceAccount
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

### 8. Template Processing Feature

ack-secret-manager supports advanced template parsing capabilities, allowing you to transform and customize secret data before synchronizing secret credentials to Kubernetes Secrets.

#### Template Fields Overview

The `ExternalSecret.spec.target.template` field supports the following configurations:

**Inline Templates (`template.data`)**
Define template transformations directly in the ExternalSecret specification:

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
        # Transform data using Go templates and Sprig functions
        formatted-config: |
          {
            "database": {
              "host": "{{ .raw-data | quote }}",
              "port": {{ "5432" | int }}
            }
          }
        # Conditional processing
        is-active: |
          {{ if eq .status "active" }}true{{ else }}false{{ end }}
```

**Template References (`templateFrom`)**
Reference templates from existing ConfigMaps or Secrets:

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
        # Reference ConfigMap templates
        - configMap:
            name: app-config-templates
            items:
              - key: database-config
                templateAs: Values  # Process as values only
          target: Data
        
        # Reference Secret templates
        - secret:
            name: secure-templates
            items:
              - key: connection-string
                templateAs: KeysAndValues  # Process as key-value pairs
          target: Data
        
        # Inline literal template
        - literal: |
            APP_ENV={{ .environment | default "production" }}
          target: Data
```

**Template Targets**
Control where template results are placed:

- `Data` (default): Place results in Secret data
- `Annotations`: Place results in Secret annotations
- `Labels`: Place results in Secret labels

**Template Scopes**
Define how template keys are interpreted:

- `Values`: Process template as values only
- `KeysAndValues`: Process template as key-value pairs (newline separated)

**Supported Template Functions**

The template processor includes [Sprig functions](http://masterminds.github.io/sprig/) for advanced processing:

```yaml
# String operations
uppercase: {{ .username | upper }}
lowercase: {{ .service | lower }}

# Default values
port: {{ .port | default "8080" }}

# Type conversion
timeout: {{ "30" | int }}

# JSON operations
config-json: {{ .config | fromJson | toJson }}

# Conditional logic
is-prod: {{ if eq .env "prod" }}true{{ else }}false{{ end }}

# String formatting
connection-url: postgresql://{{ .user }}:{{ .password }}@{{ .host }}:{{ .port }}/{{ .db }}

# Bcrypt password hashing
hashed-password: {{ bcrypt .password }}

# Htpasswd format (for basic auth)
htpasswd-auth: {{ htpasswd "admin" .password }}

# JSON path query (recommended for nested access)
db-host: {{ jsonPath .config "database.host" }}

# JSON merge
merged: {{ mergeJson .default .override }}

# Regex replacement
cleaned: {{ regexReplace .text "\\d+" "" }}

# Safe Base64 decode
decoded: {{ b64decSafe .encoded }}

# Key=Value parsing
{{ $kv := parseKeyValue .env }}
DB_URL: {{ index $kv "DATABASE_URL" }}
```

#### Template Context Access

Access secret data in templates using:
- .KEY: Direct field access (standard Go template syntax)
- index . "KEY": Using index function
- jsonPath .jsonKey "path.to.field": Extract from nested JSON (recommended)

Example:
```yaml
target:
  template:
    data:
      # Standard field access
      username: {{ .username }}
      password: {{ .password }}
      
      # Nested JSON access (recommended)
      db-host: {{ jsonPath .dbConfig "host" }}
      db-port: {{ jsonPath .dbConfig "port" }}
      
      # Or use fromJson + index
      db-user: {{ index (.dbConfig | fromJson) "user" }}
      
      # Conditional processing
      {{ if eq .environment "production" }}
      log-level: error
      {{ else }}
      log-level: debug
      {{ end }}
      
      # Loop iteration
      {{ range .ports | fromJson }}
      - port: {{ . }}
      {{ end }}
      
      # Bcrypt password hashing
      hashed-pwd: {{ bcrypt .password }}
      
      # Generate htpasswd auth file
      auth: |
        {{- $creds := list }}
        {{- range $user, $pw := . }}
        {{- $creds = append $creds (htpasswd $user $pw) }}
        {{- end }}
        {{ $creds | join "\n" }}
```

#### Template Validation

The processor validates output formats:

- Secret data keys: Must follow Kubernetes naming conventions
- Annotation keys: Must follow DNS subdomain format
- Label keys: Must follow DNS label format

Invalid keys are automatically filtered out to prevent Secret creation failures.

### 9. New ClusterExternalSecret and ClusterSecretStore Resources

ack-secret-manager introduces ClusterExternalSecret and ClusterSecretStore resources. The following are resource descriptions and examples:

**ClusterExternalSecret**

A cluster-level resource for managing and coordinating ExternalSecrets across multiple namespaces. It supports using `spec.conditions` to configure matching namespaces and can automatically create ExternalSecrets in matching namespaces.

For field descriptions, please refer to the ClusterExternalSecret section in the CRD Configuration Introduction.

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

A cluster-level resource with the same functionality as SecretStore, but can be referenced by ExternalSecrets in any namespace in the cluster, and supports using `spec.conditions` for access control.

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

For more detailed CRD resource descriptions, please refer to the following CRD Resource Details and CRD Configuration Introduction sections.

## CRD Resource Details

### CRD Resource Overview

Currently, four Custom Resource Definitions (CRDs) are provided, divided into two categories:

**Authentication Configuration Category**

1. **SecretStore**: Namespace-level resource used to define access credentials (such as RRSA, ClientKey, AK configuration, etc.)
2. **ClusterSecretStore**: Cluster-level resource with the same functionality as SecretStore, but can be referenced by ExternalSecrets in any namespace in the cluster, and supports access control configuration

**Data Synchronization Configuration Category**

1. **ExternalSecret**: Namespace-level resource used to define the basic credential information that needs to be synchronized (such as credential name, version, etc.) and specify the SecretStore
2. **ClusterExternalSecret**: Cluster-level resource used to manage and coordinate ExternalSecrets across multiple namespaces, and can automatically create ExternalSecrets in matching namespaces

### Cross-Namespace Control Mechanisms

To enhance security and flexibility, ack-secret-manager provides multiple cross-namespace control mechanisms:

**ExternalSecret Reference to SecretStore Control**

- Control whether ExternalSecret can reference SecretStore across namespaces through the `command.enableCrossNamespaceSecretStore` parameter
- The default value is true, which means cross-namespace references are allowed
- When set to false, ExternalSecret can only reference SecretStore in the same namespace

**SecretStore Reference to Authentication Resource Control**

- Control whether SecretStore can reference authentication resources (ServiceAccount, AccessKey Secret) across namespaces through the `command.enableCrossNamespaceAuthRef` parameter
- The default value is true, which means cross-namespace references are allowed
- When set to false, SecretStore can only reference authentication resources in the same namespace

**ClusterExternalSecret Match Mechanism**

- ClusterExternalSecret creates ExternalSecret resources in matching namespaces by defining namespace selection criteria through the spec.conditions field.
- Supports three match methods, with an OR relationship between conditions:

    1. namespaceSelector: Use label selectors to match namespaces that are allowed to access
    2. namespaces: Explicitly list the allowed namespace name list
    3. namespaceRegexes: Use regular expressions to match the allowed namespace name list

**ClusterSecretStore Access Control**

- ClusterSecretStore defines the namespace conditions that allow access to the resource through the `spec.conditions` field
- Supports three access control methods, with an OR relationship between conditions:

    1. namespaceSelector: Use label selectors to match namespaces that are allowed to access
    2. namespaces: Explicitly list the allowed namespace name list
    3. namespaceRegexes: Use regular expressions to match the allowed namespace name list

### Recommended Usage

**Cross-Namespace Access Recommendation**

For scenarios that require cross-namespace access, it is recommended to use the following combinations:

1. ClusterSecretStore + ExternalSecret: When multiple namespaces need to use the same authentication configuration
2. ClusterSecretStore + ClusterExternalSecret: When you need to automatically create ExternalSecrets with the same configuration in multiple namespaces

**Security Best Practices**

1. **Principle of Least Privilege**:
    - In scenarios where cross-namespace access is not required, set `command.enableCrossNamespaceSecretStore` and `command.enableCrossNamespaceAuthRef` to `false`.
    - Prefer namespace-scoped resources (`SecretStore` and `ExternalSecret`) over cluster-scoped ones.
2. **Access Control Configuration**:
    - When using `ClusterSecretStore`, explicitly configure `spec.conditions` to restrict the namespaces that can access it.
    - Avoid creating `ClusterSecretStore` resources without access restrictions.
3. **Authentication Method Selection**:
    - Prefer RRSA or ServiceAccount-based authentication, and avoid directly exposing AccessKeys in configuration.
    - Separate authentication configuration from data configuration to enhance security.
4. **Avoid using `ClusterExternalSecret` unless absolutely necessary**, to reduce the risk of secret leakage across namespaces:
    - If your workload requires synchronizing secrets across multiple namespaces, use `spec.conditions` to precisely control the target namespaces for `ExternalSecret` creation.

## CRD configuration introduction

### ExternalSecret

**spec**

| Parameter        | Description                                                               | Required |
| ---------------- | ------------------------------------------------------------------------- | -------- |
| provider         | The target cloud service for credential synchronization, such as KMS, OOS | no       |
| data             | Data source (identifier for the target data)                              | no       |
| dataProcess      | Data source requiring special processing (identifier for the target data) | no       |
| type             | Kubernetes Secret types (e.g., Opaque)                                    | no       |
| target           | Defines the configuration for the Kubernetes Secret to be generated       | no       |
| rotationInterval | The time in which the controller should reconcile its objects             | no       |

**target**

| Parameter        | Description                                         | Required | Default |
| ---------------- | --------------------------------------------------- | -------- | ------- |
| name             | Name of the target Kubernetes Secret                | no       |         |
| template         | Template configuration for transforming secret data | no       |         |

**template**

| Parameter    | Description                                              | Required | Default   |
| ------------ | -------------------------------------------------------- | -------- | --------- |
| data         | Inline template definitions                              | no       |           |
| templateFrom | References to external templates (ConfigMap/Secret)      | no       |           |
| metadata     | Template for Secret metadata (labels/annotations)        | no       |           |
| type         | Type of the target secret                                | no       |           |
| mergePolicy  | How template results should be merged with original data | no       | Replace   |

**metadata**

| Parameter   | Description                        | Required |
| ----------- | ---------------------------------- | -------- |
| annotations | Annotations to apply to the secret | no       |
| labels      | Labels to apply to the secret      | no       |

**templateFrom**

| Parameter  | Description                                               | Required | Default |
| ---------- | --------------------------------------------------------- | -------- | ------- |
| configMap  | Reference to ConfigMap containing templates               | no       |         |
| secret     | Reference to Secret containing templates                  | no       |         |
| literal    | Inline literal template string                            | no       |         |
| target     | Target location for template results (Data/Annotations/Labels) | no       | Data    |

**configMap/secret**

| Parameter | Description                                               | Required |
| --------- | --------------------------------------------------------- | -------- |
| name      | The name of the ConfigMap/Secret resource                 | yes      |
| items     | List of keys in the ConfigMap/Secret to use as templates  | yes      |

**items**

| Parameter   | Description                                    | Required | Default |
| ----------- | ---------------------------------------------- | -------- | ------- |
| key         | A key in the ConfigMap/Secret                  | yes      |         |
| templateAs  | How the template keys should be interpreted    | no       | Values  |

**templateAs**

| Value          | Description                                    |
| -------------- | ---------------------------------------------- |
| Values         | Process template as values only                |
| KeysAndValues  | Process template as key-value pairs            |

**templateFrom.target**

| Value         | Description                                    |
| ------------- | ---------------------------------------------- |
| Data          | Store template results in Secret data field    |
| Annotations   | Store template results in Secret annotations   |
| Labels        | Store template results in Secret labels        |

**mergePolicy**

| Value    | Description                                                                                                                                                          |
| -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Replace  | Uses template field names exclusively (clears existing field names in the secret)                                                                                    |
| Merge    | Merges template field names with existing secret field names; if field names in the template match those in the existing secret, the template values take precedence |

**data**

| Parameter      | Description                                                                                                                             | Required |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| key            | The unique identifier for the target credential, such as the key for KMS credentials                                                    | yes      |
| name           | The corresponding key for the credentials in the secret data of the cluster                                                             | no       |
| versionStage   | The version stage of the target credential                                                                                              | no       |
| versionId      | The version Id of the target credential, this field is not required when provider is oos                                                | no       |
| jmesPath       | If the target credential is in JSON/YAML format, you can specify to retrieve the value corresponding to a specific key in the JSON/YAML | no       |
| secretStoreRef | Information of the referenced SecretStore                                                                                               | no       |

**dataProcess（Data source requiring special processing.）**

| Parameter   | Description                                                                                                                    | Required |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------ | -------- |
| extract     | Parsing JSON/YAML for the target credential without requiring the user to specify the JSON/YAML key                            | no       |
| replaceRule | Replacing keys of the parsed secret based on specific rules to prevent illegal keys from being stored in the Kubernetes Secret | no       |

**replaceRule（The content replacement used for the Secret Key.）**

| Parameter | Description                                                             | Required |
| --------- | ----------------------------------------------------------------------- | -------- |
| target    | The string used for replacement                                         | yes      |
| source    | The string that needs to be replaced, which can be a regular expression | yes      |

**jmesPath**

| Parameter   | Description                                                                       | Required |
| ----------- | --------------------------------------------------------------------------------- | -------- |
| path        | JMESPath expression that allows users to specify the JSON/YAML key                | yes      |
| objectAlias | The data key corresponding to the Kubernetes Secret where the data will be stored | yes      |

**secretStoreRef**

| Parameter | Description                                              | Required                    |
| --------- | -------------------------------------------------------- | --------------------------- |
| name      | The specified SecretStore name                           | yes                         |
| namespace | The specified SecretStore namespace                      | Yes                         |
| kind	    | Specifies SecretStore or ClusterSecretStore to reference | no, defaults to SecretStore |

### SecretStore

**spec**

| Parameter | Description                                    | Required |
| --------- | ---------------------------------------------- | -------- |
| KMS       | Alibaba Cloud Key Management Service           | no       |
| OOS       | Alibaba Cloud OOS(Manage Encryption Parameter) | no       |

**KMS**

| parameter | Description                                                                    | Required |
| --------- | ------------------------------------------------------------------------------ | -------- |
| KMSAuth   | Credentials required to access KMS (Key Management Service) under a shared KMS | no       |

**OOS**

| Parameter | Description                                                     | Required |
| --------- | --------------------------------------------------------------- | -------- |
| OOSAuth   | Credentials required to access OOS(Manage Encryption Parameter) | no       |

**KMSAuth**/**OOSAuth**

| Parameter                | Description                                                                 | Required |
| ------------------------ | --------------------------------------------------------------------------- | -------- |
| accessKey                | Refer to the following Alibaba Cloud AccessKey Authentication Configuration | no       |
| accessKeySecret          | Refer to the following Alibaba Cloud AccessKey Authentication Configuration | no       |
| ramRoleARN               | Ram role ARN                                                                | no       |
| ramRoleSessionName       | Role session name                                                           | no       |
| oidcProviderARN          | OIDC provider ARN                                                           | no       |
| oidcTokenFilePath        | OIDC token file path                                                        | no       |
| remoteRamRoleArn         | Cross-account ram role ARN                                                  | no       |
| remoteRamRoleSessionName | Cross-account ram role session name                                         | no       |
| serviceAccountRef	       | Reference to ServiceAccount information	                                   | no       |

**Alibaba Cloud AccessKey Authentication Configuration**

The Alibaba Cloud AccessKey is critical for cloud resource access. For security reasons, it must be referenced via a Kubernetes Secret rather than configured directly in the `SecretStore` resource. The required fields are outlined below：

> The leakage of Alibaba Cloud AccessKey can lead to serious security risks. It is recommended that you use RRSA or other Alibaba Cloud authentication methods.

| parameter | Description                  | required |
| --------- | ---------------------------- | -------- |
| name      | Kubernetes Secrets name      | Yes      |
| namespace | Kubernetes Secrets namaspace | Yes      |
| key       | Kubernetes Secrets data key  | Yes      |

**ServiceAccountRef**

| Parameter | Description                                                     | Required |
| --------- | --------------------------------------------------------------- | -------- |
| name      | ServiceAccount name                                             | Yes      |
| namespace | ServiceAccount namespace (only required for ClusterSecretStore) | No       |
| audiences | Array of audiences for the ServiceAccount token aud field       | No       |

### ClusterSecretStore

ClusterSecretStore is a cluster-scoped SecretStore resource that can be referenced by ExternalSecrets in any namespace in the cluster. It has all the capabilities of SecretStore plus access control configurations that limit which namespaces can access this resource.

**spec**

| Parameter  | Description                                             | Required |
| ---------- | ------------------------------------------------------- | -------- |
| conditions | Defines namespace conditions for accessing the resource | Yes      |
| KMS        | Connect to KMS Secrets Manager to get credentials       | No       |
| OOS        | Connect to OOS service to get encrypted parameters      | No       |

**conditions**

| Parameter         | Description                                        | Required |
| ----------------- | -------------------------------------------------- | -------- |
| namespaceSelector | Match allowed namespaces using label selector      | Yes      |
| namespaces        | Explicitly list allowed namespace names            | No       |
| namespaceRegexes  | Match allowed namespace names using regex patterns | No       |

### ClusterExternalSecret

ClusterExternalSecret is a resource that manages and coordinates ExternalSecrets across multiple namespaces. It can automatically create ExternalSecrets in matching namespaces. When a matching namespace already has an ExternalSecret that needs to be created, ClusterExternalSecret completely updates the existing ExternalSecret resource with the new specification.

**spec**

| Parameter              | Description                                                                                                         | Required |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------- | -------- |
| externalSecretSpec     | Specification definition of ExternalSecret to create                                                                | Yes      |
| externalSecretName     | Name of ExternalSecret to create, defaults to the name of ClusterExternalSecret                                     | No       |
| externalSecretMetadata | Metadata of ExternalSecret to create                                                                                | No       |
| namespaceSelectors     | List of label selectors to select target namespaces(Deprecated)                                                     | No       |
| conditions             | A list of conditions for selecting target namespaces, supporting namespaceSelector, namespaces, and namespaceRegexe | No       |
| rotationInterval       | Time interval for controller to check namespace labels and reconcile objects                                        | No       |

**externalSecretMetadata**

The externalSecretMetadata field allows you to add additional metadata to the ExternalSecret resources automatically created by the ClusterExternalSecret:

| Parameter   | Description                              | Required |
| ----------- | ---------------------------------------- | -------- |
| annotations | Annotations to add to the ExternalSecret | No       |
| labels      | Labels to add to the ExternalSecret      | No       |

**conditions**

| Parameter         | Description                                        | Required |
| ----------------- | -------------------------------------------------- | -------- |
| namespaceSelector | Match allowed namespaces using label selector      | Yes      |
| namespaces        | Explicitly list allowed namespace names            | No       |
| namespaceRegexes  | Match allowed namespace names using regex patterns | No       |

## Security

Please report vulnerabilities by email to **kubernetes-security@service.aliyun.com**. Also see our [SECURITY.md](https://github.com/AliyunContainerService/ack-secret-manager/blob/master/SECURITY.md) file for details.

## Release Note

| Version  | Date       | Changes                                                      |
| -------- | ---------- | ------------------------------------------------------------ |
| `0.4.0`  | 2022/12/22 | Support sync specific key-value pairs extract from a JSON-formatted secret based on JMES path |
| `0.5.0`  | 2023/10/10 | 1.dedicated KMS credential synchronization<br />2.multiple Alibaba Cloud access credentials management,<br />3.self-resolving credentials and key rule replacement<br />4.Cross-account KMS credential synchronization. |
| `0.5.1`  | 2023/10/18 | Function and performance optimization |
| `0.5.2`  | 2024/08/01 | Large-scale resource synchronization concurrency optimization |
| `0.5.3`  | 2024/10/10 | Supports resource synchronization with second-level rate limiting, fixes for certain software package CVEs |
| `0.5.4`  | 2024/10/31 | Supports OOS encryption parameter synchronization |
| `0.5.5`  | 2024/11/22 | Supports synchronization of binary-type secrets |
| `0.5.6`  | 2025/3/3   | Supports cluster type of Acs |
| `0.5.7`  | 2025/3/26  | Support sync specific key-value pairs extract from a YAML-formatted secret |
| `0.5.8`  | 2025/3/28  | 1.Support multi-architecture deployment<br />2.Support configuring Endpoint to obtain KMS secret |
| `0.5.11` | 2025/6/17  | Provide an option to control deletion of the cluster Secret in case of KMS credential or OOS encryption parameter sync failure |
| `0.5.12` | 2025/6/25  | Support using ExternalSecret to configure the endpoint address of the KMS instance dimension to connect multiple KMS instances |
| `0.6.0`  | 2025/12/5	| 1.Support ClusterSecretStore resource<br />2.Support ClusterExternalSecret resource<br />3.Support prohibiting ExternalSecret cross-namespace reference to SecretStore resources<br />4.Support SecretStore reference to ServiceAccount configuration for authentication information |
| `0.6.1`  | 2026/2/28 	| 1.Modify the namespace matching conditions for ClusterExternalSecret resources<br />2.Add secret and serviceaccount controllers to reconcile associated ClusterSecretStores and SecretStores when resources are updated<br />3.Optimize the token refresh time for serviceaccount authentication method 
| `0.6.2`  | 2016/3/2   | Support advanced template processing capabilities to transform and customize secret data before creating Kubernetes Secret when synchronizing external secrets |

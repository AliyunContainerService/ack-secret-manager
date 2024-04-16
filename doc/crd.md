## CRD Parameters Description

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
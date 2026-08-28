# ACK Secret Manager

English | [中文简体](./README-zh_CN.md)

[ack-secret-manager](https://github.com/AliyunContainerService/ack-secret-manager)   enables you to securely import credentials stored in [Alibaba Cloud KMS Secrets Manager](https://www.alibabacloud.com/help/en/key-management-service) or [Alibaba Cloud OOS Encryption Parameter ](https://www.alibabacloud.com/help/en/oos/getting-started/manage-encryption-parameters)  into your Kubernetes cluster as native Secret objects. This ensures automatic synchronization of encrypted data while allowing applications to safely consume secrets by mounting them in Pod configurations. It helps mitigate sensitive data exposure risks in the development and deployment stages of the supply chain, ensuring compliance with security best practices.

> **⚠️ Upgrade Notes**:
>
> 1. **Since v0.6.4**: For security hardening, the default values of `enableCrossNamespaceSecretStore` and `enableCrossNamespaceAuthRef` were changed from `true` to `false`. After upgrading, cross-namespace references (ExternalSecret → SecretStore, SecretStore → ServiceAccount/AccessKey Secret) are rejected by default. If your deployment relies on cross-namespace references, explicitly set the corresponding parameters to `true` in `values.yaml` to restore the previous behavior.
> 2. **Since v0.6.7**: The default value of `enableWorkerRole` was changed from `true` to `false`. If your deployment uses WorkerRole (ECS RAM Role) authentication, explicitly set `command.enableWorkerRole` to `true` in `values.yaml`.
>
> See the [Release Note](#release-note) for details.

## 📚 Documentation

| Document | Description |
|----------|-------------|
| [📋 CRD Resources](docs/crd_resources_guide.md) | 4 CRDs overview, cross-namespace control, recommended usage, field reference |
| [🔐 Authentication Guide](docs/auth_guide.md) | Authentication methods, priority, Namespace rules |
| [🚀 Advanced Usage](docs/advanced_usage.md) | JSON/YAML parsing, cross-account sync, kmsEndpoint, credential rotation, multi-data source support |
| [📝 Template Guide](docs/template_processing_guide.md) | Template syntax, template references, custom functions |
| [👤 ServiceAccountRef](docs/serviceAccountRef.md) | Multi-tenant ServiceAccount-level RRSA authorization |
| [📦 Examples](examples/README.md) | Ready-to-use YAML examples by category |

## Install

1. Make sure that the credentials used by the ack-secret-manager has sufficient permissions to access the Alibaba Cloud product service. You can use the following two configuration methods, and we recommend you to use the second **RRSA** method to achieve authorization in the Pod level.

    - Implement pod-level authorization using [RRSA method](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/user-guide/use-rrsa-to-authorize-pods-to-access-different-cloud-services)

        * [Enable RRSA](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-ywl-59g-j8h)
        * [Use RRSA](https://www.alibabacloud.com/help/en/container-service-for-kubernetes/latest/use-rrsa-to-enforce-access-control#section-rmr-eeh-878): Create a corresponding RAM role for the specified ServiceAccount, configure a trust policy for the RAM role, and grant necessary permissions to the RAM role.
        * By configuring `ramRoleARN` and `oidcProviderARN` in SecretStore, you can bind KMS secret retrieval permissions to the specific ServiceAccount used by the `ack-secret-manager` component **without using AccessKey (AK)**.
        * In multi-tenant scenarios, using the `serviceAccountRef` authentication method in the tenant namespace’s SecretStore enables **fine-grained, ServiceAccount-level control** over KMS secret retrieval permissions. For details on SecretStore configuration, see [Authentication using serviceAccountRef](docs/serviceAccountRef.md).

    - Add permissions to the Worker RAM role associated with the cluster

        - Log in to the **Container Service for Kubernetes (ACK) console**.

        - Select your target cluster to open its **Cluster Details** page.

        - Go to the **Cluster Resources** section. Locate and click the Worker RAM role named **`KubernetesWorkerRole-xxxxxxxxxxxxxxx`**. This will redirect you to the corresponding RAM role page in the RAM console.

        - Click **Add Permissions** to create a custom policy with the following content (only grant the permissions required for the secret manager, adhering to the principle of least privilege):

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

        - Attach the created custom policy to the cluster's WorkerRole.

    - For all supported authentication configuration methods in ack-secret-manager, please refer to the [official documentation](https://www.alibabacloud.com/help/en/ack/ack-managed-and-ack-dedicated/security-and-compliance/use-ack-secret-manager-to-import-alibaba-cloud-kms-service-credentials).

    > 💡 **Detailed Authentication Guide**: For 5 authentication methods, configuration steps, priority, and Namespace rules, please refer to [Authentication Guide](docs/auth_guide.md)

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

Notes on uninstallation:

- During uninstallation, the cleanup Job built into the chart automatically deletes all custom resources of these types in the cluster (ExternalSecret, SecretStore, ClusterSecretStore, ClusterExternalSecret) first, ensuring that the uninstallation completes smoothly without being blocked by leftover resources.
- Uninstalling does not delete the Kubernetes Secrets synchronized and generated by these resources, to avoid impacting running workloads that consume these Secrets. They remain in the cluster, so please clean them up manually as needed (Note: this differs from the runtime behavior where deleting a single ExternalSecret also deletes its target Secret).

## Configuration instructions

> **CRD installation notes**: The core CRDs (`externalsecrets`, `secretstores`) are always installed with the chart; the `crds.createClusterSecretStore` and `crds.createClusterExternalSecret` switches only control whether the cluster-scoped CRDs (`clustersecretstores`, `clusterexternalsecrets`) are installed.

| **parameter**                                       | **introduction**                                                                                                                                          | **default value** |
| --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------- |
| crds.createClusterSecretStore                       | Whether to install the ClusterSecretStore CRD                                                                                                             | true              |
| crds.createClusterExternalSecret                    | Whether to install the ClusterExternalSecret CRD                                                                                                          | true              |
| env                                                 | Additional environment variables map injected into the Pod; POD_NAME/POD_NAMESPACE are reserved and entries with these names are ignored                 | {}                |
| imagePullSecrets                                    | List of image pull secrets for the Pod                                                                                                                    | []                |
| envVarsFromSecret.ACCESS_KEY_ID                     | You can build the SDK client by specifying the credential AK by setting the ACCESS_KEY_ID variable, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.SECRET_ACCESS_KEY                 | You can build the SDK client by specifying the credential SK by setting the SECRET_ACCESS_KEY variable, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.ALICLOUD_ROLE_ARN                 | You can specify the RAM role ARN used to build the SDK client by setting the ALICLOUD_ROLE_ARN variable, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_NAME        | You can specify the RAM role session name by setting the ALICLOUD_ROLE_SESSION_NAME variable to build the SDK client, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.ALICLOUD_ROLE_SESSION_EXPIRATION  | You can specify the RAM role session expiration length by setting the ALICLOUD_ROLE_SESSION_EXPIRATION variable to build the SDK client. It needs to be defined in the secret instance named alibaba-credentials. | |
| envVarsFromSecret.ALICLOUD_OIDC_PROVIDER_ARN        | You can specify the ARN of the RAM OIDC provider by setting the ALICLOUD_OIDC_PROVIDER_ARN variable to build the SDK client, which needs to be defined in the secret instance named alibaba-credentials | |
| envVarsFromSecret.ALICLOUD_OIDC_TOKEN_FILE          | You can specify the oidc token file path in the pod by setting the ALICLOUD_OIDC_TOKEN_FILE variable to build the SDK client. It needs to be defined in the secret instance named alibaba-credentials. | |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_ARN          | You can specify the RAM Role Arn of another account by setting the ALICLOUD_REMOTE_ROLE_ARN variable for role playing when pulling credential data across accounts. |         |
| envVarsFromSecret.ALICLOUD_REMOTE_ROLE_SESSION_NAME | You can specify the RAM Role Session Name by setting the ALICLOUD_REMOTE_ROLE_SESSION_NAME variable to perform role play when pulling credential data across accounts. |      |
| rrsa.enable                                         | Whether to enable the RRSA feature, the default is false. After enabling, you need to configure the ALICLOUD_ROLE_ARN and ALICLOUD_OIDC_PROVIDER_ARN parameters in envVarsFromSecret. | false |
| command.reconcilePeriod                             | The interval for the controller to re-coordinate the externalSecret instance, the default is 5 seconds                                                              | 5s      |
| command.reconcileCount                              | Specify the number of workers to concurrently coordinate the externalSecret instance. The default is 1                                                              | 1       |
| command.tokenRotationPeriod                         | Deprecated; no longer effective                                                                                                             | —       |
| command.region                                      | Pull secret credentials from the specified region                                                                                                                   |         |
| command.kmsEndpoint                                 | Pull secret credentials from the specified endpoint                                                                                                                 |         |
| command.disablePolling                              | Turn off automatic synchronization of pulling the latest credential content from the backend, default false                                                         | false   |
| command.pollingInterval                             | The interval for synchronizing existing secret instances from the backend                                                                                           | 120s    |
| command.maxConcurrentSecretPulls                    | Deprecated; use command.maxConcurrentKmsSecretPulls instead                                                                                                             | 10      |
| command.maxConcurrentKmsSecretPulls                 | Maximum number of kms secrets synchronized per second (rate limit, not concurrency)                                                                                 | 10      |
| command.maxConcurrentOosSecretPulls                 | Maximum number of oos secrets synchronized per second (rate limit, not concurrency)                                                                                 | 10      |
| command.cleanupSecretOnFailure                      | Delete the corresponding cluster Secret when all data sources fail to sync (no data available) and this flag is enabled, including templated ExternalSecrets; on partial failures the Secret is not deleted and is handled by the merge/fail-closed strategy instead | false   |
| command.processClusterSecretStore                   | Enable processing of ClusterSecretStore resources; when `watchNamespaces` or `excludeNamespaces` is configured, the explicit value of this parameter is forcibly overridden to disabled                                                                                                                   | true    |
| command.processClusterExternalSecret                | Enable processing of ClusterExternalSecret resources; when `watchNamespaces` or `excludeNamespaces` is configured, the explicit value of this parameter is forcibly overridden to disabled                                                                                                                | true    |
| command.enableCrossNamespaceSecretStore             | Enable cross namespace SecretStore reference in ExternalSecret                                                                                                      | false   |
| command.enableCrossNamespaceAuthRef                 | Enable cross namespace reference for authentication resources (ServiceAccount, AccessKey Secret) in SecretStore                                                      | false   |
| command.enableWorkerRole                            | Whether to enable WorkerRole (ECS RAM Role) authentication                                                                                                           | false   |
| command.aliuid                                      | Alibaba Cloud account for splicing OIDC identity provider ARN                                                                                               |         |
| command.clusterId                                   | Aliyun Container Service Cluster ID for splicing OIDC identity provider ARN                                                                                               |         |
| command.watchNamespaces                             | Comma-separated namespace whitelist (cache-level hard limit): the operator only watches the listed namespaces (including ExternalSecrets, SecretStores, and the Secret/ServiceAccount resources that trigger resynchronization); events from namespaces outside the list are invisible (empty = all namespaces) |         |
| command.excludeNamespaces                           | Comma-separated namespace blacklist (event-level exclusion): ExternalSecret events in listed namespaces are not processed and are never synced. A namespace must not appear in both watchNamespaces and excludeNamespaces; otherwise the operator fails to start. When either parameter is configured, the ClusterSecretStore and ClusterExternalSecret controllers are automatically disabled. Deletions of ExternalSecrets in excluded namespaces are still processed normally (the Secrets they generated are cleaned up), and SecretStores in excluded namespaces are not affected by this parameter and continue to be processed normally |         |
| image.repository                                    | Specified ack-secret-manager mirror warehouse name                                                                                                                  | registry-cn-hangzhou.ack.aliyuncs.com/acs/ack-secret-manager |
| image.tag                                           | Specified ack-secret-manager image tag                                                                                                                              | v0.6.7  |
| image.pullPolicy                                    | Image pull strategy, default is IfNotPresent                                                                                                                        | IfNotPresent  |
| cleanupImage.repository                             | Repository of the externally pre-built image used by the pre-uninstall cleanup Job (helm pre-delete hook)                                                           | registry-cn-hangzhou.ack.aliyuncs.com/acs/ack-secret-manager-cleanup |
| cleanupImage.tag                                    | Image tag of the cleanup Job                                                                                                                                        | v0.5.0  |
| cleanupImage.pullPolicy                             | Image pull policy of the cleanup Job                                                                                                                                | IfNotPresent  |
| nameOverride                                        | Override app name                                                                                                                                                   | nil     |
| fullnameOverride                                    | Override application full name                                                                                                                                      | nil     |
| rbac.create                                         | Whether to create and use RBAC resources, the default is true                                                                                                       | true    |
| securityContext                                     | Specify the Pod security context configuration of the application (e.g. fsGroup)                                                                                    | {}      |
| serviceAccount.create                               | Whether to create serviceaccount                                                                                                                                    | true    |
| serviceAccount.name                                 | Specify the name of the created serviceaccount                                                                                                                      | Automatic generated |
| serviceAccount.annotations                          | Specify adding the serviceaccount annotation tag                                                                                                                    | nil     |
| podAnnotations                                      | Specify the annotation label added to the pod                                                                                                                       | {}      |
| podLabels                                           | Specify the Label added to the pod                                                                                                                                  | {}      |
| replicaCount                                        | Number of controller copies                                                                                                                                         | 2       |
| nodeSelector                                        | The specified nodeSelector tag                                                                                                                                      | {}      |
| tolerations                                         | Specified taint tolerance configuration                                                                                                                             | []      |
| affinity                                            | Specified Pod affinity configuration                                                                                                                                | {}      |
| resources                                           | Specified Pod requests and limits configuration                                                                                                                     | requests: cpu 200m, memory 256Mi; limits: cpu 1000m, memory 1G |

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
        secretStoreRef: # not required when using WorkerRole authentication (enableWorkerRole must be set to true in values.yaml)
          name: scdemo
          namespace: default
  ```
- Execute the command to create a SecretStore test instance (not required when using WorkerRole authentication; WorkerRole is disabled by default, set `command.enableWorkerRole` to `true` in `values.yaml`):
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

### 3. Authentication Configuration

ack-secret-manager supports multiple authentication methods, including ServiceAccount RRSA, environment variable RRSA/OIDC, AK+AssumeRole, pure AccessKey, and ECS WorkerRole.

- For detailed authentication methods (including priority, configuration examples, and cross-account authentication): [Authentication Guide](docs/auth_guide.md)

### 4. Advanced Features

ack-secret-manager provides various advanced features. For detailed usage, please refer to the corresponding documentation:

| Feature | Description | Documentation |
|---------|-------------|---------------|
| JSON/YAML Parsing | Use `jmesPath` to extract JSON/YAML credential fields, use `dataProcess` for auto-parsing | [Advanced Usage](docs/advanced_usage.md#jsonyaml-credential-parsing) |
| Cross-Account Sync | Cross-account credential synchronization via `remoteRamRoleARN` | [Advanced Usage](docs/advanced_usage.md#cross-account-sync) |
| kmsEndpoint Configuration | Configure dedicated gateway, VPC endpoint, public endpoint, etc. | [Advanced Usage](docs/advanced_usage.md#kmsendpoint-configuration) |
| Credential Rotation | Configurable reconciliation and rotation intervals for continuous secret synchronization | [Advanced Usage](docs/advanced_usage.md#credential-rotation) |
| Multi-Data Source Support | Synchronize secrets from both KMS Secrets Manager and OOS Encryption Parameters | [Advanced Usage](docs/advanced_usage.md#multi-data-source-support) |
| Sync Failure Handling Semantics | How sync failures affect the cluster Secret: merge-write on partial failure, fail-closed for templated ExternalSecrets, zero-output protection | [Advanced Usage](docs/advanced_usage.md#sync-failure-handling-semantics) |
| Namespace Scope | Restrict the namespace scope of credential synchronization via `watchNamespaces` and `excludeNamespaces` | [Advanced Usage](docs/advanced_usage.md#namespace-scope) |

### 5. Template Processing

ack-secret-manager supports advanced template processing capabilities, allowing you to transform and customize secret data before creating Kubernetes Secrets. For Go template syntax, 70+ Sprig functions, `templateFrom`, `mergePolicy`, metadata templates, and custom functions, please refer to: [Template Processing Guide](docs/template_processing_guide.md)

### 6. CRD Resource Details

ack-secret-manager involves four CRD resources. For CRD overview, cross-namespace control mechanisms, security best practices, configuration examples, and field descriptions, please refer to: [CRD Resources Guide](docs/crd_resources_guide.md)

| CRD | Scope | Description |
|-----|-------|-------------|
| **SecretStore** | Namespace | Stores authentication information (RRSA, AK, ServiceAccountRef, etc.) |
| **ClusterSecretStore** | Cluster | Cluster-level authentication resource with access control, can be referenced by any namespace |
| **ExternalSecret** | Namespace | Defines credential information to sync (credential name, version, data source, etc.) |
| **ClusterExternalSecret** | Cluster | Automatically creates ExternalSecrets in matching namespaces |

## Security

Please report vulnerabilities by email to **kubernetes-security@service.aliyun.com**. Also see our [SECURITY.md](./SECURITY.md) file for details.

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
| `0.6.1`  | 2026/2/28 	| 1.Modify the namespace matching conditions for ClusterExternalSecret resources<br />2.Add secret and serviceaccount controllers to reconcile associated ClusterSecretStores and SecretStores when resources are updated<br />3.Optimize the token refresh time for serviceaccount authentication method |
| `0.6.2`  | 2026/3/2   | Support advanced template processing capabilities to transform and customize secret data before creating Kubernetes Secret when synchronizing external secrets |
| `0.6.3`  | 2026/6/30  | 1.Fixed ExternalSecret-level kmsEndpoint to take effect<br />2.Documentation restructure: added standalone auth guide, CRD resource guide, advanced usage guide and template processing guide (bilingual)<br />3.Added the examples directory with a complete example set<br />4.Other feature optimizations |
| `0.6.4`  | 2026/7/30  | **Breaking Change** (security hardening):<br />1.Cross-namespace references disabled by default (`enableCrossNamespaceSecretStore` and `enableCrossNamespaceAuthRef` changed from `true` to `false`). Set to `true` explicitly if needed.<br />2.Added KMS Endpoint SSRF validation.<br />3.Other feature optimizations and documentation/example adjustments |
| `0.6.5`  | 2026/8/10  | 1.Sync failure handling: merge-write on partial failure (successful keys get new values, failed keys retain previous values); ExternalSecrets with templates use fail-closed (write skipped, previous Secret retained); on total failure, behavior follows cleanupSecretOnFailure: false (default) skips the write and retains the previous Secret; true deletes the cluster Secret (the deletion also applies to templated ExternalSecrets; on partial failures the Secret is never deleted regardless of this flag)<br />2.Zero-output guard: when all sources succeed but produce 0 keys (e.g. backend document emptied), write is skipped and existing Secret is retained to prevent silent credential clearing. Known limitations: (1) when only some dataProcess[].extract entries produce zero keys, their keys are removed under normal all-success semantics and are not guarded; (2) when template rendering partially fails in Replace mode, only the successfully rendered keys are written as a reduced dataset, preserving the non-fatal template-execution semantics and surfacing only as a warning log<br />3.Unified backend retry strategy: KMS/OOS credential fetching uses unified exponential backoff retry (max 3 attempts, ~2s/4s intervals with jitter), cancellable during graceful shutdown<br />4.Duplicate backend key exemption: when the same backend key is referenced by multiple spec entries and one succeeds while another fails, the failure is not counted<br />5.Other feature optimizations |
| `0.6.6`  | 2026/8/14  | 1.Fix `status.synchronizationTime` freeze during steady-state polling: force-refresh sync timestamp after actual Secret writes<br />2.Fix authentication-related issues<br />3.Other feature optimizations |
| `0.6.7`  | 2026/8/21  | 1.Optimized leader election to prevent new pods from being permanently blocked due to stale old pod remaining.<br />2.**Sync timeliness enhancements**: a.SecretStore/ClusterSecretStore changes (creation, spec modifications, credential-rotation triggers, deletion) now immediately trigger a resync of the referencing ExternalSecrets (previously required waiting for a polling cycle); this event-driven sync remains effective even when automatic polling is disabled (`--disable-polling`). b.ClusterExternalSecret now immediately provisions child ExternalSecrets into namespaces that are newly created or relabeled to match its selector (previously required waiting for a polling cycle)<br />3.Namespace filtering fix: when only `--exclude-namespaces` is configured (without `--watch-namespaces`), no namespace was actually synchronized before (silent failure); after the fix, only namespaces in the exclude list are skipped and all other namespaces sync normally. Note: for deployments previously using only `--exclude-namespaces`, namespaces that were not synchronized before will start syncing after the upgrade — verify that the SecretStores/credentials in those namespaces are ready<br />4.The `enableWorkerRole` parameter default changed to false. To use WorkerRole authentication, explicitly set `enableWorkerRole` to `true` in supported clusters.<br />5.Fix known vulnerabilities<br />**Upgrade Note**: The old ConfigMap lock will not interfere with the new mechanism, and under normal circumstances it is automatically cleaned up by Kubernetes garbage collection (GC) when the old pods are deleted; only when stale pod objects in states such as Failed/Completed remain in the cluster will the lock linger, in which case manually run `kubectl delete configmap ack-secret-manager-lock -n <namespace>` to clean it up |

# E2E 测试前置条件说明

本文档说明执行 `test/e2e/` 目录下 E2E 测试的前置条件，可作为运行测试前的检查手册使用。所有结论均以当前代码为准。

## 1. 执行方式

| 命令 | 说明 |
| --- | --- |
| `make test-e2e` | 全量 E2E 测试，等价于 `go test ./test/e2e/... -v -ginkgo.v -timeout 3h` |
| `make test-e2e-template` | 模板处理子集测试，focus 为正则匹配 `Template Processing E2E`，会同时命中 `template_test.go` 与 `advanced_template_test.go` 两组用例 |
| `ginkgo -v -timeout 3h ./test/e2e` | 也可直接使用 ginkgo CLI 执行 |

## 2. 环境变量

| 变量名 | 必需性 | 用途 | 缺失行为 |
| --- | --- | --- | --- |
| `ALIBABA_CLOUD_ACCOUNT_ID` | 必需 | 阿里云主账号 ID，用于拼接 RAM 角色 ARN、OIDC Provider ARN 等 | BeforeSuite 直接 Fail |
| `CLUSTER_ID` | 必需 | ACK 集群 ID，用于获取 WorkerRole/VPC 信息、拼接 OIDC Provider 名称 `ack-rrsa-{clusterID}` | BeforeSuite 直接 Fail |
| `REGION` | 必需 | 拼接 KMS/OOS/CS 等服务 endpoint | BeforeSuite 直接 Fail，错误信息明确提及 REGION |
| `KMS_KEY_ID` | 必需 | 用于创建测试 KMS 凭据 | BeforeSuite 直接 Fail，错误信息明确提及 KMS_KEY_ID |
| `KMS_INSTANCE_ID` | 必需 | KMS 软件密钥实例 ID，用于构造专属网关 endpoint 等 | BeforeSuite 直接 Fail，错误信息明确提及 KMS_INSTANCE_ID |
| `KUBECONFIG` | 可选 | 指定集群访问配置 | 缺省回退 `~/.kube/config`，再回退 InClusterConfig |
| `ALIBABA_CLOUD_ACCESS_KEY_ID` / `ALIBABA_CLOUD_ACCESS_KEY_SECRET` | 必需（或等效凭据） | 测试进程自身调用 RAM/KMS/OOS/CS OpenAPI 的凭据，走 credentials-go 默认链 | 凭据链解析失败时相关用例无法执行 |
| `CROSS_ACCOUNT_ID`、`CROSS_ACCOUNT_ACCESS_KEY_ID`、`CROSS_ACCOUNT_ACCESS_KEY_SECRET` | 可选（三者需同时提供才生效） | 跨账号测试的目标账号 | 未提供时优雅跳过跨账号资源创建（目标账号 RAM 角色与 KMS Secret 均不创建），`cross_account_sync_test.go` 全部用例 Skip；若仅设置了旧变量名 `REMOTE_ACCOUNT_ID`/`REMOTE_ACCESS_KEY_ID`，BeforeSuite 会打印显著 WARNING 提示变量已重命名为 `CROSS_ACCOUNT_*`（不 Fail） |
| `CROSS_ACCOUNT_KMS_KEY_ID` / `CROSS_ACCOUNT_KMS_INSTANCE_ID` | 真跨账号模式下必需 | 真跨账号模式下在目标账号创建 KMS Secret 所需 | `CROSS_ACCOUNT_*` 已配置但缺失时 BeforeSuite Fail，错误信息明确列出缺失的变量名；`CROSS_ACCOUNT_*` 未配置时忽略（跨账号资源整体跳过） |

关于 AK 凭据的补充说明：

- 测试进程通过 credentials-go（v1.4.5，`credential.NewCredential(nil)`）默认凭据链获取 OpenAPI 凭据，查找顺序为：AK/STS 环境变量（`ALIBABA_CLOUD_ACCESS_KEY_ID`/`ALIBABA_CLOUD_ACCESS_KEY_SECRET`，可选 `ALIBABA_CLOUD_SECURITY_TOKEN`）→ OIDC 环境变量（`ALIBABA_CLOUD_ROLE_ARN`/`ALIBABA_CLOUD_OIDC_PROVIDER_ARN`/`ALIBABA_CLOUD_OIDC_TOKEN_FILE` 三者齐全）→ aliyun CLI 配置文件 `~/.aliyun/config.json`（**默认启用**，可用 `ALIBABA_CLOUD_CLI_PROFILE_DISABLED=true` 禁用）→ `~/.alibabacloud/credentials`（可用 `ALIBABA_CLOUD_CREDENTIALS_FILE` 覆盖路径）→ ECS 实例元数据 RAM Role（可用 `ALIBABA_CLOUD_ECS_METADATA_DISABLED=true` 禁用）→ 凭据 URI（`ALIBABA_CLOUD_CREDENTIALS_URI`，仅在设置时加入链尾）。不存在名为 `ALIBABA_CLOUD_CREDENTIALS_FILE_URI` 的环境变量。
- **会**读取 aliyun CLI 的配置文件 `~/.aliyun/config.json`：若本机存在该文件且设置了 AK 环境变量以外的凭据来源，测试进程可能实际命中 CLI profile；如需隔离本机 CLI 配置的影响，可设置 `ALIBABA_CLOUD_CLI_PROFILE_DISABLED=true`。
- 该凭据需具备 KMS（含 `CreateSecret`/`PutSecretValue`/`DeleteSecret`/`UpdateKmsInstanceBindVpc`）、OOS、RAM 全套管理权限，以及 CS `DescribeClusterDetail` 权限。

## 3. 集群与部署前置

- **必须是真实的 ACK 集群**：测试依赖 RRSA/OIDC、WorkerRole、节点元数据服务等云上能力，无法在 kind/minikube 等本地集群上运行。
- **`kube-system/ack-secret-manager` Deployment 必须已部署**：[suite_test.go](suite_test.go) 只 Get 该 Deployment，不会安装；缺失即 Fail。
- **4 个 CRD 必须预装**：`SecretStore`、`ExternalSecret`、`ClusterSecretStore`、`ClusterExternalSecret`。测试只检查不安装，缺失时提示 `please install CRDs first` 后 Fail。
- **初始部署使用默认参数即可**，测试具备环境自适应机制：
  - BeforeSuite 会自动 patch Deployment，注入 RRSA projected serviceAccountToken volume（audience 为 `sts.aliyuncs.com`，挂载到 `/var/run/secrets/tokens`），并自动创建 Secret `ack-secret-manager-rrsa-env`，以 secretKeyRef 方式注入 `ALICLOUD_ROLE_ARN`/`ALICLOUD_OIDC_PROVIDER_ARN`；AfterSuite 时清理。
  - `cross_namespace_ref_test.go` 用例会自行 patch `--enable-cross-namespace-*` 参数，用例结束后恢复。
  - `--enable-worker-role` 若被显式置为 `false`，WorkerRole 相关用例 Skip（默认值为 `true`）。
- **集群需资源充足、网络稳定**：测试会反复 patch Deployment 触发滚动更新，每次等待 rollout 完成（上限 120s）。

## 4. 云端资源前置

- **需预置 KMS 软件密钥实例及其中的对称密钥**，分别对应 `KMS_INSTANCE_ID` 与 `KMS_KEY_ID`。

  > ⚠️ **警告**：测试过程中会改写该实例的 VPC 绑定，并在结束时清空绑定。**切勿使用生产环境中正在绑定的 KMS 实例**。

- **以下资源由测试自动创建并自动清理**（资源名前缀为 `acksm-test-`）：
  - 约 26 个 KMS Secret；
  - 1 个 OOS 加密参数；
  - 多个 RAM 用户/角色/策略；
  - 向集群 WorkerRole 临时附加的 KMS/OOS/STS 权限。

## 5. 认证前置

按认证方式分别说明：

| 认证方式 | 前置条件 |
| --- | --- |
| ServiceAccount RRSA | 集群需开启 RRSA，即 OIDC Provider `ack-rrsa-{CLUSTER_ID}` 存在 |
| ENV RRSA | 同上，集群需开启 RRSA |
| AK 基础认证 | 无额外前置，测试自动创建 RAM 用户 |
| AK + AssumeRole | 无额外前置，测试自动创建 RAM 用户和角色 |
| WorkerRole | 依赖真实 ACK 集群的 Worker 角色与节点元数据服务；Deployment 需 `--enable-worker-role=true`（默认开启） |
| 跨账号 | 需配置 `CROSS_ACCOUNT_ID`/`CROSS_ACCOUNT_ACCESS_KEY_ID`/`CROSS_ACCOUNT_ACCESS_KEY_SECRET`，且真跨账号模式下还需 `CROSS_ACCOUNT_KMS_KEY_ID`/`CROSS_ACCOUNT_KMS_INSTANCE_ID`；未配置时相关用例 Skip，配置不完整（缺 `CROSS_ACCOUNT_KMS_*`）时 BeforeSuite Fail |

## 6. 工具链

| 工具 | 要求 | 说明 |
| --- | --- | --- |
| Go | ≥ 1.25.0 | 见 [go.mod](../../go.mod) |
| kubectl / helm | 可选 | 仅用于预部署 ack-secret-manager 组件与 CRD，不被测试代码直接依赖 |

首次运行前可执行 `go mod download` 预热依赖。

## 7. 运行前检查清单

- [ ] Go ≥ 1.25.0 已安装（`go version`）
- [ ] kubeconfig 可访问目标 ACK 集群（`KUBECONFIG` 或 `~/.kube/config`）
- [ ] `kube-system/ack-secret-manager` Deployment 已部署且运行正常（默认参数即可）
- [ ] 4 个 CRD 已安装（SecretStore / ExternalSecret / ClusterSecretStore / ClusterExternalSecret）
- [ ] 集群已开启 RRSA（OIDC Provider `ack-rrsa-{CLUSTER_ID}` 存在）
- [ ] 已设置 `ALIBABA_CLOUD_ACCOUNT_ID`、`CLUSTER_ID`、`REGION`
- [ ] 已设置 `KMS_KEY_ID`、`KMS_INSTANCE_ID`（使用专用测试实例，勿用生产绑定实例）
- [ ] OpenAPI 凭据可用（AK 环境变量或 credentials-go 默认链；默认链会依次尝试 OIDC 环境变量、`~/.aliyun/config.json`、`~/.alibabacloud/credentials`、ECS 实例元数据，注意本机 CLI profile 可能被命中，必要时设置 `ALIBABA_CLOUD_CLI_PROFILE_DISABLED=true`），且具备 KMS/OOS/RAM 管理权限与 CS `DescribeClusterDetail` 权限
- [ ] （可选）跨账号测试：已同时设置 `CROSS_ACCOUNT_ID`、`CROSS_ACCOUNT_ACCESS_KEY_ID`、`CROSS_ACCOUNT_ACCESS_KEY_SECRET`，及 `CROSS_ACCOUNT_KMS_KEY_ID`、`CROSS_ACCOUNT_KMS_INSTANCE_ID`
- [ ] 执行 `make test-e2e`（或 `make test-e2e-template` / `ginkgo -v -timeout 3h ./test/e2e`）



# ACK Secret Manager Cli

ACK Secret Manager Cli 帮助您简化 ACK Secret Manager 的配置与使用，并尽可能帮助您减少配置过程中的错误。

## 安装

```shell
cd cli
go install github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli
```

## 使用

1. 创建 RRSA 类型的凭据

   ![](./img/rrsa.gif)

2. 创建 RAM Role 类型的凭据（需要创建 Secret 额外保存敏感信息）

   ![](./img/ramrole.gif)

3. 创建跨账号类型的凭据（需要指定一个 SecretStore 添加跨账号配置，跨账号扮演另一个账号的角色）

   ![](./img/cross.gif)

4. 创建 ExternalSecret

   ![](./img/es.gif)




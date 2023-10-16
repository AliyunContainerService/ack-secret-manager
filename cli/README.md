

# ACK Secret Manager Cli

The ACK Secret Manager Cli helps simplify the configuration and usage of ACK Secret Manager, and strives to minimize errors during the configuration process.



## Install

```shell
go install github.com/AliyunContainerService/ack-secret-manager/cli/cmd/ack-secret-manager-cli.go
```

## Example

1. Create RRSA type credentials

   ![](./img/rrsa.gif)

2. Create RAM Role type credentials (need to create a Secret to save additional sensitive information)

   ![](./img/ramrole.gif)

3. Create cross-account type credentials (you need to specify a SecretStore to add cross-account configuration, and the cross-account assumes the role of another account)

   ![](./img/cross.gif)

4. Create ExternalSecret

   ![](./img/es.gif)




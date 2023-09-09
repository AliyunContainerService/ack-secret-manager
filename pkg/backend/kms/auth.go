package kms

import (
	"time"

	"github.com/aliyun/credentials-go/credentials"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
)

const (
	RamRoleARNAuthType  = "ram_role_arn"
	AKAuthType          = "access_key"
	EcsRamRoleAuthType  = "ecs_ram_role"
	OidcAuthType        = "oidc_role_arn"
	oidcRoleSessionName = "ack-secret-manager"
	oidcTokenFilePath   = "/var/run/secrets/tokens/ack-secret-manager"
)

type authConfig struct {
	clientName            string
	roleArn               string
	oidcArn               string
	accessKey             string
	accessSecretKey       string
	roleSessionName       string
	roleSessionExpiration string
	remoteRoleArn         string
	remoteRoleSessionName string
	refreshPeriod         time.Duration
}

func (a *authConfig) getKMSAuthCred(region string, p *Provider) (credentials.Credential, error) {
	providers := make([]provider.CredentialsProvider, 0)
	if a.oidcArn != "" && a.roleArn != "" {
		oidcProvider := provider.NewOIDCProvider(provider.OIDCProviderOptions{
			STSEndpoint:     provider.GetSTSEndpoint(region, true),
			SessionName:     oidcRoleSessionName,
			OIDCTokenFile:   oidcTokenFilePath,
			RoleArn:         a.roleArn,
			OIDCProviderArn: a.oidcArn,
			RefreshPeriod:   a.refreshPeriod,
		})
		providers = append(providers, oidcProvider)
	}
	if a.accessKey != "" && a.accessSecretKey != "" && a.roleSessionName != "" && a.roleArn != "" {
		ramRoleProvider := provider.NewRoleArnProvider(provider.NewAccessKeyProvider(a.accessKey, a.accessSecretKey), a.roleArn, provider.RoleArnProviderOptions{
			STSEndpoint:   provider.GetSTSEndpoint(region, true),
			SessionName:   a.roleSessionName,
			RefreshPeriod: a.refreshPeriod,
		})
		providers = append(providers, ramRoleProvider)
	}
	if a.accessKey != "" && a.accessSecretKey != "" {
		akProvider := provider.NewAccessKeyProvider(a.accessKey, a.accessSecretKey)
		providers = append(providers, akProvider)
	}
	providers = append(providers, provider.NewECSMetadataProvider(provider.ECSMetadataProviderOptions{
		RefreshPeriod: a.refreshPeriod,
	}))
	chainProvider := provider.NewChainProvider(providers...)
	var remoteRoleProvider *provider.RoleArnProvider
	if a.remoteRoleArn != "" && a.remoteRoleSessionName != "" {
		remoteRoleProvider = provider.NewRoleArnProvider(chainProvider, a.remoteRoleArn, provider.RoleArnProviderOptions{
			STSEndpoint:   provider.GetSTSEndpoint(region, true),
			SessionName:   a.remoteRoleSessionName,
			RefreshPeriod: a.refreshPeriod,
		})
	}
	var cred *provider.CredentialForV2SDK
	if remoteRoleProvider != nil {
		p.RegisterRamProvider(a.clientName, remoteRoleProvider)
		cred = provider.NewCredentialForV2SDK(remoteRoleProvider, provider.CredentialForV2SDKOptions{})
	} else {
		p.RegisterRamProvider(a.clientName, chainProvider)
		cred = provider.NewCredentialForV2SDK(chainProvider, provider.CredentialForV2SDKOptions{})
	}
	return cred, nil
}

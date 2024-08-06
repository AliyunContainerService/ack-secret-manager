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
	var semaphoreProvider *provider.SemaphoreProvider
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
	var cred *provider.CredentialForV2SDK
	if a.remoteRoleArn != "" && a.remoteRoleSessionName != "" {
		remoteRoleProvider = provider.NewRoleArnProvider(chainProvider, a.remoteRoleArn, provider.RoleArnProviderOptions{
			STSEndpoint:   provider.GetSTSEndpoint(region, true),
			SessionName:   a.remoteRoleSessionName,
			RefreshPeriod: a.refreshPeriod,
		})
		semaphoreProvider = provider.NewSemaphoreProvider(remoteRoleProvider, provider.SemaphoreProviderOptions{
			MaxWeight: int64(p.maxConcurrentCount),
		})
	} else {
		semaphoreProvider = provider.NewSemaphoreProvider(chainProvider, provider.SemaphoreProviderOptions{
			MaxWeight: int64(p.maxConcurrentCount),
		})
	}
	p.RegisterRamProvider(a.clientName, semaphoreProvider)
	cred = provider.NewCredentialForV2SDK(semaphoreProvider, provider.CredentialForV2SDKOptions{
		CredentialRetrievalTimeout: 10 * time.Minute,
	})
	return cred, nil
}

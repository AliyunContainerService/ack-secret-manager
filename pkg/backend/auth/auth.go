package auth

import (
	"errors"
	"os"
	"time"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	"github.com/aliyun/credentials-go/credentials"
)

const (
	RamRoleARNAuthType  = "ram_role_arn"
	AKAuthType          = "access_key"
	EcsRamRoleAuthType  = "ecs_ram_role"
	OidcAuthType        = "oidc_role_arn"
	oidcRoleSessionName = "ack-secret-manager"
	oidcTokenFilePath   = "/var/run/secrets/tokens/ack-secret-manager"
)

type AuthConfig struct {
	ClientName            string
	RoleArn               string
	OidcArn               string
	AccessKey             string
	AccessSecretKey       string
	RoleSessionName       string
	RoleSessionExpiration string
	RemoteRoleArn         string
	RemoteRoleSessionName string
	RefreshPeriod         time.Duration
}

func (a *AuthConfig) GetAuthCred(region string, maxConcurrentCount int, m *backendp.Manager) (credentials.Credential, error) {
	providers := make([]provider.CredentialsProvider, 0)
	var semaphoreProvider *provider.SemaphoreProvider
	if a.OidcArn != "" && a.RoleArn != "" {
		oidcProvider := provider.NewOIDCProvider(provider.OIDCProviderOptions{
			STSEndpoint:     provider.GetSTSEndpoint(region, true),
			SessionName:     oidcRoleSessionName,
			OIDCTokenFile:   oidcTokenFilePath,
			RoleArn:         a.RoleArn,
			OIDCProviderArn: a.OidcArn,
			RefreshPeriod:   a.RefreshPeriod,
		})
		providers = append(providers, oidcProvider)
	}
	if a.AccessKey != "" && a.AccessSecretKey != "" && a.RoleSessionName != "" && a.RoleArn != "" {
		ramRoleProvider := provider.NewRoleArnProvider(provider.NewAccessKeyProvider(a.AccessKey, a.AccessSecretKey), a.RoleArn, provider.RoleArnProviderOptions{
			STSEndpoint:   provider.GetSTSEndpoint(region, true),
			SessionName:   a.RoleSessionName,
			RefreshPeriod: a.RefreshPeriod,
		})
		providers = append(providers, ramRoleProvider)
	}
	if a.AccessKey != "" && a.AccessSecretKey != "" {
		akProvider := provider.NewAccessKeyProvider(a.AccessKey, a.AccessSecretKey)
		providers = append(providers, akProvider)
	}

	if backend.EnableWorkerRole {
		providers = append(providers, provider.NewECSMetadataProvider(provider.ECSMetadataProviderOptions{
			RefreshPeriod: a.RefreshPeriod,
		}))
	} else {
		if len(providers) == 0 {
			return nil, errors.New("Please set auth config when EnableWorkerRole is false")
		}
	}

	chainProvider := provider.NewChainProvider(providers...)
	var remoteRoleProvider *provider.RoleArnProvider
	var cred *provider.CredentialForV2SDK
	if a.RemoteRoleArn != "" && a.RemoteRoleSessionName != "" {
		remoteRoleProvider = provider.NewRoleArnProvider(chainProvider, a.RemoteRoleArn, provider.RoleArnProviderOptions{
			STSEndpoint:   provider.GetSTSEndpoint(region, true),
			SessionName:   a.RemoteRoleSessionName,
			RefreshPeriod: a.RefreshPeriod,
		})
		semaphoreProvider = provider.NewSemaphoreProvider(remoteRoleProvider, provider.SemaphoreProviderOptions{
			MaxWeight: int64(maxConcurrentCount),
		})
	} else {
		semaphoreProvider = provider.NewSemaphoreProvider(chainProvider, provider.SemaphoreProviderOptions{
			MaxWeight: int64(maxConcurrentCount),
		})
	}
	backendp.RegisterRamProvider(a.ClientName, semaphoreProvider, m)
	cred = provider.NewCredentialForV2SDK(semaphoreProvider, provider.CredentialForV2SDKOptions{
		CredentialRetrievalTimeout: 10 * time.Minute,
	})
	return cred, nil
}

func GetCredentialParameterFromEnv() AuthConfig {
	return AuthConfig{
		ClientName:            backend.EnvClient,
		RoleArn:               os.Getenv("ALICLOUD_ROLE_ARN"),
		OidcArn:               os.Getenv("ALICLOUD_OIDC_PROVIDER_ARN"),
		AccessKey:             os.Getenv("ACCESS_KEY_ID"),
		AccessSecretKey:       os.Getenv("SECRET_ACCESS_KEY"),
		RoleSessionName:       os.Getenv("ALICLOUD_ROLE_SESSION_NAME"),
		RoleSessionExpiration: os.Getenv("ALICLOUD_ROLE_SESSION_EXPIRATION"),
		RemoteRoleSessionName: os.Getenv("ALICLOUD_REMOTE_ROLE_SESSION_NAME"),
		RemoteRoleArn:         os.Getenv("ALICLOUD_REMOTE_ROLE_ARN"),
		RefreshPeriod:         time.Second * 10,
	}
}

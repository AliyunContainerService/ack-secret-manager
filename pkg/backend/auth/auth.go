package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	sts "github.com/alibabacloud-go/sts-20150401/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	credential "github.com/aliyun/credentials-go/credentials"
	"github.com/golang-jwt/jwt/v5"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
)

const (
	RamRoleARNAuthType  = "ram_role_arn"
	AKAuthType          = "access_key"
	EcsRamRoleAuthType  = "ecs_ram_role"
	OidcAuthType        = "oidc_role_arn"
	oidcRoleSessionName = "ack-secret-manager"
	oidcTokenFilePath   = "/var/run/secrets/tokens/ack-secret-manager"
)

// TokenOIDCProvider implements OIDC provider based on dynamic token
type TokenOIDCProvider struct {
	stsEndpoint     string
	sessionName     string
	token           string
	roleArn         string
	oidcProviderArn string
	refreshPeriod   time.Duration

	// Fields for dynamic token refresh
	getTokenFunc    func() (string, error)
	tokenExpireTime time.Time // When the current token expires

	// Cached credential and expiration time
	credential credential.Credential
	expireTime time.Time
}

// NewTokenOIDCProvider creates a new OIDC provider based on token
func NewTokenOIDCProvider(stsEndpoint, sessionName, token, roleArn, oidcProviderArn string, refreshPeriod time.Duration) *TokenOIDCProvider {
	// Estimate token expiration time, default 1 hour
	tokenExpireTime := time.Now().Add(1 * time.Hour)
	if exp, err := getTokenExpireTime(token); err == nil {
		tokenExpireTime = exp
	}

	return &TokenOIDCProvider{
		stsEndpoint:     stsEndpoint,
		sessionName:     sessionName,
		token:           token,
		roleArn:         roleArn,
		oidcProviderArn: oidcProviderArn,
		refreshPeriod:   refreshPeriod,
		tokenExpireTime: tokenExpireTime,
	}
}

// Credentials implements the provider.CredentialsProvider interface
func (p *TokenOIDCProvider) Credentials(context.Context) (*provider.Credentials, error) {
	cred, err := p.GetCredential()
	if err != nil {
		return nil, err
	}

	// Convert credentials.Credential to *provider.Credentials
	// Using recommended way to get credential values instead of deprecated methods
	credentialInfo, err := cred.GetCredential()
	if err != nil {
		return nil, fmt.Errorf("failed to get credential info: %v", err)
	}

	return &provider.Credentials{
		AccessKeyId:     tea.StringValue(credentialInfo.AccessKeyId),
		AccessKeySecret: tea.StringValue(credentialInfo.AccessKeySecret),
		SecurityToken:   tea.StringValue(credentialInfo.SecurityToken),
	}, nil
}

// GetCredential gets credential from STS service using OIDC token
func (p *TokenOIDCProvider) GetCredential() (credential.Credential, error) {
	// check if token refresh function is available
	needTokenRefresh := p.getTokenFunc != nil && time.Now().After(p.tokenExpireTime.Add(-5*time.Minute))

	// check if need to refresh credential
	needCredentialRefresh := p.credential == nil || time.Now().After(p.expireTime.Add(-5*time.Minute))

	if !needTokenRefresh && !needCredentialRefresh {
		return p.credential, nil
	}

	// refresh OIDC token if needed
	if needTokenRefresh {
		newToken, err := p.getTokenFunc()
		if err != nil {
			// if token refresh fails, use existing credential if still valid
			if p.credential != nil && time.Now().Before(p.expireTime) {
				klog.Warningf("Failed to refresh OIDC token, using existing credential: %v", err)
				// update last token time to avoid frequent refresh attempts
				return p.credential, nil
			}
			return nil, fmt.Errorf("failed to refresh OIDC token: %v", err)
		}

		p.token = newToken

		// update token expiration time
		if exp, err := getTokenExpireTime(newToken); err == nil {
			p.tokenExpireTime = exp
		} else {
			// if failed to parse token expiration, use default 1 hour
			p.tokenExpireTime = time.Now().Add(1 * time.Hour)
		}

		klog.Infof("Successfully refreshed OIDC token, next refresh in %v", p.tokenExpireTime.Add(-5*time.Minute))
	}

	// create STS client
	cfg := &openapi.Config{
		Endpoint: tea.String(p.stsEndpoint),
	}
	stsClient, err := sts.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create STS client: %v", err)
	}

	// call AssumeRoleWithOIDC API
	response, err := stsClient.AssumeRoleWithOIDC(&sts.AssumeRoleWithOIDCRequest{
		RoleArn:         tea.String(p.roleArn),
		OIDCProviderArn: tea.String(p.oidcProviderArn),
		OIDCToken:       tea.String(p.token),
		RoleSessionName: tea.String(p.sessionName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to assume role with oidc: %v", err)
	}

	// create sts credential object
	config := &credential.Config{
		Type:            tea.String("sts"),
		AccessKeyId:     response.Body.Credentials.AccessKeyId,
		AccessKeySecret: response.Body.Credentials.AccessKeySecret,
		SecurityToken:   response.Body.Credentials.SecurityToken,
	}

	cred, err := credential.NewCredential(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create sts credential: %v", err)
	}

	// update cached credential and expiration time
	p.credential = cred
	expireTime, err := time.Parse(time.RFC3339, tea.StringValue(response.Body.Credentials.Expiration))
	if err != nil {
		return nil, fmt.Errorf("failed to parse credential expiration time: %v", err)
	}
	p.expireTime = expireTime

	return cred, nil
}

// getTokenExpireTime extracts the expiration time from a JWT token
func getTokenExpireTime(token string) (time.Time, error) {
	// Parse the token without verifying the signature
	parsedToken, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse JWT token: %v", err)
	}

	// Get expiration time directly using the library's method
	expirationTime, err := parsedToken.Claims.GetExpirationTime()
	if err != nil || expirationTime == nil {
		return time.Time{}, fmt.Errorf("failed to get expiration time form token: %v", err)
	}

	return expirationTime.Time, nil
}

type AuthConfig struct {
	ClientName              string
	RoleArn                 string
	OidcArn                 string
	AccessKey               string
	AccessSecretKey         string
	RoleSessionName         string
	RoleSessionExpiration   string
	RemoteRoleArn           string
	RemoteRoleSessionName   string
	RefreshPeriod           time.Duration
	TokenFilePath           string
	ServiceAccountName      string
	ServiceAccountNamespace string
	KubeClient              kubernetes.Interface
	TokenAudiences          []string
}

func (a *AuthConfig) GetAuthCred(region string, maxConcurrentCount int, m *backendp.Manager) (credential.Credential, error) {
	providers := make([]provider.CredentialsProvider, 0)
	var semaphoreProvider *provider.SemaphoreProvider

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

	// Handle OIDC authentication
	if a.OidcArn != "" && a.RoleArn != "" {
		oidcProvider, err := a.createOIDCProvider(region)
		if err != nil {
			klog.Errorf("Failed to create OIDC provider: %v", err)
		} else if oidcProvider != nil {
			providers = append(providers, oidcProvider)
		}
	}

	if backend.EnableWorkerRole {
		providers = append(providers, provider.NewECSMetadataProvider(provider.ECSMetadataProviderOptions{
			RefreshPeriod: a.RefreshPeriod,
		}))
	} else {
		if len(providers) == 0 {
			return nil, errors.New("please set auth config when EnableWorkerRole is false")
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

// createOIDCProvider creates an OIDC provider with either dynamic token or file-based token
func (a *AuthConfig) createOIDCProvider(region string) (provider.CredentialsProvider, error) {
	// Always try to get fresh dynamic token when ServiceAccount info is configured
	if a.ServiceAccountName != "" && a.ServiceAccountNamespace != "" && a.KubeClient != nil {
		dynamicToken, err := a.getIdentityToken()
		if err != nil {
			// When ServiceAccountRef is explicitly used, if dynamic token acquisition fails, return error directly
			return nil, fmt.Errorf("failed to get dynamic token for ServiceAccount %s/%s: %v", a.ServiceAccountNamespace, a.ServiceAccountName, err)
		}

		if dynamicToken == "" {
			return nil, fmt.Errorf("dynamic token is empty for ServiceAccount %s/%s", a.ServiceAccountNamespace, a.ServiceAccountName)
		}

		// Use TokenOIDCProvider to avoid creating temporary files
		oidcProvider := NewTokenOIDCProvider(
			provider.GetSTSEndpoint(region, true),
			oidcRoleSessionName,
			dynamicToken,
			a.RoleArn,
			a.OidcArn,
			a.RefreshPeriod,
		)

		// Set the token refresh function
		oidcProvider.getTokenFunc = a.getIdentityToken

		return provider.CredentialsProvider(oidcProvider), nil
	}

	// Use file-based token when ServiceAccount info is not configured
	tokenFilePath := oidcTokenFilePath
	if a.TokenFilePath != "" {
		tokenFilePath = a.TokenFilePath
	}

	oidcProvider := provider.NewOIDCProvider(provider.OIDCProviderOptions{
		STSEndpoint:     provider.GetSTSEndpoint(region, true),
		SessionName:     oidcRoleSessionName,
		OIDCTokenFile:   tokenFilePath,
		RoleArn:         a.RoleArn,
		OIDCProviderArn: a.OidcArn,
		RefreshPeriod:   a.RefreshPeriod,
	})

	return oidcProvider, nil
}

// getIdentityToken gets dynamic token for specific ServiceAccount
func (a *AuthConfig) getIdentityToken() (string, error) {
	// Create TokenRequest object
	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences: a.TokenAudiences,
		},
	}

	// Use default value if audiences are not specified
	if len(a.TokenAudiences) == 0 {
		tokenRequest.Spec.Audiences = []string{"sts.aliyuncs.com"}
	}

	// Request to get ServiceAccount token
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokenResp, err := a.KubeClient.CoreV1().ServiceAccounts(a.ServiceAccountNamespace).CreateToken(ctx, a.ServiceAccountName, tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to fetch token for ServiceAccount %s/%s: %w", a.ServiceAccountNamespace, a.ServiceAccountName, err)
	}

	return tokenResp.Status.Token, nil
}

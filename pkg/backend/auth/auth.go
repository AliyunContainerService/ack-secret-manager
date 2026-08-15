package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
	RamRoleARNAuthType     = "ram_role_arn"
	AKAuthType             = "access_key"
	EcsRamRoleAuthType     = "ecs_ram_role"
	OidcAuthType           = "oidc_role_arn"
	oidcRoleSessionName    = "ack-secret-manager"
	defaultRoleSessionName = "ack-secret-manager"
	oidcTokenFilePath      = "/var/run/secrets/tokens/ack-secret-manager"
)

// TokenOIDCProvider implements OIDC provider based on dynamic token
type TokenOIDCProvider struct {
	stsEndpoint     string
	sessionName     string
	token           string
	roleArn         string
	oidcProviderArn string
	refreshPeriod   time.Duration

	// mu serializes reads and updates of token/credential/expireTime/tokenExpireTime
	mu sync.Mutex

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

// GetCredential gets credential from STS service using OIDC token.
// All reads and updates of the cached token/credential state are serialized
// by p.mu: the freshness check is re-evaluated after acquiring the lock, so
// concurrent callers never observe partially updated state and only one
// refresh runs at a time.
func (p *TokenOIDCProvider) GetCredential() (credential.Credential, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Define refresh threshold: refresh when 20% of validity period remains
	// This ensures that we refresh credentials proportionally to their validity duration
	const refreshPercentage = 0.2

	// check if token refresh function is available
	tokenRemaining := time.Until(p.tokenExpireTime)
	tokenRefreshAmount := time.Duration(float64(tokenRemaining) * refreshPercentage)
	// If token is already expired, refresh immediately
	if tokenRefreshAmount <= 0 {
		tokenRefreshAmount = time.Nanosecond // Use minimal positive duration to calculate refresh amount
	}
	needTokenRefresh := p.getTokenFunc != nil && time.Now().After(p.tokenExpireTime.Add(-tokenRefreshAmount))

	// check if need to refresh credential
	credRemaining := time.Until(p.expireTime)
	credRefreshAmount := time.Duration(float64(credRemaining) * refreshPercentage)
	// If credential is already expired, refresh immediately
	if credRefreshAmount <= 0 {
		credRefreshAmount = time.Nanosecond // Use minimal positive duration to calculate refresh amount
	}
	needCredentialRefresh := p.credential == nil || time.Now().After(p.expireTime.Add(-credRefreshAmount))

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
				// The cached credential is still valid: keep serving it as-is;
				// token refresh will be retried on the next GetCredential call.
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

		klog.Infof("Successfully refreshed OIDC token")
	}

	// create STS client
	cfg := &openapi.Config{
		Endpoint: tea.String(p.stsEndpoint),
		// Bound the worst-case blocking time of the STS network call below.
		// Without explicit timeouts a degraded STS endpoint could block this
		// refresh indefinitely while holding p.mu, serializing every
		// concurrent GetCredential caller of the same provider forever.
		// Units are milliseconds (10s connect / 30s read).
		ConnectTimeout: tea.Int(10 * 1000),
		ReadTimeout:    tea.Int(30 * 1000),
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
	ClientName string
	RoleArn    string
	OidcArn    string
	// OidcArnFromDefault reports whether OidcArn was auto-generated from
	// cluster-id/uid; an auto-derived OidcArn combined with a complete AK
	// pair leaves AK+AssumeRole in charge (see oidcTierAllowed).
	OidcArnFromDefault      bool
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

// NormalizeSessionNames fills the default role session name into
// RoleSessionName and RemoteRoleSessionName when they are empty, so that the
// AK+AssumeRole and cross-account tiers of the authentication chain are not
// silently skipped just because a session name was omitted. It never
// overrides explicitly configured values.
func (a *AuthConfig) NormalizeSessionNames() {
	if a.RoleSessionName == "" {
		a.RoleSessionName = defaultRoleSessionName
	}
	if a.RemoteRoleSessionName == "" {
		a.RemoteRoleSessionName = defaultRoleSessionName
	}
}

func (a *AuthConfig) GetAuthCred(region string, maxConcurrentCount int, m *backendp.Manager) (credential.Credential, error) {
	providers := make([]provider.CredentialsProvider, 0)
	var semaphoreProvider *provider.SemaphoreProvider

	// Normalize session names at the entry so missing session names do not
	// cause the AK+AssumeRole / cross-account tiers to be skipped.
	// The authentication chain order below is unchanged.
	a.NormalizeSessionNames()

	// Fail-closed precondition, evaluated BEFORE the OIDC block: when an
	// explicit serviceAccountRef is configured but the OIDC prerequisites
	// are missing (OidcArn empty -- e.g. cluster-id/uid could not be
	// resolved -- or RoleArn empty), the OIDC block below would be skipped
	// entirely and the chain would silently fall back to lower-priority
	// authentication methods. With an explicitly configured serviceAccountRef,
	// SA RRSA failures are fail-closed: the ExternalSecret fails instead of
	// silently degrading to other authentication methods.
	if a.ServiceAccountName != "" && (a.OidcArn == "" || a.RoleArn == "") {
		return nil, fmt.Errorf("ServiceAccount %s/%s is explicitly configured but OIDC prerequisites are missing (oidcProviderArn=%q, roleArn=%q; cluster-id/uid may be unavailable)",
			a.ServiceAccountNamespace, a.ServiceAccountName, a.OidcArn, a.RoleArn)
	}

	// Authentication chain order: ServiceAccount RRSA -> OIDC/RRSA -> RAM Role (AK+AssumeRole) -> AccessKey -> ECS Role (WorkerRole)

	// Priority 1: OIDC/RRSA (SA dynamic token preferred over file-based token).
	// With an auto-derived OidcArn plus a complete AK pair, AK+AssumeRole
	// takes precedence (0.6.2 contract); explicit config is unaffected.
	// See oidcTierAllowed.
	oidcAllowed := a.oidcTierAllowed()
	if !oidcAllowed && a.OidcArnFromDefault && a.AccessKey != "" && a.AccessSecretKey != "" && a.RoleArn != "" && a.ServiceAccountName == "" {
		klog.Infof("auto-derived oidcProviderARN with complete AccessKey: AK AssumeRole takes precedence over file-based RRSA. To use RRSA instead, explicitly configure oidcProviderARN, configure serviceAccountRef, or remove the AccessKey fields.")
	}
	if oidcAllowed {
		oidcProvider, err := a.createOIDCProvider(region)
		if err != nil {
			// When serviceAccountRef is explicitly configured, SA RRSA failures
			// are fail-closed: return the error instead of swallowing it and
			// falling back to lower-priority authentication methods.
			if a.ServiceAccountName != "" {
				return nil, fmt.Errorf("failed to create OIDC provider for ServiceAccount %s/%s: %v", a.ServiceAccountNamespace, a.ServiceAccountName, err)
			}
			klog.Errorf("OIDC authentication is unavailable: %v; using alternative authentication", err)
		} else if oidcProvider != nil {
			providers = append(providers, oidcProvider)
			if a.ServiceAccountName != "" && a.ServiceAccountNamespace != "" {
				klog.Infof("OIDC/RRSA authentication registered for %s/%s", a.ServiceAccountNamespace, a.ServiceAccountName)
			} else {
				klog.Infof("OIDC/RRSA authentication registered")
			}
		}
	}

	// Priority 2: AccessKey + AssumeRole (cross-account scenario)
	if a.AccessKey != "" && a.AccessSecretKey != "" && a.RoleSessionName != "" && a.RoleArn != "" {
		ramRoleProvider := provider.NewRoleArnProvider(provider.NewAccessKeyProvider(a.AccessKey, a.AccessSecretKey), a.RoleArn, provider.RoleArnProviderOptions{
			STSEndpoint:   provider.GetSTSEndpoint(region, true),
			SessionName:   a.RoleSessionName,
			RefreshPeriod: a.RefreshPeriod,
		})
		providers = append(providers, ramRoleProvider)
	}

	// Priority 3: Pure AccessKey (not recommended for production)
	if a.AccessKey != "" && a.AccessSecretKey != "" {
		akProvider := provider.NewAccessKeyProvider(a.AccessKey, a.AccessSecretKey)
		providers = append(providers, akProvider)
	}

	// Priority 4: WorkerRole/ECS RAM Role (fallback, simplest deployment)
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

// oidcTierAllowed reports whether the OIDC/RRSA tier (Priority 1) may enter
// the authentication chain, per the explicit-intent rules restoring the
// 0.6.2 contract. Decision order:
//  1. OidcArn or RoleArn empty -> false (prerequisites missing).
//  2. ServiceAccountName configured -> true (SA RRSA uses dynamic tokens).
//  3. OidcArn explicitly configured (!OidcArnFromDefault) -> true, fail-closed.
//  4. Incomplete AK pair -> true (dropping the tier would silently degrade).
//  5. Auto-derived OidcArn + complete AK pair -> false (AK AssumeRole
//     precedes auto-derived file-based RRSA); RRSA here requires an explicit
//     oidcProviderARN (rule 3) or serviceAccountRef (rule 2).
func (a *AuthConfig) oidcTierAllowed() bool {
	if a.OidcArn == "" || a.RoleArn == "" {
		return false
	}
	if a.ServiceAccountName != "" {
		return true
	}
	if !a.OidcArnFromDefault {
		return true
	}
	if a.AccessKey == "" || a.AccessSecretKey == "" {
		return true
	}
	// restore 0.6.2 contract: AK AssumeRole takes precedence over
	// auto-derived file-based RRSA
	return false
}

// createOIDCProvider creates an OIDC provider with either dynamic token or file-based token
func (a *AuthConfig) createOIDCProvider(region string) (provider.CredentialsProvider, error) {
	// Always try to get fresh dynamic token when ServiceAccount info is configured
	if a.ServiceAccountName != "" && a.ServiceAccountNamespace != "" && a.KubeClient != nil {
		dynamicToken, err := a.getIdentityToken()
		if err != nil {
			// When ServiceAccountRef is explicitly used, if dynamic token acquisition fails, return error directly
			return nil, fmt.Errorf("failed to obtain ServiceAccount identity token for %s/%s: %v", a.ServiceAccountNamespace, a.ServiceAccountName, err)
		}

		if dynamicToken == "" {
			return nil, fmt.Errorf("ServiceAccount identity token is empty for %s/%s", a.ServiceAccountNamespace, a.ServiceAccountName)
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

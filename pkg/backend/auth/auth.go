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

	// oidcProviderCreationErrPrefix is the unified error prefix for OIDC
	// provider creation failures, identical for the KMS and OOS client
	// paths (both flow through GetAuthCred); E2E contracts match on it.
	oidcProviderCreationErrPrefix = "failed to create OIDC provider"
)

// TokenOIDCProvider implements an OIDC provider based on dynamic token
type TokenOIDCProvider struct {
	stsEndpoint     string
	sessionName     string
	token           string
	roleArn         string
	oidcProviderArn string

	// mu serializes all reads/updates of the token/credential state below
	mu sync.Mutex

	getTokenFunc    func() (string, error)
	tokenExpireTime time.Time
	// tokenIssuedAt is zero until the first successful refresh; while
	// unknown the refresh window falls back to the remaining-time formula.
	tokenIssuedAt time.Time

	credential credential.Credential
	expireTime time.Time
	// credentialIssuedAt is zero until the first successful refresh.
	credentialIssuedAt time.Time
}

// NewTokenOIDCProvider creates a token-based OIDC provider
func NewTokenOIDCProvider(stsEndpoint, sessionName, token, roleArn, oidcProviderArn string) *TokenOIDCProvider {
	// Default 1 hour when the token expiration cannot be parsed
	tokenExpireTime := time.Now().Add(1 * time.Hour)
	if exp, err := getTokenExpireTime(token); err == nil {
		tokenExpireTime = exp
	} else {
		klog.Warningf("failed to parse OIDC token expiration time, falling back to default 1h refresh window: %v", err)
	}

	return &TokenOIDCProvider{
		stsEndpoint:     stsEndpoint,
		sessionName:     sessionName,
		token:           token,
		roleArn:         roleArn,
		oidcProviderArn: oidcProviderArn,
		tokenExpireTime: tokenExpireTime,
	}
}

// Credentials implements provider.CredentialsProvider
func (p *TokenOIDCProvider) Credentials(context.Context) (*provider.Credentials, error) {
	cred, err := p.GetCredential()
	if err != nil {
		return nil, err
	}

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

// refreshPercentage is the fraction of the TOTAL validity window at whose
// end a proactive refresh is triggered.
const refreshPercentage = 0.2

// refreshWindow computes how far before expire a refresh should start. With
// a known issuedAt the window is refreshPercentage of the TOTAL validity
// duration, so refresh triggers once remaining validity enters the last 20%
// (the remaining-time-only formula degenerates and would refresh only after
// expiration); with unknown issuedAt the legacy remaining-time window is
// kept. Lower bound: whenever the computed window is <= 0 - the item is
// already expired or its validity is so short that the percentage truncates
// to zero - the minimal positive window is returned, which means "refresh
// immediately".
func refreshWindow(expire, issuedAt, now time.Time) time.Duration {
	remaining := expire.Sub(now)
	if remaining <= 0 {
		return time.Nanosecond // already expired: refresh immediately
	}
	if issuedAt.IsZero() {
		window := time.Duration(float64(remaining) * refreshPercentage)
		if window <= 0 {
			window = time.Nanosecond
		}
		return window
	}
	total := expire.Sub(issuedAt)
	if total <= 0 {
		return time.Nanosecond
	}
	window := time.Duration(float64(total) * refreshPercentage)
	if window <= 0 {
		window = time.Nanosecond
	}
	return window
}

// GetCredential gets a credential from STS using the OIDC token. All cached
// state access is serialized by p.mu, and freshness is re-checked under the
// lock, so only one refresh runs at a time.
func (p *TokenOIDCProvider) GetCredential() (credential.Credential, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()

	// Refresh once remaining validity enters the last 20% of the total window
	tokenRefreshAmount := refreshWindow(p.tokenExpireTime, p.tokenIssuedAt, now)
	needTokenRefresh := p.getTokenFunc != nil && now.After(p.tokenExpireTime.Add(-tokenRefreshAmount))

	credRefreshAmount := refreshWindow(p.expireTime, p.credentialIssuedAt, now)
	needCredentialRefresh := p.credential == nil || now.After(p.expireTime.Add(-credRefreshAmount))

	if !needTokenRefresh && !needCredentialRefresh {
		return p.credential, nil
	}

	if needTokenRefresh {
		newToken, err := p.getTokenFunc()
		if err != nil {
			// On refresh failure keep serving the still-valid cached credential
			if p.credential != nil && time.Now().Before(p.expireTime) {
				klog.Warningf("Failed to refresh OIDC token, using existing credential: %v", err)
				return p.credential, nil
			}
			return nil, fmt.Errorf("failed to refresh OIDC token: %v", err)
		}

		p.token = newToken

		if exp, err := getTokenExpireTime(newToken); err == nil {
			p.tokenExpireTime = exp
		} else {
			p.tokenExpireTime = time.Now().Add(1 * time.Hour)
			klog.Warningf("failed to parse refreshed OIDC token expiration time, falling back to default 1h refresh window: %v", err)
		}
		// Baseline for total-validity refresh windows
		p.tokenIssuedAt = time.Now()

		klog.Infof("Successfully refreshed OIDC token")
	}

	cfg := &openapi.Config{
		Endpoint: tea.String(p.stsEndpoint),
		// Explicit timeouts: a degraded STS endpoint must not block this
		// refresh (and every concurrent GetCredential caller) forever under
		// p.mu. Units are milliseconds.
		ConnectTimeout: tea.Int(10 * 1000),
		ReadTimeout:    tea.Int(30 * 1000),
	}
	stsClient, err := sts.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create STS client: %v", err)
	}

	response, err := stsClient.AssumeRoleWithOIDC(&sts.AssumeRoleWithOIDCRequest{
		RoleArn:         tea.String(p.roleArn),
		OIDCProviderArn: tea.String(p.oidcProviderArn),
		OIDCToken:       tea.String(p.token),
		RoleSessionName: tea.String(p.sessionName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to assume role with oidc: %v", err)
	}

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

	// Replace the cached credential only after the expiration parses, so a
	// parse failure never pairs a new credential with the stale expireTime.
	expireTime, err := time.Parse(time.RFC3339, tea.StringValue(response.Body.Credentials.Expiration))
	if err != nil {
		return nil, fmt.Errorf("failed to parse credential expiration time: %v", err)
	}
	p.credential = cred
	p.expireTime = expireTime
	p.credentialIssuedAt = time.Now()

	return cred, nil
}

// getTokenExpireTime extracts the expiration time from a JWT token
func getTokenExpireTime(token string) (time.Time, error) {
	// Signature is intentionally not verified
	parsedToken, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse JWT token: %v", err)
	}

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
	// OidcArnFromDefault reports whether OidcArn was auto-derived from
	// cluster-id/uid (see oidcTierAllowed).
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

// NormalizeSessionNames fills the default role session name into empty
// RoleSessionName / RemoteRoleSessionName so the AK+AssumeRole and
// cross-account tiers are not silently skipped; explicit values are kept.
func (a *AuthConfig) NormalizeSessionNames() {
	if a.RoleSessionName == "" {
		a.RoleSessionName = defaultRoleSessionName
	}
	if a.RemoteRoleSessionName == "" {
		a.RemoteRoleSessionName = defaultRoleSessionName
	}
}

// emptyAuthChainErrorMessage builds the actionable fail-closed message for
// an auth chain that assembled zero providers. The wording is tailored to
// the error source: the ENV client (clientName == backend.EnvClient) is
// configured via environment variables, every other clientName belongs to a
// SecretStore/ClusterSecretStore whose auth fields are empty. Both point at
// the authentication guide instead of only naming the disabled WorkerRole.
func emptyAuthChainErrorMessage(clientName string) string {
	const docHint = "see docs/auth_guide.md for configuration details"
	if clientName == backend.EnvClient {
		return fmt.Sprintf("no usable authentication tier in the ENV auth chain: "+
			"configure ENV credentials (ACCESS_KEY_ID/SECRET_ACCESS_KEY), ENV RRSA variables (ALICLOUD_ROLE_ARN/ALICLOUD_OIDC_PROVIDER_ARN), "+
			"or pass --enable-worker-role=true explicitly to use the cluster WorkerRole on a supporting cluster; %s", docHint)
	}
	return fmt.Sprintf("no usable authentication tier configured for client %q: "+
		"set authentication fields in the SecretStore/ClusterSecretStore (accessKey/accessKeySecret, serviceAccountRef or oidcProviderArn), "+
		"or pass --enable-worker-role=true explicitly to use the cluster WorkerRole on a supporting cluster; %s", clientName, docHint)
}

func (a *AuthConfig) GetAuthCred(region string, maxConcurrentCount int, m *backendp.Manager) (credential.Credential, error) {
	providers := make([]provider.CredentialsProvider, 0)
	var semaphoreProvider *provider.SemaphoreProvider

	a.NormalizeSessionNames()

	// Fail-closed precondition BEFORE the OIDC block: with an explicit
	// serviceAccountRef but missing OIDC prerequisites, silently falling
	// back to lower-priority authentication is forbidden - the ExternalSecret
	// must fail instead of degrading.
	if a.ServiceAccountName != "" && (a.OidcArn == "" || a.RoleArn == "") {
		return nil, fmt.Errorf("ServiceAccount %s/%s is explicitly configured but OIDC prerequisites are missing (oidcProviderArn=%q, roleArn=%q; cluster-id/uid may be unavailable)",
			a.ServiceAccountNamespace, a.ServiceAccountName, a.OidcArn, a.RoleArn)
	}

	// Authentication chain: ServiceAccount RRSA -> OIDC/RRSA -> RAM Role (AK+AssumeRole) -> AccessKey -> ECS Role (WorkerRole)

	// Priority 1: OIDC/RRSA. With an auto-derived OidcArn plus a complete AK
	// pair, AK+AssumeRole takes precedence (0.6.2 contract); explicit config
	// is unaffected. See oidcTierAllowed.
	oidcAllowed := a.oidcTierAllowed()
	if !oidcAllowed && a.OidcArnFromDefault && a.AccessKey != "" && a.AccessSecretKey != "" && a.RoleArn != "" && a.ServiceAccountName == "" {
		klog.Infof("auto-derived oidcProviderARN with complete AccessKey: AK AssumeRole takes precedence over file-based RRSA. To use RRSA instead, explicitly configure oidcProviderARN, configure serviceAccountRef, or remove the AccessKey fields.")
	}
	if oidcAllowed {
		oidcProvider, err := a.createOIDCProvider(region)
		if err != nil {
			// Every error path of createOIDCProvider carries an explicit
			// serviceAccountRef, so this is fail-closed with no fallback.
			// The prefix is the stable error contract asserted by unit and
			// e2e tests; both the KMS and OOS client paths surface this
			// identical message through GetAuthCred.
			return nil, fmt.Errorf("%s for ServiceAccount %s/%s: %v", oidcProviderCreationErrPrefix, a.ServiceAccountNamespace, a.ServiceAccountName, err)
		}
		if oidcProvider != nil {
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
			return nil, errors.New(emptyAuthChainErrorMessage(a.ClientName))
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

// oidcTierAllowed reports whether the OIDC/RRSA tier may enter the chain
// (explicit-intent rules restoring the 0.6.2 contract). Decision order:
//  1. OidcArn or RoleArn empty -> false (prerequisites missing).
//  2. ServiceAccountName configured -> true (SA RRSA, dynamic tokens).
//  3. OidcArn explicitly configured -> true, fail-closed.
//  4. Incomplete AK pair -> true (dropping the tier would silently degrade).
//  5. Auto-derived OidcArn + complete AK pair -> false: AK AssumeRole
//     precedes auto-derived file-based RRSA.
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
	// 0.6.2 contract: AK AssumeRole precedes auto-derived file-based RRSA
	return false
}

// createOIDCProvider creates an OIDC provider with a dynamic or file-based token
func (a *AuthConfig) createOIDCProvider(region string) (provider.CredentialsProvider, error) {
	// Dynamic token when ServiceAccount info is configured
	if a.ServiceAccountName != "" && a.ServiceAccountNamespace != "" && a.KubeClient != nil {
		dynamicToken, err := a.getIdentityToken()
		if err != nil {
			// Explicit serviceAccountRef: fail-closed
			return nil, fmt.Errorf("failed to obtain ServiceAccount identity token for %s/%s: %v", a.ServiceAccountNamespace, a.ServiceAccountName, err)
		}

		if dynamicToken == "" {
			return nil, fmt.Errorf("ServiceAccount identity token is empty for %s/%s", a.ServiceAccountNamespace, a.ServiceAccountName)
		}

		// Use TokenOIDCProvider to avoid creating temporary files for the token
		oidcProvider := NewTokenOIDCProvider(
			provider.GetSTSEndpoint(region, true),
			oidcRoleSessionName,
			dynamicToken,
			a.RoleArn,
			a.OidcArn,
		)
		oidcProvider.getTokenFunc = a.getIdentityToken

		return provider.CredentialsProvider(oidcProvider), nil
	}

	// File-based token otherwise
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

// getIdentityToken requests a dynamic token for the ServiceAccount
func (a *AuthConfig) getIdentityToken() (string, error) {
	tokenRequest := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences: a.TokenAudiences,
		},
	}

	// Default audience
	if len(a.TokenAudiences) == 0 {
		tokenRequest.Spec.Audiences = []string{"sts.aliyuncs.com"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tokenResp, err := a.KubeClient.CoreV1().ServiceAccounts(a.ServiceAccountNamespace).CreateToken(ctx, a.ServiceAccountName, tokenRequest, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to fetch token for ServiceAccount %s/%s: %w", a.ServiceAccountNamespace, a.ServiceAccountName, err)
	}

	return tokenResp.Status.Token, nil
}

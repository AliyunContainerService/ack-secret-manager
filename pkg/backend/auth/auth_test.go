package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alibabacloud-go/tea/tea"
	credential "github.com/aliyun/credentials-go/credentials"
	"k8s.io/client-go/kubernetes/fake"

	ramprovider "github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/testutil"
)

// NOTE: tests mutate package-global state (backend.EnableWorkerRole) and
// share caches, so they rely on serial execution and must never call t.Parallel().

func newTestManager() *backendp.Manager {
	return &backendp.Manager{
		RamLock:     &sync.Mutex{},
		RamProvider: make(map[string]ramprovider.Stopper),
	}
}

func TestNormalizeSessionNames_DefaultInjection(t *testing.T) {
	cfg := &AuthConfig{}
	cfg.NormalizeSessionNames()
	if cfg.RoleSessionName != defaultRoleSessionName {
		t.Errorf("RoleSessionName = %q, want %q", cfg.RoleSessionName, defaultRoleSessionName)
	}
	if cfg.RemoteRoleSessionName != defaultRoleSessionName {
		t.Errorf("RemoteRoleSessionName = %q, want %q", cfg.RemoteRoleSessionName, defaultRoleSessionName)
	}
}

func TestNormalizeSessionNames_ExplicitValuesPreserved(t *testing.T) {
	cfg := &AuthConfig{
		RoleSessionName:       "custom-session",
		RemoteRoleSessionName: "custom-remote-session",
	}
	cfg.NormalizeSessionNames()
	if cfg.RoleSessionName != "custom-session" {
		t.Errorf("RoleSessionName = %q, want %q", cfg.RoleSessionName, "custom-session")
	}
	if cfg.RemoteRoleSessionName != "custom-remote-session" {
		t.Errorf("RemoteRoleSessionName = %q, want %q", cfg.RemoteRoleSessionName, "custom-remote-session")
	}
}

// TestGetAuthCredSessionNameInjection_SecretStorePath: omitted session names
// are injected so the AK+AssumeRole and cross-account tiers are not skipped.
// Tier selection is pinned by config shape (credential-type assertions are
// infeasible through the chain wrapper).
func TestGetAuthCredSessionNameInjection_SecretStorePath(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = false
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{
		ClientName:      "namespace/default/session-injection-store",
		AccessKey:       "test-ak",
		AccessSecretKey: "test-sk",
		RoleArn:         "acs:ram::123456789:role/test-role",
		RemoteRoleArn:   "acs:ram::987654321:role/test-remote-role",
		// RoleSessionName / RemoteRoleSessionName intentionally empty
		RefreshPeriod: 10 * time.Minute,
	}

	m := newTestManager()
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, m)
	if err != nil {
		t.Fatalf("GetAuthCred returned error: %v", err)
	}
	if cred == nil {
		t.Fatal("GetAuthCred returned nil credential")
	}
	if cfg.RoleSessionName != defaultRoleSessionName {
		t.Errorf("RoleSessionName = %q, want default %q so the AK+AssumeRole tier is not skipped", cfg.RoleSessionName, defaultRoleSessionName)
	}
	if cfg.RemoteRoleSessionName != defaultRoleSessionName {
		t.Errorf("RemoteRoleSessionName = %q, want default %q so the cross-account tier is not skipped", cfg.RemoteRoleSessionName, defaultRoleSessionName)
	}

	// With the injected session names the AK+AssumeRole tier is not skipped,
	// so the assembled chain must be registered under the client name.
	if _, ok := m.RamProvider[cfg.ClientName]; !ok {
		t.Fatalf("expected the provider chain to be registered under %q", cfg.ClientName)
	}
}

// TestGetAuthCredNoProvidersFailsClosed is the negative control: no AK/OIDC
// material + EnableWorkerRole=false assembles zero providers, so a successful
// GetAuthCred under the same flag proves AK/OIDC tiers were assembled.
func TestGetAuthCredNoProvidersFailsClosed(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = false
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{ClientName: "namespace/default/empty-store", RefreshPeriod: 10 * time.Minute}
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, newTestManager())
	if err == nil {
		t.Fatal("expected an error when no authentication method is configured and EnableWorkerRole is false")
	}
	if cred != nil {
		t.Fatal("expected nil credential on fail-closed error")
	}
	// Empty SecretStore auth chain: the actionable message must name the
	// store client, enumerate fix paths and point at the auth guide;
	// "no usable authentication tier" is the stable contract substring.
	if !strings.Contains(err.Error(), "no usable authentication tier configured for client \"namespace/default/empty-store\"") {
		t.Errorf("unexpected error message: %v", err)
	}
	for _, want := range []string{"accessKey/accessKeySecret", "serviceAccountRef", "--enable-worker-role=true", "docs/auth_guide.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message must enumerate the fix path %q: %v", want, err)
		}
	}
}

// TestGetAuthCredENVClientEmptyChainMessage: the ENV client (empty auth
// env vars + EnableWorkerRole=false) fails closed with the ENV-specific
// wording naming the ENV fix paths, not the SecretStore ones.
func TestGetAuthCredENVClientEmptyChainMessage(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = false
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{ClientName: backend.EnvClient, RefreshPeriod: 10 * time.Minute}
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, newTestManager())
	if err == nil {
		t.Fatal("expected an error when the ENV auth chain is empty and EnableWorkerRole is false")
	}
	if cred != nil {
		t.Fatal("expected nil credential on fail-closed error")
	}
	if !strings.Contains(err.Error(), "no usable authentication tier in the ENV auth chain") {
		t.Errorf("unexpected error message: %v", err)
	}
	for _, want := range []string{"ACCESS_KEY_ID/SECRET_ACCESS_KEY", "ALICLOUD_ROLE_ARN/ALICLOUD_OIDC_PROVIDER_ARN", "--enable-worker-role=true", "docs/auth_guide.md"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message must enumerate the ENV fix path %q: %v", want, err)
		}
	}
}

func newExplicitSATestConfig() *AuthConfig {
	return &AuthConfig{
		ClientName:              "namespace/default/explicit-sa-store",
		RoleArn:                 "acs:ram::123456789:role/sa-role",
		OidcArn:                 "acs:ram::123456789:oidc-provider/ack-rrsa-test",
		ServiceAccountName:      "missing-sa",
		ServiceAccountNamespace: "default",
		RefreshPeriod:           10 * time.Minute,
		// fake clientset without the ServiceAccount: CreateToken fails
		KubeClient: fake.NewSimpleClientset(),
	}
}

// TestServiceAccountAuth_ProviderCreationFailsClosed: with an explicit
// serviceAccountRef, an OIDC provider creation failure fails closed instead
// of falling back to lower-priority methods.
func TestServiceAccountAuth_ProviderCreationFailsClosed(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = true
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := newExplicitSATestConfig()
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, newTestManager())
	if err == nil {
		t.Fatal("expected error when OIDC provider creation fails for an explicitly configured ServiceAccount, got nil")
	}
	if cred != nil {
		t.Fatal("expected nil credential on fail-closed error")
	}
	if !strings.Contains(err.Error(), "failed to create OIDC provider for ServiceAccount default/missing-sa") {
		t.Errorf("error %q should identify the failed OIDC provider creation for the ServiceAccount", err.Error())
	}
}

// TestServiceAccountAuth_MissingOIDCPrerequisitesFailsClosed: with an
// explicit serviceAccountRef but empty OidcArn, GetAuthCred must fail closed
// instead of silently skipping the OIDC block and falling back.
func TestServiceAccountAuth_MissingOIDCPrerequisitesFailsClosed(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = true
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{
		ClientName:              "namespace/default/explicit-missing-oidc-store",
		RoleArn:                 "acs:ram::123456789:role/sa-role",
		OidcArn:                 "", // cluster-id/uid unresolved
		ServiceAccountName:      "explicit-sa",
		ServiceAccountNamespace: "default",
		RefreshPeriod:           10 * time.Minute,
		KubeClient:              fake.NewSimpleClientset(),
	}
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, newTestManager())
	if err == nil {
		t.Fatal("expected fail-closed error when OIDC prerequisites are missing for an explicitly configured ServiceAccount, got nil")
	}
	if cred != nil {
		t.Fatal("expected nil credential on fail-closed error")
	}
	if !strings.Contains(err.Error(), "OIDC prerequisites are missing") {
		t.Errorf("error %q should mention the missing OIDC prerequisites", err.Error())
	}
}

// TestServiceAccountAuth_NoExplicitSAFallsBack: without an explicit
// ServiceAccount the fail-closed guard must not misfire; the chain falls
// back to lower-priority methods without error.
func TestServiceAccountAuth_NoExplicitSAFallsBack(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = true
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{
		ClientName:              "namespace/default/no-explicit-sa-store",
		RoleArn:                 "acs:ram::123456789:role/sa-role",
		OidcArn:                 "",
		ServiceAccountName:      "", // no explicit serviceAccountRef
		ServiceAccountNamespace: "",
		RefreshPeriod:           10 * time.Minute,
		KubeClient:              fake.NewSimpleClientset(),
	}
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, newTestManager())
	if err != nil {
		t.Fatalf("expected fallback to succeed without an explicit ServiceAccount, got error: %v", err)
	}
	if cred == nil {
		t.Fatal("expected non-nil credential from fallback chain")
	}
}

// TestOIDCTierAllowed pins the OIDC/RRSA tier (Priority 1) entry conditions:
// SA RRSA and explicit oidcProviderARN always enter; incomplete AK keeps the
// tier; auto-derived OidcArn + complete AK never enters (0.6.2 contract: AK
// AssumeRole takes precedence over auto-derived file-based RRSA); missing
// RoleArn/OidcArn never enters.
func TestOIDCTierAllowed(t *testing.T) {
	const (
		roleArn         = "acs:ram::123456789:role/test-role"
		defaultOidcArn  = "acs:ram::123456789:oidc-provider/ack-rrsa-c00000000000000000000000000000000"
		explicitOidcArn = "acs:ram::123456789:oidc-provider/explicit-oidc"
	)

	// newPresentTokenFile creates a real temporary token file under t.TempDir().
	newPresentTokenFile := func(t *testing.T) string {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "oidc-token-*")
		if err != nil {
			t.Fatalf("failed to create temp token file: %v", err)
		}
		if _, err := f.WriteString("fake-oidc-token"); err != nil {
			t.Fatalf("failed to write temp token file: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("failed to close temp token file: %v", err)
		}
		return f.Name()
	}

	tests := []struct {
		name          string
		cfg           AuthConfig
		withTokenFile bool // inject a real temporary token file path
		want          bool
	}{
		{
			name: "a) SA RRSA enters the tier (dynamic token, file-independent)",
			cfg: AuthConfig{
				RoleArn:                 roleArn,
				OidcArn:                 defaultOidcArn,
				OidcArnFromDefault:      true,
				AccessKey:               "test-ak",
				AccessSecretKey:         "test-sk",
				ServiceAccountName:      "rrsa-sa",
				ServiceAccountNamespace: "default",
			},
			want: true,
		},
		{
			name: "b) explicit oidcProviderARN enters the tier (fail-closed, file absent)",
			cfg: AuthConfig{
				RoleArn:            roleArn,
				OidcArn:            explicitOidcArn,
				OidcArnFromDefault: false,
				AccessKey:          "test-ak",
				AccessSecretKey:    "test-sk",
			},
			want: true,
		},
		{
			name: "c) auto ARN + incomplete AK keeps the tier when the token file is missing",
			cfg: AuthConfig{
				RoleArn:            roleArn,
				OidcArn:            defaultOidcArn,
				OidcArnFromDefault: true,
				AccessKey:          "test-ak",
				AccessSecretKey:    "",
			},
			want: true,
		},
		{
			name: "c2) auto ARN + incomplete AK keeps the tier even when the token file exists",
			cfg: AuthConfig{
				RoleArn:            roleArn,
				OidcArn:            defaultOidcArn,
				OidcArnFromDefault: true,
				AccessKey:          "test-ak",
				AccessSecretKey:    "",
			},
			withTokenFile: true,
			want:          true,
		},
		{
			name: "d) auto ARN + complete AK never enters the tier even with the token file present",
			cfg: AuthConfig{
				RoleArn:            roleArn,
				OidcArn:            defaultOidcArn,
				OidcArnFromDefault: true,
				AccessKey:          "test-ak",
				AccessSecretKey:    "test-sk",
			},
			withTokenFile: true,
			want:          false,
		},
		{
			name: "e) auto ARN + complete AK never enters the tier with the token file missing",
			cfg: AuthConfig{
				RoleArn:            roleArn,
				OidcArn:            defaultOidcArn,
				OidcArnFromDefault: true,
				AccessKey:          "test-ak",
				AccessSecretKey:    "test-sk",
			},
			want: false,
		},
		{
			name: "missing RoleArn never enters the tier",
			cfg: AuthConfig{
				OidcArn:            explicitOidcArn,
				OidcArnFromDefault: false,
			},
			want: false,
		},
		{
			name: "missing OidcArn never enters the tier",
			cfg: AuthConfig{
				RoleArn:            roleArn,
				OidcArn:            "",
				OidcArnFromDefault: false,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			if tt.withTokenFile {
				cfg.TokenFilePath = newPresentTokenFile(t)
			}
			if got := cfg.oidcTierAllowed(); got != tt.want {
				t.Errorf("oidcTierAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestGetAuthCred_AKAssumeRoleSkipsDefaultOIDCTier: regression -- AK +
// auto-derived OidcArn must keep the file-based OIDC tier out of the chain
// (0.6.2 contract, token-file independent).
func TestGetAuthCred_AKAssumeRoleSkipsDefaultOIDCTier(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = false
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{
		ClientName:         "namespace/default/ak-assume-store",
		RoleArn:            "acs:ram::123456789:role/ak-assume-role",
		OidcArn:            "acs:ram::123456789:oidc-provider/ack-rrsa-c00000000000000000000000000000000",
		OidcArnFromDefault: true,
		AccessKey:          "test-ak",
		AccessSecretKey:    "test-sk",
		// TokenFilePath is irrelevant under the 0.6.2 contract (no file probe),
		// pinned here only to document the file-independence.
		TokenFilePath: filepath.Join(t.TempDir(), "nonexistent-oidc-token"),
		RefreshPeriod: 10 * time.Minute,
	}

	m := newTestManager()
	var cred credential.Credential
	var err error
	out := testutil.CaptureKlogOutput(t, func() {
		cred, err = cfg.GetAuthCred("cn-hangzhou", 10, m)
	})
	if err != nil {
		t.Fatalf("GetAuthCred returned error: %v", err)
	}
	if cred == nil {
		t.Fatal("GetAuthCred returned nil credential")
	}
	if _, ok := m.RamProvider[cfg.ClientName]; !ok {
		t.Fatalf("expected the AK-derived chain to be registered under %q", cfg.ClientName)
	}
	if !strings.Contains(out, "auto-derived oidcProviderARN with complete AccessKey: AK AssumeRole takes precedence over file-based RRSA. To use RRSA instead, explicitly configure oidcProviderARN, configure serviceAccountRef, or remove the AccessKey fields.") {
		t.Errorf("expected the skip log for the auto-derived oidcProviderARN with complete AccessKey, got: %q", out)
	}
	// With EnableWorkerRole disabled the registered chain consists solely
	// of the AK-derived tiers, proving the OIDC tier did not enter.
	if strings.Contains(out, "OIDC/RRSA authentication registered") {
		t.Errorf("the file-based OIDC tier must not enter the chain with an auto-derived OidcArn and complete AK, got: %q", out)
	}
}

// TestGetAuthCred_AKWithDefaultOIDCAndRRSAEnabledUsesAKAssumeRole: case 1
// regression -- token file really mounted + auto-derived OidcArn + complete
// AK: the file-based OIDC tier must stay out of the chain (0.6.2 contract).
func TestGetAuthCred_AKWithDefaultOIDCAndRRSAEnabledUsesAKAssumeRole(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = false
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	tokenFile := filepath.Join(t.TempDir(), "oidc-token")
	if err := os.WriteFile(tokenFile, []byte("fake-oidc-token"), 0o600); err != nil {
		t.Fatalf("failed to write temp token file: %v", err)
	}

	cfg := &AuthConfig{
		ClientName:         "namespace/default/ak-oidc-file-present-store",
		RoleArn:            "acs:ram::123456789:role/ak-assume-role",
		OidcArn:            "acs:ram::123456789:oidc-provider/ack-rrsa-c00000000000000000000000000000000",
		OidcArnFromDefault: true,
		AccessKey:          "test-ak",
		AccessSecretKey:    "test-sk",
		TokenFilePath:      tokenFile,
		RefreshPeriod:      10 * time.Minute,
	}

	m := newTestManager()
	var cred credential.Credential
	var err error
	out := testutil.CaptureKlogOutput(t, func() {
		cred, err = cfg.GetAuthCred("cn-hangzhou", 10, m)
	})
	if err != nil {
		t.Fatalf("GetAuthCred returned error: %v", err)
	}
	if cred == nil {
		t.Fatal("GetAuthCred returned nil credential")
	}
	if _, ok := m.RamProvider[cfg.ClientName]; !ok {
		t.Fatalf("expected the AK-derived chain to be registered under %q", cfg.ClientName)
	}
	if strings.Contains(out, "OIDC/RRSA authentication registered") {
		t.Errorf("the file-based OIDC tier must not enter the chain even when the token file exists, got: %q", out)
	}
	if !strings.Contains(out, "auto-derived oidcProviderARN with complete AccessKey: AK AssumeRole takes precedence over file-based RRSA. To use RRSA instead, explicitly configure oidcProviderARN, configure serviceAccountRef, or remove the AccessKey fields.") {
		t.Errorf("expected the AK-precedence skip log, got: %q", out)
	}
}

// TestGetAuthCred_PureRRSARetainsDefaultOIDCTier: without AK credentials
// the auto-generated OidcArn keeps the OIDC tier regardless of the token
// file (a nonexistent path pins the file-independence; an empty chain would
// fail closed, so success proves the tier was kept).
func TestGetAuthCred_PureRRSARetainsDefaultOIDCTier(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = false
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{
		ClientName:         "namespace/default/pure-rrsa-store",
		RoleArn:            "acs:ram::123456789:role/rrsa-role",
		OidcArn:            "acs:ram::123456789:oidc-provider/ack-rrsa-c00000000000000000000000000000000",
		OidcArnFromDefault: true,
		TokenFilePath:      filepath.Join(t.TempDir(), "nonexistent-oidc-token"),
		RefreshPeriod:      10 * time.Minute,
	}
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, newTestManager())
	if err != nil {
		t.Fatalf("expected the auto-generated OidcArn to keep the OIDC tier in the chain, got error: %v", err)
	}
	if cred == nil {
		t.Fatal("GetAuthCred returned nil credential")
	}
}

// TestGetAuthCred_ExplicitOIDCWithAKRetainsTier: case 2 -- an explicit
// oidcProviderARN keeps the OIDC tier even with a complete AK pair, ahead
// of the AK+AssumeRole tier by construction; absence of the skip log pins
// the explicit-intent heuristic.
func TestGetAuthCred_ExplicitOIDCWithAKRetainsTier(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = false
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{
		ClientName:         "namespace/default/explicit-oidc-ak-store",
		RoleArn:            "acs:ram::123456789:role/test-role",
		OidcArn:            "acs:ram::123456789:oidc-provider/explicit-oidc",
		OidcArnFromDefault: false,
		AccessKey:          "test-ak",
		AccessSecretKey:    "test-sk",
		RefreshPeriod:      10 * time.Minute,
	}

	m := newTestManager()
	var cred credential.Credential
	var err error
	out := testutil.CaptureKlogOutput(t, func() {
		cred, err = cfg.GetAuthCred("cn-hangzhou", 10, m)
	})
	if err != nil {
		t.Fatalf("GetAuthCred returned error: %v", err)
	}
	if cred == nil {
		t.Fatal("GetAuthCred returned nil credential")
	}
	if _, ok := m.RamProvider[cfg.ClientName]; !ok {
		t.Fatalf("expected the provider chain to be registered under %q", cfg.ClientName)
	}
	// The OIDC tier is appended before the AK+AssumeRole tier by
	// construction, so it wins by priority.
	if !strings.Contains(out, "OIDC/RRSA authentication registered") {
		t.Errorf("expected the OIDC tier to enter the chain ahead of the AK assume-role tier with an explicit oidcProviderARN, got: %q", out)
	}
	if strings.Contains(out, "auto-derived oidcProviderARN with complete AccessKey: AK AssumeRole takes precedence over file-based RRSA") {
		t.Errorf("explicitly configured oidcProviderARN must not trigger the AK-precedence skip log, got: %q", out)
	}
}

// TestGetAuthCred_SARRSAWithDefaultOIDCEntersTier: an explicit
// serviceAccountRef keeps the OIDC tier even with an auto-generated
// OidcArn; the fail-closed dynamic-token error proves the tier was entered.
func TestGetAuthCred_SARRSAWithDefaultOIDCEntersTier(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = true
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := &AuthConfig{
		ClientName:              "namespace/default/sa-rrsa-default-oidc-store",
		RoleArn:                 "acs:ram::123456789:role/sa-role",
		OidcArn:                 "acs:ram::123456789:oidc-provider/ack-rrsa-c00000000000000000000000000000000",
		OidcArnFromDefault:      true,
		ServiceAccountName:      "missing-sa",
		ServiceAccountNamespace: "default",
		RefreshPeriod:           10 * time.Minute,
		KubeClient:              fake.NewSimpleClientset(),
	}
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, newTestManager())
	if err == nil {
		t.Fatal("expected the fail-closed SA RRSA error proving the OIDC tier was entered, got nil")
	}
	if cred != nil {
		t.Fatal("expected nil credential on fail-closed error")
	}
	if !strings.Contains(err.Error(), "failed to create OIDC provider for ServiceAccount default/missing-sa") {
		t.Errorf("error %q should identify the OIDC provider creation for the ServiceAccount", err.Error())
	}
}

func newSTSCredential(t *testing.T) credential.Credential {
	t.Helper()
	cred, err := credential.NewCredential(&credential.Config{
		Type:            tea.String("sts"),
		AccessKeyId:     tea.String("cached-ak"),
		AccessKeySecret: tea.String("cached-sk"),
		SecurityToken:   tea.String("cached-token"),
	})
	if err != nil {
		t.Fatalf("failed to create sts credential: %v", err)
	}
	return cred
}

// newJWT builds an unsigned JWT whose exp claim is expDelta away from now;
// getTokenExpireTime parses tokens without signature verification.
func newJWT(t *testing.T, expDelta time.Duration) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{"exp": time.Now().Add(expDelta).Unix()})
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// TestTokenOIDCProviderGetCredential_TokenRefreshSuccess covers the
// token-refresh success path, driven to the STS boundary (unroutable
// endpoint): the connection error proves the refresh completed.
func TestTokenOIDCProviderGetCredential_TokenRefreshSuccess(t *testing.T) {
	var calls int32
	p := NewTokenOIDCProvider("127.0.0.1:1", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test")
	// Valid cached credential but expired token: an expired token always
	// triggers an immediate refresh, while the still-valid credential keeps
	// needCredentialRefresh false.
	p.credential = newSTSCredential(t)
	p.expireTime = time.Now().Add(time.Hour)
	p.tokenExpireTime = time.Now().Add(-time.Minute)
	// The refreshed token expires in 5h: tokenExpireTime must be re-derived
	// from its exp claim (the unparsable fallback would only yield ~1h).
	refreshedToken := newJWT(t, 5*time.Hour)
	p.getTokenFunc = func() (string, error) {
		atomic.AddInt32(&calls, 1)
		return refreshedToken, nil
	}

	cred, err := p.GetCredential()
	if err == nil {
		t.Fatal("expected the STS boundary failure (unroutable endpoint), got nil")
	}
	if cred != nil {
		t.Fatal("expected nil credential when the STS assume cannot complete")
	}
	if !strings.Contains(err.Error(), "failed to assume role with oidc") {
		t.Fatalf("error %q must show the round reached the STS assume step after the refresh", err.Error())
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected exactly one token refresh, got %d", calls)
	}
	if p.token != refreshedToken {
		t.Errorf("token was not replaced by the refreshed token")
	}
	// tokenExpireTime must be re-derived from the refreshed token's exp
	// claim (~5h, not the ~1h unparsable fallback).
	if remaining := time.Until(p.tokenExpireTime); remaining <= 2*time.Hour || remaining > 5*time.Hour+time.Minute {
		t.Errorf("tokenExpireTime was not parsed from the refreshed token, remaining = %v", remaining)
	}
	// The cached credential must stay untouched when the assume fails.
	if remaining := time.Until(p.expireTime); remaining < 30*time.Minute {
		t.Errorf("cached credential expiration must stay untouched, remaining = %v", remaining)
	}
}

// TestTokenOIDCProviderGetCredential_TokenRefreshFailCredentialExpired:
// token refresh fails and no valid cached credential exists, so
// GetCredential must fail with "failed to refresh OIDC token".
func TestTokenOIDCProviderGetCredential_TokenRefreshFailCredentialExpired(t *testing.T) {
	tests := []struct {
		name       string
		credential credential.Credential
		expireTime time.Time
	}{
		{
			name:       "refresh failure with nil cached credential",
			credential: nil,
			expireTime: time.Time{},
		},
		{
			name:       "refresh failure with expired cached credential",
			credential: newSTSCredential(t),
			expireTime: time.Now().Add(-time.Hour),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewTokenOIDCProvider("sts.cn-hangzhou.aliyuncs.com", "session", "unparsable-token",
				"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test")
			p.credential = tt.credential
			p.expireTime = tt.expireTime
			// Token already expired -> immediate refresh attempt.
			p.tokenExpireTime = time.Now().Add(-time.Minute)
			p.getTokenFunc = func() (string, error) {
				return "", errors.New("mock token refresh failure")
			}

			cred, err := p.GetCredential()
			if err == nil {
				t.Fatal("expected an error when the token refresh fails and no valid cached credential exists")
			}
			if cred != nil {
				t.Fatal("expected nil credential on refresh failure")
			}
			if !strings.Contains(err.Error(), "failed to refresh OIDC token") {
				t.Errorf("error %q must report the OIDC token refresh failure", err.Error())
			}
			if !strings.Contains(err.Error(), "mock token refresh failure") {
				t.Errorf("error %q must carry the underlying refresh error", err.Error())
			}
		})
	}
}

// TestRefreshWindow_TotalValidityBased pins the refresh-window math: with a
// known issue moment the window is 20% of the TOTAL validity (20% of the
// remaining time was degenerate and only fired after expiry).
func TestRefreshWindow_TotalValidityBased(t *testing.T) {
	now := time.Now()
	issued := now.Add(-time.Hour)
	expire := now.Add(time.Hour) // total validity = 2h

	// Known issuedAt: window = 20% of the total validity.
	if got, want := refreshWindow(expire, issued, now), 24*time.Minute; got != want {
		t.Errorf("refreshWindow with known issuedAt = %v, want %v", got, want)
	}
	// Unknown issuedAt: legacy remaining-time based window keeps the
	// first-fetch behavior unchanged.
	if got, want := refreshWindow(expire, time.Time{}, now), 12*time.Minute; got != want {
		t.Errorf("refreshWindow with unknown issuedAt = %v, want %v", got, want)
	}
	// Already expired: minimal positive window drives the immediate refresh.
	if got := refreshWindow(now.Add(-time.Minute), issued, now); got != time.Nanosecond {
		t.Errorf("refreshWindow for an expired item = %v, want %v", got, time.Nanosecond)
	}
	// Degenerate total validity: no crash, minimal window.
	if got := refreshWindow(expire, expire.Add(time.Minute), now); got != time.Nanosecond {
		t.Errorf("refreshWindow with issuedAt after expire = %v, want %v", got, time.Nanosecond)
	}
}

// TestRefreshWindow_LowerBoundGuard is the table-driven coverage for the
// refresh-window lower bound: normal validity keeps the percentage window,
// while every case whose computed window truncates to <= 0 (already expired
// or extremely short validity) degrades to the minimal positive window, i.e.
// "refresh immediately".
func TestRefreshWindow_LowerBoundGuard(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		expire   time.Time
		issuedAt time.Time
		want     time.Duration
	}{
		{
			name:     "normal validity with known issuedAt: 20%% of the total validity",
			expire:   now.Add(time.Hour),
			issuedAt: now.Add(-time.Hour),
			want:     24 * time.Minute,
		},
		{
			name:     "normal validity with unknown issuedAt: 20%% of the remaining time",
			expire:   now.Add(time.Hour),
			issuedAt: time.Time{},
			want:     12 * time.Minute,
		},
		{
			name:     "extremely short validity with unknown issuedAt: truncated window triggers the lower bound",
			expire:   now.Add(4 * time.Nanosecond),
			issuedAt: time.Time{},
			want:     time.Nanosecond,
		},
		{
			name:     "1ns validity with unknown issuedAt: boundary below truncation",
			expire:   now.Add(time.Nanosecond),
			issuedAt: time.Time{},
			want:     time.Nanosecond,
		},
		{
			name:     "extremely short validity with known issuedAt: truncated window triggers the lower bound",
			expire:   now.Add(2 * time.Nanosecond),
			issuedAt: now.Add(-2 * time.Nanosecond),
			want:     time.Nanosecond,
		},
		{
			name:     "boundary: expiring exactly now means refresh immediately",
			expire:   now,
			issuedAt: now.Add(-time.Hour),
			want:     time.Nanosecond,
		},
		{
			name:     "already expired means refresh immediately",
			expire:   now.Add(-time.Minute),
			issuedAt: time.Time{},
			want:     time.Nanosecond,
		},
		{
			name:     "degenerate: issuedAt after expire means refresh immediately",
			expire:   now.Add(time.Hour),
			issuedAt: now.Add(time.Hour + time.Minute),
			want:     time.Nanosecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refreshWindow(tt.expire, tt.issuedAt, now); got != tt.want {
				t.Errorf("refreshWindow(expire, issuedAt, now) = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTokenOIDCProviderGetCredential_TokenRefreshWithinLastTwentyPercent:
// refresh triggers while the token is STILL VALID, once its remaining
// validity enters the last 20% of the total lifetime; the STS boundary
// error proves it triggered before expiration.
func TestTokenOIDCProviderGetCredential_TokenRefreshWithinLastTwentyPercent(t *testing.T) {
	var calls int32
	p := NewTokenOIDCProvider("127.0.0.1:1", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test")
	// Issued 54m ago, expires in 6m: remaining 6m is inside the last-20%
	// window (12m) -> refresh must trigger while still valid.
	p.tokenIssuedAt = time.Now().Add(-54 * time.Minute)
	p.tokenExpireTime = time.Now().Add(6 * time.Minute)
	// Credential fully fresh: the refresh must come from the token side alone.
	p.credential = newSTSCredential(t)
	p.credentialIssuedAt = time.Now()
	p.expireTime = time.Now().Add(time.Hour)
	p.getTokenFunc = func() (string, error) {
		atomic.AddInt32(&calls, 1)
		return newJWT(t, 5*time.Hour), nil
	}

	cred, err := p.GetCredential()
	if err == nil {
		t.Fatal("expected the STS boundary failure (unroutable endpoint), got nil")
	}
	if cred != nil {
		t.Fatal("expected nil credential when the STS assume cannot complete")
	}
	if !strings.Contains(err.Error(), "failed to assume role with oidc") {
		t.Fatalf("error %q must show the refresh was triggered while the token was still valid", err.Error())
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected exactly one token refresh, got %d", calls)
	}
	// The successful token refresh must have re-recorded the issue moment.
	if p.tokenIssuedAt.IsZero() {
		t.Error("tokenIssuedAt must be recorded after a successful token refresh")
	}
}

// TestTokenOIDCProviderGetCredential_CredentialRefreshWithinLastTwentyPercent:
// the same last-20% window on the credential side must trigger a credential
// refresh (reaching the STS boundary) instead of serving the stale cache.
func TestTokenOIDCProviderGetCredential_CredentialRefreshWithinLastTwentyPercent(t *testing.T) {
	p := NewTokenOIDCProvider("127.0.0.1:1", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test")
	// No getTokenFunc: any refresh must come from the credential side.
	// Issued 54m ago, expires in 6m: inside the last-20% window (12m).
	p.credential = newSTSCredential(t)
	p.credentialIssuedAt = time.Now().Add(-54 * time.Minute)
	p.expireTime = time.Now().Add(6 * time.Minute)
	// Token far outside its refresh window.
	p.tokenExpireTime = time.Now().Add(48 * time.Minute)

	cred, err := p.GetCredential()
	if err == nil {
		t.Fatal("expected the STS boundary failure (unroutable endpoint), got nil")
	}
	if cred != nil {
		t.Fatal("expected nil credential when the STS assume cannot complete")
	}
	if !strings.Contains(err.Error(), "failed to assume role with oidc") {
		t.Fatalf("error %q must show the credential refresh was triggered inside the last-20%% window", err.Error())
	}
}

// TestTokenOIDCProviderGetCredential_NoRefreshOutsideWindow: both token and
// credential outside their last-20% windows -> the cached credential is
// served without any refresh attempt.
func TestTokenOIDCProviderGetCredential_NoRefreshOutsideWindow(t *testing.T) {
	p := NewTokenOIDCProvider("127.0.0.1:1", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test")
	// Remaining 30m is well outside the 12m last-20% window.
	p.tokenIssuedAt = time.Now().Add(-30 * time.Minute)
	p.tokenExpireTime = time.Now().Add(30 * time.Minute)
	p.credential = newSTSCredential(t)
	p.credentialIssuedAt = time.Now().Add(-30 * time.Minute)
	p.expireTime = time.Now().Add(30 * time.Minute)

	var calls int32
	p.getTokenFunc = func() (string, error) {
		atomic.AddInt32(&calls, 1)
		return "", errors.New("token refresh must not be called")
	}

	cred, err := p.GetCredential()
	if err != nil {
		t.Fatalf("expected the cached credential to be served, got error: %v", err)
	}
	if cred != p.credential {
		t.Fatal("expected the cached credential to be served as-is")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("expected no token refresh outside the last-20%% window, got %d calls", calls)
	}
}

// TestTokenOIDCProviderConcurrentGetCredential_CacheHit: concurrent cache
// hits; with -race this proves the token/credential reads are synchronized.
func TestTokenOIDCProviderConcurrentGetCredential_CacheHit(t *testing.T) {
	p := NewTokenOIDCProvider("sts.cn-hangzhou.aliyuncs.com", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test")
	p.credential = newSTSCredential(t)
	p.expireTime = time.Now().Add(time.Hour)
	p.tokenExpireTime = time.Now().Add(time.Hour)

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cred, err := p.GetCredential()
			if err != nil {
				errCh <- err
				return
			}
			if cred == nil {
				errCh <- errors.New("nil credential")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent GetCredential failed: %v", err)
	}
}

// TestTokenOIDCProviderConcurrentGetCredential_TokenRefreshError: expired
// token with failing getTokenFunc must still serve the valid cached
// credential; with -race this proves the refresh path is race-free.
func TestTokenOIDCProviderConcurrentGetCredential_TokenRefreshError(t *testing.T) {
	p := NewTokenOIDCProvider("sts.cn-hangzhou.aliyuncs.com", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test")
	p.credential = newSTSCredential(t)
	p.expireTime = time.Now().Add(time.Hour)
	// Force the token into the refresh window (already expired)
	p.tokenExpireTime = time.Now().Add(-time.Minute)

	var calls int64
	p.getTokenFunc = func() (string, error) {
		atomic.AddInt64(&calls, 1)
		return "", errors.New("mock token refresh failure")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cred, err := p.GetCredential()
			if err != nil {
				errCh <- err
				return
			}
			if cred == nil {
				errCh <- errors.New("nil credential")
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent GetCredential failed: %v", err)
	}
	if atomic.LoadInt64(&calls) == 0 {
		t.Error("expected token refresh to be attempted")
	}
}

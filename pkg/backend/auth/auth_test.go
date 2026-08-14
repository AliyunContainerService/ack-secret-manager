package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
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
	"k8s.io/klog"

	ramprovider "github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
)

// NOTE: tests in this file mutate package-global state (backend.EnableWorkerRole)
// and construct providers with shared caches, so they rely on the package's
// default SERIAL test execution and must never call t.Parallel().

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

// TestGetAuthCredSessionNameInjection_SecretStorePath simulates the config
// shape produced by common.BuildAuthConfig for a SecretStore where the user
// omitted both session names: the AK+AssumeRole and cross-account tiers must
// no longer be skipped. Credential-type assertions are infeasible (the chain
// wrapper hides layer types; fetching values would trigger a real STS
// round-trip), so tier selection is pinned by config shape: OidcArn empty +
// EnableWorkerRole=false leaves only AK-derived tiers.
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

	// The assembled chain must be registered under the client name: with the
	// injected session names the AK+AssumeRole tier was not skipped, so at
	// least one provider was assembled (see the function comment for why this
	// is the closest available layer assertion).
	if _, ok := m.RamProvider[cfg.ClientName]; !ok {
		t.Fatalf("expected the provider chain to be registered under %q", cfg.ClientName)
	}
}

// TestGetAuthCredNoProvidersFailsClosed is the negative control for the
// credential-layer assertions above: with EnableWorkerRole=false and no
// AK/OIDC material at all, zero providers are assembled and GetAuthCred
// fails closed -- so a successful GetAuthCred under the same flag proves
// the returned credential comes from the assembled AK/OIDC tiers.
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
	if !strings.Contains(err.Error(), "please set auth config when EnableWorkerRole is false") {
		t.Errorf("unexpected error message: %v", err)
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

// TestServiceAccountAuth_ProviderCreationFailsClosed verifies that with an
// explicit serviceAccountRef, an OIDC provider creation failure is returned
// as an error (fail-closed) instead of being swallowed and falling back to
// lower-priority authentication methods.
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

// TestServiceAccountAuth_MissingOIDCPrerequisitesFailsClosed verifies the
// fail-closed precondition in front of the OIDC block: when cluster-id/uid
// are missing (OidcArn empty) the OIDC block would be skipped entirely; with
// an explicit serviceAccountRef, GetAuthCred must fail closed instead of
// silently falling back to lower-priority methods.
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

// TestServiceAccountAuth_NoExplicitSAFallsBack verifies the fail-closed
// precondition guard does not misfire without an explicit ServiceAccount:
// the chain falls back to lower-priority methods without error.
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

// TestOIDCTierAllowed pins the OIDC/RRSA tier (Priority 1) entry condition
// for the decision matrix:
//   - SA RRSA enters the tier regardless of the token file (dynamic tokens);
//   - an explicit oidcProviderARN enters the tier (fail-closed, file absent);
//   - an incomplete AK pair keeps the tier regardless of the token file
//     (AK tiers need both AK and SK; dropping the OIDC tier would silently
//     degrade the chain);
//   - auto-derived OidcArn + complete AK never enters the tier, regardless
//     of whether the token file exists (0.6.2 contract: AK AssumeRole takes
//     precedence over auto-derived file-based RRSA);
//   - missing RoleArn never enters the tier;
//   - missing OidcArn never enters the tier.
func TestOIDCTierAllowed(t *testing.T) {
	const (
		roleArn         = "acs:ram::123456789:role/test-role"
		defaultOidcArn  = "acs:ram::123456789:oidc-provider/ack-rrsa-c00000000000000000000000000000000"
		explicitOidcArn = "acs:ram::123456789:oidc-provider/explicit-oidc"
	)

	// newPresentTokenFile creates a real temporary token file under a fresh
	// t.TempDir() and returns its path.
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

var initKlogFlagsOnce sync.Once

// captureKlogOutput redirects klog output to a buffer for the duration of
// fn. klog writes to stderr directly when logtostderr=true (the default),
// so the flag must be flipped off for SetOutput to take effect. klog v1
// registers its flags only via an explicit InitFlags call.
func captureKlogOutput(t *testing.T, fn func()) string {
	t.Helper()
	initKlogFlagsOnce.Do(func() { klog.InitFlags(nil) })
	if err := flag.Set("logtostderr", "false"); err != nil {
		t.Fatalf("failed to disable logtostderr: %v", err)
	}
	defer func() {
		klog.SetOutput(os.Stderr)
		_ = flag.Set("logtostderr", "true")
	}()
	var buf bytes.Buffer
	klog.SetOutput(&buf)
	fn()
	klog.Flush()
	return buf.String()
}

// TestGetAuthCred_AKAssumeRoleSkipsDefaultOIDCTier: regression -- AK +
// auto-derived OidcArn must not put the file-based OIDC tier into the chain
// (0.6.2 contract, independent of the token file's presence); the skip log
// plus a successful GetAuthCred (WorkerRole disabled) proves only the
// AK-derived tiers (AK+AssumeRole / pure AK) are effective.
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
		// TokenFilePath is irrelevant under the 0.6.2 contract (no file
		// probe), pinned here only to document the file-independence.
		TokenFilePath: filepath.Join(t.TempDir(), "nonexistent-oidc-token"),
		RefreshPeriod: 10 * time.Minute,
	}

	m := newTestManager()
	var cred credential.Credential
	var err error
	out := captureKlogOutput(t, func() {
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
	// The OIDC tier must not have entered the chain: with EnableWorkerRole
	// disabled the registered chain consists solely of the AK-derived tiers.
	if strings.Contains(out, "OIDC/RRSA authentication registered") {
		t.Errorf("the file-based OIDC tier must not enter the chain with an auto-derived OidcArn and complete AK, got: %q", out)
	}
}

// TestGetAuthCred_AKWithDefaultOIDCAndRRSAEnabledUsesAKAssumeRole: case 1
// regression -- rrsa.enable=true semantics (the token file is really mounted
// on disk) + auto-derived OidcArn + complete AK pair, with no explicit RRSA
// intent in the store: the file-based OIDC tier must NOT enter the chain and
// AK AssumeRole takes effect (restored 0.6.2 contract).
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
	out := captureKlogOutput(t, func() {
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

// TestGetAuthCred_PureRRSARetainsDefaultOIDCTier verifies that without AK
// credentials (pure RRSA intent) the auto-generated OidcArn still enters
// the chain regardless of the token file's presence: with
// EnableWorkerRole=false an empty chain would fail closed, so a successful
// GetAuthCred proves the file-based OIDC tier was kept. TokenFilePath is
// pinned to a nonexistent path to show the decision is file-independent.
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

// TestGetAuthCred_ExplicitOIDCWithAKRetainsTier: an explicitly configured
// oidcProviderARN keeps the OIDC tier even with a complete AK pair (case 2):
// the "OIDC/RRSA authentication registered" log proves the OIDC tier entered
// the chain -- and, by construction in GetAuthCred, it is appended to the
// provider list before the AK+AssumeRole tier, so the OIDC tier wins by
// priority. Absence of the skip log pins the explicit-intent heuristic.
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
	out := captureKlogOutput(t, func() {
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
	// The OIDC tier must be registered; it is appended to the provider list
	// before the AK+AssumeRole tier by construction, so it precedes the AK
	// assume-role tier in the chain and wins by priority.
	if !strings.Contains(out, "OIDC/RRSA authentication registered") {
		t.Errorf("expected the OIDC tier to enter the chain ahead of the AK assume-role tier with an explicit oidcProviderARN, got: %q", out)
	}
	if strings.Contains(out, "auto-derived oidcProviderARN with complete AccessKey: AK AssumeRole takes precedence over file-based RRSA") {
		t.Errorf("explicitly configured oidcProviderARN must not trigger the AK-precedence skip log, got: %q", out)
	}
}

// TestGetAuthCred_SARRSAWithDefaultOIDCEntersTier verifies that an explicit
// serviceAccountRef keeps the OIDC tier in the chain even when the OidcArn
// is auto-generated: provider creation is driven through the dynamic-token
// branch and its fail-closed error (the fake clientset has no ServiceAccount)
// proves the tier was actually entered.
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

// newJWT builds an unsigned JWT whose exp claim is expDelta away from now.
// getTokenExpireTime parses tokens without signature verification, so the
// signature segment can be a constant.
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
// successful token-refresh path (refresh -> token/tokenExpireTime update ->
// STS assume). Drives the refresh to the STS boundary (unroutable endpoint):
// the connection error proves the refresh completed. A full
// AssumeRoleWithOIDC round-trip cannot be unit-tested (STS client hardcodes
// HTTPS with strict TLS).
func TestTokenOIDCProviderGetCredential_TokenRefreshSuccess(t *testing.T) {
	var calls int32
	p := NewTokenOIDCProvider("127.0.0.1:1", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test", time.Minute)
	// Valid cached credential, but the token is already expired. Per the actual
	// production formula (refresh when now > tokenExpireTime - 20% of the
	// remaining validity) the refresh threshold is only crossed once the
	// token is expired, so an expired token triggers the immediate refresh
	// while the still-valid credential keeps needCredentialRefresh false.
	p.credential = newSTSCredential(t)
	p.expireTime = time.Now().Add(time.Hour)
	p.tokenExpireTime = time.Now().Add(-time.Minute)
	// The refreshed token is a parsable JWT expiring in 5h: the provider must
	// re-derive tokenExpireTime from its exp claim (the unparsable fallback
	// would only yield ~1h, which the assertion below discriminates against).
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
	// claim (~5h): the unparsable-token fallback would only yield ~1h.
	if remaining := time.Until(p.tokenExpireTime); remaining <= 2*time.Hour || remaining > 5*time.Hour+time.Minute {
		t.Errorf("tokenExpireTime was not parsed from the refreshed token, remaining = %v", remaining)
	}
	// The cached credential must stay untouched when the assume fails.
	if remaining := time.Until(p.expireTime); remaining < 30*time.Minute {
		t.Errorf("cached credential expiration must stay untouched, remaining = %v", remaining)
	}
}

// TestTokenOIDCProviderGetCredential_TokenRefreshFailCredentialExpired covers
// the error path at the end of the token-refresh branch: the token refresh
// fails and the cached credential cannot be served (nil or already expired),
// so GetCredential must fail with "failed to refresh OIDC token" instead of
// returning a stale credential.
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
				"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test", time.Minute)
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

// TestTokenOIDCProviderConcurrentGetCredential_CacheHit runs concurrent
// GetCredential calls against a valid cached credential. With -race this
// proves that reads of token/credential/expireTime/tokenExpireTime are
// properly synchronized.
func TestTokenOIDCProviderConcurrentGetCredential_CacheHit(t *testing.T) {
	p := NewTokenOIDCProvider("sts.cn-hangzhou.aliyuncs.com", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test", time.Minute)
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

// TestTokenOIDCProviderConcurrentGetCredential_TokenRefreshError exercises
// the concurrent token-refresh path: the token is expired, getTokenFunc
// fails, and the still-valid cached credential must be served. With -race
// this proves the refresh path is race-free.
func TestTokenOIDCProviderConcurrentGetCredential_TokenRefreshError(t *testing.T) {
	p := NewTokenOIDCProvider("sts.cn-hangzhou.aliyuncs.com", "session", "unparsable-token",
		"acs:ram::123456789:role/test", "acs:ram::123456789:oidc-provider/test", time.Minute)
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

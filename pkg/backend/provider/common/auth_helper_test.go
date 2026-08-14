package common

import (
	"context"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ramprovider "github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// NOTE: tests in this file mutate process-global state (environment
// variables via t.Setenv, backend.EnableWorkerRole), so they rely on the
// package's default SERIAL test execution and must never call t.Parallel().

// TestBuildAuthConfigFromEnvFieldMapping pins the env-var -> AuthConfig
// field mapping of BuildAuthConfigFromEnv, including the internally applied
// NormalizeSessionNames defaults for the unset session-name variables.
func TestBuildAuthConfigFromEnvFieldMapping(t *testing.T) {
	t.Setenv("ALICLOUD_ROLE_ARN", "acs:ram::123456789:role/env-role")
	t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "acs:ram::123456789:oidc-provider/env-oidc")
	t.Setenv("ACCESS_KEY_ID", "env-ak")
	t.Setenv("SECRET_ACCESS_KEY", "env-sk")
	t.Setenv("ALICLOUD_ROLE_SESSION_NAME", "explicit-session")
	t.Setenv("ALICLOUD_ROLE_SESSION_EXPIRATION", "3600")
	t.Setenv("ALICLOUD_REMOTE_ROLE_SESSION_NAME", "") // unset-equivalent -> default injected
	t.Setenv("ALICLOUD_REMOTE_ROLE_ARN", "acs:ram::987654321:role/env-remote-role")

	cfg := BuildAuthConfigFromEnv()

	if cfg.ClientName != backend.EnvClient {
		t.Errorf("ClientName = %q, want %q", cfg.ClientName, backend.EnvClient)
	}
	if cfg.RoleArn != "acs:ram::123456789:role/env-role" {
		t.Errorf("RoleArn = %q, want ALICLOUD_ROLE_ARN", cfg.RoleArn)
	}
	if cfg.OidcArn != "acs:ram::123456789:oidc-provider/env-oidc" {
		t.Errorf("OidcArn = %q, want ALICLOUD_OIDC_PROVIDER_ARN", cfg.OidcArn)
	}
	if cfg.AccessKey != "env-ak" {
		t.Errorf("AccessKey = %q, want ACCESS_KEY_ID", cfg.AccessKey)
	}
	if cfg.AccessSecretKey != "env-sk" {
		t.Errorf("AccessSecretKey = %q, want SECRET_ACCESS_KEY", cfg.AccessSecretKey)
	}
	if cfg.RoleSessionName != "explicit-session" {
		t.Errorf("explicit RoleSessionName must be preserved, got %q", cfg.RoleSessionName)
	}
	if cfg.RoleSessionExpiration != "3600" {
		t.Errorf("RoleSessionExpiration = %q, want ALICLOUD_ROLE_SESSION_EXPIRATION", cfg.RoleSessionExpiration)
	}
	if cfg.RemoteRoleArn != "acs:ram::987654321:role/env-remote-role" {
		t.Errorf("RemoteRoleArn = %q, want ALICLOUD_REMOTE_ROLE_ARN", cfg.RemoteRoleArn)
	}
	// NormalizeSessionNames is applied inside BuildAuthConfigFromEnv: the
	// unset remote session name must already carry the default here.
	if cfg.RemoteRoleSessionName != "ack-secret-manager" {
		t.Errorf("unset RemoteRoleSessionName must be defaulted by the internal NormalizeSessionNames, got %q", cfg.RemoteRoleSessionName)
	}
	if cfg.RefreshPeriod != 10*time.Second {
		t.Errorf("RefreshPeriod = %v, want 10s", cfg.RefreshPeriod)
	}
}

// TestBuildAuthConfigFromEnvSessionNameDefaults verifies the internal
// NormalizeSessionNames call when BOTH session-name env vars are unset:
// defaults must be injected so the AK+AssumeRole and cross-account tiers
// are not skipped downstream.
func TestBuildAuthConfigFromEnvSessionNameDefaults(t *testing.T) {
	t.Setenv("ALICLOUD_ROLE_SESSION_NAME", "")
	t.Setenv("ALICLOUD_REMOTE_ROLE_SESSION_NAME", "")

	cfg := BuildAuthConfigFromEnv()

	if cfg.RoleSessionName != "ack-secret-manager" {
		t.Errorf("RoleSessionName = %q, want default %q", cfg.RoleSessionName, "ack-secret-manager")
	}
	if cfg.RemoteRoleSessionName != "ack-secret-manager" {
		t.Errorf("RemoteRoleSessionName = %q, want default %q", cfg.RemoteRoleSessionName, "ack-secret-manager")
	}
}

// TestGetAuthCredSessionNameInjection_ENVPath drives the production ENV
// config (BuildAuthConfigFromEnv, session-name vars unset) through
// GetAuthCred: the injected defaults keep the AK+AssumeRole tier in the
// chain. Tier selection is pinned by config shape as in the SecretStore-path
// counterpart (credential-type assertions are infeasible).
func TestGetAuthCredSessionNameInjection_ENVPath(t *testing.T) {
	t.Setenv("ALICLOUD_ROLE_ARN", "acs:ram::123456789:role/env-role")
	t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "")
	t.Setenv("ACCESS_KEY_ID", "env-ak")
	t.Setenv("SECRET_ACCESS_KEY", "env-sk")
	t.Setenv("ALICLOUD_ROLE_SESSION_NAME", "")
	t.Setenv("ALICLOUD_ROLE_SESSION_EXPIRATION", "")
	t.Setenv("ALICLOUD_REMOTE_ROLE_SESSION_NAME", "")
	t.Setenv("ALICLOUD_REMOTE_ROLE_ARN", "")

	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = false
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	cfg := BuildAuthConfigFromEnv()

	if cfg.RoleSessionName != "ack-secret-manager" {
		t.Fatalf("BuildAuthConfigFromEnv must inject the default RoleSessionName, got %q", cfg.RoleSessionName)
	}
	if cfg.RemoteRoleSessionName != "ack-secret-manager" {
		t.Fatalf("BuildAuthConfigFromEnv must inject the default RemoteRoleSessionName, got %q", cfg.RemoteRoleSessionName)
	}

	m := &backendp.Manager{
		RamLock:     &sync.Mutex{},
		RamProvider: make(map[string]ramprovider.Stopper),
	}
	cred, err := cfg.GetAuthCred("cn-hangzhou", 10, m)
	if err != nil {
		t.Fatalf("GetAuthCred returned error: %v", err)
	}
	if cred == nil {
		t.Fatal("GetAuthCred returned nil credential")
	}
	if _, ok := m.RamProvider[cfg.ClientName]; !ok {
		t.Fatalf("expected the provider chain to be registered under %q", cfg.ClientName)
	}
}

const (
	testClusterID = "c00000000000000000000000000000000"
	testUID       = "123456789"
)

// newAKSecretStoreFixture builds a SecretStore plus the referenced AK Secret
// used by the OidcArnFromDefault marking tests.
func newAKSecretStoreFixture() (*v1alpha1.SecretStore, *corev1.Secret) {
	store := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "ak-store", Namespace: "default"},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "ak-secret", Namespace: "default"},
		Data: map[string][]byte{
			"ak": []byte("test-ak"),
			"sk": []byte("test-sk"),
		},
	}
	return store, secret
}

func newKMSAuthAdapter(oidcProviderARN string) *KMSAuthAdapter {
	kmsAuth := &v1alpha1.KMSAuth{
		RAMRoleARN:      "acs:ram::123456789:role/test-role",
		OIDCProviderARN: oidcProviderARN,
		AccessKey:       &v1alpha1.SecretRef{Name: "ak-secret", Key: "ak"},
		AccessKeySecret: &v1alpha1.SecretRef{Name: "ak-secret", Key: "sk"},
	}
	return &KMSAuthAdapter{KMSAuth: kmsAuth, StoreName: "ak-store"}
}

// TestBuildAuthConfig_OidcArnFromDefaultMarkedTraditional verifies that the
// auto-generated OidcArn produced by buildAuthConfigWithCrossNamespace is
// marked with OidcArnFromDefault and the flag survives the field-by-field
// merge in BuildAuthConfig.
func TestBuildAuthConfig_OidcArnFromDefaultMarkedTraditional(t *testing.T) {
	store, secret := newAKSecretStoreFixture()
	kube := fake.NewClientBuilder().WithObjects(secret).Build()

	cfg, err := BuildAuthConfig(context.Background(), store, kube, newKMSAuthAdapter(""), testClusterID, testUID)
	if err != nil {
		t.Fatalf("BuildAuthConfig returned error: %v", err)
	}
	wantArn := utils.GenerateDefaultOidcProviderArn(testClusterID, 123456789)
	if cfg.OidcArn != wantArn {
		t.Errorf("OidcArn = %q, want generated default %q", cfg.OidcArn, wantArn)
	}
	if !cfg.OidcArnFromDefault {
		t.Error("OidcArnFromDefault must be true for the auto-generated OidcArn")
	}
	if cfg.AccessKey != "test-ak" || cfg.AccessSecretKey != "test-sk" {
		t.Errorf("AK credentials were not merged, got ak=%q sk=%q", cfg.AccessKey, cfg.AccessSecretKey)
	}
}

// TestBuildAuthConfig_ExplicitOidcArnNotMarked verifies that an explicitly
// configured oidcProviderARN is never marked as auto-generated, so the
// OIDC/RRSA tier keeps entering the chain for explicit user choices.
func TestBuildAuthConfig_ExplicitOidcArnNotMarked(t *testing.T) {
	store, secret := newAKSecretStoreFixture()
	kube := fake.NewClientBuilder().WithObjects(secret).Build()
	explicitArn := "acs:ram::123456789:oidc-provider/explicit-oidc"

	cfg, err := BuildAuthConfig(context.Background(), store, kube, newKMSAuthAdapter(explicitArn), testClusterID, testUID)
	if err != nil {
		t.Fatalf("BuildAuthConfig returned error: %v", err)
	}
	if cfg.OidcArn != explicitArn {
		t.Errorf("OidcArn = %q, want the explicitly configured %q", cfg.OidcArn, explicitArn)
	}
	if cfg.OidcArnFromDefault {
		t.Error("OidcArnFromDefault must stay false for an explicitly configured oidcProviderARN")
	}
}

// fakeClientWithKubeClient wraps the controller-runtime fake client so it
// satisfies the GetKubeClient() interface BuildAuthConfig probes for: the
// serviceAccountRef path fail-closes when KubeClient is nil, so the merged
// flag can only be exercised through a wrapped client.
type fakeClientWithKubeClient struct {
	client.Client
	kube kubernetes.Interface
}

func (w fakeClientWithKubeClient) GetKubeClient() kubernetes.Interface { return w.kube }

// TestBuildAuthConfig_OidcArnFromDefaultMarkedServiceAccount verifies the
// marking on the serviceAccountRef path through the BuildAuthConfig entry
// point (symmetric with the traditional-path test): with no oidcProviderARN
// configured the generated ARN must be marked, and the flag must survive
// the serviceAccountRef merge in BuildAuthConfig.
func TestBuildAuthConfig_OidcArnFromDefaultMarkedServiceAccount(t *testing.T) {
	store := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "sa-store", Namespace: "default"},
	}
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rrsa-sa",
			Namespace: "default",
			Annotations: map[string]string{
				ACKRRSAAnnotation: "acs:ram::123456789:role/sa-role",
			},
		},
	}
	kube := fakeClientWithKubeClient{
		Client: fake.NewClientBuilder().WithObjects(sa).Build(),
		kube:   k8sfake.NewSimpleClientset(),
	}

	kmsAuth := &v1alpha1.KMSAuth{
		ServiceAccountRef: &v1alpha1.ServiceAccountRef{Name: "rrsa-sa"},
	}

	cfg, err := BuildAuthConfig(context.Background(), store, kube, &KMSAuthAdapter{KMSAuth: kmsAuth, StoreName: "sa-store"}, testClusterID, testUID)
	if err != nil {
		t.Fatalf("BuildAuthConfig returned error: %v", err)
	}
	wantArn := utils.GenerateDefaultOidcProviderArn(testClusterID, 123456789)
	if cfg.OidcArn != wantArn {
		t.Errorf("OidcArn = %q, want generated default %q", cfg.OidcArn, wantArn)
	}
	if !cfg.OidcArnFromDefault {
		t.Error("OidcArnFromDefault must be true for the auto-generated OidcArn merged from the serviceAccountRef branch")
	}
	if cfg.ServiceAccountName != "rrsa-sa" || cfg.ServiceAccountNamespace != "default" {
		t.Errorf("ServiceAccount fields were not merged, got %s/%s", cfg.ServiceAccountNamespace, cfg.ServiceAccountName)
	}
}

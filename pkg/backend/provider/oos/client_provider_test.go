package oos

// client_provider_test.go covers the OOS provider. The composite client-name
// assertions (custom / empty / whitespace-padded / whitespace-only endpoints
// on both the ENV and SecretStore paths) are shared with KMS and delegated to
// pkg/backend/provider/providertest. What stays here is OOS-specific: the
// "custom endpoint is ignored, with a warning" behavior, exercised both
// directly (warnIfEndpointIgnored) and through real client construction.

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/providertest"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/testutil"
)

// NOTE: tests in this file mutate process-global klog state (via
// testutil.CaptureKlogOutput) and backend.EnableWorkerRole, so they rely on
// SERIAL execution and must never call t.Parallel(). newTestProvider bypasses
// NewProvider so the global backend provider registry is untouched.

// A non-empty endpoint must produce a warning explaining that OOS always uses
// the default domain and the configured endpoint is ignored.
func TestWarnIfEndpointIgnoredEmitsWarning(t *testing.T) {
	out := testutil.CaptureKlogOutput(t, func() {
		warnIfEndpointIgnored("kms.custom-endpoint.example.com")
	})

	if !strings.Contains(out, "custom endpoint is not supported for OOS and will be ignored") {
		t.Fatalf("expected ignored-endpoint warning in klog output, got: %q", out)
	}
	if !strings.Contains(out, "kms.custom-endpoint.example.com") {
		t.Fatalf("warning should contain the configured endpoint, got: %q", out)
	}
	if !strings.Contains(out, "ignored") {
		t.Fatalf("warning should state the endpoint is ignored, got: %q", out)
	}
}

// An empty endpoint must not produce any warning.
func TestWarnIfEndpointIgnoredEmptyEndpointSilent(t *testing.T) {
	out := testutil.CaptureKlogOutput(t, func() {
		warnIfEndpointIgnored("")
	})

	if strings.Contains(out, "custom endpoint is not supported for OOS and will be ignored") {
		t.Fatalf("empty endpoint must not emit the ignored-endpoint warning, got: %q", out)
	}
}

// newTestProvider builds an OOS provider WITHOUT going through NewProvider,
// so the global backend provider registry is not touched by these tests.
func newTestProvider() *Provider {
	return &Provider{
		Manager:            NewManager("cn-hangzhou"),
		region:             "cn-hangzhou",
		name:               backend.ProviderOOSName,
		maxConcurrentCount: 10,
	}
}

// TestNewClientByENVIgnoresCustomEndpoint covers NewClientByENV with a custom
// endpoint: the warning is emitted during real construction, the endpoint is
// ignored (default OOS domain used, lazy auth chain), yet the client name keeps
// the composite "#endpoint" suffix so the registry key stays aligned with the
// ExternalSecret controller's composite cache key.
func TestNewClientByENVIgnoresCustomEndpoint(t *testing.T) {
	t.Setenv("ACCESS_KEY_ID", "env-ak")
	t.Setenv("SECRET_ACCESS_KEY", "env-sk")
	t.Setenv("ALICLOUD_ROLE_ARN", "")
	t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "")

	p := newTestProvider()
	out := testutil.CaptureKlogOutput(t, func() {
		cl, err := p.NewClientByENV("oos.custom-endpoint.example.com")
		if err != nil {
			t.Fatalf("NewClientByENV returned error: %v", err)
		}
		oosCl, ok := cl.(*OOSClient)
		if !ok || oosCl == nil {
			t.Fatalf("expected an *OOSClient, got %T", cl)
		}
		wantName := backend.EnvClient + "#oos.custom-endpoint.example.com"
		if oosCl.GetName() != wantName {
			t.Errorf("client name = %q, want %q", oosCl.GetName(), wantName)
		}
	})

	if !strings.Contains(out, "custom endpoint is not supported for OOS and will be ignored") ||
		!strings.Contains(out, "oos.custom-endpoint.example.com") {
		t.Fatalf("expected the ignored-endpoint warning with the configured endpoint, got: %q", out)
	}
}

// TestNewClientIgnoresCustomEndpoint covers NewClient with a custom endpoint:
// the endpoint is ignored with a warning (default domain, WorkerRole provides
// a lazy auth tier), and the client name keeps the composite "#endpoint"
// suffix to stay aligned with the controller's composite cache key.
func TestNewClientIgnoresCustomEndpoint(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = true
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	p := newTestProvider()
	store := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "endpoint-store", Namespace: "default"},
	}

	out := testutil.CaptureKlogOutput(t, func() {
		cl, err := p.NewClient(context.Background(), store, fake.NewClientBuilder().Build(), "oos.custom-endpoint.example.com")
		if err != nil {
			t.Fatalf("NewClient returned error: %v", err)
		}
		oosCl, ok := cl.(*OOSClient)
		if !ok || oosCl == nil {
			t.Fatalf("expected an *OOSClient, got %T", cl)
		}
		wantName := "namespace/default/endpoint-store#oos.custom-endpoint.example.com"
		if oosCl.GetName() != wantName {
			t.Errorf("client name = %q, want %q", oosCl.GetName(), wantName)
		}
	})

	if !strings.Contains(out, "custom endpoint is not supported for OOS and will be ignored") ||
		!strings.Contains(out, "oos.custom-endpoint.example.com") {
		t.Fatalf("expected the ignored-endpoint warning with the configured endpoint, got: %q", out)
	}
}

// TestCompositeClientNameContract runs the shared composite client-name
// contract against the OOS provider (the whitespace-normalization and
// empty-endpoint naming cases previously duplicated here). OOS ignores the
// custom endpoint for routing but still keeps the composite name, so the same
// contract as KMS applies.
func TestCompositeClientNameContract(t *testing.T) {
	providertest.RunCompositeClientNameContract(t,
		func() providertest.EndpointClientFactory { return newTestProvider() },
		"oos.custom-endpoint.example.com",
	)
}

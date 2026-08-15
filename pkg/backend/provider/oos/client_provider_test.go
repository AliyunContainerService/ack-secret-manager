package oos

import (
	"bytes"
	"context"
	"flag"
	"os"
	"strings"
	"sync"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
)

// NOTE: tests in this file mutate process-global state (the klog
// "logtostderr" flag and klog's output writer), so they rely on the
// package's default SERIAL test execution and must never call t.Parallel().
// The NewClient/NewClientByENV tests build their provider via
// newTestProvider, which intentionally bypasses NewProvider so the global
// backend provider registry is not touched.

var initKlogFlagsOnce sync.Once

// captureKlogOutput redirects klog output to a buffer for the duration of fn.
// klog writes to stderr directly when logtostderr=true (the default), so the
// flag must be flipped off for SetOutput to take effect. klog v1 registers
// its flags only via an explicit InitFlags call.
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

// A non-empty endpoint must produce a warning explaining that OOS always uses
// the default domain and the configured endpoint is ignored.
func TestWarnIfEndpointIgnoredEmitsWarning(t *testing.T) {
	out := captureKlogOutput(t, func() {
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
	out := captureKlogOutput(t, func() {
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

// TestNewClientByENVIgnoresCustomEndpoint covers the actual behavior path
// of NewClientByENV with a custom endpoint: the warning is emitted, the
// endpoint is ignored, and the client is still constructed against the
// default OOS domain. Credentials come from env vars so no real API call
// happens at construction time (the auth chain is lazy). The client name
// still carries the composite "#endpoint" suffix so the cache/RAM-provider
// registry key stays aligned with the ExternalSecret controller's
// composite cache key, even though OOS ignores the endpoint value.
func TestNewClientByENVIgnoresCustomEndpoint(t *testing.T) {
	t.Setenv("ACCESS_KEY_ID", "env-ak")
	t.Setenv("SECRET_ACCESS_KEY", "env-sk")
	t.Setenv("ALICLOUD_ROLE_ARN", "")
	t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "")

	p := newTestProvider()
	out := captureKlogOutput(t, func() {
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

// TestNewClientIgnoresCustomEndpoint covers the actual behavior path of
// NewClient with a custom endpoint: the endpoint is ignored with a warning;
// the client is built against the default domain. WorkerRole is enabled so
// the unconfigured store has a lazy (no-network) auth tier. The client name
// keeps the composite "#endpoint" suffix to stay aligned with the
// ExternalSecret controller's composite cache key.
func TestNewClientIgnoresCustomEndpoint(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = true
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	p := newTestProvider()
	store := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "endpoint-store", Namespace: "default"},
	}

	out := captureKlogOutput(t, func() {
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

// TestWhitespaceEndpointSameCompositeClientName pins the endpoint
// normalization contract: an endpoint with leading/trailing whitespace must
// produce the exact same composite client name as its trimmed form, on both
// the ENV path and the SecretStore path, so the OOS registration key stays
// aligned with the ExternalSecret controller's composite cache key.
func TestWhitespaceEndpointSameCompositeClientName(t *testing.T) {
	t.Run("NewClientByENV", func(t *testing.T) {
		t.Setenv("ACCESS_KEY_ID", "env-ak")
		t.Setenv("SECRET_ACCESS_KEY", "env-sk")
		t.Setenv("ALICLOUD_ROLE_ARN", "")
		t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "")

		p := newTestProvider()
		cl, err := p.NewClientByENV("  oos.custom-endpoint.example.com\t")
		if err != nil {
			t.Fatalf("NewClientByENV with whitespace endpoint returned error: %v", err)
		}
		oosCl, ok := cl.(*OOSClient)
		if !ok || oosCl == nil {
			t.Fatalf("expected an *OOSClient, got %T", cl)
		}
		wantName := backend.EnvClient + "#oos.custom-endpoint.example.com"
		if oosCl.GetName() != wantName {
			t.Errorf("whitespace endpoint client name = %q, want %q (same as trimmed form)", oosCl.GetName(), wantName)
		}
	})

	t.Run("NewClient", func(t *testing.T) {
		prevWorkerRole := backend.EnableWorkerRole
		backend.EnableWorkerRole = true
		defer func() { backend.EnableWorkerRole = prevWorkerRole }()

		p := newTestProvider()
		store := &v1alpha1.SecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: "endpoint-store", Namespace: "default"},
		}
		cl, err := p.NewClient(context.Background(), store, fake.NewClientBuilder().Build(), " oos.custom-endpoint.example.com ")
		if err != nil {
			t.Fatalf("NewClient with whitespace endpoint returned error: %v", err)
		}
		oosCl, ok := cl.(*OOSClient)
		if !ok || oosCl == nil {
			t.Fatalf("expected an *OOSClient, got %T", cl)
		}
		wantName := "namespace/default/endpoint-store#oos.custom-endpoint.example.com"
		if oosCl.GetName() != wantName {
			t.Errorf("whitespace endpoint client name = %q, want %q (same as trimmed form)", oosCl.GetName(), wantName)
		}
	})

	t.Run("whitespace-only endpoint keeps plain name", func(t *testing.T) {
		t.Setenv("ACCESS_KEY_ID", "env-ak")
		t.Setenv("SECRET_ACCESS_KEY", "env-sk")
		t.Setenv("ALICLOUD_ROLE_ARN", "")
		t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "")

		p := newTestProvider()
		cl, err := p.NewClientByENV("   ")
		if err != nil {
			t.Fatalf("NewClientByENV(whitespace-only) returned error: %v", err)
		}
		oosCl, ok := cl.(*OOSClient)
		if !ok || oosCl == nil {
			t.Fatalf("expected an *OOSClient, got %T", cl)
		}
		if oosCl.GetName() != backend.EnvClient {
			t.Errorf("whitespace-only endpoint client name = %q, want plain %q", oosCl.GetName(), backend.EnvClient)
		}
	})
}

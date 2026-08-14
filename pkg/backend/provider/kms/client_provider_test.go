// Copyright © 2025 Alibaba Cloud. All rights reserved.

// client_provider_test.go pins the composite-key alignment contract of the
// KMS provider: a non-empty custom endpoint makes NewClient/NewClientByENV
// register the client under "clientName#endpoint", the same composite key
// the ExternalSecret controller caches, so RAM provider registration and
// Delete/Stop stay symmetric. An empty endpoint keeps the plain clientName.
// Construction is fully offline: env-var / lazy WorkerRole credentials mean
// no network call happens at construction time (the auth chain is lazy).
// The custom endpoint must satisfy validateKmsEndpoint (SSRF guard), so the
// tests use a valid shared-gateway hostname.

package kms

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
)

// testEndpoint is a valid shared-gateway KMS endpoint (passes
// validateKmsEndpoint) that is NOT a cryptoservice domain, so no CA lookup
// is involved during construction.
const testEndpoint = "kms.cn-shanghai.aliyuncs.com"

// newTestProvider builds a KMS provider WITHOUT going through NewProvider,
// so the global backend provider registry is not touched by these tests.
func newTestProvider() *Provider {
	return &Provider{
		Manager:            NewManager("cn-hangzhou"),
		region:             "cn-hangzhou",
		name:               backend.ProviderKMSName,
		maxConcurrentCount: 10,
	}
}

// TestNewClientByENVCompositeClientName covers the env-var credential path:
// a custom endpoint yields the composite "env.client#<endpoint>" client
// name, and an empty endpoint keeps the plain env.client name.
func TestNewClientByENVCompositeClientName(t *testing.T) {
	t.Setenv("ACCESS_KEY_ID", "env-ak")
	t.Setenv("SECRET_ACCESS_KEY", "env-sk")
	t.Setenv("ALICLOUD_ROLE_ARN", "")
	t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "")

	p := newTestProvider()

	cl, err := p.NewClientByENV(testEndpoint)
	if err != nil {
		t.Fatalf("NewClientByENV returned error: %v", err)
	}
	kmsCl, ok := cl.(*KMSClient)
	if !ok || kmsCl == nil {
		t.Fatalf("expected a *KMSClient, got %T", cl)
	}
	wantName := backend.EnvClient + "#" + testEndpoint
	if kmsCl.GetName() != wantName {
		t.Errorf("client name = %q, want %q", kmsCl.GetName(), wantName)
	}

	// Regression guard: without a custom endpoint the plain name is kept.
	cl, err = p.NewClientByENV("")
	if err != nil {
		t.Fatalf("NewClientByENV(\"\") returned error: %v", err)
	}
	kmsCl, ok = cl.(*KMSClient)
	if !ok || kmsCl == nil {
		t.Fatalf("expected a *KMSClient, got %T", cl)
	}
	if kmsCl.GetName() != backend.EnvClient {
		t.Errorf("client name = %q, want %q", kmsCl.GetName(), backend.EnvClient)
	}
}

// TestNewClientCompositeClientName covers the SecretStore path: WorkerRole
// is enabled so the unconfigured store has a lazy (no-network) auth tier,
// and a custom endpoint yields "namespace/default/<store>#<endpoint>".
func TestNewClientCompositeClientName(t *testing.T) {
	prevWorkerRole := backend.EnableWorkerRole
	backend.EnableWorkerRole = true
	defer func() { backend.EnableWorkerRole = prevWorkerRole }()

	p := newTestProvider()
	store := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{Name: "endpoint-store", Namespace: "default"},
	}

	cl, err := p.NewClient(context.Background(), store, fake.NewClientBuilder().Build(), testEndpoint)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	kmsCl, ok := cl.(*KMSClient)
	if !ok || kmsCl == nil {
		t.Fatalf("expected a *KMSClient, got %T", cl)
	}
	wantName := "namespace/default/endpoint-store#" + testEndpoint
	if kmsCl.GetName() != wantName {
		t.Errorf("client name = %q, want %q", kmsCl.GetName(), wantName)
	}

	// Regression guard: without a custom endpoint the plain name is kept.
	cl, err = p.NewClient(context.Background(), store, fake.NewClientBuilder().Build(), "")
	if err != nil {
		t.Fatalf("NewClient(\"\") returned error: %v", err)
	}
	kmsCl, ok = cl.(*KMSClient)
	if !ok || kmsCl == nil {
		t.Fatalf("expected a *KMSClient, got %T", cl)
	}
	if kmsCl.GetName() != "namespace/default/endpoint-store" {
		t.Errorf("client name = %q, want %q", kmsCl.GetName(), "namespace/default/endpoint-store")
	}
}

// TestWhitespaceEndpointSameCompositeClientName pins the endpoint
// normalization contract: an endpoint with leading/trailing whitespace must
// produce the exact same composite client name as its trimmed form, on both
// the ENV path and the SecretStore path. Without this, whitespace variants
// would pass validateKmsEndpoint (which trims before matching) but pollute
// the composite key and misalign with the controller-side cache key.
func TestWhitespaceEndpointSameCompositeClientName(t *testing.T) {
	t.Run("NewClientByENV", func(t *testing.T) {
		t.Setenv("ACCESS_KEY_ID", "env-ak")
		t.Setenv("SECRET_ACCESS_KEY", "env-sk")
		t.Setenv("ALICLOUD_ROLE_ARN", "")
		t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "")

		p := newTestProvider()
		cl, err := p.NewClientByENV("  " + testEndpoint + "\t")
		if err != nil {
			t.Fatalf("NewClientByENV with whitespace endpoint returned error: %v", err)
		}
		kmsCl, ok := cl.(*KMSClient)
		if !ok || kmsCl == nil {
			t.Fatalf("expected a *KMSClient, got %T", cl)
		}
		wantName := backend.EnvClient + "#" + testEndpoint
		if kmsCl.GetName() != wantName {
			t.Errorf("whitespace endpoint client name = %q, want %q (same as trimmed form)", kmsCl.GetName(), wantName)
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
		cl, err := p.NewClient(context.Background(), store, fake.NewClientBuilder().Build(), "\n "+testEndpoint+" ")
		if err != nil {
			t.Fatalf("NewClient with whitespace endpoint returned error: %v", err)
		}
		kmsCl, ok := cl.(*KMSClient)
		if !ok || kmsCl == nil {
			t.Fatalf("expected a *KMSClient, got %T", cl)
		}
		wantName := "namespace/default/endpoint-store#" + testEndpoint
		if kmsCl.GetName() != wantName {
			t.Errorf("whitespace endpoint client name = %q, want %q (same as trimmed form)", kmsCl.GetName(), wantName)
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
		kmsCl, ok := cl.(*KMSClient)
		if !ok || kmsCl == nil {
			t.Fatalf("expected a *KMSClient, got %T", cl)
		}
		if kmsCl.GetName() != backend.EnvClient {
			t.Errorf("whitespace-only endpoint client name = %q, want plain %q", kmsCl.GetName(), backend.EnvClient)
		}
	})
}

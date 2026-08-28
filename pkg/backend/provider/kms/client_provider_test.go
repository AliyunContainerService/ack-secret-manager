// Copyright © 2025 Alibaba Cloud. All rights reserved.

// client_provider_test.go pins the composite-key alignment contract of the
// KMS provider: a non-empty custom endpoint registers the client under
// "clientName#endpoint" (the same key the ExternalSecret controller caches),
// keeping RAM registration and Delete/Stop symmetric; an empty endpoint keeps
// the plain clientName. The shared naming assertions live in
// pkg/backend/provider/providertest and are reused by the OOS provider too;
// only KMS-specific construction (offline lazy auth chain, a testEndpoint that
// passes the validateKmsEndpoint SSRF guard) lives here.

package kms

import (
	"testing"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/providertest"
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

// TestCompositeClientNameContract runs the shared composite client-name
// contract against the KMS provider: it covers the ENV and SecretStore paths
// with custom / empty / whitespace-padded / whitespace-only endpoints, pinning
// that a non-empty (even padded) endpoint yields "<base>#<trimmed>" while an
// empty or whitespace-only endpoint keeps the plain base name.
func TestCompositeClientNameContract(t *testing.T) {
	providertest.RunCompositeClientNameContract(t,
		func() providertest.EndpointClientFactory { return newTestProvider() },
		testEndpoint,
	)
}

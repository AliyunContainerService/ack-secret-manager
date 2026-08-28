// Copyright © 2025 Alibaba Cloud. All rights reserved.

// provider_test.go covers the lazy, retryable ENV client initialization
// (EnsureENVClient) and its failure negative cache.

package backend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// countingENVProvider is a Provider stub counting NewClientByENV calls and
// Register calls, with optional per-attempt failure.
type countingENVProvider struct {
	name       string
	newCalls   atomic.Int32
	failFirst  atomic.Bool // fail the first NewClientByENV call, then succeed
	registered sync.Map
}

func (p *countingENVProvider) Register(clientKey string, secretClient SecretClient) {
	p.registered.Store(clientKey, secretClient)
}

func (p *countingENVProvider) GetClient(clientKey string) (SecretClient, error) {
	if c, ok := p.registered.Load(clientKey); ok {
		return c.(SecretClient), nil
	}
	return nil, fmt.Errorf("client %s not registered", clientKey)
}

func (p *countingENVProvider) Delete(clientKey string) { p.registered.Delete(clientKey) }

// DeletePrefixed implements the backend.ClientManager contract documented
// on the interface (see provider.go): the plain clientKey client plus every
// composite "clientKey#endpoint" variant are removed, nothing else.
func (p *countingENVProvider) DeletePrefixed(clientKey string) {
	compositePrefix := clientKey + "#"
	var staleKeys []string
	p.registered.Range(func(key, _ any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		if k == clientKey || strings.HasPrefix(k, compositePrefix) {
			staleKeys = append(staleKeys, k)
		}
		return true
	})
	for _, k := range staleKeys {
		p.registered.Delete(k)
	}
}

func (p *countingENVProvider) NewClient(context.Context, *v1alpha1.SecretStore, client.Client, string) (SecretClient, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *countingENVProvider) NewClientByENV(string) (SecretClient, error) {
	p.newCalls.Add(1)
	if p.failFirst.CompareAndSwap(true, false) {
		return nil, fmt.Errorf("simulated ENV init failure")
	}
	return &stubENVSecretClient{name: p.name}, nil
}

func (p *countingENVProvider) GetName() string      { return p.name }
func (p *countingENVProvider) GetRegion() string    { return "" }
func (p *countingENVProvider) GetEndpoint() string  { return "" }
func (p *countingENVProvider) GetClusterId() string { return "" }
func (p *countingENVProvider) GetUid() string       { return "" }

type stubENVSecretClient struct{ name string }

func (c *stubENVSecretClient) GetName() string { return c.name }
func (c *stubENVSecretClient) GetExternalSecret(context.Context, *v1alpha1.DataSource, client.Client) (map[string][]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (c *stubENVSecretClient) GetExternalSecretWithExtract(context.Context, *v1alpha1.DataProcess, client.Client) (map[string][]byte, error) {
	return nil, fmt.Errorf("not implemented")
}

// withTestProvider swaps the global provider registry slot and restores it
// on cleanup. Mutates process-global state: tests here must stay serial.
func withTestProvider(t *testing.T, name string, p Provider) {
	t.Helper()
	prev := GetProviderByName(name)
	RegisterProvider(name, p)
	t.Cleanup(func() {
		if prev != nil {
			RegisterProvider(name, prev)
		} else {
			DeleteProvider(name)
		}
	})
}

// resetENVClientInitForTest clears the failure negative cache and any test
// clock / TTL overrides; call it at the top of every failure-driven test.
func resetENVClientInitForTest(t *testing.T) {
	t.Helper()
	envClientInitMu.Lock()
	envInitFailureErr = nil
	envInitFailedAt = time.Time{}
	envNow = time.Now
	envInitFailureTTLOverride = 0
	envClientInitMu.Unlock()
	t.Cleanup(func() {
		envClientInitMu.Lock()
		envInitFailureErr = nil
		envInitFailedAt = time.Time{}
		envNow = time.Now
		envInitFailureTTLOverride = 0
		envClientInitMu.Unlock()
	})
}

// TestEnsureENVClientConcurrentInitOnce verifies concurrent consumers
// trigger ENV client construction exactly once per provider.
func TestEnsureENVClientConcurrentInitOnce(t *testing.T) {
	resetENVClientInitForTest(t)
	p := &countingENVProvider{name: "env-test"}
	withTestProvider(t, "env-test", p)

	const goroutines = 32
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errCh <- EnsureENVClient()
		}()
	}
	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("EnsureENVClient() returned error: %v", err)
		}
	}
	if got := p.newCalls.Load(); got != 1 {
		t.Errorf("NewClientByENV called %d times, want exactly 1", got)
	}
	if _, err := p.GetClient(EnvClient); err != nil {
		t.Errorf("ENV client not registered after EnsureENVClient: %v", err)
	}
	// A subsequent call stays a no-op.
	if err := EnsureENVClient(); err != nil {
		t.Fatalf("second EnsureENVClient() returned error: %v", err)
	}
	if got := p.newCalls.Load(); got != 1 {
		t.Errorf("NewClientByENV called %d times after second call, want still 1", got)
	}
}

// TestEnsureENVClientRetriesAfterFailure verifies that a failed attempt
// arms the negative cache and the missing providers are retried after the
// TTL expires (unlike a one-shot sync.Once).
func TestEnsureENVClientRetriesAfterFailure(t *testing.T) {
	resetENVClientInitForTest(t)

	// Fake clock so the TTL expiry can be reached without sleeping.
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	envClientInitMu.Lock()
	envNow = func() time.Time { return now }
	envClientInitMu.Unlock()

	p := &countingENVProvider{name: "env-test-retry"}
	p.failFirst.Store(true)
	withTestProvider(t, "env-test-retry", p)

	if err := EnsureENVClient(); err == nil {
		t.Fatal("first EnsureENVClient() must surface the provider failure")
	}
	if _, err := p.GetClient(EnvClient); err == nil {
		t.Fatal("ENV client must not be registered after a failed init")
	}

	// Retry after the TTL expires: the provider now succeeds.
	now = now.Add(envInitFailureTTL)
	if err := EnsureENVClient(); err != nil {
		t.Fatalf("retry EnsureENVClient() returned error: %v", err)
	}
	if _, err := p.GetClient(EnvClient); err != nil {
		t.Errorf("ENV client not registered after retry: %v", err)
	}
	if got := p.newCalls.Load(); got != 2 {
		t.Errorf("NewClientByENV called %d times, want 2 (failed attempt + retry)", got)
	}

	// Once registered, further calls do not reconstruct the client.
	if err := EnsureENVClient(); err != nil {
		t.Fatalf("EnsureENVClient() after success returned error: %v", err)
	}
	if got := p.newCalls.Load(); got != 2 {
		t.Errorf("NewClientByENV called %d times, want still 2", got)
	}
}

// TestEnsureENVClientFailureNegativeCache verifies calls within the TTL
// return the cached error without rebuilding, retrying only after expiry.
func TestEnsureENVClientFailureNegativeCache(t *testing.T) {
	resetENVClientInitForTest(t)

	// Fake clock: advance manually.
	now := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	envClientInitMu.Lock()
	envNow = func() time.Time { return now }
	envClientInitMu.Unlock()

	// Persistently failing provider until succeed is flipped.
	p := &failingENVProvider{countingENVProvider: countingENVProvider{name: "env-test-negcache"}}
	withTestProvider(t, "env-test-negcache", p)

	// First attempt fails and engages the negative cache.
	firstErr := EnsureENVClient()
	if firstErr == nil {
		t.Fatal("first EnsureENVClient() must surface the provider failure")
	}
	if got := p.newCalls.Load(); got != 1 {
		t.Fatalf("NewClientByENV called %d times after first attempt, want 1", got)
	}

	// Within the TTL: cached error returned verbatim, no reconstruction.
	now = now.Add(envInitFailureTTL - time.Second)
	for i := 0; i < 3; i++ {
		if err := EnsureENVClient(); err == nil || err.Error() != firstErr.Error() {
			t.Fatalf("EnsureENVClient() within TTL = %v, want cached error %v", err, firstErr)
		}
	}
	if got := p.newCalls.Load(); got != 1 {
		t.Errorf("NewClientByENV called %d times within TTL, want still 1 (negative cache hit)", got)
	}

	// TTL expired: retried, still failing, cache re-armed.
	now = now.Add(2 * time.Second)
	if err := EnsureENVClient(); err == nil {
		t.Fatal("EnsureENVClient() after TTL expiry must retry and surface the failure")
	}
	if got := p.newCalls.Load(); got != 2 {
		t.Errorf("NewClientByENV called %d times after TTL expiry, want 2 (retry)", got)
	}

	// Cached again; then flip the provider to success.
	p.succeed.Store(true)
	now = now.Add(envInitFailureTTL - time.Second)
	if err := EnsureENVClient(); err == nil {
		t.Fatal("EnsureENVClient() within second TTL window must return the cached error")
	}
	if got := p.newCalls.Load(); got != 2 {
		t.Errorf("NewClientByENV called %d times within second TTL window, want still 2", got)
	}
	now = now.Add(time.Second)
	if err := EnsureENVClient(); err != nil {
		t.Fatalf("EnsureENVClient() after second TTL expiry returned error: %v", err)
	}
	if _, err := p.GetClient(EnvClient); err != nil {
		t.Errorf("ENV client not registered after recovery: %v", err)
	}
	// Success clears the negative cache; subsequent calls are no-ops.
	if err := EnsureENVClient(); err != nil {
		t.Fatalf("EnsureENVClient() after recovery returned error: %v", err)
	}
	if got := p.newCalls.Load(); got != 3 {
		t.Errorf("NewClientByENV called %d times, want 3 (no rebuild after registration)", got)
	}
}

// TestEnsureENVClientPartialSuccessNoNegativeCache pins the partial-progress
// contract of EnsureENVClient: when at least one provider registers its ENV
// client while another fails, the error is surfaced but the negative cache
// must NOT be armed, so the failing provider is retried immediately (no TTL
// wait) on the very next call.
func TestEnsureENVClientPartialSuccessNoNegativeCache(t *testing.T) {
	resetENVClientInitForTest(t)

	ok := &countingENVProvider{name: "env-part-ok"}
	failing := &failingENVProvider{countingENVProvider: countingENVProvider{name: "env-part-fail"}}
	withTestProvider(t, "env-part-ok", ok)
	withTestProvider(t, "env-part-fail", failing)

	// First attempt: one provider succeeds, one fails -> error surfaced,
	// but registered > 0 forbids the negative cache.
	if err := EnsureENVClient(); err == nil {
		t.Fatal("EnsureENVClient() must surface the failing provider's error on partial success")
	}
	if _, err := ok.GetClient(EnvClient); err != nil {
		t.Fatalf("successful provider's ENV client must be registered despite the other failure: %v", err)
	}
	if got := failing.newCalls.Load(); got != 1 {
		t.Fatalf("failing provider NewClientByENV called %d times, want 1", got)
	}

	// Immediate retry WITHOUT any clock advance: a cached negative result
	// would have swallowed this call. Flip the provider to success instead.
	failing.succeed.Store(true)
	if err := EnsureENVClient(); err != nil {
		t.Fatalf("immediate retry after partial success returned error: %v", err)
	}
	if got := failing.newCalls.Load(); got != 2 {
		t.Errorf("failing provider NewClientByENV called %d times, want 2 (immediate retry proves no negative cache)", got)
	}
	if _, err := failing.GetClient(EnvClient); err != nil {
		t.Errorf("recovered provider's ENV client not registered: %v", err)
	}
	// Already-registered providers are skipped, not rebuilt.
	if got := ok.newCalls.Load(); got != 1 {
		t.Errorf("successful provider NewClientByENV called %d times, want still 1", got)
	}
}

// failingENVProvider fails NewClientByENV until succeed is flipped.
type failingENVProvider struct {
	countingENVProvider
	succeed atomic.Bool
}

func (p *failingENVProvider) NewClientByENV(string) (SecretClient, error) {
	p.newCalls.Add(1)
	if !p.succeed.Load() {
		return nil, fmt.Errorf("simulated persistent ENV init failure")
	}
	return &stubENVSecretClient{name: p.name}, nil
}

// TestDeletePrefixedRemovesCompositeClients pins the DeletePrefixed contract
// declared on backend.ClientManager (provider.go): a store-level lifecycle
// event retires the plain clientName client together with every composite
// "clientName#endpoint" variant registered for a custom endpoint, while
// clients of other stores stay untouched.
func TestDeletePrefixedRemovesCompositeClients(t *testing.T) {
	p := &countingENVProvider{name: "delete-prefixed-test"}
	clientName := SecretStoreKey("default", "store")
	compositeKey := CompositeClientKey(clientName, "kms.vpc.aliyuncs.com")
	if compositeKey != clientName+"#kms.vpc.aliyuncs.com" {
		t.Fatalf("CompositeClientKey shape changed: %q", compositeKey)
	}
	unrelated := []string{
		SecretStoreKey("default", "other-store"),
		CompositeClientKey(SecretStoreKey("default", "other-store"), "kms.vpc.aliyuncs.com"),
		ClusterStoreKey("store"),
	}

	p.Register(clientName, &stubENVSecretClient{name: clientName})
	p.Register(compositeKey, &stubENVSecretClient{name: compositeKey})
	for _, k := range unrelated {
		p.Register(k, &stubENVSecretClient{name: k})
	}

	p.DeletePrefixed(clientName)

	if _, err := p.GetClient(clientName); err == nil {
		t.Errorf("plain client %q must be removed by DeletePrefixed", clientName)
	}
	if _, err := p.GetClient(compositeKey); err == nil {
		t.Errorf("composite client %q (custom endpoint variant) must be removed by DeletePrefixed", compositeKey)
	}
	for _, k := range unrelated {
		if _, err := p.GetClient(k); err != nil {
			t.Errorf("unrelated client %q must be retained, got error %v", k, err)
		}
	}
}

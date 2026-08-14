package kms

// NOTE: this file mirrors oos/client_manager_test.go -- the KMS and OOS
// client managers share the same composite-key DeletePrefixed contract, so
// the two files keep the same test cases in sync. They intentionally do NOT
// share helpers because Go test helpers cannot cross package boundaries.

import (
	"context"
	"sync/atomic"
	"testing"

	kms "github.com/alibabacloud-go/kms-20160120/v3/client"

	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
)

// recordingStopper is a provider.Stopper that counts Stop invocations, used
// to prove DeletePrefixed stops the RAM refresh routine registered under
// every removed key (plain and composite).
type recordingStopper struct {
	stopCount int32
}

func (s *recordingStopper) Stop(ctx context.Context) {
	atomic.AddInt32(&s.stopCount, 1)
}

func (s *recordingStopper) stopped() int32 {
	return atomic.LoadInt32(&s.stopCount)
}

func registerTestClient(m *Manager, key string) {
	m.Register(key, &KMSClient{kmsClient: &kms.Client{}, clientName: key})
}

func ramManager(m *Manager) *backendp.Manager {
	return &backendp.Manager{RamLock: m.RamLock, RamProvider: m.RamProvider}
}

// TestDeletePrefixedRemovesPlainAndCompositeClients verifies that
// DeletePrefixed retires the plain clientName client together with every
// composite "clientName#endpoint" variant (cache entry AND RAM provider
// refresh routine), while leaving all other clients untouched.
func TestDeletePrefixedRemovesPlainAndCompositeClients(t *testing.T) {
	m := NewManager("cn-hangzhou")
	plain := "namespace/default/store"
	composites := []string{
		plain + "#kms.vpc.aliyuncs.com",
		plain + "#kms.other.aliyuncs.com",
	}
	unrelated := []string{
		"namespace/default/other-store",
		"namespace/default/other-store#ep",
		"cluster/store",
		"env.client",
	}

	registerTestClient(m, plain)
	for _, k := range composites {
		registerTestClient(m, k)
	}
	for _, k := range unrelated {
		registerTestClient(m, k)
	}

	// RAM provider registry uses aligned keys: plain for the generic client,
	// composite for every endpoint-specific client.
	stoppers := make(map[string]*recordingStopper)
	for _, k := range append([]string{plain}, composites...) {
		stopper := &recordingStopper{}
		stoppers[k] = stopper
		backendp.RegisterRamProvider(k, stopper, ramManager(m))
		// Clean up the registered RAM provider so the test leaves no live
		// refresh routine behind; StopProvider removes the registry entry.
		t.Cleanup(func() { backendp.StopProvider(k, ramManager(m)) })
	}

	m.DeletePrefixed(plain)

	for _, k := range append([]string{plain}, composites...) {
		if _, err := m.GetClient(k); err == nil {
			t.Errorf("client %q must be removed by DeletePrefixed", k)
		}
		if stoppers[k].stopped() != 1 {
			t.Errorf("ram provider %q must be stopped exactly once, got %d", k, stoppers[k].stopped())
		}
		m.RamLock.Lock()
		_, stillThere := m.RamProvider[k]
		m.RamLock.Unlock()
		if stillThere {
			t.Errorf("ram provider entry %q must be removed from the registry", k)
		}
	}
	for _, k := range unrelated {
		if _, err := m.GetClient(k); err != nil {
			t.Errorf("unrelated client %q must be retained, got error %v", k, err)
		}
	}
}

// TestDeletePrefixedRespectsKeyBoundary verifies that a store whose name
// merely shares a textual prefix ("store-x" vs "store") is NOT treated as a
// composite variant: only "clientName" itself and "clientName#..." qualify.
func TestDeletePrefixedRespectsKeyBoundary(t *testing.T) {
	m := NewManager("cn-hangzhou")
	sibling := "namespace/default/store-x"
	registerTestClient(m, sibling)
	registerTestClient(m, sibling+"#ep")

	m.DeletePrefixed("namespace/default/store")

	if _, err := m.GetClient(sibling); err != nil {
		t.Errorf("sibling client %q must be retained, got error %v", sibling, err)
	}
	if _, err := m.GetClient(sibling + "#ep"); err != nil {
		t.Errorf("sibling composite client must be retained, got error %v", err)
	}
}

// TestDeletePrefixedEmptyIsNoop verifies that deleting a prefix with no
// registered clients (and no RAM providers) is a safe no-op.
func TestDeletePrefixedEmptyIsNoop(t *testing.T) {
	m := NewManager("cn-hangzhou")
	m.DeletePrefixed("namespace/default/ghost")
	// Idempotent second call must not panic either.
	m.DeletePrefixed("namespace/default/ghost")
}

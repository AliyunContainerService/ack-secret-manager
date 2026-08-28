// Package providertest holds shared, importable contract helpers for the KMS
// and OOS secret providers. The two providers implement the same client-manager
// and composite-client-name contracts, so their tests would otherwise carry
// near-identical duplicated cases. These helpers live in a regular package
// (not a *_test.go file) because Go test helpers cannot cross package
// boundaries.
package providertest

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ramprovider "github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	backendin "github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
)

// RecordingStopper is a ramprovider.Stopper that counts Stop invocations, used
// to prove DeletePrefixed stops the RAM refresh routine registered under every
// removed key (plain and composite).
type RecordingStopper struct {
	stopCount int32
}

// Stop records a stop invocation.
func (s *RecordingStopper) Stop(ctx context.Context) {
	atomic.AddInt32(&s.stopCount, 1)
}

// Stopped returns how many times Stop was called.
func (s *RecordingStopper) Stopped() int32 {
	return atomic.LoadInt32(&s.stopCount)
}

// ClientManager is the subset of the KMS/OOS client managers exercised by the
// shared DeletePrefixed contract. Both *kms.Manager and *oos.Manager satisfy
// it.
type ClientManager interface {
	Register(clientName string, client backendin.SecretClient)
	GetClient(clientName string) (backendin.SecretClient, error)
	DeletePrefixed(clientName string)
}

// RunClientManagerContract exercises the shared DeletePrefixed contract of the
// KMS and OOS client managers. newManager returns a fresh manager together
// with the *backendp.Manager view (RamLock/RamProvider) backing it, so the
// contract can register and inspect RAM refresh routines under aligned keys.
// newClient builds a provider-specific SecretClient registered under a key.
func RunClientManagerContract(
	t *testing.T,
	newManager func() (ClientManager, *backendp.Manager),
	newClient func(key string) backendin.SecretClient,
) {
	// DeletePrefixed retires the plain clientName client together with every
	// composite "clientName#endpoint" variant (cache entry AND RAM provider
	// refresh routine), while leaving all other clients untouched.
	t.Run("removes plain and composite clients", func(t *testing.T) {
		m, ram := newManager()
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

		targets := append([]string{plain}, composites...)
		for _, k := range targets {
			m.Register(k, newClient(k))
		}
		for _, k := range unrelated {
			m.Register(k, newClient(k))
		}

		// RAM provider registry uses aligned keys: plain for the generic
		// client, composite for every endpoint-specific client.
		stoppers := make(map[string]*RecordingStopper)
		for _, k := range targets {
			stopper := &RecordingStopper{}
			stoppers[k] = stopper
			backendp.RegisterRamProvider(k, stopper, ram)
			// Clean up so the test leaves no live refresh routine behind.
			key := k
			t.Cleanup(func() { backendp.StopProvider(key, ram) })
		}

		m.DeletePrefixed(plain)

		for _, k := range targets {
			if _, err := m.GetClient(k); err == nil {
				t.Errorf("client %q must be removed by DeletePrefixed", k)
			}
			if stoppers[k].Stopped() != 1 {
				t.Errorf("ram provider %q must be stopped exactly once, got %d", k, stoppers[k].Stopped())
			}
			ram.RamLock.Lock()
			_, stillThere := ram.RamProvider[k]
			ram.RamLock.Unlock()
			if stillThere {
				t.Errorf("ram provider entry %q must be removed from the registry", k)
			}
		}
		for _, k := range unrelated {
			if _, err := m.GetClient(k); err != nil {
				t.Errorf("unrelated client %q must be retained, got error %v", k, err)
			}
		}
	})

	// A store whose name merely shares a textual prefix ("store-x" vs "store")
	// is NOT a composite variant: only "clientName" and "clientName#..." qualify.
	t.Run("respects key boundary", func(t *testing.T) {
		m, _ := newManager()
		sibling := "namespace/default/store-x"
		m.Register(sibling, newClient(sibling))
		m.Register(sibling+"#ep", newClient(sibling+"#ep"))

		m.DeletePrefixed("namespace/default/store")

		if _, err := m.GetClient(sibling); err != nil {
			t.Errorf("sibling client %q must be retained, got error %v", sibling, err)
		}
		if _, err := m.GetClient(sibling + "#ep"); err != nil {
			t.Errorf("sibling composite client must be retained, got error %v", err)
		}
	})

	// Deleting a prefix with no registered clients (and no RAM providers) is a
	// safe, idempotent no-op that leaves both registries empty.
	t.Run("empty prefix is a safe no-op", func(t *testing.T) {
		m, ram := newManager()
		m.DeletePrefixed("namespace/default/ghost")
		m.DeletePrefixed("namespace/default/ghost")

		if _, err := m.GetClient("namespace/default/ghost"); err == nil {
			t.Errorf("ghost client must not exist after no-op DeletePrefixed")
		}
		ram.RamLock.Lock()
		ramEntries := len(ram.RamProvider)
		ram.RamLock.Unlock()
		if ramEntries != 0 {
			t.Errorf("ram provider registry must stay empty, got %d entries", ramEntries)
		}
	})

	// keep ramprovider imported for the RecordingStopper contract even if the
	// linker inlines it away in some builds.
	var _ ramprovider.Stopper = (*RecordingStopper)(nil)
}

// EndpointClientFactory is the subset of a secret provider used by the shared
// composite client-name contract. Both *kms.Provider and *oos.Provider satisfy
// it.
type EndpointClientFactory interface {
	NewClientByENV(endpoint string) (backendin.SecretClient, error)
	NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client, endpoint string) (backendin.SecretClient, error)
}

// RunCompositeClientNameContract asserts the composite client-name key
// alignment shared by the KMS and OOS providers: a non-empty (even
// whitespace-padded) custom endpoint yields "<base>#<trimmed-endpoint>", while
// an empty or whitespace-only endpoint keeps the plain base name, on both the
// env-credential path and the SecretStore path. endpoint must be a value each
// provider accepts for offline construction.
func RunCompositeClientNameContract(t *testing.T, newProvider func() EndpointClientFactory, endpoint string) {
	envBase := backendin.EnvClient
	const storeName = "endpoint-store"
	storeBase := "namespace/default/" + storeName
	trimmed := strings.TrimSpace(endpoint)

	setENVCreds := func(t *testing.T) {
		t.Setenv("ACCESS_KEY_ID", "env-ak")
		t.Setenv("SECRET_ACCESS_KEY", "env-sk")
		t.Setenv("ALICLOUD_ROLE_ARN", "")
		t.Setenv("ALICLOUD_OIDC_PROVIDER_ARN", "")
	}

	nameFromENV := func(t *testing.T, ep string) string {
		cl, err := newProvider().NewClientByENV(ep)
		if err != nil {
			t.Fatalf("NewClientByENV(%q) returned error: %v", ep, err)
		}
		if cl == nil {
			t.Fatalf("NewClientByENV(%q) returned a nil client", ep)
		}
		return cl.GetName()
	}

	nameFromStore := func(t *testing.T, ep string) string {
		// WorkerRole gives the unconfigured store a lazy (no-network) auth tier
		// so construction stays offline.
		prev := backendin.EnableWorkerRole
		backendin.EnableWorkerRole = true
		defer func() { backendin.EnableWorkerRole = prev }()

		store := &v1alpha1.SecretStore{
			ObjectMeta: metav1.ObjectMeta{Name: storeName, Namespace: "default"},
		}
		cl, err := newProvider().NewClient(context.Background(), store, fake.NewClientBuilder().Build(), ep)
		if err != nil {
			t.Fatalf("NewClient(%q) returned error: %v", ep, err)
		}
		if cl == nil {
			t.Fatalf("NewClient(%q) returned a nil client", ep)
		}
		return cl.GetName()
	}

	t.Run("custom endpoint yields composite name (ENV)", func(t *testing.T) {
		setENVCreds(t)
		if got, want := nameFromENV(t, endpoint), envBase+"#"+trimmed; got != want {
			t.Errorf("client name = %q, want %q", got, want)
		}
	})
	t.Run("empty endpoint keeps plain name (ENV)", func(t *testing.T) {
		setENVCreds(t)
		if got := nameFromENV(t, ""); got != envBase {
			t.Errorf("client name = %q, want plain %q", got, envBase)
		}
	})
	t.Run("whitespace-padded endpoint matches trimmed (ENV)", func(t *testing.T) {
		setENVCreds(t)
		if got, want := nameFromENV(t, "  "+endpoint+"\t"), envBase+"#"+trimmed; got != want {
			t.Errorf("whitespace endpoint client name = %q, want %q (same as trimmed form)", got, want)
		}
	})
	t.Run("whitespace-only endpoint keeps plain name (ENV)", func(t *testing.T) {
		setENVCreds(t)
		if got := nameFromENV(t, "   "); got != envBase {
			t.Errorf("whitespace-only endpoint client name = %q, want plain %q", got, envBase)
		}
	})

	t.Run("custom endpoint yields composite name (store)", func(t *testing.T) {
		if got, want := nameFromStore(t, endpoint), storeBase+"#"+trimmed; got != want {
			t.Errorf("client name = %q, want %q", got, want)
		}
	})
	t.Run("empty endpoint keeps plain name (store)", func(t *testing.T) {
		if got := nameFromStore(t, ""); got != storeBase {
			t.Errorf("client name = %q, want plain %q", got, storeBase)
		}
	})
	t.Run("whitespace-padded endpoint matches trimmed (store)", func(t *testing.T) {
		if got, want := nameFromStore(t, "\n "+endpoint+" "), storeBase+"#"+trimmed; got != want {
			t.Errorf("whitespace endpoint client name = %q, want %q (same as trimmed form)", got, want)
		}
	})
}

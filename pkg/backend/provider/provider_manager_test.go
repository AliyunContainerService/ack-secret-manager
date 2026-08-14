package provider

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
)

// NOTE: tests in this file mutate the package-level stopTimeout global, so
// they rely on the package's default SERIAL test execution and must never
// call t.Parallel().

// fakeStopper is a controllable provider.Stopper for lifecycle tests.
// When blockCh is non-nil, Stop blocks until blockCh is closed and
// intentionally ignores ctx, simulating a provider whose Stop does not
// honor the timeout context.
type fakeStopper struct {
	name      string
	blockCh   chan struct{}
	stopCount int32
}

func (f *fakeStopper) Stop(ctx context.Context) {
	atomic.AddInt32(&f.stopCount, 1)
	if f.blockCh != nil {
		<-f.blockCh
	}
}

func (f *fakeStopper) stopped() int32 {
	return atomic.LoadInt32(&f.stopCount)
}

func newTestManager() *Manager {
	return &Manager{
		RamLock:     &sync.Mutex{},
		RamProvider: make(map[string]provider.Stopper),
	}
}

func getRamProvider(t *testing.T, m *Manager, clientName string) (provider.Stopper, bool) {
	t.Helper()
	m.RamLock.Lock()
	defer m.RamLock.Unlock()
	p, ok := m.RamProvider[clientName]
	return p, ok
}

// A re-registration must swap the map entry under the lock and stop the old
// instance, so that the old instance's Stop never touches the new one.
func TestRegisterRamProviderStopsOldInstance(t *testing.T) {
	m := newTestManager()
	old := &fakeStopper{name: "old"}
	fresh := &fakeStopper{name: "fresh"}

	RegisterRamProvider("client-a", old, m)
	RegisterRamProvider("client-a", fresh, m)

	if old.stopped() != 1 {
		t.Fatalf("old provider should be stopped exactly once, got %d", old.stopped())
	}
	if fresh.stopped() != 0 {
		t.Fatalf("new provider must not be stopped by re-registration, got %d", fresh.stopped())
	}
	got, ok := getRamProvider(t, m, "client-a")
	if !ok || got != fresh {
		t.Fatalf("ram provider map should hold the newly registered instance")
	}
}

// A slow/ignoring Stop of the old provider must not block concurrent
// registrations (the lock is released before Stop), and the bounded
// stopTimeout caps how long the replacing call itself waits.
func TestRegisterRamProviderNotBlockedBySlowStop(t *testing.T) {
	orig := stopTimeout
	stopTimeout = 100 * time.Millisecond
	defer func() { stopTimeout = orig }()

	m := newTestManager()
	block := make(chan struct{})
	defer close(block)

	old := &fakeStopper{name: "old", blockCh: block}
	fresh := &fakeStopper{name: "fresh"}
	RegisterRamProvider("client-a", old, m)

	// Concurrent registrations on a different name must not be blocked while
	// the old provider's Stop is in flight (lock already released).
	concurrent := &fakeStopper{name: "concurrent"}
	concurrentDone := make(chan struct{})
	go func() {
		RegisterRamProvider("client-b", concurrent, m)
		close(concurrentDone)
	}()
	select {
	case <-concurrentDone:
	case <-time.After(5 * time.Second):
		t.Fatalf("concurrent RegisterRamProvider blocked while an old Stop is in flight")
	}

	// The replacing call itself is bounded by stopTimeout.
	done := make(chan struct{})
	go func() {
		RegisterRamProvider("client-a", fresh, m)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("RegisterRamProvider did not give up after stopTimeout")
	}

	got, ok := getRamProvider(t, m, "client-a")
	if !ok || got != fresh {
		t.Fatalf("ram provider map should already hold the new instance")
	}
	if old.stopped() != 1 {
		t.Fatalf("old provider Stop should have been invoked once, got %d", old.stopped())
	}
}

func TestStopProviderRemovesEntryAndStops(t *testing.T) {
	m := newTestManager()
	p := &fakeStopper{name: "p"}
	RegisterRamProvider("client-a", p, m)

	StopProvider("client-a", m)

	if p.stopped() != 1 {
		t.Fatalf("provider should be stopped exactly once, got %d", p.stopped())
	}
	if _, ok := getRamProvider(t, m, "client-a"); ok {
		t.Fatalf("ram provider map entry should be deleted")
	}

	// Stopping an absent provider is a no-op.
	StopProvider("client-a", m)
	if p.stopped() != 1 {
		t.Fatalf("stop must be idempotent for absent entries, got %d", p.stopped())
	}
}

// A slow Stop must not hold up StopProvider beyond stopTimeout, and a
// provider registered after the deletion must stay unaffected.
func TestStopProviderNotBlockedBySlowStop(t *testing.T) {
	orig := stopTimeout
	stopTimeout = 100 * time.Millisecond
	defer func() { stopTimeout = orig }()

	m := newTestManager()
	block := make(chan struct{})
	defer close(block)
	old := &fakeStopper{name: "old", blockCh: block}
	RegisterRamProvider("client-a", old, m)

	done := make(chan struct{})
	go func() {
		StopProvider("client-a", m)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("StopProvider blocked on a slow Stop")
	}

	// The slot is already released: a new registration must win and must not
	// be stopped by the in-flight Stop of the old instance.
	fresh := &fakeStopper{name: "fresh"}
	RegisterRamProvider("client-a", fresh, m)
	got, ok := getRamProvider(t, m, "client-a")
	if !ok || got != fresh {
		t.Fatalf("newly registered provider should own the slot")
	}
	if fresh.stopped() != 0 {
		t.Fatalf("new provider must not be affected by the old instance's Stop, got %d", fresh.stopped())
	}
}

// The timeout guard must give up waiting when Stop ignores the context,
// instead of blocking for the full 30s production timeout.
func TestStopProviderTimeoutPath(t *testing.T) {
	orig := stopTimeout
	stopTimeout = 50 * time.Millisecond
	defer func() { stopTimeout = orig }()

	m := newTestManager()
	block := make(chan struct{})
	defer close(block)
	old := &fakeStopper{name: "old", blockCh: block}
	RegisterRamProvider("client-a", old, m)

	start := time.Now()
	StopProvider("client-a", m)
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Fatalf("StopProvider should give up after stopTimeout, took %v", elapsed)
	}
	if old.stopped() != 1 {
		t.Fatalf("Stop should have been attempted once, got %d", old.stopped())
	}
	if _, ok := getRamProvider(t, m, "client-a"); ok {
		t.Fatalf("ram provider map entry should be deleted even when Stop times out")
	}
}

func TestRegisterRamProviderTimeoutPath(t *testing.T) {
	orig := stopTimeout
	stopTimeout = 50 * time.Millisecond
	defer func() { stopTimeout = orig }()

	m := newTestManager()
	block := make(chan struct{})
	defer close(block)
	old := &fakeStopper{name: "old", blockCh: block}
	fresh := &fakeStopper{name: "fresh"}
	RegisterRamProvider("client-a", old, m)

	start := time.Now()
	RegisterRamProvider("client-a", fresh, m)
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Fatalf("RegisterRamProvider should give up after stopTimeout, took %v", elapsed)
	}
	got, ok := getRamProvider(t, m, "client-a")
	if !ok || got != fresh {
		t.Fatalf("ram provider map should hold the new instance after timed-out Stop")
	}
	if fresh.stopped() != 0 {
		t.Fatalf("new provider must not be stopped, got %d", fresh.stopped())
	}
}

// Concurrent registrations on the same clientName must not race and must
// leave a single winner in the map. Run with -race.
func TestConcurrentRegisterRamProvider(t *testing.T) {
	m := newTestManager()
	const n = 32
	stoppers := make([]*fakeStopper, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		stoppers[i] = &fakeStopper{name: "p"}
		wg.Add(1)
		go func(s *fakeStopper) {
			defer wg.Done()
			<-start
			RegisterRamProvider("client-a", s, m)
		}(stoppers[i])
	}
	close(start)
	wg.Wait()

	got, ok := getRamProvider(t, m, "client-a")
	if !ok || got == nil {
		t.Fatalf("ram provider map must hold exactly one registered instance")
	}
	winner := 0
	for i, s := range stoppers {
		if s == got {
			winner++
		}
		if s != got && s.stopped() != 1 {
			t.Fatalf("replaced provider %d should be stopped exactly once, got %d", i, s.stopped())
		}
	}
	if winner != 1 {
		t.Fatalf("expected exactly one winner in the map, got %d", winner)
	}
}

// Nil guards must not panic.
func TestNilManagerGuards(t *testing.T) {
	RegisterRamProvider("client-a", &fakeStopper{}, nil)
	StopProvider("client-a", nil)
	RegisterRamProvider("client-a", &fakeStopper{}, &Manager{})
	StopProvider("client-a", &Manager{})
}

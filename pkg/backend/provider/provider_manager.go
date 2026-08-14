package provider

import (
	"context"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
)

type Manager struct {
	Region string
	// kms multi-account client pool
	KmsClientMap sync.Map
	// oos multi-account client pool
	OosClientMap sync.Map
	// ram lock
	RamLock *sync.Mutex
	// RamProvider pool
	RamProvider map[string]provider.Stopper
}

// stopTimeout bounds the Stop call when replacing or removing a RAM provider.
// The map entry is always swapped/removed under RamLock before Stop runs, so
// a slow Stop can never block concurrent Register/Stop on the same clientName,
// and a newly registered instance is never affected by the old instance's Stop.
var stopTimeout = 30 * time.Second

func RegisterRamProvider(clientName string, stopper provider.Stopper, m *Manager) {
	if m == nil || m.RamLock == nil {
		klog.Errorf("Manager init error")
		return
	}
	// Swap the map entry under the lock, then stop the old provider outside
	// the lock so that a blocking Stop cannot stall concurrent registrations.
	m.RamLock.Lock()
	oldProvider, ok := m.RamProvider[clientName]
	m.RamProvider[clientName] = stopper
	m.RamLock.Unlock()
	klog.Infof("register provider %v success", clientName)
	if !ok || oldProvider == nil {
		return
	}
	stopProviderInstance(clientName, oldProvider)
}

func StopProvider(clientName string, m *Manager) {
	if m == nil || m.RamLock == nil {
		klog.Errorf("Manager init error")
		return
	}
	// Remove the map entry under the lock, then stop the provider outside
	// the lock; a later registration already owns the slot and stays unaffected.
	m.RamLock.Lock()
	providerIns, ok := m.RamProvider[clientName]
	if ok {
		delete(m.RamProvider, clientName)
	}
	m.RamLock.Unlock()
	if !ok || providerIns == nil {
		return
	}
	stopProviderInstance(clientName, providerIns)
	klog.Infof("stop provider %v success", clientName)
}

// stopProviderInstance stops a provider instance with a bounded timeout. It is
// always invoked outside of RamLock.
func stopProviderInstance(clientName string, providerIns provider.Stopper) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		providerIns.Stop(timeoutCtx)
		close(done)
	}()
	select {
	case <-done:
	case <-timeoutCtx.Done():
		klog.Warningf("stop provider %v timed out after %v, continuing without waiting", clientName, stopTimeout)
	}
}

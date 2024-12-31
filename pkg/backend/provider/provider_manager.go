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
	// dkms multi-instance client pool
	DedicateKmsClientMap sync.Map
	// oos multi-account client pool
	OosClientMap sync.Map
	// ram lock
	RamLock *sync.Mutex
	// RamProvider pool
	RamProvider map[string]provider.Stopper
}

func RegisterRamProvider(clientName string, stopper provider.Stopper, m *Manager) {
	if m == nil || m.RamLock == nil {
		klog.Errorf("Manager init error")
		return
	}
	m.RamLock.Lock()
	defer m.RamLock.Unlock()
	providerIns, ok := m.RamProvider[clientName]
	if ok {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		// cancel is earlier than m.RamLock.Unlock
		defer cancel()
		providerIns.Stop(timeoutCtx)
	}
	m.RamProvider[clientName] = stopper
	klog.Infof("register provider %v success", clientName)
}

func StopProvider(clientName string, m *Manager) {
	if m == nil || m.RamLock == nil {
		klog.Errorf("Manager init error")
		return
	}
	m.RamLock.Lock()
	defer m.RamLock.Unlock()
	providerIns, ok := m.RamProvider[clientName]
	if !ok || providerIns == nil {
		return
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	// cancel is earlier than m.RamLock.Unlock
	defer cancel()
	providerIns.Stop(timeoutCtx)
	delete(m.RamProvider, clientName)
	klog.Infof("stop provider %v success", clientName)
}

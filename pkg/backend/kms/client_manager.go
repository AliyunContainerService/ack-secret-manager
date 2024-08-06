package kms

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/klog"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	backendin "github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
)

type Manager struct {
	region string
	// kms multi-account client pool
	kmsClientMap sync.Map
	// dkms multi-instance client pool
	dedicateKmsClientMap sync.Map
	// ram lock
	ramLock sync.Mutex
	// RamProvider pool
	ramProvider map[string]provider.Stopper
}

func NewManager(region string) *Manager {
	return &Manager{
		region:      region,
		ramProvider: make(map[string]provider.Stopper),
	}
}

func (m *Manager) RegisterRamProvider(clientName string, stopper provider.Stopper) {
	m.ramLock.Lock()
	defer m.ramLock.Unlock()
	providerIns, ok := m.ramProvider[clientName]
	if ok {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		// cancel is earlier than m.ramLock.Unlock
		defer cancel()
		providerIns.Stop(timeoutCtx)
	}
	m.ramProvider[clientName] = stopper
	klog.Infof("register provider %v success", clientName)
}

func (m *Manager) StopProvider(clientName string) {
	m.ramLock.Lock()
	defer m.ramLock.Unlock()
	providerIns, ok := m.ramProvider[clientName]
	if !ok || providerIns == nil {
		return
	}
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	// cancel is earlier than m.ramLock.Unlock
	defer cancel()
	providerIns.Stop(timeoutCtx)
	delete(m.ramProvider, clientName)
	klog.Infof("stop provider %v success", clientName)
}

func (m *Manager) Register(clientName string, client backendin.SecretClient) {
	kmsClient, ok := client.(*KMSClient)
	if !ok {
		klog.Errorf("client type error")
		return
	}
	if kmsClient.kmsClient != nil {
		m.kmsClientMap.Store(clientName, client)
	}
	if kmsClient.dedicatedClient != nil {
		m.dedicateKmsClientMap.Store(clientName, client)
	}
	klog.Infof("register or update client, clientName %v", clientName)
}

func (m *Manager) Delete(clientName string) {
	// delete the client map, and stop the ram provider refresh go routine
	m.kmsClientMap.Delete(clientName)
	m.dedicateKmsClientMap.Delete(clientName)
	m.StopProvider(clientName)
	klog.Infof("delete client, clientName %v", clientName)
}

func (m *Manager) GetClient(clientName string) (backendin.SecretClient, error) {
	client, ok := m.kmsClientMap.Load(clientName)
	if ok {
		kmsClient, ok := client.(*KMSClient)
		if !ok {
			return nil, fmt.Errorf("client type error,clientName %v", clientName)
		}
		return &KMSClient{
			clientName: clientName,
			kmsClient:  kmsClient.kmsClient,
		}, nil
	}
	klog.Infof("client not register in kms client pool,clientName %v", clientName)
	client, ok = m.dedicateKmsClientMap.Load(clientName)
	if ok {
		dkmsClient, ok := client.(*KMSClient)
		if !ok {
			return nil, fmt.Errorf("client type error,clientName %v", clientName)
		}
		return &KMSClient{
			dedicatedClient: dkmsClient.dedicatedClient,
			clientName:      clientName,
		}, nil
	}
	return nil, fmt.Errorf("client not register,clientName %v", clientName)
}

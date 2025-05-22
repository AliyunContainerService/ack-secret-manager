package oos

import (
	"fmt"
	"sync"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	backendin "github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	"k8s.io/klog"
)

type Manager backendp.Manager

func NewManager(region string) *Manager {
	return &Manager{
		Region:      region,
		RamLock:     &sync.Mutex{},
		RamProvider: make(map[string]provider.Stopper),
	}
}

func (m *Manager) Register(clientName string, client backendin.SecretClient) {
	oosClient, ok := client.(*OOSClient)
	if oosClient == nil {
		klog.Errorf("client is nil")
		return
	}
	if !ok {
		klog.Errorf("client type error")
		return
	}
	if oosClient.oosClient != nil {
		m.OosClientMap.Store(clientName, client)
	}
	klog.Infof("register or update client, clientName %v", clientName)
}

func (m *Manager) Delete(clientName string) {
	// delete the client map, and stop the ram provider refresh go routine
	m.OosClientMap.Delete(clientName)
	backendp.StopProvider(clientName, &backendp.Manager{
		RamLock:     m.RamLock,
		RamProvider: m.RamProvider,
	})
	klog.Infof("delete client, clientName %v", clientName)
}

func (m *Manager) GetClient(clientName string) (backendin.SecretClient, error) {
	client, ok := m.OosClientMap.Load(clientName)
	if ok {
		oosClient, ok := client.(*OOSClient)
		if !ok {
			return nil, fmt.Errorf("client type error,clientName %v", clientName)
		}
		return &OOSClient{
			clientName: clientName,
			oosClient:  oosClient.oosClient,
		}, nil
	}

	klog.Infof("client not register in oos client pool,clientName %v", clientName)
	return nil, fmt.Errorf("client not register,clientName %v", clientName)
}

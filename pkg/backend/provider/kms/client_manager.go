package kms

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
	kmsClient, ok := client.(*KMSClient)
	if kmsClient == nil {
		klog.Errorf("client is nil")
		return
	}
	if !ok {
		klog.Errorf("client type error")
		return
	}
	if kmsClient.kmsClient != nil {
		m.KmsClientMap.Store(clientName, client)
	}
	if kmsClient.dedicatedClient != nil {
		m.DedicateKmsClientMap.Store(clientName, client)
	}
	klog.Infof("register or update client, clientName %v", clientName)
}

func (m *Manager) Delete(clientName string) {
	// delete the client map, and stop the ram provider refresh go routine
	m.KmsClientMap.Delete(clientName)
	m.DedicateKmsClientMap.Delete(clientName)
	backendp.StopProvider(clientName, &backendp.Manager{
		RamLock:     m.RamLock,
		RamProvider: m.RamProvider,
	})
	klog.Infof("delete client, clientName %v", clientName)
}

func (m *Manager) GetClient(clientName string) (backendin.SecretClient, error) {
	client, ok := m.KmsClientMap.Load(clientName)
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
	client, ok = m.DedicateKmsClientMap.Load(clientName)
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

package kms

import (
	"fmt"
	"strings"
	"sync"

	"k8s.io/klog"

	"github.com/AliyunContainerService/ack-ram-tool/pkg/credentials/provider"
	backendin "github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
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
	// Check the type assertion before the nil check: a non-*KMSClient value
	// must never be reported as "client is nil".
	kmsClient, ok := client.(*KMSClient)
	if !ok {
		klog.Errorf("client type error, client is not a *KMSClient, clientName %v", clientName)
		return
	}
	if kmsClient == nil {
		klog.Errorf("kms client is nil, clientName %v", clientName)
		return
	}
	if kmsClient.kmsClient != nil {
		m.KmsClientMap.Store(clientName, client)
		klog.Infof("register or update client, clientName %v", clientName)
	}
}

func (m *Manager) Delete(clientName string) {
	// delete the client map, and stop the ram provider refresh go routine
	m.KmsClientMap.Delete(clientName)
	backendp.StopProvider(clientName, &backendp.Manager{
		RamLock:     m.RamLock,
		RamProvider: m.RamProvider,
	})
	klog.Infof("delete client, clientName %v", clientName)
}

// DeletePrefixed implements the backend.ClientManager contract (non-atomic,
// see interface doc): removes the plain clientName client plus all composite
// "clientName#endpoint" variants, stopping each one's RAM refresh routine.
func (m *Manager) DeletePrefixed(clientName string) {
	compositePrefix := clientName + "#"
	var staleKeys []string
	m.KmsClientMap.Range(func(key, _ any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		if k == clientName || strings.HasPrefix(k, compositePrefix) {
			staleKeys = append(staleKeys, k)
		}
		return true
	})
	for _, k := range staleKeys {
		m.Delete(k)
	}
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
	return nil, fmt.Errorf("client not register,clientName %v", clientName)
}

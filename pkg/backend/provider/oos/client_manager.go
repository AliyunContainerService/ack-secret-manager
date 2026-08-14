package oos

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
	// Check the type assertion before the nil check: a non-*OOSClient value
	// must never be reported as "client is nil".
	oosClient, ok := client.(*OOSClient)
	if !ok {
		klog.Errorf("client type error, client is not a *OOSClient, clientName %v", clientName)
		return
	}
	if oosClient == nil {
		klog.Errorf("oos client is nil, clientName %v", clientName)
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

// DeletePrefixed implements the backend.ClientManager contract (see the
// interface doc for the non-atomicity caveat): removes the plain clientName
// client plus all composite "clientName#endpoint" variants. The OOS endpoint
// itself is ignored, but custom-endpoint clients are still registered under
// composite keys, so every variant must be retired here.
func (m *Manager) DeletePrefixed(clientName string) {
	compositePrefix := clientName + "#"
	var staleKeys []string
	m.OosClientMap.Range(func(key, _ any) bool {
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

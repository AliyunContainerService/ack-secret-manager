package kms

// client_manager_test.go delegates to the shared providertest contract: the
// KMS and OOS client managers implement the same composite-key DeletePrefixed
// behavior, so the assertions live once in
// pkg/backend/provider/providertest and both packages call them with their
// own client type. Only the KMS-specific manager/client construction lives
// here.

import (
	"testing"

	kms "github.com/alibabacloud-go/kms-20160120/v3/client"

	backendin "github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/providertest"
)

// TestClientManagerContract runs the shared DeletePrefixed contract against a
// KMS Manager, registering *KMSClient instances.
func TestClientManagerContract(t *testing.T) {
	providertest.RunClientManagerContract(t,
		func() (providertest.ClientManager, *backendp.Manager) {
			m := NewManager("cn-hangzhou")
			// The *backendp.Manager view shares the manager's RamLock and
			// RamProvider registry so the contract can register and inspect
			// RAM refresh routines under aligned keys.
			return m, &backendp.Manager{RamLock: m.RamLock, RamProvider: m.RamProvider}
		},
		func(key string) backendin.SecretClient {
			return &KMSClient{kmsClient: &kms.Client{}, clientName: key}
		},
	)
}

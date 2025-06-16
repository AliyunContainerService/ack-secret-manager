package backend

import (
	"context"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

const EnvClient = "env.client"

const (
	ProviderKMSName = "kms"
	ProviderOOSName = "oos"
)

var EnableWorkerRole bool

type CreateProvider func(opt *ProviderOptions)

type ProviderOptions struct {
	Region           string
	KmsEndpoint      string
	KmsMaxConcurrent int
	OosMaxConcurrent int
}

var (
	SupportProvider map[string]CreateProvider
	providerMap     sync.Map
	initOnce        sync.Once
)

func init() {
	initOnce.Do(func() {
		SupportProvider = make(map[string]CreateProvider)
	})
}

func RegisterProvider(providerName string, provider Provider) {
	providerMap.Store(providerName, provider)
}

func GetProviderByName(providerName string) Provider {
	ins, ok := providerMap.Load(providerName)
	if !ok {
		return nil
	}
	provider, ok := ins.(Provider)
	if !ok {
		return nil
	}
	return provider
}

type Provider interface {
	ClientManager
	// NewClient constructs secrets client by secret store
	NewClient(ctx context.Context, endpoint, name string, store *v1alpha1.SecretStore, kube client.Client) (SecretClient, error)
	GetName() string
	GetRegion() string
}

type SecretClient interface {
	GetName() string
	// GetSecret gets secret via externalSecret
	GetExternalSecret(ctx context.Context, data *v1alpha1.DataSource, kube client.Client) (map[string][]byte, error)
	GetExternalSecretWithExtract(ctx context.Context, data *v1alpha1.DataProcess, kube client.Client) (map[string][]byte, error)
}

type ClientManager interface {
	Register(clientKey string, secretClient SecretClient)
	GetClient(clientKey string) (SecretClient, error)
	Delete(clientKey string)
}

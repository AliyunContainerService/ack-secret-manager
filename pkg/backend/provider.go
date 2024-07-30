package backend

import (
	"context"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

const EnvClient = "env.client"

type CreateProvider func(opt *ProviderOptions)

type ProviderOptions struct {
	Region        string
	MaxConcurrent int
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

func NewProviderClientByENV(ctx context.Context, region string) error {
	errs := make([]error, 0)
	providerMap.Range(func(k, v any) bool {
		provider, ok := v.(Provider)
		if !ok {
			err := fmt.Errorf("provider type error,provider name %v", k)
			errs = append(errs, err)
			return true
		}
		secretClient, err := provider.NewClientByENV(ctx, region)
		if err != nil {
			errs = append(errs, fmt.Errorf("%v new client by env error %v", k, err))
			return true
		}
		provider.Register(EnvClient, secretClient)
		return true
	})
	if len(errs) != 0 {
		return fmt.Errorf("new provider client by env error %v", errs)
	}
	return nil
}

type Provider interface {
	ClientManager
	// NewClient constructs secrets client by secret store
	NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client) (SecretClient, error)
	// NewClientByENV constructs secrets client by environment variable
	NewClientByENV(ctx context.Context, region string) (SecretClient, error)
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

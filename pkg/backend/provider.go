package backend

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	ClusterId        string
	Uid              string
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

// DeleteProvider removes a registered provider (inverse of RegisterProvider).
// Test-only; never call from production code.
func DeleteProvider(providerName string) {
	providerMap.Delete(providerName)
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

// envInitFailureTTL bounds how long a fully-failed ENV client init result is cached before retrying.
const envInitFailureTTL = 60 * time.Second

var (
	// envClientInitMu serializes EnsureENVClient callers and guards the
	// failure negative-cache state below.
	envClientInitMu   sync.Mutex
	envInitFailedAt   time.Time
	envInitFailureErr error
	// envNow is an indirection over time.Now so TTL tests can inject a fake clock.
	envNow = time.Now
	// envInitFailureTTLOverride lets tests shorten the TTL; 0 means use envInitFailureTTL.
	envInitFailureTTLOverride time.Duration
)

// EnsureENVClient lazily registers the ENV client for every provider on
// first use. Thread-safe and retryable: only providers still missing a
// client are retried. Attempts making no progress cache the error for
// envInitFailureTTL; partial success never caches. Reconcile-path only,
// so standby replicas start no credential refresh.
func EnsureENVClient() error {
	envClientInitMu.Lock()
	defer envClientInitMu.Unlock()

	ttl := envInitFailureTTL
	if envInitFailureTTLOverride > 0 {
		ttl = envInitFailureTTLOverride
	}
	now := envNow()
	// Within the TTL of a fully-failed attempt, return the cached error.
	if envInitFailureErr != nil && now.Sub(envInitFailedAt) < ttl {
		return envInitFailureErr
	}
	envInitFailureErr = nil
	envInitFailedAt = time.Time{}

	errs := make([]error, 0)
	registered := 0
	providerMap.Range(func(k, v any) bool {
		provider, ok := v.(Provider)
		if !ok {
			errs = append(errs, fmt.Errorf("provider type error,provider name %v", k))
			return true
		}
		// Skip providers whose ENV client is already registered
		if _, err := provider.GetClient(EnvClient); err == nil {
			return true
		}
		secretClient, err := provider.NewClientByENV("")
		if err != nil {
			errs = append(errs, fmt.Errorf("%v new client by env error %v", k, err))
			return true
		}
		provider.Register(EnvClient, secretClient)
		registered++
		return true
	})
	if len(errs) != 0 {
		err := fmt.Errorf("new provider client by env error %v", errs)
		// Negative cache only on zero progress; partial progress keeps
		// immediate retry semantics.
		if registered == 0 {
			envInitFailedAt = now
			envInitFailureErr = err
		}
		return err
	}
	return nil
}

type Provider interface {
	ClientManager
	// NewClient constructs a secrets client by secret store; endpoint is a
	// custom service endpoint (empty string = provider default).
	NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client, endpoint string) (SecretClient, error)
	// NewClientByENV constructs a secrets client from environment variables
	// (empty endpoint = default).
	NewClientByENV(endpoint string) (SecretClient, error)
	GetName() string
	GetRegion() string
	GetEndpoint() string
	GetClusterId() string
	GetUid() string
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
	// DeletePrefixed removes the plain clientKey client together with every
	// composite "clientKey#endpoint" variant, so store-level lifecycle events
	// retire all variants of the store's clientName at once.
	// NOT atomic: sync.Map.Range's weakly consistent snapshot may miss a
	// concurrently registered composite client; it self-heals on the next
	// store lifecycle event.
	DeletePrefixed(clientKey string)
}

package oos

import (
	"context"
	"fmt"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	oos "github.com/alibabacloud-go/oos-20190601/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/auth"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	commonp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
)

const (
	defaultOosDomain = "oos-vpc.%s.aliyuncs.com"
)

func init() {
	backend.SupportProvider[backend.ProviderOOSName] = NewProvider
}

// Provider provides the ability to generate oos clients and manage oos clients
type Provider struct {
	*Manager
	region             string
	cluster            string
	uid                string
	name               string
	maxConcurrentCount int
}

func NewProvider(opts *backend.ProviderOptions) {
	provider := &Provider{
		Manager:            NewManager(opts.Region),
		region:             opts.Region,
		cluster:            opts.ClusterId,
		uid:                opts.Uid,
		name:               backend.ProviderOOSName,
		maxConcurrentCount: opts.OosMaxConcurrent,
	}
	backend.RegisterProvider(backend.ProviderOOSName, provider)
}

func (p *Provider) GetName() string {
	return p.name
}

func (p *Provider) GetRegion() string {
	return p.region
}

func (p *Provider) GetEndpoint() string {
	return ""
}

func (p *Provider) GetClusterId() string {
	return p.cluster
}

func (p *Provider) GetUid() string {
	return p.uid
}

// NewClient creates a new OOS client.
// NOTE: endpoint satisfies the Provider interface only; OOS always uses the
// default VPC endpoint and ignores custom endpoints.
func (p *Provider) NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client, endpoint string) (backend.SecretClient, error) {
	// Trim to align with the controller-side normalizeEndpoint contract: the
	// composite key "clientName#endpoint" must match on both sides.
	endpoint = strings.TrimSpace(endpoint)

	var authProvider commonp.AuthConfigProvider
	if store.Spec.OOS != nil && store.Spec.OOS.OOS != nil {
		authProvider = &commonp.OOSAuthAdapter{OOSAuth: store.Spec.OOS.OOS}
	}

	authConfig, err := commonp.BuildAuthConfig(ctx, store, kube, authProvider, p.cluster, p.uid)
	if err != nil {
		return nil, err
	}

	warnIfEndpointIgnored(endpoint)

	// Key alignment: the controller caches custom-endpoint clients under
	// composite keys even for OOS, so the RAM registry must use the same key
	// to keep Delete/Stop symmetric. The endpoint itself is still ignored.
	authConfig.ClientName = backend.CompositeClientKey(authConfig.ClientName, endpoint)

	return p.newClientWithAuth(authConfig.ClientName, authConfig)
}

// NewClientByENV creates a new OOS client from environment credentials; the
// endpoint is accepted but ignored (see NewClient).
func (p *Provider) NewClientByENV(endpoint string) (backend.SecretClient, error) {
	endpoint = strings.TrimSpace(endpoint)

	authEnvs := commonp.BuildAuthConfigFromEnv()

	warnIfEndpointIgnored(endpoint)

	// Key alignment with the composite cache key; see NewClient.
	authEnvs.ClientName = backend.CompositeClientKey(authEnvs.ClientName, endpoint)

	return p.newClientWithAuth(authEnvs.ClientName, authEnvs)
}

// warnIfEndpointIgnored warns when a custom endpoint is requested; OOS
// always falls back to the default domain and ignores it.
func warnIfEndpointIgnored(endpoint string) {
	if endpoint != "" {
		klog.Warningf("custom endpoint is not supported for OOS and will be ignored (configured endpoint %q)", endpoint)
	}
}

func (p *Provider) newClientWithAuth(clientName string, authConfig auth.AuthConfig) (*OOSClient, error) {
	region := p.GetRegion()

	cred, err := authConfig.GetAuthCred(region, p.maxConcurrentCount, &backendp.Manager{
		RamLock:     p.RamLock,
		RamProvider: p.RamProvider,
	})
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("cred is empty")
	}

	endpoint := fmt.Sprintf(defaultOosDomain, region)

	client, err := oos.NewClient(&openapi.Config{
		Endpoint:   tea.String(endpoint),
		RegionId:   tea.String(region),
		Credential: cred,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create OOS client: %v", err)
	}

	cl := &OOSClient{
		oosClient:  client,
		clientName: clientName,
	}

	return cl, nil
}

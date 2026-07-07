package oos

import (
	"context"
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	oos "github.com/alibabacloud-go/oos-20190601/v3/client"
	"github.com/alibabacloud-go/tea/tea"
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
// NOTE: The endpoint parameter is accepted to satisfy the Provider interface,
// but OOS currently does NOT support custom endpoints. The default OOS VPC endpoint
// (oos-vpc.<region>.aliyuncs.com) is always used. Custom endpoint support may be added in the future.
func (p *Provider) NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client, endpoint string) (backend.SecretClient, error) {
	var authProvider commonp.AuthConfigProvider
	if store.Spec.OOS != nil && store.Spec.OOS.OOS != nil {
		authProvider = &commonp.OOSAuthAdapter{OOSAuth: store.Spec.OOS.OOS}
	}

	authConfig, err := commonp.BuildAuthConfig(ctx, store, kube, authProvider, p.cluster, p.uid)
	if err != nil {
		return nil, err
	}

	return p.newClientWithAuth(authConfig.ClientName, authConfig)
}

// NewClientByENV creates a new OOS client using environment variable credentials.
// NOTE: The endpoint parameter is accepted but not used — see NewClient for details.
func (p *Provider) NewClientByENV(endpoint string) (backend.SecretClient, error) {
	authEnvs := commonp.BuildAuthConfigFromEnv()

	return p.newClientWithAuth(backend.EnvClient, authEnvs)
}

func (p *Provider) newClientWithAuth(clientName string, authConfig auth.AuthConfig) (*OOSClient, error) {
	region := p.GetRegion()

	//get ram auth credential
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

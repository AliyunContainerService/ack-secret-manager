package kms

import (
	"context"
	"fmt"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	kms "github.com/alibabacloud-go/kms-20160120/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/auth"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	commonp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
)

const (
	defaultKmsDomain = "kms-vpc.%s.aliyuncs.com"
	suffix           = "cryptoservice.kms.aliyuncs.com"
)

func init() {
	backend.SupportProvider[backend.ProviderKMSName] = NewProvider
}

// Provider provides the ability to generate kms clients and manage kms clients
type Provider struct {
	*Manager
	region             string
	endpoint           string
	name               string
	clusterId          string
	uid                string
	maxConcurrentCount int
}

func NewProvider(opts *backend.ProviderOptions) {
	provider := &Provider{
		Manager:            NewManager(opts.Region),
		region:             opts.Region,
		clusterId:          opts.ClusterId,
		uid:                opts.Uid,
		endpoint:           opts.KmsEndpoint,
		name:               backend.ProviderKMSName,
		maxConcurrentCount: opts.KmsMaxConcurrent,
	}
	backend.RegisterProvider(backend.ProviderKMSName, provider)
}

func (p *Provider) GetName() string {
	return p.name
}

func (p *Provider) GetRegion() string {
	return p.region
}

func (p *Provider) GetEndpoint() string {
	if p.endpoint == "" {
		return fmt.Sprintf(defaultKmsDomain, p.region)
	}

	return p.endpoint
}

func (p *Provider) GetClusterId() string {
	return p.clusterId
}

func (p *Provider) GetUid() string {
	return p.uid
}

func (p *Provider) NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client) (backend.SecretClient, error) {
	var authProvider commonp.AuthConfigProvider
	if store.Spec.KMS != nil && store.Spec.KMS.KMS != nil {
		authProvider = &commonp.KMSAuthAdapter{KMSAuth: store.Spec.KMS.KMS, StoreName: store.Name}
	}

	authConfig, err := commonp.BuildAuthConfig(ctx, store, kube, authProvider, p.clusterId, p.uid)
	if err != nil {
		return nil, err
	}

	return p.newClientWithAuth(authConfig.ClientName, authConfig)
}

func (p *Provider) NewClientByENV() (backend.SecretClient, error) {
	authEnvs := commonp.BuildAuthConfigFromEnv()

	return p.newClientWithAuth(backend.EnvClient, authEnvs)
}

func (p *Provider) newClientWithAuth(clientName string, auth auth.AuthConfig) (*KMSClient, error) {
	region := p.GetRegion()

	cred, err := auth.GetAuthCred(region, p.maxConcurrentCount, &backendp.Manager{
		RamLock:     p.Manager.RamLock,
		RamProvider: p.Manager.RamProvider,
	})
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("cred is empty")
	}

	endpoint := p.GetEndpoint()
	config := &openapi.Config{
		RegionId:   tea.String(p.Region),
		Endpoint:   tea.String(endpoint),
		Credential: cred,
	}
	if strings.Contains(endpoint, suffix) {
		config.Ca = tea.String(RegionIdAndCaMap[region])
	}

	client, err := kms.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create KMS client: %v", err)
	}

	cl := &KMSClient{
		kmsClient:  client,
		clientName: clientName,
	}

	return cl, nil
}

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

func (p *Provider) NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client, endpoint string) (backend.SecretClient, error) {
	// Trim to align with the controller-side normalizeEndpoint contract:
	// the composite key "clientName#endpoint" must match on both sides.
	endpoint = strings.TrimSpace(endpoint)

	var authProvider commonp.AuthConfigProvider
	if store.Spec.KMS != nil && store.Spec.KMS.KMS != nil {
		authProvider = &commonp.KMSAuthAdapter{KMSAuth: store.Spec.KMS.KMS, StoreName: store.Name}
	}

	authConfig, err := commonp.BuildAuthConfig(ctx, store, kube, authProvider, p.clusterId, p.uid)
	if err != nil {
		return nil, err
	}

	// Key alignment: a custom endpoint yields a composite cache key
	// ("clientName#endpoint") in the ExternalSecret controller, so the RAM
	// provider registry must use the same composite key. Otherwise the
	// composite client's RegisterRamProvider would replace (and stop) the
	// plain client's refresh provider under the bare clientName, and
	// Delete(compositeKey) would never stop the composite refresh routine.
	if endpoint != "" {
		authConfig.ClientName = fmt.Sprintf("%s#%s", authConfig.ClientName, endpoint)
	}

	return p.newClientWithAuth(authConfig.ClientName, authConfig, endpoint)
}

func (p *Provider) NewClientByENV(endpoint string) (backend.SecretClient, error) {
	// Normalize first; see NewClient for the key-alignment rationale.
	endpoint = strings.TrimSpace(endpoint)

	authEnvs := commonp.BuildAuthConfigFromEnv()

	// Key alignment with the composite cache key; see NewClient.
	if endpoint != "" {
		authEnvs.ClientName = fmt.Sprintf("%s#%s", authEnvs.ClientName, endpoint)
	}

	return p.newClientWithAuth(authEnvs.ClientName, authEnvs, endpoint)
}

func (p *Provider) newClientWithAuth(clientName string, auth auth.AuthConfig, customEndpoint string) (*KMSClient, error) {
	region := p.GetRegion()

	cred, err := auth.GetAuthCred(region, p.maxConcurrentCount, &backendp.Manager{
		RamLock:     p.RamLock,
		RamProvider: p.RamProvider,
	})
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("cred is empty")
	}

	// Use custom endpoint if provided, otherwise use provider's default.
	// All endpoints are validated to prevent SSRF attacks (CWE-918).
	// Custom endpoints come from user-controlled ExternalSecret CR fields;
	// the default endpoint comes from the --kms-endpoint CLI flag which
	// could be misconfigured by a cluster admin.
	endpoint := customEndpoint
	if endpoint == "" {
		endpoint = p.GetEndpoint()
	}
	if err := validateKmsEndpoint(endpoint); err != nil {
		return nil, err
	}

	config := &openapi.Config{
		RegionId:   tea.String(p.Region),
		Endpoint:   tea.String(endpoint),
		Credential: cred,
	}
	// Set CA certificate based on endpoint type using anchored suffix match
	// instead of unanchored strings.Contains to prevent false positives
	if isCryptoserviceEndpoint(endpoint) {
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

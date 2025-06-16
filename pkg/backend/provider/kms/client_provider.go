package kms

import (
	"context"
	"fmt"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	kms "github.com/alibabacloud-go/kms-20160120/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/auth"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
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
	maxConcurrentCount int
}

func NewProvider(opts *backend.ProviderOptions) {
	provider := &Provider{
		Manager:            NewManager(opts.Region),
		region:             opts.Region,
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

func (p *Provider) NewClient(ctx context.Context, endpoint, name string, store *v1alpha1.SecretStore, kube client.Client) (backend.SecretClient, error) {
	region := p.GetRegion()

	authConfig := auth.GetCredentialParameterFromEnv()
	authConfig.ClientName = name
	authConfig.RefreshPeriod = time.Minute * 10

	if store != nil && store.Spec.KMS != nil && store.Spec.KMS.KMS != nil {
		kmsConfig := store.Spec.KMS.KMS
		if kmsConfig.AccessKey != nil {
			accessKey, err := utils.GetConfigFromSecret(ctx, kube, kmsConfig.AccessKey)
			if err != nil {
				klog.Errorf("get ak config from secret error %v", err)
			} else if len(accessKey) > 0 {
				authConfig.AccessKey = string(accessKey)
			}
		}
		if kmsConfig.AccessKeySecret != nil {
			accessKeySecret, err := utils.GetConfigFromSecret(ctx, kube, kmsConfig.AccessKeySecret)
			if err != nil {
				klog.Errorf("get sk config from secret error %v", err)
			} else if len(accessKeySecret) > 0 {
				authConfig.AccessSecretKey = string(accessKeySecret)
			}
		}
		if kmsConfig.RAMRoleARN != "" {
			authConfig.RoleArn = kmsConfig.RAMRoleARN
		}
		if kmsConfig.OIDCProviderARN != "" {
			authConfig.OidcArn = kmsConfig.OIDCProviderARN
		}
		if kmsConfig.RAMRoleSessionName != "" {
			authConfig.RoleSessionName = kmsConfig.RAMRoleSessionName
		}
		if kmsConfig.RoleSessionExpiration != "" {
			authConfig.RoleSessionExpiration = kmsConfig.RoleSessionExpiration
		}
		if kmsConfig.RemoteRAMRoleSessionName != "" {
			authConfig.RemoteRoleSessionName = kmsConfig.RemoteRAMRoleSessionName
		}
		if kmsConfig.RemoteRAMRoleARN != "" {
			authConfig.RemoteRoleArn = kmsConfig.RemoteRAMRoleARN
		}
	}

	cred, err := authConfig.GetAuthCred(region, p.maxConcurrentCount, &backendp.Manager{
		RamLock:     p.Manager.RamLock,
		RamProvider: p.Manager.RamProvider,
	})
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("cred is empty")
	}

	if endpoint == "" {
		endpoint = p.endpoint
	}
	if endpoint == "" {
		endpoint = fmt.Sprintf(defaultKmsDomain, region)
	}

	config := &openapi.Config{
		Endpoint:   tea.String(endpoint),
		RegionId:   tea.String(region),
		Credential: cred,
	}
	if strings.Contains(endpoint, suffix) {
		config.Ca = tea.String(RegionIdAndCaMap[region])
	}

	client, err := kms.NewClient(config)
	if err != nil {
		return nil, err
	}

	return &KMSClient{
		kmsClient:  client,
		clientName: name,
	}, nil
}

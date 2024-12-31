package oos

import (
	"context"
	"fmt"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	oos "github.com/alibabacloud-go/oos-20190601/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/auth"
	backendp "github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
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
	name               string
	maxConcurrentCount int
}

func NewProvider(opts *backend.ProviderOptions) {
	provider := &Provider{
		Manager:            NewManager(opts.Region),
		region:             opts.Region,
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

func (p *Provider) NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client) (backend.SecretClient, error) {
	clientName := fmt.Sprintf("%s/%s", store.Namespace, store.Name)
	region := p.GetRegion()
	shareClient, err := NewOOSClient(ctx, store, kube, region, p)
	if err != nil {
		klog.Errorf("new share oos client error %v", err)
		return nil, err
	}
	cl := &OOSClient{
		oosClient:  shareClient,
		clientName: clientName,
	}
	return cl, nil
}

func NewOOSClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client, region string, p *Provider) (*oos.Client, error) {
	if store.Spec.OOS == nil {
		return nil, fmt.Errorf("oos config is empty")
	}

	var ak, sk string
	oosConfig := store.Spec.OOS.OOS
	auth := auth.AuthConfig{
		ClientName:    fmt.Sprintf("%s/%s", store.Namespace, store.Name),
		RefreshPeriod: time.Minute * 10,
	}

	if oosConfig != nil {
		if oosConfig.AccessKey != nil {
			accessKey, err := utils.GetConfigFromSecret(ctx, kube, oosConfig.AccessKey)
			if err != nil {
				klog.Errorf("get ak config from secret error %v", err)
				ak = ""
			} else {
				ak = string(accessKey)
			}
			auth.AccessKey = ak
		}
		if oosConfig.AccessKeySecret != nil {
			accessKeySecret, err := utils.GetConfigFromSecret(ctx, kube, oosConfig.AccessKeySecret)
			if err != nil {
				klog.Errorf("get sk config from secret error %v", err)
				sk = ""
			} else {
				sk = string(accessKeySecret)
			}
			auth.AccessSecretKey = sk
		}
		auth.RoleArn = oosConfig.RAMRoleARN
		auth.OidcArn = oosConfig.OIDCProviderARN
		auth.RoleSessionName = oosConfig.RAMRoleSessionName
		auth.RoleSessionExpiration = oosConfig.RoleSessionExpiration
		auth.RemoteRoleSessionName = oosConfig.RemoteRAMRoleSessionName
		auth.RemoteRoleArn = oosConfig.RemoteRAMRoleARN
	}

	//get ram auth credential
	cred, err := auth.GetAuthCred(region, p.maxConcurrentCount, &backendp.Manager{
		RamLock: p.Manager.RamLock,
		RamProvider: p.Manager.RamProvider,
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
		return nil, err
	}
	return client, nil
}

func (p *Provider) NewClientByENV(ctx context.Context, region string) (backend.SecretClient, error) {
	authEnvs := auth.GetCredentialParameterFromEnv()
	//get ram auth credential
	cred, err := authEnvs.GetAuthCred(region, p.maxConcurrentCount, &backendp.Manager{
		RamLock: p.Manager.RamLock,
		RamProvider: p.Manager.RamProvider,
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
		return nil, err
	}
	cl := &OOSClient{
		oosClient:  client,
		clientName: backend.EnvClient,
	}
	return cl, nil
}

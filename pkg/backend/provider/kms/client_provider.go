package kms

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	kms "github.com/alibabacloud-go/kms-20160120/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	dkmsopenapi "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi"
	dkms "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/sdk"
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
	HTTPS            = "https"
	noCA             = "noca"
	hasCA            = "hasca"
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

func (p *Provider) GetEndpoint() string {
	return p.endpoint
}

func (p *Provider) NewClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client) (backend.SecretClient, error) {
	if store.Spec.KMS == nil {
		return nil, fmt.Errorf("kms config is empty")
	}
	clientName := fmt.Sprintf("%s/%s", store.Namespace, store.Name)
	if store.Spec.KMS.DedicatedKMS != nil {
		dkmsClient, err := NewDedicateKMSClient(ctx, store, kube)
		if err != nil {
			klog.Errorf("new dkms client error %v", err)
			return nil, err
		}
		cl := &KMSClient{
			dedicatedClient: dkmsClient,
			clientName:      clientName,
		}
		return cl, nil
	}
	shareClient, err := NewShareKMSClient(ctx, store, kube, p)
	if err != nil {
		klog.Errorf("new share kms client error %v", err)
		return nil, err
	}
	cl := &KMSClient{
		kmsClient:  shareClient,
		clientName: clientName,
	}
	return cl, nil
}

func NewDedicateKMSClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client) (*dkms.Client, error) {
	dkmsConfig := store.Spec.KMS.DedicatedKMS
	var clientKey, password string
	clientKeyBytes, err := utils.GetConfigFromSecret(ctx, kube, dkmsConfig.ClientKeyContent)
	if err != nil {
		klog.Errorf("get client key from secret error %v", err)
		return nil, err
	}
	clientKey = string(clientKeyBytes)
	passwordBytes, err := utils.GetConfigFromSecret(ctx, kube, dkmsConfig.Password)
	if err != nil {
		klog.Errorf("get password from secret error %v", err)
		return nil, err
	}
	password = string(passwordBytes)
	openAPIcfg := &dkmsopenapi.Config{
		Protocol:         tea.String(HTTPS),
		ClientKeyContent: tea.String(clientKey),
		Password:         tea.String(password),
		Endpoint:         tea.String(fmt.Sprintf("%s.%s", dkmsConfig.Endpoint, suffix)),
	}
	if dkmsConfig.CA != "" {
		ca, err := base64.StdEncoding.DecodeString(dkmsConfig.CA)
		if err != nil {
			klog.Errorf("get ca error %v", err)
			return nil, err
		}
		openAPIcfg.Ca = tea.String(string(ca))
		openAPIcfg.NoProxy = tea.String(hasCA)
	} else {
		openAPIcfg.NoProxy = tea.String(noCA)
	}
	openAPIcfg.IgnoreSSL = tea.Bool(dkmsConfig.IgnoreSSL)
	dClient, err := dkms.NewClient(openAPIcfg)
	if err != nil {
		klog.Errorf("new dkms client error %v", err)
		return nil, err
	}
	return dClient, nil
}

func NewShareKMSClient(ctx context.Context, store *v1alpha1.SecretStore, kube client.Client, p *Provider) (*kms.Client, error) {
	var ak, sk string
	kmsConfig := store.Spec.KMS.KMS
	auth := auth.AuthConfig{
		ClientName:    fmt.Sprintf("%s/%s", store.Namespace, store.Name),
		RefreshPeriod: time.Minute * 10,
	}
	region := p.GetRegion()
	endpoint := p.GetEndpoint()

	if kmsConfig != nil {
		if kmsConfig.AccessKey != nil {
			accessKey, err := utils.GetConfigFromSecret(ctx, kube, kmsConfig.AccessKey)
			if err != nil {
				klog.Errorf("get ak config from secret error %v", err)
				ak = ""
			} else {
				ak = string(accessKey)
			}
			auth.AccessKey = ak
		}
		if kmsConfig.AccessKeySecret != nil {
			accessKeySecret, err := utils.GetConfigFromSecret(ctx, kube, kmsConfig.AccessKeySecret)
			if err != nil {
				klog.Errorf("get sk config from secret error %v", err)
				sk = ""
			} else {
				sk = string(accessKeySecret)
			}
			auth.AccessSecretKey = sk
		}
		auth.RoleArn = kmsConfig.RAMRoleARN
		auth.OidcArn = kmsConfig.OIDCProviderARN
		auth.RoleSessionName = kmsConfig.RAMRoleSessionName
		auth.RoleSessionExpiration = kmsConfig.RoleSessionExpiration
		auth.RemoteRoleSessionName = kmsConfig.RemoteRAMRoleSessionName
		auth.RemoteRoleArn = kmsConfig.RemoteRAMRoleARN
	}
	//get ram auth credential
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

	if endpoint == "" {
		endpoint = fmt.Sprintf(defaultKmsDomain, region)
	}

	config := &openapi.Config{
		Endpoint:   tea.String(endpoint),
		Credential: cred,
	}
	if strings.Contains(endpoint, suffix) {
		config.Ca = tea.String(RegionIdAndCaMap[region])
	}
	client, err := kms.NewClient(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (p *Provider) NewClientByENV() (backend.SecretClient, error) {
	authEnvs := auth.GetCredentialParameterFromEnv()
	region := p.GetRegion()
	endpoint := p.GetEndpoint()
	//get ram auth credential
	cred, err := authEnvs.GetAuthCred(region, p.maxConcurrentCount, &backendp.Manager{
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
		endpoint = fmt.Sprintf(defaultKmsDomain, region)
	}

	config := &openapi.Config{
		Endpoint:   tea.String(endpoint),
		Credential: cred,
	}
	if strings.Contains(endpoint, suffix) {
		config.Ca = tea.String(RegionIdAndCaMap[region])
	}
	client, err := kms.NewClient(config)
	if err != nil {
		return nil, err
	}
	cl := &KMSClient{
		kmsClient:  client,
		clientName: backend.EnvClient,
	}
	return cl, nil
}

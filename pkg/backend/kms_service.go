package backend

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	aliCloudAuth "github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials/providers"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/kms"
	"github.com/go-logr/logr"
	"github.com/golang/glog"
	"os"
	"reflect"
	"sync"
	"time"
)

const (
	// https protocol.
	Https           = "https"
	AccessKeyId     = "ACCESS_KEY_ID"
	AccessKeySecret = "ACCESS_KEY_SECRET"
)

type client struct {
	kmsClient           *kms.Client
	provider            providers.Provider
	region              string
	lastCreds           aliCloudAuth.Credential
	credLock            *sync.RWMutex //share the latest credentials across goroutines.
	tokenRotationPeriod time.Duration
	logger              logr.Logger
}

func newKMSClient(log logr.Logger, cfg Config) *client {
	region := cfg.Region
	//init client
	client := client{
		tokenRotationPeriod: cfg.TokenRotationPeriod,
		logger:              log,
		region:              region,
		credLock:            new(sync.RWMutex),
	}
	return &client
}

func setConfig(c *client) error {
	if c.region == "" {
		return nil
	}
	credConfig := &providers.Configuration{}
	credConfig.AccessKeyID = os.Getenv(AccessKeyId)
	credConfig.AccessKeySecret = os.Getenv(AccessKeySecret)

	credentialChain := []providers.Provider{
		providers.NewConfigurationCredentialProvider(credConfig),
		providers.NewEnvCredentialProvider(),
		providers.NewInstanceMetadataProvider(),
	}
	credProvider := providers.NewChainProvider(credentialChain)
	//Do an initial credential fetch because we want to err right away if we can't even get a first set.
	lastCreds, err := credProvider.Retrieve()
	if err != nil {
		return err
	}
	c.logger.Info("get last credential", "lastCreds", lastCreds)


	clientConfig := sdk.NewConfig()
	clientConfig.Scheme = "https"
	kclient, err := kms.NewClientWithOptions(c.region, clientConfig, lastCreds)
	if err != nil {
		return fmt.Errorf("failed to init kms client, err: %v", err)
	}

	c.kmsClient = kclient
	c.provider = credProvider
	c.lastCreds = lastCreds
	return nil
}

//refresh the client credential if ak not set
func (c *client) pullForCreds(ctx context.Context) {
	go func(ctx context.Context) {
		ticker := time.NewTicker(c.tokenRotationPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				glog.Warningf("stopping the pulling channel")
				return
			case <-ticker.C:
				if err := c.checkCredentials(c.provider); err != nil {
					glog.Warningf("unable to retrieve current credentials, error: %v", err)
				}
			}
		}
	}(ctx)
}

func (c *client) checkCredentials(credProvider providers.Provider) error {
	c.logger.Info("checking for new credentials")
	currentCreds, err := credProvider.Retrieve()
	if err != nil {
		return err
	}
	// need DeepEqual for refresh lastCreds
	if reflect.DeepEqual(currentCreds, c.lastCreds) {
		return nil
	}
	c.logger.Info("credentials rotate")
	c.lastCreds = currentCreds

	clientConfig := sdk.NewConfig()
	clientConfig.Scheme = "https"
	kclient, err := kms.NewClientWithOptions(c.region, clientConfig, currentCreds)
	if err != nil {
		return fmt.Errorf("failed to init kms client, err: %v", err)
	}
	c.credLock.Lock()
	defer c.credLock.Unlock()
	c.kmsClient = kclient
	return nil
}

func (c *client) GetSecret(key string, queryCondition *SecretQueryCondition) (string, error) {
	data := ""
	if key == "" {
		return data, utils.EmptySecretKeyError{ErrType: utils.EmptySecretKeyErrorType}
	}
	request := kms.CreateGetSecretValueRequest()
	request.Scheme = Https
	request.SecretName = key
	if queryCondition.VersionId != "" {
		request.VersionId = queryCondition.VersionId
	}
	if queryCondition.VersionStage != "" {
		request.VersionStage = queryCondition.VersionStage
	}
	c.credLock.RLock()
	defer c.credLock.RUnlock()

	response, err := c.kmsClient.GetSecretValue(request)
	if err != nil {
		c.logger.Error(err, "failed to get secret value from kms", "key", key)
		return data, err
	}
	if response.SecretDataType == utils.BinaryType {
		c.logger.Error(err, "not support binary type yet", "key", key)
		return data, utils.BackendSecretTypeNotSupportError{ErrType: utils.EmptySecretKeyErrorType, Key: key}
	}
	c.logger.Info("got secret data from kms service", "key", key, "response", response)
	data = response.SecretData
	return data, nil
}

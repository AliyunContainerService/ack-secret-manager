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
	Https = "https"

	// max retry number to wait CSR response come back to parse root cert from it.
	maxRetryNum = 5

	// initial retry wait time duration when waiting root cert is available.
	retryWaitDuration = 800 * time.Millisecond
)

type client struct {
	kmsClient           *kms.Client
	provider            providers.Provider
	region              string
	lastCreds           aliCloudAuth.Credential
	credLock            sync.Mutex //share the latest credentials across goroutines.
	secretID            string
	tokenRotationPeriod time.Duration
	renewTTLIncrement   int
	logger              logr.Logger
}

func newKMSClient(l logr.Logger, cfg Config) (*client, error) {

	credConfig := &providers.Configuration{}
	credConfig.AccessKeyID = os.Getenv("ACCESS_KEY_ID")
	credConfig.AccessKeySecret = os.Getenv("ACCESS_KEY_SECRET")

	credentialChain := []providers.Provider{
		providers.NewConfigurationCredentialProvider(credConfig),
		providers.NewEnvCredentialProvider(),
		providers.NewInstanceMetadataProvider(),
	}
	credProvider := providers.NewChainProvider(credentialChain)

	// Do an initial population of the creds because we want to err right away if we can't
	// even get a first set.
	lastCreds, err := credProvider.Retrieve()
	if err != nil {
		return nil, err
	}
	clientConfig := sdk.NewConfig()
	clientConfig.Scheme = "https"
	region := cfg.Region
	kclient, err := kms.NewClientWithOptions(region, clientConfig, lastCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to init kms client, err: %v", err)
	}

	client := client{
		kmsClient:           kclient,
		tokenRotationPeriod: cfg.TokenRotationPeriod,
	}

	return &client, err
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
	c.credLock.Lock()
	defer c.credLock.Unlock()

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
	c.kmsClient = kclient
	return nil
}

func (c *client) GetSecret(key string, queryCondition *SecretQueryCondition) (string, error) {
	c.credLock.Lock()
	defer c.credLock.Unlock()

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

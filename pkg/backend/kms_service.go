/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package backend

import (
	"context"
	"fmt"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk"
	aliCloudAuth "github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth"
	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/auth/credentials/providers"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/alibaba-cloud-sdk-go/services/kms"
	"github.com/go-logr/logr"
	"os"
	"reflect"
	"sync"
	"time"
)

const (
	// https protocol.
	Https                         = "https"
	HangZhou                      = "cn-hangzhou"
	AccessKeyId                   = "ACCESS_KEY_ID"
	AccessKeySecret               = "ACCESS_KEY_SECRET"
	REJECTED_THROTTLING           = "Rejected.Throttling"
	SERVICE_UNAVAILABLE_TEMPORARY = "ServiceUnavailableTemporary"
	INTERNAL_FAILURE              = "InternalFailure"
	MAX_RETRY_TIMES               = 5
)

var (
	BACKOFF_DEFAULT_RETRY_INTERVAL = time.Second
	BACKOFF_DEFAULT_CAPACITY       = time.Duration(10) * time.Second
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
	instanceRegion, err := utils.GetRegion()
	if err != nil {
		log.Error(err, "failed to get region from meta server")
	}
	//replace default region with real value
	if region == "" && instanceRegion != region {
		log.Info("refine the default region", "region", instanceRegion)
		region = instanceRegion
	}
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
	}
	//there is no ecs instance role for ask cluster worker, only support pass-in ak or ram role
	withInstanceRole, err := utils.CheckInstanceRam()
	if err != nil {
		c.logger.Info("skip add instance role provider into credential chain for ask cluster", "err", err)
	}
	if withInstanceRole {
		credentialChain = append(credentialChain, providers.NewInstanceMetadataProvider())
	}
	credProvider := providers.NewChainProvider(credentialChain)
	//Do an initial credential fetch because we want to err right away if we can't even get a first set.
	lastCreds, err := credProvider.Retrieve()
	if err != nil {
		return err
	}

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
				c.logger.Info("stopping the pulling channel")
				return
			case <-ticker.C:
				if err := c.checkCredentials(c.provider); err != nil {
					c.logger.Error(err, "unable to retrieve current credentials")
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
	for retryTimes := 1; retryTimes < MAX_RETRY_TIMES; retryTimes++ {
		if err != nil {
			if !judgeNeedRetry(err) {
				c.logger.Error(err, "failed to get secret value from kms", "key", key)
				return data, err
			} else {
				time.Sleep(getWaitTimeExponential(retryTimes))
				response, err = c.kmsClient.GetSecretValue(request)
				if err != nil && retryTimes == MAX_RETRY_TIMES-1 {
					c.logger.Error(err, "failed to get secret value from kms", "key", key)
					return data, err
				}
			}
		}
		break
	}

	if response.SecretDataType == utils.BinaryType {
		c.logger.Error(err, "not support binary type yet", "key", key)
		return data, utils.BackendSecretTypeNotSupportError{ErrType: utils.EmptySecretKeyErrorType, Key: key}
	}
	c.logger.Info("got secret data from kms service", "key", key)
	data = response.SecretData
	return data, nil
}

func judgeNeedRetry(err error) bool {
	respErr, is := err.(*sdkErr.ClientError)
	if is && (respErr.ErrorCode() == REJECTED_THROTTLING || respErr.ErrorCode() == SERVICE_UNAVAILABLE_TEMPORARY || respErr.ErrorCode() == INTERNAL_FAILURE) {
		return true
	}
	return false
}

func getWaitTimeExponential(retryTimes int) time.Duration {
	sleepInterval := time.Duration(math.Pow(2, float64(retryTimes))) * BACKOFF_DEFAULT_RETRY_INTERVAL
	if sleepInterval >= BACKOFF_DEFAULT_CAPACITY {
		return BACKOFF_DEFAULT_CAPACITY
	} else {
		return sleepInterval
	}
}

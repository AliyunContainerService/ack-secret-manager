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
	"fmt"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
	openapi "github.com/alibabacloud-go/darabonba-openapi/client"
	kms "github.com/alibabacloud-go/kms-20160120/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	"github.com/aliyun/credentials-go/credentials"
	"github.com/go-logr/logr"
	"math"
	"os"
	"strconv"
	"time"
)

const (
	REJECTED_THROTTLING           = "Rejected.Throttling"
	SERVICE_UNAVAILABLE_TEMPORARY = "ServiceUnavailableTemporary"
	INTERNAL_FAILURE              = "InternalFailure"
	MAX_RETRY_TIMES               = 5
	RamRoleARNAuthType            = "ram_role_arn"
	AKAuthType                    = "access_key"
	EcsRamRoleAuthType            = "ecs_ram_role"
	OidcAuthType                  = "oidc_role_arn"
	oidcRoleSessionName           = "ack-secret-manager"
	oidcTokenFilePath             = "/var/run/secrets/tokens/ack-secret-manager"
	defaultKmsDomain              = "kms-vpc.%s.aliyuncs.com"
)

var (
	BACKOFF_DEFAULT_RETRY_INTERVAL = time.Second
	BACKOFF_DEFAULT_CAPACITY       = time.Duration(10) * time.Second
)

type client struct {
	kmsClient *kms.Client
	region    string
	logger    logr.Logger
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
		logger: log,
		region: region,
	}
	return &client
}

func (c *client) setKMSClient() error {
	roleArn := os.Getenv("ALICLOUD_ROLE_ARN")
	oidcArn := os.Getenv("ALICLOUD_OIDC_PROVIDER_ARN")
	oidcTokenFile := os.Getenv("ALICLOUD_OIDC_TOKEN_FILE")
	accessKey := os.Getenv("ACCESS_KEY_ID")
	accessSecretKey := os.Getenv("SECRET_ACCESS_KEY")
	roleSessionName := os.Getenv("ALICLOUD_ROLE_SESSION_NAME")
	roleSessionExpiration := os.Getenv("ALICLOUD_ROLE_SESSION_EXPIRATION")

	var cred credentials.Credential
	var err error
	if roleArn != "" {
		//prefer to use rrsa oidc auth type
		if oidcArn != "" && oidcTokenFile != "" {
			config := new(credentials.Config).
				SetType(OidcAuthType).
				SetOIDCProviderArn(oidcArn).
				SetOIDCTokenFilePath(oidcTokenFilePath).
				SetRoleArn(roleArn).
				SetRoleSessionName(oidcRoleSessionName)
			cred, err = credentials.NewCredential(config)
			if err != nil {
				return err
			}
			c.logger.Info("Using oidc rrsa auth..", "roleArn", roleArn, "oidcArn", oidcArn, "oidcTokenFile", oidcTokenFile)
		}
		//check if ram_role_arn auth type
		if accessKey != "" && accessSecretKey != "" {
			config := new(credentials.Config).
				SetType(RamRoleARNAuthType).
				SetAccessKeyId(accessKey).
				SetAccessKeySecret(accessSecretKey).
				SetRoleArn(roleArn).
				SetRoleSessionName(roleSessionName)
			if roleSessionExpiration != "" {
				rseInt, err := strconv.Atoi(roleSessionExpiration)
				if err != nil {
					c.logger.Error(err, "failed to parse given roleSessionExpiration", "value", roleSessionExpiration)
				} else {
					config.SetRoleSessionExpiration(rseInt)
				}
			}
			cred, err = credentials.NewCredential(config)
			if err != nil {
				return err
			}
			c.logger.Info("Using ram role arn auth..", "roleArn", roleArn, "roleSessionName", roleSessionName)
		}
	}
	//check to use access_key auth mode
	if accessKey != "" && accessSecretKey != "" {
		config := new(credentials.Config).
			SetType(AKAuthType).
			SetAccessKeyId(accessKey).
			SetAccessKeySecret(accessSecretKey)
		cred, err = credentials.NewCredential(config)
		if err != nil {
			return err
		}
		c.logger.Info("Using ak/sk auth..")
	}
	//choose ecs ram role auth mode at last
	if cred == nil {
		config := new(credentials.Config).
			SetType(EcsRamRoleAuthType)
		cred, err = credentials.NewCredential(config)
		if err != nil {
			return err
		}
		c.logger.Info("Using ecs ram role auth..")
	}
	if cred != nil {
		endpoint := fmt.Sprintf(defaultKmsDomain, c.region)
		client, err := kms.NewClient(&openapi.Config{
			Endpoint:   tea.String(endpoint),
			RegionId:   tea.String(c.region),
			Credential: cred,
		})
		if err != nil {
			return err
		}
		c.kmsClient = client
	}
	return nil
}

func (c *client) GetSecret(key string, queryCondition *SecretQueryCondition) (string, error) {
	data := ""
	if key == "" {
		return data, utils.EmptySecretKeyError{ErrType: utils.EmptySecretKeyErrorType}
	}
	request := &kms.GetSecretValueRequest{
		SecretName: tea.String(key),
	}
	if queryCondition.VersionId != "" {
		request.VersionId = tea.String(queryCondition.VersionId)
	}
	if queryCondition.VersionStage != "" {
		request.VersionStage = tea.String(queryCondition.VersionStage)
	}
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

	if *response.Body.SecretDataType == utils.BinaryType {
		c.logger.Error(err, "not support binary type yet", "key", key)
		return data, utils.BackendSecretTypeNotSupportError{ErrType: utils.EmptySecretKeyErrorType, Key: key}
	}
	c.logger.Info("got secret data from kms service", "key", key)
	data = *response.Body.SecretData
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

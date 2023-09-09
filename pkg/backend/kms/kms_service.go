package kms

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"time"

	kms "github.com/alibabacloud-go/kms-20160120/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	sdkErr "github.com/aliyun/alibaba-cloud-sdk-go/sdk/errors"
	dkmsopenapiutil "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi-util"
	dkms "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/sdk"
	"github.com/jmespath/go-jmespath"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	REJECTED_THROTTLING           = "Rejected.Throttling"
	SERVICE_UNAVAILABLE_TEMPORARY = "ServiceUnavailableTemporary"
	INTERNAL_FAILURE              = "InternalFailure"
	MAX_RETRY_TIMES               = 5
	KMSVPCDomain                  = "%s.cryptoservice.kms.aliyuncs.com"
)

var (
	BACKOFF_DEFAULT_RETRY_INTERVAL = time.Second
	BACKOFF_DEFAULT_CAPACITY       = time.Duration(10) * time.Second
)

// Client interface represent a backend client interface that should be implemented
type KMSClient struct {
	dedicatedClient *dkms.Client
	kmsClient       *kms.Client
	clientName      string
}

func (c *KMSClient) GetName() string {
	return c.clientName
}

func (c *KMSClient) getExternalData(ctx context.Context, data v1alpha1.DataSource) ([]byte, error) {
	// dkms
	if c.dedicatedClient != nil {
		dkmsData, err := c.getExternalDataFromDKMS(data)
		if err != nil {
			klog.Errorf("get external data from dkms error %v,key %v", err, data.Key)
			return nil, err
		}
		return dkmsData, nil
	}

	// kms
	kmsData, err := c.getExternalDataFromKMS(data)
	if err != nil {
		klog.Errorf("get external data from kms error %v,key %v", err, data.Key)
		return nil, err
	}
	return kmsData, nil

}
func (c *KMSClient) GetExternalSecret(ctx context.Context, data *v1alpha1.DataSource, kube client.Client) (map[string][]byte, error) {
	secretDatas := make(map[string][]byte)
	//getExternalData
	externalData, err := c.getExternalData(ctx, *data)
	if err != nil {
		klog.Errorf("get external data error %v,key %v", err, data.Key)
		return nil, err
	}
	// jmes
	if len(data.JMESPath) > 0 {
		klog.Infof("parse jmes format, key %v", data.Key)
		jsonDataMap, err := getJsonSecrets(data.JMESPath, string(externalData), data.Key)
		if err != nil {
			klog.Errorf("parse jmes format error %v, key %v, jmes %v, data.JMESPath", err, data.Key, data.JMESPath)
		} else if len(jsonDataMap) > 0 {
			//use parsed k-value in target secret
			for k, v := range jsonDataMap {
				secretDatas[k] = []byte(v)
			}
			return secretDatas, nil
		}
	}
	secretDatas[data.Name] = externalData
	return secretDatas, nil
}

func (c *KMSClient) GetExternalSecretWithExtract(ctx context.Context, data *v1alpha1.DataProcess, kube client.Client) (map[string][]byte, error) {
	secretDatas := make(map[string][]byte)
	if data.Extract == nil {
		return nil, fmt.Errorf("extract data is empty")
	}
	externalData, err := c.getExternalData(ctx, *data.Extract)
	if err != nil {
		return nil, err
	}
	tempKV := make(map[string]interface{})
	err = json.Unmarshal(externalData, &tempKV)
	if err != nil {
		klog.Errorf("extract secret error %v key %v", err, data.Extract.Key)
		return nil, err
	}
	kv := make(map[string]string)
	for k, v := range tempKV {
		kv[k] = utils.JsonStr(v)
	}
	if data.ReplaceKey != nil && len(data.ReplaceKey) != 0 {
		for _, rule := range data.ReplaceKey {
			kv, err = RewriteRegexp(rule, kv)
			if err != nil {
				klog.Errorf("replace data key failed, error %v", err)
				continue
			}
		}
	}
	for k, v := range kv {
		secretDatas[k] = []byte(v)
	}
	return secretDatas, nil
}

// RewriteRegexp rewrites a single Regexp Rewrite Operation.
func RewriteRegexp(operation v1alpha1.ReplaceRule, in map[string]string) (map[string]string, error) {
	out := make(map[string]string)
	re, err := regexp.Compile(operation.Source)
	if err != nil {
		return nil, err
	}
	for key, value := range in {
		newKey := re.ReplaceAllString(key, operation.Target)
		out[newKey] = value
	}
	return out, nil
}

func getJsonSecrets(jmesObj []v1alpha1.JMESPathObject, secretValue, key string) (jsonMap map[string]string, err error) {
	jsonMap = make(map[string]string, 0)
	var data interface{}
	err = json.Unmarshal([]byte(secretValue), &data)
	if err != nil {
		return nil, fmt.Errorf("invalid JSON used with jmesPath in secret key: %s", key)
	}
	//fetch all specified key value pairs`
	for _, jmesPathEntry := range jmesObj {
		jsonSecret, err := jmespath.Search(jmesPathEntry.Path, data)
		if err != nil {
			return nil, fmt.Errorf("Invalid JMES Path: %s.", jmesPathEntry.Path)
		}

		if jsonSecret == nil {
			return nil, fmt.Errorf("JMES Path - %s for object alias - %s does not point to a valid object.",
				jmesPathEntry.Path, jmesPathEntry.ObjectAlias)
		}

		jsonSecretAsString, isString := jsonSecret.(string)
		if !isString {
			return nil, fmt.Errorf("Invalid JMES search result type for path:%s. Only string is allowed.", jmesPathEntry.Path)
		}
		jsonMap[jmesPathEntry.ObjectAlias] = jsonSecretAsString
	}
	return jsonMap, nil
}

func (c *KMSClient) getExternalDataFromKMS(data v1alpha1.DataSource) ([]byte, error) {
	if c.kmsClient == nil {
		return nil, fmt.Errorf("kms client is nil,kms key %v", data.Key)
	}
	req := &kms.GetSecretValueRequest{
		SecretName: tea.String(data.Key),
	}
	if data.VersionStage != "" {
		req.VersionStage = tea.String(data.VersionStage)
	}
	if data.VersionId != "" {
		req.VersionId = tea.String(data.VersionId)
	}
	resp, err := c.kmsClient.GetSecretValue(req)
	for retryTimes := 1; retryTimes < MAX_RETRY_TIMES; retryTimes++ {
		if err != nil {
			if !judgeNeedRetry(err) {
				klog.Errorf("failed to get secret value from kms,key %v,error %v", data.Key, err)
				return nil, err
			} else {
				time.Sleep(getWaitTimeExponential(retryTimes))
				resp, err = c.kmsClient.GetSecretValue(req)
				if err != nil && retryTimes == MAX_RETRY_TIMES-1 {
					klog.Errorf("failed to get secret value from kms,key %v,error %v", data.Key, err)
					return nil, err
				}
			}
		}
		break
	}
	if *resp.Body.SecretDataType == utils.BinaryType {
		klog.Errorf("not support binary type yet,key %v", data.Key)
		return nil, utils.BackendSecretTypeNotSupportError{ErrType: utils.EmptySecretKeyErrorType, Key: data.Key}
	}
	klog.Infof("got secret data from kms service,key %v", data.Key)
	return []byte(*resp.Body.SecretData), nil
}

func (c *KMSClient) getExternalDataFromDKMS(data v1alpha1.DataSource) ([]byte, error) {
	if c.dedicatedClient == nil {
		return nil, fmt.Errorf("dkms client is nil,kms key %v", data.Key)
	}
	req := &dkms.GetSecretValueRequest{
		SecretName: tea.String(data.Key),
	}
	if data.VersionStage != "" {
		req.VersionStage = tea.String(data.VersionStage)
	}
	if data.VersionId != "" {
		req.VersionId = tea.String(data.VersionId)
	}

	runtimeOptions := &dkmsopenapiutil.RuntimeOptions{}
	resp, err := c.dedicatedClient.GetSecretValueWithOptions(req, runtimeOptions)
	for retryTimes := 1; retryTimes < MAX_RETRY_TIMES; retryTimes++ {
		if err != nil {
			if !judgeNeedRetry(err) {
				klog.Errorf("failed to get secret value from kms,key %v,error %v", data.Key, err)
				return nil, err
			} else {
				time.Sleep(getWaitTimeExponential(retryTimes))
				resp, err = c.dedicatedClient.GetSecretValueWithOptions(req, runtimeOptions)
				if err != nil && retryTimes == MAX_RETRY_TIMES-1 {
					klog.Errorf("failed to get secret value from kms,key %v,error %v", data.Key, err)
					return nil, err
				}
			}
		}
		break
	}
	if *resp.SecretDataType == utils.BinaryType {
		klog.Errorf("not support binary type yet,key %v", data.Key)
		return nil, utils.BackendSecretTypeNotSupportError{ErrType: utils.EmptySecretKeyErrorType, Key: data.Key}
	}
	klog.Infof("got secret data from kms service,key %v", data.Key)
	return []byte(*resp.SecretData), nil
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

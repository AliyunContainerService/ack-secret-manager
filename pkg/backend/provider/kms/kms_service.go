package kms

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	kms "github.com/alibabacloud-go/kms-20160120/v2/client"
	"github.com/alibabacloud-go/tea/tea"
	dkmsopenapiutil "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/openapi-util"
	dkms "github.com/aliyun/alibabacloud-dkms-gcs-go-sdk/sdk"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
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
		jsonDataMap, err := utils.GetJsonSecrets(data.JMESPath, string(externalData), data.Key)
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
	if len(data.ReplaceKey) != 0 {
		for _, rule := range data.ReplaceKey {
			kv, err = utils.RewriteRegexp(rule, kv)
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
	if err != nil {
		if !utils.JudgeNeedRetry(err) {
			klog.Errorf("failed to get secret value from kms,key %v,error %v", data.Key, err)
			return nil, err
		} else {
			time.Sleep(utils.GetWaitTimeExponential(1))
			resp, err = c.kmsClient.GetSecretValue(req)
			if err != nil {
				klog.Errorf("retry to get secret value from kms failed,key %v,error %v", data.Key, err)
				return nil, err
			}
		}
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("get secret value from kms failed because response is empty, key %v", data.Key)
	}
	if resp.Body.SecretDataType != nil && *resp.Body.SecretDataType == utils.BinaryType {
		klog.Infof("got binary secret data from kms service,key %v", data.Key)
		originData, err := base64.StdEncoding.DecodeString(*resp.Body.SecretData)
		if err != nil {
			return nil, fmt.Errorf("decode binary data error %v,key %v", err, data.Key)
		}
		return originData, nil
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
	if err != nil {
		if !utils.JudgeNeedRetry(err) {
			klog.Errorf("failed to get secret value from kms,key %v,error %v", data.Key, err)
			return nil, err
		} else {
			time.Sleep(utils.GetWaitTimeExponential(1))
			resp, err = c.dedicatedClient.GetSecretValueWithOptions(req, runtimeOptions)
			if err != nil {
				klog.Errorf("retry to get secret value from kms failed,key %v,error %v", data.Key, err)
				return nil, err
			}
		}
	}
	if *resp.SecretDataType == utils.BinaryType {
		klog.Errorf("not support binary type yet,key %v", data.Key)
		return nil, utils.BackendSecretTypeNotSupportError{ErrType: utils.EmptySecretKeyErrorType, Key: data.Key}
	}
	klog.Infof("got secret data from kms service,key %v", data.Key)
	return []byte(*resp.SecretData), nil
}

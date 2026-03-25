package kms

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	kms "github.com/alibabacloud-go/kms-20160120/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
	util "github.com/alibabacloud-go/tea-utils/v2/service"
)

// Client interface represent a backend client interface that should be implemented
type KMSClient struct {
	kmsClient  *kms.Client
	clientName string
}

func (c *KMSClient) GetName() string {
	return c.clientName
}

func (c *KMSClient) getExternalData(data v1alpha1.DataSource) ([]byte, error) {
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
	runTimeOption := &util.RuntimeOptions{}
	if strings.Contains(data.KmsEndpoint, suffix) {
		runTimeOption.SetCa(RegionIdAndCaMap[tea.StringValue(c.kmsClient.RegionId)])
	}
	resp, err := c.kmsClient.GetSecretValueWithOptions(req, runTimeOption)
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

func (c *KMSClient) GetExternalSecret(data *v1alpha1.DataSource, kube client.Client) (map[string][]byte, error) {
	// getExternalData
	externalData, err := c.getExternalData(*data)
	if err != nil {
		klog.Errorf("get external data error %v,key %v", err, data.Key)
		return nil, err
	}

	// Process data with common function
	return common.ProcessExternalSecretData(data, externalData)
}

func (c *KMSClient) GetExternalSecretWithExtract(data *v1alpha1.DataProcess, kube client.Client) (map[string][]byte, error) {
	if data.Extract == nil {
		return nil, fmt.Errorf("extract data is empty")
	}

	// getExternalData
	externalData, err := c.getExternalData(*data.Extract)
	if err != nil {
		return nil, err
	}

	// Process extracted data with common function
	return common.ProcessExtractedExternalSecretData(data, externalData)
}

package oos

import (
	"fmt"
	"time"

	oos "github.com/alibabacloud-go/oos-20190601/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// Client interface represent a backend client interface that should be implemented
type OOSClient struct {
	oosClient  *oos.Client
	clientName string
}

func (c *OOSClient) GetName() string {
	return c.clientName
}

func (c *OOSClient) getExternalData(data v1alpha1.DataSource) ([]byte, error) {
	if c.oosClient == nil {
		return nil, fmt.Errorf("oos client is nil,oos key %v", data.Key)
	}
	req := &oos.GetSecretParameterRequest{
		Name:           tea.String(data.Key),
		WithDecryption: tea.Bool(true),
	}
	resp, err := c.oosClient.GetSecretParameter(req)
	if err != nil {
		if !utils.JudgeNeedRetry(err) {
			klog.Errorf("failed to get secret value from oos,key %v,error %v", data.Key, err)
			return nil, err
		} else {
			time.Sleep(utils.GetWaitTimeExponential(1))
			resp, err = c.oosClient.GetSecretParameter(req)
			if err != nil {
				klog.Errorf("retry to get secret value from oos failed,key %v,error %v", data.Key, err)
				return nil, err
			}
		}
	}

	klog.Infof("got secret data from oos service,key %v", data.Key)
	return []byte(*resp.Body.Parameter.Value), nil
}

func (c *OOSClient) GetExternalSecret(data *v1alpha1.DataSource, kube client.Client) (map[string][]byte, error) {
	// getExternalData
	externalData, err := c.getExternalData(*data)
	if err != nil {
		klog.Errorf("get external data error %v,key %v", err, data.Key)
		return nil, err
	}

	// Process data with common function
	return common.ProcessExternalSecretData(data, externalData)
}

func (c *OOSClient) GetExternalSecretWithExtract(data *v1alpha1.DataProcess, kube client.Client) (map[string][]byte, error) {
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

package oos

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	oos "github.com/alibabacloud-go/oos-20190601/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// Client interface represent a backend client interface that should be implemented
type OOSClient struct {
	oosClient       *oos.Client
	clientName      string
}

func (c *OOSClient) GetName() string {
	return c.clientName
}

func (c *OOSClient) getExternalData(ctx context.Context, data v1alpha1.DataSource) ([]byte, error) {
	// oos
	oosData, err := c.getExternalDataFromOOS(data)
	if err != nil {
		klog.Errorf("get external data from oos error %v,key %v", err, data.Key)
		return nil, err
	}

	return oosData, nil
}

func (c *OOSClient) GetExternalSecret(ctx context.Context, data *v1alpha1.DataSource, kube client.Client) (map[string][]byte, error) {
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

func (c *OOSClient) GetExternalSecretWithExtract(ctx context.Context, data *v1alpha1.DataProcess, kube client.Client) (map[string][]byte, error) {
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

func (c *OOSClient) getExternalDataFromOOS(data v1alpha1.DataSource) ([]byte, error) {
	if c.oosClient == nil {
		return nil, fmt.Errorf("oos client is nil,oos key %v", data.Key)
	}
	req := &oos.GetSecretParameterRequest{
		Name: tea.String(data.Key),
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

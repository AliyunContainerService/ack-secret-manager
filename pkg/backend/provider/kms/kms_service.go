package kms

import (
	"context"
	"encoding/base64"
	"fmt"

	kms "github.com/alibabacloud-go/kms-20160120/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

// KMSClient implements the backend secret client for KMS
type KMSClient struct {
	kmsClient  *kms.Client
	clientName string
}

func (c *KMSClient) GetName() string {
	return c.clientName
}

func (c *KMSClient) getExternalData(ctx context.Context, data v1alpha1.DataSource) ([]byte, error) {
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

	// Fetch with bounded retries on transient errors; resp is only assigned
	// on success to avoid stale values.
	var resp *kms.GetSecretValueResponse
	if err := common.FetchWithRetry(ctx, func() error {
		r, fetchErr := c.kmsClient.GetSecretValue(req)
		if fetchErr != nil {
			klog.Warningf("get secret value from kms failed, key %v, error %v", data.Key, fetchErr)
			return fetchErr
		}
		resp = r
		return nil
	}); err != nil {
		// Final failure after bounded retries: keep Warning level (the per-attempt
		// logs above are also Warning) so the exhausted-retry outcome is never
		// quieter than the individual attempts. At most one such line per sync
		// round; the controller records the Error-level counterpart.
		klog.Warningf("failed to get secret value from kms after retries, key %v, error %v", data.Key, err)
		return nil, err
	}

	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("get secret value from kms failed because response is empty, key %v", data.Key)
	}
	// Guard against a structurally empty response: dereferencing SecretData
	// without this check would panic (symmetric with the OOS side guard).
	if resp.Body.SecretData == nil {
		return nil, fmt.Errorf("get secret value from kms failed because secret data is empty, key %v", data.Key)
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

func (c *KMSClient) GetExternalSecret(ctx context.Context, data *v1alpha1.DataSource, kube client.Client) (map[string][]byte, error) {
	externalData, err := c.getExternalData(ctx, *data)
	if err != nil {
		klog.Errorf("get external data error %v,key %v", err, data.Key)
		return nil, err
	}

	return common.ProcessExternalSecretData(data, externalData)
}

func (c *KMSClient) GetExternalSecretWithExtract(ctx context.Context, data *v1alpha1.DataProcess, kube client.Client) (map[string][]byte, error) {
	if data.Extract == nil {
		return nil, fmt.Errorf("extract data is empty")
	}

	externalData, err := c.getExternalData(ctx, *data.Extract)
	if err != nil {
		klog.Errorf("get external data error %v,key %v", err, data.Extract.Key)
		return nil, err
	}

	return common.ProcessExtractedExternalSecretData(data, externalData)
}

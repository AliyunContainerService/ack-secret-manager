package oos

import (
	"context"
	"fmt"

	oos "github.com/alibabacloud-go/oos-20190601/v3/client"
	"github.com/alibabacloud-go/tea/tea"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/provider/common"
)

// OOSClient implements the backend secret client for OOS
type OOSClient struct {
	oosClient  *oos.Client
	clientName string
}

func (c *OOSClient) GetName() string {
	return c.clientName
}

func (c *OOSClient) getExternalData(ctx context.Context, data v1alpha1.DataSource) ([]byte, error) {
	if c.oosClient == nil {
		return nil, fmt.Errorf("oos client is nil,oos key %v", data.Key)
	}
	req := &oos.GetSecretParameterRequest{
		Name:           tea.String(data.Key),
		WithDecryption: tea.Bool(true),
	}
	// Fetch with bounded retries on transient errors; resp is only assigned
	// on success to avoid stale values.
	var resp *oos.GetSecretParameterResponse
	if err := common.FetchWithRetry(ctx, func() error {
		r, fetchErr := c.oosClient.GetSecretParameter(req)
		if fetchErr != nil {
			klog.Warningf("get secret parameter from oos failed, key %v, error %v", data.Key, fetchErr)
			return fetchErr
		}
		resp = r
		return nil
	}); err != nil {
		// Final failure after bounded retries: keep Warning level (the per-attempt
		// logs above are also Warning) so the exhausted-retry outcome is never
		// quieter than the individual attempts. At most one such line per sync
		// round; the controller records the Error-level counterpart.
		klog.Warningf("failed to get secret parameter from oos after retries, key %v, error %v", data.Key, err)
		return nil, err
	}

	// Guard against a structurally empty response: dereferencing
	// Body/Parameter/Value without these checks would panic.
	if resp == nil || resp.Body == nil || resp.Body.Parameter == nil || resp.Body.Parameter.Value == nil {
		return nil, fmt.Errorf("get secret parameter from oos failed because response is empty, key %v", data.Key)
	}
	klog.Infof("got secret data from oos service,key %v", data.Key)
	return []byte(*resp.Body.Parameter.Value), nil
}

func (c *OOSClient) GetExternalSecret(ctx context.Context, data *v1alpha1.DataSource, kube client.Client) (map[string][]byte, error) {
	externalData, err := c.getExternalData(ctx, *data)
	if err != nil {
		klog.Errorf("get external data error %v,key %v", err, data.Key)
		return nil, err
	}

	return common.ProcessExternalSecretData(data, externalData)
}

func (c *OOSClient) GetExternalSecretWithExtract(ctx context.Context, data *v1alpha1.DataProcess, kube client.Client) (map[string][]byte, error) {
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

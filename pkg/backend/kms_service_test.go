package backend

import (
	"github.com/aliyun/alibaba-cloud-sdk-go/services/kms"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sync"
	"testing"
	"time"
)

const (
	testKeyData  = "foo"
	versionStage = "ACSCurrent"
)

func TestAliKmsServiceClient(t *testing.T) {

	logger := logf.Log.WithName("test")
	backendCfg := Config{
		Region:              "cn-hangzhou",
		TokenRotationPeriod: time.Minute * 10,
	}
	client := newKMSClient(logger, backendCfg)
	t.Logf("client is %v", client)

	err := setConfig(client)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("client is %v", client)

}

type mockAlicloudKMSClient struct {
	credLock sync.RWMutex
	keyID    string
}

func (client *mockAlicloudKMSClient) GetSecretValue(request *kms.GetSecretValueRequest) (response *kms.GetSecretValueResponse, err error) {
	response = kms.CreateGetSecretValueResponse()
	response.SecretData = testKeyData
	response.VersionStages = kms.VersionStagesInGetSecretValue{
		VersionStage: []string{versionStage},
	}
	return response, nil
}

func TestGetSecret(t *testing.T) {

	logger := logf.Log.WithName("test")
	backendCfg := Config{
		Region:              "cn-hangzhou",
		TokenRotationPeriod: time.Minute * 10,
	}
	err := newKMSClient(logger, backendCfg)
	if err != nil {
		t.Fatal(err)
	}
}

// Encrypt is a mocked call that returns a base64 encoded string.
func (m *mockAlicloudKMSClient) GetSecret(key string, queryCondition *SecretQueryCondition) (string, error) {
	return "", nil
}

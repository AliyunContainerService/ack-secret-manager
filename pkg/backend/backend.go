package backend

import (
	"context"
	"time"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const alicloud_secretmanager = "alicloud-kms"

var supportedBackends map[string]bool

var log = logf.Log.WithName("backend")

func init() {
	supportedBackends = map[string]bool{alicloud_secretmanager: true}
}

// Config including configuration from all kinds of backend
type Config struct {
	KMSEndpoint         string
	AccessKeyID         string
	AccessKeySecret     string
	TokenRotationPeriod time.Duration
	Region              string
}

type SecretQueryCondition struct {
	VersionId    string
	VersionStage string
}

// Client interface represent a backend client interface that should be implemented
type Client interface {
	GetSecret(key string, queryCondition *SecretQueryCondition) (string, error)
}

// NewBackendClient returns and implementation of Client interface, given the selected backend
func NewBackendClient(ctx context.Context, backend string, cfg Config) (*Client, error) {
	var err error
	var client Client

	if !supportedBackends[backend] {
		err = &utils.BackendNotImplementedError{ErrType: utils.BackendNotImplementedErrorType, Backend: backend}

		return nil, err
	}
	switch backend {
	case alicloud_secretmanager:
		aliClient := newKMSClient(log, cfg)
		err = setConfig(aliClient)
		if err != nil {
			log.Error(err, "failed to set config for alicloud kms client")
			return nil, err
		}
		//loop to refresh the client credential
		if cfg.AccessKeyID == "" && cfg.AccessKeySecret == "" {
			aliClient.pullForCreds(ctx)
		}
		client = aliClient
		log.Info("NewBackendClient finish", "client", client, "aliClient.kmsClient", aliClient.kmsClient)
	}
	return &client, err
}

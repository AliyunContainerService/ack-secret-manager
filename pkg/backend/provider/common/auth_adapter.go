package common

import (
	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
)

// KMSAuthAdapter adapts KMSAuth to AuthConfigProvider interface
type KMSAuthAdapter struct {
	*v1alpha1.KMSAuth
	StoreName string
}

func (a *KMSAuthAdapter) GetServiceAccountRef() *v1alpha1.ServiceAccountRef {
	if a.KMSAuth == nil {
		return nil
	}
	return a.ServiceAccountRef
}

func (a *KMSAuthAdapter) GetAccessKey() *v1alpha1.SecretRef {
	if a.KMSAuth == nil {
		return nil
	}
	return a.AccessKey
}

func (a *KMSAuthAdapter) GetAccessKeySecret() *v1alpha1.SecretRef {
	if a.KMSAuth == nil {
		return nil
	}
	return a.AccessKeySecret
}

func (a *KMSAuthAdapter) GetRAMRoleARN() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.RAMRoleARN
}

func (a *KMSAuthAdapter) GetRAMRoleSessionName() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.RAMRoleSessionName
}

func (a *KMSAuthAdapter) GetOIDCProviderARN() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.OIDCProviderARN
}

func (a *KMSAuthAdapter) GetOIDCTokenFilePath() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.OIDCTokenFilePath
}

func (a *KMSAuthAdapter) GetRoleSessionExpiration() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.RoleSessionExpiration
}

func (a *KMSAuthAdapter) GetRemoteRAMRoleARN() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.RemoteRAMRoleARN
}

func (a *KMSAuthAdapter) GetRemoteRAMRoleSessionName() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.RemoteRAMRoleSessionName
}

func (a *KMSAuthAdapter) GetSecretStoreName() string {
	return a.StoreName
}

// OOSAuthAdapter adapts OOSAuth to AuthConfigProvider interface
type OOSAuthAdapter struct {
	*v1alpha1.OOSAuth
	StoreName string
}

func (a *OOSAuthAdapter) GetServiceAccountRef() *v1alpha1.ServiceAccountRef {
	if a.OOSAuth == nil {
		return nil
	}
	return a.ServiceAccountRef
}

func (a *OOSAuthAdapter) GetAccessKey() *v1alpha1.SecretRef {
	if a.OOSAuth == nil {
		return nil
	}
	return a.AccessKey
}

func (a *OOSAuthAdapter) GetAccessKeySecret() *v1alpha1.SecretRef {
	if a.OOSAuth == nil {
		return nil
	}
	return a.AccessKeySecret
}

func (a *OOSAuthAdapter) GetRAMRoleARN() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.RAMRoleARN
}

func (a *OOSAuthAdapter) GetRAMRoleSessionName() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.RAMRoleSessionName
}

func (a *OOSAuthAdapter) GetOIDCProviderARN() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OIDCProviderARN
}

func (a *OOSAuthAdapter) GetOIDCTokenFilePath() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OIDCTokenFilePath
}

func (a *OOSAuthAdapter) GetRoleSessionExpiration() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.RoleSessionExpiration
}

func (a *OOSAuthAdapter) GetRemoteRAMRoleARN() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.RemoteRAMRoleARN
}

func (a *OOSAuthAdapter) GetRemoteRAMRoleSessionName() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.RemoteRAMRoleSessionName
}

func (a *OOSAuthAdapter) GetSecretStoreName() string {
	return a.StoreName
}

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
	return a.KMSAuth.ServiceAccountRef
}

func (a *KMSAuthAdapter) GetAccessKey() *v1alpha1.SecretRef {
	if a.KMSAuth == nil {
		return nil
	}
	return a.KMSAuth.AccessKey
}

func (a *KMSAuthAdapter) GetAccessKeySecret() *v1alpha1.SecretRef {
	if a.KMSAuth == nil {
		return nil
	}
	return a.KMSAuth.AccessKeySecret
}

func (a *KMSAuthAdapter) GetRAMRoleARN() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.KMSAuth.RAMRoleARN
}

func (a *KMSAuthAdapter) GetRAMRoleSessionName() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.KMSAuth.RAMRoleSessionName
}

func (a *KMSAuthAdapter) GetOIDCProviderARN() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.KMSAuth.OIDCProviderARN
}

func (a *KMSAuthAdapter) GetOIDCTokenFilePath() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.KMSAuth.OIDCTokenFilePath
}

func (a *KMSAuthAdapter) GetRoleSessionExpiration() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.KMSAuth.RoleSessionExpiration
}

func (a *KMSAuthAdapter) GetRemoteRAMRoleARN() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.KMSAuth.RemoteRAMRoleARN
}

func (a *KMSAuthAdapter) GetRemoteRAMRoleSessionName() string {
	if a.KMSAuth == nil {
		return ""
	}
	return a.KMSAuth.RemoteRAMRoleSessionName
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
	return a.OOSAuth.ServiceAccountRef
}

func (a *OOSAuthAdapter) GetAccessKey() *v1alpha1.SecretRef {
	if a.OOSAuth == nil {
		return nil
	}
	return a.OOSAuth.AccessKey
}

func (a *OOSAuthAdapter) GetAccessKeySecret() *v1alpha1.SecretRef {
	if a.OOSAuth == nil {
		return nil
	}
	return a.OOSAuth.AccessKeySecret
}

func (a *OOSAuthAdapter) GetRAMRoleARN() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OOSAuth.RAMRoleARN
}

func (a *OOSAuthAdapter) GetRAMRoleSessionName() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OOSAuth.RAMRoleSessionName
}

func (a *OOSAuthAdapter) GetOIDCProviderARN() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OOSAuth.OIDCProviderARN
}

func (a *OOSAuthAdapter) GetOIDCTokenFilePath() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OOSAuth.OIDCTokenFilePath
}

func (a *OOSAuthAdapter) GetRoleSessionExpiration() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OOSAuth.RoleSessionExpiration
}

func (a *OOSAuthAdapter) GetRemoteRAMRoleARN() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OOSAuth.RemoteRAMRoleARN
}

func (a *OOSAuthAdapter) GetRemoteRAMRoleSessionName() string {
	if a.OOSAuth == nil {
		return ""
	}
	return a.OOSAuth.RemoteRAMRoleSessionName
}

func (a *OOSAuthAdapter) GetSecretStoreName() string {
	return a.StoreName
}

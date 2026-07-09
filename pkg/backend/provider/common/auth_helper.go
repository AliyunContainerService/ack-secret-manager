package common

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend/auth"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
)

const (
	// ACKRRSAAnnotation is the annotation key for RRSA role ARN on ServiceAccount
	ACKRRSAAnnotation = "ack.alibabacloud.com/role-arn"
	// DefaultOIDCTokenFile is the default path for OIDC token file
	DefaultOIDCTokenFile = "/var/run/secrets/tokens/ack-secret-manager"
	// TokenAudience is the default value for RAM oidc token auth
	TokenAudience = "sts.aliyuncs.com"
)

// EnableCrossNamespaceAuthRef controls whether cross namespace references are allowed for auth
var EnableCrossNamespaceAuthRef = true

// AuthConfigProvider defines an interface for extracting authentication configuration
// from provider-specific auth structs (KMSAuth, OOSAuth, etc.)
type AuthConfigProvider interface {
	GetServiceAccountRef() *v1alpha1.ServiceAccountRef
	GetAccessKey() *v1alpha1.SecretRef
	GetAccessKeySecret() *v1alpha1.SecretRef
	GetRAMRoleARN() string
	GetRAMRoleSessionName() string
	GetOIDCProviderARN() string
	GetOIDCTokenFilePath() string
	GetRoleSessionExpiration() string
	GetRemoteRAMRoleARN() string
	GetRemoteRAMRoleSessionName() string
	GetSecretStoreName() string
}

// BuildAuthConfig builds an auth.AuthConfig from a SecretStore and AuthConfigProvider
func BuildAuthConfig(
	ctx context.Context,
	store *v1alpha1.SecretStore,
	kube client.Client,
	authProvider AuthConfigProvider,
	clusterId string,
	uid string,
) (auth.AuthConfig, error) {
	var clientName string
	if store.Namespace != "" {
		// SecretStore
		clientName = fmt.Sprintf("namespace/%s/%s", store.Namespace, store.Name)
	} else {
		// ClusterSecretStore
		clientName = fmt.Sprintf("cluster/%s", store.Name)
	}

	authConfig := auth.AuthConfig{
		ClientName:    clientName,
		RefreshPeriod: time.Minute * 10,
		TokenFilePath: DefaultOIDCTokenFile,
	}

	// Try to extract kubernetes.Interface from wrapped client
	if wrappedClient, ok := kube.(interface{ GetKubeClient() kubernetes.Interface }); ok {
		authConfig.KubeClient = wrappedClient.GetKubeClient()
		klog.Info("Using kubernetes client interface from wrapped client for dynamic token acquisition")
	} else {
		klog.Info("Kubernetes client does not support GetKubeClient, skipping dynamic token acquisition")
	}

	if authProvider == nil {
		return authConfig, nil
	}

	// Handle ServiceAccount authentication method (highest priority)
	if authProvider.GetServiceAccountRef() != nil {
		saConfig, err := buildServiceAccountRefAuthConfig(ctx, store, kube, authProvider, clusterId, uid)
		if err != nil {
			return authConfig, err
		}
		// Merge ServiceAccount auth config into main config
		authConfig.RoleArn = saConfig.RoleArn
		authConfig.OidcArn = saConfig.OidcArn
		authConfig.ServiceAccountName = saConfig.ServiceAccountName
		authConfig.ServiceAccountNamespace = saConfig.ServiceAccountNamespace
		authConfig.TokenAudiences = saConfig.TokenAudiences
		authConfig.RemoteRoleArn = saConfig.RemoteRoleArn
		authConfig.RemoteRoleSessionName = saConfig.RemoteRoleSessionName
	} else {
		// Use traditional authentication method
		traditionalConfig, err := buildAuthConfigWithCrossNamespace(ctx, store, kube, authProvider, clusterId, uid)
		if err != nil {
			return authConfig, err
		}
		// Merge traditional auth config into main config
		authConfig.AccessKey = traditionalConfig.AccessKey
		authConfig.AccessSecretKey = traditionalConfig.AccessSecretKey
		authConfig.RoleArn = traditionalConfig.RoleArn
		authConfig.OidcArn = traditionalConfig.OidcArn
		authConfig.RoleSessionName = traditionalConfig.RoleSessionName
		authConfig.RoleSessionExpiration = traditionalConfig.RoleSessionExpiration
		authConfig.RemoteRoleSessionName = traditionalConfig.RemoteRoleSessionName
		authConfig.RemoteRoleArn = traditionalConfig.RemoteRoleArn
		authConfig.TokenFilePath = traditionalConfig.TokenFilePath
	}

	return authConfig, nil
}

// buildServiceAccountAuth builds authentication config from ServiceAccount
func buildServiceAccountRefAuthConfig(
	ctx context.Context,
	store *v1alpha1.SecretStore,
	kube client.Client,
	authProvider AuthConfigProvider,
	clusterId string,
	uid string,
) (auth.AuthConfig, error) {
	saRef := authProvider.GetServiceAccountRef()
	sa := &corev1.ServiceAccount{}

	// Determine the namespace for the ServiceAccount
	saNamespace := store.Namespace
	// If store.Namespace is empty, it means this is a ClusterSecretStore
	// For ClusterSecretStore, we use the namespace specified in ServiceAccountRef
	// For SecretStore, we check if cross namespace reference is enabled
	if store.Namespace == "" {
		// This is a ClusterSecretStore
		if saRef.Namespace == "" {
			return auth.AuthConfig{}, fmt.Errorf("ClusterSecretStore requires namespace to be specified in serviceAccountRef")
		}
		saNamespace = saRef.Namespace
	} else {
		// This is a SecretStore
		// If ServiceAccountRef.Namespace is specified and differs from store's namespace,
		// we need to check if cross namespace reference is enabled
		if saRef.Namespace != "" && saRef.Namespace != store.Namespace {
			// Check if cross namespace reference is enabled
			if !EnableCrossNamespaceAuthRef {
				return auth.AuthConfig{}, fmt.Errorf("cross namespace ServiceAccountRef is disabled, cannot reference ServiceAccount in namespace %s from SecretStore in namespace %s", saRef.Namespace, store.Namespace)
			}
			saNamespace = saRef.Namespace
		}
	}

	err := kube.Get(ctx, client.ObjectKey{
		Namespace: saNamespace,
		Name:      saRef.Name,
	}, sa)
	if err != nil {
		return auth.AuthConfig{}, fmt.Errorf("failed to get ServiceAccount %s/%s: %v", saNamespace, saRef.Name, err)
	}

	// Get RRSA role ARN from annotation
	roleArn, ok := sa.Annotations[ACKRRSAAnnotation]
	if !ok {
		return auth.AuthConfig{}, fmt.Errorf("ServiceAccount %s/%s does not have RRSA annotation %s", saNamespace, saRef.Name, ACKRRSAAnnotation)
	}

	klog.Infof("Using RRSA role ARN from ServiceAccount %s/%s annotation: %s", saNamespace, saRef.Name, roleArn)

	// Use configured OIDC Provider ARN, or default value if not configured
	oidcProviderArn := authProvider.GetOIDCProviderARN()
	if oidcProviderArn == "" || !utils.IsValidOidcProviderArn(oidcProviderArn) {
		if clusterId != "" && uid != "" {
			klog.Warningf("Invalid oidcProviderARN %s defined in SecretStore %s, will use default", oidcProviderArn, authProvider.GetSecretStoreName())
			// Generate default OIDC Provider ARN based on clusterId and uid
			uidInt64, err := strconv.ParseInt(uid, 10, 64)
			if err != nil {
				klog.Warningf("Failed to parse uid %s as int64, using 0: %v", uid, err)
				return auth.AuthConfig{}, err
			}
			oidcProviderArn = utils.GenerateDefaultOidcProviderArn(clusterId, uidInt64)
		}
	}

	// Determine token audiences
	tokenAudiences := saRef.Audiences
	if len(tokenAudiences) == 0 {
		tokenAudiences = []string{TokenAudience}
	}

	// Read cross-account (remote) role configuration from provider
	remoteRoleArn := authProvider.GetRemoteRAMRoleARN()
	remoteRoleSessionName := authProvider.GetRemoteRAMRoleSessionName()

	return auth.AuthConfig{
		RoleArn:                 roleArn,
		OidcArn:                 oidcProviderArn,
		ServiceAccountName:      saRef.Name,
		ServiceAccountNamespace: saNamespace,
		TokenAudiences:          tokenAudiences,
		RemoteRoleArn:           remoteRoleArn,
		RemoteRoleSessionName:   remoteRoleSessionName,
	}, nil
}

// buildTraditionalAuth builds authentication config from traditional auth methods
func buildAuthConfigWithCrossNamespace(
	ctx context.Context,
	store *v1alpha1.SecretStore,
	kube client.Client,
	authProvider AuthConfigProvider,
	clusterId string,
	uid string,
) (auth.AuthConfig, error) {
	authConfig := auth.AuthConfig{
		TokenFilePath: DefaultOIDCTokenFile,
	}

	if authProvider.GetAccessKey() != nil {
		// Determine the namespace for the AccessKey Secret
		accessKeyNamespace := store.Namespace
		// If store.Namespace is empty, it means this is a ClusterSecretStore
		// For ClusterSecretStore, we use the namespace specified in AccessKey
		// For SecretStore, we check if cross namespace reference is enabled
		if store.Namespace == "" {
			// This is a ClusterSecretStore
			if authProvider.GetAccessKey().Namespace == "" {
				return auth.AuthConfig{}, fmt.Errorf("ClusterSecretStore requires namespace to be specified in accessKey")
			}
			accessKeyNamespace = authProvider.GetAccessKey().Namespace
		} else {
			// This is a SecretStore
			// If AccessKey.Namespace is specified and differs from store's namespace,
			// we need to check if cross namespace reference is enabled
			if authProvider.GetAccessKey().Namespace != "" && authProvider.GetAccessKey().Namespace != store.Namespace {
				// Check if cross namespace reference is enabled
				if !EnableCrossNamespaceAuthRef {
					return auth.AuthConfig{}, fmt.Errorf("cross namespace AccessKey is disabled, cannot reference Secret in namespace %s from SecretStore in namespace %s", authProvider.GetAccessKey().Namespace, store.Namespace)
				}
				accessKeyNamespace = authProvider.GetAccessKey().Namespace
			}
		}

		accessKeyRef := &v1alpha1.SecretRef{
			Name:      authProvider.GetAccessKey().Name,
			Namespace: accessKeyNamespace,
			Key:       authProvider.GetAccessKey().Key,
		}

		accessKey, err := utils.GetConfigFromSecret(ctx, kube, accessKeyRef)
		if err != nil {
			klog.Errorf("get ak config from secret error %v", err)
		} else {
			authConfig.AccessKey = string(accessKey)
		}
	}

	if authProvider.GetAccessKeySecret() != nil {
		// Determine the namespace for the AccessKeySecret Secret
		accessKeySecretNamespace := store.Namespace
		// If store.Namespace is empty, it means this is a ClusterSecretStore
		// For ClusterSecretStore, we use the namespace specified in AccessKeySecret
		// For SecretStore, we check if cross namespace reference is enabled
		if store.Namespace == "" {
			// This is a ClusterSecretStore
			if authProvider.GetAccessKeySecret().Namespace == "" {
				return auth.AuthConfig{}, fmt.Errorf("ClusterSecretStore requires namespace to be specified in accessKeySecret")
			}
			accessKeySecretNamespace = authProvider.GetAccessKeySecret().Namespace
		} else {
			// This is a SecretStore
			// If AccessKeySecret.Namespace is specified and differs from store's namespace,
			// we need to check if cross namespace reference is enabled
			if authProvider.GetAccessKeySecret().Namespace != "" && authProvider.GetAccessKeySecret().Namespace != store.Namespace {
				// Check if cross namespace reference is enabled
				if !EnableCrossNamespaceAuthRef {
					return auth.AuthConfig{}, fmt.Errorf("cross namespace AccessKeySecret is disabled, cannot reference Secret in namespace %s from SecretStore in namespace %s", authProvider.GetAccessKeySecret().Namespace, store.Namespace)
				}
				accessKeySecretNamespace = authProvider.GetAccessKeySecret().Namespace
			}
		}

		accessKeySecretRef := &v1alpha1.SecretRef{
			Name:      authProvider.GetAccessKeySecret().Name,
			Namespace: accessKeySecretNamespace,
			Key:       authProvider.GetAccessKeySecret().Key,
		}

		accessKeySecret, err := utils.GetConfigFromSecret(ctx, kube, accessKeySecretRef)
		if err != nil {
			klog.Errorf("get sk config from secret error %v", err)
		} else {
			authConfig.AccessSecretKey = string(accessKeySecret)
		}
	}

	authConfig.RoleArn = authProvider.GetRAMRoleARN()
	authConfig.OidcArn = authProvider.GetOIDCProviderARN()
	authConfig.RoleSessionName = authProvider.GetRAMRoleSessionName()
	authConfig.RoleSessionExpiration = authProvider.GetRoleSessionExpiration()
	authConfig.RemoteRoleSessionName = authProvider.GetRemoteRAMRoleSessionName()
	authConfig.RemoteRoleArn = authProvider.GetRemoteRAMRoleARN()

	// Use specified token file path if provided
	if authProvider.GetOIDCTokenFilePath() != "" {
		authConfig.TokenFilePath = authProvider.GetOIDCTokenFilePath()
	}

	// Validate OIDC Provider ARN if required (only for KMS)
	if authConfig.OidcArn == "" || !utils.IsValidOidcProviderArn(authConfig.OidcArn) {
		if uid != "" && clusterId != "" {
			klog.Warningf("Invalid oidcProviderARN %s defined in SecretStore %s, will use default", authConfig.OidcArn, authProvider.GetSecretStoreName())
			// Generate default OIDC Provider ARN based on clusterId and uid
			uidInt64, err := strconv.ParseInt(uid, 10, 64)
			if err != nil {
				klog.Warningf("Failed to parse uid %s as int64, using 0: %v", uid, err)
				return auth.AuthConfig{}, err
			}
			authConfig.OidcArn = utils.GenerateDefaultOidcProviderArn(clusterId, uidInt64)
		}
	}

	return authConfig, nil
}

func BuildAuthConfigFromEnv() auth.AuthConfig {
	return auth.AuthConfig{
		ClientName:            backend.EnvClient,
		RoleArn:               os.Getenv("ALICLOUD_ROLE_ARN"),
		OidcArn:               os.Getenv("ALICLOUD_OIDC_PROVIDER_ARN"),
		AccessKey:             os.Getenv("ACCESS_KEY_ID"),
		AccessSecretKey:       os.Getenv("SECRET_ACCESS_KEY"),
		RoleSessionName:       os.Getenv("ALICLOUD_ROLE_SESSION_NAME"),
		RoleSessionExpiration: os.Getenv("ALICLOUD_ROLE_SESSION_EXPIRATION"),
		RemoteRoleSessionName: os.Getenv("ALICLOUD_REMOTE_ROLE_SESSION_NAME"),
		RemoteRoleArn:         os.Getenv("ALICLOUD_REMOTE_ROLE_ARN"),
		RefreshPeriod:         time.Second * 10,
	}
}

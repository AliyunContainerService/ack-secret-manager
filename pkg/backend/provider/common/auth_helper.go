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
	// ACKRRSAAnnotation is the RRSA role ARN annotation key on ServiceAccount
	ACKRRSAAnnotation = "ack.alibabacloud.com/role-arn"
	// DefaultOIDCTokenFile is the default OIDC token file path
	DefaultOIDCTokenFile = "/var/run/secrets/tokens/ack-secret-manager"
	// TokenAudience is the default audience for RAM oidc token auth
	TokenAudience = "sts.aliyuncs.com"
)

// EnableCrossNamespaceAuthRef controls whether cross-namespace auth references are allowed
var EnableCrossNamespaceAuthRef = false

// AuthConfigProvider extracts authentication configuration from
// provider-specific auth structs (KMSAuth, OOSAuth, etc.)
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
		clientName = backend.SecretStoreKey(store.Namespace, store.Name)
	} else {
		// ClusterSecretStore
		clientName = backend.ClusterStoreKey(store.Name)
	}

	authConfig := auth.AuthConfig{
		ClientName:    clientName,
		RefreshPeriod: time.Minute * 10,
		TokenFilePath: DefaultOIDCTokenFile,
	}

	// Extract kubernetes.Interface from the wrapped client
	if wrappedClient, ok := kube.(interface{ GetKubeClient() kubernetes.Interface }); ok {
		authConfig.KubeClient = wrappedClient.GetKubeClient()
	}

	// Fail-closed: with an explicit serviceAccountRef, dynamic token
	// acquisition is mandatory; silently degrading to the file-based token
	// would authenticate with a different identity than requested.
	if authProvider != nil && authProvider.GetServiceAccountRef() != nil && authConfig.KubeClient == nil {
		return authConfig, fmt.Errorf("serviceAccountRef is configured in SecretStore %s but the kubernetes client is unavailable for ServiceAccount authentication", authProvider.GetSecretStoreName())
	}

	if authProvider == nil {
		return authConfig, nil
	}

	// ServiceAccount authentication (highest priority)
	if authProvider.GetServiceAccountRef() != nil {
		saConfig, err := buildServiceAccountRefAuthConfig(ctx, store, kube, authProvider, clusterId, uid)
		if err != nil {
			return authConfig, err
		}
		authConfig.RoleArn = saConfig.RoleArn
		authConfig.OidcArn = saConfig.OidcArn
		authConfig.OidcArnFromDefault = saConfig.OidcArnFromDefault
		authConfig.ServiceAccountName = saConfig.ServiceAccountName
		authConfig.ServiceAccountNamespace = saConfig.ServiceAccountNamespace
		authConfig.TokenAudiences = saConfig.TokenAudiences
		authConfig.RemoteRoleArn = saConfig.RemoteRoleArn
		authConfig.RemoteRoleSessionName = saConfig.RemoteRoleSessionName
	} else {
		// Traditional authentication
		traditionalConfig, err := buildAuthConfigWithCrossNamespace(ctx, store, kube, authProvider, clusterId, uid)
		if err != nil {
			return authConfig, err
		}
		authConfig.AccessKey = traditionalConfig.AccessKey
		authConfig.AccessSecretKey = traditionalConfig.AccessSecretKey
		authConfig.RoleArn = traditionalConfig.RoleArn
		authConfig.OidcArn = traditionalConfig.OidcArn
		authConfig.OidcArnFromDefault = traditionalConfig.OidcArnFromDefault
		authConfig.RoleSessionName = traditionalConfig.RoleSessionName
		authConfig.RoleSessionExpiration = traditionalConfig.RoleSessionExpiration
		authConfig.RemoteRoleSessionName = traditionalConfig.RemoteRoleSessionName
		authConfig.RemoteRoleArn = traditionalConfig.RemoteRoleArn
		authConfig.TokenFilePath = traditionalConfig.TokenFilePath
	}

	return authConfig, nil
}

// buildServiceAccountRefAuthConfig builds authentication config from a ServiceAccount reference (RRSA).
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

	// ClusterSecretStore requires an explicit namespace; SecretStore allows
	// cross-namespace only when EnableCrossNamespaceAuthRef is set.
	saNamespace := store.Namespace
	if store.Namespace == "" {
		if saRef.Namespace == "" {
			return auth.AuthConfig{}, fmt.Errorf("ClusterSecretStore requires namespace to be specified in serviceAccountRef")
		}
		saNamespace = saRef.Namespace
	} else {
		if saRef.Namespace != "" && saRef.Namespace != store.Namespace {
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

	// RRSA role ARN from annotation
	roleArn, ok := sa.Annotations[ACKRRSAAnnotation]
	if !ok {
		return auth.AuthConfig{}, fmt.Errorf("ServiceAccount %s/%s does not have RRSA annotation %s", saNamespace, saRef.Name, ACKRRSAAnnotation)
	}

	klog.Infof("Using RRSA role ARN from ServiceAccount %s/%s annotation: %s", saNamespace, saRef.Name, roleArn)

	// Configured OIDC Provider ARN, or a default derived from clusterId/uid
	oidcProviderArn, oidcArnFromDefault, err := resolveOidcProviderArn(authProvider, authProvider.GetOIDCProviderARN(), clusterId, uid)
	if err != nil {
		return auth.AuthConfig{}, err
	}

	tokenAudiences := saRef.Audiences
	if len(tokenAudiences) == 0 {
		tokenAudiences = []string{TokenAudience}
	}

	// Cross-account (remote) role configuration
	remoteRoleArn := authProvider.GetRemoteRAMRoleARN()
	remoteRoleSessionName := authProvider.GetRemoteRAMRoleSessionName()

	return auth.AuthConfig{
		RoleArn:                 roleArn,
		OidcArn:                 oidcProviderArn,
		OidcArnFromDefault:      oidcArnFromDefault,
		ServiceAccountName:      saRef.Name,
		ServiceAccountNamespace: saNamespace,
		TokenAudiences:          tokenAudiences,
		RemoteRoleArn:           remoteRoleArn,
		RemoteRoleSessionName:   remoteRoleSessionName,
	}, nil
}

// buildAuthConfigWithCrossNamespace builds authentication config from traditional (AK/OIDC/env) sources with cross-namespace checks.
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
		// Same namespace rules as serviceAccountRef above
		accessKeyNamespace := store.Namespace
		if store.Namespace == "" {
			if authProvider.GetAccessKey().Namespace == "" {
				return auth.AuthConfig{}, fmt.Errorf("ClusterSecretStore requires namespace to be specified in accessKey")
			}
			accessKeyNamespace = authProvider.GetAccessKey().Namespace
		} else {
			if authProvider.GetAccessKey().Namespace != "" && authProvider.GetAccessKey().Namespace != store.Namespace {
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
			return auth.AuthConfig{}, fmt.Errorf("failed to get access key from secret %s/%s: %w", accessKeyRef.Namespace, accessKeyRef.Name, err)
		}
		authConfig.AccessKey = string(accessKey)
	}

	if authProvider.GetAccessKeySecret() != nil {
		accessKeySecretNamespace := store.Namespace
		if store.Namespace == "" {
			if authProvider.GetAccessKeySecret().Namespace == "" {
				return auth.AuthConfig{}, fmt.Errorf("ClusterSecretStore requires namespace to be specified in accessKeySecret")
			}
			accessKeySecretNamespace = authProvider.GetAccessKeySecret().Namespace
		} else {
			if authProvider.GetAccessKeySecret().Namespace != "" && authProvider.GetAccessKeySecret().Namespace != store.Namespace {
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
			return auth.AuthConfig{}, fmt.Errorf("failed to get access key secret from secret %s/%s: %w", accessKeySecretRef.Namespace, accessKeySecretRef.Name, err)
		}
		authConfig.AccessSecretKey = string(accessKeySecret)
	}

	authConfig.RoleArn = authProvider.GetRAMRoleARN()
	authConfig.OidcArn = authProvider.GetOIDCProviderARN()
	authConfig.RoleSessionName = authProvider.GetRAMRoleSessionName()
	authConfig.RoleSessionExpiration = authProvider.GetRoleSessionExpiration()
	authConfig.RemoteRoleSessionName = authProvider.GetRemoteRAMRoleSessionName()
	authConfig.RemoteRoleArn = authProvider.GetRemoteRAMRoleARN()

	if authProvider.GetOIDCTokenFilePath() != "" {
		authConfig.TokenFilePath = authProvider.GetOIDCTokenFilePath()
	}

	// Validate OIDC Provider ARN (KMS only)
	resolvedOidcArn, oidcArnFromDefault, err := resolveOidcProviderArn(authProvider, authConfig.OidcArn, clusterId, uid)
	if err != nil {
		return auth.AuthConfig{}, err
	}
	authConfig.OidcArn = resolvedOidcArn
	authConfig.OidcArnFromDefault = oidcArnFromDefault

	return authConfig, nil
}

// resolveOidcProviderArn validates the configured OIDC Provider ARN and falls
// back to a default derived from clusterId/uid when it is missing or invalid.
// It returns the resolved ARN, whether it was auto-derived (OidcArnFromDefault
// semantics), and any error encountered while parsing uid.
func resolveOidcProviderArn(authProvider AuthConfigProvider, oidcArn, clusterId, uid string) (string, bool, error) {
	if oidcArn != "" && utils.IsValidOidcProviderArn(oidcArn) {
		return oidcArn, false, nil
	}
	if clusterId != "" && uid != "" {
		if oidcArn == "" {
			klog.Infof("oidcProviderARN not configured in SecretStore %s, using default derived from cluster-id/uid", authProvider.GetSecretStoreName())
		} else {
			klog.Warningf("Invalid oidcProviderARN %s defined in SecretStore %s, will use default", oidcArn, authProvider.GetSecretStoreName())
		}
		uidInt64, err := strconv.ParseInt(uid, 10, 64)
		if err != nil {
			klog.Warningf("Failed to parse uid %s as int64: %v", uid, err)
			return "", false, err
		}
		// Mark auto-derived ARNs so the auth chain can tell them apart
		// from explicitly configured ones.
		return utils.GenerateDefaultOidcProviderArn(clusterId, uidInt64), true, nil
	}
	klog.Warningf("Cannot generate default OIDC provider ARN for SecretStore %s: cluster-id or uid is missing (clusterId=%q, uid=%q); oidcProviderARN stays %q", authProvider.GetSecretStoreName(), clusterId, uid, oidcArn)
	return oidcArn, false, nil
}

// BuildAuthConfigFromEnv builds an auth.AuthConfig from environment variables
func BuildAuthConfigFromEnv() auth.AuthConfig {
	cfg := auth.AuthConfig{
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
	// Fill default session names so the AK+AssumeRole and cross-account tiers
	// are not skipped when the corresponding env vars are not set.
	cfg.NormalizeSessionNames()
	return cfg
}

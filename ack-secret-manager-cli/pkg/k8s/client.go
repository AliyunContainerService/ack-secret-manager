package k8s

import (
	"context"
	"fmt"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/apis/alibabacloud/v1alpha1"
	aliababaclientset "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/client/clientset/versioned"
)

var (
	k8sClient kubernetes.Interface
	crdClient aliababaclientset.Interface
)

func InitClient(filePath string) error {
	config, err := clientcmd.BuildConfigFromFlags("", filePath)
	if err != nil {
		return err
	}
	crdClient, err = aliababaclientset.NewForConfig(config)
	if err != nil {
		return err
	}
	k8sClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	return nil
}

func CreateSecretStoreByRRSA(name, namespace, provider, ramRole string) error {
	ctx := context.Background()
	secretStore := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"creator": "ack-secret-manager-cli",
			},
		},
		Spec: v1alpha1.SecretStoreSpec{
			KMS: &v1alpha1.KMSProvider{
				KMS: &v1alpha1.KMSAuth{
					OIDCProviderARN: provider,
					RAMRoleARN:      ramRole,
				},
			},
		},
	}
	_, err := crdClient.AlibabacloudV1alpha1().SecretStores(namespace).Create(ctx, secretStore, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func CreateSecretStoreByAKSK(name, namespace, akName, akNamespace, akKey, skName, skNamespace, skKey, ramRoleArn, roleSession string) error {
	ctx := context.Background()
	secretStore := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"creator": "ack-secret-manager-cli",
			},
		},
		Spec: v1alpha1.SecretStoreSpec{
			KMS: &v1alpha1.KMSProvider{
				KMS: &v1alpha1.KMSAuth{
					AccessKey: &v1alpha1.SecretRef{
						Name:      akName,
						Namespace: akNamespace,
						Key:       akKey,
					},
					AccessKeySecret: &v1alpha1.SecretRef{
						Name:      skName,
						Namespace: skNamespace,
						Key:       skKey,
					},
				},
			},
		},
	}
	if ramRoleArn != "" {
		secretStore.Spec.KMS.KMS.RAMRoleARN = ramRoleArn
		secretStore.Spec.KMS.KMS.RAMRoleSessionName = roleSession
	}
	_, err := crdClient.AlibabacloudV1alpha1().SecretStores(namespace).Create(ctx, secretStore, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func CheckSecretKeyExist(name, namespace, key string) (bool, error) {
	ctx := context.Background()
	secret, err := k8sClient.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	_, ok := secret.Data[key]
	if ok {
		return true, nil
	}
	return false, nil
}

func CreateOrUpdateSecret(name, namespace, key, value string) error {
	ctx := context.Background()
	secret, err := k8sClient.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			newSecret := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Data: map[string][]byte{
					key: []byte(value),
				},
			}
			_, err = k8sClient.CoreV1().Secrets(namespace).Create(ctx, newSecret, metav1.CreateOptions{})
			if err != nil {
				return err
			}
			return nil
		}
		return err
	}
	secret.Data[key] = []byte(value)
	_, err = k8sClient.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func CreateEmptySecretStore(name, namespace string) error {
	ctx := context.Background()
	secretStore := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"creator": "ack-secret-manager-cli",
			},
		},
		Spec: v1alpha1.SecretStoreSpec{
			KMS: &v1alpha1.KMSProvider{
				KMS: nil,
			},
		},
	}
	_, err := crdClient.AlibabacloudV1alpha1().SecretStores(namespace).Create(ctx, secretStore, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func CreateDKMSSecretStore(name, namespace, ckName, ckNamespace, ckKey, pwName, pwNamespace, pwKey, cert, endpoint string) error {
	ctx := context.Background()
	secretStore := &v1alpha1.SecretStore{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"creator": "ack-secret-manager-cli",
			},
		},
		Spec: v1alpha1.SecretStoreSpec{
			KMS: &v1alpha1.KMSProvider{
				DedicatedKMS: &v1alpha1.DedicatedKMSAuth{
					Protocol: "https",
					Endpoint: endpoint,
					ClientKeyContent: &v1alpha1.SecretRef{
						Name:      ckName,
						Namespace: ckNamespace,
						Key:       ckKey,
					},
					Password: &v1alpha1.SecretRef{
						Name:      pwName,
						Namespace: pwNamespace,
						Key:       pwKey,
					},
				},
			},
		},
	}
	if cert == "" {
		secretStore.Spec.KMS.DedicatedKMS.IgnoreSSL = true
	} else {
		secretStore.Spec.KMS.DedicatedKMS.IgnoreSSL = false
		secretStore.Spec.KMS.DedicatedKMS.CA = cert
	}
	_, err := crdClient.AlibabacloudV1alpha1().SecretStores(namespace).Create(ctx, secretStore, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func ListSecretStore(limit int64, continueToken string) ([]v1alpha1.SecretStore, string, error) {
	ctx := context.Background()
	listOption := metav1.ListOptions{
		Limit:    limit,
		Continue: continueToken,
	}
	secretstores, err := crdClient.AlibabacloudV1alpha1().SecretStores("").List(ctx, listOption)
	if err != nil {
		return nil, continueToken, err
	}
	return secretstores.Items, secretstores.Continue, nil
}

func GetSecretStoreAndUpdate(name, namespace, remoteRoleArn, remoteRoleSession string) error {
	ctx := context.Background()
	secretstore, err := crdClient.AlibabacloudV1alpha1().SecretStores(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if secretstore.Spec.KMS.DedicatedKMS != nil {
		return fmt.Errorf("dkms SecretStore %s/%s does not support cross-account", namespace, name)
	}
	if secretstore.Spec.KMS.KMS == nil {
		secretstore.Spec.KMS.KMS = &v1alpha1.KMSAuth{
			RemoteRAMRoleSessionName: remoteRoleSession,
			RemoteRAMRoleARN:         remoteRoleArn,
		}
	} else {
		secretstore.Spec.KMS.KMS.RemoteRAMRoleARN = remoteRoleArn
		secretstore.Spec.KMS.KMS.RemoteRAMRoleSessionName = remoteRoleSession
	}
	_, err = crdClient.AlibabacloudV1alpha1().SecretStores(namespace).Update(ctx, secretstore, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func CreateExternalSecret(data []v1alpha1.DataSource, dataProcess []v1alpha1.DataProcess, name, namespace, secretType string) error {
	ctx := context.Background()
	es := &v1alpha1.ExternalSecret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
			Labels: map[string]string{
				"creator": "ack-secret-manager-cli",
			},
		},
		Spec: v1alpha1.ExternalSecretSpec{
			Provider:    "kms",
			Data:        data,
			Type:        secretType,
			DataProcess: dataProcess,
		},
	}
	_, err := crdClient.AlibabacloudV1alpha1().ExternalSecrets(namespace).Create(ctx, es, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	return nil
}

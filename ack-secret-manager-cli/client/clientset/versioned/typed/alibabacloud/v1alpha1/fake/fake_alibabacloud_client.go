// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1alpha1 "github.com/AliyunContainerService/ack-secret-manager/ack-secret-manager-cli/pkg/client/clientset/versioned/typed/alibabacloud/v1alpha1"
	rest "k8s.io/client-go/rest"
	testing "k8s.io/client-go/testing"
)

type FakeAlibabacloudV1alpha1 struct {
	*testing.Fake
}

func (c *FakeAlibabacloudV1alpha1) ExternalSecrets(namespace string) v1alpha1.ExternalSecretInterface {
	return &FakeExternalSecrets{c, namespace}
}

func (c *FakeAlibabacloudV1alpha1) SecretStores(namespace string) v1alpha1.SecretStoreInterface {
	return &FakeSecretStores{c, namespace}
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *FakeAlibabacloudV1alpha1) RESTClient() rest.Interface {
	var ret *rest.RESTClient
	return ret
}
package backend

import (
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// WrappedClient wraps both controller-runtime client and kubernetes client;
// it is shared across controllers, and the auth chain probes it through the
// GetKubeClient() interface.
type WrappedClient struct {
	client.Client
	KubeClient kubernetes.Interface
}

// GetKubeClient returns the kubernetes client interface
func (w *WrappedClient) GetKubeClient() kubernetes.Interface {
	return w.KubeClient
}

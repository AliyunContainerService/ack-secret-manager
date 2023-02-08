/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"path/filepath"
	ctrl "sigs.k8s.io/controller-runtime"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	apis "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/backend"
	"k8s.io/client-go/rest"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var r *ExternalSecretReconciler
var testEnv *envtest.Environment

type fakeBackendSecret struct {
	Key     string
	Content string
}

type fakeBackend struct {
	fakeSecrets []fakeBackendSecret
}

func newFakeBackend(fakeSecrets []fakeBackendSecret) fakeBackend {
	return fakeBackend{
		fakeSecrets: fakeSecrets,
	}
}

func (f fakeBackend) GetSecret(key string, queryCondition *backend.SecretQueryCondition) (string, error) {
	for _, fakeSecret := range f.fakeSecrets {
		if fakeSecret.Key == key {
			return fakeSecret.Content, nil
		}
	}
	return "", errors.New("Not found")

}

func getReconciler() *ExternalSecretReconciler {
	return r
}

func TestExternalSecretController(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{})
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd")},
	}

	cfg, err := testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)
	err = apis.AddToScheme(scheme)
	Expect(err).ToNot(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                  scheme,
		LeaderElection:          true,
		LeaderElectionNamespace: "default",
		LeaderElectionID:        "ack-secret-manager-leader-election",
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(mgr).ToNot(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).ToNot(HaveOccurred())

	// Setup Scheme for all resources
	err = apis.AddToScheme(mgr.GetScheme())
	Expect(err).ToNot(HaveOccurred())

	r = &ExternalSecretReconciler{
		Backend: newFakeBackend([]fakeBackendSecret{
			{"secret/data/pathtosecret1", "value"},
		}),
		Client:               k8sClient,
		APIReader:            k8sClient,
		Log:                  logf.Log.WithName("controllers-test").WithName("ExternalSecret"),
		Ctx:                  context.Background(),
		ReconciliationPeriod: 1 * time.Second,
	}
	err = r.SetupWithManager(mgr, 1)
	Expect(err).ToNot(HaveOccurred())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})

package v1alpha1

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"golang.org/x/net/context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// These tests are written in BDD-style using Ginkgo framework. Refer to
// http://onsi.github.io/ginkgo to learn more.

var _ = Describe("ExternalSecret", func() {
	var (
		key              types.NamespacedName
		created, fetched *ExternalSecret
		cfg              *rest.Config
		k8sClient        client.Client
		testEnv          *envtest.Environment
	)

	BeforeEach(func() {
		// Add any setup steps that needs to be executed before each test
	})

	AfterEach(func() {
		// Add any teardown steps that needs to be executed after each test
	})

	// Add Tests for OpenAPI validation (or additonal CRD features) specified in
	// your API definition.
	// Avoid adding tests for vanilla CRUD operations because they would
	// test Kubernetes API server, which isn't the goal here.
	Context("Create API", func() {

		It("should create an object successfully", func() {

			key = types.NamespacedName{
				Name:      "foo",
				Namespace: "default",
			}
			created = &ExternalSecret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo",
					Namespace: "default",
				},
				Spec: ExternalSecretSpec{
					Name: "foo",
					Type: "Opaque",
					Data: []DataSource{
						{
							Key:          "test",
							Name:         "foo",
							VersionStage: "v1test",
						},
						{
							Key:          "test2",
							Name:         "foo2",
							VersionStage: "v2test",
						},
					},
				},
			}
			var err error
			cfg, err = testEnv.Start()
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg).ToNot(BeNil())

			k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
			Expect(err).ToNot(HaveOccurred())
			Expect(k8sClient).ToNot(BeNil())

			By("creating an API obj")
			Expect(k8sClient.Create(context.TODO(), created)).To(Succeed())

			fetched = &ExternalSecret{}
			Expect(k8sClient.Get(context.TODO(), key, fetched)).To(Succeed())
			Expect(fetched).To(Equal(created))

			By("deleting the created object")
			Expect(k8sClient.Delete(context.TODO(), created)).To(Succeed())
			Expect(k8sClient.Get(context.TODO(), key, created)).ToNot(Succeed())
		})
	})

})

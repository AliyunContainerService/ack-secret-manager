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
	"encoding/base64"
	"fmt"

	apis "github.com/AliyunContainerService/ack-secret-manager/pkg/apis/alibabacloud/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	encodedValue = "value"
)

var _ = Describe("SecretsManager", func() {
	var (
		r  *ExternalSecretReconciler
		sd = &apis.ExternalSecret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "externalsec-test",
			},
			Spec: apis.ExternalSecretSpec{
				Name: "secret-test",
				Type: "Opaque",
				Data: []apis.DataSource{
					{
						Name:         "test1",
						Key:          "data1",
						VersionStage: "version1",
					},
					{
						Name:         "test2",
						Key:          "data2",
						VersionStage: "version2",
					},
				},
			},
		}

		sdBackendSecretNotFound = &apis.ExternalSecret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "externalsec-beckend-secret-not-found",
			},
			Spec: apis.ExternalSecretSpec{
				Name: "secret-backend-secret-not-found",
				Type: "Opaque",
				Data: []apis.DataSource{
					{
						Name:         "notfound",
						Key:          "data1",
						VersionStage: "version1",
					},
				},
			},
		}

		sdExcludedNs = &apis.ExternalSecret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "externalsec-excluded-ns",
			},
			Spec: apis.ExternalSecretSpec{
				Name: "sexternalsec-excluded-ns",
				Type: "Opaque",
				Data: []apis.DataSource{
					{
						Name:         "test1",
						Key:          "data1",
						VersionStage: "version1",
					},
				},
			},
		}
	)

	BeforeEach(func() {
		r = getReconciler()
	})

	AfterEach(func() {

	})

	Context("ExternalSecretReconciler.Reconcile", func() {
		It("Create a externalsecret and read the secret", func() {
			err := r.Create(context.Background(), sd)
			fmt.Printf("err: %v", err)
			Expect(err).To(BeNil())
			res, err2 := r.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: sd.Namespace,
					Name:      sd.Name,
				},
			})

			Expect(res).ToNot(BeNil())
			Expect(err2).To(BeNil())

			data, err3 := r.getCurrentData("default", "secret-test")
			Expect(err3).To(BeNil())
			fmt.Printf("data: %v", data)
		})
		It("Create a externalSecret with a secret not deployed in the backend", func() {
			err := r.Create(context.Background(), sdBackendSecretNotFound)
			Expect(err).To(BeNil())
			res, err2 := r.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: sdBackendSecretNotFound.Namespace,
					Name:      sdBackendSecretNotFound.Name,
				},
			})
			Expect(err2).ToNot(BeNil())
			Expect(res).To(Equal(reconcile.Result{}))
		})
		It("Create a externalsecret in a excluded namespace", func() {
			r2 := getReconciler()
			r2.WatchNamespaces = map[string]bool{sdExcludedNs.Namespace: false}
			err := r.Create(context.Background(), sdExcludedNs)
			res, err2 := r.Reconcile(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: sdExcludedNs.Namespace,
					Name:      sdExcludedNs.Name,
				},
			})
			Expect(err).To(BeNil())
			Expect(err2).To(BeNil())
			Expect(res).To(Equal(reconcile.Result{}))
		})
	})
	Context("ExternalSecretReconciler.upsertSecret", func() {
		It("Upsert a secret twice should not raise an error", func() {
			decodedBytes, _ := base64.StdEncoding.DecodeString(encodedValue)
			err := r.upsertSecret(sd, map[string][]byte{"foo": decodedBytes})
			Expect(err).To(BeNil())
			err2 := r.upsertSecret(sd, map[string][]byte{"foo": decodedBytes})
			Expect(err2).To(BeNil())
		})
	})
})

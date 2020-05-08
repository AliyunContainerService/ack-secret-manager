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

package main

import (
	"fmt"
	"github.com/AliyunContainerService/ack-secret-manager/pkg/utils"
	"github.com/golang/glog"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"time"
)

const (
	secretFinalizer = "finalizer.ack.secrets-manager.alibabacloud.com"
	crdName         = "externalsecrets.alibabacloud.com"
	maxRetryNum     = 3
)

var (
	externalSecretGRV = schema.GroupVersionResource{
		Group:    "alibabacloud.com",
		Version:  "v1alpha1",
		Resource: "externalsecrets",
	}
)

// getKubernetesClients returns all the required clients(token CRD client and origin k8s cli) to communicate with
func getKubernetesClients() (dynamic.Interface, apiextensionsclient.Interface, error) {
	var err error
	var cfg *rest.Config

	cfg, err = rest.InClusterConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("error loading kubernetes configuration inside cluster, "+
			"check app is running outside kubernetes cluster or run in development mode: %s", err)
	}

	// Create clients.
	apiCli := apiextensionsclient.NewForConfigOrDie(cfg)

	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, err
	}
	return client, apiCli, nil
}

func main() {
	k8sCli, apiCli, err := getKubernetesClients()
	if err != nil {
		glog.Fatalf("failed to get external secret clientset, err %v", err)
	}

	externalSecrets, err := k8sCli.Resource(externalSecretGRV).Namespace("").List(metav1.ListOptions{})
	if err != nil {
		glog.Errorf("failed to list all external secrets, err %v", err)
		return
	}

	//cleanup all existing externalsecrets
	for _, externalSecret := range externalSecrets.Items {
		// clean finalizer first
		name := externalSecret.GetName()
		glog.Infof("removing external secret %s", name)
		externalSecret.SetFinalizers(utils.Remove(externalSecret.GetFinalizers(), secretFinalizer))

		_, err := k8sCli.Resource(externalSecretGRV).Namespace(externalSecret.GetNamespace()).Update(&externalSecret, metav1.UpdateOptions{})
		if err != nil {
			glog.Fatalf("failed to update external secrets %s, err: %v", name, err)
		}

		err = k8sCli.Resource(externalSecretGRV).Namespace(externalSecret.GetNamespace()).Delete(name, &metav1.DeleteOptions{})
		if err != nil {
			glog.Fatalf("failed to delete external secrets %s, err: %v", name, err)
		}
	}

	//delete ExternalSecret crd
	err = apiCli.ApiextensionsV1beta1().CustomResourceDefinitions().Delete(crdName, &metav1.DeleteOptions{})
	if err != nil {
		glog.Fatalf("failed to delete external secrets crd, err: %v", err)
	}
	//ensure the crd cleanup
	retryNum := 0
	for ; retryNum < maxRetryNum; retryNum++ {
		_, err = apiCli.ApiextensionsV1beta1().CustomResourceDefinitions().Get(crdName, metav1.GetOptions{})
		if err != nil && apierrors.IsNotFound(err) {
			glog.Infof("finish cleanup external secrets")
			return
		}
		if retryNum < maxRetryNum {
			time.Sleep(2 * time.Second)
		}
	}
}

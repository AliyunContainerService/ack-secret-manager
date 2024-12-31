/*
Copyright 2023.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// SecretStoreSpec defines the desired state of SecretStore

// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:MaxProperties=1
type SecretStoreSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// maybe support more alibabacloud product
	KMS *KMSProvider `json:"KMS,omitempty"`
	OOS *OOSProvider `json:"OOS,omitempty"`
}

// +kubebuilder:validation:MaxProperties=1
type KMSProvider struct {
	// +optional
	KMS *KMSAuth `json:"KMSAuth,omitempty"`
	// +optional
	DedicatedKMS *DedicatedKMSAuth `json:"dedicatedKMSAuth,omitempty"`
}

type KMSAuth struct {
	AccessKey                *SecretRef `json:"accessKey,omitempty"`
	AccessKeySecret          *SecretRef `json:"accessKeySecret,omitempty"`
	RAMRoleARN               string     `json:"ramRoleARN,omitempty"`
	RAMRoleSessionName       string     `json:"ramRoleSessionName,omitempty"`
	OIDCProviderARN          string     `json:"oidcProviderARN,omitempty"`
	OIDCTokenFilePath        string     `json:"oidcTokenFilePath,omitempty"`
	RoleSessionExpiration    string     `json:"roleSessionExpiration,omitempty"`
	RemoteRAMRoleARN         string     `json:"remoteRamRoleARN,omitempty"`
	RemoteRAMRoleSessionName string     `json:"remoteRamRoleSessionName,omitempty"`
}

type OOSProvider struct {
	OOS *OOSAuth `json:"OOSAuth,omitempty"`
}

type OOSAuth struct {
	AccessKey                *SecretRef `json:"accessKey,omitempty"`
	AccessKeySecret          *SecretRef `json:"accessKeySecret,omitempty"`
	RAMRoleARN               string     `json:"ramRoleARN,omitempty"`
	RAMRoleSessionName       string     `json:"ramRoleSessionName,omitempty"`
	OIDCProviderARN          string     `json:"oidcProviderARN,omitempty"`
	OIDCTokenFilePath        string     `json:"oidcTokenFilePath,omitempty"`
	RoleSessionExpiration    string     `json:"roleSessionExpiration,omitempty"`
	RemoteRAMRoleARN         string     `json:"remoteRamRoleARN,omitempty"`
	RemoteRAMRoleSessionName string     `json:"remoteRamRoleSessionName,omitempty"`
}

type DedicatedKMSAuth struct {
	Protocol string `json:"protocol"`
	Endpoint string `json:"endpoint"`
	CA       string `json:"ca,omitempty"`
	// if ignoreSSL=true custom don't need fill the CA
	IgnoreSSL        bool       `json:"ignoreSSL,omitempty"`
	ClientKeyContent *SecretRef `json:"clientKeyContent"`
	Password         *SecretRef `json:"password"`
}

type SecretRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

// SecretStoreStatus defines the observed state of SecretStore
type SecretStoreStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:path=secretstores
//+kubebuilder:object:generate=true

// SecretStore is the Schema for the secretstores API
type SecretStore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SecretStoreSpec   `json:"spec,omitempty"`
	Status SecretStoreStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SecretStoreList contains a list of SecretStore
type SecretStoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SecretStore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SecretStore{}, &SecretStoreList{})
}

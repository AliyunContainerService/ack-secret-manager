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
	corev1 "k8s.io/api/core/v1"
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
}

type KMSAuth struct {
	// +optional
	AccessKey *SecretRef `json:"accessKey,omitempty"`
	// +optional
	AccessKeySecret          *SecretRef `json:"accessKeySecret,omitempty"`
	RAMRoleARN               string     `json:"ramRoleARN,omitempty"`
	RAMRoleSessionName       string     `json:"ramRoleSessionName,omitempty"`
	OIDCProviderARN          string     `json:"oidcProviderARN,omitempty"`
	OIDCTokenFilePath        string     `json:"oidcTokenFilePath,omitempty"`
	RoleSessionExpiration    string     `json:"roleSessionExpiration,omitempty"`
	RemoteRAMRoleARN         string     `json:"remoteRamRoleARN,omitempty"`
	RemoteRAMRoleSessionName string     `json:"remoteRamRoleSessionName,omitempty"`
	// +optional
	ServiceAccountRef *ServiceAccountRef `json:"serviceAccountRef,omitempty"`
}

type OOSProvider struct {
	OOS *OOSAuth `json:"OOSAuth,omitempty"`
}

type OOSAuth struct {
	// +optional
	AccessKey *SecretRef `json:"accessKey,omitempty"`
	// +optional
	AccessKeySecret          *SecretRef `json:"accessKeySecret,omitempty"`
	RAMRoleARN               string     `json:"ramRoleARN,omitempty"`
	RAMRoleSessionName       string     `json:"ramRoleSessionName,omitempty"`
	OIDCProviderARN          string     `json:"oidcProviderARN,omitempty"`
	OIDCTokenFilePath        string     `json:"oidcTokenFilePath,omitempty"`
	RoleSessionExpiration    string     `json:"roleSessionExpiration,omitempty"`
	RemoteRAMRoleARN         string     `json:"remoteRamRoleARN,omitempty"`
	RemoteRAMRoleSessionName string     `json:"remoteRamRoleSessionName,omitempty"`
	// +optional
	ServiceAccountRef *ServiceAccountRef `json:"serviceAccountRef,omitempty"`
}

// SecretRef references a Secret resource.
// For SecretStore, this Secret must be in the same namespace as the SecretStore.
// For ClusterSecretStore, the Namespace field specifies which namespace the Secret exists in.
type SecretRef struct {
	Name string `json:"name"`
	// +optional
	// Namespace of the Secret.
	// For SecretStore, this field is ignored and the namespace of the SecretStore is used.
	// For ClusterSecretStore, this field is required to specify the namespace where the Secret exists.
	Namespace string `json:"namespace,omitempty"`
	Key       string `json:"key"`
}

// ServiceAccountRef references a ServiceAccount resource.
// For SecretStore, it is in the same namespace as the SecretStore.
// For ClusterSecretStore, Namespace is required to specify the namespace of the ServiceAccount.
type ServiceAccountRef struct {
	// Name of the ServiceAccount
	Name string `json:"name"`
	// +optional
	// Namespace of the ServiceAccount.
	// For SecretStore, this field is ignored and the namespace of the SecretStore is used.
	// For ClusterSecretStore, this field is required to specify the namespace where the ServiceAccount exists.
	Namespace string `json:"namespace,omitempty"`
	// +optional
	Audiences []string `json:"audiences,omitempty"`
}

// SecretStoreConditionType represents the condition of the SecretStore.
type SecretStoreConditionType string

// These are valid conditions of a secret store.
const (
	// SecretStoreReady indicates that the store is ready and able to serve requests.
	SecretStoreReady SecretStoreConditionType = "Ready"

	ReasonInvalidStore          = "InvalidStoreConfiguration"
	ReasonInvalidProviderConfig = "InvalidProviderConfig"
	ReasonValidationFailed      = "ValidationFailed"
	ReasonValidationUnknown     = "ValidationUnknown"
	ReasonStoreValid            = "Valid"
	ReasonClientCreationFailed  = "ClientCreationFailed"
	StoreUnmaintained           = "StoreUnmaintained"
)

// SecretStoreStatusCondition contains condition information for a SecretStore.
type SecretStoreStatusCondition struct {
	Type   SecretStoreConditionType `json:"type"`
	Status corev1.ConditionStatus   `json:"status"`

	// +optional
	Reason string `json:"reason,omitempty"`

	// +optional
	Message string `json:"message,omitempty"`

	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// SecretStoreCapabilities defines the possible operations a SecretStore can do.
type SecretStoreCapabilities string

// These are the valid capabilities of a secret store.
const (
	// SecretStoreReadOnly indicates that the store can only read secrets.
	SecretStoreReadOnly SecretStoreCapabilities = "ReadOnly"
	// SecretStoreWriteOnly indicates that the store can only write secrets.
	SecretStoreWriteOnly SecretStoreCapabilities = "WriteOnly"
	// SecretStoreReadWrite indicates that the store can both read and write secrets.
	SecretStoreReadWrite SecretStoreCapabilities = "ReadWrite"
)

// SecretStoreStatus defines the observed state of SecretStore
type SecretStoreStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// +optional
	Conditions []SecretStoreStatusCondition `json:"conditions,omitempty"`
	// +optional
	Capabilities SecretStoreCapabilities `json:"capabilities,omitempty"`
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

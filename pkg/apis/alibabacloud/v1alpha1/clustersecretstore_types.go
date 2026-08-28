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

// ClusterSecretStoreSpec defines the desired state of ClusterSecretStore
// +kubebuilder:validation:MinProperties=1
type ClusterSecretStoreSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// maybe support more alibabacloud product
	KMS *KMSProvider `json:"KMS,omitempty"`
	OOS *OOSProvider `json:"OOS,omitempty"`

	// Used to constraint a ClusterSecretStore to specific namespaces. Relevant only to ClusterSecretStore
	// +optional
	Conditions []ClusterSecretStoreCondition `json:"conditions,omitempty"`
}

// ClusterSecretStoreCondition describes a condition by which to choose namespaces to process ExternalSecrets in
// for a ClusterSecretStore instance.
type ClusterSecretStoreCondition struct {
	// Choose namespace using a labelSelector
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// Choose namespaces by name
	// +optional
	// +kubebuilder:validation:items:MinLength:=1
	// +kubebuilder:validation:items:MaxLength:=63
	// +kubebuilder:validation:items:Pattern:=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Namespaces []string `json:"namespaces,omitempty"`

	// Choose namespaces by using regex matching
	// +optional
	NamespaceRegexes []string `json:"namespaceRegexes,omitempty"`
}

// ClusterSecretStoreStatus defines the observed state of ClusterSecretStore
type ClusterSecretStoreStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// +optional
	Conditions []SecretStoreStatusCondition `json:"conditions,omitempty"`
	// +optional
	Capabilities SecretStoreCapabilities `json:"capabilities,omitempty"`
	// ClientGeneration is a monotonic counter bumped every time the Store
	// controller successfully rebuilds the backend client and persists the
	// status. Consumers (the ExternalSecret reverse-watch predicates and the
	// client-freshness guard) use it to detect client rebuilds even when
	// metadata.generation is unchanged (e.g. trigger-annotation rotations).
	// +optional
	ClientGeneration int64 `json:"clientGeneration,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:storageversion

// ClusterSecretStore represents a secure external location for storing secrets, which can be referenced as part of `storeRef` fields.
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].reason`
// +kubebuilder:printcolumn:name="Capabilities",type=string,JSONPath=`.status.capabilities`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:subresource:status
// +kubebuilder:metadata:labels="external-secrets.io/component=controller"
// +kubebuilder:resource:scope=Cluster,categories={external-secrets},shortName=css
type ClusterSecretStore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSecretStoreSpec   `json:"spec,omitempty"`
	Status ClusterSecretStoreStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterSecretStoreList contains a list of ClusterSecretStore resources.
type ClusterSecretStoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterSecretStore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterSecretStore{}, &ClusterSecretStoreList{})
}

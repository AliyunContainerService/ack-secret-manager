package v1alpha1

import (
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ExternalSecretSpec defines the desired state of ExternalSecret
// +k8s:openapi-gen=true
type ExternalSecretSpec struct {
	// Important: Run "operator-sdk generate k8s" to regenerate code after modifying this file
	// Add custom validation using kubebuilder tags: https://book-v1.book.kubebuilder.io/beyond_basics/generating_crd.html
	Name        string       `json:"name"`
	Type        string       `json:"type,omitempty"`
	BackendType string       `json:"backendType"`
	RoleArn     string       `json:"roleArn,omitempty"`
	Data        []DataSource `json:"data,omitempty"`
	DataFrom    string       `json:"dataFrom,omitempty"`
	Template    v1.Secret    `json:"template,omitempty"`
}

type DataSource struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	VersionStage string `json:"versionStage,omitempty"`
	VersionId    string `json:"versionStage,omitempty"`
}

// SecretDefinitionStatus defines the observed state of SecretDefinition
type ExternalSecretStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ExternalSecret is the Schema for the externalsecrets API
// +k8s:openapi-gen=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=externalsecrets,scope=Namespaced
type ExternalSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ExternalSecretSpec   `json:"spec,omitempty"`
	Status ExternalSecretStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ExternalSecretList contains a list of ExternalSecret
type ExternalSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ExternalSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ExternalSecret{}, &ExternalSecretList{})
}

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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ExternalSecretSpec defines the desired state of ExternalSecret
// +k8s:openapi-gen=true
type ExternalSecretSpec struct {
	Provider    string        `json:"provider,omitempty"`
	Data        []DataSource  `json:"data,omitempty"`
	DataProcess []DataProcess `json:"dataProcess,omitempty"`
	Type        string        `json:"type,omitempty"`
	// Target defines how the secret is created in the cluster
	Target *ExternalSecretTarget `json:"target,omitempty"`
	// The time in which the controller should reconcile its objects and recheck namespaces for labels.
	RotationInterval *metav1.Duration `json:"rotationInterval,omitempty"`
}

// ExternalSecretTarget defines the target secret
type ExternalSecretTarget struct {
	// Name defines the name of the secret resource to be managed.
	// If not set, the name will be auto-generated based on the ExternalSecret name.
	// +optional
	Name string `json:"name,omitempty"`

	// Template defines a template that can be used to generate or transform the secret data
	// +optional
	Template *ExternalSecretTemplate `json:"template,omitempty"`
}

// ExternalSecretTemplate defines the template for generating secret data
type ExternalSecretTemplate struct {
	// Data defines the target secret's data field.
	// +optional
	Data map[string]string `json:"data,omitempty"`

	// TemplateFrom specifies sources for templates.
	// +optional
	TemplateFrom []TemplateFrom `json:"templateFrom,omitempty"`

	// Metadata defines the target secret's metadata fields.
	// +optional
	Metadata *ExternalSecretTemplateMetadata `json:"metadata,omitempty"`

	// Type defines the target secret's type field.
	// +optional
	Type corev1.SecretType `json:"type,omitempty"`

	// MergePolicy defines how template results should be merged with the original data.
	// Defaults to "Replace"
	// +optional
	// +kubebuilder:default="Replace"
	MergePolicy TemplateMergePolicy `json:"mergePolicy,omitempty"`
}

// ExternalSecretTemplateMetadata defines the metadata for the generated secret
type ExternalSecretTemplateMetadata struct {
	// Annotations to apply to the target secret.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
	// Labels to apply to the target secret.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// TemplateMergePolicy defines how template results should be merged with the original data.
// +kubebuilder:validation:Enum=Replace;Merge
type TemplateMergePolicy string

const (
	// MergePolicyReplace replaces the entire template content during merge operations.
	MergePolicyReplace TemplateMergePolicy = "Replace"

	// MergePolicyMerge merges the template content with existing values.
	MergePolicyMerge TemplateMergePolicy = "Merge"
)

// TemplateTarget defines the target field where the template result will be stored.
// +kubebuilder:validation:Enum=Data;Annotations;Labels
type TemplateTarget string

const (
	// TemplateTargetData stores template results in the data field of the secret.
	TemplateTargetData TemplateTarget = "Data"

	// TemplateTargetAnnotations stores template results in the annotations field of the secret.
	TemplateTargetAnnotations TemplateTarget = "Annotations"

	// TemplateTargetLabels stores template results in the labels field of the secret.
	TemplateTargetLabels TemplateTarget = "Labels"
)

// TemplateFrom specifies a source for templates.
// Each item in the list can either reference a ConfigMap or a Secret resource.
type TemplateFrom struct {
	ConfigMap *TemplateRef `json:"configMap,omitempty"`
	Secret    *TemplateRef `json:"secret,omitempty"`

	// Target specifies where to place the template result.
	// For Secret resources, common values are: "Data", "Annotations", "Labels".
	// +optional
	// +kubebuilder:default="Data"
	Target TemplateTarget `json:"target,omitempty"`

	// +optional
	Literal *string `json:"literal,omitempty"`
}

// TemplateRef specifies a reference to either a ConfigMap or a Secret resource.
type TemplateRef struct {
	// The name of the ConfigMap/Secret resource
	// +kubebuilder:validation:MinLength:=1
	// +kubebuilder:validation:MaxLength:=253
	// +kubebuilder:validation:Pattern:=^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$
	Name string `json:"name"`

	// A list of keys in the ConfigMap/Secret to use as templates for Secret data
	Items []TemplateRefItem `json:"items"`
}

// TemplateRefItem specifies a key in the ConfigMap/Secret to use as a template for Secret data.
type TemplateRefItem struct {
	// A key in the ConfigMap/Secret
	// +kubebuilder:validation:MinLength:=1
	// +kubebuilder:validation:MaxLength:=253
	// +kubebuilder:validation:Pattern:=^[-._a-zA-Z0-9]+$
	Key string `json:"key"`

	// +kubebuilder:default="Values"
	TemplateAs TemplateScope `json:"templateAs,omitempty"`
}

// TemplateScope specifies how the template keys should be interpreted.
// +kubebuilder:validation:Enum=Values;KeysAndValues
type TemplateScope string

// These are used to define the scope of templates.
const (
	TemplateScopeValues        TemplateScope = "Values"
	TemplateScopeKeysAndValues TemplateScope = "KeysAndValues"
)

type DataSource struct {
	SecretStoreRef *SecretStoreRef `json:"secretStoreRef,omitempty"`
	Key            string          `json:"key"`
	Name           string          `json:"name,omitempty"`
	VersionStage   string          `json:"versionStage,omitempty"`
	VersionId      string          `json:"versionId,omitempty"`
	//Optional array to specify what json key value pairs to extract from a secret and mount as individual secrets
	JMESPath    []JMESPathObject `json:"jmesPath,omitempty"`
	KmsEndpoint string           `json:"kmsEndpoint,omitempty"`
}

type SecretStoreRef struct {
	Name string `json:"name"`
	// Kind of the SecretStore resource (SecretStore or ClusterSecretStore)
	// Defaults to SecretStore
	// +optional
	// +kubebuilder:validation:Enum=SecretStore;ClusterSecretStore
	Kind string `json:"kind,omitempty"`
	// Namespace of the referenced SecretStore. Cross-namespace references are
	// controlled by the enable-cross-namespace-auth-ref switch.
	// Optional; defaults to the namespace of the ExternalSecret when empty.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

type DataProcess struct {
	Extract *DataSource `json:"extract,omitempty"`
	// +optional
	ReplaceKey []ReplaceRule `json:"replaceRule,omitempty"`
}

type ReplaceRule struct {
	Target string `json:"target"`
	Source string `json:"source"`
}

type DataSyncResult struct {
	ExternalSecretKey   string      `json:"ExternalSecretKey,omitempty"`
	Status              string      `json:"status,omitempty"`
	Reason              string      `json:"reason,omitempty"`
	SynchronizationTime metav1.Time `json:"synchronizationTime,omitempty"`
}

// ExternalSecretStatus defines the observed state of ExternalSecret
type ExternalSecretStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	DataSyncResults []DataSyncResult `json:"dataSyncResults,omitempty"`
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

// An individual json key value pair to mount
type JMESPathObject struct {
	//JMES path to use for retrieval
	Path string `json:"path"`

	//File name in which to store the secret in.
	ObjectAlias string `json:"objectAlias"`
}

func init() {
	SchemeBuilder.Register(&ExternalSecret{}, &ExternalSecretList{})
}

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LatitudeMachineTemplateSpec struct {
	Template LatitudeMachineTemplateResource `json:"template"`
}

type LatitudeMachineTemplateResource struct {
	// Standard object's metadata.
	// +optional
	ObjectMeta `json:"metadata,omitempty"`
	Spec       LatitudeMachineSpec `json:"spec,omitempty"`
}

// ObjectMeta is metadata that all persisted resources must have, which includes all objects
// users must create. This is a copy of customizable fields from metav1.ObjectMeta.
type ObjectMeta struct {
	// Map of string keys and values that can be used to organize and categorize
	// (scope and select) objects. May match selectors of replication controllers
	// and services.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations is an unstructured key value map stored with a resource that may be
	// set by external tools to store and retrieve arbitrary metadata.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=latitudemachinetemplates,scope=Namespaced,shortName=latmt
type LatitudeMachineTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec LatitudeMachineTemplateSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true
type LatitudeMachineTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LatitudeMachineTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LatitudeMachineTemplate{}, &LatitudeMachineTemplateList{})
}

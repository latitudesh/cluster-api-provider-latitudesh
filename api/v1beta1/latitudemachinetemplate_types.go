package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LatitudeMachineTemplateSpec struct {
	Template LatitudeMachineTemplateResource `json:"template"`
}

type LatitudeMachineTemplateResource struct {
	Spec LatitudeMachineSpec `json:"spec,omitempty"`
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

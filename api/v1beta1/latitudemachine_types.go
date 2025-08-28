package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LatitudeMachineSpec struct {
	// +kubebuilder:validation:MinLength=1
	OperatingSystem string `json:"operatingSystem"`

	// +kubebuilder:validation:MinLength=1
	Plan string `json:"plan"`
}

type LatitudeMachineStatus struct {
	// +optional
	Ready bool `json:"ready,omitempty"`

	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=latitudemachines,scope=Namespaced,shortName=latm
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="ProviderID",type=string,JSONPath=".status.providerID"
type LatitudeMachine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LatitudeMachineSpec   `json:"spec,omitempty"`
	Status LatitudeMachineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type LatitudeMachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LatitudeMachine `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LatitudeMachine{}, &LatitudeMachineList{})
}

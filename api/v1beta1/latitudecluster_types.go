package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// APIEndpoint local (MVP)
type APIEndpoint struct {
	Host string `json:"host,omitempty"`
	Port int32  `json:"port,omitempty"`
}

type ProjectRef struct {
	ProjectID string `json:"projectID,omitempty"`
}

type LatitudeClusterSpec struct {
	Location   string      `json:"location,omitempty"`
	ProjectRef *ProjectRef `json:"projectRef,omitempty"`
}

type LatitudeClusterStatus struct {
	Ready                bool        `json:"ready,omitempty"`
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=latitudeclusters,scope=Namespaced,shortName=latc
type LatitudeCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   LatitudeClusterSpec   `json:"spec,omitempty"`
	Status LatitudeClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type LatitudeClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []LatitudeCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LatitudeCluster{}, &LatitudeClusterList{})
}

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
	// ProjectID is the Latitude.sh project ID
	ProjectID string `json:"projectID,omitempty"`
}

// VLANConfig defines configuration for VLAN networking
type VLANConfig struct {
	// Subnet for the VLAN (e.g., "10.8.0.0/24")
	// +kubebuilder:validation:Required
	Subnet string `json:"subnet"`

	// VID (VLAN ID number). If not specified, Latitude will assign one
	// +optional
	VID *int `json:"vid,omitempty"`

	// ExistingVLANID to use instead of creating a new one
	// If specified, the controller will not create or delete the VLAN
	// +optional
	ExistingVLANID *string `json:"existingVLANID,omitempty"`

	// NetworkInterface to use for VLAN (e.g., "eno2", "enp1s0f1")
	// If not specified, will attempt to auto-detect
	// +optional
	NetworkInterface string `json:"networkInterface,omitempty"`
}

type LatitudeClusterSpec struct {
	// Location is the Latitude.sh region/site where resources will be created
	Location string `json:"location,omitempty"`

	// ProjectRef is the Latitude.sh project reference
	ProjectRef *ProjectRef `json:"projectRef,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// VLANConfig defines optional VLAN networking configuration
	// When specified, the controller will create/manage a VLAN for the cluster
	// +optional
	VLANConfig *VLANConfig `json:"vlanConfig,omitempty"`
}

type LatitudeClusterStatus struct {
	// Ready indicates that the cluster infrastructure is ready
	Ready bool `json:"ready,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// VLANID is the ID of the VLAN created or used by this cluster
	// +optional
	VLANID *string `json:"vlanID,omitempty"`

	// Conditions defines current service state of the LatitudeCluster
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=latitudeclusters,scope=Namespaced,shortName=latc
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready"
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

// GetConditions returns the set of conditions for this object
func (c *LatitudeCluster) GetConditions() []metav1.Condition {
	return c.Status.Conditions
}

// SetConditions sets the conditions for this object
func (c *LatitudeCluster) SetConditions(conditions []metav1.Condition) {
	c.Status.Conditions = conditions

}

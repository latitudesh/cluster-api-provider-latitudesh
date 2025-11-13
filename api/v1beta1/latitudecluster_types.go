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

// LoadBalancerSpec defines the load balancer configuration for HA control planes
type LoadBalancerSpec struct {
	// Enabled indicates whether to provision a HAProxy load balancer
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Plan is the Latitude.sh server plan for the load balancer
	// Defaults to "c2-small-x86" if not specified
	// +optional
	Plan string `json:"plan,omitempty"`

	// OperatingSystem is the OS for the load balancer server
	// Defaults to "ubuntu_24_04_x64_lts" if not specified
	// +optional
	OperatingSystem string `json:"operatingSystem,omitempty"`

	// Port is the port where HAProxy will listen for Kubernetes API traffic
	// Defaults to 6443
	// +optional
	// +kubebuilder:default=6443
	Port int32 `json:"port,omitempty"`

	// StatsPort is the port for HAProxy stats interface
	// Defaults to 9000. Set to 0 to disable.
	// +optional
	// +kubebuilder:default=9000
	StatsPort int32 `json:"statsPort,omitempty"`
}

type LatitudeClusterSpec struct {
	// Location is the Latitude.sh region/site where resources will be created
	Location string `json:"location,omitempty"`

	// ProjectRef is the Latitude.sh project reference
	ProjectRef *ProjectRef `json:"projectRef,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// LoadBalancer defines the load balancer configuration for HA control planes
	// +optional
	LoadBalancer *LoadBalancerSpec `json:"loadBalancer,omitempty"`
}

// LoadBalancerStatus defines the observed state of the load balancer
type LoadBalancerStatus struct {
	// ServerID is the Latitude.sh server ID of the load balancer
	// +optional
	ServerID string `json:"serverID,omitempty"`

	// InternalIP is the internal IP address of the load balancer
	// +optional
	InternalIP string `json:"internalIP,omitempty"`

	// PublicIP is the public IP address of the load balancer
	// +optional
	PublicIP string `json:"publicIP,omitempty"`

	// Ready indicates whether the load balancer is ready
	// +optional
	Ready bool `json:"ready,omitempty"`
}

type LatitudeClusterStatus struct {
	// Ready indicates that the cluster infrastructure is ready
	Ready bool `json:"ready,omitempty"`

	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane
	// +optional
	ControlPlaneEndpoint APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// LoadBalancer contains the status of the load balancer if enabled
	// +optional
	LoadBalancer *LoadBalancerStatus `json:"loadBalancer,omitempty"`

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

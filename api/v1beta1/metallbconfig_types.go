/*
Copyright 2025.

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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MetalLBConfigSpec defines the desired state of MetalLBConfig
type MetalLBConfigSpec struct {
	// ClusterRef references the LatitudeCluster this MetalLB configuration serves
	ClusterRef MetalLBClusterRef `json:"clusterRef"`

	// IPAddressPool defines the pool of IP addresses that MetalLB can allocate
	IPAddressPool IPAddressPool `json:"ipAddressPool"`

	// Version is the MetalLB version to install (default: v0.14.3)
	// +kubebuilder:default="v0.14.3"
	// +optional
	Version string `json:"version,omitempty"`

	// ControlPlaneEndpoint configures if a LoadBalancer Service should be created
	// for the Kubernetes API server
	// +optional
	ControlPlaneEndpoint *ControlPlaneEndpointConfig `json:"controlPlaneEndpoint,omitempty"`

	// L2Advertisements defines Layer 2 advertisement configuration
	// +optional
	L2Advertisements []L2Advertisement `json:"l2Advertisements,omitempty"`
}

// MetalLBClusterRef references a LatitudeCluster
type MetalLBClusterRef struct {
	// Name is the name of the LatitudeCluster
	Name string `json:"name"`

	// Namespace is the namespace of the LatitudeCluster (defaults to the same namespace)
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// IPAddressPool defines a pool of IP addresses
type IPAddressPool struct {
	// Name is the name of the IP address pool
	Name string `json:"name"`

	// Addresses is a list of IP address ranges in CIDR notation or range format
	// Examples: "192.168.1.240/28", "192.168.1.240-192.168.1.250"
	Addresses []string `json:"addresses"`

	// AutoAssign controls whether IP addresses are automatically assigned from this pool
	// +kubebuilder:default=true
	// +optional
	AutoAssign *bool `json:"autoAssign,omitempty"`

	// AvoidBuggyIPs prevents allocating IPs ending in .0 or .255
	// +kubebuilder:default=false
	// +optional
	AvoidBuggyIPs *bool `json:"avoidBuggyIPs,omitempty"`
}

// L2Advertisement defines how to advertise IP addresses via Layer 2
type L2Advertisement struct {
	// Name is the name of the L2 advertisement
	// +optional
	Name string `json:"name,omitempty"`

	// IPAddressPools is a list of IP address pool names to advertise
	// +optional
	IPAddressPools []string `json:"ipAddressPools,omitempty"`

	// Interfaces is a list of network interfaces to advertise on
	// If empty, all interfaces are used
	// +optional
	Interfaces []string `json:"interfaces,omitempty"`

	// NodeSelectors allows limiting which nodes can advertise IPs
	// +optional
	NodeSelectors []metav1.LabelSelector `json:"nodeSelectors,omitempty"`
}

// ControlPlaneEndpointConfig configures LoadBalancer for Kubernetes API
type ControlPlaneEndpointConfig struct {
	// Enabled controls whether to create a LoadBalancer Service for the API server
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Port is the port for the Kubernetes API server (default: 6443)
	// +kubebuilder:default=6443
	// +optional
	Port int32 `json:"port,omitempty"`

	// IPAddress is the specific IP to assign (must be within the pool)
	// If not specified, an IP is automatically allocated
	// +optional
	IPAddress string `json:"ipAddress,omitempty"`
}

// MetalLBConfigStatus defines the observed state of MetalLBConfig
type MetalLBConfigStatus struct {
	// Ready indicates that MetalLB is installed and configured
	Ready bool `json:"ready,omitempty"`

	// Installed indicates that MetalLB has been installed in the workload cluster
	Installed bool `json:"installed,omitempty"`

	// Version is the installed MetalLB version
	// +optional
	Version string `json:"version,omitempty"`

	// ControlPlaneEndpoint is the LoadBalancer IP assigned to the API server
	// +optional
	ControlPlaneEndpoint string `json:"controlPlaneEndpoint,omitempty"`

	// AllocatedIPs tracks IPs allocated from the pool
	// +optional
	AllocatedIPs []string `json:"allocatedIPs,omitempty"`

	// Conditions defines current service state of the MetalLBConfig
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=metallbconfigs,scope=Namespaced,shortName=mlb
// +kubebuilder:printcolumn:name="Ready",type="boolean",JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="Installed",type="boolean",JSONPath=".status.installed"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".status.controlPlaneEndpoint"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".status.version"
// MetalLBConfig is the Schema for the metallbconfigs API
type MetalLBConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MetalLBConfigSpec   `json:"spec,omitempty"`
	Status MetalLBConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// MetalLBConfigList contains a list of MetalLBConfig
type MetalLBConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []MetalLBConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&MetalLBConfig{}, &MetalLBConfigList{})
}

// GetConditions returns the set of conditions for this object
func (m *MetalLBConfig) GetConditions() []metav1.Condition {
	return m.Status.Conditions
}

// SetConditions sets the conditions for this object
func (m *MetalLBConfig) SetConditions(conditions []metav1.Condition) {
	m.Status.Conditions = conditions
}

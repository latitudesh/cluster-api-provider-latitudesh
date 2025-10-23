package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

type LatitudeMachineSpec struct {
	// ProviderID is the unique identifier as specified by the cloud provider
	// +optional
	ProviderID *string `json:"providerID,omitempty"`

	// +kubebuilder:validation:MinLength=1
	// OperatingSystem is the operating system slug for the server
	OperatingSystem string `json:"operatingSystem"`

	// +kubebuilder:validation:MinLength=1
	// Plan is the server plan slug
	Plan string `json:"plan"`

	// SSHKeys is a list of SSH key IDs to be installed on the server
	SSHKeys []string `json:"sshKeys,omitempty"`
}

type LatitudeMachineStatus struct {
	// Ready indicates that the machine is ready
	// +optional
	Ready bool `json:"ready,omitempty"`

	// ProviderID is the provider ID of the server
	// +optional
	ProviderID string `json:"providerID,omitempty"`

	// ServerID is the Latitude.sh server ID
	// +optional
	ServerID string `json:"serverID,omitempty"`

	// UserDataID is the Latitude.sh user data ID
	// +optional
	UserDataID string `json:"userDataID,omitempty"`

	// Addresses contains the addresses associated with the machine
	// +optional
	Addresses []clusterv1.MachineAddress `json:"addresses,omitempty"`

	// Conditions defines current service state of the LatitudeMachine
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=latitudemachines,scope=Namespaced,shortName=latm
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.ready"
// +kubebuilder:printcolumn:name="ProviderID",type=string,JSONPath=".status.providerID"
// +kubebuilder:printcolumn:name="ServerID",type=string,JSONPath=".status.serverID"
// +kubebuilder:printcolumn:name="UserDataID",type=string,JSONPath=".status.userDataID"
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

// GetConditions returns the set of conditions for this object
func (m *LatitudeMachine) GetConditions() []metav1.Condition {
	return m.Status.Conditions
}

// SetConditions sets the conditions for this object
func (m *LatitudeMachine) SetConditions(conditions []metav1.Condition) {
	m.Status.Conditions = conditions
}

// Condition types and reasons
const (
	// InstanceReadyCondition reports on current status of the instance
	InstanceReadyCondition = "InstanceReady"

	// InstanceProvisionFailedReason used when the instance couldn't be created
	InstanceProvisionFailedReason = "InstanceProvisionFailed"

	// InstanceNotReadyReason used when the instance is not ready
	InstanceNotReadyReason = "InstanceNotReady"

	// InstanceDeletionFailedReason used when the instance couldn't be deleted
	InstanceDeletionFailedReason = "InstanceDeletionFailed"

	// WaitingForClusterInfrastructureReason used when machine is waiting for cluster infrastructure
	WaitingForClusterInfrastructureReason = "WaitingForClusterInfrastructure"

	// WaitingForBootstrapDataReason used when machine is waiting for bootstrap data
	WaitingForBootstrapDataReason = "WaitingForBootstrapData"
)

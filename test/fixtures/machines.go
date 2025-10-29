package fixtures

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// NewLatitudeMachine returns a test LatitudeMachine
func NewLatitudeMachine(name, namespace, plan, os string) *infrav1.LatitudeMachine {
	return &infrav1.LatitudeMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: infrav1.LatitudeMachineSpec{
			Plan:            plan,
			OperatingSystem: os,
		},
	}
}

// NewMachine returns a test CAPI Machine
func NewMachine(name, namespace, clusterName string) *clusterv1.Machine {
	return &clusterv1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				clusterv1.ClusterNameLabel: clusterName,
			},
		},
	}
}

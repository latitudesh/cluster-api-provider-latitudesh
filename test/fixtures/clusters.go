package fixtures

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// NewLatitudeCluster returns a test LatitudeCluster
func NewLatitudeCluster(name, namespace, location, projectID string) *infrav1.LatitudeCluster {
	return &infrav1.LatitudeCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: infrav1.LatitudeClusterSpec{
			Location: location,
			ProjectRef: &infrav1.ProjectRef{
				ProjectID: projectID,
			},
		},
	}
}

// NewCluster returns a test CAPI Cluster
func NewCluster(name, namespace string) *clusterv1.Cluster {
	return &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: clusterv1.ClusterSpec{
			ClusterNetwork: &clusterv1.ClusterNetwork{
				Pods: &clusterv1.NetworkRanges{
					CIDRBlocks: []string{"192.168.0.0/16"},
				},
			},
		},
	}
}

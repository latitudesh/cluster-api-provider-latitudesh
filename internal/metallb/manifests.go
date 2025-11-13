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

package metallb

import (
	"context"
	"fmt"

	infrav1 "github.com/latitudesh/cluster-api-provider-latitudesh/api/v1beta1"
	"k8s.io/client-go/kubernetes"
)

const (
	// MetalLB namespace
	MetalLBNamespace = "metallb-system"

	// Default MetalLB version
	DefaultMetalLBVersion = "v0.14.3"
)

// GetMetalLBManifestURL returns the URL for MetalLB manifest
func GetMetalLBManifestURL(version string) string {
	if version == "" {
		version = DefaultMetalLBVersion
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/metallb/metallb/%s/config/manifests/metallb-native.yaml", version)
}

// InstallMetalLB installs MetalLB in the workload cluster
func InstallMetalLB(ctx context.Context, clientset *kubernetes.Clientset, version string) error {
	// For now, we'll return nil and assume MetalLB is installed via kubectl apply
	// In a production implementation, we would:
	// 1. Download the manifest from GetMetalLBManifestURL
	// 2. Parse the YAML
	// 3. Apply each resource to the cluster using the clientset
	// 4. Wait for MetalLB pods to be ready

	// TODO: Implement actual installation logic
	// For now, users should manually install MetalLB with:
	// kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.14.3/config/manifests/metallb-native.yaml

	return nil
}

// GenerateIPAddressPoolYAML generates IPAddressPool CR YAML
func GenerateIPAddressPoolYAML(pool infrav1.IPAddressPool) string {
	autoAssign := true
	if pool.AutoAssign != nil {
		autoAssign = *pool.AutoAssign
	}

	avoidBuggyIPs := false
	if pool.AvoidBuggyIPs != nil {
		avoidBuggyIPs = *pool.AvoidBuggyIPs
	}

	addresses := ""
	for i, addr := range pool.Addresses {
		if i > 0 {
			addresses += "\n  "
		}
		addresses += fmt.Sprintf("- %s", addr)
	}

	return fmt.Sprintf(`apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: %s
  namespace: %s
spec:
  addresses:
  %s
  autoAssign: %t
  avoidBuggyIPs: %t
`, pool.Name, MetalLBNamespace, addresses, autoAssign, avoidBuggyIPs)
}

// GenerateL2AdvertisementYAML generates L2Advertisement CR YAML
func GenerateL2AdvertisementYAML(l2ad infrav1.L2Advertisement) string {
	name := l2ad.Name
	if name == "" {
		name = "default-l2-advertisement"
	}

	yaml := fmt.Sprintf(`apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: %s
  namespace: %s
spec:
`, name, MetalLBNamespace)

	if len(l2ad.IPAddressPools) > 0 {
		yaml += "  ipAddressPools:\n"
		for _, pool := range l2ad.IPAddressPools {
			yaml += fmt.Sprintf("  - %s\n", pool)
		}
	}

	if len(l2ad.Interfaces) > 0 {
		yaml += "  interfaces:\n"
		for _, iface := range l2ad.Interfaces {
			yaml += fmt.Sprintf("  - %s\n", iface)
		}
	}

	if len(l2ad.NodeSelectors) > 0 {
		yaml += "  nodeSelectors:\n"
		for _, selector := range l2ad.NodeSelectors {
			yaml += "  - matchLabels:\n"
			for k, v := range selector.MatchLabels {
				yaml += fmt.Sprintf("      %s: %s\n", k, v)
			}
		}
	}

	return yaml
}

// GenerateControlPlaneServiceYAML generates a LoadBalancer Service for the Kubernetes API
func GenerateControlPlaneServiceYAML(mlbConfig *infrav1.MetalLBConfig) string {
	port := int32(6443)
	if mlbConfig.Spec.ControlPlaneEndpoint != nil && mlbConfig.Spec.ControlPlaneEndpoint.Port != 0 {
		port = mlbConfig.Spec.ControlPlaneEndpoint.Port
	}

	yaml := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: kubernetes-api-lb
  namespace: default
spec:
  type: LoadBalancer
  ports:
  - port: %d
    targetPort: %d
    protocol: TCP
    name: https
  selector:
    component: kube-apiserver
    tier: control-plane
`, port, port)

	// Add specific IP if requested
	if mlbConfig.Spec.ControlPlaneEndpoint != nil && mlbConfig.Spec.ControlPlaneEndpoint.IPAddress != "" {
		yaml += fmt.Sprintf("  loadBalancerIP: %s\n", mlbConfig.Spec.ControlPlaneEndpoint.IPAddress)
	}

	// Add annotations for MetalLB
	yaml += "  annotations:\n"
	yaml += fmt.Sprintf("    metallb.universe.tf/address-pool: %s\n", mlbConfig.Spec.IPAddressPool.Name)

	return yaml
}

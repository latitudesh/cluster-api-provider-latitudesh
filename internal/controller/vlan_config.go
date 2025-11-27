/*
Copyright 2025 Latitude.sh.

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

package controllers

import (
	"fmt"
	"net"
	"strings"
)

// VLANNetplanConfig holds configuration for generating VLAN netplan
type VLANNetplanConfig struct {
	VID       int    // VLAN ID number (e.g., 2011)
	Subnet    string // Subnet CIDR (e.g., "10.10.0.0/24")
	IPAddress string // IP address for this machine (e.g., "10.10.0.10")
	Interface string // Parent interface (defaults to eno2)
}

// GenerateVLANNetplanConfig generates the netplan configuration for a VLAN interface
// Format per Latitude.sh docs: https://www.latitude.sh/docs/networking/private-networks
func GenerateVLANNetplanConfig(cfg VLANNetplanConfig) string {
	if cfg.Interface == "" {
		cfg.Interface = "eno2"
	}

	// Calculate the mask from subnet
	_, ipNet, err := net.ParseCIDR(cfg.Subnet)
	maskSize := 24 // default
	if err == nil {
		maskSize, _ = ipNet.Mask.Size()
	}

	// Generate netplan VLAN config per Latitude docs format
	return fmt.Sprintf(`  vlans:
    vlan.%d:
      id: %d
      link: %s
      addresses: [%s/%d]
`, cfg.VID, cfg.VID, cfg.Interface, cfg.IPAddress, maskSize)
}

// InjectVLANConfigIntoCloudInit injects VLAN netplan configuration into cloud-init userdata
// It appends the VLAN config to /etc/netplan/50-cloud-init.yaml and applies netplan
func InjectVLANConfigIntoCloudInit(userData string, cfg VLANNetplanConfig) string {
	if cfg.VID == 0 || cfg.IPAddress == "" || cfg.Subnet == "" {
		return userData
	}

	if cfg.Interface == "" {
		cfg.Interface = "eno2"
	}

	// Calculate the mask from subnet
	_, ipNet, err := net.ParseCIDR(cfg.Subnet)
	maskSize := 24
	if err == nil {
		maskSize, _ = ipNet.Mask.Size()
	}

	// Script to detect private interface and configure VLAN
	// The private interface has IP in 100.64.0.0/10 or 10.0.0.0/8 range
	// We detect it dynamically since interface names vary (eno2, enp1s0f1, etc.)
	// Indentation: vlans (2 spaces), vlan.X (4 spaces), properties (6 spaces)
	// Also configure node-ip for RKE2/kubelet so MetalLB can work
	vlanCmd := fmt.Sprintf(`PRIV_IFACE=$(ip -4 addr show | grep -E "inet (100\.|10\.)" | grep -v "10\.10\." | awk '{print $NF}' | head -1); if [ -z "$PRIV_IFACE" ]; then PRIV_IFACE="%s"; fi; printf "\n  vlans:\n    vlan.%d:\n      id: %d\n      link: $PRIV_IFACE\n      addresses: [%s/%d]\n" >> /etc/netplan/50-cloud-init.yaml && netplan apply && mkdir -p /etc/rancher/rke2/config.yaml.d && echo "node-ip: %s" > /etc/rancher/rke2/config.yaml.d/99-node-ip.yaml`,
		cfg.Interface, cfg.VID, cfg.VID, cfg.IPAddress, maskSize, cfg.IPAddress)

	// Check if userData already has runcmd section
	if strings.Contains(userData, "runcmd:") {
		// Insert after runcmd: line (so it runs first)
		idx := strings.Index(userData, "runcmd:")
		newlineIdx := strings.Index(userData[idx:], "\n")
		if newlineIdx != -1 {
			insertPoint := idx + newlineIdx + 1
			entry := fmt.Sprintf("  - '%s'\n", escapeForYAML(vlanCmd))
			return userData[:insertPoint] + entry + userData[insertPoint:]
		}
	}

	// No runcmd section, append one
	return userData + fmt.Sprintf("\nruncmd:\n  - '%s'\n", escapeForYAML(vlanCmd))
}

// escapeForYAML escapes single quotes for YAML single-quoted strings
func escapeForYAML(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// CalculateVLANIPAddress calculates an IP address for a machine within the VLAN subnet
// machineIndex is used to assign unique IPs (0 = .10, 1 = .11, etc.)
func CalculateVLANIPAddress(subnet string, machineIndex int) (string, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("invalid subnet CIDR: %w", err)
	}

	// Start from .10 to leave room for infrastructure IPs
	baseOffset := 10 + machineIndex

	ip := ipNet.IP.To4()
	if ip == nil {
		return "", fmt.Errorf("only IPv4 subnets are supported")
	}

	// Calculate the new IP
	newIP := make(net.IP, 4)
	copy(newIP, ip)

	// Add offset to the last octet (simple approach for /24 networks)
	// For larger networks, this would need more sophisticated logic
	newIP[3] = byte(baseOffset)

	return newIP.String(), nil
}

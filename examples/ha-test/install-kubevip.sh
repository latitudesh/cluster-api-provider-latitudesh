#!/bin/bash
# install-kubevip.sh
# Script to install kube-vip on the first RKE2 control plane node
#
# Usage:
#   1. SSH into the first control plane node
#   2. Copy this script to the node
#   3. Run: sudo bash install-kubevip.sh <VIP_ADDRESS>
#
# Example:
#   sudo bash install-kubevip.sh 100.64.208.100

set -e

# Check if VIP is provided
if [ -z "$1" ]; then
    echo "Error: VIP address is required"
    echo "Usage: $0 <VIP_ADDRESS>"
    echo "Example: $0 100.64.208.100"
    exit 1
fi

VIP_ADDRESS="$1"
INTERFACE="${2:-ens3}"  # Default to ens3, can be overridden

echo "=== kube-vip Installation Script ==="
echo "VIP Address: $VIP_ADDRESS"
echo "Network Interface: $INTERFACE"
echo "===================================="

# Detect the correct network interface if default doesn't exist
if ! ip link show "$INTERFACE" &>/dev/null; then
    echo "Warning: Interface $INTERFACE not found"
    echo "Available interfaces:"
    ip -o link show | awk -F': ' '{print $2}'

    # Try common alternatives
    for iface in eno2 eth0 enp0s3; do
        if ip link show "$iface" &>/dev/null; then
            INTERFACE="$iface"
            echo "Using interface: $INTERFACE"
            break
        fi
    done
fi

# Verify RKE2 is installed
if [ ! -f /etc/rancher/rke2/rke2.yaml ]; then
    echo "Error: RKE2 kubeconfig not found at /etc/rancher/rke2/rke2.yaml"
    echo "Please ensure RKE2 is installed and running"
    exit 1
fi

# Create manifests directory if it doesn't exist
mkdir -p /var/lib/rancher/rke2/server/manifests

# Create kube-vip static pod manifest
echo "Creating kube-vip manifest..."
cat > /var/lib/rancher/rke2/server/manifests/kube-vip.yaml <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: kube-vip
  namespace: kube-system
spec:
  containers:
  - name: kube-vip
    image: ghcr.io/kube-vip/kube-vip:v0.8.0
    imagePullPolicy: IfNotPresent
    args:
    - manager
    env:
    - name: vip_arp
      value: "true"
    - name: port
      value: "6443"
    - name: vip_interface
      value: "$INTERFACE"
    - name: vip_cidr
      value: "32"
    - name: cp_enable
      value: "true"
    - name: cp_namespace
      value: kube-system
    - name: vip_ddns
      value: "false"
    - name: svc_enable
      value: "false"
    - name: vip_leaderelection
      value: "true"
    - name: vip_leaseduration
      value: "15"
    - name: vip_renewdeadline
      value: "10"
    - name: vip_retryperiod
      value: "2"
    - name: vip_address
      value: "$VIP_ADDRESS"
    - name: prometheus_server
      value: ":2112"
    securityContext:
      capabilities:
        add:
        - NET_ADMIN
        - NET_RAW
    volumeMounts:
    - mountPath: /etc/kubernetes/admin.conf
      name: kubeconfig
    resources:
      requests:
        cpu: 50m
        memory: 64Mi
  hostAliases:
  - hostnames:
    - kubernetes
    ip: 127.0.0.1
  hostNetwork: true
  volumes:
  - name: kubeconfig
    hostPath:
      path: /etc/rancher/rke2/rke2.yaml
      type: FileOrCreate
EOF

echo "kube-vip manifest created at /var/lib/rancher/rke2/server/manifests/kube-vip.yaml"

# Wait for kube-vip to start
echo "Waiting for kube-vip to start (this may take up to 60 seconds)..."
for i in {1..60}; do
    if ip addr show "$INTERFACE" | grep -q "$VIP_ADDRESS"; then
        echo "✓ VIP $VIP_ADDRESS is active on interface $INTERFACE"
        break
    fi
    if [ $i -eq 60 ]; then
        echo "Warning: VIP not detected after 60 seconds"
        echo "Check logs with: kubectl --kubeconfig=/etc/rancher/rke2/rke2.yaml logs -n kube-system kube-vip"
        exit 1
    fi
    sleep 1
done

# Update kubeconfig to use VIP
echo "Updating kubeconfig to use VIP..."
cp /etc/rancher/rke2/rke2.yaml /etc/rancher/rke2/rke2.yaml.backup
sed -i "s|server: https://127.0.0.1:6443|server: https://$VIP_ADDRESS:6443|" /etc/rancher/rke2/rke2.yaml

# Update apiserver cert SANs
echo "Updating apiserver certificate SANs..."
cat > /etc/rancher/rke2/config.yaml.d/99-vip.yaml <<EOF
tls-san:
  - "$VIP_ADDRESS"
  - 127.0.0.1
  - localhost
EOF

# Restart RKE2 to apply cert changes
echo "Restarting RKE2 to apply certificate changes..."
systemctl restart rke2-server

# Wait for apiserver to be ready
echo "Waiting for apiserver to be ready..."
sleep 30

# Verify VIP is working
echo "Verifying VIP connectivity..."
export KUBECONFIG=/etc/rancher/rke2/rke2.yaml
if kubectl get nodes &>/dev/null; then
    echo "✓ Successfully connected to apiserver via VIP"
else
    echo "✗ Failed to connect to apiserver via VIP"
    echo "Restoring original kubeconfig..."
    cp /etc/rancher/rke2/rke2.yaml.backup /etc/rancher/rke2/rke2.yaml
    exit 1
fi

# Show current status
echo ""
echo "=== Installation Complete ==="
echo "VIP Address: $VIP_ADDRESS"
echo "Interface: $INTERFACE"
echo ""
echo "Current IP addresses on $INTERFACE:"
ip -4 addr show "$INTERFACE"
echo ""
echo "Next steps:"
echo "1. Verify VIP is active: ip addr show $INTERFACE | grep $VIP_ADDRESS"
echo "2. Test apiserver: kubectl --kubeconfig=/etc/rancher/rke2/rke2.yaml get nodes"
echo "3. Apply phase3-ha-cluster-rke2.yaml from management cluster"
echo "4. Monitor scaling: kubectl get machines,kubeadmcontrolplane -w"
echo "=============================="

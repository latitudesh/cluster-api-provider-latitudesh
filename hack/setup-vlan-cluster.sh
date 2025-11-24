#!/bin/bash
set -e

# VLAN + MetalLB Cluster Setup Helper Script
# This script helps automate VLAN attachment and configuration

echo "=== VLAN + MetalLB Cluster Setup Helper ==="
echo ""

# Check required tools
for tool in kubectl curl jq; do
  if ! command -v $tool &> /dev/null; then
    echo "ERROR: $tool is required but not installed"
    exit 1
  fi
done

# Configuration
read -p "Enter Latitude API Key: " API_KEY
read -p "Enter Project ID (e.g., proj_xxxxx): " PROJECT_ID
read -p "Enter VLAN ID (e.g., vlan_xxxxx): " VLAN_ID
read -p "Enter cluster name: " CLUSTER_NAME
read -p "Enter VLAN base IP (e.g., 10.8.0): " VLAN_BASE

echo ""
echo "=== Step 1: Getting Server IDs from LatitudeMachines ==="
SERVERS=$(kubectl get latitudemachine -l cluster.x-k8s.io/cluster-name=$CLUSTER_NAME \
  -o json | jq -r '.items[] | "\(.status.serverID // "pending")|\(.status.addresses[0].address // "pending")"')

if [ -z "$SERVERS" ]; then
  echo "ERROR: No LatitudeMachines found for cluster $CLUSTER_NAME"
  exit 1
fi

echo "Found servers:"
echo "$SERVERS"
echo ""

# Attach servers to VLAN
echo "=== Step 2: Attaching Servers to VLAN ==="
INDEX=10
for SERVER_INFO in $SERVERS; do
  SERVER_ID=$(echo $SERVER_INFO | cut -d'|' -f1)
  SERVER_IP=$(echo $SERVER_INFO | cut -d'|' -f2)

  if [ "$SERVER_ID" = "pending" ] || [ "$SERVER_IP" = "pending" ]; then
    echo "Skipping server (not ready yet): $SERVER_INFO"
    continue
  fi

  echo "Attaching $SERVER_ID to VLAN $VLAN_ID..."

  RESPONSE=$(curl -s -w "\n%{http_code}" -X POST \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"data\":{\"type\":\"virtual_network_assignment\",\"attributes\":{\"server_id\":\"$SERVER_ID\",\"virtual_network_id\":\"$VLAN_ID\"}}}" \
    "https://api.latitude.sh/virtual_networks/assignments")

  HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
  BODY=$(echo "$RESPONSE" | sed '$d')

  if [ "$HTTP_CODE" = "201" ]; then
    echo "✅ Successfully attached $SERVER_ID"
  elif echo "$BODY" | jq -e '.errors[0].detail' | grep -q "already assigned"; then
    echo "✅ Already attached $SERVER_ID"
  else
    echo "❌ Failed to attach $SERVER_ID (HTTP $HTTP_CODE)"
    echo "$BODY" | jq '.'
  fi

  echo ""
done

echo "=== Step 3: Configuring VLAN IPs ==="
echo ""
echo "Detected network interface (checking first server)..."
FIRST_IP=$(echo "$SERVERS" | head -1 | cut -d'|' -f2)

if [ "$FIRST_IP" != "pending" ]; then
  INTERFACE=$(ssh -o StrictHostKeyChecking=no ubuntu@$FIRST_IP "ip -o link show | grep -E 'eno[0-9]|enp[0-9]s0f[0-9]' | head -1 | awk '{print \$2}' | tr -d ':'")
  echo "Detected interface: $INTERFACE"
  echo ""
else
  echo "Warning: First server not ready, using default interface 'enp1s0f1'"
  INTERFACE="enp1s0f1"
  echo ""
fi

read -p "Is this interface correct? (y/n): " CONFIRM
if [ "$CONFIRM" != "y" ]; then
  read -p "Enter correct interface name: " INTERFACE
fi

echo ""
INDEX=10
for SERVER_INFO in $SERVERS; do
  SERVER_ID=$(echo $SERVER_INFO | cut -d'|' -f1)
  SERVER_IP=$(echo $SERVER_INFO | cut -d'|' -f2)

  if [ "$SERVER_IP" = "pending" ]; then
    echo "Skipping VLAN config for pending server"
    continue
  fi

  VLAN_IP="${VLAN_BASE}.${INDEX}"
  echo "Configuring $SERVER_IP with VLAN IP $VLAN_IP..."

  ssh -o StrictHostKeyChecking=no ubuntu@$SERVER_IP "sudo tee /etc/netplan/99-vlan.yaml > /dev/null << 'EOF'
network:
  version: 2
  vlans:
    vlan.2011:
      id: 2011
      link: $INTERFACE
      addresses: [$VLAN_IP/24]
EOF
sudo netplan apply"

  if [ $? -eq 0 ]; then
    echo "✅ Configured $VLAN_IP on $SERVER_IP"
  else
    echo "❌ Failed to configure $SERVER_IP"
  fi

  echo ""
  INDEX=$((INDEX + 1))
done

echo "=== Step 4: Verifying VLAN Connectivity ==="
FIRST_IP=$(echo "$SERVERS" | head -1 | cut -d'|' -f2)
if [ "$FIRST_IP" != "pending" ]; then
  echo "Testing from $FIRST_IP..."
  ssh -o StrictHostKeyChecking=no ubuntu@$FIRST_IP "
    for i in 10 11 12; do
      IP=${VLAN_BASE}.\$i
      echo -n \"Ping \$IP: \"
      if ping -c 1 -W 2 \$IP > /dev/null 2>&1; then
        echo '✅'
      else
        echo '❌'
      fi
    done
  "
fi

echo ""
echo "=== Step 5: Applying MetalLB Configuration ==="
read -p "Enter VLAN VID (e.g., 2011): " VLAN_VID
read -p "Enter MetalLB IP pool start (e.g., ${VLAN_BASE}.100): " POOL_START
read -p "Enter MetalLB IP pool end (e.g., ${VLAN_BASE}.150): " POOL_END

kubectl apply -f - <<EOF
---
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: vlan-pool
  namespace: metallb-system
spec:
  addresses:
  - ${POOL_START}-${POOL_END}
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: vlan-l2
  namespace: metallb-system
spec:
  ipAddressPools:
  - vlan-pool
  interfaces:
  - vlan.${VLAN_VID}
EOF

echo ""
echo "=== Setup Complete! ==="
echo ""
echo "Next steps:"
echo "1. Verify nodes: kubectl get nodes -o wide"
echo "2. Check providerIDs: kubectl get nodes -o jsonpath='{range .items[*]}{\\.metadata.name}{\": \"}{\\.spec.providerID}{\"\\n\"}{end}'"
echo "3. Test LoadBalancer:"
echo ""
echo "kubectl apply -f - <<EOF"
echo "apiVersion: v1"
echo "kind: Service"
echo "metadata:"
echo "  name: test-lb"
echo "spec:"
echo "  type: LoadBalancer"
echo "  ports:"
echo "  - port: 80"
echo "    targetPort: 80"
echo "  selector:"
echo "    app: nginx-test"
echo "---"
echo "apiVersion: apps/v1"
echo "kind: Deployment"
echo "metadata:"
echo "  name: nginx-test"
echo "spec:"
echo "  replicas: 2"
echo "  selector:"
echo "    matchLabels:"
echo "      app: nginx-test"
echo "  template:"
echo "    metadata:"
echo "      labels:"
echo "        app: nginx-test"
echo "    spec:"
echo "      containers:"
echo "      - name: nginx"
echo "        image: nginx:alpine"
echo "        ports:"
echo "        - containerPort: 80"
echo "EOF"
echo ""
echo "4. Check LoadBalancer IP: kubectl get svc test-lb"
echo "5. Test connectivity: curl http://<EXTERNAL-IP>"

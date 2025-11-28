# VLAN + MetalLB Setup Guide

This guide explains how to deploy a High Availability Kubernetes cluster on Latitude.sh using VLAN networking and MetalLB for LoadBalancer services, without requiring a Cloud Controller Manager (CCM).

## Overview

This solution provides:
- **3-node HA control plane** with automatic failover
- **VLAN private networking** for secure node-to-node communication
- **MetalLB LoadBalancer** services without CCM
- **Automatic providerID configuration** for proper node lifecycle management

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Public Network                           │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐              │
│  │  CP-0    │    │  CP-1    │    │  CP-2    │              │
│  │64.34.x.x │    │64.34.x.x │    │64.34.x.x │              │
│  └────┬─────┘    └────┬─────┘    └────┬─────┘              │
└───────┼───────────────┼───────────────┼─────────────────────┘
        │               │               │
┌───────┼───────────────┼───────────────┼─────────────────────┐
│       │        VLAN 2011 (Private)    │                     │
│  ┌────▼─────┐    ┌────▼─────┐    ┌────▼─────┐              │
│  │10.8.0.10 │    │10.8.0.11 │    │10.8.0.12 │              │
│  │          │    │          │    │          │              │
│  │ MetalLB  │◄───┤ MetalLB  │◄───┤ MetalLB  │              │
│  │ Speaker  │    │ Speaker  │    │ Speaker  │              │
│  └──────────┘    └──────────┘    └──────────┘              │
│       │               │               │                     │
│       └───────────────┴───────────────┘                     │
│              LoadBalancer VIP Pool                          │
│              10.8.0.100-10.8.0.150                         │
└─────────────────────────────────────────────────────────────┘
```

## Prerequisites

1. **Latitude.sh Account**
   - Project ID
   - API Key with server management permissions

2. **VLAN Setup**
   - Create a VLAN in your Latitude project
   - Note the VLAN ID (e.g., `vlan_mgWeN63zY5Yd7`)
   - Note the VID (e.g., `2011`)
   - Note the subnet (e.g., `10.8.0.0/24`)

3. **DNS or VIP**
   - DNS record pointing to your control plane endpoint, OR
   - Pre-allocated VIP from Latitude team

4. **Management Cluster**
   - Existing Kubernetes cluster with CAPI installed
   - kubectl configured

## Step-by-Step Deployment

### 1. Prepare SSH Keys Secret

```bash
kubectl create secret generic latitude-ssh-keys \
  --from-literal=authorized_keys="ssh-rsa AAAA... your-key"
```

### 2. Customize the Manifest

Edit `examples/vlan-ha-cluster-with-metallb.yaml`:

```yaml
# Update these values:
- controlPlaneEndpoint.host: "your-endpoint.example.com"
- LatitudeCluster.spec.projectRef.projectID: "proj_xxxxx"
- VLAN VID: 2011 (your VID)
- VLAN subnet: 10.8.0.0/24 (your subnet)
- Network interface: eno2 or enp1s0f1 (check with: ip link show)
- API key in get-latitude-server-id.py script
- Project ID in latitude-server-info file
```

**IMPORTANT**: The network interface name varies by server model:
- Check the correct interface: `ssh ubuntu@server-ip "ip link show"`
- Update both occurrences in the VLAN netplan configuration

### 3. Deploy the Cluster

```bash
kubectl apply -f examples/vlan-ha-cluster-with-metallb.yaml
```

Monitor cluster creation:
```bash
kubectl get cluster,machine -w
```

### 4. Wait for Control Plane Ready

Wait for the first control plane to bootstrap (5-10 minutes):
```bash
kubectl get rke2controlplane -w
```

### 5. Attach Servers to VLAN

Once servers are provisioned, attach them to the VLAN using the Latitude API:

```bash
export API_KEY="your-api-key"
export VLAN_ID="vlan_xxxxx"

# Get server IDs
kubectl get latitudemachine -o jsonpath='{.items[*].status.serverID}'

# Attach each server to VLAN
for SERVER_ID in sv_xxx sv_yyy sv_zzz; do
  curl -X POST \
    -H "Authorization: Bearer $API_KEY" \
    -H "Content-Type: application/json" \
    -d "{\"data\":{\"type\":\"virtual_network_assignment\",\"attributes\":{\"server_id\":\"$SERVER_ID\",\"virtual_network_id\":\"$VLAN_ID\"}}}" \
    "https://api.latitude.sh/virtual_networks/assignments"
done
```

### 6. Configure VLAN IPs on Additional Nodes

The first control plane gets the base IP (10.8.0.10) from the manifest. Configure additional nodes manually:

**Get server IPs:**
```bash
kubectl get latitudemachine -o jsonpath='{range .items[*]}{.metadata.name}{": "}{.status.addresses[0].address}{"\n"}{end}'
```

**For CP-1 (second control plane):**
```bash
ssh ubuntu@<CP-1-PUBLIC-IP> "sudo tee /etc/netplan/99-vlan.yaml << 'EOF'
network:
  version: 2
  vlans:
    vlan.2011:
      id: 2011
      link: enp1s0f1
      addresses: [10.8.0.11/24]
EOF
sudo netplan apply"
```

**For CP-2 (third control plane):**
```bash
ssh ubuntu@<CP-2-PUBLIC-IP> "sudo tee /etc/netplan/99-vlan.yaml << 'EOF'
network:
  version: 2
  vlans:
    vlan.2011:
      id: 2011
      link: enp1s0f1
      addresses: [10.8.0.12/24]
EOF
sudo netplan apply"
```

### 7. Verify VLAN Connectivity

Test connectivity between nodes:
```bash
ssh ubuntu@<CP-0-PUBLIC-IP> "ping -c 3 10.8.0.11 && ping -c 3 10.8.0.12"
```

### 8. Configure MetalLB

Get kubeconfig:
```bash
clusterctl get kubeconfig vlan-ha-cluster > vlan-ha-cluster.kubeconfig
export KUBECONFIG=vlan-ha-cluster.kubeconfig
```

Verify MetalLB is running:
```bash
kubectl get pods -n metallb-system
```

Apply MetalLB configuration:
```bash
kubectl apply -f examples/metallb-vlan-config.yaml
```

### 9. Verify the Setup

Check nodes:
```bash
kubectl get nodes -o wide
```

Verify providerIDs:
```bash
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{": "}{.spec.providerID}{"\n"}{end}'
```

Expected output:
```
control-plane-0: latitude://sv_xxxxx
control-plane-1: latitude://sv_yyyyy
control-plane-2: latitude://sv_zzzzz
```

### 10. Test LoadBalancer

Deploy a test service:
```bash
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: test-lb
spec:
  type: LoadBalancer
  ports:
  - port: 80
    targetPort: 80
  selector:
    app: nginx-test
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: nginx-test
spec:
  replicas: 2
  selector:
    matchLabels:
      app: nginx-test
  template:
    metadata:
      labels:
        app: nginx-test
    spec:
      containers:
      - name: nginx
        image: nginx:alpine
        ports:
        - containerPort: 80
EOF
```

Check the LoadBalancer IP:
```bash
kubectl get svc test-lb
```

Expected output:
```
NAME      TYPE           CLUSTER-IP     EXTERNAL-IP   PORT(S)        AGE
test-lb   LoadBalancer   10.x.x.x       10.8.0.100    80:xxxxx/TCP   30s
```

Test connectivity from a control plane node:
```bash
ssh ubuntu@<CP-0-PUBLIC-IP> "curl -s http://10.8.0.100 | head -5"
```

You should see the nginx welcome page.

## How It Works

### Automatic providerID Configuration

The `get-latitude-server-id.py` script runs during bootstrap:
1. Reads server hostname and public IP from cloud-init metadata
2. Queries Latitude API: `GET /servers?filter[project]={project_id}`
3. Matches server by IP or hostname
4. Writes providerID to RKE2 kubelet config: `latitude://sv_xxxxx`
5. RKE2 starts with correct providerID

**Key insight**: Using `filter[project]` avoids API pagination issues (default returns only 20 servers).

### MetalLB Without CCM

MetalLB works independently of Kubernetes cloud providers:
- **Layer 2 mode**: Uses ARP/NDP to advertise IPs on the VLAN
- **Speaker pods** run on each node, listening for LoadBalancer services
- When a service is created, MetalLB assigns an IP from the pool
- Speaker announces the IP via ARP on the VLAN interface
- Traffic flows directly over the VLAN network

This approach **does NOT require**:
- Cloud Controller Manager (CCM)
- External load balancer infrastructure
- Modifications to Kubernetes core

### Hybrid Networking

- **Pod networking**: Calico uses public interfaces for pod-to-pod communication
- **LoadBalancer services**: MetalLB uses VLAN interface for external access
- **Firewall**: Configured to allow Kubernetes + Calico ports

Required firewall ports:
- `22/tcp` - SSH
- `6443/tcp` - Kubernetes API
- `9345/tcp` - RKE2 supervisor
- `2379:2380/tcp` - etcd
- `10250/tcp` - kubelet
- `10259/tcp` - kube-scheduler
- `10257/tcp` - kube-controller-manager
- `4789/udp` - VXLAN (Calico)
- `5473/tcp` - Calico Typha (CRITICAL - missing this causes pod connectivity issues)

## Troubleshooting

### Calico Pods Not Ready

**Symptom**: `calico-node` pods stuck in `0/1 Running`

**Cause**: Typha port (5473/tcp) not allowed in firewall

**Fix**:
```bash
for ip in <node-ips>; do
  ssh ubuntu@$ip "sudo ufw allow 5473/tcp comment 'Calico Typha'"
done
```

### MetalLB Webhook Timeout

**Symptom**: `failed calling webhook "ipaddresspoolvalidationwebhook.metallb.io": context deadline exceeded`

**Cause**: Pod-to-pod networking not working (usually Typha firewall issue)

**Fix**: Ensure all Calico pods are ready first

### VLAN Interface Not Found

**Symptom**: `Device "vlan.2011" does not exist`

**Cause**: Wrong network interface name in netplan

**Fix**: Check correct interface and update manifest:
```bash
ssh ubuntu@<node-ip> "ip link show"
# Update netplan with correct interface (eno2, enp1s0f1, etc.)
```

### Server Not Found by API Script

**Symptom**: `ERROR: Could not find server ID`

**Cause**: API pagination (only returns 20 servers without filter)

**Fix**: Script already uses `filter[project]={project_id}` - ensure project ID is correct

## Limitations

1. **Manual VLAN IP configuration** on additional control planes (CP-1, CP-2)
   - Could be automated with CAPL controller enhancement
   - Requires knowing node order/index

2. **No automated DNS management**
   - User must update DNS records manually
   - Latitude API doesn't support DNS management yet

3. **VLAN attachment requires API call**
   - Not automated by CAPL controller
   - Could be integrated in future versions

4. **Single VLAN per cluster**
   - All nodes must be in same VLAN
   - Multi-VLAN scenarios not tested

## Future Improvements

1. **CAPL Controller Enhancement**
   - Automatic VLAN attachment during machine provisioning
   - Automatic VLAN IP assignment based on machine index
   - Detect and configure correct network interface

2. **Automated Testing**
   - E2E tests for VLAN + MetalLB setup
   - Verify LoadBalancer functionality
   - Test HA failover scenarios

3. **Documentation**
   - Network architecture diagrams
   - Troubleshooting flowcharts
   - Performance benchmarks

## References

- [MetalLB Documentation](https://metallb.universe.tf/)
- [Calico Documentation](https://docs.tigera.io/calico/latest/about/)
- [RKE2 Documentation](https://docs.rke2.io/)
- [Latitude.sh API Documentation](https://docs.latitude.sh/reference/api)
- [Cluster API Documentation](https://cluster-api.sigs.k8s.io/)

## Testing Results

Successfully tested with:
- **Date**: 2024-11-24
- **Cluster**: vlan-ha-final-test
- **Nodes**: 3 control planes (c2-small-x86)
- **OS**: Ubuntu 24.04 LTS
- **VLAN**: vlan_mgWeN63zY5Yd7 (VID: 2011, 10.8.0.0/24)
- **LoadBalancer**: nginx deployment (2 replicas)
- **Result**: ✅ All tests passed

Verified:
- ✅ All nodes have correct providerID (`latitude://sv_xxxxx`)
- ✅ VLAN connectivity between all nodes
- ✅ MetalLB assigns IPs from VLAN pool
- ✅ LoadBalancer service accessible from all nodes
- ✅ HTTP 200 responses from test service
- ✅ Pod-to-pod connectivity across nodes
- ✅ HA etcd cluster operational

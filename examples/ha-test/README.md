# HA Cluster Deployment Guide - RKE2 with kube-vip

This guide walks you through deploying a High Availability Kubernetes cluster on Latitude.sh with:
- **3 Control Plane nodes** with kube-vip for HA
- **2 Worker nodes**
- **RKE2** distribution
- **Calico CNI** in VXLAN mode
- **LoadBalancer** support via kube-vip cloud provider

---

## Prerequisites

1. **Management Cluster Ready**
   - CAPI bootstrap completed (`hack/bootstrap.sh` ran successfully)
   - CAPL controller running: `kubectl get pods -n capl-dev`

2. **Latitude.sh Account**
   - Project ID (get from Latitude.sh dashboard)
   - SSH Key ID (get from Latitude.sh dashboard)
   - API Key configured in management cluster secret
   - SSH keys secret created (see Step 0.5 below)

3. **Tools Installed**
   - `kubectl`
   - `clusterctl`
   - SSH client

---

## Phase 0: Prepare SSH Keys Secret

### Step 0.5: Create SSH Keys Secret

From the **management cluster**, create a Kubernetes secret with your SSH key IDs:

```bash
# Get your SSH key ID from Latitude.sh dashboard or API
# Format: ssh_xxxxx (e.g., ssh_dexA0q4pXalQV)

# Create the secret
kubectl create secret generic latitude-ssh-keys \
  --from-literal=ssh-key-ids="ssh_dexA0q4pXalQV"

# For multiple SSH keys, use comma-separated list
kubectl create secret generic latitude-ssh-keys \
  --from-literal=ssh-key-ids="ssh_abc123,ssh_def456"

# Verify the secret was created
kubectl get secret latitude-ssh-keys -o yaml
```

**For production**, consider using [External Secrets Operator](../external-secrets/README.md) to sync SSH keys from a secure backend (AWS Secrets Manager, Vault, etc.).

---

## Phase 1: Deploy Single Control Plane

### Step 1.1: Apply Phase 1 Manifest

From the **management cluster** (e.g., `ssh ubuntu@64.34.82.159`):

```bash
cd ~/cluster-api-provider-latitudesh
kubectl apply -f examples/ha-test/phase1-single-cp-rke2.yaml
```

### Step 1.2: Monitor Deployment

```bash
# Watch all resources
kubectl get cluster,rke2controlplane,machines,latitudemachines -w

# Check specific resources
kubectl get machines
kubectl get latitudemachines -o yaml
```

**Wait for:**
- `Machine` status: `Running`
- `LatitudeMachine` status: `Ready: true`

This typically takes **10-15 minutes** (server provisioning + RKE2 bootstrap).

### Step 1.3: Get Server IP and Subnet

```bash
# Get the IP address of the provisioned server
kubectl get latitudemachine ha-test-nyc-control-plane-XXXXX -o yaml | grep -A 10 addresses

# Example output:
#   addresses:
#   - address: 100.64.208.10
#     type: ExternalIP
#   - address: default-ha-test-nyc-control-plane-abcde
#     type: Hostname
```

**Note the IP:** e.g., `100.64.208.10`
**Identify subnet:** `100.64.208.0/24`

### Step 1.4: Request VIP from Latitude.sh

Contact Latitude.sh support:

```
Subject: Request VIP for Kubernetes HA Cluster

Hi,

I need a Virtual IP (VIP) for a Kubernetes HA control plane in project <YOUR_PROJECT_ID>.

Current server IP: 100.64.208.10 (example)
Requested VIP: 100.64.208.100 (or any available IP in same subnet)
Location: NYC (or your chosen location)

This VIP will be managed by kube-vip for Kubernetes API high availability.

Thanks!
```

**Wait for confirmation** that the VIP is allocated and available.

---

## Phase 2: Install kube-vip on First Control Plane

### Step 2.1: Get Control Plane SSH Access

```bash
# Get the server IP
CP_IP=$(kubectl get latitudemachine -l cluster.x-k8s.io/control-plane=true -o jsonpath='{.items[0].status.addresses[?(@.type=="ExternalIP")].address}')

echo "Control Plane IP: $CP_IP"

# SSH into the control plane
ssh ubuntu@$CP_IP
```

### Step 2.2: Copy and Run Installation Script

From your **local machine**, copy the script to the CP:

```bash
scp examples/ha-test/install-kubevip.sh ubuntu@$CP_IP:~/
```

On the **control plane node**, run:

```bash
# Replace with your actual VIP
sudo bash ~/install-kubevip.sh 100.64.208.100
```

**The script will:**
1. Detect the correct network interface
2. Create kube-vip static pod manifest
3. Wait for VIP to become active
4. Update kubeconfig to use VIP
5. Update apiserver certificate SANs
6. Restart RKE2 to apply changes

### Step 2.3: Verify VIP is Active

On the control plane:

```bash
# Check VIP is assigned
ip addr show ens3 | grep 100.64.208.100

# Test apiserver via VIP
export KUBECONFIG=/etc/rancher/rke2/rke2.yaml
kubectl get nodes
kubectl get pods -A
```

Expected output:
- VIP visible in `ip addr` output
- `kubectl` commands work successfully
- All pods in `kube-system` namespace running

---

## Phase 3: Scale to 3 Control Planes + 2 Workers

### Step 3.1: Edit Phase 3 Manifest

On the **management cluster**, edit the manifest to replace VIP placeholders:

```bash
cd ~/cluster-api-provider-latitudesh

# Replace YOUR_VIP_HERE with your actual VIP (do this in 4 places)
sed -i 's/YOUR_VIP_HERE/100.64.208.100/g' examples/ha-test/phase3-ha-cluster-rke2.yaml

# Also update the LoadBalancer IP range (avoid the VIP itself)
sed -i 's/CHANGE_THIS_TO_YOUR_LB_RANGE/100.64.208.150-100.64.208.180/g' examples/ha-test/phase3-ha-cluster-rke2.yaml

# Verify changes
grep -n "100.64.208.100" examples/ha-test/phase3-ha-cluster-rke2.yaml
```

### Step 3.2: Apply Phase 3 Manifest

```bash
kubectl apply -f examples/ha-test/phase3-ha-cluster-rke2.yaml
```

This will:
- Update `controlPlaneEndpoint` to use the VIP
- Scale `RKE2ControlPlane` from 1 to 3 replicas
- Create `MachineDeployment` with 2 workers

### Step 3.3: Monitor Scaling

```bash
# Watch all resources
kubectl get cluster,rke2controlplane,machines,machinedeployments -w

# Expected progression:
# - 2 new CP machines created (takes ~10-15 min each)
# - CP machines join via VIP
# - 2 worker machines created (takes ~10 min each)
# - Workers join the cluster
```

**Total time:** ~40-60 minutes for full cluster

### Step 3.4: Verify Cluster Health

```bash
# Get workload cluster kubeconfig
clusterctl get kubeconfig ha-test-nyc > ha-test-nyc.kubeconfig

# Check nodes
kubectl --kubeconfig=ha-test-nyc.kubeconfig get nodes

# Expected output: 5 nodes (3 control-plane, 2 worker)
# NAME                                   STATUS   ROLES                       AGE
# default-ha-test-nyc-control-plane-0    Ready    control-plane,etcd,master   45m
# default-ha-test-nyc-control-plane-1    Ready    control-plane,etcd,master   25m
# default-ha-test-nyc-control-plane-2    Ready    control-plane,etcd,master   15m
# default-ha-test-nyc-md-0-xxxxx-yyyyy   Ready    <none>                      10m
# default-ha-test-nyc-md-0-xxxxx-zzzzz   Ready    <none>                      8m

# Check all pods
kubectl --kubeconfig=ha-test-nyc.kubeconfig get pods -A

# Verify kube-vip is running on all CPs
kubectl --kubeconfig=ha-test-nyc.kubeconfig get pods -n kube-system | grep kube-vip
```

---

## Phase 4: Test High Availability

### Test 4.1: API Server Failover

```bash
# From management cluster, delete one control plane machine
kubectl delete machine ha-test-nyc-control-plane-XXXXX

# Monitor: The cluster should remain accessible via VIP
kubectl --kubeconfig=ha-test-nyc.kubeconfig get nodes -w

# CAPI will automatically create a replacement CP
```

### Test 4.2: LoadBalancer Service

Create a test LoadBalancer service:

```bash
kubectl --kubeconfig=ha-test-nyc.kubeconfig create deployment nginx --image=nginx
kubectl --kubeconfig=ha-test-nyc.kubeconfig expose deployment nginx --port=80 --type=LoadBalancer

# Wait for External IP
kubectl --kubeconfig=ha-test-nyc.kubeconfig get svc nginx -w

# Test access
curl http://<EXTERNAL-IP>
```

---

## Troubleshooting

### Problem: VIP not active after install-kubevip.sh

**Check:**
```bash
# On CP node
sudo crictl ps | grep kube-vip
sudo crictl logs <container-id>

# Check RKE2 logs
sudo journalctl -u rke2-server -f
```

**Solution:**
- Verify VIP is actually allocated by Latitude.sh
- Check network interface name is correct
- Ensure no firewall blocking ARP

### Problem: New CPs fail to join

**Check:**
```bash
# On new CP node
ssh ubuntu@<NEW_CP_IP>
sudo journalctl -u rke2-server -f

# Look for TLS or connection errors
```

**Solution:**
- Verify VIP is reachable from new CP
- Check certificates include VIP in SANs
- Ensure port 6443 is open

### Problem: Workers fail to join

**Check:**
```bash
# On worker node
ssh ubuntu@<WORKER_IP>
sudo journalctl -u rke2-agent -f
```

**Solution:**
- Verify VIP is reachable from worker
- Check network connectivity to CPs
- Verify firewall rules allow required ports

### Problem: No LoadBalancer IP assigned

**Check:**
```bash
kubectl --kubeconfig=ha-test-nyc.kubeconfig get configmap -n kube-system kubevip -o yaml
kubectl --kubeconfig=ha-test-nyc.kubeconfig logs -n kube-system -l app=kube-vip-ds
```

**Solution:**
- Verify IP range in ConfigMap doesn't overlap with VIP
- Check kube-vip-cloud-controller is running
- Ensure kube-vip DaemonSet is running on all nodes

---

## Cleanup

To delete the entire cluster:

```bash
# From management cluster
kubectl delete cluster ha-test-nyc

# This will delete all machines and servers
# Monitor: kubectl get machines,latitudemachines -w
```

---

## Architecture Diagram

```
                                    ┌─────────────────┐
                                    │   VIP           │
                                    │  100.64.208.100 │
                                    └────────┬────────┘
                                             │
                 ┌───────────────────────────┼───────────────────────────┐
                 │                           │                           │
         ┌───────▼────────┐         ┌───────▼────────┐         ┌───────▼────────┐
         │  Control Plane │         │  Control Plane │         │  Control Plane │
         │      Node 1    │         │      Node 2    │         │      Node 3    │
         │  kube-vip (L)  │         │  kube-vip (F)  │         │  kube-vip (F)  │
         │  100.64.208.10 │         │  100.64.208.11 │         │  100.64.208.12 │
         └────────────────┘         └────────────────┘         └────────────────┘
                 │                           │                           │
                 └───────────────────────────┼───────────────────────────┘
                                             │
                                   ┌─────────┴─────────┐
                                   │                   │
                          ┌────────▼────────┐  ┌──────▼──────────┐
                          │  Worker Node 1  │  │  Worker Node 2  │
                          │ 100.64.208.20   │  │ 100.64.208.21   │
                          └─────────────────┘  └─────────────────┘

L = Leader (holds VIP)
F = Follower (ready to take over)
```

---

## Key Points

✅ **VIP Failover:** kube-vip uses leader election. If CP1 fails, CP2 or CP3 takes over the VIP
✅ **Automatic Recovery:** CAPI recreates failed machines automatically
✅ **LoadBalancer Support:** kube-vip cloud provider allocates IPs from the configured range
✅ **Production Ready:** 3 CP quorum handles 1 node failure gracefully

---

## Next Steps

After successful deployment:

1. **Configure kubectl context:**
   ```bash
   kubectl config set-context ha-test-nyc --cluster=ha-test-nyc --user=ha-test-nyc-admin
   kubectl config use-context ha-test-nyc
   ```

2. **Install applications:**
   - Ingress controller (nginx, traefik)
   - Cert-manager for TLS
   - Monitoring (Prometheus, Grafana)
   - Storage (Rook-Ceph, Longhorn)

3. **Backup etcd:**
   ```bash
   # RKE2 includes automated etcd snapshots
   # Snapshots stored in: /var/lib/rancher/rke2/server/db/snapshots/
   ```

4. **Document your setup:**
   - VIP address
   - Server IPs
   - LoadBalancer IP range
   - SSH keys used

---

## Support

For issues:
- **CAPL Issues:** https://github.com/latitudesh/cluster-api-provider-latitudesh/issues
- **Latitude.sh Support:** support@latitude.sh
- **CAPI Documentation:** https://cluster-api.sigs.k8s.io/
- **RKE2 Documentation:** https://docs.rke2.io/

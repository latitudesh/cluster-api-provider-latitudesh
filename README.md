# cluster-api-provider-latitudesh

> [!WARNING]
> This project is currently in **early alpha**.  
> It is **not production-ready** and may change significantly as development continues.

Kubernetes Cluster API infrastructure provider for [Latitude.sh](https://www.latitude.sh/) bare metal servers.

## Current Status

### Implemented
- [x] Basic infrastructure provider (VM provisioning)
- [x] DNS-based control plane endpoint approach
- [x] Cluster creation via CAPI manifests
- [x] Integration with Latitude.sh API
- [x] Single control plane deployment
- [x] Worker node provisioning
- [x] CNI plugin support (tested with Calico)

### Partially Implemented (Requires Manual Intervention)
- [ ] Automated DNS A record management
- [ ] Automatic kubeadm bootstrap timing
- [ ] Worker node auto-join to cluster
- [ ] Cluster upgrade operations

### Planned / In Progress
- [ ] Load Balancer support for HA control plane
- [ ] Automated DNS management via Latitude API
- [ ] Health checks and retry logic for bootstrap
- [ ] Multi-control plane HA setup
- [ ] Cluster scaling (add/remove nodes)
- [ ] Machine health checks
- [ ] Full e2e test suite

### Known Limitations
- DNS A record must be manually updated after control plane creation
- kubeadm init may require manual reset/reinit due to DNS propagation timing
- Workers may need manual intervention to join cluster
- Not production-ready without Load Balancer implementation

### Production Readiness Checklist
- [ ] Automated DNS record management
- [ ] Load Balancer support
- [ ] Control plane High Availability (3+ nodes)
- [ ] Automated failure recovery
- [ ] Comprehensive test coverage
- [ ] Documentation for production deployment

---

**Current Focus:** Implementing automated DNS management and Load Balancer support for production HA clusters.

For testing/development clusters, manual DNS configuration is required. See [PR #8](https://github.com/latitudesh/cluster-api-provider-latitudesh/pull/8) for detailed testing steps.

## Quick Start
- [x] Link to [Contributing guide](CONTRIBUTING.md)
- [x] Link to Testing (PR #8)
- [ ] Link to Examples

## License

Apache 2.0
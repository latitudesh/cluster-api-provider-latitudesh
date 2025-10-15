## Problem

## Solution

### Setup

> [!IMPORTANT]
> We are testing on **Ubuntu**

You need to provision a new fresh machine (remember to use our [IaC provider](https://github.com/latitudesh/terraform-provider-latitudesh).

Install docker and refresh the session:

```sh
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
newgrp docker
id -nG
```

Then test without `sudo`:

```sh
docker info
docker ps
```

Clone the repository and go into the project directory:

```sh
git clone https://github.com/latitudesh/cluster-api-provider-latitudesh.git
cd cluster-api-provider-latitudesh
git checkout <BRANCH_NAME>
```

Export your API Key:

```sh
export LATITUDE_API_KEY=<YOUR_API_KEY>
```

Run the bootstrap script:

```sh
sudo apt install make
make bootstrap
```

### Results

- 
-
-

### Delete Cluster and All Resources

From CAPI (Management Cluster).

This will delete the cluster and trigger deletion of all 3 machines (control plane + 2 workers):

```sh
# Delete the cluster (this cascades to all machines)
make clean
```

---

### Known Limitations

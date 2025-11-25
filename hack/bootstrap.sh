#!/usr/bin/env bash

set -euo pipefail

sudo apt install -y make
sudo snap install go --classic

source "$(dirname "$0")/.env.dev"

CAPL_NAMESPACE="${CAPL_NAMESPACE:-capl-dev}"
CLUSTER_NAME="${CLUSTER_NAME:-capl-system}"

PINNED_VERSION="v1.10.5"

# Detect architecture
ARCH_RAW=$(uname -m) # x86_64 | aarch64 | arm64
case "$ARCH_RAW" in
x86_64 | amd64) ARCH=amd64 ;;
arm64 | aarch64) ARCH=arm64 ;;
*)
  echo "❌ Unsupported architecture: $ARCH_RAW"
  exit 1
  ;;
esac

# Versions (can be overridden via env vars)
KIND_VERSION="${KIND_VERSION:-v0.26.0}"
KUBECTL_VERSION="${KUBECTL_VERSION:-v1.34.1}"

install_kind() {
  if command -v kind &>/dev/null; then return; fi
  echo "⚠️ kind not found. Installing..."
  url="https://kind.sigs.k8s.io/dl/${KIND_VERSION}/kind-linux-${ARCH}"
  echo "→ $url"
  curl -fsSL "$url" -o kind
  chmod +x kind
  sudo mv kind /usr/local/bin/kind
  echo "✅ kind installed ($(kind version | tr -d '\n'))"
}

install_kubectl() {
  if command -v kubectl &>/dev/null; then return; fi
  echo "⚠️ kubectl not found. Installing..."
  url="https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl"
  echo "→ $url"
  curl -fsSL "$url" -o kubectl
  chmod +x kubectl
  sudo mv kubectl /usr/local/bin/kubectl
  echo "✅ kubectl installed ($(kubectl version --client --short 2>/dev/null || true))"
}

install_docker() {
  if command -v docker &>/dev/null; then
    return
  fi

  echo "❌ Docker was not found on your machine."
  echo "➡️ Please install Docker manually before continuing:"
  echo "   https://docs.docker.com/engine/install/ubuntu/"
  exit 1
}

install_clusterctl() {
  if command -v clusterctl &>/dev/null; then return; fi
  echo "⚠️ clusterctl not found. Installing..."

  CLUSTERCTL_VERSION="${CLUSTERCTL_VERSION:-$PINNED_VERSION}"
  url="https://github.com/kubernetes-sigs/cluster-api/releases/download/${CLUSTERCTL_VERSION}/clusterctl-linux-${ARCH}"
  curl -fsSL "$url" -o clusterctl
  chmod +x clusterctl
  sudo mv clusterctl /usr/local/bin/clusterctl
  echo "✅ clusterctl installed ($CLUSTERCTL_VERSION)"
}

install_kustomize() {
  if command -v kustomize >/dev/null 2>&1; then
    return
  fi
  echo "! kustomize not found. installing..."
  curl -s https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh | bash
  mkdir -p "$HOME/bin"
  mv kustomize "$HOME/bin/kustomize"
  export PATH="$HOME/bin:$PATH"
}

# install dependencies if missing
install_kind
install_kubectl
install_clusterctl
install_kustomize
install_docker

# kind
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.29.8}"

if ! kind get clusters | grep -qx "$CLUSTER_NAME"; then
  cat >/tmp/kind-${CLUSTER_NAME}.yaml <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: ${CLUSTER_NAME}
nodes:
- role: control-plane
  image: ${KIND_NODE_IMAGE}
- role: worker
  image: ${KIND_NODE_IMAGE}
YAML
  kind create cluster --config /tmp/kind-${CLUSTER_NAME}.yaml --wait 120s
else
  echo "Kind cluster '${CLUSTER_NAME}' already exists"
fi

# context
CTX="kind-${CLUSTER_NAME}"

mkdir -p "$HOME/.kube"
tmp="$(mktemp)"
kind export kubeconfig --name "$CLUSTER_NAME" --kubeconfig "$tmp" || kind get kubeconfig --name "$CLUSTER_NAME" >"$tmp"
if [[ -f "$HOME/.kube/config" ]]; then
  KUBECONFIG="$HOME/.kube/config:$tmp" kubectl config view --flatten >"$HOME/.kube/config.merged"
  mv "$HOME/.kube/config.merged" "$HOME/.kube/config"
else
  mv "$tmp" "$HOME/.kube/config"
fi
rm -f "$tmp"

kubectl config use-context "$CTX"
kubectl get nodes -o wide
kubectl -n kube-system get pods

# cert-manager
CERT_MANAGER_FILE="config/certmanager/cert-manager.crds.yaml"

kubectl apply -f "$CERT_MANAGER_FILE"
kubectl -n cert-manager rollout status deploy/cert-manager --timeout=4m
kubectl -n cert-manager rollout status deploy/cert-manager-webhook --timeout=4m
kubectl -n cert-manager rollout status deploy/cert-manager-cainjector --timeout=4m

kubectl -n cert-manager wait --for=condition=Available deploy/cert-manager-webhook --timeout=4m

# build/load image & overrides
export IMG="ttl.sh/capl-$(date +%s):1h"
export STABLE_IMG_NAME="${STABLE_IMG_NAME:-capl-manager:dev}"

STABLE_IMG="${STABLE_IMG:-ghcr.io/latitudesh/${STABLE_IMG_NAME}}"

if [[ "$STABLE_IMG" != */* ]]; then
  STABLE_IMG="ghcr.io/latitudesh/${STABLE_IMG}"
fi

export CAPL_VERSION="${CAPL_VERSION:-v0.1.0}"

docker build --build-arg LATITUDE_API_KEY="${LATITUDE_API_KEY:-}" \
	-t "$STABLE_IMG" \
	-t "$IMG" \
	.

kind load docker-image "$STABLE_IMG" --name "$CLUSTER_NAME" || true
echo "STABLE_IMG=$STABLE_IMG"

# secret
if [[ -z "${LATITUDE_API_KEY:-}" ]]; then
  echo "⚠️  LATITUDE_API_KEY not set - using dummy token"
  echo "   Set it in hack/.env.dev for real testing"

  exit 1
fi

kubectl get ns "${CAPL_NAMESPACE}" >/dev/null 2>&1 || kubectl create ns "${CAPL_NAMESPACE}"

BASE_URL="https://api.latitudesh.sh"

kubectl -n "${CAPL_NAMESPACE}" create secret generic latitudesh-credentials \
  --from-literal=API_TOKEN="${LATITUDE_API_KEY:-dummy-token}" \
  --from-literal=LATITUDE_API_KEY="${LATITUDE_API_KEY:-dummy-token}" \
  --from-literal=BASE_URL="${LATITUDE_BASE_URL:-https://api.latitudesh.sh}" \
  --dry-run=client -o yaml | kubectl apply -f -

command -v make >/dev/null && {
  make generate || true
  make manifests || true
}

kustomize build config/crd | kubectl apply -f -
(
  cd config/default

  kustomize edit set namespace "$CAPL_NAMESPACE"
  kustomize edit set image controller="$STABLE_IMG" || kustomize edit set image manager="$STABLE_IMG"
)

kustomize build config/default > "/tmp/components.yaml"

# hard-fix
sed -i -E 's#image:[[:space:]]*[^[:space:]]*(manager|controller|capl-manager|cluster-api-provider-latitudesh):[^[:space:]]*#image: '"$STABLE_IMG"'#g' /tmp/components.yaml

cat > "/tmp/metadata.yaml" <<'YAML'
apiVersion: clusterctl.cluster.x-k8s.io/v1alpha3
kind: Metadata
releaseSeries:
- major: 0
  minor: 1
  contract: v1beta1
YAML

PROVIDER=infrastructure-latitudesh
VERSION=v0.1.0
BASE="$HOME/.cluster-api/overrides/${PROVIDER}/${VERSION}"

mkdir -p "$BASE"
cp /tmp/components.yaml "$BASE/components.yaml"
cp /tmp/metadata.yaml  "$BASE/metadata.yaml"

# clusterctl config local
cat > "$HOME/.cluster-api/clusterctl.yaml" <<EOF
providers:
  - name: "latitudesh"
    type: "InfrastructureProvider"
    url: "file://$BASE/components.yaml"
EOF

echo "$ overrides OK!"
ls -l $BASE

# clusterctl init 
echo "$ clusterctl init --infrastructure latitudesh ..."

clusterctl init \
  --core cluster-api:$PINNED_VERSION \
  --bootstrap kubeadm:$PINNED_VERSION,rke2:v0.20.0 \
  --control-plane kubeadm:$PINNED_VERSION,rke2:v0.20.0 \
  --infrastructure latitudesh:v0.1.0 \
  --wait-providers

kubectl -n capi-system get deploy
kubectl -n "$CAPL_NAMESPACE" get deploy,pods

kubectl -n "$CAPL_NAMESPACE" rollout status deploy/capl-controller-manager --timeout=5m

echo
echo "🏁 Bootstrap finished!"
echo
echo "Create a demo cluster using:"
echo "  kubectl apply -f examples/10-demo-cluster.yaml"

#!/usr/bin/env bash

set -euo pipefail
source "$(dirname "$0")/.env.dev"

CAPL_NAMESPACE="${CAPL_NAMESPACE:-capl-dev}"

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
KIND_VERSION="${KIND_VERSION:-v0.23.0}"
KUBECTL_VERSION="${KUBECTL_VERSION:-$(curl -fsSL https://dl.k8s.io/release/stable.txt)}"

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
  version=$(curl -fsSL https://api.github.com/repos/kubernetes-sigs/cluster-api/releases/latest |
    grep tag_name |
    cut -d '"' -f4)
  url="https://github.com/kubernetes-sigs/cluster-api/releases/download/${version}/clusterctl-linux-${ARCH}"
  echo "→ $url"
  curl -fsSL "$url" -o clusterctl
  chmod +x clusterctl
  sudo mv clusterctl /usr/local/bin/clusterctl
  echo "✅ clusterctl installed ($(clusterctl version | head -n1))"
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
if ! kind get clusters | grep -qx "$CLUSTER_NAME"; then
  cat >/tmp/kind-${CLUSTER_NAME}.yaml <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: ${CLUSTER_NAME}
nodes:
- role: control-plane
- role: worker
YAML
  kind create cluster --config /tmp/kind-${CLUSTER_NAME}.yaml
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
CERT_MANAGER_FILE="cert-manager.crds.yaml"
curl -L \
  https://github.com/cert-manager/cert-manager/releases/download/v1.18.2/cert-manager.yaml \
  -o "$CERT_MANAGER_FILE"

kubectl apply -f "$CERT_MANAGER_FILE"
kubectl -n cert-manager rollout status deploy/cert-manager --timeout=4m
kubectl -n cert-manager rollout status deploy/cert-manager-webhook --timeout=4m
kubectl -n cert-manager rollout status deploy/cert-manager-cainjector --timeout=4m

# secret
BASE_URL="https://api.latitudesh.sh"

kubectl get ns ${CAPL_NAMESPACE} >/dev/null 2>&1 || kubectl create ns ${CAPL_NAMESPACE}

kubectl -n ${CAPL_NAMESPACE} create secret generic latitudesh-credentials \
  --from-literal=LATITUDE_API_KEY="${LATITUDE_API_KEY:-dummy-token}" \
  --from-literal=BASE_URL="${LATITUDE_BASE_URL:-https://api.latitudesh.sh}" \
  --dry-run=client -o yaml | kubectl apply -f -

# build/load image & overrides
export IMG="ttl.sh/capl-$(date +%s):1h"
export CAPL_VERSION="${CAPL_VERSION:-v0.1.0}"

docker build --build-arg LATITUDE_API_KEY="${LATITUDE_API_KEY:-}" -t "$IMG" .
kind load docker-image "$IMG" --name "$CLUSTER_NAME" || docker push "$IMG"

docker tag "$IMG" capl-manager:dev
kind load docker-image capl-manager:dev --name "$CLUSTER_NAME"

echo "IMG=$IMG"

command -v make >/dev/null && {
  make generate || true
  make manifests || true
}

kustomize build config/crd | kubectl apply -f -
(
  cd config/default && kustomize edit set namespace "$CAPL_NAMESPACE"
  kustomize edit set image controller="$IMG" || kustomize edit set image manager="$IMG"
)

kustomize build config/default > "/tmp/components.yaml"

cat > "/tmp/metadata.yaml" <<'YAML'
apiVersion: clusterctl.cluster.x-k8s.io/v1alpha3
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
echo "$ clusterctl init --infrastructure latitudesh"
CLUSTERCTL_CONFIG="$HOME/.cluster-api/clusterctl.yaml" clusterctl init \
  --core cluster-api \
  --bootstrap kubeadm \
  --control-plane kubeadm \
  --infrastructure latitudesh

kubectl -n capi-system get deploy
kubectl get crds | grep -E 'cluster\.x-k8s\.io|latitudesh'

kubectl -n ${CAPL_NAMESPACE} rollout status deploy/capl-controller-manager --timeout=5m
kubectl -n ${CAPL_NAMESPACE} logs deploy/capl-controller-manager -c manager --tail=200

#!/usr/bin/env bash

set -euo pipefail
source "$(dirname "$0")/.env.dev"

# kind
if ! kind get clusters | grep -qx "$CLUSTER_NAME"; then
  cat > /tmp/kind-${CLUSTER_NAME}.yaml <<YAML
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
kind export kubeconfig --name "$CLUSTER_NAME" --kubeconfig "$tmp" || kind get kubeconfig --name "$CLUSTER_NAME" > "$tmp"
if [[ -f "$HOME/.kube/config" ]]; then
  KUBECONFIG="$HOME/.kube/config:$tmp" kubectl config view --flatten > "$HOME/.kube/config.merged"
  mv "$HOME/.kube/config.merged" "$HOME/.kube/config"
else
  mv "$tmp" "$HOME/.kube/config"
fi
rm -f "$tmp"

kubectl config use-context "$CTX"
kubectl get nodes -o wide
kubectl -n kube-system get pods

# cert-manager
#CERT_MANAGER_FILE="$(dirname "$0")/../hack/cert-manager.crds.yaml"
CERT_MANAGER_FILE="cert-manager.crds.yaml"
curl -L \
	https://github.com/cert-manager/cert-manager/releases/download/v1.18.2/cert-manager.yaml \
	-o "$CERT_MANAGER_FILE"

kubectl apply -f "$CERT_MANAGER_FILE"
kubectl -n cert-manager rollout status deploy/cert-manager --timeout=4m
kubectl -n cert-manager rollout status deploy/cert-manager-webhook --timeout=4m
kubectl -n cert-manager rollout status deploy/cert-manager-cainjector --timeout=4m

# install CAPI
clusterctl init --infrastructure docker

kubectl -n capi-system get deploy
kubectl get crds | grep 'cluster\.x-k8s\.io'

# build and load
export IMG="ttl.sh/capl-$(date +%s):1h"

docker build -t "$IMG" .
kind load docker-image "$IMG" --name capl-dev || docker push "$IMG"

docker tag "$IMG" capl-manager:dev
kind load docker-image capl-manager:dev --name capl-dev

echo "IMG=$IMG"

# secret
API_TOKEN="YOUR_API_TOKEN"
BASE_URL="https://api.latitudesh.sh"

kubectl -n capl-system create secret generic latitudesh-credentials \
  --from-literal=API_TOKEN="${LATITUDE_BEARER:-dummy-token}" \
  --from-literal=BASE_URL="${LATITUDE_BASE_URL:-https://api.latitudesh.sh}" \
  --dry-run=client -o yaml | kubectl apply -f -

# apply crds
command -v make >/dev/null && { make generate || true; make manifests || true; }

kustomize build config/crd | kubectl apply -f -
( cd config/default && kustomize edit set image capl-manager:dev="$IMG" )
kustomize build config/default | kubectl apply -f -

kubectl -n capl-system rollout status deploy/capl-controller-manager --timeout=5m
kubectl -n capl-system logs deploy/capl-controller-manager -c manager --tail=200


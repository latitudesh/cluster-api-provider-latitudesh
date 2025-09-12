#!/usr/bin/env bash

set -euo pipefail
NS=${CAPL_NAMESPACE:-capl-dev}

kubectl config current-context

DEPLOY=$(kubectl -n "$NS" get deploy -l 'app.kubernetes.io/name=cluster-api-provider-latitudesh' -o name | grep controller-manager || true)
[[ -n "${DEPLOY:-}" ]] && kubectl -n "$NS" scale "$DEPLOY" --replicas=0 || true

CLUSTERCTL_CONFIG="$HOME/.cluster-api/clusterctl.yaml" \
clusterctl delete --infrastructure latitudesh || true

kubectl -n "$NS" delete providers.clusterctl.cluster.x-k8s.io infrastructure-latitudesh --ignore-not-found

kubectl -n "$NS" delete secret latitudesh-credentials --ignore-not-found
kubectl delete ns "$NS" --ignore-not-found

kubectl get crd | awk '/latitude.*infrastructure.cluster.x-k8s.io/{print $1}' | \
  xargs -r kubectl delete crd


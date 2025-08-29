#!/usr/bin/env bash

set -euo pipefail

cat >/tmp/lat-machine-smoke.yaml <<'YAML'
apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
kind: LatitudeMachine
metadata:
  name: hello-1
  namespace: default
spec:
  plan: c2-small-x86
  operatingSystem: ubuntu-24.04
YAML

kubectl apply -f /tmp/lat-machine-smoke.yaml
sleep 1
kubectl get latitudemachine hello-1 -o yaml | yq '.status'

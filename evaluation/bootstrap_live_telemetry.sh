#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-topo-cluster}"
BUILD_TELEMETRY_IMAGE="${BUILD_TELEMETRY_IMAGE:-1}"

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

run() {
  echo
  echo "==> $*"
  "$@"
}

main() {
  require_cmd kubectl
  require_cmd linkerd
  require_cmd kind
  require_cmd docker

  if kubectl get configmap -n linkerd linkerd-config >/dev/null 2>&1; then
    echo
    echo "==> linkerd control plane already exists; skipping fresh install"
  else
    echo
    echo "==> linkerd install --crds | kubectl apply -f -"
    linkerd install --crds | kubectl apply -f -
    echo
    echo "==> linkerd install | kubectl apply -f -"
    linkerd install | kubectl apply -f -
  fi

  if kubectl get namespace linkerd-viz >/dev/null 2>&1 && kubectl get deploy -n linkerd-viz prometheus >/dev/null 2>&1; then
    echo
    echo "==> linkerd viz already exists; skipping fresh install"
  else
    echo
    echo "==> linkerd viz install | kubectl apply -f -"
    linkerd viz install | kubectl apply -f -
  fi

  run linkerd check
  run linkerd viz check

  run kubectl label namespace emojivoto linkerd.io/inject=enabled --overwrite
  run kubectl annotate namespace emojivoto linkerd.io/inject=enabled --overwrite

  if [[ "$BUILD_TELEMETRY_IMAGE" == "1" ]]; then
    run docker build -t telemetry-api:latest "$ROOT_DIR/telemetry-api"
  fi
  run kind load docker-image telemetry-api:latest --name "$CLUSTER_NAME"

  run kubectl apply -f "$ROOT_DIR/telemetry-api/prom-rbac.yaml"
  run kubectl apply -f "$ROOT_DIR/telemetry-api/telemetry-deployment.yaml"

  run kubectl rollout restart deployment -n emojivoto emoji vote-bot voting web
  run kubectl rollout restart deployment -n default telemetry-api

  run kubectl rollout status deployment/emoji -n emojivoto
  run kubectl rollout status deployment/vote-bot -n emojivoto
  run kubectl rollout status deployment/voting -n emojivoto
  run kubectl rollout status deployment/web -n emojivoto
  run kubectl rollout status deployment/telemetry-api -n default

  echo
  echo "Live telemetry bootstrap complete."
  echo "Next terminal:"
  echo "  kubectl port-forward svc/telemetry-service 8080:80"
}

main

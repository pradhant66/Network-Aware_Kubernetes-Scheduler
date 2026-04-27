#!/bin/bash

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-topo-cluster}"
TOPOLOGY_PRESET="${TOPOLOGY_PRESET:-${1:-simple-3node}}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TOPOLOGY_DIR="$ROOT_DIR/topologies"
KIND_CONFIG_FILE="$TOPOLOGY_DIR/${TOPOLOGY_PRESET}.kind.yaml"
LABELS_FILE="$TOPOLOGY_DIR/${TOPOLOGY_PRESET}.labels.tsv"

usage() {
  cat <<EOF
Usage: $0 [topology-preset]

Available presets:
  simple-3node
  single-zone-3rack
  three-zone-3worker
  two-zone-4worker
  three-zone-6worker

Examples:
  $0
  $0 two-zone-4worker
  TOPOLOGY_PRESET=three-zone-6worker $0
EOF
}

if [[ "${1:-}" == "--list" ]]; then
  find "$TOPOLOGY_DIR" -name "*.kind.yaml" -maxdepth 1 -type f -print \
    | sed 's#.*/##' \
    | sed 's/\.kind\.yaml$//' \
    | sort
  exit 0
fi

if [[ "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ ! -f "$KIND_CONFIG_FILE" || ! -f "$LABELS_FILE" ]]; then
  echo "unknown topology preset: $TOPOLOGY_PRESET" >&2
  echo >&2
  usage >&2
  exit 1
fi

echo "🚀 Step 1: Creating Kind cluster preset=$TOPOLOGY_PRESET..."
kind create cluster --name "$CLUSTER_NAME" --config "$KIND_CONFIG_FILE"

echo "🏷️ Step 2: Applying topology labels..."
while IFS=$'\t' read -r node_name region zone rack; do
  [[ -z "$node_name" ]] && continue
  kubectl label node "$node_name" \
    topology.kubernetes.io/region="$region" \
    topology.kubernetes.io/zone="$zone" \
    topology.kubernetes.io/rack="$rack" \
    --overwrite
done < "$LABELS_FILE"

echo "📦 Step 3: Deploying the Emojivoto demo application..."
kubectl apply -f https://run.linkerd.io/emojivoto.yml

echo "⏳ Waiting for deployments to be available..."
kubectl wait --for=condition=available deployment --all -n emojivoto --timeout=90s

echo "✅ Cluster setup complete! Topology preset '$TOPOLOGY_PRESET' is active."

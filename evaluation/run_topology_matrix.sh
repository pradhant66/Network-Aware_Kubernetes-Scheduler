#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RESULTS_ROOT="${RESULTS_ROOT:-$ROOT_DIR/evaluation/results}"
TOPOLOGIES=("$@")

if [[ ${#TOPOLOGIES[@]} -eq 0 ]]; then
  TOPOLOGIES=(
    "simple-3node"
    "single-zone-3rack"
    "three-zone-3worker"
    "two-zone-4worker"
    "three-zone-6worker"
  )
fi

LIVE_GRAPH_URL="${LIVE_GRAPH_URL:-http://127.0.0.1:8080/api/v1/traffic}"
USE_LIVE_GRAPH="${USE_LIVE_GRAPH:-1}"
ENABLE_BURST="${ENABLE_BURST:-1}"
ENABLE_HOTSPOT="${ENABLE_HOTSPOT:-1}"
BUILD_SCHEDULER="${BUILD_SCHEDULER:-0}"
BUILD_TELEMETRY_IMAGE="${BUILD_TELEMETRY_IMAGE:-1}"
AUTO_BOOTSTRAP="${AUTO_BOOTSTRAP:-1}"
CLUSTER_NAME="${CLUSTER_NAME:-topo-cluster}"
PORT_FORWARD_PID=""

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

cleanup() {
  if [[ -n "$PORT_FORWARD_PID" ]] && kill -0 "$PORT_FORWARD_PID" >/dev/null 2>&1; then
    kill "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
    wait "$PORT_FORWARD_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

check_live_telemetry() {
  curl -fsS -o /dev/null "$LIVE_GRAPH_URL"
}

live_graph_has_entries() {
  curl -fsS "$LIVE_GRAPH_URL" | python3 -c 'import json,sys; data=json.load(sys.stdin); raise SystemExit(0 if isinstance(data, list) and len(data) > 0 else 1)'
}

delete_cluster_if_exists() {
  if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
    run kind delete cluster --name "$CLUSTER_NAME"
  fi
}

start_port_forward() {
  local log_file="$ROOT_DIR/evaluation/telemetry-port-forward.log"
  if lsof -tiTCP:8080 -sTCP:LISTEN >/dev/null 2>&1; then
    local stale_pid
    stale_pid="$(lsof -tiTCP:8080 -sTCP:LISTEN | head -n 1)"
    echo "stopping stale port-forward on 8080 (pid=$stale_pid)"
    kill "$stale_pid" >/dev/null 2>&1 || true
    sleep 1
  fi
  : >"$log_file"
  (
    cd "$ROOT_DIR"
    kubectl port-forward -n default svc/telemetry-service 8080:80
  ) >"$log_file" 2>&1 &
  PORT_FORWARD_PID=$!
}

main() {
  require_cmd kind
  require_cmd kubectl
  require_cmd curl

  mkdir -p "$RESULTS_ROOT"

  for topology in "${TOPOLOGIES[@]}"; do
    delete_cluster_if_exists
    run "$ROOT_DIR/cluster-setup.sh" "$topology"

    if [[ "$AUTO_BOOTSTRAP" == "1" ]]; then
      BUILD_TELEMETRY_IMAGE="$BUILD_TELEMETRY_IMAGE" CLUSTER_NAME="$CLUSTER_NAME" \
        run "$ROOT_DIR/evaluation/bootstrap_live_telemetry.sh"
    fi

    start_port_forward
    echo
    echo "Waiting for live telemetry endpoint: $LIVE_GRAPH_URL"

    local tries=0
    until check_live_telemetry; do
      tries=$((tries + 1))
      if [[ $tries -ge 30 ]]; then
        echo "live telemetry endpoint is still unavailable after waiting." >&2
        echo "See evaluation/telemetry-port-forward.log for port-forward details." >&2
        exit 1
      fi
      sleep 2
    done

    echo
    echo "Waiting for non-empty live traffic graph..."
    tries=0
    until live_graph_has_entries; do
      tries=$((tries + 1))
      if [[ $tries -ge 60 ]]; then
        echo "live traffic graph is still empty after waiting." >&2
        echo "See evaluation/telemetry-port-forward.log and verify load is reaching emojivoto." >&2
        exit 1
      fi
      sleep 2
    done

    run env \
      RESULT_DIR="$RESULTS_ROOT/${topology}-live" \
      USE_LIVE_GRAPH="$USE_LIVE_GRAPH" \
      LIVE_GRAPH_URL="$LIVE_GRAPH_URL" \
      BUILD_SCHEDULER="$BUILD_SCHEDULER" \
      ENABLE_BURST="$ENABLE_BURST" \
      ENABLE_HOTSPOT="$ENABLE_HOTSPOT" \
      "$ROOT_DIR/evaluation/run_mode_comparison.sh" default network-only cpu-proximity centrality pid

    run env \
      MPLCONFIGDIR="$ROOT_DIR/.mpl-cache" \
      XDG_CACHE_HOME="$ROOT_DIR/.mpl-cache" \
      python3 "$ROOT_DIR/evaluation/plot_scheduler_results.py" --results-dir "$RESULTS_ROOT/${topology}-live"
  done

  run python3 "$ROOT_DIR/evaluation/summarize_topology_results.py" --results-root "$RESULTS_ROOT"
}

main

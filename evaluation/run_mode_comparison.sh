#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVAL_DIR="$ROOT_DIR/evaluation"
RESULT_DIR="${RESULT_DIR:-$EVAL_DIR}"
GRAPH_FILE="${GRAPH_FILE:-$ROOT_DIR/topology_graph.json}"
LIVE_GRAPH_URL="${LIVE_GRAPH_URL:-http://127.0.0.1:8080/api/v1/traffic}"
USE_LIVE_GRAPH="${USE_LIVE_GRAPH:-0}"
PROMETHEUS_BASE_URL="${PROMETHEUS_BASE_URL:-http://localhost:9090/api/v1/query?query=}"
BUILD_SCHEDULER="${BUILD_SCHEDULER:-1}"
ENABLE_BURST="${ENABLE_BURST:-1}"
ENABLE_HOTSPOT="${ENABLE_HOTSPOT:-1}"

MODES=("$@")
if [[ ${#MODES[@]} -eq 0 ]]; then
  MODES=("default" "network-only" "cpu-proximity" "centrality")
fi

SCHEDULER_PID=""

cleanup() {
  if [[ -n "${SCHEDULER_PID}" ]] && kill -0 "${SCHEDULER_PID}" >/dev/null 2>&1; then
    kill "${SCHEDULER_PID}" >/dev/null 2>&1 || true
    wait "${SCHEDULER_PID}" 2>/dev/null || true
  fi
}
trap cleanup EXIT

stop_listener_on_port() {
  local port="$1"
  local pid=""
  pid="$(lsof -tiTCP:"$port" -sTCP:LISTEN 2>/dev/null | head -n 1 || true)"
  if [[ -n "$pid" ]]; then
    echo "stopping stale listener on port $port (pid=$pid)"
    kill "$pid" >/dev/null 2>&1 || true
    sleep 1
    if kill -0 "$pid" >/dev/null 2>&1; then
      kill -9 "$pid" >/dev/null 2>&1 || true
    fi
  fi
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

live_graph_has_entries() {
  curl -fsS "$LIVE_GRAPH_URL" | python3 -c 'import json,sys; data=json.load(sys.stdin); raise SystemExit(0 if isinstance(data, list) and len(data) > 0 else 1)'
}

wait_for_live_graph() {
  local tries=0
  echo
  echo "==> waiting for non-empty live traffic graph: $LIVE_GRAPH_URL"
  until live_graph_has_entries; do
    tries=$((tries + 1))
    if [[ $tries -ge 45 ]]; then
      echo "live traffic graph is still empty after waiting." >&2
      echo "verify telemetry is healthy and burst/load traffic is actually reaching emojivoto." >&2
      exit 1
    fi
    sleep 2
  done
}

run() {
  echo
  echo "==> $*"
  "$@"
}

result_file_for_mode() {
  case "$1" in
    default) echo "$RESULT_DIR/default-result.json" ;;
    network-only) echo "$RESULT_DIR/topo-network-result.json" ;;
    cpu-proximity) echo "$RESULT_DIR/topo-cpu-result.json" ;;
    centrality) echo "$RESULT_DIR/topo-centrality-result.json" ;;
    pid) echo "$RESULT_DIR/topo-pid-result.json" ;;
    *) echo "$RESULT_DIR/$1-result.json" ;;
  esac
}

graph_snapshot_file_for_mode() {
  case "$1" in
    default) echo "$RESULT_DIR/default-graph.json" ;;
    network-only) echo "$RESULT_DIR/topo-network-graph.json" ;;
    cpu-proximity) echo "$RESULT_DIR/topo-cpu-graph.json" ;;
    centrality) echo "$RESULT_DIR/topo-centrality-graph.json" ;;
    pid) echo "$RESULT_DIR/topo-pid-graph.json" ;;
    *) echo "$RESULT_DIR/$1-graph.json" ;;
  esac
}

ensure_supporting_workloads() {
  if [[ "$ENABLE_BURST" == "1" ]]; then
    run kubectl apply -f "$ROOT_DIR/loadgen-burst.yaml"
  fi
  if [[ "$ENABLE_HOTSPOT" == "1" ]]; then
    run kubectl apply -f "$EVAL_DIR/cpu-hotspot-worker.yaml"
    run kubectl rollout status deployment/cpu-hotspot-worker -n emojivoto
  fi
}

refresh_graph() {
  if [[ "$USE_LIVE_GRAPH" == "1" ]]; then
    run curl -fsS -o /dev/null "$LIVE_GRAPH_URL"
    wait_for_live_graph
  else
    run env PROMETHEUS_BASE_URL="$PROMETHEUS_BASE_URL" python3 "$ROOT_DIR/gen_graph.py"
  fi
}

save_graph_snapshot() {
  local mode="$1"
  local out_file
  out_file="$(graph_snapshot_file_for_mode "$mode")"
  if [[ "$USE_LIVE_GRAPH" == "1" ]]; then
    run curl -fsS "$LIVE_GRAPH_URL" -o "$out_file"
  else
    cp "$GRAPH_FILE" "$out_file"
  fi
}

assess_args() {
  if [[ "$USE_LIVE_GRAPH" == "1" ]]; then
    printf -- "--graph-url %s" "$LIVE_GRAPH_URL"
  else
    printf -- "--graph-file %s" "$GRAPH_FILE"
  fi
}

cleanup_eval_deployments() {
  run kubectl delete deployment -n emojivoto web-like-default --ignore-not-found
  run kubectl delete deployment -n emojivoto web-like-topo --ignore-not-found
}

start_scheduler() {
  local mode="$1"
  local log_file="$RESULT_DIR/${mode}-scheduler.log"
  local secure_port="10359"
  case "$mode" in
    network-only) secure_port="10359" ;;
    cpu-proximity) secure_port="10369" ;;
    centrality) secure_port="10379" ;;
    pid) secure_port="10389" ;;
  esac
  cleanup
  stop_listener_on_port "$secure_port"
  : >"$log_file"
  echo
  echo "==> starting topo-scheduler in mode=$mode secure-port=$secure_port"
  (
    cd "$ROOT_DIR"
    if [[ "$USE_LIVE_GRAPH" == "1" ]]; then
      TOPO_SCORING_MODE="$mode" \
      TOPO_TRAFFIC_GRAPH_URL="$LIVE_GRAPH_URL" \
      ./bin/kube-scheduler --config=scheduler-config.yaml --secure-port="$secure_port" --v=4
    else
      TOPO_SCORING_MODE="$mode" \
      TOPO_TRAFFIC_GRAPH_FILE="$GRAPH_FILE" \
      ./bin/kube-scheduler --config=scheduler-config.yaml --secure-port="$secure_port" --v=4
    fi
  ) >"$log_file" 2>&1 &
  SCHEDULER_PID=$!
  sleep 4
  if ! kill -0 "${SCHEDULER_PID}" >/dev/null 2>&1; then
    echo "scheduler exited unexpectedly; see $log_file" >&2
    exit 1
  fi
}

run_default_mode() {
  cleanup_eval_deployments
  refresh_graph
  save_graph_snapshot default
  run kubectl apply -f "$EVAL_DIR/web-like-default.yaml"
  run kubectl rollout status deployment/web-like-default -n emojivoto
  local out_file
  local graph_args
  out_file="$(result_file_for_mode default)"
  graph_args="$(assess_args)"
  echo
  echo "==> python3 $EVAL_DIR/assess_run.py --namespace emojivoto --selector evaluation-run=default $graph_args"
  # shellcheck disable=SC2086
  python3 "$EVAL_DIR/assess_run.py" --namespace emojivoto --selector evaluation-run=default $graph_args | tee "$out_file"
  echo "saved default results to $out_file"
}

run_topo_mode() {
  local mode="$1"
  cleanup_eval_deployments
  refresh_graph
  save_graph_snapshot "$mode"
  start_scheduler "$mode"
  run kubectl apply -f "$EVAL_DIR/web-like-topo.yaml"
  run kubectl rollout status deployment/web-like-topo -n emojivoto
  local out_file
  local graph_args
  out_file="$(result_file_for_mode "$mode")"
  graph_args="$(assess_args)"
  echo
  echo "==> python3 $EVAL_DIR/assess_run.py --namespace emojivoto --selector evaluation-run=topo $graph_args"
  # shellcheck disable=SC2086
  python3 "$EVAL_DIR/assess_run.py" --namespace emojivoto --selector evaluation-run=topo $graph_args | tee "$out_file"
  echo "saved $mode results to $out_file"
}

print_summary() {
  echo
  echo "==> summary"
  python3 - "$RESULT_DIR" <<'PY'
import json
import sys
from pathlib import Path

base = Path(sys.argv[1])
files = [
    ("default", base / "default-result.json"),
    ("network-only", base / "topo-network-result.json"),
    ("cpu-proximity", base / "topo-cpu-result.json"),
    ("centrality", base / "topo-centrality-result.json"),
    ("pid", base / "topo-pid-result.json"),
]

for label, path in files:
    if not path.exists():
        continue
    data = json.loads(path.read_text())
    print(label)
    print("  total_weighted_cost:", data["total_weighted_cost"])
    print("  edge_counts:", data["edge_counts"])
    print()
PY
}

main() {
  require_cmd kubectl
  require_cmd python3
  require_cmd make
  if [[ "$USE_LIVE_GRAPH" == "1" ]]; then
    require_cmd curl
  fi
  mkdir -p "$RESULT_DIR"

  if [[ "$BUILD_SCHEDULER" == "1" ]]; then
    run make -C "$ROOT_DIR" build
  fi

  ensure_supporting_workloads

  for mode in "${MODES[@]}"; do
    case "$mode" in
      default)
        run_default_mode
        ;;
      network-only|cpu-proximity|centrality|pid)
        run_topo_mode "$mode"
        ;;
      *)
        echo "unsupported mode: $mode" >&2
        exit 1
        ;;
    esac
  done

  print_summary
}

main

import urllib.request
import urllib.parse
import json
import sys
import subprocess

BASE_URL = 'http://localhost:9090/api/v1/query?query='

# -------------------------------
# Config
# -------------------------------
NAMESPACES = ["emojivoto"]   # add/remove namespaces here
RPS_THRESHOLD = 0.05                        # filter noise
MAX_EDGES     = 100                         # per-namespace cap

# Monitoring services to ignore — their traffic is scrape noise, not app traffic
NOISE_APPS = {"prometheus", "metrics-api", "tap", "tap-injector", "linkerd-proxy"}

# -------------------------------sleep 90 && python3 gen_graph.py
# Dynamic Prometheus Queries
# -------------------------------

def make_queries(namespace):
    return {
        "rps":         f'sum(rate(request_total{{namespace="{namespace}",direction="outbound"}}[1m]))by(pod,dst_pod)',
        "latency":     f'histogram_quantile(0.99,sum(rate(response_latency_ms_bucket{{namespace="{namespace}",direction="outbound"}}[1m]))by(le,pod,dst_pod))',
        "bytes":       f'sum(rate(tcp_write_bytes_total{{namespace="{namespace}",direction="outbound"}}[1m]))by(pod,dst_pod)',
        "errors":      f'sum(rate(response_total{{namespace="{namespace}",direction="outbound",classification="failure"}}[1m]))by(pod,dst_pod)',
        "connections": f'sum(tcp_active_connections{{namespace="{namespace}",direction="outbound"}})by(pod,dst_pod)',
        "retransmits": f'sum(rate(tcp_retransmits_total{{namespace="{namespace}",direction="outbound"}}[1m]))by(pod,dst_pod)',
    }

# -------------------------------
# Kubernetes Helpers
# -------------------------------

def get_pod_node_mapping():
    try:
        output = subprocess.check_output(
            ["kubectl", "get", "pods", "-A", "-o", "json"],
            text=True
        )
        data = json.loads(output)

        mapping = {}
        for item in data["items"]:
            pod  = item["metadata"]["name"]
            node = item["spec"].get("nodeName", "unknown")
            mapping[pod] = node

        return mapping
    except Exception as e:
        print(f"Error fetching pod-node mapping: {e}")
        sys.exit(1)

# -------------------------------
# Prometheus Fetch
# -------------------------------

def fetch_prom(query):
    url = BASE_URL + urllib.parse.quote(query)
    try:
        req = urllib.request.urlopen(url)
        return json.loads(req.read()).get('data', {}).get('result', [])
    except Exception as e:
        print(f"Error fetching data: {e}. Is Prometheus port-forward running?")
        sys.exit(1)

# -------------------------------
# Core Logic
# -------------------------------

def fetch_and_parse(namespace, pod_to_node):
    queries = make_queries(namespace)

    # STEP 1: Build graph ONLY from RPS
    rps_data = fetch_prom(queries["rps"])

    graph = {}

    for item in rps_data:
        metric = item['metric']
        val    = float(item['value'][1])

        if val < RPS_THRESHOLD:
            continue  # ignore noise

        source_pod = metric.get('pod')
        dest_pod   = metric.get('dst_pod')

        if not source_pod or not dest_pod:
            continue

        source_node = pod_to_node.get(source_pod, "unknown")
        dest_node   = pod_to_node.get(dest_pod,   "unknown")

        if source_node == "unknown" or dest_node == "unknown":
            continue  # skip unmapped pods

        # Extract app name from pod name (strip last two hash segments)
        parts      = source_pod.split('-')
        source_app = '-'.join(parts[:-2]) if len(parts) >= 3 else source_pod

        # Skip monitoring/scrape traffic — it's noise, not real app traffic
        if source_app in NOISE_APPS:
            continue

        # Initialize source
        if source_pod not in graph:
            graph[source_pod] = {
                "source_pod":  source_pod,
                "source_app":  source_app,
                "source_node": source_node,
                "namespace":   namespace,
                "target_nodes": {}
            }

        # Initialize target node bucket
        if dest_node not in graph[source_pod]["target_nodes"]:
            graph[source_pod]["target_nodes"][dest_node] = {
                "target_node": dest_node,
                "target_pods": {}
            }

        # Initialize target pod entry
        graph[source_pod]["target_nodes"][dest_node]["target_pods"][dest_pod] = {
            "target_pod":           dest_pod,
            "requests_per_second":  round(val, 2),
            "p99_latency_ms":       0.0,
            "bytes_per_second":     0.0,
            "errors_per_second":    0.0,
            "active_connections":   0,
            "retransmits_per_second": 0.0
        }

    # STEP 2: Enrich existing edges with additional metrics

    datasets = {
        "p99_latency_ms":         fetch_prom(queries["latency"]),
        "bytes_per_second":       fetch_prom(queries["bytes"]),
        "errors_per_second":      fetch_prom(queries["errors"]),
        "active_connections":     fetch_prom(queries["connections"]),
        "retransmits_per_second": fetch_prom(queries["retransmits"]),
    }

    for metric_name, data in datasets.items():
        for item in data:
            metric  = item['metric']
            val_str = item['value'][1]

            if val_str == "NaN":
                continue

            val        = float(val_str)
            source_pod = metric.get('pod')
            dest_pod   = metric.get('dst_pod')

            if source_pod not in graph:
                continue

            dest_node = pod_to_node.get(dest_pod, "unknown")
            if dest_node not in graph[source_pod]["target_nodes"]:
                continue

            if dest_pod not in graph[source_pod]["target_nodes"][dest_node]["target_pods"]:
                continue

            val = round(val, 2) if isinstance(val, float) else int(val)
            graph[source_pod]["target_nodes"][dest_node]["target_pods"][dest_pod][metric_name] = val

    # STEP 3: Flatten dict structure into arrays

    final_output = []

    for source_data in graph.values():
        target_nodes_array = []

        for node_data in source_data["target_nodes"].values():
            pods_array = list(node_data["target_pods"].values())
            if not pods_array:
                continue
            target_nodes_array.append({
                "target_node": node_data["target_node"],
                "target_pods": pods_array
            })

        if target_nodes_array:
            source_data["target_nodes"] = target_nodes_array
            final_output.append(source_data)

    # STEP 4: Sort by req/s and cap at MAX_EDGES

    all_edges = []
    for s in final_output:
        for n in s["target_nodes"]:
            for p in n["target_pods"]:
                all_edges.append((s, n, p))

    all_edges.sort(key=lambda x: x[2]["requests_per_second"], reverse=True)
    all_edges = all_edges[:MAX_EDGES]

    # Rebuild trimmed graph from top edges
    trimmed = {}
    for s, n, p in all_edges:
        key = s["source_pod"]

        if key not in trimmed:
            trimmed[key] = {**s, "target_nodes": {}}

        node = n["target_node"]
        if node not in trimmed[key]["target_nodes"]:
            trimmed[key]["target_nodes"][node] = {
                "target_node": node,
                "target_pods": []
            }

        trimmed[key]["target_nodes"][node]["target_pods"].append(p)

    final_clean = []
    for s in trimmed.values():
        s["target_nodes"] = list(s["target_nodes"].values())
        final_clean.append(s)

    return final_clean

# -------------------------------
# Main
# -------------------------------

if __name__ == "__main__":
    print("Generating clean topology graph...")

    # Fetch pod→node mapping once for all namespaces
    print("  Building pod-to-node mapping...")
    pod_to_node = get_pod_node_mapping()
    print(f"  Mapped {len(pod_to_node)} pods to nodes")

    all_results = []
    for ns in NAMESPACES:
        print(f"  Fetching namespace: {ns}...")
        results = fetch_and_parse(ns, pod_to_node)
        all_results.extend(results)
        print(f"  Found {len(results)} source pods in '{ns}'")

    with open('topology_graph.json', 'w') as f:
        json.dump(all_results, f, indent=2)

    print(f"\n✅ topology_graph.json generated!")
    print(f"   {len(all_results)} total source pods across {len(NAMESPACES)} namespaces: {NAMESPACES}")
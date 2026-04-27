#!/usr/bin/env python3
import argparse
import json
import subprocess
import sys
import urllib.request

SAME_NODE = 0.1
SAME_RACK = 1.0
SAME_AZ = 5.0
CROSS_AZ = 20.0

ZONE_LABEL = "topology.kubernetes.io/zone"
RACK_LABEL = "topology.kubernetes.io/rack"


def run_json(cmd):
    result = subprocess.run(cmd, capture_output=True, text=True, check=True)
    return json.loads(result.stdout)


def load_graph(url=None, path=None):
    if url:
        with urllib.request.urlopen(url) as response:
            return json.loads(response.read().decode("utf-8"))
    if path:
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    raise ValueError("either --graph-url or --graph-file is required")


def source_app_for_pod(pod):
    labels = pod.get("metadata", {}).get("labels", {})
    for key in ("app", "app.kubernetes.io/name", "run"):
        value = labels.get(key)
        if value:
            return value
    return pod["metadata"]["name"]


def distance_bucket(source_node, target_node, nodes):
    if source_node == target_node:
        return "same_node", SAME_NODE

    source = nodes[source_node]
    target = nodes[target_node]
    if source["rack"] and source["rack"] == target["rack"]:
        return "same_rack", SAME_RACK
    if source["zone"] and source["zone"] == target["zone"]:
        return "same_zone", SAME_AZ
    return "cross_zone", CROSS_AZ


def main():
    parser = argparse.ArgumentParser(description="Assess weighted placement cost for an evaluation run.")
    parser.add_argument("--namespace", default="emojivoto")
    parser.add_argument("--selector", required=True, help="Label selector for the evaluation pods")
    parser.add_argument("--graph-url")
    parser.add_argument("--graph-file")
    args = parser.parse_args()

    graph = load_graph(url=args.graph_url, path=args.graph_file)
    graph_by_source = {(entry.get("namespace"), entry.get("source_app")): entry for entry in graph}

    pods = run_json(["kubectl", "get", "pods", "-n", args.namespace, "-l", args.selector, "-o", "json"])
    all_pods = run_json(["kubectl", "get", "pods", "-n", args.namespace, "-o", "json"])
    nodes_json = run_json(["kubectl", "get", "nodes", "-o", "json"])

    pod_items = [pod for pod in pods["items"] if pod.get("spec", {}).get("nodeName")]
    if not pod_items:
        print("No scheduled pods matched the selector.", file=sys.stderr)
        sys.exit(1)

    nodes = {}
    for node in nodes_json["items"]:
        labels = node.get("metadata", {}).get("labels", {})
        nodes[node["metadata"]["name"]] = {
            "zone": labels.get(ZONE_LABEL, ""),
            "rack": labels.get(RACK_LABEL, ""),
        }

    pod_to_node = {
        pod["metadata"]["name"]: pod.get("spec", {}).get("nodeName", "")
        for pod in all_pods["items"]
    }

    summary = {
        "pod_count": len(pod_items),
        "total_weighted_cost": 0.0,
        "edge_counts": {
            "same_node": 0,
            "same_rack": 0,
            "same_zone": 0,
            "cross_zone": 0,
        },
        "edge_weighted_bytes": {
            "same_node": 0.0,
            "same_rack": 0.0,
            "same_zone": 0.0,
            "cross_zone": 0.0,
        },
        "pods": [],
    }

    for pod in sorted(pod_items, key=lambda item: item["metadata"]["name"]):
        pod_name = pod["metadata"]["name"]
        node_name = pod["spec"]["nodeName"]
        source_app = source_app_for_pod(pod)
        entry = graph_by_source.get((args.namespace, source_app))

        pod_cost = 0.0
        dependency_details = []
        if entry:
            for dep in entry.get("traffic_dependencies", []):
                target_pod = dep["target_pod"]
                target_node = pod_to_node.get(target_pod)
                if not target_node or target_node not in nodes or node_name not in nodes:
                    bucket, distance = "cross_zone", CROSS_AZ
                else:
                    bucket, distance = distance_bucket(node_name, target_node, nodes)

                bytes_per_second = float(dep.get("bytes_per_second", 0.0))
                edge_cost = bytes_per_second * distance
                pod_cost += edge_cost
                summary["edge_counts"][bucket] += 1
                summary["edge_weighted_bytes"][bucket] += bytes_per_second
                dependency_details.append({
                    "target_pod": target_pod,
                    "target_node": target_node,
                    "distance_bucket": bucket,
                    "distance_multiplier": distance,
                    "bytes_per_second": round(bytes_per_second, 2),
                    "edge_cost": round(edge_cost, 2),
                })

        summary["total_weighted_cost"] += pod_cost
        summary["pods"].append({
            "pod": pod_name,
            "node": node_name,
            "source_app": source_app,
            "weighted_cost": round(pod_cost, 2),
            "dependencies": dependency_details,
        })

    summary["total_weighted_cost"] = round(summary["total_weighted_cost"], 2)
    for key, value in summary["edge_weighted_bytes"].items():
        summary["edge_weighted_bytes"][key] = round(value, 2)

    print(json.dumps(summary, indent=2))


if __name__ == "__main__":
    main()

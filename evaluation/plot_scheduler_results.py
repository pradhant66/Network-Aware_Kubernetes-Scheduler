#!/usr/bin/env python3

import argparse
import json
from pathlib import Path

import matplotlib.pyplot as plt


MODE_SPECS = [
    ("default", "default-result.json", "default-graph.json"),
    ("network-only", "topo-network-result.json", "topo-network-graph.json"),
    ("cpu-proximity", "topo-cpu-result.json", "topo-cpu-graph.json"),
    ("centrality", "topo-centrality-result.json", "topo-centrality-graph.json"),
    ("pid", "topo-pid-result.json", "topo-pid-graph.json"),
]

EDGE_BUCKETS = ["same_node", "same_rack", "same_zone", "cross_zone"]
EDGE_COLORS = {
    "same_node": "#2E8B57",
    "same_rack": "#7BC96F",
    "same_zone": "#F4C542",
    "cross_zone": "#D64541",
}


def read_json(path: Path):
    if not path.exists():
        return None
    return json.loads(path.read_text())


def collect_mode_data(results_dir: Path):
    rows = []
    for mode, result_name, graph_name in MODE_SPECS:
        result = read_json(results_dir / result_name)
        if not result:
            continue
        graph = read_json(results_dir / graph_name)
        rows.append(
            {
                "mode": mode,
                "result": result,
                "graph": graph or [],
            }
        )
    if not rows:
        raise SystemExit(f"no result files found in {results_dir}")
    return rows


def extract_latency_stats(graph_entries):
    latencies = []
    per_edge = {}
    for entry in graph_entries:
        src = entry.get("source_app", "unknown")
        for dep in entry.get("traffic_dependencies", []):
            p99 = dep.get("p99_latency_ms")
            if p99 is None:
                continue
            latencies.append(float(p99))
            edge_name = f"{src}->{dep.get('target_pod', dep.get('target_app', 'unknown'))}"
            per_edge.setdefault(edge_name, []).append(float(p99))
    avg_latency = sum(latencies) / len(latencies) if latencies else 0.0
    max_latency = max(latencies) if latencies else 0.0
    per_edge_avg = {name: sum(vals) / len(vals) for name, vals in per_edge.items()}
    return avg_latency, max_latency, per_edge_avg


def plot_total_cost(rows, output_dir: Path):
    modes = [r["mode"] for r in rows]
    costs = [r["result"]["total_weighted_cost"] for r in rows]
    colors = ["#4C78A8", "#59A14F", "#F28E2B", "#E15759", "#B07AA1"][: len(modes)]
    fig, ax = plt.subplots(figsize=(9, 5))
    ax.bar(modes, costs, color=colors)
    ax.set_title("Total Weighted Network Cost by Scheduler")
    ax.set_ylabel("Weighted Cost")
    ax.set_xlabel("Scheduler Mode")
    ax.grid(axis="y", alpha=0.25)
    fig.tight_layout()
    fig.savefig(output_dir / "total_weighted_cost.png", dpi=180)
    plt.close(fig)

    positive_costs = [cost for cost in costs if cost > 0]
    if positive_costs:
        fig, ax = plt.subplots(figsize=(9, 5))
        ax.bar(modes, costs, color=colors)
        ax.set_yscale("log")
        ax.set_title("Total Weighted Network Cost by Scheduler (Log Scale)")
        ax.set_ylabel("Weighted Cost (log scale)")
        ax.set_xlabel("Scheduler Mode")
        ax.grid(axis="y", which="both", alpha=0.25)
        fig.tight_layout()
        fig.savefig(output_dir / "total_weighted_cost_log.png", dpi=180)
        plt.close(fig)

    if costs:
        baseline = costs[0]
        improvements = [0.0 if baseline == 0 else ((baseline - cost) / baseline) * 100.0 for cost in costs]
        fig, ax = plt.subplots(figsize=(9, 5))
        bars = ax.bar(modes, improvements, color=colors)
        ax.set_title("Improvement Over Default Scheduler")
        ax.set_ylabel("Improvement (%)")
        ax.set_xlabel("Scheduler Mode")
        ax.set_ylim(bottom=0)
        ax.grid(axis="y", alpha=0.25)
        for bar, value in zip(bars, improvements):
            ax.text(bar.get_x() + bar.get_width() / 2, value + 0.5, f"{value:.2f}%", ha="center", va="bottom", fontsize=9)
        fig.tight_layout()
        fig.savefig(output_dir / "improvement_vs_default.png", dpi=180)
        plt.close(fig)


def plot_edge_buckets(rows, output_dir: Path):
    modes = [r["mode"] for r in rows]
    fig, ax = plt.subplots(figsize=(10, 5))
    bottom = [0] * len(rows)
    for bucket in EDGE_BUCKETS:
        values = [r["result"]["edge_counts"].get(bucket, 0) for r in rows]
        ax.bar(modes, values, bottom=bottom, label=bucket.replace("_", " ").title(), color=EDGE_COLORS[bucket])
        bottom = [b + v for b, v in zip(bottom, values)]
    ax.set_title("Placement Buckets by Scheduler")
    ax.set_ylabel("Edge Count")
    ax.set_xlabel("Scheduler Mode")
    ax.legend()
    ax.grid(axis="y", alpha=0.25)
    fig.tight_layout()
    fig.savefig(output_dir / "edge_buckets_stacked.png", dpi=180)
    plt.close(fig)


def plot_latency_summary(rows, output_dir: Path):
    modes = []
    avg_vals = []
    max_vals = []
    for row in rows:
        avg_latency, max_latency, _ = extract_latency_stats(row["graph"])
        modes.append(row["mode"])
        avg_vals.append(avg_latency)
        max_vals.append(max_latency)

    fig, ax = plt.subplots(figsize=(10, 5))
    xpos = list(range(len(modes)))
    width = 0.38
    ax.bar([x - width / 2 for x in xpos], avg_vals, width=width, label="Average p99 Latency", color="#4C78A8")
    ax.bar([x + width / 2 for x in xpos], max_vals, width=width, label="Max p99 Latency", color="#F28E2B")
    ax.set_xticks(xpos)
    ax.set_xticklabels(modes)
    ax.set_title("Observed Latency Snapshot by Scheduler")
    ax.set_ylabel("Latency (ms)")
    ax.set_xlabel("Scheduler Mode")
    ax.legend()
    ax.grid(axis="y", alpha=0.25)
    fig.tight_layout()
    fig.savefig(output_dir / "latency_summary.png", dpi=180)
    plt.close(fig)


def plot_per_edge_latency(rows, output_dir: Path):
    edge_names = []
    per_mode = {}
    for row in rows:
        _, _, edge_map = extract_latency_stats(row["graph"])
        per_mode[row["mode"]] = edge_map
        for edge_name in edge_map:
            if edge_name not in edge_names:
                edge_names.append(edge_name)

    if not edge_names:
        return

    fig, ax = plt.subplots(figsize=(12, 6))
    xpos = list(range(len(edge_names)))
    width = 0.8 / max(1, len(rows))
    for idx, row in enumerate(rows):
        mode = row["mode"]
        values = [per_mode.get(mode, {}).get(edge_name, 0.0) for edge_name in edge_names]
        ax.bar(
            [x - 0.4 + width / 2 + idx * width for x in xpos],
            values,
            width=width,
            label=mode,
        )
    ax.set_xticks(xpos)
    ax.set_xticklabels(edge_names, rotation=20, ha="right")
    ax.set_title("Per-Edge p99 Latency by Scheduler")
    ax.set_ylabel("Latency (ms)")
    ax.set_xlabel("Traffic Edge")
    ax.legend()
    ax.grid(axis="y", alpha=0.25)
    fig.tight_layout()
    fig.savefig(output_dir / "latency_per_edge.png", dpi=180)
    plt.close(fig)


def write_summary(rows, output_dir: Path):
    summary = []
    for row in rows:
        avg_latency, max_latency, edge_map = extract_latency_stats(row["graph"])
        summary.append(
            {
                "mode": row["mode"],
                "total_weighted_cost": row["result"]["total_weighted_cost"],
                "edge_counts": row["result"]["edge_counts"],
                "average_p99_latency_ms": avg_latency,
                "max_p99_latency_ms": max_latency,
                "per_edge_latency_ms": edge_map,
            }
        )
    (output_dir / "plot_summary.json").write_text(json.dumps(summary, indent=2))


def main():
    parser = argparse.ArgumentParser(description="Plot scheduler comparison graphs from one result directory.")
    parser.add_argument("--results-dir", default="evaluation", help="Directory containing result and graph JSON files")
    parser.add_argument("--output-dir", default=None, help="Directory to write PNG files into")
    args = parser.parse_args()

    results_dir = Path(args.results_dir)
    output_dir = Path(args.output_dir) if args.output_dir else results_dir / "plots"
    output_dir.mkdir(parents=True, exist_ok=True)

    rows = collect_mode_data(results_dir)
    plot_total_cost(rows, output_dir)
    plot_edge_buckets(rows, output_dir)
    plot_latency_summary(rows, output_dir)
    plot_per_edge_latency(rows, output_dir)
    write_summary(rows, output_dir)

    print(f"wrote plots to {output_dir}")


if __name__ == "__main__":
    main()

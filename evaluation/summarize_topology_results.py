#!/usr/bin/env python3

import argparse
import json
from pathlib import Path


MODE_FILES = {
    "default": "default-result.json",
    "network-only": "topo-network-result.json",
    "cpu-proximity": "topo-cpu-result.json",
    "centrality": "topo-centrality-result.json",
    "pid": "topo-pid-result.json",
}


def load_result(path: Path):
    if not path.exists():
        return None
    return json.loads(path.read_text())


def pct_improvement(baseline: float, value: float):
    if baseline == 0:
        return 0.0
    return ((baseline - value) / baseline) * 100.0


def format_float(value):
    return f"{value:.2f}"


def collect_rows(results_root: Path):
    rows = []
    for topology_dir in sorted(p for p in results_root.iterdir() if p.is_dir()):
        loaded = {mode: load_result(topology_dir / filename) for mode, filename in MODE_FILES.items()}
        default = loaded.get("default")
        if not default:
            continue
        baseline = default["total_weighted_cost"]
        for mode, data in loaded.items():
            if not data:
                continue
            rows.append(
                {
                    "topology": topology_dir.name,
                    "mode": mode,
                    "cost": data["total_weighted_cost"],
                    "improvement_pct": pct_improvement(baseline, data["total_weighted_cost"]),
                    "same_node": data["edge_counts"].get("same_node", 0),
                    "same_rack": data["edge_counts"].get("same_rack", 0),
                    "same_zone": data["edge_counts"].get("same_zone", 0),
                    "cross_zone": data["edge_counts"].get("cross_zone", 0),
                }
            )
    return rows


def print_markdown(rows):
    print("| Topology | Mode | Total Cost | Improvement vs Default | Same Node | Same Rack | Same Zone | Cross Zone |")
    print("|---|---|---:|---:|---:|---:|---:|---:|")
    for row in rows:
        print(
            "| {topology} | {mode} | {cost} | {improvement} | {same_node} | {same_rack} | {same_zone} | {cross_zone} |".format(
                topology=row["topology"],
                mode=row["mode"],
                cost=format_float(row["cost"]),
                improvement=f'{row["improvement_pct"]:.2f}%',
                same_node=row["same_node"],
                same_rack=row["same_rack"],
                same_zone=row["same_zone"],
                cross_zone=row["cross_zone"],
            )
        )


def print_json(rows):
    print(json.dumps(rows, indent=2))


def main():
    parser = argparse.ArgumentParser(description="Summarize scheduler comparison results across topology directories.")
    parser.add_argument(
        "--results-root",
        default="evaluation/results",
        help="Directory containing per-topology result folders",
    )
    parser.add_argument(
        "--format",
        choices=("markdown", "json"),
        default="markdown",
        help="Output format",
    )
    args = parser.parse_args()

    results_root = Path(args.results_root)
    if not results_root.exists():
        raise SystemExit(f"results root does not exist: {results_root}")

    rows = collect_rows(results_root)
    if not rows:
        raise SystemExit("no topology result directories with default-result.json found")

    if args.format == "json":
        print_json(rows)
    else:
        print_markdown(rows)


if __name__ == "__main__":
    main()

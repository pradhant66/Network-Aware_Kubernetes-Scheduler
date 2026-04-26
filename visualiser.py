import json
import networkx as nx
import matplotlib.pyplot as plt
from matplotlib.cm import ScalarMappable
from matplotlib.colors import Normalize
import matplotlib.patches as mpatches
from collections import defaultdict
import re

# Load telemetry JSON
with open("topology_graph.json", "r") as f:
    telemetry = json.load(f)

# Map real pod names -> short readable names
pod_aliases = {}
pod_counters = defaultdict(int)

def get_short_name(pod_name):
    # Extract deployment/service prefix
    match = re.match(r"([a-zA-Z0-9-]+?)-[a-z0-9]{8,10}-[a-z0-9]{5}", pod_name)

    if match:
        base_name = match.group(1)
    else:
        # fallback: take everything before last dash
        base_name = "-".join(pod_name.split("-")[:-1])

    if pod_name not in pod_aliases:
        pod_counters[base_name] += 1
        pod_aliases[pod_name] = f"{base_name}-{pod_counters[base_name]}"

    return pod_aliases[pod_name]

# Create directed graph
G = nx.DiGraph()

# Track node groups (physical k8s nodes)
node_groups = {}

# Parse telemetry
for source in telemetry:
    source_pod = get_short_name(source["source_pod"])
    source_node = source["source_node"]

    G.add_node(source_pod)
    node_groups[source_pod] = source_node

    for target_group in source["target_nodes"]:
        target_node = target_group["target_node"]

        for target in target_group["target_pods"]:
            target_pod = get_short_name(target["target_pod"])

            G.add_node(target_pod)
            node_groups[target_pod] = target_node

            G.add_edge(
                source_pod,
                target_pod,
                rps=target["requests_per_second"],
                latency=target["p99_latency_ms"],
                bytes_sec=target["bytes_per_second"]
            )

# Color map for physical nodes
unique_nodes = list(set(node_groups.values()))
palette = plt.cm.Set3.colors
node_color_map = {
    node: palette[i % len(palette)]
    for i, node in enumerate(unique_nodes)
}

node_colors = [
    node_color_map[node_groups[node]]
    for node in G.nodes()
]

# Edge thickness based on RPS
edge_widths = [
    max(1, G[u][v]["rps"] * 8)
    for u, v in G.edges()
]


# Layout
pos = nx.spring_layout(
    G,
    k=3.5,   # more spacing
    iterations=300,
    seed=42
)

# Create figure
fig, ax = plt.subplots(figsize=(18, 12))
ax.set_facecolor("#fafafa")

# Draw nodes
nx.draw_networkx_nodes(
    G,
    pos,
    node_color=node_colors,
    node_size=1200,
    edgecolors="black",
    linewidths=1
)

# Edge weights based on requests/sec
rps_values = [
    G[u][v]["rps"]
    for u, v in G.edges()
]

# Normalize RPS for colormap
norm = plt.Normalize(
    vmin=min(rps_values),
    vmax=max(rps_values)
)

# Blue gradient for traffic intensity
cmap = plt.cm.Blues

edge_colors = [
    cmap(norm(G[u][v]["rps"]))
    for u, v in G.edges()
]

# Draw edges
nx.draw_networkx_edges(
    G,
    pos,
    edge_color=edge_colors,
    width=edge_widths,
    arrows=True,
    arrowsize=20,
    alpha=0.85,
    connectionstyle="arc3,rad=0.15"
)

# Draw labels
nx.draw_networkx_labels(
    G,
    pos,
    font_size=9,
    font_weight="bold"
)

# Edge labels (RPS + latency)
edge_labels = {
    (u, v): f"{G[u][v]['rps']} rps"
    for u, v in G.edges()
}

edge_widths = [
    max(1, G[u][v]["rps"] * 10)
    for u, v in G.edges()
]

nx.draw_networkx_edge_labels(
    G,
    pos,
    edge_labels=edge_labels,
    font_size=7
)

sm = plt.cm.ScalarMappable(
    norm=norm,
    cmap=cmap
)
sm.set_array([])

cbar = fig.colorbar(
    sm,
    ax=ax,
    fraction=0.046,
    pad=0.04
)

cbar.set_label("Requests Per Second (RPS)")

# Legend for Kubernetes nodes
legend_handles = [
    mpatches.Patch(
        color=node_color_map[node],
        label=node
    )
    for node in unique_nodes
]

plt.legend(
    handles=legend_handles,
    title="Kubernetes Nodes",
    loc="upper left"
)

plt.title(
    "Service Mesh Telemetry Graph\n(Edge width & color = Requests Per Second)",
    fontsize=18,
    fontweight="bold"
)

plt.axis("off")
plt.tight_layout()
plt.savefig("telemetry_graph.png", dpi=300)
plt.show()
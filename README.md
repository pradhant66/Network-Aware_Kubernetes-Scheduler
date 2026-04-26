# Network-Aware Scheduler: Observation Layer (Telemetry API)

This repository contains the infrastructure and microservice code for the **Observation Layer** of a custom Network-Aware Kubernetes Scheduler.

The system uses the **Linkerd Service Mesh** to capture east-west traffic telemetry between pods and exposes the live traffic graph through a Python Flask API. That graph is consumed by the scheduler to make topology-aware placement decisions.

---

## Prerequisites

Install the following locally:
- [Docker Desktop](https://www.docker.com/products/docker-desktop/) (4.x or later) on macOS
- [Kind](https://kind.sigs.k8s.io/) v0.20.0
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Linkerd CLI](https://linkerd.io/2.14/getting-started/) v2.14.x

> **Apple Silicon note:** Kind requires Docker Desktop with `cgroupfs` as the cgroup driver. Do not set `native.cgroupdriver=systemd` in Docker Engine settings — it will prevent worker nodes from joining.

---

## Cluster Topology

The Kind cluster simulates a multi-rack, multi-zone data center topology using node labels applied by Person 2. The logical structure is:

```
Region: us-east-1
  Zone: us-east-1a  →  rack-1  (2 worker nodes)
  Zone: us-east-1b  →  rack-2  (2 worker nodes)
Region: us-west-2
  Zone: us-west-2a  →  rack-3  (1 worker node)
  Zone: us-west-2b  →  rack-4  (2 worker nodes)
```

Node labels are applied by Person 2 using:
```
topology.kubernetes.io/zone
topology.kubernetes.io/rack
```

---

## Setup and Run

### 1. Create the Kind Cluster

```bash
make
```

This creates a Kind cluster named `topology-cluster` using `kind-config.yaml`.

### 2. Install Linkerd

```bash
linkerd install --crds | kubectl apply -f -
linkerd install | kubectl apply -f -
linkerd check
```

Wait for all checks to pass before continuing. Version warnings (`unsupported version channel`) are safe to ignore.

### 3. Install Linkerd Viz (Prometheus)

```bash
linkerd viz install | kubectl apply -f -
linkerd viz check
```

### 4. Deploy Emojivoto

```bash
make deploy-demo
```

This deploys the emojivoto demo application into the `emojivoto` namespace. It includes four services:

- `web` — HTTP frontend, calls emoji-svc and voting-svc
- `emoji-svc` — serves emoji lists
- `voting-svc` — records votes (gRPC)
- `vote-bot` — simulates background user traffic

### 5. Inject Linkerd into Emojivoto

```bash
kubectl get deploy -n emojivoto -o yaml | linkerd inject - | kubectl apply -f -
```

### 6. Enable Linkerd Injection in Default Namespace

```bash
kubectl annotate namespace default linkerd.io/inject=enabled
```

This ensures the telemetry API pod gets a Linkerd sidecar automatically.

### 7. Build and Load the Telemetry API Image

```bash
cd telemetry-api
docker build --no-cache -t telemetry-api:latest .
kind load docker-image telemetry-api:latest --name topology-cluster
cd ..
```

### 8. Deploy the Telemetry API

```bash
kubectl apply -f telemetry-api/telemetry-deployment.yaml
kubectl rollout restart deployment/telemetry-api -n default
kubectl apply -f telemetry-api/prom-rbac.yaml
```

### 9. Deploy Load Generators

Load generators live in the `emojivoto` namespace so their outbound traffic appears in Linkerd metrics:

```bash
kubectl annotate namespace emojivoto linkerd.io/inject=enabled --overwrite
kubectl apply -f loadgen-deployment.yaml
kubectl apply -f loadgen-burst.yaml
```
---

## Traffic Architecture

```
emojivoto namespace
├── vote-bot   (1 pod)   → web-svc             ~0.3 rps (background)
├── loadgen    (10 pods) → web-svc             ~30 rps per pod (steady)
├── loadgen-burst (5 pods) → web-svc           ~300 rps during burst, 0 during quiet
├── web        (5 pods)  → emoji-svc, voting-svc
├── emoji-svc  (5 pods)  → receives only
└── voting-svc (5 pods)  → receives only
```

> **Important:** `voting-svc` uses gRPC and cannot be hit directly with curl. Traffic to `voting-svc` is driven indirectly by `web` on each vote request. Use the `/vote` endpoint on `web-svc` to trigger voting traffic.

### Service DNS Names (inside cluster)

| Service | DNS | Port |
|---|---|---|
| web | `web-svc.emojivoto.svc.cluster.local` | 80 |
| emoji | `emoji-svc.emojivoto.svc.cluster.local` | 8080 |
| voting | `voting-svc.emojivoto.svc.cluster.local` | 8080 |

---

## Scaling Traffic

Scale replicas for a richer graph:

```bash
kubectl scale deployment emoji voting web -n emojivoto --replicas=5
```

Scale load generators:

```bash
kubectl scale deployment loadgen -n emojivoto --replicas=10
```

Watch live traffic:

```bash
while true; do
  clear
  echo "=== $(date) ==="
  linkerd viz stat deployment -n emojivoto
  sleep 5
done
```

---

## Generating `topology_graph.json`

First port-forward Prometheus:

```bash
kubectl port-forward svc/prometheus -n linkerd-viz 9090:9090 &
```

Then run:

```bash
python3 gen_graph.py
```

This writes a metrics-enriched graph to `topology_graph.json`. Run continuously to keep it fresh:

```bash
while true; do
  python3 gen_graph.py
  sleep 30
done
```

Capture burst vs quiet snapshots for evaluation:

```bash
python3 gen_graph.py && cp topology_graph.json topology_graph_burst.json
# wait for quiet period
python3 gen_graph.py && cp topology_graph.json topology_graph_quiet.json
```

---

## Internal API Contract

The telemetry service is accessible inside the cluster at:

```
http://telemetry-service.default.svc.cluster.local/api/v1/traffic
```

Expose it locally for testing:

```bash
kubectl port-forward svc/telemetry-service 8080:80 -n default &
curl http://localhost:8080/api/v1/traffic | python3 -m json.tool
```

**Response fields per traffic dependency:**

| Field | Description |
|---|---|
| `bytes_per_second` | Outbound bandwidth from source to target |
| `p99_latency_ms` | 99th percentile latency for that path |
| `requests_per_second` | Request rate on that path |
| `errors_per_second` | Failure rate on that path |
| `active_connections` | Current open TCP connections |
| `retransmits_per_second` | TCP retransmission rate |

**Sample response:**

```json
[
  {
    "namespace": "emojivoto",
    "source_app": "vote-bot",
    "traffic_dependencies": [
      {
        "target_pod": "web-7d64655496-w6kw9",
        "bytes_per_second": 149.84,
        "p99_latency_ms": 8.37,
        "requests_per_second": 1.96,
        "errors_per_second": 0.18,
        "active_connections": 0,
        "retransmits_per_second": 0.0
      }
    ]
  }
]
```

---

## Understanding `topology_graph.json`

`topology_graph.json` is a runtime view of which pods are actively talking to which other pods, enriched with traffic metrics. It is not a static physical topology.

Each entry contains:
- `source_pod` — the calling pod
- `source_app` — the calling application name
- `source_node` — the Kubernetes node the source pod is running on
- `namespace` — the Kubernetes namespace
- `target_nodes` — grouped list of destination nodes and their pods

The scheduler uses this file to compute placement cost:

```
Cost(pod_p, node_n) = Σ traffic(p, q) × distance(n, node(q))
```

Where `distance` is determined by the rack/zone topology labels on each node.

---

## Validation

Verify the telemetry API from inside the cluster:

```bash
kubectl run -i --tty --rm debug --image=curlimages/curl \
  --restart=Never -n default -- \
  curl -s http://telemetry-service.default.svc.cluster.local/api/v1/traffic
```

Verify Linkerd is capturing traffic:

```bash
linkerd viz stat deployment -n emojivoto
```

All deployments should show non-zero RPS and `MESHED` column should show all replicas (e.g. `5/5`).

---

## Handoff to Person 2 and 3

**For Person 2** — node topology:
```bash
kubectl get nodes -o custom-columns="NODE:.metadata.name,ZONE:.metadata.labels.topology\.kubernetes\.io/zone,RACK:.metadata.labels.topology\.kubernetes\.io/rack"
```

**For Person 3** — live graph endpoint:
```bash
kubectl port-forward svc/telemetry-service 8080:80 -n default &
curl http://localhost:8080/api/v1/traffic
```

**For both** — Prometheus:
```bash
kubectl port-forward svc/prometheus -n linkerd-viz 9090:9090 &
```

---

## Cleanup

```bash
make clean
```

This deletes the Kind cluster and all resources inside it.
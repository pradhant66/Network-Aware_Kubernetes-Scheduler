# Network-Aware Scheduler: Observation Layer (Telemetry API)

This repository contains the infrastructure and microservice code for the **Observation Layer** of a custom Network-Aware Kubernetes Scheduler.

The system uses the **Linkerd Service Mesh** to capture east-west traffic telemetry between pods and exposes the live traffic graph through a Python Flask API. That graph can be consumed by a scheduler to make topology-aware placement decisions.

---

## Prerequisites

Install the following locally:
- Docker or [OrbStack](https://orbstack.dev/) on macOS
- [Kind](https://kind.sigs.k8s.io/)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Linkerd CLI](https://linkerd.io/2.14/getting-started/)

---

## Setup and Run

### 1. Create the Kind cluster
From the repository root:

```bash
make
```

This will:
- create a Kind cluster named `topology-cluster`
- apply worker node labels for topology testing

### 2. Choose and deploy a demo application

**Option A: Emojivoto (Simple - 4 services)**
```bash
make deploy-demo
```

**Option B: Online Boutique (Complex - 11 services)**
```bash
make deploy-boutique
```

### Topology structure
- **Region: us-east-1**
  - Zone: us-east-1a (Rack 1: 2 nodes)
  - Zone: us-east-1b (Rack 2: 2 nodes)
- **Region: us-west-2**
  - Zone: us-west-2a (Rack 1: 1 node)
  - Zone: us-west-2b (Rack 2: 2 nodes)

### 3. Install Linkerd and Viz
Install Linkerd control plane and observability components:

```bash
linkerd install --crds | kubectl apply -f -
linkerd install | kubectl apply -f -
linkerd check
linkerd viz install | kubectl apply -f -
```

### 4. Mesh the application

**For Emojivoto:**
```bash
kubectl get deploy -n emojivoto -o yaml | linkerd inject - | kubectl apply -f -
```

**For Online Boutique:**
```bash
kubectl get deploy -n default -o yaml | linkerd inject - | kubectl apply -f -
```

### Alternative: Online Boutique Demo Application

For a more complex microservice topology with 11 services (vs Emojivoto's 4), deploy Google's Online Boutique instead:

```bash
make deploy-boutique
```

This deploys a realistic e-commerce application with services like:
- `frontend`: Web frontend
- `recommendationservice`: Product recommendations
- `checkoutservice`: Checkout processing
- `cartservice`: Shopping cart (with Redis)
- `currencyservice`: Currency conversion
- `emailservice`: Email notifications
- `paymentservice`: Payment processing
- `productcatalogservice`: Product catalog
- `shippingservice`: Shipping calculations
- `adservice`: Ad serving
- `loadgenerator`: Built-in traffic generator

After deployment, mesh the application:

```bash
kubectl get deploy -n default -o yaml | linkerd inject - | kubectl apply -f -
```

To scale for larger graphs:

```bash
make scale-boutique
```

The Online Boutique provides a much richer topology for testing network-aware scheduling algorithms compared to Emojivoto's simple voting application.

### 5. Build and load the telemetry API image

```bash
cd telemetry-api
docker build --no-cache -t telemetry-api:latest .
kind load docker-image telemetry-api:latest --name topology-cluster
```

### 6. Deploy the telemetry API

```bash
cd ..
kubectl apply -f telemetry-api/telemetry-deployment.yaml
kubectl get deployment telemetry-api -o yaml | linkerd inject - | kubectl apply -f -
kubectl apply -f telemetry-api/prom-rbac.yaml
```

### 7. Expose the API locally for testing

The service port is `80` and the container listens on `8080`.

```bash
kubectl port-forward svc/telemetry-service 8080:80 -n default
```

Then test locally:

```bash
curl http://localhost:8080/api/v1/traffic | jq
```

---

## Scaling the Application and Traffic

To increase the size and complexity of the observed graph, scale the services and deploy traffic generators:

**For Emojivoto:**
```bash
make scale-apps
make loadgen
```

**For Online Boutique:**
```bash
make scale-boutique
# (loadgenerator is built-in)
```

This scales replicas to create more distinct source/target paths and deploys a pod that continuously requests the `web` service.

Once the traffic generator is running, regenerate the topology graph:

```bash
make generate-graph
```

---

## Internal API Contract

The telemetry service exposes traffic data internally at:

`http://telemetry-service.default.svc.cluster.local/api/v1/traffic`

Each source pod returns a list of outbound dependencies and metrics.

**Returned fields:**
- `bytes_per_second`
- `p99_latency_ms`
- `requests_per_second`
- `errors_per_second`
- `active_connections`
- `retransmits_per_second`

**Sample response:**

```json
[
  {
    "namespace": "emojivoto",
    "pod_name": "vote-bot-77b6c7959b-cgbx8",
    "traffic_dependencies": [
      {
        "target_pod": "web-7d64655496-57q6k",
        "bytes_per_second": 150.06,
        "p99_latency_ms": 9.59,
        "requests_per_second": 1.96,
        "errors_per_second": 0.16,
        "active_connections": 0,
        "retransmits_per_second": 0.0
      }
    ]
  }
]
```

---

## Understanding `topology_graph.json`

`topology_graph.json` is a generated representation of the live traffic topology inside the mesh. It is not a static physical topology; it is a runtime view of which application pods are actively talking to which other pods, plus metrics for those edges.

Each top-level object contains:
- `source_app`: the calling application or pod source
- `namespace`: the Kubernetes namespace
- `traffic_dependencies`: an array of outbound relationships

Each dependency contains:
- `target_pod`: the destination pod
- `bytes_per_second`: outbound bandwidth from source to target
- `p99_latency_ms`: 99th percentile latency for that traffic path
- `requests_per_second`: request rate on that path
- `errors_per_second`: failure rate on that path
- `active_connections`: current open TCP connections
- `retransmits_per_second`: retransmission rate on that path

This file is useful for the scheduler because it lets scheduling logic consider both network topology and live traffic behavior. For example, the scheduler can:
- prefer nodes in the same zone/rack for high-bandwidth, low-latency flows
- avoid pods with high error or retransmission rates
- group related services onto nearby topology labels to reduce cross-zone traffic

---

## Generating `topology_graph.json`

The repository includes `gen_graph.py`, which can generate a local topology file from Prometheus metrics.

First port-forward Prometheus:

```bash
kubectl port-forward svc/prometheus 9090:9090 -n linkerd-viz
```

Then run:

```bash
python3 gen_graph.py
```

This writes a metrics-enriched graph to `topology_graph.json`.

---

## Validation

To verify from inside the cluster:

```bash
kubectl run -i --tty --rm debug --image=curlimages/curl --restart=Never -- curl -s http://telemetry-service.default.svc.cluster.local/api/v1/traffic | jq
```

---

## Cleanup

Remove the Kind cluster:

```bash
make clean
```

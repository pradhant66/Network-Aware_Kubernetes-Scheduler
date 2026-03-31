# Network-Aware Scheduler: Observation Layer (Telemetry API)

This directory contains the infrastructure and microservice code for the **Observation Layer** of our custom Network-Aware Kubernetes Scheduler. 

This system leverages the Linkerd Service Mesh to passively monitor east-west network traffic between pods. It exposes a continuously updated, machine-readable dependency graph (via an internal HTTP API) that the Custom Scheduler uses to make topology-aware placement decisions.

---

## Prerequisites

To run this environment locally, ensure you have the following installed:
* **Docker** (or [OrbStack](https://orbstack.dev/) recommended for MacOS)
* **[Kind](https://kind.sigs.k8s.io/)** (Kubernetes IN Docker)
* **[kubectl](https://kubernetes.io/docs/tasks/tools/)**
* **[Linkerd CLI](https://linkerd.io/2.14/getting-started/)**

---

## Infrastructure & Service Mesh Setup

To establish the local multi-node cluster, apply simulated rack/zone topology labels, and deploy the `emojivoto` demo application wrapped in the Linkerd Service Mesh -

### 1. Spin up the Cluster
Run the Makefile from the root of the repository. This will create a 4-node cluster (1 control plane, 3 workers) and deploy the demo app.
```bash
make
```

### 2. Install Linkerd
Install the Linkerd control plane and its observability stack (Prometheus):
```bash
linkerd install --crds | kubectl apply -f -
linkerd install | kubectl apply -f -
linkerd check
linkerd viz install | kubectl apply -f -
```
### 3. Mesh the Application

Inject the Linkerd proxies into the running `emojivoto` pods so they begin reporting network telemetry:

```bash
kubectl get deploy -n emojivoto -o yaml | linkerd inject - | kubectl apply -f -
```

---

Next we automate the extraction of network metrics. The `telemetry-api` is a Python Flask microservice that queries Linkerd's internal Prometheus, processes `tcp_write_bytes_total` metrics, and formats the output.

### 1. Build and Load the Image

Because we are using a local `kind` cluster, we must build the Docker image and side-load it directly into the nodes.

```bash
cd telemetry-api
docker build -t telemetry-api:latest .
kind load docker-image telemetry-api:latest --name topo-cluster
```

### 2. Deploy the API & Security Policies

Deploy the API pod, its internal service, and the required Linkerd RBAC policies (authorizes our API to query the Prometheus admin server). 

```bash
# Inject Linkerd into our API and deploy it
kubectl get deployment telemetry-api -o yaml | linkerd inject - | kubectl apply -f -

# Apply the authorization policy
kubectl apply -f prom-rbac.yaml
```

---

## 📡 API Contract (JSON Output)

The `telemetry-api` serves data internally at:
`http://telemetry-service.default.svc.cluster.local/api/v1/traffic`

When the Custom Scheduler hits this endpoint, it returns a JSON array representing the live directed graph of pod-to-pod network traffic (measured in bytes per second).

**Sample Output:**

```json
[
  {
    "namespace": "emojivoto",
    "pod_name": "vote-bot-77b6c7959b-dmjbv",
    "traffic_dependencies": [
      {
        "bytes_per_second": 149.55,
        "target_pod": "web-7d64655496-k2mck"
      }
    ]
  },
  {
    "namespace": "emojivoto",
    "pod_name": "web-7d64655496-k2mck",
    "traffic_dependencies": [
      {
        "bytes_per_second": 196.82,
        "target_pod": "emoji-745c8cd747-25n65"
      },
      {
        "bytes_per_second": 93.42,
        "target_pod": "voting-846d476944-5grgm"
      }
    ]
  }
]
```

---

## 🧪 Verification & Testing

To verify the entire Observation Layer is functioning correctly, you can spin up a temporary debug pod inside the cluster to curl the internal API endpoint:

```bash
kubectl run -i --tty --rm debug --image=curlimages/curl --restart=Never -- curl -s [http://telemetry-service.default.svc.cluster.local/api/v1/traffic](http://telemetry-service.default.svc.cluster.local/api/v1/traffic) | jq
```
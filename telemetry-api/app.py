import requests
from flask import Flask, jsonify

app = Flask(__name__)

PROMETHEUS_URL = "http://prometheus.linkerd-viz.svc.cluster.local:9090/api/v1/query"

# 1. Bandwidth Query (Bytes/sec) - using inbound metrics
QUERY_BYTES = 'sum by (pod, client_id) (rate(request_total{namespace="default", direction="inbound"}[1m]))'
# 2. Latency Query (P99 ms) - using inbound response metrics
QUERY_LATENCY = 'histogram_quantile(0.99, sum by (le, pod, client_id) (rate(response_latency_ms_bucket{namespace="default", direction="inbound"}[1m])))'
# 3. Request Rate Query (RPS) - using inbound metrics
QUERY_RPS = 'sum by (pod, client_id) (rate(request_total{namespace="default", direction="inbound"}[1m]))'
# 4. Error Rate Query (Errors/sec) - using inbound metrics
QUERY_ERRORS = 'sum by (pod, client_id) (rate(response_total{namespace="default", direction="inbound", classification="failure"}[1m]))'
# 5. Active Connections Query - using inbound metrics
QUERY_CONNECTIONS = 'sum by (pod, client_id) (tcp_active_connections{namespace="default", direction="inbound"})'
# 6. Packet Retransmissions - using inbound metrics
QUERY_RETRANSMITS = 'sum by (pod, client_id) (rate(tcp_retransmits_total{namespace="default", direction="inbound"}[1m]))'

def query_prom(query_string):
    """Helper function to fetch data from Prometheus"""
    try:
        response = requests.get(PROMETHEUS_URL, params={'query': query_string}, timeout=5)
        response.raise_for_status()
        return response.json().get('data', {}).get('result', [])
    except Exception:
        return []

@app.route('/api/v1/traffic', methods=['GET'])
def get_traffic():
    # Fetch all metrics
    bytes_data = query_prom(QUERY_BYTES)
    latency_data = query_prom(QUERY_LATENCY)
    rps_data = query_prom(QUERY_RPS)
    errors_data = query_prom(QUERY_ERRORS)
    connections_data = query_prom(QUERY_CONNECTIONS)
    retransmits_data = query_prom(QUERY_RETRANSMITS)

    graph = {}

    # Helper function to process and merge data
    def process_data(prom_results, metric_key):
        for item in prom_results:
            metric = item.get('metric', {})
            # For inbound metrics: pod is destination, client_id contains source
            dest_pod = metric.get('pod')
            client_id = metric.get('client_id', '')
            
            # Extract source app from client_id (e.g., "frontend.default.serviceaccount..." -> "frontend")
            source_app = client_id.split('.')[0] if client_id else 'unknown'
            
            # Skip if we don't have both source and destination
            if not dest_pod or not source_app or source_app == 'unknown':
                continue
            
            # Extract the actual value, skip if empty or NaN
            val_str = item.get('value', [0, "0"])[1]
            if val_str == "NaN":
                continue
                
            val = float(val_str)
            if val < 0:  # Allow 0 for some metrics like errors
                continue

            # Initialize the graph entry if it doesn't exist
            if source_app not in graph:
                graph[source_app] = {"source_app": source_app, "namespace": "default", "traffic_dependencies": []}
            
            # Find or create the dependency entry for this destination
            dep_entry = None
            for dep in graph[source_app]["traffic_dependencies"]:
                if dep["target_pod"] == dest_pod:
                    dep_entry = dep
                    break
            
            if dep_entry is None:
                dep_entry = {
                    "target_pod": dest_pod, 
                    "bytes_per_second": 0.0, 
                    "p99_latency_ms": 0.0, 
                    "requests_per_second": 0.0,
                    "errors_per_second": 0.0,
                    "active_connections": 0,
                    "retransmits_per_second": 0.0
                }
                graph[source_app]["traffic_dependencies"].append(dep_entry)
            
            # Update the specific metric
            dep_entry[metric_key] = round(val, 2) if isinstance(val, float) else int(val)

    # Process all datasets
    process_data(bytes_data, "bytes_per_second")
    process_data(latency_data, "p99_latency_ms")
    process_data(rps_data, "requests_per_second")
    process_data(errors_data, "errors_per_second")
    process_data(connections_data, "active_connections")
    process_data(retransmits_data, "retransmits_per_second")

    # Format the final output to match the JSON array contract
    final_output = list(graph.values())

    return jsonify(final_output)

@app.route('/healthz', methods=['GET'])
def healthz():
    return jsonify({"status": "healthy"}), 200

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8080)
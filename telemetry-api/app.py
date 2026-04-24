import requests
from flask import Flask, jsonify

app = Flask(__name__)

PROMETHEUS_URL = "http://prometheus.linkerd-viz.svc.cluster.local:9090/api/v1/query"

# 1. Bandwidth Query (Bytes/sec)
QUERY_BYTES = 'sum by (pod, dst_pod) (rate(tcp_write_bytes_total{namespace="emojivoto", direction="outbound"}[1m]))'
# 2. Latency Query (P99 ms)
QUERY_LATENCY = 'histogram_quantile(0.99, sum by (le, pod, dst_pod) (rate(response_latency_ms_bucket{namespace="emojivoto", direction="outbound"}[1m])))'
# 3. Request Rate Query (RPS)
QUERY_RPS = 'sum by (pod, dst_pod) (rate(request_total{namespace="emojivoto", direction="outbound"}[1m]))'

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
    # Fetch all three metrics
    bytes_data = query_prom(QUERY_BYTES)
    latency_data = query_prom(QUERY_LATENCY)
    rps_data = query_prom(QUERY_RPS)

    graph = {}

    # Helper function to process and merge data
    def process_data(prom_results, metric_key):
        for item in prom_results:
            metric = item.get('metric', {})
            source = metric.get('pod')
            dest = metric.get('dst_pod')
            
            # Extract the actual value, skip if empty or NaN
            val_str = item.get('value', [0, "0"])[1]
            if not source or not dest or val_str == "NaN":
                continue
                
            val = float(val_str)
            if val <= 0:
                continue

            # Initialize the graph entry if it doesn't exist
            if source not in graph:
                graph[source] = {"pod_name": source, "namespace": "emojivoto", "dependencies": {}}
            if dest not in graph[source]["dependencies"]:
                graph[source]["dependencies"][dest] = {"target_pod": dest, "bytes_per_second": 0.0, "p99_latency_ms": 0.0, "requests_per_second": 0.0}
            
            # Update the specific metric
            graph[source]["dependencies"][dest][metric_key] = round(val, 2)

    # Process all three datasets
    process_data(bytes_data, "bytes_per_second")
    process_data(latency_data, "p99_latency_ms")
    process_data(rps_data, "requests_per_second")

    # Format the final output to match the JSON array contract
    final_output = []
    for source_pod, data in graph.items():
        # Convert the dependencies dictionary back to a list
        data["traffic_dependencies"] = list(data["dependencies"].values())
        del data["dependencies"] # Clean up the temporary dictionary
        final_output.append(data)

    return jsonify(final_output)

@app.route('/healthz', methods=['GET'])
def healthz():
    return jsonify({"status": "healthy"}), 200

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8080)
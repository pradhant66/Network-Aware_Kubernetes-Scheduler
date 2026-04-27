import requests
from flask import Flask, jsonify

app = Flask(__name__)

PROMETHEUS_URL = "http://prometheus.linkerd-viz.svc.cluster.local:9090/api/v1/query"

# These inbound/client_id queries match the Linkerd metrics currently available
# in the Kind cluster and produce the same logical graph contract as gen_graph.py.
QUERY_RPS = 'sum(rate(request_total{namespace="emojivoto",direction="inbound"}[1m]))by(pod,client_id)'
QUERY_LATENCY = 'histogram_quantile(0.99, sum(rate(response_latency_ms_bucket{namespace="emojivoto",direction="inbound"}[1m]))by(le,pod,client_id))'
QUERY_BYTES = 'sum(rate(tcp_read_bytes_total{namespace="emojivoto",direction="inbound"}[1m]))by(pod,client_id)'
QUERY_ERRORS = 'sum(rate(response_total{namespace="emojivoto",direction="inbound",classification="failure"}[1m]))by(pod,client_id)'
QUERY_CONNECTIONS = 'sum(tcp_open_connections{namespace="emojivoto",direction="inbound"})by(pod,client_id)'
QUERY_RETRANSMITS = 'sum(rate(tcp_retransmits_total{namespace="emojivoto",direction="inbound"}[1m]))by(pod,client_id)'


def source_app_from_client_id(client_id):
    if not client_id:
        return ""

    source_app = client_id.split('.')[0]
    # vote-bot appears as "default" in this Linkerd identity format.
    if source_app == "default":
        return "vote-bot"
    return source_app

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
    # Fetch all metrics used to enrich the traffic graph
    rps_data = query_prom(QUERY_RPS)
    latency_data = query_prom(QUERY_LATENCY)
    bytes_data = query_prom(QUERY_BYTES)
    errors_data = query_prom(QUERY_ERRORS)
    connections_data = query_prom(QUERY_CONNECTIONS)
    retransmits_data = query_prom(QUERY_RETRANSMITS)

    graph = {}

    # Helper function to process and merge data
    def process_data(prom_results, metric_key):
        for item in prom_results:
            metric = item.get('metric', {})
            client_id = metric.get('client_id')
            dest_pod = metric.get('pod')

            # Extract the actual value, skip if empty or NaN
            val_str = item.get('value', [0, "0"])[1]
            if not client_id or not dest_pod or val_str == "NaN":
                continue

            # Ignore Prometheus scraping noise and any unexpected identities.
            if "prometheus" in client_id:
                continue

            source_app = source_app_from_client_id(client_id)
            if not source_app:
                continue

            val = float(val_str)
            if val <= 0:
                continue

            # Initialize the graph entry if it doesn't exist
            if source_app not in graph:
                graph[source_app] = {
                    "source_app": source_app,
                    "namespace": "emojivoto",
                    "dependencies": {},
                }
            if dest_pod not in graph[source_app]["dependencies"]:
                graph[source_app]["dependencies"][dest_pod] = {
                    "target_pod": dest_pod,
                    "bytes_per_second": 0.0,
                    "p99_latency_ms": 0.0,
                    "requests_per_second": 0.0,
                    "errors_per_second": 0.0,
                    "active_connections": 0,
                    "retransmits_per_second": 0.0,
                }

            # Update the specific metric
            graph[source_app]["dependencies"][dest_pod][metric_key] = round(val, 2)

    # Process all three datasets
    process_data(rps_data, "requests_per_second")
    process_data(latency_data, "p99_latency_ms")
    process_data(bytes_data, "bytes_per_second")
    process_data(errors_data, "errors_per_second")
    process_data(connections_data, "active_connections")
    process_data(retransmits_data, "retransmits_per_second")

    # Format the final output to match the JSON array contract
    final_output = []
    for _, data in graph.items():
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

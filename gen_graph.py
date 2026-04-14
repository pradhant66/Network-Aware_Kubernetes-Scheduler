import urllib.request
import urllib.parse
import json
import sys

BASE_URL = 'http://localhost:9090/api/v1/query?query='

# The Three Golden Metrics (Inbound Perspective)
# 1. Requests Per Second (RPS)
Q_RPS = 'sum(rate(request_total{namespace="emojivoto",direction="inbound"}[1m]))by(pod,client_id)'
# 2. P99 Latency in Milliseconds
Q_LATENCY = 'histogram_quantile(0.99, sum(rate(response_latency_ms_bucket{namespace="emojivoto",direction="inbound"}[1m]))by(le,pod,client_id))'
# 3. Network Bandwidth in Bytes/Second
Q_BYTES = 'sum(rate(tcp_read_bytes_total{namespace="emojivoto",direction="inbound"}[1m]))by(pod,client_id)'

def fetch_prom(query):
    """Fetches and parses a PromQL query safely."""
    url = BASE_URL + urllib.parse.quote(query)
    try:
        req = urllib.request.urlopen(url)
        return json.loads(req.read()).get('data', {}).get('result', [])
    except Exception as e:
        print(f"Error fetching data: {e}. Is the port-forward running?")
        sys.exit(1)

def fetch_and_parse():
    # Fetch all three datasets
    rps_data = fetch_prom(Q_RPS)
    latency_data = fetch_prom(Q_LATENCY)
    bytes_data = fetch_prom(Q_BYTES)

    graph = {}

    def process_dataset(raw_data, metric_name):
        for item in raw_data:
            metric = item['metric']
            
            # 1. Filter out noise
            if 'client_id' not in metric:
                continue
            client = metric['client_id']
            if 'prometheus' in client:
                continue

            # 2. Extract source and target
            source_app = client.split('.')[0]
            if source_app == 'default': 
                source_app = 'vote-bot'

            target_pod = metric.get('pod', 'unknown')
            
            # Handle NaN values that Prometheus sometimes returns
            val_str = item['value'][1]
            if val_str == "NaN":
                continue
            val = float(val_str)

            # 3. Build the nested dictionary structure
            if source_app not in graph:
                graph[source_app] = {
                    "source_app": source_app,
                    "namespace": "emojivoto",
                    "dependencies_map": {} # Temporary map to group by target pod
                }
            
            if target_pod not in graph[source_app]["dependencies_map"]:
                graph[source_app]["dependencies_map"][target_pod] = {
                    "target_pod": target_pod,
                    "requests_per_second": 0.0,
                    "p99_latency_ms": 0.0,
                    "bytes_per_second": 0.0
                }
            
            # Update the specific metric
            graph[source_app]["dependencies_map"][target_pod][metric_name] = round(val, 2)

    # Process all three queries into our graph
    process_dataset(rps_data, "requests_per_second")
    process_dataset(latency_data, "p99_latency_ms")
    process_dataset(bytes_data, "bytes_per_second")

    # Format into the final JSON list contract
    final_output = []
    for app_name, app_data in graph.items():
        # Convert the temporary dependencies_map dictionary back into a clean list
        app_data["traffic_dependencies"] = list(app_data["dependencies_map"].values())
        del app_data["dependencies_map"]
        final_output.append(app_data)

    return final_output

if __name__ == "__main__":
    print("Gathering RPS, Latency, and Bandwidth metrics...")
    clean_graph = fetch_and_parse()
    
    # Write to file
    with open('topology_graph.json', 'w') as f:
        json.dump(clean_graph, f, indent=2)
        
    print("Successfully generated rich topology_graph.json!")
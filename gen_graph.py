import urllib.request
import json
import sys

# The Prometheus API URL (requires the port-forward to be running)
URL = 'http://localhost:9090/api/v1/query?query=sum(rate(request_total{namespace="emojivoto",direction="inbound"}[1m]))by(pod,client_id)'

def fetch_and_parse():
    try:
        req = urllib.request.urlopen(URL)
        raw_data = json.loads(req.read())
    except Exception as e:
        print(f"Error fetching data: {e}. Is the port-forward running?")
        sys.exit(1)

    graph = {}

    for item in raw_data['data']['result']:
        metric = item['metric']
        
        # 1. Filter out noise (health checks and prometheus scrapers)
        if 'client_id' not in metric:
            continue
        client = metric['client_id']
        if 'prometheus' in client:
            continue

        # 2. Extract the source application name
        # Linkerd formats client_id as "serviceaccount.namespace...", we just want the first part.
        source_app = client.split('.')[0]
        if source_app == 'default': 
            source_app = 'vote-bot' # Fix for the vote-bot using the default service account

        target_pod = metric['pod']
        rps = float(item['value'][1])

        # 3. Build the clean JSON contract
        if source_app not in graph:
            graph[source_app] = {
                "source_app": source_app,
                "namespace": "emojivoto",
                "traffic_dependencies": []
            }

        graph[source_app]["traffic_dependencies"].append({
            "target_pod": target_pod,
            "requests_per_second": round(rps, 2)
        })

    # Output as a formatted JSON list
    return list(graph.values())

if __name__ == "__main__":
    clean_graph = fetch_and_parse()
    
    # Write to file
    with open('topology_graph.json', 'w') as f:
        json.dump(clean_graph, f, indent=2)
        
    print("Successfully generated topology_graph.json!")
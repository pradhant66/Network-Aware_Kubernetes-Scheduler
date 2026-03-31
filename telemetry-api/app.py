import requests
from flask import Flask, jsonify

app = Flask(__name__)

# K8s internal DNS for the Linkerd Prometheus service
PROMETHEUS_URL = "http://prometheus.linkerd-viz.svc.cluster.local:9090/api/v1/query"

# We use the bytes/second query as it is the best proxy for network cost
QUERY = 'sum by (pod, dst_pod) (rate(tcp_write_bytes_total{namespace="emojivoto", direction="outbound"}[1m]))'

@app.route('/api/v1/traffic', methods=['GET'])
def get_traffic():
    try:
        # Request data from Prometheus
        response = requests.get(PROMETHEUS_URL, params={'query': QUERY}, timeout=5)
        response.raise_for_status()
        results = response.json().get('data', {}).get('result', [])
    except Exception as e:
        return jsonify({"error": f"Failed to connect to Prometheus: {str(e)}"}), 500

    # Dictionary to group dependencies by source pod
    graph = {}
    
    for item in results:
        metric = item.get('metric', {})
        source_pod = metric.get('pod')
        dest_pod = metric.get('dst_pod')
        
        # Prometheus returns values as [timestamp, "value_string"]
        value_str = item.get('value', [0, "0"])[1]

        # Skip empty destinations or zero traffic
        if not source_pod or not dest_pod:
            continue
            
        bytes_per_sec = float(value_str)
        if bytes_per_sec <= 0:
            continue

        # If we haven't seen this source pod yet, initialize it
        if source_pod not in graph:
            graph[source_pod] = {
                "pod_name": source_pod,
                "namespace": "emojivoto",
                "traffic_dependencies": []
            }

        # Append the destination target to this pod's dependency list
        graph[source_pod]["traffic_dependencies"].append({
            "target_pod": dest_pod,
            "bytes_per_second": round(bytes_per_sec, 2)
        })

    # Convert the dictionary values to a list to match the JSON contract
    return jsonify(list(graph.values()))

@app.route('/healthz', methods=['GET'])
def healthz():
    return jsonify({"status": "healthy"}), 200

if __name__ == '__main__':
    # Run on 0.0.0.0 so it is accessible outside the container
    app.run(host='0.0.0.0', port=8080)
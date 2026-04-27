package score_algorithm

import (
	"encoding/json"
	"testing"
)

func TestEvaluateNodesNetworkOnly(t *testing.T) {
	t.Setenv(algorithmModeEnv, ModeNetworkOnly)

	// 1. Mock K8s Payload (from Person 2)
	k8sJSON := []byte(`{
		"pod_to_schedule": "web-new-instance",
		"candidate_nodes": [
			{"name": "worker-1", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 88.5, "active_pods": 42},
			{"name": "worker-2", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 35.0, "active_pods": 15},
			{"name": "worker-3", "zone": "us-east-1b", "rack": "rack-2", "cpu_utilization_pct": 12.0, "active_pods": 8}
		]
	}`)

	// 2. Mock Telemetry Payload (from Person 1)
	telemetryJSON := []byte(`{
		"traffic_dependencies": [
			{"target_pod": "emoji-svc-abc", "current_node": "worker-2", "bytes_per_second": 8500},
			{"target_pod": "voting-svc-xyz", "current_node": "worker-3", "bytes_per_second": 1200}
		]
	}`)

	// 3. Execute your library function
	outputJSON, err := EvaluateNodes(k8sJSON, telemetryJSON)

	// 4. Check for catastrophic errors (e.g., bad JSON formatting)
	if err != nil {
		t.Fatalf("EvaluateNodes failed with error: %v", err)
	}

	// 5. Parse the output back into Go structs to check the math
	var results []NodeScore
	if err := json.Unmarshal(outputJSON, &results); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	// 6. Assertions: Did it do what we expect?
	if len(results) == 0 {
		t.Fatalf("Expected results, got an empty array")
	}

	// Based on our Naive Greedy math, worker-2 should have the lowest cost
	winner := results[0].Node
	if winner != "worker-2" {
		t.Errorf("Expected winner to be 'worker-2', but got '%s'", winner)
	}

	// 7. Log the final JSON output so you can visually inspect it
	t.Logf("\nSuccessfully generated the final payload for Person 2:\n%s", string(outputJSON))
}

func TestEvaluateNodesCPUProximity(t *testing.T) {
	t.Setenv(algorithmModeEnv, ModeCPUProximity)

	k8sJSON := []byte(`{
		"pod_to_schedule": "web-new-instance",
		"candidate_nodes": [
			{"name": "worker-1", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 95.0, "active_pods": 42},
			{"name": "worker-2", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 35.0, "active_pods": 15},
			{"name": "worker-3", "zone": "us-east-1b", "rack": "rack-2", "cpu_utilization_pct": 12.0, "active_pods": 8}
		]
	}`)

	telemetryJSON := []byte(`{
		"traffic_dependencies": [
			{"target_pod": "emoji-svc-abc", "current_node": "worker-1", "bytes_per_second": 8500},
			{"target_pod": "voting-svc-xyz", "current_node": "worker-2", "bytes_per_second": 1200}
		]
	}`)

	outputJSON, err := EvaluateNodes(k8sJSON, telemetryJSON)
	if err != nil {
		t.Fatalf("EvaluateNodes failed with error: %v", err)
	}

	var results []NodeScore
	if err := json.Unmarshal(outputJSON, &results); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected results, got an empty array")
	}

	// CPU-aware scoring should avoid the 95%% utilized worker-1 despite its locality.
	if results[0].Node != "worker-2" {
		t.Fatalf("Expected CPU-aware winner to be worker-2, got %s", results[0].Node)
	}

	if results[0].CPUPenalty <= 1.0 {
		t.Fatalf("Expected enriched results to include CPU penalty details, got %+v", results[0])
	}
}

func TestEvaluateNodesCentrality(t *testing.T) {
	t.Setenv(algorithmModeEnv, ModeCentrality)

	k8sJSON := []byte(`{
		"pod_to_schedule": "web-new-instance",
		"candidate_nodes": [
			{"name": "worker-1", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 92.0, "active_pods": 42},
			{"name": "worker-2", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 35.0, "active_pods": 15},
			{"name": "worker-3", "zone": "us-east-1b", "rack": "rack-2", "cpu_utilization_pct": 12.0, "active_pods": 8}
		]
	}`)

	telemetryJSON := []byte(`{
		"traffic_dependencies": [
			{"target_pod": "emoji-svc-abc", "current_node": "worker-1", "bytes_per_second": 9000},
			{"target_pod": "voting-svc-xyz", "current_node": "worker-2", "bytes_per_second": 2000}
		]
	}`)

	outputJSON, err := EvaluateNodes(k8sJSON, telemetryJSON)
	if err != nil {
		t.Fatalf("EvaluateNodes failed with error: %v", err)
	}

	var results []NodeScore
	if err := json.Unmarshal(outputJSON, &results); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected results, got an empty array")
	}

	if results[0].Node != "worker-2" {
		t.Fatalf("Expected centrality winner to be worker-2, got %s", results[0].Node)
	}

	if results[0].PodClassification != "Hub" {
		t.Fatalf("Expected centrality mode to classify the pod as Hub, got %+v", results[0])
	}
}

func TestEvaluateNodesPID(t *testing.T) {
	t.Setenv(algorithmModeEnv, ModePID)

	k8sJSON := []byte(`{
		"pod_to_schedule": "web-new-instance",
		"candidate_nodes": [
			{"name": "worker-1", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 45.0, "active_pods": 20},
			{"name": "worker-2", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 45.0, "active_pods": 20},
			{"name": "worker-3", "zone": "us-east-1b", "rack": "rack-2", "cpu_utilization_pct": 45.0, "active_pods": 20}
		],
		"controller": {
			"current_cluster_cross_rack_bps": 20000,
			"integral": 1000,
			"prev_error": 5000
		}
	}`)

	telemetryJSON := []byte(`{
		"traffic_dependencies": [
			{"target_pod": "emoji-svc-abc", "current_node": "worker-1", "bytes_per_second": 5000},
			{"target_pod": "voting-svc-xyz", "current_node": "worker-3", "bytes_per_second": 5000}
		]
	}`)

	outputJSON, err := EvaluateNodes(k8sJSON, telemetryJSON)
	if err != nil {
		t.Fatalf("EvaluateNodes failed with error: %v", err)
	}

	var results []NodeScore
	if err := json.Unmarshal(outputJSON, &results); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected results, got an empty array")
	}

	if results[0].Node != "worker-1" && results[0].Node != "worker-2" {
		t.Fatalf("Expected PID to prefer rack-local nodes over cross-rack worker-3, got %s", results[0].Node)
	}

	if results[0].DynamicWeight <= 1.0 {
		t.Fatalf("Expected PID mode to enrich results with dynamic weight, got %+v", results[0])
	}
}

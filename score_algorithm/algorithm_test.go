package score_algorithm

import (
	"encoding/json"
	"testing"
)

func TestEvaluateNodes(t *testing.T) {
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

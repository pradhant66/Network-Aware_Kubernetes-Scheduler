package score_algorithm

import (
	"encoding/json"
	"testing"
)

func TestEvaluateNodesCPU(t *testing.T) {
	k8sJSON := []byte(`{
		"pod_to_schedule": "backend-api",
		"candidate_nodes": [
			{"name": "worker-1", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 95.0, "active_pods": 30},
			{"name": "worker-2", "zone": "us-east-1a", "rack": "rack-2", "cpu_utilization_pct": 15.0, "active_pods": 5},
			{"name": "worker-3", "zone": "us-east-1a", "rack": "rack-3", "cpu_utilization_pct": 50.0, "active_pods": 10}
		]
	}`)

	// Target pod is on worker-3. Both worker-1 and worker-2 are in the same AZ (but different racks).
	// Their baseline network distance to worker-3 is exactly tied (5000 raw cost).
	telemetryJSON := []byte(`{
		"traffic_dependencies": [
			{"target_pod": "database", "current_node": "worker-3", "bytes_per_second": 1000.0}
		]
	}`)

	outputJSON, err := EvaluateNodesCPU(k8sJSON, telemetryJSON)

	if err != nil {
		t.Fatalf("EvaluateNodesCPU failed: %v", err)
	}

	var results []NodeScoreCPU
	if err := json.Unmarshal(outputJSON, &results); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected results, got empty array")
	}

	// Find the ranking index of worker-1 and worker-2
	var worker1Rank, worker2Rank int
	for i, res := range results {
		if res.Node == "worker-1" {
			worker1Rank = i
		}
		if res.Node == "worker-2" {
			worker2Rank = i
		}
	}

	// 1. Assert the relative ranking (lower index is better)
	// Because of the 5.48x CPU penalty on worker-1, worker-2 MUST be ranked ahead of it.
	if worker2Rank > worker1Rank {
		t.Errorf("FAIL: Expected 'worker-2' to outrank 'worker-1'. Worker-2 rank: %d, Worker-1 rank: %d", worker2Rank, worker1Rank)
	}

	// 2. Validate the penalty math
	var worker1Score NodeScoreCPU
	for _, res := range results {
		if res.Node == "worker-1" {
			worker1Score = res
		}
	}

	if worker1Score.Penalty < 4.0 {
		t.Errorf("FAIL: Expected worker-1 to have a massive penalty multiplier (>4.0), got %f", worker1Score.Penalty)
	}

	t.Logf("\nPASS! CPU penalty successfully broke the tie (worker-2 rank: %d, worker-1 rank: %d). Output:\n%s", worker2Rank, worker1Rank, string(outputJSON))
}

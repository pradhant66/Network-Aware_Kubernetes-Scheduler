package score_algorithm

import (
	"encoding/json"
	"math"
	"testing"
)

func TestEvaluateNodesCentrality(t *testing.T) {
	k8sJSON := []byte(`{
		"pod_to_schedule": "database-primary",
		"candidate_nodes": [
			{"name": "worker-1", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 95.0, "active_pods": 50},
			{"name": "worker-2", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 85.0, "active_pods": 30},
			{"name": "worker-3", "zone": "us-east-1b", "rack": "rack-2", "cpu_utilization_pct": 5.0, "active_pods": 2}
		]
	}`)

	// Total bandwidth = 12,000 B/s. This crosses the 10,000 threshold -> HUB.
	// Almost all traffic points to worker-1.
	telemetryJSON := []byte(`{
		"traffic_dependencies": [
			{"target_pod": "api-gateway", "current_node": "worker-1", "bytes_per_second": 8000.0},
			{"target_pod": "auth-svc", "current_node": "worker-1", "bytes_per_second": 4000.0}
		]
	}`)

	outputJSON, err := EvaluateNodesCentrality(k8sJSON, telemetryJSON)

	if err != nil {
		t.Fatalf("EvaluateNodesCentrality failed: %v", err)
	}

	var results []NodeScoreCent
	if err := json.Unmarshal(outputJSON, &results); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected results, got empty array")
	}

	// 1. Assert VIP Classification
	if results[0].PodClassification != "Hub" {
		t.Errorf("FAIL: Expected pod to be classified as 'Hub', got '%s'", results[0].PodClassification)
	}

	// 2. Assert the Hard Filter Triggered
	winner := results[0].Node
	if winner != "worker-3" {
		t.Errorf("FAIL: Expected 'worker-3' to win due to Hard Filter, but got '%s'", winner)
	}

	// 3. Verify worker-1 was actually disqualified with MaxFloat64
	for _, res := range results {
		if res.Node == "worker-1" && res.Score != math.MaxFloat64 {
			t.Errorf("FAIL: Expected worker-1 to have score of MaxFloat64, got %f", res.Score)
		}
	}

	t.Logf("\nPASS! Hub isolated to core node safely. Output:\n%s", string(outputJSON))
}

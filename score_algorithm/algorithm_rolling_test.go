package score_algorithm

import (
	"encoding/json"
	"testing"
)

func TestEvaluateNodesRollingWindow(t *testing.T) {
	k8sJSON := []byte(`{
		"pod_to_schedule": "web-new-instance",
		"candidate_nodes": [
			{"name": "worker-1", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 88.5},
			{"name": "worker-2", "zone": "us-east-1a", "rack": "rack-1", "cpu_utilization_pct": 35.0},
			{"name": "worker-3", "zone": "us-east-1b", "rack": "rack-2", "cpu_utilization_pct": 12.0}
		]
	}`)

	// emoji-svc spikes massively at the end.
	// EMA (alpha 0.5) will smooth this 9000 spike down to ~7031.
	telemetryJSON := []byte(`{
		"traffic_dependencies": [
			{"target_pod": "emoji-svc-abc", "current_node": "worker-2", "traffic_samples_bps": [1000.0, 1500.0, 2000.0, 8500.0, 9000.0]},
			{"target_pod": "voting-svc-xyz", "current_node": "worker-3", "traffic_samples_bps": [1200.0, 1100.0, 1300.0, 1200.0, 1250.0]}
		]
	}`)

	outputJSON, err := EvaluateNodesRollingWindow(k8sJSON, telemetryJSON)

	if err != nil {
		t.Fatalf("EvaluateNodesRollingWindow failed: %v", err)
	}

	var results []NodeScoreWin
	if err := json.Unmarshal(outputJSON, &results); err != nil {
		t.Fatalf("Failed to parse output JSON: %v", err)
	}

	// 1. Basic sanity check
	if len(results) == 0 {
		t.Fatalf("Expected results, got an empty array")
	}

	// 2. The Real Assertions: Verify the actual scoring and priority
	winner := results[0].Node

	// Because emoji-svc (on worker-2) has the heaviest smoothed traffic (~7031 B/s)
	// compared to voting-svc (~1231 B/s), the network-aware scheduler MUST
	// prioritize putting the new pod on worker-2 to minimize latency.
	if winner != "worker-2" {
		t.Errorf("FAIL: Expected 'worker-2' to win due to heavy EMA traffic, but got '%s'", winner)
	}

	// Verify the math didn't break. The winning score should be around ~25,328
	// (7031 * 0.1 same-node distance) + (1231 * 20.0 cross-AZ distance).
	expectedWinningScore := 25328.0
	actualScore := results[0].Score

	// Give a small tolerance for floating point math variations
	if actualScore < (expectedWinningScore-10) || actualScore > (expectedWinningScore+10) {
		t.Errorf("FAIL: Expected winning score to be ~%.0f, got %f", expectedWinningScore, actualScore)
	}

	t.Logf("\nPASS! Final prioritized node ranking:\n%s", string(outputJSON))
}

package score_algorithm

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestEvaluateNodesPID_Stream(t *testing.T) {
	// A simulated stream of incoming cluster traffic states over time
	// Target Max is 5000 B/s.
	trafficStream := []float64{
		2000.0,  // Cycle 1: Calm, well below target.
		15000.0, // Cycle 2: Massive East-West traffic spike.
		15000.0, // Cycle 3: Sustained spike (Integral windup will occur).
		4000.0,  // Cycle 4: Traffic drops back to normal.
	}

	// We start with a blank state
	currentPIDState := PIDState{
		CurrentCrossRackBps: 0.0,
		Integral:            0.0,
		PrevError:           0.0,
	}

	t.Logf("\n=== STARTING PID STREAM TEST (Target: 5000 B/s) ===\n")

	for cycle, currentTraffic := range trafficStream {
		currentPIDState.CurrentCrossRackBps = currentTraffic

		// 1. Build the dynamic K8s JSON with our running PID memory
		k8sPayload := K8sPayloadPID{
			PodToSchedule: fmt.Sprintf("pod-cycle-%d", cycle+1),
			CandidateNodes: []NodePID{
				{Name: "worker-1", Zone: "us-east-1a", Rack: "rack-1"},
				{Name: "worker-2", Zone: "us-east-1a", Rack: "rack-2"}, // Cross-Rack
			},
			Controller: currentPIDState,
		}
		k8sJSON, _ := json.Marshal(k8sPayload)

		// 2. Mock Telemetry (Sending heavy traffic to worker-1)
		telemetryJSON := []byte(`{
			"traffic_dependencies": [
				{"target_pod": "db", "current_node": "worker-1", "bytes_per_second": 1000.0}
			]
		}`)

		// 3. Execute the algorithm
		outputJSON, err := EvaluateNodesPID(k8sJSON, telemetryJSON)
		if err != nil {
			t.Fatalf("Cycle %d failed: %v", cycle+1, err)
		}

		// 4. Parse the output
		var result OutputPayloadPID
		if err := json.Unmarshal(outputJSON, &result); err != nil {
			t.Fatalf("Failed to parse output JSON: %v", err)
		}

		// 5. Extract the Dynamic Weight for worker-2 (which is Cross-Rack and gets penalized)
		var crossRackWeight float64
		for _, res := range result.Rankings {
			if res.Node == "worker-2" {
				crossRackWeight = res.DynamicWeight
			}
		}

		t.Logf("Cycle %d | Actual Traffic: %5.0f B/s | Dynamic Network Weight: %5.2fx", cycle+1, currentTraffic, crossRackWeight)

		// 6. Assertions to prove the PID is working mathematically
		if cycle == 0 && crossRackWeight > 1.0 {
			t.Errorf("FAIL: Expected weight to remain at baseline 1.0 during calm traffic.")
		}
		if cycle == 1 && crossRackWeight <= 1.0 {
			t.Errorf("FAIL: Expected weight to spike above 1.0 during traffic surge.")
		}
		if cycle == 2 && crossRackWeight <= result.NewPIDState.PrevError {
			// In cycle 3, traffic is the same as cycle 2, but the Integral term compounded the error.
			// The weight MUST be higher than the previous cycle.
			t.Logf("  -> Notice how the weight compounded (Integral windup) because the spike sustained!")
		}

		// 7. Update our running memory for the next loop iteration (closing the feedback loop)
		currentPIDState = result.NewPIDState
	}
	t.Logf("\n=== STREAM TEST COMPLETE ===\n")
}

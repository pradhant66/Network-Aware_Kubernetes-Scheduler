package score_algorithm

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// PID Tuning Constants
const (
	Kp = 0.005 // Proportional gain
	Ki = 0.001 // Integral gain
	Kd = 0.002 // Derivative gain

	TargetMaxCrossRackBps = 5000.0 // Our Setpoint
)

// Base Distances
const (
	SameNodePID = 0.1
	SameRackPID = 1.0
	SameAZPID   = 5.0
	CrossAZPID  = 20.0
)

// --- Input Structs ---

type NodePID struct {
	Name string `json:"name"`
	Zone string `json:"zone"`
	Rack string `json:"rack"`
}

type PIDState struct {
	CurrentCrossRackBps float64 `json:"current_cluster_cross_rack_bps"`
	Integral            float64 `json:"integral"`
	PrevError           float64 `json:"prev_error"`
}

type K8sPayloadPID struct {
	PodToSchedule  string    `json:"pod_to_schedule"`
	CandidateNodes []NodePID `json:"candidate_nodes"`
	Controller     PIDState  `json:"pid_state"` // NEW: The memory of the system
}

type DependencyPID struct {
	TargetPod      string  `json:"target_pod"`
	CurrentNode    string  `json:"current_node"`
	BytesPerSecond float64 `json:"bytes_per_second"`
}

type TelemetryPayloadPID struct {
	TrafficDependencies []DependencyPID `json:"traffic_dependencies"`
}

// --- Output Struct ---

type NodeScorePID struct {
	Node          string  `json:"node"`
	Score         float64 `json:"score"`
	DynamicWeight float64 `json:"dynamic_weight"`
}

type OutputPayloadPID struct {
	Rankings    []NodeScorePID `json:"rankings"`
	NewPIDState PIDState       `json:"new_pid_state"`
}

// EvaluateNodesPID dynamically scales distance penalties based on cluster health
func EvaluateNodesPID(k8sJSON []byte, telemetryJSON []byte) ([]byte, error) {
	var k8sState K8sPayloadPID
	if err := json.Unmarshal(k8sJSON, &k8sState); err != nil {
		return nil, fmt.Errorf("failed to parse K8s JSON: %w", err)
	}

	var telemetryState TelemetryPayloadPID
	if err := json.Unmarshal(telemetryJSON, &telemetryState); err != nil {
		return nil, fmt.Errorf("failed to parse Telemetry JSON: %w", err)
	}

	topologyMap := make(map[string]NodePID)
	for _, node := range k8sState.CandidateNodes {
		topologyMap[node.Name] = node
	}

	// --- 1. The PID Calculation ---

	// Error = Actual - Target
	errVal := k8sState.Controller.CurrentCrossRackBps - TargetMaxCrossRackBps

	// Calculate PID Terms
	pTerm := Kp * errVal
	iTerm := Ki * k8sState.Controller.Integral
	dTerm := Kd * (errVal - k8sState.Controller.PrevError)

	// Combine for the dynamic network weight multiplier
	pidOutput := pTerm + iTerm + dTerm

	// We ensure the weight never drops below 1.0 (baseline behavior)
	dynamicNetworkWeight := math.Max(1.0, 1.0+pidOutput)

	// --- 2. The Scoring Loop ---

	var results []NodeScorePID
	for _, candidate := range k8sState.CandidateNodes {
		totalScore := 0.0

		for _, dep := range telemetryState.TrafficDependencies {
			target, exists := topologyMap[dep.CurrentNode]
			baseDistance := CrossAZPID

			if exists {
				if candidate.Name == target.Name {
					baseDistance = SameNodePID
				} else if candidate.Rack == target.Rack {
					baseDistance = SameRackPID
				} else if candidate.Zone == target.Zone {
					baseDistance = SameAZPID
				}
			}

			// Apply the dynamic weight ONLY to cross-boundary traffic
			actualDistance := baseDistance
			if baseDistance > SameRackPID {
				// If the cluster is bleeding east-west traffic, this weight spikes,
				// severely punishing candidate nodes that aren't geographically close.
				actualDistance = baseDistance * dynamicNetworkWeight
			}

			totalScore += dep.BytesPerSecond * actualDistance
		}

		results = append(results, NodeScorePID{
			Node:          candidate.Name,
			Score:         totalScore,
			DynamicWeight: dynamicNetworkWeight,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score < results[j].Score
	})

	// Calculate the new state to hand back to K8s
	newPIDState := PIDState{
		CurrentCrossRackBps: k8sState.Controller.CurrentCrossRackBps, // Handled by Telemetry next cycle
		Integral:            k8sState.Controller.Integral + errVal,   // Add current error to integral
		PrevError:           errVal,                                  // Save current error for next cycle's derivative
	}

	finalPayload := OutputPayloadPID{
		Rankings:    results,
		NewPIDState: newPIDState,
	}

	outputJSON, err := json.MarshalIndent(finalPayload, "", "  ")

	// In a real implementation, you would also return the updated Integral and PrevError
	// back to Person 2 so they can pass it into the next cycle.
	// outputJSON, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to generate output JSON: %w", err)
	}

	return outputJSON, nil
}

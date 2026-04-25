package score_algorithm

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

const (
	SameNodeCPU = 0.1
	SameRackCPU = 1.0
	SameAZCPU   = 5.0
	CrossAZCPU  = 20.0

	// The threshold where we start aggressively penalizing CPU
	CPUWarningThreshold = 80.0
)

// --- Input Structs ---

type NodeCPU struct {
	Name              string  `json:"name"`
	Zone              string  `json:"zone"`
	Rack              string  `json:"rack"`
	CPUUtilizationPct float64 `json:"cpu_utilization_pct"`
	ActivePods        int     `json:"active_pods"`
}

type K8sPayloadCPU struct {
	PodToSchedule  string    `json:"pod_to_schedule"`
	CandidateNodes []NodeCPU `json:"candidate_nodes"`
}

type DependencyCPU struct {
	TargetPod      string  `json:"target_pod"`
	CurrentNode    string  `json:"current_node"`
	BytesPerSecond float64 `json:"bytes_per_second"`
}

type TelemetryPayloadCPU struct {
	TrafficDependencies []DependencyCPU `json:"traffic_dependencies"`
}

// --- Output Struct ---

type NodeScoreCPU struct {
	Node       string  `json:"node"`
	Score      float64 `json:"score"`
	RawNetwork float64 `json:"raw_network"`
	CPU        float64 `json:"cpu"`
	Penalty    float64 `json:"penalty"`
}

// Helper: Calculate distance multiplier
func calculateDistanceCPU(candidate NodeCPU, targetNodeName string, topologyMap map[string]NodeCPU) float64 {
	target, exists := topologyMap[targetNodeName]
	if !exists {
		return CrossAZCPU
	}
	if candidate.Name == target.Name {
		return SameNodeCPU
	} else if candidate.Rack == target.Rack {
		return SameRackCPU
	} else if candidate.Zone == target.Zone {
		return SameAZCPU
	}
	return CrossAZCPU
}

// Helper: Calculate Exponential CPU Penalty
func calculateCPUPenalty(cpuPct float64) float64 {
	exponent := (cpuPct - CPUWarningThreshold) / 10.0

	// Cap the exponent to prevent math overflow on edge cases
	if exponent > 10.0 {
		exponent = 10.0
	} else if exponent < -10.0 {
		exponent = -10.0
	}

	return 1.0 + math.Exp(exponent)
}

// EvaluateNodesCPU is PUBLIC. It combines network cost with an exponential CPU penalty.
func EvaluateNodesCPU(k8sJSON []byte, telemetryJSON []byte) ([]byte, error) {
	var k8sState K8sPayloadCPU
	if err := json.Unmarshal(k8sJSON, &k8sState); err != nil {
		return nil, fmt.Errorf("failed to parse K8s JSON: %w", err)
	}

	var telemetryState TelemetryPayloadCPU
	if err := json.Unmarshal(telemetryJSON, &telemetryState); err != nil {
		return nil, fmt.Errorf("failed to parse Telemetry JSON: %w", err)
	}

	topologyMap := make(map[string]NodeCPU)
	for _, node := range k8sState.CandidateNodes {
		topologyMap[node.Name] = node
	}

	var results []NodeScoreCPU
	for _, candidate := range k8sState.CandidateNodes {
		rawNetworkCost := 0.0

		// 1. Calculate raw network distance
		for _, dep := range telemetryState.TrafficDependencies {
			distance := calculateDistanceCPU(candidate, dep.CurrentNode, topologyMap)
			rawNetworkCost += dep.BytesPerSecond * distance
		}

		// 2. Calculate the CPU penalty
		cpu := candidate.CPUUtilizationPct
		penaltyMultiplier := calculateCPUPenalty(cpu)

		// 3. Final Score
		finalCost := rawNetworkCost * penaltyMultiplier

		results = append(results, NodeScoreCPU{
			Node:       candidate.Name,
			Score:      finalCost,
			RawNetwork: rawNetworkCost,
			CPU:        cpu,
			Penalty:    penaltyMultiplier,
		})
	}

	// Sort nodes by lowest cost (Best fit first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score < results[j].Score
	})

	outputJSON, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to generate output JSON: %w", err)
	}

	return outputJSON, nil
}

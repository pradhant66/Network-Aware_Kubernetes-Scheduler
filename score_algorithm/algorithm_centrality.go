package score_algorithm

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

const (
	SameNodeCent = 0.1
	SameRackCent = 1.0
	SameAZCent   = 5.0
	CrossAZCent  = 20.0

	// HubThreshold: Total bandwidth (B/s) that classifies a pod as a VIP.
	HubThreshold = 10000.0

	// HubCPUFilterThreshold: If a node's CPU is above this, it is instantly disqualified for Hubs.
	HubCPUFilterThreshold = 80.0
)

// --- Input Structs ---

type NodeCent struct {
	Name              string  `json:"name"`
	Zone              string  `json:"zone"`
	Rack              string  `json:"rack"`
	CPUUtilizationPct float64 `json:"cpu_utilization_pct"`
	ActivePods        int     `json:"active_pods"`
}

type K8sPayloadCent struct {
	PodToSchedule  string     `json:"pod_to_schedule"`
	CandidateNodes []NodeCent `json:"candidate_nodes"`
}

type DependencyCent struct {
	TargetPod      string  `json:"target_pod"`
	CurrentNode    string  `json:"current_node"`
	BytesPerSecond float64 `json:"bytes_per_second"`
}

type TelemetryPayloadCent struct {
	TrafficDependencies []DependencyCent `json:"traffic_dependencies"`
}

// --- Output Struct ---

type NodeScoreCent struct {
	Node              string  `json:"node"`
	Score             float64 `json:"score"`
	PodClassification string  `json:"pod_classification"` // "Hub" or "Spoke"
}

func calculateDistanceCent(candidate NodeCent, targetNodeName string, topologyMap map[string]NodeCent) float64 {
	target, exists := topologyMap[targetNodeName]
	if !exists {
		return CrossAZCent
	}
	if candidate.Name == target.Name {
		return SameNodeCent
	} else if candidate.Rack == target.Rack {
		return SameRackCent
	} else if candidate.Zone == target.Zone {
		return SameAZCent
	}
	return CrossAZCent
}

// EvaluateNodesCentrality is the PUBLIC function for the Hub and Spoke algorithm
func EvaluateNodesCentrality(k8sJSON []byte, telemetryJSON []byte) ([]byte, error) {
	var k8sState K8sPayloadCent
	if err := json.Unmarshal(k8sJSON, &k8sState); err != nil {
		return nil, fmt.Errorf("failed to parse K8s JSON: %w", err)
	}

	var telemetryState TelemetryPayloadCent
	if err := json.Unmarshal(telemetryJSON, &telemetryState); err != nil {
		return nil, fmt.Errorf("failed to parse Telemetry JSON: %w", err)
	}

	topologyMap := make(map[string]NodeCent)
	for _, node := range k8sState.CandidateNodes {
		topologyMap[node.Name] = node
	}

	// 1. Calculate Degree Centrality (Total Bandwidth)
	totalAggregateBandwidth := 0.0
	for _, dep := range telemetryState.TrafficDependencies {
		totalAggregateBandwidth += dep.BytesPerSecond
	}

	// 2. Classify the Pod
	isHub := totalAggregateBandwidth >= HubThreshold
	classification := "Spoke"
	if isHub {
		classification = "Hub"
	}

	var results []NodeScoreCent
	for _, candidate := range k8sState.CandidateNodes {
		networkCost := 0.0

		// Always calculate baseline network cost
		for _, dep := range telemetryState.TrafficDependencies {
			distance := calculateDistanceCent(candidate, dep.CurrentNode, topologyMap)
			networkCost += dep.BytesPerSecond * distance
		}

		totalScore := 0.0

		// 3. Piecewise Cost Function
		if isHub && candidate.CPUUtilizationPct > HubCPUFilterThreshold {
			// HARD FILTER: Node is too crowded. Give it an impossibly high score.
			totalScore = math.MaxFloat64
		} else if isHub {
			// Hub on a safe node: Apply a gentle CPU multiplier to break ties
			cpuMultiplier := 1.0 + (candidate.CPUUtilizationPct / 100.0)
			totalScore = networkCost * cpuMultiplier
		} else {
			// Spoke: Pure greedy network cost
			totalScore = networkCost
		}

		results = append(results, NodeScoreCent{
			Node:              candidate.Name,
			Score:             totalScore,
			PodClassification: classification,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score < results[j].Score
	})

	outputJSON, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to generate output JSON: %w", err)
	}

	return outputJSON, nil
}

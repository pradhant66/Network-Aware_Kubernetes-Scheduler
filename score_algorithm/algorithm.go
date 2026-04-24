package score_algorithm

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Constants for Network Distance
const (
	SameNode = 0.1
	SameRack = 1.0
	SameAZ   = 5.0
	CrossAZ  = 20.0
)

// --- Input Structs ---

type Node struct {
	Name              string  `json:"name"`
	Zone              string  `json:"zone"`
	Rack              string  `json:"rack"`
	CPUUtilizationPct float64 `json:"cpu_utilization_pct"` // Ready for Phase 2
	ActivePods        int     `json:"active_pods"`
}

// Payload 1 (Core K8s)
type K8sPayload struct {
	PodToSchedule  string `json:"pod_to_schedule"`
	CandidateNodes []Node `json:"candidate_nodes"`
}

type Dependency struct {
	TargetPod      string  `json:"target_pod"`
	CurrentNode    string  `json:"current_node"`
	BytesPerSecond float64 `json:"bytes_per_second"`
}

// Payload 2 (Telemetry)
type TelemetryPayload struct {
	TrafficDependencies []Dependency `json:"traffic_dependencies"`
}

// --- Output Struct ---

type NodeScore struct {
	Node  string  `json:"node"`
	Score float64 `json:"score"`
}

func calculateDistanceMultiplier(candidate Node, targetNodeName string, topologyMap map[string]Node) float64 {
	target, exists := topologyMap[targetNodeName]
	if !exists {
		return CrossAZ
	}
	if candidate.Name == target.Name {
		return SameNode
	} else if candidate.Rack == target.Rack {
		return SameRack
	} else if candidate.Zone == target.Zone {
		return SameAZ
	}
	return CrossAZ
}

// EvaluateNodes is PUBLIC. It takes two JSON byte arrays and returns one JSON byte array.
func EvaluateNodes(k8sJSON []byte, telemetryJSON []byte) ([]byte, error) {
	// 1. Parse the incoming JSON payloads
	var k8sState K8sPayload
	if err := json.Unmarshal(k8sJSON, &k8sState); err != nil {
		return nil, fmt.Errorf("failed to parse K8s JSON: %w", err)
	}

	var telemetryState TelemetryPayload
	if err := json.Unmarshal(telemetryJSON, &telemetryState); err != nil {
		return nil, fmt.Errorf("failed to parse Telemetry JSON: %w", err)
	}

	// 2. Build a quick lookup map for the current topology
	topologyMap := make(map[string]Node)
	for _, node := range k8sState.CandidateNodes {
		topologyMap[node.Name] = node
	}

	// 3. Calculate scores using the Naive Greedy logic
	var results []NodeScore
	for _, candidate := range k8sState.CandidateNodes {
		totalNetworkCost := 0.0

		for _, dep := range telemetryState.TrafficDependencies {
			distance := calculateDistanceMultiplier(candidate, dep.CurrentNode, topologyMap)
			totalNetworkCost += dep.BytesPerSecond * distance
		}

		results = append(results, NodeScore{
			Node:  candidate.Name,
			Score: totalNetworkCost,
		})
	}

	// 4. Sort nodes by lowest cost (Best fit first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score < results[j].Score
	})

	// 5. Convert the final slice of results back into a JSON byte array
	outputJSON, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to generate output JSON: %w", err)
	}

	return outputJSON, nil
}

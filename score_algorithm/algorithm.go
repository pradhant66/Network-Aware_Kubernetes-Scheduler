package score_algorithm

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
)

// Constants for Network Distance
const (
	SameNode = 0.1
	SameRack = 1.0
	SameAZ   = 5.0
	CrossAZ  = 20.0
)

const (
	ModeNetworkOnly  = "network-only"
	ModeCPUProximity = "cpu-proximity"
	ModeCentrality   = "centrality"
	ModePID          = "pid"

	algorithmModeEnv    = "TOPO_SCORING_MODE"
	cpuWarningThreshold = 80.0
	hubThreshold        = 10000.0
	pidTargetCrossRackBps = 5000.0
	pidKp                 = 0.005
	pidKi                 = 0.001
	pidKd                 = 0.002
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
	Controller     *PIDControllerState `json:"controller,omitempty"`
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
	Node              string  `json:"node"`
	Score             float64 `json:"score"`
	RawNetworkCost    float64 `json:"raw_network_cost,omitempty"`
	CPUPenalty        float64 `json:"cpu_penalty,omitempty"`
	CPUUtilizationPct float64 `json:"cpu_utilization_pct,omitempty"`
	PodClassification string  `json:"pod_classification,omitempty"`
	DynamicWeight     float64 `json:"dynamic_weight,omitempty"`
}

type PIDControllerState struct {
	CurrentCrossRackBps float64 `json:"current_cluster_cross_rack_bps"`
	Integral            float64 `json:"integral"`
	PrevError           float64 `json:"prev_error"`
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

func calculateCPUPenalty(cpuPct float64) float64 {
	exponent := (cpuPct - cpuWarningThreshold) / 10.0
	if exponent < -10 {
		exponent = -10
	}
	if exponent > 10 {
		exponent = 10
	}
	return 1.0 + math.Exp(exponent)
}

func scoringMode() string {
	mode := os.Getenv(algorithmModeEnv)
	switch mode {
	case "", ModeNetworkOnly:
		return ModeNetworkOnly
	case ModeCPUProximity:
		return ModeCPUProximity
	case ModeCentrality:
		return ModeCentrality
	case ModePID:
		return ModePID
	default:
		return ModeNetworkOnly
	}
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

	mode := scoringMode()
	totalAggregateBandwidth := 0.0
	for _, dep := range telemetryState.TrafficDependencies {
		totalAggregateBandwidth += dep.BytesPerSecond
	}

	classification := "Spoke"
	if totalAggregateBandwidth >= hubThreshold {
		classification = "Hub"
	}

	dynamicWeight := 1.0
	if mode == ModePID && k8sState.Controller != nil {
		errVal := k8sState.Controller.CurrentCrossRackBps - pidTargetCrossRackBps
		pTerm := pidKp * errVal
		iTerm := pidKi * k8sState.Controller.Integral
		dTerm := pidKd * (errVal - k8sState.Controller.PrevError)
		dynamicWeight = math.Max(1.0, 1.0+pTerm+iTerm+dTerm)
	}

	// 3. Calculate scores using the selected logic
	var results []NodeScore
	for _, candidate := range k8sState.CandidateNodes {
		totalNetworkCost := 0.0

		for _, dep := range telemetryState.TrafficDependencies {
			distance := calculateDistanceMultiplier(candidate, dep.CurrentNode, topologyMap)
			if mode == ModePID && distance > SameRack {
				distance *= dynamicWeight
			}
			totalNetworkCost += dep.BytesPerSecond * distance
		}

		finalCost := totalNetworkCost
		cpuPenalty := 1.0
		if mode == ModeCPUProximity {
			cpuPenalty = calculateCPUPenalty(candidate.CPUUtilizationPct)
			finalCost = totalNetworkCost * cpuPenalty
		} else if mode == ModeCentrality {
			if classification == "Hub" && candidate.CPUUtilizationPct > cpuWarningThreshold {
				finalCost = math.MaxFloat64
			} else if classification == "Hub" {
				cpuPenalty = 1.0 + (candidate.CPUUtilizationPct / 100.0)
				finalCost = totalNetworkCost * cpuPenalty
			}
		}

		results = append(results, NodeScore{
			Node:              candidate.Name,
			Score:             finalCost,
			RawNetworkCost:    totalNetworkCost,
			CPUPenalty:        cpuPenalty,
			CPUUtilizationPct: candidate.CPUUtilizationPct,
			PodClassification: classification,
			DynamicWeight:     dynamicWeight,
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

package score_algorithm

import (
	"encoding/json"
	"fmt"
	"sort"
)

const (
	SameNodeWin = 0.1
	SameRackWin = 1.0
	SameAZWin   = 5.0
	CrossAZWin  = 20.0

	// EMASmoothingFactor determines how heavily to weight the most recent traffic sample.
	// 0.5 is a balanced starting point.
	EMASmoothingFactor = 0.5
)

// --- Input Structs ---

type NodeWin struct {
	Name              string  `json:"name"`
	Zone              string  `json:"zone"`
	Rack              string  `json:"rack"`
	CPUUtilizationPct float64 `json:"cpu_utilization_pct"`
	ActivePods        int     `json:"active_pods"`
}

type K8sPayloadWin struct {
	PodToSchedule  string    `json:"pod_to_schedule"`
	CandidateNodes []NodeWin `json:"candidate_nodes"`
}

type DependencyWin struct {
	TargetPod         string    `json:"target_pod"`
	CurrentNode       string    `json:"current_node"`
	TrafficSamplesBps []float64 `json:"traffic_samples_bps"` // NEW: Array of historical data
}

type TelemetryPayloadWin struct {
	TrafficDependencies []DependencyWin `json:"traffic_dependencies"`
}

// --- Output Struct ---

type NodeScoreWin struct {
	Node    string  `json:"node"`
	Score   float64 `json:"score"`
	EmaUsed float64 `json:"ema_used"` // Exposing this for debugging/visibility
}

// Helper: Calculate Exponential Moving Average
func calculateEMA(samples []float64, alpha float64) float64 {
	if len(samples) == 0 {
		return 0.0
	}

	// Start the EMA with the oldest data point in the array (index 0)
	ema := samples[0]

	// Iterate forward through time to the newest data point
	for i := 1; i < len(samples); i++ {
		ema = (alpha * samples[i]) + ((1.0 - alpha) * ema)
	}

	return ema
}

func calculateDistanceWin(candidate NodeWin, targetNodeName string, topologyMap map[string]NodeWin) float64 {
	target, exists := topologyMap[targetNodeName]
	if !exists {
		return CrossAZWin
	}
	if candidate.Name == target.Name {
		return SameNodeWin
	} else if candidate.Rack == target.Rack {
		return SameRackWin
	} else if candidate.Zone == target.Zone {
		return SameAZWin
	}
	return CrossAZWin
}

// EvaluateNodesRollingWindow is the PUBLIC function for this specific algorithm
func EvaluateNodesRollingWindow(k8sJSON []byte, telemetryJSON []byte) ([]byte, error) {
	var k8sState K8sPayloadWin
	if err := json.Unmarshal(k8sJSON, &k8sState); err != nil {
		return nil, fmt.Errorf("failed to parse K8s JSON: %w", err)
	}

	var telemetryState TelemetryPayloadWin
	if err := json.Unmarshal(telemetryJSON, &telemetryState); err != nil {
		return nil, fmt.Errorf("failed to parse Telemetry JSON: %w", err)
	}

	topologyMap := make(map[string]NodeWin)
	for _, node := range k8sState.CandidateNodes {
		topologyMap[node.Name] = node
	}

	var results []NodeScoreWin
	for _, candidate := range k8sState.CandidateNodes {
		totalNetworkCost := 0.0
		totalEMA := 0.0

		for _, dep := range telemetryState.TrafficDependencies {
			distance := calculateDistanceWin(candidate, dep.CurrentNode, topologyMap)

			// Calculate the smoothed traffic volume using our array of samples
			smoothedTraffic := calculateEMA(dep.TrafficSamplesBps, EMASmoothingFactor)

			totalNetworkCost += smoothedTraffic * distance
			totalEMA += smoothedTraffic // Storing just for output visibility
		}

		results = append(results, NodeScoreWin{
			Node:    candidate.Name,
			Score:   totalNetworkCost,
			EmaUsed: totalEMA,
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

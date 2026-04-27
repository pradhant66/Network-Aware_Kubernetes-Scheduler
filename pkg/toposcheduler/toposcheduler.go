// pkg/toposcheduler/toposcheduler.go
package toposcheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
	"sigs.k8s.io/scheduler-plugins/score_algorithm"
)

const Name = "TopoScheduler"

const (
	zoneLabel          = "topology.kubernetes.io/zone"
	rackLabel          = "topology.kubernetes.io/rack"
	trafficGraphURLEnv = "TOPO_TRAFFIC_GRAPH_URL"
	trafficGraphFileEnv = "TOPO_TRAFFIC_GRAPH_FILE"
)

type TopoScheduler struct {
	handle framework.Handle
	mu     sync.Mutex
	pid    pidStateCache
}

type pidStateCache struct {
	Integral     float64
	PrevError    float64
	LastPodKey   string
	LastSnapshot score_algorithm.PIDControllerState
}

type CandidateNode struct {
	Name              string  `json:"name"`
	Zone              string  `json:"zone"`
	Rack              string  `json:"rack"`
	CPUUtilizationPct float64 `json:"cpu_utilization_pct"`
	ActivePods        int     `json:"active_pods"`
}

type AlgorithmInput struct {
	PodToSchedule       string              `json:"pod_to_schedule"`
	CandidateNodes      []CandidateNode     `json:"candidate_nodes"`
	TrafficDependencies []TrafficDependency `json:"traffic_dependencies"`
}

type TrafficGraphEntry struct {
	SourceApp           string              `json:"source_app"`
	PodName             string              `json:"pod_name"`
	Namespace           string              `json:"namespace"`
	TrafficDependencies []TrafficDependency `json:"traffic_dependencies"`
}

type TrafficDependency struct {
	TargetPod       string  `json:"target_pod"`
	RequestsPerSec  float64 `json:"requests_per_second"`
	P99LatencyMS    float64 `json:"p99_latency_ms"`
	BytesPerSec     float64 `json:"bytes_per_second"`
	CurrentNodeName string  `json:"current_node,omitempty"`
}

var _ framework.ScorePlugin = &TopoScheduler{}

func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	return &TopoScheduler{handle: h}, nil
}

func (n *TopoScheduler) Name() string {
	return Name
}

func candidateFromNodeInfo(nodeInfo fwk.NodeInfo) (CandidateNode, *fwk.Status) {
	node := nodeInfo.Node()
	if node == nil {
		return CandidateNode{}, fwk.NewStatus(fwk.Error, "node not found")
	}

	labels := node.Labels
	return CandidateNode{
		Name:              node.Name,
		Zone:              labels[zoneLabel],
		Rack:              labels[rackLabel],
		CPUUtilizationPct: cpuRequestsPct(nodeInfo),
		ActivePods:        len(nodeInfo.GetPods()),
	}, nil
}

func cpuRequestsPct(nodeInfo fwk.NodeInfo) float64 {
	allocatableCPU := nodeInfo.GetAllocatable().GetMilliCPU()
	if allocatableCPU == 0 {
		return 0
	}

	requestedCPU := nodeInfo.GetRequested().GetMilliCPU()
	return float64(requestedCPU) * 100 / float64(allocatableCPU)
}

func sourceAppForPod(pod *v1.Pod) string {
	labels := pod.GetLabels()
	for _, key := range []string{"app", "app.kubernetes.io/name", "run"} {
		if value := labels[key]; value != "" {
			return value
		}
	}

	parts := strings.Split(pod.Name, "-")
	if len(parts) >= 3 && looksLikeReplicaSuffix(parts[len(parts)-1]) && looksLikeReplicaSuffix(parts[len(parts)-2]) {
		return strings.Join(parts[:len(parts)-2], "-")
	}

	return pod.Name
}

func looksLikeReplicaSuffix(value string) bool {
	if len(value) < 5 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func (n *TopoScheduler) trafficForPod(ctx context.Context, pod *v1.Pod) ([]TrafficGraphEntry, *TrafficGraphEntry, error) {
	url := os.Getenv(trafficGraphURLEnv)
	graph, err := n.loadTrafficGraph(ctx, url)
	if err != nil {
		return nil, nil, err
	}

	sourceApp := sourceAppForPod(pod)
	for i := range graph {
		if matchesTrafficEntry(graph[i], pod, sourceApp) {
			n.attachTargetNodeNames(pod.Namespace, graph[i].TrafficDependencies)
			return graph, &graph[i], nil
		}
	}

	return graph, nil, nil
}

func (n *TopoScheduler) loadTrafficGraph(ctx context.Context, url string) ([]TrafficGraphEntry, error) {
	if url != "" {
		requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("telemetry API returned status %s", resp.Status)
		}

		var graph []TrafficGraphEntry
		if err := json.NewDecoder(resp.Body).Decode(&graph); err != nil {
			return nil, err
		}
		return graph, nil
	}

	file := os.Getenv(trafficGraphFileEnv)
	if file == "" {
		file = "topology_graph.json"
	}

	data, err := os.ReadFile(filepath.Clean(file))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var graph []TrafficGraphEntry
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, err
	}
	return graph, nil
}

func matchesTrafficEntry(entry TrafficGraphEntry, pod *v1.Pod, sourceApp string) bool {
	if entry.Namespace != "" && entry.Namespace != pod.Namespace {
		return false
	}
	if entry.SourceApp == sourceApp || entry.PodName == pod.Name || entry.PodName == sourceApp {
		return true
	}
	return false
}

func algorithmInputForCandidate(pod *v1.Pod, candidate CandidateNode, traffic *TrafficGraphEntry) AlgorithmInput {
	input := AlgorithmInput{
		PodToSchedule:  pod.Name,
		CandidateNodes: []CandidateNode{candidate},
	}
	if traffic != nil {
		input.TrafficDependencies = traffic.TrafficDependencies
	}
	return input
}

func (n *TopoScheduler) attachTargetNodeNames(namespace string, dependencies []TrafficDependency) {
	podLister := n.handle.SharedInformerFactory().Core().V1().Pods().Lister()
	for i := range dependencies {
		targetPod, err := podLister.Pods(namespace).Get(dependencies[i].TargetPod)
		if err != nil {
			continue
		}
		dependencies[i].CurrentNodeName = targetPod.Spec.NodeName
	}
}

func (n *TopoScheduler) candidateNodes() ([]CandidateNode, *fwk.Status) {
	nodeInfos, err := n.handle.SnapshotSharedLister().NodeInfos().List()
	if err != nil {
		return nil, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to list node infos: %v", err))
	}

	candidates := make([]CandidateNode, 0, len(nodeInfos))
	for _, nodeInfo := range nodeInfos {
		candidate, status := candidateFromNodeInfo(nodeInfo)
		if status != nil {
			return nil, status
		}
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

func nodeByName(candidates []CandidateNode) map[string]CandidateNode {
	byName := make(map[string]CandidateNode, len(candidates))
	for _, candidate := range candidates {
		byName[candidate.Name] = candidate
	}
	return byName
}

func (n *TopoScheduler) sourceNodeForEntry(entry TrafficGraphEntry) string {
	podLister := n.handle.SharedInformerFactory().Core().V1().Pods().Lister()

	if entry.PodName != "" && entry.Namespace != "" {
		if pod, err := podLister.Pods(entry.Namespace).Get(entry.PodName); err == nil {
			return pod.Spec.NodeName
		}
	}

	if entry.SourceApp == "" || entry.Namespace == "" {
		return ""
	}

	pods, err := podLister.Pods(entry.Namespace).List(nil)
	if err != nil {
		return ""
	}

	for _, pod := range pods {
		if pod.Spec.NodeName == "" {
			continue
		}
		if sourceAppForPod(pod) == entry.SourceApp {
			return pod.Spec.NodeName
		}
	}

	return ""
}

func (n *TopoScheduler) currentCrossRackBps(graph []TrafficGraphEntry, candidates []CandidateNode) float64 {
	nodes := nodeByName(candidates)
	total := 0.0

	for _, entry := range graph {
		sourceNodeName := n.sourceNodeForEntry(entry)
		if sourceNodeName == "" {
			continue
		}

		sourceNode, ok := nodes[sourceNodeName]
		if !ok {
			continue
		}

		for _, dep := range entry.TrafficDependencies {
			targetNode, ok := nodes[dep.CurrentNodeName]
			if !ok {
				continue
			}

			if sourceNode.Name == targetNode.Name {
				continue
			}
			if sourceNode.Rack != "" && targetNode.Rack != "" && sourceNode.Rack == targetNode.Rack {
				continue
			}
			total += dep.BytesPerSec
		}
	}

	return total
}

func (n *TopoScheduler) pidSnapshotForPod(pod *v1.Pod, currentCrossRackBps float64) *score_algorithm.PIDControllerState {
	podKey := string(pod.UID)
	if podKey == "" {
		podKey = pod.Namespace + "/" + pod.Name
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	if n.pid.LastPodKey == podKey {
		snapshot := n.pid.LastSnapshot
		snapshot.CurrentCrossRackBps = currentCrossRackBps
		return &snapshot
	}

	snapshot := score_algorithm.PIDControllerState{
		CurrentCrossRackBps: currentCrossRackBps,
		Integral:            n.pid.Integral,
		PrevError:           n.pid.PrevError,
	}

	errVal := currentCrossRackBps - 5000.0
	n.pid.Integral += errVal
	n.pid.PrevError = errVal
	n.pid.LastPodKey = podKey
	n.pid.LastSnapshot = snapshot

	return &snapshot
}

func scoreForCandidate(candidateName string, results []score_algorithm.NodeScore) int64 {
	if len(results) == 0 {
		return framework.MinNodeScore
	}

	minCost := results[0].Score
	maxCost := results[0].Score
	selectedCost := maxCost
	found := false

	for _, result := range results {
		if result.Score < minCost {
			minCost = result.Score
		}
		if result.Score > maxCost {
			maxCost = result.Score
		}
		if result.Node == candidateName {
			selectedCost = result.Score
			found = true
		}
	}

	if !found {
		return framework.MinNodeScore
	}
	if maxCost == minCost {
		return framework.MaxNodeScore
	}

	normalized := float64(framework.MaxNodeScore) * (maxCost - selectedCost) / (maxCost - minCost)
	if normalized < float64(framework.MinNodeScore) {
		return framework.MinNodeScore
	}
	if normalized > float64(framework.MaxNodeScore) {
		return framework.MaxNodeScore
	}
	return int64(normalized)
}

// Score signature updated to match the latest K8s API (no pointer on CycleState, uses NodeInfo instead of string)
func (n *TopoScheduler) Score(ctx context.Context, state fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	candidate, status := candidateFromNodeInfo(nodeInfo)
	if status != nil {
		return 0, status
	}

	klog.Infof("[TopoScheduler] Evaluating Pod %s/%s on Node %s zone=%s rack=%s",
		pod.Namespace,
		pod.Name,
		candidate.Name,
		candidate.Zone,
		candidate.Rack,
	)
	klog.Infof("[TopoScheduler] Candidate node resources node=%s cpu_requests_pct=%.2f active_pods=%d",
		candidate.Name,
		candidate.CPUUtilizationPct,
		candidate.ActivePods,
	)

	graph, traffic, err := n.trafficForPod(ctx, pod)
	if err != nil {
		klog.ErrorS(err, "Failed to fetch telemetry graph", "pod", pod.Name)
	} else if traffic != nil {
		klog.Infof("[TopoScheduler] Traffic graph for source_app=%s dependencies=%d",
			traffic.SourceApp,
			len(traffic.TrafficDependencies),
		)
		for _, dependency := range traffic.TrafficDependencies {
			klog.Infof("[TopoScheduler] dependency target_pod=%s target_node=%s rps=%.2f p99_ms=%.2f bytes_per_sec=%.2f",
				dependency.TargetPod,
				dependency.CurrentNodeName,
				dependency.RequestsPerSec,
				dependency.P99LatencyMS,
				dependency.BytesPerSec,
			)
		}
	} else {
		klog.V(4).Info("No telemetry graph entry found for pod", "pod", pod.Name, "sourceApp", sourceAppForPod(pod))
	}

	algorithmInput := algorithmInputForCandidate(pod, candidate, traffic)
	klog.Infof("[TopoScheduler] Algorithm contract pod_to_schedule=%s candidate_nodes=%d traffic_dependencies=%d",
		algorithmInput.PodToSchedule,
		len(algorithmInput.CandidateNodes),
		len(algorithmInput.TrafficDependencies),
	)

	candidateNodes, status := n.candidateNodes()
	if status != nil {
		return 0, status
	}

	var controller *score_algorithm.PIDControllerState
	if os.Getenv("TOPO_SCORING_MODE") == score_algorithm.ModePID {
		controller = n.pidSnapshotForPod(pod, n.currentCrossRackBps(graph, candidateNodes))
		klog.Infof("[TopoScheduler] PID controller pod=%s current_cross_rack_bps=%.2f integral=%.2f prev_error=%.2f",
			pod.Name,
			controller.CurrentCrossRackBps,
			controller.Integral,
			controller.PrevError,
		)
	}

	k8sPayload, err := json.Marshal(struct {
		PodToSchedule  string                             `json:"pod_to_schedule"`
		CandidateNodes []CandidateNode                    `json:"candidate_nodes"`
		Controller     *score_algorithm.PIDControllerState `json:"controller,omitempty"`
	}{
		PodToSchedule:  pod.Name,
		CandidateNodes: candidateNodes,
		Controller:     controller,
	})
	if err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to marshal k8s payload: %v", err))
	}

	telemetryPayload, err := json.Marshal(struct {
		TrafficDependencies []TrafficDependency `json:"traffic_dependencies"`
	}{
		TrafficDependencies: algorithmInput.TrafficDependencies,
	})
	if err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to marshal telemetry payload: %v", err))
	}

	resultJSON, err := score_algorithm.EvaluateNodes(k8sPayload, telemetryPayload)
	if err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to evaluate nodes: %v", err))
	}

	var results []score_algorithm.NodeScore
	if err := json.Unmarshal(resultJSON, &results); err != nil {
		return 0, fwk.NewStatus(fwk.Error, fmt.Sprintf("failed to parse node scores: %v", err))
	}

	score := scoreForCandidate(candidate.Name, results)
	for _, result := range results {
		if result.Node == candidate.Name {
			klog.Infof("[TopoScheduler] Final score pod=%s node=%s score=%d raw_network_cost=%.2f cpu_penalty=%.2f cpu_pct=%.2f dynamic_weight=%.2f mode=%s",
				pod.Name,
				candidate.Name,
				score,
				result.RawNetworkCost,
				result.CPUPenalty,
				result.CPUUtilizationPct,
				result.DynamicWeight,
				os.Getenv("TOPO_SCORING_MODE"),
			)
			break
		}
	}

	return score, fwk.NewStatus(fwk.Success, "")
}

func (n *TopoScheduler) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

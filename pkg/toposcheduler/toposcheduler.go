// pkg/toposcheduler/toposcheduler.go
package toposcheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const Name = "TopoScheduler"

const (
	zoneLabel          = "topology.kubernetes.io/zone"
	rackLabel          = "topology.kubernetes.io/rack"
	trafficGraphURLEnv = "TOPO_TRAFFIC_GRAPH_URL"
)

type TopoScheduler struct {
	handle framework.Handle
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

func (n *TopoScheduler) trafficForPod(ctx context.Context, pod *v1.Pod) (*TrafficGraphEntry, error) {
	url := os.Getenv(trafficGraphURLEnv)
	if url == "" {
		return nil, nil
	}

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

	sourceApp := sourceAppForPod(pod)
	for i := range graph {
		if graph[i].Namespace == pod.Namespace && graph[i].SourceApp == sourceApp {
			n.attachTargetNodeNames(pod.Namespace, graph[i].TrafficDependencies)
			return &graph[i], nil
		}
	}

	return nil, nil
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

	traffic, err := n.trafficForPod(ctx, pod)
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

	score := rand.Int63n(100)

	return score, fwk.NewStatus(fwk.Success, "")
}

func (n *TopoScheduler) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

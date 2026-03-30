// pkg/toposcheduler/toposcheduler.go
package toposcheduler

import (
	"context"
	"math/rand"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"

	fwk "k8s.io/kube-scheduler/framework"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const Name = "TopoScheduler"

type TopoScheduler struct {
	handle framework.Handle
}

var _ framework.ScorePlugin = &TopoScheduler{}

func New(_ context.Context, _ runtime.Object, h framework.Handle) (framework.Plugin, error) {
	return &TopoScheduler{handle: h}, nil
}

func (n *TopoScheduler) Name() string {
	return Name
}

// Score signature updated to match the latest K8s API (no pointer on CycleState, uses NodeInfo instead of string)
func (n *TopoScheduler) Score(ctx context.Context, state fwk.CycleState, pod *v1.Pod, nodeInfo fwk.NodeInfo) (int64, *fwk.Status) {
	
	// Extract the node name from the NodeInfo object for logging
	nodeName := nodeInfo.Node().Name
	
	klog.Infof("🚀 [TopoScheduler] Evaluating Pod %s on Node %s", pod.Name, nodeName)

	score := rand.Int63n(100)

	return score, fwk.NewStatus(fwk.Success, "")
}

func (n *TopoScheduler) ScoreExtensions() framework.ScoreExtensions {
	return nil
}
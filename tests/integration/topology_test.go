package integration

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/model"
)

func TestTopologyGroupsPodsByNode(t *testing.T) {
	client, _ := NewFakeClient("",
		NewNode("node-a", true),
		NewNode("node-b", false), // NotReady
		NewPodOnNode("demo", "web-1", "node-a", "Running"),
		NewPodOnNode("demo", "web-2", "node-a", "Running"),
		NewPodOnNode("prod", "api-0", "node-b", "Running"),
		NewPodOnNode("demo", "pending-0", "", "Pending"), // unscheduled
	)

	nodes, err := client.Topology(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]model.TopologyNode{}
	for _, n := range nodes {
		byName[n.Name] = n
	}

	if a, ok := byName["node-a"]; !ok || len(a.Pods) != 2 || a.Status != model.HealthOk {
		t.Errorf("node-a: got %+v, want 2 pods and Ok", a)
	}
	if b, ok := byName["node-b"]; !ok || len(b.Pods) != 1 || b.Status != model.HealthError {
		t.Errorf("node-b: got %+v, want 1 pod and Error (NotReady)", b)
	}
	if u, ok := byName["(unscheduled)"]; !ok || len(u.Pods) != 1 {
		t.Errorf("expected 1 unscheduled pod, got %+v", u)
	}
	if got := countRealNodesHelper(nodes); got != 2 {
		t.Errorf("expected 2 real nodes, got %d", got)
	}
}

func countRealNodesHelper(nodes []model.TopologyNode) int {
	n := 0
	for _, nd := range nodes {
		if nd.Reason != "unscheduled" {
			n++
		}
	}
	return n
}

func TestTopologyComputesRequestsAndCapacity(t *testing.T) {
	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": "n1"},
		"status": map[string]any{
			"conditions":  []any{map[string]any{"type": "Ready", "status": "True"}},
			"allocatable": map[string]any{"cpu": "4", "memory": "8Gi"},
		},
	}}
	pod := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "p1", "namespace": "demo"},
		"spec": map[string]any{"nodeName": "n1", "containers": []any{
			map[string]any{"name": "c", "resources": map[string]any{
				"requests": map[string]any{"cpu": "500m", "memory": "512Mi"},
			}},
		}},
		"status": map[string]any{"phase": "Running"},
	}}

	client, _ := NewFakeClient("", node, pod)
	nodes, err := client.Topology(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var n1 model.TopologyNode
	for _, n := range nodes {
		if n.Name == "n1" {
			n1 = n
		}
	}
	if n1.AllocCPU != 4 {
		t.Errorf("AllocCPU=%v want 4", n1.AllocCPU)
	}
	if n1.AllocMem != 8*1024*1024*1024 {
		t.Errorf("AllocMem=%v want 8Gi", n1.AllocMem)
	}
	if n1.ReqCPU != 0.5 {
		t.Errorf("ReqCPU=%v want 0.5", n1.ReqCPU)
	}
	if n1.ReqMem != 512*1024*1024 {
		t.Errorf("ReqMem=%v want 512Mi", n1.ReqMem)
	}
	if len(n1.Pods) != 1 || n1.Pods[0].CPUReq != 0.5 {
		t.Errorf("pod requests not computed: %+v", n1.Pods)
	}
}

func TestTopologyRanksBiggestPodFirst(t *testing.T) {
	node := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"name": "n1"},
		"status": map[string]any{
			"conditions":  []any{map[string]any{"type": "Ready", "status": "True"}},
			"allocatable": map[string]any{"cpu": "8", "memory": "16Gi"},
		},
	}}
	small := podReq("demo", "small", "n1", "500m", "256Mi")
	big := podReq("demo", "big", "n1", "4", "1Gi")
	client, _ := NewFakeClient("", node, small, big)
	nodes, err := client.Topology(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	var n1 model.TopologyNode
	for _, n := range nodes {
		if n.Name == "n1" {
			n1 = n
		}
	}
	if len(n1.Pods) != 2 || n1.Pods[0].Name != "big" {
		t.Fatalf("biggest pod must rank first, got %+v", n1.Pods)
	}
}

func podReq(ns, name, node, cpu, mem string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{"nodeName": node, "containers": []any{
			map[string]any{"name": "c", "resources": map[string]any{
				"requests": map[string]any{"cpu": cpu, "memory": mem},
			}},
		}},
		"status": map[string]any{"phase": "Running"},
	}}
}

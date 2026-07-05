package integration

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestListPods(t *testing.T) {
	client, _ := NewFakeClient("demo",
		NewPod("demo", "web-1", "Running"),
		NewPod("demo", "web-2", "Pending"),
		NewPod("other", "elsewhere", "Running"),
	)
	objs, err := client.List(context.Background(), PodsType, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 pods in demo, got %d", len(objs))
	}
	byName := map[string]model.ResourceObject{}
	for _, o := range objs {
		byName[o.Name] = o
	}
	if byName["web-1"].Status.Level != model.HealthOk {
		t.Errorf("Running pod should be Ok, got %v", byName["web-1"].Status.Level)
	}
	if byName["web-2"].Status.Level != model.HealthWarning {
		t.Errorf("Pending pod should be Warning, got %v", byName["web-2"].Status.Level)
	}
}

func TestParseResourceTypesFiltersSubresourcesAndFlagsCRDs(t *testing.T) {
	lists := []*metav1.APIResourceList{
		DiscoveryList("v1",
			metav1.APIResource{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			metav1.APIResource{Name: "pods/log", Kind: "Pod", Namespaced: true, Verbs: []string{"get"}},
			metav1.APIResource{Name: "bindings", Kind: "Binding", Namespaced: true, Verbs: []string{"create"}}, // not listable
		),
		DiscoveryList("example.com/v1",
			metav1.APIResource{Name: "widgets", Kind: "Widget", Namespaced: true, Verbs: []string{"list", "get"}},
		),
	}
	types := kube.ParseResourceTypes(lists)

	var haveWidget, haveCRDFlag, havePods bool
	for _, ty := range types {
		if ty.Resource == "pods/log" || ty.Resource == "bindings" {
			t.Fatalf("subresource/non-listable type leaked: %s", ty.Resource)
		}
		if ty.Resource == "pods" {
			havePods = true
			if ty.IsCRD {
				t.Errorf("pods should not be flagged as CRD")
			}
		}
		if ty.Resource == "widgets" {
			haveWidget = true
			haveCRDFlag = ty.IsCRD
		}
	}
	if !havePods || !haveWidget {
		t.Fatalf("expected pods and widgets in discovered types, got %+v", types)
	}
	if !haveCRDFlag {
		t.Errorf("widgets (example.com) should be flagged as CRD")
	}
}

func TestStreamPodLogsCompletesWithoutError(t *testing.T) {
	client, _ := NewFakeClient("demo", NewPod("demo", "web-1", "Running"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := client.StreamPodLogs(ctx, "demo", "web-1", "", 10, false)
	var done bool
	for line := range ch {
		if line.Err != nil {
			t.Fatalf("unexpected log stream error: %v", line.Err)
		}
		if line.Done {
			done = true
		}
	}
	if !done {
		t.Fatalf("log stream did not signal completion")
	}
}

func TestStreamWorkloadLogsMergesPods(t *testing.T) {
	p1 := NewPod("demo", "web-1", "Running")
	p1.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"app": "web"}
	p2 := NewPod("demo", "web-2", "Running")
	p2.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"app": "web"}
	client, _ := NewFakeClient("demo", p1, p2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := client.StreamWorkloadLogs(ctx, "demo", "app=web", 10, false)

	pods := map[string]bool{}
	done := false
	for line := range ch {
		if line.Err != nil {
			t.Fatalf("unexpected error: %v", line.Err)
		}
		if line.Pod != "" && line.Text != "" {
			pods[line.Pod] = true
		}
		if line.Done {
			done = true
		}
	}
	if !done {
		t.Fatal("merged stream must signal completion")
	}
	if !pods["web-1"] || !pods["web-2"] {
		t.Fatalf("lines from both pods expected, got %v", pods)
	}
}

func TestReadyCount(t *testing.T) {
	dep := map[string]interface{}{
		"spec":   map[string]interface{}{"replicas": int64(3)},
		"status": map[string]interface{}{"readyReplicas": int64(2)},
	}
	if r, d, ok := kube.ReadyCount("Deployment", dep); !ok || r != 2 || d != 3 {
		t.Errorf("deployment: %d/%d ok=%v", r, d, ok)
	}
	ds := map[string]interface{}{
		"status": map[string]interface{}{"desiredNumberScheduled": int64(5), "numberReady": int64(5)},
	}
	if r, d, ok := kube.ReadyCount("DaemonSet", ds); !ok || r != 5 || d != 5 {
		t.Errorf("daemonset: %d/%d ok=%v", r, d, ok)
	}
	pod := map[string]interface{}{
		"status": map[string]interface{}{"containerStatuses": []interface{}{
			map[string]interface{}{"ready": true},
			map[string]interface{}{"ready": false},
		}},
	}
	if r, d, ok := kube.ReadyCount("Pod", pod); !ok || r != 1 || d != 2 {
		t.Errorf("pod: %d/%d ok=%v", r, d, ok)
	}
	if _, _, ok := kube.ReadyCount("ConfigMap", map[string]interface{}{}); ok {
		t.Error("configmap must have no ready notion")
	}
}

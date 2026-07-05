package integration

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestOwnerExtractsFirstOwnerReference(t *testing.T) {
	pod := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "back-abc12",
			"ownerReferences": []interface{}{
				map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "ReplicaSet",
					"name":       "back-7f9c4",
				},
			},
		},
	}
	ref, ok := kube.Owner(pod)
	if !ok {
		t.Fatal("owner should be found")
	}
	if ref.Group != "apps" || ref.Version != "v1" || ref.Kind != "ReplicaSet" || ref.Name != "back-7f9c4" {
		t.Fatalf("unexpected ref: %+v", ref)
	}

	// Top of the chain: no ownerReferences.
	if _, ok := kube.Owner(map[string]interface{}{"metadata": map[string]interface{}{"name": "x"}}); ok {
		t.Fatal("object without ownerReferences must report no owner")
	}
}

func endpoints(ns, name string, ready, notReady int) *unstructured.Unstructured {
	addr := func(n int) []interface{} {
		out := make([]interface{}, n)
		for i := range out {
			out[i] = map[string]interface{}{"ip": "10.0.0.1"}
		}
		return out
	}
	subset := map[string]interface{}{}
	if ready > 0 {
		subset["addresses"] = addr(ready)
	}
	if notReady > 0 {
		subset["notReadyAddresses"] = addr(notReady)
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Endpoints",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"subsets":    []interface{}{subset},
	}}
}

func TestEndpointsSummaryCountsBackends(t *testing.T) {
	client, _ := NewFakeClient("demo",
		endpoints("demo", "web", 3, 1),
	)
	ready, notReady, err := client.EndpointsSummary(context.Background(), "demo", "web")
	if err != nil {
		t.Fatal(err)
	}
	if ready != 3 || notReady != 1 {
		t.Fatalf("ready=%d notReady=%d, want 3/1", ready, notReady)
	}
}

func TestEndpointsSummaryBrokenService(t *testing.T) {
	client, _ := NewFakeClient("demo",
		endpoints("demo", "broken", 0, 2),
	)
	ready, notReady, err := client.EndpointsSummary(context.Background(), "demo", "broken")
	if err != nil {
		t.Fatal(err)
	}
	if ready != 0 || notReady != 2 {
		t.Fatalf("ready=%d notReady=%d, want 0/2 (broken link)", ready, notReady)
	}
}

func TestServiceStatusFromBackends(t *testing.T) {
	withSelector := map[string]interface{}{
		"spec": map[string]interface{}{"selector": map[string]interface{}{"app": "web"}},
	}
	noSelector := map[string]interface{}{"spec": map[string]interface{}{}}
	eps := map[string][2]int{
		"demo/web":    {3, 0},
		"demo/broken": {0, 2},
		// demo/dead absent → zero backends
	}

	if s := kube.ServiceStatus(withSelector, eps, "demo", "web"); s.Level != model.HealthOk || s.Reason != "3 eps" {
		t.Errorf("healthy service: %+v", s)
	}
	if s := kube.ServiceStatus(withSelector, eps, "demo", "broken"); s.Level != model.HealthWarning || s.Reason != "0/2 eps" {
		t.Errorf("not-ready service: %+v", s)
	}
	if s := kube.ServiceStatus(withSelector, eps, "demo", "dead"); s.Level != model.HealthError || s.Reason != "no eps" {
		t.Errorf("dead service: %+v", s)
	}
	if s := kube.ServiceStatus(noSelector, eps, "demo", "ext"); s.Level != model.HealthOk || s.Reason != "external" {
		t.Errorf("selector-less service: %+v", s)
	}
}

func TestEndpointsByServiceBatch(t *testing.T) {
	client, _ := NewFakeClient("",
		endpoints("demo", "web", 3, 0),
		endpoints("prod", "api", 0, 1),
	)
	eps, err := client.EndpointsByService(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if eps["demo/web"] != [2]int{3, 0} || eps["prod/api"] != [2]int{0, 1} {
		t.Fatalf("unexpected batch: %v", eps)
	}
}

func endpointSlice(ns, svc, name string, ready, notReady int) *unstructured.Unstructured {
	eps := []interface{}{}
	for i := 0; i < ready; i++ {
		eps = append(eps, map[string]interface{}{"conditions": map[string]interface{}{"ready": true}})
	}
	for i := 0; i < notReady; i++ {
		eps = append(eps, map[string]interface{}{"conditions": map[string]interface{}{"ready": false}})
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "discovery.k8s.io/v1",
		"kind":       "EndpointSlice",
		"metadata": map[string]interface{}{
			"name": name, "namespace": ns,
			"labels": map[string]interface{}{"kubernetes.io/service-name": svc},
		},
		"endpoints": eps,
	}}
}

func TestEndpointSlicesPreferredAndAggregated(t *testing.T) {
	client, _ := NewFakeClient("",
		// Two slices for the same service must aggregate.
		endpointSlice("demo", "web", "web-abc", 2, 1),
		endpointSlice("demo", "web", "web-def", 1, 0),
		// Legacy endpoints for the same service must be IGNORED when slices exist.
		endpoints("demo", "web", 9, 9),
	)
	eps, err := client.EndpointsByService(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if eps["demo/web"] != [2]int{3, 1} {
		t.Fatalf("slices should aggregate to 3 ready/1 notReady, got %v", eps["demo/web"])
	}

	ready, notReady, err := client.EndpointsSummary(context.Background(), "demo", "web")
	if err != nil {
		t.Fatal(err)
	}
	if ready != 3 || notReady != 1 {
		t.Fatalf("summary via slices: ready=%d notReady=%d, want 3/1", ready, notReady)
	}
}

func TestResolveMarkedPods(t *testing.T) {
	dep := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]interface{}{"name": "back", "namespace": "demo"},
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": "back"}},
		},
	}}
	pod1 := NewPod("demo", "back-abc", "Running")
	pod1.Object["metadata"].(map[string]interface{})["labels"] = map[string]interface{}{"app": "back"}
	other := NewPod("demo", "other", "Running")
	client, _ := NewFakeClient("demo", pod1, other)

	marked := []model.ResourceObject{
		{Type: model.ResourceType{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments"},
			Namespace: "demo", Name: "back", Raw: dep.Object},
		{Type: model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods"},
			Namespace: "prod", Name: "direct-pod"},
	}
	allowed, err := kube.ResolveMarkedPods(context.Background(), client, marked)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed["demo/back-abc"] {
		t.Error("workload selector should include its pod")
	}
	if !allowed["prod/direct-pod"] {
		t.Error("marked pod should be included directly")
	}
	if allowed["demo/other"] {
		t.Error("unrelated pod must not be included")
	}
}

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// buildBigCluster seeds 100 nodes and 5,000 pods (SC-005 scale target).
func buildBigCluster() []*unstructured.Unstructured {
	objs := make([]*unstructured.Unstructured, 0, 5100)
	for n := 0; n < 100; n++ {
		objs = append(objs, NewNode(fmt.Sprintf("node-%03d", n), true))
	}
	for i := 0; i < 5000; i++ {
		p := NewPodOnNode(fmt.Sprintf("ns-%02d", i%20), fmt.Sprintf("pod-%04d", i),
			fmt.Sprintf("node-%03d", i%100), "Running")
		objs = append(objs, p)
	}
	return objs
}

// TestScaleListAndTopology validates SC-005: listing and topology stay
// responsive at 5,000 pods across 100 nodes. Thresholds are generous to
// absorb CI variance — the point is catching quadratic blowups.
func TestScaleListAndTopology(t *testing.T) {
	client, _ := NewFakeClient("", buildBigCluster()...)

	start := time.Now()
	objs, err := client.List(context.Background(), PodsType, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 5000 {
		t.Fatalf("expected 5000 pods, got %d", len(objs))
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("List(5000 pods) took %v (>3s)", d)
	}

	start = time.Now()
	nodes, err := client.Topology(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 100 {
		t.Fatalf("expected 100 nodes, got %d", len(nodes))
	}
	total := 0
	for _, n := range nodes {
		total += len(n.Pods)
	}
	if total != 5000 {
		t.Fatalf("topology should place all 5000 pods, got %d", total)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Topology(5000/100) took %v (>3s)", d)
	}

	start = time.Now()
	if _, err := client.Diagnostics(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("Diagnostics(5000 pods) took %v (>3s)", d)
	}
}

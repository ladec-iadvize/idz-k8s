package integration

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/model"
)

func podWithStatus(ns, name string, status map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"status":     status,
	}}
}

func containerStatus(name string, restarts int64, waiting, lastTermReason string, exitCode int64) map[string]any {
	cs := map[string]any{"name": name, "restartCount": restarts}
	if waiting != "" {
		cs["state"] = map[string]any{"waiting": map[string]any{"reason": waiting}}
	}
	if lastTermReason != "" {
		cs["lastState"] = map[string]any{"terminated": map[string]any{"reason": lastTermReason, "exitCode": exitCode}}
	}
	return cs
}

func TestDiagnosticsDetectsFailures(t *testing.T) {
	client, _ := NewFakeClient("demo",
		// OOMKilled with restarts.
		podWithStatus("demo", "oom-pod", map[string]any{
			"containerStatuses": []any{containerStatus("app", 5, "", "OOMKilled", 137)},
		}),
		// CrashLoopBackOff (waiting).
		podWithStatus("demo", "crash-pod", map[string]any{
			"containerStatuses": []any{containerStatus("app", 3, "CrashLoopBackOff", "", 0)},
		}),
		// Healthy: no restarts, running.
		podWithStatus("demo", "healthy-pod", map[string]any{
			"containerStatuses": []any{containerStatus("app", 0, "", "", 0)},
		}),
		// Evicted pod.
		podWithStatus("demo", "evicted-pod", map[string]any{
			"reason":  "Evicted",
			"message": "The node was low on resource: memory",
		}),
	)

	rows, err := client.Diagnostics(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]model.Diagnostic{}
	for _, d := range rows {
		got[d.Pod] = d
	}

	if _, ok := got["healthy-pod"]; ok {
		t.Error("healthy pod must not produce a finding")
	}
	if d, ok := got["oom-pod"]; !ok || !strings.Contains(d.Reason, "OOMKilled") || d.Level != model.HealthError {
		t.Errorf("oom-pod finding wrong: %+v", d)
	}
	if d, ok := got["crash-pod"]; !ok || !strings.Contains(d.Reason, "CrashLoopBackOff") || d.Level != model.HealthError {
		t.Errorf("crash-pod finding wrong: %+v", d)
	}
	if d, ok := got["evicted-pod"]; !ok || !strings.Contains(d.Reason, "Evicted") || d.Level != model.HealthError {
		t.Errorf("evicted-pod finding wrong: %+v", d)
	}
}

func TestDiagnosticsDetectsUnschedulablePending(t *testing.T) {
	stuck := podWithStatus("demo", "pending-stuck", map[string]any{
		"phase": "Pending",
		"conditions": []any{map[string]any{
			"type": "PodScheduled", "status": "False",
			"reason":  "Unschedulable",
			"message": "0/3 nodes are available: 3 Insufficient cpu.",
		}},
	})
	// Scheduled but still starting (PodScheduled=True): NOT a finding.
	starting := podWithStatus("demo", "pending-starting", map[string]any{
		"phase": "Pending",
		"conditions": []any{map[string]any{
			"type": "PodScheduled", "status": "True",
		}},
	})
	client, _ := NewFakeClient("demo", stuck, starting)

	rows, err := client.Diagnostics(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]model.Diagnostic{}
	for _, d := range rows {
		got[d.Pod] = d
	}
	d, ok := got["pending-stuck"]
	if !ok || d.Level != model.HealthError {
		t.Fatalf("stuck pending pod must be flagged Error, got %+v", d)
	}
	if !strings.Contains(d.Reason, "Insufficient cpu") {
		t.Errorf("finding must carry the scheduler message, got %q", d.Reason)
	}
	if _, ok := got["pending-starting"]; ok {
		t.Error("a scheduled-but-starting pod must not be flagged")
	}
}

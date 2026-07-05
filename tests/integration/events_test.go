package integration

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/kube"
)

func event(ns, name, etype, reason, objName, ts string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion":     "v1",
		"kind":           "Event",
		"metadata":       map[string]any{"name": name, "namespace": ns},
		"type":           etype,
		"reason":         reason,
		"message":        reason + " for " + objName,
		"lastTimestamp":  ts,
		"involvedObject": map[string]any{"kind": "Pod", "name": objName},
	}}
}

func TestEventsSortedMostRecentFirst(t *testing.T) {
	// harness lists events via dynamic client; register the list kind.
	client, _ := newFakeClientWithEvents("demo",
		event("demo", "e1", "Normal", "Scheduled", "web-1", "2026-07-03T10:00:00Z"),
		event("demo", "e2", "Warning", "BackOff", "web-2", "2026-07-03T12:00:00Z"),
		event("demo", "e3", "Normal", "Pulled", "web-1", "2026-07-03T11:00:00Z"),
	)
	rows, err := client.Events(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 events, got %d", len(rows))
	}
	if rows[0].Reason != "BackOff" || !rows[0].Warning() {
		t.Errorf("most recent should be the 12:00 Warning BackOff, got %+v", rows[0])
	}
	if rows[2].Reason != "Scheduled" {
		t.Errorf("oldest should be Scheduled, got %+v", rows[2])
	}
}

// newFakeClientWithEvents builds a fake client that can list events.
func newFakeClientWithEvents(ns string, objs ...*unstructured.Unstructured) (*kube.Client, any) {
	return NewFakeClient(ns, objs...)
}

package unit

import (
	"testing"

	"github.com/iadvize/idz-k8s/internal/kube"
)

func liveObj(annotation string, replicas int64) map[string]interface{} {
	meta := map[string]interface{}{"name": "back", "namespace": "demo"}
	if annotation != "" {
		meta["annotations"] = map[string]interface{}{
			"kubectl.kubernetes.io/last-applied-configuration": annotation,
		}
	}
	return map[string]interface{}{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": meta,
		"spec": map[string]interface{}{
			"replicas": replicas,
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{"name": "app", "image": "nginx:1.27"},
					},
				},
			},
		},
		"status": map[string]interface{}{"readyReplicas": replicas},
	}
}

const applied = `{"apiVersion":"apps/v1","kind":"Deployment",` +
	`"metadata":{"name":"back","namespace":"demo","labels":{"team":"core"}},` +
	`"spec":{"replicas":3,"template":{"spec":{"containers":[{"name":"app","image":"nginx:1.26"}]}}}}`

// TestDriftDetectsChangesAndRemovals (FR-033): only baseline fields are
// compared — server-added fields (status…) are never drift.
func TestDriftDetectsChangesAndRemovals(t *testing.T) {
	drifts, ok := kube.Drift(liveObj(applied, 5))
	if !ok {
		t.Fatal("baseline present, ok must be true")
	}
	byPath := map[string]struct{ a, l string }{}
	for _, d := range drifts {
		byPath[d.Path] = struct{ a, l string }{d.Applied, d.Live}
	}
	if d, found := byPath["spec.replicas"]; !found || d.a != "3" || d.l != "5" {
		t.Fatalf("replicas drift missing/wrong: %+v", drifts)
	}
	if d, found := byPath["spec.template.spec.containers[0].image"]; !found || d.a != "nginx:1.26" || d.l != "nginx:1.27" {
		t.Fatalf("image drift missing/wrong: %+v", drifts)
	}
	if d, found := byPath["metadata.labels"]; !found || d.l != "<absent>" {
		t.Fatalf("removed labels must be flagged <absent>: %+v", drifts)
	}
	if len(drifts) != 3 {
		t.Fatalf("server-added fields must not be drift, got %+v", drifts)
	}
}

// TestDriftNoBaselineAndNoDrift: no annotation → ok=false (the UI states no
// baseline); identical values → zero drift (number types tolerated).
func TestDriftNoBaselineAndNoDrift(t *testing.T) {
	if _, ok := kube.Drift(liveObj("", 3)); ok {
		t.Fatal("no annotation must report no baseline")
	}
	same := `{"apiVersion":"apps/v1","kind":"Deployment",` +
		`"metadata":{"name":"back","namespace":"demo"},` +
		`"spec":{"replicas":3,"template":{"spec":{"containers":[{"name":"app","image":"nginx:1.27"}]}}}}`
	drifts, ok := kube.Drift(liveObj(same, 3))
	if !ok || len(drifts) != 0 {
		t.Fatalf("expected clean diff (JSON float vs live int64 must be equal), got %+v", drifts)
	}
}

// TestDriftUnparseableBaseline: a corrupt annotation is reported, never
// silently ignored.
func TestDriftUnparseableBaseline(t *testing.T) {
	drifts, ok := kube.Drift(liveObj("{not json", 3))
	if !ok || len(drifts) != 1 || drifts[0].Path != "(annotation)" {
		t.Fatalf("corrupt baseline must surface: %+v ok=%v", drifts, ok)
	}
}

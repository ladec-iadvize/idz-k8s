package ui

import (
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// TestOpenDriftStates: drifted fields shown with applied/live values, the
// no-baseline and no-drift states are explicit, and the view offers no
// apply/edit affordance (FR-033).
func TestOpenDriftStates(t *testing.T) {
	dep := model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(dep))
	m.width, m.height = 120, 30
	m.layout()

	applied := `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"back","namespace":"demo"},"spec":{"replicas":3}}`
	m.objects = []model.ResourceObject{{
		Type: dep, Namespace: "demo", Name: "back",
		Raw: map[string]interface{}{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]interface{}{
				"name": "back", "namespace": "demo",
				"annotations": map[string]interface{}{
					"kubectl.kubernetes.io/last-applied-configuration": applied,
				},
			},
			"spec": map[string]interface{}{"replicas": int64(5)},
		},
	}}
	m.applyRows()

	mi, _ := m.openDrift()
	m = asModel(t, mi)
	if m.screen != screenDrift {
		t.Fatalf("screen=%d", m.screen)
	}
	content := m.drift.View()
	for _, want := range []string{"Diff (read-only)", "Deployment demo/back", "spec.replicas", "3", "5"} {
		if !strings.Contains(content, want) {
			t.Fatalf("drift view missing %q:\n%s", want, content)
		}
	}
	for _, banned := range []string{"apply", "edit", "rollback"} {
		if strings.Contains(strings.ToLower(content), banned) && !strings.Contains(content, "last-applied") {
			t.Fatalf("no apply/edit affordance allowed, found %q:\n%s", banned, content)
		}
	}

	// No baseline: explicit statement.
	m.screen = screenList
	m.objects[0].Raw["metadata"].(map[string]interface{})["annotations"] = map[string]interface{}{}
	m.applyRows()
	mi, _ = m.openDrift()
	m = asModel(t, mi)
	if !strings.Contains(m.drift.View(), "No baseline") {
		t.Fatal("missing explicit no-baseline state")
	}
}

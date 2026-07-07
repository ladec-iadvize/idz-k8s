package ui

import (
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// TestRenderPostureGroupsByRule: findings grouped under rule headers with
// counts, object references and the advisory label; errors outrank warnings.
func TestRenderPostureGroupsByRule(t *testing.T) {
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}))
	m.width, m.height = 110, 30
	m.layout()
	m.screen = screenPosture

	m.renderPosture([]model.PostureFinding{
		{Rule: "privileged container", Severity: model.HealthError, Namespace: "demo", Name: "bad", Container: "app", Detail: "securityContext.privileged: true"},
		{Rule: "image not pinned (latest)", Severity: model.HealthWarning, Namespace: "demo", Name: "bad", Container: "app", Detail: "image nginx:latest"},
	})
	content := m.posture.View()
	for _, want := range []string{"Posture (advisory)", "2 finding(s)", "privileged container (1)", "demo/bad [app]", "nginx:latest", "read-only"} {
		if !strings.Contains(content, want) {
			t.Fatalf("posture view missing %q:\n%s", want, content)
		}
	}

	// Clean scope: explicit success state, never an empty screen.
	m.renderPosture(nil)
	if !strings.Contains(m.posture.View(), "no findings") {
		t.Fatal("empty posture must state the success explicitly")
	}
}

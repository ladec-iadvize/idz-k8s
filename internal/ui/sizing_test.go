package ui

import (
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func sizingModel(t *testing.T) Model {
	t.Helper()
	dep := model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m := New(&kube.Client{}, config.Defaults(), "", WithInitialType(dep))
	m.width, m.height = 100, 30
	m.layout()
	m.objects = []model.ResourceObject{{
		Type: dep, Namespace: "demo", Name: "back",
		Raw: map[string]interface{}{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]interface{}{"name": "back", "namespace": "demo"},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": "back"}},
			},
		},
	}}
	m.applyRows()
	return m
}

// TestSizingWithoutMetricsStatesNoRecommendation (SC-013): no Prometheus →
// the view says so explicitly and shows no figure.
func TestSizingWithoutMetricsStatesNoRecommendation(t *testing.T) {
	m := sizingModel(t)
	mi, cmd := m.openSizing()
	m = asModel(t, mi)
	if cmd != nil {
		t.Fatal("no metrics source: nothing should be fetched")
	}
	if m.screen != screenSizing {
		t.Fatalf("screen=%d want sizing", m.screen)
	}
	content := m.sizingVP.View()
	if !strings.Contains(content, "No recommendation") {
		t.Fatalf("missing explicit no-recommendation state:\n%s", content)
	}
}

// TestRenderSizingShowsDataBehindVerdicts (FR-023): every verdict comes with
// the observed numbers; a no-data resource states it without figures.
func TestRenderSizingShowsDataBehindVerdicts(t *testing.T) {
	m := sizingModel(t)
	m.screen = screenSizing
	adv := model.SizingAdvice{
		Workload: "Deployment/back", Namespace: "demo", Pods: 3,
		CPU: model.EvaluateSizing(model.ResourceSizing{
			Kind: model.MetricCPU, HasData: true, Avg: 0.1, Peak: 0.2, Request: 1, Limit: 2,
		}),
		Memory: model.EvaluateSizing(model.ResourceSizing{Kind: model.MetricMemory}),
	}
	m.renderSizing(adv)
	content := m.sizingVP.View()
	for _, want := range []string{"Advisory", "observed", "request", "oversized", "no data for this window", "no recommendation"} {
		if !strings.Contains(content, want) {
			t.Fatalf("sizing view missing %q:\n%s", want, content)
		}
	}
}

// TestSizingOnKindWithoutPods: a ConfigMap has no pods — the view refuses
// with a status hint instead of opening empty.
func TestSizingOnKindWithoutPods(t *testing.T) {
	m := sizingModel(t)
	m.curType = model.ResourceType{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
	m.objects = []model.ResourceObject{{Namespace: "demo", Name: "cm", Raw: map[string]interface{}{}}}
	m.applyRows()
	mi, _ := m.openSizing()
	m = asModel(t, mi)
	if m.screen == screenSizing {
		t.Fatal("sizing must not open on a kind without pods")
	}
	if m.statusMsg == "" {
		t.Fatal("expected a status hint")
	}
}

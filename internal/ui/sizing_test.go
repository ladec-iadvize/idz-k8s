package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/table"

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

// TestSizingOverviewRenderAndDrill: the overview table renders verdicts and
// gauges per workload; Enter opens the detail panel and Esc returns.
func TestSizingOverviewRenderAndDrill(t *testing.T) {
	m := sizingModel(t)
	m.screen = screenSizingList
	m.sizingRows = []model.SizingAdvice{
		{
			Workload: "Deployment/back", Namespace: "demo", Pods: 3,
			CPU:    model.EvaluateSizing(model.ResourceSizing{Kind: model.MetricCPU, HasData: true, Avg: 1.2, Peak: 1.5, Request: 1, Limit: 4}),
			Memory: model.EvaluateSizing(model.ResourceSizing{Kind: model.MetricMemory, HasData: true, Avg: 4e8, Peak: 6e8, Request: 1e9}),
		},
		{
			Workload: "Deployment/front", Namespace: "demo", Pods: 2,
			CPU:    model.EvaluateSizing(model.ResourceSizing{Kind: model.MetricCPU}),
			Memory: model.EvaluateSizing(model.ResourceSizing{Kind: model.MetricMemory, HasData: true, Avg: 1e8, Peak: 2e8, Request: 1e9}),
		},
	}
	m.sizingObjs = m.objects[:1]
	m.sizingWin.SetRows(make([]table.Row, len(m.sizingRows)))
	m.layout()

	view := m.sizingListView()
	for _, want := range []string{"WORKLOAD", "back", "front", "under", "ok", "no data", "over"} {
		if !strings.Contains(view, want) {
			t.Fatalf("overview missing %q:\n%s", want, view)
		}
	}

	// Enter on the first row → detail; Esc → back to the overview.
	mi, cmd := m.openSizingDetail(0)
	m = asModel(t, mi)
	if m.screen != screenSizing || cmd == nil {
		t.Fatalf("detail did not open (screen=%d cmd=%v)", m.screen, cmd)
	}
	mi, _ = m.goBack()
	m = asModel(t, mi)
	if m.screen != screenSizingList {
		t.Fatalf("Esc from detail should return to the overview, screen=%d", m.screen)
	}
}

// TestAdviceSeverityOrdering: worst verdicts rank first (under > over >
// no-request > ok > no-data).
func TestAdviceSeverityOrdering(t *testing.T) {
	mk := func(v model.SizingVerdict) model.SizingAdvice {
		return model.SizingAdvice{CPU: model.ResourceSizing{Verdict: v}}
	}
	order := []model.SizingVerdict{model.SizingUnder, model.SizingOver, model.SizingNoRequest, model.SizingOK, model.SizingNoData}
	for i := 0; i < len(order)-1; i++ {
		if adviceSeverity(mk(order[i])) <= adviceSeverity(mk(order[i+1])) {
			t.Fatalf("severity(%d) must outrank severity(%d)", order[i], order[i+1])
		}
	}
	// The row severity is the WORST of both resources.
	mixed := model.SizingAdvice{
		CPU:    model.ResourceSizing{Verdict: model.SizingOK},
		Memory: model.ResourceSizing{Verdict: model.SizingUnder},
	}
	if adviceSeverity(mixed) != adviceSeverity(mk(model.SizingUnder)) {
		t.Fatal("row severity must take the worst resource")
	}
}

// TestSizingOverviewColumnsAndSort: separate right-aligned AVG/REQ columns,
// titled STATUS columns, and working column sort (regression for the
// 2026-07-06 UX feedback).
func TestSizingOverviewColumnsAndSort(t *testing.T) {
	m := sizingModel(t)
	m.screen = screenSizingList
	m.sizingRows = []model.SizingAdvice{
		{Workload: "Deployment/zeta", Namespace: "demo", Pods: 1,
			CPU: model.EvaluateSizing(model.ResourceSizing{Kind: model.MetricCPU, HasData: true, Avg: 1.2, Peak: 1.5, Request: 1, Limit: 4})},
		{Workload: "Deployment/alpha", Namespace: "demo", Pods: 5,
			CPU: model.EvaluateSizing(model.ResourceSizing{Kind: model.MetricCPU, HasData: true, Avg: 0.1, Peak: 0.2, Request: 1, Limit: 2})},
	}
	m.sizingObjs = []model.ResourceObject{{Name: "zeta"}, {Name: "alpha"}}
	m.sizingWin.SetRows(make([]table.Row, 2))
	m.layout()

	view := m.sizingListView()
	header := strings.SplitN(view, "\n", 2)[0]
	for _, want := range []string{"WORKLOAD", "PODS", "CPU", "AVG", "REQ", "STATUS", "MEMORY"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header missing %q:\n%s", want, header)
		}
	}
	if strings.Count(header, "STATUS") != 2 || strings.Count(header, "AVG") != 2 {
		t.Fatalf("both resources need titled AVG and STATUS columns:\n%s", header)
	}

	// Sort by WORKLOAD (column 0) ascending: alpha before zeta.
	m.sizingSortCol, m.sizingSortAsc = 0, true
	m.applySizingSort()
	if m.sizingRows[0].Workload != "Deployment/alpha" || m.sizingObjs[0].Name != "alpha" {
		t.Fatalf("name sort broken (rows and objs must move together): %+v / %+v",
			m.sizingRows[0].Workload, m.sizingObjs[0].Name)
	}
	// Flip direction.
	m.sizingSortAsc = false
	m.applySizingSort()
	if m.sizingRows[0].Workload != "Deployment/zeta" {
		t.Fatalf("descending sort broken: %+v", m.sizingRows[0].Workload)
	}
	// -1 = severity default: the under-provisioned zeta row first.
	m.sizingSortCol = -1
	m.applySizingSort()
	if m.sizingRows[0].CPU.Verdict != model.SizingUnder {
		t.Fatalf("severity default should put the worst first, got %+v", m.sizingRows[0].CPU.Verdict)
	}
}

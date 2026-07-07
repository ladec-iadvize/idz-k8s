package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func plainModel(t *testing.T) Model {
	t.Helper()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}))
	m.width, m.height = 110, 30
	m.layout()
	return m
}

// TestDiagGroupedByFailureType (owner request 2026-07-07): findings grouped
// under one header per failure type, error groups first.
func TestDiagGroupedByFailureType(t *testing.T) {
	m := plainModel(t)
	m.screen = screenDiag
	m.renderDiag([]model.Diagnostic{
		{Namespace: "demo", Pod: "a", Container: "app", Reason: "restarted x4", Level: model.HealthWarning},
		{Namespace: "demo", Pod: "b", Container: "app", Reason: "OOMKilled (x3 restarts)", Level: model.HealthError},
		{Namespace: "demo", Pod: "c", Container: "app", Reason: "OOMKilled (x9 restarts)", Level: model.HealthError},
		{Namespace: "demo", Pod: "d", Reason: "Evicted: node memory pressure", Level: model.HealthError},
	})
	content := m.diag.View()
	for _, want := range []string{"OOMKilled (2)", "Evicted (1)", "restarted (1)", "4 finding(s)"} {
		if !strings.Contains(content, want) {
			t.Fatalf("diag view missing %q:\n%s", want, content)
		}
	}
	// Error groups come before warning groups.
	if strings.Index(content, "OOMKilled (2)") > strings.Index(content, "restarted (1)") {
		t.Fatal("error-level groups must render first")
	}
}

func TestDiagCategoryFolding(t *testing.T) {
	cases := map[string]string{
		"OOMKilled (x3 restarts)":       "OOMKilled",
		"Evicted: node memory pressure": "Evicted",
		"restarted x4":                  "restarted",
		"CrashLoopBackOff":              "CrashLoopBackOff",
		"Error (exit 1, x2)":            "Error",
	}
	for in, want := range cases {
		if got := diagCategory(in); got != want {
			t.Errorf("diagCategory(%q)=%q want %q", in, got, want)
		}
	}
}

// TestSearchWorksOnEveryContentView: the same '/' flow works on a non-detail
// view (posture here) — commit, hits, Esc clears then Esc leaves.
func TestSearchWorksOnEveryContentView(t *testing.T) {
	m := plainModel(t)
	m.screen = screenPosture
	m.renderPosture([]model.PostureFinding{
		{Rule: "privileged container", Severity: model.HealthError, Namespace: "demo", Name: "bad", Container: "app", Detail: "securityContext.privileged: true"},
	})
	mi, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	if !m.searchTyping {
		t.Fatal("'/' must open the search on the posture view")
	}
	for _, r := range "privileged" {
		mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if len(m.searchHits) == 0 || m.searchScreen != screenPosture {
		t.Fatalf("hits=%v screen=%d", m.searchHits, m.searchScreen)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.searchQuery != "" || m.screen != screenPosture {
		t.Fatal("first Esc must clear and stay")
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.screen != screenList {
		t.Fatal("second Esc must leave")
	}
}

// TestLogsSearchSurvivesStreaming: new log lines keep the highlight and
// recompute the hits.
func TestLogsSearchSurvivesStreaming(t *testing.T) {
	m := plainModel(t)
	m.screen = screenLogs
	m.logBuf = []string{"boot ok", "error: timeout"}
	m.setContent(screenLogs, strings.Join(m.logBuf, "\n"))
	m.searchQuery, m.searchScreen = "error", screenLogs
	m.applySearch(true)
	if len(m.searchHits) != 1 {
		t.Fatalf("hits=%v", m.searchHits)
	}
	m.logBuf = append(m.logBuf, "error: again")
	m.setContent(screenLogs, strings.Join(m.logBuf, "\n"))
	if len(m.searchHits) != 2 {
		t.Fatalf("streaming must recompute hits, got %v", m.searchHits)
	}
}

// TestSizingOverviewFilter: '/' on the sizing table narrows the rows and
// keeps rows/objs paired.
func TestSizingOverviewFilter(t *testing.T) {
	m := plainModel(t)
	m.curType = model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m.screen = screenSizingList
	mi, _ := m.Update(sizingListMsg{
		rows: []model.SizingAdvice{
			{Workload: "Deployment/back", Namespace: "demo"},
			{Workload: "Deployment/front", Namespace: "demo"},
		},
		objs: []model.ResourceObject{{Name: "back"}, {Name: "front"}},
	})
	m = asModel(t, mi)
	if len(m.sizingRows) != 2 {
		t.Fatalf("master rows=%d", len(m.sizingRows))
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	if !m.sizingFiltering {
		t.Fatal("'/' must open the sizing filter")
	}
	for _, r := range "front" {
		mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if len(m.sizingRows) != 1 || m.sizingObjs[0].Name != "front" {
		t.Fatalf("filter broken: rows=%+v objs=%+v", m.sizingRows, m.sizingObjs)
	}
	// Esc while typing clears back to the full set.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if len(m.sizingRows) != 2 {
		t.Fatalf("clear broken: rows=%d", len(m.sizingRows))
	}
}

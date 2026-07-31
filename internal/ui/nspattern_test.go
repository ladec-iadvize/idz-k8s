package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// TestNamespacePickerGlobBecomesSelectablePattern: typing "staging-*" in the
// namespace picker offers a pattern option; selecting it scopes the client.
func TestNamespacePickerGlobBecomesSelectablePattern(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 100, 24
	m.layout()

	// Open the picker with a fixed option list (no live cluster in tests).
	m.pickerKind = pickNamespace
	m.pickerReturn = screenList
	m.pickerOpts = []string{allNamespacesLabel, "staging-front", "prod"}
	m.pickerQuery = ""
	m.applyPickerRows()
	m.screen = screenPicker

	for _, r := range "staging-*" {
		mi, _ := m.handlePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	row, ok := m.pickerWin.Selected()
	if !ok || row[0] != nsPatternPrefix+"staging-*" {
		t.Fatalf("first row should be the pattern option, got %v", row)
	}

	mi, _ := m.pickerSelect()
	m = asModel(t, mi)
	if m.client.Namespace != "staging-*" {
		t.Fatalf("client namespace=%q want staging-*", m.client.Namespace)
	}
	if m.screen != screenList {
		t.Fatalf("expected to land on the list, screen=%d", m.screen)
	}
}

// TestNamespaceMultiSelectByMark (owner request 2026-07-31): Space marks
// namespaces in the 'n' picker; Enter scopes to all of them (comma list,
// matched client-side like the glob patterns).
func TestNamespaceMultiSelectByMark(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	m := New(&kube.Client{Namespace: ""}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 120, 30
	m.layout()
	m.objects = []model.ResourceObject{
		{Type: pods, Namespace: "audience-back", Name: "p1"},
		{Type: pods, Namespace: "conversation-back", Name: "p2"},
	}
	mi, _ := m.openPicker(pickNamespace)
	m = asModel(t, mi)

	mark := func(name string) {
		t.Helper()
		for i, row := range m.pickerWin.rows {
			if row[0] == name {
				m.pickerWin.cursor = i
				mi, _ := m.handlePickerKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
				m = asModel(t, mi)
				return
			}
		}
		t.Fatalf("namespace %q not in picker", name)
	}
	mark("audience-back")
	mark("conversation-back")
	if len(m.pickerMarked) != 2 {
		t.Fatalf("expected 2 marked namespaces, got %v", m.pickerMarked)
	}
	// Space on the all-namespaces sentinel is a no-op.
	m.pickerWin.Home()
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = asModel(t, mi)
	if len(m.pickerMarked) != 2 {
		t.Fatalf("the sentinel must not be markable, got %v", m.pickerMarked)
	}
	// Marking again unmarks.
	mark("audience-back")
	if m.pickerMarked["audience-back"] {
		t.Fatal("Space must toggle the mark off")
	}
	mark("audience-back") // re-mark for the selection below

	mi, _ = m.pickerSelect()
	m = asModel(t, mi)
	if got := m.client.Namespace; got != "audience-back,conversation-back" {
		t.Fatalf("scope must join the marked namespaces, got %q", got)
	}
	if m.screen != screenList {
		t.Fatal("selection must land back on the list")
	}
	// The comma scope is a pattern: only the two marked namespaces match.
	if !kube.MatchNamespace(m.client.Namespace, "audience-back") ||
		!kube.MatchNamespace(m.client.Namespace, "conversation-back") ||
		kube.MatchNamespace(m.client.Namespace, "other") {
		t.Fatal("comma scope must match exactly the marked namespaces")
	}
}

// TestResetViewGoesHome (owner report 2026-07-31: "reset view" left the
// filters in place): 'R' returns to the deployments list across all
// namespaces with no filter anywhere — saved filters of other types
// included (they kept resurfacing on type switches).
func TestResetViewGoesHome(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	deps := model.ResourceType{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true}
	m := New(&kube.Client{Namespace: "audience-back"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 120, 30
	m.layout()
	svc := model.ResourceType{Version: "v1", Kind: "Service", Resource: "services", Namespaced: true}
	m.types = []model.ResourceType{pods, deps, svc}
	m.filter.SetValue("back")
	m.cfg.ViewPrefs = map[string]config.ViewPref{
		pods.Key(): {Filter: "sticky-pods-filter"}, // current type: whole pref drops
		svc.Key():  {Filter: "sticky-svc-filter", Columns: []string{"NAME"}},
	}

	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'R'}})
	m = asModel(t, mi)
	if cmd == nil {
		t.Fatal("reset must reload the list")
	}
	if m.curType.Key() != deps.Key() {
		t.Fatalf("reset must land on deployments, got %q", m.curType.Key())
	}
	if m.client.Namespace != "" {
		t.Fatalf("reset must scope to all namespaces, got %q", m.client.Namespace)
	}
	if m.filter.Value() != "" {
		t.Fatalf("reset must clear the active filter, got %q", m.filter.Value())
	}
	if f := m.cfg.ViewPrefs[pods.Key()].Filter; f != "" {
		t.Fatalf("the current type's pref must be dropped, got filter %q", f)
	}
	if f := m.cfg.ViewPrefs[svc.Key()].Filter; f != "" {
		t.Fatalf("reset must clear every saved filter, got %q", f)
	}
	if cols := m.cfg.ViewPrefs[svc.Key()].Columns; len(cols) != 1 {
		t.Fatalf("other types' column arrangements must survive a reset, got %v", cols)
	}
}

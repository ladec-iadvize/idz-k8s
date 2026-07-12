package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func nsPickerModel(t *testing.T) Model {
	t.Helper()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}))
	m.width, m.height = 100, 30
	m.layout()
	m.pickerKind = pickNamespace
	m.pickerReturn = screenList
	m.pickerOpts = []string{allNamespacesLabel, "audience-engagement", "audience-system-rabbitmq", "audience-back", "prod"}
	m.pickerQuery = ""
	m.applyPickerRows()
	m.screen = screenPicker
	return m
}

// Owner bug 2026-07-12 (1/2): arrows must move the picker cursor through the
// full Update path.
func TestNamespacePickerArrowsMoveCursor(t *testing.T) {
	m := nsPickerModel(t)
	for i := 0; i < 2; i++ {
		mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = asModel(t, mi)
	}
	if m.pickerWin.cursor != 2 {
		t.Fatalf("two KeyDown must land on row 2, cursor=%d", m.pickerWin.cursor)
	}
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = asModel(t, mi)
	if m.pickerWin.cursor != 1 {
		t.Fatalf("KeyUp must go back to row 1, cursor=%d", m.pickerWin.cursor)
	}
}

// Owner bug 2026-07-12 (2/2): a glob query must list every namespace the
// pattern matches (not filter them out by literal substring).
func TestNamespacePickerGlobShowsMatches(t *testing.T) {
	m := nsPickerModel(t)
	for _, r := range "audience-*" {
		mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	rows := m.pickerWin.rows
	if len(rows) != 4 { // pattern row + the 3 audience-* namespaces
		got := []string{}
		for _, r := range rows {
			got = append(got, r[0])
		}
		t.Fatalf("glob must show the pattern + its 3 matches, got %v", got)
	}

	// And the arrows now have somewhere to go: down onto a real namespace,
	// Enter selects it as an exact scope.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(t, mi)
	if m.pickerWin.cursor != 1 {
		t.Fatalf("arrow after glob stuck at %d", m.pickerWin.cursor)
	}
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if kube.IsNamespacePattern(m.client.Namespace) || m.client.Namespace == "" {
		t.Fatalf("selecting a previewed namespace must scope to it exactly, got %q", m.client.Namespace)
	}
}

package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
)

func podRaw(name, ip, app string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]interface{}{
			"name": name, "namespace": "demo",
			"labels": map[string]interface{}{"app": app},
		},
		"status": map[string]interface{}{"podIP": ip},
	}
}

// TestCustomColumnCells: "label:" and "field:" specs resolve against the
// object; absent values render "-", non-scalars "…".
func TestCustomColumnCells(t *testing.T) {
	m := newViewsModel(t)
	m.objects[0].Raw = podRaw("api", "10.0.0.7", "back")
	o := m.objects[0]

	if got := customColumn("label:app").cell(&m, o); got != "back" {
		t.Errorf("label cell=%q want back", got)
	}
	if got := customColumn("field:.status.podIP").cell(&m, o); got != "10.0.0.7" {
		t.Errorf("field cell=%q want 10.0.0.7", got)
	}
	if got := customColumn("label:team").cell(&m, o); got != "-" {
		t.Errorf("absent label=%q want -", got)
	}
	if got := customColumn("field:.spec.containers").cell(&m, o); got != "-" {
		t.Errorf("missing path=%q want -", got)
	}
	if got := customColumn("field:.metadata.labels").cell(&m, o); got != "…" {
		t.Errorf("non-scalar=%q want …", got)
	}
}

// TestCustomColumnPrefResolution: prefixed specs become live columns; a stale
// plain title is still dropped (FR-025 tolerance preserved).
func TestCustomColumnPrefResolution(t *testing.T) {
	m := newViewsModel(t)
	m.cfg.ViewPrefs = map[string]config.ViewPref{
		"v1/pods": {Columns: []string{"NAME", "label:app", "field:.status.podIP", "BOGUS"}},
	}
	got := colTitles(m.columnsForType())
	want := []string{"NAME", "app", ".status.podIP"}
	if len(got) != len(want) {
		t.Fatalf("columns=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("columns=%v want %v", got, want)
		}
	}
}

// TestChooserAddCustomFieldFlow: the "add custom field…" action row prompts
// for a spec, adds the column, and Enter persists it in the prefs.
func TestChooserAddCustomFieldFlow(t *testing.T) {
	m := newViewsModel(t)
	m.objects[0].Raw = podRaw("api", "10.0.0.7", "back")
	mi, _ := m.openColumnChooser()
	m = asModel(t, mi)
	if m.colItems[len(m.colItems)-1].title != addFieldLabel {
		t.Fatalf("last chooser row should be the add-field action, got %+v", m.colItems[len(m.colItems)-1])
	}

	// Enter on the action row opens the prompt (not the apply).
	m.pickerWin.End()
	mi, _ = m.pickerSelect()
	m = asModel(t, mi)
	if !m.fieldNaming {
		t.Fatal("Enter on the action row must open the field prompt")
	}

	// Typing must be captured (no 'q' quit), Enter adds the column.
	for _, r := range ".status.podIP" {
		mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	mi, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if cmd != nil {
		t.Fatal("adding a column must not quit or fetch")
	}
	found := false
	for _, it := range m.colItems {
		if it.title == "field:.status.podIP" && it.on {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom column not added: %+v", m.colItems)
	}

	// Apply: the spec is persisted and the live column renders the value.
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	specs := m.cfg.ViewPrefs["v1/pods"].Columns
	has := false
	for _, s := range specs {
		if s == "field:.status.podIP" {
			has = true
		}
	}
	if !has {
		t.Fatalf("spec not persisted: %v", specs)
	}
	// Wide enough terminal for the extra column (lines are hard-truncated
	// to the width — geometry invariant).
	m.width = 220
	m.layout()
	m.applyRows()
	view := m.listView()
	if !strings.Contains(view, "10.0.0.7") || !strings.Contains(view, ".status.podIP") {
		t.Fatalf("list must show the custom column and its value:\n%s", view)
	}
}

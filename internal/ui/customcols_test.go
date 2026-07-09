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
	// Custom columns render with built-in-looking headers (owner feedback).
	want := []string{"NAME", "APP", "POD IP"}
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
	if !strings.Contains(view, "10.0.0.7") || !strings.Contains(view, "POD IP") {
		t.Fatalf("list must show the custom column and its value:\n%s", view)
	}
}

// TestChooserRemovesCustomField (owner request 2026-07-07: erase a typo'd
// field): Backspace deletes a custom entry, never a built-in one.
func TestChooserRemovesCustomField(t *testing.T) {
	m := newViewsModel(t)
	m.cfg.ViewPrefs = map[string]config.ViewPref{
		"v1/pods": {Columns: []string{"NAME", "field:.status.podIp_typo"}},
	}
	mi, _ := m.openColumnChooser()
	m = asModel(t, mi)
	// Find the custom row.
	idx := -1
	for i, it := range m.colItems {
		if it.title == "field:.status.podIp_typo" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("custom entry missing from the chooser: %+v", m.colItems)
	}
	m.pickerWin.cursor = idx
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = asModel(t, mi)
	for _, it := range m.colItems {
		if it.title == "field:.status.podIp_typo" {
			t.Fatal("custom field not removed")
		}
	}
	// Built-in columns refuse deletion.
	m.pickerWin.cursor = 0 // NAME
	before := len(m.colItems)
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = asModel(t, mi)
	if len(m.colItems) != before {
		t.Fatal("built-in column must not be removable")
	}
	// Apply: the pref no longer contains the removed field.
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	for _, c := range m.cfg.ViewPrefs["v1/pods"].Columns {
		if c == "field:.status.podIp_typo" {
			t.Fatal("removed field persisted")
		}
	}
}

func TestCustomTitleMapping(t *testing.T) {
	cases := map[string]string{
		"label:app":                       "APP",
		"label:karpenter.sh/nodepool":     "NODEPOOL", // meaningful tail only
		"label:app.kubernetes.io/version": "VERSION",
		"field:.status.podIP":             "POD IP",
		"field:.spec.nodeName":            "NODE NAME",
		"field:.spec.storageClassName":    "STORAGE CLASS NAME",
		"field:.spec.schedule":            "SCHEDULE",
	}
	for in, want := range cases {
		if got := customTitle(in); got != want {
			t.Errorf("customTitle(%q)=%q want %q", in, got, want)
		}
	}
}

// TestDottedLabelKeyColumns (owner bug 2026-07-09): app version behind
// metadata.labels."app.kubernetes.io/version" must be addable — whatever
// form the user types.
func TestDottedLabelKeyColumns(t *testing.T) {
	m := newViewsModel(t)
	raw := podRaw("api", "10.0.0.7", "back")
	raw["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["app.kubernetes.io/version"] = "2.25.2"
	m.objects[0].Raw = raw
	o := m.objects[0]

	// Direct label spec with a dotted key.
	if got := customColumn("label:app.kubernetes.io/version").cell(&m, o); got != "2.25.2" {
		t.Errorf("label cell=%q want 2.25.2", got)
	}
	// Field path through metadata.labels: greedy dotted-key matching.
	if got := customColumn("field:.metadata.labels.app.kubernetes.io/version").cell(&m, o); got != "2.25.2" {
		t.Errorf("field cell=%q want 2.25.2", got)
	}

	// Chooser input normalization: what people naturally type.
	for _, input := range []string{
		"app.kubernetes.io/version",
		"metadata.labels.app.kubernetes.io/version",
		".metadata.labels.app.kubernetes.io/version",
	} {
		mi, _ := m.openColumnChooser()
		m2 := asModel(t, mi)
		m2.addCustomColumn(input)
		found := ""
		for _, it := range m2.colItems {
			if it.on && isCustomSpec(it.title) {
				found = it.title
			}
		}
		if found != "label:app.kubernetes.io/version" {
			t.Errorf("input %q normalized to %q, want label:app.kubernetes.io/version", input, found)
		}
	}
}

// TestLegacyBrokenSpecsHeal (owner follow-up 2026-07-09): a spec persisted
// as "label:metadata.labels.<key>" by an older build must render the label
// value anyway — configs are never left broken.
func TestLegacyBrokenSpecsHeal(t *testing.T) {
	m := newViewsModel(t)
	raw := podRaw("api", "10.0.0.7", "back")
	raw["metadata"].(map[string]interface{})["labels"].(map[string]interface{})["app.kubernetes.io/version"] = "2.25.2"
	o := m.objects[0]
	o.Raw = raw

	for _, legacy := range []string{
		"label:metadata.labels.app.kubernetes.io/version",
		"label:.metadata.labels.app.kubernetes.io/version",
	} {
		col := customColumn(legacy)
		if got := col.cell(&m, o); got != "2.25.2" {
			t.Errorf("legacy spec %q renders %q, want 2.25.2", legacy, got)
		}
		if col.title != "VERSION" {
			t.Errorf("legacy spec %q title %q, want VERSION", legacy, col.title)
		}
	}
}

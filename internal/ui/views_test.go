package ui

import (
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

var podType = model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}

func newViewsModel(t *testing.T) Model {
	t.Helper()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithConfigPath(filepath.Join(t.TempDir(), "config.yaml")),
		WithInitialType(podType),
	)
	m.types = []model.ResourceType{
		podType,
		{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
	}
	m.width, m.height = 120, 24
	m.layout()
	m.objects = []model.ResourceObject{
		{Name: "api", Namespace: "demo"},
		{Name: "worker", Namespace: "demo"},
	}
	m.applyRows()
	return m
}

func colTitles(cols []listColumn) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.title
	}
	return out
}

func TestColumnPrefAppliedOrderAndSubset(t *testing.T) {
	m := newViewsModel(t)
	m.cfg.ViewPrefs = map[string]config.ViewPref{
		"v1/pods": {Columns: []string{"NAME", "NODE", "BOGUS", "AGE"}},
	}
	got := colTitles(m.columnsForType())
	want := []string{"NAME", "NODE", "AGE"} // saved order first, BOGUS dropped
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("columns=%v want prefix %v", got, want)
		}
	}
	for _, c := range got {
		if c == "BOGUS" {
			t.Fatalf("stale title must be dropped: %v", got)
		}
	}

	// A pref matching nothing falls back to the type defaults (FR-025).
	m.cfg.ViewPrefs["v1/pods"] = config.ViewPref{Columns: []string{"NOPE"}}
	if got := colTitles(m.columnsForType()); len(got) != len(colTitles(m.columnsBase())) {
		t.Fatalf("all-unknown pref must fall back to defaults, got %v", got)
	}
}

func TestSelectionSurvivesCustomColumns(t *testing.T) {
	m := newViewsModel(t)
	// Hide NAMESPACE entirely: index-based selection must still resolve.
	m.cfg.ViewPrefs = map[string]config.ViewPref{"v1/pods": {Columns: []string{"NAME", "AGE"}}}
	m.applyRows()
	m.win.Move(1)
	obj, ok := m.selectedObject()
	if !ok || obj.Name != "worker" {
		t.Fatalf("selectedObject=%+v ok=%v, want worker", obj, ok)
	}
}

func TestColumnChooserToggleReorderApply(t *testing.T) {
	m := newViewsModel(t)
	mi, _ := m.openColumnChooser()
	m = asModel(t, mi)
	if m.screen != screenPicker || m.pickerKind != pickColumns {
		t.Fatalf("chooser did not open (screen=%d kind=%d)", m.screen, m.pickerKind)
	}
	// Base pod columns: NAMESPACE NAME READY RESTARTS NODE STATUS AGE.
	if m.colItems[0].title != "NAMESPACE" || !m.colItems[0].on {
		t.Fatalf("unexpected first item %+v", m.colItems[0])
	}

	// NAME cannot be hidden.
	m.pickerWin.cursor = 1
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeySpace})
	m = asModel(t, mi)
	if !m.colItems[1].on {
		t.Fatal("NAME was hidden — it must stay")
	}

	// Hide NAMESPACE, move NAME left (to the front).
	m.pickerWin.cursor = 0
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeySpace})
	m = asModel(t, mi)
	if m.colItems[0].on {
		t.Fatal("NAMESPACE still on after toggle")
	}
	m.pickerWin.cursor = 1 // NAME
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyLeft})
	m = asModel(t, mi)
	if m.colItems[0].title != "NAME" {
		t.Fatalf("reorder failed, first item=%q", m.colItems[0].title)
	}

	// Enter applies: pref stored, list shows the new arrangement.
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.screen != screenList {
		t.Fatalf("chooser did not close, screen=%d", m.screen)
	}
	got := colTitles(m.columnsForType())
	if got[0] != "NAME" {
		t.Fatalf("applied columns=%v, want NAME first", got)
	}
	for _, c := range got {
		if c == "NAMESPACE" {
			t.Fatal("NAMESPACE still displayed after hiding it")
		}
	}
	if len(m.cfg.ViewPrefs["v1/pods"].Columns) == 0 {
		t.Fatal("column arrangement was not persisted in ViewPrefs")
	}

	// Persisted on disk too.
	saved, err := config.Load(m.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.ViewPrefs["v1/pods"].Columns) == 0 {
		t.Fatal("column arrangement missing from the saved config")
	}
}

func TestChooserDefaultArrangementClearsPref(t *testing.T) {
	m := newViewsModel(t)
	mi, _ := m.openColumnChooser()
	m = asModel(t, mi)
	// Apply without touching anything: no pref should be stored.
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if pref, ok := m.cfg.ViewPrefs["v1/pods"]; ok {
		t.Fatalf("default arrangement must not create a pref entry, got %+v", pref)
	}
}

func TestSortAndFilterPersistedPerTypeAndRestored(t *testing.T) {
	m := newViewsModel(t)
	// Sort by NAME (column 2 of the pod defaults) descending, filter "api".
	m.sortCol, m.sortAsc = 2, false
	m.filter.SetValue("api")
	m.persistViewPref()

	pref := m.cfg.ViewPrefs["v1/pods"]
	if pref.SortCol != "NAME" || pref.SortAsc || pref.Filter != "api" {
		t.Fatalf("persisted pref=%+v", pref)
	}

	// Switching type resets, switching back restores.
	m.curType = m.types[1] // deployments
	m.applyViewPref()
	if m.sortCol != -1 || m.filter.Value() != "" {
		t.Fatalf("deployments must start clean, sortCol=%d filter=%q", m.sortCol, m.filter.Value())
	}
	m.curType = podType
	m.applyViewPref()
	if m.sortCol != 2 || m.sortAsc || m.filter.Value() != "api" {
		t.Fatalf("pod pref not restored: sortCol=%d asc=%v filter=%q", m.sortCol, m.sortAsc, m.filter.Value())
	}
}

func TestPersistViewPrefDropsAllDefaultEntry(t *testing.T) {
	m := newViewsModel(t)
	m.cfg.ViewPrefs = map[string]config.ViewPref{"v1/pods": {Filter: "old"}}
	m.sortCol = -1
	m.filter.SetValue("")
	m.persistViewPref()
	if _, ok := m.cfg.ViewPrefs["v1/pods"]; ok {
		t.Fatal("an all-default pref must be removed, not stored")
	}
}

func TestSaveOpenAndResetNamedView(t *testing.T) {
	m := newViewsModel(t)
	m.cfg.ViewPrefs = map[string]config.ViewPref{"v1/pods": {Columns: []string{"NAME", "NODE"}}}
	m.sortCol, m.sortAsc = 1, true
	m.filter.SetValue("api")
	m.saveCurrentView("crashwatch")
	if len(m.cfg.SavedViews) != 1 {
		t.Fatalf("SavedViews=%v", m.cfg.SavedViews)
	}
	v := m.cfg.SavedViews[0]
	if v.Type != "v1/pods" || v.Namespace != "demo" || v.Filter != "api" || v.SortCol != "NAME" {
		t.Fatalf("saved view=%+v", v)
	}

	// Same name updates in place.
	m.filter.SetValue("worker")
	m.saveCurrentView("crashwatch")
	if len(m.cfg.SavedViews) != 1 || m.cfg.SavedViews[0].Filter != "worker" {
		t.Fatalf("update in place failed: %+v", m.cfg.SavedViews)
	}

	// Drift away, then re-open the view from the picker.
	m.curType = m.types[1]
	m.client.Namespace = "other"
	m.filter.SetValue("")
	mi, _ := m.openViewPicker()
	m = asModel(t, mi)
	if m.pickerKind != pickView || len(m.pickerOpts) != 3 { // save + reset + crashwatch
		t.Fatalf("view picker opts=%v", m.pickerOpts)
	}
	m.pickerWin.End() // the saved view is listed after the two actions
	mi, _ = m.pickerSelect()
	m = asModel(t, mi)
	if m.curType.Key() != "v1/pods" || m.client.Namespace != "demo" || m.filter.Value() != "worker" {
		t.Fatalf("view not applied: type=%q ns=%q filter=%q", m.curType.Key(), m.client.Namespace, m.filter.Value())
	}
	if got := colTitles(m.columnsForType()); got[0] != "NAME" || got[1] != "NODE" {
		t.Fatalf("view columns not applied: %v", got)
	}

	// Reset drops the customization of the current type.
	m.resetCurrentView()
	if _, ok := m.cfg.ViewPrefs["v1/pods"]; ok {
		t.Fatal("reset must delete the type pref")
	}
	if m.filter.Value() != "" || m.sortCol != -1 {
		t.Fatalf("reset left filter=%q sortCol=%d", m.filter.Value(), m.sortCol)
	}
}

func TestSaveViewNamingKeysAreCaptured(t *testing.T) {
	m := newViewsModel(t)
	m.viewNaming = true
	// Typing "q" must edit the name, not quit the app.
	mi, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = asModel(t, mi)
	if cmd != nil {
		t.Fatal("typing in the naming prompt triggered a command (quit?)")
	}
	if m.viewName != "q" {
		t.Fatalf("viewName=%q", m.viewName)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.viewNaming || len(m.cfg.SavedViews) != 1 || m.cfg.SavedViews[0].Name != "q" {
		t.Fatalf("naming commit failed: naming=%v views=%+v", m.viewNaming, m.cfg.SavedViews)
	}
}

func TestApplySavedViewUnknownTypeIsTolerated(t *testing.T) {
	m := newViewsModel(t)
	mi, _ := m.applySavedView(config.SavedView{Name: "gone", Type: "acme.io/v1/widgets"})
	m = asModel(t, mi)
	if m.errMsg == "" {
		t.Fatal("expected an explicit error message for a view whose type is absent")
	}
	if m.curType.Key() != "v1/pods" {
		t.Fatalf("current type must be unchanged, got %q", m.curType.Key())
	}
}

// TestFlexColumnCappedToContent (owner report 2026-07-10): on a wide
// terminal the NAME column stops at its longest content instead of
// swallowing the whole width and pushing the other columns to the far right.
func TestFlexColumnCappedToContent(t *testing.T) {
	m := newViewsModel(t)
	m.width = 400 // very wide terminal
	m.layout()
	m.objects = []model.ResourceObject{
		{Name: "sdk-app-service-cfb7d7c8b-5znsw", Namespace: "demo"},
		{Name: "short", Namespace: "demo"},
	}
	m.applyRows()
	cols := m.columnsForType()
	widths := m.listWidths(cols)
	nameIdx := -1
	for i, c := range cols {
		if c.title == "NAME" {
			nameIdx = i + 1 // +1: mark column
		}
	}
	longest := len("sdk-app-service-cfb7d7c8b-5znsw")
	if w := widths[nameIdx]; w < longest || w > longest+4 {
		t.Fatalf("NAME width=%d, want ~%d (content-capped, not %d-wide)", w, longest+2, m.width)
	}
	// Narrow terminal keeps the old behavior (flex absorbs what exists).
	m.width = 100
	if w := m.listWidths(cols)[nameIdx]; w > 100 {
		t.Fatalf("narrow width must stay bounded, got %d", w)
	}
}

package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestTypePickerTypeToFilterAndSelect(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.types = []model.ResourceType{
		pods,
		{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet", Namespaced: true},
	}
	m.width, m.height = 100, 20
	m.layout()

	mi, _ := m.openPicker(pickType)
	m = mi.(Model)
	if m.screen != screenPicker {
		t.Fatalf("picker did not open, screen=%d", m.screen)
	}
	t.Logf("picker options=%v selectedRow=%v", m.pickerOpts, m.picker.SelectedRow())

	// Type-to-filter: typing "stateful" narrows to the statefulsets entry (k9s-style).
	for _, r := range "stateful" {
		mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mi.(Model)
	}
	rows := m.picker.SelectedRow()
	t.Logf("after typing 'stateful': selectedRow=%v", rows)
	if len(rows) == 0 || rows[0] != "apps/v1/statefulsets" {
		t.Fatalf("type-to-filter did not narrow to statefulsets, got %v", rows)
	}

	// Select the filtered result.
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = mi.(Model)
	t.Logf("after enter: curType=%q screen=%d", m.curType.Key(), m.screen)
	if m.curType.Resource != "statefulsets" {
		t.Fatalf("BUG: selection did not switch to statefulsets, got %q", m.curType.Key())
	}

	// Backspace clears part of the filter and widens the list again.
	mi, _ = m.openPicker(pickType)
	m = mi.(Model)
	for _, r := range "pods" {
		mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mi.(Model)
	}
	if got := len(m.picker.Rows()); got != 1 {
		t.Fatalf("expected 1 row after filtering 'pods', got %d", got)
	}
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = mi.(Model)
	if got := len(m.picker.Rows()); got < 1 {
		t.Fatalf("backspace should widen results, got %d rows", got)
	}
}

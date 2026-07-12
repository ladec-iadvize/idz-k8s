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

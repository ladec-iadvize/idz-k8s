package ui

import (
	"os"
	"path/filepath"
	"slices"
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
	selRow, _ := m.pickerWin.Selected()
	t.Logf("picker options=%v selectedRow=%v", m.pickerOpts, selRow)

	// Type-to-filter: typing "stateful" narrows to the statefulsets entry (k9s-style).
	for _, r := range "stateful" {
		mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mi.(Model)
	}
	rows, _ := m.pickerWin.Selected()
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
	if got := m.pickerWin.Len(); got != 1 {
		t.Fatalf("expected 1 row after filtering 'pods', got %d", got)
	}
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyBackspace})
	m = mi.(Model)
	if got := m.pickerWin.Len(); got < 1 {
		t.Fatalf("backspace should widen results, got %d rows", got)
	}
}

// TestNamespacePickerOpensInstantlyAndUpdatesAsync: opening the namespace
// picker must never call the cluster synchronously (a slow apiserver would
// freeze the whole Update loop for its timeout); best-effort options show
// immediately and the real list lands via nsOptionsMsg.
func TestNamespacePickerOpensInstantlyAndUpdatesAsync(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 100, 24
	m.layout()
	m.objects = []model.ResourceObject{{Namespace: "demo", Name: "web-1"}}

	mi, cmd := m.openPicker(pickNamespace)
	m = asModel(t, mi)
	if m.screen != screenPicker {
		t.Fatalf("picker did not open, screen=%d", m.screen)
	}
	if cmd == nil {
		t.Fatalf("expected an async namespace-fetch cmd, got nil")
	}
	if len(m.pickerOpts) == 0 || m.pickerOpts[0] != allNamespacesLabel {
		t.Fatalf("best-effort options should open with the all-namespaces sentinel, got %v", m.pickerOpts)
	}
	if !slices.Contains(m.pickerOpts, "demo") {
		t.Fatalf("best-effort options should include on-screen namespaces, got %v", m.pickerOpts)
	}

	// The async result replaces the options: sorted, sentinel kept on top.
	mi, _ = m.Update(nsOptionsMsg{opts: []string{"zeta", "alpha"}})
	m = asModel(t, mi)
	want := []string{allNamespacesLabel, "alpha", "zeta"}
	if !slices.Equal(m.pickerOpts, want) {
		t.Fatalf("async options not applied, got %v want %v", m.pickerOpts, want)
	}

	// A result landing after the picker closed must be dropped.
	m.screen = screenList
	mi, _ = m.Update(nsOptionsMsg{opts: []string{"stale"}})
	m = asModel(t, mi)
	if !slices.Equal(m.pickerOpts, want) {
		t.Fatalf("stale async options should be dropped, got %v", m.pickerOpts)
	}
}

const pickerTestKubeconfig = `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: c1
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: dev
  context:
    cluster: c1
    user: u1
- name: prod
  context:
    cluster: c1
    user: u1
users:
- name: u1
  user:
    token: fake-token
`

// TestContextPickerMarksActiveAndStripsOnSelect: the context picker annotates
// the active context (FR-003 marker, same "  (…)" language as the type
// picker) and the annotation is stripped before the context is used.
func TestContextPickerMarksActiveAndStripsOnSelect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(pickerTestKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	m := New(&kube.Client{}, config.Defaults(), path, WithInitialType(pods))
	m.width, m.height = 100, 24
	m.layout()

	mi, _ := m.openPicker(pickContext)
	m = asModel(t, mi)
	if !slices.Contains(m.pickerOpts, "dev"+activeContextSuffix) {
		t.Fatalf("active context should carry the marker, got %v", m.pickerOpts)
	}
	if !slices.Contains(m.pickerOpts, "prod") {
		t.Fatalf("inactive context should be unmarked, got %v", m.pickerOpts)
	}

	// Selecting the marked entry (cursor starts on "dev  (active)") must
	// strip the suffix: the new client targets "dev", not a bogus name.
	mi, _ = m.pickerSelect()
	m = asModel(t, mi)
	if got := m.client.ActiveContext(); got != "dev" {
		t.Fatalf("selected context should be dev, got %q (marker not stripped?)", got)
	}
	if m.screen != screenList {
		t.Fatalf("expected to land on the list, screen=%d", m.screen)
	}
}

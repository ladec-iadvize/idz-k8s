package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestPersistSavesLastSelections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	m := New(&kube.Client{Namespace: "team-a"}, config.Defaults(), "",
		WithConfigPath(path),
		WithInitialType(model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment"}),
	)
	m.persist()

	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastNamespace != "team-a" {
		t.Errorf("LastNamespace=%q want team-a", got.LastNamespace)
	}
	if got.LastType != "apps/v1/deployments" {
		t.Errorf("LastType=%q want apps/v1/deployments", got.LastType)
	}
}

// TestConfigFileHotReload (owner request 2026-08-21): the config file is a
// supported editing surface for view customizations — hand edits to
// viewPrefs (including label:/field: custom columns) must take effect at the
// next tick, without a restart, while the in-app 'C' chooser keeps working
// against the same file.
func TestConfigFileHotReload(t *testing.T) {
	dep := model.ResourceType{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true}
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Defaults()
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	m := New(&kube.Client{Namespace: "demo"}, cfg, "",
		WithConfigPath(path), WithInitialType(dep))
	m.width, m.height = 160, 30
	m.layout()

	hasTitle := func(title string) bool {
		for _, c := range m.columnsForType() {
			if c.title == title {
				return true
			}
		}
		return false
	}
	if hasTitle("TEAM") {
		t.Fatal("custom column must not exist before the file edit")
	}

	// Hand-edit the file: a custom label column for deployments. Bump the
	// mtime past filesystem granularity so the change is unambiguous.
	edited := cfg
	edited.ViewPrefs = map[string]config.ViewPref{
		dep.Key(): {Columns: []string{"NAME", "label:team"}},
	}
	if err := config.Save(path, edited); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	mi, _ := m.Update(tickMsg{})
	m = asModel(t, mi)
	if !hasTitle("TEAM") {
		t.Fatalf("file edit not picked up at tick — columns: %v", titlesOf(m.columnsForType()))
	}
	if m.columnsForType()[0].title != "NAME" {
		t.Fatalf("file-defined order not applied — columns: %v", titlesOf(m.columnsForType()))
	}

	// A malformed file must keep the current settings (FR-025 tolerance) and
	// say so, never crash or reset.
	if err := os.WriteFile(path, []byte("viewPrefs: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	future = future.Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	mi, _ = m.Update(tickMsg{})
	m = asModel(t, mi)
	if !hasTitle("TEAM") {
		t.Fatal("malformed file must not drop the current arrangement")
	}
	if !strings.Contains(m.statusMsg, "does not parse") {
		t.Fatalf("malformed file must be reported, statusMsg=%q", m.statusMsg)
	}
}

// TestPersistIsNotAnExternalEdit: the app's own saves must not trigger the
// hot-reload path (a reload notice on every in-app change would be noise).
func TestPersistIsNotAnExternalEdit(t *testing.T) {
	dep := model.ResourceType{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true}
	path := filepath.Join(t.TempDir(), "config.yaml")
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithConfigPath(path), WithInitialType(dep))
	m.width, m.height = 160, 30
	m.layout()
	m.persist()
	mi, _ := m.Update(tickMsg{})
	m = asModel(t, mi)
	if strings.Contains(m.statusMsg, "reloaded") {
		t.Fatalf("own save read as external edit, statusMsg=%q", m.statusMsg)
	}
}

func titlesOf(cols []listColumn) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.title
	}
	return out
}

func TestFindTypeByKey(t *testing.T) {
	types := []model.ResourceType{
		{Version: "v1", Resource: "pods", Kind: "Pod"},
		{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment"},
	}
	if _, ok := findTypeByKey(types, ""); ok {
		t.Error("empty key must not match")
	}
	if _, ok := findTypeByKey(types, "batch/v1/jobs"); ok {
		t.Error("unknown key must not match")
	}
	got, ok := findTypeByKey(types, "apps/v1/deployments")
	if !ok || got.Resource != "deployments" {
		t.Errorf("expected deployments, got %+v ok=%v", got, ok)
	}
}

// TestActiveFilterFollowsTypeSwitch (owner decision 2026-07-30): a committed
// '/' filter follows across ':' type switches (visible as the header chip);
// without an active filter, the target type's saved filter comes back.
func TestActiveFilterFollowsTypeSwitch(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	deps := model.ResourceType{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 120, 30
	m.layout()
	m.types = []model.ResourceType{pods, deps}
	m.cfg.ViewPrefs = map[string]config.ViewPref{
		deps.Key(): {Filter: "saved-dep-filter"},
	}

	switchTo := func(key string) {
		t.Helper()
		mi, _ := m.openPicker(pickType)
		m = asModel(t, mi)
		found := false
		for i, row := range m.pickerWin.rows {
			if strings.HasPrefix(row[0], key) {
				m.pickerWin.cursor = i
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("type %q not in picker", key)
		}
		mi, _ = m.pickerSelect()
		m = asModel(t, mi)
	}

	// An active filter follows to the next type, overriding its saved one.
	m.filter.SetValue("back")
	switchTo(deps.Key())
	if got := m.filter.Value(); got != "back" {
		t.Fatalf("active filter must follow the type switch, got %q", got)
	}
	// No active filter → the target type's saved filter is restored.
	m.filter.SetValue("")
	switchTo(pods.Key())
	if got := m.filter.Value(); got != "" {
		t.Fatalf("pods have no saved filter, got %q", got)
	}
	switchTo(deps.Key())
	if got := m.filter.Value(); got != "saved-dep-filter" {
		t.Fatalf("saved filter must come back when nothing is active, got %q", got)
	}
}

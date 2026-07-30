package ui

import (
	"path/filepath"
	"strings"
	"testing"

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

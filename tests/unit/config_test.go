package unit

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/model"
	"github.com/iadvize/idz-k8s/internal/ui/theme"
)

func TestConfigDefaults(t *testing.T) {
	d := config.Defaults()
	if d.RefreshIntervalSeconds != 5 {
		t.Fatalf("expected default refresh 5s, got %d", d.RefreshIntervalSeconds)
	}
	if d.SchemaVersion != 1 {
		t.Fatalf("expected schemaVersion 1, got %d", d.SchemaVersion)
	}
}

func TestConfigLoadMissingReturnsDefaults(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if cfg.RefreshIntervalSeconds != 5 {
		t.Fatalf("missing file should yield defaults, got %d", cfg.RefreshIntervalSeconds)
	}
}

func TestConfigLoadCorruptFallsBackToDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	if err := writeFile(path, "this: : : not valid yaml: ["); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err == nil {
		t.Fatalf("corrupt file should surface an error for logging")
	}
	if cfg.RefreshIntervalSeconds != 5 {
		t.Fatalf("corrupt file must still yield defaults (no crash), got %d", cfg.RefreshIntervalSeconds)
	}
}

func TestConfigSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	in := config.Defaults()
	in.RefreshIntervalSeconds = 10
	in.PrometheusURL = "http://prom:9090"
	if err := config.Save(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if out.RefreshIntervalSeconds != 10 || out.PrometheusURL != "http://prom:9090" {
		t.Fatalf("round trip mismatch: %+v", out)
	}
}

func TestConfigNormalizeInvalidRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := writeFile(path, "schemaVersion: 1\nrefreshIntervalSeconds: 0\ntheme: bogus\n"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RefreshIntervalSeconds != 5 {
		t.Fatalf("invalid refresh must normalize to 5, got %d", cfg.RefreshIntervalSeconds)
	}
	if cfg.Theme != "auto" {
		t.Fatalf("invalid theme must normalize to auto, got %q", cfg.Theme)
	}
}

func TestHealthFallback(t *testing.T) {
	// Non-color fallback: symbols must be distinct and present (FR-020).
	seen := map[string]bool{}
	for _, l := range []model.HealthLevel{model.HealthOk, model.HealthWarning, model.HealthError, model.HealthUnknown} {
		s := l.Symbol()
		if s == "" {
			t.Fatalf("health %d has empty symbol", l)
		}
		if seen[s] {
			t.Fatalf("duplicate symbol %q", s)
		}
		seen[s] = true
		if l.Label() == "" {
			t.Fatalf("health %d has empty label", l)
		}
	}
}

// TestConfigSchemaIsAllowlisted: the keyword grep below can only catch a
// field whose NAME contains a banned word — never a secret VALUE. This
// value-level guard forces every new Config field through a conscious
// "this can never hold secret material" decision.
func TestConfigSchemaIsAllowlisted(t *testing.T) {
	allowed := map[string]bool{
		"SchemaVersion": true, "RefreshIntervalSeconds": true, "PrometheusURL": true,
		"Theme": true, "LastContext": true, "LastNamespace": true, "LastType": true,
		"ViewPrefs": true, "SavedViews": true,
	}
	tp := reflect.TypeOf(config.Config{})
	for i := 0; i < tp.NumField(); i++ {
		if name := tp.Field(i).Name; !allowed[name] {
			t.Errorf("new Config field %q: confirm it can never hold secret material (FR-015/Principle IV), then allowlist it here", name)
		}
	}
	if tp.NumField() != len(allowed) {
		t.Errorf("Config has %d fields, allowlist has %d — remove stale entries", tp.NumField(), len(allowed))
	}
}

// TestConfigFileNeverContainsSecrets: the preferences file must only hold
// layout/preferences — never credentials or secret-ish material (Principle IV).
func TestConfigFileNeverContainsSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Defaults()
	cfg.PrometheusURL = "http://prom:9090"
	cfg.LastContext = "dev-main"
	cfg.LastNamespace = "team-a"
	cfg.LastType = "v1/pods"
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.ToLower(string(data))
	for _, banned := range []string{"token", "password", "secret", "kubeconfig", "certificate", "bearer"} {
		if strings.Contains(content, banned) {
			t.Errorf("config file must never contain %q, got:\n%s", banned, content)
		}
	}
}

// TestViewPrefsAndSavedViewsRoundTrip: the US8 customizations persist and
// reload identically.
func TestViewPrefsAndSavedViewsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Defaults()
	cfg.ViewPrefs = map[string]config.ViewPref{
		"v1/pods": {Columns: []string{"NAME", "NODE"}, SortCol: "RESTARTS", SortAsc: false, Filter: "api"},
	}
	cfg.SavedViews = []config.SavedView{
		{Name: "crashwatch", Type: "v1/pods", Namespace: "team-a", Columns: []string{"NAME"}, SortCol: "AGE", Filter: "worker"},
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := got.ViewPrefs["v1/pods"]
	if len(p.Columns) != 2 || p.SortCol != "RESTARTS" || p.SortAsc || p.Filter != "api" {
		t.Fatalf("ViewPrefs round-trip mismatch: %+v", p)
	}
	if len(got.SavedViews) != 1 || got.SavedViews[0].Name != "crashwatch" || got.SavedViews[0].Type != "v1/pods" {
		t.Fatalf("SavedViews round-trip mismatch: %+v", got.SavedViews)
	}
}

// TestSavedViewsToleranceOnLoad: nameless/typeless/duplicate saved views are
// dropped on load instead of breaking startup (FR-025).
func TestSavedViewsToleranceOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := `schemaVersion: 1
refreshIntervalSeconds: 5
theme: auto
savedViews:
  - name: ""
    type: v1/pods
  - name: orphan
  - name: keep
    type: v1/pods
  - name: keep
    type: apps/v1/deployments
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.SavedViews) != 1 {
		t.Fatalf("expected 1 surviving view, got %+v", got.SavedViews)
	}
	if got.SavedViews[0].Name != "keep" || got.SavedViews[0].Type != "v1/pods" {
		t.Fatalf("wrong survivor (first wins): %+v", got.SavedViews[0])
	}
}

// TestThemeForName: explicit names resolve, unknown falls back to auto (the
// terminal default) without erroring — visible difference asserted via a
// palette that differs between dark and light.
func TestThemeForName(t *testing.T) {
	dark := theme.ForName("dark")
	light := theme.ForName("light")
	if dark.Error.GetForeground() == light.Error.GetForeground() {
		t.Fatal("dark and light palettes must differ")
	}
	// auto/unknown must not panic and must return one of the two.
	for _, n := range []string{"auto", "none", "wat"} {
		got := theme.ForName(n)
		if got.Error.GetForeground() != dark.Error.GetForeground() &&
			got.Error.GetForeground() != light.Error.GetForeground() {
			t.Fatalf("ForName(%q) returned an unexpected palette", n)
		}
	}
}

// TestHelpStylesAreReadable (owner report 2026-07-12): the help overlay must
// not rely on faint defaults — both palettes define an explicit, readable
// description color, distinct per theme.
func TestHelpStylesAreReadable(t *testing.T) {
	dark, light := theme.ForName("dark"), theme.ForName("light")
	if dark.HelpDesc.GetForeground() == nil || light.HelpDesc.GetForeground() == nil {
		t.Fatal("help description colors must be explicit, not faint defaults")
	}
	if dark.HelpDesc.GetForeground() == light.HelpDesc.GetForeground() {
		t.Fatal("help colors must be tuned per background")
	}
	if !dark.HelpKey.GetBold() || !light.HelpKey.GetBold() {
		t.Fatal("shortcut keys must stand out (bold)")
	}
}

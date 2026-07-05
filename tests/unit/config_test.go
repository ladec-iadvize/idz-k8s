package unit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/model"
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

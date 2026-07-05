// Package config loads and saves the small, secret-free preferences file.
// A malformed or unreadable file loads as defaults and never blocks startup
// (see contracts/config-schema.md). No credentials or secret values are stored.
package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config holds user preferences. See contracts/config-schema.md (v1).
type Config struct {
	SchemaVersion          int    `yaml:"schemaVersion"`
	RefreshIntervalSeconds int    `yaml:"refreshIntervalSeconds"`
	PrometheusURL          string `yaml:"prometheusURL,omitempty"`
	Theme                  string `yaml:"theme"`
	LastContext            string `yaml:"lastContext,omitempty"`
	LastNamespace          string `yaml:"lastNamespace,omitempty"`
	LastType               string `yaml:"lastType,omitempty"`
}

// Defaults returns the built-in defaults (FR-006: refresh default ~5s).
func Defaults() Config {
	return Config{
		SchemaVersion:          1,
		RefreshIntervalSeconds: 5,
		Theme:                  "auto",
	}
}

// DefaultPath returns $XDG_CONFIG_HOME/idz-k8s/config.yaml (or ~/.config/...).
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "config.yaml"
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "idz-k8s", "config.yaml")
}

// Load reads the config file. A missing/malformed file yields Defaults() with a
// nil error for "not found" and returns the parse error only for reporting; the
// caller may log it and proceed with defaults (FR-026 tolerance analogue).
func Load(path string) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	var parsed Config
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		// Corrupt file: fall back to defaults, surface the error for logging.
		return Defaults(), err
	}
	cfg = normalize(parsed)
	return cfg, nil
}

// normalize applies field rules from the contract (invalid → default).
func normalize(c Config) Config {
	d := Defaults()
	if c.SchemaVersion < 1 {
		c.SchemaVersion = d.SchemaVersion
	}
	if c.RefreshIntervalSeconds < 1 {
		c.RefreshIntervalSeconds = d.RefreshIntervalSeconds
	}
	switch c.Theme {
	case "auto", "dark", "light", "none":
	default:
		c.Theme = d.Theme
	}
	return c
}

// Save writes the config atomically (write-temp-then-rename).
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(normalize(c))
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

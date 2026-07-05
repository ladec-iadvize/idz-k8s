package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestHelmPackageNeverConstructsMutatingActions is a source-level guard for
// the Helm read-only invariant (FR-029/SC-017): the helm package must never
// reference an install/upgrade/rollback/uninstall action.
func TestHelmPackageNeverConstructsMutatingActions(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	helmDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "internal", "helm")
	entries, err := os.ReadDir(helmDir)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"NewInstall", "NewUpgrade", "NewRollback", "NewUninstall",
		"action.Install", "action.Upgrade", "action.Rollback", "action.Uninstall",
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(helmDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		for _, f := range forbidden {
			if strings.Contains(src, f) {
				t.Errorf("%s references %q — the helm layer must stay read-only", e.Name(), f)
			}
		}
	}
}

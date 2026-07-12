package integration

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestHelmPackageNeverConstructsMutatingActions is a source-level guard for
// the Helm read-only invariant (FR-029/SC-017): the helm package must never
// reference an install/upgrade/rollback/uninstall action, nor mutate release
// storage. The walk is recursive so restructuring internal/helm into
// sub-packages cannot silently drop files out of the guard.
func TestHelmPackageNeverConstructsMutatingActions(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	helmDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "internal", "helm")
	forbidden := []string{
		"NewInstall", "NewUpgrade", "NewRollback", "NewUninstall",
		"action.Install", "action.Upgrade", "action.Rollback", "action.Uninstall",
		// Storage-level mutations: the same driver object that serves
		// ListReleases/History also exposes writes — ban them at the source.
		"Releases.Create", "Releases.Update", "Releases.Delete",
	}
	checked := 0
	err := filepath.WalkDir(helmDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		src := string(data)
		for _, f := range forbidden {
			if strings.Contains(src, f) {
				t.Errorf("%s references %q — the helm layer must stay read-only", d.Name(), f)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("no helm source files swept — wrong directory?")
	}
}

package integration

// v3 helm admin actions (rollback/uninstall) against the in-memory storage
// driver and Helm's own fake kube client — no live cluster.

import (
	"testing"

	"helm.sh/helm/v3/pkg/release"
)

func TestHelmRollbackCreatesNewRevision(t *testing.T) {
	hc := memoryHelm(t,
		rel("audience-back", "back", 1, release.StatusSuperseded, "common-deployment-chart", "0.27.0"),
		rel("audience-back", "back", 2, release.StatusDeployed, "common-deployment-chart", "0.28.1"),
	)
	if err := hc.Rollback("audience-back", "back", 0); err != nil {
		t.Fatal(err)
	}
	hist, err := hc.History("audience-back", "back")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("history after rollback: %+v", hist)
	}
	// Most recent first: revision 3 is the rollback to revision 1's chart.
	if hist[0].Revision != 3 || hist[0].Status != release.StatusDeployed.String() {
		t.Fatalf("rollback revision: %+v", hist[0])
	}
	rows, err := hc.Releases("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ChartVersion != "0.27.0" {
		t.Fatalf("rolled-back release: %+v", rows)
	}
}

func TestHelmUninstallRemovesRelease(t *testing.T) {
	hc := memoryHelm(t,
		rel("audience-back", "back", 1, release.StatusDeployed, "common-deployment-chart", "0.28.1"),
		rel("staging-back", "front", 1, release.StatusDeployed, "common-deployment-chart", "0.27.0"),
	)
	if err := hc.Uninstall("audience-back", "back"); err != nil {
		t.Fatal(err)
	}
	rows, err := hc.Releases("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Name != "front" {
		t.Fatalf("releases after uninstall: %+v", rows)
	}
}

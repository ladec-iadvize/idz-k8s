package integration

import (
	"io"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	helmtime "helm.sh/helm/v3/pkg/time"

	helmpkg "github.com/iadvize/idz-k8s/internal/helm"
	"github.com/iadvize/idz-k8s/internal/model"
)

// memoryHelm builds a helm client backed by Helm's in-memory storage driver,
// seeded with the given releases (research D10: no live cluster needed).
func memoryHelm(t *testing.T, rels ...*release.Release) *helmpkg.Client {
	t.Helper()
	mem := driver.NewMemory()
	store := storage.Init(mem)
	for _, r := range rels {
		if err := store.Create(r); err != nil {
			t.Fatal(err)
		}
	}
	// Memory.Create pins the driver to the last release's namespace; reset to
	// "" AFTER seeding so List sees all namespaces.
	mem.SetNamespace("")
	cfg := &action.Configuration{
		Releases:   store,
		KubeClient: &kubefake.PrintingKubeClient{Out: io.Discard},
		Log:        func(string, ...interface{}) {}, // Rollback/Uninstall call it unconditionally
	}
	// Honor the namespace argument like the real driver does: Memory.Get only
	// sees the pinned namespace, so admin actions need their release's one
	// ("" keeps the all-namespaces list behavior).
	return helmpkg.NewWithProvider(func(ns string) (*action.Configuration, error) {
		mem.SetNamespace(ns)
		return cfg, nil
	})
}

func rel(ns, name string, revision int, status release.Status, chartName, version string) *release.Release {
	return &release.Release{
		Name:      name,
		Namespace: ns,
		Version:   revision,
		Info: &release.Info{
			Status:       status,
			LastDeployed: helmtime.Now(),
			Description:  "Upgrade complete",
		},
		Chart: &chart.Chart{Metadata: &chart.Metadata{
			Name: chartName, Version: version, AppVersion: "1.2.3",
		}},
	}
}

func TestHelmReleasesReadOnlyList(t *testing.T) {
	hc := memoryHelm(t,
		rel("audience-back", "back", 12, release.StatusDeployed, "common-deployment-chart", "0.28.1"),
		rel("staging-back", "front", 3, release.StatusFailed, "common-deployment-chart", "0.27.0"),
	)
	rows, err := hc.Releases("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(rows))
	}
	byNS := map[string]model.HelmRelease{}
	for _, r := range rows {
		byNS[r.Namespace] = r
	}
	ok := byNS["audience-back"]
	if ok.Chart != "common-deployment-chart" || ok.ChartVersion != "0.28.1" || ok.Revision != 12 {
		t.Errorf("deployed release mapped wrong: %+v", ok)
	}
	if ok.Health() != model.HealthOk {
		t.Errorf("deployed should be Ok, got %v", ok.Health())
	}
	if failed := byNS["staging-back"]; failed.Health() != model.HealthError {
		t.Errorf("failed release should be Error, got %+v", failed)
	}
}

func TestHelmHistoryMostRecentFirst(t *testing.T) {
	hc := memoryHelm(t,
		rel("demo", "web", 1, release.StatusSuperseded, "web-chart", "1.0.0"),
		rel("demo", "web", 2, release.StatusDeployed, "web-chart", "1.1.0"),
	)
	revs, err := hc.History("demo", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions, got %d", len(revs))
	}
	if revs[0].Revision != 2 || revs[0].Status != "deployed" {
		t.Fatalf("most recent revision first, got %+v", revs[0])
	}
	if revs[1].Revision != 1 || revs[1].Status != "superseded" {
		t.Fatalf("older revision second, got %+v", revs[1])
	}
}

func TestHelmDetailResourcesAndValues(t *testing.T) {
	r := rel("demo", "web", 2, release.StatusDeployed, "web-chart", "1.1.0")
	r.Manifest = `---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
---
apiVersion: v1
kind: Service
metadata:
  name: web
  namespace: demo
---
# a comment-only doc that must be skipped
`
	r.Config = map[string]interface{}{
		"replicaCount": 3,
		"image":        map[string]interface{}{"tag": "1.2.3"},
	}
	hc := memoryHelm(t, r)

	det, err := hc.Detail("demo", "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(det.History) != 1 || det.History[0].Revision != 2 {
		t.Fatalf("history wrong: %+v", det.History)
	}
	if len(det.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d (%+v)", len(det.Resources), det.Resources)
	}
	if det.Resources[0].Kind != "Deployment" || det.Resources[0].Name != "web" {
		t.Errorf("first resource wrong: %+v", det.Resources[0])
	}
	if det.Resources[1].Kind != "Service" || det.Resources[1].Namespace != "demo" {
		t.Errorf("second resource wrong: %+v", det.Resources[1])
	}
	if !strings.Contains(det.Values, "replicaCount: 3") || !strings.Contains(det.Values, "tag: 1.2.3") {
		t.Errorf("values YAML wrong: %q", det.Values)
	}
}

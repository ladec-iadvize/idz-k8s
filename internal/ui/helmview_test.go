package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/helm"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func helmModel(t *testing.T) Model {
	t.Helper()
	dep := model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(dep))
	m.width, m.height = 120, 30
	m.layout()
	m.screen = screenHelm
	m.helmTable.SetColumns([]table.Column{
		{Title: "NAMESPACE", Width: 22}, {Title: "RELEASE", Width: 28}, {Title: "CHART", Width: 20},
		{Title: "VERSION", Width: 12}, {Title: "REV", Width: 5}, {Title: "STATUS", Width: 14}, {Title: "UPDATED", Width: 9},
	})
	m.helmRows = []model.HelmRelease{
		{Namespace: "demo", Name: "back-api", Chart: "backend", Status: "deployed", Updated: time.Now()},
		{Namespace: "demo", Name: "front", Chart: "webapp", Status: "deployed", Updated: time.Now()},
	}
	m.renderHelm()
	return m
}

// TestHelmFilterNarrowsReleases (owner bug 2026-07-07: '/' did nothing in the
// helm view): typing is captured, Enter commits, Esc clears.
func TestHelmFilterNarrowsReleases(t *testing.T) {
	m := helmModel(t)
	if m.helmWin.Len() != 2 {
		t.Fatalf("seed rows=%d", m.helmWin.Len())
	}
	// '/' opens the filter.
	mi, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	if !m.helmFiltering {
		t.Fatal("'/' must open the helm filter")
	}
	// Typing "q" must edit the query, never quit.
	for _, r := range "front" {
		mi, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if cmd != nil {
			t.Fatal("typing in the helm filter triggered a command")
		}
		m = asModel(t, mi)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.helmFiltering || m.helmQuery != "front" {
		t.Fatalf("commit failed: filtering=%v query=%q", m.helmFiltering, m.helmQuery)
	}
	if m.helmWin.Len() != 1 {
		t.Fatalf("filter 'front' should keep 1 release, got %d", m.helmWin.Len())
	}
	// The committed query stays visible as a header chip.
	if line, _ := m.buildHeaderLine(); !strings.Contains(line, "filter:front") {
		t.Fatalf("committed helm filter must show in the header:\n%s", line)
	}
	// Esc while typing clears it.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.helmQuery != "" || m.helmWin.Len() != 2 {
		t.Fatalf("Esc should clear: query=%q rows=%d", m.helmQuery, m.helmWin.Len())
	}
}

// TestHelmHeaderChipShowsHelm (owner bug 2026-07-07): the type chip must
// reflect the helm screen, not the previously browsed resource type.
func TestHelmHeaderChipShowsHelm(t *testing.T) {
	m := helmModel(t)
	line, _ := m.buildHeaderLine()
	if !strings.Contains(line, "helm releases") {
		t.Fatalf("helm view header must say 'helm releases':\n%s", line)
	}
	if strings.Contains(line, "apps/v1/deployments") {
		t.Fatalf("header still shows the previous type:\n%s", line)
	}
	// Back on the list, the chip shows the browsed type again.
	m.screen = screenList
	line, _ = m.buildHeaderLine()
	if !strings.Contains(line, "apps/v1/deployments") {
		t.Fatalf("list header must show the resource type:\n%s", line)
	}
}

// helmDetailModel returns a model showing a release detail with two deployed
// resources, hooks, notes and a two-revision history.
func helmDetailModel(t *testing.T) Model {
	t.Helper()
	m := helmModel(t)
	svcType := model.ResourceType{Version: "v1", Kind: "Service", Resource: "services", Namespaced: true}
	m.types = []model.ResourceType{svcType}
	now := time.Now()
	det := helmDetailMsg{ns: "demo", name: "back-api", detail: helm.ReleaseDetail{
		History: []model.HelmRevision{
			{Revision: 2, Status: "deployed", Updated: now, Description: "Upgrade complete",
				ChartVersion: "0.28.1", AppVersion: "1.2.3"},
			{Revision: 1, Status: "superseded", Updated: now.Add(-time.Hour), Description: "Install complete",
				ChartVersion: "0.27.0", AppVersion: "1.2.2"},
		},
		Resources: []model.HelmResource{
			{APIVersion: "apps/v1", Kind: "Deployment", Name: "back",
				Manifest: "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: back\nspec:\n  replicas: 2"},
			{APIVersion: "v1", Kind: "Service", Name: "back", Namespace: "demo",
				Manifest: "apiVersion: v1\nkind: Service\nmetadata:\n  name: back\nspec:\n  ports:\n  - port: 8080"},
		},
		Values: "replicaCount: 2\n",
		Notes:  "Release deployed. Check https://back.example",
		Hooks: []model.HelmHook{{Name: "back-migrate", Kind: "Job",
			Events: []string{"pre-upgrade"}, Phase: "Failed", Started: now}},
		Chart: model.HelmChartInfo{Name: "common-deployment-chart", Version: "0.28.1",
			AppVersion: "1.2.3", Description: "iAdvize common chart", Dependencies: []string{"redis-17.1.0"}},
	}}
	m.screen = screenHelmHist
	mi, _ := m.Update(det)
	return asModel(t, mi)
}

// TestHelmDetailShowsRicherHistoryAndSections (owner request 2026-08-05:
// "peut-être plus d'info" on the history): per-revision chart/app versions,
// the chart metadata, its hooks and NOTES.txt.
func TestHelmDetailShowsRicherHistoryAndSections(t *testing.T) {
	m := helmDetailModel(t)
	// The whole rendered content, not just the visible window: the lower
	// sections legitimately sit below the fold on a 30-line terminal.
	view := xansi.Strip(m.vpRaw[screenHelmHist])
	for _, want := range []string{
		"CHART", "APP", "0.28.1", "0.27.0", "1.2.2", // history columns, both revisions
		"common-deployment-chart", "iAdvize common chart", "redis-17.1.0", // chart section
		"Hooks", "back-migrate", "Failed", "pre-upgrade", // hooks
		"Notes", "back.example", // NOTES.txt
		"Values", "replicaCount: 2", // unchanged
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("detail view missing %q:\n%s", want, view)
		}
	}
	// The current revision is marked.
	if !strings.Contains(view, "▸ 2") {
		t.Fatalf("the deployed revision must be marked:\n%s", view)
	}
}

// TestHelmResourceDefinitionAndLive (owner request 2026-08-05): ↑/↓ select a
// deployed resource, Enter shows the chart-rendered definition, 'y' fetches
// the live object.
func TestHelmResourceDefinitionAndLive(t *testing.T) {
	m := helmDetailModel(t)
	if m.helmResSel != 0 || len(m.helmResLines) != 2 {
		t.Fatalf("selection state: sel=%d lines=%v", m.helmResSel, m.helmResLines)
	}
	if !strings.Contains(xansi.Strip(m.helmHist.View()), "▸ Deployment/back") {
		t.Fatalf("the first resource must be selected:\n%s", xansi.Strip(m.helmHist.View()))
	}

	// Enter opens ITS definition, not the whole manifest.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.screen != screenHelmRes {
		t.Fatalf("Enter must open the resource definition, screen=%d", m.screen)
	}
	def := xansi.Strip(m.helmRes.View())
	if !strings.Contains(def, "replicas: 2") || strings.Contains(def, "port: 8080") {
		t.Fatalf("definition must be the selected resource only:\n%s", def)
	}

	// Esc returns to the detail; ↓ moves to the Service.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.screen != screenHelmHist {
		t.Fatalf("Esc must return to the release detail, screen=%d", m.screen)
	}
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(t, mi)
	if m.helmResSel != 1 {
		t.Fatalf("↓ must move the resource selection, got %d", m.helmResSel)
	}
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if def := xansi.Strip(m.helmRes.View()); !strings.Contains(def, "port: 8080") {
		t.Fatalf("the Service definition should show:\n%s", def)
	}

	// 'y' asks the cluster for the live object (the type is discoverable).
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = asModel(t, mi)
	if m.screen != screenHelmRes || cmd == nil {
		t.Fatalf("'y' must open the live view and fetch it (screen=%d cmd=%v)", m.screen, cmd != nil)
	}
	// A failed fetch is reported, never faked.
	mi, _ = m.Update(helmResourceMsg{label: "demo/Service/back", err: errors.New("forbidden")})
	m = asModel(t, mi)
	if !strings.Contains(xansi.Strip(m.helmRes.View()), "forbidden") {
		t.Fatalf("a fetch error must be shown:\n%s", xansi.Strip(m.helmRes.View()))
	}
}

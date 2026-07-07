package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
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

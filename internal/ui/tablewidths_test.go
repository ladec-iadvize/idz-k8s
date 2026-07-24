package ui

// Content-driven layout (v3): long content gets the room it needs on a wide
// terminal, columns shrink proportionally (never below readable minimums) on
// a narrow one, and no rendered line ever exceeds the terminal width.

import (
	"strings"
	"testing"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestFitColumns(t *testing.T) {
	// Everything fits → every column gets exactly what it needs.
	needs, mins := []int{10, 30, 8}, []int{4, 8, 4}
	if got := fitColumns(needs, mins, 60); got[0] != 10 || got[1] != 30 || got[2] != 8 {
		t.Fatalf("fit: %v", got)
	}
	// Deficit → proportional shrink, total exactly avail, nothing below min.
	got := fitColumns(needs, mins, 40)
	sum := 0
	for i, w := range got {
		if w < mins[i] {
			t.Fatalf("column %d shrank below its minimum: %v", i, got)
		}
		sum += w
	}
	if sum != 40 {
		t.Fatalf("shrunk widths must use exactly the available space, got %d: %v", sum, got)
	}
	if got[1] >= 30 || got[1] <= got[0] {
		t.Fatalf("the widest column must give up the most: %v", got)
	}
	// Tighter than the minimums → the minimums (frame truncation handles it).
	got = fitColumns(needs, mins, 10)
	if got[0] != 4 || got[1] != 8 || got[2] != 4 {
		t.Fatalf("min floor: %v", got)
	}
}

func fluidPodModel(t *testing.T, width int, node string) Model {
	t.Helper()
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = width, 24
	m.layout()
	m.objects = []model.ResourceObject{{
		Type: pods, Namespace: "demo", Name: "web-1",
		Raw: map[string]any{
			"metadata": map[string]any{"name": "web-1", "namespace": "demo"},
			"spec":     map[string]any{"nodeName": node},
			"status":   map[string]any{"phase": "Running", "podIP": "10.0.0.7"},
		},
	}}
	m.applyRows()
	return m
}

// TestFluidListShowsLongContentOnWideTerminal: the NODE column used to be
// hard-capped at 24 runes — on a wide terminal the full node name must show.
func TestFluidListShowsLongContentOnWideTerminal(t *testing.T) {
	node := "ip-10-123-45-67.eu-west-1.compute.internal" // 43 runes > the old 24
	m := fluidPodModel(t, 200, node)
	view := m.listView()
	if !strings.Contains(view, node) {
		t.Fatalf("full node name must be visible on a 200-col terminal:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if w := len([]rune(xansi.Strip(line))); w > 200 {
			t.Fatalf("line exceeds the terminal width (%d): %q", w, line)
		}
	}
}

// TestFluidListShrinksOnNarrowTerminal: under pressure every line still fits
// and no column collapses below its readable minimum.
func TestFluidListShrinksOnNarrowTerminal(t *testing.T) {
	m := fluidPodModel(t, 60, "ip-10-123-45-67.eu-west-1.compute.internal")
	widths := m.listWidths(m.columnsForType())
	total := 0
	for i, w := range widths {
		if i > 0 && w < 4 {
			t.Fatalf("column %d below the readable minimum: %v", i, widths)
		}
		total += w + 1
	}
	if total-1 > 60 {
		t.Fatalf("columns+gaps exceed the 60-col terminal: %v (total %d)", widths, total-1)
	}
	for _, line := range strings.Split(m.listView(), "\n") {
		if w := len([]rune(xansi.Strip(line))); w > 60 {
			t.Fatalf("line exceeds the terminal width (%d): %q", w, line)
		}
	}
}

// TestFluidListDropsDeadPadding: a short namespace no longer pays the old
// fixed 28-rune column — the column hugs its content.
func TestFluidListDropsDeadPadding(t *testing.T) {
	m := fluidPodModel(t, 200, "node-1")
	widths := m.listWidths(m.columnsForType())
	// widths[1] is NAMESPACE: content "demo" (4) vs title "NAMESPACE" (9).
	if widths[1] != 9 {
		t.Fatalf("NAMESPACE must hug max(title, content)=9, got %d (%v)", widths[1], widths)
	}
}

// TestHelmWidthsContentDriven: the helm table sizes CHART/RELEASE from the
// rows and keeps the widths in sync with the click→column mapping.
func TestHelmWidthsContentDriven(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 160, 30
	m.layout()
	m.helmRows = []model.HelmRelease{{
		Namespace: "audience-back", Name: "back",
		Chart: "a-very-long-chart-name-that-was-previously-capped", ChartVersion: "0.28.1",
		Revision: 12, Status: "deployed",
	}}
	widths := m.helmColWidths()
	if widths[2] < len([]rune("a-very-long-chart-name-that-was-previously-capped")) {
		t.Fatalf("CHART must fit its content on a wide terminal: %v", widths)
	}
	total := 0
	for _, w := range widths {
		total += w
	}
	if total > 160 {
		t.Fatalf("helm widths exceed the terminal: %v", widths)
	}
}

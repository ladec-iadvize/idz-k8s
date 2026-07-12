package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// Geometry is sacred (CLAUDE.md): the usage and sizing tables map header
// clicks to columns with their own width helpers, which had zero coverage —
// a shifted offset would silently sort the wrong column.

func clickAt(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

func TestUsageHeaderClickSortsTheClickedColumn(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 120, 30
	m.layout()
	m.screen = screenTop
	m.usageRows = []model.UsageRow{
		{Namespace: "demo", Name: "web-1", CPU: 0.5, Mem: 100, HasCPU: true, HasMem: true},
		{Namespace: "demo", Name: "web-2", CPU: 0.1, Mem: 300, HasCPU: true, HasMem: true},
	}

	widths := m.usageWidths(m.usageColumns())
	if len(widths) < 2 {
		t.Fatalf("expected at least 2 usage columns, got %d", len(widths))
	}

	// usageColumnAt: first cell of column 0, first cell of column 1, and the
	// separator between them (must map to nothing).
	if col, ok := m.usageColumnAt(0); !ok || col != 0 {
		t.Fatalf("x=0 should be column 0, got col=%d ok=%v", col, ok)
	}
	if col, ok := m.usageColumnAt(widths[0] + 1); !ok || col != 1 {
		t.Fatalf("x=%d should be column 1, got col=%d ok=%v", widths[0]+1, col, ok)
	}
	if _, ok := m.usageColumnAt(widths[0]); ok {
		t.Fatalf("x=%d is the separator, must map to no column", widths[0])
	}

	// End-to-end: a click on the column-1 header (y=2) selects it for sort,
	// a second click flips the direction.
	mi, _ := m.handleMouse(clickAt(widths[0]+1, 2))
	m = asModel(t, mi)
	if m.usageSortCol != 1 || !m.usageSortAsc {
		t.Fatalf("header click should sort column 1 asc, got col=%d asc=%v", m.usageSortCol, m.usageSortAsc)
	}
	mi, _ = m.handleMouse(clickAt(widths[0]+1, 2))
	m = asModel(t, mi)
	if m.usageSortCol != 1 || m.usageSortAsc {
		t.Fatalf("second click should flip to desc, got col=%d asc=%v", m.usageSortCol, m.usageSortAsc)
	}
}

func TestSizingHeaderClickSortsTheClickedColumn(t *testing.T) {
	dep := model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(dep))
	m.width, m.height = 120, 30
	m.layout()
	m.screen = screenSizingList
	// rows and objs are parallel arrays — applySizingSort reorders both.
	m.sizingRows = []model.SizingAdvice{
		{Workload: "Deployment/api", Namespace: "demo", Pods: 3},
		{Workload: "Deployment/web", Namespace: "demo", Pods: 1},
	}
	m.sizingObjs = []model.ResourceObject{
		{Namespace: "demo", Name: "api"},
		{Namespace: "demo", Name: "web"},
	}

	widths := m.sizingWidths(m.sizingColumns())
	if len(widths) < 2 {
		t.Fatalf("expected at least 2 sizing columns, got %d", len(widths))
	}

	if col, ok := m.sizingColumnAt(0); !ok || col != 0 {
		t.Fatalf("x=0 should be column 0, got col=%d ok=%v", col, ok)
	}
	if col, ok := m.sizingColumnAt(widths[0] + 1); !ok || col != 1 {
		t.Fatalf("x=%d should be column 1 (PODS), got col=%d ok=%v", widths[0]+1, col, ok)
	}
	if _, ok := m.sizingColumnAt(widths[0]); ok {
		t.Fatalf("x=%d is the separator, must map to no column", widths[0])
	}

	mi, _ := m.handleMouse(clickAt(widths[0]+1, 2))
	m = asModel(t, mi)
	if m.sizingSortCol != 1 || !m.sizingSortAsc {
		t.Fatalf("header click should sort column 1 asc, got col=%d asc=%v", m.sizingSortCol, m.sizingSortAsc)
	}
	// Sorting by PODS asc must actually reorder the rows.
	if m.sizingRows[0].Pods != 1 {
		t.Fatalf("rows not sorted by PODS asc: %+v", m.sizingRows)
	}
	mi, _ = m.handleMouse(clickAt(widths[0]+1, 2))
	m = asModel(t, mi)
	if m.sizingSortCol != 1 || m.sizingSortAsc {
		t.Fatalf("second click should flip to desc, got col=%d asc=%v", m.sizingSortCol, m.sizingSortAsc)
	}
}

package ui

// Usage / top view (Prometheus-backed): table machinery, filtering,
// sorting and rendering; openTop enters the view.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
	"github.com/iadvize/idz-k8s/internal/ui/components"
)

func (m Model) usagePanel() string {
	w := 20
	return "Usage (last 1h):\n" +
		"  " + components.UsageLine("CPU", m.detailCPU, w) + "\n" +
		"  " + components.UsageLine("MEM", m.detailMem, w) + "\n"
}

func (m *Model) openTop() (tea.Model, tea.Cmd) {
	m.screen = screenTop
	m.usageRows, m.usageAllRows = nil, nil
	m.usageWin.SetRows(nil)
	if !m.metrics.Enabled() {
		m.errMsg = "usage: metrics unavailable (no Prometheus reachable — see --prometheus-url)"
		m.screen = screenList
		return m, nil
	}
	m.usageTypeKey = "v1/pods"
	if !strings.EqualFold(m.curType.Kind, "Pod") {
		m.usageTypeKey = m.curType.Key()
	}
	m.statusMsg = "observing usage…"
	m.layout()
	// Marked pods scope the pods view (consistency with f/v/z).
	markedKeys := map[string]bool{}
	if strings.EqualFold(m.curType.Kind, "Pod") {
		for k := range m.marked {
			markedKeys[k] = true
		}
	}
	// Deployments aggregate their pods' usage; the pods view lists them raw.
	var workloads []model.ResourceObject
	if !strings.EqualFold(m.curType.Kind, "Pod") {
		src := m.rowObjs
		if len(m.marked) > 0 {
			src = make([]model.ResourceObject, 0, len(m.marked))
			for _, o := range m.marked {
				src = append(src, o)
			}
		}
		for _, o := range src {
			if _, ok := kube.PodSelectorLabels(o.Raw); ok {
				workloads = append(workloads, o)
			}
		}
	}
	return m, m.fetchUsage(workloads, markedKeys)
}

// usageColumn is one column of the usage table ('u').
type usageColumn = houseColumn[model.UsageRow]

// usageColumns defines the table: CPU and memory side by side — no metric
// toggle (consistency, owner 2026-07-09). Gauges are relative to the top
// consumer of each column.
func (m *Model) usageColumns() []usageColumn {
	var maxCPU, maxMem float64
	for _, r := range m.usageAllRows {
		if r.CPU > maxCPU {
			maxCPU = r.CPU
		}
		if r.Mem > maxMem {
			maxMem = r.Mem
		}
	}
	relGauge := func(v float64, has bool, max float64) string {
		if !has || max <= 0 {
			return m.theme.Faint.Render(strings.Repeat("·", 12))
		}
		return m.coloredGauge(v/max, 12)
	}
	valOrDash := func(v float64, has bool, format func(float64) string) string {
		if !has {
			return "—"
		}
		return format(v)
	}
	cols := []usageColumn{
		{title: "NAME",
			cell: func(m *Model, r model.UsageRow) string {
				if m.client != nil && (m.client.Namespace == "" || kube.IsNamespacePattern(m.client.Namespace)) {
					return r.Namespace + "/" + r.Name
				}
				return r.Name
			},
			less: func(a, b model.UsageRow) bool { return a.Namespace+a.Name < b.Namespace+b.Name }},
	}
	if m.usageIsAgg {
		cols = append(cols, usageColumn{title: "PODS", right: true,
			cell: func(_ *Model, r model.UsageRow) string { return fmt.Sprintf("%d", r.Pods) },
			less: func(a, b model.UsageRow) bool { return a.Pods < b.Pods }})
	}
	cols = append(cols,
		usageColumn{title: "CPU", right: true,
			cell: func(_ *Model, r model.UsageRow) string { return valOrDash(r.CPU, r.HasCPU, components.FormatCPU) },
			less: func(a, b model.UsageRow) bool { return a.CPU < b.CPU }},
		usageColumn{title: "",
			cell: func(m *Model, r model.UsageRow) string { return relGauge(r.CPU, r.HasCPU, maxCPU) },
			less: func(a, b model.UsageRow) bool { return a.CPU < b.CPU }},
		usageColumn{title: "MEMORY", right: true,
			cell: func(_ *Model, r model.UsageRow) string { return valOrDash(r.Mem, r.HasMem, components.FormatMemory) },
			less: func(a, b model.UsageRow) bool { return a.Mem < b.Mem }},
		usageColumn{title: "",
			cell: func(m *Model, r model.UsageRow) string { return relGauge(r.Mem, r.HasMem, maxMem) },
			less: func(a, b model.UsageRow) bool { return a.Mem < b.Mem }},
	)
	return cols
}

// applyUsageFilter rebuilds the visible rows (name/namespace substring),
// then re-applies the sort.
func (m *Model) applyUsageFilter() {
	q := strings.ToLower(strings.TrimSpace(m.usageFilterQ))
	rows := make([]model.UsageRow, 0, len(m.usageAllRows))
	for _, r := range m.usageAllRows {
		if q != "" && !strings.Contains(strings.ToLower(r.Namespace+"/"+r.Name), q) {
			continue
		}
		rows = append(rows, r)
	}
	m.usageRows = rows
	m.usageWin.SetRows(make([]table.Row, len(rows)))
	m.applyUsageSort()
}

// applyUsageSort: selected column order, or CPU-descending by default (the
// hottest consumers first).
func (m *Model) applyUsageSort() {
	cols := m.usageColumns()
	less := func(a, b model.UsageRow) bool { return a.CPU > b.CPU }
	if m.usageSortCol >= 0 && m.usageSortCol < len(cols) {
		l := cols[m.usageSortCol].less
		if m.usageSortAsc {
			less = l
		} else {
			less = func(a, b model.UsageRow) bool { return l(b, a) }
		}
	}
	sort.SliceStable(m.usageRows, func(i, j int) bool { return less(m.usageRows[i], m.usageRows[j]) })
	m.usageWin.Home()
}

// usageWidths / usageColumnAt delegate to the house table geometry helpers.
func (m *Model) usageWidths(cols []usageColumn) []int {
	return houseWidths(m, cols, m.usageRows, m.usageSortCol)
}

func (m *Model) usageColumnAt(x int) (int, bool) {
	return houseColumnAt(m.usageWidths(m.usageColumns()), x)
}

// usageListView renders the table in the house style.
func (m Model) usageListView() string {
	cols := m.usageColumns()
	return houseTableView(&m, cols, m.usageWidths(cols), m.usageRows,
		&m.usageWin, m.usageSortCol, m.usageSortAsc,
		" no data — Prometheus unreachable or no samples (nothing is estimated)")
}

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
type usageColumn struct {
	title string
	width int // 0 = flexible
	right bool
	cell  func(m *Model, r model.UsageRow) string
	less  func(a, b model.UsageRow) bool
}

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
		{title: "NAME", width: 0,
			cell: func(m *Model, r model.UsageRow) string {
				if m.client != nil && (m.client.Namespace == "" || kube.IsNamespacePattern(m.client.Namespace)) {
					return r.Namespace + "/" + r.Name
				}
				return r.Name
			},
			less: func(a, b model.UsageRow) bool { return a.Namespace+a.Name < b.Namespace+b.Name }},
	}
	if m.usageIsAgg {
		cols = append(cols, usageColumn{title: "PODS", width: 4, right: true,
			cell: func(_ *Model, r model.UsageRow) string { return fmt.Sprintf("%d", r.Pods) },
			less: func(a, b model.UsageRow) bool { return a.Pods < b.Pods }})
	}
	cols = append(cols,
		usageColumn{title: "CPU", width: 9, right: true,
			cell: func(_ *Model, r model.UsageRow) string { return valOrDash(r.CPU, r.HasCPU, components.FormatCPU) },
			less: func(a, b model.UsageRow) bool { return a.CPU < b.CPU }},
		usageColumn{title: "", width: 14,
			cell: func(m *Model, r model.UsageRow) string { return relGauge(r.CPU, r.HasCPU, maxCPU) },
			less: func(a, b model.UsageRow) bool { return a.CPU < b.CPU }},
		usageColumn{title: "MEMORY", width: 9, right: true,
			cell: func(_ *Model, r model.UsageRow) string { return valOrDash(r.Mem, r.HasMem, components.FormatMemory) },
			less: func(a, b model.UsageRow) bool { return a.Mem < b.Mem }},
		usageColumn{title: "", width: 14,
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

// usageWidths / usageColumnAt mirror the sizing table geometry helpers.
func (m *Model) usageWidths(cols []usageColumn) []int {
	widths := make([]int, len(cols))
	fixed := 0
	flexIdx := -1
	for i, c := range cols {
		w := c.width
		if w == 0 {
			flexIdx, w = i, 20
		}
		widths[i] = w
		fixed += w
	}
	if flexIdx >= 0 {
		if extra := m.width - fixed - len(cols); extra > 0 {
			content := len([]rune(cols[flexIdx].title))
			for _, r := range m.usageRows {
				if l := len([]rune(cols[flexIdx].cell(m, r))); l > content {
					content = l
				}
			}
			if need := content + 2 - widths[flexIdx]; need < extra {
				if need < 0 {
					need = 0
				}
				extra = need
			}
			widths[flexIdx] += extra
		}
	}
	return widths
}

func (m *Model) usageColumnAt(x int) (int, bool) {
	widths := m.usageWidths(m.usageColumns())
	pos := 0
	for i, w := range widths {
		if x >= pos && x < pos+w {
			return i, true
		}
		pos += w + 1
	}
	return 0, false
}

// usageListView renders the table in the house style.
func (m Model) usageListView() string {
	cols := m.usageColumns()
	widths := m.usageWidths(cols)
	var b strings.Builder
	head := ""
	for i, c := range cols {
		title := c.title
		if m.usageSortCol == i {
			if m.usageSortAsc {
				title += " ↑"
			} else {
				title += " ↓"
			}
		}
		cell := padTo(title, widths[i])
		if c.right {
			cell = padLeft(title, widths[i])
		}
		head += cell + " "
	}
	b.WriteString(m.theme.TableHeader.Render(padTo(head, m.width)))
	b.WriteString("\n")
	if len(m.usageRows) == 0 {
		b.WriteString(m.theme.Faint.Render(" no data — Prometheus unreachable or no samples (nothing is estimated)"))
		b.WriteString("\n")
	}
	from := m.usageWin.win
	to := from + m.usageWin.height
	if to > len(m.usageRows) {
		to = len(m.usageRows)
	}
	for i := from; i < to; i++ {
		r := m.usageRows[i]
		line := ""
		for j, c := range cols {
			raw := c.cell(&m, r)
			var cell string
			switch {
			case c.right:
				cell = padLeft(raw, widths[j])
			case strings.Contains(raw, "\x1b"):
				cell = padTo2(raw, widths[j])
			default:
				cell = padTo(raw, widths[j])
			}
			line += cell + " "
		}
		if i == m.usageWin.cursor {
			line = m.theme.TableSelected.Render(padTo2(line, m.width))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := to - from; i < m.usageWin.height; i++ {
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

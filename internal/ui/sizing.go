package ui

// Sizing recommendations (US6, FR-023 — advisory):
// overview table machinery, per-workload detail and rendering.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
	"github.com/iadvize/idz-k8s/internal/ui/components"
)

// openSizing ('z'): on a pods list, the selected pod's detail panel; on a
// workload list, the overview TABLE of every visible workload — Enter on a
// row then opens its detail panel.
func (m *Model) openSizing() (tea.Model, tea.Cmd) {
	if strings.EqualFold(m.curType.Kind, "Pod") {
		obj, found := m.selectedObject()
		if !found {
			m.statusMsg = "sizing: select a pod first"
			return m, nil
		}
		if !m.metrics.Enabled() {
			return m.openSizingUnavailable()
		}
		return m.openSizingFor(obj, "", screenList)
	}
	// Workloads with a pod selector, scoped like 'f'/'v': the marked set if
	// any, otherwise everything currently visible (filter applied).
	src := m.rowObjs
	if len(m.marked) > 0 {
		src = make([]model.ResourceObject, 0, len(m.marked))
		for _, o := range m.marked {
			src = append(src, o)
		}
	}
	var workloads []model.ResourceObject
	for _, o := range src {
		if _, ok := kube.PodSelectorLabels(o.Raw); ok {
			workloads = append(workloads, o)
		}
	}
	if len(workloads) == 0 {
		m.statusMsg = "sizing: no workload with pods here — open it on deployments, statefulsets…"
		return m, nil
	}
	if !m.metrics.Enabled() {
		return m.openSizingUnavailable()
	}
	m.screen = screenSizingList
	m.sizingRows, m.sizingObjs = nil, nil
	m.sizingWin.SetRows(nil)
	m.statusMsg = fmt.Sprintf("observing %d workload(s) over the last hour…", len(workloads))
	m.layout()
	return m, m.fetchSizingList(workloads)
}

// openSizingUnavailable states the explicit no-metrics case (FR-023/SC-013):
// without observed data there is NO recommendation — never an estimate.
func (m *Model) openSizingUnavailable() (tea.Model, tea.Cmd) {
	m.screen = screenSizing
	m.sizingFrom = screenList
	m.setContent(screenSizing, m.rule("Sizing (advisory)")+"\n\n"+
		"No recommendation: metrics are unavailable (no Prometheus reachable\n"+
		"for this context). Sizing is derived only from observed usage —\n"+
		"nothing is ever estimated. Use --prometheus-url to force a source.")
	m.layout()
	return m, nil
}

// openSizingFor opens the detail panel for one workload or pod.
func (m *Model) openSizingFor(obj model.ResourceObject, selector string, from screen) (tea.Model, tea.Cmd) {
	m.screen = screenSizing
	m.sizingFrom = from
	m.setContent(screenSizing, "observing "+m.curType.Kind+"/"+obj.Name+" over the last hour…")
	m.layout()
	return m, m.fetchSizing(obj, selector)
}

// openSizingDetail opens the detail panel from an overview row.
func (m Model) openSizingDetail(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.sizingObjs) {
		return m, nil
	}
	obj := m.sizingObjs[i]
	sel, _ := kube.PodSelector(obj.Raw)
	return m.openSizingFor(obj, sel, screenSizingList)
}

// fetchSizingList feeds the overview: ONE pods list + FOUR Prometheus
// queries (per-pod avg/peak × cpu/mem over the window), then client-side
// selector matching and verdicts — the cost does not grow with the number
// of workloads.
func (m Model) fetchSizingList(workloads []model.ResourceObject) tea.Cmd {
	cl, mc, ns, kind := m.client, m.metrics, m.client.Namespace, m.curType.Kind
	exactNS := exactNamespace(ns) // pattern → whole cluster; workloads are already scope-filtered
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pods, err := cl.List(ctx, podResourceType, ns)
		if err != nil {
			return sizingListMsg{err: err}
		}
		w := metrics.TrendWindow
		avgCPU := topConsumersToMap(mc.TopN(ctx, metrics.ScopeAvgByPod(exactNS, model.MetricCPU, w), model.MetricCPU))
		peakCPU := topConsumersToMap(mc.TopN(ctx, metrics.ScopePeakByPod(exactNS, model.MetricCPU, w), model.MetricCPU))
		avgMem := topConsumersToMap(mc.TopN(ctx, metrics.ScopeAvgByPod(exactNS, model.MetricMemory, w), model.MetricMemory))
		peakMem := topConsumersToMap(mc.TopN(ctx, metrics.ScopePeakByPod(exactNS, model.MetricMemory, w), model.MetricMemory))

		rows := make([]model.SizingAdvice, 0, len(workloads))
		objs := make([]model.ResourceObject, 0, len(workloads))
		for _, wl := range workloads {
			sel, ok := kube.PodSelectorLabels(wl.Raw)
			if !ok {
				continue
			}
			cpu := model.ResourceSizing{Kind: model.MetricCPU}
			mem := model.ResourceSizing{Kind: model.MetricMemory}
			var nPods, nCPU, nMem float64
			for _, p := range pods {
				if p.Namespace != wl.Namespace || !kube.LabelsMatch(p.Raw, sel) {
					continue
				}
				nPods++
				cr, cli, mr, ml := kube.PodResources(p.Raw)
				cpu.Request += cr
				cpu.Limit += cli
				mem.Request += mr
				mem.Limit += ml
				key := p.Namespace + "/" + p.Name
				if a, ok1 := avgCPU[key]; ok1 {
					if pk, ok2 := peakCPU[key]; ok2 {
						cpu.Avg += a
						if pk > cpu.Peak {
							cpu.Peak = pk
						}
						nCPU++
					}
				}
				if a, ok1 := avgMem[key]; ok1 {
					if pk, ok2 := peakMem[key]; ok2 {
						mem.Avg += a
						if pk > mem.Peak {
							mem.Peak = pk
						}
						nMem++
					}
				}
			}
			if nPods > 0 {
				cpu.Request, cpu.Limit = cpu.Request/nPods, cpu.Limit/nPods
				mem.Request, mem.Limit = mem.Request/nPods, mem.Limit/nPods
			}
			if nCPU > 0 {
				cpu.Avg, cpu.HasData = cpu.Avg/nCPU, true
			}
			if nMem > 0 {
				mem.Avg, mem.HasData = mem.Avg/nMem, true
			}
			rows = append(rows, model.SizingAdvice{
				Workload:  kind + "/" + wl.Name,
				Namespace: wl.Namespace,
				Pods:      int(nPods),
				CPU:       model.EvaluateSizing(cpu),
				Memory:    model.EvaluateSizing(mem),
			})
			objs = append(objs, wl)
		}
		// Worst first: what needs attention is visible without scrolling.
		// One permutation applied to both parallel slices.
		idx := make([]int, len(rows))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool {
			return adviceSeverity(rows[idx[a]]) > adviceSeverity(rows[idx[b]])
		})
		sortedRows := make([]model.SizingAdvice, len(rows))
		sortedObjs := make([]model.ResourceObject, len(objs))
		for to, from := range idx {
			sortedRows[to], sortedObjs[to] = rows[from], objs[from]
		}
		return sizingListMsg{rows: sortedRows, objs: sortedObjs}
	}
}

// adviceSeverity ranks a row by its worst verdict (under > over > no-request
// > ok > no-data).
func adviceSeverity(a model.SizingAdvice) int {
	rank := func(v model.SizingVerdict) int {
		switch v {
		case model.SizingUnder:
			return 4
		case model.SizingOver:
			return 3
		case model.SizingNoRequest:
			return 2
		case model.SizingOK:
			return 1
		default:
			return 0
		}
	}
	r1, r2 := rank(a.CPU.Verdict), rank(a.Memory.Verdict)
	if r2 > r1 {
		return r2
	}
	return r1
}

// fetchSizing resolves the pods, their configured requests/limits (cluster),
// and their observed usage (Prometheus), then evaluates the verdicts.
func (m Model) fetchSizing(obj model.ResourceObject, selector string) tea.Cmd {
	cl, mc, kind := m.client, m.metrics, m.curType.Kind
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pods := []model.ResourceObject{obj}
		if !strings.EqualFold(kind, "Pod") {
			var err error
			pods, err = cl.ListSelected(ctx, podResourceType, obj.Namespace, selector)
			if err != nil {
				return sizingMsg{err: err}
			}
		}
		adv := model.SizingAdvice{Workload: kind + "/" + obj.Name, Namespace: obj.Namespace, Pods: len(pods)}
		names := make([]string, 0, len(pods))
		var cpuReq, cpuLim, memReq, memLim float64
		for _, p := range pods {
			names = append(names, p.Name)
			cr, cli, mr, ml := kube.PodResources(p.Raw)
			cpuReq += cr
			cpuLim += cli
			memReq += mr
			memLim += ml
		}
		if n := float64(len(pods)); n > 0 {
			// Per-pod configuration (pods of one workload share a template).
			cpuReq, cpuLim, memReq, memLim = cpuReq/n, cpuLim/n, memReq/n, memLim/n
		}
		cpu, mem := mc.WorkloadSizing(ctx, obj.Namespace, names, metrics.TrendWindow)
		cpu.Request, cpu.Limit = cpuReq, cpuLim
		mem.Request, mem.Limit = memReq, memLim
		adv.CPU, adv.Memory = model.EvaluateSizing(cpu), model.EvaluateSizing(mem)
		return sizingMsg{advice: adv}
	}
}

// verdictBadge renders a compact colored verdict cell for the overview.
func (m Model) verdictBadge(v model.SizingVerdict) string {
	switch v {
	case model.SizingUnder:
		return m.theme.Error.Render("✗ under")
	case model.SizingOver:
		return m.theme.Warning.Render("! over")
	case model.SizingNoRequest:
		return m.theme.Warning.Render("! no req")
	case model.SizingOK:
		return m.theme.Ok.Render("✓ ok")
	default:
		return m.theme.Faint.Render("· no data")
	}
}

// sizingGauge shows the observed peak against the request (or the limit when
// no request is set) — the one-glance "is it sized right" visual.
func (m Model) sizingGauge(rs model.ResourceSizing, width int) string {
	den := rs.Request
	if den <= 0 {
		den = rs.Limit
	}
	if !rs.HasData || den <= 0 {
		return m.theme.Faint.Render(strings.Repeat("·", width))
	}
	return m.coloredGauge(rs.Peak/den, width)
}

// sizingColumn is one column of the sizing overview table (cells may carry
// ANSI styling; WORKLOAD is the flex column).
type sizingColumn = houseColumn[model.SizingAdvice]

// sizingColumns defines the overview: separate, aligned AVG/REQ columns and a
// titled STATUS per resource — every column is sortable ('s'/'S' or a header
// click; default order is severity, worst first).
func (m *Model) sizingColumns() []sizingColumn {
	sevRank := func(v model.SizingVerdict) int {
		return adviceSeverity(model.SizingAdvice{CPU: model.ResourceSizing{Verdict: v}})
	}
	util := func(rs model.ResourceSizing) float64 {
		den := rs.Request
		if den <= 0 {
			den = rs.Limit
		}
		if !rs.HasData || den <= 0 {
			return -1 // unknown sorts last in asc
		}
		return rs.Peak / den
	}
	avgOrDash := func(rs model.ResourceSizing, format func(float64) string) string {
		if !rs.HasData {
			return "—"
		}
		return format(rs.Avg)
	}
	reqOrDash := func(rs model.ResourceSizing, format func(float64) string) string {
		if rs.Request <= 0 {
			return "—"
		}
		return format(rs.Request)
	}
	return []sizingColumn{
		{title: "WORKLOAD",
			cell: func(m *Model, a model.SizingAdvice) string {
				if m.client != nil && (m.client.Namespace == "" || kube.IsNamespacePattern(m.client.Namespace)) {
					return a.Namespace + "/" + strings.TrimPrefix(a.Workload, m.curType.Kind+"/")
				}
				return a.Workload
			},
			less: func(a, b model.SizingAdvice) bool {
				return a.Namespace+a.Workload < b.Namespace+b.Workload
			}},
		{title: "PODS", right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return fmt.Sprintf("%d", a.Pods) },
			less: func(a, b model.SizingAdvice) bool { return a.Pods < b.Pods }},
		{title: "CPU",
			cell: func(m *Model, a model.SizingAdvice) string { return m.sizingGauge(a.CPU, 10) },
			less: func(a, b model.SizingAdvice) bool { return util(a.CPU) < util(b.CPU) }},
		{title: "AVG", right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return avgOrDash(a.CPU, components.FormatCPU) },
			less: func(a, b model.SizingAdvice) bool { return a.CPU.Avg < b.CPU.Avg }},
		{title: "REQ", right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return reqOrDash(a.CPU, components.FormatCPU) },
			less: func(a, b model.SizingAdvice) bool { return a.CPU.Request < b.CPU.Request }},
		{title: "STATUS",
			cell: func(m *Model, a model.SizingAdvice) string { return m.verdictBadge(a.CPU.Verdict) },
			less: func(a, b model.SizingAdvice) bool { return sevRank(a.CPU.Verdict) < sevRank(b.CPU.Verdict) }},
		{title: "MEMORY",
			cell: func(m *Model, a model.SizingAdvice) string { return m.sizingGauge(a.Memory, 10) },
			less: func(a, b model.SizingAdvice) bool { return util(a.Memory) < util(b.Memory) }},
		{title: "AVG", right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return avgOrDash(a.Memory, components.FormatMemory) },
			less: func(a, b model.SizingAdvice) bool { return a.Memory.Avg < b.Memory.Avg }},
		{title: "REQ", right: true,
			cell: func(_ *Model, a model.SizingAdvice) string { return reqOrDash(a.Memory, components.FormatMemory) },
			less: func(a, b model.SizingAdvice) bool { return a.Memory.Request < b.Memory.Request }},
		{title: "STATUS",
			cell: func(m *Model, a model.SizingAdvice) string { return m.verdictBadge(a.Memory.Verdict) },
			less: func(a, b model.SizingAdvice) bool { return sevRank(a.Memory.Verdict) < sevRank(b.Memory.Verdict) }},
	}
}

// sizingWidths resolves the overview widths (WORKLOAD absorbs the rest).
func (m *Model) sizingWidths(cols []sizingColumn) []int {
	return houseWidths(m, cols, m.sizingRows, m.sizingSortCol)
}

// sizingColumnAt maps a header click to a column index.
func (m *Model) sizingColumnAt(x int) (int, bool) {
	return houseColumnAt(m.sizingWidths(m.sizingColumns()), x)
}

// applySizingFilter rebuilds the visible overview rows from the master set
// (workload name/namespace substring), then re-applies the sort.
func (m *Model) applySizingFilter() {
	q := strings.ToLower(strings.TrimSpace(m.sizingQuery))
	rows := make([]model.SizingAdvice, 0, len(m.sizingAllRows))
	objs := make([]model.ResourceObject, 0, len(m.sizingAllObjs))
	for i, r := range m.sizingAllRows {
		if q != "" && !strings.Contains(strings.ToLower(r.Namespace+"/"+r.Workload), q) {
			continue
		}
		rows = append(rows, r)
		objs = append(objs, m.sizingAllObjs[i])
	}
	m.sizingRows, m.sizingObjs = rows, objs
	m.sizingWin.SetRows(make([]table.Row, len(rows)))
	m.applySizingSort()
}

// applySizingSort reorders rows and their objects together: the selected
// column's order, or severity worst-first when no column is selected.
func (m *Model) applySizingSort() {
	cols := m.sizingColumns()
	idx := make([]int, len(m.sizingRows))
	for i := range idx {
		idx[i] = i
	}
	less := func(a, b int) bool {
		return adviceSeverity(m.sizingRows[a]) > adviceSeverity(m.sizingRows[b])
	}
	if m.sizingSortCol >= 0 && m.sizingSortCol < len(cols) {
		l := cols[m.sizingSortCol].less
		less = func(a, b int) bool {
			if m.sizingSortAsc {
				return l(m.sizingRows[a], m.sizingRows[b])
			}
			return l(m.sizingRows[b], m.sizingRows[a])
		}
	}
	sort.SliceStable(idx, func(a, b int) bool { return less(idx[a], idx[b]) })
	rows := make([]model.SizingAdvice, len(idx))
	objs := make([]model.ResourceObject, len(idx))
	for to, from := range idx {
		rows[to], objs[to] = m.sizingRows[from], m.sizingObjs[from]
	}
	m.sizingRows, m.sizingObjs = rows, objs
	m.sizingWin.Home()
}

// sizingListView renders the overview with real, aligned columns.
func (m Model) sizingListView() string {
	cols := m.sizingColumns()
	return houseTableView(&m, cols, m.sizingWidths(cols), m.sizingRows,
		&m.sizingWin, m.sizingSortCol, m.sizingSortAsc,
		" observing… (advisory — nothing is applied automatically)")
}

// renderSizing renders the advisory view: observed data first, verdict after —
// a recommendation is never shown without the numbers behind it (FR-023).
func (m *Model) renderSizing(a model.SizingAdvice) {
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Sizing (advisory) — %s in %s, %d pod(s), last 1h", a.Workload, a.Namespace, a.Pods)) + "\n\n")
	b.WriteString(m.theme.Faint.Render("Advisory only, based on observed usage per pod — this tool never applies changes.") + "\n\n")
	m.renderResourceSizing(&b, "CPU", a.CPU, components.FormatCPU)
	b.WriteString("\n")
	m.renderResourceSizing(&b, "MEMORY", a.Memory, components.FormatMemory)
	m.setContent(screenSizing, b.String())
	m.layout()
}

func (m *Model) renderResourceSizing(b *strings.Builder, label string, rs model.ResourceSizing, format func(float64) string) {
	b.WriteString(m.rule(label) + "\n")
	orUnset := func(v float64) string {
		if v <= 0 {
			return "—"
		}
		return format(v)
	}
	fmt.Fprintf(b, "  configured  request %s   limit %s   (per pod)\n", orUnset(rs.Request), orUnset(rs.Limit))
	// Reference for the gauges: the request (what sizing is about), else the
	// limit. Colored bars make over/under readable at a glance (FR-036).
	den, ref := rs.Request, "request"
	if den <= 0 {
		den, ref = rs.Limit, "limit"
	}
	switch {
	case !rs.HasData:
		b.WriteString("  observed    no data for this window\n")
	case den > 0:
		fmt.Fprintf(b, "  avg   %s %s   %.0f%% of %s\n", m.coloredGauge(rs.Avg/den, 16), padTo(format(rs.Avg), 9), rs.Avg/den*100, ref)
		fmt.Fprintf(b, "  peak  %s %s   %.0f%% of %s\n", m.coloredGauge(rs.Peak/den, 16), padTo(format(rs.Peak), 9), rs.Peak/den*100, ref)
	default:
		fmt.Fprintf(b, "  observed    avg %s   peak %s   (nothing configured to compare against)\n", format(rs.Avg), format(rs.Peak))
	}
	pct := func(v, of float64) string { return fmt.Sprintf("%.0f%%", v/of*100) }
	var verdict string
	switch rs.Verdict {
	case model.SizingNoData:
		verdict = m.theme.Faint.Render("— no recommendation: not enough observed data (nothing is estimated)")
	case model.SizingNoRequest:
		verdict = m.theme.Warning.Render("! no request configured — consider setting one near the observed peak " + format(rs.Peak))
	case model.SizingUnder:
		reason := "average " + format(rs.Avg) + " ≥ request " + format(rs.Request)
		if rs.Limit > 0 && rs.Peak >= model.SizingLimitFrac*rs.Limit {
			reason = "peak " + format(rs.Peak) + " = " + pct(rs.Peak, rs.Limit) + " of the limit (OOM/throttling risk)"
		}
		verdict = m.theme.Error.Render("✗ under-provisioned / at risk: " + reason)
	case model.SizingOver:
		verdict = m.theme.Warning.Render("! requests appear oversized: peak " + format(rs.Peak) + " = " + pct(rs.Peak, rs.Request) + " of the request")
	default:
		verdict = m.theme.Ok.Render("✓ sized correctly: peak " + format(rs.Peak) + " = " + pct(rs.Peak, rs.Request) + " of the request")
	}
	b.WriteString("  " + verdict + "\n")
}

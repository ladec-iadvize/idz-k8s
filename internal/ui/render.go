package ui

// Pure content renderers: describe/detail text, topology and
// diagnostics views, banner/kikoo art, YAML colorizing and string helpers.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
	"github.com/iadvize/idz-k8s/internal/ui/components"
	"github.com/iadvize/idz-k8s/internal/ui/theme"
)

// describeContent builds a kubectl-describe-like summary from the object plus
// its own events (nil while they load).
func describeContent(obj model.ResourceObject, events []model.Event, th theme.Theme, width int) string {
	if width < 40 {
		width = 100
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", th.Title.Render("Describe: "+obj.Type.Kind+"/"+obj.Name))
	fmt.Fprintf(&b, "Namespace:  %s\n", orDash(obj.Namespace))
	fmt.Fprintf(&b, "Created:    %s (%s ago)\n", obj.CreatedAt.Format(time.RFC3339), kube.Age(obj.CreatedAt, time.Now()))
	fmt.Fprintf(&b, "Status:     %s %s", obj.Status.Level.Symbol(), obj.Status.Level.Label())
	if obj.Status.Reason != "" {
		fmt.Fprintf(&b, " (%s)", obj.Status.Reason)
	}
	b.WriteString("\n")

	if labels, found, _ := unstructured.NestedStringMap(obj.Raw, "metadata", "labels"); found && len(labels) > 0 {
		b.WriteString("\nLabels:\n")
		writeSortedMap(&b, labels)
	}
	if ann, found, _ := unstructured.NestedStringMap(obj.Raw, "metadata", "annotations"); found && len(ann) > 0 {
		b.WriteString("\nAnnotations:\n")
		delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
		writeSortedMap(&b, ann)
	}

	if conds, found, _ := unstructured.NestedSlice(obj.Raw, "status", "conditions"); found && len(conds) > 0 {
		b.WriteString("\nConditions:\n")
		fmt.Fprintf(&b, "  %-28s %-7s %-24s %s\n", "TYPE", "STATUS", "REASON", "MESSAGE")
		for _, c := range conds {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			ctype, _ := cm["type"].(string)
			cstatus, _ := cm["status"].(string)
			reason, _ := cm["reason"].(string)
			message, _ := cm["message"].(string)
			// Full message, hard-wrapped under the MESSAGE column (owner bug
			// 2026-07-07: truncated messages hid the actual diagnosis).
			const msgCol = 64 // 2+28+1+7+1+24+1
			avail := width - msgCol - 1
			if avail < 24 {
				fmt.Fprintf(&b, "  %-28s %-7s %-24s\n", ctype, cstatus, orDash(reason))
				for _, l := range wrapTo(orDash(message), width-8) {
					b.WriteString("      " + l + "\n")
				}
				continue
			}
			chunks := wrapTo(orDash(message), avail)
			fmt.Fprintf(&b, "  %-28s %-7s %-24s %s\n", ctype, cstatus, orDash(reason), chunks[0])
			for _, l := range chunks[1:] {
				b.WriteString(strings.Repeat(" ", msgCol) + l + "\n")
			}
		}
	}

	b.WriteString("\nEvents:\n")
	if len(events) == 0 {
		b.WriteString(th.Faint.Render("  none in the current retention window"))
		b.WriteString("\n")
	} else {
		now := time.Now()
		for i, e := range events {
			if i >= 15 {
				break
			}
			cnt := ""
			if e.Count > 1 {
				cnt = fmt.Sprintf(" x%d", e.Count)
			}
			// Full message, wrapped — never truncated (owner bug 2026-07-07).
			full := fmt.Sprintf("  %-5s %s %s%s — %s", kube.Age(e.Time, now), eventBadge(e), e.Reason, cnt, e.Message)
			for j, l := range wrapTo(full, width-4) {
				if j > 0 {
					l = "    " + l
				}
				if e.Warning() {
					b.WriteString(th.Warning.Render(l))
				} else {
					b.WriteString(th.Faint.Render(l))
				}
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeSortedMap(b *strings.Builder, m map[string]string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "  %s: %s\n", k, m[k])
	}
}

// renderDetail composes the detail content: an optional usage panel (pods) plus
// the cleaned object YAML. It does not change scroll position (so a late-arriving
// usage update does not jump the view).
func (m *Model) renderDetail() {
	raw := cleanForDisplay(m.detailObj.Raw)
	if strings.EqualFold(m.curType.Kind, "Secret") && !m.revealSecret {
		raw = maskSecret(raw)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		m.errMsg = err.Error()
		return
	}
	var b strings.Builder
	if m.detailHasUsage {
		b.WriteString(m.usagePanel())
		b.WriteString("\n")
	}
	if strings.EqualFold(m.curType.Kind, "Secret") {
		if m.revealSecret {
			b.WriteString("# secret values REVEALED — press 'x' to mask\n")
		} else {
			b.WriteString("# secret values masked — press 'x' to reveal\n")
		}
	}
	b.WriteString(m.colorizeYAML(string(out)))
	m.setDetailContent(b.String())
}

func (m *Model) openTopology() (tea.Model, tea.Cmd) {
	m.screen = screenTopology
	m.setContent(screenTopology, "loading topology…")
	m.layout()
	return m, m.fetchTopology()
}

func (m *Model) renderTopology(nodes []model.TopologyNode) {
	m.topoNodes = nodes
	if m.topoSel >= len(nodes) {
		m.topoSel = 0
	}
	m.renderTopologyView()
}

// renderTopologyView renders from state (selection changes re-render).
func (m *Model) renderTopologyView() {
	nodes := m.topoNodes
	scope := m.client.Namespace
	if scope == "" {
		scope = "all namespaces"
	}
	// Pod names use the room they need (owner report 2026-07-11): the
	// column grows with the longest ns/name, bounded by the terminal.
	podW := 40
	for _, n := range nodes {
		for _, p := range n.Pods {
			if l := len([]rune(p.Namespace + "/" + p.Name)); l > podW {
				podW = l
			}
		}
	}
	if lim := m.width - 42; podW > lim && lim >= 40 {
		podW = lim
	}
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Topology — %d nodes, pods in %s", countRealNodes(nodes), scope)) + "\n\n")
	m.topoNodeLines = nil
	for ni, n := range nodes {
		// Node header (colored by node health; selectable — Enter drills).
		m.topoNodeLines = append(m.topoNodeLines, strings.Count(b.String(), "\n"))
		header := fmt.Sprintf("%s %s  (%d pods)", n.Status.Symbol(), n.Name, len(n.Pods))
		if ni == m.topoSel {
			b.WriteString(m.theme.TableSelected.Render(padTo2(header+"  — Enter opens its pods", m.width)))
		} else {
			b.WriteString(m.nodeStyle(n.Status).Render(header))
		}
		b.WriteString("\n")

		// Capacity: colored gauges + reserved/allocatable + free.
		if n.AllocCPU > 0 {
			f := n.ReqCPU / n.AllocCPU
			fmt.Fprintf(&b, "    CPU %s %s / %s  free %s\n", m.coloredGauge(f, 12),
				components.FormatCPU(n.ReqCPU), components.FormatCPU(n.AllocCPU), components.FormatCPU(n.AllocCPU-n.ReqCPU))
		}
		if n.AllocMem > 0 {
			f := n.ReqMem / n.AllocMem
			fmt.Fprintf(&b, "    MEM %s %s / %s  free %s\n", m.coloredGauge(f, 12),
				components.FormatMemory(n.ReqMem), components.FormatMemory(n.AllocMem), components.FormatMemory(n.AllocMem-n.ReqMem))
		}

		// Pod table with aligned columns; % in dedicated columns.
		b.WriteString(m.theme.Faint.Render(fmt.Sprintf("      %-*s %9s %5s %10s %5s", podW, "POD", "CPU", "CPU%", "MEM", "MEM%")))
		b.WriteString("\n")
		for _, p := range n.Pods {
			name := truncate(p.Namespace+"/"+p.Name, podW)
			cpuCell, memCell := "-", "-"
			cpuPct, memPct := "", ""
			if p.CPUReq > 0 {
				cpuCell = components.FormatCPU(p.CPUReq)
			}
			if p.MemReq > 0 {
				memCell = components.FormatMemory(p.MemReq)
			}
			if n.AllocCPU > 0 {
				cpuPct = m.pctCell(p.CPUReq / n.AllocCPU)
			}
			if n.AllocMem > 0 {
				memPct = m.pctCell(p.MemReq / n.AllocMem)
			}
			fmt.Fprintf(&b, "    %s %-*s %9s %5s %10s %5s\n",
				p.Status.Symbol(), podW, name, cpuCell, cpuPct, memCell, memPct)
		}
		b.WriteString("\n")
	}
	m.setContent(screenTopology, b.String())
}

func (m Model) nodeStyle(l model.HealthLevel) lipgloss.Style {
	switch l {
	case model.HealthError:
		return m.theme.Error
	case model.HealthWarning:
		return m.theme.Warning
	default:
		return m.theme.Ok
	}
}

// coloredGauge renders a gauge colored by utilization: green <70%, yellow
// <90%, red otherwise — a quick visual of how full the node is.
func (m Model) coloredGauge(frac float64, width int) string {
	g := components.Gauge(frac, width)
	switch {
	case frac >= 0.9:
		return m.theme.Error.Render(g)
	case frac >= 0.7:
		return m.theme.Warning.Render(g)
	default:
		return m.theme.Ok.Render(g)
	}
}

// pctCell renders a right-aligned, threshold-colored percentage (fixed width;
// the ANSI color is zero-width so columns stay aligned in the terminal).
func (m Model) pctCell(frac float64) string {
	s := fmt.Sprintf("%3d%%", int(frac*100+0.5))
	switch {
	case frac >= 0.9:
		return m.theme.Error.Render(s)
	case frac >= 0.7:
		return m.theme.Warning.Render(s)
	default:
		return m.theme.Ok.Render(s)
	}
}

// truncate shortens a plain string to n display cells. It counts RUNES, not
// bytes — multi-byte glyphs (—, ✓, →, ●) must not trigger a spurious cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func countRealNodes(nodes []model.TopologyNode) int {
	n := 0
	for _, nd := range nodes {
		if nd.Reason != "unscheduled" {
			n++
		}
	}
	return n
}

func (m *Model) openDiag() (tea.Model, tea.Cmd) {
	m.screen = screenDiag
	m.setContent(screenDiag, "scanning for failing workloads…")
	m.layout()
	return m, m.fetchDiag()
}

func (m *Model) renderDiag(rows []model.Diagnostic) {
	m.diagAll = rows
	m.diagSel = 0
	m.renderDiagView()
}

// renderDiagView renders from state so selection/filter changes re-render
// without refetching.
func (m *Model) renderDiagView() {
	rows := filterFindings(m.diagAll, m.diagErrOnly,
		func(d model.Diagnostic) model.HealthLevel { return d.Level }, &m.diagSel)
	m.renderDiagContent(rows)
}

func (m *Model) renderDiagContent(rows []model.Diagnostic) {
	scope := m.client.Namespace
	if scope == "" {
		scope = "all namespaces"
	}
	if len(m.marked) > 0 {
		scope = fmt.Sprintf("%d marked", len(m.marked))
	}
	var b strings.Builder
	b.WriteString(m.rule(fmt.Sprintf("Workload failure diagnostics — %s, %d finding(s)", scope, len(rows))) + "\n\n")
	if len(rows) == 0 {
		b.WriteString(m.theme.Ok.Render("✓ no failing workloads detected"))
		m.setContent(screenDiag, b.String())
		return
	}
	// Grouped by failure type (posture-style, owner request 2026-07-07):
	// error-level groups first, then by size.
	byCat := map[string][]model.Diagnostic{}
	var order []string
	for _, d := range rows {
		c := diagCategory(d.Reason)
		if len(byCat[c]) == 0 {
			order = append(order, c)
		}
		byCat[c] = append(byCat[c], d)
	}
	sort.SliceStable(order, func(i, j int) bool {
		wi, wj := diagGroupWeight(byCat[order[i]]), diagGroupWeight(byCat[order[j]])
		if wi != wj {
			return wi > wj
		}
		return len(byCat[order[i]]) > len(byCat[order[j]])
	})
	groups := make([]findingGroup, 0, len(order))
	for _, cat := range order {
		ds := byCat[cat]
		items := make([]findingItem, 0, len(ds))
		for _, d := range ds {
			who := d.Namespace + "/" + d.Pod
			if d.Container != "" {
				who += " [" + d.Container + "]"
			}
			items = append(items, findingItem{level: d.Level, who: who, detail: d.Reason,
				ref: objRef{typeKey: "v1/pods", ns: d.Namespace, name: d.Pod}})
		}
		groups = append(groups, findingGroup{title: cat, items: items})
	}
	m.renderFindingGroups(&b, groups, m.diagSel, 45, &m.diagRefs, &m.diagLines)
	b.WriteString(m.theme.Faint.Render("Enter opens the pod · 'w' errors only · ↑/↓ select"))
	b.WriteString("\n")
	m.setContent(screenDiag, b.String())
	m.keepFindingVisible(&m.diag, m.diagLines, m.diagSel)
}

// diagCategory folds a diagnostic reason to its failure type: "OOMKilled
// (x3 restarts)" → "OOMKilled", "Evicted: node pressure" → "Evicted",
// "restarted x4" → "restarted".
func diagCategory(reason string) string {
	if i := strings.IndexAny(reason, ":("); i > 0 {
		reason = reason[:i]
	}
	fields := strings.Fields(reason)
	for len(fields) > 1 {
		last := fields[len(fields)-1]
		if strings.HasPrefix(last, "x") && len(last) > 1 {
			fields = fields[:len(fields)-1]
			continue
		}
		break
	}
	return strings.Join(fields, " ")
}

// diagGroupWeight ranks a group by its worst severity.
func diagGroupWeight(ds []model.Diagnostic) int {
	w := 0
	for _, d := range ds {
		if int(d.Level) > w {
			w = int(d.Level)
		}
	}
	return w
}

// kikooArt is the --kikoo banner (iAdvize green, centered, truncated to the
// width — banner lines NEVER wrap).
var kikooArt = []string{
	"██╗██████╗ ███████╗      ██╗  ██╗ █████╗ ███████╗",
	"██║██╔══██╗╚══███╔╝      ██║ ██╔╝██╔══██╗██╔════╝",
	"██║██║  ██║  ███╔╝ █████╗█████╔╝ ╚█████╔╝███████╗",
	"██║██║  ██║ ███╔╝  ╚════╝██╔═██╗ ██╔══██╗╚════██║",
	"██║██████╔╝███████╗      ██║  ██╗╚█████╔╝███████║",
	"╚═╝╚═════╝ ╚══════╝      ╚═╝  ╚═╝ ╚════╝ ╚══════╝",
}

// kikooBubble is the actual iAdvize logo: the mint-green disc with the
// smile near the bottom (shown left of the art on wide terminals). Same
// height as kikooArt.
var kikooBubble = []string{
	"    ▄█████▄    ",
	"  ▄█████████▄  ",
	"  ███████████  ",
	"  ██▄ ▀▀▀ ▄██  ",
	"  ▀█████████▀  ",
	"    iAdvize    ",
}

// kikooHelm is the Kubernetes side (right of the art).
var kikooHelm = []string{
	"      │        ",
	"  ╭───┴───╮    ",
	"──┤   ⎈   ├──  ",
	"  ╰───┬───╯    ",
	"      │        ",
	"  ~ ~ ~ ~ ~    ",
}

// kikooPattern fills the remaining flanks with floating conversation
// bubbles (deterministic per row — Date/rand are unavailable by design).
var kikooPattern = []string{
	"  ·    ○     ·   ",
	"     ·    ◌    · ",
	" ○     ·     ·   ",
	"    ·     ◌    · ",
	"  ·   ○      ·   ",
	"      ·    ·   ○ ",
}

// repeatToWidth tiles a pattern to exactly w runes.
func repeatToWidth(pattern string, w int) string {
	if w <= 0 {
		return ""
	}
	r := []rune(strings.Repeat(pattern, w/len([]rune(pattern))+1))
	return string(r[:w])
}

// bannerH returns the banner height: 0 unless kikoo is on AND the terminal
// is comfortably large (the banner never eats a small screen).
func (m Model) bannerH() int {
	if !m.kikoo || m.height < 28 || m.width < 60 {
		return 0
	}
	return len(kikooArt) + 2 // art + tagline + blank
}

// banner renders the kikoo header block (bannerH lines exactly): the
// iAdvize bubble logo, the idz-k8s art and a helm, with a bubble pattern
// filling the flanks on wide terminals.
func (m Model) banner() string {
	artW := len([]rune(kikooArt[0]))
	sideW := len([]rune(kikooBubble[0]))
	wide := m.width >= artW+2*sideW+8

	var b strings.Builder
	for i, l := range kikooArt {
		style := theme.KikooGreen
		if i >= len(kikooArt)-2 {
			style = theme.KikooDarkGreen
		}
		var plain, styled string
		if wide {
			core := kikooBubble[i] + " " + l + " " + kikooHelm[i]
			fill := (m.width - len([]rune(core))) / 2
			left := repeatToWidth(kikooPattern[i%len(kikooPattern)], fill)
			right := repeatToWidth(kikooPattern[(i+3)%len(kikooPattern)], m.width-len([]rune(core))-fill)
			plain = left + core + right
			styled = theme.KikooDarkGreen.Render(left) +
				theme.KikooGreen.Render(kikooBubble[i]) + " " +
				style.Render(l) + " " +
				theme.KikooDarkGreen.Render(kikooHelm[i]) +
				theme.KikooDarkGreen.Render(right)
		} else {
			plain = l
			styled = style.Render(l)
		}
		pad := (m.width - len([]rune(plain))) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(xansi.Truncate(styled, m.width, ""))
		b.WriteString("\n")
	}
	tag := "⎈  the Kubernetes overview & admin TUI — powered by iAdvize 💚"
	pad := (m.width - len([]rune(tag))) / 2
	if pad < 0 {
		pad = 0
	}
	b.WriteString(strings.Repeat(" ", pad))
	b.WriteString(xansi.Truncate(theme.KikooDarkGreen.Render(tag), m.width, ""))
	b.WriteString("\n\n")
	return b.String()
}

// rule renders a full-width thin separator; with a title it becomes a section
// header: "── Title ───────" (title in the table-header color, dashes faint).
func (m Model) rule(title string) string {
	w := m.width
	if w < 10 {
		w = 80
	}
	if title == "" {
		return m.theme.Faint.Render(strings.Repeat("─", w))
	}
	used := 3 + len([]rune(title)) + 1 // "── " + title + " "
	rest := w - used
	if rest < 0 {
		rest = 0
	}
	return m.theme.Faint.Render("── ") + m.theme.TableHeader.Render(title) +
		m.theme.Faint.Render(" "+strings.Repeat("─", rest))
}

// wrapTo hard-wraps text to a display width (rune-counted, word-aware) so a
// viewport line can never auto-wrap in the terminal — auto-wrap shifts every
// line below and breaks the click geometry. Unbreakable tokens are cut.
func wrapTo(s string, w int) []string {
	if w < 10 {
		w = 10
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range words {
			for len([]rune(word)) > w {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				r := []rune(word)
				out = append(out, string(r[:w]))
				word = string(r[w:])
			}
			switch {
			case line == "":
				line = word
			case len([]rune(line))+1+len([]rune(word)) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		out = append(out, line)
	}
	return out
}

// padLeft right-aligns a plain string in a fixed width (rune-counted).
func padLeft(s string, w int) string {
	s = truncate(s, w)
	if pad := w - len([]rune(s)); pad > 0 {
		return strings.Repeat(" ", pad) + s
	}
	return s
}

// padTo2 pads a styled (ANSI) string to a display width without truncating.
func padTo2(s string, w int) string {
	if pad := w - lipgloss.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// colorizeYAML tints YAML for readability: keys in blue, comments faint.
// Purely visual (line-by-line, no parsing) — content is preserved verbatim.
func (m Model) colorizeYAML(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		trimmed := strings.TrimLeft(l, " ")
		indent := l[:len(l)-len(trimmed)]
		switch {
		case strings.HasPrefix(trimmed, "#"):
			lines[i] = indent + m.theme.Faint.Render(trimmed)
		default:
			if c := strings.Index(trimmed, ":"); c > 0 {
				key := trimmed[:c]
				if !strings.ContainsAny(key, " \"'{}[]") {
					lines[i] = indent + m.theme.YamlKey.Render(key) + trimmed[c:]
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

// typeOptionLabel renders a type-picker option with its kubectl-style
// aliases, e.g. "v1/services  (svc)" — typing "svc" then matches it.
func typeOptionLabel(t model.ResourceType) string {
	if len(t.ShortNames) == 0 {
		return t.Key()
	}
	return t.Key() + "  (" + strings.Join(t.ShortNames, ",") + ")"
}

// statusCell renders the STATUS column: the reason when we have one (more
// informative: "Running", "3 eps", "no eps"), the level label otherwise, and a
// neutral "—" for kinds that simply have no status (ConfigMap, ServiceAccount…)
// instead of a misleading "? Unknown".
func statusCell(s model.StatusSummary) string {
	if s.Level == model.HealthUnknown && s.Reason == "" {
		return "—"
	}
	label := s.Reason
	if label == "" {
		label = s.Level.Label()
	}
	return s.Level.Symbol() + " " + label
}

func findTypeByKey(types []model.ResourceType, key string) (model.ResourceType, bool) {
	if key == "" {
		return model.ResourceType{}, false
	}
	for _, t := range types {
		if t.Key() == key {
			return t, true
		}
	}
	return model.ResourceType{}, false
}

func defaultType(types []model.ResourceType) model.ResourceType {
	for _, t := range types {
		if t.Group == "" && t.Resource == "pods" {
			return t
		}
	}
	return types[0]
}

// cleanForDisplay returns a shallow clone of the object with noisy internal
// bookkeeping removed from the detail view: metadata.managedFields (Server-Side
// Apply field ownership, the "f:" tree) and the verbose last-applied annotation.
// This mirrors what kubectl describe / k9s hide by default.
func cleanForDisplay(raw map[string]interface{}) map[string]interface{} {
	clone := make(map[string]interface{}, len(raw))
	for k, v := range raw {
		clone[k] = v
	}
	if meta, ok := clone["metadata"].(map[string]interface{}); ok {
		m2 := make(map[string]interface{}, len(meta))
		for k, v := range meta {
			if k == "managedFields" {
				continue
			}
			m2[k] = v
		}
		if ann, ok := m2["annotations"].(map[string]interface{}); ok {
			a2 := make(map[string]interface{}, len(ann))
			for k, v := range ann {
				if k == "kubectl.kubernetes.io/last-applied-configuration" {
					continue
				}
				a2[k] = v
			}
			m2["annotations"] = a2
		}
		clone["metadata"] = m2
	}
	return clone
}

func maskSecret(raw map[string]interface{}) map[string]interface{} {
	clone := make(map[string]interface{}, len(raw))
	for k, v := range raw {
		clone[k] = v
	}
	if data, ok := clone["data"].(map[string]interface{}); ok {
		masked := make(map[string]interface{}, len(data))
		for k := range data {
			masked[k] = "••••••"
		}
		clone["data"] = masked
	}
	return clone
}

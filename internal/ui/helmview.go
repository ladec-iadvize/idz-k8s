package ui

// Helm releases view (US12): list, sorting, detail
// (manifest/values/history) rendering and key handling.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
	"sigs.k8s.io/yaml"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// helmLiveYAML renders a live object the way the detail view does: noisy
// bookkeeping (managedFields, last-applied) stripped, secrets masked.
func helmLiveYAML(obj model.ResourceObject) string {
	raw := cleanForDisplay(obj.Raw)
	if strings.EqualFold(obj.Type.Kind, "Secret") {
		raw = maskSecret(raw)
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		return "(cannot render: " + err.Error() + ")"
	}
	return strings.TrimRight(string(out), "\n")
}

func (m *Model) openHelm() (tea.Model, tea.Cmd) {
	if m.helm == nil {
		m.statusMsg = "helm view unavailable (no helm reader configured)"
		return m, nil
	}
	m.screen = screenHelm
	m.updateHelmColumns()
	m.helmWin.SetRows([]table.Row{{"", "loading helm releases…", "", "", "", "", ""}})
	m.helmWin.Sync(&m.helmTable)
	m.layout()
	hc, ns := m.helm, m.client.Namespace
	return m, func() tea.Msg {
		rows, err := hc.Releases(ns)
		return helmMsg{rows: rows, err: err}
	}
}

// helmColWidths resolves the helm table's column widths from the visible
// rows — content-driven like every other table (fitColumns); compact
// defaults while the list is still loading (placeholder row).
func (m *Model) helmColWidths() []int {
	titles := []string{"NAMESPACE", "RELEASE", "CHART", "VERSION", "REV", "STATUS", "UPDATED"}
	if len(m.helmRows) == 0 {
		return []int{22, 28, 16, 12, 5, 14, 9}
	}
	now := time.Now()
	needs := make([]int, len(titles))
	mins := make([]int, len(titles))
	for i, t := range titles {
		tw := len([]rune(t))
		if m.helmSortCol == i {
			tw += 2 // sort arrow
		}
		needs[i] = tw
		mins[i] = colMin(tw)
	}
	grow := func(i int, cell string) {
		if l := len([]rune(cell)); l > needs[i] {
			needs[i] = l
		}
	}
	for _, r := range m.helmRows {
		grow(0, r.Namespace)
		grow(1, r.Name)
		grow(2, r.Chart)
		grow(3, r.ChartVersion)
		grow(4, fmt.Sprintf("%d", r.Revision))
		grow(5, r.Health().Symbol()+" "+r.Status)
		grow(6, kube.Age(r.Updated, now))
	}
	// -10: the bubbles table needs slack for its own cell padding.
	return fitColumns(needs, mins, m.width-10)
}

// helmLess returns the sort order for a helm column index.
func helmLess(col int) func(a, b model.HelmRelease) bool {
	switch col {
	case 0:
		return func(a, b model.HelmRelease) bool { return a.Namespace+a.Name < b.Namespace+b.Name }
	case 1:
		return func(a, b model.HelmRelease) bool { return a.Name < b.Name }
	case 2:
		return func(a, b model.HelmRelease) bool { return a.Chart < b.Chart }
	case 3:
		return func(a, b model.HelmRelease) bool { return a.ChartVersion < b.ChartVersion }
	case 4:
		return func(a, b model.HelmRelease) bool { return a.Revision < b.Revision }
	case 5:
		return func(a, b model.HelmRelease) bool { return a.Status < b.Status }
	default:
		return func(a, b model.HelmRelease) bool { return a.Updated.Before(b.Updated) }
	}
}

// updateHelmColumns refreshes the header titles with the sort arrow.
func (m *Model) updateHelmColumns() {
	titles := []string{"NAMESPACE", "RELEASE", "CHART", "VERSION", "REV", "STATUS", "UPDATED"}
	widths := m.helmColWidths()
	cols := make([]table.Column, len(titles))
	for i, t := range titles {
		t = sortArrowTitle(t, m.helmSortCol == i, m.helmSortAsc)
		cols[i] = table.Column{Title: t, Width: widths[i]}
	}
	m.helmTable.SetColumns(cols)
}

func (m *Model) renderHelm() {
	now := time.Now()
	m.updateHelmColumns()
	terms := filterTerms(m.helmQuery)
	src := m.helmRows
	if m.helmSortCol >= 0 {
		src = make([]model.HelmRelease, len(m.helmRows))
		copy(src, m.helmRows)
		l := helmLess(m.helmSortCol)
		sort.SliceStable(src, func(i, j int) bool {
			if m.helmSortAsc {
				return l(src[i], src[j])
			}
			return l(src[j], src[i])
		})
	}
	rows := make([]table.Row, 0, len(src))
	for _, r := range src {
		// Every column, like every other filterable table.
		if len(terms) > 0 && !matchesTerms(rowHaystack(r.Namespace+"/"+r.Name,
			r.Chart, r.ChartVersion, r.AppVersion, r.Status, kube.Age(r.Updated, now)), terms) {
			continue
		}
		rows = append(rows, table.Row{
			r.Namespace, r.Name, r.Chart, r.ChartVersion,
			fmt.Sprintf("%d", r.Revision),
			r.Health().Symbol() + " " + r.Status,
			kube.Age(r.Updated, now),
		})
	}
	m.helmWin.SetRows(rows)
	m.helmWin.Sync(&m.helmTable)
}

func (m Model) handleHelmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Open):
		return m.openHelmDetail(false)
	case hit(msg, m.keys.Values):
		return m.openHelmDetail(true) // values only
	case hit(msg, m.keys.Filter):
		m.helmFiltering = true
		return m, nil
	case hit(msg, m.keys.Actions):
		return m.openActions()
	case hit(msg, m.keys.Sort):
		m.helmSortCol++
		if m.helmSortCol > 6 {
			m.helmSortCol = -1
		}
		m.helmSortAsc = true
		m.renderHelm()
		return m, nil
	case hit(msg, m.keys.SortDir):
		if m.helmSortCol >= 0 {
			m.helmSortAsc = !m.helmSortAsc
			m.renderHelm()
		}
		return m, nil
	}
	m.navigate(&m.helmWin, msg)
	m.helmWin.Sync(&m.helmTable)
	return m, nil
}

// openHelmDetail opens the release detail (history + resources + values), or
// only the values when valuesOnly is set ('v' — quick copy-friendly view).
func (m Model) openHelmDetail(valuesOnly bool) (tea.Model, tea.Cmd) {
	row, _ := m.helmWin.Selected()
	if len(row) < 2 || m.helm == nil {
		return m, nil
	}
	ns, name := row[0], row[1]
	m.helmValuesOnly = valuesOnly
	m.setHelmHistContent("loading release " + ns + "/" + name + "…")
	m.screen = screenHelmHist
	m.layout()
	hc, kc, types := m.helm, m.client, m.types
	return m, func() tea.Msg {
		det, err := hc.Detail(ns, name)
		out := helmDetailMsg{ns: ns, name: name, detail: det, err: err}
		if err != nil || valuesOnly {
			// Values-only view needs no live resource checks.
			return out
		}
		// Live-check each deployed resource against the cluster (read-only
		// GETs) so drift and broken deploys are visible.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out.live = make([]helmResLive, len(det.Resources))
		for i, r := range det.Resources {
			t, ok := typeForManifest(types, r)
			if !ok {
				continue // known=false
			}
			rns := r.Namespace
			if rns == "" {
				rns = ns
			}
			st, found, gerr := kc.GetObjectStatus(ctx, t, rns, r.Name)
			if gerr != nil {
				continue
			}
			out.live[i] = helmResLive{status: st, found: found, known: true}
		}
		return out
	}
}

// typeForManifest resolves a manifest head (apiVersion+kind) to a discovered,
// browsable resource type.
func typeForManifest(types []model.ResourceType, r model.HelmResource) (model.ResourceType, bool) {
	group, version := "", r.APIVersion
	if i := strings.Index(r.APIVersion, "/"); i >= 0 {
		group, version = r.APIVersion[:i], r.APIVersion[i+1:]
	}
	for _, t := range types {
		if t.Group == group && t.Version == version && t.Kind == r.Kind {
			return t, true
		}
	}
	return model.ResourceType{}, false
}

// renderHelmDetail shows everything about a release: history, the resources
// the chart deployed (with their LIVE state), and the user-supplied values.
// In values-only mode ('v') it renders just the values.
// renderHelmDetail stores the payload and renders it; renderHelmDetailView
// re-renders from the stored one when the resource selection moves.
func (m *Model) renderHelmDetail(msg helmDetailMsg) {
	m.helmDetail = msg
	m.helmResSel = 0
	m.renderHelmDetailView()
}

// handleHelmDetailKey: ↑/↓ walk the deployed resources, Enter opens the
// selected resource's DEFINITION (the chart's rendered manifest), 'y' its
// LIVE object from the cluster, 'v' the values (owner request 2026-08-05).
func (m Model) handleHelmDetailKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	res := m.helmDetail.detail.Resources
	switch {
	case hit(msg, m.keys.Open):
		return m.openHelmResource(false)
	case hit(msg, m.keys.Yaml):
		return m.openHelmResource(true)
	case hit(msg, m.keys.Values):
		m.helmValuesOnly = !m.helmValuesOnly
		m.renderHelmDetailView()
		m.helmHist.GotoTop()
		return m, nil
	}
	switch msg.Type {
	case tea.KeyUp:
		if m.helmResSel > 0 {
			m.helmResSel--
			m.renderHelmDetailView()
			m.keepFindingVisible(&m.helmHist, m.helmResLines, m.helmResSel)
		}
		return m, nil
	case tea.KeyDown:
		if m.helmResSel < len(res)-1 {
			m.helmResSel++
			m.renderHelmDetailView()
			m.keepFindingVisible(&m.helmHist, m.helmResLines, m.helmResSel)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.helmHist, cmd = m.helmHist.Update(msg)
	return m, cmd
}

// openHelmResource shows one deployed resource: its rendered definition, or
// (live=true) the object as it exists in the cluster right now.
func (m Model) openHelmResource(live bool) (tea.Model, tea.Cmd) {
	res := m.helmDetail.detail.Resources
	if m.helmResSel < 0 || m.helmResSel >= len(res) {
		m.statusMsg = "no deployed resource to open"
		return m, nil
	}
	r := res[m.helmResSel]
	ns := r.Namespace
	if ns == "" {
		ns = m.helmDetail.ns
	}
	label := r.Kind + "/" + r.Name
	m.screen = screenHelmRes
	m.layout()
	if !live {
		body := r.Manifest
		if strings.TrimSpace(body) == "" {
			body = "(the release manifest carries no document for this resource)"
		}
		m.setContent(screenHelmRes, m.rule("Definition (chart-rendered) — "+ns+"/"+label)+"\n\n"+
			m.colorizeYAML(strings.TrimRight(body, "\n"))+"\n\n"+
			m.theme.Faint.Render("'y' on the release detail shows the LIVE object · Esc goes back"))
		m.helmRes.GotoTop()
		return m, nil
	}
	t, ok := typeForManifest(m.types, r)
	if !ok {
		m.setContent(screenHelmRes, m.rule("Live object — "+ns+"/"+label)+"\n\n"+
			m.theme.Warning.Render("this type is not browsable with your credentials — showing nothing rather than guessing"))
		return m, nil
	}
	m.setContent(screenHelmRes, "loading "+ns+"/"+label+" from the cluster…")
	cl := m.client
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		obj, err := cl.GetObject(ctx, t, ns, r.Name)
		return helmResourceMsg{label: ns + "/" + label, obj: obj, err: err}
	}
}

func (m *Model) renderHelmDetailView() {
	msg := m.helmDetail
	var b strings.Builder
	title := "Helm release — " + msg.ns + "/" + msg.name
	if m.helmValuesOnly {
		title = "Helm values — " + msg.ns + "/" + msg.name
	}
	fmt.Fprintf(&b, "%s\n\n", m.theme.Title.Render(title))
	if msg.err != nil {
		b.WriteString(m.theme.Error.Render("⚠ " + msg.err.Error()))
		m.setHelmHistContent(b.String())
		return
	}
	if m.helmValuesOnly {
		if msg.detail.Values == "" {
			b.WriteString(m.theme.Faint.Render("(none — chart defaults)"))
			b.WriteString("\n")
		} else {
			b.WriteString(m.colorizeYAML(strings.TrimRight(msg.detail.Values, "\n")))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(m.theme.Faint.Render("tip: press 'm' to disable the mouse and copy text"))
		m.setHelmHistContent(b.String())
		m.helmHist.GotoTop()
		return
	}
	now := time.Now()

	// History.
	b.WriteString(m.rule("History"))
	b.WriteString("\n")
	// CHART/APP come from each revision's OWN stored chart, so the history
	// answers "what shipped at revision N" (owner request 2026-08-05).
	chartW, appW := 7, 5
	for _, r := range msg.detail.History {
		if n := len([]rune(r.ChartVersion)); n > chartW {
			chartW = n
		}
		if n := len([]rune(r.AppVersion)); n > appW {
			appW = n
		}
	}
	chartW, appW = clampW(chartW, 7, 18), clampW(appW, 5, 18)
	fmt.Fprintf(&b, "  %-5s %-16s %-*s %-*s %-9s %s\n", "REV", "STATUS", chartW, "CHART", appW, "APP", "WHEN", "DESCRIPTION")
	descW := m.width - 40 - chartW - appW
	if descW < 30 {
		descW = 30
	}
	for i, r := range msg.detail.History {
		if i >= 8 {
			b.WriteString(m.theme.Faint.Render(fmt.Sprintf("  … %d older revisions", len(msg.detail.History)-i)))
			b.WriteString("\n")
			break
		}
		cur := " "
		if i == 0 {
			cur = "▸" // the revision currently deployed
		}
		line := fmt.Sprintf("%s %-5d %-16s %-*s %-*s %-9s %s", cur, r.Revision, r.Status,
			chartW, orDash(r.ChartVersion), appW, orDash(r.AppVersion),
			kube.Age(r.Updated, now), truncate(r.Description, descW))
		switch {
		case r.Status == "failed":
			b.WriteString(m.theme.Error.Render(line))
		case strings.HasPrefix(r.Status, "pending"):
			b.WriteString(m.theme.Warning.Render(line))
		case r.Status == "deployed":
			b.WriteString(m.theme.Ok.Render(line))
		default:
			b.WriteString(m.theme.Faint.Render(line))
		}
		b.WriteString("\n")
	}

	// Resources deployed by the chart, with live status (drift detection).
	b.WriteString("\n")
	b.WriteString(m.rule(fmt.Sprintf("Resources (%d, live state)", len(msg.detail.Resources))))
	b.WriteString("\n")
	// Name column sized by the content (was a fixed 60), status keeps room.
	resW := 24
	for _, r := range msg.detail.Resources {
		if n := len([]rune(r.Kind + "/" + r.Name)); n > resW {
			resW = n
		}
	}
	if lim := m.width - 40; resW > lim && lim >= 24 {
		resW = lim
	}
	if m.helmResSel >= len(msg.detail.Resources) {
		m.helmResSel = len(msg.detail.Resources) - 1
	}
	if m.helmResSel < 0 {
		m.helmResSel = 0
	}
	m.helmResLines = make([]int, 0, len(msg.detail.Resources))
	for i, r := range msg.detail.Resources {
		// Remember each resource's content line: ↑/↓ and clicks select it.
		m.helmResLines = append(m.helmResLines, strings.Count(b.String(), "\n"))
		cursor := "  "
		if i == m.helmResSel {
			cursor = "▸ "
		}
		label := fmt.Sprintf("%s%-*s", cursor, resW, truncate(r.Kind+"/"+r.Name, resW))
		var line string
		switch {
		case i >= len(msg.live) || !msg.live[i].known:
			line = m.theme.Faint.Render(label + " —")
		case !msg.live[i].found:
			line = m.theme.Error.Render(label + " ✗ MISSING in cluster")
		default:
			st := msg.live[i].status
			line = label + " " + m.theme.Status(st)
			if st.Reason != "" {
				line += m.theme.Faint.Render(" (" + st.Reason + ")")
			}
		}
		if i == m.helmResSel {
			line = m.theme.Selected.Render(xansi.Strip(line))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(msg.detail.Resources) > 0 {
		b.WriteString(m.theme.Faint.Render("  ↑/↓ select · Enter: its definition · 'y': its live object") + "\n")
	}

	// Chart metadata Helm stored with the revision.
	if c := msg.detail.Chart; c.Name != "" {
		b.WriteString("\n")
		b.WriteString(m.rule("Chart"))
		b.WriteString("\n")
		fmt.Fprintf(&b, "  %s-%s", c.Name, orDash(c.Version))
		if c.AppVersion != "" {
			fmt.Fprintf(&b, "  (app %s)", c.AppVersion)
		}
		if c.Deprecated {
			b.WriteString("  " + m.theme.Error.Render("⚠ DEPRECATED chart"))
		}
		b.WriteString("\n")
		if c.Description != "" {
			b.WriteString("  " + m.theme.Faint.Render(truncate(c.Description, m.width-4)) + "\n")
		}
		if c.Home != "" {
			b.WriteString("  " + m.theme.Faint.Render(truncate(c.Home, m.width-4)) + "\n")
		}
		if len(c.Dependencies) > 0 {
			b.WriteString("  " + m.theme.Faint.Render("subcharts: "+truncate(strings.Join(c.Dependencies, ", "), m.width-14)) + "\n")
		}
	}

	// Chart hooks and their last run — where a failed pre-upgrade job shows.
	if len(msg.detail.Hooks) > 0 {
		b.WriteString("\n")
		b.WriteString(m.rule(fmt.Sprintf("Hooks (%d)", len(msg.detail.Hooks))))
		b.WriteString("\n")
		for _, h := range msg.detail.Hooks {
			when := "never run"
			if !h.Started.IsZero() {
				when = kube.Age(h.Started, now) + " ago"
			}
			line := fmt.Sprintf("  %-28s %-14s %-22s %s",
				truncate(h.Name, 28), truncate(h.Kind, 14),
				truncate(strings.Join(h.Events, ","), 22), when)
			switch h.Phase {
			case "Failed":
				b.WriteString(m.theme.Error.Render(line + "  ✗ " + h.Phase))
			case "Succeeded":
				b.WriteString(m.theme.Ok.Render(line + "  ✓ " + h.Phase))
			case "":
				b.WriteString(m.theme.Faint.Render(line))
			default:
				b.WriteString(m.theme.Warning.Render(line + "  ! " + h.Phase))
			}
			b.WriteString("\n")
		}
	}

	// NOTES.txt of the current revision — what helm printed after the deploy.
	if strings.TrimSpace(msg.detail.Notes) != "" {
		b.WriteString("\n")
		b.WriteString(m.rule("Notes (NOTES.txt)"))
		b.WriteString("\n")
		for _, l := range strings.Split(strings.TrimRight(msg.detail.Notes, "\n"), "\n") {
			b.WriteString("  " + truncate(l, m.width-2) + "\n")
		}
	}

	// User-supplied values.
	b.WriteString("\n")
	b.WriteString(m.rule("Values (user-supplied)"))
	b.WriteString("\n")
	if msg.detail.Values == "" {
		b.WriteString(m.theme.Faint.Render("  (none — chart defaults)"))
		b.WriteString("\n")
	} else {
		for _, line := range strings.Split(strings.TrimRight(msg.detail.Values, "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}

	m.setHelmHistContent(b.String())
	m.helmHist.GotoTop()
}

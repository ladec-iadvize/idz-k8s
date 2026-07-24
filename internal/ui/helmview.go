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

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func (m *Model) openHelm() (tea.Model, tea.Cmd) {
	if m.helm == nil {
		m.statusMsg = "helm view unavailable (no helm reader configured)"
		return m, nil
	}
	m.screen = screenHelm
	m.helmTable.SetColumns([]table.Column{
		{Title: "NAMESPACE", Width: 22},
		{Title: "RELEASE", Width: 28},
		{Title: "CHART", Width: max(16, m.width-100)},
		{Title: "VERSION", Width: 12},
		{Title: "REV", Width: 5},
		{Title: "STATUS", Width: 14},
		{Title: "UPDATED", Width: 9},
	})
	m.helmWin.SetRows([]table.Row{{"", "loading helm releases…", "", "", "", "", ""}})
	m.helmWin.Sync(&m.helmTable)
	m.layout()
	hc, ns := m.helm, m.client.Namespace
	return m, func() tea.Msg {
		rows, err := hc.Releases(ns)
		return helmMsg{rows: rows, err: err}
	}
}

// helmColWidths returns the helm table's column widths (must stay in sync
// with openHelm's SetColumns).
func (m *Model) helmColWidths() []int {
	chart := 16
	for _, r := range m.helmRows {
		if l := len([]rune(r.Chart)) + 2; l > chart {
			chart = l
		}
	}
	if lim := m.width - 100; chart > lim && lim >= 16 {
		chart = lim
	}
	return []int{22, 28, chart, 12, 5, 14, 9}
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
	q := strings.ToLower(strings.TrimSpace(m.helmQuery))
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
		if q != "" && !strings.Contains(strings.ToLower(r.Namespace+"/"+r.Name+" "+r.Chart), q) {
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
func (m *Model) renderHelmDetail(msg helmDetailMsg) {
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
	fmt.Fprintf(&b, "  %-5s %-16s %-9s %s\n", "REV", "STATUS", "WHEN", "DESCRIPTION")
	for i, r := range msg.detail.History {
		if i >= 8 {
			b.WriteString(m.theme.Faint.Render(fmt.Sprintf("  … %d older revisions", len(msg.detail.History)-i)))
			b.WriteString("\n")
			break
		}
		line := fmt.Sprintf("  %-5d %-16s %-9s %s", r.Revision, r.Status, kube.Age(r.Updated, now), truncate(r.Description, 66))
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
	for i, r := range msg.detail.Resources {
		label := fmt.Sprintf("  %-60s", truncate(r.Kind+"/"+r.Name, 60))
		switch {
		case i >= len(msg.live) || !msg.live[i].known:
			b.WriteString(m.theme.Faint.Render(label + " —"))
		case !msg.live[i].found:
			b.WriteString(m.theme.Error.Render(label + " ✗ MISSING in cluster"))
		default:
			st := msg.live[i].status
			b.WriteString(label + " " + m.theme.Status(st))
			if st.Reason != "" {
				b.WriteString(m.theme.Faint.Render(" (" + st.Reason + ")"))
			}
		}
		b.WriteString("\n")
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

package ui

// Events timeline view: open/filter/key handling and the visual
// timeline rendering (lanes, markers, badges).

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func (m *Model) openEvents() (tea.Model, tea.Cmd) {
	m.screen = screenEvents
	m.eventsQuery = ""
	// Contextual: opening the timeline from a list pre-sets the kind filter to
	// the type being browsed (e.g. Deployment from the deployments list).
	// Clear or change it with 'k'.
	m.eventsKind = m.curType.Kind
	// Marked resources (Space) scope the timeline first; otherwise a drilled
	// pod list scopes to exactly those pods.
	m.eventsScope = nil
	m.eventsScopeFor = ""
	if len(m.marked) > 0 {
		scope := make(map[string]bool, len(m.marked))
		for k := range m.marked {
			scope[k] = true
		}
		m.eventsScope = scope
		m.eventsScopeFor = fmt.Sprintf("%d marked", len(m.marked))
	} else if (m.drillSelector != "" || m.drillNode != "") && len(m.objects) > 0 {
		scope := make(map[string]bool, len(m.objects))
		for _, o := range m.objects {
			scope[o.Namespace+"/"+o.Name] = true
		}
		m.eventsScope = scope
		m.eventsScopeFor = m.drillFor
	}
	m.events.SetContent("loading events…")
	m.layout()
	return m, m.fetchEvents()
}

// handleEventsKey (outside typing mode): '/' edits the filter, 'k' opens the
// kind selector, 'n' the namespace picker. ↑/↓ move the Recent selection
// (highlighted on the timeline); PgUp/PgDn scroll the view.
func (m Model) handleEventsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Open):
		// Jump to the object the selected event references (consistency:
		// Enter navigates everywhere).
		if m.recentSel >= 0 && m.recentSel < len(m.eventsShown) {
			e := m.eventsShown[m.recentSel]
			return m.openDescribeRef(objRef{kind: e.ObjKind, ns: e.Namespace, name: e.ObjName})
		}
		return m, nil
	case hit(msg, m.keys.Filter):
		m.eventsFiltering = true
		m.renderEvents()
		return m, nil
	case hit(msg, m.keys.Kind):
		return m.openKindPicker()
	case hit(msg, m.keys.WarnOnly):
		m.eventsWarnOnly = !m.eventsWarnOnly
		m.recentSel, m.recentWin = 0, 0
		m.renderEvents()
		return m, nil
	case hit(msg, m.keys.Namespace):
		return m.openPicker(pickNamespace)
	}
	switch msg.Type {
	case tea.KeyUp:
		if m.recentSel > 0 {
			m.recentSel--
			m.renderEvents()
		}
		return m, nil
	case tea.KeyDown:
		m.recentSel++ // clamped in renderEvents
		m.renderEvents()
		return m, nil
	}
	var cmd tea.Cmd
	m.events, cmd = m.events.Update(msg)
	return m, cmd
}

const (
	timelineLaneWidth = 30 // width of the object-name column
	timelineMaxLanes  = 25 // lanes shown before "+N more"
)

// renderEvents draws a visual timeline: a time axis, one lane per object, and
// markers placed proportionally to when each event happened — so you can SEE
// what happened when. The most recent events are detailed below the graph.
func (m *Model) renderEvents() {
	scope := m.client.Namespace
	if scope == "" {
		scope = "all namespaces"
	}
	q := strings.ToLower(strings.TrimSpace(m.eventsQuery))

	// Filter: kind selector ('k') + text query ('/'). The text query matches
	// the OBJECT identity only (namespace/kind/name) — not reasons/messages —
	// so typing "back" matches pods named *back*, not every BackOff event.
	var evs []model.Event
	for _, e := range m.eventRows {
		if m.eventsWarnOnly && !e.Warning() {
			continue // severity filter: warnings only (FR-014)
		}
		if m.eventsScope != nil && !m.eventsScope[e.Namespace+"/"+e.ObjName] {
			continue // scoped to a drilled workload's own pods
		}
		if m.eventsKind != "" && e.ObjKind != m.eventsKind {
			continue
		}
		ident := e.Namespace + "/" + e.ObjKind + "/" + e.ObjName
		if q != "" && !strings.Contains(strings.ToLower(ident), q) {
			continue
		}
		if e.Time.IsZero() {
			continue
		}
		evs = append(evs, e)
	}
	m.eventsShown = evs

	kindLabel := m.eventsKind
	if kindLabel == "" {
		kindLabel = "All"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Events timeline — %s   ", scope)
	if m.eventsScopeFor != "" {
		b.WriteString(m.theme.StatusBar.Render("scope:["+m.eventsScopeFor+"]") + "  ")
	}
	// The kind selector and filter hint are clickable (zones by width, T048).
	m.eventsKindZone = clickZone{x0: lipgloss.Width(b.String())}
	b.WriteString(m.theme.StatusBar.Render("kind:[" + kindLabel + " ▾]"))
	m.eventsKindZone.x1 = lipgloss.Width(b.String())
	if m.eventsWarnOnly {
		b.WriteString("  " + m.theme.Warning.Render("▲ warnings only ('w' toggles)"))
	}
	m.eventsFilterHit = clickZone{x0: lipgloss.Width(b.String())}
	switch {
	case m.eventsFiltering:
		b.WriteString("  filter: " + m.eventsQuery + "▏" + m.theme.Faint.Render("  (Enter: save, Esc: cancel)"))
	case q != "":
		b.WriteString("  filter: " + m.eventsQuery + m.theme.Faint.Render("  (/ to edit)"))
	default:
		b.WriteString(m.theme.Faint.Render("  (/ filter, k kind, w warnings, n namespace)"))
	}
	m.eventsFilterHit.x1 = lipgloss.Width(b.String())
	b.WriteString("\n\n")

	if len(evs) == 0 {
		b.WriteString(m.theme.Faint.Render("no events match"))
		m.events.SetContent(b.String())
		m.events.GotoTop()
		return
	}

	// Time window across the filtered events (they arrive most-recent first).
	start, end := evs[0].Time, evs[0].Time
	for _, e := range evs {
		if e.Time.Before(start) {
			start = e.Time
		}
		if e.Time.After(end) {
			end = e.Time
		}
	}
	span := end.Sub(start)
	if span < time.Minute {
		span = time.Minute
		start = end.Add(-span)
	}

	axisW := m.width - timelineLaneWidth - 6
	if axisW < 20 {
		axisW = 20
	}
	pos := func(t time.Time) int {
		p := int(float64(t.Sub(start)) / float64(span) * float64(axisW-1))
		if p < 0 {
			p = 0
		}
		if p >= axisW {
			p = axisW - 1
		}
		return p
	}

	// Group events into lanes (one per object), most recent activity first.
	type lane struct {
		name   string
		latest time.Time
		events []model.Event
	}
	byObj := map[string]*lane{}
	var order []*lane
	for _, e := range evs {
		key := e.Namespace + "/" + e.ObjKind + "/" + e.ObjName
		l, ok := byObj[key]
		if !ok {
			l = &lane{name: e.ObjKind + "/" + e.ObjName}
			byObj[key] = l
			order = append(order, l)
		}
		l.events = append(l.events, e)
		if e.Time.After(l.latest) {
			l.latest = e.Time
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].latest.After(order[j].latest) })

	// Time labels: start / mid / end above the axis.
	lbl := func(t time.Time) string { return t.Local().Format("15:04") }
	labels := make([]byte, axisW)
	for i := range labels {
		labels[i] = ' '
	}
	placeLabel := func(p int, s string) {
		if p+len(s) > axisW {
			p = axisW - len(s)
		}
		if p < 0 {
			p = 0
		}
		copy(labels[p:], s)
	}
	// \x01 marks label padding so the dash-filler leaves a space around times.
	placeLabel(0, "\x01"+lbl(start)+"\x01")
	placeLabel(axisW/2-3, "\x01"+lbl(start.Add(span/2))+"\x01")
	placeLabel(axisW-7, "\x01"+lbl(end)+"\x01")
	// Section rule with the time labels embedded: ── Timeline ── 11:49 ── …
	lead := "── "
	title := "Timeline"
	// Rune count, not len(): "─" is 3 bytes for 1 cell — byte-counting shoved
	// the axis 4 columns left of the lane markers.
	gap := timelineLaneWidth + 2 - len([]rune(lead)) - len(title) - 1
	if gap < 1 {
		gap = 1
	}
	axisRule := make([]byte, axisW)
	for i := range axisRule {
		if labels[i] == ' ' {
			axisRule[i] = '-'
		} else {
			axisRule[i] = labels[i]
		}
	}
	ruleLine := strings.ReplaceAll(string(axisRule), "-", "─")
	ruleLine = strings.ReplaceAll(ruleLine, "\x01", " ")
	b.WriteString(m.theme.Faint.Render(lead) + m.theme.TableHeader.Render(title) +
		m.theme.Faint.Render(" "+strings.Repeat("─", gap)+ruleLine))
	b.WriteString("\n")

	// Selection: ↑/↓ walk ALL filtered events (most recent first); the detail
	// list below is a sliding window that follows the selection, and the
	// selected event lights up on the timeline.
	const recentWinSize = 8
	if m.recentSel >= len(evs) {
		m.recentSel = len(evs) - 1
	}
	if m.recentSel < 0 {
		m.recentSel = 0
	}
	if m.recentSel < m.recentWin {
		m.recentWin = m.recentSel
	}
	if m.recentSel >= m.recentWin+recentWinSize {
		m.recentWin = m.recentSel - recentWinSize + 1
	}
	if maxWin := len(evs) - recentWinSize; m.recentWin > maxWin {
		m.recentWin = maxWin
	}
	if m.recentWin < 0 {
		m.recentWin = 0
	}
	var sel *model.Event
	if len(evs) > 0 {
		sel = &evs[m.recentSel]
	}
	sameObj := func(e model.Event, l *model.Event) bool {
		return l != nil && e.Namespace == l.Namespace && e.ObjKind == l.ObjKind && e.ObjName == l.ObjName
	}

	// Lanes with proportionally placed markers.
	shown := order
	if len(shown) > timelineMaxLanes {
		shown = shown[:timelineMaxLanes]
	}
	for _, l := range shown {
		type cell struct {
			count   int
			warning bool
		}
		cells := make([]cell, axisW)
		laneSelected := len(l.events) > 0 && sameObj(l.events[0], sel)
		selPos := -1
		if laneSelected && sel != nil {
			selPos = pos(sel.Time)
		}
		for _, e := range l.events {
			p := pos(e.Time)
			cells[p].count += max(e.Count, 1)
			cells[p].warning = cells[p].warning || e.Warning()
		}
		var row strings.Builder
		name := fmt.Sprintf("%-*s", timelineLaneWidth, truncate(l.name, timelineLaneWidth))
		if laneSelected {
			row.WriteString(m.theme.Selected.Render(name))
		} else {
			row.WriteString(name)
		}
		row.WriteString(" │")
		for i, c := range cells {
			switch {
			case i == selPos && c.count > 0:
				// The selected event's bucket: inverse video so it pops out.
				row.WriteString(m.theme.Highlight.Render(marker(c.count, markerSym(c.warning))))
			case c.count == 0:
				row.WriteString(m.theme.Faint.Render("·"))
			case c.warning:
				row.WriteString(m.theme.Error.Render(marker(c.count, "▲")))
			default:
				row.WriteString(m.theme.Ok.Render(marker(c.count, "•")))
			}
		}
		row.WriteString("│")
		b.WriteString(row.String())
		b.WriteString("\n")
	}
	if len(order) > timelineMaxLanes {
		fmt.Fprintf(&b, "%s\n", m.theme.Faint.Render(fmt.Sprintf("… +%d more objects (type to filter)", len(order)-timelineMaxLanes)))
	}

	// Legend + a sliding detail window over ALL events, following the selection.
	b.WriteString("\n")
	b.WriteString(m.theme.Faint.Render("▲ warning   • normal   (digit = repeated events)   ↑/↓ select"))
	b.WriteString("\n")
	b.WriteString(m.rule(fmt.Sprintf("Events (%d/%d)", m.recentSel+1, len(evs))))
	b.WriteString("\n")
	now := time.Now()
	winEnd := m.recentWin + recentWinSize
	if winEnd > len(evs) {
		winEnd = len(evs)
	}
	if m.recentWin > 0 {
		b.WriteString(m.theme.Faint.Render(fmt.Sprintf("  ↑ %d more recent", m.recentWin)))
		b.WriteString("\n")
	}
	// Record where the detail rows start (content line) for click-to-select.
	m.recentBaseLine = strings.Count(b.String(), "\n")
	m.recentShown = winEnd - m.recentWin
	for i := m.recentWin; i < winEnd; i++ {
		e := evs[i]
		cnt := ""
		if e.Count > 1 {
			cnt = fmt.Sprintf(" x%d", e.Count)
		}
		cursor := "  "
		if i == m.recentSel {
			cursor = "▶ "
		}
		line := fmt.Sprintf("%s%-4s %s %-34s %s%s — %s",
			cursor, kube.Age(e.Time, now), eventBadge(e), truncate(e.ObjKind+"/"+e.ObjName, 34), e.Reason, cnt, truncate(e.Message, 60))
		switch {
		case i == m.recentSel:
			b.WriteString(m.theme.Selected.Render(line))
		case e.Warning():
			b.WriteString(m.theme.Warning.Render(line))
		default:
			b.WriteString(m.theme.Faint.Render(line))
		}
		b.WriteString("\n")
	}
	if winEnd < len(evs) {
		b.WriteString(m.theme.Faint.Render(fmt.Sprintf("  ↓ %d older", len(evs)-winEnd)))
		b.WriteString("\n")
	}
	// Full message of the selected event (owner bug 2026-07-07: the one-line
	// rows are width-truncated for click geometry, so the selection gets a
	// dedicated wrapped block — placed AFTER the rows, geometry untouched).
	if m.recentSel >= 0 && m.recentSel < len(evs) {
		e := evs[m.recentSel]
		b.WriteString("\n" + m.rule("selected event — full message") + "\n")
		cnt := ""
		if e.Count > 1 {
			cnt = fmt.Sprintf(" x%d", e.Count)
		}
		head := fmt.Sprintf("  %s %s %s/%s — %s%s", kube.Age(e.Time, now), eventBadge(e),
			e.ObjKind, e.ObjName, e.Reason, cnt)
		b.WriteString(m.theme.Section.Render(truncate(head, m.width-2)) + "\n")
		for _, l := range wrapTo(e.Message, m.width-6) {
			line := "  " + l
			if e.Warning() {
				b.WriteString(m.theme.Warning.Render(line))
			} else {
				b.WriteString(line)
			}
			b.WriteString("\n")
		}
	}

	m.events.SetContent(b.String())
	m.events.GotoTop()
}

// markerSym picks the base symbol for a timeline cell.
func markerSym(warning bool) string {
	if warning {
		return "▲"
	}
	return "•"
}

// marker renders a single-cell marker: the symbol, or a digit when several
// events share the same time bucket.
func marker(count int, sym string) string {
	if count <= 1 {
		return sym
	}
	if count > 9 {
		return "+"
	}
	return string(rune('0' + count))
}

func eventBadge(e model.Event) string {
	if e.Warning() {
		return "▲"
	}
	return "•"
}

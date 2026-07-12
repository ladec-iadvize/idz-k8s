package ui

// Viewport search ('/', vim-like) over describe/YAML and helm
// detail: target resolution, highlighting and hit navigation.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"
)

// handleSearchKey handles '/', 'n' and 'N' on a searchable viewport.
func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case hit(msg, m.keys.Filter):
		m.searchTyping, m.searchInput = true, m.searchQuery
		return m, nil, true
	case hit(msg, m.keys.SearchNext) && len(m.searchHits) > 0:
		m.searchStep(1)
		return m, nil, true
	case hit(msg, m.keys.SearchPrev) && len(m.searchHits) > 0:
		m.searchStep(-1)
		return m, nil, true
	}
	return m, nil, false
}

// vpFor maps a screen to its content viewport ('/' searches all of them;
// the events timeline keeps its own dedicated filter instead).
func (m *Model) vpFor(sc screen) *viewport.Model {
	switch sc {
	case screenDetail:
		return &m.detail
	case screenLogs:
		return &m.logsView
	case screenDiag:
		return &m.diag
	case screenTopology:
		return &m.topo
	case screenHelmHist:
		return &m.helmHist
	case screenSizing:
		return &m.sizingVP
	case screenPosture:
		return &m.posture
	case screenConnectivity:
		return &m.connectivity
	case screenAccess:
		return &m.access
	case screenDrift:
		return &m.drift
	}
	return nil
}

// searchableNow reports whether '/' means viewport search on this screen.
func (m *Model) searchableNow() bool { return m.vpFor(m.screen) != nil }

// setContent stores a viewport's raw content and renders it, keeping an
// active search highlighted when the content refreshes (describe events
// landing, log lines streaming in…).
func (m *Model) setContent(sc screen, content string) {
	vp := m.vpFor(sc)
	if vp == nil {
		return
	}
	if m.vpRaw == nil {
		m.vpRaw = map[screen]string{}
	}
	m.vpRaw[sc] = content
	if m.searchQuery != "" && m.searchScreen == sc {
		m.applySearch(false)
		return
	}
	vp.SetContent(content)
}

// setDetailContent keeps existing call sites readable.
func (m *Model) setDetailContent(content string) { m.setContent(screenDetail, content) }

// setHelmHistContent keeps existing call sites readable.
func (m *Model) setHelmHistContent(content string) { m.setContent(screenHelmHist, content) }

// searchTarget maps the search's own screen to its viewport and raw content.
func (m *Model) searchTarget() (*viewport.Model, string) {
	if vp := m.vpFor(m.searchScreen); vp != nil {
		return vp, m.vpRaw[m.searchScreen]
	}
	return nil, ""
}

// clearSearch restores the unhighlighted content.
func (m *Model) clearSearch() {
	m.searchQuery, m.searchInput = "", ""
	m.searchHits, m.searchIdx = nil, 0
	if vp, raw := m.searchTarget(); vp != nil {
		vp.SetContent(raw)
	}
	m.statusMsg = ""
}

// applySearch highlights every match and (optionally) jumps to the first.
func (m *Model) applySearch(jumpFirst bool) {
	vp, raw := m.searchTarget()
	if vp == nil {
		return
	}
	if m.searchQuery == "" {
		m.clearSearch()
		return
	}
	highlighted, hits := highlightMatches(raw, m.searchQuery, m.theme.Highlight.Render)
	vp.SetContent(highlighted)
	m.searchHits = hits
	if len(hits) == 0 {
		m.statusMsg = "no match for “" + m.searchQuery + "” — Esc clears"
		return
	}
	if jumpFirst {
		m.searchIdx = 0
	}
	if m.searchIdx >= len(hits) {
		m.searchIdx = 0
	}
	m.gotoSearchHit()
}

// gotoSearchHit scrolls the current match into view (upper third).
func (m *Model) gotoSearchHit() {
	vp, _ := m.searchTarget()
	if vp == nil || len(m.searchHits) == 0 {
		return
	}
	off := m.searchHits[m.searchIdx] - vp.Height/3
	if off < 0 {
		off = 0
	}
	vp.SetYOffset(off)
	m.statusMsg = fmt.Sprintf("match %d/%d for “%s” — 'n' next · 'N' previous · Esc clears",
		m.searchIdx+1, len(m.searchHits), m.searchQuery)
}

// searchStep moves to the next/previous match, wrapping around.
func (m *Model) searchStep(dir int) {
	if len(m.searchHits) == 0 {
		return
	}
	m.searchIdx = (m.searchIdx + dir + len(m.searchHits)) % len(m.searchHits)
	m.gotoSearchHit()
}

// highlightMatches rebuilds matching lines with the query highlighted
// (case-insensitive). Styled lines are flattened to plain text on match so
// the highlight offsets stay exact — the searched term wins over syntax
// color on those lines. Returns the content and the matching line numbers.
func highlightMatches(content, query string, mark func(...string) string) (string, []int) {
	q := strings.ToLower(query)
	lines := strings.Split(content, "\n")
	var hits []int
	for i, l := range lines {
		plain := xansi.Strip(l)
		low := strings.ToLower(plain)
		if !strings.Contains(low, q) {
			continue
		}
		hits = append(hits, i)
		var b strings.Builder
		for {
			j := strings.Index(low, q)
			if j < 0 {
				b.WriteString(plain)
				break
			}
			b.WriteString(plain[:j])
			end := j + len(q)
			if end > len(plain) {
				end = len(plain)
			}
			b.WriteString(mark(plain[j:end]))
			plain, low = plain[end:], low[end:]
		}
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n"), hits
}

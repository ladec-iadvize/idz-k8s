package ui

// Log viewport rendering (owner request 2026-08-03): long lines are readable
// either by scrolling sideways (←/→) or by turning wrapping on ('w').
// Everything here is ANSI-aware — merged log lines carry a colored per-pod
// prefix, so slicing them by runes would cut escape sequences and bleed
// color across the screen.

import (
	"fmt"
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
)

// logHStep is how far ←/→ shift the view (a word-ish chunk, like less).
const logHStep = 20

// renderLogs rebuilds the log viewport from the buffer, honoring the wrap
// toggle and the horizontal offset. Called on every new line, on resize and
// on each toggle — the buffer stays the single source of truth (never the
// rendered View, CLAUDE.md).
func (m *Model) renderLogs() {
	if len(m.logBuf) == 0 {
		m.setContent(screenLogs, "waiting for logs…")
		return
	}
	w := m.width
	if w < 20 {
		w = 20
	}
	out := make([]string, 0, len(m.logBuf))
	for _, l := range m.logBuf {
		switch {
		case m.logWrap:
			// Word-aware, ANSI-safe hard wrap: the viewport never soft-wraps
			// on its own (that would desync every line count).
			out = append(out, strings.Split(xansi.Wrap(l, w, ""), "\n")...)
		case m.logHOffset > 0:
			out = append(out, xansi.TruncateLeft(l, m.logHOffset, ""))
		default:
			out = append(out, l)
		}
	}
	m.setContent(screenLogs, strings.Join(out, "\n"))
}

// logMaxWidth is the widest buffered line (display cells), used to clamp the
// horizontal offset so ←/→ can never scroll past the content.
func (m *Model) logMaxWidth() int {
	maxW := 0
	for _, l := range m.logBuf {
		if w := xansi.StringWidth(l); w > maxW {
			maxW = w
		}
	}
	return maxW
}

// scrollLogs shifts the horizontal offset by delta steps, clamped to the
// content. A no-op while wrapping is on (nothing is cut to scroll to).
func (m *Model) scrollLogs(delta int) {
	if m.logWrap {
		m.statusMsg = "wrap is ON — nothing is cut off ('w' to scroll sideways instead)"
		return
	}
	off := m.logHOffset + delta
	if off < 0 {
		off = 0
	}
	// Keep at least a screen's worth of text visible at the far right.
	if limit := m.logMaxWidth() - m.width/2; off > limit {
		if limit < 0 {
			limit = 0
		}
		off = limit
	}
	if off == m.logHOffset {
		return
	}
	m.logHOffset = off
	m.renderLogs()
	if m.logHOffset == 0 {
		m.statusMsg = "logs: back to column 0"
		return
	}
	m.statusMsg = fmt.Sprintf("logs shifted +%d columns (←/→ to move, 'w' wraps instead)", m.logHOffset)
}

// toggleLogWrap switches wrapping. Turning it ON drops the horizontal offset:
// with everything visible there is nothing left to scroll to.
func (m *Model) toggleLogWrap() {
	m.logWrap = !m.logWrap
	if m.logWrap {
		m.logHOffset = 0
		m.statusMsg = "wrap ON — long lines fold onto the next line ('w' to turn off)"
	} else {
		m.statusMsg = "wrap OFF — ←/→ shift the view sideways ('w' to turn back on)"
	}
	m.renderLogs()
}

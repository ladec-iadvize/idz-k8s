package ui

// Log readability (owner request 2026-08-03): ←/→ shift the view sideways,
// 'w' folds long lines. Both must stay ANSI-safe — merged log lines carry a
// colored per-pod prefix.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
	"github.com/iadvize/idz-k8s/internal/ui/theme"
)

func logsModel(t *testing.T, lines ...string) Model {
	t.Helper()
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 80, 24
	m.layout()
	m.screen = screenLogs
	m.logBuf = lines
	m.renderLogs()
	return m
}

// TestLogsHorizontalScroll: →  reveals the tail of a long line, ← comes
// back, and the offset never runs past the content.
func TestLogsHorizontalScroll(t *testing.T) {
	// Wider than the 80-column terminal so the needle really is cut off.
	long := strings.Repeat("a", 100) + "NEEDLE-AT-COLUMN-100"
	m := logsModel(t, long)
	if strings.Contains(xansi.Strip(m.logsView.View()), "NEEDLE") {
		t.Fatal("precondition: the needle must be off-screen at offset 0")
	}

	// Three steps of 20 columns bring column 60 into view.
	for range 3 {
		mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = asModel(t, mi)
	}
	if m.logHOffset != 3*logHStep {
		t.Fatalf("offset=%d, want %d", m.logHOffset, 3*logHStep)
	}
	if !strings.Contains(xansi.Strip(m.logsView.View()), "NEEDLE-AT-COLUMN-100") {
		t.Fatalf("scrolling right must reveal the tail:\n%s", m.logsView.View())
	}
	if !strings.Contains(m.statusMsg, "shifted") {
		t.Fatalf("the shift must be announced, got %q", m.statusMsg)
	}

	// ← returns; the offset floors at 0 (no negative scroll).
	for range 5 {
		mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
		m = asModel(t, mi)
	}
	if m.logHOffset != 0 {
		t.Fatalf("offset must floor at 0, got %d", m.logHOffset)
	}

	// Scrolling far right stops within the content (a screen stays visible).
	for range 20 {
		mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = asModel(t, mi)
	}
	if m.logHOffset > m.logMaxWidth() {
		t.Fatalf("offset %d ran past the widest line (%d)", m.logHOffset, m.logMaxWidth())
	}
	if got := xansi.Strip(m.logsView.View()); !strings.Contains(got, "NEEDLE") {
		t.Fatalf("the far-right view must still show content:\n%s", got)
	}
}

// TestLogsWrapToggle: 'w' folds a long line onto several shorter ones, none
// wider than the terminal, and turning it on resets the horizontal offset.
func TestLogsWrapToggle(t *testing.T) {
	long := strings.Repeat("word ", 60) // ~300 cells on an 80-col terminal
	m := logsModel(t, long)
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = asModel(t, mi)
	if m.logHOffset == 0 {
		t.Fatal("precondition: the view should be shifted before wrapping")
	}

	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = asModel(t, mi)
	if !m.logWrap || m.logHOffset != 0 {
		t.Fatalf("wrap ON must reset the offset: wrap=%v offset=%d", m.logWrap, m.logHOffset)
	}
	raw := m.vpRaw[screenLogs]
	lines := strings.Split(raw, "\n")
	if len(lines) < 3 {
		t.Fatalf("a ~300-cell line must fold onto several lines, got %d", len(lines))
	}
	for _, l := range lines {
		if w := xansi.StringWidth(l); w > m.width {
			t.Fatalf("wrapped line exceeds the terminal (%d > %d): %q", w, m.width, l)
		}
	}
	// Every word survives the fold (nothing is dropped).
	if strings.Count(strings.Join(lines, " "), "word") != 60 {
		t.Fatalf("wrapping must not drop content:\n%s", raw)
	}
	// ←/→ are inert while wrapping, and say so.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = asModel(t, mi)
	if m.logHOffset != 0 || !strings.Contains(m.statusMsg, "wrap is ON") {
		t.Fatalf("scrolling must be a no-op while wrapping: offset=%d msg=%q", m.logHOffset, m.statusMsg)
	}

	// 'w' again turns it off.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = asModel(t, mi)
	if m.logWrap || !strings.Contains(m.statusMsg, "wrap OFF") {
		t.Fatalf("second 'w' must turn wrapping off, msg=%q", m.statusMsg)
	}
}

// TestLogsScrollAndWrapKeepANSIIntact: merged logs carry a colored pod
// prefix — neither the sideways shift nor the fold may cut an escape
// sequence (that bleeds color across the whole screen).
func TestLogsScrollAndWrapKeepANSIIntact(t *testing.T) {
	prefix := theme.PodPrefix("web-1").Render("[web-1]")
	line := prefix + " " + strings.Repeat("payload ", 30)
	m := logsModel(t, line)

	check := func(what string) {
		t.Helper()
		raw := m.vpRaw[screenLogs]
		// An unterminated escape sequence is the failure mode: every ESC[ we
		// emit must be closed by the reset that lipgloss appends.
		if strings.Count(raw, "\x1b[") > 0 && !strings.Contains(raw, "\x1b[0m") {
			t.Fatalf("%s: color left unterminated:\n%q", what, raw)
		}
		for _, l := range strings.Split(raw, "\n") {
			if strings.HasSuffix(xansi.Strip(l), "\x1b") {
				t.Fatalf("%s: line ends mid-escape: %q", what, l)
			}
		}
	}
	check("offset 0")

	// Shift right past the colored prefix.
	for range 2 {
		mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
		m = asModel(t, mi)
	}
	check("shifted")
	// The visible text must be the tail of the payload, prefix scrolled away.
	if got := xansi.Strip(m.vpRaw[screenLogs]); strings.Contains(got, "[web-1]") {
		t.Fatalf("the prefix should have scrolled off: %q", got)
	}

	// Wrap the colored line.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = asModel(t, mi)
	check("wrapped")
	if !strings.Contains(xansi.Strip(m.vpRaw[screenLogs]), "[web-1]") {
		t.Fatal("wrapping must bring the prefix back into view")
	}
}

// TestLogsNewLinesRespectTheMode: lines arriving while wrapping is on are
// folded too (the buffer is re-rendered, never appended raw).
func TestLogsNewLinesRespectTheMode(t *testing.T) {
	m := logsModel(t, "first")
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = asModel(t, mi)
	mi, _ = m.Update(logLineMsg{Text: strings.Repeat("late ", 60)})
	m = asModel(t, mi)
	for _, l := range strings.Split(m.vpRaw[screenLogs], "\n") {
		if w := xansi.StringWidth(l); w > m.width {
			t.Fatalf("a line arriving under wrap must be folded (%d > %d)", w, m.width)
		}
	}
}

// TestLogsClear (owner request 2026-08-06): ctrl+l drops what is buffered
// without touching the stream — new lines keep arriving afterwards.
func TestLogsClear(t *testing.T) {
	m := logsModel(t, "first line", "second line")
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = asModel(t, mi)
	if len(m.logBuf) != 0 {
		t.Fatalf("ctrl+l must empty the buffer, got %v", m.logBuf)
	}
	if got := xansi.Strip(m.logsView.View()); strings.Contains(got, "first line") {
		t.Fatalf("the view must be cleared:\n%s", got)
	}
	if !strings.Contains(m.statusMsg, "cleared") || !strings.Contains(m.statusMsg, "streaming continues") {
		t.Fatalf("clearing must say the stream is untouched, got %q", m.statusMsg)
	}
	// The stream keeps feeding the view.
	mi, _ = m.Update(logLineMsg{Text: "after the clear"})
	m = asModel(t, mi)
	if len(m.logBuf) != 1 || !strings.Contains(xansi.Strip(m.logsView.View()), "after the clear") {
		t.Fatalf("new lines must still arrive: %v", m.logBuf)
	}
	// Clearing an empty buffer is a no-op that says so.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = asModel(t, mi)
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = asModel(t, mi)
	if !strings.Contains(m.statusMsg, "already empty") {
		t.Fatalf("a second clear must be explicit, got %q", m.statusMsg)
	}
}

// TestLogsSeparator (owner request 2026-08-06): 'M' marks the current point
// in the stream with a full-width rule carrying its time.
func TestLogsSeparator(t *testing.T) {
	m := logsModel(t, "before the mark")
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	m = asModel(t, mi)
	mi, _ = m.Update(logLineMsg{Text: "after the mark"})
	m = asModel(t, mi)

	raw := xansi.Strip(m.vpRaw[screenLogs])
	lines := strings.Split(raw, "\n")
	if len(lines) != 3 {
		t.Fatalf("expected line + separator + line, got %d:\n%s", len(lines), raw)
	}
	sep := lines[1]
	if !strings.Contains(sep, "──") || strings.Contains(sep, "before") {
		t.Fatalf("the separator must be a rule of its own: %q", sep)
	}
	if len([]rune(sep)) != m.width {
		t.Fatalf("the separator must span the terminal (%d), got %d: %q", m.width, len([]rune(sep)), sep)
	}
	if lines[0] != "before the mark" || lines[2] != "after the mark" {
		t.Fatalf("the mark must sit between the two lines:\n%s", raw)
	}

	// It is stored as a sentinel, so a resize redraws it at the new width.
	mi, _ = m.Update(tea.WindowSizeMsg{Width: 50, Height: 24})
	m = asModel(t, mi)
	sep = strings.Split(xansi.Strip(m.vpRaw[screenLogs]), "\n")[1]
	if len([]rune(sep)) != 50 {
		t.Fatalf("after a resize the separator must span 50, got %d: %q", len([]rune(sep)), sep)
	}

	// Wrapping does not fold it, and scrolling sideways keeps it full width.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = asModel(t, mi)
	if sep := strings.Split(xansi.Strip(m.vpRaw[screenLogs]), "\n")[1]; len([]rune(sep)) != 50 {
		t.Fatalf("wrapped separator=%q", sep)
	}
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}}) // wrap off
	m = asModel(t, mi)
	m.logBuf = append(m.logBuf, strings.Repeat("x", 300))
	m.renderLogs()
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = asModel(t, mi)
	if sep := strings.Split(xansi.Strip(m.vpRaw[screenLogs]), "\n")[1]; len([]rune(sep)) != 50 {
		t.Fatalf("a shifted view must keep the separator full width, got %d: %q", len([]rune(sep)), sep)
	}

	// A second mark adds a second rule.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	m = asModel(t, mi)
	if n := strings.Count(xansi.Strip(m.vpRaw[screenLogs]), "──"); n < 2 {
		t.Fatalf("expected two separators, found %d", n)
	}
	// Clearing removes them too.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	m = asModel(t, mi)
	if len(m.logBuf) != 0 {
		t.Fatalf("clear must drop separators as well: %v", m.logBuf)
	}
}

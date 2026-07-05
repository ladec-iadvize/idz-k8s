package ui

import "github.com/charmbracelet/bubbles/table"

// winTable owns row windowing for a bubbles table so the visible slice is
// always known — the bubbles table never scrolls internally, which makes
// mouse clicks map to rows exactly (US7). Sync() pushes the window into the
// rendered table.
type winTable struct {
	rows   []table.Row
	cursor int
	win    int // first visible row
	height int // visible row capacity
}

func (w *winTable) SetHeight(h int) {
	if h < 1 {
		h = 1
	}
	w.height = h
	w.clamp()
}

func (w *winTable) SetRows(rows []table.Row) {
	w.rows = rows
	w.clamp()
}

func (w *winTable) Len() int { return len(w.rows) }

// Selected returns the row under the cursor.
func (w *winTable) Selected() (table.Row, bool) {
	if w.cursor < 0 || w.cursor >= len(w.rows) {
		return nil, false
	}
	return w.rows[w.cursor], true
}

// Move shifts the selection by delta rows.
func (w *winTable) Move(delta int) { w.cursor += delta; w.clamp() }

// Page shifts by one window height (dir = ±1).
func (w *winTable) Page(dir int) { w.Move(dir * w.height) }

func (w *winTable) Home() { w.cursor = 0; w.clamp() }
func (w *winTable) End()  { w.cursor = len(w.rows) - 1; w.clamp() }

// ClickVisible selects the row at a window-relative position (0 = first
// visible row). Returns false when the position has no row.
func (w *winTable) ClickVisible(rel int) bool {
	idx := w.win + rel
	if rel < 0 || rel >= w.height || idx < 0 || idx >= len(w.rows) {
		return false
	}
	w.cursor = idx
	w.clamp()
	return true
}

// Range reports the visible span for the footer: [from, to] over total (1-based).
func (w *winTable) Range() (from, to, total int) {
	if len(w.rows) == 0 {
		return 0, 0, 0
	}
	end := w.win + w.height
	if end > len(w.rows) {
		end = len(w.rows)
	}
	return w.win + 1, end, len(w.rows)
}

// Sync renders the current window into the bubbles table.
func (w *winTable) Sync(t *table.Model) {
	end := w.win + w.height
	if end > len(w.rows) {
		end = len(w.rows)
	}
	if w.win > end {
		w.win = end
	}
	t.SetRows(w.rows[w.win:end])
	t.SetCursor(w.cursor - w.win)
}

func (w *winTable) clamp() {
	if len(w.rows) == 0 {
		w.cursor, w.win = 0, 0
		return
	}
	if w.cursor < 0 {
		w.cursor = 0
	}
	if w.cursor >= len(w.rows) {
		w.cursor = len(w.rows) - 1
	}
	if w.height < 1 {
		w.height = 1
	}
	// Window follows the cursor.
	if w.cursor < w.win {
		w.win = w.cursor
	}
	if w.cursor >= w.win+w.height {
		w.win = w.cursor - w.height + 1
	}
	if maxWin := len(w.rows) - w.height; w.win > maxWin {
		w.win = maxWin
	}
	if w.win < 0 {
		w.win = 0
	}
}

package ui

// House-style table core shared by the usage ('u') and sizing ('z') views:
// column model, content-driven width resolution (fitColumns), click→column
// mapping and the renderer (styled header with sort arrows, right alignment,
// ANSI-aware padding). listview.go keeps its own machinery (mark column,
// custom columns).

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// houseColumn describes one column of a house-style table (usage, sizing).
// Widths are content-driven (houseWidths/fitColumns).
type houseColumn[R any] struct {
	title string
	right bool
	cell  func(*Model, R) string
	less  func(a, b R) bool
}

// houseWidths resolves the column widths from the actual content: each
// column is as wide as its widest visible cell (ANSI-aware — gauges carry
// color codes), shrinking proportionally under a narrow terminal. sortCol
// reserves room for the active column's sort arrow (-1 = none).
func houseWidths[R any](m *Model, cols []houseColumn[R], rows []R, sortCol int) []int {
	needs := make([]int, len(cols))
	mins := make([]int, len(cols))
	for i, c := range cols {
		titleW := len([]rune(c.title))
		if i == sortCol {
			titleW += 2 // sort arrow (" ↑"/" ↓")
		}
		n := titleW
		for _, r := range rows {
			if l := lipgloss.Width(c.cell(m, r)); l > n {
				n = l
			}
		}
		if n < 4 {
			n = 4
		}
		needs[i] = n
		mins[i] = colMin(titleW)
	}
	return fitColumns(needs, mins, m.width-len(cols))
}

// houseColumnAt maps a header click x to a column index (1-column separators).
func houseColumnAt(widths []int, x int) (int, bool) {
	pos := 0
	for i, w := range widths {
		if x >= pos && x < pos+w {
			return i, true
		}
		pos += w + 1
	}
	return 0, false
}

// sortArrowTitle appends the sort arrow to the active sort column's title.
func sortArrowTitle(title string, active, asc bool) string {
	if !active {
		return title
	}
	if asc {
		return title + " ↑"
	}
	return title + " ↓"
}

// houseTableView renders a house-style table: styled header with the sort
// arrow on the active column, the visible window with right alignment and
// ANSI-aware padding, a faint empty-state line, padded to full body height.
func houseTableView[R any](m *Model, cols []houseColumn[R], widths []int, rows []R,
	win *winTable, sortCol int, sortAsc bool, empty string) string {
	var b strings.Builder
	head := ""
	for i, c := range cols {
		title := sortArrowTitle(c.title, sortCol == i, sortAsc)
		cell := padTo(title, widths[i])
		if c.right {
			cell = padLeft(title, widths[i])
		}
		head += cell + " "
	}
	b.WriteString(m.theme.TableHeader.Render(padTo(head, m.width)))
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(m.theme.Faint.Render(empty))
		b.WriteString("\n")
	}
	from := win.win
	to := from + win.height
	if to > len(rows) {
		to = len(rows)
	}
	for i := from; i < to; i++ {
		r := rows[i]
		line := ""
		for j, c := range cols {
			raw := c.cell(m, r)
			var cell string
			switch {
			case c.right:
				cell = padLeft(raw, widths[j])
			case strings.Contains(raw, "\x1b"):
				cell = padTo2(raw, widths[j]) // styled: pad without truncating
			default:
				cell = padTo(raw, widths[j])
			}
			line += cell + " "
		}
		if i == win.cursor {
			line = m.theme.TableSelected.Render(padTo2(line, m.width))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	for i := to - from; i < win.height; i++ {
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

package ui

// House-style table core shared by the usage ('u') and sizing ('z') views:
// column model, width resolution (one flex column absorbing the leftover,
// capped to its content), click→column mapping and the renderer (styled
// header with sort arrows, right alignment, ANSI-aware padding).
// listview.go keeps its own machinery (mark column, custom columns).

import "strings"

// houseColumn describes one column of a house-style table (usage, sizing).
type houseColumn[R any] struct {
	title string
	width int // 0 = the flex column absorbing leftover width
	right bool
	cell  func(*Model, R) string
	less  func(a, b R) bool
}

// houseWidths resolves the column widths: fixed columns keep their declared
// width, the flex column (width 0, minimum flexMin) absorbs the leftover
// terminal width, capped to its longest content+2.
func houseWidths[R any](m *Model, cols []houseColumn[R], flexMin int, rows []R) []int {
	widths := make([]int, len(cols))
	fixed := 0
	flexIdx := -1
	for i, c := range cols {
		w := c.width
		if w == 0 {
			flexIdx, w = i, flexMin
		}
		widths[i] = w
		fixed += w
	}
	if flexIdx >= 0 {
		if extra := m.width - fixed - len(cols); extra > 0 {
			content := len([]rune(cols[flexIdx].title))
			for _, r := range rows {
				if l := len([]rune(cols[flexIdx].cell(m, r))); l > content {
					content = l
				}
			}
			if need := content + 2 - widths[flexIdx]; need < extra {
				if need < 0 {
					need = 0
				}
				extra = need
			}
			widths[flexIdx] += extra
		}
	}
	return widths
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

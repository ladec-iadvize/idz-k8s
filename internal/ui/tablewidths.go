package ui

// Content-driven column sizing (owner requests 2026-07-24 and 2026-07-28
// "fill the screen"): every table column gets at least the width its content
// needs — long cells (node names, images, hosts…) are no longer truncated
// while the terminal has room. When the terminal is WIDER than the content,
// the leftover is spread across every column proportionally to its natural
// width, so the table spans the full screen (k9s-style) instead of leaving a
// dead right margin — and no single column opens a desert (owner report
// 2026-07-10). When it is too narrow, columns shrink proportionally down to
// a readable minimum instead of overflowing.

// fitColumns resolves the final widths. needs[i] is the width column i wants
// (longest content, title included); mins[i] the width below which it should
// never shrink; avail the space the columns must fill together (separators
// already subtracted by the caller).
func fitColumns(needs, mins []int, avail int) []int {
	total := 0
	for _, n := range needs {
		total += n
	}
	if total <= avail {
		// Wider than the content: stretch every column proportionally so the
		// table fills the terminal (extra space becomes even breathing room,
		// never one giant gap).
		out := make([]int, len(needs))
		copy(out, needs)
		if extra := avail - total; extra > 0 && total > 0 {
			given := 0
			for i, n := range needs {
				g := extra * n / total
				out[i] += g
				given += g
			}
			// Rounding remainder: one cell at a time, widest first.
			for i := 0; given < extra && len(out) > 0; i = (i + 1) % len(out) {
				out[i]++
				given++
			}
		}
		return out
	}
	minTotal := 0
	for _, w := range mins {
		minTotal += w
	}
	if avail <= minTotal {
		// Even the minimums overflow: the per-line padTo(m.width) truncation
		// keeps the frame intact.
		out := make([]int, len(mins))
		copy(out, mins)
		return out
	}
	// Shrink each column proportionally to what it can give up.
	deficit := total - avail
	shrinkable := total - minTotal
	out := make([]int, len(needs))
	taken := 0
	for i, n := range needs {
		cut := deficit * (n - mins[i]) / shrinkable
		out[i] = n - cut
		taken += cut
	}
	// Integer rounding leaves a remainder: take it from the widest columns.
	for taken < deficit {
		widest := -1
		for i, w := range out {
			if w > mins[i] && (widest < 0 || w > out[widest]) {
				widest = i
			}
		}
		if widest < 0 {
			break
		}
		out[widest]--
		taken++
	}
	return out
}

// colMin bounds how far a column may shrink: its title stays readable up to
// 12 runes (longer headers may truncate under pressure), never below 4.
func colMin(titleW int) int {
	if titleW > 12 {
		return 12
	}
	if titleW < 4 {
		return 4
	}
	return titleW
}

package ui

// Content-driven column sizing (owner request 2026-07-24 "fill the screen"):
// every table column gets the width its content actually needs — long cells
// (node names, images, hosts…) are no longer truncated while the terminal
// has room, and declared widths no longer pad short content with dead space.
// When the terminal is too narrow for everything, columns shrink
// proportionally down to a readable minimum instead of overflowing.

// fitColumns resolves the final widths. needs[i] is the width column i wants
// (longest content, title included); mins[i] the width below which it should
// never shrink; avail the space left for the columns themselves (separators
// already subtracted by the caller).
func fitColumns(needs, mins []int, avail int) []int {
	total := 0
	for _, n := range needs {
		total += n
	}
	if total <= avail {
		return needs
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

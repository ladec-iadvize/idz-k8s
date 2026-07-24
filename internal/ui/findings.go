package ui

// Shared machinery of the grouped findings views (diagnostics 'f' and
// posture 'p'): keyboard navigation (Enter opens the referenced object,
// 'w' toggles errors-only, ↑/↓ move the selection), the errors-only filter
// with selection clamping, and the grouped renderer that tracks each
// finding's line for mouse clicks. The per-view glue (grouping rules,
// titles, footers) stays in render.go / advisory.go.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/model"
)

// findingsNav wires one findings view's state into the shared key handler.
type findingsNav struct {
	sel      *int
	refs     []objRef
	errOnly  *bool
	rerender func()
	vp       *viewport.Model
}

// handleFindingsKey handles the keys shared by the diag and posture views.
func (m *Model) handleFindingsKey(msg tea.KeyMsg, f findingsNav) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Open):
		if *f.sel >= 0 && *f.sel < len(f.refs) {
			return m.openDescribeRef(f.refs[*f.sel])
		}
		return *m, nil
	case hit(msg, m.keys.WarnOnly):
		*f.errOnly = !*f.errOnly
		*f.sel = 0
		f.rerender()
		return *m, nil
	}
	switch msg.Type {
	case tea.KeyUp:
		if *f.sel > 0 {
			*f.sel--
			f.rerender()
		}
		return *m, nil
	case tea.KeyDown:
		if *f.sel < len(f.refs)-1 {
			*f.sel++
			f.rerender()
		}
		return *m, nil
	}
	var cmd tea.Cmd
	*f.vp, cmd = f.vp.Update(msg)
	return *m, cmd
}

// filterFindings applies the errors-only toggle and clamps the selection to
// the remaining rows.
func filterFindings[F any](rows []F, errOnly bool, level func(F) model.HealthLevel, sel *int) []F {
	if errOnly {
		kept := make([]F, 0, len(rows))
		for _, f := range rows {
			if level(f) >= model.HealthError {
				kept = append(kept, f)
			}
		}
		rows = kept
	}
	if *sel >= len(rows) {
		*sel = len(rows) - 1
	}
	if *sel < 0 {
		*sel = 0
	}
	return rows
}

// findingItem is one selectable finding line.
type findingItem struct {
	level  model.HealthLevel
	ref    objRef // the object Enter/click opens
	who    string // ns/name [container]
	detail string
}

// findingGroup is one section header with its findings, already ordered.
type findingGroup struct {
	title string
	items []findingItem
}

// findingWhoWidth sizes the findings' object column with the terminal
// (~1/3 of the width) instead of a fixed 45/52 (content-driven layout, v3).
func (m *Model) findingWhoWidth() int {
	return clampW(m.width/3, 34, 64)
}

// clampW bounds a computed width to [lo, hi].
func clampW(w, lo, hi int) int {
	if w < lo {
		return lo
	}
	if w > hi {
		return hi
	}
	return w
}

// renderFindingGroups writes the grouped findings — a rule() header per
// group, severity-colored lines, the selection highlighted — and records
// each finding's ref and content line for Enter/mouse clicks.
func (m *Model) renderFindingGroups(b *strings.Builder, groups []findingGroup,
	sel, whoWidth int, refs *[]objRef, lines *[]int) {
	*refs, *lines = nil, nil
	idx := 0
	for _, g := range groups {
		b.WriteString(m.rule(fmt.Sprintf("%s (%d)", g.title, len(g.items))) + "\n")
		for _, it := range g.items {
			*refs = append(*refs, it.ref)
			*lines = append(*lines, strings.Count(b.String(), "\n"))
			line := fmt.Sprintf("  %s %-*s %s", it.level.Symbol(), whoWidth, truncate(it.who, whoWidth), it.detail)
			switch {
			case idx == sel:
				line = m.theme.TableSelected.Render(padTo2(line, m.width))
			case it.level == model.HealthError:
				line = m.theme.Error.Render(line)
			case it.level == model.HealthWarning:
				line = m.theme.Warning.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
			idx++
		}
		b.WriteString("\n")
	}
}

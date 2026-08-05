package ui

// Containers view — the leaf of the drill chain (Enter on a pod, owner
// request 2026-07-31). Same house-table look as usage/sizing: content-driven
// widths, s/S sorting, header-click sort, whole-row health coloring. Enter
// opens THAT container's logs; the actions palette shells into it.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// openContainers shows the selected pod's containers.
func (m *Model) openContainers(pod model.ResourceObject) tea.Cmd {
	m.containerPod = pod
	m.containerRows = kube.PodContainers(pod.Raw)
	m.containerSortCol, m.containerSortAsc = -1, true
	m.containerWin.Home()
	m.applyContainerRows()
	m.screen = screenContainers
	m.layout()
	if len(m.containerRows) == 0 {
		m.statusMsg = "Pod/" + pod.Name + " declares no container — Esc to go back"
		return nil
	}
	m.statusMsg = fmt.Sprintf("%d container(s) of Pod/%s — Enter: logs · 'a': shell · Esc back",
		len(m.containerRows), pod.Name)
	// Refresh the pod so container states are live, not the list snapshot.
	cl, t, ns, name := m.client, m.curType, pod.Namespace, pod.Name
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		obj, err := cl.GetObject(ctx, t, ns, name)
		if err != nil {
			return errMsg{err: err}
		}
		return containersMsg{pod: obj}
	}
}

// containerColumns describes the table (widths are content-driven).
func (m *Model) containerColumns() []houseColumn[model.Container] {
	return []houseColumn[model.Container]{
		{title: "NAME", cell: func(_ *Model, c model.Container) string {
			if c.Init {
				return c.Name + " (init)"
			}
			return c.Name
		}, less: func(a, b model.Container) bool { return a.Name < b.Name }},
		{title: "READY", cell: func(m *Model, c model.Container) string {
			if c.Ready {
				return m.theme.Ok.Render("✓")
			}
			return m.theme.Error.Render("✗")
		}, less: func(a, b model.Container) bool { return !a.Ready && b.Ready }},
		{title: "STATE", cell: func(_ *Model, c model.Container) string {
			// statusCell keeps the REAL state text (symbol + reason), like the
			// main list — theme.Status would flatten it to "error"/"warning".
			return statusCell(model.StatusSummary{Level: c.Level, Reason: c.State})
		}, less: func(a, b model.Container) bool {
			if a.Level != b.Level {
				return a.Level < b.Level
			}
			return a.State < b.State
		}},
		{title: "RESTARTS", right: true, cell: func(_ *Model, c model.Container) string {
			return fmt.Sprintf("%d", c.Restarts)
		}, less: func(a, b model.Container) bool { return a.Restarts < b.Restarts }},
		// WHY the previous run ended — an OOMKill is invisible in STATE once
		// the container is Running again (owner request 2026-08-05).
		{title: "LAST TERMINATION", cell: func(m *Model, c model.Container) string {
			if c.LastTerminated == "" {
				return "—"
			}
			label := c.LastTerminated
			if !c.LastTerminatedAt.IsZero() {
				label += " · " + kube.Age(c.LastTerminatedAt, m.now()) + " ago"
			}
			if strings.Contains(c.LastTerminated, "OOMKilled") {
				return m.theme.Error.Render(label)
			}
			if strings.Contains(c.LastTerminated, "Completed") {
				return m.theme.Faint.Render(label)
			}
			return m.theme.Warning.Render(label)
		}, less: func(a, b model.Container) bool { return a.LastTerminated < b.LastTerminated }},
		{title: "IMAGE", cell: func(_ *Model, c model.Container) string { return orDash(c.Image) },
			less: func(a, b model.Container) bool { return a.Image < b.Image }},
	}
}

// applyContainerRows feeds the windowed table (sort applied).
func (m *Model) applyContainerRows() {
	cols := m.containerColumns()
	if m.containerSortCol >= 0 && m.containerSortCol < len(cols) {
		less := cols[m.containerSortCol].less
		if less != nil {
			sort.SliceStable(m.containerRows, func(i, j int) bool {
				if m.containerSortAsc {
					return less(m.containerRows[i], m.containerRows[j])
				}
				return less(m.containerRows[j], m.containerRows[i])
			})
		}
	}
	// The window only tracks the cursor/visible span; the cells come from
	// containerRows (house-table convention).
	m.containerWin.SetRows(make([]table.Row, len(m.containerRows)))
}

// containersView renders the table.
func (m Model) containersView() string {
	cols := m.containerColumns()
	widths := houseWidths(&m, cols, m.containerRows, m.containerSortCol)
	return houseTableView(&m, cols, widths, m.containerRows,
		&m.containerWin, m.containerSortCol, m.containerSortAsc,
		" this pod declares no container")
}

// containerColumnAt maps a header click to a column index.
func (m *Model) containerColumnAt(x int) (int, bool) {
	return houseColumnAt(houseWidths(m, m.containerColumns(), m.containerRows, m.containerSortCol), x)
}

// selectedContainer returns the container under the cursor.
func (m *Model) selectedContainer() (model.Container, bool) {
	i := m.containerWin.cursor
	if i < 0 || i >= len(m.containerRows) {
		return model.Container{}, false
	}
	return m.containerRows[i], true
}

// handleContainersKey: Enter opens the container's logs, 'a' its actions,
// s/S sort, navigation as everywhere else.
func (m Model) handleContainersKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Open) || hit(msg, m.keys.Logs):
		return m.openContainerLogs()
	case hit(msg, m.keys.Actions):
		return m.openActions()
	case hit(msg, m.keys.Sort):
		m.containerSortCol++
		if m.containerSortCol >= len(m.containerColumns()) {
			m.containerSortCol = -1 // back to declaration order
		}
		m.containerSortAsc = true
		m.applyContainerRows()
		return m, nil
	case hit(msg, m.keys.SortDir):
		if m.containerSortCol >= 0 {
			m.containerSortAsc = !m.containerSortAsc
			m.applyContainerRows()
		}
		return m, nil
	}
	m.navigate(&m.containerWin, msg)
	return m, nil
}

// openContainerLogs streams the selected container's logs.
func (m Model) openContainerLogs() (tea.Model, tea.Cmd) {
	c, ok := m.selectedContainer()
	if !ok {
		return m, nil
	}
	pod := m.containerPod
	if m.logCancel != nil {
		m.logCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.logCh = m.client.StreamPodLogs(ctx, pod.Namespace, pod.Name, c.Name, 200, true)
	m.logCancel = cancel
	m.logBuf = nil
	m.logPaused = false
	m.statusMsg = "logs: " + pod.Name + " › " + c.Name
	m.setContent(screenLogs, "waiting for logs…")
	m.screen = screenLogs
	m.logsFrom = screenContainers // Esc returns to the containers list
	m.layout()
	return m, m.nextLogLine()
}

// containerActions are the admin actions of the containers view: a shell in
// THIS container (the palette's single entry point stays consistent).
func (m *Model) containerActions() ([]actionEntry, string) {
	c, ok := m.selectedContainer()
	if !ok {
		return nil, ""
	}
	pod := m.containerPod
	label := "Pod/" + pod.Name + " › " + c.Name
	name := c.Name
	return []actionEntry{
		{"shell", "interactive shell in " + label, func(m *Model) (tea.Model, tea.Cmd) {
			return m.openShellIn(pod, name)
		}},
		{"logs", "stream the logs of " + label, func(m *Model) (tea.Model, tea.Cmd) {
			return m.openContainerLogs()
		}},
	}, label
}

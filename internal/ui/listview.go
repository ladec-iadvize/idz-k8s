package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/table"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// listColumn is one type-aware column of the main list. width 0 = flexible
// (absorbs the remaining terminal width).
type listColumn struct {
	title string
	width int
	cell  func(m *Model, o model.ResourceObject) string
	less  func(a, b model.ResourceObject) bool // optional custom sort
}

// columnsForType returns the columns that make sense for the type being
// browsed — kubectl-like, instead of a one-size-fits-all layout (the mark
// column is implicit at index 0).
func (m *Model) columnsForType() []listColumn {
	name := listColumn{title: "NAME", width: 0,
		cell: func(_ *Model, o model.ResourceObject) string { return o.Name },
		less: func(a, b model.ResourceObject) bool { return a.Name < b.Name }}
	ns := listColumn{title: "NAMESPACE", width: 28,
		cell: func(_ *Model, o model.ResourceObject) string { return o.Namespace },
		less: func(a, b model.ResourceObject) bool {
			if a.Namespace != b.Namespace {
				return a.Namespace < b.Namespace
			}
			return a.Name < b.Name
		}}
	status := listColumn{title: "STATUS", width: 18,
		cell: func(m *Model, o model.ResourceObject) string {
			st := o.Status
			if r, d, ok := kube.ReadyCount(m.curType.Kind, o.Raw); ok && d > 0 && r < d {
				lvl := model.HealthWarning
				if float64(r)/float64(d) <= 0.6 {
					lvl = model.HealthError
				}
				if lvl > st.Level {
					st = model.StatusSummary{Level: lvl, Reason: fmt.Sprintf("%d/%d ready", r, d)}
				}
			}
			return statusCell(st)
		},
		less: func(a, b model.ResourceObject) bool {
			if a.Status.Level != b.Status.Level {
				return a.Status.Level < b.Status.Level
			}
			return a.Status.Reason < b.Status.Reason
		}}
	age := listColumn{title: "AGE", width: 8,
		cell: func(m *Model, o model.ResourceObject) string { return kube.Age(o.CreatedAt, m.now()) },
		less: func(a, b model.ResourceObject) bool { return a.CreatedAt.After(b.CreatedAt) }}
	ready := listColumn{title: "READY", width: 9,
		cell: func(m *Model, o model.ResourceObject) string {
			if r, d, ok := kube.ReadyCount(m.curType.Kind, o.Raw); ok {
				return fmt.Sprintf("%d/%d", r, d)
			}
			return "-"
		},
		less: func(a, b model.ResourceObject) bool {
			ra, da, _ := kube.ReadyCount("", nil) // placeholders; replaced below
			_ = ra
			_ = da
			return false
		}}
	// readiness sort needs the kind; bind it here.
	kind := m.curType.Kind
	ready.less = func(a, b model.ResourceObject) bool {
		ra, da, _ := kube.ReadyCount(kind, a.Raw)
		rb, db, _ := kube.ReadyCount(kind, b.Raw)
		fa, fb := readyFrac(ra, da), readyFrac(rb, db)
		if fa != fb {
			return fa < fb
		}
		return a.Name < b.Name
	}

	switch {
	case strings.EqualFold(kind, "Pod"):
		restarts := listColumn{title: "RESTARTS", width: 9,
			cell: func(_ *Model, o model.ResourceObject) string {
				if r := kube.PodRestarts(o.Raw); r > 0 {
					return fmt.Sprintf("%d", r)
				}
				return "0"
			},
			less: func(a, b model.ResourceObject) bool { return kube.PodRestarts(a.Raw) < kube.PodRestarts(b.Raw) }}
		node := listColumn{title: "NODE", width: 24,
			cell: func(_ *Model, o model.ResourceObject) string { return orDash(kube.PodNode(o.Raw)) },
			less: func(a, b model.ResourceObject) bool { return kube.PodNode(a.Raw) < kube.PodNode(b.Raw) }}
		return []listColumn{ns, name, ready, restarts, node, status, age}

	case strings.EqualFold(kind, "Node"):
		pods := listColumn{title: "PODS READY", width: 11,
			cell: func(m *Model, o model.ResourceObject) string {
				c, ok := m.nodePods[o.Name]
				if !ok {
					return "-"
				}
				return fmt.Sprintf("%d/%d", c[0], c[1])
			},
			less: func(a, b model.ResourceObject) bool {
				ca, cb := m.nodePods[a.Name], m.nodePods[b.Name]
				return readyFrac(ca[0], ca[1]) < readyFrac(cb[0], cb[1])
			}}
		instance := listColumn{title: "INSTANCE", width: 14,
			cell: func(_ *Model, o model.ResourceObject) string {
				return orDash(kube.ObjectLabel(o.Raw, "node.kubernetes.io/instance-type", "beta.kubernetes.io/instance-type"))
			},
			less: func(a, b model.ResourceObject) bool {
				return kube.ObjectLabel(a.Raw, "node.kubernetes.io/instance-type") < kube.ObjectLabel(b.Raw, "node.kubernetes.io/instance-type")
			}}
		pool := listColumn{title: "NODEPOOL", width: 18,
			cell: func(_ *Model, o model.ResourceObject) string {
				return orDash(kube.ObjectLabel(o.Raw, "karpenter.sh/nodepool", "karpenter.sh/provisioner-name", "eks.amazonaws.com/nodegroup"))
			},
			less: func(a, b model.ResourceObject) bool {
				return kube.ObjectLabel(a.Raw, "karpenter.sh/nodepool", "karpenter.sh/provisioner-name") <
					kube.ObjectLabel(b.Raw, "karpenter.sh/nodepool", "karpenter.sh/provisioner-name")
			}}
		return []listColumn{name, pods, instance, pool, status, age}

	case strings.EqualFold(kind, "HorizontalPodAutoscaler"):
		targets := listColumn{title: "TARGETS", width: 22,
			cell: func(_ *Model, o model.ResourceObject) string {
				_, _, _, _, t := kube.HPAInfo(o.Raw)
				return t
			}}
		minC := listColumn{title: "MIN", width: 5,
			cell: func(_ *Model, o model.ResourceObject) string {
				mn, _, _, _, _ := kube.HPAInfo(o.Raw)
				return fmt.Sprintf("%d", mn)
			}}
		maxC := listColumn{title: "MAX", width: 5,
			cell: func(_ *Model, o model.ResourceObject) string {
				_, mx, _, _, _ := kube.HPAInfo(o.Raw)
				return fmt.Sprintf("%d", mx)
			}}
		repl := listColumn{title: "REPLICAS", width: 9,
			cell: func(_ *Model, o model.ResourceObject) string {
				_, _, cur, des, _ := kube.HPAInfo(o.Raw)
				if des != 0 && des != cur {
					return fmt.Sprintf("%d→%d", cur, des)
				}
				return fmt.Sprintf("%d", cur)
			}}
		return []listColumn{ns, name, targets, minC, maxC, repl, age}

	case strings.EqualFold(kind, "Service"):
		svcType := listColumn{title: "TYPE", width: 13,
			cell: func(_ *Model, o model.ResourceObject) string {
				t, _, _ := unstructuredString(o.Raw, "spec", "type")
				if t == "" {
					t = "ClusterIP"
				}
				return t
			}}
		return []listColumn{ns, name, svcType, status, age}

	default:
		cols := []listColumn{}
		if m.curType.Namespaced {
			cols = append(cols, ns)
		}
		cols = append(cols, name)
		// READY only where the kind actually has the notion.
		switch kind {
		case "Deployment", "StatefulSet", "ReplicaSet", "DaemonSet":
			cols = append(cols, ready)
		}
		return append(cols, status, age)
	}
}

// rowHealth decides the whole-row color: full readiness → normal, partially
// ready → yellow, ≤60% ready → red (user rule); an Error status stays red.
func (m *Model) rowHealth(o model.ResourceObject) model.HealthLevel {
	lvl := o.Status.Level
	if r, d, ok := kube.ReadyCount(m.curType.Kind, o.Raw); ok && d > 0 {
		frac := float64(r) / float64(d)
		switch {
		case frac <= 0.6 && lvl < model.HealthError:
			return model.HealthError
		case frac < 1 && lvl < model.HealthWarning:
			return model.HealthWarning
		}
	}
	if strings.EqualFold(m.curType.Kind, "Node") {
		if c, ok := m.nodePods[o.Name]; ok && c[1] > 0 {
			frac := float64(c[0]) / float64(c[1])
			switch {
			case frac <= 0.6 && lvl < model.HealthError:
				return model.HealthError
			case frac < 1 && lvl < model.HealthWarning:
				return model.HealthWarning
			}
		}
	}
	return lvl
}

// listWidths resolves the widths of the current columns (mark col included at
// index 0; the flexible column absorbs the remainder).
func (m *Model) listWidths(cols []listColumn) []int {
	widths := make([]int, 0, len(cols)+1)
	widths = append(widths, 2) // mark
	fixed := 2
	flexIdx := -1
	for i, c := range cols {
		w := c.width
		if w == 0 {
			flexIdx = i + 1
			w = 20
		}
		widths = append(widths, w)
		fixed += w
	}
	if flexIdx >= 0 {
		if extra := m.width - fixed - len(cols); extra > 0 {
			widths[flexIdx] += extra
		}
	}
	return widths
}

// applyRows rebuilds the visible rows from m.objects: filter, sort, format
// the type-aware cells, and remember each row's health for coloring.
func (m *Model) applyRows() {
	cols := m.columnsForType()
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))

	objs := m.objects
	if m.sortCol >= 1 && m.sortCol <= len(cols) {
		objs = make([]model.ResourceObject, len(m.objects))
		copy(objs, m.objects)
		c := cols[m.sortCol-1]
		less := c.less
		if less == nil {
			less = func(a, b model.ResourceObject) bool { return c.cell(m, a) < c.cell(m, b) }
		}
		sort.SliceStable(objs, func(i, j int) bool {
			if m.sortAsc {
				return less(objs[i], objs[j])
			}
			return less(objs[j], objs[i])
		})
	}

	rows := make([]table.Row, 0, len(objs))
	levels := make([]model.HealthLevel, 0, len(objs))
	for _, o := range objs {
		if q != "" && !strings.Contains(strings.ToLower(o.Namespace+"/"+o.Name), q) {
			continue
		}
		mark := " "
		if _, ok := m.marked[o.Namespace+"/"+o.Name]; ok {
			mark = "●"
		}
		row := table.Row{mark}
		for _, c := range cols {
			row = append(row, c.cell(m, o))
		}
		rows = append(rows, row)
		levels = append(levels, m.rowHealth(o))
	}
	m.rowLevels = levels
	m.win.SetRows(rows)
}

// listView renders the type-aware list: styled header with sort arrows, then
// the visible window with whole-row health coloring and a background-
// highlighted selection.
func (m Model) listView() string {
	cols := m.columnsForType()
	widths := m.listWidths(cols)

	var b strings.Builder
	// Header.
	head := padTo("", widths[0])
	for i, c := range cols {
		title := c.title
		if m.sortCol == i+1 {
			if m.sortAsc {
				title += " ↑"
			} else {
				title += " ↓"
			}
		}
		head += " " + padTo(title, widths[i+1])
	}
	b.WriteString(m.theme.TableHeader.Render(padTo(head, m.width)))
	b.WriteString("\n")

	// Visible rows.
	from := m.win.win
	to := from + m.win.height
	if to > m.win.Len() {
		to = m.win.Len()
	}
	for i := from; i < to; i++ {
		row := m.win.rows[i]
		line := padTo(row[0], widths[0])
		for j := 1; j < len(row) && j < len(widths); j++ {
			line += " " + padTo(row[j], widths[j])
		}
		line = padTo(line, m.width)
		switch {
		case i == m.win.cursor:
			line = m.theme.TableSelected.Render(line)
		case i < len(m.rowLevels) && m.rowLevels[i] == model.HealthError:
			line = m.theme.Error.Render(line)
		case i < len(m.rowLevels) && m.rowLevels[i] == model.HealthWarning:
			line = m.theme.Warning.Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	// Pad to full body height so footer geometry stays fixed.
	for i := to - from; i < m.win.height; i++ {
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func unstructuredString(raw map[string]interface{}, fields ...string) (string, bool, error) {
	cur := raw
	for i, f := range fields {
		if i == len(fields)-1 {
			s, _ := cur[f].(string)
			return s, s != "", nil
		}
		next, ok := cur[f].(map[string]interface{})
		if !ok {
			return "", false, nil
		}
		cur = next
	}
	return "", false, nil
}

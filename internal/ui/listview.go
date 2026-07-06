package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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

// columnsForType returns the columns actually displayed: the type's base set,
// filtered and reordered by the user's saved arrangement when one exists
// (US8). Unknown titles in the pref are dropped; a pref that matches nothing
// falls back to the defaults (FR-025 tolerance).
func (m *Model) columnsForType() []listColumn {
	cols := m.columnsBase()
	pref := m.cfg.ViewPrefs[m.curType.Key()]
	if len(pref.Columns) == 0 {
		return cols
	}
	byTitle := make(map[string]listColumn, len(cols))
	for _, c := range cols {
		byTitle[c.title] = c
	}
	out := make([]listColumn, 0, len(pref.Columns))
	for _, t := range pref.Columns {
		if c, ok := byTitle[t]; ok {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return cols
	}
	return out
}

// columnsBase returns every column the type offers, in default order —
// kubectl-like, instead of a one-size-fits-all layout (the mark column is
// implicit at index 0).
func (m *Model) columnsBase() []listColumn {
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

	case strings.EqualFold(kind, "Ingress"):
		class := listColumn{title: "CLASS", width: 14,
			cell: func(_ *Model, o model.ResourceObject) string {
				c, _, _ := unstructured.NestedString(o.Raw, "spec", "ingressClassName")
				return orDash(c)
			}}
		hosts := listColumn{title: "HOSTS", width: 42,
			cell: func(_ *Model, o model.ResourceObject) string {
				rules, _, _ := unstructured.NestedSlice(o.Raw, "spec", "rules")
				var hs []string
				for _, r := range rules {
					if rm, ok := r.(map[string]interface{}); ok {
						if h, _ := rm["host"].(string); h != "" {
							hs = append(hs, h)
						}
					}
				}
				if len(hs) == 0 {
					return "*"
				}
				out := strings.Join(hs[:min(2, len(hs))], ",")
				if len(hs) > 2 {
					out += fmt.Sprintf(" +%d", len(hs)-2)
				}
				return out
			}}
		return []listColumn{ns, name, class, hosts, status, age}

	case strings.EqualFold(kind, "PersistentVolumeClaim"):
		capa := listColumn{title: "CAPACITY", width: 9,
			cell: func(_ *Model, o model.ResourceObject) string {
				c, _, _ := unstructured.NestedString(o.Raw, "status", "capacity", "storage")
				return orDash(c)
			}}
		class := listColumn{title: "STORAGECLASS", width: 16,
			cell: func(_ *Model, o model.ResourceObject) string {
				c, _, _ := unstructured.NestedString(o.Raw, "spec", "storageClassName")
				return orDash(c)
			}}
		return []listColumn{ns, name, capa, class, status, age}

	case strings.EqualFold(kind, "PersistentVolume"):
		capa := listColumn{title: "CAPACITY", width: 9,
			cell: func(_ *Model, o model.ResourceObject) string {
				c, _, _ := unstructured.NestedString(o.Raw, "spec", "capacity", "storage")
				return orDash(c)
			}}
		claim := listColumn{title: "CLAIM", width: 34,
			cell: func(_ *Model, o model.ResourceObject) string {
				cns, _, _ := unstructured.NestedString(o.Raw, "spec", "claimRef", "namespace")
				cn, _, _ := unstructured.NestedString(o.Raw, "spec", "claimRef", "name")
				if cn == "" {
					return "-"
				}
				return cns + "/" + cn
			}}
		class := listColumn{title: "STORAGECLASS", width: 16,
			cell: func(_ *Model, o model.ResourceObject) string {
				c, _, _ := unstructured.NestedString(o.Raw, "spec", "storageClassName")
				return orDash(c)
			}}
		return []listColumn{name, capa, claim, class, status, age}

	case strings.EqualFold(kind, "Job"):
		compl := listColumn{title: "COMPLETIONS", width: 12,
			cell: func(_ *Model, o model.ResourceObject) string {
				want := int64(1)
				if v, found, _ := unstructured.NestedInt64(o.Raw, "spec", "completions"); found {
					want = v
				}
				done, _, _ := unstructured.NestedInt64(o.Raw, "status", "succeeded")
				return fmt.Sprintf("%d/%d", done, want)
			}}
		dur := listColumn{title: "DURATION", width: 9,
			cell: func(m *Model, o model.ResourceObject) string {
				start, _, _ := unstructured.NestedString(o.Raw, "status", "startTime")
				end, _, _ := unstructured.NestedString(o.Raw, "status", "completionTime")
				return jobDuration(start, end, m.now())
			}}
		return []listColumn{ns, name, compl, dur, status, age}

	case strings.EqualFold(kind, "CronJob"):
		sched := listColumn{title: "SCHEDULE", width: 14,
			cell: func(_ *Model, o model.ResourceObject) string {
				c, _, _ := unstructured.NestedString(o.Raw, "spec", "schedule")
				return orDash(c)
			}}
		susp := listColumn{title: "SUSPEND", width: 8,
			cell: func(_ *Model, o model.ResourceObject) string {
				if v, found, _ := unstructured.NestedBool(o.Raw, "spec", "suspend"); found && v {
					return "yes"
				}
				return "no"
			}}
		last := listColumn{title: "LAST RUN", width: 9,
			cell: func(m *Model, o model.ResourceObject) string {
				ts, _, _ := unstructured.NestedString(o.Raw, "status", "lastScheduleTime")
				return relTime(ts, m.now())
			}}
		return []listColumn{ns, name, sched, susp, last, status, age}

	case strings.EqualFold(kind, "ConfigMap"):
		data := listColumn{title: "DATA", width: 5,
			cell: func(_ *Model, o model.ResourceObject) string {
				d, _ := o.Raw["data"].(map[string]interface{})
				b, _ := o.Raw["binaryData"].(map[string]interface{})
				return fmt.Sprintf("%d", len(d)+len(b))
			}}
		return []listColumn{ns, name, data, age}

	case strings.EqualFold(kind, "Secret"):
		typ := listColumn{title: "TYPE", width: 28,
			cell: func(_ *Model, o model.ResourceObject) string {
				t, _ := o.Raw["type"].(string)
				return orDash(t)
			}}
		data := listColumn{title: "DATA", width: 5,
			cell: func(_ *Model, o model.ResourceObject) string {
				d, _ := o.Raw["data"].(map[string]interface{})
				return fmt.Sprintf("%d", len(d))
			}}
		return []listColumn{ns, name, typ, data, age}

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
	kept := make([]model.ResourceObject, 0, len(objs))
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
		kept = append(kept, o)
	}
	m.rowLevels = levels
	m.rowObjs = kept
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

// jobDuration formats how long a Job ran (or has been running).
func jobDuration(startRFC, endRFC string, now time.Time) string {
	start, err := time.Parse(time.RFC3339, startRFC)
	if err != nil {
		return "-"
	}
	end := now
	if e, err := time.Parse(time.RFC3339, endRFC); err == nil {
		end = e
	}
	d := end.Sub(start)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// relTime renders an RFC3339 timestamp as a compact age ("-" when absent).
func relTime(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "-"
	}
	return kube.Age(t, now)
}

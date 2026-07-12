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
	"github.com/iadvize/idz-k8s/internal/ui/components"
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
	seen := map[string]bool{}
	for _, t := range pref.Columns {
		switch {
		case byTitle[t].cell != nil:
			out = append(out, byTitle[t])
			seen[t] = true
		case strings.HasPrefix(t, "label:") || strings.HasPrefix(t, "field:"):
			// User-defined columns: a label value or a dot-path field.
			out = append(out, customColumn(t))
		}
		// Anything else is a stale title from an old version: dropped (FR-025).
	}
	if len(out) == 0 {
		return cols
	}
	// Base columns in NEITHER list are new since the pref was saved: they
	// ALWAYS show up, so updates never ship invisible features (owner
	// reports 2026-07-09, twice). Legacy prefs (saved before the hidden
	// list existed) may resurface a column once — hide it again with 'C'
	// and the choice sticks from then on.
	hidden := map[string]bool{}
	for _, h := range pref.Hidden {
		hidden[h] = true
	}
	for _, c := range cols {
		if !seen[c.title] && !hidden[c.title] {
			out = append(out, c)
		}
	}
	return out
}

// customColumn builds a user-defined column: "label:app" shows that label's
// value, "field:.spec.nodeName" the object field at that dot path. Both are
// stored prefixed in the prefs so stale plain titles stay distinguishable;
// the HEADER renders like the built-in columns (owner feedback 2026-07-07:
// same look as the other fields).
func customColumn(spec string) listColumn {
	title := customTitle(spec)
	if k, ok := strings.CutPrefix(spec, "label:"); ok {
		// Heal specs saved before the input normalization existed: a key
		// accidentally stored as "metadata.labels.<key>" is what the user
		// meant by "<key>" (no real label key starts that way).
		k = strings.TrimPrefix(k, ".")
		k = strings.TrimPrefix(k, "metadata.labels.")
		return listColumn{title: title, width: 16,
			cell: func(_ *Model, o model.ResourceObject) string {
				return orDash(kube.ObjectLabel(o.Raw, k))
			}}
	}
	path := strings.TrimPrefix(spec, "field:")
	fields := strings.Split(strings.TrimPrefix(path, "."), ".")
	return listColumn{title: title, width: 20,
		cell: func(_ *Model, o model.ResourceObject) string {
			return fieldCell(o.Raw, fields)
		}}
}

// customTitle derives a built-in-looking header from a custom spec:
// "label:app" → "APP", "field:.status.podIP" → "POD IP".
func customTitle(spec string) string {
	if k, ok := strings.CutPrefix(spec, "label:"); ok {
		k = strings.TrimPrefix(strings.TrimPrefix(k, "."), "metadata.labels.")
		// Long conventional keys keep only their meaningful tail:
		// app.kubernetes.io/version → VERSION.
		if i := strings.LastIndex(k, "/"); i >= 0 && i+1 < len(k) {
			k = k[i+1:]
		}
		return strings.ToUpper(k)
	}
	path := strings.TrimPrefix(spec, "field:")
	seg := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		seg = path[i+1:]
	}
	return strings.ToUpper(camelSplit(seg))
}

// camelSplit inserts spaces at camelCase boundaries ("podIP" → "pod IP").
func camelSplit(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' && runes[i-1] >= 'a' && runes[i-1] <= 'z' {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isCustomSpec reports whether a chooser entry is a user-defined column.
func isCustomSpec(spec string) bool {
	return strings.HasPrefix(spec, "label:") || strings.HasPrefix(spec, "field:")
}

// fieldCell walks a dot path of map fields and renders the scalar found there
// ("-" when absent, "…" for a non-scalar like a list or object).
func fieldCell(raw map[string]interface{}, fields []string) string {
	// Map keys may legally contain dots (app.kubernetes.io/version), so the
	// walk backtracks: every join length is tried, and a shorter key that
	// dead-ends never hides a longer one.
	cur, ok := resolveField(raw, fields)
	if !ok {
		return "-"
	}
	switch v := cur.(type) {
	case string:
		return orDash(v)
	case bool, int64, float64:
		return fmt.Sprintf("%v", v)
	case nil:
		return "-"
	default:
		return "…"
	}
}

// resolveField resolves a dot path against nested maps, trying every join
// length of the remaining segments (dotted keys) with backtracking.
func resolveField(cur interface{}, fields []string) (interface{}, bool) {
	if len(fields) == 0 {
		return cur, true
	}
	mm, ok := cur.(map[string]interface{})
	if !ok {
		return nil, false
	}
	key := ""
	for j := 0; j < len(fields); j++ {
		if j == 0 {
			key = fields[0]
		} else {
			key += "." + fields[j]
		}
		if v, found := mm[key]; found {
			if out, done := resolveField(v, fields[j+1:]); done {
				return out, true
			}
		}
	}
	return nil, false
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
		usageKey := func(o model.ResourceObject) string { return o.Namespace + "/" + o.Name }
		// Live usage columns (owner request 2026-07-09): raw value and % of
		// the pod's request, fed at tick cadence — "—" when Prometheus has
		// no sample or no request is set (never estimated).
		cpuUse := listColumn{title: "CPU", width: 8,
			cell: func(m *Model, o model.ResourceObject) string {
				if v, ok := m.podUsageCPU[usageKey(o)]; ok {
					return components.FormatCPU(v)
				}
				return "—"
			},
		}
		memUse := listColumn{title: "MEM", width: 9,
			cell: func(m *Model, o model.ResourceObject) string {
				if v, ok := m.podUsageMem[usageKey(o)]; ok {
					return components.FormatMemory(v)
				}
				return "—"
			}}
		cpuPct := listColumn{title: "CPU%R", width: 6,
			cell: func(m *Model, o model.ResourceObject) string {
				return usagePctCell(m.podUsageCPU[usageKey(o)], hasKey(m.podUsageCPU, usageKey(o)), reqCPU(o))
			}}
		memPct := listColumn{title: "MEM%R", width: 6,
			cell: func(m *Model, o model.ResourceObject) string {
				return usagePctCell(m.podUsageMem[usageKey(o)], hasKey(m.podUsageMem, usageKey(o)), reqMem(o))
			}}
		// Numeric sorts (missing data sorts last ascending).
		cpuUse.less = func(a, b model.ResourceObject) bool {
			return m.podUsageCPU[usageKey(a)] < m.podUsageCPU[usageKey(b)]
		}
		memUse.less = func(a, b model.ResourceObject) bool {
			return m.podUsageMem[usageKey(a)] < m.podUsageMem[usageKey(b)]
		}
		cpuPct.less = func(a, b model.ResourceObject) bool {
			return usageFrac(m.podUsageCPU[usageKey(a)], reqCPU(a)) < usageFrac(m.podUsageCPU[usageKey(b)], reqCPU(b))
		}
		memPct.less = func(a, b model.ResourceObject) bool {
			return usageFrac(m.podUsageMem[usageKey(a)], reqMem(a)) < usageFrac(m.podUsageMem[usageKey(b)], reqMem(b))
		}
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
		return []listColumn{ns, name, ready, cpuUse, cpuPct, memUse, memPct, restarts, node, status, age}

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
			// Cap the flexible column to its longest visible content (+2):
			// on a wide terminal an uncapped NAME opens a desert between
			// the name and the next column (owner report 2026-07-10).
			if need := m.flexContentW + 2 - widths[flexIdx]; need < extra {
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
	// Longest content of the flexible column (title included) for listWidths.
	m.flexContentW = 0
	flexIdx := -1
	for i, c := range cols {
		if c.width == 0 {
			flexIdx = i
			m.flexContentW = len([]rune(c.title))
		}
	}
	if flexIdx >= 0 {
		for _, r := range rows {
			if l := len([]rune(r[flexIdx+1])); l > m.flexContentW {
				m.flexContentW = l
			}
		}
	}
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
		title := sortArrowTitle(c.title, m.sortCol == i+1, m.sortAsc)
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

// Helpers behind the pods list usage columns.

func hasKey(m map[string]float64, k string) bool { _, ok := m[k]; return ok }

func reqCPU(o model.ResourceObject) float64 {
	c, _, _, _ := kube.PodResources(o.Raw)
	return c
}

func reqMem(o model.ResourceObject) float64 {
	_, _, mm, _ := kube.PodResources(o.Raw)
	return mm
}

// usageFrac returns usage/request (-1 when unknown, sorting last ascending).
func usageFrac(usage, request float64) float64 {
	if request <= 0 || usage <= 0 {
		return -1
	}
	return usage / request
}

// usagePctCell renders "% of request": explicit "—" without data or request.
func usagePctCell(usage float64, hasData bool, request float64) string {
	if !hasData || request <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", usage/request*100)
}

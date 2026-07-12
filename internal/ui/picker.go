package ui

// Modal picker (namespace/type/context/event-kind/view/columns):
// opening, options, filtering, selection and its key handling.

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/helm"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
)

func (m Model) handlePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Enter selects the highlighted option.
	if hit(msg, m.keys.Open) {
		return m.pickerSelect()
	}
	// Column chooser: Space toggles, ←/→ reorders; no type-to-filter (and no
	// key may leak to global shortcuts while the chooser is open).
	if m.pickerKind == pickColumns {
		switch msg.Type {
		case tea.KeySpace:
			m.toggleColItem(m.pickerWin.cursor)
			return m, nil
		case tea.KeyLeft, tea.KeyRight:
			i, j := m.pickerWin.cursor, m.pickerWin.cursor-1
			if msg.Type == tea.KeyRight {
				j = i + 1
			}
			if i >= 0 && i < len(m.colItems) && j >= 0 && j < len(m.colItems) &&
				m.colItems[i].title != addFieldLabel && m.colItems[j].title != addFieldLabel {
				m.colItems[i], m.colItems[j] = m.colItems[j], m.colItems[i]
				m.applyColumnRows()
				m.pickerWin.Move(j - i)
			}
			return m, nil
		case tea.KeyBackspace, tea.KeyDelete:
			m.removeColItem(m.pickerWin.cursor)
			return m, nil
		case tea.KeyRunes:
			return m, nil
		}
	}
	// Arrow/page navigation on the windowed table.
	switch msg.Type {
	case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
		m.navigate(&m.pickerWin, msg)
		return m, nil
	case tea.KeyBackspace:
		if m.pickerQuery != "" {
			r := []rune(m.pickerQuery) // delete the last rune, never a byte
			m.pickerQuery = string(r[:len(r)-1])
			m.applyPickerRows()
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		// Type-to-filter: narrow the options as the user types (k9s-style ":").
		m.pickerQuery += string(msg.Runes)
		m.applyPickerRows()
		return m, nil
	}
	// Every meaningful key is handled above; the picker is rendered straight
	// from pickerWin, so there is no widget left to delegate to.
	return m, nil
}

// openKindPicker offers the object kinds present in the current events — the
// TUI equivalent of a dropdown at the top of the timeline.
func (m Model) openKindPicker() (tea.Model, tea.Cmd) {
	seen := map[string]bool{}
	opts := []string{}
	for _, e := range m.eventRows {
		if e.ObjKind != "" && !seen[e.ObjKind] {
			seen[e.ObjKind] = true
			opts = append(opts, e.ObjKind)
		}
	}
	sort.Strings(opts)
	m.pickerKind = pickEventKind
	m.pickerReturn = screenEvents
	m.pickerQuery = ""
	m.pickerOpts = append([]string{allKindsLabel}, opts...)
	rows := make([]table.Row, len(m.pickerOpts))
	for i, o := range m.pickerOpts {
		rows[i] = table.Row{o}
	}
	m.pickerWin.SetRows(rows)
	m.screen = screenPicker
	m.layout()
	return m, nil
}

func (m Model) openPicker(kind pickerKind) (tea.Model, tea.Cmd) {
	m.pickerKind = kind
	m.pickerReturn = m.screen // return here when the picker closes
	var (
		opts []string
		cmd  tea.Cmd
	)
	switch kind {
	case pickType:
		// Native Kubernetes types first, CRDs below (owner request
		// 2026-07-09) — each group alphabetical; type-to-filter unchanged.
		var natives, crds []string
		for _, t := range m.types {
			if t.IsCRD {
				crds = append(crds, typeOptionLabel(t))
			} else {
				natives = append(natives, typeOptionLabel(t))
			}
		}
		sort.Strings(natives)
		sort.Strings(crds)
		opts = append(append(opts, natives...), crds...)
	case pickContext:
		ctxs, err := kube.Contexts(m.kubeconfigPath, m.client.ActiveContext())
		if err != nil {
			m.errMsg = err.Error()
			return m, nil
		}
		for _, c := range ctxs {
			label := c.Name
			if c.Active {
				label += activeContextSuffix
			}
			opts = append(opts, label)
		}
	case pickNamespace:
		// Open instantly on best-effort options (derived from the objects on
		// screen); the real cluster list arrives via nsOptionsMsg. Listing
		// synchronously here would freeze the whole TUI on a slow apiserver.
		opts = m.namespaceOptions()
		cmd = m.fetchNamespaceOptions()
	}
	if kind != pickType {
		sort.Strings(opts)
	}
	switch kind {
	case pickNamespace:
		// Offer an "all namespaces" choice at the top (cross-namespace view).
		opts = append([]string{allNamespacesLabel}, opts...)
	case pickType:
		// Helm releases are reachable from ':' too (type "helm"), not only 'H'.
		opts = append([]string{helmReleasesLabel}, opts...)
	}
	m.pickerOpts = opts
	m.pickerQuery = ""
	m.applyPickerRows()
	m.screen = screenPicker
	m.layout()
	return m, cmd
}

// fetchNamespaceOptions lists the cluster's namespaces off the Update loop.
func (m Model) fetchNamespaceOptions() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		nss, err := client.Namespaces(ctx)
		return nsOptionsMsg{opts: nss, err: err}
	}
}

func (m Model) pickerLabel() string {
	switch m.pickerKind {
	case pickType:
		return "resource type"
	case pickNamespace:
		return "namespace"
	case pickContext:
		return "context"
	case pickEventKind:
		return "kind"
	case pickColumns:
		return "columns — " + m.curType.Key()
	case pickView:
		return "views"
	default:
		return "select"
	}
}

// applyPickerRows rebuilds the picker rows from pickerOpts filtered by the
// current type-to-filter query (case-insensitive substring match).
func (m *Model) applyPickerRows() {
	q := strings.ToLower(strings.TrimSpace(m.pickerQuery))
	rows := make([]table.Row, 0, len(m.pickerOpts))
	// Namespace picker: a glob query ("staging-*") becomes a selectable
	// pattern scope of its own, on top of the matching literal entries.
	if m.pickerKind == pickNamespace && kube.IsNamespacePattern(strings.TrimSpace(m.pickerQuery)) {
		rows = append(rows, table.Row{nsPatternPrefix + strings.TrimSpace(m.pickerQuery)})
	}
	for _, o := range m.pickerOpts {
		if q == "" || strings.Contains(strings.ToLower(o), q) {
			rows = append(rows, table.Row{o})
		}
	}
	m.pickerWin.SetRows(rows)
}

func (m Model) namespaceOptions() []string {
	// Best-effort: derive namespaces from currently listed objects plus common ones.
	set := map[string]bool{}
	for _, o := range m.objects {
		if o.Namespace != "" {
			set[o.Namespace] = true
		}
	}
	set["default"] = true
	set["kube-system"] = true
	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}
	return out
}

func (m Model) pickerSelect() (tea.Model, tea.Cmd) {
	row, _ := m.pickerWin.Selected()
	if len(row) == 0 {
		return m.goBack()
	}
	choice := row[0]
	switch m.pickerKind {
	case pickType:
		if choice == helmReleasesLabel {
			m.screen = screenList // leave the picker even if helm is unavailable
			return m.openHelm()
		}
		choice = strings.TrimSpace(strings.SplitN(choice, "  (", 2)[0])
		for _, t := range m.types {
			if t.Key() == choice {
				m.curType = t
			}
		}
		// A type switch is a fresh view: leave any drill and the marked
		// selection, then restore the type's own saved customization —
		// filter, sort — instead of a leftover from the previous type (a
		// saved filter is always visible as a header chip, never invisible).
		m.resetDrill()
		m.applyViewPref()
		m.marked = map[string]model.ResourceObject{}
		m.statusMsg = ""
		m.screen = screenList
		m.layout()
		m.persist()
		cmds := []tea.Cmd{m.listObjects()}
		if strings.EqualFold(m.curType.Kind, "Pod") && m.metrics.Enabled() {
			cmds = append(cmds, m.fetchListUsage())
		}
		return m, tea.Batch(cmds...)
	case pickNamespace:
		switch {
		case choice == allNamespacesLabel:
			m.client.Namespace = "" // empty → list across all namespaces
		case strings.HasPrefix(choice, nsPatternPrefix):
			m.client.Namespace = strings.TrimPrefix(choice, nsPatternPrefix)
		default:
			m.client.Namespace = choice
		}
		m.layout()
		m.persist()
		if m.pickerReturn == screenEvents {
			// Namespace changed from the timeline: stay there and reload it.
			// A drill scope no longer applies to the new namespace.
			m.eventsScope = nil
			m.eventsScopeFor = ""
			m.resetDrill()
			m.screen = screenEvents
			m.events.SetContent("loading events…")
			return m, m.fetchEvents()
		}
		// Changing namespace from the list leaves any drill (its workload
		// belonged to the previous scope) and the marked selection.
		m.resetDrill()
		m.marked = map[string]model.ResourceObject{}
		m.statusMsg = ""
		m.screen = screenList
		return m, m.listObjects()
	case pickEventKind:
		if choice == allKindsLabel {
			m.eventsKind = ""
		} else {
			m.eventsKind = choice
		}
		m.screen = screenEvents
		m.layout()
		m.renderEvents()
		return m, nil
	case pickColumns:
		if i := m.pickerWin.cursor; i >= 0 && i < len(m.colItems) && m.colItems[i].title == addFieldLabel {
			m.fieldNaming, m.fieldInput = true, ""
			return m, nil
		}
		return m.applyColumnChoice()
	case pickView:
		switch choice {
		case saveViewLabel:
			m.viewNaming, m.viewName = true, ""
			m.screen = m.pickerReturn
			m.layout()
			return m, nil
		case resetViewLabel:
			m.resetCurrentView()
			m.screen = screenList
			m.layout()
			return m, m.listObjects()
		}
		name := strings.TrimSpace(strings.SplitN(choice, "  (", 2)[0])
		for _, v := range m.cfg.SavedViews {
			if v.Name == name {
				return m.applySavedView(v)
			}
		}
		return m.goBack()
	case pickContext:
		choice = strings.TrimSuffix(choice, activeContextSuffix)
		nc, err := kube.NewClient(kube.Options{KubeconfigPath: m.kubeconfigPath, Context: choice})
		if err != nil {
			m.errMsg = err.Error()
			return m.goBack()
		}
		m.client.Close() // stop the old context's informer watches
		m.client = nc
		m.helm = helm.New(m.kubeconfigPath, choice) // helm reads the new cluster too
		m.marked = map[string]model.ResourceObject{}
		m.screen = screenList
		m.layout()
		m.persist()
		cmds := []tea.Cmd{m.loadTypes()}
		// Re-link metrics to the new cluster's Prometheus (unless an explicit URL).
		if m.cfg.PrometheusURL == "" {
			m.metrics = &metrics.Client{} // disable until re-discovered
			cmds = append(cmds, m.discoverMetrics())
		}
		return m, tea.Batch(cmds...)
	}
	return m.goBack()
}

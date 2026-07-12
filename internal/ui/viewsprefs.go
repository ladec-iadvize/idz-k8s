package ui

// Customizable views (US8, FR-024/FR-025): per-type filter/sort
// prefs, the column chooser and saved views.

import (
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/model"
)

// applyViewPref restores the current type's saved customization: committed
// filter and sort (resolved by column title; a title that no longer matches
// any column is silently ignored — FR-025 tolerance).
func (m *Model) applyViewPref() {
	pref := m.cfg.ViewPrefs[m.curType.Key()]
	m.filter.SetValue(pref.Filter)
	m.sortCol, m.sortAsc = -1, true
	if pref.SortCol != "" {
		for i, c := range m.columnsForType() {
			if c.title == pref.SortCol {
				m.sortCol, m.sortAsc = i+1, pref.SortAsc
				break
			}
		}
	}
}

// persistViewPref snapshots the current sort and committed filter into the
// type's pref — what you see (header chip, sort arrow) is what comes back
// next time. An all-default pref is removed rather than stored.
func (m *Model) persistViewPref() {
	key := m.curType.Key()
	if key == "" || key == "/" {
		return
	}
	pref := m.cfg.ViewPrefs[key]
	cols := m.columnsForType()
	pref.SortCol, pref.SortAsc = "", false
	if m.sortCol >= 1 && m.sortCol <= len(cols) {
		pref.SortCol, pref.SortAsc = cols[m.sortCol-1].title, m.sortAsc
	}
	pref.Filter = strings.TrimSpace(m.filter.Value())
	if len(pref.Columns) == 0 && pref.Hidden == nil && pref.SortCol == "" && pref.Filter == "" {
		delete(m.cfg.ViewPrefs, key)
	} else {
		if m.cfg.ViewPrefs == nil {
			m.cfg.ViewPrefs = map[string]config.ViewPref{}
		}
		m.cfg.ViewPrefs[key] = pref
	}
	m.persist()
}

// openColumnChooser opens the 'C' modal: every column the current type offers,
// visible ones first in display order, then the hidden ones.
func (m Model) openColumnChooser() (tea.Model, tea.Cmd) {
	base := m.columnsBase()
	baseTitles := map[string]bool{}
	for _, c := range base {
		baseTitles[c.title] = true
	}
	// Shown columns, in order — custom ones keep their stored "label:"/
	// "field:" spec so they round-trip through the prefs unchanged.
	var items []colItem
	on := map[string]bool{}
	if pref := m.cfg.ViewPrefs[m.curType.Key()]; len(pref.Columns) > 0 {
		for _, spec := range pref.Columns {
			if baseTitles[spec] || strings.HasPrefix(spec, "label:") || strings.HasPrefix(spec, "field:") {
				items = append(items, colItem{title: spec, on: true})
				on[spec] = true
			}
		}
	}
	if len(items) == 0 {
		for _, c := range base {
			items = append(items, colItem{title: c.title, on: true})
			on[c.title] = true
		}
	}
	for _, c := range base {
		if !on[c.title] {
			items = append(items, colItem{title: c.title})
		}
	}
	items = append(items, colItem{title: addFieldLabel})
	m.colItems = items
	m.pickerKind = pickColumns
	m.pickerReturn = screenList
	m.pickerQuery = ""
	// The bubbles table panics on SetRows without columns — always set them.
	m.applyColumnRows()
	m.pickerWin.Home()
	m.screen = screenPicker
	m.layout()
	return m, nil
}

// applyColumnRows re-renders the chooser rows from colItems.
func (m *Model) applyColumnRows() {
	rows := make([]table.Row, 0, len(m.colItems))
	for _, it := range m.colItems {
		chk := "· "
		if it.on {
			chk = "✓ "
		}
		label := it.title
		if isCustomSpec(it.title) {
			// Shown like a built-in column, the spec kept as a reminder.
			label = customTitle(it.title) + "  (" + it.title + ")"
		}
		rows = append(rows, table.Row{chk + label})
	}
	m.pickerWin.SetRows(rows)
}

// toggleColItem flips a column's visibility (NAME stays — every other action
// in the list needs a way to identify the row).
func (m *Model) toggleColItem(i int) {
	if i < 0 || i >= len(m.colItems) {
		return
	}
	if m.colItems[i].title == addFieldLabel {
		m.fieldNaming, m.fieldInput = true, ""
		return
	}
	if m.colItems[i].title == "NAME" && m.colItems[i].on {
		m.statusMsg = "the NAME column cannot be hidden"
		return
	}
	m.colItems[i].on = !m.colItems[i].on
	m.applyColumnRows()
}

// addCustomColumn inserts a user-defined column into the chooser: a leading
// '.' means an object field path, anything else a label key.
func (m *Model) addCustomColumn(spec string) {
	if spec == "" {
		return
	}
	// Normalize: "metadata.labels.<key>" (with or without a leading dot) is
	// what people naturally type for a label — turn it into a label column,
	// where dotted keys like app.kubernetes.io/version just work.
	core := strings.TrimPrefix(spec, ".")
	if k, ok := strings.CutPrefix(core, "metadata.labels."); ok && k != "" {
		spec = "label:" + k
	} else if strings.HasPrefix(spec, ".") {
		spec = "field:" + spec
	} else {
		spec = "label:" + strings.TrimPrefix(spec, "label:")
	}
	for i := range m.colItems {
		if m.colItems[i].title == spec {
			m.colItems[i].on = true
			m.applyColumnRows()
			return
		}
	}
	// Insert before the trailing "add custom field…" action row.
	at := len(m.colItems)
	if at > 0 && m.colItems[at-1].title == addFieldLabel {
		at--
	}
	m.colItems = append(m.colItems[:at], append([]colItem{{title: spec, on: true}}, m.colItems[at:]...)...)
	m.applyColumnRows()
	m.statusMsg = "column added — Enter applies the arrangement"
}

// removeColItem deletes a user-defined column from the chooser (⌫ — the
// typo eraser). Built-in columns can only be hidden, never removed.
func (m *Model) removeColItem(i int) {
	if i < 0 || i >= len(m.colItems) {
		return
	}
	if !isCustomSpec(m.colItems[i].title) {
		if m.colItems[i].title != addFieldLabel {
			m.statusMsg = "built-in columns can only be hidden (Space) — ⌫ removes custom fields"
		}
		return
	}
	m.colItems = append(m.colItems[:i], m.colItems[i+1:]...)
	m.applyColumnRows()
	m.statusMsg = "custom field removed — Enter applies"
}

// applyColumnChoice commits the chooser (Enter): store the arrangement — or
// clear it when it matches the type's default — re-resolve the sort against
// the new columns, and persist.
func (m Model) applyColumnChoice() (tea.Model, tea.Cmd) {
	var sortTitle string
	if cols := m.columnsForType(); m.sortCol >= 1 && m.sortCol <= len(cols) {
		sortTitle = cols[m.sortCol-1].title
	}
	titles := make([]string, 0, len(m.colItems))
	for _, it := range m.colItems {
		if it.on {
			titles = append(titles, it.title)
		}
	}
	base := m.columnsBase()
	def := len(titles) == len(base)
	if def {
		for i, c := range base {
			if titles[i] != c.title {
				def = false
				break
			}
		}
	}
	key := m.curType.Key()
	if m.cfg.ViewPrefs == nil {
		m.cfg.ViewPrefs = map[string]config.ViewPref{}
	}
	pref := m.cfg.ViewPrefs[key]
	pref.Columns = titles
	// Record what was explicitly turned OFF, so base columns added by
	// future updates (in neither list) show up by default.
	pref.Hidden = []string{}
	for _, it := range m.colItems {
		if !it.on && !isCustomSpec(it.title) && it.title != addFieldLabel {
			pref.Hidden = append(pref.Hidden, it.title)
		}
	}
	if def {
		pref.Columns, pref.Hidden = nil, nil
	}
	m.cfg.ViewPrefs[key] = pref
	m.screen = screenList
	m.layout()
	// The sorted column may have been hidden or moved: follow it by title.
	m.sortCol = -1
	if sortTitle != "" {
		for i, c := range m.columnsForType() {
			if c.title == sortTitle {
				m.sortCol = i + 1
				break
			}
		}
	}
	m.applyRows()
	m.persistViewPref()
	return m, nil
}

// openViewPicker opens the 'V' modal: saved views plus save/reset actions.
func (m Model) openViewPicker() (tea.Model, tea.Cmd) {
	opts := []string{saveViewLabel, resetViewLabel}
	for _, v := range m.cfg.SavedViews {
		opts = append(opts, viewOptionLabel(v))
	}
	m.pickerKind = pickView
	m.pickerReturn = m.screen
	m.pickerOpts = opts
	m.pickerQuery = ""
	// The bubbles table panics on SetRows without columns — always set them.
	m.applyPickerRows()
	m.pickerWin.Home()
	m.screen = screenPicker
	m.layout()
	return m, nil
}

// viewOptionLabel renders a saved view as a picker option.
func viewOptionLabel(v config.SavedView) string {
	ns := v.Namespace
	if ns == "" {
		ns = "all ns"
	}
	return v.Name + "  (" + v.Type + ", " + ns + ")"
}

// saveCurrentView stores the whole current arrangement — type, namespace,
// columns, sort, filter — under a name (same name = update).
func (m *Model) saveCurrentView(name string) {
	if name == "" {
		m.statusMsg = "view not saved (empty name)"
		return
	}
	v := config.SavedView{
		Name:      name,
		Type:      m.curType.Key(),
		Namespace: m.client.Namespace,
		Columns:   m.cfg.ViewPrefs[m.curType.Key()].Columns,
		Hidden:    m.cfg.ViewPrefs[m.curType.Key()].Hidden,
		Filter:    strings.TrimSpace(m.filter.Value()),
	}
	if cols := m.columnsForType(); m.sortCol >= 1 && m.sortCol <= len(cols) {
		v.SortCol, v.SortAsc = cols[m.sortCol-1].title, m.sortAsc
	}
	updated := false
	for i := range m.cfg.SavedViews {
		if m.cfg.SavedViews[i].Name == name {
			m.cfg.SavedViews[i] = v
			updated = true
			break
		}
	}
	if !updated {
		m.cfg.SavedViews = append(m.cfg.SavedViews, v)
	}
	m.persist()
	if updated {
		m.statusMsg = "view “" + name + "” updated"
	} else {
		m.statusMsg = "view “" + name + "” saved — 'V' opens it anytime"
	}
}

// applySavedView switches to a saved view: type, namespace, columns, sort,
// filter. The view's arrangement becomes the type's current pref.
func (m Model) applySavedView(v config.SavedView) (tea.Model, tea.Cmd) {
	t, ok := findTypeByKey(m.types, v.Type)
	if !ok {
		m.errMsg = "view " + v.Name + ": type " + v.Type + " not available on this cluster"
		m.screen = m.pickerReturn
		m.layout()
		return m, nil
	}
	m.curType = t
	m.client.Namespace = v.Namespace
	if m.cfg.ViewPrefs == nil {
		m.cfg.ViewPrefs = map[string]config.ViewPref{}
	}
	m.cfg.ViewPrefs[v.Type] = config.ViewPref{Columns: v.Columns, Hidden: v.Hidden, SortCol: v.SortCol, SortAsc: v.SortAsc, Filter: v.Filter}
	m.resetDrill()
	m.marked = map[string]model.ResourceObject{}
	m.applyViewPref()
	m.statusMsg = "view “" + v.Name + "”"
	m.screen = screenList
	m.layout()
	m.persist()
	return m, m.listObjects()
}

// resetCurrentView drops every customization of the current type ('R').
func (m *Model) resetCurrentView() {
	delete(m.cfg.ViewPrefs, m.curType.Key())
	m.filter.SetValue("")
	m.sortCol, m.sortAsc = -1, true
	m.applyRows()
	m.persist()
	m.statusMsg = "view reset to defaults"
}

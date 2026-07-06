// Package keys defines the single source of truth for keybindings and their
// help metadata (FR-009 familiar/non-exotic, FR-010 discoverable help). No key
// performs a mutating action — the tool is read-only (FR-012).
package keys

import "github.com/charmbracelet/bubbles/key"

// KeyMap holds every binding. Help text is attached so the overlay stays in
// sync with behavior.
type KeyMap struct {
	Up        key.Binding
	Down      key.Binding
	PageUp    key.Binding
	PageDown  key.Binding
	Home      key.Binding
	End       key.Binding
	Open      key.Binding
	Back      key.Binding
	Filter    key.Binding
	Jump      key.Binding
	Logs      key.Binding
	Yaml      key.Binding
	Describe  key.Binding
	Owner     key.Binding
	Top       key.Binding
	Diag      key.Binding
	Topology  key.Binding
	Events    key.Binding
	Mark      key.Binding
	Sort      key.Binding
	SortDir   key.Binding
	Columns   key.Binding
	Views     key.Binding
	ResetView key.Binding
	Values    key.Binding
	Pause     key.Binding
	WarnOnly  key.Binding
	Mouse     key.Binding
	Kind      key.Binding
	Namespace key.Binding
	Context   key.Binding
	Help      key.Binding
	Quit      key.Binding
}

// Default returns the conventional bindings (see contracts/keybindings.md).
func Default() KeyMap {
	return KeyMap{
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		PageUp:    key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp", "page up")),
		PageDown:  key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("PgDn", "page down")),
		Home:      key.NewBinding(key.WithKeys("home"), key.WithHelp("Home", "top")),
		End:       key.NewBinding(key.WithKeys("end"), key.WithHelp("End", "bottom")),
		Open:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", "open")),
		Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", "back")),
		Filter:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Jump:      key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "jump to type")),
		Logs:      key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
		Yaml:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yaml")),
		Describe:  key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe")),
		Owner:     key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "owner")),
		Top:       key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "top usage")),
		Diag:      key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "failures")),
		Topology:  key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "topology")),
		Events:    key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "events")),
		Mark:      key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "mark")),
		Sort:      key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort column")),
		SortDir:   key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "sort asc/desc")),
		Columns:   key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "choose columns")),
		Views:     key.NewBinding(key.WithKeys("V"), key.WithHelp("V", "views (save/open)")),
		ResetView: key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "reset view")),
		Values:    key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "values")),
		Pause:     key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "pause/resume")),
		WarnOnly:  key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "warnings only")),
		Mouse:     key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mouse on/off (copy text)")),
		Kind:      key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "kind")),
		Namespace: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "namespace")),
		Context:   key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "context")),
		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:      key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp lists the most relevant bindings for the status bar.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Open, k.Filter, k.Logs, k.Top, k.Diag, k.Topology, k.Namespace, k.Context, k.Help, k.Quit}
}

// FullHelp lists all bindings grouped for the help overlay.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End},
		{k.Open, k.Back, k.Filter, k.Jump, k.Logs, k.Top, k.Diag, k.Topology},
		{k.Sort, k.Columns, k.Views, k.ResetView},
		{k.Namespace, k.Context, k.Help, k.Quit},
	}
}

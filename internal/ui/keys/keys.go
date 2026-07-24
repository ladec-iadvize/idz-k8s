// Package keys defines the single source of truth for keybindings and their
// help metadata (FR-009 familiar/non-exotic, FR-010 discoverable help).
// Mutating actions (v3 admin) go through the 'a' actions palette or 'e'
// edit — and always end in an explicit confirmation before anything runs.
package keys

import "github.com/charmbracelet/bubbles/key"

// KeyMap holds every binding. Help text is attached so the overlay stays in
// sync with behavior.
type KeyMap struct {
	Up         key.Binding
	Down       key.Binding
	PageUp     key.Binding
	PageDown   key.Binding
	Home       key.Binding
	End        key.Binding
	Open       key.Binding
	Back       key.Binding
	Filter     key.Binding
	Jump       key.Binding
	Logs       key.Binding
	Yaml       key.Binding
	Describe   key.Binding
	Owner      key.Binding
	Palette    key.Binding // one entry point for every analysis view
	Actions    key.Binding // one entry point for every admin action (v3)
	Edit       key.Binding
	SearchNext key.Binding
	SearchPrev key.Binding
	Mark       key.Binding
	Sort       key.Binding
	SortDir    key.Binding
	Columns    key.Binding
	Views      key.Binding
	ResetView  key.Binding
	Values     key.Binding
	Reveal     key.Binding
	Pause      key.Binding
	WarnOnly   key.Binding
	Mouse      key.Binding
	Kind       key.Binding
	Namespace  key.Binding
	Context    key.Binding
	Help       key.Binding
	Quit       key.Binding
}

// Default returns the conventional bindings (see contracts/keybindings.md).
func Default() KeyMap {
	return KeyMap{
		Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		PageUp:     key.NewBinding(key.WithKeys("pgup"), key.WithHelp("PgUp", "page up")),
		PageDown:   key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("PgDn", "page down")),
		Home:       key.NewBinding(key.WithKeys("home"), key.WithHelp("Home", "top")),
		End:        key.NewBinding(key.WithKeys("end"), key.WithHelp("End", "bottom")),
		Open:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", "open")),
		Back:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", "back")),
		Filter:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Jump:       key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "jump to type")),
		Logs:       key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
		Yaml:       key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yaml")),
		Describe:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "describe")),
		Owner:      key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "owner")),
		Palette:    key.NewBinding(key.WithKeys(">"), key.WithHelp(">", "views palette")),
		Actions:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "actions (admin)")),
		Edit:       key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit yaml")),
		SearchNext: key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next match")),
		SearchPrev: key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "previous match")),
		Mark:       key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "mark")),
		Sort:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort column")),
		SortDir:    key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "sort asc/desc")),
		Columns:    key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "choose columns")),
		Views:      key.NewBinding(key.WithKeys("V"), key.WithHelp("V", "views (save/open)")),
		ResetView:  key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "reset view")),
		Values:     key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "values")),
		Reveal:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "reveal/mask secret values")),
		Pause:      key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "pause/resume")),
		WarnOnly:   key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "warnings only")),
		Mouse:      key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mouse on/off (copy text)")),
		Kind:       key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "kind")),
		Namespace:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "namespace")),
		Context:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "context")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp lists the most relevant bindings for the status bar.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Open, k.Filter, k.Logs, k.Palette, k.Namespace, k.Context, k.Help, k.Quit}
}

// FullHelp lists all bindings grouped for the help overlay. Every KeyMap
// field must appear here — the overlay is the discoverability contract
// (FR-010), an absent binding is an undiscoverable one.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Home, k.End},
		{k.Open, k.Back, k.Filter, k.Jump, k.Mark, k.SearchNext, k.SearchPrev},
		{k.Logs, k.Yaml, k.Describe, k.Owner, k.Palette, k.Actions, k.Edit},
		{k.Sort, k.SortDir, k.Columns, k.Views, k.ResetView},
		{k.Kind, k.Namespace, k.Context, k.Values, k.Reveal, k.Pause, k.WarnOnly},
		{k.Mouse, k.Help, k.Quit},
	}
}

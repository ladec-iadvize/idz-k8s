// Package theme centralizes all styling. Visual changes (colors, layout) live
// here and in components/ so they can be adjusted without touching data logic
// (constitution Principle II). Colors degrade gracefully: lipgloss honors
// NO_COLOR and non-color terminals, and every status also carries a symbol
// (FR-020, FR-022).
package theme

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/iadvize/idz-k8s/internal/model"
)

// Theme holds the styles used across views. The palette aims for a friendly,
// legible interface (FR-036): colored identity chips in the header, clear
// selection highlight, tinted section titles — while degrading gracefully on
// no-color terminals (lipgloss honors NO_COLOR).
type Theme struct {
	Title     lipgloss.Style
	StatusBar lipgloss.Style
	Help      lipgloss.Style
	HelpKey   lipgloss.Style // shortcut keys in the help overlay/footer
	HelpDesc  lipgloss.Style // their descriptions — readable, never faint
	Selected  lipgloss.Style
	Highlight lipgloss.Style // inverse video, for timeline marker highlighting
	Faint     lipgloss.Style
	Error     lipgloss.Style
	Ok        lipgloss.Style
	Warning   lipgloss.Style

	// Header identity chips.
	AppBadge lipgloss.Style // the app name badge
	CtxVal   lipgloss.Style // context value
	NsVal    lipgloss.Style // namespace value
	TypeVal  lipgloss.Style // resource type value

	// Tables.
	TableHeader   lipgloss.Style // column headers
	TableSelected lipgloss.Style // selected row (background highlight)

	// Content.
	Section  lipgloss.Style // section titles inside sub-views
	YamlKey  lipgloss.Style // YAML keys in detail/values views
	Position lipgloss.Style // "12-38/842" footer position

	// Modals (picker, help): rounded border tinted like the app badge.
	ModalBorder lipgloss.Style
}

// Default returns the default theme. lipgloss automatically drops color on
// terminals that do not support it, so this also serves the degraded case.
func Default() Theme {
	return Theme{
		Title:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")),
		StatusBar: lipgloss.NewStyle().Foreground(lipgloss.Color("12")),
		Help:      lipgloss.NewStyle().Faint(true),
		HelpKey:   lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		HelpDesc:  lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Selected:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		Highlight: lipgloss.NewStyle().Reverse(true).Bold(true),
		Faint:     lipgloss.NewStyle().Faint(true),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color("204")),
		Ok:        lipgloss.NewStyle().Foreground(lipgloss.Color("78")),
		Warning:   lipgloss.NewStyle().Foreground(lipgloss.Color("214")),

		AppBadge: lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("230")).Bold(true).Padding(0, 1),
		CtxVal:   lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("153")).Bold(true).Padding(0, 1),
		NsVal:    lipgloss.NewStyle().Background(lipgloss.Color("22")).Foreground(lipgloss.Color("156")).Bold(true).Padding(0, 1),
		TypeVal:  lipgloss.NewStyle().Background(lipgloss.Color("54")).Foreground(lipgloss.Color("183")).Bold(true).Padding(0, 1),

		TableHeader:   lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		TableSelected: lipgloss.NewStyle().Background(lipgloss.Color("25")).Foreground(lipgloss.Color("231")).Bold(true),

		Section:  lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("117")).Bold(true).Padding(0, 1),
		YamlKey:  lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
		Position: lipgloss.NewStyle().Foreground(lipgloss.Color("81")),

		ModalBorder: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(0, 1),
	}
}

// light returns a palette tuned for light terminal backgrounds: darker
// foregrounds, soft chip backgrounds — same structure, same symbols.
func light() Theme {
	return Theme{
		Title:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")),
		StatusBar: lipgloss.NewStyle().Foreground(lipgloss.Color("25")),
		Help:      lipgloss.NewStyle().Faint(true),
		HelpKey:   lipgloss.NewStyle().Foreground(lipgloss.Color("25")).Bold(true),
		HelpDesc:  lipgloss.NewStyle().Foreground(lipgloss.Color("238")),
		Selected:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("28")),
		Highlight: lipgloss.NewStyle().Reverse(true).Bold(true),
		Faint:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color("160")),
		Ok:        lipgloss.NewStyle().Foreground(lipgloss.Color("28")),
		Warning:   lipgloss.NewStyle().Foreground(lipgloss.Color("130")),

		AppBadge: lipgloss.NewStyle().Background(lipgloss.Color("61")).Foreground(lipgloss.Color("231")).Bold(true).Padding(0, 1),
		CtxVal:   lipgloss.NewStyle().Background(lipgloss.Color("153")).Foreground(lipgloss.Color("17")).Bold(true).Padding(0, 1),
		NsVal:    lipgloss.NewStyle().Background(lipgloss.Color("157")).Foreground(lipgloss.Color("22")).Bold(true).Padding(0, 1),
		TypeVal:  lipgloss.NewStyle().Background(lipgloss.Color("183")).Foreground(lipgloss.Color("53")).Bold(true).Padding(0, 1),

		TableHeader:   lipgloss.NewStyle().Foreground(lipgloss.Color("25")).Bold(true),
		TableSelected: lipgloss.NewStyle().Background(lipgloss.Color("117")).Foreground(lipgloss.Color("16")).Bold(true),

		Section:  lipgloss.NewStyle().Background(lipgloss.Color("253")).Foreground(lipgloss.Color("25")).Bold(true).Padding(0, 1),
		YamlKey:  lipgloss.NewStyle().Foreground(lipgloss.Color("26")),
		Position: lipgloss.NewStyle().Foreground(lipgloss.Color("25")),

		ModalBorder: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("61")).Padding(0, 1),
	}
}

// ForName resolves a configured theme name: "dark" and "light" are explicit,
// "auto" follows the terminal's background (the OS/terminal default), and
// anything else falls back to auto. "none" keeps the dark palette — lipgloss
// drops the colors under NO_COLOR and the symbols carry the meaning.
func ForName(name string) Theme {
	switch name {
	case "dark":
		return Default()
	case "light":
		return light()
	default: // auto, none, unknown
		if lipgloss.HasDarkBackground() {
			return Default()
		}
		return light()
	}
}

// iAdvize brand green — the mint of the logo — and a darker shade for depth.
// Brand colors are theme-independent by nature (used by the --kikoo banner).
var (
	KikooGreen     = lipgloss.NewStyle().Foreground(lipgloss.Color("#3DDFA4")).Bold(true)
	KikooDarkGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#1FA97A"))
)

// podLogPalette holds visually distinct colors for merged-log pod prefixes.
var podLogPalette = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("39")),  // blue
	lipgloss.NewStyle().Foreground(lipgloss.Color("208")), // orange
	lipgloss.NewStyle().Foreground(lipgloss.Color("135")), // purple
	lipgloss.NewStyle().Foreground(lipgloss.Color("42")),  // green
	lipgloss.NewStyle().Foreground(lipgloss.Color("214")), // yellow
	lipgloss.NewStyle().Foreground(lipgloss.Color("45")),  // cyan
	lipgloss.NewStyle().Foreground(lipgloss.Color("197")), // pink
	lipgloss.NewStyle().Foreground(lipgloss.Color("190")), // lime
}

// PodPrefix picks a stable color for a pod name (same pod → same color for
// the whole session, no state needed).
func PodPrefix(pod string) lipgloss.Style {
	h := 0
	for _, r := range pod {
		h = h*31 + int(r)
	}
	if h < 0 {
		h = -h
	}
	return podLogPalette[h%len(podLogPalette)]
}

// Status renders a health as "<symbol> <label>", colored, with the symbol as a
// non-color fallback so meaning survives without color (FR-020).
func (t Theme) Status(s model.StatusSummary) string {
	label := s.Level.Symbol() + " " + s.Level.Label()
	switch s.Level {
	case model.HealthOk:
		return t.Ok.Render(label)
	case model.HealthWarning:
		return t.Warning.Render(label)
	case model.HealthError:
		return t.Error.Render(label)
	default:
		return t.Faint.Render(label)
	}
}

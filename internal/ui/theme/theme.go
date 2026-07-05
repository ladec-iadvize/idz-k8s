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
	Selected  lipgloss.Style
	Highlight lipgloss.Style // inverse video, for timeline marker highlighting
	Faint     lipgloss.Style
	Error     lipgloss.Style
	Ok        lipgloss.Style
	Warning   lipgloss.Style

	// Header identity chips.
	AppBadge lipgloss.Style // the app name badge
	ROBadge  lipgloss.Style // "read-only" badge
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
}

// Default returns the default theme. lipgloss automatically drops color on
// terminals that do not support it, so this also serves the degraded case.
func Default() Theme {
	return Theme{
		Title:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")),
		StatusBar: lipgloss.NewStyle().Foreground(lipgloss.Color("12")),
		Help:      lipgloss.NewStyle().Faint(true),
		Selected:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10")),
		Highlight: lipgloss.NewStyle().Reverse(true).Bold(true),
		Faint:     lipgloss.NewStyle().Faint(true),
		Error:     lipgloss.NewStyle().Foreground(lipgloss.Color("204")),
		Ok:        lipgloss.NewStyle().Foreground(lipgloss.Color("78")),
		Warning:   lipgloss.NewStyle().Foreground(lipgloss.Color("214")),

		AppBadge: lipgloss.NewStyle().Background(lipgloss.Color("62")).Foreground(lipgloss.Color("230")).Bold(true).Padding(0, 1),
		ROBadge:  lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("214")).Padding(0, 1),
		CtxVal:   lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("153")).Bold(true).Padding(0, 1),
		NsVal:    lipgloss.NewStyle().Background(lipgloss.Color("22")).Foreground(lipgloss.Color("156")).Bold(true).Padding(0, 1),
		TypeVal:  lipgloss.NewStyle().Background(lipgloss.Color("54")).Foreground(lipgloss.Color("183")).Bold(true).Padding(0, 1),

		TableHeader:   lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true),
		TableSelected: lipgloss.NewStyle().Background(lipgloss.Color("25")).Foreground(lipgloss.Color("231")).Bold(true),

		Section:  lipgloss.NewStyle().Background(lipgloss.Color("237")).Foreground(lipgloss.Color("117")).Bold(true).Padding(0, 1),
		YamlKey:  lipgloss.NewStyle().Foreground(lipgloss.Color("75")),
		Position: lipgloss.NewStyle().Foreground(lipgloss.Color("81")),
	}
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

package theme

import (
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// Animation primitives (FR-040). Each takes a frame counter and returns a
// fixed-width string, so an animated cell never shifts the layout. Meaning
// never rides on color alone: the glyphs move by themselves under NO_COLOR.

var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// Dark-background glow: the iAdvize mint fading through sky and lavender to
// pink. The light palette uses deeper shades of the same hues.
var (
	glowDark  = []color.Color{lipgloss.Color("#3DDFA4"), lipgloss.Color("#5FAFFF"), lipgloss.Color("#AF87FF"), lipgloss.Color("#FF5FAF")}
	glowLight = []color.Color{lipgloss.Color("#1FA97A"), lipgloss.Color("#2F7FD0"), lipgloss.Color("#7E57C2"), lipgloss.Color("#D81B60")}
)

// glowCycle is the glow as a closed loop (last stop back to the first), so
// colors cycling over time never jump.
func (t Theme) glowCycle(steps int) []color.Color {
	g := t.glow()
	stops := append(append([]color.Color{}, g...), g[0])
	return lipgloss.Blend1D(steps, stops...)
}

func (t Theme) glow() []color.Color {
	if len(t.Glow) == 0 {
		return glowDark
	}
	return t.Glow
}

const spinnerHues = 24

// Spinner renders one braille spinner cell whose color travels around the
// glow.
func (t Theme) Spinner(frame int) string {
	glyph := spinnerFrames[mod(frame, len(spinnerFrames))]
	hues := t.glowCycle(spinnerHues)
	return lipgloss.NewStyle().Foreground(hues[mod(frame, len(hues))]).Bold(true).Render(glyph)
}

const cometTail = 8

// Comet renders a track of the given width with a glowing comet bouncing
// along it — an "in progress, duration unknown" indicator.
func (t Theme) Comet(width, frame int) string {
	if width <= 0 {
		return ""
	}
	head, dir := 0, 1
	if width > 1 {
		period := 2 * (width - 1)
		head = mod(frame, period)
		if head >= width {
			head, dir = period-head, -1
		}
	}
	tail := lipgloss.Blend1D(cometTail, t.glow()...)
	var b strings.Builder
	for x := 0; x < width; x++ {
		d := (head - x) * dir
		if d >= 0 && d < cometTail {
			glyph := "━"
			if d >= cometTail/2 {
				glyph = "─" // the tail thins out, readable without color too
			}
			b.WriteString(lipgloss.NewStyle().Foreground(tail[d]).Render(glyph))
			continue
		}
		b.WriteString(t.Faint.Render("─"))
	}
	return b.String()
}

var eighths = []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}

// Bar renders a progress bar of the given width filled to frac (0..1) with
// the glow gradient, a glint sweeping across the filled part, and an
// eighth-block head so small steps still show.
func (t Theme) Bar(width int, frac float64, frame int) string {
	if width <= 0 {
		return ""
	}
	frac = math.Max(0, math.Min(1, frac))
	cells := frac * float64(width)
	full := int(cells)
	part := int((cells - float64(full)) * 8)
	grad := lipgloss.Blend1D(width, t.glow()...)
	glint := -1
	if full > 0 {
		glint = mod(frame, full+6) // a short pause off the end between sweeps
	}
	var b strings.Builder
	for x := 0; x < full; x++ {
		c := grad[x]
		if x == glint {
			c = lipgloss.Lighten(c, 0.45)
		}
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render("█"))
	}
	rest := width - full
	if part > 0 && rest > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(grad[full]).Render(eighths[part]))
		rest--
	}
	b.WriteString(t.Faint.Render(strings.Repeat("░", rest)))
	return b.String()
}

func mod(a, n int) int {
	if n <= 0 {
		return 0
	}
	return ((a % n) + n) % n
}

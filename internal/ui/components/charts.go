// Package components holds reusable, self-contained TUI widgets. Charts are
// rendered with Unicode blocks (no external chart dependency) so they stay
// simple, testable, and degrade to plain text (FR-019, FR-022).
package components

import (
	"fmt"
	"strings"

	"github.com/iadvize/idz-k8s/internal/model"
)

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// Gauge renders a horizontal bar for fraction in [0,1] of the given width, e.g.
// "[■■■■■□□□□□]". Values outside [0,1] are clamped.
func Gauge(fraction float64, width int) string {
	if width < 2 {
		width = 2
	}
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(fraction*float64(width) + 0.5)
	return "[" + strings.Repeat("■", filled) + strings.Repeat("□", width-filled) + "]"
}

// Sparkline renders a compact block sparkline of the values. An empty series
// renders as the explicit "unavailable" marker rather than a blank/misleading
// chart (FR-021).
func Sparkline(values []float64) string {
	if len(values) == 0 {
		return "— unavailable"
	}
	min, max := values[0], values[0]
	for _, v := range values {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	var b strings.Builder
	for _, v := range values {
		idx := 0
		if span > 0 {
			idx = int((v - min) / span * float64(len(sparkRunes)-1))
		}
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkRunes) {
			idx = len(sparkRunes) - 1
		}
		b.WriteRune(sparkRunes[idx])
	}
	return b.String()
}

// FormatCPU formats CPU cores as Kubernetes-style quantities (e.g. 450m, 1.20).
func FormatCPU(cores float64) string {
	if cores < 1 {
		return fmt.Sprintf("%dm", int(cores*1000+0.5))
	}
	return fmt.Sprintf("%.2f", cores)
}

// FormatMemory formats bytes as binary units (Ki/Mi/Gi).
func FormatMemory(bytes float64) string {
	const unit = 1024.0
	switch {
	case bytes >= unit*unit*unit:
		return fmt.Sprintf("%.1fGi", bytes/(unit*unit*unit))
	case bytes >= unit*unit:
		return fmt.Sprintf("%.0fMi", bytes/(unit*unit))
	case bytes >= unit:
		return fmt.Sprintf("%.0fKi", bytes/unit)
	default:
		return fmt.Sprintf("%.0fB", bytes)
	}
}

// FormatValue formats a usage value according to its metric kind.
func FormatValue(kind model.MetricKind, v float64) string {
	if kind == model.MetricMemory {
		return FormatMemory(v)
	}
	return FormatCPU(v)
}

// UsageLine renders one gauge line: "<label> [gauge] <cur>/<limit> (NN%)".
// When usage is unavailable it renders an explicit unavailable line (FR-021).
func UsageLine(label string, u model.Usage, width int) string {
	if !u.Available {
		return fmt.Sprintf("%-7s %s", label, "metrics unavailable")
	}
	denom := u.Limit
	if denom <= 0 {
		denom = u.Request
	}
	pct := ""
	frac := 0.0
	if denom > 0 {
		frac = u.Current / denom
		pct = fmt.Sprintf(" (%d%%)", int(frac*100+0.5))
	}
	ref := "no request/limit"
	if denom > 0 {
		kind := "req"
		if u.Limit > 0 {
			kind = "lim"
		}
		ref = fmt.Sprintf("%s/%s %s", FormatValue(u.Kind, u.Current), FormatValue(u.Kind, denom), kind)
	} else {
		ref = FormatValue(u.Kind, u.Current) + " used, " + ref
	}
	spark := Sparkline(seriesValues(u.Series))
	return fmt.Sprintf("%-7s %s %s%s  %s", label, Gauge(frac, width), ref, pct, spark)
}

func seriesValues(s []model.MetricSample) []float64 {
	out := make([]float64, len(s))
	for i, p := range s {
		out[i] = p.Value
	}
	return out
}

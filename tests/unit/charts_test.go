package unit

import (
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/model"
	"github.com/iadvize/idz-k8s/internal/ui/components"
)

func TestGaugeClampsAndFills(t *testing.T) {
	if g := components.Gauge(0, 10); strings.Count(g, "■") != 0 {
		t.Errorf("0%% gauge should have no filled blocks: %q", g)
	}
	if g := components.Gauge(1, 10); strings.Count(g, "■") != 10 {
		t.Errorf("100%% gauge should be full: %q", g)
	}
	if g := components.Gauge(2, 10); strings.Count(g, "■") != 10 {
		t.Errorf("over-100%% must clamp to full: %q", g)
	}
	if g := components.Gauge(-1, 10); strings.Count(g, "■") != 0 {
		t.Errorf("negative must clamp to empty: %q", g)
	}
}

func TestSparklineEmptyIsUnavailable(t *testing.T) {
	if s := components.Sparkline(nil); !strings.Contains(s, "unavailable") {
		t.Errorf("empty series must render 'unavailable', got %q", s)
	}
	if s := components.Sparkline([]float64{1, 2, 3, 4}); strings.Contains(s, "unavailable") {
		t.Errorf("non-empty series should render blocks, got %q", s)
	}
}

func TestFormatCPUAndMemory(t *testing.T) {
	if got := components.FormatCPU(0.45); got != "450m" {
		t.Errorf("FormatCPU(0.45)=%q want 450m", got)
	}
	if got := components.FormatCPU(1.5); got != "1.50" {
		t.Errorf("FormatCPU(1.5)=%q want 1.50", got)
	}
	if got := components.FormatMemory(512 * 1024 * 1024); got != "512Mi" {
		t.Errorf("FormatMemory(512Mi)=%q", got)
	}
}

func TestUsageLineUnavailable(t *testing.T) {
	line := components.UsageLine("CPU", model.Usage{Kind: model.MetricCPU, Available: false}, 20)
	if !strings.Contains(line, "unavailable") {
		t.Errorf("unavailable usage must say so, got %q", line)
	}
}

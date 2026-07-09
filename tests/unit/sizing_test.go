package unit

import (
	"strings"
	"testing"
	"time"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
)

// TestEvaluateSizingVerdicts: the advisory matrix (FR-023). SC-013: no data →
// SizingNoData, never a fabricated verdict.
func TestEvaluateSizingVerdicts(t *testing.T) {
	cases := []struct {
		name string
		in   model.ResourceSizing
		want model.SizingVerdict
	}{
		{"no data beats everything", model.ResourceSizing{HasData: false, Request: 1, Limit: 2}, model.SizingNoData},
		{"no request", model.ResourceSizing{HasData: true, Avg: 0.1, Peak: 0.2}, model.SizingNoRequest},
		{"at risk near limit", model.ResourceSizing{HasData: true, Avg: 0.5, Peak: 1.9, Request: 1, Limit: 2}, model.SizingUnder},
		{"under: avg above request", model.ResourceSizing{HasData: true, Avg: 1.2, Peak: 1.4, Request: 1, Limit: 4}, model.SizingUnder},
		{"over: peak below half the request", model.ResourceSizing{HasData: true, Avg: 0.1, Peak: 0.3, Request: 1}, model.SizingOver},
		{"ok", model.ResourceSizing{HasData: true, Avg: 0.5, Peak: 0.8, Request: 1, Limit: 2}, model.SizingOK},
	}
	for _, c := range cases {
		if got := model.EvaluateSizing(c.in).Verdict; got != c.want {
			t.Errorf("%s: verdict=%d want %d", c.name, got, c.want)
		}
	}
}

// TestSizingPromQLShapes: queries target the conventional metric names, anchor
// the pod set exactly, and observe the requested window.
func TestSizingPromQLShapes(t *testing.T) {
	pods := []string{"web-1", "web-2"}
	avgCPU := metrics.WorkloadAvgPerPod("demo", pods, model.MetricCPU, time.Hour)
	for _, want := range []string{
		`container_cpu_usage_seconds_total`, `namespace="demo"`, `pod=~"^(web-1|web-2)$"`,
		`avg_over_time`, `[60m:1m]`, `sum by (pod)`,
	} {
		if !strings.Contains(avgCPU, want) {
			t.Errorf("avg cpu query missing %q:\n%s", want, avgCPU)
		}
	}
	peakMem := metrics.WorkloadPeakPerPod("demo", pods, model.MetricMemory, time.Hour)
	for _, want := range []string{`container_memory_working_set_bytes`, `max_over_time`, `[60m:1m]`} {
		if !strings.Contains(peakMem, want) {
			t.Errorf("peak mem query missing %q:\n%s", want, peakMem)
		}
	}
	if strings.Contains(peakMem, "rate(") {
		t.Error("memory query must not rate() a gauge")
	}
}

// TestWorkloadSizingDisabledClient: no metrics source → HasData stays false on
// both resources (the UI then states "no recommendation").
func TestWorkloadSizingDisabledClient(t *testing.T) {
	var c metrics.Client // zero value = disabled
	cpu, mem := c.WorkloadSizing(t.Context(), "demo", []string{"web-1"}, time.Hour)
	if cpu.HasData || mem.HasData {
		t.Fatal("disabled client must never report observed data")
	}
	if model.EvaluateSizing(cpu).Verdict != model.SizingNoData {
		t.Fatal("verdict without data must be SizingNoData")
	}
}

// TestScopeQueriesShapes: the overview's four batch queries aggregate by
// (namespace,pod) and honor the exact-namespace matcher only when given one.
func TestScopeQueriesShapes(t *testing.T) {
	q := metrics.ScopeAvgByPod("demo", model.MetricCPU, time.Hour)
	for _, want := range []string{`sum by (namespace,pod)`, `namespace="demo"`, `avg_over_time`, `[60m:1m]`} {
		if !strings.Contains(q, want) {
			t.Errorf("scoped avg query missing %q:\n%s", want, q)
		}
	}
	all := metrics.ScopePeakByPod("", model.MetricMemory, time.Hour)
	if strings.Contains(all, "namespace=") {
		t.Errorf("cluster-wide query must not pin a namespace:\n%s", all)
	}
	if !strings.Contains(all, "max_over_time") || !strings.Contains(all, "container_memory_working_set_bytes") {
		t.Errorf("peak mem query malformed:\n%s", all)
	}
}

// TestAgeSecondPrecisionDuringStartup (owner request 2026-07-09): ages under
// five minutes show seconds ("2m31s") so app startup is watchable; the
// coarser buckets are unchanged.
func TestAgeSecondPrecisionDuringStartup(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		12 * time.Second:               "12s",
		2*time.Minute + 31*time.Second: "2m31s",
		4*time.Minute + 59*time.Second: "4m59s",
		5 * time.Minute:                "5m",
		42 * time.Minute:               "42m",
		3 * time.Hour:                  "3h",
		49 * time.Hour:                 "2d",
	}
	for d, want := range cases {
		if got := kube.Age(now.Add(-d), now); got != want {
			t.Errorf("Age(%v)=%q want %q", d, got, want)
		}
	}
}

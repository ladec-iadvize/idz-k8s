package unit

import (
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
)

// TestPromQLBuilders pins the query shapes (a rename would silently break all
// usage visuals into "unavailable" — FR-021 hides the breakage by design).
func TestPromQLBuilders(t *testing.T) {
	q := metrics.PodUsage("demo", "web-1", model.MetricCPU)
	for _, want := range []string{"container_cpu_usage_seconds_total", `namespace="demo"`, `pod="web-1"`, "rate("} {
		if !strings.Contains(q, want) {
			t.Errorf("cpu query missing %q: %s", want, q)
		}
	}
	q = metrics.PodUsage("demo", "web-1", model.MetricMemory)
	if !strings.Contains(q, "container_memory_working_set_bytes") {
		t.Errorf("memory query wrong: %s", q)
	}
	q = metrics.TopPods(15, model.MetricCPU)
	if !strings.Contains(q, "topk(15") {
		t.Errorf("top query wrong: %s", q)
	}
}

// TestPromQLRangeAndScopeBuilders pins the 1h-sparkline and usage-view
// scope queries, which had no shape guard at all.
func TestPromQLRangeAndScopeBuilders(t *testing.T) {
	if got, want := metrics.PodUsageRange("demo", "web-1", model.MetricCPU),
		metrics.PodUsage("demo", "web-1", model.MetricCPU); got != want {
		t.Errorf("range query must reuse the instant expression, got %s", got)
	}

	q := metrics.ScopeNowByPod("demo", model.MetricCPU)
	for _, want := range []string{"sum by (namespace,pod)", `namespace="demo"`, "rate(container_cpu_usage_seconds_total"} {
		if !strings.Contains(q, want) {
			t.Errorf("scoped cpu query missing %q: %s", want, q)
		}
	}
	q = metrics.ScopeNowByPod("", model.MetricMemory)
	if strings.Contains(q, "namespace=") {
		t.Errorf("cluster-wide query must not pin a namespace: %s", q)
	}
	if !strings.Contains(q, "container_memory_working_set_bytes") {
		t.Errorf("memory scope query wrong: %s", q)
	}
}

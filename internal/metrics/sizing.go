package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/iadvize/idz-k8s/internal/model"
)

// Sizing queries (US6): per-pod average and peak usage of a workload's pods
// over the trend window, via PromQL subqueries. As everywhere, a missing
// answer yields HasData=false — the UI then says "no recommendation" and
// never estimates (FR-021/FR-023).

// podsRegex anchors an exact-match alternation for a set of pod names.
func podsRegex(pods []string) string { return "^(" + strings.Join(pods, "|") + ")$" }

// sizingBase is the per-pod usage vector for the given pods.
func sizingBase(namespace string, pods []string, kind model.MetricKind) string {
	if kind == model.MetricMemory {
		return fmt.Sprintf(`sum by (pod) (container_memory_working_set_bytes{namespace=%q,pod=~%q,container!=""})`,
			namespace, podsRegex(pods))
	}
	return fmt.Sprintf(`sum by (pod) (rate(container_cpu_usage_seconds_total{namespace=%q,pod=~%q,container!=""}[5m]))`,
		namespace, podsRegex(pods))
}

// WorkloadAvgPerPod builds the average per-pod usage over the window.
func WorkloadAvgPerPod(namespace string, pods []string, kind model.MetricKind, window time.Duration) string {
	return fmt.Sprintf(`avg(avg_over_time((%s)[%dm:1m]))`, sizingBase(namespace, pods, kind), int(window.Minutes()))
}

// WorkloadPeakPerPod builds the maximum per-pod usage over the window.
func WorkloadPeakPerPod(namespace string, pods []string, kind model.MetricKind, window time.Duration) string {
	return fmt.Sprintf(`max(max_over_time((%s)[%dm:1m]))`, sizingBase(namespace, pods, kind), int(window.Minutes()))
}

// WorkloadSizing observes the pods over the window for both resource kinds.
// Requests/limits are NOT filled here (they come from the cluster, not from
// Prometheus); verdicts are left to model.EvaluateSizing.
func (c *Client) WorkloadSizing(ctx context.Context, namespace string, pods []string, window time.Duration) (cpu, mem model.ResourceSizing) {
	cpu.Kind, mem.Kind = model.MetricCPU, model.MetricMemory
	if !c.Enabled() || len(pods) == 0 {
		return cpu, mem
	}
	if avg, ok := c.InstantScalar(ctx, WorkloadAvgPerPod(namespace, pods, model.MetricCPU, window)); ok {
		if peak, ok2 := c.InstantScalar(ctx, WorkloadPeakPerPod(namespace, pods, model.MetricCPU, window)); ok2 {
			cpu.Avg, cpu.Peak, cpu.HasData = avg, peak, true
		}
	}
	if avg, ok := c.InstantScalar(ctx, WorkloadAvgPerPod(namespace, pods, model.MetricMemory, window)); ok {
		if peak, ok2 := c.InstantScalar(ctx, WorkloadPeakPerPod(namespace, pods, model.MetricMemory, window)); ok2 {
			mem.Avg, mem.Peak, mem.HasData = avg, peak, true
		}
	}
	return cpu, mem
}

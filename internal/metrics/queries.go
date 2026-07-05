package metrics

import (
	"fmt"

	"github.com/iadvize/idz-k8s/internal/model"
)

// PromQL builders. These target the standard cAdvisor/kube-state metric names
// exposed by a typical Prometheus + kubelet setup. Names are conventional; if a
// cluster uses different exporters the queries simply return no data and the UI
// shows "unavailable" (never a fabricated value).

// PodUsage returns the instant PromQL for a pod's CPU (cores) or memory (bytes).
func PodUsage(namespace, pod string, kind model.MetricKind) string {
	if kind == model.MetricMemory {
		return fmt.Sprintf(`sum(container_memory_working_set_bytes{namespace=%q,pod=%q,container!=""})`, namespace, pod)
	}
	return fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace=%q,pod=%q,container!=""}[5m]))`, namespace, pod)
}

// PodUsageRange returns the range PromQL (same expression; evaluated over a window).
func PodUsageRange(namespace, pod string, kind model.MetricKind) string {
	return PodUsage(namespace, pod, kind)
}

// TopPods returns the instant PromQL for the top-N pods by CPU or memory,
// labelled by namespace and pod.
func TopPods(n int, kind model.MetricKind) string {
	if kind == model.MetricMemory {
		return fmt.Sprintf(`topk(%d, sum by (namespace,pod) (container_memory_working_set_bytes{container!=""}))`, n)
	}
	return fmt.Sprintf(`topk(%d, sum by (namespace,pod) (rate(container_cpu_usage_seconds_total{container!=""}[5m])))`, n)
}

package unit

import (
	"testing"

	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
	"github.com/iadvize/idz-k8s/internal/ui/keys"
)

// TestEveryBindingHasHelp keeps the help overlay in sync with the keymap:
// a binding without help text would be undiscoverable (FR-010).
func TestEveryBindingHasHelp(t *testing.T) {
	k := keys.Default()
	for _, b := range []struct {
		name string
		keys []string
		help string
	}{
		{"Up", k.Up.Keys(), k.Up.Help().Key}, {"Down", k.Down.Keys(), k.Down.Help().Key},
		{"Open", k.Open.Keys(), k.Open.Help().Key}, {"Back", k.Back.Keys(), k.Back.Help().Key},
		{"Filter", k.Filter.Keys(), k.Filter.Help().Key}, {"Jump", k.Jump.Keys(), k.Jump.Help().Key},
		{"Logs", k.Logs.Keys(), k.Logs.Help().Key}, {"Yaml", k.Yaml.Keys(), k.Yaml.Help().Key},
		{"Describe", k.Describe.Keys(), k.Describe.Help().Key}, {"Owner", k.Owner.Keys(), k.Owner.Help().Key},
		{"Top", k.Top.Keys(), k.Top.Help().Key}, {"Diag", k.Diag.Keys(), k.Diag.Help().Key},
		{"Topology", k.Topology.Keys(), k.Topology.Help().Key}, {"Events", k.Events.Keys(), k.Events.Help().Key},
		{"Mark", k.Mark.Keys(), k.Mark.Help().Key},
		{"Sort", k.Sort.Keys(), k.Sort.Help().Key}, {"Values", k.Values.Keys(), k.Values.Help().Key},
		{"Pause", k.Pause.Keys(), k.Pause.Help().Key}, {"WarnOnly", k.WarnOnly.Keys(), k.WarnOnly.Help().Key},
		{"Mouse", k.Mouse.Keys(), k.Mouse.Help().Key}, {"Kind", k.Kind.Keys(), k.Kind.Help().Key},
		{"Namespace", k.Namespace.Keys(), k.Namespace.Help().Key}, {"Context", k.Context.Keys(), k.Context.Help().Key},
		{"Help", k.Help.Keys(), k.Help.Help().Key}, {"Quit", k.Quit.Keys(), k.Quit.Help().Key},
	} {
		if len(b.keys) == 0 {
			t.Errorf("%s: no key bound", b.name)
		}
		if b.help == "" {
			t.Errorf("%s: no help text (undiscoverable)", b.name)
		}
	}
}

// TestPromQLBuilders pins the query shapes (a rename would silently break all
// usage visuals into "unavailable").
func TestPromQLBuilders(t *testing.T) {
	q := metrics.PodUsage("demo", "web-1", model.MetricCPU)
	for _, want := range []string{"container_cpu_usage_seconds_total", `namespace="demo"`, `pod="web-1"`, "rate("} {
		if !contains(q, want) {
			t.Errorf("cpu query missing %q: %s", want, q)
		}
	}
	q = metrics.PodUsage("demo", "web-1", model.MetricMemory)
	if !contains(q, "container_memory_working_set_bytes") {
		t.Errorf("memory query wrong: %s", q)
	}
	q = metrics.TopPods(15, model.MetricCPU)
	if !contains(q, "topk(15") {
		t.Errorf("top query wrong: %s", q)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

package ui

// Empty lists must explain themselves (owner report 2026-08-03: drilling a
// CronJob whose Jobs were cleaned up by its history limits showed a blank
// table plus "broken link?", which reads as a bug).

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func emptyStateModel(t *testing.T, kind, resource string, group string) Model {
	t.Helper()
	ty := model.ResourceType{Group: group, Version: "v1", Kind: kind, Resource: resource, Namespaced: true}
	m := New(&kube.Client{Namespace: ""}, config.Defaults(), "", WithInitialType(ty))
	m.width, m.height = 120, 20
	m.layout()
	m.applyRows()
	return m
}

// TestEmptyCronJobDrillExplainsHistoryLimits: the reported case. An empty Job
// list under a CronJob is NORMAL — it must say so (and point at 'trigger'),
// never call it a broken link.
func TestEmptyCronJobDrillExplainsHistoryLimits(t *testing.T) {
	m := emptyStateModel(t, "Job", "jobs", "batch")
	m.drillStack = []drillFrame{{typ: model.ResourceType{Kind: "CronJob", Resource: "cronjobs"}}}
	m.drillFor = "CronJob/nightly"
	m.drillOwnerUID = "cron-uid-1"
	m.drillNamespace = "demo"

	note := m.emptyDrillNote()
	for _, want := range []string{"no jobs right now", "history", "trigger", "Esc"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note must mention %q, got %q", want, note)
		}
	}
	if strings.Contains(note, "broken link") {
		t.Fatalf("a CronJob between runs is not a broken link: %q", note)
	}
	// The explanation is rendered in the body, where the void is.
	view := xansi.Strip(m.listView())
	if !strings.Contains(view, "no jobs right now") {
		t.Fatalf("the empty body must carry the note:\n%s", view)
	}
	// Geometry is untouched: the body still fills exactly its height.
	if got, want := strings.Count(m.listView(), "\n")+1, m.win.height+1; got != want {
		t.Fatalf("body lines=%d, want %d (header + win height)", got, want)
	}
	// The status line stays short so it never truncates.
	if w := m.shortDrillWarning(); len([]rune(w)) > 80 || !strings.Contains(w, "⚠") {
		t.Fatalf("short warning=%q", w)
	}
}

// TestEmptySelectorDrillStillFlagsBrokenLink: for a workload/Service the
// emptiness IS the symptom — that wording must survive.
func TestEmptySelectorDrillStillFlagsBrokenLink(t *testing.T) {
	m := emptyStateModel(t, "Pod", "pods", "")
	m.drillStack = []drillFrame{{typ: model.ResourceType{Kind: "Service", Resource: "services"}}}
	m.drillFor = "Service/front"
	m.drillSelector = "app=front"

	note := m.emptyDrillNote()
	if !strings.Contains(note, "broken link") || !strings.Contains(note, "selector") {
		t.Fatalf("an empty selector drill must stay diagnostic, got %q", note)
	}

	// Ingress → its backend services: also a genuine broken link.
	m2 := emptyStateModel(t, "Service", "services", "")
	m2.drillStack = []drillFrame{{typ: model.ResourceType{Kind: "Ingress", Resource: "ingresses"}}}
	m2.drillFor = "Ingress/web"
	m2.drillNames = map[string]bool{"front": true}
	if note := m2.emptyDrillNote(); !strings.Contains(note, "backend") {
		t.Fatalf("ingress note=%q", note)
	}

	// Node → pods: neutral, no alarm.
	m3 := emptyStateModel(t, "Pod", "pods", "")
	m3.drillStack = []drillFrame{{typ: model.ResourceType{Kind: "Node", Resource: "nodes"}}}
	m3.drillFor = "Node/ip-10-0-1-2"
	m3.drillNode = "ip-10-0-1-2"
	if note := m3.emptyDrillNote(); !strings.Contains(note, "ip-10-0-1-2") || strings.Contains(note, "broken") {
		t.Fatalf("node note=%q", note)
	}
}

// TestEmptyListExplainsFilterAndScope: outside a drill, an empty list names
// the reason — an unmatched filter, an empty scope, or a lost connection.
func TestEmptyListExplainsFilterAndScope(t *testing.T) {
	m := emptyStateModel(t, "Pod", "pods", "")
	if note := m.emptyListNote(); !strings.Contains(note, "no pods in any namespace") {
		t.Fatalf("all-namespaces note=%q", note)
	}

	m.client.Namespace = "demo"
	if note := m.emptyListNote(); !strings.Contains(note, "namespace demo") {
		t.Fatalf("single-namespace note=%q", note)
	}

	m.client.Namespace = "staging-*,prod"
	if note := m.emptyListNote(); !strings.Contains(note, "namespaces matching") {
		t.Fatalf("multi-scope note=%q", note)
	}

	m.filter.SetValue("nope")
	note := m.emptyListNote()
	if !strings.Contains(note, "filter:nope") || !strings.Contains(note, "Esc clears") {
		t.Fatalf("filter note must name the filter and how to clear it, got %q", note)
	}

	// An outage must never read as an empty cluster (FR-016/FR-021).
	m.filter.SetValue("")
	m.disconnected = true
	if note := m.emptyListNote(); !strings.Contains(note, "unreachable") {
		t.Fatalf("disconnected note=%q", note)
	}
}

// TestEmptyDrillStatusFromObjectsMsg: the objectsMsg path sets the short
// warning (not the old "broken link?" sentence) when a drill lands empty.
func TestEmptyDrillStatusFromObjectsMsg(t *testing.T) {
	m := emptyStateModel(t, "Job", "jobs", "batch")
	m.drillStack = []drillFrame{{typ: model.ResourceType{Kind: "CronJob", Resource: "cronjobs"}}}
	m.drillFor = "CronJob/nightly"
	m.drillOwnerUID = "cron-uid-1"

	mi, _ := m.Update(objectsMsg{objects: nil})
	m = asModel(t, mi)
	if !strings.Contains(m.statusMsg, "CronJob/nightly") || strings.Contains(m.statusMsg, "broken link") {
		t.Fatalf("status=%q", m.statusMsg)
	}
	if !strings.Contains(xansi.Strip(m.View()), "history") {
		t.Fatalf("the view must explain the empty drill:\n%s", xansi.Strip(m.View()))
	}
	_ = tea.KeyMsg{}
}

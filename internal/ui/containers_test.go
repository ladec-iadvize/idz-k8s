package ui

// The containers view surfaces WHY a container stopped last time (owner
// request 2026-08-05): an OOMKill is invisible in the current state once the
// container is running again.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// oomPodModel lists one pod whose container was OOMKilled and restarted.
func oomPodModel(t *testing.T) Model {
	t.Helper()
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	raw := map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "web-1", "namespace": "demo"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "app", "image": "app:1"}}},
		"status": map[string]any{"phase": "Running", "containerStatuses": []any{
			map[string]any{"name": "app", "ready": true, "restartCount": int64(4),
				"state": map[string]any{"running": map[string]any{}},
				"lastState": map[string]any{"terminated": map[string]any{
					"reason": "OOMKilled", "exitCode": int64(137)}}},
		}},
	}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 160, 30
	m.layout()
	m.objects = []model.ResourceObject{{Type: pods, Namespace: "demo", Name: "web-1", Raw: raw}}
	m.applyRows()
	return m
}

// TestRestartsColumnStaysAPlainCount (owner decision 2026-08-06: the
// termination reason belongs to the events timeline and the containers
// view, not to this column — it was too noisy in the pod lists).
func TestRestartsColumnStaysAPlainCount(t *testing.T) {
	m := oomPodModel(t)
	row, ok := m.win.Selected()
	if !ok {
		t.Fatal("no row")
	}
	for i, c := range m.columnsForType() {
		if c.title != "RESTARTS" {
			continue
		}
		if got := row[i+1]; got != "4" { // +1: the mark column
			t.Fatalf("RESTARTS must be a plain count, got %q", got)
		}
		if c.less == nil {
			t.Fatal("RESTARTS must keep its numeric comparator")
		}
	}
}

// TestContainersViewShowsLastTermination: Enter on the pod lists the
// container with its previous termination.
func TestContainersViewShowsLastTermination(t *testing.T) {
	m := oomPodModel(t)
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.screen != screenContainers {
		t.Fatalf("Enter must open the containers view, screen=%d", m.screen)
	}
	if len(m.containerRows) != 1 || m.containerRows[0].LastTerminated != "OOMKilled (exit 137)" {
		t.Fatalf("container rows=%+v", m.containerRows)
	}
	view := xansi.Strip(m.containersView())
	for _, want := range []string{"LAST TERMINATION", "OOMKilled (exit 137)", "Running"} {
		if !strings.Contains(view, want) {
			t.Fatalf("containers view missing %q:\n%s", want, view)
		}
	}
}

// TestLastTerminationColumnAvailableInChooser: the pods list offers a
// dedicated column too — off by default, like every extra column
// (kubectl -o wide parity rule).
func TestLastTerminationColumnAvailableInChooser(t *testing.T) {
	m := oomPodModel(t)
	found, visibleByDefault := false, false
	for _, c := range m.columnsBase() {
		if c.title == "LAST TERMINATION" {
			found = true
			visibleByDefault = !c.off
			if got := c.cell(&m, m.objects[0]); !strings.Contains(got, "OOMKilled") {
				t.Fatalf("the column must render the reason, got %q", got)
			}
		}
	}
	if !found {
		t.Fatal("LAST TERMINATION must exist as a choosable column")
	}
	if visibleByDefault {
		t.Fatal("extra columns stay off by default (kubectl -o wide parity)")
	}
	_ = kube.Age
}

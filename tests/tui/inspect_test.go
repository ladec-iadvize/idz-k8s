// Package tui holds teatest-based interaction tests for the Bubble Tea program.
package tui

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/ui"
	"github.com/iadvize/idz-k8s/tests/integration"
)

func waitForContains(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	waitForAll(t, tm, want)
}

// waitForAll waits for ONE frame containing every wanted substring. Chaining
// several waitForContains calls cannot work: teatest's output is a stream and
// each call consumes it, so an already-read frame is gone for the next call.
func waitForAll(t *testing.T, tm *teatest.TestModel, wants ...string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		for _, w := range wants {
			if !bytes.Contains(b, []byte(w)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(5*time.Second), teatest.WithCheckInterval(50*time.Millisecond))
}

func TestListThenDetailRendersPod(t *testing.T) {
	client, _ := integration.NewFakeClient("demo",
		integration.NewPod("demo", "web-1", "Running"),
		integration.NewPod("demo", "web-2", "Pending"),
	)
	m := ui.New(client, config.Defaults(), "", ui.WithInitialType(integration.PodsType))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))

	// List renders the pods (US1).
	waitForContains(t, tm, "web-1")

	// 'y' opens the YAML detail → kind: Pod (FR-004). Enter now walks the
	// drill chain into the pod's containers (owner request 2026-07-31).
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	waitForContains(t, tm, "kind: Pod")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestEnterOnPodOpensContainers: the drill chain's leaf — Enter on a pod
// lists its containers with their state.
func TestEnterOnPodOpensContainers(t *testing.T) {
	pod := integration.NewPod("demo", "web-1", "Running")
	pod.Object["spec"] = map[string]any{"containers": []any{
		map[string]any{"name": "app", "image": "nginx:1.27"},
		map[string]any{"name": "sidecar", "image": "envoy:1.30"},
	}}
	pod.Object["status"].(map[string]any)["containerStatuses"] = []any{
		map[string]any{"name": "app", "ready": true, "restartCount": int64(2),
			"state": map[string]any{"running": map[string]any{}}},
		map[string]any{"name": "sidecar", "ready": false,
			"state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}},
	}
	client, _ := integration.NewFakeClient("demo", pod)
	m := ui.New(client, config.Defaults(), "", ui.WithInitialType(integration.PodsType))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 30))

	waitForContains(t, tm, "web-1")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForAll(t, tm, "sidecar", "CrashLoopBackOff", "nginx:1.27")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

func TestSecretMaskedByDefault(t *testing.T) {
	client, _ := integration.NewFakeClient("demo",
		integration.NewSecret("demo", "creds"),
	)
	m := ui.New(client, config.Defaults(), "", ui.WithInitialType(integration.SecretsType))
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))

	waitForContains(t, tm, "creds")

	// Open detail: the secret value must be masked, not shown in clear (FR-015).
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForContains(t, tm, "••••••")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

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
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(want))
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

	// Open detail on the selected pod → YAML shows kind: Pod (FR-004).
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitForContains(t, tm, "kind: Pod")

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

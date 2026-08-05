package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func eventsModel(t *testing.T) Model {
	t.Helper()
	now := time.Now()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}))
	m.width, m.height = 100, 30
	m.layout()
	m.screen = screenEvents
	m.eventRows = []model.Event{
		{Time: now.Add(-1 * time.Minute), Type: "Warning", Reason: "BackOff", ObjKind: "Pod", ObjName: "api-7c9"},
		{Time: now.Add(-5 * time.Minute), Type: "Normal", Reason: "ScalingReplicaSet", ObjKind: "Deployment", ObjName: "api"},
	}
	m.renderEvents()
	return m
}

func send(t *testing.T, m Model, msg tea.KeyMsg) Model {
	t.Helper()
	mi, _ := m.Update(msg)
	switch v := mi.(type) {
	case Model:
		return v
	case *Model:
		return *v
	default:
		t.Fatalf("unexpected model type %T", mi)
		return m
	}
}

func typeRunes(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestEventsFilterTypingSwallowsGlobalKeys(t *testing.T) {
	m := eventsModel(t)
	// Enter typing mode with '/' then type "quick" — 'q' must NOT quit,
	// 'n' must NOT open the namespace picker.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.eventsFiltering {
		t.Fatal("'/' should enter filter typing mode")
	}
	m = typeRunes(t, m, "qn")
	if m.screen != screenEvents {
		t.Fatalf("typing 'n' while filtering must stay on events, got screen=%d", m.screen)
	}
	if m.eventsQuery != "qn" {
		t.Fatalf("query=%q want qn", m.eventsQuery)
	}
	// Enter commits and exits typing mode, keeping the query.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.eventsFiltering || m.eventsQuery != "qn" {
		t.Fatalf("Enter must save the filter, got filtering=%v query=%q", m.eventsFiltering, m.eventsQuery)
	}
	// Esc while typing cancels.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.eventsQuery != "" {
		t.Fatalf("Esc must cancel the filter, got %q", m.eventsQuery)
	}
}

func TestEventsNamespaceReachableAfterCommit(t *testing.T) {
	m := eventsModel(t)
	// Outside typing mode, 'n' opens the namespace picker and returns to events.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if m.screen != screenPicker || m.pickerReturn != screenEvents {
		t.Fatalf("'n' should open picker returning to events, screen=%d return=%d", m.screen, m.pickerReturn)
	}
	// Esc goes back to the timeline, not the list.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.screen != screenEvents {
		t.Fatalf("Esc from picker must return to events, got %d", m.screen)
	}
}

func TestEventsKindPickerFilters(t *testing.T) {
	m := eventsModel(t)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if m.screen != screenPicker || m.pickerKind != pickEventKind {
		t.Fatalf("'k' should open the kind picker, screen=%d kind=%d", m.screen, m.pickerKind)
	}
	// Options include the sentinel + both kinds present in the events.
	joined := strings.Join(m.pickerOpts, ",")
	for _, want := range []string{allKindsLabel, "Pod", "Deployment"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("kind options missing %q: %v", want, m.pickerOpts)
		}
	}
	// Select "Deployment" via type-to-filter + Enter.
	m = typeRunes(t, m, "deploy")
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.screen != screenEvents || m.eventsKind != "Deployment" {
		t.Fatalf("selecting kind should filter timeline, screen=%d kind=%q", m.screen, m.eventsKind)
	}
	if !strings.Contains(m.events.View(), "kind:[Deployment") {
		t.Error("header should show the selected kind")
	}
	if strings.Contains(m.events.View(), "api-7c9") {
		t.Error("Pod events must be filtered out when kind=Deployment")
	}
}

func TestOpenEventsPresetsKindFromCurrentView(t *testing.T) {
	m := eventsModel(t)
	// Browsing deployments → 'v' pre-sets kind:[Deployment].
	m.screen = screenList
	m.curType = model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	mi, _ := m.openEvents() // reached via the '>' palette since 2026-07-12
	m = asModel(t, mi)
	if m.screen != screenEvents {
		t.Fatalf("events view should open, got screen=%d", m.screen)
	}
	if m.eventsKind != "Deployment" {
		t.Fatalf("kind should be pre-set to Deployment, got %q", m.eventsKind)
	}
	m.renderEvents()
	view := m.events.View()
	if !strings.Contains(view, "kind:[Deployment") {
		t.Error("header should show the pre-set kind")
	}
	if strings.Contains(view, "api-7c9") {
		t.Error("Pod events must be hidden when kind is pre-set to Deployment")
	}
}

func TestEventsSelectionWalksAllEvents(t *testing.T) {
	m := eventsModel(t)
	// 12 events on the same object, minutes apart.
	now := time.Now()
	m.eventRows = nil
	for i := 0; i < 12; i++ {
		m.eventRows = append(m.eventRows, model.Event{
			Time: now.Add(-time.Duration(i) * time.Minute), Type: "Normal",
			Reason: "Pulled", ObjKind: "Pod", ObjName: "web-1",
		})
	}
	m.recentSel, m.recentWin = 0, 0
	m.renderEvents()

	// Walk down past the initial window of 8: selection and window must follow.
	for i := 0; i < 10; i++ {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.recentSel != 10 {
		t.Fatalf("selection should reach index 10, got %d", m.recentSel)
	}
	if m.recentWin == 0 {
		t.Fatal("window should have scrolled to keep the selection visible")
	}
	view := m.events.View()
	if !strings.Contains(view, "Events (11/12)") {
		t.Errorf("position indicator missing, got header-less view")
	}
	if !strings.Contains(view, "more recent") {
		t.Errorf("scrolled window should indicate hidden newer events")
	}
	// Walk back up beyond the window start.
	for i := 0; i < 10; i++ {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.recentSel != 0 || m.recentWin != 0 {
		t.Fatalf("selection/window should return to top, got sel=%d win=%d", m.recentSel, m.recentWin)
	}
}

// TestEventsViewUsesTerminalWidth (owner report 2026-07-29): lane names were
// hard-cut at 30 runes and event messages at 60 while half a wide screen sat
// empty — both now size with the content and the terminal.
func TestEventsViewUsesTerminalWidth(t *testing.T) {
	now := time.Now()
	longName := "stats-presence-collector-auto-worker-58ffd747f8-dqjl8"
	longMsg := "Successfully pulled image \"824262939987.dkr.ecr.eu-central-1.amazonaws.com/stats-presence-collector:1.2.3\" in 1.2s"
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}))
	m.width, m.height = 220, 40
	m.layout()
	m.screen = screenEvents
	m.eventRows = []model.Event{
		{Time: now.Add(-1 * time.Minute), Type: "Normal", Reason: "Pulled", ObjKind: "Pod", ObjName: longName, Message: longMsg},
	}
	m.renderEvents()
	view := m.events.View()
	if !strings.Contains(view, "Pod/"+longName) {
		t.Fatalf("lane name must show in full on a 220-col terminal:\n%s", view)
	}
	if !strings.Contains(view, longMsg) {
		t.Fatalf("event message must use the available width, not stop at 60 runes:\n%s", view)
	}

	// A narrow terminal still never overflows its width.
	m.width = 80
	m.layout()
	m.renderEvents()
	for _, line := range strings.Split(xansi.Strip(m.events.View()), "\n") {
		if w := len([]rune(line)); w > 80 {
			t.Fatalf("line exceeds the 80-col terminal (%d): %q", w, line)
		}
	}
}

// TestEventsTimelineScale (owner request 2026-08-05): 't' cycles the time
// window, which filters the events AND rescales the axis; the chip and the
// legend name the current scale, and an empty window says how to widen it.
func TestEventsTimelineScale(t *testing.T) {
	now := time.Now()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}))
	m.width, m.height = 140, 40
	m.layout()
	m.screen = screenEvents
	m.eventRows = []model.Event{
		{Time: now.Add(-1 * time.Minute), Type: "Warning", Reason: "BackOff", ObjKind: "Pod", ObjName: "fresh"},
		{Time: now.Add(-30 * time.Minute), Type: "Normal", Reason: "Pulled", ObjKind: "Pod", ObjName: "halfhour"},
		{Time: now.Add(-10 * time.Hour), Type: "Normal", Reason: "Scheduled", ObjKind: "Pod", ObjName: "ancient"},
	}
	m.renderEvents()
	if len(m.eventsShown) != 3 || m.eventsWindow != 0 {
		t.Fatalf("default scale must show everything: shown=%d window=%v", len(m.eventsShown), m.eventsWindow)
	}
	if !strings.Contains(xansi.Strip(m.events.View()), "scale:[all ▾]") {
		t.Fatalf("the scale chip must be visible:\n%s", xansi.Strip(m.events.View()))
	}

	// 't' → 5m: only the fresh event survives, and the axis rescales to it.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if m.eventsWindow != 5*time.Minute {
		t.Fatalf("first 't' must select 5m, got %v", m.eventsWindow)
	}
	if len(m.eventsShown) != 1 || m.eventsShown[0].ObjName != "fresh" {
		t.Fatalf("the 5m window must keep only the recent event: %+v", m.eventsShown)
	}
	view := xansi.Strip(m.events.View())
	if !strings.Contains(view, "scale:[5m ▾]") || strings.Contains(view, "ancient") {
		t.Fatalf("view must reflect the 5m scale:\n%s", view)
	}

	// 't' again → 15m keeps the 1-minute event, still not the 30-minute one.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if m.eventsWindow != 15*time.Minute || len(m.eventsShown) != 1 {
		t.Fatalf("15m window: %v shown=%d", m.eventsWindow, len(m.eventsShown))
	}
	// → 1h picks up the half-hour one.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if m.eventsWindow != time.Hour || len(m.eventsShown) != 2 {
		t.Fatalf("1h window: %v shown=%d", m.eventsWindow, len(m.eventsShown))
	}
	// Cycling all the way round comes back to "all".
	for range 3 {
		m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	}
	if m.eventsWindow != 0 || len(m.eventsShown) != 3 {
		t.Fatalf("the cycle must return to all: %v shown=%d", m.eventsWindow, len(m.eventsShown))
	}

	// An empty window explains how to widen it instead of just "no events".
	m.eventRows = []model.Event{{Time: now.Add(-2 * time.Hour), Type: "Normal", Reason: "Pulled",
		ObjKind: "Pod", ObjName: "old"}}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}}) // 5m
	if got := xansi.Strip(m.events.View()); !strings.Contains(got, "'t' widens the scale") {
		t.Fatalf("an empty scale must say how to widen it:\n%s", got)
	}
}

// TestEventsDetailRowsGrowWithHeight: a taller terminal shows more events and
// more lanes (owner request: "voir plus d'events").
func TestEventsDetailRowsGrowWithHeight(t *testing.T) {
	now := time.Now()
	rows := make([]model.Event, 0, 40)
	for i := range 40 {
		rows = append(rows, model.Event{Time: now.Add(-time.Duration(i) * time.Minute),
			Type: "Normal", Reason: "Pulled", ObjKind: "Pod",
			ObjName: fmt.Sprintf("pod-%02d", i), Namespace: "demo"})
	}
	count := func(height int) int {
		m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
			WithInitialType(model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}))
		m.width, m.height = 140, height
		m.layout()
		m.screen = screenEvents
		m.eventRows = rows
		m.renderEvents()
		return m.recentShown
	}
	small, large := count(30), count(60)
	if large <= small {
		t.Fatalf("a taller terminal must detail more events: %d rows at h=30 vs %d at h=60", small, large)
	}
}

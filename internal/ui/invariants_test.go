package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// TestTypingBackspaceDeletesRuneNotByte: every typing mode must delete the
// last RUNE on Backspace — byte-truncation left an invalid UTF-8 tail on
// screen after deleting a multi-byte character ('é', view names in French…).
func TestTypingBackspaceDeletesRuneNotByte(t *testing.T) {
	m := eventsModel(t)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeRunes(t, m, "café")
	m = send(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.eventsQuery != "caf" {
		t.Fatalf("backspace after 'café' should leave 'caf', got %q (bytes: %x)", m.eventsQuery, m.eventsQuery)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = send(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	m = send(t, m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.eventsQuery != "" {
		t.Fatalf("three more backspaces should empty the query, got %q", m.eventsQuery)
	}

	// Same contract in the picker's type-to-filter.
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	p := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	p.types = []model.ResourceType{pods}
	p.width, p.height = 100, 24
	p.layout()
	pi, _ := p.openPicker(pickType)
	p = asModel(t, pi)
	pi, _ = p.handlePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'é'}})
	p = asModel(t, pi)
	pi, _ = p.handlePickerKey(tea.KeyMsg{Type: tea.KeyBackspace})
	p = asModel(t, pi)
	if p.pickerQuery != "" {
		t.Fatalf("picker backspace should delete the whole rune, got %q (bytes: %x)", p.pickerQuery, p.pickerQuery)
	}
}

// TestListFilterEscCancels: on the main list, Esc while typing a filter must
// CANCEL it (clear-then-back) like every other filter mode — it used to
// commit the filter exactly like Enter (invariant 0 violation).
func TestListFilterEscCancels(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.types = []model.ResourceType{pods}
	m.width, m.height = 100, 24
	m.layout()

	// '/' enters filtering, type a query, Esc cancels it entirely.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.filtering {
		t.Fatal("'/' should enter list filtering mode")
	}
	m = typeRunes(t, m, "web")
	if m.filter.Value() != "web" {
		t.Fatalf("filter value=%q want web", m.filter.Value())
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.filtering || m.filter.Value() != "" {
		t.Fatalf("Esc must cancel and clear the filter, got filtering=%v value=%q", m.filtering, m.filter.Value())
	}

	// Enter still commits (kept as a chip).
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = typeRunes(t, m, "api")
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.filtering || m.filter.Value() != "api" {
		t.Fatalf("Enter must commit the filter, got filtering=%v value=%q", m.filtering, m.filter.Value())
	}
}

// TestTimelineAxisAlignedWithLanes: the timeline rule counted the "── " lead
// in BYTES (7) instead of cells (3), shoving the axis and its time labels 4
// columns left of the lane markers.
func TestTimelineAxisAlignedWithLanes(t *testing.T) {
	m := eventsModel(t)
	var ruleLine string
	for _, l := range strings.Split(m.events.View(), "\n") {
		if strings.Contains(l, "Timeline") {
			ruleLine = xansi.Strip(l)
			break
		}
	}
	if ruleLine == "" {
		t.Fatal("timeline rule line not found")
	}
	// Axis geometry: lead(3) + "Timeline"(8) + space(1) + gap dashes fill up
	// to timelineLaneWidth+2, where the axis starts with a spaced time label
	// → the first digit sits at rune index timelineLaneWidth+3.
	firstDigit := -1
	for i, r := range []rune(ruleLine) { // rune index — '─' is 3 bytes for 1 cell
		if r >= '0' && r <= '9' {
			firstDigit = i
			break
		}
	}
	if want := timelineLaneWidth + 3; firstDigit != want {
		t.Fatalf("first time label digit at rune %d, want %d (axis misaligned)\nrule: %q", firstDigit, want, ruleLine)
	}
}

// TestSecretRevealUsesKeymapBinding: the secret-reveal toggle must be a real
// keys.KeyMap binding (discoverable, per-screen scoped) — it was a hardcoded
// "x" invisible to the help system.
func TestSecretRevealUsesKeymapBinding(t *testing.T) {
	secret := model.ResourceType{Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(secret))
	m.width, m.height = 100, 24
	m.layout()
	m.screen = screenDetail

	// The binding toggles reveal on a Secret detail.
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !m.revealSecret {
		t.Fatal("'x' should reveal secret values on a Secret detail")
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.revealSecret {
		t.Fatal("'x' again should mask secret values")
	}

	// And it is discoverable: the Secret-detail keymap advertises it.
	km := m.screenKeymap()
	found := false
	for _, b := range km.ShortHelp() {
		if b.Help().Key == "x" {
			found = true
		}
	}
	if !found {
		t.Fatal("reveal binding missing from the Secret detail footer help")
	}

	// A non-Secret detail neither toggles nor advertises it.
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	m.curType = pods
	m.revealSecret = false
	m = send(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if m.revealSecret {
		t.Fatal("'x' must not toggle reveal outside Secret details")
	}
	for _, b := range m.screenKeymap().ShortHelp() {
		if b.Help().Key == "x" {
			t.Fatal("reveal binding must not be advertised on non-Secret details")
		}
	}
}

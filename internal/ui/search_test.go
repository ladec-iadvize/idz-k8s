package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func detailModel(t *testing.T) Model {
	t.Helper()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}))
	m.width, m.height = 100, 24
	m.layout()
	m.screen = screenDetail
	m.setDetailContent("kind: Deployment\nname: back\nimage: nginx:1.27\nreplicas: 3\nimage: redis:7")
	return m
}

// TestViewportSearchHighlightsAndNavigates: '/' searches, matches are
// highlighted, n/N cycle with wrap-around, Esc clears then goes back.
func TestViewportSearchHighlightsAndNavigates(t *testing.T) {
	m := detailModel(t)

	// '/' opens the prompt; typing is captured (no 'q' quit, no 'm' toggle).
	mi, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	if !m.searchTyping {
		t.Fatal("'/' must open the search prompt on the detail view")
	}
	for _, r := range "image" {
		mi, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if cmd != nil {
			t.Fatal("typing in the search prompt triggered a command")
		}
		m = asModel(t, mi)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)

	if len(m.searchHits) != 2 || m.searchIdx != 0 {
		t.Fatalf("hits=%v idx=%d, want 2 hits starting at 0", m.searchHits, m.searchIdx)
	}
	if !strings.Contains(m.statusMsg, "match 1/2") {
		t.Fatalf("status must show the position, got %q", m.statusMsg)
	}

	// n / N navigate with wrap-around.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = asModel(t, mi)
	if m.searchIdx != 1 {
		t.Fatalf("n: idx=%d want 1", m.searchIdx)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = asModel(t, mi)
	if m.searchIdx != 0 {
		t.Fatalf("n wrap: idx=%d want 0", m.searchIdx)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")})
	m = asModel(t, mi)
	if m.searchIdx != 1 {
		t.Fatalf("N wrap: idx=%d want 1", m.searchIdx)
	}

	// First Esc clears the search and stays; second Esc leaves the screen.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.searchQuery != "" || m.screen != screenDetail {
		t.Fatalf("first Esc must only clear the search (query=%q screen=%d)", m.searchQuery, m.screen)
	}
	if len(m.searchHits) != 0 {
		t.Fatal("clearing must drop the recorded hits")
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.screen != screenList {
		t.Fatalf("second Esc must go back, screen=%d", m.screen)
	}
}

// TestSearchSurvivesContentRefresh: content arriving while a search is active
// (describe events landing) keeps the highlight.
func TestSearchSurvivesContentRefresh(t *testing.T) {
	m := detailModel(t)
	m.searchQuery = "image"
	m.searchScreen = screenDetail
	m.applySearch(true)
	if len(m.searchHits) != 2 {
		t.Fatalf("hits=%v", m.searchHits)
	}
	m.setDetailContent("image: nginx:1.27\nother: line")
	if len(m.searchHits) != 1 {
		t.Fatalf("refresh must recompute the hits, got %v", m.searchHits)
	}
}

// TestHighlightMatchesIsCaseInsensitiveAndAnsiSafe: styled lines match on
// their plain text, every occurrence is wrapped, and no-match content is
// returned untouched.
func TestHighlightMatchesIsCaseInsensitiveAndAnsiSafe(t *testing.T) {
	mark := func(parts ...string) string { return "«" + strings.Join(parts, "") + "»" }
	styled := "\x1b[38;5;75mImage\x1b[0m: nginx"
	out, hits := highlightMatches(styled+"\nplain\nimage: redis IMAGE", "image", mark)
	if len(hits) != 2 || hits[0] != 0 || hits[1] != 2 {
		t.Fatalf("hits=%v", hits)
	}
	if !strings.Contains(out, "«Image»") {
		t.Fatalf("styled line must be matched on its plain text:\n%s", out)
	}
	if strings.Count(out, "«") != 3 { // Image, image, IMAGE
		t.Fatalf("every occurrence must be wrapped:\n%s", out)
	}
	same, none := highlightMatches("abc\ndef", "zzz", mark)
	if same != "abc\ndef" || none != nil {
		t.Fatalf("no-match must return content untouched, got %q %v", same, none)
	}
}

package ui

import (
	"strings"
	"testing"
	"time"

	xansi "github.com/charmbracelet/x/ansi"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestWrapTo(t *testing.T) {
	// Word-aware wrap, no line above the width.
	lines := wrapTo("0/76 nodes are available: 16 Insufficient memory, 46 node(s) didn't match Pod's node affinity", 30)
	if len(lines) < 3 {
		t.Fatalf("expected several lines, got %v", lines)
	}
	for _, l := range lines {
		if len([]rune(l)) > 30 {
			t.Fatalf("line exceeds width: %q", l)
		}
	}
	if strings.Join(lines, " ") != "0/76 nodes are available: 16 Insufficient memory, 46 node(s) didn't match Pod's node affinity" {
		t.Fatalf("content lost in wrap: %v", lines)
	}
	// Unbreakable token (long image URL) is cut, never overflowing.
	for _, l := range wrapTo("824262939987.dkr.ecr.eu-central-1.amazonaws.com/idz-docker/service:2.25.2", 20) {
		if len([]rune(l)) > 20 {
			t.Fatalf("unbreakable token overflow: %q", l)
		}
	}
	// Rune safety (multi-byte glyphs).
	for _, l := range wrapTo(strings.Repeat("é—✓ ", 30), 15) {
		if len([]rune(l)) > 15 {
			t.Fatalf("rune overflow: %q", l)
		}
	}
}

// TestDescribeShowsFullEventAndConditionMessages (owner bug 2026-07-07):
// long messages wrap instead of ending in an ellipsis.
func TestDescribeShowsFullEventAndConditionMessages(t *testing.T) {
	longMsg := "0/74 nodes are available: 16 Insufficient memory, 4 node(s) had untolerated taints, 46 node(s) didn't match the Pod's node affinity selector for this workload"
	obj := model.ResourceObject{
		Type: model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true},
		Name: "web-1", Namespace: "demo", CreatedAt: time.Now(),
		Raw: map[string]interface{}{
			"status": map[string]interface{}{
				"conditions": []interface{}{map[string]interface{}{
					"type": "Ready", "status": "False", "reason": "ContainersNotReady",
					"message": longMsg,
				}},
			},
		},
	}
	events := []model.Event{{Reason: "FailedScheduling", Type: "Warning", Message: longMsg, Time: time.Now()}}
	th := New(&kube.Client{}, config.Defaults(), "").theme
	content := describeContent(obj, events, th, 100)
	if strings.Contains(content, "…") {
		t.Fatalf("describe still truncates:\n%s", content)
	}
	// The tail of the long message must be present (twice: condition + event).
	if strings.Count(content, "affinity selector for this workload") != 2 {
		t.Fatalf("full messages missing:\n%s", content)
	}
	for _, l := range strings.Split(content, "\n") {
		if len([]rune(l)) > 100 {
			t.Fatalf("line exceeds the terminal width (would auto-wrap): %q", l)
		}
	}
}

// TestTimelineShowsSelectedEventFullMessage: the 'v' view renders the
// selected event's complete message in a dedicated block, while the list
// rows stay one-line (click geometry).
func TestTimelineShowsSelectedEventFullMessage(t *testing.T) {
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}))
	m.width, m.height = 100, 34
	m.layout()
	m.screen = screenEvents
	longMsg := "Back-off pulling image \"824262939987.dkr.ecr.eu-central-1.amazonaws.com/idz-docker/employment-livefeed-service:2.25.2\" because of repeated failures"
	m.eventRows = []model.Event{
		{Reason: "BackOff", Type: "Warning", Message: longMsg, ObjKind: "Pod", ObjName: "web-1", Time: time.Now()},
		{Reason: "Scheduled", Type: "Normal", Message: "ok", ObjKind: "Pod", ObjName: "web-1", Time: time.Now().Add(-time.Minute)},
	}
	m.recentSel = 0
	m.renderEvents()
	content := m.events.View()
	if !strings.Contains(content, "selected event — full message") {
		t.Fatalf("missing full-message block:\n%s", content)
	}
	if !strings.Contains(content, "because of repeated failures") {
		t.Fatalf("selected message still truncated:\n%s", content)
	}
	for _, l := range strings.Split(content, "\n") {
		if len([]rune(xansi.Strip(l))) > 100 {
			t.Fatalf("line exceeds width: %q", l)
		}
	}
}

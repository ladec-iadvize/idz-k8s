package ui

// v3 admin UI contract: the 'a' palette offers only the actions the selected
// kind supports, and NOTHING mutates without the confirmation modal (or a
// value prompt) — Esc always cancels, typing-mode keys never leak.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

var deploymentsType = model.ResourceType{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true}

func fakeDeployment(ns, name string, replicas int64) map[string]any {
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"replicas": replicas,
			"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
		},
	}
}

// adminModel builds a list model on a Deployment backed by a fake dynamic
// client, with one row selected.
func adminModel(t *testing.T) Model {
	t.Helper()
	raw := fakeDeployment("demo", "back", 3)
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList",
		}, &unstructured.Unstructured{Object: raw})
	m := New(&kube.Client{Namespace: "demo", Dynamic: dyn}, config.Defaults(), "",
		WithInitialType(deploymentsType))
	m.width, m.height = 120, 30
	m.layout()
	m.objects = []model.ResourceObject{{
		Type: deploymentsType, Namespace: "demo", Name: "back", Raw: raw,
	}}
	m.applyRows()
	return m
}

func pressRune(t *testing.T, m Model, r rune) Model {
	t.Helper()
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	return asModel(t, mi)
}

func pickerOptions(m Model) string {
	var b strings.Builder
	for _, row := range m.pickerWin.rows {
		b.WriteString(row[0])
		b.WriteString("\n")
	}
	return b.String()
}

func TestActionsPaletteMatchesKind(t *testing.T) {
	m := adminModel(t)
	m = pressRune(t, m, 'a')
	if m.screen != screenPicker || m.pickerKind != pickAction {
		t.Fatalf("'a' must open the actions palette (screen=%v kind=%v)", m.screen, m.pickerKind)
	}
	opts := pickerOptions(m)
	for _, want := range []string{"scale", "restart", "port-forward", "edit", "delete"} {
		if !strings.Contains(opts, want) {
			t.Fatalf("deployment actions missing %q:\n%s", want, opts)
		}
	}
	for _, forbidden := range []string{"cordon", "suspend"} {
		if strings.Contains(opts, forbidden) {
			t.Fatalf("deployment actions must not offer %q:\n%s", forbidden, opts)
		}
	}

	// Node selection → cordon, no scale.
	m = adminModel(t)
	m.curType = model.ResourceType{Version: "v1", Kind: "Node", Resource: "nodes"}
	m.objects = []model.ResourceObject{{Type: m.curType, Name: "ip-10-0-1-2",
		Raw: map[string]any{"metadata": map[string]any{"name": "ip-10-0-1-2"}}}}
	m.applyRows()
	m = pressRune(t, m, 'a')
	opts = pickerOptions(m)
	if !strings.Contains(opts, "cordon") || strings.Contains(opts, "scale") {
		t.Fatalf("node actions wrong:\n%s", opts)
	}
}

// selectAction opens the palette and selects the entry with the given id.
func selectAction(t *testing.T, m Model, id string) Model {
	t.Helper()
	m = pressRune(t, m, 'a')
	for i, row := range m.pickerWin.rows {
		if strings.HasPrefix(row[0], id+" ") || strings.HasPrefix(row[0], id+"	") || strings.HasPrefix(strings.TrimSpace(row[0]), id+" ") {
			m.pickerWin.cursor = i
			mi, _ := m.pickerSelect()
			return asModel(t, mi)
		}
	}
	t.Fatalf("action %q not in palette:\n%s", id, pickerOptions(m))
	return m
}

func TestDeleteGoesThroughConfirmation(t *testing.T) {
	m := adminModel(t)
	m = selectAction(t, m, "delete")
	if !m.confirming || !strings.Contains(m.confirmTitle, "DELETE Deployment/back") {
		t.Fatalf("delete must arm the confirmation modal (confirming=%v title=%q)", m.confirming, m.confirmTitle)
	}
	if !strings.Contains(m.View(), "destructive action") {
		t.Fatal("the confirmation modal must be visible and flag the destruction")
	}

	// 'q' while confirming must neither quit nor mutate (typing-mode rule).
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = asModel(t, mi)
	if cmd != nil || !m.confirming {
		t.Fatal("keys other than Enter/Esc must be swallowed by the modal")
	}

	// Esc cancels: no command, modal gone.
	mi, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if cmd != nil || m.confirming {
		t.Fatal("Esc must cancel without running anything")
	}

	// Enter runs the armed command; the fake records the delete.
	m = selectAction(t, m, "delete")
	mi, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.confirming || cmd == nil {
		t.Fatal("Enter must disarm the modal and run the mutation")
	}
	msg, ok := cmd().(adminMsg)
	if !ok || msg.err != nil {
		t.Fatalf("mutation result: %+v", msg)
	}
	objs, err := m.client.List(t.Context(), deploymentsType, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 0 {
		t.Fatalf("deployment still there after confirmed delete: %+v", objs)
	}
}

func TestScalePromptAppliesReplicas(t *testing.T) {
	m := adminModel(t)
	m = selectAction(t, m, "scale")
	if m.promptKind != promptScale {
		t.Fatalf("scale must open the replicas prompt (kind=%v)", m.promptKind)
	}
	if m.promptInput != "3" {
		t.Fatalf("prompt must be pre-filled with the current replicas, got %q", m.promptInput)
	}
	// Backspace the prefill, type 5, Enter.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = asModel(t, mi)
	m = pressRune(t, m, '5')
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.promptKind != promptNone || cmd == nil {
		t.Fatal("Enter must commit the prompt and run the scale")
	}
	if msg, ok := cmd().(adminMsg); !ok || msg.err != nil {
		t.Fatalf("scale result: %+v", msg)
	}
	obj, err := m.client.GetObject(t.Context(), deploymentsType, "demo", "back")
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _ := unstructured.NestedInt64(obj.Raw, "spec", "replicas"); r != 5 {
		t.Fatalf("replicas=%d, want 5", r)
	}
}

func TestScalePromptRejectsGarbage(t *testing.T) {
	m := adminModel(t)
	m = selectAction(t, m, "scale")
	for m.promptInput != "" { // clear the prefill
		mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = asModel(t, mi)
	}
	m = pressRune(t, m, 'x')
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if cmd != nil || !strings.Contains(m.errMsg, "replicas") {
		t.Fatalf("garbage replicas must error without mutating (err=%q)", m.errMsg)
	}
}

func TestParseForwardPorts(t *testing.T) {
	cases := []struct {
		in            string
		local, remote int
		wantErr       bool
	}{
		{"8080:80", 8080, 80, false},
		{"80", 80, 80, false},
		{"0:80", 0, 80, false}, // 0 = OS-assigned local port
		{"", 0, 0, true},
		{"x:80", 0, 0, true},
		{"80:", 0, 0, true},
	}
	for _, c := range cases {
		l, r, err := parseForwardPorts(c.in)
		if (err != nil) != c.wantErr || l != c.local || r != c.remote {
			t.Fatalf("parseForwardPorts(%q) = %d,%d,%v", c.in, l, r, err)
		}
	}
}

func TestSuggestForwardPorts(t *testing.T) {
	svc := map[string]any{"spec": map[string]any{"ports": []any{map[string]any{"port": int64(443)}}}}
	if got := suggestForwardPorts("Service", svc); got != "443:443" {
		t.Fatalf("service suggestion=%q", got)
	}
	pod := map[string]any{"spec": map[string]any{"containers": []any{
		map[string]any{"ports": []any{map[string]any{"containerPort": int64(8080)}}},
	}}}
	if got := suggestForwardPorts("Pod", pod); got != "8080:8080" {
		t.Fatalf("pod suggestion=%q", got)
	}
	if got := suggestForwardPorts("Pod", map[string]any{}); got != "" {
		t.Fatalf("portless suggestion=%q", got)
	}
}

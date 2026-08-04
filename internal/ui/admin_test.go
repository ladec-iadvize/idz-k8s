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

// TestBulkDeleteOnMarkedPods (owner request 2026-07-30): mark several pods
// with Space, open the actions palette — the bulk delete targets ALL of
// them, still behind the confirmation modal, and consumes the marks.
func TestBulkDeleteOnMarkedPods(t *testing.T) {
	podsType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	mkPod := func(name string) map[string]any {
		return map[string]any{
			"apiVersion": "v1", "kind": "Pod",
			"metadata": map[string]any{"name": name, "namespace": "demo"},
		}
	}
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			{Version: "v1", Resource: "pods"}: "PodList",
		},
		&unstructured.Unstructured{Object: mkPod("web-1")},
		&unstructured.Unstructured{Object: mkPod("web-2")},
		&unstructured.Unstructured{Object: mkPod("web-3")})
	m := New(&kube.Client{Namespace: "demo", Dynamic: dyn}, config.Defaults(), "",
		WithInitialType(podsType))
	m.width, m.height = 120, 30
	m.layout()
	for _, n := range []string{"web-1", "web-2", "web-3"} {
		m.objects = append(m.objects, model.ResourceObject{
			Type: podsType, Namespace: "demo", Name: n, Raw: mkPod(n)})
	}
	m.applyRows()

	// Mark web-1 and web-2 (cursor row then next row).
	m = pressRune(t, m, ' ')
	m.win.Move(1)
	m = pressRune(t, m, ' ')
	if len(m.marked) != 2 {
		t.Fatalf("expected 2 marked pods, got %d", len(m.marked))
	}

	m = selectAction(t, m, "delete-marked")
	if !m.confirming || !strings.Contains(m.confirmTitle, "2 marked Pod(s)") ||
		!strings.Contains(m.confirmTitle, "web-1") || !strings.Contains(m.confirmTitle, "web-2") {
		t.Fatalf("bulk delete must confirm with count and names, got %q", m.confirmTitle)
	}
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if cmd == nil {
		t.Fatal("Enter must run the bulk mutation")
	}
	msg, ok := cmd().(adminMsg)
	if !ok || msg.err != nil || !msg.clearMarks {
		t.Fatalf("bulk result: %+v", msg)
	}
	mi, _ = m.Update(msg)
	m = asModel(t, mi)
	if len(m.marked) != 0 {
		t.Fatal("a successful bulk action must consume the marks")
	}
	objs, err := m.client.List(t.Context(), podsType, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Name != "web-3" {
		t.Fatalf("both marked pods must be deleted, remaining: %+v", objs)
	}
}

// TestBulkActionsMatchKind: marked deployments offer restart-marked, marked
// pods do not; the single-selection actions stay available below.
func TestBulkActionsMatchKind(t *testing.T) {
	m := adminModel(t)
	m = pressRune(t, m, ' ') // mark the deployment under the cursor
	m = pressRune(t, m, 'a')
	opts := pickerOptions(m)
	for _, want := range []string{"restart-marked", "delete-marked", "scale", "edit"} {
		if !strings.Contains(opts, want) {
			t.Fatalf("marked deployment palette missing %q:\n%s", want, opts)
		}
	}
	if strings.Contains(opts, "cordon-marked") || strings.Contains(opts, "suspend-marked") {
		t.Fatalf("deployment palette must not offer node/cronjob bulk actions:\n%s", opts)
	}
}

// TestShellAndTriggerInPalette: pods offer "shell"; CronJobs offer "trigger"
// behind the confirmation modal.
func TestShellAndTriggerInPalette(t *testing.T) {
	m := adminModel(t) // deployment: shell offered (resolves a pod), no trigger
	m = pressRune(t, m, 'a')
	opts := pickerOptions(m)
	if !strings.Contains(opts, "shell") || strings.Contains(opts, "trigger") {
		t.Fatalf("deployment palette wrong:\n%s", opts)
	}

	m = adminModel(t)
	m.curType = model.ResourceType{Group: "batch", Version: "v1", Kind: "CronJob", Resource: "cronjobs", Namespaced: true}
	m.objects = []model.ResourceObject{{Type: m.curType, Namespace: "demo", Name: "nightly",
		Raw: map[string]any{"metadata": map[string]any{"name": "nightly", "namespace": "demo"},
			"spec": map[string]any{"schedule": "0 * * * *"}}}}
	m.applyRows()
	m = selectAction(t, m, "trigger")
	if !m.confirming || !strings.Contains(m.confirmTitle, "trigger CronJob/nightly") {
		t.Fatalf("trigger must arm the confirmation, got %q", m.confirmTitle)
	}
}

// cronDrilledModel returns a model showing the (empty) Jobs level drilled
// from a CronJob — the exact state the owner reported on 2026-08-03.
func cronDrilledModel(t *testing.T, jobs ...model.ResourceObject) Model {
	t.Helper()
	cronType := model.ResourceType{Group: "batch", Version: "v1", Kind: "CronJob", Resource: "cronjobs", Namespaced: true}
	jobType := model.ResourceType{Group: "batch", Version: "v1", Kind: "Job", Resource: "jobs", Namespaced: true}
	cronRaw := map[string]any{
		"apiVersion": "batch/v1", "kind": "CronJob",
		"metadata": map[string]any{"name": "nightly", "namespace": "demo", "uid": "cron-uid-1"},
		"spec": map[string]any{"schedule": "0 * * * *",
			"jobTemplate": map[string]any{"spec": map[string]any{}}},
	}
	scheme := runtime.NewScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{
			{Group: "batch", Version: "v1", Resource: "cronjobs"}: "CronJobList",
			{Group: "batch", Version: "v1", Resource: "jobs"}:     "JobList",
		}, &unstructured.Unstructured{Object: cronRaw})
	m := New(&kube.Client{Namespace: "demo", Dynamic: dyn}, config.Defaults(), "",
		WithInitialType(cronType))
	m.types = []model.ResourceType{cronType, jobType}
	m.width, m.height = 140, 30
	m.layout()
	m.objects = []model.ResourceObject{{Type: cronType, Namespace: "demo", Name: "nightly", Raw: cronRaw}}
	m.applyRows()
	// Drill into the Jobs level.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	m.objects = jobs
	m.applyRows()
	return m
}

// TestParentActionsInDrilledList (owner report 2026-08-03: 'a' in the
// CronJob → Jobs view triggered nothing): the palette offers the PARENT's
// actions there, even when the child list is empty, and triggering works.
func TestParentActionsInDrilledList(t *testing.T) {
	m := cronDrilledModel(t) // empty Jobs level
	if len(m.rowObjs) != 0 || m.drillParent.Name != "nightly" {
		t.Fatalf("precondition: empty jobs level under the cronjob, parent=%q", m.drillParent.Name)
	}
	m = pressRune(t, m, 'a')
	opts := pickerOptions(m)
	for _, want := range []string{"trigger-parent", "suspend-parent"} {
		if !strings.Contains(opts, want) {
			t.Fatalf("an empty drilled list must offer the parent's actions, missing %q:\n%s", want, opts)
		}
	}

	// Selecting trigger-parent confirms, then creates the Job.
	m = selectAction(t, m, "trigger-parent")
	if !m.confirming || !strings.Contains(m.confirmTitle, "CronJob/nightly") {
		t.Fatalf("trigger-parent must confirm on the parent, got %q", m.confirmTitle)
	}
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if cmd == nil {
		t.Fatal("Enter must run the trigger")
	}
	msg, ok := cmd().(adminMsg)
	if !ok || msg.err != nil {
		t.Fatalf("trigger result: %+v", msg)
	}
	if !strings.Contains(msg.summary, "Job/nightly-manual-") {
		t.Fatalf("summary must name the created job, got %q", msg.summary)
	}

	// The created Job carries the owner reference, so the SAME drilled level
	// lists it — that was the reported "delay" (it never appeared at all).
	jobType := model.ResourceType{Group: "batch", Version: "v1", Kind: "Job", Resource: "jobs", Namespaced: true}
	created, err := m.client.List(t.Context(), jobType, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 1 {
		t.Fatalf("expected the triggered job, got %+v", created)
	}
	if !kube.OwnedByUID(created[0].Raw, "cron-uid-1") {
		t.Fatalf("the triggered job must be owned by the CronJob: %+v", created[0].Raw["metadata"])
	}

	// With a child selected, the parent's actions come AFTER the child's own.
	m2 := cronDrilledModel(t, model.ResourceObject{Type: jobType, Namespace: "demo", Name: "nightly-123",
		Raw: map[string]any{"metadata": map[string]any{"name": "nightly-123", "namespace": "demo"}}})
	m2 = pressRune(t, m2, 'a')
	opts2 := pickerOptions(m2)
	if !strings.Contains(opts2, "trigger-parent") || !strings.Contains(opts2, "delete") {
		t.Fatalf("both the child's and the parent's actions must show:\n%s", opts2)
	}
	if strings.Index(opts2, "delete") > strings.Index(opts2, "trigger-parent") {
		t.Fatalf("the selection's own actions must come first:\n%s", opts2)
	}
}

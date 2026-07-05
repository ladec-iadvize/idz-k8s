package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func deploymentModel(t *testing.T) Model {
	t.Helper()
	dep := model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m := New(&kube.Client{Namespace: ""}, config.Defaults(), "", WithInitialType(dep))
	m.types = []model.ResourceType{
		dep,
		{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
	}
	m.objects = []model.ResourceObject{{
		Type: dep, Namespace: "audience-back", Name: "back",
		Status: model.StatusSummary{Level: model.HealthOk},
		Raw: map[string]interface{}{
			"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]interface{}{"name": "back", "namespace": "audience-back"},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{
					"matchLabels": map[string]interface{}{"app": "back"},
				},
			},
		},
	}}
	m.width, m.height = 120, 30
	m.layout()
	m.applyRows()
	return m
}

func TestEnterOnDeploymentDrillsToPods(t *testing.T) {
	m := deploymentModel(t)
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.curType.Resource != "pods" {
		t.Fatalf("Enter on a Deployment should switch to pods, got %q", m.curType.Key())
	}
	if m.drillSelector != "app=back" {
		t.Fatalf("drill selector should be app=back, got %q", m.drillSelector)
	}
	if m.drillFor != "Deployment/back" {
		t.Fatalf("drillFor=%q", m.drillFor)
	}
	if m.client.Namespace != "" {
		t.Fatalf("drill must NOT change the user's namespace filter, got %q", m.client.Namespace)
	}
	if m.drillNamespace != "audience-back" {
		t.Fatalf("drill should scope the query to the workload namespace internally, got %q", m.drillNamespace)
	}
	if cmd == nil {
		t.Fatal("drill should trigger a list reload")
	}
	if !strings.Contains(m.header(), "pods ⊂ Deployment/back") {
		t.Errorf("header should show the drill context, got %q", m.header())
	}
	// Esc restores the deployments list.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.curType.Resource != "deployments" || m.drillSelector != "" {
		t.Fatalf("Esc should exit the drill, got type=%q selector=%q", m.curType.Key(), m.drillSelector)
	}
}

func TestYamlKeyStillShowsDetail(t *testing.T) {
	m := deploymentModel(t)
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = asModel(t, mi)
	if m.screen != screenDetail {
		t.Fatalf("'y' should open the YAML detail, screen=%d", m.screen)
	}
	if !strings.Contains(m.detail.View(), "kind: Deployment") {
		t.Error("YAML view should render the object")
	}
}

func TestDescribeShowsSummary(t *testing.T) {
	m := deploymentModel(t)
	m.objects[0].CreatedAt = time.Now().Add(-2 * time.Hour)
	m.applyRows()
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	m = asModel(t, mi)
	if m.screen != screenDetail {
		t.Fatalf("'d' should open describe, screen=%d", m.screen)
	}
	view := m.detail.View()
	if !strings.Contains(view, "Describe: Deployment/back") {
		t.Errorf("describe header missing: %q", view)
	}
	if cmd == nil {
		t.Fatal("describe should fetch the object's events")
	}
}

func asModel(t *testing.T, mi tea.Model) Model {
	t.Helper()
	switch v := mi.(type) {
	case Model:
		return v
	case *Model:
		return *v
	default:
		t.Fatalf("unexpected model type %T", mi)
		return Model{}
	}
}

func TestEventsFromDrillScopedToItsPods(t *testing.T) {
	m := deploymentModel(t)
	// Drill into Deployment/back.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	// The drilled list now shows one pod.
	podType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	m.objects = []model.ResourceObject{{Type: podType, Namespace: "audience-back", Name: "back-abc12"}}
	m.applyRows()
	// Open the timeline from the drilled view.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = asModel(t, mi)
	if m.eventsScope == nil || !m.eventsScope["audience-back/back-abc12"] {
		t.Fatalf("timeline should be scoped to the drilled pods, got %v", m.eventsScope)
	}
	if m.eventsScopeFor != "Deployment/back" {
		t.Fatalf("scope label=%q", m.eventsScopeFor)
	}
	// Events from other pods are filtered out; own pod stays.
	now := time.Now()
	m.eventRows = []model.Event{
		{Time: now, Type: "Warning", Reason: "BackOff", ObjKind: "Pod", ObjName: "back-abc12", Namespace: "audience-back"},
		{Time: now, Type: "Warning", Reason: "BackOff", ObjKind: "Pod", ObjName: "other-pod", Namespace: "conversations"},
	}
	m.renderEvents()
	view := m.events.View()
	if !strings.Contains(view, "back-abc12") {
		t.Error("own pod's events must be shown")
	}
	if strings.Contains(view, "other-pod") {
		t.Error("other pods' events must be hidden when scoped")
	}
	if !strings.Contains(view, "scope:[Deployment/back]") {
		t.Error("header should show the scope")
	}
}

func TestOwnerKeyWalksUpTheChain(t *testing.T) {
	podType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	rsType := model.ResourceType{Group: "apps", Version: "v1", Kind: "ReplicaSet", Resource: "replicasets", Namespaced: true}
	m := New(&kube.Client{Namespace: ""}, config.Defaults(), "", WithInitialType(podType))
	m.types = []model.ResourceType{podType, rsType}
	m.objects = []model.ResourceObject{{
		Type: podType, Namespace: "demo", Name: "back-abc12",
		Raw: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name": "back-abc12",
				"ownerReferences": []interface{}{
					map[string]interface{}{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "back-7f9c4"},
				},
			},
		},
	}}
	m.width, m.height = 120, 30
	m.layout()
	m.applyRows()

	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = asModel(t, mi)
	if m.curType.Kind != "ReplicaSet" {
		t.Fatalf("'o' should switch to the owner type, got %q", m.curType.Key())
	}
	if m.filter.Value() != "back-7f9c4" {
		t.Fatalf("filter should target the owner name, got %q", m.filter.Value())
	}
	if cmd == nil {
		t.Fatal("owner navigation should reload the list")
	}
}

func TestOwnerKeyNoOwner(t *testing.T) {
	m := deploymentModel(t) // deployment fixture has no ownerReferences
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	m = asModel(t, mi)
	if cmd != nil || m.curType.Kind != "Deployment" {
		t.Fatalf("no owner: should stay put, type=%q", m.curType.Key())
	}
	if !strings.Contains(m.statusMsg, "no owner") {
		t.Errorf("statusMsg should explain, got %q", m.statusMsg)
	}
}

func TestTypeSwitchClearsDrillAndFilter(t *testing.T) {
	m := deploymentModel(t)
	// Drill into the deployment, then walk to owner to set a text filter too.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	m.filter.SetValue("leftover")
	// Jump to another type via the ':' picker.
	svc := model.ResourceType{Version: "v1", Kind: "Service", Resource: "services", Namespaced: true}
	m.types = append(m.types, svc)
	mi, _ = m.openPicker(pickType)
	m = asModel(t, mi)
	for _, r := range "services" {
		mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	mi, _ = m.handlePickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)

	if m.curType.Resource != "services" {
		t.Fatalf("type should switch to services, got %q", m.curType.Key())
	}
	if m.drillSelector != "" || m.drillNamespace != "" {
		t.Fatalf("drill must be cleared on type switch, got selector=%q ns=%q", m.drillSelector, m.drillNamespace)
	}
	if m.filter.Value() != "" {
		t.Fatalf("text filter must be cleared on type switch, got %q", m.filter.Value())
	}
}

func TestMouseToggle(t *testing.T) {
	m := deploymentModel(t)
	m.mouseOn = true
	mi, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = asModel(t, mi)
	if m.mouseOn {
		t.Fatal("'m' should disable mouse capture")
	}
	if cmd == nil {
		t.Fatal("toggle must emit a tea command to change mouse mode")
	}
	if !strings.Contains(m.statusMsg, "copy") {
		t.Errorf("status should explain copy mode, got %q", m.statusMsg)
	}
	mi, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	m = asModel(t, mi)
	if !m.mouseOn || cmd == nil {
		t.Fatal("'m' again should re-enable mouse capture")
	}
}

func TestPickerTypingSwallowsGlobalKeys(t *testing.T) {
	m := deploymentModel(t)
	mi, _ := m.openPicker(pickType)
	m = asModel(t, mi)
	m.mouseOn = true
	// Type "qm" — must go into the picker filter, not quit / toggle mouse.
	m = send2(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = send2(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if m.screen != screenPicker {
		t.Fatalf("typing must stay in the picker, screen=%d", m.screen)
	}
	if m.pickerQuery != "qm" {
		t.Fatalf("picker query=%q want qm", m.pickerQuery)
	}
	if !m.mouseOn {
		t.Fatal("typing 'm' in a picker must not toggle the mouse")
	}
}

func send2(t *testing.T, m Model, msg tea.KeyMsg) Model {
	t.Helper()
	mi, _ := m.Update(msg)
	return asModel(t, mi)
}

func TestLogBufferAccumulates(t *testing.T) {
	m := deploymentModel(t)
	m.screen = screenLogs
	mi, _ := m.Update(logLineMsg{Text: "line one"})
	m = asModel(t, mi)
	mi, _ = m.Update(logLineMsg{Text: "line two"})
	m = asModel(t, mi)
	view := m.logsView.View()
	if !strings.Contains(view, "line one") || !strings.Contains(view, "line two") {
		t.Fatalf("both log lines must be visible, got %q", view)
	}
	if len(m.logBuf) != 2 {
		t.Fatalf("buffer should hold 2 lines, got %d", len(m.logBuf))
	}
}

func TestDeploymentListShowsReadyColumn(t *testing.T) {
	m := deploymentModel(t)
	m.objects[0].Raw["spec"].(map[string]interface{})["replicas"] = int64(3)
	m.objects[0].Raw["status"] = map[string]interface{}{"readyReplicas": int64(1)}
	m.applyRows()
	row, _ := m.win.Selected()
	if len(row) < 5 {
		t.Fatalf("row too short: %v", row)
	}
	if row[3] != "1/3" {
		t.Fatalf("READY column = %q, want 1/3", row[3])
	}
	if !strings.Contains(row[4], "1/3 ready") {
		t.Fatalf("status should flag partial readiness, got %q", row[4])
	}
}

func TestMergedLogPrefixColoredAndStable(t *testing.T) {
	m := deploymentModel(t)
	m.screen = screenLogs
	mi, _ := m.Update(logLineMsg{Pod: "web-1", Text: "hello"})
	m = asModel(t, mi)
	if len(m.logBuf) != 1 || !strings.Contains(m.logBuf[0], "[web-1]") || !strings.Contains(m.logBuf[0], "hello") {
		t.Fatalf("merged line must carry the pod prefix, got %q", m.logBuf)
	}
	// Stable color: same pod always maps to the same style.
	if podPrefixStyle("web-1").Render("x") != podPrefixStyle("web-1").Render("x") {
		t.Fatal("pod prefix color must be deterministic")
	}
	// Single-pod stream (Pod == "") keeps raw lines.
	mi, _ = m.Update(logLineMsg{Text: "raw"})
	m = asModel(t, mi)
	if m.logBuf[1] != "raw" {
		t.Fatalf("single-pod lines must stay raw, got %q", m.logBuf[1])
	}
}

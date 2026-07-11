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

func plainModel(t *testing.T) Model {
	t.Helper()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}))
	m.width, m.height = 110, 30
	m.layout()
	return m
}

// TestDiagGroupedByFailureType (owner request 2026-07-07): findings grouped
// under one header per failure type, error groups first.
func TestDiagGroupedByFailureType(t *testing.T) {
	m := plainModel(t)
	m.screen = screenDiag
	m.renderDiag([]model.Diagnostic{
		{Namespace: "demo", Pod: "a", Container: "app", Reason: "restarted x4", Level: model.HealthWarning},
		{Namespace: "demo", Pod: "b", Container: "app", Reason: "OOMKilled (x3 restarts)", Level: model.HealthError},
		{Namespace: "demo", Pod: "c", Container: "app", Reason: "OOMKilled (x9 restarts)", Level: model.HealthError},
		{Namespace: "demo", Pod: "d", Reason: "Evicted: node memory pressure", Level: model.HealthError},
	})
	content := m.diag.View()
	for _, want := range []string{"OOMKilled (2)", "Evicted (1)", "restarted (1)", "4 finding(s)"} {
		if !strings.Contains(content, want) {
			t.Fatalf("diag view missing %q:\n%s", want, content)
		}
	}
	// Error groups come before warning groups.
	if strings.Index(content, "OOMKilled (2)") > strings.Index(content, "restarted (1)") {
		t.Fatal("error-level groups must render first")
	}
}

func TestDiagCategoryFolding(t *testing.T) {
	cases := map[string]string{
		"OOMKilled (x3 restarts)":       "OOMKilled",
		"Evicted: node memory pressure": "Evicted",
		"restarted x4":                  "restarted",
		"CrashLoopBackOff":              "CrashLoopBackOff",
		"Error (exit 1, x2)":            "Error",
	}
	for in, want := range cases {
		if got := diagCategory(in); got != want {
			t.Errorf("diagCategory(%q)=%q want %q", in, got, want)
		}
	}
}

// TestSearchWorksOnEveryContentView: the same '/' flow works on a non-detail
// view (posture here) — commit, hits, Esc clears then Esc leaves.
func TestSearchWorksOnEveryContentView(t *testing.T) {
	m := plainModel(t)
	m.screen = screenPosture
	m.renderPosture([]model.PostureFinding{
		{Rule: "privileged container", Severity: model.HealthError, Namespace: "demo", Name: "bad", Container: "app", Detail: "securityContext.privileged: true"},
	})
	mi, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	if !m.searchTyping {
		t.Fatal("'/' must open the search on the posture view")
	}
	for _, r := range "privileged" {
		mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if len(m.searchHits) == 0 || m.searchScreen != screenPosture {
		t.Fatalf("hits=%v screen=%d", m.searchHits, m.searchScreen)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.searchQuery != "" || m.screen != screenPosture {
		t.Fatal("first Esc must clear and stay")
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.screen != screenList {
		t.Fatal("second Esc must leave")
	}
}

// TestLogsSearchSurvivesStreaming: new log lines keep the highlight and
// recompute the hits.
func TestLogsSearchSurvivesStreaming(t *testing.T) {
	m := plainModel(t)
	m.screen = screenLogs
	m.logBuf = []string{"boot ok", "error: timeout"}
	m.setContent(screenLogs, strings.Join(m.logBuf, "\n"))
	m.searchQuery, m.searchScreen = "error", screenLogs
	m.applySearch(true)
	if len(m.searchHits) != 1 {
		t.Fatalf("hits=%v", m.searchHits)
	}
	m.logBuf = append(m.logBuf, "error: again")
	m.setContent(screenLogs, strings.Join(m.logBuf, "\n"))
	if len(m.searchHits) != 2 {
		t.Fatalf("streaming must recompute hits, got %v", m.searchHits)
	}
}

// TestSizingOverviewFilter: '/' on the sizing table narrows the rows and
// keeps rows/objs paired.
func TestSizingOverviewFilter(t *testing.T) {
	m := plainModel(t)
	m.curType = model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m.screen = screenSizingList
	mi, _ := m.Update(sizingListMsg{
		rows: []model.SizingAdvice{
			{Workload: "Deployment/back", Namespace: "demo"},
			{Workload: "Deployment/front", Namespace: "demo"},
		},
		objs: []model.ResourceObject{{Name: "back"}, {Name: "front"}},
	})
	m = asModel(t, mi)
	if len(m.sizingRows) != 2 {
		t.Fatalf("master rows=%d", len(m.sizingRows))
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	if !m.sizingFiltering {
		t.Fatal("'/' must open the sizing filter")
	}
	for _, r := range "front" {
		mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if len(m.sizingRows) != 1 || m.sizingObjs[0].Name != "front" {
		t.Fatalf("filter broken: rows=%+v objs=%+v", m.sizingRows, m.sizingObjs)
	}
	// Esc while typing clears back to the full set.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if len(m.sizingRows) != 2 {
		t.Fatalf("clear broken: rows=%d", len(m.sizingRows))
	}
}

// TestLiveRefreshThrottle: a burst of change signals coalesces into ONE
// throttled flush, and the flush refreshes only the list screen.
func TestLiveRefreshThrottle(t *testing.T) {
	m := plainModel(t)
	m.screen = screenList

	mi, cmd := m.Update(changeMsg{ok: true})
	m = asModel(t, mi)
	if !m.changePending || cmd == nil {
		t.Fatalf("first change must schedule a flush (pending=%v)", m.changePending)
	}
	// Burst: further signals only re-arm the wait, no second flush timer.
	mi, _ = m.Update(changeMsg{ok: true})
	m = asModel(t, mi)
	if !m.changePending {
		t.Fatal("burst must keep the single pending flush")
	}

	mi, cmd = m.Update(changeFlushMsg{})
	m = asModel(t, mi)
	if m.changePending || cmd == nil {
		t.Fatalf("flush must clear pending and refresh the list (cmd=%v)", cmd)
	}

	// Outside the list, a flush refreshes nothing.
	m.screen = screenPosture
	m.changePending = true
	mi, cmd = m.Update(changeFlushMsg{})
	m = asModel(t, mi)
	if cmd != nil {
		t.Fatal("flush must be a no-op outside the list screen")
	}
}

// TestNodeViewsAreContextual (owner decision 2026-07-09): 't' opens only from
// deployments/nodes, 'u' only from nodes; elsewhere a hint, no screen change.
func TestNodeViewsAreContextual(t *testing.T) {
	m := plainModel(t) // pods list
	// 'u' passes the gate from pods (rev. 2026-07-09) — without a metrics
	// client it lands on the explicit unavailable message, NOT the type hint.
	mi, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = asModel(t, mi)
	if !strings.Contains(m.errMsg, "metrics unavailable") {
		t.Fatalf("'u' on pods must pass the gate (errMsg=%q statusMsg=%q)", m.errMsg, m.statusMsg)
	}
	m.errMsg = ""
	// …but 't' still hints there.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = asModel(t, mi)
	if m.screen != screenList || m.statusMsg == "" {
		t.Fatalf("'t' on pods must hint (screen=%d)", m.screen)
	}

	// 'u' hints on nodes now (usage reads pods).
	m.curType = model.ResourceType{Version: "v1", Resource: "nodes", Kind: "Node", Namespaced: false}
	m.statusMsg = ""
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = asModel(t, mi)
	if m.screen != screenList || !strings.Contains(m.statusMsg, "pods") {
		t.Fatalf("'u' on nodes must hint (screen=%d msg=%q)", m.screen, m.statusMsg)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = asModel(t, mi)
	if m.screen != screenTopology {
		t.Fatalf("'t' on nodes must open topology, screen=%d", m.screen)
	}
}

// TestUsageTable: CPU and memory side by side (no toggle), sortable and
// filterable like every other table, CPU-descending by default, explicit
// missing-metric cells.
func TestUsageTable(t *testing.T) {
	m := plainModel(t)
	m.client.Namespace = "" // all-ns → names prefixed
	m.screen = screenTop
	mi, _ := m.Update(usageTableMsg{rows: []model.UsageRow{
		{Namespace: "demo", Name: "small", Pods: 1, CPU: 0.5, Mem: 4e8, HasCPU: true, HasMem: true},
		{Namespace: "demo", Name: "big", Pods: 1, CPU: 2.0, Mem: 1e9, HasCPU: true, HasMem: true},
		{Namespace: "demo", Name: "silent", Pods: 1},
	}})
	m = asModel(t, mi)

	// Default order: hottest CPU first.
	if m.usageRows[0].Name != "big" {
		t.Fatalf("default order must be CPU desc, got %+v", m.usageRows[0])
	}
	view := m.usageListView()
	for _, want := range []string{"NAME", "CPU", "MEMORY", "demo/big", "—"} {
		if !strings.Contains(view, want) {
			t.Fatalf("usage view missing %q:\n%s", want, view)
		}
	}

	// '/' filters rows like every other table.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = asModel(t, mi)
	if !m.usageTyping {
		t.Fatal("'/' must open the usage filter")
	}
	for _, r := range "big" {
		mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = asModel(t, mi)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if len(m.usageRows) != 1 || m.usageRows[0].Name != "big" {
		t.Fatalf("filter broken: %+v", m.usageRows)
	}

	// 's' cycles the sortable columns; 'S' flips.
	m.usageFilterQ = ""
	m.applyUsageFilter()
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = asModel(t, mi)
	if m.usageSortCol != 0 || m.usageRows[0].Name != "big" {
		t.Fatalf("name sort: col=%d first=%s", m.usageSortCol, m.usageRows[0].Name)
	}
}

// TestUsageAggregatesPerDeployment: from a workload list, rows sum the pods
// matched by each selector.
func TestUsageAggregatesPerDeployment(t *testing.T) {
	m := plainModel(t)
	m.screen = screenTop
	mi, _ := m.Update(usageTableMsg{isAgg: true, rows: []model.UsageRow{
		{Namespace: "demo", Name: "back", Pods: 3, CPU: 1.5, Mem: 3e9, HasCPU: true, HasMem: true},
	}})
	m = asModel(t, mi)
	view := m.usageListView()
	for _, want := range []string{"PODS", "back", "3"} {
		if !strings.Contains(view, want) {
			t.Fatalf("aggregated view missing %q:\n%s", want, view)
		}
	}
}

// TestDiagSelectionEnterAndSeverityFilter: findings are selectable, Enter
// jumps to the pod's describe (Esc returns), 'w' keeps errors only.
func TestDiagSelectionEnterAndSeverityFilter(t *testing.T) {
	m := plainModel(t)
	m.types = []model.ResourceType{{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}}
	m.screen = screenDiag
	m.renderDiag([]model.Diagnostic{
		{Namespace: "demo", Pod: "boom", Container: "app", Reason: "OOMKilled (x3 restarts)", Level: model.HealthError},
		{Namespace: "demo", Pod: "slow", Container: "app", Reason: "restarted x2", Level: model.HealthWarning},
	})
	if len(m.diagRefs) != 2 {
		t.Fatalf("refs=%v", m.diagRefs)
	}
	// Down selects the second finding; Enter opens its describe.
	mi, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(t, mi)
	mi, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.screen != screenDetail || cmd == nil || m.detailName != "slow" {
		t.Fatalf("Enter must open the pod describe (screen=%d name=%q)", m.screen, m.detailName)
	}
	// Esc returns to the failures view, not the list.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.screen != screenDiag {
		t.Fatalf("Esc must return to failures, screen=%d", m.screen)
	}
	// 'w' keeps only error-level findings.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	m = asModel(t, mi)
	if len(m.diagRefs) != 1 || m.diagRefs[0].name != "boom" {
		t.Fatalf("errors-only filter broken: %v", m.diagRefs)
	}
}

// TestPostureEnterTargetsTheRightKind: netpol findings open the namespace,
// TLS ones the secret, container rules the pod.
func TestPostureEnterTargetsTheRightKind(t *testing.T) {
	m := plainModel(t)
	m.screen = screenPosture
	m.renderPosture([]model.PostureFinding{
		{Rule: kube.RuleNoNetpol, Severity: model.HealthWarning, Namespace: "demo", Name: "demo"},
		{Rule: kube.RuleTLSExpiry, Severity: model.HealthError, Namespace: "demo", Name: "cert"},
		{Rule: kube.RulePrivileged, Severity: model.HealthError, Namespace: "demo", Name: "bad", Container: "app"},
	})
	want := map[string]string{"demo": "v1/namespaces", "cert": "v1/secrets", "bad": "v1/pods"}
	for _, ref := range m.postureRefs {
		if want[ref.name] != ref.typeKey {
			t.Errorf("ref %q → %q, want %q", ref.name, ref.typeKey, want[ref.name])
		}
	}
}

// TestEventsEnterOpensObject: Enter on the selected event jumps to the
// referenced object's describe.
func TestEventsEnterOpensObject(t *testing.T) {
	m := plainModel(t)
	m.types = []model.ResourceType{{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}}
	m.width, m.height = 100, 34
	m.layout()
	m.screen = screenEvents
	m.eventRows = []model.Event{
		{Reason: "BackOff", Type: "Warning", Message: "x", ObjKind: "Pod", ObjName: "web-1", Namespace: "demo", Time: time.Now()},
	}
	m.renderEvents()
	mi, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.screen != screenDetail || cmd == nil || m.detailName != "web-1" {
		t.Fatalf("Enter must open the event's object (screen=%d name=%q)", m.screen, m.detailName)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.screen != screenEvents {
		t.Fatalf("Esc must return to the timeline, screen=%d", m.screen)
	}
}

// TestHelmSort: 's' cycles columns with arrows, 'S' flips — like every table.
func TestHelmSort(t *testing.T) {
	m := helmModel(t)
	mi, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = asModel(t, mi)
	if m.helmSortCol != 0 {
		t.Fatalf("sortCol=%d", m.helmSortCol)
	}
	// Column 1 = RELEASE: back-api before front ascending.
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = asModel(t, mi)
	if got := m.helmWin.rows[0][1]; got != "back-api" {
		t.Fatalf("asc sort first=%q", got)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	m = asModel(t, mi)
	if got := m.helmWin.rows[0][1]; got != "front" {
		t.Fatalf("desc sort first=%q", got)
	}
}

// TestTopologySelectionAndDrill (owner request 2026-07-11): node headers are
// selectable, wide pod names get room, Enter drills into the node's pods.
func TestTopologySelectionAndDrill(t *testing.T) {
	m := plainModel(t)
	m.width = 300
	m.layout()
	m.types = []model.ResourceType{{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}}
	m.screen = screenTopology
	longPod := "ha-conversations/sdk-app-service-cfb7d7c8b-verylongsuffix"
	m.renderTopology([]model.TopologyNode{
		{Name: "node-a", Pods: []model.TopologyPod{{Namespace: "ha-conversations", Name: "sdk-app-service-cfb7d7c8b-verylongsuffix"}}},
		{Name: "node-b"},
	})
	content := m.vpRaw[screenTopology]
	if !strings.Contains(content, longPod) {
		t.Fatalf("wide terminal must show the full pod name:\n%s", content)
	}
	if len(m.topoNodeLines) != 2 {
		t.Fatalf("node lines=%v", m.topoNodeLines)
	}
	// Down selects node-b; Enter drills into its pods.
	mi, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(t, mi)
	if m.topoSel != 1 {
		t.Fatalf("topoSel=%d", m.topoSel)
	}
	mi, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.screen != screenList || m.drillNode != "node-b" || cmd == nil {
		t.Fatalf("Enter must drill (screen=%d node=%q)", m.screen, m.drillNode)
	}
}

// TestUsageEnterOpensRowObject: Enter on a usage row describes the pod (pods
// mode) or the workload (aggregate mode).
func TestUsageEnterOpensRowObject(t *testing.T) {
	m := plainModel(t)
	m.types = []model.ResourceType{{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}}
	m.screen = screenTop
	m.usageTypeKey = "v1/pods"
	mi, _ := m.Update(usageTableMsg{rows: []model.UsageRow{
		{Namespace: "demo", Name: "web-1", Pods: 1, CPU: 1, HasCPU: true},
	}})
	m = asModel(t, mi)
	mi, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, mi)
	if m.screen != screenDetail || cmd == nil || m.detailName != "web-1" {
		t.Fatalf("Enter must open the row's object (screen=%d name=%q)", m.screen, m.detailName)
	}
	mi, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.screen != screenTop {
		t.Fatalf("Esc must return to usage, screen=%d", m.screen)
	}
}

// TestFindingsMouseClickSelects: clicking a finding line selects it in the
// failures view (parity with the tables).
func TestFindingsMouseClickSelects(t *testing.T) {
	m := plainModel(t)
	m.types = []model.ResourceType{{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}}
	m.screen = screenDiag
	m.renderDiag([]model.Diagnostic{
		{Namespace: "demo", Pod: "a", Reason: "OOMKilled (x1)", Level: model.HealthError},
		{Namespace: "demo", Pod: "b", Reason: "OOMKilled (x2)", Level: model.HealthError},
	})
	// Click the second finding's line (content y = line + 2 with no scroll).
	y := m.diagLines[1] + 2
	mi, _ := m.Update(tea.MouseMsg{X: 4, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = asModel(t, mi)
	if m.diagSel != 1 {
		t.Fatalf("click must select the finding, sel=%d", m.diagSel)
	}
}

// TestKikooBannerGeometry (--kikoo): the banner renders on large terminals,
// vanishes on small ones, and mouse clicks keep hitting the right rows.
func TestKikooBannerGeometry(t *testing.T) {
	m := plainModel(t)
	m.kikoo = true
	m.width, m.height = 120, 40
	m.layout()
	m.objects = []model.ResourceObject{
		{Name: "first", Namespace: "demo"},
		{Name: "second", Namespace: "demo"},
	}
	m.applyRows()

	view := m.View()
	if !strings.Contains(view, "iAdvize") {
		t.Fatalf("banner missing on a large terminal")
	}
	if got := strings.Count(view, "\n") + 1; got > m.height {
		t.Fatalf("view is %d lines for a %d-line terminal (banner must fit the budget)", got, m.height)
	}

	// A click on the SECOND row: banner-less geometry says y=4; on screen it
	// is bannerH lower. The normalized handler must select row 1.
	y := 4 + m.bannerH()
	mi, _ := m.Update(tea.MouseMsg{X: 5, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = asModel(t, mi)
	if m.win.cursor != 1 {
		t.Fatalf("banner shifted the click mapping: cursor=%d want 1", m.win.cursor)
	}
	// Clicks inside the banner are inert.
	mi, _ = m.Update(tea.MouseMsg{X: 5, Y: 1, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = asModel(t, mi)
	if m.win.cursor != 1 {
		t.Fatal("banner clicks must be decoration-only")
	}

	// Small terminal: banner disappears entirely.
	m.height = 24
	m.layout()
	if m.bannerH() != 0 || strings.Contains(m.View(), "iAdvize 💚") {
		t.Fatal("banner must vanish on small terminals")
	}
}

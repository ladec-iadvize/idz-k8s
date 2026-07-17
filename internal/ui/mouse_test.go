package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestWinTableWindowingAndClicks(t *testing.T) {
	w := winTable{}
	w.SetHeight(5)
	rows := make([]table.Row, 20)
	for i := range rows {
		rows[i] = table.Row{"ns", "row"}
	}
	w.SetRows(rows)

	// Cursor movement drags the window.
	w.Move(9)
	if w.cursor != 9 || w.win != 5 {
		t.Fatalf("cursor=%d win=%d, want 9/5", w.cursor, w.win)
	}
	// Click maps window-relative position to absolute row.
	if !w.ClickVisible(2) || w.cursor != 7 {
		t.Fatalf("click rel=2 should select row 7, got %d", w.cursor)
	}
	// Out-of-range clicks are rejected.
	if w.ClickVisible(99) || w.ClickVisible(-1) {
		t.Fatal("out-of-range clicks must be rejected")
	}
	// Range reports the visible span.
	if from, to, total := w.Range(); from != 6 || to != 10 || total != 20 {
		t.Fatalf("range=%d-%d/%d, want 6-10/20", from, to, total)
	}
	// End clamps window to the tail.
	w.End()
	if w.cursor != 19 || w.win != 15 {
		t.Fatalf("End: cursor=%d win=%d, want 19/15", w.cursor, w.win)
	}
}

func listModelForMouse(t *testing.T, n int) Model {
	t.Helper()
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	for i := 0; i < n; i++ {
		m.objects = append(m.objects, model.ResourceObject{
			Type: pods, Namespace: "demo", Name: "pod-" + string(rune('a'+i)),
			Status: model.StatusSummary{Level: model.HealthOk},
			Raw:    map[string]interface{}{"apiVersion": "v1", "kind": "Pod"},
		})
	}
	m.width, m.height = 100, 20
	m.layout()
	m.applyRows()
	return m
}

func click(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
}

func TestMouseClickSelectsRow(t *testing.T) {
	m := listModelForMouse(t, 6)
	// Rows start at y=3; click the 4th visible row.
	mi, _ := m.Update(click(5, 6))
	m = asModel(t, mi)
	row, ok := m.win.Selected()
	if !ok || row[2] != "pod-d" {
		t.Fatalf("click y=5 should select pod-d, got %v", row)
	}
}

func TestMouseDoubleClickOpensDetail(t *testing.T) {
	m := listModelForMouse(t, 3)
	mi, _ := m.Update(click(5, 3))
	m = asModel(t, mi)
	mi, _ = m.Update(click(5, 3)) // within 500ms in test time
	m = asModel(t, mi)
	if m.screen != screenDetail {
		t.Fatalf("double-click should open the detail, screen=%d", m.screen)
	}
}

func TestMouseWheelMovesSelection(t *testing.T) {
	m := listModelForMouse(t, 30)
	wheel := tea.MouseMsg{Button: tea.MouseButtonWheelDown, Action: tea.MouseActionPress}
	mi, _ := m.Update(wheel)
	m = asModel(t, mi)
	if m.win.cursor != 3 {
		t.Fatalf("wheel down should move selection by 3, got %d", m.win.cursor)
	}
}

func TestEventsClickSelectsRecent(t *testing.T) {
	m := eventsModel(t)
	m.renderEvents()
	if m.recentShown < 2 {
		t.Fatalf("fixture should show at least 2 recent events, got %d", m.recentShown)
	}
	// Click the 2nd detail row: viewport starts at y=2, content line =
	// YOffset + y - 2 must equal recentBaseLine+1.
	y := m.recentBaseLine + 1 + 2 - m.events.YOffset
	mi, _ := m.Update(click(4, y))
	m = asModel(t, mi)
	if m.recentSel != 1 {
		t.Fatalf("click should select the 2nd recent event, got %d", m.recentSel)
	}
	_ = time.Now
}

func TestSpaceMarksAndScopesEvents(t *testing.T) {
	m := listModelForMouse(t, 4)
	// Space marks the row under the cursor; the row gains the ● marker.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = asModel(t, mi)
	if len(m.marked) != 1 {
		t.Fatalf("space should mark 1 resource, got %d", len(m.marked))
	}
	row, _ := m.win.Selected()
	if row[0] != "●" {
		t.Fatalf("marked row should show ●, got %q", row[0])
	}
	// The events view (palette entry) scopes to the marked resource.
	mi, _ = m.openEvents()
	m = asModel(t, mi)
	if m.eventsScope == nil || !m.eventsScope["demo/pod-a"] {
		t.Fatalf("events should be scoped to the marked resource, got %v", m.eventsScope)
	}
	if m.eventsScopeFor != "1 marked" {
		t.Fatalf("scope label=%q", m.eventsScopeFor)
	}
}

func TestSortCycleAndHeaderClick(t *testing.T) {
	m := listModelForMouse(t, 3) // pod-a, pod-b, pod-c (API order)
	// Reverse the fixture so unsorted != sorted.
	m.objects[0], m.objects[2] = m.objects[2], m.objects[0]
	m.applyRows()
	row, _ := m.win.Selected()
	if row[2] != "pod-c" {
		t.Fatalf("precondition: unsorted first row should be pod-c, got %q", row[2])
	}
	// 's' cycles to NAMESPACE sort, then to NAME.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = asModel(t, mi)
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = asModel(t, mi)
	if m.sortCol != 2 || !m.sortAsc {
		t.Fatalf("sort state: col=%d asc=%v", m.sortCol, m.sortAsc)
	}
	m.win.Home()
	row, _ = m.win.Selected()
	if row[2] != "pod-a" {
		t.Fatalf("NAME asc should put pod-a first, got %q", row[2])
	}
	// 'S' reverses.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = asModel(t, mi)
	m.win.Home()
	row, _ = m.win.Selected()
	if row[2] != "pod-c" {
		t.Fatalf("NAME desc should put pod-c first, got %q", row[2])
	}
	// Header click on NAME (x within its range) toggles again → asc.
	x := 2 + 1 + 28 + 1 // inside the NAME column (after the mark and ns columns + gaps)
	mi, _ = m.Update(click(x, 2))
	m = asModel(t, mi)
	if m.sortCol != 2 || !m.sortAsc {
		t.Fatalf("header click should re-sort NAME asc, got col=%d asc=%v", m.sortCol, m.sortAsc)
	}
}

func TestLogsPauseStopsAutoScroll(t *testing.T) {
	m := listModelForMouse(t, 1)
	m.screen = screenLogs
	// Space pauses; buffering continues; status explains.
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = asModel(t, mi)
	if !m.logPaused || !strings.Contains(m.statusMsg, "paused") {
		t.Fatalf("space should pause logs, paused=%v msg=%q", m.logPaused, m.statusMsg)
	}
	mi, _ = m.Update(logLineMsg{Text: "while paused"})
	m = asModel(t, mi)
	if len(m.logBuf) != 1 {
		t.Fatal("lines must keep buffering while paused")
	}
	// End resumes.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = asModel(t, mi)
	if m.logPaused {
		t.Fatal("End should resume the follow")
	}
	// Scrolling up auto-pauses.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = asModel(t, mi)
	if !m.logPaused {
		t.Fatal("scrolling up should auto-pause")
	}
}

func TestEventsWarningsOnlyFilter(t *testing.T) {
	m := eventsModel(t) // fixture: 1 Warning (BackOff) + 1 Normal (ScalingReplicaSet)
	mi, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	m = asModel(t, mi)
	if !m.eventsWarnOnly {
		t.Fatal("'w' should enable warnings-only")
	}
	view := m.events.View()
	if !strings.Contains(view, "BackOff") {
		t.Error("warning event must remain")
	}
	if strings.Contains(view, "ScalingReplicaSet") {
		t.Error("normal event must be filtered out")
	}
	if !strings.Contains(view, "warnings only") {
		t.Error("header should show the severity chip")
	}
}

func TestFooterLabelClickTriggersAction(t *testing.T) {
	m := listModelForMouse(t, 2)
	var fz *clickZone
	for _, z := range m.footerZones() {
		if z.key == ">" {
			zz := z
			fz = &zz
			break
		}
	}
	if fz == nil {
		t.Fatal("footer should expose a zone for the '>' palette")
	}
	mi, _ := m.Update(click(fz.x0, m.bodyH+4))
	m = asModel(t, mi)
	if m.screen != screenPicker || m.pickerKind != pickPalette {
		t.Fatalf("clicking the palette label should open it, screen=%d kind=%d", m.screen, m.pickerKind)
	}
}

func TestHeaderChipClickOpensPicker(t *testing.T) {
	m := listModelForMouse(t, 2)
	var nz *clickZone
	for _, z := range m.headerZones() {
		if z.key == "n" {
			zz := z
			nz = &zz
			break
		}
	}
	if nz == nil {
		t.Fatal("header should expose the ns chip zone")
	}
	mi, _ := m.Update(click(nz.x0, 0))
	m = asModel(t, mi)
	if m.screen != screenPicker || m.pickerKind != pickNamespace {
		t.Fatalf("clicking the ns chip should open the namespace picker, screen=%d kind=%d", m.screen, m.pickerKind)
	}
}

func TestReconnectStatusTransition(t *testing.T) {
	m := listModelForMouse(t, 1)
	mi, _ := m.Update(objectsMsg{err: contextDeadline{}})
	m = asModel(t, mi)
	if !m.disconnected || !strings.Contains(m.errMsg, "retrying") {
		t.Fatalf("error should flag disconnection, got %q", m.errMsg)
	}
	mi, _ = m.Update(objectsMsg{objects: m.objects})
	m = asModel(t, mi)
	if m.disconnected || m.errMsg != "" || !strings.Contains(m.statusMsg, "reconnected") {
		t.Fatalf("success should announce reconnection, got err=%q status=%q", m.errMsg, m.statusMsg)
	}
}

type contextDeadline struct{}

func (contextDeadline) Error() string { return "context deadline exceeded" }

func TestHeaderChipWorksFromAnyScreen(t *testing.T) {
	m := listModelForMouse(t, 2)
	m.screen = screenTopology // a sub-view, not the list
	var tz *clickZone
	for _, z := range m.headerZones() {
		if z.key == ":" {
			zz := z
			tz = &zz
			break
		}
	}
	if tz == nil {
		t.Fatal("type chip zone missing")
	}
	mi, _ := m.Update(click(tz.x0, 0))
	m = asModel(t, mi)
	if m.screen != screenPicker || m.pickerReturn != screenTopology {
		t.Fatalf("type chip should open the picker from topology, screen=%d return=%d", m.screen, m.pickerReturn)
	}
	// Esc returns to the topology view, not the list.
	mi, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = asModel(t, mi)
	if m.screen != screenTopology {
		t.Fatalf("Esc should return to topology, got %d", m.screen)
	}
}

func TestPickerModalClickAndOutsideClose(t *testing.T) {
	m := listModelForMouse(t, 2)
	m.types = []model.ResourceType{
		{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true},
		{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true},
	}
	mi, _ := m.openPicker(pickType)
	m = asModel(t, mi)
	_, geom := m.pickerModal()
	// Double-click the SECOND option (apps/v1/deployments — the helm entry is
	// pinned first).
	mi, _ = m.Update(click(geom.x+2, geom.optTop+1))
	m = asModel(t, mi)
	mi, _ = m.Update(click(geom.x+2, geom.optTop+1))
	m = asModel(t, mi)
	if m.curType.Resource != "deployments" {
		t.Fatalf("double-click should pick deployments, got %q", m.curType.Key())
	}
	// Reopen; a click OUTSIDE the box closes the modal.
	mi, _ = m.openPicker(pickType)
	m = asModel(t, mi)
	mi, _ = m.Update(click(0, m.bodyH+2)) // far from the centered box
	m = asModel(t, mi)
	if m.screen == screenPicker {
		t.Fatal("clicking outside the modal should close it")
	}
}

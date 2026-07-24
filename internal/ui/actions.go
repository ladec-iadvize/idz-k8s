package ui

// User actions and per-screen key handlers: list/top navigation,
// drill-down into pods, describe/detail/logs openers (goBack stays in app.go).

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func (m Model) handleTopKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Open):
		if c := m.usageWin.cursor; c >= 0 && c < len(m.usageRows) {
			r := m.usageRows[c]
			return m.openDescribeRef(objRef{typeKey: m.usageTypeKey, ns: r.Namespace, name: r.Name})
		}
		return m, nil
	case hit(msg, m.keys.Filter):
		m.usageTyping = true
		return m, nil
	case hit(msg, m.keys.Sort):
		m.usageSortCol++
		if m.usageSortCol >= len(m.usageColumns()) {
			m.usageSortCol = -1 // back to CPU-desc default
		}
		m.usageSortAsc = true
		m.applyUsageSort()
		return m, nil
	case hit(msg, m.keys.SortDir):
		m.usageSortAsc = !m.usageSortAsc
		m.applyUsageSort()
		return m, nil
	}
	m.navigate(&m.usageWin, msg)
	return m, nil
}

func (m Model) handleListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case hit(msg, m.keys.Filter):
		m.filtering = true
		m.filter.Focus()
		return m, nil
	case hit(msg, m.keys.Open):
		return m.openListSelection()
	case hit(msg, m.keys.Yaml):
		cmd := m.openDetail()
		return m, cmd
	case hit(msg, m.keys.Describe):
		return m, m.openDescribe()
	case hit(msg, m.keys.Owner):
		return m, m.gotoOwner()
	case hit(msg, m.keys.Actions):
		return m.openActions()
	case hit(msg, m.keys.Edit):
		return m, m.startEdit()
	case hit(msg, m.keys.Logs):
		return m.openLogs()
	case hit(msg, m.keys.Mark):
		m.toggleMark()
		return m, nil
	case hit(msg, m.keys.Sort):
		// Cycle through the current type's columns, then back to none.
		m.sortCol++
		if m.sortCol > len(m.columnsForType()) {
			m.sortCol = -1
		} else if m.sortCol < 1 {
			m.sortCol = 1
		}
		m.sortAsc = true
		m.applyRows()
		m.persistViewPref()
		return m, nil
	case hit(msg, m.keys.SortDir):
		if m.sortCol >= 1 {
			m.sortAsc = !m.sortAsc
			m.applyRows()
			m.persistViewPref()
		}
		return m, nil
	case hit(msg, m.keys.Columns):
		return m.openColumnChooser()
	case hit(msg, m.keys.Views):
		return m.openViewPicker()
	case hit(msg, m.keys.ResetView):
		m.resetCurrentView()
		return m, nil
	}
	m.navigate(&m.win, msg)
	return m, nil
}

// toggleMark marks/unmarks the resource under the cursor (Space). Marked
// resources scope the failures ('f') and events ('v') views.
func (m *Model) toggleMark() {
	obj, ok := m.selectedObject()
	if !ok {
		return
	}
	key := obj.Namespace + "/" + obj.Name
	if _, ok := m.marked[key]; ok {
		delete(m.marked, key)
	} else {
		m.marked[key] = obj
	}
	if len(m.marked) > 0 {
		m.statusMsg = fmt.Sprintf("%d marked — 'f'/'v' scope to the selection, Space to unmark", len(m.marked))
	} else {
		m.statusMsg = ""
	}
	m.applyRows()
}

// drillNodePods opens the node-drill pod list from anywhere (topology).
func (m *Model) drillNodePods(node string) (tea.Model, tea.Cmd) {
	pods, okType := findTypeByKey(m.types, "v1/pods")
	if !okType {
		pods = podResourceType
	}
	m.drillPrevType = m.curType
	m.drillNode = node
	m.drillFor = "Node/" + node
	m.curType = pods
	m.screen = screenList
	m.statusMsg = "pods on " + m.drillFor + " — Esc to go back"
	m.layout()
	if m.metrics.Enabled() {
		return m, tea.Batch(m.listObjects(), m.fetchListUsage())
	}
	return m, m.listObjects()
}

// openListSelection is the Enter action of the list: drill into a workload's
// pods, or open the YAML detail (k9s-like).
func (m Model) openListSelection() (tea.Model, tea.Cmd) {
	if cmd, ok := m.drillIntoPods(); ok {
		return m, cmd
	}
	cmd := m.openDetail()
	return m, cmd
}

// navigate applies standard movement keys to a windowed table.
func (m *Model) navigate(w *winTable, msg tea.KeyMsg) {
	switch {
	case hit(msg, m.keys.Up):
		w.Move(-1)
	case hit(msg, m.keys.Down):
		w.Move(1)
	case hit(msg, m.keys.PageUp):
		w.Page(-1)
	case hit(msg, m.keys.PageDown):
		w.Page(1)
	case hit(msg, m.keys.Home):
		w.Home()
	case hit(msg, m.keys.End):
		w.End()
	}
}

func (m Model) handleScrollKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	if m.screen == screenDetail {
		// Toggle secret reveal on the detail of a Secret (keys.Reveal — 'x'
		// also means connectivity on the list; per-screen scoping keeps both).
		if hit(msg, m.keys.Reveal) && strings.EqualFold(m.curType.Kind, "Secret") {
			m.revealSecret = !m.revealSecret
			m.renderDetail()
			return m, nil
		}
		m.detail, cmd = m.detail.Update(msg)
		return m, cmd
	}
	// Logs: Space pauses/resumes the follow; scrolling up pauses so the view
	// stops being yanked to the bottom; End resumes at the tail (FR-005).
	switch {
	case hit(msg, m.keys.Pause):
		m.logPaused = !m.logPaused
		if m.logPaused {
			m.statusMsg = "logs paused — Space or End to resume"
		} else {
			m.statusMsg = ""
			m.logsView.GotoBottom()
		}
		return m, nil
	case hit(msg, m.keys.End):
		m.logPaused = false
		m.statusMsg = ""
		m.logsView.GotoBottom()
		return m, nil
	case hit(msg, m.keys.Up) || hit(msg, m.keys.PageUp):
		if !m.logPaused {
			m.logPaused = true
			m.statusMsg = "logs paused — Space or End to resume"
		}
	}
	m.logsView, cmd = m.logsView.Update(msg)
	return m, cmd
}

func (m Model) delegate(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.screen {
	case screenDetail:
		m.detail, cmd = m.detail.Update(msg)
	case screenLogs:
		m.logsView, cmd = m.logsView.Update(msg)
	case screenDiag:
		m.diag, cmd = m.diag.Update(msg)
	case screenSizing:
		m.sizingVP, cmd = m.sizingVP.Update(msg)
	case screenPosture:
		m.posture, cmd = m.posture.Update(msg)
	case screenConnectivity:
		m.connectivity, cmd = m.connectivity.Update(msg)
	case screenAccess:
		m.access, cmd = m.access.Update(msg)
	case screenDrift:
		m.drift, cmd = m.drift.Update(msg)
	case screenTopology:
		m.topo, cmd = m.topo.Update(msg)
	case screenEvents:
		m.events, cmd = m.events.Update(msg)
	case screenHelm:
		m.helmTable, cmd = m.helmTable.Update(msg)
	case screenHelmHist:
		m.helmHist, cmd = m.helmHist.Update(msg)
	}
	return m, cmd
}

// drillIntoPods switches the list to the pods owned by the selected workload
// (Deployment, ReplicaSet, StatefulSet, DaemonSet, Job, Service) via its label
// selector. ok=false when the selection has no pod selector (e.g. a Pod).
func (m *Model) drillIntoPods() (tea.Cmd, bool) {
	obj, found := m.selectedObject()
	if !found || strings.EqualFold(m.curType.Kind, "Pod") || m.drillSelector != "" || m.drillNode != "" {
		return nil, false
	}
	pods, okType := findTypeByKey(m.types, "v1/pods")
	if !okType {
		pods = podResourceType
	}
	// A node has no selector: drilling shows the pods scheduled on it —
	// the list twin of the topology view (complementary, not redundant:
	// topology is capacity-oriented, this is a full pod list with filter,
	// sort, marks, logs…).
	if strings.EqualFold(m.curType.Kind, "Node") {
		m.drillPrevType = m.curType
		m.drillNode = obj.Name
		m.drillFor = "Node/" + obj.Name
		m.curType = pods
		m.statusMsg = "pods on " + m.drillFor + " — Esc to go back"
		return m.listObjects(), true
	}
	sel, ok := kube.PodSelector(obj.Raw)
	if !ok {
		return nil, false
	}
	m.drillPrevType = m.curType
	m.drillSelector = sel
	m.drillFor = m.curType.Kind + "/" + obj.Name
	m.drillNamespace = obj.Namespace // query scope only; ns filter untouched
	m.curType = pods
	m.statusMsg = "pods of " + m.drillFor + " — Esc to go back"
	if m.metrics.Enabled() {
		return tea.Batch(m.listObjects(), m.fetchListUsage()), true
	}
	return m.listObjects(), true
}

// gotoOwner walks one step UP the ownership chain (US9): from a Pod to its
// ReplicaSet, from a ReplicaSet to its Deployment, etc. It switches the list
// to the owner's type with the filter pre-set to the owner's name, so pressing
// 'o' repeatedly climbs the chain.
func (m *Model) gotoOwner() tea.Cmd {
	obj, found := m.selectedObject()
	if !found {
		return nil
	}
	ref, ok := kube.Owner(obj.Raw)
	if !ok {
		m.statusMsg = m.curType.Kind + "/" + obj.Name + " has no owner (top of the chain)"
		return nil
	}
	var ownerType model.ResourceType
	okType := false
	for _, t := range m.types {
		if t.Group == ref.Group && t.Version == ref.Version && t.Kind == ref.Kind {
			ownerType, okType = t, true
			break
		}
	}
	if !okType {
		m.statusMsg = fmt.Sprintf("owner %s/%s: type not browsable", ref.Kind, ref.Name)
		return nil
	}
	// Leave any drill; land on the owner's list, filtered to its name.
	m.resetDrill()
	m.curType = ownerType
	m.filter.SetValue(ref.Name)
	m.statusMsg = "owner of " + obj.Name + ": " + ref.Kind + "/" + ref.Name
	return m.listObjects()
}

// exitDrill restores the workload list the drill-down came from.
// resetDrill clears every drill-down scope field (selector, node, label,
// owning namespace).
func (m *Model) resetDrill() {
	m.drillSelector, m.drillNode, m.drillFor, m.drillNamespace = "", "", "", ""
}

func (m *Model) exitDrill() tea.Cmd {
	m.curType = m.drillPrevType
	m.resetDrill()
	m.statusMsg = ""
	return m.listObjects()
}

// openDescribe shows a describe-style summary (metadata, conditions) plus the
// object's own events — like `kubectl describe`.
func (m *Model) openDescribe() tea.Cmd {
	obj, found := m.selectedObject()
	if !found {
		return nil
	}
	m.detailObj = obj
	m.detailNS, m.detailName = obj.Namespace, obj.Name
	m.detailHasUsage = false
	m.screen = screenDetail
	m.setDetailContent(describeContent(obj, nil, m.theme, m.width) + "\nEvents: loading…")
	m.detail.GotoTop()
	m.layout()
	cl, ns, name := m.client, obj.Namespace, obj.Name
	kind := m.curType.Kind
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		rows, _ := cl.Events(ctx, ns)
		var own []model.Event
		for _, e := range rows {
			if e.ObjName == name {
				own = append(own, e)
			}
		}
		// Services: surface the backends so a broken link (0 ready endpoints)
		// is visible at describe time (US9, SC-015).
		extra := ""
		if strings.EqualFold(kind, "Service") {
			ready, notReady, err := cl.EndpointsSummary(ctx, ns, name)
			switch {
			case err != nil:
				extra = "\nEndpoints: unavailable (" + err.Error() + ")\n"
			case ready == 0:
				extra = fmt.Sprintf("\nEndpoints: ⚠ 0 ready (%d not ready) — service has NO backends\n", notReady)
			default:
				extra = fmt.Sprintf("\nEndpoints: %d ready, %d not ready\n", ready, notReady)
			}
		}
		return describeMsg{ns: ns, name: name, events: own, extra: extra}
	}
}

func (m *Model) openDetail() tea.Cmd {
	obj, ok := m.selectedObject()
	if !ok {
		return nil
	}
	m.detailObj = obj
	m.detailNS, m.detailName = obj.Namespace, obj.Name
	m.detailHasUsage = false
	m.detailCPU, m.detailMem = model.Usage{}, model.Usage{}
	m.screen = screenDetail
	m.renderDetail()
	m.detail.GotoTop()
	m.layout()
	// For pods, fetch usage (gauges + 1h trend) from Prometheus, if configured.
	if m.metrics.Enabled() && strings.EqualFold(m.curType.Kind, "Pod") {
		return m.fetchPodUsage(obj.Namespace, obj.Name, obj.Raw)
	}
	return nil
}

func (m Model) openLogs() (tea.Model, tea.Cmd) {
	obj, ok := m.selectedObject()
	if !ok {
		return m, nil
	}
	if m.logCancel != nil {
		m.logCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())

	switch {
	case strings.EqualFold(m.curType.Kind, "Pod"):
		m.logCh = m.client.StreamPodLogs(ctx, obj.Namespace, obj.Name, "", 200, true)
		m.statusMsg = "logs: " + obj.Name
	default:
		// Workloads: merge the logs of every pod the selector owns (FR-034).
		sel, hasSel := kube.PodSelector(obj.Raw)
		if !hasSel {
			cancel()
			m.statusMsg = "logs are available on pods and workloads with a selector"
			return m, nil
		}
		m.logCh = m.client.StreamWorkloadLogs(ctx, obj.Namespace, sel, 100, true)
		m.statusMsg = "merged logs: " + m.curType.Kind + "/" + obj.Name + " (one prefix per pod)"
	}

	m.logCancel = cancel
	m.logBuf = nil
	m.logPaused = false
	m.setContent(screenLogs, "waiting for logs…")
	m.screen = screenLogs
	m.layout()
	return m, m.nextLogLine()
}

func (m Model) nextLogLine() tea.Cmd {
	ch := m.logCh
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return logLineMsg{Done: true}
		}
		return logLineMsg(line)
	}
}

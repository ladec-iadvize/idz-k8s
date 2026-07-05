package ui

import (
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func strip(s string) string { return ansiRe.ReplaceAllString(s, "") }

// TestSnapshotRender renders the list and detail frames to scratchpad files so
// the visual layout can be inspected without a cluster or a live terminal.
func TestSnapshotRender(t *testing.T) {
	out := os.Getenv("SNAPSHOT_DIR")
	if out == "" {
		t.Skip("set SNAPSHOT_DIR to render snapshots")
	}
	now := time.Now()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}))
	m.objects = []model.ResourceObject{
		{Namespace: "demo", Name: "web-1", Status: model.StatusSummary{Level: model.HealthOk, Reason: "Running"}, CreatedAt: now.Add(-72 * time.Hour),
			Raw: map[string]interface{}{"apiVersion": "v1", "kind": "Pod",
				"metadata": map[string]interface{}{"name": "web-1", "namespace": "demo"},
				"status":   map[string]interface{}{"phase": "Running", "podIP": "10.1.2.3"}}},
		{Namespace: "demo", Name: "web-2", Status: model.StatusSummary{Level: model.HealthWarning, Reason: "Pending"}, CreatedAt: now.Add(-10 * time.Minute)},
		{Namespace: "demo", Name: "api-0", Status: model.StatusSummary{Level: model.HealthError, Reason: "CrashLoopBackOff"}, CreatedAt: now.Add(-5 * time.Hour)},
	}
	m.width, m.height = 100, 20
	m.layout()
	m.applyRows()

	if err := os.WriteFile(out+"/list.txt", []byte(strip(m.View())), 0o644); err != nil {
		t.Fatal(err)
	}

	m.openDetail()
	if err := os.WriteFile(out+"/detail.txt", []byte(strip(m.View())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Picker with type-to-filter (the ":" flow).
	m.types = []model.ResourceType{
		{Version: "v1", Resource: "pods", Kind: "Pod"},
		{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment"},
		{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet"},
		{Group: "apps", Version: "v1", Resource: "daemonsets", Kind: "DaemonSet"},
		{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job"},
	}
	mi, _ := m.openPicker(pickType)
	pm := mi.(Model)
	for _, r := range "dep" {
		mi, _ = pm.handlePickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		pm = mi.(Model)
	}
	if err := os.WriteFile(out+"/picker.txt", []byte(strip(pm.View())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Namespace picker with the "all namespaces" option on top.
	nm := m
	nm.pickerKind = pickNamespace
	nm.pickerQuery = ""
	nm.pickerOpts = []string{allNamespacesLabel, "default", "demo", "kube-system", "prod"}
	nm.picker.SetColumns([]table.Column{{Title: "select (type to filter)", Width: 60}})
	nm.applyPickerRows()
	nm.screen = screenPicker
	nm.layout()
	if err := os.WriteFile(out+"/ns_picker.txt", []byte(strip(nm.View())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Top consumers view (US2).
	tv := m
	tv.topKind = model.MetricCPU
	tv.screen = screenTop
	tv.layout()
	tv.renderTop([]model.TopConsumer{
		{Namespace: "prod", Name: "api-7c9", Kind: model.MetricCPU, Value: 1.85},
		{Namespace: "prod", Name: "worker-2", Kind: model.MetricCPU, Value: 1.10},
		{Namespace: "demo", Name: "web-1", Kind: model.MetricCPU, Value: 0.45},
	})
	if err := os.WriteFile(out+"/top.txt", []byte(strip(tv.View())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Detail usage panel (US2): a pod with CPU/memory gauges + 1h sparkline.
	series := func(vals ...float64) []model.MetricSample {
		s := make([]model.MetricSample, len(vals))
		for i, v := range vals {
			s[i] = model.MetricSample{T: now, Value: v}
		}
		return s
	}
	dv := m
	dv.detailObj = dv.objects[0]
	dv.detailHasUsage = true
	dv.detailCPU = model.Usage{Kind: model.MetricCPU, Current: 0.45, Request: 0.5, Limit: 1.0, Available: true,
		Series: series(0.30, 0.35, 0.40, 0.55, 0.50, 0.45)}
	dv.detailMem = model.Usage{Kind: model.MetricMemory, Current: 380 * 1024 * 1024, Request: 256 * 1024 * 1024, Limit: 512 * 1024 * 1024, Available: true,
		Series: series(300, 320, 350, 370, 390, 380)}
	dv.screen = screenDetail
	dv.layout()
	dv.renderDetail()
	if err := os.WriteFile(out+"/detail_usage.txt", []byte(strip(dv.View())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Failure diagnostics view (US10).
	fv := m
	fv.screen = screenDiag
	fv.layout()
	fv.renderDiag([]model.Diagnostic{
		{Namespace: "prod", Pod: "api-7c9", Container: "api", Restarts: 5, Reason: "OOMKilled (x5 restarts)", Level: model.HealthError},
		{Namespace: "prod", Pod: "worker-2", Container: "worker", Restarts: 3, Reason: "CrashLoopBackOff", Level: model.HealthError},
		{Namespace: "demo", Pod: "batch-9", Container: "job", Restarts: 2, Reason: "Error (exit 1, x2)", Level: model.HealthWarning},
		{Namespace: "demo", Pod: "old-pod", Reason: "Evicted: node low on memory", Level: model.HealthError},
	})
	if err := os.WriteFile(out+"/diag.txt", []byte(strip(fv.View())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Topology view (US4).
	pv := m
	pv.screen = screenTopology
	pv.layout()
	gi := 1024.0 * 1024 * 1024
	pv.renderTopology([]model.TopologyNode{
		{Name: "ip-10-0-1-12", Status: model.HealthOk, Reason: "Ready",
			AllocCPU: 8, AllocMem: 16 * gi, ReqCPU: 3.2, ReqMem: 6 * gi,
			Pods: []model.TopologyPod{
				{Namespace: "prod", Name: "api-7c9", Status: model.HealthOk, CPUReq: 2, MemReq: 4 * gi},
				{Namespace: "prod", Name: "worker-2", Status: model.HealthError, CPUReq: 1.2, MemReq: 2 * gi},
			}},
		{Name: "ip-10-0-2-34", Status: model.HealthWarning, Reason: "MemoryPressure",
			AllocCPU: 4, AllocMem: 8 * gi, ReqCPU: 3.5, ReqMem: 7 * gi,
			Pods: []model.TopologyPod{
				{Namespace: "demo", Name: "web-1", Status: model.HealthOk, CPUReq: 0.5, MemReq: 512 * 1024 * 1024},
			}},
	})
	if err := os.WriteFile(out+"/topology.txt", []byte(strip(pv.View())), 0o644); err != nil {
		t.Fatal(err)
	}

	// Events timeline (US5).
	ev := m
	ev.screen = screenEvents
	ev.layout()
	ev.eventRows = []model.Event{
		{Time: now.Add(-1 * time.Minute), Type: "Warning", Reason: "BackOff", Message: "Back-off restarting failed container api", ObjKind: "Pod", ObjName: "api-7c9", Count: 3},
		{Time: now.Add(-12 * time.Minute), Type: "Warning", Reason: "BackOff", Message: "Back-off restarting failed container api", ObjKind: "Pod", ObjName: "api-7c9", Count: 2},
		{Time: now.Add(-28 * time.Minute), Type: "Warning", Reason: "Unhealthy", Message: "Liveness probe failed: HTTP 503", ObjKind: "Pod", ObjName: "api-7c9"},
		{Time: now.Add(-8 * time.Minute), Type: "Normal", Reason: "Pulled", Message: "Container image already present on machine", ObjKind: "Pod", ObjName: "web-1"},
		{Time: now.Add(-45 * time.Minute), Type: "Normal", Reason: "Scheduled", Message: "Successfully assigned demo/web-1 to ip-10-0-1-12", ObjKind: "Pod", ObjName: "web-1"},
		{Time: now.Add(-35 * time.Minute), Type: "Warning", Reason: "FailedScheduling", Message: "0/3 nodes are available: insufficient cpu", ObjKind: "Pod", ObjName: "batch-9"},
	}
	ev.renderEvents()
	if err := os.WriteFile(out+"/events.txt", []byte(strip(ev.View())), 0o644); err != nil {
		t.Fatal(err)
	}
}

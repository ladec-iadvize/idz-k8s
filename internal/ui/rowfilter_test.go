package ui

// Filtering across every visible column (owner request 2026-08-03: '/' only
// looked at namespace/name, so a node's VERSION was unfilterable).

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestFilterTermsAndHaystack(t *testing.T) {
	if got := filterTerms("  Back   1.30 "); len(got) != 2 || got[0] != "back" || got[1] != "1.30" {
		t.Fatalf("terms=%v (lowercase, whitespace-split)", got)
	}
	if len(filterTerms("   ")) != 0 {
		t.Fatal("a blank query has no terms")
	}
	// ANSI in a cell must not hide the text behind it.
	hay := rowHaystack("demo/web-1", "\x1b[32m✓ Running\x1b[0m", "v1.30.2")
	if !strings.Contains(hay, "running") || !strings.Contains(hay, "v1.30.2") {
		t.Fatalf("haystack=%q", hay)
	}
	if strings.Contains(hay, "\x1b") {
		t.Fatalf("escape sequences must be stripped: %q", hay)
	}
	if !matchesTerms(hay, []string{"web-1", "running"}) {
		t.Fatal("every term must be matchable")
	}
	if matchesTerms(hay, []string{"web-1", "absent"}) {
		t.Fatal("terms are AND-ed, not OR-ed")
	}
}

// nodeListModel lists two nodes with different kubelet versions and roles.
func nodeListModel(t *testing.T) Model {
	t.Helper()
	nodes := model.ResourceType{Version: "v1", Kind: "Node", Resource: "nodes"}
	mk := func(name, version, ip string) model.ResourceObject {
		return model.ResourceObject{Type: nodes, Name: name,
			Raw: map[string]any{
				"apiVersion": "v1", "kind": "Node",
				"metadata": map[string]any{"name": name},
				"status": map[string]any{
					"nodeInfo":  map[string]any{"kubeletVersion": version},
					"addresses": []any{map[string]any{"type": "InternalIP", "address": ip}},
					"conditions": []any{
						map[string]any{"type": "Ready", "status": "True"},
					},
				},
			}}
	}
	m := New(&kube.Client{Namespace: ""}, config.Defaults(), "", WithInitialType(nodes))
	m.width, m.height = 160, 30
	m.layout()
	m.objects = []model.ResourceObject{
		mk("ip-10-32-74-107", "v1.30.2", "10.32.74.107"),
		mk("ip-10-32-92-218", "v1.31.0", "10.32.92.218"),
	}
	m.applyRows()
	return m
}

// TestFilterMatchesAnyVisibleColumn: the owner's case — filtering nodes by
// their VERSION column keeps only the matching ones.
func TestFilterMatchesAnyVisibleColumn(t *testing.T) {
	m := nodeListModel(t)
	if len(m.rowObjs) != 2 {
		t.Fatalf("precondition: 2 nodes, got %d", len(m.rowObjs))
	}

	// VERSION is a visible column for nodes: filtering on it works.
	m.filter.SetValue("1.31")
	m.applyRows()
	if len(m.rowObjs) != 1 || m.rowObjs[0].Name != "ip-10-32-92-218" {
		t.Fatalf("filtering by kubelet version failed: %+v", m.rowObjs)
	}

	// INTERNAL-IP too.
	m.filter.SetValue("10.32.74")
	m.applyRows()
	if len(m.rowObjs) != 1 || m.rowObjs[0].Name != "ip-10-32-74-107" {
		t.Fatalf("filtering by IP failed: %+v", m.rowObjs)
	}

	// Name filtering still works (no regression).
	m.filter.SetValue("92-218")
	m.applyRows()
	if len(m.rowObjs) != 1 || m.rowObjs[0].Name != "ip-10-32-92-218" {
		t.Fatalf("filtering by name broke: %+v", m.rowObjs)
	}

	// Space = AND across columns: version AND name fragment.
	m.filter.SetValue("1.30 74-107")
	m.applyRows()
	if len(m.rowObjs) != 1 || m.rowObjs[0].Name != "ip-10-32-74-107" {
		t.Fatalf("AND across columns failed: %+v", m.rowObjs)
	}
	m.filter.SetValue("1.30 92-218") // contradictory: no row has both
	m.applyRows()
	if len(m.rowObjs) != 0 {
		t.Fatalf("contradictory terms must match nothing, got %+v", m.rowObjs)
	}

	// A term matching nothing empties the list (the header chip keeps the
	// filter visible, so it can never look like a broken view).
	m.filter.SetValue("nonexistent")
	m.applyRows()
	if len(m.rowObjs) != 0 {
		t.Fatalf("unmatched filter must empty the list, got %+v", m.rowObjs)
	}
}

// TestFilterMatchesIdentityEvenWhenColumnHidden: namespace/name stay
// matchable even if their columns are turned off in the 'C' chooser.
func TestFilterMatchesIdentityEvenWhenColumnHidden(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	m := New(&kube.Client{Namespace: ""}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 140, 30
	m.layout()
	// Only READY is visible — no NAMESPACE, no NAME column.
	m.cfg.ViewPrefs = map[string]config.ViewPref{
		pods.Key(): {Columns: []string{"READY"},
			Hidden: []string{"NAMESPACE", "NAME", "STATUS", "RESTARTS", "AGE", "IP", "NODE"}},
	}
	m.objects = []model.ResourceObject{
		{Type: pods, Namespace: "audience-back", Name: "web-1",
			Raw: map[string]any{"metadata": map[string]any{"name": "web-1", "namespace": "audience-back"}}},
		{Type: pods, Namespace: "other", Name: "db-1",
			Raw: map[string]any{"metadata": map[string]any{"name": "db-1", "namespace": "other"}}},
	}
	m.applyRows()
	m.filter.SetValue("audience-back")
	m.applyRows()
	if len(m.rowObjs) != 1 || m.rowObjs[0].Name != "web-1" {
		t.Fatalf("identity must stay filterable with the columns hidden: %+v", m.rowObjs)
	}
}

// TestFilterScaleGuard: filtering now renders every row's cells before
// deciding, so this path must stay cheap at the documented scale (5,000 pods)
// — it runs on every keystroke and every refresh tick.
func TestFilterScaleGuard(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	m := New(&kube.Client{Namespace: ""}, config.Defaults(), "", WithInitialType(pods))
	m.width, m.height = 160, 40
	m.layout()
	for i := range 5000 {
		name := fmt.Sprintf("web-%d", i)
		m.objects = append(m.objects, model.ResourceObject{Type: pods, Namespace: "demo", Name: name,
			Raw: map[string]any{
				"metadata": map[string]any{"name": name, "namespace": "demo"},
				"spec":     map[string]any{"nodeName": "ip-10-32-74-107.eu-central-1.compute.internal"},
				"status": map[string]any{"phase": "Running", "podIP": "10.32.1.5",
					"containerStatuses": []any{map[string]any{"name": "app", "ready": true, "restartCount": int64(0)}}},
			}})
	}
	start := time.Now()
	m.filter.SetValue("running 10.32")
	for range 5 {
		m.applyRows()
	}
	// Measured ~7ms per pass locally; 1s for 5 passes leaves room for a slow
	// CI runner while still catching an accidental quadratic.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("5 filtered applyRows over 5000 pods took %v — too slow for a keystroke path", elapsed)
	}
}

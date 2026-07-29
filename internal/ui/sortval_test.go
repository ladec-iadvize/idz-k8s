package ui

// Numeric-aware sorting (owner bug 2026-07-29: AVAILABLE looked random —
// numeric cells compared as strings, "10" < "2").

import (
	"testing"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestSortValue(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"3", 3, true},
		{"10", 10, true},
		{"1.5", 1.5, true},
		{"126m", 0.126, true},
		{"706Mi", 706 * (1 << 20), true},
		{"1.5Gi", 1.5 * (1 << 30), true},
		{"10G", 10e9, true},
		{"31%", 31, true},
		{"1/2", 0.5, true},
		{"0/0", 0, true},
		{"-", 0, false},
		{"—", 0, false},
		{"", 0, false},
		{"Running", 0, false},
		{"nginx:latest", 0, false},
	}
	for _, c := range cases {
		got, ok := sortValue(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("sortValue(%q) = %v,%v want %v,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestCellLess(t *testing.T) {
	if !cellLess("2", "10") {
		t.Fatal(`"2" must sort before "10" (numeric, not lexicographic)`)
	}
	if !cellLess("706Mi", "1.5Gi") {
		t.Fatal("quantities must compare by value")
	}
	if !cellLess("5", "-") {
		t.Fatal("value-less cells must sort last ascending")
	}
	if !cellLess("alpha", "beta") || cellLess("beta", "alpha") {
		t.Fatal("textual cells keep the string order")
	}
}

// TestSortByAvailableIsNumeric: the owner's exact case — Deployments sorted
// by AVAILABLE must order 1 < 2 < 10, not "1" < "10" < "2".
func TestSortByAvailableIsNumeric(t *testing.T) {
	dep := model.ResourceType{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(dep))
	m.width, m.height = 160, 30
	m.layout()
	m.filter = textinput.New()
	mk := func(name string, avail int64) model.ResourceObject {
		return model.ResourceObject{Type: dep, Namespace: "demo", Name: name,
			Raw: map[string]any{
				"metadata": map[string]any{"name": name, "namespace": "demo"},
				"spec":     map[string]any{"replicas": avail},
				"status":   map[string]any{"availableReplicas": avail, "updatedReplicas": avail},
			}}
	}
	m.objects = []model.ResourceObject{mk("two", 2), mk("ten", 10), mk("one", 1)}

	// Find the AVAILABLE visual column (mark col is 0 → +1).
	cols := m.columnsForType()
	for i, c := range cols {
		if c.title == "AVAILABLE" {
			m.sortCol = i + 1
		}
	}
	if m.sortCol < 1 {
		t.Fatal("AVAILABLE column not found")
	}
	m.sortAsc = true
	m.applyRows()
	var names []string
	for _, o := range m.rowObjs {
		names = append(names, o.Name)
	}
	if names[0] != "one" || names[1] != "two" || names[2] != "ten" {
		t.Fatalf("AVAILABLE asc must order 1,2,10 — got %v", names)
	}
	m.sortAsc = false
	m.applyRows()
	if m.rowObjs[0].Name != "ten" {
		t.Fatalf("AVAILABLE desc must put 10 first — got %v", m.rowObjs[0].Name)
	}
}

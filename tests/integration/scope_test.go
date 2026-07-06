package integration

import (
	"context"
	"testing"

	"github.com/iadvize/idz-k8s/internal/kube"
)

// TestNamespacePatternScope: a glob scope like "staging-*" lists across all
// namespaces and keeps only the matching ones (US 2026-07-06).
func TestNamespacePatternScope(t *testing.T) {
	client, _ := NewFakeClient("",
		NewPod("staging-front", "web-1", "Running"),
		NewPod("staging-back", "api-1", "Running"),
		NewPod("prod-front", "web-1", "Running"),
	)

	objs, err := client.List(context.Background(), PodsType, "staging-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 staging pods, got %d: %+v", len(objs), objs)
	}
	for _, o := range objs {
		if !kube.MatchNamespace("staging-*", o.Namespace) {
			t.Errorf("pod %s/%s leaked outside the pattern", o.Namespace, o.Name)
		}
	}

	// Diagnostics and Events honor the same scope (no server-side error from
	// sending a glob as a namespace name).
	if _, err := client.Diagnostics(context.Background(), "staging-*"); err != nil {
		t.Fatalf("Diagnostics with pattern: %v", err)
	}
	if _, err := client.Events(context.Background(), "staging-*"); err != nil {
		t.Fatalf("Events with pattern: %v", err)
	}
}

func TestNamespacePatternHelpers(t *testing.T) {
	if kube.IsNamespacePattern("team-a") {
		t.Error("plain name flagged as pattern")
	}
	for _, p := range []string{"staging-*", "prod-?", "team-[ab]"} {
		if !kube.IsNamespacePattern(p) {
			t.Errorf("%q not detected as pattern", p)
		}
	}
	cases := []struct {
		pattern, ns string
		want        bool
	}{
		{"staging-*", "staging-front", true},
		{"staging-*", "staging", false},
		{"staging-*", "prod", false},
		{"*-back", "team-back", true},
		{"team-[", "team-a", false}, // malformed pattern matches nothing
	}
	for _, c := range cases {
		if got := kube.MatchNamespace(c.pattern, c.ns); got != c.want {
			t.Errorf("MatchNamespace(%q,%q)=%v want %v", c.pattern, c.ns, got, c.want)
		}
	}
}

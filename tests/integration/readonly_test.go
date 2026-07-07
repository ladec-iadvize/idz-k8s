package integration

import (
	"context"
	"testing"
	"time"

	"github.com/iadvize/idz-k8s/internal/kube"
)

// TestInspectionIssuesOnlyReadVerbs runs the inspection flows against fake
// clients and asserts that no mutating verb was ever issued (SC-006, FR-012).
func TestInspectionIssuesOnlyReadVerbs(t *testing.T) {
	client, dyn := NewFakeClient("demo",
		NewPod("demo", "web-1", "Running"),
		NewSecret("demo", "creds"),
	)

	// Exercise the read paths a user hits during inspection.
	if _, err := client.List(context.Background(), PodsType, "demo"); err != nil {
		t.Fatal(err)
	}
	secretType := PodsType
	secretType.Kind, secretType.Resource = "Secret", "secrets"
	if _, err := client.List(context.Background(), secretType, "demo"); err != nil {
		t.Fatal(err)
	}

	verbs := VerbsFromActions(dyn.Actions())
	if len(verbs) == 0 {
		t.Fatal("expected some recorded actions")
	}
	if err := kube.AssertReadOnly(verbs); err != nil {
		t.Fatalf("read-only invariant broken: %v (verbs=%v)", err, verbs)
	}
}

func TestIsMutatingVerb(t *testing.T) {
	read := []string{"get", "list", "watch", ""}
	for _, v := range read {
		if kube.IsMutatingVerb(v) {
			t.Errorf("%q should be read-only", v)
		}
	}
	mutating := []string{"create", "update", "patch", "delete", "deletecollection", "eviction"}
	for _, v := range mutating {
		if !kube.IsMutatingVerb(v) {
			t.Errorf("%q should be flagged mutating", v)
		}
	}
}

func TestAssertReadOnlyDetectsMutation(t *testing.T) {
	if err := kube.AssertReadOnly([]string{"list", "get"}); err != nil {
		t.Fatalf("read-only verbs should pass, got %v", err)
	}
	if err := kube.AssertReadOnly([]string{"list", "create"}); err == nil {
		t.Fatal("a mutating verb must be detected")
	}
}

// TestAllReadFlowsIssueOnlyReadVerbs sweeps every kube read path the UI uses
// and asserts the zero-mutation invariant across the WHOLE surface (T064).
func TestAllReadFlowsIssueOnlyReadVerbs(t *testing.T) {
	client, dyn := NewFakeClient("demo",
		NewNode("n1", true),
		NewPodOnNode("demo", "web-1", "n1", "Running"),
		NewSecret("demo", "creds"),
		NewNamespace("demo"),
		endpoints("demo", "web", 1, 0),
		event("demo", "e1", "Warning", "BackOff", "web-1", "2026-07-05T10:00:00Z"),
	)
	ctx := context.Background()

	if _, err := client.List(ctx, PodsType, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListSelected(ctx, PodsType, "demo", "app=web"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Namespaces(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Events(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Topology(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Diagnostics(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.EndpointsSummary(ctx, "demo", "web"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.EndpointsByService(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PodsOnNode(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Posture(ctx, "demo", time.Now()); err != nil {
		t.Fatal(err)
	}

	verbs := VerbsFromActions(dyn.Actions())
	if len(verbs) == 0 {
		t.Fatal("expected recorded actions")
	}
	if err := kube.AssertReadOnly(verbs); err != nil {
		t.Fatalf("zero-mutation invariant broken: %v (verbs=%v)", err, verbs)
	}
}

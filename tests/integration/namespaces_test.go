package integration

import (
	"context"
	"testing"
)

func TestNamespacesListsClusterNamespaces(t *testing.T) {
	client, _ := NewFakeClient("demo",
		NewNamespace("demo"),
		NewNamespace("kube-system"),
		NewNamespace("prod"),
		NewPod("demo", "web-1", "Running"),
	)
	nss, err := client.Namespaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nss) != 3 {
		t.Fatalf("expected 3 namespaces, got %d (%v)", len(nss), nss)
	}
	// Sorted by name.
	want := []string{"demo", "kube-system", "prod"}
	for i, n := range want {
		if nss[i] != n {
			t.Fatalf("namespace %d = %q, want %q (list=%v)", i, nss[i], n, nss)
		}
	}
}

func TestListAcrossAllNamespaces(t *testing.T) {
	client, _ := NewFakeClient("demo",
		NewPod("demo", "web-1", "Running"),
		NewPod("prod", "api-0", "Running"),
	)
	// Empty namespace → list across all namespaces (the "all namespaces" option).
	objs, err := client.List(context.Background(), PodsType, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Fatalf("expected 2 pods across all namespaces, got %d", len(objs))
	}
}

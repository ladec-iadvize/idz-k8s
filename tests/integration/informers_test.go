package integration

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/iadvize/idz-k8s/internal/kube"
)

// TestInformerCacheServesAndUpdates (T089): after the first LIST warms the
// watch, lists come from the cache and follow cluster changes — with only
// read verbs on the wire.
func TestInformerCacheServesAndUpdates(t *testing.T) {
	client, dyn := NewFakeClient("demo",
		NewPod("demo", "web-1", "Running"),
		NewPod("other", "outside", "Running"),
	)
	defer client.Close()
	ctx := context.Background()

	// First call may fall back to a direct LIST while the watch syncs.
	if _, err := client.List(ctx, PodsType, "demo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cache sync", func() bool { return client.UsingCache(PodsType) })

	objs, err := client.List(ctx, PodsType, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Name != "web-1" {
		t.Fatalf("cached namespace list wrong: %+v", objs)
	}

	// A pod created after sync must appear via the WATCH (no new LIST).
	if _, err := dyn.Resource(podsGVR).Namespace("demo").Create(ctx, NewPod("demo", "web-2", "Running"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "watch delivery", func() bool {
		objs, err := client.List(ctx, PodsType, "demo")
		return err == nil && len(objs) == 2
	})

	// Read-only invariant: the informer path uses list+watch only (the
	// create above is the test's own seeding, not the client under test).
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "create" {
			continue // test seeding
		}
		if err := kube.AssertReadOnly([]string{a.GetVerb()}); err != nil {
			t.Fatal(err)
		}
	}

	// Selector and pattern scopes work against the cache too.
	labeled := NewPod("demo", "labeled", "Running")
	meta := labeled.Object["metadata"].(map[string]interface{})
	meta["labels"] = map[string]interface{}{"app": "web"}
	if _, err := dyn.Resource(podsGVR).Namespace("demo").Create(ctx, labeled, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "selector via cache", func() bool {
		objs, err := client.ListSelected(ctx, PodsType, "demo", "app=web")
		return err == nil && len(objs) == 1 && objs[0].Name == "labeled"
	})
	all, err := client.List(ctx, PodsType, "")
	if err != nil || len(all) != 4 {
		t.Fatalf("all-namespaces via cache: %v %d", err, len(all))
	}
	// Stable ordering (informer stores are unordered).
	if all[0].Namespace > all[1].Namespace {
		t.Fatalf("cache list must be sorted: %+v", all)
	}
}

// TestInformerCloseIsSafe: Close is idempotent-ish and safe on a client that
// never started informers.
func TestInformerCloseIsSafe(t *testing.T) {
	client, _ := NewFakeClient("demo")
	client.Close() // nothing started: must not panic
	if _, err := client.List(context.Background(), PodsType, "demo"); err != nil {
		t.Fatal(err)
	}
	client.Close()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestChangesSignalOnWatchEvents: cluster changes surface on the coalesced
// Changes channel (the UI's live-refresh source), and Close ends the stream.
func TestChangesSignalOnWatchEvents(t *testing.T) {
	client, dyn := NewFakeClient("demo", NewPod("demo", "web-1", "Running"))
	ch := client.Changes()
	ctx := context.Background()

	// Warm the informer, drain the initial sync signals.
	if _, err := client.List(ctx, PodsType, "demo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cache sync", func() bool { return client.UsingCache(PodsType) })
	for drained := false; !drained; {
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			drained = true
		}
	}

	// A cluster change must produce a signal.
	if _, err := dyn.Resource(podsGVR).Namespace("demo").Create(ctx, NewPod("demo", "web-2", "Running"), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatal("channel closed unexpectedly")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no change signal after a pod creation")
	}

	// Close ends the stream (waiters re-arm on the replacement client).
	client.Close()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected a closed channel after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed by Close")
	}
}

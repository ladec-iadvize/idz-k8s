package integration

// v3 admin operations (FR-012 v3): every mutation the UI can trigger is
// exercised against the fake clients — the right verb on the right resource,
// nothing more. The UI-side confirmation gate is tested in internal/ui.

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// DeploymentsType is the apps/v1 Deployment type used across admin tests.
var DeploymentsType = model.ResourceType{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true}

// NodesType is the cluster-scoped v1 Node type.
var NodesType = model.ResourceType{Version: "v1", Kind: "Node", Resource: "nodes"}

// CronJobsType is the batch/v1 CronJob type.
var CronJobsType = model.ResourceType{Group: "batch", Version: "v1", Kind: "CronJob", Resource: "cronjobs", Namespaced: true}

// NewDeployment builds an unstructured Deployment with the given replicas.
func NewDeployment(namespace, name string, replicas int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec": map[string]any{
			"replicas": replicas,
			"selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": name}},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "main", "image": "nginx"}}},
			},
		},
	}}
}

// NewCronJob builds an unstructured CronJob (suspend unset → active).
func NewCronJob(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "CronJob",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"spec":       map[string]any{"schedule": "0 * * * *"},
	}}
}

func getRaw(t *testing.T, cl *kube.Client, rt model.ResourceType, ns, name string) map[string]any {
	t.Helper()
	obj, err := cl.GetObject(context.Background(), rt, ns, name)
	if err != nil {
		t.Fatal(err)
	}
	return obj.Raw
}

func TestScaleWorkloadPatchesReplicas(t *testing.T) {
	client, _ := NewFakeClient("demo", NewDeployment("demo", "back", 3))
	defer client.Close()
	if err := client.ScaleWorkload(context.Background(), DeploymentsType, "demo", "back", 5); err != nil {
		t.Fatal(err)
	}
	raw := getRaw(t, client, DeploymentsType, "demo", "back")
	if r, _, _ := unstructured.NestedInt64(raw, "spec", "replicas"); r != 5 {
		t.Fatalf("replicas=%d, want 5", r)
	}
}

func TestDeleteObjectRemovesIt(t *testing.T) {
	client, _ := NewFakeClient("demo", NewPod("demo", "web-1", "Running"), NewPod("demo", "web-2", "Running"))
	defer client.Close()
	if err := client.DeleteObject(context.Background(), PodsType, "demo", "web-1"); err != nil {
		t.Fatal(err)
	}
	objs, err := client.List(context.Background(), PodsType, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Name != "web-2" {
		t.Fatalf("pods after delete: %+v", objs)
	}
}

func TestRolloutRestartStampsTemplateAnnotation(t *testing.T) {
	client, _ := NewFakeClient("demo", NewDeployment("demo", "back", 3))
	defer client.Close()
	at := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	if err := client.RolloutRestart(context.Background(), DeploymentsType, "demo", "back", at); err != nil {
		t.Fatal(err)
	}
	raw := getRaw(t, client, DeploymentsType, "demo", "back")
	got, _, _ := unstructured.NestedString(raw, "spec", "template", "metadata", "annotations", "kubectl.kubernetes.io/restartedAt")
	if got != "2026-07-24T10:00:00Z" {
		t.Fatalf("restartedAt=%q", got)
	}
}

func TestSetCordonTogglesUnschedulable(t *testing.T) {
	client, _ := NewFakeClient("", NewNode("ip-10-0-1-2", true))
	defer client.Close()
	ctx := context.Background()
	if err := client.SetCordon(ctx, NodesType, "ip-10-0-1-2", true); err != nil {
		t.Fatal(err)
	}
	raw := getRaw(t, client, NodesType, "", "ip-10-0-1-2")
	if v, _, _ := unstructured.NestedBool(raw, "spec", "unschedulable"); !v {
		t.Fatal("node not cordoned")
	}
	if err := client.SetCordon(ctx, NodesType, "ip-10-0-1-2", false); err != nil {
		t.Fatal(err)
	}
	raw = getRaw(t, client, NodesType, "", "ip-10-0-1-2")
	if v, _, _ := unstructured.NestedBool(raw, "spec", "unschedulable"); v {
		t.Fatal("node still cordoned")
	}
}

func TestSetSuspendTogglesCronJob(t *testing.T) {
	client, _ := NewFakeClient("demo", NewCronJob("demo", "nightly"))
	defer client.Close()
	if err := client.SetSuspend(context.Background(), CronJobsType, "demo", "nightly", true); err != nil {
		t.Fatal(err)
	}
	raw := getRaw(t, client, CronJobsType, "demo", "nightly")
	if v, _, _ := unstructured.NestedBool(raw, "spec", "suspend"); !v {
		t.Fatal("cronjob not suspended")
	}
}

func TestObjectYAMLAndApplyRoundTrip(t *testing.T) {
	client, _ := NewFakeClient("demo", NewDeployment("demo", "back", 3))
	defer client.Close()
	ctx := context.Background()
	doc, err := client.ObjectYAML(ctx, DeploymentsType, "demo", "back")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, "name: back") || strings.Contains(doc, "managedFields") {
		t.Fatalf("yaml=%q", doc)
	}
	// The edit a user would make in $EDITOR: bump the replica count.
	edited := strings.Replace(doc, "replicas: 3", "replicas: 7", 1)
	if edited == doc {
		t.Fatalf("fixture yaml did not contain replicas: 3:\n%s", doc)
	}
	if err := client.ApplyYAML(ctx, DeploymentsType, []byte(edited)); err != nil {
		t.Fatal(err)
	}
	raw := getRaw(t, client, DeploymentsType, "demo", "back")
	if r, _, _ := unstructured.NestedInt64(raw, "spec", "replicas"); r != 7 {
		t.Fatalf("replicas=%d, want 7", r)
	}
}

func TestApplyYAMLRejectsGarbage(t *testing.T) {
	client, _ := NewFakeClient("demo")
	defer client.Close()
	if err := client.ApplyYAML(context.Background(), DeploymentsType, []byte(":\n  - not yaml")); err == nil {
		t.Fatal("garbage YAML must not be applied")
	}
	if err := client.ApplyYAML(context.Background(), DeploymentsType, []byte("apiVersion: apps/v1\nkind: Deployment\n")); err == nil {
		t.Fatal("a document without metadata.name must not be applied")
	}
}

func TestFirstReadyPodPrefersReadyOnes(t *testing.T) {
	notReady := NewPod("demo", "web-0", "Running")
	notReady.Object["metadata"].(map[string]any)["labels"] = map[string]any{"app": "web"}
	notReady.Object["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"ready": false}}
	ready := NewPod("demo", "web-1", "Running")
	ready.Object["metadata"].(map[string]any)["labels"] = map[string]any{"app": "web"}
	ready.Object["status"].(map[string]any)["containerStatuses"] = []any{map[string]any{"ready": true}}
	other := NewPod("demo", "other", "Running")

	client, _ := NewFakeClient("demo", notReady, ready, other)
	defer client.Close()
	name, err := client.FirstReadyPod(context.Background(), "demo", "app=web")
	if err != nil {
		t.Fatal(err)
	}
	if name != "web-1" {
		t.Fatalf("target pod=%q, want the ready web-1", name)
	}
	if _, err := client.FirstReadyPod(context.Background(), "demo", "app=nothing"); err == nil {
		t.Fatal("an empty selector match must error, never guess")
	}
}

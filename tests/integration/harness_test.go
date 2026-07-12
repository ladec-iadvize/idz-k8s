package integration

import (
	"context"
	"testing"

	"github.com/iadvize/idz-k8s/internal/model"
)

// TestHarnessRegistersEveryAppKind: the fake dynamic client refuses to LIST
// an unregistered kind, so every type the app has dedicated columns for must
// be listable through the harness — otherwise integration tests on workloads
// hit a wall the moment they are written.
func TestHarnessRegistersEveryAppKind(t *testing.T) {
	client, _ := NewFakeClient("demo")
	types := []model.ResourceType{
		{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true},
		{Version: "v1", Kind: "Secret", Resource: "secrets", Namespaced: true},
		{Version: "v1", Kind: "Namespace", Resource: "namespaces"},
		{Version: "v1", Kind: "Node", Resource: "nodes"},
		{Version: "v1", Kind: "Event", Resource: "events", Namespaced: true},
		{Version: "v1", Kind: "Service", Resource: "services", Namespaced: true},
		{Version: "v1", Kind: "ConfigMap", Resource: "configmaps", Namespaced: true},
		{Version: "v1", Kind: "PersistentVolumeClaim", Resource: "persistentvolumeclaims", Namespaced: true},
		{Version: "v1", Kind: "PersistentVolume", Resource: "persistentvolumes"},
		{Group: "apps", Version: "v1", Kind: "Deployment", Resource: "deployments", Namespaced: true},
		{Group: "apps", Version: "v1", Kind: "StatefulSet", Resource: "statefulsets", Namespaced: true},
		{Group: "apps", Version: "v1", Kind: "ReplicaSet", Resource: "replicasets", Namespaced: true},
		{Group: "apps", Version: "v1", Kind: "DaemonSet", Resource: "daemonsets", Namespaced: true},
		{Group: "batch", Version: "v1", Kind: "Job", Resource: "jobs", Namespaced: true},
		{Group: "batch", Version: "v1", Kind: "CronJob", Resource: "cronjobs", Namespaced: true},
		{Group: "autoscaling", Version: "v2", Kind: "HorizontalPodAutoscaler", Resource: "horizontalpodautoscalers", Namespaced: true},
		{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress", Resource: "ingresses", Namespaced: true},
		{Group: "networking.k8s.io", Version: "v1", Kind: "NetworkPolicy", Resource: "networkpolicies", Namespaced: true},
	}
	for _, ty := range types {
		if _, err := client.List(context.Background(), ty, "demo"); err != nil {
			t.Errorf("%s not listable through the harness: %v — register it in NewFakeClient's listKinds", ty.Key(), err)
		}
	}
}

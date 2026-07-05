package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/iadvize/idz-k8s/internal/model"
)

// namespacesGVR is the cluster-scoped core Namespace resource.
var namespacesGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}

// Namespaces lists the cluster's namespaces (read-only "list"), sorted by name.
func (c *Client) Namespaces(ctx context.Context) ([]string, error) {
	if c == nil || c.Dynamic == nil {
		return nil, fmt.Errorf("kubernetes client not initialized")
	}
	ul, err := c.Dynamic.Resource(namespacesGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ul.Items))
	for i := range ul.Items {
		out = append(out, ul.Items[i].GetName())
	}
	sort.Strings(out)
	return out, nil
}

// List returns the objects of a resource type in a namespace (read-only "list").
// For cluster-scoped types, namespace is ignored.
func (c *Client) List(ctx context.Context, t model.ResourceType, namespace string) ([]model.ResourceObject, error) {
	return c.ListSelected(ctx, t, namespace, "")
}

// ListSelected is List restricted to a label selector (e.g. the pods managed
// by a Deployment). An empty selector lists everything.
func (c *Client) ListSelected(ctx context.Context, t model.ResourceType, namespace, labelSelector string) ([]model.ResourceObject, error) {
	ri := c.Dynamic.Resource(GVR(t))
	lopts := metav1.ListOptions{LabelSelector: labelSelector}
	var (
		ul  *unstructured.UnstructuredList
		err error
	)
	if t.Namespaced && namespace != "" {
		ul, err = ri.Namespace(namespace).List(ctx, lopts)
	} else {
		ul, err = ri.List(ctx, lopts)
	}
	if err != nil {
		return nil, err
	}
	out := make([]model.ResourceObject, 0, len(ul.Items))
	for i := range ul.Items {
		item := ul.Items[i]
		out = append(out, model.ResourceObject{
			Type:      t,
			Namespace: item.GetNamespace(),
			Name:      item.GetName(),
			Status:    deriveStatus(&item),
			CreatedAt: item.GetCreationTimestamp().Time,
			Raw:       item.Object,
		})
	}
	return out, nil
}

// deriveStatus produces a display health from common status shapes. It is a
// read-only best-effort projection; unknown shapes map to HealthUnknown.
func deriveStatus(u *unstructured.Unstructured) model.StatusSummary {
	phase, _, _ := unstructured.NestedString(u.Object, "status", "phase")
	switch phase {
	case "Running", "Active", "Bound", "Succeeded":
		return model.StatusSummary{Level: model.HealthOk, Reason: phase}
	case "Pending":
		return model.StatusSummary{Level: model.HealthWarning, Reason: phase}
	case "Failed", "Unknown":
		return model.StatusSummary{Level: model.HealthError, Reason: phase}
	}
	// Fall back to Ready-type conditions when no phase is present.
	conds, found, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	if found {
		for _, c := range conds {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			ctype, _ := cm["type"].(string)
			cstatus, _ := cm["status"].(string)
			if strings.EqualFold(ctype, "Ready") || strings.EqualFold(ctype, "Available") {
				if cstatus == "True" {
					return model.StatusSummary{Level: model.HealthOk, Reason: ctype}
				}
				reason, _ := cm["reason"].(string)
				return model.StatusSummary{Level: model.HealthWarning, Reason: reason}
			}
		}
	}
	return model.StatusSummary{Level: model.HealthUnknown}
}

// Age formats a duration since creation compactly (e.g. 3d, 5h, 2m).
func Age(created time.Time, now time.Time) string {
	if created.IsZero() {
		return "<unknown>"
	}
	d := now.Sub(created)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h"
	default:
		return itoa(int(d.Hours()/24)) + "d"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// PodResources sums the CPU (cores) and memory (bytes) requests and limits
// across a pod's containers, read from its unstructured spec. Missing values
// count as 0. Used to scale usage gauges against request/limit (FR-019).
func PodResources(raw map[string]interface{}) (cpuReq, cpuLim, memReq, memLim float64) {
	spec, _ := raw["spec"].(map[string]interface{})
	containers, _ := spec["containers"].([]interface{})
	for _, c := range containers {
		cm, _ := c.(map[string]interface{})
		res, _ := cm["resources"].(map[string]interface{})
		req, _ := res["requests"].(map[string]interface{})
		lim, _ := res["limits"].(map[string]interface{})
		cpuReq += quantity(req, "cpu", true)
		cpuLim += quantity(lim, "cpu", true)
		memReq += quantity(req, "memory", false)
		memLim += quantity(lim, "memory", false)
	}
	return
}

func quantity(m map[string]interface{}, key string, cpu bool) float64 {
	s, _ := m[key].(string)
	return parseQuantity(s, cpu)
}

// parseQuantity parses a Kubernetes quantity string into cores (cpu=true) or
// bytes (cpu=false). Empty or invalid input yields 0.
func parseQuantity(s string, cpu bool) float64 {
	if s == "" {
		return 0
	}
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0
	}
	if cpu {
		return float64(q.MilliValue()) / 1000.0
	}
	return float64(q.Value())
}

// PodSelector extracts the label selector a workload uses to own its pods:
// spec.selector.matchLabels for controllers (Deployment, ReplicaSet,
// StatefulSet, DaemonSet, Job) or spec.selector for Services. Returns the
// selector in "k=v,k=v" form. ok is false when the object has none.
func PodSelector(raw map[string]interface{}) (string, bool) {
	// Controllers: spec.selector.matchLabels
	if ml, found, _ := unstructured.NestedStringMap(raw, "spec", "selector", "matchLabels"); found && len(ml) > 0 {
		return joinSelector(ml), true
	}
	// Services: spec.selector is a plain map
	if sel, found, _ := unstructured.NestedStringMap(raw, "spec", "selector"); found && len(sel) > 0 {
		return joinSelector(sel), true
	}
	return "", false
}

func joinSelector(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}

// GetObjectStatus fetches one object read-only and derives its display status.
// found=false when the object does not exist (e.g. a chart resource that was
// deleted — useful to spot drift in the Helm release view).
func (c *Client) GetObjectStatus(ctx context.Context, t model.ResourceType, namespace, name string) (model.StatusSummary, bool, error) {
	ri := c.Dynamic.Resource(GVR(t))
	var (
		u   *unstructured.Unstructured
		err error
	)
	if t.Namespaced && namespace != "" {
		u, err = ri.Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		u, err = ri.Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		if apierrors.IsNotFound(err) {
			return model.StatusSummary{}, false, nil
		}
		return model.StatusSummary{}, false, err
	}
	return deriveStatus(u), true, nil
}

// ReadyCount extracts "ready/desired" for workload kinds:
// Deployment/StatefulSet/ReplicaSet → status.readyReplicas / spec.replicas,
// DaemonSet → status.numberReady / status.desiredNumberScheduled,
// Pod → ready containers / total containers.
// ok=false for kinds without a ready notion (Service, ConfigMap, …).
func ReadyCount(kind string, raw map[string]interface{}) (ready, desired int, ok bool) {
	switch kind {
	case "Deployment", "StatefulSet", "ReplicaSet":
		desired = 1 // Kubernetes default when spec.replicas is unset
		if d, found, _ := unstructured.NestedInt64(raw, "spec", "replicas"); found {
			desired = int(d)
		}
		r, _, _ := unstructured.NestedInt64(raw, "status", "readyReplicas")
		return int(r), desired, true
	case "DaemonSet":
		d, _, _ := unstructured.NestedInt64(raw, "status", "desiredNumberScheduled")
		r, _, _ := unstructured.NestedInt64(raw, "status", "numberReady")
		return int(r), int(d), true
	case "Pod":
		statuses, found, _ := unstructured.NestedSlice(raw, "status", "containerStatuses")
		if !found {
			return 0, 0, false
		}
		for _, cs := range statuses {
			cm, isMap := cs.(map[string]interface{})
			if !isMap {
				continue
			}
			desired++
			if r, _ := cm["ready"].(bool); r {
				ready++
			}
		}
		return ready, desired, true
	}
	return 0, 0, false
}

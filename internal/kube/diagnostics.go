package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/iadvize/idz-k8s/internal/model"
)

var podsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

// Diagnostics lists workload-failure findings in a namespace (read-only, US10):
// evicted pods, and per-container CrashLoopBackOff, OOMKilled, non-zero exits,
// and restart counts. Only "interesting" (non-healthy) items are returned.
func (c *Client) Diagnostics(ctx context.Context, namespace string) ([]model.Diagnostic, error) {
	apiNS, pattern := namespaceScope(namespace)
	ul, err := c.listGVR(ctx, podsGVR, apiNS, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var out []model.Diagnostic
	for i := range ul.Items {
		if pattern != "" && !MatchNamespace(pattern, ul.Items[i].GetNamespace()) {
			continue
		}
		out = append(out, diagnosePod(&ul.Items[i])...)
	}
	return out, nil
}

// diagnosePod extracts findings from a single pod's status.
func diagnosePod(u *unstructured.Unstructured) []model.Diagnostic {
	ns := u.GetNamespace()
	name := u.GetName()
	var out []model.Diagnostic

	// Evicted pods surface as phase=Failed, reason=Evicted.
	if reason, _, _ := unstructured.NestedString(u.Object, "status", "reason"); reason == "Evicted" {
		msg, _, _ := unstructured.NestedString(u.Object, "status", "message")
		out = append(out, model.Diagnostic{Namespace: ns, Pod: name, Reason: "Evicted: " + msg, Level: model.HealthError})
	}

	// Pending pods stuck unschedulable (US11/FR-028): surface the scheduler's
	// own explanation (e.g. "0/3 nodes are available: insufficient cpu").
	if phase, _, _ := unstructured.NestedString(u.Object, "status", "phase"); phase == "Pending" {
		if reason, msg, stuck := unschedulableReason(u); stuck {
			label := "Unschedulable"
			if reason != "" && reason != "Unschedulable" {
				label = reason
			}
			if msg != "" {
				label += ": " + msg
			}
			out = append(out, model.Diagnostic{Namespace: ns, Pod: name, Reason: label, Level: model.HealthError})
		}
	}

	statuses, _, _ := unstructured.NestedSlice(u.Object, "status", "containerStatuses")
	for _, cs := range statuses {
		m, ok := cs.(map[string]interface{})
		if !ok {
			continue
		}
		cname, _ := m["name"].(string)
		restarts := nestedInt(m, "restartCount")

		// Current waiting reason (e.g. CrashLoopBackOff, ImagePullBackOff).
		if waitReason, ok := nestedStr(m, "state", "waiting", "reason"); ok && isBadWaiting(waitReason) {
			out = append(out, model.Diagnostic{Namespace: ns, Pod: name, Container: cname, Restarts: restarts, Reason: waitReason, Level: model.HealthError})
			continue
		}

		// Last termination reason (OOMKilled, non-zero exit).
		if termReason, ok := nestedStr(m, "lastState", "terminated", "reason"); ok {
			exit := nestedInt(m, "lastState", "terminated", "exitCode")
			switch {
			case termReason == "OOMKilled":
				out = append(out, model.Diagnostic{Namespace: ns, Pod: name, Container: cname, Restarts: restarts,
					Reason: fmt.Sprintf("OOMKilled (x%d restarts)", restarts), Level: model.HealthError})
				continue
			case exit != 0:
				out = append(out, model.Diagnostic{Namespace: ns, Pod: name, Container: cname, Restarts: restarts,
					Reason: fmt.Sprintf("%s (exit %d, x%d)", termReason, exit, restarts), Level: model.HealthWarning})
				continue
			}
		}

		if restarts > 0 {
			out = append(out, model.Diagnostic{Namespace: ns, Pod: name, Container: cname, Restarts: restarts,
				Reason: fmt.Sprintf("restarted x%d", restarts), Level: model.HealthWarning})
		}
	}
	return out
}

// unschedulableReason reports whether a Pending pod is stuck because the
// scheduler cannot place it: the PodScheduled condition is False. Returns the
// condition's reason and message (the scheduler's explanation). A Pending pod
// that IS scheduled (containers still starting) is not stuck.
func unschedulableReason(u *unstructured.Unstructured) (reason, message string, stuck bool) {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		ctype, _ := cm["type"].(string)
		cstatus, _ := cm["status"].(string)
		if ctype == "PodScheduled" && cstatus == "False" {
			r, _ := cm["reason"].(string)
			msg, _ := cm["message"].(string)
			return r, msg, true
		}
	}
	return "", "", false
}

func isBadWaiting(reason string) bool {
	switch reason {
	case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "CreateContainerError":
		return true
	}
	return false
}

func nestedInt(m map[string]interface{}, fields ...string) int {
	v, found, err := unstructured.NestedInt64(m, fields...)
	if !found || err != nil {
		return 0
	}
	return int(v)
}

func nestedStr(m map[string]interface{}, fields ...string) (string, bool) {
	v, found, err := unstructured.NestedString(m, fields...)
	if !found || err != nil || v == "" {
		return "", false
	}
	return v, true
}

package kube

// Container projection behind the containers view (Enter on a pod — the last
// step of the drill chain, owner request 2026-07-31) and the owner-UID helper
// behind owner-based drills (CronJob → Jobs).

import (
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/model"
)

// PodContainers lists a pod's init containers then its regular ones, each
// merged with its status (ready, state, restarts). Containers without a
// status yet (pod still being scheduled) show an explicit unknown state —
// never a fabricated "Running" (FR-021).
func PodContainers(raw map[string]interface{}) []model.Container {
	var out []model.Container
	add := func(field, statusField string, init bool) {
		specs, _, _ := unstructured.NestedSlice(raw, "spec", field)
		statuses, _, _ := unstructured.NestedSlice(raw, "status", statusField)
		byName := map[string]map[string]interface{}{}
		for _, s := range statuses {
			if sm, ok := s.(map[string]interface{}); ok {
				if n, _ := sm["name"].(string); n != "" {
					byName[n] = sm
				}
			}
		}
		for _, c := range specs {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := cm["name"].(string)
			image, _ := cm["image"].(string)
			ct := model.Container{Name: name, Image: image, Init: init,
				State: "unknown", Level: model.HealthUnknown}
			if st, found := byName[name]; found {
				ct.Ready, _ = st["ready"].(bool)
				if r, ok, _ := unstructured.NestedInt64(st, "restartCount"); ok {
					ct.Restarts = int(r)
				}
				ct.State, ct.Level = containerState(st, ct.Ready, init)
				ct.LastTerminated, ct.LastTerminatedAt = lastTermination(st)
			}
			out = append(out, ct)
		}
	}
	add("initContainers", "initContainerStatuses", true)
	add("containers", "containerStatuses", false)
	return out
}

// containerState renders the container's state and its health level.
func containerState(status map[string]interface{}, ready, init bool) (string, model.HealthLevel) {
	state, _, _ := unstructured.NestedMap(status, "state")
	for _, phase := range []string{"running", "waiting", "terminated"} {
		sub, found, _ := unstructured.NestedMap(state, phase)
		if !found {
			continue
		}
		reason, _ := sub["reason"].(string)
		switch phase {
		case "running":
			if ready {
				return "Running", model.HealthOk
			}
			return "Running (not ready)", model.HealthWarning
		case "waiting":
			label := "Waiting"
			if reason != "" {
				label = "Waiting: " + reason
			}
			// CrashLoopBackOff / ImagePullBackOff / ErrImagePull are failures;
			// a plain ContainerCreating is just a transition.
			if reason == "ContainerCreating" || reason == "PodInitializing" || reason == "" {
				return label, model.HealthWarning
			}
			return label, model.HealthError
		default:
			code, _, _ := unstructured.NestedInt64(sub, "exitCode")
			label := "Terminated"
			if reason != "" {
				label = "Terminated: " + reason
			}
			if code != 0 {
				label = fmt.Sprintf("%s (exit %d)", label, code)
				return label, model.HealthError
			}
			// An init container that completed successfully is the norm.
			if init {
				return label, model.HealthOk
			}
			return label, model.HealthWarning
		}
	}
	return "unknown", model.HealthUnknown
}

// lastTermination summarises status.lastState.terminated — why the PREVIOUS
// run of the container ended. "" when it never terminated before.
func lastTermination(status map[string]interface{}) (string, time.Time) {
	term, found, _ := unstructured.NestedMap(status, "lastState", "terminated")
	if !found {
		return "", time.Time{}
	}
	reason, _ := term["reason"].(string)
	code, hasCode, _ := unstructured.NestedInt64(term, "exitCode")
	label := reason
	if label == "" {
		label = "Terminated"
	}
	if hasCode {
		label = fmt.Sprintf("%s (exit %d)", label, code)
	}
	at := time.Time{}
	if ts, _, _ := unstructured.NestedString(term, "finishedAt"); ts != "" {
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			at = parsed
		}
	}
	return label, at
}

// PodLastTermination returns the most relevant "why did it stop" of a pod:
// the last termination of any container, worst first (an OOMKill outranks a
// clean Completed). "" when no container ever terminated.
func PodLastTermination(raw map[string]interface{}) string {
	best, bestRank := "", -1
	rank := func(label string) int {
		switch {
		case strings.Contains(label, "OOMKilled"):
			return 3
		case strings.Contains(label, "Error"), strings.Contains(label, "exit 1"):
			return 2
		case strings.Contains(label, "Completed"):
			return 0
		default:
			return 1
		}
	}
	for _, field := range []string{"containerStatuses", "initContainerStatuses"} {
		statuses, _, _ := unstructured.NestedSlice(raw, "status", field)
		for _, s := range statuses {
			sm, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			if label, _ := lastTermination(sm); label != "" {
				if r := rank(label); r > bestRank {
					best, bestRank = label, r
				}
			}
		}
	}
	return best
}

// OwnedByUID reports whether an object's ownerReferences name the given UID —
// the precise parent test behind owner-based drills (a CronJob's Jobs), safer
// than matching names.
func OwnedByUID(raw map[string]interface{}, uid string) bool {
	if uid == "" {
		return false
	}
	refs, _, _ := unstructured.NestedSlice(raw, "metadata", "ownerReferences")
	for _, r := range refs {
		if rm, ok := r.(map[string]interface{}); ok {
			if u, _ := rm["uid"].(string); u == uid {
				return true
			}
		}
	}
	return false
}

// ObjectUID returns an object's metadata.uid ("" when absent).
func ObjectUID(raw map[string]interface{}) string {
	uid, _, _ := unstructured.NestedString(raw, "metadata", "uid")
	return uid
}

// IngressServiceNames lists the Service names an Ingress routes to (default
// backend included) — the child set of an Ingress drill.
func IngressServiceNames(raw map[string]interface{}) []string {
	seen := map[string]bool{}
	var out []string
	addName := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if n, _, _ := unstructured.NestedString(raw, "spec", "defaultBackend", "service", "name"); n != "" {
		addName(n)
	}
	rules, _, _ := unstructured.NestedSlice(raw, "spec", "rules")
	for _, r := range rules {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		paths, _, _ := unstructured.NestedSlice(rm, "http", "paths")
		for _, p := range paths {
			pm, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			n, _, _ := unstructured.NestedString(pm, "backend", "service", "name")
			addName(n)
		}
	}
	return out
}

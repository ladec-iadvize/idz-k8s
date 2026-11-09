package kube

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// RolloutProgress is where a workload's rollout stands, read from its
// status the way `kubectl rollout status` reads it. Every number comes from
// the API object; nothing is interpolated.
type RolloutProgress struct {
	Desired int
	Updated int // replicas already running the current template
	Ready   int // replicas ready (available, for Deployments/DaemonSets)
	Done    bool
	// Observed: the controller has seen the latest spec. Before that the
	// status still describes the previous rollout and is not progress.
	Observed bool
	// Failed carries the controller's own reason when the rollout gave up
	// (Deployment ProgressDeadlineExceeded) — empty otherwise.
	Failed string
}

// Fraction is the share of the rollout that is both updated and ready, in
// [0,1]; 1 once Done.
func (p RolloutProgress) Fraction() float64 {
	if p.Done {
		return 1
	}
	if !p.Observed || p.Desired <= 0 {
		return 0
	}
	n := min(p.Updated, p.Ready)
	return min(float64(n)/float64(p.Desired), 1)
}

// RolloutStatus evaluates a workload's rollout from its raw object.
// ok=false for kinds (or update strategies) that have no trackable rollout.
func RolloutStatus(kind string, raw map[string]interface{}) (p RolloutProgress, ok bool) {
	i := func(fields ...string) int {
		v, _, _ := unstructured.NestedInt64(raw, fields...)
		return int(v)
	}
	gen, observed := i("metadata", "generation"), i("status", "observedGeneration")
	desiredReplicas := func() int {
		if v, found, _ := unstructured.NestedInt64(raw, "spec", "replicas"); found {
			return int(v)
		}
		return 1 // Kubernetes default when spec.replicas is unset
	}
	switch kind {
	case "Deployment":
		p = RolloutProgress{Desired: desiredReplicas(), Updated: i("status", "updatedReplicas"), Ready: i("status", "availableReplicas")}
		if observed < gen {
			return p, true
		}
		p.Observed = true
		if cond := deploymentCondition(raw, "Progressing"); cond["reason"] == "ProgressDeadlineExceeded" {
			p.Failed = "ProgressDeadlineExceeded"
			return p, true
		}
		total := i("status", "replicas")
		p.Done = p.Updated >= p.Desired && total <= p.Updated && p.Ready >= p.Updated
		return p, true
	case "StatefulSet":
		if s, _, _ := unstructured.NestedString(raw, "spec", "updateStrategy", "type"); s == "OnDelete" {
			return RolloutProgress{}, false // pods only change when deleted by hand
		}
		p = RolloutProgress{Desired: desiredReplicas(), Updated: i("status", "updatedReplicas"), Ready: i("status", "readyReplicas")}
		if observed < gen {
			return p, true
		}
		p.Observed = true
		cur, _, _ := unstructured.NestedString(raw, "status", "currentRevision")
		upd, _, _ := unstructured.NestedString(raw, "status", "updateRevision")
		if part, found, _ := unstructured.NestedInt64(raw, "spec", "updateStrategy", "rollingUpdate", "partition"); found && part > 0 {
			// A partitioned rollout only updates ordinals >= partition.
			want := max(p.Desired-int(part), 0)
			p.Done = p.Ready >= p.Desired && p.Updated >= want
			return p, true
		}
		p.Done = p.Ready >= p.Desired && p.Updated >= p.Desired && (cur == "" || cur == upd)
		return p, true
	case "DaemonSet":
		if s, _, _ := unstructured.NestedString(raw, "spec", "updateStrategy", "type"); s == "OnDelete" {
			return RolloutProgress{}, false
		}
		p = RolloutProgress{Desired: i("status", "desiredNumberScheduled"),
			Updated: i("status", "updatedNumberScheduled"), Ready: i("status", "numberAvailable")}
		if observed < gen {
			return p, true
		}
		p.Observed = true
		p.Done = p.Updated >= p.Desired && p.Ready >= p.Desired
		return p, true
	case "ReplicaSet":
		// No template rollout: a ReplicaSet only converges on its count.
		p = RolloutProgress{Desired: desiredReplicas(), Ready: i("status", "readyReplicas")}
		p.Updated = i("status", "replicas")
		if observed < gen {
			return p, true
		}
		p.Observed = true
		p.Done = p.Updated == p.Desired && p.Ready >= p.Desired
		return p, true
	}
	return RolloutProgress{}, false
}

func deploymentCondition(raw map[string]interface{}, typ string) map[string]interface{} {
	conds, _, _ := unstructured.NestedSlice(raw, "status", "conditions")
	for _, c := range conds {
		if cm, ok := c.(map[string]interface{}); ok && cm["type"] == typ {
			return cm
		}
	}
	return nil
}

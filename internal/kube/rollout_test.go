package kube

import "testing"

func TestRolloutStatus(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		raw      map[string]interface{}
		ok, done bool
		failed   string
		frac     float64
	}{
		{
			name: "deployment not yet observed by the controller",
			kind: "Deployment",
			raw: map[string]interface{}{
				"metadata": map[string]interface{}{"generation": int64(4)},
				"spec":     map[string]interface{}{"replicas": int64(3)},
				"status": map[string]interface{}{"observedGeneration": int64(3),
					"replicas": int64(3), "updatedReplicas": int64(3), "availableReplicas": int64(3)},
			},
			ok: true, frac: 0,
		},
		{
			name: "deployment mid-rollout: old replicas still running",
			kind: "Deployment",
			raw: map[string]interface{}{
				"metadata": map[string]interface{}{"generation": int64(4)},
				"spec":     map[string]interface{}{"replicas": int64(4)},
				"status": map[string]interface{}{"observedGeneration": int64(4),
					"replicas": int64(5), "updatedReplicas": int64(2), "availableReplicas": int64(4)},
			},
			ok: true, frac: 0.5,
		},
		{
			name: "deployment complete",
			kind: "Deployment",
			raw: map[string]interface{}{
				"metadata": map[string]interface{}{"generation": int64(4)},
				"spec":     map[string]interface{}{"replicas": int64(4)},
				"status": map[string]interface{}{"observedGeneration": int64(4),
					"replicas": int64(4), "updatedReplicas": int64(4), "availableReplicas": int64(4)},
			},
			ok: true, done: true, frac: 1,
		},
		{
			name: "deployment scaled to zero",
			kind: "Deployment",
			raw: map[string]interface{}{
				"metadata": map[string]interface{}{"generation": int64(2)},
				"spec":     map[string]interface{}{"replicas": int64(0)},
				"status":   map[string]interface{}{"observedGeneration": int64(2)},
			},
			ok: true, done: true, frac: 1,
		},
		{
			name: "deployment past its progress deadline",
			kind: "Deployment",
			raw: map[string]interface{}{
				"metadata": map[string]interface{}{"generation": int64(4)},
				"spec":     map[string]interface{}{"replicas": int64(2)},
				"status": map[string]interface{}{"observedGeneration": int64(4),
					"replicas": int64(3), "updatedReplicas": int64(1), "availableReplicas": int64(2),
					"conditions": []interface{}{map[string]interface{}{"type": "Progressing", "reason": "ProgressDeadlineExceeded"}}},
			},
			ok: true, failed: "ProgressDeadlineExceeded", frac: 0.5,
		},
		{
			name: "statefulset revision still rolling",
			kind: "StatefulSet",
			raw: map[string]interface{}{
				"metadata": map[string]interface{}{"generation": int64(2)},
				"spec":     map[string]interface{}{"replicas": int64(3)},
				"status": map[string]interface{}{"observedGeneration": int64(2),
					"readyReplicas": int64(3), "updatedReplicas": int64(1),
					"currentRevision": "web-a", "updateRevision": "web-b"},
			},
			ok: true, frac: 1.0 / 3,
		},
		{
			name: "statefulset done",
			kind: "StatefulSet",
			raw: map[string]interface{}{
				"metadata": map[string]interface{}{"generation": int64(2)},
				"spec":     map[string]interface{}{"replicas": int64(3)},
				"status": map[string]interface{}{"observedGeneration": int64(2),
					"readyReplicas": int64(3), "updatedReplicas": int64(3),
					"currentRevision": "web-b", "updateRevision": "web-b"},
			},
			ok: true, done: true, frac: 1,
		},
		{
			name: "statefulset OnDelete has no trackable rollout",
			kind: "StatefulSet",
			raw: map[string]interface{}{
				"spec": map[string]interface{}{"updateStrategy": map[string]interface{}{"type": "OnDelete"}},
			},
		},
		{
			name: "daemonset rolling",
			kind: "DaemonSet",
			raw: map[string]interface{}{
				"metadata": map[string]interface{}{"generation": int64(7)},
				"status": map[string]interface{}{"observedGeneration": int64(7),
					"desiredNumberScheduled": int64(10), "updatedNumberScheduled": int64(6), "numberAvailable": int64(9)},
			},
			ok: true, frac: 0.6,
		},
		{
			name: "service has no rollout",
			kind: "Service",
			raw:  map[string]interface{}{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, ok := RolloutStatus(c.kind, c.raw)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if !ok {
				return
			}
			if p.Done != c.done || p.Failed != c.failed {
				t.Fatalf("done=%v failed=%q, want done=%v failed=%q (%+v)", p.Done, p.Failed, c.done, c.failed, p)
			}
			if got := p.Fraction(); got < c.frac-1e-9 || got > c.frac+1e-9 {
				t.Fatalf("fraction = %v, want %v (%+v)", got, c.frac, p)
			}
		})
	}
}

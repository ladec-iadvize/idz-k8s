package kube

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/iadvize/idz-k8s/internal/model"
)

// Drift (US16, FR-033): live object vs its last-applied configuration
// (kubectl.kubernetes.io/last-applied-configuration). Pure derivation from
// the object already in hand — no API call, no apply affordance anywhere.

// lastAppliedAnnotation is written by kubectl apply and declarative tooling.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// Drift compares the live object against its last-applied baseline. ok is
// false when no baseline exists (the caller states it — FR-033 AC2). Only
// fields PRESENT in the baseline are compared: server-added defaults and
// status are not drift.
func Drift(raw map[string]interface{}) (drifts []model.Drift, ok bool) {
	meta, _ := raw["metadata"].(map[string]interface{})
	anns, _ := meta["annotations"].(map[string]interface{})
	src, _ := anns[lastAppliedAnnotation].(string)
	if src == "" {
		return nil, false
	}
	var applied map[string]interface{}
	if err := json.Unmarshal([]byte(src), &applied); err != nil {
		return []model.Drift{{Path: "(annotation)", Applied: "unparseable last-applied JSON", Live: err.Error()}}, true
	}
	compare("", applied, raw, &drifts)
	sort.Slice(drifts, func(i, j int) bool { return drifts[i].Path < drifts[j].Path })
	return drifts, true
}

// compare walks the applied config; every leaf must match the live value.
func compare(path string, applied, live interface{}, out *[]model.Drift) {
	switch a := applied.(type) {
	case map[string]interface{}:
		lm, isMap := live.(map[string]interface{})
		if !isMap {
			*out = append(*out, model.Drift{Path: path, Applied: compact(a), Live: compact(live)})
			return
		}
		for k, av := range a {
			sub := k
			if path != "" {
				sub = path + "." + k
			}
			if sub == "metadata.annotations."+lastAppliedAnnotation {
				continue // the baseline itself
			}
			lv, exists := lm[k]
			if !exists {
				*out = append(*out, model.Drift{Path: sub, Applied: compact(av), Live: "<absent>"})
				continue
			}
			compare(sub, av, lv, out)
		}
	case []interface{}:
		ls, isSlice := live.([]interface{})
		if !isSlice || len(a) != len(ls) {
			*out = append(*out, model.Drift{Path: path, Applied: compact(a), Live: compact(live)})
			return
		}
		for i := range a {
			compare(fmt.Sprintf("%s[%d]", path, i), a[i], ls[i], out)
		}
	default:
		if !scalarEqual(applied, live) {
			*out = append(*out, model.Drift{Path: path, Applied: compact(applied), Live: compact(live)})
		}
	}
}

// scalarEqual compares leaves, tolerating JSON number representation
// differences between the annotation (float64) and the live object (int64).
func scalarEqual(a, b interface{}) bool {
	if na, oka := toFloat(a); oka {
		if nb, okb := toFloat(b); okb {
			return na == nb
		}
		return false
	}
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

// compact renders a value for the drift table (JSON, truncated by the UI).
func compact(v interface{}) string {
	if v == nil {
		return "null"
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return strings.TrimSpace(string(b))
}

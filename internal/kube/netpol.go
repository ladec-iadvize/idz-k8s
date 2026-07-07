package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/model"
)

// Connectivity (US14, FR-031): which NetworkPolicies select a set of pod
// labels in a namespace, and what ingress/egress they allow. Read-only,
// derived strictly from the policies' spec.

// Connectivity lists the namespace's NetworkPolicies and evaluates them
// against the given pod labels.
func (c *Client) Connectivity(ctx context.Context, namespace, subject string, podLabels map[string]string) (model.ConnectivityReport, error) {
	rep := model.ConnectivityReport{Subject: subject, Namespace: namespace}
	ul, err := c.Dynamic.Resource(netpolGVR).Namespace(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return rep, err
	}
	for i := range ul.Items {
		np := &ul.Items[i]
		sel, _, _ := unstructured.NestedMap(np.Object, "spec", "podSelector")
		if !selectorMatches(sel, podLabels) {
			continue
		}
		rep.Policies = append(rep.Policies, np.GetName())
		for _, pt := range policyTypes(np) {
			switch pt {
			case "Ingress":
				rep.IngressRestricted = true
				rep.Ingress = append(rep.Ingress, directionRules(np, "ingress", "from")...)
			case "Egress":
				rep.EgressRestricted = true
				rep.Egress = append(rep.Egress, directionRules(np, "egress", "to")...)
			}
		}
	}
	sort.Strings(rep.Policies)
	return rep, nil
}

// selectorMatches evaluates a label selector (matchLabels + matchExpressions)
// against pod labels. An empty selector selects every pod in the namespace.
func selectorMatches(sel map[string]interface{}, labels map[string]string) bool {
	ml, _, _ := unstructured.NestedStringMap(sel, "matchLabels")
	for k, v := range ml {
		if labels[k] != v {
			return false
		}
	}
	exprs, _, _ := unstructured.NestedSlice(sel, "matchExpressions")
	for _, e := range exprs {
		em, ok := e.(map[string]interface{})
		if !ok {
			return false
		}
		key, _ := em["key"].(string)
		op, _ := em["operator"].(string)
		var vals []string
		if raw, ok := em["values"].([]interface{}); ok {
			for _, v := range raw {
				if sv, ok := v.(string); ok {
					vals = append(vals, sv)
				}
			}
		}
		val, has := labels[key]
		switch op {
		case "In":
			if !has || !containsStr(vals, val) {
				return false
			}
		case "NotIn":
			if has && containsStr(vals, val) {
				return false
			}
		case "Exists":
			if !has {
				return false
			}
		case "DoesNotExist":
			if has {
				return false
			}
		default:
			return false // unknown operator: be conservative, do not match
		}
	}
	return true
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// policyTypes returns the effective policyTypes, applying the API default:
// Ingress always, plus Egress when egress rules are present.
func policyTypes(np *unstructured.Unstructured) []string {
	if pts, found, _ := unstructured.NestedStringSlice(np.Object, "spec", "policyTypes"); found && len(pts) > 0 {
		return pts
	}
	out := []string{"Ingress"}
	if egress, _, _ := unstructured.NestedSlice(np.Object, "spec", "egress"); len(egress) > 0 {
		out = append(out, "Egress")
	}
	return out
}

// directionRules summarizes one direction of a policy. A declared direction
// with zero rules allows NOTHING (default deny); a rule with no peers allows
// everywhere; a rule with no ports allows all ports.
func directionRules(np *unstructured.Unstructured, direction, peerField string) []model.PolicyRule {
	rules, _, _ := unstructured.NestedSlice(np.Object, "spec", direction)
	out := make([]model.PolicyRule, 0, len(rules))
	for _, r := range rules {
		rm, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		rule := model.PolicyRule{Policy: np.GetName()}
		if peers, ok := rm[peerField].([]interface{}); ok && len(peers) > 0 {
			for _, p := range peers {
				if pm, ok := p.(map[string]interface{}); ok {
					rule.Peers = append(rule.Peers, peerLabel(pm))
				}
			}
		} else {
			rule.Peers = []string{"anywhere"}
		}
		if ports, ok := rm["ports"].([]interface{}); ok {
			for _, p := range ports {
				if pm, ok := p.(map[string]interface{}); ok {
					rule.Ports = append(rule.Ports, portLabel(pm))
				}
			}
		}
		out = append(out, rule)
	}
	return out
}

// peerLabel renders one peer of a rule.
func peerLabel(pm map[string]interface{}) string {
	var parts []string
	if ip, ok := pm["ipBlock"].(map[string]interface{}); ok {
		cidr, _ := ip["cidr"].(string)
		lbl := cidr
		if exc, ok := ip["except"].([]interface{}); ok && len(exc) > 0 {
			lbl += fmt.Sprintf(" (except %d block(s))", len(exc))
		}
		parts = append(parts, lbl)
	}
	nsSel, hasNS := pm["namespaceSelector"].(map[string]interface{})
	podSel, hasPod := pm["podSelector"].(map[string]interface{})
	switch {
	case hasNS && hasPod:
		parts = append(parts, "pods "+selectorLabel(podSel)+" in namespaces "+selectorLabel(nsSel))
	case hasNS:
		parts = append(parts, "namespaces "+selectorLabel(nsSel))
	case hasPod:
		parts = append(parts, "pods "+selectorLabel(podSel)+" (same ns)")
	}
	if len(parts) == 0 {
		return "anywhere"
	}
	return strings.Join(parts, " + ")
}

// selectorLabel renders a label selector compactly ("app=front", "<all>").
func selectorLabel(sel map[string]interface{}) string {
	ml, _, _ := unstructured.NestedStringMap(sel, "matchLabels")
	keys := make([]string, 0, len(ml))
	for k := range ml {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+ml[k])
	}
	if exprs, _, _ := unstructured.NestedSlice(sel, "matchExpressions"); len(exprs) > 0 {
		pairs = append(pairs, fmt.Sprintf("+%d expr", len(exprs)))
	}
	if len(pairs) == 0 {
		return "<all>"
	}
	return strings.Join(pairs, ",")
}

// portLabel renders one port of a rule ("TCP/8080", "TCP/8080-9090").
func portLabel(pm map[string]interface{}) string {
	proto, _ := pm["protocol"].(string)
	if proto == "" {
		proto = "TCP"
	}
	port := ""
	switch v := pm["port"].(type) {
	case string:
		port = v
	case int64:
		port = fmt.Sprintf("%d", v)
	case float64:
		port = fmt.Sprintf("%d", int64(v))
	}
	if port == "" {
		return proto + "/any"
	}
	if end, ok := pm["endPort"].(int64); ok && end > 0 {
		return fmt.Sprintf("%s/%s-%d", proto, port, end)
	}
	return proto + "/" + port
}

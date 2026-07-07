package kube

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/iadvize/idz-k8s/internal/model"
)

// Posture rules (US13, FR-030): advisory, read-only findings derived ONLY
// from observed configuration — every finding references the concrete
// object/field, nothing is fabricated.

var (
	netpolGVR  = schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}
	secretsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
)

// Rule labels — the UI groups findings by these.
const (
	RuleNoResources = "missing requests/limits"
	RulePrivileged  = "privileged container"
	RuleRoot        = "may run as root"
	RuleNoProbes    = "missing probes"
	RuleLatestImage = "image not pinned (latest)"
	RuleNoNetpol    = "namespace without NetworkPolicy"
	RuleTLSExpiry   = "TLS certificate near/past expiry"
)

// tlsExpirySoon is the advisory warning horizon for TLS certificates.
const tlsExpirySoon = 30 * 24 * time.Hour

// Posture evaluates the rules over the namespace scope (name, glob pattern,
// or "" for all). now is injected for testability.
func (c *Client) Posture(ctx context.Context, namespace string, now time.Time) ([]model.PostureFinding, error) {
	apiNS, pattern := namespaceScope(namespace)
	list := func(gvr schema.GroupVersionResource) (*unstructured.UnstructuredList, error) {
		ri := c.Dynamic.Resource(gvr)
		if apiNS != "" {
			return ri.Namespace(apiNS).List(ctx, metav1.ListOptions{})
		}
		return ri.List(ctx, metav1.ListOptions{})
	}
	inScope := func(ns string) bool { return pattern == "" || MatchNamespace(pattern, ns) }

	pods, err := list(podsGVR)
	if err != nil {
		return nil, err
	}
	var out []model.PostureFinding
	nsSeen := map[string]bool{}
	for i := range pods.Items {
		p := &pods.Items[i]
		if !inScope(p.GetNamespace()) {
			continue
		}
		phase, _, _ := unstructured.NestedString(p.Object, "status", "phase")
		if phase == "Succeeded" || phase == "Failed" {
			continue // finished pods are not a live posture concern
		}
		nsSeen[p.GetNamespace()] = true
		out = append(out, podFindings(p)...)
	}

	// Namespaces of the scoped pods that have no NetworkPolicy at all.
	if npols, err := list(netpolGVR); err == nil {
		covered := map[string]bool{}
		for i := range npols.Items {
			covered[npols.Items[i].GetNamespace()] = true
		}
		nss := make([]string, 0, len(nsSeen))
		for ns := range nsSeen {
			if !covered[ns] {
				nss = append(nss, ns)
			}
		}
		sort.Strings(nss)
		for _, ns := range nss {
			out = append(out, model.PostureFinding{
				Rule: RuleNoNetpol, Severity: model.HealthWarning,
				Namespace: ns, Name: ns,
				Detail: "no NetworkPolicy selects anything in this namespace (all traffic allowed)",
			})
		}
	}
	// The NetworkPolicy API group may simply not be served: skip silently
	// rather than failing the whole report (advisory view).

	if secrets, err := list(secretsGVR); err == nil {
		for i := range secrets.Items {
			s := &secrets.Items[i]
			if !inScope(s.GetNamespace()) {
				continue
			}
			if f, ok := tlsFinding(s, now); ok {
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// podFindings applies the container-level rules to one live pod.
func podFindings(p *unstructured.Unstructured) []model.PostureFinding {
	ns, name := p.GetNamespace(), p.GetName()
	var out []model.PostureFinding
	add := func(rule string, sev model.HealthLevel, container, detail string) {
		out = append(out, model.PostureFinding{
			Rule: rule, Severity: sev, Namespace: ns, Name: name,
			Container: container, Detail: detail,
		})
	}
	ownedByJob := false
	for _, ref := range p.GetOwnerReferences() {
		if ref.Kind == "Job" {
			ownedByJob = true
		}
	}
	podSC, _, _ := unstructured.NestedMap(p.Object, "spec", "securityContext")
	containers, _, _ := unstructured.NestedSlice(p.Object, "spec", "containers")
	for _, c := range containers {
		ctr, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		cname, _ := ctr["name"].(string)

		// missing requests/limits
		var missing []string
		for _, part := range []string{"requests", "limits"} {
			res, _, _ := unstructured.NestedStringMap(ctr, "resources", part)
			for _, k := range []string{"cpu", "memory"} {
				if res[k] == "" {
					missing = append(missing, k+" "+strings.TrimSuffix(part, "s"))
				}
			}
		}
		if len(missing) > 0 {
			add(RuleNoResources, model.HealthWarning, cname, "no "+strings.Join(missing, ", "))
		}

		// privileged
		if priv, found, _ := unstructured.NestedBool(ctr, "securityContext", "privileged"); found && priv {
			add(RulePrivileged, model.HealthError, cname, "securityContext.privileged: true")
		}

		// may run as root
		ctrSC, _, _ := unstructured.NestedMap(ctr, "securityContext")
		if detail, root := rootDetail(podSC, ctrSC); root {
			add(RuleRoot, model.HealthWarning, cname, detail)
		}

		// missing probes (jobs legitimately run without them)
		if !ownedByJob {
			var probes []string
			if _, found, _ := unstructured.NestedMap(ctr, "livenessProbe"); !found {
				probes = append(probes, "liveness")
			}
			if _, found, _ := unstructured.NestedMap(ctr, "readinessProbe"); !found {
				probes = append(probes, "readiness")
			}
			if len(probes) > 0 {
				add(RuleNoProbes, model.HealthWarning, cname, "no "+strings.Join(probes, "/")+" probe")
			}
		}

		// image not pinned
		if img, _ := ctr["image"].(string); img != "" {
			tag := ""
			if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
				tag = img[i+1:]
			}
			if tag == "" || tag == "latest" {
				add(RuleLatestImage, model.HealthWarning, cname, "image "+img)
			}
		}
	}
	return out
}

// rootDetail decides the "may run as root" rule from the pod- and
// container-level security contexts (the container overrides the pod).
func rootDetail(podSC, ctrSC map[string]interface{}) (string, bool) {
	nonRoot := func(sc map[string]interface{}) (bool, bool) {
		v, ok := sc["runAsNonRoot"].(bool)
		return v, ok
	}
	user := func(sc map[string]interface{}) (int64, bool) {
		v, ok := sc["runAsUser"].(int64)
		return v, ok
	}
	if v, ok := nonRoot(ctrSC); ok {
		if v {
			return "", false
		}
	} else if v, ok := nonRoot(podSC); ok && v {
		return "", false
	}
	if u, ok := user(ctrSC); ok {
		if u == 0 {
			return "securityContext.runAsUser: 0", true
		}
		return "", false
	}
	if u, ok := user(podSC); ok {
		if u == 0 {
			return "securityContext.runAsUser: 0 (pod)", true
		}
		return "", false
	}
	return "no runAsNonRoot/runAsUser set", true
}

// tlsFinding flags a kubernetes.io/tls secret whose certificate is expired or
// expires within the warning horizon.
func tlsFinding(s *unstructured.Unstructured, now time.Time) (model.PostureFinding, bool) {
	if t, _, _ := unstructured.NestedString(s.Object, "type"); t != "kubernetes.io/tls" {
		return model.PostureFinding{}, false
	}
	crtB64, _, _ := unstructured.NestedString(s.Object, "data", "tls.crt")
	if crtB64 == "" {
		return model.PostureFinding{}, false
	}
	der, err := base64.StdEncoding.DecodeString(crtB64)
	if err != nil {
		return model.PostureFinding{}, false
	}
	block, _ := pem.Decode(der)
	if block == nil {
		return model.PostureFinding{}, false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return model.PostureFinding{}, false
	}
	f := model.PostureFinding{
		Rule: RuleTLSExpiry, Namespace: s.GetNamespace(), Name: s.GetName(),
	}
	switch {
	case cert.NotAfter.Before(now):
		f.Severity = model.HealthError
		f.Detail = fmt.Sprintf("tls.crt EXPIRED %s (%s)", cert.NotAfter.Format("2006-01-02"), cert.Subject.CommonName)
		return f, true
	case cert.NotAfter.Before(now.Add(tlsExpirySoon)):
		f.Severity = model.HealthWarning
		f.Detail = fmt.Sprintf("tls.crt expires %s, in %d day(s) (%s)",
			cert.NotAfter.Format("2006-01-02"), int(cert.NotAfter.Sub(now).Hours()/24), cert.Subject.CommonName)
		return f, true
	}
	return model.PostureFinding{}, false
}

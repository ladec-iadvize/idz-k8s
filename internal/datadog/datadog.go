// Package datadog builds deep-links into Datadog Logs (FR-039, US17).
//
// It is pure string work: the tool never talks to Datadog. The URL is handed
// to the operator's browser, whose own Datadog session authenticates — no
// credential is held, nothing leaves the tool (guarded by TestNoNetworkImports).
package datadog

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Defaults match the iAdvize log pipeline (Grafana Alloy → OTLP ingestion):
// logs carry @namespace, @cluster_name and @pod_owner attributes, and
// @container_name holds the POD name — not the standard kube_* agent tags.
// Pods are matched by name alone: their name is unique, and some system
// pods (kube-system) ship without @cluster_name. Workloads and namespaces
// are cluster-scoped so dev and prod never mix under a shared namespace name.
const (
	DefaultSite           = "datadoghq.eu"
	DefaultPodQuery       = "@namespace:{namespace} @container_name:{pod}"
	DefaultWorkloadQuery  = "@cluster_name:{context} @namespace:{namespace} @pod_owner:{owner}"
	DefaultNamespaceQuery = "@cluster_name:{context} @namespace:{namespace}"

	// Off disables the link (site: off in the config file).
	Off = "off"
)

// Settings are the user-tunable parts of the link (config file, `datadog:`).
// Empty fields fall back to the defaults above.
type Settings struct {
	Site           string
	PodQuery       string
	WorkloadQuery  string
	NamespaceQuery string
}

// Scope is what the link is about: the selected object plus the context it
// lives in.
type Scope struct {
	Kind      string // Pod, Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob, Namespace
	Namespace string // the object's namespace ("" for a Namespace)
	Name      string
	Context   string // kube context name — the Datadog cluster name by default
	// Siblings are the other objects of the same kind in the namespace. A
	// prefix-matched owner (Deployment → ReplicaSet/<name>-*) would also catch
	// a sibling named <name>-<suffix>; those are excluded explicitly.
	Siblings []string
}

// ErrDisabled is returned when the link is switched off in the config.
var ErrDisabled = errors.New("the Datadog link is disabled (datadog.site: off in the config file)")

// Supported reports whether a kind has a Datadog log scope.
func Supported(kind string) bool {
	_, ok := levelOf(kind)
	return ok
}

type level int

const (
	levelPod level = iota
	levelWorkload
	levelNamespace
)

func levelOf(kind string) (level, bool) {
	switch strings.ToLower(kind) {
	case "pod":
		return levelPod, true
	case "deployment", "statefulset", "daemonset", "replicaset", "job", "cronjob":
		return levelWorkload, true
	case "namespace":
		return levelNamespace, true
	}
	return 0, false
}

// LogsURL returns the Datadog Logs URL for the scope, or an error that says
// why no valid link can be built — never a guessed or partial URL.
func LogsURL(s Settings, sc Scope) (string, error) {
	host, err := appHost(s.Site)
	if err != nil {
		return "", err
	}
	q, err := Query(s, sc)
	if err != nil {
		return "", err
	}
	v := url.Values{}
	v.Set("query", q)
	v.Set("live", "true")
	return "https://" + host + "/logs?" + v.Encode(), nil
}

// Query renders the Datadog search query for the scope.
func Query(s Settings, sc Scope) (string, error) {
	lvl, ok := levelOf(sc.Kind)
	if !ok {
		return "", fmt.Errorf("no Datadog log scope for %s — select a pod, a workload or a namespace", orUnknown(sc.Kind))
	}
	if sc.Name == "" {
		return "", errors.New("nothing selected")
	}
	vals := map[string]string{
		"name":    escape(sc.Name),
		"kind":    strings.ToLower(sc.Kind),
		"context": escape(sc.Context),
	}
	var tmpl, field string
	switch lvl {
	case levelPod:
		tmpl, field = or(s.PodQuery, DefaultPodQuery), "podQuery"
		vals["namespace"] = escape(sc.Namespace)
		vals["pod"] = escape(sc.Name)
	case levelWorkload:
		tmpl, field = or(s.WorkloadQuery, DefaultWorkloadQuery), "workloadQuery"
		vals["namespace"] = escape(sc.Namespace)
		vals["owner"] = ownerPattern(sc.Kind, sc.Name, sc.Siblings)
	case levelNamespace:
		tmpl, field = or(s.NamespaceQuery, DefaultNamespaceQuery), "namespaceQuery"
		vals["namespace"] = escape(sc.Name)
	}
	return render(tmpl, field, vals)
}

var placeholder = regexp.MustCompile(`\{([A-Za-z]+)\}`)

// render substitutes {placeholders}. An unknown placeholder or one without a
// value for this scope is an error: a query with a hole in it would match
// the wrong logs, which is worse than no link (FR-021 spirit).
func render(tmpl, field string, vals map[string]string) (string, error) {
	var bad error
	out := placeholder.ReplaceAllStringFunc(tmpl, func(p string) string {
		key := p[1 : len(p)-1]
		v, known := vals[key]
		switch {
		case bad != nil:
		case !known:
			bad = fmt.Errorf("datadog.%s uses %s, which has no value for this selection", field, p)
		case v == "":
			bad = fmt.Errorf("datadog.%s needs %s, which is empty here", field, p)
		}
		return v
	})
	if bad != nil {
		return "", bad
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("datadog.%s renders an empty query", field)
	}
	return out, nil
}

// ownerPattern matches the pods' ownerReference as "<Kind>/<name>". Pods of
// a Deployment are owned by its ReplicaSets (<name>-<hash>) and pods of a
// CronJob by its Jobs (<name>-<suffix>), hence the prefix wildcard — with
// the same-kind siblings sharing that prefix excluded (owner check
// 2026-10-05: `back` vs `back-traceability-kafka-connect` in ha-back).
func ownerPattern(kind, name string, siblings []string) string {
	var ownerKind string
	switch strings.ToLower(kind) {
	case "deployment":
		ownerKind = "ReplicaSet"
	case "cronjob":
		ownerKind = "Job"
	default:
		return canonicalKind(kind) + "/" + escape(name)
	}
	base := ownerKind + "/" + escape(name) + "-*"
	var excl []string
	for _, s := range siblings {
		if s != name && strings.HasPrefix(s, name+"-") {
			excl = append(excl, "-"+ownerKind+"/"+escape(s)+"-*")
		}
	}
	if len(excl) == 0 {
		return base
	}
	sort.Strings(excl)
	return "(" + base + " " + strings.Join(excl, " ") + ")"
}

func canonicalKind(kind string) string {
	switch strings.ToLower(kind) {
	case "statefulset":
		return "StatefulSet"
	case "daemonset":
		return "DaemonSet"
	case "replicaset":
		return "ReplicaSet"
	case "job":
		return "Job"
	}
	return kind
}

// appHost maps a Datadog site to its web app host: the two historical sites
// use the app. subdomain (app.datadoghq.eu), the others are their own host
// (us3.datadoghq.com, ap1.datadoghq.com…).
func appHost(site string) (string, error) {
	s := strings.TrimSpace(strings.ToLower(site))
	if s == "" {
		s = DefaultSite
	}
	if s == Off {
		return "", ErrDisabled
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	s = strings.TrimSuffix(s, "/")
	if !strings.Contains(s, ".") || strings.ContainsAny(s, " /?#@:") {
		return "", fmt.Errorf("datadog.site %q is not a Datadog site (e.g. datadoghq.eu)", site)
	}
	if s == "datadoghq.com" || s == "datadoghq.eu" {
		return "app." + s, nil
	}
	return s, nil
}

// escape backslash-escapes Datadog query syntax characters in a value.
// Kubernetes names never contain them; context names may (ARN-style
// contexts carry ':' and '/'), and an unescaped ':' would split the term.
func escape(v string) string {
	var b strings.Builder
	for _, r := range v {
		if strings.ContainsRune(`+=&|><!(){}[]^"~*?:\/ `, r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func or(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func orUnknown(kind string) string {
	if kind == "" {
		return "this selection"
	}
	return kind
}

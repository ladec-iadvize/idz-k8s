package datadog

import (
	"errors"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// queryOf decodes the query parameter of a built link.
func queryOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("unparsable link %q: %v", link, err)
	}
	return u.Query().Get("query")
}

func TestLogsURLPerScopeLevel(t *testing.T) {
	ctx := "prod-main-eks-workloads"
	cases := []struct {
		name string
		sc   Scope
		want string
	}{
		{"pod", Scope{Kind: "Pod", Namespace: "ha-back", Name: "back-6cbb495679-x7k2p", Context: ctx},
			"@namespace:ha-back @container_name:back-6cbb495679-x7k2p"},
		{"deployment", Scope{Kind: "Deployment", Namespace: "ha-back", Name: "api", Context: ctx},
			"@cluster_name:prod-main-eks-workloads @namespace:ha-back @pod_owner:ReplicaSet/api-*"},
		{"statefulset", Scope{Kind: "StatefulSet", Namespace: "data", Name: "redis", Context: ctx},
			"@cluster_name:prod-main-eks-workloads @namespace:data @pod_owner:StatefulSet/redis"},
		{"daemonset", Scope{Kind: "DaemonSet", Namespace: "kube-system", Name: "kube-proxy", Context: ctx},
			"@cluster_name:prod-main-eks-workloads @namespace:kube-system @pod_owner:DaemonSet/kube-proxy"},
		{"job", Scope{Kind: "Job", Namespace: "batch", Name: "nightly-29001", Context: ctx},
			"@cluster_name:prod-main-eks-workloads @namespace:batch @pod_owner:Job/nightly-29001"},
		{"cronjob", Scope{Kind: "CronJob", Namespace: "batch", Name: "nightly", Context: ctx},
			"@cluster_name:prod-main-eks-workloads @namespace:batch @pod_owner:Job/nightly-*"},
		{"namespace", Scope{Kind: "Namespace", Name: "ha-back", Context: ctx},
			"@cluster_name:prod-main-eks-workloads @namespace:ha-back"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			link, err := LogsURL(Settings{}, c.sc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.HasPrefix(link, "https://app.datadoghq.eu/logs?") {
				t.Errorf("default site must be the EU app, got %s", link)
			}
			if got := queryOf(t, link); got != c.want {
				t.Errorf("query = %q, want %q", got, c.want)
			}
		})
	}
}

// A prefix-matched owner must not swallow a sibling whose name extends it
// (prod: `back` vs `back-traceability-kafka-connect` in ha-back).
func TestOwnerPrefixExcludesSiblings(t *testing.T) {
	sc := Scope{Kind: "Deployment", Namespace: "ha-back", Name: "back", Context: "prod",
		Siblings: []string{"back", "back-traceability-kafka-connect", "backoffice", "front"}}
	q, err := Query(Settings{}, sc)
	if err != nil {
		t.Fatal(err)
	}
	want := "@pod_owner:(ReplicaSet/back-* -ReplicaSet/back-traceability-kafka-connect-*)"
	if !strings.HasSuffix(q, want) {
		t.Errorf("query = %q, want suffix %q", q, want)
	}
	if strings.Contains(q, "backoffice") || strings.Contains(q, "front") {
		t.Errorf("only names extending %q-… are siblings to exclude: %q", sc.Name, q)
	}
}

func TestSiteAndTemplateOverrides(t *testing.T) {
	s := Settings{
		Site:           "us5.datadoghq.com",
		PodQuery:       "kube_namespace:{namespace} pod_name:{pod} env:{context}",
		WorkloadQuery:  "kube_{kind}:{name}",
		NamespaceQuery: "kube_namespace:{namespace}",
	}
	link, err := LogsURL(s, Scope{Kind: "Pod", Namespace: "ns", Name: "p", Context: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://us5.datadoghq.com/logs?") {
		t.Errorf("site override not honored: %s", link)
	}
	if got := queryOf(t, link); got != "kube_namespace:ns pod_name:p env:dev" {
		t.Errorf("pod template override not honored: %q", got)
	}
	q, _ := Query(s, Scope{Kind: "Deployment", Namespace: "ns", Name: "api"})
	if q != "kube_deployment:api" {
		t.Errorf("workload template override not honored: %q", q)
	}
	for site, host := range map[string]string{
		"datadoghq.com": "app.datadoghq.com", "https://datadoghq.eu/": "app.datadoghq.eu",
		" DatadogHQ.eu ": "app.datadoghq.eu", "ap1.datadoghq.com": "ap1.datadoghq.com",
	} {
		link, err := LogsURL(Settings{Site: site}, Scope{Kind: "Pod", Namespace: "n", Name: "p"})
		if err != nil || !strings.HasPrefix(link, "https://"+host+"/logs?") {
			t.Errorf("site %q → %q (%v), want host %s", site, link, err, host)
		}
	}
}

// SC-024: every case where no valid link exists says why — never a URL.
func TestNoGuessedLink(t *testing.T) {
	pod := Scope{Kind: "Pod", Namespace: "ns", Name: "p", Context: "dev"}
	cases := []struct {
		name string
		s    Settings
		sc   Scope
		want string
	}{
		{"disabled", Settings{Site: "off"}, pod, "disabled"},
		{"bogus site", Settings{Site: "not a site"}, pod, "not a Datadog site"},
		{"unsupported kind", Settings{}, Scope{Kind: "ConfigMap", Namespace: "ns", Name: "c"}, "no Datadog log scope for ConfigMap"},
		{"no selection", Settings{}, Scope{Kind: "Pod"}, "nothing selected"},
		{"unknown placeholder", Settings{PodQuery: "service:{service}"}, pod, "{service}"},
		{"placeholder outside its level", Settings{PodQuery: "@pod_owner:{owner}"}, pod, "{owner}"},
		{"empty context", Settings{}, Scope{Kind: "Namespace", Name: "ns"}, "{context}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			link, err := LogsURL(c.s, c.sc)
			if err == nil {
				t.Fatalf("expected an explicit error, got link %q", link)
			}
			if link != "" {
				t.Errorf("an error must come with NO link, got %q", link)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not say %q", err, c.want)
			}
		})
	}
	if _, err := LogsURL(Settings{Site: "off"}, pod); !errors.Is(err, ErrDisabled) {
		t.Errorf("site off must return ErrDisabled, got %v", err)
	}
}

// ARN-style context names carry ':' and '/': escaped, never a broken term.
func TestValuesAreEscaped(t *testing.T) {
	q, err := Query(Settings{}, Scope{Kind: "Namespace", Name: "ns",
		Context: "arn:aws:eks:eu-central-1:1:cluster/dev"})
	if err != nil {
		t.Fatal(err)
	}
	want := `@cluster_name:arn\:aws\:eks\:eu-central-1\:1\:cluster\/dev @namespace:ns`
	if q != want {
		t.Errorf("query = %q, want %q", q, want)
	}
	if got := strconv.Quote(escape("a b*")); got != strconv.Quote(`a\ b\*`) {
		t.Errorf("escape = %s", got)
	}
}

// FR-039 / SC-024: the tool performs ZERO calls to Datadog. This package is
// the only place that knows about Datadog; it must not be able to reach the
// network at all.
func TestNoNetworkImports(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range parsed.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if p == "net" || strings.HasPrefix(p, "net/http") || p == "net/rpc" || p == "os/exec" ||
				strings.Contains(p, "datadog-api-client") {
				t.Errorf("%s imports %s: the Datadog link must stay a pure URL builder", f, p)
			}
		}
	}
}

package kube

import (
	"context"
	"sort"

	authv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/iadvize/idz-k8s/internal/model"
)

// AccessSummary asks the API server what the current credentials may do in a
// namespace (SelfSubjectRulesReview — the ONE allowed "create" in this
// read-only tool: pure introspection, it grants and changes nothing), keeps
// the read-relevant rules, and derives which browsable types cannot be
// listed (FR-032).
func (c *Client) AccessSummary(ctx context.Context, namespace string, types []model.ResourceType) (model.AccessReport, error) {
	if namespace == "" || IsNamespacePattern(namespace) {
		namespace = "default" // the review is namespace-scoped by design
	}
	rep := model.AccessReport{Namespace: namespace}
	res, err := c.Clientset.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx,
		&authv1.SelfSubjectRulesReview{Spec: authv1.SelfSubjectRulesReviewSpec{Namespace: namespace}},
		metav1.CreateOptions{})
	if err != nil {
		return rep, err
	}
	rep.Incomplete = res.Status.Incomplete
	rep.Evaluation = res.Status.EvaluationError
	for _, r := range res.Status.ResourceRules {
		reads := readOnlyVerbs(r.Verbs)
		if len(reads) == 0 {
			continue // the tool only ever uses read verbs; the rest is noise here
		}
		rep.Rules = append(rep.Rules, model.AccessRule{
			Verbs: reads, Groups: r.APIGroups, Resources: r.Resources, Names: r.ResourceNames,
		})
	}
	for _, t := range types {
		if !rulesAllow(res.Status.ResourceRules, t.Group, t.Resource, "list") {
			rep.Unlistable = append(rep.Unlistable, t.Key())
		}
	}
	sort.Strings(rep.Unlistable)
	return rep, nil
}

// IsForbidden reports whether an API error is an RBAC denial — shown as an
// access problem ('a' explains), never as a lost connection.
func IsForbidden(err error) bool { return apierrors.IsForbidden(err) }

// readOnlyVerbs keeps get/list/watch (or the '*' wildcard, expanded).
func readOnlyVerbs(verbs []string) []string {
	var out []string
	for _, v := range verbs {
		if v == "*" {
			return []string{"get", "list", "watch"}
		}
		if readVerbs[v] {
			out = append(out, v)
		}
	}
	return out
}

// rulesAllow reports whether any rule grants the verb on group/resource.
func rulesAllow(rules []authv1.ResourceRule, group, resource, verb string) bool {
	match := func(xs []string, v string) bool {
		for _, x := range xs {
			if x == "*" || x == v {
				return true
			}
		}
		return false
	}
	for _, r := range rules {
		if match(r.Verbs, verb) && match(r.APIGroups, group) && match(r.Resources, resource) {
			return true
		}
	}
	return false
}

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
// namespace (SelfSubjectRulesReview — pure introspection, it grants and
// changes nothing), reports every granted verb (read AND write — the admin
// actions run under the same credentials), and derives which browsable types
// cannot be listed (FR-032).
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
		if len(r.Verbs) == 0 {
			continue
		}
		rep.Rules = append(rep.Rules, model.AccessRule{
			Verbs: canonicalVerbs(r.Verbs), Groups: r.APIGroups, Resources: r.Resources, Names: r.ResourceNames,
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

// canonicalVerbs orders verbs read-first (get/list/watch, then writes) so
// the access view stays scannable; a '*' wildcard stands alone.
func canonicalVerbs(verbs []string) []string {
	rank := map[string]int{"get": 0, "list": 1, "watch": 2, "create": 3, "update": 4, "patch": 5, "delete": 6, "deletecollection": 7}
	out := make([]string, 0, len(verbs))
	for _, v := range verbs {
		if v == "*" {
			return []string{"*"}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, iOK := rank[out[i]]
		rj, jOK := rank[out[j]]
		switch {
		case iOK && jOK:
			return ri < rj
		case iOK != jOK:
			return iOK // known verbs first, exotic ones after
		default:
			return out[i] < out[j]
		}
	})
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

package integration

import (
	"context"
	"testing"

	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/iadvize/idz-k8s/internal/model"
)

// seedRulesReview makes the fake clientset answer SelfSubjectRulesReview.
func seedRulesReview(t *testing.T, cs interface {
	PrependReactor(verb, resource string, fn k8stesting.ReactionFunc)
}, rules []authv1.ResourceRule, incomplete bool) {
	t.Helper()
	cs.PrependReactor("create", "selfsubjectrulesreviews",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, &authv1.SelfSubjectRulesReview{
				Status: authv1.SubjectRulesReviewStatus{
					ResourceRules: rules,
					Incomplete:    incomplete,
				},
			}, nil
		})
}

// TestAccessSummary (FR-032): the server's rules are summarized to their read
// verbs and the unlistable browsable types are derived — the ONLY create ever
// issued is the SelfSubjectRulesReview introspection itself.
func TestAccessSummary(t *testing.T) {
	client, _ := NewFakeClient("demo")
	fake := client.Clientset.(interface {
		PrependReactor(verb, resource string, fn k8stesting.ReactionFunc)
		Actions() []k8stesting.Action
	})
	seedRulesReview(t, fake, []authv1.ResourceRule{
		{Verbs: []string{"get", "list", "watch", "delete"}, APIGroups: []string{""}, Resources: []string{"pods", "pods/log"}},
		{Verbs: []string{"*"}, APIGroups: []string{"apps"}, Resources: []string{"deployments"}},
		{Verbs: []string{"create"}, APIGroups: []string{""}, Resources: []string{"secrets"}}, // write-only: not a read rule
	}, false)

	types := []model.ResourceType{
		{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		{Group: "batch", Version: "v1", Resource: "cronjobs", Kind: "CronJob", Namespaced: true},
	}
	rep, err := client.AccessSummary(context.Background(), "demo", types)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Namespace != "demo" || rep.Incomplete {
		t.Fatalf("report=%+v", rep)
	}
	// delete filtered out; '*' expanded; write-only rule dropped.
	if len(rep.Rules) != 2 {
		t.Fatalf("rules=%+v", rep.Rules)
	}
	if got := rep.Rules[0].Verbs; len(got) != 3 {
		t.Fatalf("read verbs=%v (delete must be filtered)", got)
	}
	if got := rep.Rules[1].Verbs; len(got) != 3 {
		t.Fatalf("wildcard verbs=%v (must expand to get/list/watch)", got)
	}
	// cronjobs has no matching rule → unlistable.
	if len(rep.Unlistable) != 1 || rep.Unlistable[0] != "batch/v1/cronjobs" {
		t.Fatalf("unlistable=%v", rep.Unlistable)
	}

	// Zero-mutation guarantee: the sole create is the introspection review.
	for _, a := range fake.Actions() {
		if a.GetVerb() == "create" && a.GetResource().Resource != "selfsubjectrulesreviews" {
			t.Fatalf("unexpected mutating action: %s %s", a.GetVerb(), a.GetResource().Resource)
		}
	}
}

// TestAccessSummaryPatternScopeFallsBack: the review is namespace-scoped, so
// "" and glob scopes are evaluated in "default" (and say so).
func TestAccessSummaryPatternScopeFallsBack(t *testing.T) {
	client, _ := NewFakeClient("")
	fake := client.Clientset.(interface {
		PrependReactor(verb, resource string, fn k8stesting.ReactionFunc)
		Actions() []k8stesting.Action
	})
	seedRulesReview(t, fake, nil, true)
	rep, err := client.AccessSummary(context.Background(), "staging-*", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Namespace != "default" || !rep.Incomplete {
		t.Fatalf("report=%+v", rep)
	}
}

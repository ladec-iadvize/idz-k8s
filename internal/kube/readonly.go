package kube

import "fmt"

// readVerbs are the only Kubernetes verbs this tool is ever allowed to issue.
// The tool is strictly read-only (FR-012, FR-018, SC-006); no mutating verb is
// wired anywhere in this package.
var readVerbs = map[string]bool{
	"get":   true,
	"list":  true,
	"watch": true,
}

// IsMutatingVerb reports whether a Kubernetes API verb changes cluster state.
// Empty verbs are treated as non-mutating (no-ops).
func IsMutatingVerb(verb string) bool {
	if verb == "" {
		return false
	}
	return !readVerbs[verb]
}

// AssertReadOnly returns an error if any verb in the list is mutating. It is the
// reusable check behind the zero-mutation guarantee: tests feed it the verbs
// actually issued by a run (e.g. from a fake client's recorded actions) and it
// fails if the tool ever attempted to change cluster state (SC-006).
func AssertReadOnly(verbs []string) error {
	for _, v := range verbs {
		if IsMutatingVerb(v) {
			return fmt.Errorf("read-only invariant violated: mutating verb %q was issued", v)
		}
	}
	return nil
}

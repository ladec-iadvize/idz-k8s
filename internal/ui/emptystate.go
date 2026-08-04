package ui

// Empty-list explanations (owner report 2026-08-03: drilling from a CronJob
// to its Jobs "looked broken"). A blank table plus a "broken link?" warning
// reads as a bug, when the usual cause is legitimate: a CronJob's completed
// Jobs are removed by its history limits. Every empty list now says WHY it is
// empty, in the terms of what produced it.

import "strings"

// emptyDrillNote explains an empty DRILLED list. The drill mode is derived
// from the state that produced the level, so no extra bookkeeping is needed.
func (m *Model) emptyDrillNote() string {
	child := m.curType.Resource
	switch {
	case m.drillOwnerUID != "":
		if strings.HasPrefix(m.drillFor, "CronJob/") {
			// The normal case, not a failure: kubectl shows the same nothing.
			return "no " + child + " right now — a CronJob keeps only its recent " + child +
				" (its history limits), so between two runs there are none · " +
				"'a' → trigger runs one now · Esc goes back"
		}
		return "no " + child + " owned by " + m.drillFor + " right now · Esc goes back"
	case m.drillSelector != "":
		// Here emptiness IS a symptom: a workload/Service selecting nothing.
		return m.drillFor + ": its selector matches NO " + child + " (broken link?) · Esc goes back"
	case len(m.drillNames) > 0:
		return m.drillFor + ": none of its backend " + child + " exist (broken link?) · Esc goes back"
	case m.drillNode != "":
		return "no pod scheduled on " + strings.TrimPrefix(m.drillFor, "Node/") + " · Esc goes back"
	default:
		return "no " + child + " here · Esc goes back"
	}
}

// emptyListNote explains any empty list: a drilled level, a filter that
// matches nothing, or a genuinely empty scope.
func (m *Model) emptyListNote() string {
	if m.drilling() {
		return m.emptyDrillNote()
	}
	if q := strings.TrimSpace(m.filter.Value()); q != "" {
		return "no " + m.curType.Resource + " matches filter:" + q + " — '/' edits it, Esc clears it"
	}
	scope := m.client.Namespace
	switch {
	case scope == "":
		scope = "any namespace"
	case kubeIsMultiScope(scope):
		scope = "namespaces matching " + scope
	default:
		scope = "namespace " + scope
	}
	if m.disconnected {
		// Never let an outage look like an empty cluster (FR-016/FR-021).
		return "nothing to show while the cluster is unreachable — the banner above has the reason"
	}
	return "no " + m.curType.Resource + " in " + scope
}

// kubeIsMultiScope reports whether the namespace scope selects several
// namespaces (a glob or a Space-marked comma list).
func kubeIsMultiScope(scope string) bool { return strings.ContainsAny(scope, "*?[,") }

// shortDrillWarning is the status-line half of the empty-drill feedback: the
// eye-catching part, while the body carries the full explanation.
func (m *Model) shortDrillWarning() string {
	return "⚠ " + m.drillFor + ": no " + m.curType.Resource + " — see the note below"
}

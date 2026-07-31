package kube

import (
	"path"
	"strings"
)

// The namespace scope accepts glob patterns (US-requested, 2026-07-06): a
// scope like "staging-*" means every namespace it matches. The Kubernetes API
// only lists one-or-all namespaces, so a pattern is resolved by listing across
// all namespaces and filtering locally.

// IsNamespacePattern reports whether the scope is a glob (path.Match syntax:
// '*', '?', '[…]') or a comma-separated multi-namespace selection ("a,b" —
// Space-marked in the namespace picker) rather than a single namespace name.
func IsNamespacePattern(ns string) bool { return strings.ContainsAny(ns, "*?[,") }

// MatchNamespace reports whether a namespace matches the scope: each
// comma-separated part is a glob (an exact name matches itself). A malformed
// part matches nothing (the view goes empty — never a wrong scope silently
// widened).
func MatchNamespace(pattern, ns string) bool {
	for _, part := range strings.Split(pattern, ",") {
		if ok, err := path.Match(strings.TrimSpace(part), ns); err == nil && ok {
			return true
		}
	}
	return false
}

// namespaceScope splits the UI scope into the API-server namespace ("" = all)
// and the local glob filter ("" = none).
func namespaceScope(namespace string) (apiNS, pattern string) {
	if IsNamespacePattern(namespace) {
		return "", namespace
	}
	return namespace, ""
}

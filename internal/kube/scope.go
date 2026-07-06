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
// '*', '?', '[…]') rather than a single namespace name.
func IsNamespacePattern(ns string) bool { return strings.ContainsAny(ns, "*?[") }

// MatchNamespace reports whether a namespace matches the glob pattern. A
// malformed pattern matches nothing (the view goes empty — never a wrong
// scope silently widened).
func MatchNamespace(pattern, ns string) bool {
	ok, err := path.Match(pattern, ns)
	return err == nil && ok
}

// namespaceScope splits the UI scope into the API-server namespace ("" = all)
// and the local glob filter ("" = none).
func namespaceScope(namespace string) (apiNS, pattern string) {
	if IsNamespacePattern(namespace) {
		return "", namespace
	}
	return namespace, ""
}

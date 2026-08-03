package ui

// Row filtering across EVERY visible column (owner request 2026-08-03: the
// '/' filter only looked at namespace/name, so filtering by a node's VERSION
// or a pod's IMAGE was impossible). One predicate shared by every filterable
// table so '/' behaves identically everywhere (invariant 0).

import (
	"strings"

	xansi "github.com/charmbracelet/x/ansi"
)

// filterTerms splits a query into lowercase AND-terms. Kubernetes names never
// contain spaces, so a space is free to mean "and also": "1.30 ready" keeps
// the rows matching both, each in any column.
func filterTerms(query string) []string {
	return strings.Fields(strings.ToLower(query))
}

// rowHaystack builds the lowercase text a row is matched against: its
// identity (always, even when the NAMESPACE column is hidden) plus every
// rendered cell. ANSI is stripped so a styled cell (gauges, badges) can never
// swallow a match.
func rowHaystack(identity string, cells ...string) string {
	var b strings.Builder
	b.Grow(len(identity) + 16*len(cells))
	b.WriteString(strings.ToLower(identity))
	for _, c := range cells {
		if c == "" {
			continue
		}
		b.WriteByte(' ')
		if strings.IndexByte(c, 0x1b) >= 0 {
			c = xansi.Strip(c)
		}
		b.WriteString(strings.ToLower(c))
	}
	return b.String()
}

// matchesTerms reports whether the haystack contains every term.
func matchesTerms(haystack string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(haystack, t) {
			return false
		}
	}
	return true
}

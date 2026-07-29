package ui

// Numeric-aware sorting (owner bug 2026-07-29: sorting Deployments by
// AVAILABLE looked random). Columns without a dedicated less used to compare
// their rendered cells as STRINGS — "10" < "2" — so every numeric column
// ordered lexicographically. cellLess understands what the cells render:
// plain numbers, kubectl quantities (126m, 706Mi, 1.5Gi), percentages (31%)
// and ready fractions (1/2); anything else falls back to case-insensitive
// text. Ascending puts value-less cells ("-", "—") last.

import (
	"strconv"
	"strings"
)

// cellLess is the default column comparator (used when a column defines no
// dedicated less): numeric when both cells parse, textual otherwise.
func cellLess(a, b string) bool {
	av, aok := sortValue(a)
	bv, bok := sortValue(b)
	switch {
	case aok && bok:
		if av != bv {
			return av < bv
		}
		return strings.ToLower(a) < strings.ToLower(b)
	case aok != bok:
		return aok // numeric cells first; "-"/"—"/text sort last ascending
	default:
		return strings.ToLower(a) < strings.ToLower(b)
	}
}

// quantitySuffixes maps kubectl-style suffixes to multipliers (CPU millicores
// and binary/decimal byte units — enough to ORDER cells of one column).
var quantitySuffixes = map[string]float64{
	"m": 1e-3,
	"k": 1e3, "K": 1e3, "Ki": 1024,
	"M": 1e6, "Mi": 1 << 20,
	"G": 1e9, "Gi": 1 << 30,
	"T": 1e12, "Ti": 1 << 40,
}

// sortValue parses a rendered cell into a comparable number.
// ok=false for empty/dash/textual cells.
func sortValue(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || s == "—" {
		return 0, false
	}
	// Ready fraction "1/2" → the fraction (0/0 → 0).
	if num, den, found := strings.Cut(s, "/"); found {
		n, errN := strconv.ParseFloat(num, 64)
		d, errD := strconv.ParseFloat(den, 64)
		if errN == nil && errD == nil {
			if d == 0 {
				return 0, true
			}
			return n / d, true
		}
		return 0, false
	}
	// Percentage "31%".
	if v, ok := strings.CutSuffix(s, "%"); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f, true
		}
		return 0, false
	}
	// Plain number.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, true
	}
	// Quantity with a unit suffix ("126m", "706Mi", "1.5Gi").
	for _, l := range []int{2, 1} {
		if len(s) > l {
			if mult, ok := quantitySuffixes[s[len(s)-l:]]; ok {
				if f, err := strconv.ParseFloat(s[:len(s)-l], 64); err == nil {
					return f * mult, true
				}
			}
		}
	}
	return 0, false
}

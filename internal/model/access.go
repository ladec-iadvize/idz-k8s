package model

// Access (US15, FR-032): what the operator's credentials can read, from the
// server's own answer (SelfSubjectRulesReview) — never guessed.

// AccessRule is one allowed-action row.
type AccessRule struct {
	Verbs     []string
	Groups    []string
	Resources []string
	Names     []string // resourceNames restriction, if any
}

// AccessReport summarizes read access in one namespace.
type AccessReport struct {
	Namespace  string // namespace the review was evaluated in
	Incomplete bool   // the server may return a partial rule set
	Evaluation string // server-side evaluation error, verbatim
	Rules      []AccessRule
	Unlistable []string // browsable type keys the credentials cannot list
}

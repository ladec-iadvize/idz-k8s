package model

// Sizing (US6, FR-023): advisory right-sizing derived from observed usage vs
// configured requests/limits. Verdicts are advisory only — the tool never
// applies anything — and are NEVER produced without real observed data
// (SC-013: insufficient data → SizingNoData, no fabricated figures).

// SizingVerdict classifies one resource (CPU or memory) of a workload.
type SizingVerdict int

const (
	SizingNoData    SizingVerdict = iota // insufficient observation → no recommendation
	SizingNoRequest                      // no request configured to compare against
	SizingOK
	SizingOver  // over-provisioned: observed peak far below the request
	SizingUnder // under-provisioned / at risk: at/above request or near the limit
)

// Sizing thresholds (single source of truth, used by tests and the UI copy).
const (
	SizingOverFrac  = 0.5 // peak below 50% of the request → over-provisioned
	SizingLimitFrac = 0.9 // peak at ≥90% of the limit → at risk (OOM/throttling)
)

// ResourceSizing is the observed-vs-configured picture for one resource kind.
// Values are per pod: cores for CPU, bytes for memory.
type ResourceSizing struct {
	Kind           MetricKind
	Avg, Peak      float64 // observed over the window; meaningful only when HasData
	HasData        bool
	Request, Limit float64 // configured (0 = unset)
	Verdict        SizingVerdict
}

// SizingAdvice bundles both resources for one workload.
type SizingAdvice struct {
	Workload  string // e.g. "Deployment/back"
	Namespace string
	Pods      int // pods observed
	CPU       ResourceSizing
	Memory    ResourceSizing
}

// EvaluateSizing derives the advisory verdict from the observed data. Order
// matters: no data beats everything (never invent), risk beats savings.
func EvaluateSizing(rs ResourceSizing) ResourceSizing {
	switch {
	case !rs.HasData:
		rs.Verdict = SizingNoData
	case rs.Request <= 0:
		rs.Verdict = SizingNoRequest
	case rs.Limit > 0 && rs.Peak >= SizingLimitFrac*rs.Limit:
		rs.Verdict = SizingUnder
	case rs.Avg >= rs.Request:
		rs.Verdict = SizingUnder
	case rs.Peak < SizingOverFrac*rs.Request:
		rs.Verdict = SizingOver
	default:
		rs.Verdict = SizingOK
	}
	return rs
}

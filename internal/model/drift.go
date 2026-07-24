package model

// Drift is one field whose live value differs from the last-applied
// configuration (US16, FR-033 — observation only).
type Drift struct {
	Path    string // dotted field path, e.g. spec.replicas
	Applied string // value in the last-applied baseline
	Live    string // value on the cluster ("<absent>" when removed)
}

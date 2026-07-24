package model

// PostureFinding is one advisory compliance finding (US13, FR-030): a
// concrete object/field violating a common security or reliability practice.
// Advisory — derived only from observed configuration.
type PostureFinding struct {
	Rule      string      // rule label, e.g. "privileged container"
	Severity  HealthLevel // HealthWarning or HealthError
	Namespace string
	Name      string // object name (pod, namespace, secret)
	Container string // set for container-level findings
	Detail    string // the concrete field/value behind the finding
}

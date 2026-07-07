package model

// Connectivity (US14, FR-031): per-pod NetworkPolicy summary — which policies
// select the pod and what they allow. Derived only from the policies'
// configuration; cross-namespace effects are summarized, never resolved.

// PolicyRule is one allow rule of a selecting policy, summarized for display.
type PolicyRule struct {
	Policy string   // policy name
	Peers  []string // e.g. "pods app=front (same ns)", "namespaces env=prod", "10.0.0.0/8"
	Ports  []string // e.g. "TCP/8080"; empty = all ports
}

// ConnectivityReport is the full per-pod (or pod-template) summary.
type ConnectivityReport struct {
	Subject   string // e.g. "pod demo/back-abc" or "pods of Deployment/back"
	Namespace string
	Policies  []string // names of the selecting policies

	// Restricted is per direction: true when at least one selecting policy
	// declares that policyType — only the listed rules are then allowed.
	IngressRestricted bool
	EgressRestricted  bool
	Ingress           []PolicyRule
	Egress            []PolicyRule
}

<!--
Sync Impact Report
==================
Version change: [template/unversioned] → 1.0.0
Ratification: initial adoption of the idz-k8s constitution.

Modified principles: n/a (initial ratification)
Added principles:
  - I. Performance & Resource Efficiency
  - II. Long-Term Maintainability
  - III. Development Best Practices
  - IV. Security by Default
Added sections:
  - Operational Constraints
  - Development Workflow
  - Governance

Templates requiring updates:
  - ✅ .specify/templates/plan-template.md (Constitution Check gate references
       the four principles; no structural change required — gate is generic)
  - ✅ .specify/templates/spec-template.md (no mandatory-section change)
  - ✅ .specify/templates/tasks-template.md (task categories already cover
       testing, security, performance, and observability work)
  - ✅ .specify/templates/checklist-template.md (no change required)

Follow-up TODOs: none.
-->

# idz-k8s Constitution

## Core Principles

### I. Performance & Resource Efficiency

Every workload MUST declare CPU and memory requests and limits; unbounded pods
are not allowed to merge. Manifests MUST target measurable objectives (latency,
throughput, or resource headroom) rather than "feels fast". Autoscaling and
resource sizing decisions MUST be backed by observed metrics, not guesses.
Changes that regress a documented performance target MUST NOT ship without an
explicit, recorded justification.

Rationale: A Kubernetes platform is a shared, cost-bearing resource. Undeclared
or over-provisioned workloads degrade neighbours and inflate spend; performance
that is not measured cannot be defended or improved.

### II. Long-Term Maintainability

Configuration MUST favour clarity and reuse over cleverness: no duplicated
manifests where a base + overlay (Kustomize/Helm) applies, no copy-paste values
that a shared source can hold. Every non-obvious decision MUST be documented at
the point of use. Dependencies (charts, base images, controllers) MUST be pinned
to explicit versions and kept current on a defined cadence. Dead or unused
resources MUST be removed, not commented out.

Rationale: Infrastructure outlives the person who wrote it. Simplicity, pinned
versions, and inline rationale are what let the next operator change it safely
years later.

### III. Development Best Practices

All infrastructure changes MUST flow through version control and peer review —
no manual, undocumented changes to running clusters (GitOps as the source of
truth). Manifests MUST be validated (lint, schema/policy checks, dry-run) in CI
before merge. Changes SHOULD be small, reversible, and independently deployable.
Every change MUST be reproducible from the repository alone; environments differ
only by declared, version-controlled configuration.

Rationale: Reproducibility and review are what separate an engineered platform
from an accreted one. If the cluster can only be rebuilt from someone's memory,
it cannot be operated reliably.

### IV. Security by Default

Workloads MUST run with least privilege: no privileged containers, no
unnecessary capabilities, non-root by default, and read-only root filesystems
where feasible. Secrets MUST NOT be committed in plaintext; they MUST be managed
through an approved secrets mechanism. Network access MUST be restricted by
default (deny-by-default network policies) and opened only as required. Base
images and dependencies MUST be scanned for known vulnerabilities, and critical
findings MUST block release.

Rationale: A cluster is a high-value target and a blast radius. Security applied
by default — least privilege, no plaintext secrets, restricted networking — is
far cheaper than remediating a breach after the fact, and aligns with iAdvize's
obligation to protect client data.

## Operational Constraints

- Every deployed workload MUST expose health signals (liveness/readiness) and
  emit logs and metrics sufficient to diagnose incidents without shell access.
- Production changes MUST be observable: a change is not "done" until its effect
  can be seen in monitoring.
- Client data, KPIs, and internal information handled by workloads stay within
  the authorized scope and MUST NOT be exposed outside it.
- Never fabricate capacity, cost, or SLA figures; when a figure is unknown, flag
  it explicitly (⚠) rather than estimating.

## Development Workflow

- Changes are proposed via pull request against version control; direct cluster
  edits are permitted only for documented emergency break-glass, and MUST be
  reconciled back into the repository immediately after.
- CI MUST run lint, policy/security checks, and a dry-run render before a change
  is eligible to merge; a red gate blocks merge.
- At least one reviewer MUST approve, and the review MUST explicitly consider the
  four Core Principles above.
- Rollback MUST be possible for any change; a change without a rollback path
  requires recorded justification.

## Governance

This constitution supersedes ad-hoc practices for the idz-k8s project. All pull
requests and reviews MUST verify compliance with the Core Principles; deviations
MUST be justified in the Complexity Tracking section of the relevant plan and
approved by a maintainer.

Amendments MUST be proposed via pull request, include a rationale and, where
behaviour changes, a migration note. Versioning follows semantic rules:
MAJOR for backward-incompatible governance or principle removal/redefinition,
MINOR for a new principle or materially expanded guidance, PATCH for
clarifications and non-semantic refinements.

Compliance is reviewed at least at each significant platform change and whenever
the constitution is amended. Runtime and agent guidance files MUST stay
consistent with this document; on conflict, this constitution prevails.

**Version**: 1.0.0 | **Ratified**: 2026-07-02 | **Last Amended**: 2026-07-02

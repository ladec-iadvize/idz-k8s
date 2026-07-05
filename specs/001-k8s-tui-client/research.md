# Research: Kubernetes TUI Overview Client (read-only)

**Feature**: 001-k8s-tui-client | **Date**: 2026-07-03

This supersedes the pre-pivot research. Scope is the clarified v1: a strictly
read-only overview/debug tool (P1+P2 stories), Prometheus as the single metrics
source, Helm read-only, dynamic discovery incl. CRDs. No `NEEDS CLARIFICATION`
remain (spec clarify sessions resolved refresh cadence, CRD scope, secret policy,
multi-cluster, v1 scope, and the metrics source/window).

---

## D1 — Implementation language: Go

**Decision**: Go (1.23+).

**Rationale**: Native Kubernetes ecosystem — `client-go`, discovery, informers,
and the Helm v3 SDK are all Go and first-class. Single static binary is trivial
to distribute to operators (maintainability/reproducibility). The reference tool
(k9s) is Go.

**Alternatives**: Rust (`kube-rs`, thinner discovery/Helm story); Python
(Textual, weaker packaging + K8s/Helm libraries). Rejected for a K8s+Helm client.

---

## D2 — Kubernetes access: client-go dynamic + discovery + informers (read-only)

**Decision**: Dynamic client + discovery client as the primary path; typed
clients for convenience on well-known kinds; kubeconfig via `clientcmd`. **Only
read verbs** (`get`/`list`/`watch`, and `pods/log`) are ever issued.

**Rationale**: FR-002 needs all resource types incl. CRDs → discovery enumerates
served GVRs and the dynamic client reads any of them. Read-only construction
(never wiring create/update/patch/delete/exec) is the simplest way to guarantee
SC-006 (zero mutating calls) and Principle IV.

**Alternatives**: typed-only (can't see CRDs → fails FR-002); raw REST (reinvents
auth/discovery). Rejected.

---

## D3 — Refresh: shared informers with a configurable resync

**Decision**: Back active views with shared informers (watch + local cache);
surface changes to the UI on a configurable tick, default ~5 s (FR-006). Fall
back to interval `LIST` where watch is unavailable.

**Rationale**: Clarified as configurable periodic refresh (~5 s), not sub-second
streaming. Informers are API-friendly (one watch, not repeated lists) → Principle
I and SC-005. UI diffs the cache on the tick.

**Alternatives**: poll everything (hammers API); pure event-driven (rejected by
clarification, noisier). 

---

## D4 — TUI framework: Bubble Tea (+ Bubbles + Lip Gloss)

**Decision**: Charm stack — Bubble Tea runtime, Bubbles widgets (table, viewport,
textinput, help, spinner), Lip Gloss styling.

**Rationale**: Needs mouse click/wheel (FR-011), full keyboard + discoverable help
(FR-008/FR-010), rich visuals (FR-019), and no-color degradation (FR-022). Bubble
Tea has first-class mouse support; Bubbles ships `help` and `table`; Lip Gloss
adapts to color capability (honors `NO_COLOR`). The pure update function is
testable with `teatest` (Principle II).

**Alternatives**: tview/tcell (k9s) — weaker mouse/custom-rendering, harder to
unit-test; termui/gocui — too low-level. Rejected.

---

## D5 — Metrics: Prometheus as the single source (instantaneous + 1h history)

**Decision**: Use the Prometheus HTTP API (`client_golang/api` + `v1`): instant
queries for gauges/top-consumers/node "used", and `query_range` over a rolling
**last-1-hour** window for trend charts. The Prometheus endpoint is configurable
(CLI flag / config). An availability probe drives the "unavailable" state.

**Rationale**: Clarified — trend charts require a historical backend, window = 1h,
and Prometheus is the single source (no metrics-server). One integration, one
source of truth, consistent numbers between gauge and chart. Honoring the
constitution's data-integrity rule: when Prometheus is unreachable, show
"unavailable" rather than estimate (FR-021).

**Alternatives**: metrics-server (instantaneous only, no history → can't do trend
charts, and a second source); two-source (metrics-server + Prometheus) —
redundant given Prometheus is required. Rejected per clarification.

**Open (plan-detail, non-blocking)**: exact PromQL and how the Prometheus URL is
discovered/entered (flag/config vs in-cluster service). Captured as tasks.

---

## D6 — Helm release state: read-only via the Helm v3 storage driver

**Decision**: Read releases and history using `helm.sh/helm/v3` (`action.NewList`,
`action.NewHistory`) configured with a `RESTClientGetter` from the active
kubeconfig and the cluster's release storage (Secrets by default). **No** install/
upgrade/rollback/uninstall actions are ever constructed.

**Rationale**: Deployments are Helm-based (spec). Helm stores releases in-cluster;
the SDK's read actions expose name/namespace/chart/version/revision/status/history
(FR-029) using only read access. Not wiring the mutating actions guarantees the
read-only property.

**Alternatives**: shelling out to the `helm` binary (extra runtime dependency,
harder to test); reading release Secrets and decoding by hand (re-implements the
SDK, brittle across storage drivers). Rejected.

---

## D7 — Large-scale rendering: virtualization over the informer cache

**Decision**: Render only the visible window of rows/nodes from the informer
cache; filter/sort/graph operate on the cache, not re-fetched data. Topology
groups by node and virtualizes long pod lists.

**Rationale**: SC-005 (≥ 5,000 pods / 100 nodes). Per-frame cost independent of
size; no per-keystroke API calls (Principle I).

---

## D8 — Debug views: derivation strategy

**Decision**: Build the debug views by deriving from cached API objects, not new
data sources:
- **Dependency graph** (FR-026): from `ownerReferences` + Service/Endpoints +
  Ingress backends.
- **Failure diagnostics** (FR-027): from pod `status.containerStatuses`
  (`lastState.terminated.reason` = OOMKilled/exit code, `restartCount`) and
  evicted pod status.
- **Scheduling & capacity** (FR-028): pending reasons from pod conditions/events;
  node `allocatable` vs summed pod `requests`; "used" from Prometheus.
- **Events timeline** (FR-014): from the Events API, time-ordered, filterable;
  window bounded by cluster event retention, shown honestly.

**Rationale**: These are read-only projections of data already cached — cheap,
consistent, and testable with fakes. Aligns with Principle I/II.

---

## D9 — Keybindings: familiar, non-exotic, discoverable

**Decision**: Conventional bindings (arrows/PgUp/PgDn/Home/End, `/` filter, `Esc`
back, `?` help, `q` quit, `Enter` open, `:` jump), plus single letters for the
debug views (topology, events, graph, diagnostics, scheduling, helm). Defined once
in `internal/ui/keys` with help metadata (FR-009/FR-010). No mutating bindings
exist (read-only).

**Rationale**: FR-009 forbids exotic combos; mirrors kubectl/k9s/less conventions
(SC-004). Central keymap keeps help in sync.

---

## D10 — Testing strategy

**Decision**: Unit (`testify`) for config/model/keymap/PromQL builders/degradation;
integration against fake clientset + `envtest` (discovery/CRDs), a stub Prometheus
HTTP server, and a fake Helm storage; `teatest` golden/interaction tests for TUI
flows and keyboard/mouse parity. A test asserts **zero mutating verbs** are issued
(SC-006).

**Rationale**: Verifies each read-only source without a live cluster/terminal
(Principle III); makes the read-only guarantee and keyboard/mouse parity
test-enforced.

---

## Summary of resolved decisions

| # | Area | Decision |
|---|------|----------|
| D1 | Language | Go 1.23+ |
| D2 | K8s access | client-go dynamic + discovery + informers, read verbs only |
| D3 | Refresh | shared informers, configurable resync, default ~5 s |
| D4 | TUI | Bubble Tea + Bubbles + Lip Gloss |
| D5 | Metrics | Prometheus single source; instant + 1h `query_range` |
| D6 | Helm | Helm v3 SDK read-only (list/history); no mutating actions |
| D7 | Scale | viewport virtualization over informer cache |
| D8 | Debug views | derived from cached API objects |
| D9 | Keybindings | familiar, centralized, help-driven, no mutating keys |
| D10 | Testing | unit + fake/envtest/stub-Prometheus/fake-Helm + teatest; zero-mutation assertion |

All decisions are consistent with the constitution (performance, maintainability,
best practices, security/read-only) and the clarified v1 scope. No blocking
unknowns for Phase 1.

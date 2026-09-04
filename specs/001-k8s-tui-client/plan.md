# Implementation Plan: Kubernetes TUI Overview & Admin Client

**Branch**: `001-k8s-tui-client` | **Date**: 2026-07-03 | **Spec**: [spec.md](./spec.md)

**Updated**: 2026-09-04 — realigned with the v3 administration pivot (2026-07-24)
and everything shipped through v3.11.0. The original read-only wording is kept
below only where it documents history.

**Input**: Feature specification from `specs/001-k8s-tui-client/spec.md`

## Summary

A terminal UI to get an overview of, debug, **and (v3) administer** a Kubernetes
cluster. It browses
any resource type (built-in + CRDs), streams logs, and turns cluster state into
debugging views: graphical usage (gauges + last-1-hour trend charts), a topology
of pods across nodes, an events timeline, an ownership/dependency graph, workload
failure diagnostics (CrashLoop/OOMKilled/evictions), scheduling & capacity, and a
Helm release overview with per-revision detail. Fully keyboard-operable with familiar shortcuts,
also mouse-clickable. *(v3)* Admin actions — edit (merge apply), scale (single
and bulk over marks), rolling restart, delete, cordon/uncordon, suspend/resume/
trigger CronJobs, port-forward, shell-into-pod, Helm rollback/uninstall — all
behind a mandatory confirmation step (FR-012 v3); node drain stays out of scope.

Technical approach: a single Go application on `client-go` (dynamic client +
discovery for CRDs, informer caches, RBAC-bound) and the Charm
TUI stack (Bubble Tea + Bubbles + Lip Gloss) with custom Unicode charts (research
D5 — ntcharts was replaced). **Prometheus
is the single metrics source** for both instantaneous usage and the rolling
1-hour history. Helm release state is read from its in-cluster
storage; rollback/uninstall go through the Helm SDK under the confirmation
contract. One cluster context is active at a time; data refreshes on a configurable
interval (default ~5 s) with watch-driven updates via shared informers.

**v1 scope (clarified)**: P1+P2 user stories — US1 inspection, US2 graphical debug
views, US3 keyboard, US4 topology, US5 events timeline, US7 mouse, US9 dependency
graph, US10 failure diagnostics, US11 scheduling & capacity, US12 Helm overview.
P3 stories (sizing recommendations, customizable views, posture, connectivity,
access/RBAC view, read-only diff) are **out of v1** (backlog).

## Technical Context

**Language/Version**: Go 1.23+

**Primary Dependencies**:
- `k8s.io/client-go`, `k8s.io/apimachinery`, `k8s.io/api`, `k8s.io/cli-runtime` — kubeconfig, dynamic + typed clients, discovery (incl. CRDs), informers, admin verbs (v3) + SPDY exec/port-forward
- `github.com/charmbracelet/bubbletea` / `bubbles` / `lipgloss` — TUI runtime, widgets, styling (mouse + graceful degradation)
- ~~`github.com/NimbleMarkets/ntcharts`~~ — replaced by custom Unicode charts (research D5)
- `github.com/prometheus/client_golang/api` (+ `api/prometheus/v1`) — Prometheus HTTP API / PromQL, single metrics source
- `helm.sh/helm/v3` — Helm release list/history/detail via its storage driver; rollback/uninstall (v3, confirmed)
- `github.com/spf13/cobra` — CLI entry point, flags, help

**Storage**: Local YAML file under the OS user config dir (`$XDG_CONFIG_HOME/idz-k8s/config.yaml`) for a small set of preferences (refresh interval, Prometheus endpoint, theme, last context). No credentials or secret values persisted. (Saved/customizable views are P3 → not in v1.)

**Testing**: `go test` + `testify`; `teatest` for TUI interaction/golden tests; client-go **fake clientset** + `envtest` for the kube layer; a stub Prometheus HTTP server and a fake Helm storage for those integrations.

**Target Platform**: Cross-platform terminal (Linux, macOS), modern emulators with mouse reporting; graceful degradation without color/mouse/rich rendering.

**Project Type**: Single-project CLI/TUI application (no server component).

**Performance Goals**:
- Resource lists and topology interactive with ≥ 5,000 pods across ≥ 100 nodes (virtualized rendering, informer cache).
- Data refreshed on a configurable interval, default ~5 s (FR-006).
- Locate a pod + open details < 15 s; logs within 3 interactions (SC-001/SC-002).

**Constraints**:
- ~~Strictly read-only~~ **superseded by v3 (2026-07-24)**: admin operations exist (kube admin.go/portforward.go, helm rollback/uninstall) and every mutation requires an explicit UI confirmation (FR-012 v3, SC-006 v3).
- 100% keyboard/mouse parity; keyboard-complete without a mouse (FR-008).
- Runs within the operator's RBAC; no privilege escalation; RBAC denials on admin actions surfaced explicitly; no secret persistence (FR-015, FR-018).
- Trend/usage data requires Prometheus; when unreachable, show explicit "unavailable" (FR-019/FR-021).
- Graceful degradation in no-color / no-rich-rendering / small-window terminals.
- One active cluster context at a time (FR-003).

**Scale/Scope**: 10 v1 user stories (P1+P2); all API resource types via dynamic discovery incl. CRDs; single operator per running instance; Helm-managed deployments.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

The constitution targets the platform; principles are applied to this client
that observes **and (v3) administers** a cluster.

| Principle | Application | Status |
|-----------|-------------|--------|
| I. Performance & Resource Efficiency | A well-behaved API client: informer caches (one watch, not repeated lists), virtualized rendering, bounded configurable refresh, bounded Prometheus queries (1-hour window). Targets are measurable (5,000 pods / 100 nodes, refresh interval). | ✅ PASS |
| II. Long-Term Maintainability | Single Go module, layered (`kube` / `metrics` / `helm` / `model` / `ui`), deps pinned in `go.mod`+sums, decisions recorded in research.md. View logic driven by data-declared columns, not duplicated per type. | ✅ PASS |
| III. Development Best Practices | All code in VCS; CI runs fmt/vet/lint/test/build before merge; small reversible changes; binary reproducible from source. | ✅ PASS |
| IV. Security by Default | *(updated v3)* Least privilege via the operator's own RBAC — never elevates; every mutation behind an explicit confirmation (FR-012 v3, test-enforced SC-006) and attributed via the `idz-k8s` field manager; persists no credentials/secrets; masks secrets by default. | ✅ PASS |

Data-integrity alignment (constitution): trend charts, capacity "used", and top
consumers are sourced from real Prometheus data and show an explicit "unavailable"
state instead of estimating — never fabricated. No violations → **Complexity
Tracking not required**.

## Project Structure

### Documentation (this feature)

```text
specs/001-k8s-tui-client/
├── plan.md              # This file
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/           # Phase 1 output
│   ├── cli-interface.md
│   ├── config-schema.md
│   ├── keybindings.md
│   └── data-sources.md   # K8s verbs (reads + confirmed admin) + Prometheus + Helm surface
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 (/speckit-tasks — NOT created here)
```

### Source Code (repository root)

```text
cmd/
└── idz-k8s/                 # cobra root; wires config + clients + TUI

internal/
├── config/                  # load/save small preferences (YAML); defaults; safe fallback
├── kube/                    # client-go: kubeconfig, REST config, dynamic client,
│   │                        #   discovery (incl. CRDs), informers, reads + admin ops (v3)
│   ├── admin.go             # (v3) edit/scale/restart/delete/cordon/suspend/trigger — confirmed
│   ├── portforward.go       # (v3) port-forward
│   ├── discovery.go         # enumerate all served resource types (CRDs)
│   ├── client.go            # dynamic/typed read clients, context switching
│   ├── informers.go         # shared informer cache + configurable-interval surfacing
│   ├── logs.go              # single + merged multi-pod log tail (FR-005/FR-034)
│   ├── graph.go             # ownership/routing edges (Deploy→RS→Pod, Svc→Endpoints)
│   ├── diagnostics.go       # restart counts, termination reasons (OOMKilled), evictions
│   └── scheduling.go        # pending reasons + node allocatable/requested
├── metrics/                 # Prometheus client (single source): instantaneous + 1h series
│   ├── prometheus.go        # query/query_range, availability probe
│   └── queries.go           # PromQL for usage, node "used", top consumers
├── helm/                    # Helm release list + history + detail (storage driver); rollback/uninstall (v3)
├── model/                   # toolkit-agnostic domain types (Resource, Node, Event,
│                            #   MetricSeries, HelmRelease, DependencyEdge, SchedulingReason)
├── ui/                      # Bubble Tea program
│   ├── app.go               # root model, routing, global keymap, mouse handling
│   ├── keys/                # keybinding definitions + help metadata
│   ├── views/               # list, detail, logs, topology, events-timeline,
│   │                        #   dependency-graph, diagnostics, scheduling, helm, top-consumers
│   ├── components/          # table, gauge, sparkline/trend chart, status badge
│   └── theme/               # lipgloss styles + no-color / degraded fallbacks
└── telemetry/               # local structured logging (file/stderr), no external calls

tests/
├── unit/                    # config, model, keymap, degradation, PromQL builders
├── integration/             # fake clientset / envtest; stub Prometheus; fake Helm storage
└── tui/                     # teatest interaction/golden tests, keyboard/mouse parity
```

**Structure Decision**: Single Go project (CLI/TUI, no server). Layered split
isolates the three data sources — `kube` (API), `metrics` (Prometheus),
`helm` (release storage) — behind a toolkit-agnostic `model`, so each is testable
with fakes/stubs without a terminal or a live cluster (Principle II/III). The `ui`
layer never talks to a data source directly; it consumes `model`.

## vNext batch (specified 2026-09-04 — open, see tasks.md Phases N1–N5)

- **Vim mode (FR-038)**: a second keymap in `internal/ui/keys/` selected by a
  launch flag; no new dependency. The default keymap must stay byte-identical
  without the flag; `TestEveryBindingHasHelp` extends to both maps.
- **Topology rework (FR-013 vNext)**: pressure heatmap/grid + collapsed nodes +
  observed usage. Adds per-node/per-pod usage to the topology fetch (existing
  Prometheus layer, D5 constraints apply — bounded queries, explicit
  unavailable). Mouse geometry of the new layout ships with its tests.
- **Datadog deep-link (FR-039)**: config fields (`datadogSite` default
  `datadoghq.eu`, query template) + a URL builder + OS browser open
  (`open`/`xdg-open`). Zero network calls from the tool; headless fallback
  prints the URL. No credentials stored (FR-015/invariant 3 unchanged).
- **N4/N5 audits**: engineering passes (code cleanup à la 2026-07-12;
  view-consistency sweep + backlog grooming) — tracked as tasks, no FR.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| ~~CI gate deferred (T003/T004)~~ **RESOLVED 2026-07-05** | The repo now lives on GitHub (ladec-iadvize/idz-k8s); `.github/workflows/ci.yml` runs gofmt/vet/golangci-lint/build/test on every push and PR | Deviation closed — the constitution's Principle III gate is in force. |

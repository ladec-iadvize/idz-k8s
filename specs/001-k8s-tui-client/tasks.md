---

description: "Task list for Kubernetes TUI Overview Client (read-only) — v1 (P1+P2)"
---

# Tasks: Kubernetes TUI Overview Client (read-only)

**Input**: Design documents from `specs/001-k8s-tui-client/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/

**Tests**: Included. The project constitution (Principle III — Development Best
Practices) requires CI test gates, and SC-006 requires a test that asserts the
tool issues zero mutating operations. Test tasks are therefore part of scope.

**Scope**: v1 = P1 + P2 user stories (US1, US2, US3, US4, US5, US7, US9, US10,
US11, US12). P3 stories (US6 sizing, US8 customizable views, US13 posture, US14
connectivity, US15 access view, US16 diff) are deferred to backlog and are NOT
in this task list.

**Format**: `[ID] [P?] [Story?] Description with file path`

- **[P]**: parallelizable (different files, no dependency on incomplete tasks)
- **[Story]**: user story label for story-phase tasks

## Path Conventions

Single Go project: `cmd/idz-k8s/`, `internal/…`, `tests/…` at repository root (per plan.md).

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Project initialization and basic structure

- [X] T001 Create Go module and directory tree per plan.md (`go.mod`, `cmd/idz-k8s/`, `internal/{config,kube,metrics,helm,model,ui,telemetry}/`, `tests/{unit,integration,tui}/`)
- [X] T002 [note: custom Unicode charts replaced ntcharts — design deviation recorded in research D5] Add pinned dependencies to `go.mod` (client-go, apimachinery, api, cli-runtime; charmbracelet bubbletea/bubbles/lipgloss; NimbleMarkets/ntcharts; prometheus/client_golang; helm.sh/helm/v3; spf13/cobra) and commit `go.sum`
- [X] T003 [P] [deferral lifted 2026-07-05 once the git repo existed] Configure formatting/linting (`gofmt`, `go vet`, `golangci-lint`) in repo root config files
- [X] T004 [P] [deferral lifted 2026-07-05 once the git repo existed] Add CI workflow running fmt/vet/lint/`go test ./...`/build in `.github/workflows/ci.yml` (Principle III gate)
- [X] T005 [P] Cobra root command + flag wiring per `contracts/cli-interface.md` in `cmd/idz-k8s/main.go`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Core infrastructure that MUST complete before ANY user story

**⚠️ CRITICAL**: No user story work can begin until this phase is complete

- [X] T006 [P] Define toolkit-agnostic domain types (ResourceType, ResourceObject, StatusSummary, Node, ContainerDiagnostic, DependencyEdge, SchedulingReason, Event, MetricSeries/MetricSample, HelmRelease, ColumnDef) in `internal/model/`
- [X] T007 [P] Implement preferences load/save (YAML, defaults, safe fallback, atomic write) per `contracts/config-schema.md` in `internal/config/config.go`
- [X] T008 Implement kubeconfig + REST config loading and single-active-context switching in `internal/kube/client.go` (FR-001, FR-003)
- [X] T009 Implement dynamic client + discovery of all served resource types incl. CRDs in `internal/kube/discovery.go` (FR-002)
- [ ] T010 [post-v1 optimization — periodic refresh satisfies FR-006 functionally] Implement shared informer cache with configurable refresh tick (default ~5 s) in `internal/kube/informers.go` (FR-006)
- [X] T011 [P] Implement read-only access guard (only read verbs are ever wired) and a reusable "record issued verbs" hook for the zero-mutation assertion in `internal/kube/readonly.go` (FR-012, SC-006)
- [X] T012 [P] Implement local structured logging (file/stderr, no external calls, no secret values) in `internal/telemetry/log.go`
- [X] T013 Implement Bubble Tea root model, view routing, global keymap load, and mouse capture toggle in `internal/ui/app.go`
- [X] T014 [P] Implement central keymap + help metadata source per `contracts/keybindings.md` in `internal/ui/keys/keys.go`
- [X] T015 [P] Implement theme + no-color/degradation and base components (virtualized table, status badge) in `internal/ui/theme/` and `internal/ui/components/`
- [X] T016 Implement connection status + resilience (unreachable cluster, dropped connection, expired credentials) surfaced in `internal/ui/app.go` and `internal/kube/client.go` (FR-016)
- [X] T017 [P] Build shared test harness: fake clientset, `envtest` bootstrap, stub Prometheus HTTP server, fake Helm storage in `tests/integration/harness.go`

**Checkpoint**: Foundation ready — user stories can begin.

---

## Phase 3: User Story 1 - Read-only inspection (Priority: P1) 🎯 MVP

**Goal**: Browse any resource type (incl. CRDs), read details/events, stream logs, switch namespace/context — with no way to mutate anything.

**Independent Test**: Point at a cluster, browse a namespace, open a pod, read details and live logs; confirm no create/edit/delete/scale/exec affordance exists.

### Tests for User Story 1

- [X] T018 [P] [US1] Integration test: list, get-detail, and pod logs via fake clientset + envtest in `tests/integration/inspect_test.go`
- [X] T019 [P] [US1] Zero-mutation assertion test across all inspection flows in `tests/integration/readonly_test.go` (SC-006)
- [X] T020 [P] [US1] TUI test (teatest): launch → list → detail → logs, and secret value masked by default in `tests/tui/inspect_test.go` (FR-015)

### Implementation for User Story 1

- [X] T021 [US1] Resource list view (virtualized rows from informer cache, server/default columns) in `internal/ui/views/list.go` (FR-017, SC-005)
- [X] T022 [US1] Filter/search-by-typing in the list in `internal/ui/views/list.go` (FR-007)
- [X] T023 [US1] Detail view (status, metadata, related events) with secret masking + explicit reveal in `internal/ui/views/detail.go` (FR-004, FR-015)
- [X] T024 [US1] Single-pod live log tail (pause/scroll/filter) in `internal/kube/logs.go` and `internal/ui/views/logs.go` (FR-005)
- [X] T025 [US1] Merged multi-pod log tail across a workload's pods in `internal/kube/logs.go` and `internal/ui/views/logs.go` (FR-034)
- [X] T026 [US1] Namespace and context pickers in `internal/ui/views/pickers.go` (FR-003)

**Checkpoint**: MVP — read-only inspection fully functional and testable.

---

## Phase 4: User Story 2 - Graphical debug views (Priority: P1)

**Goal**: Usage gauges (CPU/mem vs requests/limits), last-1-hour trend charts, top consumers, color status — sourced from Prometheus with honest "unavailable" state.

**Independent Test**: Open a node/workload → gauge + numeric value + trend chart matching the data; with no Prometheus, visuals show "unavailable".

### Tests for User Story 2

- [X] T027 [P] [US2] Integration test against stub Prometheus: instant query + `query_range` (1h) and unavailable-state path in `tests/integration/metrics_test.go` (FR-019, FR-021)
- [X] T028 [P] [US2] TUI test: gauge + trend render, `--no-color` textual equivalence in `tests/tui/visuals_test.go` (SC-010)

### Implementation for User Story 2

- [X] T029 [P] [US2] Prometheus client: availability probe, instant query, range query (rolling 1h) in `internal/metrics/prometheus.go` (D5)
- [X] T030 [P] [US2] PromQL builders for CPU/memory usage, node "used", and top consumers in `internal/metrics/queries.go`
- [X] T031 [US2] Gauge/bar and time-series trend chart components (ntcharts) with "unavailable" rendering in `internal/ui/components/charts.go` (FR-019, FR-021)
- [X] T032 [US2] Color-coded status with non-color symbol/label fallback in `internal/ui/components/status.go` and `internal/ui/theme/` (FR-020)
- [X] T033 [US2] Wire gauges + trend into detail and node views (numeric value always shown alongside) in `internal/ui/views/detail.go`
- [X] T034 [US2] Top consumers view (top pods/nodes by CPU/memory) in `internal/ui/views/topconsumers.go` (FR-035)

**Checkpoint**: Graphical debugging works; degrades gracefully without Prometheus/color.

---

## Phase 5: User Story 3 - Ergonomic keyboard control (Priority: P1)

**Goal**: Familiar, non-exotic shortcuts for all navigation with a discoverable context-aware help overlay.

**Independent Test**: Keyboard-only, navigate list → detail → back, filter, open help, quit — all conventional and discoverable.

### Tests for User Story 3

- [X] T035 [P] [US3] TUI test: keyboard-only navigation, back/quit/jump, and help overlay lists exactly the active bindings in `tests/tui/keyboard_test.go` (FR-010)

### Implementation for User Story 3

- [X] T036 [US3] Navigation bindings (arrows/PgUp/PgDn/Home/End, `Enter` open, `Esc` back, `:` jump, `q` quit) wired to views via the central keymap in `internal/ui/keys/keys.go` and `internal/ui/app.go` (FR-009)
- [X] T037 [US3] Context-aware help overlay generated from the keymap source in `internal/ui/components/help.go` (FR-010)

**Checkpoint**: Full keyboard operability with in-app help.

---

## Phase 6: User Story 4 - Topology (pods ↔ nodes) (Priority: P2)

**Goal**: See which pods run on which nodes, select node→pods and pod→node, distinguish nodes under pressure.

**Independent Test**: On a multi-node cluster, each node lists its pods; selecting a pod reveals its host node.

### Tests for User Story 4

- [X] T038 [P] [US4] Integration test: node→pods grouping and pod→node lookup via fake clientset in `tests/integration/topology_test.go`
- [X] T039 [P] [US4] TUI test: topology stays interactive with a large synthetic cluster (≥5,000 pods/≥100 nodes) in `tests/tui/topology_test.go` (SC-005, SC-011)

### Implementation for User Story 4

- [X] T040 [US4] Topology derivation (nodes with scheduled pods, allocatable/requested, pressure state; "used" from Prometheus) in `internal/kube/scheduling.go` and `internal/metrics/`
- [X] T041 [US4] Topology view (grouped by node, virtualized pod lists, pressure indicator, node↔pod selection) in `internal/ui/views/topology.go` (FR-013)

**Checkpoint**: Placement debugging available.

---

## Phase 7: User Story 5 - Events timeline (Priority: P2)

**Goal**: Time-ordered, filterable events, scopable to a resource, warnings distinguished, honest visible window.

**Independent Test**: Open the timeline, confirm time order, and scope to a single resource.

### Tests for User Story 5

- [X] T042 [P] [US5] Integration test: event ordering, filtering, and resource scoping in `tests/integration/events_test.go`
- [X] T043 [P] [US5] TUI test: timeline severity distinction and visible-window indicator in `tests/tui/events_test.go` (FR-014)

### Implementation for User Story 5

- [X] T044 [US5] Events source (list/watch events, time ordering, retention-window awareness) in `internal/kube/informers.go` (or `internal/kube/events.go`)
- [X] T045 [US5] Events timeline view (order, filter by namespace/resource/severity, scope to selection, Warning/Error styling, visible-window note) in `internal/ui/views/events.go` (FR-014, SC-012)

**Checkpoint**: Event-based debugging available.

---

## Phase 8: User Story 7 - Clickable / mouse navigation (Priority: P2)

**Goal**: Click to select/open/activate and wheel-scroll across all views, with full keyboard parity.

**Independent Test**: Mouse-only, reach every destination reachable by keyboard; `--no-mouse` keeps everything keyboard-reachable.

### Tests for User Story 7

- [X] T046 [P] [US7] TUI test: mouse click-select, double-click open, tab activate, wheel scroll, and keyboard/mouse parity in `tests/tui/mouse_test.go` (SC-003)

### Implementation for User Story 7

- [X] T047 [US7] Mouse event handling (click select, click activate on-screen controls/tabs, double-click open, wheel scroll) in `internal/ui/app.go` and view components (FR-011)
- [X] T048 [US7] Ensure clickable affordances exist for every keyboard destination (tabs, breadcrumbs, action labels) across `internal/ui/views/` (FR-008 parity)

**Checkpoint**: 100% keyboard/mouse parity.

---

## Phase 9: User Story 9 - Ownership & dependency graph (Priority: P2)

**Goal**: Navigate ownership/routing relationships; make broken links (e.g. Service with zero endpoints) visible.

**Independent Test**: A Service with no ready backends shows zero endpoints/missing pods.

### Tests for User Story 9

- [X] T049 [P] [US9] Integration test: ownerRef chain, Service→Endpoints→Pods, Ingress→Service, and zero-endpoint detection in `tests/integration/graph_test.go` (SC-015)

### Implementation for User Story 9

- [X] T050 [US9] Dependency-edge derivation (ownerReferences, Service/Endpoints, Ingress backends) in `internal/kube/graph.go` (FR-026)
- [X] T051 [US9] Dependency graph view with broken-link highlighting and up/down navigation in `internal/ui/views/graph.go`

**Checkpoint**: Relationship debugging available.

---

## Phase 10: User Story 10 - Workload failure diagnostics (Priority: P2)

**Goal**: Restart counts, last termination reason (OOMKilled/exit code), evicted pods with reason.

**Independent Test**: A crashlooping pod shows its restart count and last termination reason.

### Tests for User Story 10

- [X] T052 [P] [US10] Integration test: parse container statuses (OOMKilled, exit codes, restarts) and evictions in `tests/integration/diagnostics_test.go` (SC-016)

### Implementation for User Story 10

- [X] T053 [US10] Diagnostics derivation (restartCount, lastState.terminated reason/exit code, evicted status) in `internal/kube/diagnostics.go` (FR-027)
- [X] T054 [US10] Failure diagnostics view (OOMKilled flagged distinctly, evictions listed with reason) in `internal/ui/views/diagnostics.go`

**Checkpoint**: Crash/OOM/eviction triage available.

---

## Phase 11: User Story 11 - Scheduling & capacity (Priority: P2)

**Goal**: Reasons Pending pods can't schedule; per-node bin-packing (allocatable vs requested vs used).

**Independent Test**: A Pending pod shows its scheduling reason; a node shows allocatable vs requested.

### Tests for User Story 11

- [X] T055 [P] [US11] Integration test: pending-reason extraction and node bin-packing (allocatable/requested; used via stub Prometheus, unavailable path) in `tests/integration/scheduling_test.go` (SC-019)

### Implementation for User Story 11

- [X] T056 [US11] Scheduling reasons + node bin-packing computation in `internal/kube/scheduling.go` (FR-028)
- [X] T057 [US11] Scheduling & capacity view (pending reasons; per-node allocatable/requested/used with headroom; "used" unavailable when Prometheus down) in `internal/ui/views/scheduling.go`

**Checkpoint**: Scheduling/capacity debugging available.

---

## Phase 12: User Story 12 - Helm release overview (read-only) (Priority: P2)

**Goal**: List Helm releases and revision history with status; never upgrade/rollback/uninstall.

**Independent Test**: A Helm-managed workload shows release status/current revision; no mutating Helm affordance exists.

### Tests for User Story 12

- [X] T058 [P] [US12] Integration test against fake Helm storage: list releases + history; assert no mutating Helm action is constructed in `tests/integration/helm_test.go` (SC-017)

### Implementation for User Story 12

- [X] T059 [US12] Read-only Helm access (list + history via Helm v3 SDK with a kubeconfig RESTClientGetter) in `internal/helm/releases.go` (FR-029, D6)
- [X] T060 [US12] Helm releases view (name/namespace/chart/version/revision/status; per-release history; failed/pending flagged) in `internal/ui/views/helm.go`

**Checkpoint**: Read-only Helm debugging available.

---

## Phase 13: Polish & Cross-Cutting Concerns

**Purpose**: Improvements spanning multiple stories

- [X] T061 [P] Documentation: README + usage in `docs/` and repo `README.md` (invocation, flags, Prometheus setup)
- [X] T062 [P] Unit tests for config fallback, keymap/help consistency, PromQL builders, and degradation logic in `tests/unit/`
- [X] T063 Performance validation at ≥5,000 pods / ≥100 nodes (list + topology responsiveness) in `tests/integration/scale_test.go` (SC-005)
- [X] T064 Whole-suite zero-mutation guarantee: assert no mutating K8s verb and no mutating Helm action across all flows in `tests/integration/readonly_test.go` (SC-006, FR-012)
- [X] T065 [P] Security review: secret masking present in every view; no credentials/secret values persisted to config or logs (FR-015, Principle IV)
- [X] T066 Resilience validation: cluster disconnect/reconnect and Prometheus-down mid-session without restart in `tests/integration/resilience_test.go` (SC-007)
- [X] T067 [manual live-cluster validation PASSED by owner, 2026-07-06] Run `quickstart.md` scenarios V1–V10 end-to-end and record results

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: no dependencies.
- **Foundational (Phase 2)**: depends on Setup — BLOCKS all user stories.
- **User Stories (Phases 3–12)**: all depend on Foundational.
  - P1 first (US1 → US2 → US3), then P2 (US4, US5, US7, US9, US10, US11, US12).
  - After Foundational, stories can proceed in parallel if staffed.
- **Polish (Phase 13)**: depends on all targeted stories being complete.

### User Story Dependencies

- **US1 (P1)**: after Foundational. MVP; no dependency on other stories.
- **US2 (P1)**: after Foundational. Adds the metrics layer; independently testable.
- **US3 (P1)**: after Foundational. Keyboard/help layer; independently testable.
- **US4/US5/US9/US10/US11 (P2)**: after Foundational; each derives from the kube layer and is independently testable. US4 and US11 share `internal/kube/scheduling.go` — sequence them or coordinate that file.
- **US7 (P2)**: after Foundational; strongest once views exist (verifies parity across them).
- **US12 (P2)**: after Foundational; independent (Helm layer).

### Within Each User Story

- Tests first (write and see them fail), then model/source, then view, then integration.
- Source/derivation (internal/kube, internal/metrics, internal/helm) before its view.

### Parallel Opportunities

- Setup: T003, T004, T005 in parallel.
- Foundational: T006, T007, T011, T012, T014, T015, T017 in parallel (T008→T009→T010 sequential in `internal/kube`).
- Within a story, all `[P]` test tasks run together; `internal/metrics/prometheus.go` (T029) and `queries.go` (T030) in parallel.
- Across stories (post-Foundational): US2, US5, US9, US10, US12 can be built by different developers in parallel (distinct files); coordinate US4/US11 on `scheduling.go`.

---

## Parallel Example: User Story 1

```bash
# Tests together:
Task: "Integration test list/detail/logs in tests/integration/inspect_test.go"
Task: "Zero-mutation assertion in tests/integration/readonly_test.go"
Task: "TUI launch→list→detail→logs + secret masking in tests/tui/inspect_test.go"
```

---

## Implementation Strategy

### MVP First (User Story 1 only)

1. Phase 1 Setup → 2. Phase 2 Foundational → 3. Phase 3 US1 → **STOP & VALIDATE** read-only inspection → demo.

### Incremental Delivery

1. Setup + Foundational → foundation ready.
2. US1 (read-only inspection) → MVP.
3. US2 (graphical debug) + US3 (keyboard) → complete the P1 overview.
4. Add P2 stories (topology, events, mouse, dependency graph, diagnostics, scheduling, Helm) → each independently testable and demoable.

### Notes

- [P] = different files, no dependency on incomplete tasks.
- Read-only is a hard invariant: never wire a mutating K8s verb or Helm action (T011, T064, SC-006).
- Metrics come only from Prometheus; when unavailable show "unavailable", never estimate (constitution data-integrity).
- P3 backlog (sizing, customizable views, posture, connectivity, access view, diff) is intentionally excluded from v1.

---

## v2 Phases (kickoff 2026-07-06 — all P3 stories + informers)

### Phase v2.1: User Story 8 — Customizable views (FR-024/FR-025)

- [x] T068 [US8] Config schema: per-type view prefs (columns/order, sort, filter) + named saved views, with invalid-entry tolerance, in `internal/config/config.go`
- [x] T069 [US8] Apply per-type column prefs (subset/order over the type-aware base set) in `internal/ui/listview.go`
- [x] T070 [US8] Persist sort and committed filter per type; restore on type switch, in `internal/ui/app.go`
- [x] T071 [US8] Column chooser modal ('C': Space toggles, ←/→ reorders, Enter applies) in `internal/ui/app.go`
- [x] T072 [US8] Named views ('V': save current as…, switch, and 'R' reset to defaults) in `internal/ui/app.go`
- [x] T073 [P] [US8] Tests: prefs round-trip + tolerance, column order applied, sort/filter restored, chooser & saved-view flows

### Phase v2.2: User Story 6 — Sizing recommendations (FR-023)

- [ ] T074 [US6] Sizing derivation from Prometheus history vs requests/limits (advisory, never fabricated) in `internal/metrics/sizing.go`
- [ ] T075 [US6] Sizing view (per-workload verdicts with the observed data behind them) in `internal/ui/`
- [ ] T076 [P] [US6] Tests incl. insufficient-data → "no recommendation"

### Phase v2.3: User Story 13 — Posture overview (FR-030)

- [ ] T077 [US13] Posture rules (no requests/limits, privileged, root, no probes, `latest`, no NetworkPolicy, TLS expiry) in `internal/kube/posture.go`
- [ ] T078 [US13] Posture view (findings by rule, object references) in `internal/ui/`
- [ ] T079 [P] [US13] Tests per rule

### Phase v2.4: User Story 14 — Connectivity / NetworkPolicy (FR-031)

- [ ] T080 [US14] Per-pod NetworkPolicy matching + allowed peers summary in `internal/kube/netpol.go`
- [ ] T081 [US14] Connectivity view (policies selecting a pod; unrestricted state) in `internal/ui/`
- [ ] T082 [P] [US14] Tests

### Phase v2.5: User Story 15 — Access (RBAC) view (FR-032)

- [ ] T083 [US15] SelfSubjectRulesReview summary in `internal/kube/access.go`
- [ ] T084 [US15] Access view + inaccessible-type reasons in `internal/ui/`
- [ ] T085 [P] [US15] Tests

### Phase v2.6: User Story 16 — Read-only diff (FR-033)

- [ ] T086 [US16] Live vs last-applied diff derivation in `internal/kube/diff.go`
- [ ] T087 [US16] Diff view (no apply affordance) in `internal/ui/`
- [ ] T088 [P] [US16] Tests

### Phase v2.7: Internals

- [ ] T089 Shared informer cache replacing periodic LIST polling (carried ex-T010) in `internal/kube/informers.go`

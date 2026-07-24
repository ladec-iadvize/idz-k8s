# Quickstart & Validation Guide: Kubernetes TUI Overview Client (read-only)

**Feature**: 001-k8s-tui-client | **Date**: 2026-07-03

Proves the v1 (P1+P2) feature works end-to-end. References the contracts
([cli-interface](./contracts/cli-interface.md), [keybindings](./contracts/keybindings.md),
[config-schema](./contracts/config-schema.md), [data-sources](./contracts/data-sources.md))
and the [data model](./data-model.md) rather than duplicating them.

## Prerequisites

- Go 1.23+ toolchain.
- A reachable Kubernetes cluster with a kubeconfig that has **read** access
  (kind/minikube works). A multi-node cluster is best for topology.
- A **Prometheus** endpoint scraping the cluster (single metrics source) for the
  usage/trend/top-consumers scenarios. Without it, those views show "unavailable".
- Helm-managed workloads for the Helm scenario.
- A terminal with mouse reporting for the mouse scenario.

## Build & run

```bash
go build ./cmd/idz-k8s
./idz-k8s                                   # current context/namespace
./idz-k8s -n kube-system                    # start in a namespace
./idz-k8s --prometheus-url http://localhost:9090   # point at Prometheus
```

## Validation scenarios

Each maps to a v1 user story / acceptance criteria in [spec.md](./spec.md).

### V1 — Inspection (US1, SC-001/SC-002/SC-006)
1. Launch; confirm context, namespace, and a resource list appear.
2. Select a pod, `Enter` → details (status, metadata, events).
3. `l` → live logs (pause/scroll/filter); `L` → merged logs across a workload's pods.
4. Search the whole interface for any create/edit/delete/scale/exec affordance → **none exists**.
**Expected**: pod detail < 15 s; logs ≤ 3 interactions; admin actions (v3) only via 'a'/'e' and always behind a confirmation.

### V2 — Graphical debug views (US2, SC-009/SC-010)
1. Open a node/workload → CPU/memory gauge + numeric value; a **last-1-hour** trend chart renders.
2. Status is color-coded AND carries a symbol/label.
3. Run with `--no-color` (or `NO_COLOR=1`) → same info as text.
4. Run without `--prometheus-url` → gauges/charts show "unavailable"; the rest works.
**Expected**: visuals match numbers; textual equivalence; honest "unavailable".

### V3 — Keyboard & mouse (US3/US7, SC-003)
1. Keyboard-only: list → detail → back (`Esc`), `/` filter, `?` help.
2. Mouse-only: click a row (select), double-click (open), click a view tab, wheel-scroll.
3. Relaunch `--no-mouse` → everything still reachable by keyboard.
**Expected**: 100% keyboard/mouse parity.

### V4 — Topology (US4, SC-005/SC-011)
1. `t` → nodes with the pods scheduled on each; select a node → its pods + usage.
2. Select a pod → its host node highlighted.
3. On a namespace with ≥ 5,000 pods / ≥ 100 nodes → topology stays interactive.
**Expected**: which-node-hosts-which-pod answerable in < 10 s.

### V5 — Events timeline (US5, SC-012)
1. `v` → events in time order; filter by namespace/resource/severity.
2. From a selected resource, scope the timeline to it in ≤ 2 interactions.
3. Confirm the visible window is indicated (not implying events beyond retention).

### V6 — Dependency graph (US9, SC-015)
1. `g` on a Deployment → ReplicaSets and Pods linked.
2. `g` on a Service with no ready backends → zero endpoints / missing pods visible in < 20 s.

### V7 — Failure diagnostics (US10, SC-016)
1. `f` on a crashlooping pod → restart count + last termination reason (e.g. **OOMKilled**) within 2 interactions.
2. Evicted pods listed with reason; OOMKilled flagged distinctly.

### V8 — Scheduling & capacity (US11, SC-019)
1. With a Pending pod present, `p` → its scheduling reason within 2 interactions.
2. A node shows allocatable vs requested (and "used" from Prometheus, or "unavailable").

### V9 — Helm overview (US12, SC-017)
1. `H` → releases with name/namespace/chart/version/revision/status.
2. Open a release → revision history; failed/pending flagged.
3. Confirm rollback/uninstall (v3) are reachable from the actions palette and always ask for confirmation; install/upgrade are not offered.

### V10 — Resilience (Edge cases, SC-007)
1. Kill/restore cluster connectivity mid-session → connection status shown; recovers without restart.
2. Stop Prometheus mid-session → usage visuals switch to "unavailable"; the rest keeps working.

## Automated test entry points

- Unit: `go test ./internal/...`
- Integration (fake clientset / envtest, stub Prometheus, fake Helm storage): `go test ./tests/integration/...`
- TUI (teatest golden/interaction, keyboard/mouse parity): `go test ./tests/tui/...`
- Confirmation guarantee (v3): integration tests exercise every admin operation against fakes, and UI tests assert **no mutation runs without its confirmation step** (SC-006 v3).

See [research.md](./research.md) D10 for the testing rationale.

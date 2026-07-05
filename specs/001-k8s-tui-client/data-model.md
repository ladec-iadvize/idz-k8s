# Data Model: Kubernetes TUI Overview Client (read-only)

**Feature**: 001-k8s-tui-client | **Date**: 2026-07-03

Covers the in-memory domain the UI consumes (read-only projections of API,
Prometheus, and Helm data) and the small local preferences file. Concrete Go
types live in `internal/model` and `internal/config`. This is the v1 (P1+P2)
model; P3 entities (Saved View, Posture Finding, Sizing Recommendation) are
deferred.

---

## In-memory domain (all read-only)

### ResourceType
Discovered API resource type the operator can browse.

| Field | Type | Notes |
|-------|------|-------|
| group / version / kind | string | GVK |
| resource | string | plural wire name |
| namespaced | bool | scope |
| verbs | []string | advertised verbs (read subset used) |
| isCRD | bool | discovered as a Custom Resource |
| defaultColumns | []ColumnDef | from server `additionalPrinterColumns` when available, else sensible defaults |

**Source**: discovery client (FR-002). **Uniqueness**: (group, version, resource).

### ResourceObject
A single instance of a ResourceType.

| Field | Type | Notes |
|-------|------|-------|
| type | ResourceType (ref) | its kind |
| namespace / name / uid | string | identity |
| resourceVersion | string | change detection |
| status | StatusSummary | derived health |
| raw | Unstructured | full object for detail |
| createdAt | timestamp | age |

**Lifecycle**: created → updated → deleted, reflected via informer cache (read-only observation).

### StatusSummary
Display health for color coding (FR-020).

| Field | Type | Notes |
|-------|------|-------|
| level | enum {Ok, Warning, Error, Unknown} | color |
| label / symbol | string | non-color fallback |
| reason | string | optional |

### Node
A cluster machine (topology + capacity).

| Field | Type | Notes |
|-------|------|-------|
| name | string | |
| allocatable | {cpu, memory} | schedulable capacity |
| requested | {cpu, memory} | summed pod requests |
| used | {cpu, memory} \| unavailable | from Prometheus (FR-028); unavailable if Prometheus down |
| pods | []ref(ResourceObject) | scheduled pods (topology) |
| pressure | enum {Ok, Warning, Error, Unknown} | visual state |

### ContainerDiagnostic (failure diagnostics, FR-027)

| Field | Type | Notes |
|-------|------|-------|
| pod / container | ref / string | subject |
| restartCount | int | |
| lastTerminationReason | string | e.g. OOMKilled, Error |
| lastExitCode | int | |
| evicted | bool | + evictionReason for evicted pods |

### DependencyEdge (FR-026)

| Field | Type | Notes |
|-------|------|-------|
| from / to | ref(ResourceObject) | endpoints |
| relation | enum {owns, routes-to} | owns (ownerRef), routes-to (Svc→Endpoints→Pods, Ingress→Svc) |

A Service with zero routes-to edges to ready pods is a visible "broken link".

### SchedulingReason (FR-028)

| Field | Type | Notes |
|-------|------|-------|
| pod | ref | pending/unschedulable pod |
| reason | string | e.g. insufficient cpu/memory |

### Event (timeline, FR-014)

| Field | Type | Notes |
|-------|------|-------|
| time | timestamp | ordering key |
| type | enum {Normal, Warning} | severity (Warning/Error distinguished) |
| involvedObject | ref | scoping |
| reason / message | string | |

Bounded by cluster event retention; the timeline shows its visible window.

### MetricSeries / MetricSample (FR-019)

| Field | Type | Notes |
|-------|------|-------|
| subject | ref (workload/node) | |
| kind | enum {CPU, Memory} | |
| current | quantity \| unavailable | instant value (Prometheus) |
| request / limit | quantity | from the object spec |
| series | []{ts, value} | rolling last-1-hour (Prometheus query_range) |
| available | bool | false ⇒ "unavailable" state (FR-021) |

**Source**: Prometheus only (single source). No metrics-server.

### HelmRelease (FR-029, read-only)

| Field | Type | Notes |
|-------|------|-------|
| name / namespace | string | |
| chart / chartVersion / appVersion | string | |
| revision | int | current |
| status | enum {deployed, failed, pending, superseded, ...} | flagged when failed/pending |
| history | []{revision, status, updated} | revision history |

**Source**: Helm v3 storage (read). No mutating operations exist.

### ColumnDef
Column definition for a resource list.

| Field | Type | Notes |
|-------|------|-------|
| id / header | string | |
| jsonPath | string | field into the object |
| width | int/auto | |
| sortable | bool | |

*(User-customized/saved column sets are P3 — not in v1; v1 uses server/default columns.)*

### Shortcut
A keyboard binding scoped to a view, listed in the help overlay.

---

## Persisted preferences (`~/.config/idz-k8s/config.yaml`)

Small and secret-free (Principle IV). Customizable/saved views are P3 → absent in v1.

### AppConfig

| Field | Type | Default | Notes |
|-------|------|---------|-------|
| schemaVersion | int | 1 | forward-compatible migration |
| refreshIntervalSeconds | int | 5 | configurable cadence (FR-006); invalid → 5 |
| prometheusURL | string | (unset) | single metrics source endpoint (D5) |
| theme | string | "auto" | color / auto-degrade |
| lastContext | string | (unset) | remembered active context |

**Fallback rule (FR-026 tolerance analogue)**: a malformed/unreadable config loads
as empty defaults and the client still starts; an unset `prometheusURL` simply
yields "metrics unavailable" states, never a crash.

---

## Relationships

- `ResourceType 1—* ResourceObject`.
- `Node 1—* ResourceObject` (pods scheduled on it) — topology.
- `ResourceObject 1—* ContainerDiagnostic`, `1—* Event`, `1—* MetricSample`.
- `DependencyEdge` connects two `ResourceObject`s (owns / routes-to).
- `HelmRelease *—* ResourceObject` (a release manages workloads) via labels/annotations.
- `AppConfig` is a single local document.

## Validation rules (from requirements)

- Only read verbs are ever issued; zero mutating operations (FR-012, SC-006).
- Refresh interval ≥ 1 s (else default) — FR-006.
- Secret/sensitive fields masked by default in every view — FR-015.
- Metrics values shown only when Prometheus provides them; otherwise "unavailable", never estimated — FR-021 + constitution data-integrity.
- Config load never blocks startup on bad data.
- Every visual value has a textual equivalent field — SC-010.

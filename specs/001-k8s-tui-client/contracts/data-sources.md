# Contract: Data Sources

The tool reads from three sources. v3: it also carries admin operations
(kube: edit/scale/delete/restart/cordon/suspend/port-forward; helm:
rollback/uninstall), every one behind an explicit UI confirmation. It
operates strictly within the operator's RBAC (FR-012, FR-018, SC-006). Nothing
here requires cluster-admin or any write permission.

## 1. Kubernetes API (client-go)

| Operation | Verb | Resources | Notes |
|-----------|------|-----------|-------|
| Discover types | `get`/`list` (discovery) | APIGroups, APIResources | all types incl. CRDs (FR-002) |
| List/observe | `list` + `watch` | any discovered resource | via shared informers (D3) |
| Get detail | `get` | selected resource | detail view (FR-004) |
| Events | `list`/`watch` | `events` | timeline (FR-014) |
| Logs | `get` | `pods/log` | single + merged multi-pod tail (FR-005/FR-034) |
| Access probe (optional) | `create` | `SelfSubjectRulesReview` | read-only introspection of the operator's own permissions; not a resource mutation |

**Only** the read verbs above are ever issued. There is no code path for
`create`/`update`/`patch`/`delete`/`eviction`/`exec` on cluster resources. If the
operator lacks `list`/`watch` on a type, it is shown as inaccessible with a clear
message rather than erroring the app (FR-016).

> Note: `SelfSubjectRulesReview` is a Kubernetes read-only self-introspection
> call (it changes no cluster state) and is the only `create`-verb call permitted;
> it powers accessibility messaging. It is optional and behind graceful fallback.

## 2. Prometheus (single metrics source, D5)

| Operation | API | Purpose |
|-----------|-----|---------|
| Availability probe | `GET /-/ready` or a cheap query | drive "available/unavailable" state (FR-021) |
| Instant query | `GET /api/v1/query` | current CPU/memory for gauges, node "used", top consumers |
| Range query | `GET /api/v1/query_range` | rolling **last-1-hour** trend series (FR-019) |

Endpoint from `--prometheus-url` / config. When unset or unreachable, every
usage/trend visual shows an explicit "unavailable" state — values are never
estimated (FR-021, constitution data-integrity). Queries are bounded (1-hour
window, capped resolution) to stay API-friendly (Principle I).

## 3. Helm release storage (read-only, D6)

| Operation | Helm v3 action | Purpose |
|-----------|----------------|---------|
| List releases | `action.NewList` | releases with name/namespace/chart/version/revision/status (FR-029) |
| Release history | `action.NewHistory` | per-release revision history |

Configured with a `RESTClientGetter` from the active kubeconfig and the cluster's
release storage (Secrets by default). **No** `install`/`upgrade`/`rollback`/
`uninstall` action is ever constructed. Reading releases uses only read access to
the storage objects.

## Cross-source contract rules

- The `ui` layer never calls a data source directly; it consumes `internal/model`,
  which the three source layers populate.
- A test harness exercises every admin operation against fakes (right verb,
  right resource) and the UI tests assert the confirmation gate: **no mutation
  without its confirmation step** (SC-006 v3).
- Secret objects are listed/inspected like any resource, but `data` values are
  masked by default and revealed only on explicit request (FR-015).

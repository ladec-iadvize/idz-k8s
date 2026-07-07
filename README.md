# idz-k8s — read-only Kubernetes overview TUI

`idz-k8s` is a terminal UI to **observe and debug** a Kubernetes cluster. It is
**strictly read-only**: it never creates, edits, deletes, scales, or execs into
anything — administration stays in your usual tools (k9s, kubectl). What it
adds is a graphical, discoverable overview: usage gauges and trend charts,
a pods↔nodes topology with bin-packing, an events timeline, workload failure
diagnostics, dependency navigation, and a Helm release inspector.

It targets a general technical audience: everything is discoverable from the
interface itself (visible menus, contextual shortcut help, clickable
controls) — prior kubectl/k9s experience is not required.

## Build & run

Requires Go 1.23+ and a kubeconfig with read access.

```bash
go build -o idz-k8s ./cmd/idz-k8s
./idz-k8s                      # current kubeconfig context, all namespaces
./idz-k8s -n team-a            # start on one namespace
./idz-k8s --context prod-eks   # start on a specific context
```

Flags: `--kubeconfig`, `--context`, `-n/--namespace`, `--config`,
`--prometheus-url`, `--refresh` (seconds), `--no-mouse`, `--no-color`.

## Views

| Key | View |
|-----|------|
| (list) | Browse any resource type — built-ins and CRDs — with READY/STATUS/AGE |
| `Enter` | Drill down: a workload/Service opens **its pods**, a node opens **the pods it hosts**; a pod opens its YAML |
| `y` / `d` | YAML view / describe (conditions + the object's events; Services show their backends) |
| `l` | Live logs — on a workload: **merged logs of all its pods**, color-coded per pod |
| `t` | Topology: pods per node, reserved vs allocatable CPU/RAM, free room, biggest pods first |
| `v` | Events **timeline**: a time axis per object, warnings highlighted, selectable details |
| `f` | Failure diagnostics **grouped by failure type** (CrashLoopBackOff, OOMKilled, evictions, restarts, unschedulable — with the scheduler's reason), error groups first |
| `u` | Top consumers (CPU/memory, via Prometheus) |
| `x` | Connectivity: which NetworkPolicies select a pod (or a workload's template) and the allowed ingress/egress peers/ports — explicit **unrestricted** and **default-deny** states |
| `a` | Access (RBAC): the API server's own answer on what your credentials can read, plus the discovered types you cannot list; a 403 on a list names the type instead of faking a disconnection |
| `p` | Posture (advisory): best-practice findings by rule — missing requests/limits, privileged/root containers, missing probes, `latest` images, namespaces without NetworkPolicy, TLS certificates near/past expiry |
| `z` | Sizing (advisory): a recap **table of every listed workload** — usage-vs-request gauges and ✓/!/✗ verdicts for CPU & memory, worst first; `Enter` opens the detailed panel (avg/peak gauges vs request/limit). Never applied, never estimated |
| `:helm` | Helm releases (read-only): history, deployed resources with **live state**, values — reachable from the `:` picker like any resource |
| `o` | Jump to the owner (pod → ReplicaSet → Deployment) |
| `D` | Diff (read-only): live object vs its `last-applied` configuration — drifted fields with both values; explicit no-baseline / no-drift states; nothing can be applied |

## Interaction

- **Keyboard**: arrows/PgUp/PgDn, `/` filter (centered input, live), `:` resource
  type (kubectl short names work: `:svc`, `:deploy`, `:helm`…), `n` namespace (a glob like `staging-*` scopes
  every view to all matching namespaces), `c` context, `?` contextual help, `q` quit. `s`/`S` sort
  columns, `Space` marks resources (then `f`/`v` scope to the selection),
  `w` warnings-only in the timeline, `Space` pauses log follow.
- **Customizable views**: `C` opens the column chooser (Space shows/hides,
  `←`/`→` reorders — per resource type), including **custom fields**: a label
  key (`app`, `team`) or any object field by dot path (`.status.podIP`,
  `.spec.nodeName`). Sort and committed filters are
  remembered per type across launches, `V` saves the whole arrangement (type,
  namespace, columns, sort, filter) as a **named view** and reopens it later,
  `R` resets the current type to its defaults.
- **Mouse**: click to select, double-click to open, wheel to scroll, click
  column headers to sort, click header chips (ctx/ns/type) and footer shortcut
  labels to trigger them. `m` toggles mouse capture to select/copy text.
- Pickers and filters open as **centered modals** over the current view.
  **`/` works everywhere, the same way**: it filters row views (lists, Helm
  releases, events, sizing table) and searches content views vim-style
  (describe/YAML, logs, failures, topology, posture, connectivity, access,
  diff, Helm detail & values) — matches highlighted, `n`/`N` to navigate,
  `Esc` clears first, then goes back.

## Metrics (Prometheus)

Prometheus is the single metrics source (gauges, 1-hour trend charts, top
consumers). The in-cluster Prometheus is **auto-discovered per context** and
reached through the Kubernetes API server proxy — no port-forward needed.
Override with `--prometheus-url`. Without a reachable Prometheus, usage visuals
show an explicit “unavailable” state; everything else keeps working.

## Configuration

`~/.config/idz-k8s/config.yaml` (auto-managed, never contains credentials):

```yaml
schemaVersion: 1
refreshIntervalSeconds: 5
prometheusURL: ""          # optional override
theme: auto
lastContext: dev-main      # restored on startup
lastNamespace: ""          # "" = all namespaces (the default scope)
lastType: apps/v1/deployments
viewPrefs:                 # per-type customization ('C', 's', '/')
  v1/pods:
    columns: [NAME, RESTARTS, NODE, STATUS, AGE]
    sortCol: RESTARTS
    sortAsc: false
savedViews:                # named views ('V')
  - name: crashwatch
    type: v1/pods
    namespace: ""
    sortCol: RESTARTS
    filter: api
```

Invalid or stale entries (an unknown column, a type absent from the cluster)
are ignored gracefully — they never break startup.

## Guarantees

- **Zero mutating operations** — enforced by construction (no mutating verb is
  wired) and by tests that sweep every data path, including the Helm layer.
- Secrets are **masked by default** (explicit reveal only); nothing sensitive
  is ever persisted or logged.
- Graceful degradation: no color (`NO_COLOR`), no mouse, unreachable
  Prometheus, lost cluster connection (auto-retry with status).
- Responsive at ≥5,000 pods / 100 nodes (windowed rendering; validated in tests).

## Development

```bash
go build ./... && go vet ./... && go test ./...
```

- `internal/kube` — read-only client-go layer (discovery incl. CRDs, lists,
  logs, topology, diagnostics, endpoints)
- `internal/metrics` — Prometheus (instant + range queries, autodiscovery proxy)
- `internal/helm` — Helm release storage reader (read-only)
- `internal/ui` — Bubble Tea interface (views, theme, keymap, mouse)
- `specs/001-k8s-tui-client/` — the full spec-kit lifecycle: spec, plan,
  research, contracts, quickstart, tasks

The manual validation scenarios live in
[`specs/001-k8s-tui-client/quickstart.md`](specs/001-k8s-tui-client/quickstart.md).

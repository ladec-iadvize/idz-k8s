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

## Install

Prebuilt binaries for Linux/macOS (amd64/arm64) are attached to every
[release](https://github.com/ladec-iadvize/idz-k8s/releases) — download,
untar, run. Or build from source:

## Build & run

Requires Go 1.26+ and a kubeconfig with read access.

```bash
go build -o idz-k8s ./cmd/idz-k8s
./idz-k8s                      # current kubeconfig context, all namespaces
./idz-k8s -n team-a            # start on one namespace
./idz-k8s --context prod-eks   # start on a specific context
```

Flags: `--kubeconfig`, `--context`, `-n/--namespace`, `--config`,
`--prometheus-url`, `--refresh` (seconds), `--theme` (auto/dark/light —
`auto` follows the terminal background; also persistable as `theme:` in the
config file), `--no-mouse`, `--no-color`, `--version`, and `--kikoo` (you will know it
when you see it 💚).

## Views

| Key | View |
|-----|------|
| (list) | Browse any resource type — built-ins and CRDs — with READY/STATUS/AGE; default columns mirror **`kubectl get -o wide`** per type (pods add IP/NODE, deployments UP-TO-DATE/AVAILABLE, services CLUSTER-IP/PORT(S), nodes ROLES/VERSION/INTERNAL-IP…); extra columns — live pod usage (CPU/MEM + `%R` of request), karpenter INSTANCE/NODEPOOL, IMAGES, SELECTOR… — are one `Space` away in the `C` chooser |
| `Enter` | Drill down: a workload/Service opens **its pods**, a node opens **the pods it hosts**; a pod opens its YAML |
| `y` / `d` | YAML view / describe (conditions + the object's events, messages in full; Services show their backends). Secret values are **masked**; `x` on a Secret's detail reveals/hides them |
| `l` | Live logs — on a workload: **merged logs of all its pods**, color-coded per pod |
| `t` | Topology: pods per node, reserved vs allocatable CPU/RAM, free room, biggest pods first |
| `v` | Events **timeline**: a time axis per object, warnings highlighted (`w` = warnings only, `k` = kind filter); the selected event's message shows in full below the list, `Enter` opens the referenced object |
| `f` | Failure diagnostics **grouped by failure type** (CrashLoopBackOff, OOMKilled, evictions, restarts, unschedulable — with the scheduler's reason), error groups first; `↑`/`↓` select, `Enter` opens the pod, `w` errors only |
| `u` | Usage (from the pods or deployments list): **CPU and memory side by side** — values, gauges relative to the top consumer; per-deployment rows aggregate their pods; sortable/filterable like every table |
| `x` | Connectivity: which NetworkPolicies select a pod (or a workload's template) and the allowed ingress/egress peers/ports — explicit **unrestricted** and **default-deny** states |
| `a` | Access (RBAC): the API server's own answer on what your credentials can read, plus the discovered types you cannot list; a 403 on a list names the type instead of faking a disconnection |
| `p` | Posture (advisory): best-practice findings by rule — missing requests/limits, privileged/root containers, missing probes, `latest` images, namespaces without NetworkPolicy, TLS certificates near/past expiry; `Enter` opens the referenced object, `w` errors only |
| `z` | Sizing (advisory): a recap **table of every listed workload** — usage-vs-request gauges and ✓/!/✗ verdicts for CPU & memory, worst first; `Enter` opens the detailed panel (avg/peak gauges vs request/limit). Never applied, never estimated |
| `:helm` | Helm releases (read-only): history, deployed resources with **live state**, values — reachable from the `:` picker like any resource; sortable (`s`/`S`, header click) and filterable like every table |
| `o` | Jump to the owner (pod → ReplicaSet → Deployment) |
| `D` | Diff (read-only): live object vs its `last-applied` configuration — drifted fields with both values; explicit no-baseline / no-drift states; nothing can be applied |

## Interaction

- **Keyboard**: arrows/PgUp/PgDn, `/` filter (centered input, live), `:` resource
  type (kubectl short names work: `:svc`, `:deploy`, `:helm`…; native types listed first, CRDs below), `n` namespace (a glob like `staging-*` scopes
  every view to all matching namespaces), `c` context, `?` contextual help, `q` quit. `s`/`S` sort
  columns, `Space` marks resources (then `f`/`v`/`z` scope to the selection),
  `w` warnings-only in the timeline, `Space` pauses log follow.
- **Customizable views**: `C` opens the column chooser (Space shows/hides,
  `←`/`→` reorders — per resource type), including **custom fields**: a label
  key (`app`, `team`) or any object field by dot path (`.status.podIP`,
  `.spec.nodeName`) — rendered like built-in columns (`POD IP`), removable
  with `⌫` in the chooser. Sort and committed filters are
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
- **Live updates**: the list follows the cluster in real time (watch-driven,
  throttled to ~4 fps) — a rolling update is visible as it happens, no manual
  refresh; the periodic tick remains only as a safety net.

## Cookbook

Concrete, keystroke-by-keystroke recipes for the common moves.

**Add a column (pod IP on the pods list)**
1. `:po` `Enter` to open the pods list, then `C` (column chooser)
2. `End` to reach `◆ add custom field…`, `Enter`
3. Type `.status.podIP` — a leading dot means an object field; a plain word
   (`app`, `team`) means a label key — then `Enter`
4. `Enter` again to apply: a `POD IP` column appears, and it is remembered
   for pods across restarts

**Remove a column (or fix a typo)**
`C`, move onto the entry — custom ones read like `POD IP  (field:.status.podIP)` —
and press `⌫`: the custom field is deleted (`Enter` applies). Built-in columns
can only be hidden with `Space` (NAME always stays); `R` resets the whole type
to its defaults.

**Reorder / hide columns**
`C`, `←`/`→` moves the highlighted column, `Space` shows/hides it, `Enter`
applies.

**Save a named view (a "crashwatch")**
1. On pods: `/` then `api` `Enter` (filter), `s` until RESTARTS, `S` (descending)
2. `V` → `◆ save current view as…` → type `crashwatch` `Enter`
3. Later, from anywhere: `V` → `crashwatch` restores type, namespace, columns,
   sort and filter. Saving under the same name updates it.

**Scope to a namespace family**
`n`, type `staging-*`, select `◆ pattern: staging-*` — every view (lists,
events, failures, topology, posture) follows the pattern.

**Watch a rolling update live**
`:deploy`, `Enter` on the deployment: its pods appear, terminate and turn
ready in real time (watch-driven, ~4 fps). `Esc` returns to the deployments.

**Find text anywhere**
`/` behaves the same everywhere: on row views it filters (the committed query
stays visible as a header chip); on content views (describe/YAML, logs, Helm
values…) it highlights every match — `n`/`N` navigate, the first `Esc` clears,
the second leaves the view.

**Reveal a Secret**
`:secret`, then `Enter` (or `y`) to open its YAML: values are masked; `x`
toggles the reveal. Nothing is ever written to disk or logs.

**Check if an app is right-sized**
`:deploy` then `z`: one row per workload, worst first, with usage-vs-request
gauges. `Enter` on a row opens the detailed panel (average and peak bars
against request/limit).

## Advisory criteria (sizing)

Verdicts are derived only from observed data over the last hour, in this
order — thresholds live in `internal/model/sizing.go`:

1. no observed data → **no recommendation** (never estimated)
2. no request configured → hint to set one near the observed peak
3. peak ≥ 90% of the limit → **under-provisioned / at risk** (OOM, throttling)
4. average ≥ request → **under-provisioned**
5. peak < 50% of the request → **over-provisioned**
6. otherwise → **sized correctly**

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

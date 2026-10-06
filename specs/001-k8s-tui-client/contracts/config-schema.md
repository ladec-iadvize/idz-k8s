# Contract: Configuration File Schema

**Location**: `$XDG_CONFIG_HOME/idz-k8s/config.yaml` (default `~/.config/idz-k8s/config.yaml`), overridable with `--config`.

**Guarantee**: NEVER contains credentials or secret values (Principle IV). Holds
only preferences. The file is a supported editing surface: hand edits are
picked up while the client runs (see Load/save contract).

## Schema

```yaml
schemaVersion: 1                 # int, required
refreshIntervalSeconds: 5        # int >= 1; invalid → 5 (FR-006)
prometheusURL: http://prometheus.monitoring:9090   # single metrics source (D5); unset → metrics "unavailable"
theme: auto                      # "auto" | "dark" | "light" | "none"
lastContext: prod-eu             # string, optional convenience
lastNamespace: ""                # "" = all namespaces
lastType: apps/v1/deployments    # resource type key restored at startup
loginCommand: ""                 # command run when credentials are rejected (never a credential)
viewPrefs:                       # per-type view customization (US8, FR-024/FR-025)
  <type key>:                    # e.g. v1/pods, apps/v1/deployments
    columns: [NAME, "label:team", "field:.spec.nodeName"]  # display order; built-in titles, label:<key>, field:.<path>
    hidden: [UP-TO-DATE]         # base columns explicitly turned off
    sortCol: READY               # column title; "" = none
    sortAsc: true
    filter: api
datadog:                         # logs deep-link (FR-039); every field optional, unset = built-in default
  site: datadoghq.eu             # Datadog site; "off" disables the link
  window: 1h                     # live time span of the link (Go duration or Nd)
  podQuery: "@namespace:{namespace} @container_name:{pod}"
  workloadQuery: "@cluster_name:{context} @namespace:{namespace} @pod_owner:{owner}"
  namespaceQuery: "@cluster_name:{context} @namespace:{namespace}"
savedViews:                      # named arrangements (US8)
  - name: crashwatch
    type: v1/pods
    namespace: ""
    columns: []                  # same semantics as viewPrefs
    hidden: []
    sortCol: RESTARTS
    sortAsc: false
    filter: api
```

## Field rules

| Field | Constraint | On violation |
|-------|-----------|--------------|
| `schemaVersion` | integer ≥ 1 | unknown/absent → best-effort load |
| `refreshIntervalSeconds` | integer ≥ 1 | missing/invalid → 5 |
| `prometheusURL` | valid URL | absent/invalid → all usage/trend visuals show "unavailable" (never a crash) |
| `theme` | one of enum | unknown → `auto` |
| `lastContext` | string | unresolved at runtime → fall back to kubeconfig current-context |
| `datadog.site` | Datadog site host (`datadoghq.eu`, `us5.datadoghq.com`…) or `off` | not a site → explicit message on use, no link |
| `datadog.window` | Go duration or `Nd`, > 0 | invalid → explicit message on use, no link; unset → 1h |
| `datadog.*Query` | template; placeholders `{namespace}` `{pod}` `{name}` `{kind}` `{context}` `{owner}` | unknown/empty placeholder → explicit message on use, no link. Defaults stay in code (not written to the file) |

## Load/save contract

- **Load** at startup; a malformed/unreadable file loads as empty defaults and the
  client still starts (a warning goes to the log, not a blocking error).
- **Save** on preference change; writes are atomic (write-temp-then-rename).
- **Live reload** (owner request 2026-08-21): the file's mtime is checked at
  refresh-tick cadence; an external change is reloaded and applied without a
  restart (a status message confirms it). A file that does not parse keeps the
  CURRENT in-memory settings — never a reset — and is reported. The client's
  own saves are not treated as external changes. In-app changes persist
  immediately, so file and session only race within one refresh interval;
  last writer wins.
- Forward compatibility: unknown keys are preserved on rewrite where feasible, or
  dropped safely; never crash on an unexpected key.

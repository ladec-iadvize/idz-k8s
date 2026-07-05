# Contract: Configuration File Schema

**Location**: `$XDG_CONFIG_HOME/idz-k8s/config.yaml` (default `~/.config/idz-k8s/config.yaml`), overridable with `--config`.

**Guarantee**: NEVER contains credentials or secret values (Principle IV). Holds
only a few preferences. (Customizable/saved views are P3 → not in the v1 schema.)

## Schema (v1)

```yaml
schemaVersion: 1                 # int, required
refreshIntervalSeconds: 5        # int >= 1; invalid → 5 (FR-006)
prometheusURL: http://prometheus.monitoring:9090   # single metrics source (D5); unset → metrics "unavailable"
theme: auto                      # "auto" | "dark" | "light" | "none"
lastContext: prod-eu             # string, optional convenience
```

## Field rules

| Field | Constraint | On violation |
|-------|-----------|--------------|
| `schemaVersion` | integer ≥ 1 | unknown/absent → best-effort load |
| `refreshIntervalSeconds` | integer ≥ 1 | missing/invalid → 5 |
| `prometheusURL` | valid URL | absent/invalid → all usage/trend visuals show "unavailable" (never a crash) |
| `theme` | one of enum | unknown → `auto` |
| `lastContext` | string | unresolved at runtime → fall back to kubeconfig current-context |

## Load/save contract

- **Load** at startup; a malformed/unreadable file loads as empty defaults and the
  client still starts (a warning goes to the log, not a blocking error).
- **Save** on preference change; writes are atomic (write-temp-then-rename).
- Forward compatibility: unknown keys are preserved on rewrite where feasible, or
  dropped safely; never crash on an unexpected key.

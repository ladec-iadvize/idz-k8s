# Contract: CLI Interface

Single binary `idz-k8s`. Thin CLI — a read-only overview tool; most interaction is
inside the TUI. There is **no** mutating flag or action; being read-only is the
nature of the tool, not an option.

## Invocation

```text
idz-k8s [flags]
```

No flags → opens the TUI on the operator's current kubeconfig context/namespace.

## Flags

| Flag | Type | Default | Purpose | Maps to |
|------|------|---------|---------|---------|
| `--kubeconfig` | path | `$KUBECONFIG` or `~/.kube/config` | kubeconfig to load | FR-001 |
| `--context` | string | current-context | start on a specific context | FR-003 |
| `-n, --namespace` | string | context default | start in a namespace | FR-003 |
| `--config` | path | `$XDG_CONFIG_HOME/idz-k8s/config.yaml` | preferences file | FR-006 |
| `--prometheus-url` | url | auto-discovered | Override the metrics source. By default the in-cluster Prometheus is auto-discovered per context and reached via the API server proxy (no port-forward). | FR-019, D5 |
| `--refresh` | duration | config / 5s | override refresh interval this run | FR-006 |
| `--no-mouse` | bool | false | disable mouse capture (keyboard-only) | FR-008 |
| `--no-color` | bool | false (honors `NO_COLOR`) | force plain rendering | FR-022 |
| `--version` / `-h, --help` | bool | — | print and exit | — |

## Behavior contract

- Exit code `0` on clean quit; non-zero on fatal startup error (e.g. no reachable
  kubeconfig) with a readable stderr message (FR-016).
- The tool issues **only read-oriented** operations; there is no code path to
  create/edit/delete/scale/exec, and no Helm upgrade/rollback/uninstall (FR-012).
- Unknown resource types, an unreachable/unset Prometheus, limited event
  retention, or a bad config MUST NOT prevent startup; they degrade to defaults /
  "unavailable" states (FR-021, FR-026-analogue).
- Diagnostics/logs go to stderr or a log file; the TUI owns stdout. When stdout is
  not a TTY, the tool errors clearly instead of rendering.

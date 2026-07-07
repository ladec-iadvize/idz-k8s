# Contract: Keybindings & Interaction (read-only)

Familiar, non-exotic bindings (FR-009), surfaced in a context-aware help overlay
(FR-010). Full keyboard/mouse parity (FR-008/FR-011, SC-003). There are **no
mutating bindings** — the tool is read-only. This is the authoritative list the
`help` overlay and `internal/ui/keys` must match.

## Global

| Key | Action | Mouse equivalent |
|-----|--------|------------------|
| `?` | Toggle help overlay | click `?`/help label |
| `q` / `Ctrl+C` | Quit | click quit label |
| `Esc` | Back / close overlay | click breadcrumb / close `×` |
| `:` | Jump to a resource type | click resource-type tab |
| `/` | Filter/search current list or timeline; vim-like highlighted search in content views (describe/YAML, Helm detail), then `n`/`N` navigate matches | click filter field |
| `Tab` / `Shift+Tab` | Move focus between panes | click target pane |

## Navigation (lists & views)

| Key | Action | Mouse equivalent |
|-----|--------|------------------|
| `↑`/`↓` | Move selection | click row |
| `PgUp`/`PgDn`, `Home`/`End` | Page / jump to ends | wheel scroll / scrollbar |
| `Enter` | Open selected resource detail | double-click / click "open" |
| `l` | Logs (single pod) | click "logs" |
| `L` | Merged logs across a workload's pods (FR-034) | click "all logs" |

## Context & namespace

| Key | Action |
|-----|--------|
| `c` | Context picker (single active context, FR-003) |
| `n` | Namespace picker |

## Debug / overview views (all read-only)

| Key | View | Maps to |
|-----|------|---------|
| `t` | Topology (pods ↔ nodes) | US4 / FR-013 |
| `v` | Events timeline | US5 / FR-014 |
| `g` | Dependency graph for the selection | US9 / FR-026 |
| `f` | Failure diagnostics (restarts/OOM/evictions) | US10 / FR-027 |
| `p` | Scheduling & capacity | US11 / FR-028 |
| `:helm` | Helm releases (read-only, via the type picker) | US12 / FR-029 |
| `u` | Top consumers (CPU/memory) | FR-035 |
| `z` | Sizing recommendations (advisory) | US6 / FR-023 |
| `p` | Posture / compliance overview (advisory) | US13 / FR-030 |
| `x` | Per-pod connectivity / NetworkPolicy view | US14 / FR-031 |
| `a` | Access (RBAC) view | US15 / FR-032 |
| `D` | Read-only diff: live vs last-applied | US16 / FR-033 |

## Customizable views (US8, FR-024/FR-025)

| Key | Action |
|-----|--------|
| `s` / `S` | Sort by next column / flip direction (persisted per type) |
| `C` | Column chooser: `Space` shows/hides, `←`/`→` reorders, `Enter` applies; "add custom field…" accepts a label key or a `.dot.path` object field |
| `V` | Views: save the current arrangement under a name, open or manage saved views |
| `R` | Reset the current type's view to its defaults |

The committed `/` filter is also remembered per type. All customizations live
in the local config file and tolerate invalid entries (FR-025).

## Rules

- No binding maps to an exotic/hard-to-reach-only combination (FR-009).
- No key performs a mutating action anywhere (read-only, FR-012).
- Secret reveal is an explicit key on a masked field (masked by default, no extra
  gate) — FR-015.
- The help overlay lists exactly the bindings active in the current view, generated
  from the same keymap source (FR-010).

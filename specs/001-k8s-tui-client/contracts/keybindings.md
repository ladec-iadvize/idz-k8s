# Contract: Keybindings & Interaction

Familiar, non-exotic bindings (FR-009), surfaced in a context-aware help overlay
(FR-010). Full keyboard/mouse parity (FR-008/FR-011, SC-003). *(v3)* Mutating
bindings exist, but **no key mutates directly**: every one opens a confirmation
modal or value prompt first (FR-012 v3, SC-006). The authoritative, test-enforced
binding list is `internal/ui/keys` (`TestEveryBindingHasHelp`); this contract
records the interaction rules and the stable core bindings, not every key.

## Global

| Key | Action | Mouse equivalent |
|-----|--------|------------------|
| `?` | Toggle help overlay | click `?`/help label |
| `q` / `Ctrl+C` | Quit | click quit label |
| `Esc` | Back / close overlay | click breadcrumb / close `×` |
| `:` | Jump to a resource type | click resource-type tab |
| `/` | One consistent behavior everywhere: filters row views (lists, Helm releases, events, sizing table) and searches content views with highlighting (describe/YAML, logs, failures, topology, posture, connectivity, access, diff, Helm detail), then `n`/`N` navigate matches | click filter field |
| `Tab` / `Shift+Tab` | Move focus between panes | click target pane |

## Navigation (lists & views)

| Key | Action | Mouse equivalent |
|-----|--------|------------------|
| `↑`/`↓` | Move selection | click row |
| `PgUp`/`PgDn`, `Home`/`End` | Page / jump to ends | wheel scroll / scrollbar |
| `Enter` | Drill DOWN one level of the resource chain (v3.5, FR-026); kinds outside the chain open the YAML detail | double-click / click "open" |
| `Esc` | Pop exactly one drill level (restores namespace scope when the drill moved it) | click breadcrumb |
| `Space` | Mark/unmark the row — marks scope analysis views and are the target of bulk actions (FR-037) | click mark cell |
| `l` | Logs (single pod) | click "logs" |
| `L` | Merged logs across a workload's pods (FR-034) | click "all logs" |

## Admin (v3 — every action confirmed, FR-012)

| Key | Action |
|-----|--------|
| `a` | Actions palette for the selection — and, in a drilled list, the PARENT's actions too (even on an empty level). Includes edit (merge apply), scale, rolling restart, delete, cordon/uncordon, suspend/resume/trigger CronJob, port-forward, shell, Helm rollback/uninstall, and the bulk *-marked variants (delete/restart/scale) |
| `e` | Edit YAML in `$EDITOR` — applied as a merge of original→edited, behind the confirmation contract |

## Logs view (FR-005)

| Key | Action |
|-----|--------|
| `w` | Toggle line wrap |
| `←`/`→` | Scroll long lines sideways |
| `M` | Insert a stream separator (redrawn at current width) |
| `Ctrl+L` | Clear the visible buffer (never stops the stream) |

## Events timeline (FR-014)

| Key | Action |
|-----|--------|
| `t` | Cycle the time scale (5m/15m/1h/6h/24h/all) — filters AND rescales, counts visible |

## Context & namespace

| Key | Action |
|-----|--------|
| `c` | Context picker (single active context, FR-003) |
| `n` | Namespace picker |

## Debug / overview views

**Since 2026-07-12 every analysis view opens from the `>` views palette**
(type-to-filter, like `:`); the per-view keys below are RETIRED. Navigation
keys (`:`/`n`/`c`/`/`) are global across views.

| Key | View | Maps to |
|-----|------|---------|
| `t` | Topology (pods ↔ nodes) | US4 / FR-013 |
| `v` | Events timeline | US5 / FR-014 |
| `g` | Dependency graph for the selection | US9 / FR-026 |
| `f` | Failure diagnostics (restarts/OOM/evictions) | US10 / FR-027 |
| `p` | Scheduling & capacity | US11 / FR-028 |
| `:helm` | Helm releases (via the type picker; detail: Enter = rendered manifest, `y` = live object, `v` = values) | US12 / FR-029 |
| `u` | Top consumers (CPU/memory) | FR-035 |
| `z` | Sizing recommendations (advisory) | US6 / FR-023 |
| `p` | Posture / compliance overview (advisory) | US13 / FR-030 |
| `x` | Per-pod connectivity / NetworkPolicy view | US14 / FR-031 |
| `a` | Access (RBAC) view | US15 / FR-032 |
| `D` | Diff: live vs last-applied (no apply affordance; fixing drift goes through edit) | US16 / FR-033 |

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
- *(v3)* No key performs a mutation DIRECTLY: every mutating flow passes through a confirmation modal or value prompt before any API call (FR-012 v3, SC-006 — test-enforced).
- Typing modes (picker, filter, prompts) swallow keys before global shortcuts.
- Secret reveal is an explicit key on a masked field (masked by default, no extra
  gate) — FR-015.
- The help overlay lists exactly the bindings active in the current view, generated
  from the same keymap source (FR-010).

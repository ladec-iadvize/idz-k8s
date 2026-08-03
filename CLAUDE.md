# CLAUDE.md — working on idz-k8s

Kubernetes overview & admin TUI (Go + Bubble Tea). This file captures the
non-negotiable invariants and the traps we already fell into. Read it before
changing anything.

## Non-negotiable invariants

0. **Consistency is a core product value** (owner directive 2026-07-09).
   Every view must offer the same interactions the same way: '/' filters row
   views and searches content views, tables sort with s/S + header click,
   gauges/verdicts/rule() headers share one visual language, marks (Space)
   scope the analysis views, Esc means clear-then-back. When adding a view,
   reuse the established patterns — and when you SPOT an inconsistency,
   report it to the owner instead of leaving it. Analysis views open ONLY
   from the '>' palette (no dedicated shortcuts); navigation keys
   (':'/'n'/'c'/'/') are global across views (owner decision 2026-07-12).

1. **No mutation without confirmation** (FR-012 v3, SC-006 v3 — the read-only
   rule was reversed by owner decision 2026-07-24). Admin actions (edit YAML,
   scale, rolling restart, delete, cordon/uncordon, suspend/resume,
   port-forward, Helm rollback/uninstall) live in `internal/kube/admin.go`,
   `portforward.go` and `internal/helm`. EVERY mutating flow must pass through
   the confirmation modal (`requestConfirm`) or a value prompt before any API
   call — never wire a mutation to a bare keypress. New admin verbs ship with
   an operation test (fakes, `tests/integration/admin_test.go`) AND a UI
   confirmation-gate test (`internal/ui/admin_test.go`). Mutations carry the
   `idz-k8s` field manager. Exec-into-pod shipped in v3.4 (owner request
   2026-07-31: 'shell' in the actions palette, native SPDY + TTY) along
   with CronJob triggering; node drain is still out of scope (a future
   spec change, not a patch).
2. **Never fabricate data** (FR-021 + project constitution). When Prometheus
   or any source is unreachable, show an explicit "unavailable" state — never
   estimate, never render an empty chart as if it were data.
3. **Secrets masked by default** (FR-015); nothing sensitive is ever written
   to the config file (`TestConfigFileNeverContainsSecrets`) or to logs.
4. **Spec-first**: scope changes go through `specs/001-k8s-tui-client/`
   (spec.md, tasks.md, checklists). Deviations from the constitution
   (`.specify/memory/constitution.md`) must be recorded in plan.md's
   Complexity Tracking.

## Workflow (v2+, owner decision 2026-07-06)

One branch per user story (`feat/us6-sizing`-style), one PR, **squash-merge
to main only after the CI run is green** — never merge red, never push a
story directly to main. Small doc/bookkeeping commits may go straight to
main. The merge is done by whoever runs the story (including Claude) once
CI passes.

Releases: tag `vX.Y.Z` on main → the Release workflow (goreleaser) builds
the binaries, publishes the GitHub release, and commits the updated
Homebrew formula (`Formula/idz-k8s.rb`) back to main. Users install/update
via `brew` (see README) — never tell them to `go build`.

## Definition of done (every change)

```bash
go build ./... && go vet ./... && golangci-lint run ./... && go test ./...
```

All four green locally before commit — CI (`.github/workflows/ci.yml`) runs
the same gate and a red gate blocks merge. New behavior ships with a test;
bug fixes ship with the regression test that would have caught them.

## Architecture (layering is load-bearing)

```
internal/kube     client-go (discovery incl. CRDs, lists via shared-informer cache
                  with direct-LIST fallback, logs, topology, diagnostics,
                  admin ops + port-forward — see invariant 1)
internal/metrics  Prometheus — the ONLY metrics source (instant + 1h range, API-proxy autodiscovery)
internal/helm     Helm release storage reader + rollback/uninstall actions (UI-confirmed)
internal/model    toolkit-agnostic domain types — no client-go, no Bubble Tea imports
internal/ui       Bubble Tea (app.go state machine, listview.go type-aware lists, theme/, keys/)
tests/            unit + integration (fakes only) + tui (teatest) — NEVER require a live cluster
```

The UI never talks to a data source directly; data layers never import UI.
Keep it that way — it is what makes everything testable with fakes.

## UI traps (all were real bugs — do not reintroduce)

- **Geometry is sacred.** Mouse click→row mapping assumes exact line positions
  (list rows start at y=3; picker modal rows via `pickerModal()` geometry).
  Any line added/removed in header/footer/rules shifts offsets in
  `handleMouse` — update them AND the tests together.
- **Column widths are content-driven and fill the terminal** (fitColumns in
  tablewidths.go, owner requests 2026-07-24 + 2026-07-28): every column gets
  at least its widest visible cell, leftover width is spread proportionally
  across ALL columns (never one giant gap — owner report 2026-07-10), and a
  narrow terminal shrinks them proportionally down to colMin. Never
  reintroduce fixed per-column widths, and never hard-code x offsets in
  geometry tests — compute them from listWidths/houseWidths/helmColWidths.
- **Nothing may wrap.** Header, status line and footer are truncated to the
  terminal width (`xansi.Truncate`); a wrapped line silently shifts all mouse
  coordinates.
- **Widths are counted in RUNES, never bytes** (`truncate`/`padTo`). Glyphs
  like `—`, `✓`, `●` are multi-byte; byte-counting produced phantom `…`.
- **Log rendering goes through `renderLogs()`** (logsview.go): the buffer is
  re-rendered on every new line, resize and toggle so the wrap ('w') and the
  horizontal offset (←/→) always apply — never append a raw line to the
  viewport. Slicing/folding log lines MUST use `xansi` (Wrap/TruncateLeft):
  merged lines carry a colored pod prefix and rune slicing cuts escape
  sequences, bleeding color across the screen.
- **Never accumulate content in a viewport's `View()`** — it is windowed and
  padded. Use a real buffer (see `logBuf`).
- **Typing modes must swallow keys BEFORE global shortcuts** (see the picker
  and events-filter blocks at the top of `handleKey`), or typing "q"/"m"
  quits the app / toggles the mouse.
- **All styling lives in `internal/ui/theme/`** (plus per-view render funcs).
  No hardcoded colors in views; everything must degrade under `NO_COLOR`
  (symbols ✓/!/✗ carry meaning without color).
- Keybindings live ONLY in `internal/ui/keys/` with help text —
  `TestEveryBindingHasHelp` fails on an undiscoverable binding. Per-screen
  visibility is `screenKeymap()`.

## Drill chain (Enter) — owner request 2026-07-31

`Enter` walks DOWN one level; `Esc` pops exactly one (`drillStack` in
`internal/ui/drill.go`). The chain table (`drillChain`) maps a kind to its
child type and HOW children are selected: `bySelector` (workloads/Service →
Pods), `byOwner` (CronJob → Jobs, via ownerReferences UID), `byNode`,
`byNamespace` (the only drill that moves the user's ns scope — Esc restores
it), `byNames` (Ingress → its backend Services). Pods are the leaf: they
open the containers view (`internal/ui/containers.go`). Adding a level is
one `drillChain` entry + a cells test. Kinds absent from the table keep
opening the YAML detail.

## Kubernetes specifics

- Use `discovery.k8s.io/v1 EndpointSlice`, not `v1 Endpoints` (deprecated
  K8s ≥1.33) — legacy fallback exists. Server deprecation warnings are
  silenced via `rest.NoWarnings{}` (they corrupt the TUI otherwise).
- Cross-namespace label selectors collide (every `*-back` namespace has
  `app=back`): always scope selector queries to the owning namespace
  (see `drillNamespace`).
- Type-aware list columns live in `internal/ui/listview.go`
  (`columnsForType`) — adding a type is ~15 lines + a cells test in
  `drill_test.go` (`TestDedicatedColumnsPerType`). Default-VISIBLE columns
  mirror `kubectl get -o wide` (owner decision 2026-07-12); anything extra
  (usage, karpenter, images…) carries `off: true` — available in the 'C'
  chooser, never default. User-specific columns belong in the user's
  viewPrefs, not in code.

## Testing conventions

- Fakes only: `tests/integration/harness.go` (fake dynamic/clientset —
  register new list kinds there), stub Prometheus HTTP server, Helm
  in-memory storage driver (beware: `Memory.Create` pins the driver
  namespace — call `SetNamespace("")` AFTER seeding).
- White-box UI tests live in `internal/ui/*_test.go`; `Update` may return
  `Model` or `*Model` — use the `asModel` helper.
- Scale guard: `TestScaleListAndTopology` (5,000 pods / 100 nodes, <3s).

## Language & style

- Code, comments, commit messages, docs: **English**. Conversation with the
  owner: French.
- Commits: conventional prefixes (`feat:`, `fix:`, `ci:`, `docs:`), body
  explains the why; end with the Co-Authored-By trailer when authored with
  Claude.

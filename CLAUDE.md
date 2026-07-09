# CLAUDE.md — working on idz-k8s

Read-only Kubernetes overview TUI (Go + Bubble Tea). This file captures the
non-negotiable invariants and the traps we already fell into. Read it before
changing anything.

## Non-negotiable invariants

0. **Consistency is a core product value** (owner directive 2026-07-09).
   Every view must offer the same interactions the same way: '/' filters row
   views and searches content views, tables sort with s/S + header click,
   gauges/verdicts/rule() headers share one visual language, marks (Space)
   scope the analysis views, Esc means clear-then-back. When adding a view,
   reuse the established patterns — and when you SPOT an inconsistency,
   report it to the owner instead of leaving it.

1. **Strictly read-only** (FR-012, SC-006). Never wire a mutating Kubernetes
   verb (`create/update/patch/delete/eviction/exec`) or a mutating Helm action
   (`Install/Upgrade/Rollback/Uninstall`). This is enforced by tests:
   `TestAllReadFlowsIssueOnlyReadVerbs` sweeps every kube path and
   `TestHelmPackageNeverConstructsMutatingActions` greps the helm package
   source. The only allowed `create` is `SelfSubjectRulesReview` (read-only
   introspection). An opt-in admin mode is a **v3 spec change**, not a patch.
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

## Definition of done (every change)

```bash
go build ./... && go vet ./... && golangci-lint run ./... && go test ./...
```

All four green locally before commit — CI (`.github/workflows/ci.yml`) runs
the same gate and a red gate blocks merge. New behavior ships with a test;
bug fixes ship with the regression test that would have caught them.

## Architecture (layering is load-bearing)

```
internal/kube     read-only client-go (discovery incl. CRDs, lists via shared-informer cache
                  with direct-LIST fallback, logs, topology, diagnostics)
internal/metrics  Prometheus — the ONLY metrics source (instant + 1h range, API-proxy autodiscovery)
internal/helm     Helm release storage reader (storage access only, no action pkg mutations)
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
- **Nothing may wrap.** Header, status line and footer are truncated to the
  terminal width (`xansi.Truncate`); a wrapped line silently shifts all mouse
  coordinates.
- **Widths are counted in RUNES, never bytes** (`truncate`/`padTo`). Glyphs
  like `—`, `✓`, `●` are multi-byte; byte-counting produced phantom `…`.
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

## Kubernetes specifics

- Use `discovery.k8s.io/v1 EndpointSlice`, not `v1 Endpoints` (deprecated
  K8s ≥1.33) — legacy fallback exists. Server deprecation warnings are
  silenced via `rest.NoWarnings{}` (they corrupt the TUI otherwise).
- Cross-namespace label selectors collide (every `*-back` namespace has
  `app=back`): always scope selector queries to the owning namespace
  (see `drillNamespace`).
- Type-aware list columns live in `internal/ui/listview.go`
  (`columnsForType`) — adding a type is ~15 lines + a cells test in
  `drill_test.go` (`TestDedicatedColumnsPerType`).

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

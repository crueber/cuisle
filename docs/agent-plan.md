# Cuisle: the fastest path to "development complete" with a raft of Haiku subagents

**Goal:** get from this docs-only repo to roadmap **M0–M7 done** (feature-complete, tested, within budgets) in the
least wall-clock time. M8 (release) follows. The plan spends strong-model effort where mistakes are expensive (the Wails
spike, contracts, tests as specification, the sync apply, merges). Everything else, roughly 85% of the code, goes to many
small, parallel **Haiku** implementers working against frozen contracts and failing tests.

> TL;DR for the orchestrator:
> 1. **Wave 0, you:** spike Wails, scaffold, freeze contracts. Have Sonnet test-authors turn the specs into failing tests.
> 2. **Waves 1–3:** fan out Haiku implementers, one work package (WP) each, in isolated git worktrees, at most
>    8–10 at a time.
> 3. **Every WP** must pass its gate. Merge in dependency order and run the full suite after each merge.
> 4. **Escalate** a WP to Sonnet after two failed Haiku attempts. Never let an implementer change a contract.

---

## 1. Roles and models

| Role | Model | Where defined | Does | Never does |
|---|---|---|---|---|
| **Orchestrator** | The main Claude Code session (Opus, or Sonnet if Opus isn't available) | `CLAUDE.md` → `AGENTS.md` → this file | Wails spike; scaffold; contracts; dispatch; merges; escalations; doc updates; human checkpoints | Write leaf implementations that a WP covers |
| **Test author** | Sonnet | `.claude/agents/cuisle-test-author.md` | Turns spec sections into failing tests and golden files before implementation | Implements production code |
| **Implementer** | **Haiku** | `.claude/agents/cuisle-implementer.md` | One WP: makes the given tests pass inside its owned paths | Touches files outside its ownership; adds dependencies; edits contracts or docs |
| **Reviewer** | Sonnet | `.claude/agents/cuisle-reviewer.md` | Reviews a finished WP diff against its spec sections and the invariants (AGENTS.md §3); runs the gates | Rewrites the WP itself (it returns findings) |

Haiku is fast and cheap, but weaker at cross-cutting design and at ambiguous specs. The plan removes both problems.
Every Haiku task gets frozen interfaces, an explicit file allowlist, a short reading list, and **tests that already fail
for the right reason**.

## 2. Ground rules (enforced by the gate, not by trust)

1. **Contracts first, frozen.** Wave 0 creates every package's exported types, interfaces and function signatures, the
   SQL schema, the bound-service method set, the view-model types, the generated TS bindings, the frontend API facade
   and the layout function signatures. Implementers may not change any exported signature. If a contract looks wrong,
   they stop and report `CONTRACT-ISSUE: …`; only the orchestrator changes contracts, then re-dispatches.
2. **Tests are the spec.** Each WP ships with failing tests written in Wave 0 (or 0.5) from `testing.md`, `ux-spec.md`
   §5–§7 and the golden cases. A WP is done when its tests pass, not when the code "looks right".
3. **File ownership.**
   - Each WP has an allowlist of paths. The gate rejects any diff outside it (`git diff --name-only main...HEAD`).
   - Shared files belong to the orchestrator: `go.mod`, `go.sum`, `package.json`, the lockfile, `Taskfile.yml`, docs,
     contracts, generated bindings.
   - An implementer that needs a doc change writes `docs/wp-notes/WP-NN.md` instead.
4. **No new dependencies.** The runtime dependency sets are fixed (AGENTS.md §3). Dev dependencies are added by the
   orchestrator in Wave 0.
5. **Isolation.** One git worktree and branch per WP (`wp/NN-slug`), via the Agent tool's `isolation: "worktree"`.
   Merges happen only in the orchestrator's checkout.
6. **Small WPs.** Target 150–600 lines of production code, ≤ 8 files, one package or one component family, and a reading
   list of ≤ 4 doc sections. Split anything bigger.
7. **Escalation.** If a WP fails its gate twice with Haiku, re-run it with Sonnet and the reviewer's findings. If it fails
   twice with Sonnet, the orchestrator takes it.

## 3. The gate (run by the orchestrator, or the reviewer, before merging a WP)

```
git diff --name-only main...wp/NN | scripts/agent/check-ownership WP-NN      # allowlist (Wave 0 writes this script)
go build ./... && go vet ./... && go run ./scripts/layercheck
CGO_ENABLED=0 go test -tags purego ./<owned packages>/... -count=1                     # backend WPs
(cd frontend && npx tsc --noEmit && npx eslint <owned> && npx vitest run <owned tests>)   # frontend WPs
no edits to: contracts (internal/**/contract*.go, internal/model, internal/app/types.go, migrations/0001_init.sql,
             frontend/src/api/types.ts, frontend/src/layout/*.d.ts), go.mod/go.sum, package*.json
reviewer verdict (UI, sync and anything touching persistence): PASS
```

After each merge into `main`, run the full suite (`task lint test`). If it fails, revert the merge and send the WP back
with the failure.

## 4. Waves

```
W0  orchestrator + Sonnet test authors     spike → scaffold → contracts → failing tests → fixtures       (serial, ~3–4 h)
W1  Haiku × 15  leaves                      no dependency between them                                     (~1–1.5 h)
W2  Haiku × 13  domain + screens            needs W1                                                       (~1.5–2 h)
W3  Haiku × 3 + Sonnet × 1  integration     needs W2: sync, view, app wiring                               (~1.5 h)
W4  Haiku × 5 + Sonnet × 1  verification    screenshot parity, perf, a11y, benches, theming, packaging     (~2 h)
W5  human checkpoint + Haiku bug pool       real Jira site, screen share, three OSes                       (~half day, human-paced)
```

These times are wall-clock estimates assuming 8–10 concurrent agents and a cooperative toolchain. The critical path is
**W0 → WP-06/07 → WP-18/19 → WP-29/30 → WP-32 → WP-33/34**. The frontend runs in parallel against `api/mock.ts` and only
meets the real backend at WP-32.

### Wave 0: orchestrator (do not delegate the spike)

| Step | Output | Notes |
|---|---|---|
| 0.1 Wails v3 spike | Roadmap M0 items 1–5 | Highest-risk unknown (architecture R1). Fix A §3 and §4.4 wording to reality before writing contracts. |
| 0.2 Scaffold | All directories from A §2 and §4.2; `Taskfile.yml`; tokens generator; fonts; CI files made real | Roadmap M0 items 6–10 |
| 0.3 Contracts (Go) | See the list below | Exported types and signatures only. Bodies are `panic("WP-NN")` or `return …, apperr.NotImplemented`. |
| 0.4 Contracts (TS) | `api/types.ts` (re-export of generated bindings), the `api/index.ts` facade signature, `api/events.ts`, `layout/*.ts` signatures that throw, `components/*` prop interfaces, `state/*` signal declarations | |
| 0.5 Failing tests | One Sonnet test-author per row of §5, in parallel | Tests reference only contracts. Golden files are written now: the view JSON, the export JSON, the layout cases (T §4). |
| 0.6 Fixtures | The `fixture.json` shape and the mock `view_mob.json` | Content is generated by WP-08 and WP-14. Wave 0 fixes the shapes only. |
| 0.7 Agent tooling | `scripts/agent/check-ownership` (reads the §5 allowlists from `docs/agent-plan.ownership.txt`), install the subagents: `mkdir -p .claude/agents && cp scripts/agent/subagents/*.md .claude/agents/` (sources live in `scripts/agent/subagents/`; commit both) | |
| 0.8 Checkpoint | Chris skims the contracts (optional, ~15 min). `go build ./...` and `tsc` are green, and tests fail for the right reason. | |

**Contracts to freeze in 0.3:**
- `internal/model` (A §2.1).
- `internal/apperr` (codes, A §3.4).
- `internal/source` (`Source`, `Capabilities`, registry).
- `internal/sink` (`Sink`, `Field`, registry, `Dispatcher`).
- `internal/secrets.Keyring`.
- `internal/sync.Emitter` / `Clock`.
- `internal/cache`, `internal/settings` and `internal/view` exported function sets.
- `internal/export` document types (`cuisle.export.v1`).
- `internal/history.LoadAsOf`.
- `internal/app`: every service struct with every bound method (A §3.2), plus all view-model and event payload types.
- `migrations/0001_init.sql` (D §2 verbatim).

### Work packages

Columns:
- **Owns** is the allowlist (tests live beside the code they cover and are owned by the same WP).
- **Reads** is the minimal reading list.
- **Done when** is the WP-specific part of the gate.

#### Wave 1: leaves (parallel)

| WP | Model | Owns | Depends | Reads | Done when |
|---|---|---|---|---|---|
| 01 | Haiku | `internal/paths`, `internal/logx`, `internal/buildinfo` | W0 | A §6.2, §6.4 | `go test ./internal/{paths,logx,buildinfo}/...` (incl. redaction) |
| 02 | Haiku | `internal/store` (not `migrations/0001`) | W0 | A §6.3; D §3 | migration runner, backups, weekly `VACUUM INTO`, newer-schema refusal tests |
| 03 | Haiku | `internal/secrets` | W0 | A §2 (Keyring), §6.4 | in-memory + go-keyring impl; account naming tests |
| 04 | Haiku | `internal/pulse/segments*.go`, `lane*.go`, `blocked*.go` | W0 | J §5.1–5.5; U §7 | segments, days-in-lane, over-limit, blocked spans, `asOf` tests |
| 05 | Haiku | `internal/pulse/marks*.go`, `label*.go`, `speakers*.go` | W0 | J §5.3, §5.8; T §2 pulse | TZ/DST buckets, mark levels, `Pulse.Label`, daily shuffle tests |
| 06 | Haiku | `internal/source/jira/client*.go`, `policy*.go`, `errors*.go` | W0 | J §1, §3 | auth, pagination, Retry-After, backoff, 4-concurrency, cancel tests (against httptest) |
| 07 | Haiku | `internal/source/jira/jiratest/server*.go`, `faults*.go`, `fixture*.go` | W0 | J §6 | endpoint handlers + fault injection tests |
| 08 | Haiku | `internal/source/jira/jiratest/jql*.go`, `cmd/fixturegen`, `testdata/fixture.json` | W0 | J §2.8, §6 | JQL subset tests; fixturegen reproduces the mockup scenes |
| 09 | Haiku | `internal/export` | W0 | A §3.5 | golden `cuisle.export.v1`; privacy scan test |
| 10 | Haiku | `frontend/scripts/tokens.mjs`, `frontend/src/styles/base.css`, `frontend/src/assets/fonts`, `components/Icon*` | W0 | A §4.7; U §10–11; `design/tokens.json` | tokens generator tests (key mismatch fails); fonts ≤ 160 KB |
| 11 | Haiku | `frontend/src/components/Combobox*` | W0 | A §4.6; U §9.3 | Combobox unit + axe tests |
| 12 | Haiku | `frontend/src/components/{Segmented,Switch,Stepper,Checkbox,TextField,Toast,SavedIndicator,Banner,KeyLegend}*` | W0 | U §9.3, §10 | component tests |
| 13 | Haiku | `frontend/src/layout/*` | W0 | U §3–§7; T §4 | every T §4 golden case passes |
| 14 | Haiku | `frontend/src/api/mock.ts`, `frontend/src/api/fixtures/*`, `scripts/mockfixture` | W0 | A §4.2–4.4; `design/reference/sample-data.json` | mock facade serves `view_mob.json` etc.; type-checks against bindings |
| 15 | Haiku | `frontend/src/state/*`, `frontend/src/keys.ts` | W0 | A §4.3, §5.3; U §8 | signal stores; autosave queue tests (debounce, 250 ms indicator, retry, ordering) |

#### Wave 2: domain logic and screens (parallel, after their W1 deps merge)

| WP | Model | Owns | Depends | Reads | Done when |
|---|---|---|---|---|---|
| 16 | Haiku | `internal/settings/standup*.go`, `app_settings*.go`, `uistate*.go` | 02 | D §2.1–2.3; A §3.2 ConfigService | CRUD, archive/restore, move, CopySettingsFrom, DiscardDraft tests |
| 17 | Haiku | `internal/settings/lanes*.go`, `columns*.go`, `speakers*.go` | 02 | D §2.3; T §2 settings | walk order, limits, RebuildStatusLanes, speakers + seed tests |
| 18 | Haiku | `internal/source/jira/endpoints_ref*.go` (spaces, boards, config, sprints, users, statuses, fields, validate) | 06, 07, 08 | J §2.1–2.7, §2.12 | mapping-to-model tests against jiratest |
| 19 | Haiku | `internal/source/jira/plan*.go`, `scan*.go`, `hydrate*.go`, `dev*.go` | 06, 07, 08 | J §2.8–2.11, §4.3 | search/jql paging, bulk changelog, comments top-up, dev-status degrade tests |
| 20 | Haiku | `internal/cache` (read side + write helpers, not apply) | 02 | D §2.4–2.6, §4 | `LoadCurrent` snapshot tests on seeded DB |
| 21 | Haiku | `internal/sink` (dispatcher, outbox), `internal/sink/file` | 02, 09 | D §2.8; ADR 0012 | idempotency, ≤ 8 attempts, no timers, atomic file write tests |
| 22 | Haiku | `internal/history` (stub + test helper) | 20 | ADR 0011 | stub returns `not_implemented`; reconstruction helper used by T §3 test compiles |
| 23 | Haiku | `frontend/src/views/standup/{StandupScreen,Header,Tabs,StandupSwitcher,Rail}*` | 10–15 | U §2, §4; A §4.5 | component tests; tab modes at 4 widths |
| 24 | Haiku | `frontend/src/views/standup/{PersonView,TicketRow,PulseTrack,TrackBackdrop,MoreBar,Legend}*` | 10–15 | U §5, §7 | density, more bar, track geometry render tests |
| 25 | Haiku | `frontend/src/views/standup/{BoardView,DoneList,BacklogList}*` | 10–15 | U §6 | lane balancing render, lists, Unassigned lane tests |
| 26 | Haiku | `frontend/src/views/configure/{ConfigureScreen,StandupList,JumpBar}*`, `sections/{Source,StatusMapping}*` | 10–15 | U §9.1–9.5 (Source, Mapping) | autosave wiring tests via mock |
| 27 | Haiku | `frontend/src/views/configure/sections/{Speakers,PersonView,Pulse,WholeBoard}*` | 10–15 | U §9.4 | ordered lists, walk-order presets, preview track tests |
| 28 | Haiku | `frontend/src/views/configure/sections/{Export,Display,Connection}*`, `frontend/src/views/connect/*` | 10–15 | U §9.4, §9.7; product-spec F10 | sink rows, Connect form tests |

#### Wave 3: integration

| WP | Model | Owns | Depends | Reads | Done when |
|---|---|---|---|---|---|
| 29 | Haiku | `internal/sync/{orchestrator,plan,hydrate,progress}*.go` | 18, 19, 20 | J §4.1–4.5; A §5.4 | phase, progress, cancel, 429 tests over jiratest |
| 30 | **Sonnet** | `internal/sync/apply*.go`, `internal/cache/apply*.go` | 16, 17, 20, 21 | J §4.6; D §2.5–2.7; ADR 0009, 0011 | atomic apply; never-delete; spans; config history; speaker discovery; after-sync enqueue; T §3 reconstruction test |
| 31 | Haiku | `internal/view` | 04, 05, 16, 17, 20 | A §3.2, §5.2; ADR 0008 | golden `view_mob.golden.json` == `frontend/src/api/fixtures/view_mob.json`; `BenchmarkStandupView` |
| 32 | Haiku → reviewer | `internal/app`, `cmd/cuisle` | 21, 22, 29, 30, 31 | A §3, §5.1, §6.3 | every bound method wired; `--fake-jira`; boot.js; `day:rolled`; no-network-without-trigger test; app runs against the real bindings |

#### Wave 4: verification and polish (parallel)

| WP | Model | Owns | Reads | Done when |
|---|---|---|---|---|
| 33 | Haiku | `frontend/tests/screens/*` | T §5; U §13 | screenshot parity at 4 sizes. Diffs over threshold become a fix list for the orchestrator to dispatch. |
| 34 | Haiku | `frontend/tests/perf/*`, `frontend/tests/a11y/*` | A §6.1; U §10 | budgets and axe green, light and dark |
| 35 | Haiku | `internal/**/bench_test.go`, `cmd/cuisle` bench flag | A §6.1 | benchstat baselines committed; `--bench-startup` JSON |
| 36 | **Sonnet** | `design/tokens.json` (dark values), `frontend/src/styles/*` | U §10–11 | dark contrast verified; Large (TV) layouts screenshot-reviewed |
| 37 | Haiku | `build/**`, `scripts/publish/*`, `.forgejo/workflows/*`, `scripts/update-pins.sh`, `internal/relsign` | A §6.5; ADR 0013 | `task package` on Linux + macOS (local); publish plan-mode clean; release.yml lint |
| 38 | Haiku pool | bug fixes from 33, 34 and W5 | per bug | per bug |

## 5. Dispatching a WP (orchestrator)

```text
Agent(subagent_type="cuisle-implementer", isolation="worktree", description="WP-24 person view", prompt=<template>)
```

**Prompt template** (fill every field; keep it under ~60 lines; Haiku performs best with exact paths and commands):

```
WORK PACKAGE: WP-24 — Person view rows, pulse track, more bar
BRANCH: wp/24-person-view (you are in your own worktree)
OWNED PATHS (you may create/edit only these):
  frontend/src/views/standup/PersonView*, TicketRow*, PulseTrack*, TrackBackdrop*, MoreBar*, Legend*
READ FIRST (only these):
  docs/ux-spec.md §5 and §7 · frontend/src/api/types.ts (StandupView, TicketView, Pulse) ·
  frontend/src/layout/density.ts, below.ts, track.ts (implemented, use them) · design/mockups/person-1920x1080.png
CONTRACTS (frozen — do not change): frontend/src/api/types.ts, frontend/src/layout/*.ts signatures, component props in
  frontend/src/views/standup/*.props.ts
TESTS TO MAKE PASS: frontend/src/views/standup/PersonView.test.tsx, PulseTrack.test.tsx, MoreBar.test.tsx
COMMANDS: cd frontend && npx vitest run src/views/standup && npx tsc --noEmit && npx eslint src/views/standup
RULES: AGENTS.md §3 invariants. No new dependencies. No raw hex colours (tokens only). Never hide tickets to fit.
IF A CONTRACT OR TEST LOOKS WRONG: stop, do not work around it; reply "CONTRACT-ISSUE: <file>: <problem>".
WHEN DONE: commit on your branch with message "WP-24: <summary>", then reply with: files changed, test output tail,
  anything you were unsure about.
```

**Throughput tips** (from what worked on Weave, AGENTS.md §9):
- **Merge order:** the slice others depend on goes first.
- **After every merge:** run `go mod tidy` and regenerate bindings and tokens. Never hand-merge generated files.
- **Expect conflicts** only in orchestrator-owned files, by design.
- **Keep the queue full:** when one agent returns, dispatch the next ready WP immediately (the dependency columns
  above).
- **Batch reviews:** one reviewer pass can check 2–3 small Haiku WPs together.
- **Screenshots:** Haiku can read them. Give UI WPs the matching mockup PNG and let the reviewer compare a Playwright
  screenshot of the mock-mode app against it.

## 6. Human checkpoints (Chris)
1. **After W0:** skim the contracts and the list of failing tests (optional, ~15 min).
2. **After W2:** run `task dev` (fake Jira) and click through the screens. Visual sign-off before polish.
3. **W5:** a real Jira site, a screen share over Zoom/Teams, and a Windows build from GitHub after a publish (Chris runs
   "publish to github").

## 7. Definition of "development complete"

Done means all of the following hold:
- Roadmap M0–M7 DoDs are met.
- The full `task lint test` suite is green on Forgejo (Linux) and on GitHub (Linux/macOS/Windows) after a publish.
- Every A §6.1 budget passes.
- Screenshot parity holds within threshold.
- Zero axe violations.
- The T §6 manual script passes.

M8 (signed artifacts and release notes) is the next step and is driven by Chris's publish.

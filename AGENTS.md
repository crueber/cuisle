# AGENTS.md: working on Cuisle

Read this first. It's the fast path: what the project is, the rules that must not break, how to build, verify and
release, and how work is split across agents. The contracts live in `docs/`. This file tells you which one to open.

## 1. What this is

Cuisle (Irish for "pulse") is a desktop app for running team standups from Jira Cloud boards: Go + **Wails v3** +
**Preact/@preact/signals** + SQLite (pure Go). It shows every ticket for the person speaking on one screen with a per-ticket
pulse track, a whole-board view, multiple standups (tabs), and a Configure screen that autosaves. Jira data is synced
explicitly and kept locally, so it opens instantly and builds up history. The core is provider-neutral: Jira is one
**source** adapter, and outbound **sink** adapters (v1: File/JSON) send standup data to other systems.

- **Repo:** `https://github.com/crueber/cuisle.git` (Forgejo, private origin), branch `main`. GitHub
  (`github.com/crueber/cuisle`) is a **published mirror** (§8).
- **Module path:** `github.com/crueber/cuisle`. **Bundle id:** `crueber.cuisle`. **Wordmark:** "CUISLE".
- **Status:** all work packages implemented and merged; final polish, CI bring-up and UAT in progress. See the
  progress list in CLAUDE.md and docs/wp-notes/orchestrator-backlog.md.

### Standing preferences from the maintainer (don't re-litigate)
- **Nothing is hidden to make things fit.** Tighten density, then scroll with the "Scroll for more — X below" bar. Never
  collapse tickets to counts or summaries for space.
- **No horizontal scrolling, anywhere in the app** (invariant 16). Wrap, tighten or stack instead.
- **Sync is explicit.** Resync button/`R`, an optional sync-on-open when stale, and leaving Configure after source
  changes. **No background polling, ever.**
- **Autosave everything.** No Save buttons. **Every dropdown is an autocomplete** (the shared `Combobox`).
- **Keep synced data.** History is never pruned (a history view comes later). Deleting a standup archives it.
- **Adapters, not hard-wiring.** Sync depends on `source.Source`; exports go through `sink.Sink`. Jira is one adapter.
- **Light and fast.** Frontend runtime deps are exactly `preact`, `@preact/signals`, `@wailsio/runtime`. The budgets in
  architecture §6.1 are CI gates.
- **Legible over a screen share** (big mono keys, chips, high contrast); light and dark themes both supported.
- **Agents never publish to GitHub** and never push tags. An agent's job ends at the Forgejo push, and only when Chris
  asks.
- Commit messages end with the `Co-Authored-By: Claude …` trailer given in the session.

## 2. Repository map

Planned: Wave 0 creates it. Details are in architecture §2 and §4.2.

```
cmd/cuisle             main: flags (--fake-jira, --bench-startup), paths, logger, store, adapter registration, services, window
cmd/fixturegen         design/reference/sample-data.json → internal/source/jira/jiratest/testdata/fixture.json
internal/model         provider-neutral domain types (stdlib only)          internal/apperr   typed errors + codes
internal/source        Source interface, Capabilities, registry             internal/source/jira  Jira Cloud adapter
internal/source/jira/jiratest  fake Jira HTTP server (fixture, JQL subset, fault injection)
internal/sink          Sink interface, registry, outbox Dispatcher          internal/sink/file    v1 reference sink
internal/export        cuisle.export.v1 document builder                     internal/history      LoadAsOf (stub in v1)
internal/store         SQLite open/pragmas/migrations/backups (no rules)     internal/cache        neutral record store, LoadCurrent, apply
internal/settings      app + standup config                                  internal/secrets      Keyring (service crueber.cuisle)
internal/sync          orchestrator over source.Source                       internal/pulse        pure pulse math (asOf)
internal/view          StandupView/Config/Bootstrap from a BoardSnapshot     internal/app          Wails services (thin)
internal/paths internal/logx internal/buildinfo internal/relsign (M8, port of Weave's release signing)
frontend/              Preact app: src/{api,state,layout,views,components,styles}, scripts/tokens.mjs, tests/
design/                mockups/*.png (approved), reference/ (DOM snapshots, prototype-logic.js, sample-data.json), tokens.json
docs/                  product-spec, ux-spec, architecture, data-model, jira-sync, roadmap, testing, agent-plan, adr/, wp-notes/
build/                 Wails build assets; build/ci/linux.Dockerfile (Forgejo CI image)
scripts/               layercheck/, agent/check-ownership, publish-to-github.sh, publish/{ci,release}.yml, update-pins.sh
.forgejo/workflows/    ci.yml (Linux), ci-image.yml, publish-github.yml
.claude/agents/        cuisle-implementer (haiku), cuisle-test-author (sonnet), cuisle-reviewer (sonnet); installed from scripts/agent/subagents/ in Wave 0
```

### Which doc answers what

| Question | Doc |
|---|---|
| What and why, feature acceptance criteria, product decisions | `docs/product-spec.md` |
| Exact layout, sizes, density ladder, pulse geometry, keyboard, Configure sections | `docs/ux-spec.md` (it wins over the mockups; §13 lists the differences) |
| Packages, bound services/methods, events, errors, frontend stack, budgets, CI/signing, risks | `docs/architecture.md` |
| SQLite DDL, migrations, history and retention, sinks outbox | `docs/data-model.md` |
| Jira endpoints, sync algorithm, pulse mapping, fake Jira | `docs/jira-sync.md` |
| Milestones and DoD | `docs/roadmap.md` |
| What must be tested, golden cases | `docs/testing.md` |
| How to split the work across agents | `docs/agent-plan.md` |
| Decisions already made | `docs/adr/0001…0015` |

## 3. Invariants (tests and reviewers enforce these)

1. **Never hide tickets to fit** (ux-spec §5.5–5.6, §6.4). Density ladder, then scroll and the more bar.
2. **No network without a trigger.** The only allowed triggers are:
   - Resync, the sync-on-open policy, and leaving Configure after source changes.
   - Jira-backed combobox typing, board pick and filter validation, and Connect.
   - Explicit sink sends or after-sync sends.
   - In the ticket modal: opening the Status combobox (transitions), the four writes, **Refresh this ticket**, and
     opening an attachment or a full image not yet cached. Opening the modal itself is offline. A test asserts this with the fake Jira request log. No tickers, no polling, no retry timers.
3. **Autosave.** Every Configure control persists on change (text 400ms debounce). There's no Save button; **Done**
   returns.
4. **Every dropdown is the shared `Combobox`.** No native `<select>`.
5. **Secrets only in the OS keychain** (service `crueber.cuisle`; accounts `<source>:<host>:<account>`, `sink:<id>`).
   Never in SQLite, logs, crash output, exports or the frontend.
6. **Never delete synced history.**
   - Record tables append. Issues that leave get `removed_at`; membership is spans.
   - Config history is fingerprinted.
   - Deleting a standup archives it.
7. **Layering** (architecture §2 table, `scripts/layercheck`):
   - `sync` imports `source`, never `source/jira`.
   - Only `cmd/cuisle` imports concrete adapters.
   - Only `internal/app` and `cmd/cuisle` import Wails.
   - Nothing imports `app`.
8. **Go computes data; TS lays out** (ADR 0008). View models come from `view.Build(snapshot, asOf)`. TS never derives
   pulse data.
9. **Pure-Go SQLite.** `CGO_ENABLED=0 go test -tags purego ./internal/...` must pass (the `purego` tag is how Wails v3.1 compiles without cgo; it only matters for `internal/app`).
10. **Frontend runtime deps are fixed** (`preact`, `@preact/signals`, `@wailsio/runtime`). Adding one needs an ADR and
    the 70 KB gz JS budget (ADR 0014, as amended for F12).
11. **Tokens only.** No raw hex in components. Lane colours via `data-lane`. Sizes scale with `--scale` (Large/TV).
12. **Exports, logs and crash output carry no comment bodies, emails or secrets** (`cuisle.export.v1`, additive-only
    within v1). Comment bodies *are* stored locally (ADR 0015).
13. **Jira writes are explicit user actions only.** The only writes are transition, assign, flag and add comment, from
    the ticket modal. Each is sent once, optimistically, and reverted on failure, with no automatic retry. Success
    re-fetches the one issue through the normal apply (history recorded).
14. **Cuisle renders Jira rich text itself.** Go normalises ADF (`internal/adf`); Preact renders typed nodes. Never
    `innerHTML` and never `renderedFields` HTML.
15. **Every Jira request goes through the connection's limiter** (jira-sync §3.1): token bucket, header-adaptive, a
    global pause on 429, and interactive requests ahead of bulk. Never call the HTTP client around it, and never add
    retry loops outside it.
16. **Nothing scrolls sideways.** Not the window, a screen, a panel, the modal or a box inside it (code blocks and tables
    wrap). Lay out with grid/flex and `minmax(0, 1fr)`/`min-width: 0`; never CSS multi-column (WebKit fragments it
    sideways). `tests/screens/no-sideways-scroll.spec.ts` checks every screen at five sizes; run `task e2e:webkit` too
    when touching layout, because the app's webview is WebKit and CI's Playwright is Chromium.

## 4. Everyday commands

Keep this list current. Frontend tools are called as `./node_modules/.bin/<tool>` from `frontend/` (some sandboxes
block `npx` and `npm run`; the Taskfile never uses them).

```
task dev              # app against the fake Jira (CUISLE_FAKE_JIRA=default, data in ./.dev-data), hot reload
task dev:real         # app against your Jira site (token from the keychain)
task dev:mock         # frontend only, in a browser, against src/api/mock.ts (vite --mode mock)
task bindings         # regenerate TS bindings from Go services (committed; never hand-edit frontend/src/bindings)
task bindings:check   # CI: fail if committed bindings differ from the Go services
task tokens           # design/tokens.json → frontend/src/styles/tokens.css + tokens.ts
task test             # CGO_ENABLED=0 go test -tags purego ./... + vitest   (test:go / test:fe separately)
task lint             # go vet, layercheck, staticcheck (if installed), tsc --noEmit, eslint
task bench            # Go benchmarks (compare with benchstat against bench/baseline.txt)
task size             # vite build + size-limit
task e2e              # Playwright perf/a11y/screenshots against vite preview --mode mock
task e2e:webkit       # the no-sideways-scroll rule in WebKit (the app's real engine)
task build            # production build for this OS (binary in bin/)
task package          # bundle/installer for this OS into bin/ (macOS: local, ad-hoc signed)
go run ./scripts/layercheck                   # architecture §2 dependency table
git diff --name-only main...wp/NN | scripts/agent/check-ownership WP-NN   # WP allowlist gate
go run ./cmd/fixturegen                       # regenerate the fake-Jira fixture
go test ./internal/view -update               # regenerate the golden StandupView (review the diff!)
./scripts/publish-to-github.sh --repo crueber/cuisle --version vX.Y.Z   # PLAN ONLY; never --apply from an agent
```

## 5. How to…

| Task | Steps |
|---|---|
| **Add a bound method** | Add it to the service in `internal/app` (thin: validate, then call the domain package, map errors, emit events). Document it in architecture §3.2. Run `task bindings`. Expose it through `frontend/src/api/index.ts` only. Add it to `api/mock.ts`. |
| **Add a migration** | Create `internal/store/migrations/NNNN_name.sql`: forward-only, one transaction, never drops record data. Add a migration test (up from the previous schema with data; record row counts preserved). Update data-model.md. |
| **Add a setting** | Standup setting: a column in `standup_settings` plus a migration, settings CRUD, `StandupPatch` field, the ux-spec §9.4 control, and the config-history fingerprint. App setting: an `app_setting` key in architecture §6.4. |
| **Add a source adapter** | New `internal/source/<kind>` implementing `source.Source` with honest `Capabilities`. Register it in `cmd/cuisle`. Write a fake server in `<kind>test`. Nothing in `sync`, `cache` or `view` changes. If it does, the contract is wrong: write an ADR. |
| **Add a sink adapter** | New `internal/sink/<kind>` implementing `sink.Sink` (`ConfigSchema`, `Validate`, `Deliver`, idempotent by key). Register it in `cmd/cuisle`. Secrets go in the keychain `sink:<id>`. The UI renders from `ConfigSchema`, so no new screen is needed. Payload stays `cuisle.export.v1`. |
| **Change a layout rule** | Update ux-spec first. Change the pure function in `frontend/src/layout/`, then the golden cases in testing §4 and the Vitest suite. |
| **Regenerate goldens** | The `-update` flag for Go, then `npm run test -- -u` for the frontend screenshots. Always review and commit diffs deliberately, with the reason. |

## 6. Verifying work (what "done" means here)
- The WP or milestone DoD in roadmap.md and agent-plan.md is met.
- `task lint test` is green, budgets hold (`task size`, `task bench`), and the layering check passes.
- UI: compare a mock-mode Playwright screenshot with the mockup at the same size, minding ux-spec §13. Judge by looking
  at screenshots, not by assumption.
- Docs updated in the same change when reality differed (and an ADR if a decision changed).

## 7. Security checklist for any change
- **Secrets:** tokens and sink secrets never leave the keychain. No secret in logs (redaction test), DB bytes (scan test),
  exports, crash output or the frontend.
- **Stored content:**
  - Descriptions, comment bodies and attachment images are stored locally (ADR 0015). They never go to logs at any
    level, crash output, exports or diagnostics.
  - Issue summaries aren't logged at `info`.
  - The media cache lives under the app data dir and is served only through `/media/{id}`.
- **The webview:** it loads only embedded assets. No remote scripts or fonts, and no `eval`.
- **File sink:** writes only into the user-chosen folder (clean the path, no traversal), atomically.
- **Error output:** user-facing messages carry no tokens and no raw HTTP bodies; raw detail goes to the log at `debug`.

## 8. CI, release, Forgejo and GitHub (same model as Weave)
- **Forgejo (origin):** `.forgejo/workflows/ci.yml` runs Linux test, build and package, plus a Forgejo release with Linux
  assets on `v*` tags.
  - **No JavaScript actions:** checkout uses plain git, and releases go through the Forgejo API with curl.
  - **Job image:** `github.com/crueber/cuisle-ci`, built by `ci-image.yml` from `build/ci/linux.Dockerfile` with
    daemonless buildah. Every buildah call needs `--storage-driver vfs`, and builds need `--isolation=chroot` (lessons
    from Weave).
  - **Pinning:** images are pinned by digest via `scripts/update-pins.sh`.
- **GitHub (published mirror):** Chris runs Forgejo → Actions → "publish to github" (`publish-github.yml`, secret
  `PUBLISH_GITHUB_TOKEN`) and picks a patch, minor or major bump.
  - **What the script does:** `scripts/publish-to-github.sh` exports the tree, rewrites `github.com` references,
    removes `.forgejo/` and installs `.github/workflows/` from `scripts/publish/*.yml`. It pushes one squash commit, then
    the tag separately.
  - **GitHub workflows:** `ci.yml` runs Linux, macOS and Windows. `release.yml` builds, self-signs and publishes the
    GitHub Release.
  - **Agents never run `--apply` or push to GitHub.**
- **macOS day-to-day builds are local** (`task package`, ad-hoc signed).
- **Signing is self-signed** (ADR 0013).
  - macOS: Chris's stable "Cuisle Self-Signed" cert (`MACOS_SIGN_P12` / `_PASSWORD`), or ad-hoc as a fallback.
  - Windows: self-signed Authenticode (`WINDOWS_SIGN_PFX` / `_PASSWORD`).
  - Linux: unsigned.
  - `SHA256SUMS.txt` everywhere, plus an optional ed25519 `.sig` (`RELEASE_SIGNING_KEY`, port of Weave's relsign).
  - Release notes carry the Gatekeeper, SmartScreen and Linux-keyring first-run steps.

## 9. Working in parallel
Follow [docs/agent-plan.md](docs/agent-plan.md):
- **Freeze the contracts first.** The orchestrator owns the Wails spike, the contracts, the merges and all shared files.
- **Write failing tests as the spec:** Sonnet test-authors write them before anyone implements.
- **Small work packages with explicit file ownership:** Haiku implementers, each in its own `git worktree`.
- **Gate every merge** (ownership check, build, vet, layercheck, tests, reviewer). Merge dependencies first and run the
  full suite after each merge.
- **Expect conflicts** only in `go.mod`/`go.sum`, `package.json`/lock, generated bindings and tokens, and docs. Regenerate
  those; don't hand-merge them.
- **Escalate** a WP to Sonnet after two failed Haiku attempts.

## 10. Open items and decisions log
- **Verify Wails v3 names in the M0 spike** (architecture R1): `MarshalError`, asset middleware, context injection and
  the bindings flags.
- **Dev-status is an internal Jira API** (R2): degrade gracefully and warn.
- **Post-v1:** History view UI, more sinks (webhook, CSV/Markdown, Linear, GitHub Projects, Asana, Notion, Azure
  Boards), more sources, archive/restore UI, speaker timer, working-days option, OAuth, auto-update.
- **Decided 2026-11-23 (later):** ticket modal (F12) with full content synced up front and light writes (ADR 0015);
  layout A "Docket" (ux-spec §14).
- **Decided 2026-11-23:**
  - The JS budget rises from 50 to 70 KB gz (all JS) for the ticket modal.
  - Disconnect keeps synced content. It offers deletion only when local content is past the storage cap
    (`storage.cleanup_offer_mb`, default 2048 MB; ux-spec §14.11).
- **Decided 2026-11-23:**
  - Module `github.com/crueber/cuisle`, bundle `crueber.cuisle`.
  - GitHub CI for 3 OSes via the Weave publish model, with Forgejo for Linux builds.
  - Self-signed builds.
  - History kept forever (archive, no purge).
  - Provider-neutral core with source and sink adapters.

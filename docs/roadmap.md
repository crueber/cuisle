# Cuisle: roadmap

Milestones are ordered so the app is runnable end to end against the **fake Jira** as early as possible. The real Jira
site only matters from M3 onward. Each milestone ends with its Definition of Done (DoD) met and Forgejo CI (Linux)
green. The macOS and Windows matrix runs on GitHub after a publish. Docs are the source of truth: change them in the
same commit when implementation proves them wrong.

**How the work is executed:** [agent-plan.md](agent-plan.md) maps these milestones onto parallel waves of Haiku
subagents. The milestones below are the *what*; the plan is the *how, fast*.

Feature IDs (F1…F11) refer to [product-spec.md](product-spec.md). Section refs: **A** = [architecture.md](architecture.md),
**D** = [data-model.md](data-model.md), **J** = [jira-sync.md](jira-sync.md), **U** = [ux-spec.md](ux-spec.md),
**T** = [testing.md](testing.md).

---

## M0: Foundations and Wails v3 spike
**Goal:** an empty Cuisle window builds and runs on Linux and macOS (locally), and Forgejo CI proves Linux.

1. `go mod init github.com/crueber/cuisle` (decided). Go ≥ 1.24.
2. Install and **pin** Wails v3 (`wails3` CLI and `github.com/wailsapp/wails/v3` at the same exact version) and the
   matching `@wailsio/runtime`. Record the versions in ADR 0001.
3. Scaffold with `wails3 init`, then reshape to A §2 and A §4.2:
   - `cmd/cuisle`.
   - `internal/…` stubs, including `model`, `source`, `source/jira`, `sink`, `sink/file`, `export` and `history`.
   - `frontend/` with Preact + @preact/signals + Vite + TS strict.
4. **Spike R1 (A §7):** verify service registration, context injection, error marshalling, asset middleware for
   `/boot.js`, events, and binding-generation flags. Fix A §3 and §4.4 to match reality.
5. `Taskfile.yml`: `dev`, `dev:real`, `bindings`, `tokens`, `build`, `package`, `test`, `bench`, `size`, `lint`.
6. `frontend/scripts/tokens.mjs`: `design/tokens.json` → `tokens.css` + `tokens.ts`. Fail on a light/dark key mismatch;
   ignore `$` keys.
7. Bundle IBM Plex Sans Condensed and Mono (500/600/700) as WOFF2 Latin subsets, ≤ 160 KB total.
8. `scripts/layercheck` (the A §2 dependency table as a Go program; fails CI on violation).
9. **CI (Weave model, A §6.5).** These files are already drafted in the repo; make them real and check the "M0:" markers:
   - `build/ci/linux.Dockerfile` and `.forgejo/workflows/ci-image.yml`.
   - `.forgejo/workflows/ci.yml`.
   - `.forgejo/workflows/publish-github.yml` + `scripts/publish-to-github.sh` (ported from Weave) +
     `scripts/publish/{ci,release}.yml`.
   - `scripts/update-pins.sh` (port from Weave) to pin images by digest.
10. AGENTS.md command list kept current.

**DoD:**
- `task dev` opens a window showing "CUISLE" in the tokens' fonts and colours on Linux and macOS.
- Forgejo CI builds and smoke-runs Linux.
- `publish-to-github.sh` (plan mode, no `--apply`) produces a clean tree with `.github/workflows/` installed and no
  references to the private Forgejo host.
- Bindings for one dummy service are generated and type-checked.

## M1: Data layer
1. `internal/paths`, `internal/logx` (slog, rotation, redaction), `internal/apperr`, `internal/buildinfo`.
2. `internal/store`: pragmas, writer and reader pools, embedded migrations, backup-before-migrate, newer-schema refusal,
   `quick_check` handling, and the **weekly `VACUUM INTO` backup** (A §6.3).
3. `migrations/0001_init.sql` = D §2 verbatim. That's the neutral record store (`connection`, `space`, `board*`,
   `status`, `issue_type`, `sprint`, `person`, `issue`, `status_change`, `assignee_change`, `flag_change`, `comment`,
   `dev_commit`, `dev_pr_event`), plus the history tables (`standup_issue_span`, `standup_config_history`), the sinks
   (`sink`, `sink_outbox`), config and sync bookkeeping.
4. `internal/settings`: app settings (A §6.4) and standup CRUD (create with defaults, duplicate, soft delete = archive,
   restore, move, patch), walk order, limits, column map + `RebuildStatusLanes`, speakers (include, move, sort, add,
   remove, daily seed), and the `CopySettingsFrom` / `DiscardDraft` rules. Every mutation bumps `standup.rev`.
5. `internal/secrets`: `Keyring` (service `crueber.cuisle`, accounts `<source>:<host>:<account>` and `sink:<id>`),
   go-keyring and in-memory implementations.
6. `internal/model` (neutral types, A §2.1).

**DoD:**
- `CGO_ENABLED=0 go test -tags purego ./internal/...` passes.
- Migration tests pass, including "record tables survive a migration".
- Settings rules have table tests (T §2).

## M2: Source adapter (Jira), fake Jira, Connect (F1)
1. `internal/source`: `Source` interface, `Capabilities`, registry (A §2).
2. `internal/source/jira`:
   - REST client: Basic auth, J §3 request policy, DTOs.
   - `Plan`/`Scan`/`Hydrate`/`DevActivity` mapped to `model`.
   - `ValidateFilter` (JQL).
3. `internal/source/jira/jiratest`: fake server, JQL subset, fault injection (J §6). `cmd/fixturegen` from
   `design/reference/sample-data.json`.
4. `ConnectionService`: `Connection`, `Connect(ConnectInput)`, `Disconnect`, `SearchSpaces`, `SearchBoards(spaceKey)`,
   `SearchUsers`, `ValidateFilter`.
5. Frontend: `api/` facade + `api/mock.ts`, `ConnectScreen` (U §9.7), and the **Combobox** (A §4.6) with its full
   keyboard contract.
6. `cuisle --fake-jira` dev mode.

**DoD:**
- Connect works against the fake Jira and, manually, a real site.
- A test asserts the token is absent from the DB file bytes and the logs.
- The Combobox passes its unit and a11y tests.
- The layering check proves `sync` never imports `source/jira`.

## M3: Sync engine with history recording (F8, F11)
1. `internal/cache`:
   - Read and write the neutral store.
   - `LoadCurrent(standupID) → model.BoardSnapshot`.
   - Apply that **never deletes** record rows, tombstones issues that left (`removed_at`), and maintains
     `standup_issue_span` and `standup_config_history` (on a fingerprint change only) and `assignee_change`.
2. `internal/sync` over `source.Source`:
   - Plan, then scan and diff, then hydrate, then an atomic apply in one transaction (J §4, ADR 0009).
   - Progress events, cancel, partial warnings, interrupted-run recovery.
3. `SyncService` (`Start`, `Cancel`, `Status`). Events `sync:*`, `data:changed`, `connection:auth`.
4. Sync-on-open policy (offline at launch is a quiet header state, not a banner).
5. Speaker auto-discovery and pruning on apply.
6. After a successful sync, enqueue `after_sync` sink deliveries. The dispatcher arrives in M4, so this step only
   enqueues for now.

**DoD:**
- Every integration test in J §6 passes, plus the history tests in T §3.
- The no-network-without-trigger test passes.
- One board syncs manually against a real site.

## M4: View models, pulse, export, sinks backend (F3–F5, F10, F11)
1. `internal/pulse` (pure, `asOf`-parameterised):
   - Segments, day buckets (OS timezone), days in lane, over-limit, blocked spans.
   - `Pulse.Label` (the aria text).
   - Speaker order.
2. `internal/view`:
   - `Build(snapshot, asOf)` → `StandupView`, plus `StandupConfig` and `Bootstrap`.
   - `StandupService.View` and `ConfigService.Get`.
   - The `/boot.js` middleware and the `day:rolled` timer.
3. `internal/history`: a `LoadAsOf` stub. `HistoryService` is bound and returns `not_implemented`.
4. `internal/export`: the `cuisle.export.v1` builder (A §3.5) and a golden JSON.
5. `internal/sink`: registry and the outbox `Dispatcher` (idempotency keys, ≤ 8 attempts, retried only on a trigger).
   `internal/sink/file` writes atomically. `SinkService` (`Kinds`, `List`, `Save`, `Delete`, `Send`, `Outbox`, `Retry`).
   Events `sink:delivered` / `sink:failed`.
6. Golden `testdata/view_mob.golden.json`. It must equal the frontend mock fixture (T §3).

**DoD:**
- Benchmarks meet A §6.1, including `BenchmarkStandupView` on a fixture with 3 simulated years of history.
- Pulse table tests pass, including a past `asOf` ignoring later events.
- The sink outbox tests pass.

## M5: Standup screen (F2, F3, F4, F5, F9 keyboard)
1. Header:
   - Wordmark.
   - Tabs with `tabMode` (full, key, menu, "+N more"). Drafts aren't tabs.
   - Sync status with Resync, cancel and the offline state.
   - Date and Configure.
2. Rail: full and compact.
3. Person view:
   - Title row and legend modes, pinned axis.
   - Stacked and side-gutter groups.
   - The five-step `chooseDensity` ladder (U §5.5).
   - The more bar with `belowFold`. Done is last.
4. Whole board: `balanceLanes` (speaking order), Unassigned lane, Done list, Backlog list, more bar.
5. `PulseTrack` plus a per-list `TrackBackdrop` (U §7).
6. Keyboard map (U §8), `OpenIssue`, banners.
7. `layout/*.ts` Vitest suites with the T §4 golden cases.

**DoD:**
- Visual parity with the `person-*` and `board-*` mockups at all four sizes, after the U §13 differences.
- Switch budgets pass.

## M6: Configure (F6, F7, F10 UI, Display)
1. Frame: standups rail, title row (Duplicate, Delete → archive + Undo), save indicator, **Done**.
2. Sections per U §9.4:
   - Source, Status mapping.
   - Person view (walk order + presets, Rows).
   - Speakers.
   - Pulse (with a live preview).
   - Whole board.
   - **Export** (sinks: add a File (JSON) destination, Send now, After each sync, status, Retry).
   - Display.
   - Connection.
3. Autosave pipeline (A §5.3), retry, "Not saved · Retry".
4. Source changes set "needs sync", and leaving Configure triggers a full sync.
5. New-standup draft flow: drafts excluded from tabs; Start from = `CopySettingsFrom`; an empty draft is discarded on
   leave.
6. Responsive layouts (3, 2, 1 columns) plus the jump bar.

**DoD:**
- Every control autosaves, survives a restart and shows the indicator.
- Parity with the `configure-*` mockups.
- The Configure save budget passes.

## M7: Polish, themes, accessibility, performance
1. Dark theme values (U §10 contrast); System theme following.
2. Large (TV) text size.
3. Axe in light and dark; a manual VoiceOver/NVDA pass.
4. All A §6.1 budgets enforced in CI; `--bench-startup` on every OS (GitHub matrix after a publish).
5. Copy review (U §12); "Copy diagnostics".

**DoD:**
- Zero axe violations and every budget green.
- The T §6 manual script passes on Linux and macOS locally, and on Windows from the GitHub build.

## M8: Packaging and first release (self-signed)
1. Linux AppImage + `.deb` published as a Forgejo release by `ci.yml` on tags.
2. GitHub publish via the Forgejo "publish to github" workflow (Chris triggers it) → `release.yml`:
   - macOS universal `.dmg`, self-signed with Chris's stable "Cuisle Self-Signed" cert, or ad-hoc as a fallback.
   - Windows NSIS installer, self-signed Authenticode.
   - Linux packages.
   - `SHA256SUMS.txt`, plus an optional ed25519 `.sig` (port Weave's `relsign`).
3. README install section with the Gatekeeper, SmartScreen and Linux-keyring first-run steps (ADR 0013).
4. Release checklist: RSS check, full-sync timing on a real site, upgrade-migration test from the previous release's DB,
   and a history-preservation check across upgrade.

**DoD:**
- A fresh install from each release asset connects and runs a standup.
- History recorded before the upgrade is still present after it.

---

## M9: Ticket modal (F12, ADR 0015)
Backend and UI. M8 (release) is independent. Design hand-off: `docs/features/ticket-modal/` and
`design/mockups/ticket-*.png`.

0. **Limiter** (J §3.1): every Jira request goes through one limiter per connection, and the request budgets are a CI
   gate: issue and content traffic ≤ 250 requests for a full fixture sync and ≤ 50 for an incremental with 3 changes,
   plus a separate dev-status cap (jira-sync §3.1).
1. **Adapter** (J §2.2, §2.8–2.10, §2.14, §2.15):
   - Field registry and kind mapping; `*all` hydrate; comment bodies; `FetchMedia`.
   - Writes: `Transitions`, `Transition`, `Assign`, `SetFlag`, `AddComment`, and `GetIssue`.
   - jiratest content, media, writes and faults. The fixture `MOB-1421` covers every ADF node and field kind.
2. **Sync:**
   - `content` and `media` phases.
   - One-issue apply for write re-fetches, which records history like a sync.
   - Never overwrite newer data.
   - Migration `0003_ticket_content` (D §2.5a, §3).
3. **Backend:**
   - `internal/adf` and `internal/media` (cache, 320×200 thumbnails, cap, eviction).
   - `view.BuildTicket` → `TicketDetail`; `TicketService`; field prefs; media cache settings.
   - The `/media/{id}[/thumb]` routes.
   - Events `ticket:updated`, `ticket:write_failed`, `media:ready`, `sync:throttled`.
4. **UI** (ux-spec §14):
   - Modal geometry at 4 sizes; header with pulse strip and stepper.
   - Anchor strip; ADF components; attachments, subtasks and links.
   - Comments with composer, draft, sending/failed states and the standup marker.
   - Field rail with Not-set folding, the Status/Assignee comboboxes and the Flag switch; lightbox.
   - The "Updated in Jira · Reload" bar; row hover/focus; live update of the row behind the modal.
   - Refresh this ticket (↻, `R`, 10 s cooldown) and the rate-limit label (U §14.12–14.13).
   - Configure → Ticket fields; Display → Storage (size, Clear image cache, cleanup-offer cap); Disconnect's over-cap
     delete offer (U §14.11).

**DoD:**
- Parity with `ticket-*.png` at all four sizes.
- `TicketService.Get` makes no network request; modal open p95 < 100 ms on the fixture.
- **ADF and field coverage:** an ADF golden covers every node in U §14.4; a field-kind matrix covers every kind in
  U §14.6.
- **Writes:** success / failure / revert / no auto-retry / history-recorded tests pass against jiratest.
- **Drafts:** a draft survives a restart.
- **Privacy:** exports and logs contain no comment bodies (scan tests).
- **Throttling:** the request-budget gate passes; the transport-bypass test proves every request goes through the limiter.
- **Quality gates:** axe is clean (modal, combobox, lightbox), and every budget is green (JS entry ≤ 80 KB gz, lazy ≤ 20 KB since 2026-11-25).

---

## Later (v1.x candidates)
- **History view UI** (the store, the `asOf` builder and `HistoryService` are ready) and historical range exports
- **More sinks:** webhook, CSV, Markdown notes, Linear, GitHub Projects, Asana, Notion, Azure Boards
- **More sources** (the same `source.Source` interface)
- Archive/restore list for deleted standups
- Speaker timer
- Working-days option
- OAuth 2.0 (3LO)
- ~~Auto-update~~ (done 2026-11-24, ADR 0016)
- Drag-and-drop reordering
- Multiple connections

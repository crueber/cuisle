# Cuisle: testing plan

Tests are part of each milestone's Definition of Done (see [roadmap.md](roadmap.md)). The fake Jira server
(`internal/source/jira/jiratest`, [jira-sync.md](jira-sync.md) §6) and the frontend mock facade (`frontend/src/api/mock.ts`)
mean almost nothing needs a real Jira site or a real window.

## 1. Pyramid

| Layer | Tooling | Runs | Scope |
|---|---|---|---|
| Go unit | `go test` (table-driven), `CGO_ENABLED=0 -tags purego` | every push (Forgejo: Linux; GitHub: all OSes) | `pulse`, `settings`, `store` migrations, `source/jira` DTO decoding + mapping to `model`, `export`, `sink` outbox, `apperr`, `paths`, log redaction, `scripts/layercheck` |
| Go integration | `go test ./internal/sync/...` with `jiratest` + temp DB | every push | full and incremental sync, faults, atomic apply, **history recording**, golden view, sink after-sync delivery |
| Go benchmarks | `go test -bench` + `benchstat` vs. a committed baseline | every push, Ubuntu | `BenchmarkBootstrap`, `BenchmarkStandupView`, `BenchmarkConfigUpdate` |
| Frontend unit | Vitest | every push | `layout/*` pure functions, `state/save.ts` queue, Combobox logic, keyboard map |
| Frontend e2e / perf / a11y | Playwright against `vite preview --mode mock` | every push, Ubuntu | switching budgets, autosave UX, axe (light and dark), screenshot parity |
| Packaged smoke | packaged binary with `--bench-startup` on fixture DB "L" | every push, all OSes (Linux under `xvfb-run`) | it starts, paints, exits; start-up budget |
| Manual | the script in §6 | each milestone and release | real Jira site, keychain prompts, screen share legibility |

## 2. Go unit: must-have cases

### `internal/pulse`
Write these as table tests over hand-written changelogs. **Don't use the fixture only**, since its history is whole-day.
- A status before the window is clipped at day 0; a ticket created inside the window has no segment before creation.
- Multiple transitions in one day: the last status wins the day, and the segment list stays contiguous.
- Reopen: Done, then In Progress, then Done again.
- An unmapped status, or one mapped to `hidden`, draws in the To Do tone; the segment list stays contiguous (jira-sync §5.2).
- Flagged spans start before the window, stay open at today, or toggle several times in one day.
- Day buckets are in the OS timezone, including both DST transition days (America/Chicago and Europe/Dublin).
- Days in lane counts calendar days with a minimum of 1. Limit 0 means never over the limit. Blocked forces the chip red.
- Done window: `sprint`, `14d`, and `yesterday` on a Monday (looks back to Friday); a Kanban board with `sprint` is
  rejected.
- Speaker order: manual order, daily shuffle stable within a date and different on the next date, Unassigned
  first/last/skip, excluded speakers with tickets.

### `internal/settings`
- Create standup → defaults: walk order qa, review, prog, todo; limits 3/2/5/10; pulse 21; backlog 12; done window
  sprint; rows fit.
- Choosing a Kanban board forces `scope=board` and `done_window=14d`.
- `SetWalkOrder` accepts any permutation of the 4 lanes and rejects duplicates, missing lanes and `done`.
- Column remap rebuilds `standup_status_lane` in the same transaction.
- Duplicate copies settings, lanes, map and speakers. Soft delete (archive), restore, and **no purge** (spans and config history survive). Move keeps positions
  dense.
- Every mutation bumps `rev`. (Patches carry no expected rev: saves are serialised per field, last write wins.)

### `internal/store`
- Migrating a fresh DB reaches the latest schema. A backup file is written before a pending migration. A newer schema
  version refuses to open. A corrupt file is renamed and restored from backup.

### History and retention (F11, ADR 0011)
- Re-hydrating an issue never deletes its existing `status_change`, `assignee_change`, `flag_change`, `comment`,
  `dev_commit` or `dev_pr_event` rows. New rows append; duplicates are ignored by source id.
- An issue that leaves the sprint gets `removed_at` and its `standup_issue_span` closes (`valid_to` set). When it
  returns, a **new** span opens. Three syncs give two spans.
- `standup_config_history` gains a row only when the effective-config fingerprint changes. Saving the same value twice
  adds nothing.
- `assignee_change` decodes from the changelog, including to and from unassigned.
- `pulse` with a past `asOf` ignores every event after `asOf`, and days in lane are computed as of that date.
- **Reconstruction test:** after scripted syncs over 10 fake days (move, reassign, flag, leave sprint), rebuild day 4's
  membership and lanes from the stored records with a test-only `history` helper. It must match what the view showed on
  day 4.
- Migrations preserve every record table (row counts before and after).
- Archiving a standup (soft delete) keeps its spans and config history. Nothing is purged at startup.
- `BenchmarkStandupView` runs on a fixture carrying 3 simulated years of history and stays within budget.

### Sinks and export (F10, ADR 0012)
- The `cuisle.export.v1` golden JSON for the MOB fixture contains no comment bodies, emails or secrets (assert by
  scanning).
- Outbox: the unique idempotency key means an after-sync send fires exactly once per sync run. A failed delivery
  increments attempts and is retried only on Retry or the next successful sync. After the 8th failure the row is
  `dead`. No timers.
- The file sink writes to a temp file and renames, so a partial file is never visible. Its config is validated against
  `ConfigSchema`.
- The sink secret lives only in the keychain (`sink:<id>`).

### Ticket content and light writes (F12, ADR 0015)
**`internal/adf`** (golden, one case per row of ux-spec §14.4; inputs in `testdata/adf/*.json`, outputs `*.golden.json`):
- Every node and mark normalises to the documented `adf.Node` shape: headings 1–6, paragraph/hardBreak, all text marks,
  textColor (hex passed through), link, inline code, codeBlock with language, blockquote, rule, nested bullet/ordered
  lists, taskList (done state), decisionList, the five panel types, table (colwidth, colspan, rowspan, header row and
  column), expand/nestedExpand, status lozenge colour → lane lozenge, mention (self vs other), date (past flag relative
  to `asOf`), emoji (+ shortName fallback), inlineCard/blockCard (synced issue vs external URL), mediaSingle
  (width/layout/caption), mediaGroup/mediaInline (file card).
- Unknown node → `unknown` with its concatenated text and `Doc.Unsupported` incremented; unknown mark dropped;
  malformed/empty/non-JSON input never panics (fuzz test `FuzzParse`).
- Media resolution order (jira-sync §2.14): media id match, alt-filename match, positional match, unresolved →
  `Missing=true`. External media is never resolved to a fetch.
- `FromPlainText`: paragraphs, hard breaks, mention ranges (incl. adjacent and at string ends, multi-byte text) round-trip
  through `Parse`.

**Field kinds** (`source/jira` decoding + `view.BuildTicket`):
- Matrix: one fixture custom field per `FieldValue.Kind` (text, paragraph, number, date, datetime, url, option, options,
  cascade, user, users, group, groups, version, versions, component, components, labels, project, team, unknown) decodes
  from the Jira JSON shape in jira-sync §2.2 and yields the expected `RailRow`.
- **Empty-field folding:** `null`, `""`, `[]` and empty objects produce no row and appear in the group's `NotSet` in rail
  order; an empty Assignee still has a row; a group with no rows and no `NotSet` is omitted.
- Field prefs: pinned order respected, hidden fields absent from rows and from `NotSet`, default = non-empty custom
  fields A–Z; prefs are per connection.
- A field id missing from `field_def` is stored as `unknown` and schedules a registry refresh.

**`internal/media`:**
- Thumbnail generated for png/jpeg/gif/webp/bmp; oversize (> 25 MB) and undecodable files become file cards; a 100 MP
  image is rejected without allocating it.
- Cap and eviction: with a cap of N bytes, originals are evicted least-recently-viewed first until under the cap;
  thumbnails are never evicted; `Touch` changes the order; `Clear` removes originals and thumbnails and nulls the paths;
  orphan files are swept at startup.
- The `/media/{id}` routes serve only cached files, set `nosniff`, and return 404 on a miss **without any request**.

**Light writes** (integration, fake Jira; one set per action: transition, assign, flag, comment):
- **Success:** the write request is sent exactly once; `GetIssue` refresh follows; the `issue` row, `status_change` /
  `assignee_change` / `flag_change` (with Jira's history id and timestamp, not a locally invented row), comments and
  `standup.rev` are updated in one transaction; `ticket:updated {cause:"write"}` and `data:changed` are emitted.
- **Failure** (`400`, `403`, `409`, offline): typed error (`write_rejected` with Jira's message, `permission_denied`,
  `conflict`, `offline`), the DB is byte-identical, `ticket:write_failed` is emitted, and **the request log shows no
  second attempt** (no auto-retry, including for 429/5xx).
- `NeedsScreen` transitions are never posted (`transition_needs_screen`); a screen with only optional fields is.
- Refresh fails after a successful write → local delta applied, warning `refresh_failed`, no change-table row; the next
  sync merges the real changelog entry (no duplicate).
- A sync apply carrying an older `updated` for an issue does not overwrite a newer row written by a refresh.
- Writes are refused before any request when auth is not `ok` or the capability is absent.
- **Transitions are fetched only on open:** `TicketService.Get` makes zero requests; `Transitions` makes exactly one and
  caches; offline returns the cached list with `Offline=true`; the cache row is dropped when the status changes.
- **Drafts:** `SaveDraft` then restart (reopen the DB) → `Draft` returns text and mentions; empty text deletes the row;
  a successful `AddComment` deletes it; a failed one keeps it.
- Comments: an edited comment updates `body_adf` and `updated_at`; a comment missing from a complete fetch gets
  `deleted_at` and disappears from the modal but its pulse mark stays; restricted visibility is carried to the view.

### Security
- **Content never leaks:** after a full fixture sync and a session that opens tickets and performs each write, the log
  files contain none of the fixture's description/comment/draft/field-value marker strings (each fixture body embeds a
  unique marker), at `info` and at `debug`; `Diagnostics()` output and the `cuisle.export.v1` golden contain none either.
- After `Connect`, the DB file bytes and the log files **don't contain the token** (search the raw bytes).
- Redaction removes `Authorization` headers and JQL values at `info`.

## 3. Go integration (sync)
Every test listed in jira-sync.md §6 "Tests that must exist", plus:
- **No-trigger, no-network:** start the app services with sync-on-open `never` and a fixture DB. Assert the fake
  server's request log is empty, **and no sink delivery ran**, after bootstrap, view building, standup switching and
  Configure edits to view-only settings.
- **Layering:** `scripts/layercheck` proves `internal/sync` never imports `internal/source/jira`, and that only
  `cmd/cuisle` imports concrete adapters.
- **No network on ticket open:** after a sync, clear the fake server's request log, then call `TicketService.Get` for
  every fixture issue, `Draft`/`SaveDraft`, the `/media/{id}/thumb` route for every image, and step through a view.
  The log must stay empty. Then `Transitions`, each write, `EnsureMedia` on an evicted image and `OpenAttachment` on a
  PDF must each produce exactly the documented requests (jira-sync §2.14–2.15).
- **Content sync:** first sync stores descriptions, all comments with bodies, field values, links, subtasks and
  attachment rows; the media phase runs after `sync:finished`, fills thumbnails, captures `media_file_id` from the
  redirect and emits `media:ready`; an incremental sync with no changes fetches no comments and no media; a changed
  description rewrites content only for that issue (`content_hash`).
- **Golden ticket:** `TicketService.Get` for `MOB-1421` (every ADF node, every field kind) matches
  `testdata/ticket_mob1421.golden.json`; this JSON feeds the frontend mock and the screenshot tests.
- **Sync-on-open `stale`:** syncs only standups older than the threshold.
- **Golden view:** `StandupService.View` for the MOB fixture standup matches `testdata/view_mob.golden.json`.
  Regenerate with `go test ./internal/view -update` and review the diff in the PR. This JSON is what the frontend
  mock serves, which links Go output to the screenshots.

## 4. Frontend unit: golden layout cases
Port the rules, then assert these exact outcomes (from [ux-spec.md](ux-spec.md) §5.5 and the mockups).

| Function | Input | Expected |
|---|---|---|
| `chooseDensity` | 5 groups (QA 3, Review 3, Prog 5, To Do 3, Done 6), H = 900 | `compact-side`, no scroll |
| `chooseDensity` | same, H = 720 | `tight-side`, no scroll |
| `chooseDensity` | same, H = 558 | `tight-side`, scroll |
| `chooseDensity` | same, `rows_mode = roomy`, H = 900 | `roomy`, scroll |
| `belowFold` | `tight-side`, fold = 442 (H 478 − 36) | "2 To Do · 6 Done below" |
| `tabMode` | 5 standups, header 1920 wide | `full` |
| `tabMode` | 5 standups, 1440 | `key` |
| `tabMode` | 5 standups, 1024 | `key` (switch to `menu` only when the keys don't fit; test with 9 standups at 1024) |
| `balanceLanes` | MOB fixture, `mainW` 1656, lane row 23 | 2 columns; Unassigned first in column 1; speaking order preserved within each column |
| Track geometry | ticket with history `[todo 5, prog 6, review 3]`, N = 21 | segments start at day 7 with widths 5, 6, 3 days, the last ending at day 21 |

Also cover:
- The autosave queue: coalescing, 400 ms text debounce, the 250 ms "Saving…" delay, retry after failure, and
  ordering of saves to different fields.
- The Combobox: filtering order (prefix matches first, then substring), the full keyboard contract, async stale-response
  dropping, and restore on Escape.
- Global keys are ignored while focus is in inputs or comboboxes.

### Ticket modal (Vitest)
- **ADF render snapshots:** `AdfDoc` rendered from the golden node tree, one DOM snapshot per node type of ux-spec §14.4,
  plus the unknown-node fallback; assert no `innerHTML`/`dangerouslySetInnerHTML` anywhere in `components/adf` (lint rule
  + test) and that links never navigate the webview.
- `contrast.ts`: sampled textColor values reach ≥ 4.5:1 on both themes' backgrounds while keeping hue.
- **Stepping order:** `J`/`K` (and `→`/`←`) follow the person view's walk order then Done, and the board's lane order,
  Done, backlog; stop at both ends; never change the speaker; stepper shows `n / total`.
- **Esc unwinding:** combobox or lightbox → composer blur (draft kept) → modal; focus returns to the opening row.
- Inline-card back stack: open synced card → `Backspace` returns; non-synced card opens the browser.
- Optimistic write state machine: apply → success replaces, failure reverts both the modal and the row behind it and
  shows the reason; failed comment keeps text with Retry / Edit / Discard; nothing retries on its own.
- Comment order toggle persists (`ticket.comment_order`); the "new since standup" marker sits at the D3 boundary and
  flips with the order.

## 5. Playwright (mock mode)
- **Screenshot parity:** each mockup size (1920×1080, 1440×900, 1280×720, 1024×640) for person, board and configure.
  Compare to `design/mockups/*.png` with a perceptual threshold (start at 2% of pixels with `maxDiffPixelRatio`),
  **after** the documented differences in ux-spec §13 are applied. Treat the mockups as a guide, not pixel law. When a
  deliberate deviation happens, commit new baselines under `frontend/tests/__screenshots__/` and update the mockup or
  §13 in the same PR.
- **Budgets** (architecture §6.1): standup switch ≤ 50 ms, person switch ≤ 32 ms with zero facade calls, Combobox
  keystroke ≤ 16 ms (CI fails above 32 ms). Use 20 runs, 4× CPU throttle, and fail on p95.
  - **Where they gate (Chris, 2026-11-24):** locally with `task e2e`, before every merge. On GitHub's shared runners
    the perf specs run with `PERF_CPU_THROTTLE=2` and are **report-only** (`continue-on-error`), because the same
    commit passes and fails there run to run. Accessibility (`tests/a11y`) stays a hard gate in GitHub CI.
- **Autosave UX:** change each control type, reload mock state, and assert the value persisted and the indicator went
  through its states.
- **Nothing hidden:** at 1024×640, the person view's DOM contains every ticket in the fixture for that person, and the
  more bar text matches `belowFold`.
- **axe:** zero violations on the person view, board, Configure (all sections expanded), Connect, the ticket modal and
  the lightbox, in both themes.
- **Ticket screenshots:** `ticket-1920x1080`, `ticket-1440x900`, `ticket-1280x720`, `ticket-1024x640`,
  `ticket-edit-states-1920x1080` and `ticket-lightbox-1920x1080` against `design/mockups/ticket-*.png`, same threshold
  and rules as above (the `ticket-alt-*` mockups are rejected alternatives and are not tested).
- **Ticket budgets** (architecture §6.1): modal open ≤ 100 ms p95 with zero facade network calls, step ≤ 50 ms,
  optimistic write feedback ≤ 32 ms; if the ticket UI is code-split, its chunk is prefetched before the measurement.

## 6. Manual test script (each milestone / release, each OS)
1. Fresh install, then Connect to a real site with a classic API token. Check the keychain prompt and that the token is
   absent from the DB.
2. Create 3 standups (one Kanban). Switch with clicks and `1`–`3`. Restart the app and confirm it opens on the last
   standup instantly, from cache, with no network (use the OS network monitor or disconnect Wi-Fi).
3. Resync (`R`) and watch the progress; cancel mid-way and confirm the old data remains.
4. Walk speakers with ←/→, set a custom walk order and check that the person view follows it.
5. Zoom the window to 200% (or Large text at a small size) and confirm nothing is hidden and the more bar is accurate.
6. Share the screen over Zoom at 1080p and read the person view from the receiving side: keys, chips and BLOCKED must be
   legible.
7. Switch the theme between light, dark and system, and toggle the OS theme while the app is running.
8. Go offline and Resync: you get a banner, cached data stays, and nothing crashes. Revoke the token, Resync, then
   reconnect.

### Ticket refresh and throttling (ADR 0015, jira-sync §3.1)
- **Refresh:**
  - Refresh issues requests only for that issue (jiratest request log).
  - It historises a status change made "in Jira" between syncs.
  - It updates the row behind the modal and the other standups containing the issue.
  - A second Refresh within 10 s returns `cooldown` with zero requests.
  - Offline leaves the stored content untouched.
- **Limiter rate and priority:**
  - Under a 200-request synthetic sync, observed throughput never exceeds `max_rps` + burst (fake clock).
  - An interactive request queued behind 100 sync requests is sent within one token interval.
  - Media never starts while sync requests are waiting.
- **Limiter signals:**
  - `X-RateLimit-NearLimit: true` halves the rate, and it recovers +1/30 s.
  - The structured `RateLimit` header with `r/q < 0.2` does the same.
- **Limiter on 429:**
  - A 429 with `Retry-After: 3` pauses all lanes for ≥ 3 s, and the sync request is retried.
  - A write that gets 429 is **not** retried and surfaces `rate_limited{retryInMs}`.
- **Bypass check:** no code path reaches `http.Client` without the limiter (a test wraps the transport and fails on
  any unlimited request).
- **Request budgets** (jira-sync §3.1; jiratest request log; a CI gate):
  - Issue and content traffic (all but dev-status): a full fixture sync uses ≤ 250 requests, and an incremental sync
    with 3 changes ≤ 50.
  - Dev-status: ≤ 1 summary per active dev target per sync, and details only for targets whose fingerprint changed
    (≤ 2 each).

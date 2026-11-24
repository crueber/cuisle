# WP-T9: ticket modal wired end to end

Everything F12 built in isolation (limiter, ADF, media, content, writes, ticket backend, modal shell, content UI) is now
connected: the services have bodies, the app serves `/media`, `cmd/cuisle` wires the media cache and the throttle bridge,
and the modal is mounted and opened from every row. Sources: architecture §2.4, §3.2, §3.3, §4.9, ux-spec §14,
F12-freeze.md, WP-T0/T6/T7/T8 notes.

## Backend (`internal/app`, `cmd/cuisle`)

| Area | Where | Notes |
|---|---|---|
| `TicketService.Get` | `services_ticket.go`, `ticket_support.go` | One reader transaction: effective config, snapshot, `LoadTicketTx`, `FieldsInUseTx`; then `view.BuildTicketWith` with `SelfAccountID`, `Writes` (from the adapter's capabilities **with the stored source meta**, so Flag is reported), `AuthState`, `FieldsInUse`. Zero network (asserted). |
| `Transitions` | same | Interactive read, stored in `transition_cache`; offline gives the cached list with `Offline=true` (empty list when never cached). `ToLane` uses the standup's status mapping of the first live standup that has the issue (the signature has no standup id), else the status category. |
| Four writes | `write()` | Refused before any request when the connection is not ok or the capability is absent (a `ticket:write_failed` is still emitted). Per-issue in-flight lock (`conflict`), one send with `source.WithInteractive`, never retried. Success: `GetIssue` + `Orchestrator.ApplyIssue` on a detached 60 s context, then `ticket:updated {cause: write}` and one `data:changed {what: view}` per standup. Refresh failure: `ApplyIssueDelta` with the known effect (status from the cached transition, assignee, flag) + warning `refresh_failed`. `AddComment` deletes the draft on success only. |
| `Refresh` | same | 10 s per-issue cooldown (`cooldown` + `retryInMs`, zero requests), per-issue lock, `GetIssue` + `ApplyIssue`, emits `ticket:updated {cause: write}` (the modal acts only on `sync`) and `data:changed`. New image attachments get thumbnails in a background goroutine (media lane), tracked by `Deps.bg`. |
| Drafts | `ticket_support.go` | `Draft`, `SaveDraft` (empty text deletes; mentions validated against the text). Never logged. |
| `EnsureMedia`, `OpenAttachment` | `ticket_media.go` | Cache miss: `FetchMedia` then `media.Cache.Put`, learns the media id, emits `media:ready`. `OpenAttachment` writes `<temp>/cuisle-attachment-*/<safe name>` (0600, 1 GiB cap, stale folders swept after 24 h) and calls `Deps.OpenFile`. **Risky types** (`.exe`, `.bat`, `.sh`, `.app`, `.lnk`, ... ) are refused with a validation error: opening them would run them. |
| `SearchMentions` | `services_ticket.go` | Board people, cached people, then (non-empty query only) `SearchUsers` on the interactive lane. De-duplicated, 20 max. |
| Field prefs | `services_ticket.go` | `TicketFields`, `Pin`, `Unpin`, `Move`, `SetHidden` over `ticket_field_pref`. Pinned = rows with a position (hidden ones keep it and carry `hidden`), Hidden = every hidden field (`position -1` when only hidden), Available = custom, unpinned, A-Z. Only custom fields can be pinned; any registry field can be hidden. |
| `Disconnect(deleteContent)` | `disconnect.go` | Deletes `issue_content`, `issue_field_value`, `issue_link`, `transition_cache`, `ticket_draft`, comment **bodies** (rows stay), clears the media cache, flags every standup `needs_sync`, then `VACUUM`. Record and history rows are never touched. Storage sizes for the offer come from `Diagnostics.dbBytes` + `MediaCache`, as T8 reads them. |
| `AppService.MediaCache/ClearMediaCache` | `ticket_media.go` | `Stats` plus the cap from `media.cache_max_mb` (default 1024). |
| `AppService.OpenURL` | `services.go` | New bound method: http, https and mailto only. `cmd/cuisle`'s opener now accepts mailto too. Facade `api.app.openUrl` + mock; T7's `openLink` uses it (its direct `@wailsio/runtime` call is gone). |
| `/media/{id}`, `/media/{id}/thumb` | `ticket_media.go` (`MediaMiddleware`, `AssetMiddleware`) | Cache only: stored mime (non-image mime is served as `octet-stream`), `nosniff`, `private, immutable`, a CSP of `default-src 'none'; sandbox`, `404` on a miss, never a fetch. Ids must be canonical positive integers. `cmd/cuisle` installs `AssetMiddleware` (boot.js + media). |
| Offline verdict | `ticket_support.go` | `Connection.State` has no offline value, so `Get` sets `Can.Reason = "offline"` (writes stay offered by capability; the modal disables them) after an `offline` error from any source call. It clears on **any** later successful source call (ticket calls, autocomplete, search) and when a sync finished OK after the verdict. |
| `cmd/cuisle` | `main.go`, `throttle.go` | `media.New(<data>/media, ...)`, `Deps.Media`, `Deps.OpenFile` (Wails `Browser.OpenFile`), `sync.Options.Media` (`app.NewMediaStore`), `Evict` once after the window's runtime is ready (off the cold-start path; it also runs the orphan sweep), `throttleBridge` (forwards events, feeds `jira.LimiterFor(conn).SetOnThrottle` into `sync:throttled`, re-attaches on `connection:auth`), hidden `jira.max_rps` applied at startup through `SetDefaultPolicy`, `Services.Shutdown` in cleanup. |

### App settings (frozen-file exception, needed)
`internal/settings/app_settings.go` rejected every key F12 uses, so T7's comment-order toggle, T8's Storage section and the
media cap would all have failed with `validation` in the real app. Added (validated, defaults applied by the readers because
`AppDefaults` is frozen): `ticket.comment_order` (newest|oldest), `media.cache_max_mb` (64 to 1048576), `storage.cleanup_offer_mb`
(1024 to 65536), `jira.max_rps` (1 to 20, hidden).

## Frontend (`frontend/src`, `frontend/tests`)

- `state/ticketSteps.ts` (+ tests): the step list. Person view: the person's tickets in walk order, then Done. Board: lanes
  (Unassigned first), Done, then backlog items that have an `issueId`. `openTicketFromRow(key)` opens the modal; a backlog row
  with no local issue falls back to opening Jira.
- `App.tsx` mounts `TicketModal` (leaving the standup route closes it). Rows of the person view, board, Done list and backlog
  open it. `keys.ts` ignores every global key while the modal is open (`R` never resyncs behind it).
- `state/sync.ts` ignores `sync:progress` unless it belongs to that standup's current running run (late media ticks).
- Configure: Storage (after Display) and Ticket fields sections, 11 JumpBar pills. In the 3-column layout Ticket fields ends
  column 3 to balance heights.
- Mock: `fixtures/ticket_mob1421.golden.json` is the Go golden with ids normalised to the mock's views (local issue ids +100,
  `row.sourceId` = `10099` + the golden id, `u-iris` becomes `iris`; times untouched). Regenerate with
  `node tests/fixtures/ticketFixture.mjs` from `frontend/`; `src/api/mock.golden.test.ts` fails if it drifts from the Go golden.
  **Deviation:** `ticket_mob1421.json` stays as the hand-built mockup data because the T2/T7/T8 component tests assert its exact
  content; `MockOptions.handBuiltTickets` keeps those suites on it and the app/mock mode serves the golden. `ticket_mob1429.json`
  is unchanged. Mock hooks for Playwright: `window.__CUISLE_MOCK_OPTIONS__` (failures, `holdMs`) and `window.__CUISLE_MOCK_CALLS__`.

## Tests

- Go (`internal/app/ticket_test.go`, harness options `withMedia`, `withNow`, `withFakeOptions`, a write-failure hook in the
  switch transport): golden equality (`TicketService.Get(MOB-1421)` equals `testdata/ticket_mob1421.golden.json` except the
  placeholder `self`), no-trigger test for the modal on `newQuietApp` (open, step, drafts, field prefs, `/media`: empty request
  log, no sink, no run), media routes (200 cached / 404 miss / no request), `EnsureMedia`, `OpenAttachment` (+ the refusal of
  `.exe`), `Transitions` (one request, cache, offline), each write's success (one request, rev, events, history rows), failure
  matrix 400/403/409/429/500/503 (typed code, exactly one attempt, nothing stored, event, draft kept; a 429 closes the gate so
  later writes are refused with no request), overlapping write and Refresh give `conflict`, refresh-failure delta + warning,
  Refresh (only that issue, cooldown, offline untouched), drafts across a restart, field prefs, `Disconnect(deleteContent)`,
  media info/clear, settings validation, `OpenURL`, `SearchMentions`, offline verdict clearing, content never in the log.
  `BenchmarkTicketDetail/L`: 7.3 ms/op (budget 25). `cmd/cuisle/throttle_test.go`: the bridge and `jira.max_rps`.
- Vitest 96 files / 1890 tests, Playwright 154 tests (6 ticket screenshots, 6 axe scans, 3 perf specs), Go suite and
  `-race` on `internal/app` and `cmd/cuisle` green.

## Budgets

| | Result | Budget |
|---|---|---|
| JS (gz) | 63.91 kB | 70 |
| CSS (gz) | 15.68 kB | 18 |
| Fonts | 118.83 kB | 160 |
| Modal open p95 (4x CPU, golden MOB-1421, zero facade network calls) | 27.7 to 35 ms | 100 |
| Step J/K p95 | about 14 ms | 50 |
| Optimistic write p95 | about 17 ms | 32 |
| `BenchmarkTicketDetail/L` | 7.3 ms/op | 25 |

## Screenshot comparison (ticket-*.png at 1920x1080, 1440x900, 1280x720, 1024x640, edit states, lightbox)

Layout, typography, colour and geometry match the mockups at all four sizes; data differs (Saffron golden vs the hand-built
mockup). Remaining differences, all from T7/T8 CSS or markup (not fixed here): the Assignee combobox has no initials box; the
Flag row wraps "Not flagged" under the switch; the Parent summary is mono; the Open in Jira icon differs from the mockup's
arrow; the anchor strip background is white where the mockup is cream; the lightbox scrim is a little lighter than .92; the
lightbox stub image is stretched because the test stub PNG is 1200x720 against an attachment that says 1200x440.

## Smoke test (server build, fake Jira)

`go build -tags server`, `CUISLE_FAKE_JIRA=default`, a fresh data dir, driven with Playwright: the first sync (`R`), a board row
opens MOB-1421 (description, links, comments, attachments); the Status combobox lists the transitions (network on open, the
needs-screen one marked); picking one wrote through the fake and the status, pulse and header lane followed (To Do to In
Progress); a comment raised COMMENTS 5 to 6 (the fake stores its own text for new comments); Refresh showed `refreshed 19:52 ·
just this ticket` and then the disabled `Refresh 10s` cooldown; `/media/{id}` and `/thumb` answered 200 with the stored mime;
no console errors.

## Deviations, exceptions, follow-ups

- **Ownership exceptions (all expected to be flagged):** `internal/settings/app_settings.go` (above); T7/T8 files touched by the
  frontend half: `Comments.tsx` (the "Not sent · " prefix was rendered twice: CONTRACT-ISSUE between `state/ticket.ts` and
  `Comments.tsx`, stripped in the component), `TicketContent.tsx/.css` (anchor strip flush and full width as in the mockup),
  `TicketHeader.tsx` and `Lightbox.tsx` (`<header>` became `<div>`: axe `landmark-no-duplicate-banner`), and the three
  `TicketModal*.test.tsx` files (`createMockApi({ handBuiltTickets: true })`); T7's `TicketModal.util.ts` (one call site moved to
  `api.app.openUrl`, authorised).
- **Docs to update (not mine):** architecture §3.2 lacks `AppService.OpenURL`; §6.4 lacks `ticket.comment_order`, `media.cache_max_mb`,
  `storage.cleanup_offer_mb`, `jira.max_rps`; testing §3 names the golden `ticket_svc1429.golden.json`, the file is `ticket_mob1421.golden.json`.
- `EnsureMedia`/`OpenAttachment` fetches go through `FetchMedia`, which marks the media lane itself, so a lightbox click can
  queue behind a running media phase. Marking user-clicked downloads interactive needs a change in `source/jira/media.go` (T4's).
- `Services.Shutdown` waits for the post-Refresh thumbnails; `Services.WaitBackground` is for tests.
- Not done by choice: the screen behind the modal is not `inert` (making it inert breaks T7's focus return to the opener unless
  the opener is captured first); `TicketModal` reads the theme at render, so a theme change while it is open re-clamps textColor
  only on the next render.
- The old `jira-sync.md` and `contract.go` rate comments (T0's CONTRACT-ISSUE 3) are untouched.

## Review fixes (second pass)

1. **Attachment allowlist.** `OpenAttachment` now opens only `pdf txt md csv log json png jpg jpeg gif webp bmp heic mp4 mov mp3 wav
   docx xlsx pptx odt ods odp rtf zip`; everything else (no extension included) is refused with the existing "For safety..." validation
   message before any request or `OpenFile`. The name is sanitised first (separators, Windows-reserved characters, controls, bidi
   overrides such as U+202E removed, trailing dots and spaces trimmed), so the checked extension is the written one. Windows device
   names (CON, PRN, AUX, NUL, COM1-9, LPT1-9, judged on the part before the first dot, any extension) get a `_` prefix. Names over
   200 bytes fail with the validation message. Table test over `a.EXE.`, `a.tool`, `a.html`, `a.svg`, `a.webloc`, `a.jnlp`,
   `x.sh\x00`, `..\a.exe`, the RLO-spoofed `.exe`, `CON.txt`, a 300-character name and more.
2. **Comment survives a failed refresh.** The pending bubble becomes "sent" (full opacity, no buttons) with the note "Couldn't
   refresh · showing your comment locally" and is removed once any later detail carries the comment. `PendingComment.state`
   gains `"sent"` (props.ts, contract file: review it).
3. **Stale standup.** `write()` calls `loadLive` before anything is sent; a stale standup id makes zero requests (tested for all
   four writes and Refresh, with a nonexistent and an archived standup).
4. **Undo behind the modal.** The `ticketSession` guard in `keys.ts` is above the Ctrl/Cmd+Z branch; tested.
5. **No-trigger test** also runs Pin, Move, Hide, Unhide, Unpin, the comment-order setting and the full-size `/media/{id}` route
   (asserted to serve at least one cached original).
6. **settings** const block moved above `validAppSetting`'s doc comment.
7. **Double "Not sent · ".** `state/ticket.ts` now stores only the reason; `Comments.tsx` adds the prefix once; the regex is gone.
8. **Dates.** Date fields read "4 Dec 2026", datetime fields "4 Dec 2026 09:30" (viewer's zone), in `view/ticket_rail.go`. The
   Dates group (Created, Updated, Resolved) is unchanged (ISO). Go golden and the normalised mock golden regenerated.
9. **Unknown-kind fields.** A name-like member (`name`, `value`, `title`, `displayName`, `key`) of an object, the joined readings of
   an array (only when every member is readable), or a plain string, number or boolean becomes `text` with `mono` false; the raw
   JSON stays in mono only when nothing readable exists.
10-12. Flag on one line, header about 129 px (rule at y 179 at 1920, mockup 178; y 171 at 1440), initials box in the Assignee
   combobox, Parent key mono only, anchor strip on `--color-bg`. The lightbox scrim already used `rgba(22,24,29,.92)`.

Remaining visual differences vs the mockups: no "in MOB-1200 ..." parent link in the header (the golden ticket has no parent) and a
different Open in Jira icon; the rail is about 8 px narrower at 1440; footer keycaps sized slightly differently; Send sits below the
composer (mockup: right); the status combobox has no "Filter transitions" row; "Your text is kept." is on its own line; the ticket
modal stays faintly visible behind the lightbox; the lightbox stub image is stretched by the test PNG; Priority has no icon; data
differs (Sprint 42 vs 47, Due 29 Nov vs 4 Dec).

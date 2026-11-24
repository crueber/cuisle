# F12 (ticket modal): orchestrator decisions for the contract freeze and Wave F12

Source of truth: `docs/architecture.md` §2.1 (Source), §2.4 (adf, media), §3.2 (TicketService), §4 (view types),
`docs/ux-spec.md` §14, `docs/jira-sync.md` §2.14–2.15 and §3.1, `docs/data-model.md` §2.5a and §3, ADR 0015, and the
mockups `design/mockups/ticket-*.png`. **Transcribe the merged blocks; don't redesign them.** Where this file adds a
decision, it wins.

## Decisions (2026-11-23)
1. **Request budgets (Chris):**
   - Issue and content traffic (all but `/rest/dev-status/**`): ≤ 250 requests for a full fixture sync, ≤ 50 for an
     incremental with 3 changes.
   - Dev-status has its own cap: ≤ 1 summary per active target per sync, and details only for targets whose
     fingerprint changed (≤ 2 each).
   - Measured v1 baseline: 159 requests full (129 of them dev-status), 38 incremental (29 dev-status).
2. **Limiter home:** `internal/source/jira/limiter`, one instance per connection, held in a registry keyed by
   connection ID (or base URL for not-yet-saved connections) inside the jira package. This mirrors today's
   process-wide `DefaultPolicy`, so `source.Options` doesn't change. T0 owns the registry and wraps the existing
   `Client`.
3. **Migration:** the content tables ship as `internal/store/migrations/0003_ticket_content.sql` (frozen with the
   contracts), not in `0001_init`, because a release exists. The `comment` columns are added with `ALTER TABLE`, and
   `updated_at` is backfilled from `created_at`. Standups get `needs_sync=1`.
4. **Capabilities stay false until T4 and T5 merge.**
   - The jira adapter gets the new `Source` methods in two non-frozen files:
     - `internal/source/jira/content.go` (`Fields`, `FetchMedia`, `GetIssue`), owned by T4;
     - `internal/source/jira/writes.go` (`Transitions`, `Transition`, `Assign`, `SetFlag`, `AddComment`), owned by T5.
   - Both start as stubs returning `source.ErrUnsupported`.
   - Every test fake of `source.Source` gets the same stubs.
5. **`Disconnect(ctx, deleteContent bool)`:** the freeze changes the signature and updates every caller (Go tests,
   the TS facade, the mock and `Connection.tsx`) to pass `false`, so `main` keeps compiling and passing. T9 implements
   the delete.
6. **The connected user in fixtures is fake.** The composer's "posts to Jira as …" and Self mentions use the fake
   account's display name (`Dev User` for `jiratest`, `Mock User` for the mock), never a real person.
7. **Mock fixtures for the modal:** the freeze hand-builds `frontend/src/api/fixtures/ticket_mob1421.json` and
   `ticket_mob1429.json` as `TicketDetail`s matching the mockups (every ADF node of ux-spec §14.4 in MOB-1421; the edit
   states in MOB-1429). `api/mock.ts` serves them from `ticket.get` for those two keys, plus a minimal generated detail
   for any other ticket. T9 later replaces them with the Go golden (`ticket_mob1421.golden.json`).

## Freeze follow-ups (decided after the freeze commit 09a9f1d)
8. **`source/jira` may import `internal/adf`** (adf imports only `model`), so T5 builds the comment body with
   `adf.FromPlainText` as jira-sync §2.15 says. layercheck and the architecture §2 table are updated.
9. **`apperr.Error.RetryInMs`** (JSON `retryInMs`, omitted when 0; `WithRetryIn(ms)`), mirrored as
   `AppError.retryInMs` in `errors.ts`. It carries `cooldown` and `rate_limited` waits (§3.2, jira-sync §3.1).
10. **`view.BacklogItem.IssueID`** (`issueId`), the member's local id, so backlog rows open and step in the modal
    (ux-spec §14.1). The mock generator gives backlog items ids of `standup*10000 + n`.
11. The freeze agent's own decisions (stub files owned by the WPs, extra ConnectionService/AppService stubs, new error
    codes, the `Fix<>` optional members, 21-day ticket fixtures, `sync.MediaStore`, rail kinds) are accepted as they
    are; see the commit message of 09a9f1d and the code comments.

## Ownership gaps closed (the package README left them open)
- `frontend/src/state/ticket*.ts` (open/close, stepping, optimistic writes, drafts): **T7**.
- `frontend/src/keys.ts` (`R`, `J`/`K`, `C`, `S`, `A`, `F`, `O` and Esc inside the modal), and standup rows and board
  rows opening the modal instead of Jira: **T9**.
- **T6** also owns the existing sync phase wiring and the cache apply for comments and issues (`internal/sync/**`,
  `internal/cache/**`), because the content and media phases and the one-issue apply edit them.
- **T4** also owns the existing hydrate and endpoint files it must extend (`internal/source/jira/hydrate*.go`,
  `endpoints_*.go`). **T5** owns only new write files plus `writes.go`.
- Old-rule tests:
  - `internal/source/jira/hydrate_test.go` (no comment bodies fetched): T4 updates it.
  - `internal/export/export_test.go`: still valid, because exports carry no bodies.
  - `Connection.test.tsx`: the freeze updates the Disconnect call; T8 adds the delete-offer tests.

## Process
- Failing tests come first (AGENTS §9). Test-author output goes on each WP branch, not `main`.
- Stage by path, never `git add -A` (files arrive in the tree from outside).
- Push to Forgejo only when Chris asks; never to GitHub.

## Test-author SPEC-NOTEs confirmed
- **WP-T1 (adf), all 18 accepted as written.** Including:
  - ADF dates are the timestamp's UTC calendar date; `Past` is relative to `Now()`'s local day, and the same day is not past.
  - Links with schemes other than http, https and mailto are dropped.
  - textColor values that aren't lowercase `#rrggbb` are dropped.
- **WP-T2 (ADF components), all accepted as written.** This includes the `data-adf` and `data-*` test hooks, the
  textColor clamp (≥ 4.5:1 against the theme background, hue within 25°), and media src `/media/{id}` when cached
  "full", with fallback to the thumb and then a placeholder with "Load image" (architecture §4.9 wins over my brief).
  Unknown nodes say "unsupported block ↗" (the spec copy), and `expandAll` uses token semantics. A snapshot golden is
  optional, added after the first green run and reviewed.
- **WP-T3 (media), all 13 accepted.**
  - Undecodable, disallowed, over-25-MiB and over-60-MP files become file cards: no error and no decode.
  - The limits are inclusive.
  - Eviction takes never-viewed rows first, oldest created first, then rows by `last_viewed_at`.
  - **Orphan sweep (decided):** runs inside `Evict`. Files under the media dir that no attachment row references
    through `file_path` or `thumb_path` are removed and counted in `freedBytes`. The app calls `Evict` once at startup
    (WP-T9) and after each media phase, so the contract doesn't change. WP-T3 adds a test for it.
- **WP-T5 (writes), all accepted:**
  - the fake workflow invention (Done needs a screen with a required resolution; Code Review is optional-only);
  - the status-code mapping;
  - offline before send, timeout after;
  - `RetryInMs` = Retry-After × 1000.
  - **Ordering:** the T5 implementer starts after **T0** (the client gains a PUT method and a never-retry write path,
    since client*.go is T0's) and **T1** (`adf.FromPlainText`) are merged.
  - The fake options the docs promise (`RefreshFails`, `StaleChangelog`; `Options.Writes` isn't needed) are added to
    `jiratest/contract.go` as an orchestrator amendment before T6 and T9.
- **WP-T4 (content), accepted, with one decision.**
  - **Field registry (decided):** a process-wide cache inside the jira package, keyed by connection base URL, shared by
    `Fields()`, `Hydrate` and `GetIssue`. It is refreshed when older than 24 h or on a full sync (`source.Options.Full`),
    which keeps jira-sync §2.2's "once per connection per day". A fresh base URL (each test server) still costs exactly
    one `/field` request, so the tests' "one per Adapter" assertion holds.
  - The others are accepted as written:
    - content lives in an overlay `jiratest/testdata/content.json` (fixturegen owns `fixture.json`);
    - the kind mapping, including no rule producing `component`;
    - comments are excluded from the content hash;
    - the `GetIssue` request shape;
    - `FetchMedia` follows redirects without `Authorization`.
  - **Ordering:** the T4 implementer starts after T0 merges (T4 extends the client's hydrate path).
- **WP-T8 (content UI), all 11 accepted.**
  - The Composer is prop-driven and never calls the api. **WP-T7's state layer owns the drafts:** the 400 ms debounced
    `ticket.saveDraft`, the flush on blur, clearing after a successful `addComment`, and optimistic pending comments.
  - Comments arrive oldest first; "newest" reverses them, and the marker sits below the new comments.
  - Sizes display in decimal units (412 KB); the `storage.cleanup_offer_mb` cap is in MiB (1 GB = 1024 MB in the stepper).
  - The Disconnect offer reads sizes on click and never blocks the confirm.
  - "COMMENTS 0" always shows (the composer lives in that section).

## Amendments before WP-T6 (orchestrator)
- `source.WithInteractive(ctx)` / `source.IsInteractive(ctx)`: the core's neutral marker for user-initiated calls
  (Connect, ticket Refresh, opening Status, writes, mention search). The jira client maps it to the limiter's
  interactive lane, tested by `client_interactive_test.go`. **WP-T9 marks** the TicketService and Connect calls with
  it, since `internal/app` may not import the jira package.
- jiratest `Options.RefreshFails` and `Options.StaleChangelog` are frozen; **WP-T6 implements** their handlers (it now
  owns `jiratest/**`).
- `Capabilities()` reads `contentCapability()` (content.go) and `writesCapabilities(meta)` (writes.go). Both are true
  for the jira adapter now that T4 and T5 have merged.
- **WP-T7 (modal shell), all accepted.** This includes the invented `state/ticket.ts` API, the `ticket:updated`
  handling (sync cause → "Updated in Jira" names; Reload refetches), optimistic writes with deep revert and no re-send,
  the refresh state machine, and draft autosave (400 ms; flush on blur, unmount, switching issue, close, pagehide).
  - **Geometry:** the ux-spec §14.2 *table* wins over its prose (1440 → 1240 wide, 1280 → 1216, 1024 → 700 content
    column). The prose is corrected to match.
  - **Copy:** "Jira is rate-limiting · try again in Ns" for errors; "waiting for Jira (rate limit) · Ns" for the
    `waiting` phase in the header.
  - **Step-list builder (decided):** WP-T9 owns it, in `frontend/src/state/ticketSteps.ts`. Person view: walk order,
    then Done. Board: lanes, then Done, then backlog (testing §4). T7's state takes `steps` as an argument.
  - **Offline:** `Connection.state` has no "offline" value; WP-T9 decides how the modal feeds `FieldRail.offline`.
  - **WP-T7's implementer is Sonnet** (programmatic opening of the shared Combobox is subtle; 267 tests).
- **WP-T7 merged.** Follow-ups for **WP-T9**:
  - (a) Add a bound `AppService.OpenURL(url)` (http/https/mailto only) plus a facade `api.app.openUrl`, and replace
    T7's direct `@wailsio/runtime` `Browser.OpenURL` use. Bindings only through `src/api` (AGENTS §3).
  - (b) Comment order persists through the save queue, so it briefly drives Configure's "Saved" indicator. Acceptable,
    but check it in UAT.
  - **Measured with the modal mounted:** JS 63.0 KB gz (limit 70), **CSS 15.7 KB gz (limit 12): a budget decision is
    needed.**
- **WP-T6 (ticket backend), rulings:**
  - **Golden ≠ mock.** The hand-built mock and the fake's MOB-1421 overlay are different data. The shape matches
    exactly. Per decision 7, **WP-T9 replaces `frontend/src/api/fixtures/ticket_mob1421.json` with the Go golden**
    (normalised) and re-shoots the screenshots.
  - **`view.BuildTicketWith(snap, t, prefs, asOf, TicketOptions)`** and `view.CanFor` are accepted as non-frozen
    additions. **WP-T9 calls `BuildTicketWith`**, passing SelfAccountID, Writes, AuthState and RefreshFailed.
  - Go follows the spec where the mock disagrees (custom fields A–Z, days in lane, "Original estimate", the derived
    texts).
  - Accepted as written:
    - the parent snapshot as a synthetic `issue_field_value` row `parent`;
    - `Orchestrator.WaitMedia()` (Resync isn't blocked by downloads);
    - the rev bump even when the stored row was newer;
    - the lane fallback from the status category;
    - the media-id lookup costing 2 requests.
  - **WP-T9 wires:**
    - `sync.Options.Media` to an adapter over `media.Cache`;
    - `Evict` at startup;
    - emitting `ticket:updated {cause: write}` and `data:changed` from `IssueApplied`;
    - the draft, transition-cache, field-pref and storage helpers.

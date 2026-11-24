# WP-T6: ticket detail backend

Content and media phases, the one-issue apply, `cache.LoadTicket`, `view.BuildTicket`, the jiratest F12 faults, and the
MOB-1421 golden. Sources: architecture §3.2 ("Rules for view.BuildTicket", "Light writes"), data-model §2.5a and Q9,
jira-sync §2.14 and §4.8, ux-spec §14.6, ADR 0015, F12-freeze.md. 91 new tests (cache 27, sync 29, view 30, jiratest 5).

**Tests-first record.** Red before the implementation (seen failing for the right reason, `not_implemented` or the missing
behaviour): the `LoadTicket` suite, the one-issue apply suite (`ApplyIssue`, `ApplyIssueDelta`, `RefreshFails`,
`StaleChangelog` end to end), the jiratest fault tests, and the `BuildTicket` and rail suites. Written alongside the
implementation and then checked by **mutation** (a mutated implementation made the named tests fail): the cache apply suite
(skip-newer, hash gating, tombstones) and the sync content and media suites (no `Evict` after the phase, media id not
recorded, `Shutdown` not cancelling the phase, `ticket:updated` not emitted, comments never tombstoned, the lookup's media
id not stored).

## What was built

| Area | Files | Notes |
|---|---|---|
| Per-issue apply | `cache/content_apply.go` | `ApplyIssueDetailTx` is the one upsert behind both the sync apply and `ApplyIssue`. `ApplyIssueTx` (WP-30 signature) now delegates to it. |
| Content tables | same | `issue_content`, `issue_field_value`, `issue_link`, `attachment`, `field_def` (`ApplyFieldDefsTx`). Replaced per issue only when `content_hash` changes. |
| Comments | same | Bodies, visibility, `updated_at`; tombstone (`deleted_at`) only after a complete fetch (`d.Content != nil`) and never for a skipped row; a returning comment clears it; a body-less upsert never erases a body. |
| Never overwrite newer | same | The issue row is skipped when the incoming `updated_at` is strictly older (a zero incoming time never counts as older). Change rows and comments still merge; content is left alone. |
| Delta | same | `ApplyIssueDeltaTx` touches status/assignee/flag only, never `updated_at`, no change rows. |
| LoadTicket | `cache/ticket.go`, `content_refs.go` | One reader tx. People and Issues hold only what the ticket references (mentions and issue keys are read from the stored ADF). |
| Media helpers | `cache/content_media.go` | Targets for the media phase, media id and fetch error recording. |
| Sync | `sync/content.go`, `oneissue.go`, `media.go`, edits in `apply.go`, `orchestrator_run.go`, `orchestrator.go` | See below. |
| BuildTicket | `view/ticket*.go` | `ticket.go` (assembly), `ticket_row.go`, `ticket_rail.go`, `ticket_parts.go`, `ticket_resolver.go`, `ticket_options.go`. |
| Fake Jira | `jiratest/f12_faults.go` + 4 one-line hooks | `RefreshFails`, `StaleChangelog`; state in a side table keyed by `*Server` because `Server` is frozen. |

### Sync
- **Content phase** has no requests of its own: the adapter already fetches content and comments inside `Hydrate`. The
  orchestrator reads the field registry from the adapter's cache (`Source.Fields`, zero requests) and stores it with the
  apply. A registry failure adds warning `content_partial` and does not fail the run.
- **Comment tombstones** need a complete list: `len(d.Comments) >= d.CommentTotal` (or a total of 0); a short or early-ended
  page tombstones nothing.
- **First content is not announced:** an issue's first-ever content apply (a new issue, or an existing one with no content
  row, as after migration 0003) emits no content-derived `ticket:updated`; a status/assignee/flag change still does.
- **ticket:updated {cause: sync}** is emitted after the apply commits, once per hydrated issue that changed, with the live
  standups that contain it then. A no-change sync emits none. Changed names: status, assignee, flag, comments,
  description, fields (environment, field values, links, subtasks, parent, watchers), attachments.
- **Media phase** runs after `sync:finished`, in the run's goroutine, in a pool of two, only when `Options.Media` is set and
  the source has content:
  1. image attachments of the hydrated issues without a thumbnail: `FetchMedia` (media lane) then `MediaStore.Put`; the
     media id from the redirect is stored; `cached == "none"` falls back to the provider thumbnail (`PutThumb`);
  2. jira-sync §2.14 step 2: attachments with an unknown media id on issues whose ADF names an unmatched media id are fetched
     only to learn the id (body closed unread);
  3. `Evict` once.
  Failures set `attachment.fetch_error` and never touch the run's status. `media:ready` per landed image, `sync:progress
  {phase: media}`. A context kept per phase is cancelled by `Shutdown` (a package-level registry, because the frozen
  `Orchestrator` struct has no slot for it).
  **`Wait(standupID)` returns before the media phase ends** (the run is released first, so Resync is never blocked by
  downloads); `Shutdown` waits for it. Anything that must see the phase finished (T9's no-network-on-open test) calls the
  new exported `Orchestrator.WaitMedia()`, which waits for every run goroutine, media phases included.
  The media-id lookup of step 2 costs **two requests** (the 303 and the pre-signed download, whose body is then closed
  unread): `source.Source` has no header-only call, and jira-sync §2.14 asks for one request. A header-only
  `Source` method would halve it; it is not worth a contract change for a handful of attachments per sync.
- **One-issue apply** (`ApplyIssue`): references (status, type, people, sprints with a board) + `ApplyIssueDetailTx` +
  transition cache drop on a status change + rev bump of every live standup that has the issue as a current member (also
  when the stored row was newer: merged change rows may have changed what they show). Archived standups are not bumped.
  Membership, spans, config history and sync state are untouched. Nothing is emitted.

### Request counts (default fixture, MOB board, 55 issues)
| | requests | budget |
|---|---|---|
| full sync, non-dev (issues and content) | 79 | ≤ 250 |
| incremental, 3 changes, non-dev | 11 | ≤ 50 |
| dev-status (unchanged) | 44 summary + 85 detail full; 29 summary incremental | per-target caps hold |
| media phase | 8 (3 images + 1 media-id lookup, 2 requests each) | not in `sync_run.requests`; media lane |

A no-change incremental fetches no comments, no field list and no media.

### BuildTicket decisions the docs left open
- `Row` is built from the **ticket's own history** over 21 days (not the snapshot's); for a visible member of a 21-day
  standup it equals the board row (drift test).
- Lane for an issue whose status maps to backlog, hidden or no lane: from the status category (new → todo, indeterminate →
  prog, done → done) so the modal always has a swatch.
- **Not set:** Details lists a registry field (reporter, priority, due, labels, components, fix versions, time tracking) when
  the connection's registry has it; Parent unless the issue is an epic; Sprint on a scrum board when the issue has no
  sprint; Points when a story-points field exists (a custom number field named "Story point(s) [estimate]"); Environment
  always. **Custom is registry-driven too** (`cache.Ticket.Registry` "drives Not set and order", data-model Q9, testing.md
  "Empty-field folding"): rows are the pinned fields in pinned order, then the other non-empty custom fields A-Z; Not set
  lists the empty pinned fields first (always), then the other empty custom fields of the registry **that some other issue
  of the same space has a value for** (bounds the line on a site with hundreds of site-wide fields), A-Z. Excluded from
  Custom: hidden fields, the story points (in Details), and the fields the sync models elsewhere (jira-sync §2.2: sprint
  and rank by custom type, Flagged by name, development summary by the `:devsummarycf` suffix). The usage set is
  `cache.FieldsInUseTx(ctx, tx, issueID)` (one DISTINCT query); `cache.Ticket` is frozen, so it travels beside the ticket in
  `view.TicketOptions.FieldsInUse` (nil = unknown = none of the non-pinned ones). **T9 must call `FieldsInUseTx` in the same
  reader transaction as `LoadTicketTx` and pass it.** Dates lists Resolved; Watchers only when known. Hidden fields
  (any rail field id in `prefs.Hidden`) are neither a row nor Not set.
- Two rules worth knowing: **`not_on_board` means `removed_at` is set** (the issue left every standup; an inline-card
  issue that merely is not in this standup does not warn), and **system fields outside the Details list** (`project`,
  `security`, `resolution`, ...) are stored but not shown.
- Sprint resolves through `snap.Sprints` (the active sprints); a sprint that is not active is unnamed, so such an issue gets
  neither a row nor a Not-set entry.
- Media resolution: steps 1 (media id), 3 (alt = unique attachment name) and 4 (positional, per document, only when the
  counts match) in the resolver; `media_unresolved` is added once per ticket.
- `newSince` = local midnight of the previous Monday-to-Friday day (Monday and weekends look back to Friday).
- The parent's display snapshot is stored as `issue_field_value` row `parent` (kind unknown, raw `IssueRef`), since
  `issue_link` admits only link and subtask. `LoadTicketTx` reads it as `Ticket.Parent` and never as a field.

## Golden: `internal/app/testdata/ticket_mob1421.golden.json`
Built in `internal/sync/ticket_golden_test.go` through the real stack (jiratest + adapter + orchestrator + store +
`LoadTicket` + `BuildTicketWith`), with a fake `MediaStore` that decodes the PNGs like `internal/media`. `-update`
rewrites it; the run compares byte for byte. Only the fake server's base URL is normalised.

**Status against `frontend/src/api/fixtures/ticket_mob1421.json`: NOT equal, and cannot be.** The mock was hand-built for the
mockups; the fake Jira's overlay describes a different MOB-1421. The test asserts what must agree and logs the rest:
- `TestTicketGolden_ShapeMatchesTheMockFixture` (hard assertion): every path and every key of every object agree, and so do
  the kinds of every value once `null` is dropped (an optional member one dataset has and the other lacks); a null where
  the other side has a list fails. ADF documents are the adf package's. It passes: the Go output has exactly the shape the
  mock and the generated TS types expect.
- `TestTicketGolden_RowEqualsTheBoardRowThroughTheRealStack`: for every board ticket of the synced fixture, the modal's
  `Row` (built from the ticket's complete history) equals `view.Build`'s row (built from the standup's windowed history)
  at 21 days, so the two history loaders agree; MOB-1421's board row shows 2 days in lane.
- `TestTicketGolden_LogsTheDataDifferencesToTheMockFixture` logs the ~270 differing leaves (`go test ./internal/sync -run
  LogsTheData -v`).

### CONTRACT-ISSUE items
1. **Fixture data (one item, not a rule).** Different dataset: ids (local 7 vs 107, `u-iris` vs `iris`), times, attachments
   (3 PNG + 1 PDF vs 4 + 1), subtasks (MOB-1436/1440/1456 vs 1422-1426), link groups (3 vs 2), labels, components, fix
   version, 4 comments vs 7 (Developers-only one, an edited one), custom field names (Platform vs Platforms, Reviewers
   users vs Team...), no parent, 20 commits and 2 PRs vs 7 and 1, watchers 3 vs 4, environment set. Freeze decision 7
   already has T9 replacing the mock with this golden; nothing to change in Go.
2. **Dev model has no branch and no PR title.** `model.DevEvent` and `dev_pr_event` carry neither, so the Development group
   has no Branch row and a PR row's Text (and `DevPR.Title`) is the repository. The mock shows
   `feature/MOB-1421-unit-toggle` and "Unit preference API". Fixing it needs fields in `model.DevEvent` (frozen) and a
   migration; out of T6.
3. **`BuildTicket`'s frozen signature cannot carry** the connected account (Self), the source's capabilities and the
   connection state (`Can`), or "the refresh failed". Added `BuildTicketWith(snap, t, prefs, asOf, TicketOptions)` and
   `view.CanFor(writes, authState)`; `BuildTicket` is `BuildTicketWith` with zero options (nobody is Self, `Can` is
   `read_only`). **T9 must call `BuildTicketWith`** with `SelfAccountID` (connection account id), `Writes` (from
   `Capabilities().Writes`), `AuthState` and `RefreshFailed` (set when `ApplyIssueDelta` was used).
4. **Link and subtask assignee initials are empty for Jira.** The adapter's issue references (`wireIssueRef.toRef`) carry no
   assignee, and `cache.TicketLink` has no live assignee for a synced target, so a synced subtask cannot show its assignee
   either. The mock shows initials. Needs the adapter to request `assignee` on references (T4's files) or a
   `LiveAssigneeID` on `TicketLink` (frozen).
5. **Rules where the hand-built mock is not self-consistent with the spec** (the Go output follows the spec):
   - Custom group order: spec says pinned, then A-Z; the mock's order is not A-Z (it is hand-ordered). The mock's custom
     Not set holds one pinned empty field ("Workaround"); the golden's is empty (no other issue of the fixture fills in the
     fixture's six empty custom fields).
   - `Original estimate` appears in the mock's Details Not-set; it is a separate Jira field the rail does not model.
   - Mock text decorations that are not derivable from `FieldValue`: "20 %" on a number, "Saffron Mobile (MOB)" on a project.
   - `row.daysInLane` 3 in the mock vs 2 here: the mock's own segment (days 18-21) and pulse label disagree with the
     board's `pulse.DaysInLane`, which is what the board shows.
6. `media_unresolved` is not raised by the fake MOB-1421: its deliberately dangling media id still resolves by alt name
   (jira-sync §2.14 step 3). The unresolved path is covered by a hand-built ticket test.

## For T9
- Wire `sync.Options.Media` to an adapter over `media.Cache` (`Put` returns `Entry` there; map to "full"|"thumb"|"none"),
  call `Evict` at startup (orphan sweep), emit `ticket:updated {cause: write}` and `data:changed` from `IssueApplied`.
- Tests that must see the media phase finished call `Orchestrator.WaitMedia()` (`Wait(standupID)` does not).
- `ApplyIssue` takes the connection row id; `GetIssue` details carry the changelog, comments and content.
- Mark Refresh and writes with `source.WithInteractive(ctx)` before calling the source.
- Drafts, transition cache reads/writes, field prefs CRUD and storage usage are T9's (cache has the tables; no helpers here).

## Not done / limits
- No history view of descriptions (current value only, as designed).
- `ticket_field_pref` and `ticket_draft` have no cache helpers in this WP (reads only, via `LoadTicket`).
- A write-path media fetch (`EnsureMedia`) is T9.

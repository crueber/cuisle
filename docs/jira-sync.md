# Cuisle — Jira sync

Scope: the **Jira source adapter** (`internal/source/jira`, the v1 implementation of `source.Source`; architecture.md
§2.2) — every byte exchanged with Jira Cloud — plus the provider-neutral sync algorithm (`internal/sync`, which sees
only `source.Source`), the mapping from synced facts to pulse inputs, and the fake Jira used for development and tests.
Table/column names refer to `data-model.md`; events and services to `architecture.md` §3.

How the neutral interface maps onto this document:

| `source.Source` method | Jira implementation |
|---|---|
| `Test` | §2.1 |
| `Reference` | §2.2 (field ids → `connection.source_meta`), §2.6 |
| `SearchSpaces` / `SearchBoards` / `SearchUsers` | §2.3 / §2.4 / §2.12 |
| `BoardConfig` / `ActiveSprints` | §2.5 / §2.7 |
| `ValidateFilter` | §2.8 (`jql/parse`) |
| `Plan` | §2.8 query table (JQL strings are the opaque `Query.Expr`) |
| `Scan` | §2.8 scan field set (+ approximate count for the backlog) |
| `Hydrate` | §2.8 hydrate field set + §2.9 changelog + §2.10 comments, mapped to `model.IssueDetail` |
| `DevActivity` | §2.11 |
| `Capabilities` | `{HasSprints, HasBacklogRank, HasFlagging, HasDevActivity, HasExtraFilter, HasAssigneeHistory: true; FilterLanguage: "JQL"}` (`HasDevActivity` flips to false when the probe fails) |

## 1. Client basics (`internal/source/jira`)

- Base URL: `connection.base_url` = `https://<site>.atlassian.net` (https only; no trailing slash). Classic (unscoped)
  API tokens only in milestone 1.
- Auth: `Authorization: Basic base64(email ":" token)`; token read from the keychain per sync run (held in memory for the
  run, never logged). Headers: `Accept: application/json`, `Content-Type: application/json` on POST,
  `User-Agent: Cuisle/<version> (<os>)`.
- Timeouts: 10 s connect, 30 s per request, 5 min per sync run. HTTP/2, keep-alive, one shared `http.Client`.
- Concurrency: one global semaphore of **4** in-flight requests across all runs; plus a token bucket of 10 requests/s.
- Decoding: `encoding/json` into narrow DTOs (only the fields listed below); unknown fields ignored. Timestamps accept
  both ISO-8601 with offset (`2026-11-23T09:15:02.123+0000`) and epoch milliseconds (number or numeric string).
- Every method takes `ctx`; cancellation aborts the in-flight request.

## 2. Endpoints

`{F}` = the board's saved filter id (`standup.board_meta.filterId`), `{B}` = board id, `{S…}` = active sprint ids.

### 2.1 Auth check — `GET /rest/api/3/myself`
Used by `ConnectionService.Connect` and as the first request of every sync. Read: `accountId`, `displayName`, `active`.
200 → `auth_state='ok'`. 401 (or 200/403 with header `X-Seraph-LoginReason: AUTHENTICATED_FAILED`) → `auth_invalid`.

### 2.2 Field discovery — `GET /rest/api/3/field`
Once per site per 24 h and on every full sync. Resolve and store in `connection.source_meta`
(`sprintFieldId`, `rankFieldId`, `flaggedFieldId`; written below in their snake_case shorthand):
- `sprint_field_id`: `schema.custom == "com.pyxis.greenhopper.jira:gh-sprint"`
- `rank_field_id`: `schema.custom == "com.pyxis.greenhopper.jira:gh-lexo-rank"` (prefer the id from board config, §2.5)
- `flagged_field_id`: custom field with `name == "Flagged"` (untranslated name; fallback `clauseNames` contains `Flagged`)

Missing Flagged field → blocked detection uses status names only (warning `no_flagged_field`).

### 2.3 Space autocomplete — `GET /rest/api/3/project/search`
Params: `query=<text>`, `maxResults=20`, `orderBy=name`, `typeKey=software`, `status=live`.
Read `values[].{id,key,name,style}`. Results upserted into `space`. Debounced by the combobox (150 ms).
Offline → answer from `space` with a case-insensitive `LIKE` on key/name.

### 2.4 Board autocomplete — `GET /rest/agile/1.0/board`
Params: `projectKeyOrId=<key>`, `name=<text>` (omitted when empty), `maxResults=50`, `orderBy=name`.
Read `values[].{id,name,type}` (`type` ∈ `scrum|kanban|simple`). Upsert `board`. Paginate with `startAt` while
`isLast == false` only when `name` is empty and fewer than 200 boards have been read.

### 2.5 Board configuration — `GET /rest/agile/1.0/board/{B}/configuration`
Read: `type`, `filter.id`, `subQuery.query`, `columnConfig.columns[].{name, statuses[].id}`, `ranking.rankCustomFieldId`.
Called by `ConfigService.SetBoard` and at the start of **every** sync (columns change rarely but cheaply detectable).
Effects: replace `board_column*`; merge into `standup_column_map` (new columns auto-mapped, removed columns deleted,
`auto=0` choices preserved by column name); rebuild `standup_status_lane`.

Board type normalisation: `scrum` → scrum; `kanban` → kanban; `simple` → scrum if §2.7 returns 200, kanban if it returns
400/404 ("board does not support sprints").

Auto-mapping of a column to a lane (first rule that matches, case-insensitive on the column name; otherwise by the
status category of the column's first status):

| Rule | Lane |
|---|---|
| name contains `backlog` | `backlog` |
| name contains `review` or equals `pr` | `review` |
| name contains `qa`, `test`, `verif`, `uat`, `ready for release` | `qa` |
| last column, or every status has category `done` | `done` |
| name contains `progress`, `doing`, `dev` (not "for dev") | `prog` |
| every status has category `new` (e.g. "To Do", "Selected for Development") | `todo` |
| category `indeterminate` | `prog` |

### 2.6 Statuses — `GET /rest/api/3/status`
On full sync (and when a status id is unknown). Read `[].{id,name,statusCategory.key}` → `status`
(`category` from `statusCategory.key`: `new|indeterminate|done|undefined`; `blocked_hint` from the name).

### 2.7 Active sprint — `GET /rest/agile/1.0/board/{B}/sprint?state=active&maxResults=50`
Scrum boards only, every sync. Read `values[].{id,name,state,startDate,endDate,completeDate}` → `sprint`.
No active sprint → standup shows "No active sprint"; `scope='sprint'` yields an empty board plus Done per window from
the most recently cached sprint data; backlog still syncs.

### 2.8 Issue search — `POST /rest/api/3/search/jql`
The only issue-search endpoint used (`/rest/api/3/search` is removed). Body:

```json
{ "jql": "...", "fields": ["..."], "maxResults": 100, "nextPageToken": "<from previous page>" }
```

Pagination: repeat with `nextPageToken` until the response has none (`isLast` true/absent token). There is no `total`;
progress totals come from the scan. Pages are sequential per query (token-chained); different queries run concurrently.

Two field sets:

- **Scan** (cheap, all members): `fields: ["updated"]`, `maxResults: 1000` (server may return fewer; follow tokens).
  Returns `issues[].{id, key, fields.updated}` in JQL order.
- **Hydrate** (changed/new issues only), `maxResults: 100`, JQL `id in (<≤100 ids>) `:
  `fields: ["summary","issuetype","status","assignee","created","updated","resolutiondate","parent","comment",
  "<sprint_field_id>","<rank_field_id>","<flagged_field_id>"]`.
  Read: `summary`; `issuetype.{id,name,subtask,hierarchyLevel}`; `status.{id,name,statusCategory.key}`;
  `assignee.{accountId,displayName,active,accountType}` (may be null); `created`, `updated`, `resolutiondate`;
  `parent.key`; sprint field `[].{id,name,state,startDate,endDate,completeDate,boardId}`; rank field (string);
  flagged field (non-empty array ⇒ flagged); `comment.{total, comments[].{id, created, author.accountId}}`
  (comment bodies are skipped by the decoder and never stored).

JQL is built by the adapter's `Plan(PlanInput)` (the orchestrator never sees JQL); `{X}` = `AND (<extra_filter>)` when non-empty; `{K}` = `AND (<board_meta.subQuery>)`
for kanban when present. Status id lists come from `standup_status_lane` (`W` = working lanes todo/prog/review/qa,
`P` = prog/review/qa, `D` = done, `BL` = backlog, `T` = todo).

| Query | When | JQL | Role |
|---|---|---|---|
| `sprint` | scrum, `scope='sprint'`, active sprint exists | `filter = {F} AND sprint in ({S…}) {X} ORDER BY Rank ASC` | `board` |
| `open` | scrum, `scope='board'` | `filter = {F} AND status in (W) AND (sprint in openSprints() OR status in (P)) {X} ORDER BY Rank ASC` | `board` |
| `open` | kanban | `filter = {F} AND status in (W) {K} {X} ORDER BY Rank ASC` | `board` |
| `done` | scrum `scope='board'`, or kanban | `filter = {F} AND status in (D) AND (updated >= -15d[ OR sprint in ({S…})]) {K} {X} ORDER BY Rank ASC` | `board` |
| `backlog` | scrum | `filter = {F} AND status in (BL ∪ T) AND (sprint is EMPTY OR sprint not in openSprints()) {X} ORDER BY Rank ASC` — first 50 only | `backlog` |
| `backlog` | kanban, some column mapped to `backlog` | `filter = {F} AND status in (BL) {K} {X} ORDER BY Rank ASC` — first 50 only | `backlog` |

Notes:
- `backlog` is always fetched (limit 50 = the maximum of `backlog_rows`) so changing "Backlog rows" never needs a sync.
  Its total comes from `POST /rest/api/3/search/approximate-count` `{ "jql": "<backlog jql without ORDER BY>" }` →
  `standup_sync_state.backlog_total` (failure → −1).
- Empty status lists remove the query (e.g. no done column → no `done` query); an empty `W` is a config error surfaced
  in Configure ("Map at least one column").
- Relative dates (`-15d`) are used instead of absolute timestamps because absolute JQL datetimes are interpreted in the
  Jira user's profile timezone.
- An issue returned by both `board`-role queries is one member; if it is also in `backlog`, role `board` wins.
- The enhanced search is eventually consistent (a change made seconds ago may be missing); acceptable for a standup tool.

JQL validation for the Configure field — `POST /rest/api/3/jql/parse?validation=strict` with `{ "queries": ["<extra_filter>"] }`;
read `queries[0].errors[]`. An `ORDER BY` in the extra filter is rejected locally (`validation`).

### 2.9 Changelog — `POST /rest/api/3/changelog/bulkfetch` (chosen)
One paged call for many issues instead of `expand=changelog` (bloats search pages, capped per issue) or one
`GET /issue/{id}/changelog` per issue.

```json
{ "issueIdsOrKeys": ["10001","10002", "..."], "fieldIds": ["status", "assignee", "<flagged_field_id>"], "maxResults": 1000, "nextPageToken": "..." }
```

- Batches of ≤ 500 issue ids; follow `nextPageToken`.
- Read `issueChangeLogs[].{issueId, changeHistories[].{id, created, items[].{fieldId, field, from, to}}}`.
- `status` items → `status_change(history_id, changed_at, from_status_id=from, to_status_id=to)`.
- `assignee` items → `assignee_change(history_id, changed_at, from_account_id=from, to_account_id=to)` (`from`/`to` are
  account ids; null = unassigned). Needed for "who owned this as of a date" (ADR 0011).
- Flagged items (`fieldId == flagged_field_id` or `field == "Flagged"`) → `flag_change(flagged = (to != null && to != ""))`.
- Fetched only for **hydrated** issues, always the full history, **merged** into that issue's rows by `history_id`
  (upsert; rows are never deleted, so history survives a provider that truncates old changelog entries).
- Fallback if the endpoint returns 404/400 for the site or rejects the custom field in `fieldIds`: retry without
  `fieldIds` (filter client-side); if still failing, per-issue `GET /rest/api/3/issue/{id}/changelog?maxResults=100`
  with `startAt` paging at concurrency 4.

### 2.10 Comments
Primary source is the `comment` field in the hydrate query (§2.8). Top-up only when
`comment.total > len(comment.comments)` **and** the embedded page does not already reach back past the **backfill horizon**
(35 days = the longest pulse window plus slack; this bounds first-time fetching only — stored comments are never aged out): `GET /rest/api/3/issue/{id}/comment?orderBy=-created&maxResults=100&startAt=n`, stop at the first page whose
oldest comment is older than 35 days. Read `comments[].{id, created, author.accountId}`. Stored rows: timestamp + author only.
Comments by `accountType == "app"` authors (automation/bots) are stored but excluded from marks.

### 2.11 Development information (internal API, best effort)
Not a public API: no stability guarantee. All failures degrade to warnings.

1. **Summary** — `GET /rest/dev-status/latest/issue/summary?issueId={id}`
   Read `summary.repository.overall.{count,lastUpdated}`, `summary.pullrequest.overall.{count,lastUpdated,state,stateCount}`,
   and the keys of `summary.repository.byInstanceType` / `summary.pullrequest.byInstanceType` (e.g. `GitHub`, `bitbucket`,
   `GitLab`; passed back verbatim as `applicationType`). `dev_fingerprint = sha1(repository.count|repository.lastUpdated|pullrequest.count|pullrequest.lastUpdated|pullrequest.state)`.
   Unchanged fingerprint → skip detail.
2. **Detail** (only when the fingerprint changed), per instance type present:
   - `GET /rest/dev-status/latest/issue/detail?issueId={id}&applicationType={type}&dataType=repository`
     → `detail[].repositories[].{name, commits[].{id, authorTimestamp, merge}}` → upsert `dev_commit`.
   - `GET /rest/dev-status/latest/issue/detail?issueId={id}&applicationType={type}&dataType=pullrequest`
     → `detail[].pullRequests[].{id, status, lastUpdate, repositoryId, repositoryName}` → insert `dev_pr_event`
     `(pr_key, status, at=lastUpdate)` if that triple is new.

**Dev targets** (the only issues queried each sync; issue `updated` does not change when commits arrive, so these are
re-checked every sync): members with role `board` whose lane is `prog`, `review` or `qa`; plus members that changed
status within the last 28 days and have `dev_checked_at` older than their last status change or NULL (covers freshly Done
and bounced-back tickets once). Backlog items are never queried.

Degradation:
- Probe = the first summary call of the run. 401/403/404, or a non-JSON body → `connection.dev_activity_state='unavailable'`,
  skip the phase, warning `dev_activity_unavailable`; re-probe on the next full sync or after 24 h.
- Per-issue failures (5xx, timeout) → keep existing rows, warning `dev_activity_partial`, run status `partial`.
- UI: when unavailable, commit/PR marks and their legend entries are hidden and Configure › Pulse shows
  the one-line warning from ux-spec §9.4.

### 2.12 Users for "Add someone from this space" — `GET /rest/api/3/user/assignable/search`
Params: `project=<space_key>`, `query=<text>`, `maxResults=20`. Read `[].{accountId, displayName, active, accountType}`;
keep `active && accountType == "atlassian"`; upsert `person`. `UserOption.OnBoard` = already a speaker.
Offline → `person` `LIKE` search. Empty query → people already known for the board, most recent first.

### 2.13 Endpoint summary

| Purpose | Method + path | Frequency |
|---|---|---|
| Auth check | `GET /rest/api/3/myself` | connect, each sync |
| Fields | `GET /rest/api/3/field` | full sync / 24 h |
| Statuses | `GET /rest/api/3/status` | full sync |
| Spaces | `GET /rest/api/3/project/search` | typing |
| Boards | `GET /rest/agile/1.0/board` | typing |
| Board config | `GET /rest/agile/1.0/board/{B}/configuration` | set board, each sync |
| Active sprints | `GET /rest/agile/1.0/board/{B}/sprint?state=active` | each sync (scrum) |
| Issues | `POST /rest/api/3/search/jql` | each sync (scan + hydrate) |
| Backlog count | `POST /rest/api/3/search/approximate-count` | each sync |
| JQL check | `POST /rest/api/3/jql/parse` | typing extra filter |
| Changelog | `POST /rest/api/3/changelog/bulkfetch` | hydrated issues |
| Comments top-up | `GET /rest/api/3/issue/{id}/comment` | rare |
| Dev summary / detail | `GET /rest/dev-status/latest/issue/summary`, `.../detail` | dev targets |
| Users | `GET /rest/api/3/user/assignable/search` | typing |

## 3. Request policy

| Condition | Handling |
|---|---|
| 429 | Read `Retry-After` (seconds; fallback: `X-RateLimit-Reset` timestamp; fallback: 5 s). Close a **global gate** so all workers pause until then + 0–500 ms jitter; retry the request. Max 5 retries per request; if cumulative gate time in a run exceeds 120 s → fail `rate_limited`. Increment `sync_run.throttled`. |
| 500/502/503/504, connection reset, timeout | Retry up to 3× with exponential backoff 0.5 s → 1 s → 2 s (× jitter 0.5–1.5), honouring `Retry-After` if present. |
| 401 | No retry. Run fails `auth_invalid`; `connection.auth_state='invalid'`; emit `connection:auth {state:"invalid"}`. |
| 403 | `/myself` → `auth_invalid`. Elsewhere → `forbidden` for that resource (board/filter not visible): run fails with a message naming the resource. Dev-status 403 → degrade (§2.11). |
| 400 on search | Parse `errorMessages[]` → `filter_invalid` with Jira's message; mark `extra_filter_ok=0` when the extra filter is implicated (re-validated via `jql/parse`). |
| 404 board/filter | `not_found`: "This board no longer exists" — Configure shows the Source section in error. |
| DNS failure / no route / TLS error before any response | `offline` (TLS errors are logged in detail). No retry loop; the user presses Resync again. |
| An https:// address that answers in plain http | `offline` with "That address answers over http, not https. Check the site address." (http is only allowed for loopback, e.g. the fake Jira). |

## 4. Sync algorithm (`internal/sync`)

### 4.1 Triggers and modes

- Triggers: `manual` (Resync / `R`), `open` (sync-on-open policy), `config` (source changed in Configure).
- Modes: `incremental` (default) and `full`. A requested incremental is **promoted to full** when: the standup never
  synced; `needs_sync=1`; `last_full_at` older than 24 h; the board's column→status set changed in this run; or the active
  sprint ids differ from `standup_sync_state.active_sprint_ids`.
- One run per standup at a time (`Start` on a running standup returns the existing run id). Max 2 runs in parallel.

### 4.2 Why scan-and-diff instead of `updated >= lastSync − skew`

An `updated >=` delta cannot see issues that **left** the set (removed from sprint, moved, deleted, filter change), needs
clock-skew and timezone handling, and still requires a full pass to be correct. Cuisle instead scans membership with a
minimal field set every time (ids + `updated`, large pages: 1–3 requests for a typical board) and hydrates only issues
whose `updated` differs from the cache. This gives exact membership, exact rank order, no time arithmetic, and the same
code path for full sync (hydrate everything). A skewed `updated >=` query is not used anywhere.

### 4.3 Phases

The orchestrator is provider-neutral: each phase is a `source.Source` call (`Test`, `BoardConfig`/`ActiveSprints`/`Reference`,
`Plan`+`Scan`, `Hydrate`, `DevActivity`); the right-hand descriptions below are what the Jira adapter does inside them
(phases 3–5 are one `Hydrate` call per batch; progress phases are reported by the adapter through a callback).
All fetched data is held in memory in a `Snapshot` struct until §4.6. Progress events carry `{phase, done, total}`.

```
0. preflight   keychain token (missing → auth_missing); GET /myself
1. config      [parallel] board configuration · active sprints · (full: fields, statuses)
               → recompute column map + status lanes in memory; build queries (§2.8)
2. scan        [parallel per query] search/jql with fields=["updated"]
               → ordered id lists per role; total = distinct ids
               diff vs cache:  toHydrate = new ids ∪ ids whose updated ≠ issue.updated_at   (full: all ids)
                               toClose   = current members of this standup not in the scan (span closed; rows kept)
3. issues      hydrate toHydrate in batches of 100 ids (`id in (...)`), 4 batches in flight
4. changelog   bulkfetch for toHydrate (batches of 500)
5. comments    top-ups only (§2.10), concurrency 4
6. dev         dev targets (§2.11): summary → changed? → details; concurrency 4
7. apply       single write transaction (§4.6); then trim operational rows (separate tx)
8. sinks       enqueue + deliver for sinks with send_on='after_sync' (ok/partial runs only; architecture.md §2.3)
```

Ordering guarantees: phase N+1 starts only when N finished, except 4–6 which run concurrently with each other once 3 is
complete. Request budget, incremental with no changes: 1 (`myself`) + 2 (config, sprints) + ≤ 3 scans + 1 (count)
+ 1 per dev target.

Users: assignees and comment authors seen in hydrate responses are collected into the snapshot (`person` upserts);
no separate user calls.

### 4.4 Cancellation

`SyncService.Cancel(standupID)` cancels the run's context. In-flight requests abort; nothing has been written (data is
only in memory), so there is nothing to roll back. `sync_run.status='cancelled'`, event `sync:finished {status:"cancelled"}`.
App quit cancels all runs the same way. Switching standups or screens does **not** cancel.

### 4.5 Partial results

If phases 0–4 succeed and only comments/dev had per-issue failures, the run applies and finishes `partial` with
warnings (existing rows for the failed issues are kept). Any failure in phases 0–4 aborts the run with **no** apply:
the UI keeps showing the previous snapshot with the previous "synced" time.

### 4.6 Atomic apply (the UI never sees half a sync)

One `BEGIN IMMEDIATE` on the writer connection (`T` = the run's `started_at`):

1. Upsert `board*`, `sprint`, `status`, `issue_type`, `person`; update `connection.source_meta` /
   `dev_activity_state`.
2. Merge `standup_column_map` (auto-map new columns), rebuild `standup_status_lane`; update `standup` board snapshot
   (`board_name`, `board_type`, `board_meta`).
3. Upsert hydrated `issue` rows (by `connection_id, source_id`); for each, **upsert** `status_change`,
   `assignee_change`, `flag_change`, `comment` by natural key — never delete. Set `last_seen_at = T` on every scanned issue.
4. Upsert `dev_commit`; insert new `dev_pr_event`; set `dev_fingerprint`, `dev_checked_at`.
5. Membership: rewrite `standup_issue` for this standup from the scanned lists (with `rank_index`), and maintain
   `standup_issue_span` — open spans for new members/roles at `T`, close spans (`valid_to = T`) for issues no longer
   returned; set/clear `issue.removed_at` (`data-model.md` §2.6). Issues that left are **kept** with all their history.
6. Speakers: append newly seen assignees, refresh `display_name`, `last_seen_at`; prune stale auto rows (config only).
7. `standup_config_history`: if the effective-config fingerprint differs from the open row, close it and open a new one at `T`.
8. `standup_sync_state`: `needs_sync=0`, `last_success_at`, (`last_full_at`), `history_from` (first success only),
   `backlog_total`, `active_sprint_ids`.
9. `standup.rev = rev + 1`.
10. `COMMIT`.

Readers use WAL snapshots, so a `StandupService.View` running concurrently sees either the old or the new state in full.
After commit: finalize `sync_run`, emit `sync:finished` then `data:changed {standupId, what:"view"}` (and `what:"config"`
if step 2 or 6 changed anything). Then, for each enabled sink with `send_on='after_sync'` that covers the standup,
enqueue an outbox row (`idempotency_key = 'sync:<run_id>:<sink_id>'`) and deliver; outcomes arrive as `sink:delivered` /
`sink:failed` and never change the sync's status. Typical apply time for 400 issues: < 150 ms.

Two standups sharing issues (same board) each maintain only their own membership and spans; shared `issue` rows are upserted
with last-writer-wins on identical source data.

### 4.7 Offline and auth-failure UX

| Situation | Behaviour |
|---|---|
| Offline at launch | Cached view shown as always. Sync-on-open attempt fails `offline`: **no banner** — the header sync status reads "offline · data from 3 h ago" (alert text) until the next successful sync. No modal, no retry loop. |
| Offline on manual Resync | Dismissible banner "Offline — showing data from 3 h ago", plus the same header state. |
| Token missing (`auth_missing`) | Banner "Connect to Jira to sync" → Connect screen. Cached standups remain usable. |
| Token rejected (`auth_invalid`) | Banner "Jira rejected the token — Reconnect"; all sync triggers are suppressed until `Connect` succeeds; Configure comboboxes fall back to cached options and show "Offline — showing cached". |
| Board/filter forbidden or gone | Run fails; tab shows a warning dot; Configure › Source shows the message on the Board field. |
| Extra JQL invalid | Run fails fast `filter_invalid`; Configure shows Jira's parse error under the field. |
| Rate limited | Header `Syncing… waiting for Jira (12 s)` during gate; fails `rate_limited` after 120 s cumulative. |

## 5. Mapping synced facts → pulse inputs (`internal/pulse`, pure functions)

Inputs per ticket: current status id, `created_at`, ordered status changes, flag changes, comment/commit/PR-event
timestamps, the standup's `status_id → lane` map, lane limits, `asOf`, `loc` (local timezone), `pulseDays = N`.

**`asOf` replaces "now" everywhere below** ("today" = the local date of `asOf`). The live view passes the current time;
a historical view passes a past instant and a snapshot loaded by `internal/history`, in which the "current" status,
assignee and flag are those in force at `asOf` and events after `asOf` are absent. The functions are identical.

### 5.1 Window and day index

```
dayStart(k) = local midnight of (today − (N−1) + k)      k = 0 … N        (computed with time.Date in loc: DST-safe)
dayIndex(t) = k such that dayStart(k) ≤ t < dayStart(k+1);  t < dayStart(0) → before window
today index = N−1; the "today line" is drawn at the right edge of day N−1
```

`Axis[k] = {Date, Label: day-of-month, Weekend: Sat/Sun, Monday}`.

### 5.2 Status segments (including pre-window state)

1. Build the status timeline from the changelog: initial status = `from_status_id` of the first change (or the current
   status if there are no changes), effective from `created_at`; each change starts a new interval at `changed_at`.
   If the last change's `to_status_id` ≠ the issue's current `status_id` (changelog lag/truncation), append a synthetic
   change at `updated_at`.
2. Map each interval's status to a lane via `standup_status_lane`; unmapped or `hidden` → `todo` for drawing purposes
   when the ticket itself is visible; `backlog` draws as `todo` tone. Merge adjacent intervals with the same lane.
3. Snap to days: an interval starting at `t` begins at `dayIndex(t)` — **the day of a transition belongs to the new lane**;
   several transitions on one day → the last lane of that day wins (zero-width segments are dropped).
4. Clip to `[0, N)`: the interval active at `dayStart(0)` is the **pre-window state** and yields a segment starting at 0.
   A ticket created inside the window starts its first segment at `dayIndex(created_at)` (nothing before it).
   The last segment ends at `N` (through today).
5. Emit `PulseSeg{Lane, From, To}` oldest first. Done tickets keep their real lanes; the frontend mutes non-done
   segments for Done rows.

### 5.3 Per-day marks

Bucket by `dayIndex` in the local timezone; drop events before the window:
- `Commits[k]` = count of `dev_commit` with `is_merge=0` (deduplicated by SHA per issue).
- `PRs[k]` = count of distinct `pr_key` with a `dev_pr_event` that day. (Semantics: "PR activity that day" — open
  seen, merged, declined. The opened timestamp is not available from dev-status; see architecture risk R3.)
- `Comments[k]` = count of `comment` by non-app authors.

Only days with any non-zero value are emitted (`PulseMark{Day, Commits, PRs, Comments}`). **Events dated before the ticket's
`created_at` day are dropped** (no marks before the ticket existed). Go always sends all three counts; the marks toggles
(`show_*`) travel in `ViewOptions` and the frontend hides the disabled kinds. The frontend maps commit counts to three tick
heights (1, 2–3, 4+) and draws the PR diamond instead of the commit tick when both fall on one day (ux-spec §7).

### 5.4 Days in lane, over-limit

```
enteredLane = start of the latest contiguous run of intervals mapped to the ticket's current lane
DaysInLane  = max(1, calendarDays(localDate(now) − localDate(enteredLane)))   // entered today or yesterday = 1
            // e.g. entered Wednesday, viewed the next Monday → 5d, although the track shows 6 day columns (confirmed 2026-11-23)
OverLimit   = lane ∈ {todo,prog,review,qa} AND limit_days > 0 AND DaysInLane > limit_days
```

For `done`, `DoneAt = enteredLane` and `DaysInLane` = days since done (same minimum of 1). The chip shows `<DaysInLane>d` and is
red when `OverLimit || Blocked` (and the ticket is not done).

### 5.5 Blocked

A ticket is `Blocked` now when `flagged = 1` **or** its current status has `blocked_hint = 1`.
Blocked spans in the window = union of:
- Flag spans: fold `flag_change` in time order into `[on, off)` intervals; state before the first change is the
  inverse of that change's result; an open span ends at `now`. If `flagged = 1` but there is no flag history, span =
  `[updated_at, now)`.
- Status spans: intervals whose status has `blocked_hint = 1`.

Snap to day indexes (start day inclusive, end day inclusive), clip to the window, merge overlaps →
`Pulse.Blocked []{From, To}`.

### 5.6 Done window, backlog, counts

- Done list = role `board` tickets with lane `done` and:
  `sprint` → all (scope `sprint`), or those whose `sprint_ids` intersect the active sprint ids (scope `board`);
  `14d` → `DoneAt ≥ local midnight 14 days ago`; `yesterday` → `DoneAt ≥ local midnight of the previous working day`
  (Mon → Fri).
- Backlog = role `backlog` rows, first `backlog_rows`.
- `Counts.Active` = visible tickets in working lanes; `Unassigned` = of those, `Assignee == ""`; `Done`, `Backlog` = list lengths.

### 5.7 Kanban vs Scrum

| Aspect | Scrum | Kanban |
|---|---|---|
| Sprint call | yes | none (`Sprint = nil`) |
| `scope` | `sprint` (default) or `board` | forced `board` (DB CHECK) |
| Board members | `sprint` query, or `open` (+`done`) | `open` + `done`, each ANDed with the board sub-filter |
| Done window options | `sprint` (default), `14d`, `yesterday` | `14d` (default), `yesterday`; `sprint` rejected |
| Backlog | not in an open sprint, status in backlog/todo lanes | statuses of columns mapped to lane `backlog` (kanban-backlog boards); none otherwise |
| Header subtitle | `Sprint 42 · ends today` | `Kanban · continuous flow` |
| Promotion to full sync on sprint change | yes | n/a |

Team-managed (`simple`) boards follow whichever row matches their sprint support (§2.5).

### 5.8 Track label (`Pulse.Label`)

Plain-English summary for the track's `aria-label`, built in Go from the same data:
`"<current lane label> <DaysInLane> days[, previously <previous lane label> <n> days]; <c> commits, <p> PR updates, <m> comments in <N> days[; blocked <b> days]"`
— singular forms for 1; zero-count activity kinds omitted ("no activity in 21 days" when all are zero); the "previously"
clause only when a previous lane exists inside the window; `b` = total blocked days in the window. Done rows:
`"Done <n> days ago"` + the previous-lane clause.

## 6. Fake Jira / fixture mode (`internal/source/jira/jiratest`)

A real HTTP server (stdlib `net/http`) that implements the endpoints in §2 over an in-memory dataset, so the production
client — pagination tokens, 429 handling, decoding — is what gets exercised.

- **Start**: `cuisle --fake-jira[=path/to/fixture.json]` starts it on `127.0.0.1:<random>`, uses an in-memory keyring and
  `CUISLE_DATA_DIR=<tmp or ./.dev-data>`, pre-connects a site pointing at it, and creates one standup per fixture board
  on first run. `task dev` uses this by default; `task dev:real` does not. Tests call `jiratest.NewServer(t, fixture, opts)`.
- **Fixture file** `internal/source/jira/jiratest/testdata/fixture.json`, generated by `go run ./cmd/fixturegen` from
  `design/reference/sample-data.json`. All dates are stored **relative to "today"** and materialised at server start
  (`Options.Now`), so the mockup scenes reproduce on any date:

```jsonc
{
  "projects": [{ "id": "10001", "key": "MOB", "name": "Saffron Mobile" }],
  "boards":   [{ "id": 1, "name": "MOB board", "type": "scrum", "projectKey": "MOB", "filterId": 9001,
                 "columns": [{ "name": "Selected for Development", "statuses": ["To Do"] }, "..."],
                 "sprint": { "id": 42, "name": "Sprint 42", "startDay": -13, "endDay": 0 } }],
  "users":    [{ "accountId": "u-iris", "displayName": "Iris Calloway" }],
  "issues":   [{ "key": "MOB-1390", "type": "Story", "summary": "…", "assignee": "u-iris", "board": 1,
                 "inSprint": true, "flaggedDays": [],              // [[fromDay, toDay]] relative, 0 = today
                 "history":  [{ "status": "To Do", "days": 5 }, { "status": "In Progress", "days": 6 }, "..."],
                 "commits":  [-9, -9, -4], "prs": [{ "id": 12, "day": -3, "status": "MERGED" }], "comments": [-6, -1] }],
  "backlog":  [{ "key": "MOB-1525", "type": "Story", "summary": "…", "board": 1 }]
}
```

- **Conversion rules** from the sample shape (`tickets{k,a,s,ty,t,days,bl,blD,ev[28],cm[28],hist[{s,d}]}`):
  `hist[]` → `history` (lane ids → status names `todo→To Do`, `prog→In Progress`, `review→Code Review`, `qa→QA`,
  `done→Done`); `ev[i]` of 1 or 2 → that many commits on day `i−27`; `ev[i] == 3` → one PR event that day; `cm[i]` true →
  one comment that day; `bl`/`blD` → `flaggedDays: [[-(blD−1), 0]]`; `a` → user id; `backlog[]` → backlog issues in rank
  order (array order = rank). `people[]` → users.
- **Behaviour implemented**: Basic-auth check (any email, token `fake-token`; anything else → 401); project and board
  search with `query`/`name` filtering; board configuration; active sprint; `search/jql` with a **small JQL subset**
  sufficient for §2.8 (`filter =`, `sprint in`, `sprint is EMPTY`, `openSprints()`, `status in`, `id in`, `updated >=`,
  `AND/OR/()`, `ORDER BY Rank`), `fields` projection, `nextPageToken` paging (opaque base64 offsets), `approximate-count`;
  `changelog/bulkfetch` with `fieldIds`; issue comments; dev-status summary/detail (instance type `GitHub`);
  assignable user search; `jql/parse` (accepts anything the subset parses).
- **Fault injection** (`jiratest.Options`, also env `CUISLE_FAKE_FAULTS="429:every=7,retry=1;latency=80ms;devstatus=404"`):
  `Latency`, `Throttle{Every, RetryAfter}`, `FailStatus{PathPrefix, Code, Count}`, `DevStatus: "ok"|"404"|"403"|"garbage"`,
  `PageSize` (force tiny pages), `Unauthorized` (flip to 401 mid-run), `Mutate(func(*Dataset))` between syncs (move an
  issue, add a commit) for incremental tests.
- **Tests that must exist** (integration, `internal/sync`): first full sync populates all tables; incremental with no
  changes makes exactly the budgeted requests and hydrates 0 issues; issue removed from sprint disappears; status change
  re-hydrates only that issue; 429 storm respects `Retry-After` and succeeds; cancel mid-phase leaves DB byte-identical
  (compare `standup.rev` and table checksums); dev-status 404 → run still `ok`, with warning `dev_activity_unavailable` and no commit rows;
  401 mid-run → `auth_invalid`, no apply; golden `StandupView` JSON for the MOB standup matches
  `testdata/view_mob.golden.json` (this is the bridge to the mockups).

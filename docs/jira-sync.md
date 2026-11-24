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
| `Hydrate` | §2.8 hydrate field set (`*all`) + §2.9 changelog + §2.10 comments with bodies, mapped to `model.IssueDetail` incl. `IssueContent` |
| `Fields` | §2.2 field registry + kind mapping |
| `FetchMedia` | §2.14 |
| `GetIssue` | §2.15 refresh (single-issue `Hydrate`) |
| `Transitions` / `Transition` / `Assign` / `SetFlag` / `AddComment` | §2.15 |
| `DevActivity` | §2.11 |
| `Capabilities` | `{HasSprints, HasBacklogRank, HasFlagging, HasDevActivity, HasExtraFilter, HasAssigneeHistory, Content: true; Writes: {Transition, Assign, Flag, Comment: true}; FilterLanguage: "JQL"}` (`HasDevActivity` flips to false when the probe fails) |

## 1. Client basics (`internal/source/jira`)

- Base URL: `connection.base_url` = `https://<site>.atlassian.net` (https only; no trailing slash). Classic (unscoped)
  API tokens only in milestone 1.
- Auth: `Authorization: Basic base64(email ":" token)`; token read from the keychain per sync run (held in memory for the
  run, never logged). Headers: `Accept: application/json`, `Content-Type: application/json` on POST,
  `User-Agent: Cuisle/<version> (<os>)`.
- Timeouts: 10 s connect, 30 s per request, 5 min per sync run. HTTP/2, keep-alive, one shared `http.Client`.
- Concurrency: one global semaphore of **4** in-flight requests across all runs (media at most 2); every request goes
  through the per-connection limiter of §3.1 (token bucket 8 requests/s, burst 16; WP-T0).
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
(`sprintFieldId`, `rankFieldId`, `flaggedFieldId`, `devSummaryFieldId`; written below in their snake_case shorthand):
- `sprint_field_id`: `schema.custom == "com.pyxis.greenhopper.jira:gh-sprint"`
- `rank_field_id`: `schema.custom == "com.pyxis.greenhopper.jira:gh-lexo-rank"` (prefer the id from board config, §2.5)
- `flagged_field_id`: custom field with `name == "Flagged"` (untranslated name; fallback `clauseNames` contains `Flagged`)

- `dev_summary_field_id`: custom field whose `schema.custom` ends with `:devsummarycf` (the "Development" field). The scan
  requests it so the dev fingerprint comes with the membership scan (§2.8, §2.11). `devSummaryFieldChecked: true` is
  stored with it, so "no such field on this site" is remembered and not looked up again. A connection saved before this
  field existed has no marker: its first scan reads `/field` once (the only extra request of the upgrade).

Missing Flagged field → blocked detection uses status names only (warning `no_flagged_field`).

**Field registry (`Source.Fields` → `field_def`, ADR 0015).** Every entry of the same response becomes a `field_def` row
(`field_id = id`, `name`, `custom`, `custom_type = schema.custom`, `schema_json = schema`), with `kind` from the first
matching rule:

| `schema.type` | `schema.items` / `schema.custom` (suffix) / `schema.system` | Kind |
|---|---|---|
| `string` | custom `:textarea`; system `description`, `environment` | `paragraph` (value is ADF) |
| `string` | custom `:url` | `url` |
| `string` | anything else (`:textfield`, `summary`, epic name, …) | `text` |
| `number` | (story points, `:float`, time in seconds) | `number` |
| `date` | | `date` |
| `datetime` | | `datetime` |
| `option` | `:select`, `:radiobuttons` | `option` |
| `array` | items `option` (`:multiselect`, `:multicheckboxes`) | `options` |
| `option-with-child` | `:cascadingselect` | `cascade` |
| `user` | | `user` |
| `array` | items `user` (`:multiuserpicker`, people, request participants) | `users` |
| `group` | | `group` |
| `array` | items `group` | `groups` |
| `version` | | `version` |
| `array` | items `version` (fix versions, affects versions) | `versions` |
| `array` | items `component` | `components` |
| `array` | items `string`, system `labels` or custom `:labels` | `labels` |
| `project` | | `project` |
| `team` or custom `com.atlassian.teams:rm-teams-custom-field-team` / `com.atlassian.jira.plugin.system.customfieldtypes:atlassian-team` | | `team` |
| `priority`, `resolution`, `issuetype`, `status`, `securitylevel` | (objects with `name`) | `option` |
| anything else (Tempo account, Assets/Insight objects, `sd-*` service-desk types, `any`, app-specific) | | `unknown` (raw JSON kept; shown mono) |

Value decoding by kind: `option` → `.value` or `.name`; `options`/`versions`/`components` → each `.value`/`.name`;
`cascade` → `[.value, .child.value]`; `user(s)` → `.accountId` (+ person upsert); `group(s)` → `.name`; `team` → `.name`
(fallback `.title`); `paragraph` → the ADF object; `unknown` → the raw value (strings unquoted). `null`, `""`, `[]` and
objects with no displayable member mean **not set** (no `issue_field_value` row).
Fields excluded from `issue_field_value` because they are modelled elsewhere or are noise: `summary`, `status`,
`assignee`, `issuetype`, `created`, `updated`, `resolutiondate`, `parent`, `comment`, `attachment`, `issuelinks`,
`subtasks`, `description`, `environment`, `worklog`, `watches`/`votes` (only `watches.watchCount` is read), the sprint/rank/flagged
fields, `lastViewed`, `statuscategorychangedate`, `workratio`, `progress`, `aggregateprogress`, and development-summary
custom fields (`:devsummarycf`). Time tracking (`timetracking.{originalEstimateSeconds, remainingEstimateSeconds, timeSpentSeconds}`)
is stored as one `unknown`-kind value with a fixed id `timetracking` that the view renders as "14h of 24h".

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
Deliberately **not cached** between syncs (decided 2026-11-24): it is one request, it is the only signal that promotes a
run to full when the board's columns change, and it supplies `filterId` and `subQuery` to `Plan`; a day-old copy would
make a column change invisible to a Resync.
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

- **Scan** (cheap, all members): `fields: ["updated"]`, plus the Development summary field (§2.2) for `board`-role queries
  when the site has one, `maxResults: 1000` (server may return fewer; follow tokens). Returns
  `issues[].{id, key, fields.updated, fields.<dev field>}` in JQL order. The dev field is a string that embeds JSON
  (§2.11); it adds no request, only bytes. `backlog`-role scans omit it (backlog items are never dev targets).
- **Hydrate** (changed/new issues only), `maxResults: 50`, JQL `id in (<≤50 ids>)` (follow `nextPageToken` if the server
  returns fewer): `fields: ["*all", "-comment", "-worklog"]` — everything, including all custom fields, because the ticket
  modal shows them (ADR 0015); comments come from §2.10.
  Read for the record: `summary`; `issuetype.{id,name,subtask,hierarchyLevel}`; `status.{id,name,statusCategory.key}`;
  `assignee.{accountId,displayName,active,accountType}` (may be null); `created`, `updated`, `resolutiondate`;
  `parent.{id,key,fields.summary,fields.issuetype.id}`; sprint field `[].{id,name,state,startDate,endDate,completeDate,boardId}`;
  rank field (string); flagged field (non-empty array ⇒ flagged).
  Read for content: `description` and `environment` (ADF objects, stored verbatim as JSON text);
  `attachment[].{id, filename, mimeType, size, created, author.accountId}`;
  `issuelinks[].{id, type.{name,inward,outward}, inwardIssue|outwardIssue.{id,key,fields.{summary,status.id,issuetype.id}}}`;
  `subtasks[].{id,key,fields.{summary,status.id,issuetype.id}}` (neither payload carries an assignee, so a link or subtask
  row's initials are resolved locally at read time from the target's current `issue.assignee_account_id` when the target
  is a synced issue, and are empty otherwise; no extra request); `watches.watchCount`; `reporter`, `priority`, `labels`,
  `components`, `fixVersions`, `duedate`, `timetracking` and **every other key** decoded through the field registry (§2.2)
  into `model.FieldValue`s. Unknown keys (no `field_def`) are stored as kind `unknown` and trigger a registry refresh on
  the next sync.

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

### 2.10 Comments — `GET /rest/api/3/issue/{id}/comment`
Params: `orderBy=created`, `maxResults=100`, `startAt=n`; page until `startAt + len ≥ total`. Called **only for hydrated
issues** (new, or `updated` changed in the scan diff — adding/editing a comment bumps `updated`), always the complete list.
Read `comments[].{id, created, updated, author.{accountId,displayName,accountType}, body, visibility.{type,value}}`.
Stored per comment: timestamps, author, **`body` as ADF JSON** (`comment.body_adf`), visibility (ADR 0015).
Upsert by id; a stored comment absent from a complete fetch gets `deleted_at`. Bodies are never logged or exported.
Comments by `accountType == "app"` authors (automation/bots) are stored and shown, but excluded from pulse marks.
(This replaces the earlier "embedded comment field + top-up" approach; the search no longer requests `comment`.)

### 2.11 Development information (internal API, best effort)
Not a public API: no stability guarantee. All failures degrade to warnings.

0. **Fingerprint from the scan (the normal path, since 2026-11-24).** The Development summary field (§2.2) in the scan
   response is a string embedding JSON: `{pullrequest={...}, repository={...}, json={"cachedValue":{"errors":[],"summary":
   {"pullrequest":{"overall":{…},"byInstanceType":{…}},"repository":{…}}},"isStale":false}}`. Its `summary` has the same
   shape as the endpoint's response in step 1, so the **same** `dev_fingerprint` (formula below) and the same instance-type
   keys are read from it. It is usable unless the value is not a string, has no `json=` part or one that does not parse,
   `errors` is non-empty, `isStale` is true, or `cachedValue.summary` is missing (then the fingerprint is *unknown* and
   step 1 runs for that issue). A field that is absent from the issue's `fields` is also unknown. `null`, `""` and `"{}"`
   mean the issue has no development information and give the zero-activity fingerprint (`0||0||`), exactly what the
   endpoint's all-zero summary hashes to. The adapter hands the fingerprint to the core in `ScanItem.DevFingerprint` (an
   opaque string) and remembers the instance-type keys for step 2.
   - **Equal to the stored `dev_fingerprint`:** the core makes **no dev-status request** for the target (it is still marked
     checked, so `dev_checked_at` moves).
   - **Different:** step 2 runs directly, with no summary request.
   - **Unknown:** step 1 and then step 2, as before.
   `lastUpdated` is expected to be formatted the same way in both places (unverified against a real site); if a site differs, the first run after
   the upgrade costs one detail round per target and then settles, because the stored value is the field's.
1. **Summary** (fallback: unknown scan fingerprint) — `GET /rest/dev-status/latest/issue/summary?issueId={id}`
   Read `summary.repository.overall.{count,lastUpdated}`, `summary.pullrequest.overall.{count,lastUpdated,state,stateCount}`,
   and the keys of `summary.repository.byInstanceType` / `summary.pullrequest.byInstanceType` (e.g. `GitHub`, `bitbucket`,
   `GitLab`; passed back verbatim as `applicationType`). `dev_fingerprint = sha1(repository.count|repository.lastUpdated|pullrequest.count|pullrequest.lastUpdated|pullrequest.state)`.
   Unchanged fingerprint → skip detail.
2. **Detail** (only when the fingerprint changed), per instance type present:
   - `GET /rest/dev-status/latest/issue/detail?issueId={id}&applicationType={type}&dataType=repository`
     → `detail[].repositories[].{name, commits[].{id, authorTimestamp, merge}}` → upsert `dev_commit`.
   - `GET /rest/dev-status/latest/issue/detail?issueId={id}&applicationType={type}&dataType=pullrequest`
     → `detail[].pullRequests[].{id, name, status, lastUpdate, repositoryId, repositoryName, source.branch}` → insert
     `dev_pr_event` `(pr_key, status, at=lastUpdate)` if that triple is new; `name` is stored as `title` and
     `source.branch` as `branch` (both nullable; a re-observation of the same triple fills NULLs or a renamed title and
     never blanks them). The ticket modal's Branch row is the most recently updated PR's source branch; commits carry no
     branch in these responses, and no `dataType=branch` request is made. A title change alone does not change the
     summary fingerprint, so it is picked up the next time the PR's state or update time changes.

**Dev targets** (the only issues checked each sync; issue `updated` does not change when commits arrive, so these are
re-checked every sync, which costs nothing while the scan carries their fingerprint): members with role `board` whose lane is `prog`, `review` or `qa`; plus members that changed
status within the last 28 days and have `dev_checked_at` older than their last status change or NULL (covers freshly Done
and bounced-back tickets once). Backlog items are never queried.

Degradation:
- Probe = the first dev-status request the run actually makes: a summary call, or, when the scan supplied fingerprints,
  the first detail call (a changed target). 401/403/404, or a non-JSON body → `connection.dev_activity_state='unavailable'`,
  skip the phase, warning `dev_activity_unavailable`; re-probe on the next full sync or after 24 h. A run that makes no
  dev-status request at all (every target unchanged per the scan) proves nothing, so it leaves the stored state alone.
  While the state is `unavailable` the scan fingerprints are ignored, so the re-probe is a real summary request.
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
| Comments (with bodies) | `GET /rest/api/3/issue/{id}/comment` | hydrated issues |
| Attachment bytes / thumbnail | `GET /rest/api/3/attachment/content/{id}`, `GET /rest/api/3/attachment/thumbnail/{id}` | media phase; explicit open on cache miss |
| Single issue refresh | `GET /rest/api/3/issue/{id}?fields=*all,-comment,-worklog` | after each light write |
| Transitions | `GET /rest/api/3/issue/{id}/transitions?expand=transitions.fields` | Status combobox opened |
| Transition / assign / flag / comment | `POST …/transitions`, `PUT …/assignee`, `PUT /rest/api/3/issue/{id}`, `POST …/comment` | explicit user action |
| Dev summary | `GET /rest/dev-status/latest/issue/summary` | only a dev target whose scan fingerprint is unknown (field stale, absent or unparseable) |
| Dev detail | `GET /rest/dev-status/latest/issue/detail` | dev targets whose fingerprint changed |
| Users | `GET /rest/api/3/user/assignable/search` | typing |

### 2.14 Attachments and media (`Source.FetchMedia`, ADR 0015)

- **Original**: `GET /rest/api/3/attachment/content/{id}` with redirects **not** auto-followed. Jira answers `303` with a
  `Location` on the media service; the adapter (a) extracts the media file id from that URL
  (`…/file/<uuid>/binary…`) → `attachment.media_file_id`, then (b) follows the redirect **without** the `Authorization`
  header (the URL is pre-signed) and streams the body to `media.Cache.Put`. A `200` with a body (no redirect) is also accepted.
- **Thumbnail fallback**: `GET /rest/api/3/attachment/thumbnail/{id}?width=320&height=200&fallbackToDefault=false` — used
  only when the original is over 25 MB or cannot be decoded locally; otherwise thumbnails are generated by `internal/media`.
- **What is downloaded during sync**: image attachments (`mimeType` png/jpeg/gif/webp/bmp, ≤ 25 MB) of hydrated issues that
  have no thumbnail yet. Non-image files are metadata only and download on click (`TicketService.OpenAttachment`).
  If the cache is at its cap the original is still fetched once to make the thumbnail, then evicted by LRU.
- **Media-node → attachment mapping** (ADF `media` nodes carry `attrs.id` = media file id, `attrs.alt`, sometimes
  `width/height`; there is no documented join to attachment ids). Resolution order, performed by `adf.Resolver` at view time:
  1. `attachment.media_file_id == attrs.id` (captured from the redirect above — exact).
  2. For attachments whose media id is still unknown (non-images never downloaded): one header-only request
     `GET /attachment/content/{id}` (redirect not followed) per such attachment **during the media phase**, only for issues
     whose ADF references an unresolved media id.
  3. `attrs.alt` equals the attachment `name` (unique within the issue).
  4. Position: n-th unresolved media node ↔ n-th remaining attachment by `created` (only when the counts match).
  5. Unresolved → node rendered as "attachment unavailable ↗" (opens the issue); warning `media_unresolved`.
  External media (`attrs.type == "external"`, a URL) is rendered as a link card, never fetched.
- **When**: the media phase runs **after** the record apply (§4.3 phase 9) in a pool of 2 downloads, cancellable and
  resumable (it simply picks attachments with `is_image=1 AND thumb_path IS NULL AND removed_at IS NULL`). Each landed
  image emits `media:ready`. The modal shows a framed placeholder with the stored dimensions until then. Failures set
  `attachment.fetch_error` and are retried on the next sync (not in a loop). A sync's status is not affected by media.

### 2.15 Light writes (`Capabilities.Writes`; explicit user actions only)

| Action | Request | Notes |
|---|---|---|
| List transitions | `GET /rest/api/3/issue/{id}/transitions?expand=transitions.fields` | Read `transitions[].{id, name, to.{id,name}, hasScreen, isAvailable, fields{<id>: {required, hasDefaultValue}}}`. Drop `isAvailable == false`. `NeedsScreen = hasScreen && any field with required && !hasDefaultValue` — a screen with only optional fields is transitioned directly. Cached in `transition_cache`. |
| Transition | `POST /rest/api/3/issue/{id}/transitions` body `{"transition":{"id":"31"}}` → `204` | `NeedsScreen` transitions are never posted (UI opens the browser; a direct call returns `transition_needs_screen`). |
| Assign | `PUT /rest/api/3/issue/{id}/assignee` body `{"accountId":"5b10…"}`; unassign `{"accountId":null}` → `204` | |
| Flag / unflag | `PUT /rest/api/3/issue/{id}` body `{"fields":{"<flagged_field_id>":[{"value":"Impediment"}]}}`; unflag `{"fields":{"<flagged_field_id>":null}}` → `204` | No Flagged field discovered → `Capabilities.Writes.Flag = false`. |
| Add comment | `POST /rest/api/3/issue/{id}/comment` body `{"body": <ADF>}` → `201 {id,…}` | ADF built by `adf.FromPlainText`: blank-line-separated paragraphs, single newlines → `hardBreak`, mention ranges → `{"type":"mention","attrs":{"id":"<accountId>","text":"@Name"}}`. No visibility restriction is ever set by Cuisle. |
| Refresh | `GET /rest/api/3/issue/{id}?fields=*all,-comment,-worklog` + `POST /changelog/bulkfetch` for that id + §2.10 comments | = `Source.GetIssue`; result applied by `sync.ApplyIssue` (§4.8). |

Error mapping for writes (never retried automatically; the 429/5xx retry policy of §3 is **not** applied to POST/PUT writes —
one attempt only, so an action is never sent twice):

| Response | Code | Message shown |
|---|---|---|
| `400` with `errorMessages[]` / `errors{}` | `write_rejected` | Jira's first message verbatim (e.g. a workflow validator's text) |
| `400` on a transition complaining about a required field / screen | `transition_needs_screen` | "This transition needs more information — open in Jira" |
| `401` | `auth_invalid` | "Jira rejected the token — Reconnect" (also flips `connection:auth`) |
| `403` | `permission_denied` | "You don't have permission to do that in Jira" |
| `404` | `not_found` | "This issue no longer exists or you can't see it" |
| `409` | `conflict` | "The issue changed in Jira — reload and try again" (the refresh still runs) |
| `429` | `rate_limited` | "Jira is busy — try again in a moment" (`Retry-After` shown) |
| network failure before a response | `offline` | "Can't reach Jira" |
| network failure **after** the request was sent | `timeout` | "Jira didn't answer — checking…" then a refresh decides whether the change landed |

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

### 3.1 Throttling: one limiter per connection (ADR 0015)
Jira Cloud rate-limits API-token traffic with **per-second burst limits** and **per-issue write limits**. Over a limit,
it answers `429` with `Retry-After`. Responses may carry `X-RateLimit-Limit`, `X-RateLimit-Remaining`,
`X-RateLimit-Reset`, `X-RateLimit-NearLimit: true` (under 20% left), `RateLimit-Reason`, and the structured
`RateLimit` / `RateLimit-Policy` headers (`"jira-burst-based";r=90;t=1`). The newer points-based hourly quotas apply to
Connect/Forge apps, not API-token traffic. Cuisle still parses those headers if present, so the same code works if that
changes. Headers are often absent until you are near a limit, so Cuisle must be polite **proactively**, not only after
a 429. (Source: developer.atlassian.com/cloud/jira/platform/rate-limiting, checked 2026-11-23.)

**`internal/source/jira/limiter`** wraps the HTTP client. Every request goes through it: sync, content, media,
transitions, writes, ticket refresh, combobox search, Connect. There is one limiter per connection (site + account),
shared by all standups and all goroutines.

| Mechanism | Default | Rule |
|---|---|---|
| Token bucket (sustained rate) | **8 req/s**, burst 16 | Hidden setting `jira.max_rps` (1–20); there is no UI for it, and Diagnostics shows it. |
| Concurrency | 4 in flight (2 for media) | As in §1; media downloads also count against the bucket. |
| Priority lanes | interactive > sync > media | *Interactive:* writes, transitions, ticket Refresh, combobox search, Connect, validate. *Sync:* plan, scan, hydrate, comments, dev-status. *Media:* attachment and thumbnail downloads. A waiting interactive request takes the next token; bulk work can't starve it. Media runs only when no sync request is waiting. |
| Adaptive slow-down (AIMD) | — | If a response has `X-RateLimit-NearLimit: true`, or `Remaining/Limit < 0.2` (from either header family), halve the rate, with a floor of 1 req/s. Recover by +1 req/s every 30 s with no near-limit signal, up to the configured max. |
| 429 | — | Close the **global gate** for this connection (all lanes) until `Retry-After` (fallback: `X-RateLimit-Reset` or `RateLimit` `t=`; else 5 s), plus 0–500 ms jitter. Halve the rate. Sync requests retry per §3; **writes do not retry** (ADR 0015): they fail `rate_limited` with `retryInMs`, and the UI says "Jira is rate-limiting · try again in 6s". |
| Per-issue writes | 1 in flight per issue | Writes and Refresh for the same issue serialise (`conflict` if one is already running). This respects Jira's per-issue write limit. |
| Ticket Refresh cooldown | 10 s per issue | Enforced in `TicketService` before the limiter; it returns `cooldown{retryInMs}` with no request. |
| Manual Resync | coalesced | `R` while a sync runs is a no-op that focuses the progress. Sync-on-open stays at most once per stale window (§4.1). |

**Request economy** (keep syncs cheap; this is what keeps a 5-standup user far below the limits):
- Scan-and-diff hydrates only changed issues (§4.2). Comments are fetched only for issues whose `updated` changed.
  Attachments are downloaded once (by id) and thumbnails never re-fetched. Dev-status uses fingerprints (§2.11), taken
  from the Development field of the scan response, so an unchanged dev target costs no request at all.
- Field discovery (`/field`) runs once per connection per day, or on a full sync, not on every run.
- Standups that share a board share one scan inside a run (the same board and filter are planned once).
- **Budget check:** `sync_run.requests` and `sync_run.throttled` are recorded per run. Decided by Chris on 2026-11-23,
  after measuring v1 at 159 requests for a full sync and 38 for an incremental, almost all of it dev-status:
  - **Issue and content traffic** (everything except `/rest/dev-status/**`):
    - A full sync of the MOB fixture board (55 issues) must stay at or under **250 requests**.
    - An incremental sync with 3 changed issues must stay at or under **50**.
  - **Dev-status has its own cap:**
    - At most 1 summary request per active dev target per sync, and since 2026-11-24 only for a target whose scan
      fingerprint is unknown (stale or absent Development field).
    - Detail requests only for targets whose fingerprint changed (§2.11), at most 2 per such target, with no summary
      before them when the scan supplied the fingerprint.
  - **No-change incremental sync: at most 8 requests in total** (Chris, 2026-11-24, "good stewards"): `myself`, board
    configuration, sprints, ≤ 3 scans, count, plus one summary per target with an unknown fingerprint.
  - All of these are asserted against jiratest's request log (`internal/sync/budget_test.go`).
  - **Measured on the fake fixture board (55 issues, 44 dev targets on a first sync, 29 on later runs):**

    | Run | Before | After |
    |---|---|---|
    | Full (first) sync | 208 = 79 non-dev + 44 summary + 85 detail | 165 = 79 + 1 + 85 |
    | Incremental, 3 changed issues | 40 = 11 non-dev + 29 summary | 12 = 11 + 1 |
    | Incremental, nothing changed | 35 = 6 non-dev + 29 summary | 7 = 6 + 1 (6 when no field is stale) |

    The one remaining summary is the fake's deliberately stale Development field (`jiratest.StaleDevKey`), which
    exercises the fallback.
- **Not cached, on purpose:** `myself` (the auth check; one request that decides `auth_invalid` before anything else) and
  the board configuration (§2.5; the column-change signal).

**Visibility:**
- The limiter emits `sync:throttled {resumeAt, reason}`. The header shows `Jira is rate-limiting · resuming in 12s`
  (ux-spec §14.13).
- Diagnostics keep the last hour's requests per lane, the current rate, and throttle events. `--debug` logs every
  limiter decision.

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
1. config      [parallel] board configuration · active sprints · (full: fields → field_def, statuses)
               → recompute column map + status lanes in memory; build queries (§2.8)
2. scan        [parallel per query] search/jql with fields=["updated", <development field>]   (§2.8)
               → ordered id lists per role; total = distinct ids
               diff vs cache:  toHydrate = new ids ∪ ids whose updated ≠ issue.updated_at   (full: all ids)
                               toClose   = current members of this standup not in the scan (span closed; rows kept)
3. issues      hydrate toHydrate in batches of 50 ids (`id in (...)`, fields=*all), 4 batches in flight
               → record fields + content (description, field values, links, subtasks, attachment metadata)
4. changelog   bulkfetch for toHydrate (batches of 500)
5. content     full comments with bodies for toHydrate (§2.10), concurrency 4            [progress phase "content"]
6. dev         dev targets (§2.11): scan fingerprint == stored → nothing; changed → details; unknown → summary → changed? → details; concurrency 4
7. apply       single write transaction (§4.6); then trim operational rows (separate tx)
8. sinks       enqueue + deliver for sinks with send_on='after_sync' (ok/partial runs only; architecture.md §2.3)
9. media       image downloads + thumbnails for attachments without one (§2.14), pool of 2   [progress phase "media"]
               runs after sync:finished has been emitted; reported through sync:progress {phase:"media"} and media:ready;
               cancellable; never changes the run's status
```

Ordering guarantees: phase N+1 starts only when N finished, except 4–6 which run concurrently with each other once 3 is
complete. `sync:progress.phase` ∈ `config|scan|issues|changelog|content|dev|apply|media` (`content` replaces the former
`comments`). Request budget, incremental with no changes: 1 (`myself`) + 2 (config, sprints) + ≤ 3 scans + 1 (count)
+ 1 summary per dev target whose Development field is stale or absent (normally none), so about 6 (§3.1); content is
fetched only for issues in the diff. Per changed issue: +1 hydrate
share (1 request per 50), +1 changelog share, +⌈comments/100⌉ (min 1) comment requests, +1 per new image attachment.
First full sync of a 300-issue board: ≈ 6 hydrate + 1 changelog + ≈ 300 comment requests + dev targets + images; at 4
concurrent requests ≈ 30–60 s before images (budget in architecture.md §6.1 updated to ≤ 60 s).

Users: assignees, reporters, comment authors and user-field values seen in hydrate responses are collected into the
snapshot (`person` upserts); no separate user calls.

### 4.4 Cancellation

`SyncService.Cancel(standupID)` cancels the run's context. In-flight requests abort; nothing has been written (data is
only in memory), so there is nothing to roll back. `sync_run.status='cancelled'`, event `sync:finished {status:"cancelled"}`.
App quit cancels all runs the same way. Switching standups or screens does **not** cancel.

### 4.5 Partial results

If phases 0–4 succeed and only content (comments) or dev had per-issue failures, the run applies and finishes `partial` with
warnings (existing rows for the failed issues are kept). Any failure in phases 0–4 aborts the run with **no** apply:
the UI keeps showing the previous snapshot with the previous "synced" time.

### 4.6 Atomic apply (the UI never sees half a sync)

One `BEGIN IMMEDIATE` on the writer connection (`T` = the run's `started_at`):

1. Upsert `board*`, `sprint`, `status`, `issue_type`, `person`; update `connection.source_meta` /
   `dev_activity_state`.
2. Merge `standup_column_map` (auto-map new columns), rebuild `standup_status_lane`; update `standup` board snapshot
   (`board_name`, `board_type`, `board_meta`).
3. Upsert hydrated `issue` rows (by `connection_id, source_id`; skipped when the stored `updated_at` is newer); for each,
   **upsert** `status_change`, `assignee_change`, `flag_change`, `comment` (with body) by natural key — never delete — and,
   when `content_hash` changed, replace `issue_content`, `issue_field_value`, `issue_link` and upsert `attachment` metadata
   (`removed_at` for vanished ones). Emit `ticket:updated {cause:"sync"}` per changed issue after commit. Set `last_seen_at = T` on every scanned issue.
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
| Offline at launch | Cached view shown as always. Sync-on-open attempt fails `offline`: **no banner** — the header sync status reads "offline · data from 3h ago" (alert text) until the next successful sync. No modal, no retry loop. |
| Offline on manual Resync | Dismissible banner "Offline — showing data from 3h ago", plus the same header state. |
| Token missing (`auth_missing`) | Banner "Connect to Jira to sync" → Connect screen. Cached standups remain usable. |
| Token rejected (`auth_invalid`) | Banner "Jira rejected the token — Reconnect"; all sync triggers are suppressed until `Connect` succeeds; Configure comboboxes fall back to cached options and show "Offline — showing cached". |
| Board/filter forbidden or gone | Run fails; tab shows a warning dot; Configure › Source shows the message on the Board field. |
| Extra JQL invalid | Run fails fast `filter_invalid`; Configure shows Jira's parse error under the field. |
| Rate limited | Header `Syncing… waiting for Jira (12 s)` during gate; fails `rate_limited` after 120 s cumulative. |

### 4.8 Light writes and the one-issue apply (ADR 0015)

`TicketService.{Transition,Assign,SetFlag,AddComment}` → per-issue mutex → `Source.<write>` (one attempt, §2.15) →
`Source.GetIssue` → `sync.ApplyIssue(detail)`:

- One `BEGIN IMMEDIATE`: step 3 of §4.6 for that single issue (issue row, **merged** change tables — the status /
  assignee / flag change made by the write is recorded from Jira's own changelog entry, with Jira's timestamp and
  history id), comments, content tables, attachment rows; delete its `transition_cache` row if the status changed;
  bump `rev` of every standup that has the issue as a current member. Membership, spans, config history and sync state
  are **not** touched (a transition to Done does not remove the issue from the board until a sync says so).
- Emit `ticket:updated {cause:"write"}` and `data:changed {what:"view"}`; enqueue nothing for sinks (a write is not a sync).
- If `GetIssue` fails after the write succeeded: apply the known delta to the `issue` row only (new `status_id` /
  `assignee_account_id` / `flagged`, no change-table rows), return the detail with warning `refresh_failed`; the next sync
  hydrates the issue (its `updated` differs) and merges the real changelog entries.
- Concurrency with a running sync: both go through the single writer; whichever applies second **skips issue rows whose
  incoming `updated` is older than the stored `updated_at`** (change/comment rows are still merged — they are append-only).
- Writes are refused locally, before any request, when `connection.auth_state != 'ok'` (`auth_missing`/`auth_invalid`) or
  the capability is absent (`unsupported`).

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
- **Development summary field (§2.2, §2.11)**: `/field` lists "Development" (`customfield_10000`, `…:devsummarycf`) and every
  issue carries its value in search results, built from the same data as the dev-status summary endpoint (so the
  fingerprints agree by construction); `"{}"` for an issue with no commits or PRs. `MOB-1421` is served with
  `isStale: true` (`jiratest.StaleDevKey`, `DSIssue.DevStale`; clear it through `Mutate`) to exercise the summary fallback,
  and `Options.NoDevField` removes the field from the site entirely.
- **Content and writes (ADR 0015)**: every fixture issue carries a `description` (one fixture issue, `MOB-1421`, contains
  **every ADF node of ux-spec §14.4** plus one unknown node), `comments[]` with ADF bodies (one with
  `visibility: {type:"role", value:"Developers"}`, one edited), `attachments[]` (three images served from
  `testdata/media/*.png|jpg` — copied from the round-6 design assets — plus one PDF), custom fields covering **every
  `FieldValue.Kind`** incl. one unknown app field and several empty ones, `issuelinks`, `subtasks`. Endpoints: `*all` field
  projection, `GET /issue/{id}`, paged `GET /issue/{id}/comment`, `GET /attachment/content/{id}` (303 to a local
  `/media/file/<uuid>/binary` URL, exercising the media-id capture) and `/attachment/thumbnail/{id}`,
  `GET/POST /issue/{id}/transitions` (the fixture workflow includes one `hasScreen` transition with a required field),
  `PUT /issue/{id}/assignee`, `PUT /issue/{id}` (Flagged), `POST /issue/{id}/comment`. Writes mutate the dataset **and
  append changelog entries**, so the refresh sees real history. The request log records method + path for the
  no-network-on-open assertions.
- **Fault injection** (`jiratest.Options`, also env `CUISLE_FAKE_FAULTS="429:every=7,retry=1;latency=80ms;devstatus=404"`):
  `Latency`, `Throttle{Every, RetryAfter}`, `FailStatus{PathPrefix, Code, Count}`, `DevStatus: "ok"|"404"|"403"|"garbage"`,
  `PageSize` (force tiny pages), `Unauthorized` (flip to 401 mid-run), `Writes: "ok"|"400"|"403"|"409"|"timeout-after-send"` (per action), `RefreshFails` (GetIssue 500 after a successful write),
  `Media: "ok"|"404"|"slow"|"no-redirect"`, `StaleChangelog` (write succeeded but changelog lags), `Mutate(func(*Dataset))` between syncs (move an
  issue, add a commit) for incremental tests.
- **Tests that must exist** (integration, `internal/sync`): first full sync populates all tables; incremental with no
  changes makes exactly the budgeted requests and hydrates 0 issues; issue removed from sprint disappears; status change
  re-hydrates only that issue; 429 storm respects `Retry-After` and succeeds; cancel mid-phase leaves DB byte-identical
  (compare `standup.rev` and table checksums); dev-status 404 → run still `ok`, with warning `dev_activity_unavailable` and no commit rows;
  401 mid-run → `auth_invalid`, no apply; golden `StandupView` JSON for the MOB standup matches
  `testdata/view_mob.golden.json` (this is the bridge to the mockups).

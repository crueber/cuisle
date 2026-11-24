# WP-T4: Jira content (fields, *all hydrate, comments, media, GetIssue)

Spec: `docs/jira-sync.md` §2.2, §2.8–2.10, §2.14, §2.15, §6; ADR 0015; `docs/wp-notes/F12-freeze.md` (WP-T4 notes).

## What landed
- `fields.go`: registry (`GET /rest/api/3/field`) to `model.FieldDef` with the jira-sync §2.2 kind table (first match wins).
  - One registry per connection, process-wide. Key: normalised base URL plus the policy pointer. Adding the policy is
    the same reasoning as T0 decision 2: a reused httptest port or a test policy never inherits another connection's
    registry. In production the policy is the process default, so the key is effectively the base URL.
  - Refreshed when older than 24 h (`registryTTL`), or when the Adapter's `meta.FieldsFetchedAt` is zero (New zeroes it on
    `Options.Full`). The first fetch on an Adapter sets `meta.FieldsFetchedAt`.
  - Failures are not cached. No `component` kind is produced (as pinned by the tests).
- `hydrate.go`: `Hydrate` = registry, then search by id in batches of 50 with `["*all","-comment","-worklog"]`, then
  `complete` (changelog bulkfetch in 500s, then the full comment list per issue, then `recordTypes`).
- `content.go`: `GetIssue` = `GET /issue/{id}?fields=*all,-comment,-worklog`, then the same `mapIssue` and `complete`
  as Hydrate (so the DeepEqual holds). An issue without id or key is not_found. `contentCapability()` returns true.
- `hydrate_values.go`: per-kind decoding. A wrong JSON shape falls back to kind unknown (raw kept). An empty or
  not-displayable value is not set. User values upsert People once, without e-mail.
- `hydrate_content.go`: description, environment, attachments, links, subtasks, parent, watchers (-1 unknown), fields
  sorted by id (sprint/rank/flagged ids, `:devsummarycf` and the Issue-modelled keys excluded), and a SHA-256 of the
  content. Comments are not in the hash.
- `hydrate_comments.go`: complete comment list, `orderBy=created`, pages of 100 advancing `startAt` by what was
  received, stopping on an empty page or at the total. Bodies are stored as ADF. Visibility is kept. `updated` falls back
  to `created`. Replaces the 35-day top-up.
- `media.go`: `FetchMedia`. The original is fetched without following redirects (copy of `c.http` with
  `ErrUseLastResponse`, so the limiter transport still applies). The uuid comes from the Location, and the pre-signed hop
  has no Authorization. 404, and 403 on the pre-signed hop, map to media_unavailable. A 5xx is retried within the client
  budget. A 429 closes the gate when within MaxGate. Transport errors are stripped of the URL before mapping, so the
  signed query never reaches an error.
- `endpoints_ref.go`: `WithInteractive` on SearchSpaces, SearchBoards, SearchUsers and ValidateFilter.
- `jiratest/content.go` (new), with `server.go`, `server_api.go` and `jql_handlers.go` edited: overlay (embedded
  `content.json` and `testdata/media`), the `*all` projection, comment bodies and visibility, attachment 303, thumbnail
  (stdlib image, nearest neighbour), `/media/file/<uuid>/binary` (no auth), and `GET /issue/{id}`. The auth wall skips
  `/media/file/` only.

## Deliberate deviations and notes
- `Test` is NOT marked Interactive: sync calls `Test` (`internal/sync/orchestrator_run.go`), and
  `TestLimiter_NoSyncRequestBypassesIt` requires every sync request to stay in the sync lane.
- `GetIssue` is not marked; the caller (TicketService.Refresh) marks it.
- `fixture.json` is unchanged (fixturegen drift check passes).

## Gates (this branch)
- `go test ./internal/source/jira/...`: green.
- `go test ./...`: green except `internal/app` `TestGolden_SVCViewAndBootstrap` (see CONTRACT-ISSUE in the WP report).
- Budgets: full sync 79 non-dev (≤ 250), 44 summary, 85 detail; incremental 11 non-dev (≤ 50).

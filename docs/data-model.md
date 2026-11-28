# Cuisle — Data model

One SQLite file, `cuisle.db` (location: `architecture.md` §6.4). Driver `modernc.org/sqlite`.

## 1. Conventions

- All tables `STRICT`. Timestamps: `INTEGER` Unix **milliseconds UTC**. Booleans: `INTEGER` 0/1 with `CHECK`.
- Lane ids: `backlog | todo | prog | review | qa | done | hidden`. "Working lanes" = `todo, prog, review, qa`.
- Three families of tables:
  - **Config** (`app_setting`, `ui_state`, `standup*` settings tables, `sink`, `ticket_field_pref`, `ticket_draft`):
    user-authored, precious, migrated carefully.
  - **Record** (`issue`, `*_change`, `comment`, `dev_*`, `person`, `sprint`, `standup_issue_span`,
    `standup_config_history`): the provider-neutral store of synced facts. **Append-only in spirit and never pruned**
    (ADR 0011): it is what a future "as of" view is rebuilt from, and old facts cannot be re-fetched faithfully once the
    source moves on. Current-state tables (`standup_issue`) are derived conveniences.
  - **Content** (ADR 0015; `issue_content`, `issue_field_value`, `issue_link`, `attachment`, `field_def`, and the
    `comment` body columns): what the ticket modal renders. Stored locally, **current value only** (rewritten when the
    issue is re-hydrated; not historised, like `issue.summary`), never pruned by age, never exported or logged.
    Comments are the exception: rows append, an edit updates the body and `updated_at`.
  - **Operational** (`space`, `board*` autocomplete caches, `sync_run`, `sync_log`, `sink_outbox`, `transition_cache`): trimmed.
- **Provider-neutral naming** (ADR 0012): tables carry no provider prefix. Every synced row hangs off a `connection`
  whose `source` column names the provider (`jira` in v1). Reference rows are keyed `(connection_id, id)` where `id` is
  the provider's id as `TEXT`; `issue` has a local integer `id` plus `source_id`. Provider-specific extras live in
  `source_meta` JSON columns (Jira: field ids, board filter id, sub-query) and are read only by that source adapter.
- **Config never has a foreign key into record/operational tables.** Config rows carry the source ids *and* a
  denormalised display snapshot (space key/name, board name/type, speaker display name) so Configure and the tab bar
  render with an empty store. Record → config FKs (`standup_issue*.standup_id`) are allowed and cascade.
- Source identifiers (issue, board, sprint, space, status, type, account, changelog and comment ids) are stored as `TEXT`.
- Ordered lists use a dense `position INTEGER` (0…n−1), rewritten for the whole list inside one transaction on every
  reorder (lists are ≤ ~50 rows). No uniqueness constraint on `position` (rewrites would violate it transiently).
- Every write that changes what a standup shows bumps `standup.rev` (used by the frontend to detect stale views).

## 2. Schema (migration `0001_init.sql`)

### 2.1 App-level config

```sql
CREATE TABLE app_setting (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
-- keys: theme, text_size, sync_on_open, sync_stale_hours, ticket.comment_order, media.cache_max_mb, history_retention_days  (values/defaults: architecture.md §6.4)
```

`ui_state` (single row: last standup, window geometry) is defined in §2.3 because it references `standup`.

### 2.2 Connection (the secret is in the OS keychain, never here)

```sql
CREATE TABLE connection (
  id                    INTEGER PRIMARY KEY,
  source                TEXT NOT NULL DEFAULT 'jira',          -- provider kind; matches source.Source.Kind()
  base_url              TEXT NOT NULL UNIQUE,                  -- https://acme.atlassian.net (no trailing slash)
  account               TEXT NOT NULL,                         -- login (Jira: email); keychain account = <source>:<host>:<account>
  account_id            TEXT,                                  -- provider account id of the connected user
  display_name          TEXT,
  auth_state            TEXT NOT NULL DEFAULT 'unknown' CHECK (auth_state IN ('unknown','ok','invalid','missing')),
  verified_at           INTEGER,
  source_meta           TEXT NOT NULL DEFAULT '{}',            -- Jira: {"flaggedFieldId","sprintFieldId","rankFieldId","devSummaryFieldId","devSummaryFieldChecked","devActivity","fieldsFetchedAt"}
  dev_activity_state    TEXT NOT NULL DEFAULT 'unknown' CHECK (dev_activity_state IN ('unknown','ok','unavailable')),
  dev_activity_checked_at INTEGER,
  created_at            INTEGER NOT NULL
) STRICT;
```

Milestone 1 has exactly one row; `connection_id` columns keep multi-connection (and multi-provider) possible without a
schema rewrite.

**Changing sites (decided 2026-11-23):** a reconnect to the *same* `base_url` (case-insensitive scheme and host)
updates `account`, `account_id`, `display_name` and `auth_state` in place and keeps the id. A connect to a
*different* site while the existing connection owns any standup or issue is refused with `conflict` ("Cuisle is
connected to <host>. Switching to another Jira site isn't supported yet."). Nothing is written, and the new secret
never reaches the keychain. A connection that owns issues is never deleted, because that would cascade into synced
history. Moving to another site is therefore not supported in v1 once data exists. It's a known limitation for a
later multi-connection UI, which the schema already allows. `Disconnect` keeps the row and all data.

### 2.3 Standups (config)

```sql
CREATE TABLE standup (
  id            INTEGER PRIMARY KEY,
  connection_id INTEGER REFERENCES connection(id) ON DELETE SET NULL,
  name          TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 80),
  position      INTEGER NOT NULL,                              -- rail order; tabs + shortcuts 1–9 number the non-deleted, non-draft (board_id NOT NULL) standups in this order
  -- source (ids + display snapshot; NULL until chosen)
  space_id    TEXT,
  space_key   TEXT,
  space_name  TEXT,
  board_id      TEXT,
  board_name    TEXT,
  board_type    TEXT CHECK (board_type IN ('scrum','kanban')), -- has sprints / continuous; Jira 'simple' is normalised by sprint support
  board_meta    TEXT NOT NULL DEFAULT '{}',                    -- provider-specific snapshot; Jira: {"filterId":9001,"subQuery":"…"}
  scope         TEXT NOT NULL DEFAULT 'sprint' CHECK (scope IN ('sprint','board')),
  extra_filter     TEXT NOT NULL DEFAULT '',
  extra_filter_ok  INTEGER CHECK (extra_filter_ok IN (0,1)),         -- NULL = not validated yet
  extra_filter_error TEXT,
  rev           INTEGER NOT NULL DEFAULT 1,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL,
  deleted_at    INTEGER,                                       -- soft delete = archive: hidden from tabs/rail, never purged (history is kept); Undo/Restore clears it
  CHECK (board_type <> 'kanban' OR scope = 'board')            -- kanban has no sprint scope
) STRICT;
CREATE INDEX standup_order ON standup(position) WHERE deleted_at IS NULL;

CREATE TABLE standup_settings (
  standup_id          INTEGER PRIMARY KEY REFERENCES standup(id) ON DELETE CASCADE,
  -- person view
  rows_mode           TEXT NOT NULL DEFAULT 'fit'   CHECK (rows_mode IN ('fit','roomy')),
  -- speakers
  unassigned_position TEXT NOT NULL DEFAULT 'last'  CHECK (unassigned_position IN ('first','last','skip')),
  reshuffle_daily     INTEGER NOT NULL DEFAULT 0    CHECK (reshuffle_daily IN (0,1)),
  shuffle_seed        INTEGER NOT NULL DEFAULT 0,   -- seed of the current daily order
  shuffle_date        TEXT,                         -- local 'YYYY-MM-DD' the seed belongs to
  -- pulse
  pulse_days          INTEGER NOT NULL DEFAULT 21   CHECK (pulse_days IN (7,14,21,28)),
  show_commits        INTEGER NOT NULL DEFAULT 1    CHECK (show_commits IN (0,1)),
  show_prs            INTEGER NOT NULL DEFAULT 1    CHECK (show_prs IN (0,1)),
  show_comments       INTEGER NOT NULL DEFAULT 1    CHECK (show_comments IN (0,1)),
  shade_weekends      INTEGER NOT NULL DEFAULT 1    CHECK (shade_weekends IN (0,1)),
  -- whole board
  done_window         TEXT NOT NULL DEFAULT 'sprint' CHECK (done_window IN ('sprint','14d','yesterday')),
  backlog_rows        INTEGER NOT NULL DEFAULT 12   CHECK (backlog_rows BETWEEN 0 AND 50)
) STRICT;
-- Kanban default applied in code when a kanban board is chosen: scope='board', done_window='14d'
-- (done_window='sprint' is rejected with `validation` while board_type='kanban').

-- Walk order + per-lane limit + label: the 4 built-in working lanes (todo, prog, review, qa) plus any custom lanes the
-- user named (ids c1, c2, … from standup_settings.lane_seq, never reused). Done is always last. (Shape as of 0006.)
CREATE TABLE standup_lane (
  standup_id    INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  lane          TEXT    NOT NULL CHECK (lane IN ('todo','prog','review','qa') OR (lane GLOB 'c[1-9]*' AND lane NOT GLOB 'c*[^0-9]*')),
  walk_position INTEGER NOT NULL CHECK (walk_position >= 0),
  limit_days    INTEGER NOT NULL CHECK (limit_days BETWEEN 0 AND 99),   -- 0 = no limit
  label         TEXT    CHECK (label IS NULL OR length(label) BETWEEN 1 AND 40),  -- NULL = built-in label
  hue           INTEGER CHECK (hue IS NULL OR hue BETWEEN 1 AND 8),     -- custom lanes: extended palette index
  CHECK ((lane GLOB 'c*') = (hue IS NOT NULL)),
  CHECK (lane NOT GLOB 'c*' OR label IS NOT NULL),
  PRIMARY KEY (standup_id, lane)
) STRICT, WITHOUT ROWID;
-- standup_settings (0006): lane_seq INTEGER NOT NULL DEFAULT 0 (the last custom-lane number handed out) and
-- done_label TEXT (NULL = "Done"). Custom lanes are created at the end of the walk with limit 5 and the lowest hue
-- (1..8) no live custom lane uses; only when all eight are taken, (lane_seq − 1) mod 8 + 1. Labels are unique per standup ignoring case (checked in settings).
-- defaults on create: qa(0, limit 3), review(1, 2), prog(2, 5), todo(3, 10)   ["right to left"]

-- Status mapping: Jira board column → lane. Keyed by column name (board config exposes no stable column id).
CREATE TABLE standup_column_map (
  standup_id  INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  column_name TEXT    NOT NULL,
  position    INTEGER NOT NULL,                                -- column order on the Jira board (display order)
  lane        TEXT    NOT NULL CHECK (lane IN ('backlog','todo','prog','review','qa','done','hidden') OR (lane GLOB 'c[1-9]*' AND lane NOT GLOB 'c*[^0-9]*')),
  auto        INTEGER NOT NULL DEFAULT 1 CHECK (auto IN (0,1)),-- 1 = auto-matched, 0 = user-chosen (survives column refresh)
  PRIMARY KEY (standup_id, column_name)
) STRICT, WITHOUT ROWID;

-- Denormalised status ids per mapped column, so view queries need no join into the board cache.
CREATE TABLE standup_status_lane (
  standup_id INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  status_id  TEXT    NOT NULL,
  status_name TEXT   NOT NULL,
  column_name TEXT   NOT NULL,
  lane       TEXT    NOT NULL CHECK (lane IN ('backlog','todo','prog','review','qa','done','hidden') OR (lane GLOB 'c[1-9]*' AND lane NOT GLOB 'c*[^0-9]*')),
  PRIMARY KEY (standup_id, status_id),
  FOREIGN KEY (standup_id, column_name) REFERENCES standup_column_map(standup_id, column_name) ON DELETE CASCADE ON UPDATE CASCADE
) STRICT, WITHOUT ROWID;
-- Maintained by settings.RebuildStatusLanes(standupID) in the same tx as any change to standup_column_map
-- or a board-column refresh. A status in no board column has no row here → the issue is not shown (same as Jira).

CREATE TABLE standup_speaker (
  standup_id     INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  account_id     TEXT    NOT NULL,
  display_name   TEXT    NOT NULL,                             -- snapshot; refreshed from person on sync
  position       INTEGER NOT NULL,                             -- manual speaking order (included and excluded share one list)
  included       INTEGER NOT NULL DEFAULT 1 CHECK (included IN (0,1)),
  added_manually INTEGER NOT NULL DEFAULT 0 CHECK (added_manually IN (0,1)),   -- via "Add someone from this space"
  user_touched   INTEGER NOT NULL DEFAULT 0 CHECK (user_touched IN (0,1)),     -- user moved/toggled it → never auto-pruned
  last_seen_at   INTEGER,                                      -- last sync in which they had a ticket on this board
  created_at     INTEGER NOT NULL,
  PRIMARY KEY (standup_id, account_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX standup_speaker_order ON standup_speaker(standup_id, position);

CREATE TABLE ui_state (
  id              INTEGER PRIMARY KEY CHECK (id = 1),          -- single row
  last_standup_id INTEGER REFERENCES standup(id) ON DELETE SET NULL,
  last_view       TEXT NOT NULL DEFAULT 'standup' CHECK (last_view IN ('standup','configure')),
  win_x           INTEGER, win_y INTEGER, win_w INTEGER, win_h INTEGER,
  win_maximised   INTEGER NOT NULL DEFAULT 0 CHECK (win_maximised IN (0,1)),
  updated_at      INTEGER NOT NULL
) STRICT;
INSERT INTO ui_state (id, updated_at) VALUES (1, 0);

CREATE TABLE standup_ui_state (
  standup_id  INTEGER PRIMARY KEY REFERENCES standup(id) ON DELETE CASCADE,
  last_person TEXT,                                            -- accountId | 'unassigned' | NULL (= whole board)
  updated_at  INTEGER NOT NULL,
  board_group TEXT NOT NULL DEFAULT 'assignee' CHECK (board_group IN ('assignee','status')),  -- migration 0005 (F13)
  zoom        INTEGER NOT NULL DEFAULT 100 CHECK (zoom IN (70,80,90,100,110,125,150,175,200))     -- migration 0007 (F15)
) STRICT;
```

`board_group` is the Whole board's grouping for that standup, Assignee or Status (F13, ux-spec §6.5). It is UI state,
not Configure: it is written the moment it changes (`UIStatePatch.StandupID` + `BoardGroup`), has no save indicator, does
not bump the standup `rev`, and is never part of the config history. Migration `0005_board_group.sql` adds it with the
default `'assignee'`, so every existing row, and every standup with no row, reads as today's board. A grouping patch for
a standup with no row inserts one (`last_person` NULL = whole board); a last-person patch never touches `board_group`.

`zoom` is the zoom of the standup screen's content, in percent (F15, ux-spec §16): one level per standup, shared by its
person view and Whole board. Like `board_group` it is UI state: written the moment it changes (`UIStatePatch.StandupID` +
`Zoom`, validated against the nine steps), no save indicator, no `rev` bump, never in the config history. Migration
`0007_zoom.sql` adds it with the default `100`, so every existing row, and every standup with no row, reads as today's
screen (`UIState.Zoom` has no entry = 100). A zoom patch for a standup with no row inserts one; the other patches never
touch `zoom`.
The row cascades away with its standup.

Speaker rules (implemented in `settings` + `sync` apply step):
- After each sync, every assignee of a non-backlog ticket in the standup that has no row is appended
  (`included=1, added_manually=0`); `last_seen_at` is updated for those present.
- Auto rows with `user_touched=0`, `added_manually=0` and `last_seen_at` older than 30 days are deleted.
- Effective speaking order (computed in `pulse.SpeakerOrder`): included speakers by `position`; if `reshuffle_daily=1`,
  a Fisher–Yates shuffle seeded with `shuffle_seed`. When `shuffle_date` ≠ today (local), the next view build stores
  `shuffle_date = today`, `shuffle_seed = fnv64(standup_id, today)` first. The Shuffle button: with reshuffle off it
  rewrites `position`; with reshuffle on it sets a new random `shuffle_seed` for today. Unassigned is inserted first/last
  per `unassigned_position` and only if it has tickets; with `skip` it stays in the view model with `Included=false`
  (no rail entry, but the whole board still pins its lane first). Excluded speakers who have tickets follow, `Included=false`.

### 2.4 Reference data

```sql
-- Autocomplete caches (operational).
CREATE TABLE space (
  connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  id         TEXT    NOT NULL,
  key        TEXT    NOT NULL,
  name       TEXT    NOT NULL,
  source_meta TEXT   NOT NULL DEFAULT '{}',                    -- Jira: {"style":"classic"|"next-gen"}
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (connection_id, id)
) STRICT, WITHOUT ROWID;
CREATE INDEX space_key ON space(connection_id, key);

CREATE TABLE board (
  connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  id           TEXT    NOT NULL,
  name         TEXT    NOT NULL,
  type         TEXT    NOT NULL,                               -- raw provider type (Jira: scrum | kanban | simple)
  space_id     TEXT,
  source_meta  TEXT    NOT NULL DEFAULT '{}',                  -- Jira: {"filterId","subQuery"}
  config_fetched_at INTEGER,
  fetched_at   INTEGER NOT NULL,
  PRIMARY KEY (connection_id, id)
) STRICT, WITHOUT ROWID;
CREATE INDEX board_space ON board(connection_id, space_id);

CREATE TABLE board_column (
  connection_id INTEGER NOT NULL,
  board_id TEXT    NOT NULL,
  position INTEGER NOT NULL,
  name     TEXT    NOT NULL,
  PRIMARY KEY (connection_id, board_id, position),
  FOREIGN KEY (connection_id, board_id) REFERENCES board(connection_id, id) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

CREATE TABLE board_column_status (
  connection_id INTEGER NOT NULL,
  board_id  TEXT    NOT NULL,
  position  INTEGER NOT NULL,                                  -- column position
  status_id TEXT    NOT NULL,
  PRIMARY KEY (connection_id, board_id, status_id),
  FOREIGN KEY (connection_id, board_id, position) REFERENCES board_column(connection_id, board_id, position) ON DELETE CASCADE
) STRICT, WITHOUT ROWID;

-- Record tables (never pruned): statuses, types, sprints and people referenced by history.
CREATE TABLE status (
  connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  id       TEXT    NOT NULL,
  name     TEXT    NOT NULL,
  category TEXT    NOT NULL CHECK (category IN ('new','indeterminate','done','undefined')),
  blocked_hint INTEGER NOT NULL DEFAULT 0 CHECK (blocked_hint IN (0,1)),  -- name matches (?i)\b(blocked|impeded|on hold)\b
  PRIMARY KEY (connection_id, id)
) STRICT, WITHOUT ROWID;

CREATE TABLE issue_type (
  connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  id      TEXT    NOT NULL,
  name    TEXT    NOT NULL,
  kind    TEXT    NOT NULL CHECK (kind IN ('story','bug','task','spike','subtask','epic','other')),
  PRIMARY KEY (connection_id, id)
) STRICT, WITHOUT ROWID;
-- kind: subtask flag → 'subtask'; hierarchy level 1 → 'epic'; else by lower(name): story|bug|task|spike; else 'other'.

CREATE TABLE sprint (
  connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  id          TEXT    NOT NULL,
  board_id    TEXT    NOT NULL,                                -- board the sprint was fetched for
  name        TEXT    NOT NULL,
  state       TEXT    NOT NULL CHECK (state IN ('future','active','closed')),
  start_at    INTEGER,
  end_at      INTEGER,
  complete_at INTEGER,
  fetched_at  INTEGER NOT NULL,
  PRIMARY KEY (connection_id, id)
) STRICT, WITHOUT ROWID;
CREATE INDEX sprint_board ON sprint(connection_id, board_id, state);
-- Sprints are kept after they close: "active sprint as of T" = start_at ≤ T < COALESCE(complete_at, end_at, ∞).

CREATE TABLE person (
  connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  account_id   TEXT    NOT NULL,
  display_name TEXT    NOT NULL,
  active       INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
  account_type TEXT,                                           -- 'user' | 'app' | 'customer' (Jira atlassian → user)
  fetched_at   INTEGER NOT NULL,
  PRIMARY KEY (connection_id, account_id)
) STRICT, WITHOUT ROWID;
-- No email, no avatar: initials are derived from display_name in Go.
```

### 2.5 Issues and their history (record; never pruned)

```sql
CREATE TABLE issue (
  id                  INTEGER PRIMARY KEY,                     -- local id
  connection_id       INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  source_id           TEXT    NOT NULL,                        -- provider issue id (Jira numeric id as text)
  key                 TEXT    NOT NULL,                        -- human key, e.g. MOB-1390
  space_id            TEXT    NOT NULL,
  summary             TEXT    NOT NULL,
  issue_type_id       TEXT    NOT NULL,
  status_id           TEXT    NOT NULL,                        -- current
  assignee_account_id TEXT,                                    -- current; NULL = unassigned
  flagged             INTEGER NOT NULL DEFAULT 0 CHECK (flagged IN (0,1)),
  rank                TEXT,                                    -- provider rank token (Jira LexoRank); string order = board rank
  parent_key          TEXT,
  sprint_ids          TEXT    NOT NULL DEFAULT '[]',           -- JSON array of sprint ids the issue is in (any state)
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL,                        -- provider `updated`; drives scan-and-diff
  resolved_at         INTEGER,
  comment_total       INTEGER NOT NULL DEFAULT 0,
  dev_fingerprint     TEXT,                                    -- hash of last dev-activity summary; NULL = never fetched
  dev_checked_at      INTEGER,
  hydrated_at         INTEGER NOT NULL,                        -- when fields+changelog+comments were last fetched in full
  last_seen_at        INTEGER NOT NULL,                        -- last sync whose scan returned it for any standup
  removed_at          INTEGER,                                 -- set when it stops being a member of every standup; cleared if it returns. Row is kept.
  source_meta         TEXT    NOT NULL DEFAULT '{}'
) STRICT;
CREATE UNIQUE INDEX issue_source   ON issue(connection_id, source_id);
CREATE UNIQUE INDEX issue_key      ON issue(connection_id, key);
CREATE INDEX        issue_assignee ON issue(assignee_account_id);

-- Status history from the changelog.
CREATE TABLE status_change (
  issue_id       INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  history_id     TEXT    NOT NULL,                             -- provider changelog entry id
  changed_at     INTEGER NOT NULL,
  from_status_id TEXT    NOT NULL,
  to_status_id   TEXT    NOT NULL,
  PRIMARY KEY (issue_id, history_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX status_change_time ON status_change(issue_id, changed_at);

-- Assignee history from the changelog (who owned the ticket as of a date).
CREATE TABLE assignee_change (
  issue_id        INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  history_id      TEXT    NOT NULL,
  changed_at      INTEGER NOT NULL,
  from_account_id TEXT,                                        -- NULL = was unassigned
  to_account_id   TEXT,                                        -- NULL = unassigned
  PRIMARY KEY (issue_id, history_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX assignee_change_time ON assignee_change(issue_id, changed_at);

-- Flag history (blocked spans).
CREATE TABLE flag_change (
  issue_id   INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  history_id TEXT    NOT NULL,
  changed_at INTEGER NOT NULL,
  flagged    INTEGER NOT NULL CHECK (flagged IN (0,1)),        -- state after the change
  PRIMARY KEY (issue_id, history_id)
) STRICT, WITHOUT ROWID;

-- Comments, with bodies (ADR 0015). Bodies stay local: never in exports, logs or diagnostics.
CREATE TABLE comment (
  issue_id          INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  comment_id        TEXT    NOT NULL,
  author_account_id TEXT,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL,                          -- = created_at until edited; "edited" when greater
  body_adf          TEXT,                                      -- ADF JSON as received; NULL only for rows synced before content existed / metadata-only sources
  visibility_type   TEXT CHECK (visibility_type IN ('role','group')),   -- NULL = visible to everyone
  visibility_value  TEXT,                                      -- e.g. 'Developers'
  deleted_at        INTEGER,                                   -- comment no longer returned by the source: hidden from the modal, row kept (pulse marks stay)
  PRIMARY KEY (issue_id, comment_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX comment_time ON comment(issue_id, created_at);

-- Dev activity: commits (one row per commit per issue).
CREATE TABLE dev_commit (
  issue_id    INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  commit_id   TEXT    NOT NULL,                                -- full SHA
  repo        TEXT    NOT NULL DEFAULT '',
  authored_at INTEGER NOT NULL,
  is_merge    INTEGER NOT NULL DEFAULT 0 CHECK (is_merge IN (0,1)),
  PRIMARY KEY (issue_id, commit_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX dev_commit_time ON dev_commit(issue_id, authored_at);

-- Dev activity: pull-request events. One row per observed (PR, status, lastUpdate) — see jira-sync.md §5.3.
CREATE TABLE dev_pr_event (
  issue_id   INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  pr_key     TEXT    NOT NULL,                                 -- '<repository id or name>#<pr id>'
  status     TEXT    NOT NULL CHECK (status IN ('OPEN','MERGED','DECLINED','UNKNOWN')),
  at         INTEGER NOT NULL,                                 -- PR last-update time when observed
  title      TEXT,                                             -- PR title; NULL = not known (added by 0004)
  branch     TEXT,                                             -- PR source branch; NULL = not known (added by 0004)
  PRIMARY KEY (issue_id, pr_key, status, at)
) STRICT, WITHOUT ROWID;
CREATE INDEX dev_pr_event_time ON dev_pr_event(issue_id, at);
```

Write rules for record tables: change/comment/dev rows are **upserted by their natural key, never deleted** — including
when an issue is re-hydrated (the provider's changelog is merged in, not replaced) and when an issue leaves every board.
`ON DELETE CASCADE` exists only so that removing a whole `connection` (explicit user action, not in v1 UI) is clean.

**Why raw events instead of a per-issue-per-day bucket table:** buckets depend on the viewer's timezone, the pulse
window and the "as of" date; storing timestamps and bucketing at view-build time (a few thousand rows, < 2 ms) means a
timezone change, DST shift or a historical view never requires a resync, and there is no derived table to keep
consistent. The per-day shape exists only in the view model (`Pulse.Marks`).

### 2.5a Ticket content (ADR 0015; current-only, stored locally)

> **Parent snapshot (WP-T6):** an issue's parent is stored as a synthetic `issue_field_value` row with
> `field_id = 'parent'`, holding a JSON `model.FieldValue` that wraps an `IssueRef`. It is **not a field**. Jira's real
> `parent` field is skipped on write, so the ids never collide. `cache.LoadTicket` reads the row as `Ticket.Parent`, and
> any other reader of `issue_field_value` must skip it.

> **Ships as migration `0003_ticket_content.sql`, not in `0001_init`** (v0.1.x is released and real databases exist at
> schema v2). The tables below are created there, and the new `comment` columns in §2.5 are added with `ALTER TABLE`
> (`updated_at` backfilled from `created_at`; `body_adf`, `visibility_*` and `deleted_at` NULL for rows synced before
> content existed). §3 lists it.

```sql
CREATE TABLE issue_content (
  issue_id        INTEGER PRIMARY KEY REFERENCES issue(id) ON DELETE CASCADE,
  description_adf TEXT,                                        -- ADF JSON; NULL = empty
  environment_adf TEXT,
  watchers        INTEGER NOT NULL DEFAULT -1,                 -- -1 unknown
  content_hash    TEXT    NOT NULL,                            -- sha256 over description, environment, field values, links, attachment ids
  updated_at      INTEGER NOT NULL                             -- when this row last changed locally
) STRICT;

-- Field registry per connection (from field discovery; jira-sync.md §2.2).
CREATE TABLE field_def (
  connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  field_id      TEXT    NOT NULL,                              -- 'priority', 'customfield_10042'
  name          TEXT    NOT NULL,
  kind          TEXT    NOT NULL CHECK (kind IN ('text','paragraph','number','date','datetime','url','option','options','cascade',
                  'user','users','group','groups','version','versions','component','components','labels','project','team','unknown')),
  custom        INTEGER NOT NULL DEFAULT 0 CHECK (custom IN (0,1)),
  custom_type   TEXT,                                          -- provider type key, e.g. com.atlassian.jira.plugin.system.customfieldtypes:select
  schema_json   TEXT    NOT NULL DEFAULT '{}',
  fetched_at    INTEGER NOT NULL,
  PRIMARY KEY (connection_id, field_id)
) STRICT, WITHOUT ROWID;

-- Current value of every non-empty field not modelled as an issue column. No row = not set.
CREATE TABLE issue_field_value (
  issue_id   INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  field_id   TEXT    NOT NULL,
  kind       TEXT    NOT NULL,                                 -- kind at write time (matches field_def.kind)
  value_json TEXT    NOT NULL,                                 -- model.FieldValue as JSON: {text|number|items|users|rich|raw}
  PRIMARY KEY (issue_id, field_id)
) STRICT, WITHOUT ROWID;

-- Issue links and subtasks (current). The other issue may or may not be synced: display snapshot is stored.
CREATE TABLE issue_link (
  issue_id        INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  link_id         TEXT    NOT NULL,                            -- provider link id; for subtasks 'sub:<source_id>'
  relation        TEXT    NOT NULL CHECK (relation IN ('link','subtask')),
  label           TEXT    NOT NULL DEFAULT '',                 -- 'is blocked by', 'blocks', 'relates to' (direction applied)
  position        INTEGER NOT NULL DEFAULT 0,
  other_source_id TEXT    NOT NULL,
  other_key       TEXT    NOT NULL,
  other_summary   TEXT    NOT NULL DEFAULT '',
  other_status_id TEXT,
  other_type_id   TEXT,
  other_assignee_account_id TEXT,
  PRIMARY KEY (issue_id, link_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX issue_link_other ON issue_link(other_source_id);

-- Attachment metadata + media-cache state. Bytes live on disk under <data dir>/media (architecture.md §2.4).
CREATE TABLE attachment (
  id                INTEGER PRIMARY KEY,                       -- local id; used in /media/{id}
  issue_id          INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  source_id         TEXT    NOT NULL,                          -- provider attachment id
  media_file_id     TEXT,                                      -- id used by ADF media nodes; NULL until resolved (jira-sync.md §2.14)
  name              TEXT    NOT NULL,
  mime              TEXT    NOT NULL DEFAULT 'application/octet-stream',
  bytes             INTEGER NOT NULL DEFAULT 0,
  author_account_id TEXT,
  created_at        INTEGER NOT NULL,
  is_image          INTEGER NOT NULL DEFAULT 0 CHECK (is_image IN (0,1)),  -- renderable inline (png/jpeg/gif/webp/bmp ≤ 25 MB)
  width             INTEGER, height INTEGER,                   -- known once decoded
  thumb_path        TEXT,                                      -- relative to the media dir; NULL = no thumbnail yet
  file_path         TEXT,                                      -- relative; NULL = original not cached (never fetched, or evicted)
  cached_bytes      INTEGER NOT NULL DEFAULT 0,                -- bytes of the cached original (counts against the cap); 0 when not cached
  last_viewed_at    INTEGER,                                   -- LRU key (lightbox / inline view / open)
  evicted_at        INTEGER,                                   -- last eviction of the original
  fetch_error       TEXT,                                      -- last media fetch error code, NULL when fine
  removed_at        INTEGER,                                   -- no longer on the issue: hidden; cached files deleted
  UNIQUE (issue_id, source_id)
) STRICT;
CREATE INDEX attachment_media ON attachment(media_file_id) WHERE media_file_id IS NOT NULL;
CREATE INDEX attachment_lru   ON attachment(last_viewed_at) WHERE file_path IS NOT NULL;

-- Transitions offered for an issue, as last fetched when the Status combobox opened (operational).
CREATE TABLE transition_cache (
  issue_id   INTEGER PRIMARY KEY REFERENCES issue(id) ON DELETE CASCADE,
  json       TEXT    NOT NULL,                                 -- []model.Transition
  fetched_at INTEGER NOT NULL
) STRICT;
-- Invalidated (row deleted) whenever the issue's status changes in an apply.

-- Unsent comment draft per issue (config; local only, never sent without Send).
CREATE TABLE ticket_draft (
  issue_id      INTEGER PRIMARY KEY REFERENCES issue(id) ON DELETE CASCADE,
  body_text     TEXT    NOT NULL,
  mentions_json TEXT    NOT NULL DEFAULT '[]',                 -- [{start,end,accountId,name}]
  updated_at    INTEGER NOT NULL
) STRICT;

-- Field-rail preferences per connection (config; Configure › Ticket fields).
CREATE TABLE ticket_field_pref (
  connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
  field_id      TEXT    NOT NULL,
  position      INTEGER,                                       -- NOT NULL = pinned, in this order; NULL = not pinned
  hidden        INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0,1)),
  PRIMARY KEY (connection_id, field_id),
  CHECK (position IS NOT NULL OR hidden = 1)                   -- a row exists only if pinned or hidden
) STRICT, WITHOUT ROWID;
```

History rules (ADR 0015):

| Data | Rule |
|---|---|
| Status, assignee, flag | fully historised (`*_change`), including changes made by Cuisle's own light writes — they arrive via the post-write refresh's changelog, never invented locally |
| Comments | append; an edit overwrites `body_adf` and sets `updated_at`; a comment missing from a complete fetch gets `deleted_at` |
| Description, environment, field values, links, subtasks | current value only: `issue_content`/`issue_field_value`/`issue_link` rows are replaced per issue when its `content_hash` changes |
| Attachments | rows upserted by `source_id`; disappearing ones get `removed_at` (files deleted); cache columns are local state |
| Drafts, field prefs | local config; drafts are deleted on successful send |

A past ("as of") view therefore shows today's description/fields/comments-as-edited (same accepted limitation as summary, D10).

### 2.6 Standup ↔ issue membership: current state + temporal spans

```sql
-- Current membership (derived convenience; equals the open spans). Rewritten in the sync apply transaction.
CREATE TABLE standup_issue (
  standup_id INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  issue_id   INTEGER NOT NULL REFERENCES issue(id)   ON DELETE CASCADE,
  role       TEXT    NOT NULL CHECK (role IN ('board','backlog')),
             -- 'board'   = in scope (active sprint / whole board), any lane incl. done
             -- 'backlog' = in the ranked backlog slice (top ≤ 50)
  rank_index INTEGER NOT NULL,                                 -- 0-based order returned by the source within the role
  PRIMARY KEY (standup_id, issue_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX standup_issue_issue ON standup_issue(issue_id);
CREATE INDEX standup_issue_order ON standup_issue(standup_id, role, rank_index);

-- Membership history (record; never pruned). One row per continuous stay of an issue in a role.
CREATE TABLE standup_issue_span (
  standup_id INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  issue_id   INTEGER NOT NULL REFERENCES issue(id)   ON DELETE CASCADE,
  role       TEXT    NOT NULL CHECK (role IN ('board','backlog')),
  valid_from INTEGER NOT NULL,                                 -- started_at of the sync run that first saw it in this role
  valid_to   INTEGER,                                          -- started_at of the sync run that no longer saw it; NULL = current
  PRIMARY KEY (standup_id, issue_id, role, valid_from),
  CHECK (valid_to IS NULL OR valid_to > valid_from)
) STRICT, WITHOUT ROWID;
CREATE UNIQUE INDEX standup_issue_span_open ON standup_issue_span(standup_id, issue_id) WHERE valid_to IS NULL;
CREATE INDEX standup_issue_span_time ON standup_issue_span(standup_id, valid_from, valid_to);

-- Effective standup configuration over time (record; never pruned). A new row is opened in the sync apply
-- transaction whenever the config fingerprint differs from the open row's; the previous row is closed.
CREATE TABLE standup_config_history (
  standup_id  INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  valid_from  INTEGER NOT NULL,
  valid_to    INTEGER,                                         -- NULL = current
  fingerprint TEXT    NOT NULL,                                -- sha256 of config_json
  config_json TEXT    NOT NULL,                                -- see below
  PRIMARY KEY (standup_id, valid_from)
) STRICT, WITHOUT ROWID;
```

Maintenance (same transaction as the membership rewrite, `T` = the run's `started_at`):
- issue in scan, no open span → insert `(valid_from = T)`; open span with a different role → close it (`valid_to = T`)
  and insert the new one; open span, not in scan → close it. Rank is deliberately **not** historised (it churns on every
  reorder); past boards are ordered by current rank, then key.
- `issue.removed_at = T` when an issue's last open span closes; `NULL` again when one opens.
- `config_json` = `{scope, extraFilter, doneWindow, backlogRows, pulseDays, shadeWeekends, show:{commits,prs,comments},
  rowsMode, walkOrder:[…], limits:{lane:days}, statusLanes:[{statusId,statusName,columnName,lane}],
  speakers:[{accountId,name,position,included}], unassignedPosition, activeSprintIds:[…], lanes?:[{lane,label,kind,hue?}]}`
  — everything the view builder reads from config, so a past board uses the mapping, limits and speaking order of its
  day. `lanes` (0006) defines custom lanes and renamed built-in lanes/Done so a lane deleted or renamed later still
  resolves; it is omitted when nothing is customised, so an untouched standup keeps its fingerprint.

**Fidelity of "as of T"** (what `internal/history` can reconstruct):

| Fact | As-of source | Granularity |
|---|---|---|
| Status / lane | `status_change` + `config_history.statusLanes` | exact (changelog timestamps) |
| Assignee | `assignee_change` | exact |
| Blocked | `flag_change`, status `blocked_hint` | exact |
| Pulse marks | `comment`, `dev_commit`, `dev_pr_event` ≤ T | exact (commits/PR events only as far back as they were observed) |
| Board/backlog membership | `standup_issue_span` | per sync run; nothing before the standup's first sync |
| Lane mapping, limits, speakers, scope | `standup_config_history` | per sync run |
| Sprint | `sprint` start/complete times | exact |
| Summary, type, rank | current values only | not historised (accepted) |

### 2.7 Sync bookkeeping (operational)

```sql
CREATE TABLE standup_sync_state (
  standup_id      INTEGER PRIMARY KEY REFERENCES standup(id) ON DELETE CASCADE,
  needs_sync      INTEGER NOT NULL DEFAULT 1 CHECK (needs_sync IN (0,1)),   -- source config changed since last success
  last_attempt_at INTEGER,
  last_success_at INTEGER,
  last_full_at    INTEGER,
  history_from    INTEGER,                                     -- started_at of the first successful sync (earliest "as of")
  backlog_total   INTEGER NOT NULL DEFAULT -1,                 -- approximate count, -1 unknown
  active_sprint_ids TEXT NOT NULL DEFAULT '[]'                 -- JSON array as of last success
) STRICT;

CREATE TABLE sync_run (
  id             INTEGER PRIMARY KEY,
  standup_id     INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  mode           TEXT    NOT NULL CHECK (mode IN ('incremental','full')),
  trigger        TEXT    NOT NULL CHECK (trigger IN ('manual','open','config')),
  status         TEXT    NOT NULL CHECK (status IN ('running','ok','partial','cancelled','failed')),
  started_at     INTEGER NOT NULL,
  finished_at    INTEGER,
  issues_scanned INTEGER NOT NULL DEFAULT 0,
  issues_hydrated INTEGER NOT NULL DEFAULT 0,
  requests       INTEGER NOT NULL DEFAULT 0,
  throttled      INTEGER NOT NULL DEFAULT 0,                   -- number of rate-limit responses
  error_code     TEXT,
  error_message  TEXT,
  warnings       TEXT    NOT NULL DEFAULT '[]'                 -- JSON array of warning codes
) STRICT;
CREATE INDEX sync_run_standup ON sync_run(standup_id, started_at DESC);

CREATE TABLE sync_log (
  id      INTEGER PRIMARY KEY,
  run_id  INTEGER NOT NULL REFERENCES sync_run(id) ON DELETE CASCADE,
  at      INTEGER NOT NULL,
  level   TEXT    NOT NULL CHECK (level IN ('info','warn','error')),
  phase   TEXT    NOT NULL,
  message TEXT    NOT NULL                                     -- no secrets, no issue content
) STRICT;
CREATE INDEX sync_log_run ON sync_log(run_id, id);
```

`sync_run` rows (`running` → final status) are written in their own small transactions so a failed run is recorded
even though its data transaction never commits.

### 2.8 Sinks and outbox (outbound adapters; ADR 0012)

```sql
-- A configured destination (config). Secrets (webhook tokens, API keys) live in the keychain: account `sink:<id>`.
CREATE TABLE sink (
  id         INTEGER PRIMARY KEY,
  kind       TEXT    NOT NULL,                                 -- registered adapter kind: 'file' (v1); later 'webhook','linear',…
  name       TEXT    NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 80),
  standup_id INTEGER REFERENCES standup(id) ON DELETE CASCADE, -- NULL = applies to every standup
  config     TEXT    NOT NULL DEFAULT '{}',                    -- JSON object validated against the adapter's ConfigSchema()
  send_on    TEXT    NOT NULL DEFAULT 'manual' CHECK (send_on IN ('manual','after_sync')),
  enabled    INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

-- One row per delivery (operational). The payload is frozen at enqueue time so retries are idempotent.
CREATE TABLE sink_outbox (
  id              INTEGER PRIMARY KEY,
  sink_id         INTEGER NOT NULL REFERENCES sink(id)    ON DELETE CASCADE,
  standup_id      INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  cause           TEXT    NOT NULL CHECK (cause IN ('manual','after_sync')),
  payload_kind    TEXT    NOT NULL CHECK (payload_kind IN ('current','range')),
  as_of           INTEGER,                                     -- 'current': snapshot time
  range_from      TEXT,                                        -- 'range': local dates 'YYYY-MM-DD' (inclusive)
  range_to        TEXT,
  idempotency_key TEXT    NOT NULL UNIQUE,                     -- after_sync: 'sync:<run_id>:<sink_id>'; manual: uuid
  payload         TEXT    NOT NULL,                            -- cuisle.export.v1 JSON (architecture.md §3.6)
  status          TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivering','delivered','failed','dead')),
  attempts        INTEGER NOT NULL DEFAULT 0,
  last_error_code TEXT,
  last_error      TEXT,
  receipt         TEXT,                                        -- adapter reference: file path, remote id, HTTP status
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  delivered_at    INTEGER
) STRICT;
CREATE INDEX sink_outbox_pending ON sink_outbox(sink_id, status, id);
```

Delivery rules: a row is attempted immediately after enqueue (up to 3 tries with 1 s / 4 s backoff inside that one
delivery job). Still failing → `failed`; it is retried only on the next explicit trigger for that sink (manual Send or
Retry, or the next successful sync when `send_on='after_sync'`) — **no timers**. After 8 attempts → `dead` (manual Retry
only). `delivering` rows found at startup are reset to `failed` (interrupted). `payload` is cleared (`''`) 30 days after
`delivered_at` to bound size; the row itself is kept for 90 days.

## 3. Migrations

- Files: `internal/store/migrations/NNNN_snake_name.sql`, embedded with `//go:embed migrations/*.sql`. Numbering is
  contiguous from `0001`. **Forward-only**: no down migrations; a mistake is fixed by a new migration.
- Tracking table (created by the runner, not by a migration):

```sql
CREATE TABLE IF NOT EXISTS schema_migration (
  version    INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  checksum   TEXT    NOT NULL,          -- sha256 of the file as shipped
  applied_at INTEGER NOT NULL
) STRICT;
```

- Runner (`store.Migrate(ctx, db)`), at every start, on the writer connection:
  1. Read applied versions. If `max(applied) > max(embedded)` → `apperr{Code: db, Message: "This data was created by a newer Cuisle"}`; do not open.
  2. If an applied version's checksum differs from the embedded file → log `warn` (dev builds: fail).
  3. If anything is pending and the DB is non-empty: `VACUUM INTO '<dir>/cuisle.db.bak-v<max applied>'`; keep the 2 newest backups.
  4. For each pending file in order: `BEGIN IMMEDIATE` → exec script → insert `schema_migration` row → `PRAGMA user_version = N` → `COMMIT`.
     `PRAGMA foreign_keys` is switched off only for migrations marked with a first-line comment `-- cuisle:fk-off`
     (table rebuilds), followed by `PRAGMA foreign_key_check` before commit.
- Config **and record** tables must be migrated in place with data preserved (record data cannot be re-fetched
  faithfully). Only operational tables (`space`, `board*`, `sync_*`) may be dropped and recreated (set `needs_sync=1`
  for all standups in the same file).
- **History:**
  - `0001_init`: §2 verbatim.
  - `0002_lane_order_removed_speakers` (2026-11-23):
    - `standup_column_map.lane_order INTEGER` (NULL = board order) orders the columns mapped to the same lane.
      `ConfigService.MoveColumn` sets it, a column refresh keeps it, and a lane change clears it.
    - New config table `standup_speaker_removed (standup_id, account_id, removed_at)`: people removed from a speaker
      list, which sync never re-adds. `AddSpeaker` deletes the row; a board change clears it together with the
      speaker list. `Duplicate` copies both.
  - `0003_ticket_content` (F12, ADR 0015; work package T6): the §2.5a tables (`issue_content`, `field_def`,
    `issue_field_value`, `issue_link`, `attachment`, `ticket_draft`, `ticket_field_pref`) and the new `comment` columns
    (`ALTER TABLE … ADD COLUMN`; `updated_at` = `created_at` for existing rows). Record rows are untouched; standups get
    `needs_sync=1` so the next sync fills the content.
  - `0004_dev_branch_title` (F12 follow-up): `dev_pr_event.title` and `.branch` (nullable `ADD COLUMN`; existing rows keep
    NULL and render as before: the repository as the PR text, no Branch row). It also sets `issue.dev_fingerprint = NULL`
    so the next sync re-reads each dev target's detail once (the requests it already makes) and fills the new columns;
    record rows are untouched. A later observation of the same `(pr_key, status, at)` fills NULLs and a changed title,
    and never blanks a known value.
  - `0005_board_group` (F13): `standup_ui_state.board_group` ('assignee'|'status'), see §2.1.
  - `0007_zoom` (F15): `standup_ui_state.zoom` (the nine steps, default 100), see §2.1.
  - `0008_issue_gen` (palette title index): `issue_gen(id = 1, n)`, empty at creation (no row reads as 0), and three triggers
    on `issue` that `INSERT … ON CONFLICT DO UPDATE SET n = n + 1` after an insert, a delete, or an update that changes
    `key`, `summary` or `status_id`. Nothing else moves it (`last_seen_at`, `removed_at`, dev fingerprints, other tables).
    The finder reads it in the same read transaction as the rows of its in-memory summary index and rebuilds the index only
    when it differs. No record row is changed.
  - `0009_perf_indexes`: additive indexes `issue_space(connection_id, space_id)`, `issue_field_value_field(field_id, issue_id)`
    and `flag_change_time(issue_id, changed_at)` for ticket-modal field-in-use lookups and flag history ordering. No rows changed.
  - `0006_custom_lanes` (2026-11-25, `-- cuisle:fk-off`): rebuilds `standup_lane`, `standup_column_map` and
    `standup_status_lane` only to widen their lane CHECKs to custom ids `c<N>` (every row copied unchanged, `lane_order`
    kept, the status-lane foreign key still cascades), adds `standup_lane.label`/`.hue`, and
    `standup_settings.lane_seq`/`.done_label`. No record table is touched. Deleting a custom lane moves its columns
    (`DeleteLane`); synced history is never touched. `Duplicate` copies custom lanes, labels and the counter;
    `CopySettingsFrom` copies the built-in lanes' order, limits and labels and keeps the target's custom lanes.
- Tests (`internal/store/migrate_test.go`):
  - fresh DB → all migrations → schema dump equals `testdata/schema.golden.sql` (updated deliberately with `-update`);
  - for every historical version `N`: load `testdata/v<N>.sql` (a populated dump taken when N was current) → migrate to
    head → `foreign_key_check` and `integrity_check` clean → config and record rows preserved (assert counts and a few values, incl. spans and change tables);
  - running `Migrate` twice is a no-op; a tampered checksum is detected; a "newer DB" is refused.

## 4. Query shaping

- Reads for a view are **set-based, never per-issue**: one query per table, restricted by the standup's membership, then
  assembled in Go maps keyed by `issue_id`. A `StandupView` is 8 queries regardless of ticket count.
- All view queries run on a reader connection inside one `BEGIN` (deferred) so they see a single WAL snapshot — a
  concurrently committing sync is either entirely visible or not at all.
- Time-window predicates use precomputed bounds (`:win_start_ms` = local midnight of today − (pulse_days − 1)).
- Statements are prepared once per connection and cached (`store.Stmt`).
- **Every view is built "as of" a time.** The builder consumes a `model.BoardSnapshot` (members, issue state, config)
  and an `asOf time.Time`; it never reads tables itself. Two loaders produce that snapshot:
  `cache.LoadCurrent(standupID)` (queries Q1–Q5 over `standup_issue` and the live config; `asOf = now`) and
  `history.LoadAsOf(standupID, t)` (Q8 over `standup_issue_span`, `standup_config_history` and the change tables, with
  every event filtered to `≤ t`). The live path is the hot path and stays as cheap as before.

### 4.1 Most important queries

**Q1 — tickets of a standup with lane (board + done), in rank order**

```sql
SELECT i.id, i.key, i.summary, t.kind, i.status_id, sl.status_name, sl.lane,
       i.assignee_account_id, i.flagged, s.blocked_hint, i.created_at, i.sprint_ids, si.rank_index
FROM standup_issue si
JOIN issue i            ON i.id = si.issue_id
JOIN standup_status_lane sl  ON sl.standup_id = si.standup_id AND sl.status_id = i.status_id
LEFT JOIN issue_type t  ON t.connection_id = i.connection_id AND t.id = i.issue_type_id
LEFT JOIN status s      ON s.connection_id = i.connection_id AND s.id = i.status_id
WHERE si.standup_id = :standup AND si.role = 'board' AND sl.lane NOT IN ('hidden','backlog')
ORDER BY si.rank_index;
```

Done-window filtering and the backlog rule for `scope='board'` are applied in Go after Q2 (they need the changelog).

**Q2 — full status history for those tickets (segments, days-in-lane, done-at)**

```sql
SELECT c.issue_id, c.changed_at, c.from_status_id, c.to_status_id
FROM status_change c
JOIN standup_issue si ON si.issue_id = c.issue_id
WHERE si.standup_id = :standup AND si.role = 'board'
ORDER BY c.issue_id, c.changed_at, c.history_id;
```

**Q3 — activity events inside the pulse window (three statements of the same shape; commits shown)**

```sql
SELECT d.issue_id, d.authored_at
FROM dev_commit d
JOIN standup_issue si ON si.issue_id = d.issue_id
WHERE si.standup_id = :standup AND si.role = 'board'
  AND d.is_merge = 0 AND d.authored_at >= :win_start_ms
ORDER BY d.issue_id, d.authored_at;
-- same for comment(created_at), dev_pr_event(at), and flag_change (no window bound: prior state needed)
```

**Q4 — speakers with display data**

```sql
SELECT sp.account_id, COALESCE(u.display_name, sp.display_name) AS name, sp.position, sp.included, sp.added_manually
FROM standup_speaker sp
LEFT JOIN standup s  ON s.id = sp.standup_id
LEFT JOIN person u ON u.connection_id = s.connection_id AND u.account_id = sp.account_id
WHERE sp.standup_id = :standup
ORDER BY sp.position;
```

**Q5 — backlog slice**

```sql
SELECT i.key, i.summary, t.kind
FROM standup_issue si
JOIN issue i           ON i.id = si.issue_id
LEFT JOIN issue_type t ON t.connection_id = i.connection_id AND t.id = i.issue_type_id
WHERE si.standup_id = :standup AND si.role = 'backlog'
ORDER BY si.rank_index
LIMIT :backlog_rows;
```

**Q6 — tab bar / bootstrap (one row per standup)**

```sql
SELECT s.id, s.name, s.space_key, s.position, s.rev, s.board_id IS NOT NULL AS configured,
       ss.last_success_at, ss.needs_sync,
       (SELECT count(*) FROM standup_issue si
          JOIN issue i ON i.id = si.issue_id
          JOIN standup_status_lane sl ON sl.standup_id = si.standup_id AND sl.status_id = i.status_id
         WHERE si.standup_id = s.id AND si.role = 'board' AND sl.lane IN ('todo','prog','review','qa')) AS active_count
FROM standup s LEFT JOIN standup_sync_state ss ON ss.standup_id = s.id
WHERE s.deleted_at IS NULL
ORDER BY s.position;
```

**Q7 — autosave write (shape of every config mutation)**

```sql
-- inside BEGIN IMMEDIATE
UPDATE standup_settings SET pulse_days = :v WHERE standup_id = :id;
UPDATE standup SET rev = rev + 1, updated_at = :now WHERE id = :id AND deleted_at IS NULL RETURNING rev;
-- 0 rows returned → apperr not_found
```

**Q9 — `cache.LoadTicket(issueID)`: everything the ticket modal needs, one reader transaction, no network**

```sql
SELECT i.*, c.description_adf, c.environment_adf, c.watchers, c.updated_at AS content_updated_at
FROM issue i LEFT JOIN issue_content c ON c.issue_id = i.id WHERE i.id = :issue;

SELECT v.field_id, v.kind, v.value_json, d.name, d.custom
FROM issue_field_value v JOIN issue i ON i.id = v.issue_id
LEFT JOIN field_def d ON d.connection_id = i.connection_id AND d.field_id = v.field_id
WHERE v.issue_id = :issue;

SELECT d.field_id, d.name, d.kind, d.custom, p.position, COALESCE(p.hidden, 0) AS hidden    -- registry + prefs → rows and "Not set"
FROM field_def d JOIN issue i ON i.connection_id = d.connection_id
LEFT JOIN ticket_field_pref p ON p.connection_id = d.connection_id AND p.field_id = d.field_id
WHERE i.id = :issue;

SELECT comment_id, author_account_id, created_at, updated_at, body_adf, visibility_type, visibility_value
FROM comment WHERE issue_id = :issue AND deleted_at IS NULL ORDER BY created_at, comment_id;

SELECT id, source_id, media_file_id, name, mime, bytes, author_account_id, created_at, is_image, width, height,
       thumb_path IS NOT NULL AS has_thumb, file_path IS NOT NULL AS has_file
FROM attachment WHERE issue_id = :issue AND removed_at IS NULL ORDER BY is_image DESC, created_at DESC;

SELECT l.relation, l.label, l.other_key, l.other_summary, l.other_status_id, l.other_type_id, l.other_assignee_account_id,
       o.id AS other_issue_id, o.summary AS live_summary, o.status_id AS live_status_id,
       o.assignee_account_id AS live_assignee_account_id   -- the synced target's CURRENT assignee (link/subtask row initials)
FROM issue_link l JOIN issue i ON i.id = l.issue_id
LEFT JOIN issue o ON o.connection_id = i.connection_id AND o.source_id = l.other_source_id
WHERE l.issue_id = :issue ORDER BY l.relation, l.label, l.position;

SELECT body_text, mentions_json FROM ticket_draft WHERE issue_id = :issue;
-- plus the per-issue forms of Q2/Q3 (status, flag, comment times, dev events) for the header pulse and the Development group.
```

Inline-card and mention resolution inside ADF use two prepared lookups (`issue` by `(connection_id, key)`, `person` by
account id) batched per document.

**Q8 — membership and state as of `:t` (history loader; same shape feeds the same builder)**

```sql
SELECT i.id, i.key, i.summary, i.created_at, sp.role,
       COALESCE((SELECT c.to_status_id   FROM status_change c   WHERE c.issue_id = i.id AND c.changed_at <= :t
                 ORDER BY c.changed_at DESC, c.history_id DESC LIMIT 1),
                (SELECT c.from_status_id FROM status_change c   WHERE c.issue_id = i.id
                 ORDER BY c.changed_at, c.history_id LIMIT 1),
                i.status_id)                                        AS status_id_at,
       CASE WHEN EXISTS (SELECT 1 FROM assignee_change a WHERE a.issue_id = i.id)
            THEN COALESCE((SELECT a.to_account_id   FROM assignee_change a WHERE a.issue_id = i.id AND a.changed_at <= :t
                           ORDER BY a.changed_at DESC, a.history_id DESC LIMIT 1),
                          (SELECT a.from_account_id FROM assignee_change a WHERE a.issue_id = i.id
                           ORDER BY a.changed_at, a.history_id LIMIT 1))
            ELSE i.assignee_account_id END                           AS assignee_at
FROM standup_issue_span sp
JOIN issue i ON i.id = sp.issue_id
WHERE sp.standup_id = :standup AND sp.valid_from <= :t AND (sp.valid_to IS NULL OR sp.valid_to > :t)
ORDER BY i.rank, i.key;
-- config: SELECT config_json FROM standup_config_history
--         WHERE standup_id = :standup AND valid_from <= :t AND (valid_to IS NULL OR valid_to > :t);
```

(The assignee expression treats "last change before `:t` set it to NULL" as unassigned only via the first branch's
NULL; the Go loader resolves this precisely from the ordered rows — the SQL above is the shape, not the final text.)

## 5. Retention and pruning

**Default: synced data is kept forever** (ADR 0011). Nothing in the record tables (§1) is ever deleted by the app:
issues that leave a board keep their row and all history (`removed_at` is set, spans are closed), comments/commits/PR
events are never aged out, closed sprints and departed people stay.

`cache.Trim(ctx)` runs after every successful sync apply (same writer, separate transaction) and at startup, and touches
**operational data only**:

| Data | Rule |
|---|---|
| `space`, `board` (+ columns) | autocomplete caches: delete when `fetched_at` older than 30 days and not referenced by any standup |
| `sync_run` (+ `sync_log`) | keep the newest 50 per standup and nothing older than 90 days |
| `sink_outbox` | clear `payload` 30 days after delivery; delete rows older than 90 days (`dead` rows: 180 days) |
| `transition_cache` | delete rows older than 7 days (also invalidated on status change) |
| Media cache (files, not rows) | originals evicted least-recently-viewed (`attachment.last_viewed_at`, never-viewed first by oldest `created_at`) until `SUM(cached_bytes)` ≤ `media.cache_max_mb` (default 1024 MB); thumbnails are never evicted; "Clear image cache" removes both and nulls the path columns; orphan files (no row) are swept at startup |
| `standup` with `deleted_at` | **never purged** (Chris: keep synced data for a future history view). A deleted standup is archived: hidden everywhere, no sync, its spans and config history kept; restorable later (archive list is a post-v1 UI). |
| DB backups | keep 2 newest `cuisle.db.bak-v*` (pre-migration) and 1 `cuisle.db.bak-weekly` (rewritten at most weekly, at startup) |

`PRAGMA optimize` on clean shutdown; no scheduled `VACUUM` (`auto_vacuum=INCREMENTAL` is set at creation and
`PRAGMA incremental_vacuum(200)` runs after trimming).

### 5.1 Size estimates (keep-forever)

Assumptions for one busy 300-issue board: ~1 000 issues pass through per year (≈ 40 per two-week sprint), daily syncs.

| Table | Rows / year | Approx. bytes / row incl. indexes | MB / year |
|---|---|---|---|
| `issue` | 1 000 | 450 | 0.5 |
| `status_change` | 6 000 | 110 | 0.7 |
| `assignee_change` | 2 000 | 120 | 0.2 |
| `flag_change` | 300 | 60 | < 0.1 |
| `comment` | 6 000 | 110 | 0.7 |
| `dev_commit` | 10 000 | 150 | 1.5 |
| `dev_pr_event` | 2 500 | 110 | 0.3 |
| `standup_issue_span` | 4 000 (incl. backlog churn) | 90 | 0.4 |
| `standup_config_history` | ≤ 60 | 4 000 | 0.2 |
| **Subtotal (history, ADR 0011)** | ≈ 32 000 | | **≈ 4–5 MB** |
| `issue_content` (description + environment ADF) | 1 000 | 5 000 (3–8 KB typical) | 5 |
| `comment` body columns (added to the rows above) | 6 000 | + 900 | 5.5 |
| `issue_field_value` | 25 000 (≈ 25 non-empty fields per issue) | 120 | 3 |
| `issue_link` | 3 000 | 200 | 0.6 |
| `attachment` (metadata) | 1 500 | 300 | 0.5 |
| `field_def` | ≈ 300 per connection (one-off) | 400 | 0.1 |
| **Total (history + content, ADR 0015)** | ≈ 70 000 | | **≈ 20 MB per board-year** |

Media is separate and bounded: thumbnails ≈ 15 KB each (1 500 attachments, ~60 % images → ≈ 15 MB per board-year, never
evicted); full-size images ≈ 0.4 MB average, capped at `media.cache_max_mb` (1 GB default) across all standups.

Five such standups ≈ 100 MB of database per year; ten years ≈ 1 GB, which SQLite handles without tuning (the weekly
backup copies the DB only, never the media directory). View
queries are bounded by current membership (live) or by span/time indexes (as-of), so history growth does not slow the
live path; `BenchmarkStandupView` runs against a fixture DB carrying 3 simulated years to prove it.

### 5.2 Optional cap (reserved, not implemented in v1)

`app_setting` key `history_retention_days` (`0` = keep forever, the default and the only behaviour in v1). If a cap is
ever implemented it must delete whole issues only (an issue whose last span closed before the cutoff, with all its
change/comment/dev/content rows and cached media) and close-dated spans/config rows — never partial histories of issues still on a board.

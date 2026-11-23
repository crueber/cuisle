-- 0001_init: data-model.md §2 verbatim. FROZEN CONTRACT; never edit (add a new migration instead).

CREATE TABLE app_setting (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
-- keys: theme, text_size, sync_on_open, sync_stale_hours  (values/defaults: architecture.md §6.4)

CREATE TABLE connection (
  id                    INTEGER PRIMARY KEY,
  source                TEXT NOT NULL DEFAULT 'jira',          -- provider kind; matches source.Source.Kind()
  base_url              TEXT NOT NULL UNIQUE,                  -- https://acme.atlassian.net (no trailing slash)
  account               TEXT NOT NULL,                         -- login (Jira: email); keychain account = <source>:<host>:<account>
  account_id            TEXT,                                  -- provider account id of the connected user
  display_name          TEXT,
  auth_state            TEXT NOT NULL DEFAULT 'unknown' CHECK (auth_state IN ('unknown','ok','invalid','missing')),
  verified_at           INTEGER,
  source_meta           TEXT NOT NULL DEFAULT '{}',            -- Jira: {"flaggedFieldId","sprintFieldId","rankFieldId","fieldsFetchedAt"}
  dev_activity_state    TEXT NOT NULL DEFAULT 'unknown' CHECK (dev_activity_state IN ('unknown','ok','unavailable')),
  dev_activity_checked_at INTEGER,
  created_at            INTEGER NOT NULL
) STRICT;

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

-- Walk order + per-lane limit in one table: exactly 4 rows per standup (todo, prog, review, qa). Done is always last.
CREATE TABLE standup_lane (
  standup_id    INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  lane          TEXT    NOT NULL CHECK (lane IN ('todo','prog','review','qa')),
  walk_position INTEGER NOT NULL CHECK (walk_position BETWEEN 0 AND 3),
  limit_days    INTEGER NOT NULL CHECK (limit_days BETWEEN 0 AND 99),   -- 0 = no limit
  PRIMARY KEY (standup_id, lane)
) STRICT, WITHOUT ROWID;
-- defaults on create: qa(0, limit 3), review(1, 2), prog(2, 5), todo(3, 10)   ["right to left"]

-- Status mapping: Jira board column → lane. Keyed by column name (board config exposes no stable column id).
CREATE TABLE standup_column_map (
  standup_id  INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  column_name TEXT    NOT NULL,
  position    INTEGER NOT NULL,                                -- column order on the Jira board (display order)
  lane        TEXT    NOT NULL CHECK (lane IN ('backlog','todo','prog','review','qa','done','hidden')),
  auto        INTEGER NOT NULL DEFAULT 1 CHECK (auto IN (0,1)),-- 1 = auto-matched, 0 = user-chosen (survives column refresh)
  PRIMARY KEY (standup_id, column_name)
) STRICT, WITHOUT ROWID;

-- Denormalised status ids per mapped column, so view queries need no join into the board cache.
CREATE TABLE standup_status_lane (
  standup_id INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  status_id  TEXT    NOT NULL,
  status_name TEXT   NOT NULL,
  column_name TEXT   NOT NULL,
  lane       TEXT    NOT NULL CHECK (lane IN ('backlog','todo','prog','review','qa','done','hidden')),
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
  updated_at  INTEGER NOT NULL
) STRICT;

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

-- Comments: timestamp + author only. Bodies are never stored.
CREATE TABLE comment (
  issue_id          INTEGER NOT NULL REFERENCES issue(id) ON DELETE CASCADE,
  comment_id        TEXT    NOT NULL,
  author_account_id TEXT,
  created_at        INTEGER NOT NULL,
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
  PRIMARY KEY (issue_id, pr_key, status, at)
) STRICT, WITHOUT ROWID;
CREATE INDEX dev_pr_event_time ON dev_pr_event(issue_id, at);

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

-- 0003: ticket content (F12, ADR 0015; data-model.md §2.5a). Forward-only, additive: no record row is changed or
-- dropped. New tables for descriptions, field values, links, attachments, transitions, drafts and rail preferences;
-- comment bodies and visibility as new comment columns. Standups get needs_sync=1 so the next sync fills the content.

-- Comments, with bodies (data-model.md §2.5). updated_at = created_at until edited ("edited" when greater). ADD COLUMN
-- NOT NULL needs a constant default, so it is added with DEFAULT 0 and backfilled from created_at below.
ALTER TABLE comment ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE comment ADD COLUMN body_adf TEXT;                       -- ADF JSON as received; NULL for rows synced before content existed
ALTER TABLE comment ADD COLUMN visibility_type TEXT CHECK (visibility_type IN ('role','group'));  -- NULL = visible to everyone
ALTER TABLE comment ADD COLUMN visibility_value TEXT;               -- e.g. 'Developers'
ALTER TABLE comment ADD COLUMN deleted_at INTEGER;                  -- no longer returned by the source: hidden, row kept
UPDATE comment SET updated_at = created_at;

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
-- Invalidated (row deleted) whenever the issue's status changes in an apply.
CREATE TABLE transition_cache (
  issue_id   INTEGER PRIMARY KEY REFERENCES issue(id) ON DELETE CASCADE,
  json       TEXT    NOT NULL,                                 -- []model.Transition
  fetched_at INTEGER NOT NULL
) STRICT;

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

-- The next sync fills the content (an absent standup_sync_state row already means "needs sync").
UPDATE standup_sync_state SET needs_sync = 1;

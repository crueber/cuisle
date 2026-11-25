-- cuisle:fk-off
-- 0006: lanes the user names (custom working lanes) and display labels for the built-in lanes (data-model.md §2.2).
-- Forward-only. No record table is touched. Three configuration tables are rebuilt only to widen their lane CHECK
-- constraints (SQLite cannot alter a CHECK); every row is copied unchanged, and lane_order (0002) is kept.
--
-- Custom lane ids are 'c1', 'c2', … per standup, from standup_settings.lane_seq, which only ever grows, so an id is
-- never reused after a delete. A custom lane is a working lane: it has a walk position and a days-in-lane limit.

-- Walk order + per-lane limit + label: the four built-in working lanes plus any custom lanes.
CREATE TABLE standup_lane_new (
  standup_id    INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  lane          TEXT    NOT NULL CHECK (lane IN ('todo','prog','review','qa') OR (lane GLOB 'c[1-9]*' AND lane NOT GLOB 'c*[^0-9]*')),
  walk_position INTEGER NOT NULL CHECK (walk_position >= 0),
  limit_days    INTEGER NOT NULL CHECK (limit_days BETWEEN 0 AND 99),   -- 0 = no limit
  label         TEXT    CHECK (label IS NULL OR length(label) BETWEEN 1 AND 40),  -- NULL = built-in default label
  hue           INTEGER CHECK (hue IS NULL OR hue BETWEEN 1 AND 8),     -- custom lanes: extended palette index
  CHECK ((lane GLOB 'c*') = (hue IS NOT NULL)),         -- only custom lanes have a hue, and they always do
  CHECK (lane NOT GLOB 'c*' OR label IS NOT NULL),      -- custom lanes always have a label
  PRIMARY KEY (standup_id, lane)
) STRICT, WITHOUT ROWID;
INSERT INTO standup_lane_new (standup_id, lane, walk_position, limit_days)
  SELECT standup_id, lane, walk_position, limit_days FROM standup_lane;

CREATE TABLE standup_column_map_new (
  standup_id  INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  column_name TEXT    NOT NULL,
  position    INTEGER NOT NULL,
  lane        TEXT    NOT NULL CHECK (lane IN ('backlog','todo','prog','review','qa','done','hidden') OR (lane GLOB 'c[1-9]*' AND lane NOT GLOB 'c*[^0-9]*')),
  auto        INTEGER NOT NULL DEFAULT 1 CHECK (auto IN (0,1)),
  lane_order  INTEGER,
  PRIMARY KEY (standup_id, column_name)
) STRICT, WITHOUT ROWID;
INSERT INTO standup_column_map_new (standup_id, column_name, position, lane, auto, lane_order)
  SELECT standup_id, column_name, position, lane, auto, lane_order FROM standup_column_map;

CREATE TABLE standup_status_lane_new (
  standup_id  INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  status_id   TEXT    NOT NULL,
  status_name TEXT    NOT NULL,
  column_name TEXT    NOT NULL,
  lane        TEXT    NOT NULL CHECK (lane IN ('backlog','todo','prog','review','qa','done','hidden') OR (lane GLOB 'c[1-9]*' AND lane NOT GLOB 'c*[^0-9]*')),
  PRIMARY KEY (standup_id, status_id),
  FOREIGN KEY (standup_id, column_name) REFERENCES standup_column_map(standup_id, column_name) ON DELETE CASCADE ON UPDATE CASCADE
) STRICT, WITHOUT ROWID;
INSERT INTO standup_status_lane_new (standup_id, status_id, status_name, column_name, lane)
  SELECT standup_id, status_id, status_name, column_name, lane FROM standup_status_lane;

DROP TABLE standup_status_lane;
DROP TABLE standup_column_map;
DROP TABLE standup_lane;
ALTER TABLE standup_lane_new RENAME TO standup_lane;
ALTER TABLE standup_column_map_new RENAME TO standup_column_map;
ALTER TABLE standup_status_lane_new RENAME TO standup_status_lane;

-- The custom-lane id counter (the last id handed out) and the Done lane's display label (NULL = "Done").
ALTER TABLE standup_settings ADD COLUMN lane_seq   INTEGER NOT NULL DEFAULT 0 CHECK (lane_seq >= 0);
ALTER TABLE standup_settings ADD COLUMN done_label TEXT CHECK (done_label IS NULL OR length(done_label) BETWEEN 1 AND 40);

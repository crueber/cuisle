-- 0002: order of board columns within a lane, and speakers removed from a standup.
-- Forward-only, additive: no record data is touched.

-- Order of a column among the columns mapped to the same lane (ConfigService.MoveColumn). NULL = board order; tickets in
-- a lane are grouped by this order, then board rank.
ALTER TABLE standup_column_map ADD COLUMN lane_order INTEGER;

-- People removed from a standup's speaker list (ConfigService.RemoveSpeaker). Sync never re-adds them; adding the person
-- again (AddSpeaker) deletes the row. Cleared when the standup's space or board changes.
CREATE TABLE standup_speaker_removed (
  standup_id INTEGER NOT NULL REFERENCES standup(id) ON DELETE CASCADE,
  account_id TEXT    NOT NULL,
  removed_at INTEGER NOT NULL,
  PRIMARY KEY (standup_id, account_id)
) STRICT, WITHOUT ROWID;

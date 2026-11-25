-- 0007: per-standup zoom of the standup screen content, person view and Whole board (F15; data-model.md §2.1
-- standup_ui_state). Forward-only, additive: no record row is changed or dropped. Existing rows (and standups with no
-- row) read as 100, which is exactly today's screen.
ALTER TABLE standup_ui_state ADD COLUMN zoom INTEGER NOT NULL DEFAULT 100 CHECK (zoom IN (70,80,90,100,110,125,150,175,200));

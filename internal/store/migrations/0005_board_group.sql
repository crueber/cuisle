-- 0005: per-standup grouping of the Whole board, Assignee or Status (F13; data-model.md §2.1 standup_ui_state).
-- Forward-only, additive: no record row is changed or dropped. Existing rows (and standups with no row) read as
-- 'assignee', which is exactly today's board.
ALTER TABLE standup_ui_state ADD COLUMN board_group TEXT NOT NULL DEFAULT 'assignee' CHECK (board_group IN ('assignee','status'));

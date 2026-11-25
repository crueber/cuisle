-- 0008: issue_gen, a counter that moves whenever a row of `issue` is added or removed, or its key, summary or status
-- changes (data-model.md §2.1; the palette's title index is rebuilt only when it moves). Forward-only, additive: no
-- record row is changed or dropped, and the table starts empty (a missing row reads as 0).
-- The counter is bumped by triggers, so every write path is covered (sync apply, the one-issue and delta applies,
-- FetchByKey), and it is read in the same read transaction as the data it guards, so an index built from a snapshot
-- always carries that snapshot's counter. Writes that change no key, summary or status (last_seen_at, removed_at, dev
-- fingerprints) and writes to other tables (settings, recents, runs) do not move it.
CREATE TABLE issue_gen (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  n  INTEGER NOT NULL
);

CREATE TRIGGER issue_gen_insert AFTER INSERT ON issue
BEGIN
  INSERT INTO issue_gen (id, n) VALUES (1, 1) ON CONFLICT (id) DO UPDATE SET n = n + 1;
END;

CREATE TRIGGER issue_gen_update AFTER UPDATE OF key, summary, status_id ON issue
WHEN old.key IS NOT new.key OR old.summary IS NOT new.summary OR old.status_id IS NOT new.status_id
BEGIN
  INSERT INTO issue_gen (id, n) VALUES (1, 1) ON CONFLICT (id) DO UPDATE SET n = n + 1;
END;

CREATE TRIGGER issue_gen_delete AFTER DELETE ON issue
BEGIN
  INSERT INTO issue_gen (id, n) VALUES (1, 1) ON CONFLICT (id) DO UPDATE SET n = n + 1;
END;

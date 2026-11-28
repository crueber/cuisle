package cache

import (
	"context"
	"database/sql"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// Retention windows of data-model.md §5 (operational data only).
const (
	cacheMaxAge   = 30 * 24 * time.Hour
	runMaxAge     = 90 * 24 * time.Hour
	runKeepPerSt  = 50
	outboxMaxAge  = 90 * 24 * time.Hour
	deadMaxAge    = 180 * 24 * time.Hour
	payloadMaxAge = 30 * 24 * time.Hour
)

// UpsertSpaces / UpsertBoards / UpsertPeople refresh the autocomplete caches.
func UpsertSpaces(ctx context.Context, w *sql.DB, connID int64, spaces []model.Space, at time.Time) error {
	return withWriteTx(ctx, w, func(tx *sql.Tx) error {
		for _, s := range spaces {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO space (connection_id, id, key, name, source_meta, fetched_at) VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(connection_id, id) DO UPDATE SET
				  key = excluded.key, name = excluded.name, source_meta = excluded.source_meta, fetched_at = excluded.fetched_at`,
				connID, s.ID, s.Key, s.Name, encodeMap(s.Meta), store.Ms(at))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func UpsertBoards(ctx context.Context, w *sql.DB, connID int64, boards []model.Board, at time.Time) error {
	return withWriteTx(ctx, w, func(tx *sql.Tx) error {
		for _, b := range boards {
			typ := b.RawType
			if typ == "" {
				typ = b.Kind
			}
			_, err := tx.ExecContext(ctx, `
				INSERT INTO board (connection_id, id, name, type, space_id, source_meta, fetched_at) VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(connection_id, id) DO UPDATE SET
				  name = excluded.name, type = excluded.type, space_id = excluded.space_id,
				  source_meta = CASE WHEN excluded.source_meta = '{}' THEN board.source_meta ELSE excluded.source_meta END,
				  fetched_at = excluded.fetched_at`,
				connID, b.ID, b.Name, typ, nullStr(b.SpaceID), encodeMap(b.Meta), store.Ms(at))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func UpsertPeople(ctx context.Context, w *sql.DB, connID int64, people []model.Person, at time.Time) error {
	return withWriteTx(ctx, w, func(tx *sql.Tx) error {
		return ApplyPeopleTx(ctx, tx, connID, people, at)
	})
}

// SearchSpaces / SearchBoards / SearchPeople answer offline from the caches (case-insensitive LIKE on key/name;
// empty query = all, by name), at most limit rows.
func SearchSpaces(ctx context.Context, q *sql.DB, connID int64, query string, limit int) ([]model.Space, error) {
	pat := likeArg(query)
	rows, err := q.QueryContext(ctx, `
		SELECT id, key, name, source_meta FROM space
		 WHERE connection_id = ? AND (key LIKE ? ESCAPE '\' OR name LIKE ? ESCAPE '\')
		 ORDER BY name, id LIMIT ?`, connID, pat, pat, limitArg(limit))
	if err != nil {
		return nil, dbErr(err, "The spaces could not be searched.")
	}
	defer rows.Close()
	out := []model.Space{}
	for rows.Next() {
		var s model.Space
		var meta string
		if err := rows.Scan(&s.ID, &s.Key, &s.Name, &meta); err != nil {
			return nil, dbErr(err, "The spaces could not be searched.")
		}
		s.Meta = decodeMap(meta)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The spaces could not be searched.")
	}
	return out, nil
}

func SearchBoards(ctx context.Context, q *sql.DB, connID int64, spaceKey, query string, limit int) ([]model.Board, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT id, name, type, COALESCE(space_id, ''), source_meta FROM board
		 WHERE connection_id = ?
		   AND (? = '' OR space_id IN (SELECT id FROM space WHERE connection_id = ? AND key = ?))
		   AND name LIKE ? ESCAPE '\'
		 ORDER BY name, id LIMIT ?`, connID, spaceKey, connID, spaceKey, likeArg(query), limitArg(limit))
	if err != nil {
		return nil, dbErr(err, "The boards could not be searched.")
	}
	defer rows.Close()
	out := []model.Board{}
	for rows.Next() {
		var b model.Board
		var meta string
		if err := rows.Scan(&b.ID, &b.Name, &b.RawType, &b.SpaceID, &meta); err != nil {
			return nil, dbErr(err, "The boards could not be searched.")
		}
		b.Kind = b.RawType
		b.Meta = decodeMap(meta)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The boards could not be searched.")
	}
	return out, nil
}

func SearchPeople(ctx context.Context, q *sql.DB, connID int64, query string, limit int) ([]model.Person, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT account_id, display_name, active, COALESCE(account_type, 'user') FROM person
		 WHERE connection_id = ? AND display_name LIKE ? ESCAPE '\'
		 ORDER BY display_name, account_id LIMIT ?`, connID, likeArg(query), limitArg(limit))
	if err != nil {
		return nil, dbErr(err, "The people could not be searched.")
	}
	defer rows.Close()
	out := []model.Person{}
	for rows.Next() {
		var p model.Person
		var active int
		if err := rows.Scan(&p.AccountID, &p.DisplayName, &active, &p.Kind); err != nil {
			return nil, dbErr(err, "The people could not be searched.")
		}
		p.Active = active != 0
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The people could not be searched.")
	}
	return out, nil
}

// doomedRuns selects sync runs past 90 days or beyond the newest 50 of their standup.
const doomedRuns = `
	started_at < ? OR id IN (
	  SELECT id FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY standup_id ORDER BY started_at DESC, id DESC) AS rn
	                    FROM sync_run) WHERE rn > ?)`

// Trim deletes operational rows only (data-model.md §5 table) and runs PRAGMA incremental_vacuum(200).
// Record and config tables are never touched. Standups referenced by a space or board keep it cached.
func Trim(ctx context.Context, w *sql.DB, now time.Time) error {
	cacheCut := store.Ms(now.Add(-cacheMaxAge))
	runCut := store.Ms(now.Add(-runMaxAge))
	payloadCut := store.Ms(now.Add(-payloadMaxAge))
	outboxCut := store.Ms(now.Add(-outboxMaxAge))
	deadCut := store.Ms(now.Add(-deadMaxAge))

	err := withWriteTx(ctx, w, func(tx *sql.Tx) error {
		stmts := []struct {
			q    string
			args []any
		}{
			{`DELETE FROM board_column_status WHERE (connection_id, board_id) IN (
			    SELECT connection_id, id FROM board WHERE fetched_at < ?
			     AND NOT EXISTS (SELECT 1 FROM standup s WHERE s.board_id = board.id))`, []any{cacheCut}},
			{`DELETE FROM board_column WHERE (connection_id, board_id) IN (
			    SELECT connection_id, id FROM board WHERE fetched_at < ?
			     AND NOT EXISTS (SELECT 1 FROM standup s WHERE s.board_id = board.id))`, []any{cacheCut}},
			{`DELETE FROM board WHERE fetched_at < ? AND NOT EXISTS (SELECT 1 FROM standup s WHERE s.board_id = board.id)`,
				[]any{cacheCut}},
			{`DELETE FROM space WHERE fetched_at < ? AND NOT EXISTS (SELECT 1 FROM standup s WHERE s.space_id = space.id)`,
				[]any{cacheCut}},
			{`DELETE FROM sync_log WHERE run_id IN (SELECT id FROM sync_run WHERE ` + doomedRuns + `)`,
				[]any{runCut, runKeepPerSt}},
			{`DELETE FROM sync_run WHERE ` + doomedRuns, []any{runCut, runKeepPerSt}},
			{`UPDATE sink_outbox SET payload = '' WHERE status = 'delivered' AND delivered_at < ? AND payload <> ''`,
				[]any{payloadCut}},
			{`DELETE FROM sink_outbox WHERE (status = 'dead' AND created_at < ?) OR (status <> 'dead' AND created_at < ?)`,
				[]any{deadCut, outboxCut}},
		}
		for _, s := range stmts {
			if _, err := tx.ExecContext(ctx, s.q, s.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if _, err := w.ExecContext(ctx, "PRAGMA incremental_vacuum(200)"); err != nil {
		return dbErr(err, "The database could not be compacted.")
	}
	return nil
}

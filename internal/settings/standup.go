package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

// standupSelect is the column list of a standup row joined with its sync flag. Nullable text is coalesced to "".
const standupSelect = `SELECT s.id, s.connection_id, s.name, s.position,
	COALESCE(s.space_id, ''), COALESCE(s.space_key, ''), COALESCE(s.space_name, ''),
	COALESCE(s.board_id, ''), COALESCE(s.board_name, ''), COALESCE(s.board_type, ''), s.board_meta,
	s.scope, s.extra_filter, s.extra_filter_ok, COALESCE(s.extra_filter_error, ''),
	s.rev, s.created_at, s.updated_at, s.deleted_at, COALESCE(ss.needs_sync, 1)
	FROM standup s LEFT JOIN standup_sync_state ss ON ss.standup_id = s.id`

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanStandup(sc rowScanner) (Standup, error) {
	var (
		st                      Standup
		conn, okFlag, deletedAt sql.NullInt64
		meta                    string
		createdMs, updatedMs    int64
		needs                   int64
	)
	err := sc.Scan(&st.ID, &conn, &st.Name, &st.Position,
		&st.SpaceID, &st.SpaceKey, &st.SpaceName,
		&st.BoardID, &st.BoardName, &st.BoardType, &meta,
		&st.Scope, &st.ExtraFilter, &okFlag, &st.ExtraFilterError,
		&st.Rev, &createdMs, &updatedMs, &deletedAt, &needs)
	if err != nil {
		return Standup{}, err
	}
	st.ConnectionID = conn.Int64
	if okFlag.Valid {
		b := okFlag.Int64 == 1
		st.ExtraFilterOK = &b
		st.FilterCheckedAt = store.FromMs(updatedMs) // contract: updated_at of the last SetFilterCheck
	}
	st.CreatedAt = store.FromMs(createdMs)
	st.UpdatedAt = store.FromMs(updatedMs)
	if deletedAt.Valid {
		st.DeletedAt = store.FromMs(deletedAt.Int64)
	}
	st.NeedsSync = needs == 1
	if meta != "" {
		if err := json.Unmarshal([]byte(meta), &st.BoardMeta); err != nil {
			return Standup{}, apperr.Wrap(apperr.DB, "Could not read settings.", err)
		}
	}
	return st, nil
}

// loadStandupTx reads one standup (archived ones included) or returns not_found.
func loadStandupTx(ctx context.Context, tx *sql.Tx, id int64) (Standup, error) {
	st, err := scanStandup(tx.QueryRowContext(ctx, standupSelect+` WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Standup{}, apperr.New(apperr.NotFound, "That standup no longer exists.")
	}
	if err != nil {
		return Standup{}, dbErr(err)
	}
	return st, nil
}

// loadLiveTx reads one live standup; archived or unknown ids are not_found.
func loadLiveTx(ctx context.Context, tx *sql.Tx, id int64) (Standup, error) {
	st, err := loadStandupTx(ctx, tx, id)
	if err != nil {
		return Standup{}, err
	}
	if !st.DeletedAt.IsZero() {
		return Standup{}, apperr.New(apperr.NotFound, "That standup no longer exists.")
	}
	return st, nil
}

// List returns live standups by position (includeArchived adds soft-deleted ones at the end).
func (s *Store) List(ctx context.Context, includeArchived bool) ([]Standup, error) {
	q := standupSelect + ` WHERE s.deleted_at IS NULL ORDER BY s.position, s.id`
	if includeArchived {
		q = standupSelect + ` ORDER BY (s.deleted_at IS NOT NULL), s.position, s.id`
	}
	var out []Standup
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, q)
		if err != nil {
			return dbErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			st, err := scanStandup(rows)
			if err != nil {
				return dbErr(err)
			}
			out = append(out, st)
		}
		return dbErr(rows.Err())
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Get returns one standup (archived ones included) and its settings.
func (s *Store) Get(ctx context.Context, id int64) (Standup, Settings, error) {
	var (
		st Standup
		se Settings
	)
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		if st, err = loadStandupTx(ctx, tx, id); err != nil {
			return err
		}
		se, err = readSettingsTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return Standup{}, Settings{}, err
	}
	return st, se, nil
}

// Create inserts a draft: name "New standup", position last, default settings (data-model.md §2.3 defaults),
// DefaultLanes, a standup_sync_state row (needs_sync=1), connection_id = the single connection if any.
func (s *Store) Create(ctx context.Context) (int64, error) {
	var id int64
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		now := store.Ms(s.Now())
		var conn sql.NullInt64
		var n int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*), MIN(id) FROM connection`).Scan(&n, &conn); err != nil {
			return dbErr(err)
		}
		if n != 1 {
			conn = sql.NullInt64{}
		}
		var pos int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(position) + 1, 0) FROM standup WHERE deleted_at IS NULL`).Scan(&pos); err != nil {
			return dbErr(err)
		}
		res, err := tx.ExecContext(ctx,
			`INSERT INTO standup (connection_id, name, position, created_at, updated_at) VALUES (?, 'New standup', ?, ?, ?)`,
			conn, pos, now, now)
		if err != nil {
			return dbErr(err)
		}
		if id, err = res.LastInsertId(); err != nil {
			return dbErr(err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO standup_settings (standup_id) VALUES (?)`, id); err != nil {
			return dbErr(err)
		}
		if err := writeLanesTx(ctx, tx, id, DefaultLanes); err != nil {
			return err
		}
		return SetNeedsSyncTx(ctx, tx, id)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// dbErr keeps *apperr.Error values and wraps anything else as a retryable db error.
func dbErr(err error) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	return apperr.Wrap(apperr.DB, "Could not read or save settings.", err)
}

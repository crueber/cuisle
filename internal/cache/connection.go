package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

// GetConnection returns the single connection; none → apperr not_found.
func GetConnection(ctx context.Context, q *sql.DB) (ConnRow, error) {
	var c ConnRow
	var accountID, displayName sql.NullString
	var meta string
	var verified, devChecked, created sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT id, source, base_url, account, account_id, display_name, auth_state, verified_at,
		       source_meta, dev_activity_state, dev_activity_checked_at, created_at
		  FROM connection ORDER BY id LIMIT 1`).
		Scan(&c.ID, &c.Source, &c.BaseURL, &c.Account, &accountID, &displayName, &c.AuthState, &verified,
			&meta, &c.DevActivityState, &devChecked, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return c, apperr.New(apperr.NotFound, "No Jira connection is set up yet.")
	}
	if err != nil {
		return c, dbErr(err, "The connection could not be read.")
	}
	c.AccountID = accountID.String
	c.DisplayName = displayName.String
	c.VerifiedAt = msTime(verified)
	c.SourceMeta = decodeMap(meta)
	c.DevCheckedAt = msTime(devChecked)
	c.CreatedAt = msTime(created)
	return c, nil
}

// SaveConnection stores the connection (v1 keeps exactly one row). Rules (data-model.md section 2.2):
//   - Same base_url: the row is updated in place (id unchanged).
//   - A different base_url while the existing row owns any standup or issue: apperr conflict and nothing is written.
//     Synced history is never rewritten or deleted.
//   - A different base_url while the existing row owns neither: it is replaced (nothing cascades).
//
// Returns the id.
func SaveConnection(ctx context.Context, w *sql.DB, c ConnRow) (int64, error) {
	var id int64
	err := withWriteTx(ctx, w, func(tx *sql.Tx) error {
		var err error
		id, err = saveConnectionTx(ctx, tx, c)
		return err
	})
	return id, err
}

type connKey struct {
	id      int64
	baseURL string
}

func saveConnectionTx(ctx context.Context, tx *sql.Tx, c ConnRow) (int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, base_url FROM connection ORDER BY id")
	if err != nil {
		return 0, err
	}
	var existing []connKey
	for rows.Next() {
		var k connKey
		if err := rows.Scan(&k.id, &k.baseURL); err != nil {
			_ = rows.Close()
			return 0, err
		}
		existing = append(existing, k)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()

	var target int64
	for _, k := range existing {
		if strings.EqualFold(k.baseURL, c.BaseURL) {
			target = k.id
		}
	}
	if target == 0 {
		// Different site: refuse when any existing row owns history, otherwise it may be replaced.
		for _, k := range existing {
			owned, err := ownsData(ctx, tx, k.id)
			if err != nil {
				return 0, err
			}
			if owned {
				return 0, errSwitchRefused(k)
			}
		}
		for _, k := range existing {
			if _, err := tx.ExecContext(ctx, "DELETE FROM connection WHERE id = ?", k.id); err != nil {
				return 0, err
			}
		}
	} else {
		// Same site: any other row cannot exist in v1; leave stray rows that own data alone, drop empty ones.
		for _, k := range existing {
			if k.id == target {
				continue
			}
			owned, err := ownsData(ctx, tx, k.id)
			if err != nil {
				return 0, err
			}
			if !owned {
				if _, err := tx.ExecContext(ctx, "DELETE FROM connection WHERE id = ?", k.id); err != nil {
					return 0, err
				}
			}
		}
	}

	source := c.Source
	if source == "" {
		source = "jira"
	}
	authState := c.AuthState
	if authState == "" {
		authState = "unknown"
	}
	devState := c.DevActivityState
	if devState == "" {
		devState = "unknown"
	}
	meta := encodeMap(c.SourceMeta)

	if target == 0 {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO connection (source, base_url, account, account_id, display_name, auth_state, verified_at,
			                        source_meta, dev_activity_state, dev_activity_checked_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			source, c.BaseURL, c.Account, nullStr(c.AccountID), nullStr(c.DisplayName), authState, nullMs(c.VerifiedAt),
			meta, devState, nullMs(c.DevCheckedAt), store.Ms(c.CreatedAt))
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	// base_url is the match key and stays as stored.
	_, err = tx.ExecContext(ctx, `
		UPDATE connection SET source = ?, account = ?, account_id = ?, display_name = ?,
		       auth_state = ?, verified_at = ?, source_meta = ?, dev_activity_state = ?, dev_activity_checked_at = ?
		 WHERE id = ?`,
		source, c.Account, nullStr(c.AccountID), nullStr(c.DisplayName), authState, nullMs(c.VerifiedAt),
		meta, devState, nullMs(c.DevCheckedAt), target)
	if err != nil {
		return 0, err
	}
	return target, nil
}

func errSwitchRefused(k connKey) error {
	return apperr.New(apperr.Conflict, fmt.Sprintf(
		"Cuisle is connected to %s. Switching to another Jira site isn't supported yet.", hostOf(k.baseURL)))
}

// CheckSiteSwitch is the read-only twin of the refusal inside SaveConnection: it returns the same conflict error when
// saving a connection to baseURL would be refused (a different site while an existing connection owns a standup or an
// issue), and nil otherwise. Callers run it before spending a network call or a keychain write on a refused Connect.
func CheckSiteSwitch(ctx context.Context, r *sql.DB, baseURL string) error {
	const msg = "The connection could not be read."
	rows, err := r.QueryContext(ctx, "SELECT id, base_url FROM connection ORDER BY id")
	if err != nil {
		return dbErr(err, msg)
	}
	var existing []connKey
	for rows.Next() {
		var k connKey
		if err := rows.Scan(&k.id, &k.baseURL); err != nil {
			_ = rows.Close()
			return dbErr(err, msg)
		}
		existing = append(existing, k)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return dbErr(err, msg)
	}
	_ = rows.Close()
	for _, k := range existing {
		if strings.EqualFold(k.baseURL, baseURL) {
			return nil
		}
	}
	for _, k := range existing {
		var owned bool
		err := r.QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM standup WHERE connection_id = ?)
			    OR EXISTS(SELECT 1 FROM issue WHERE connection_id = ?)`, k.id, k.id).Scan(&owned)
		if err != nil {
			return dbErr(err, msg)
		}
		if owned {
			return errSwitchRefused(k)
		}
	}
	return nil
}

// ownsData reports whether the connection owns any standup or any issue row.
func ownsData(ctx context.Context, tx *sql.Tx, connID int64) (bool, error) {
	var owned bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM standup WHERE connection_id = ?)
		    OR EXISTS(SELECT 1 FROM issue WHERE connection_id = ?)`, connID, connID).Scan(&owned)
	return owned, err
}

// hostOf is the host part of a base URL, for messages.
func hostOf(baseURL string) string {
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		return u.Host
	}
	return baseURL
}

// SetAuthState updates auth_state (and verified_at when ok).
func SetAuthState(ctx context.Context, w *sql.DB, connID int64, state string, at time.Time) error {
	var res sql.Result
	var err error
	if state == "ok" {
		res, err = w.ExecContext(ctx, "UPDATE connection SET auth_state = ?, verified_at = ? WHERE id = ?",
			state, nullMs(at), connID)
	} else {
		res, err = w.ExecContext(ctx, "UPDATE connection SET auth_state = ? WHERE id = ?", state, connID)
	}
	if err != nil {
		return dbErr(err, "The sign-in state could not be saved.")
	}
	return requireOneRow(res, "The connection no longer exists.")
}

// SetConnectionMetaTx updates source_meta, dev_activity_state and dev_activity_checked_at.
func SetConnectionMetaTx(ctx context.Context, tx *sql.Tx, connID int64, meta map[string]any, devState string, at time.Time) error {
	if devState == "" {
		devState = "unknown"
	}
	res, err := tx.ExecContext(ctx,
		"UPDATE connection SET source_meta = ?, dev_activity_state = ?, dev_activity_checked_at = ? WHERE id = ?",
		encodeMap(meta), devState, nullMs(at), connID)
	if err != nil {
		return dbErr(err, "The connection could not be saved.")
	}
	return requireOneRow(res, "The connection no longer exists.")
}

// requireOneRow maps an UPDATE that matched nothing to apperr not_found.
func requireOneRow(res sql.Result, msg string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return dbErr(err, "The data could not be saved.")
	}
	if n == 0 {
		return apperr.New(apperr.NotFound, msg)
	}
	return nil
}

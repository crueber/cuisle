package app

import (
	"context"
	"database/sql"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/settings"
)

// deleteContent removes the synced content of a connection (ux-spec §14.11) and keeps every record and history row:
// issues, change tables, comment rows (bodies only are cleared), member spans, dev events and attachment rows stay.
// Standups are flagged as needing a sync so a later reconnect fills the content again. Then the media cache is cleared
// and the database is vacuumed.
func (d *Deps) deleteContent(ctx context.Context, connID int64) error {
	ctx = context.WithoutCancel(ctx)
	mine := `(SELECT id FROM issue WHERE connection_id = ?)`
	err := d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM issue_content WHERE issue_id IN ` + mine,
			`DELETE FROM issue_field_value WHERE issue_id IN ` + mine,
			`DELETE FROM issue_link WHERE issue_id IN ` + mine,
			`DELETE FROM transition_cache WHERE issue_id IN ` + mine,
			`DELETE FROM ticket_draft WHERE issue_id IN ` + mine,
			`UPDATE comment SET body_adf = NULL, visibility_type = NULL, visibility_value = NULL WHERE issue_id IN ` + mine,
		} {
			if _, err := tx.ExecContext(ctx, q, connID); err != nil {
				return apperr.Wrap(apperr.DB, "The synced content could not be deleted.", err)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM standup`)
		if err != nil {
			return apperr.Wrap(apperr.DB, "The synced content could not be deleted.", err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return apperr.Wrap(apperr.DB, "The synced content could not be deleted.", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return apperr.Wrap(apperr.DB, "The synced content could not be deleted.", err)
		}
		for _, id := range ids {
			if err := settings.SetNeedsSyncTx(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if d.Media != nil {
		if err := d.Media.Clear(ctx); err != nil {
			return err
		}
	}
	if _, err := d.DB.W.ExecContext(ctx, `VACUUM`); err != nil {
		d.log().Warn("vacuum after deleting content failed", "err", err)
	}
	return nil
}

package cache

import (
	"context"
	"database/sql"
	"errors"

	"github.com/crueber/cuisle/internal/apperr"
)

// FieldsInUseTx lists the fields that have a value on at least one OTHER issue of the same space (one DISTINCT query):
// the custom fields worth listing as "Not set" on this ticket. A site-wide registry holds fields that are not on the
// project's screens at all; a field nobody in the space ever filled in is noise. The result is returned beside
// cache.Ticket, not on it, because Ticket belongs to the frozen contract; the caller hands it to
// view.TicketOptions.FieldsInUse (read it in the same reader transaction as LoadTicketTx). Unknown issue → apperr
// not_found.
func FieldsInUseTx(ctx context.Context, tx *sql.Tx, issueID int64) (map[string]bool, error) {
	var connID int64
	var space string
	err := tx.QueryRowContext(ctx, `SELECT connection_id, space_id FROM issue WHERE id = ?`, issueID).Scan(&connID, &space)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.New(apperr.NotFound, "This ticket is not in Cuisle.")
	}
	if err != nil {
		return nil, dbErr(err, "The ticket could not be read.")
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT v.field_id
		  FROM issue_field_value v JOIN issue i ON i.id = v.issue_id
		 WHERE i.connection_id = ? AND i.space_id = ? AND i.id <> ?`, connID, space, issueID)
	if err != nil {
		return nil, dbErr(err, "The field usage could not be read.")
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, dbErr(err, "The field usage could not be read.")
		}
		out[id] = true
	}
	return out, dbErr(rows.Err(), "The field usage could not be read.")
}

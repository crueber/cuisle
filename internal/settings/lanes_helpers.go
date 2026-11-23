package settings

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

// notFoundStandup is the error for a missing or archived standup.
func notFoundStandup() error {
	return apperr.New(apperr.NotFound, "That standup no longer exists.")
}

// dbErr and ackTx are shared with the WP-16 files (standup.go, standup_mutate.go).

// standupBoardTx checks that a standup is live (not_found otherwise) and returns its connection and board ids.
// A draft has no board, so boardID is NULL.
func standupBoardTx(ctx context.Context, tx *sql.Tx, id int64) (connID sql.NullInt64, boardID sql.NullString, err error) {
	err = tx.QueryRowContext(ctx,
		`SELECT connection_id, board_id FROM standup WHERE id = ? AND deleted_at IS NULL`, id).Scan(&connID, &boardID)
	if errors.Is(err, sql.ErrNoRows) {
		return connID, boardID, notFoundStandup()
	}
	if err != nil {
		return connID, boardID, dbErr(err)
	}
	return connID, boardID, nil
}

// writeStandup runs fn in one write transaction after checking the standup is live, bumps its rev and returns the
// Ack. An error from fn rolls everything back, including the rev bump.
func (s *Store) writeStandup(ctx context.Context, id int64, fn func(tx *sql.Tx) error) (Ack, error) {
	var ack Ack
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := standupBoardTx(ctx, tx, id); err != nil {
			return err
		}
		if err := fn(tx); err != nil {
			return err
		}
		rev, err := BumpRevTx(ctx, tx, id, s.Now())
		if err != nil {
			return err
		}
		ack, err = s.ackTx(ctx, tx, id, rev)
		return err
	})
	return ack, err
}

// isWorkingLane reports whether lane is one of the four walk lanes.
func isWorkingLane(lane string) bool {
	return slices.Contains(model.WorkingLanes, lane)
}

// validWalkOrder reports whether lanes is a permutation of the four working lanes.
func validWalkOrder(lanes []string) bool {
	if len(lanes) != len(model.WorkingLanes) {
		return false
	}
	seen := make(map[string]bool, len(lanes))
	for _, lane := range lanes {
		if !isWorkingLane(lane) || seen[lane] {
			return false
		}
		seen[lane] = true
	}
	return true
}

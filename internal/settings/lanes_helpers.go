package settings

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"

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

// validWalkOrder reports whether lanes is a permutation of the working lanes in cur.
func validWalkOrder(cur []model.LaneConfig, lanes []string) bool {
	if len(lanes) != len(cur) {
		return false
	}
	want := make(map[string]bool, len(cur))
	for _, l := range cur {
		want[l.Lane] = true
	}
	seen := make(map[string]bool, len(lanes))
	for _, lane := range lanes {
		if !want[lane] || seen[lane] {
			return false
		}
		seen[lane] = true
	}
	return true
}

// customLaneID is the id of the custom lane with sequence number seq.
func customLaneID(seq int) string { return model.CustomLanePrefix + strconv.Itoa(seq) }

// customLaneSeq is the sequence number of a custom lane id (0 when lane is not one).
func customLaneSeq(lane string) int {
	if !model.IsCustomLane(lane) {
		return 0
	}
	n, _ := strconv.Atoi(lane[len(model.CustomLanePrefix):])
	return n
}

// customLaneIDs lists the custom lanes among lanes in creation order.
func customLaneIDs(lanes []model.LaneConfig) []string {
	var out []string
	for _, l := range lanes {
		if model.IsCustomLane(l.Lane) {
			out = append(out, l.Lane)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return customLaneSeq(a) - customLaneSeq(b) })
	return out
}

// laneExists reports whether lane is a lane a column of this standup can map to: a built-in lane id or one of its
// custom lanes (lanes are the standup's working lanes).
func laneExists(lanes []model.LaneConfig, lane string) bool {
	return model.EffectiveConfig{Lanes: lanes}.HasLane(lane)
}

// laneExistsTx is laneExists for the standup's current lanes.
func laneExistsTx(ctx context.Context, tx *sql.Tx, id int64, lane string) (bool, error) {
	if !model.IsCustomLane(lane) {
		return laneExists(nil, lane), nil
	}
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM standup_lane WHERE standup_id = ? AND lane = ?`, id, lane).Scan(&n); err != nil {
		return false, dbErr(err)
	}
	return n == 1, nil
}

// mergeBuiltinLanes returns target's lanes with the built-in lanes taken from source: the built-in walk slots of
// target are filled with source's built-in lanes in source's walk order (limits and labels from source); custom lanes
// keep their slots. Positions are rewritten densely.
func mergeBuiltinLanes(target, source []model.LaneConfig) []model.LaneConfig {
	var src []model.LaneConfig
	for _, l := range source {
		if !model.IsCustomLane(l.Lane) {
			src = append(src, l)
		}
	}
	out := make([]model.LaneConfig, 0, len(target))
	next := 0
	for _, l := range target {
		if !model.IsCustomLane(l.Lane) && next < len(src) {
			l = src[next]
			next++
		}
		l.Position = len(out)
		out = append(out, l)
	}
	return out
}

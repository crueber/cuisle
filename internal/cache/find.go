package cache

// Reads for the palette's ticket finder (F14, ux-spec §15.3) and the `@` people list. All local: one reader
// transaction, no network (AGENTS invariant 2). The matching itself lives in internal/keyfind; this file only narrows
// the candidates with SQL and loads the facts a hit row shows.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// FindKeyRow is one candidate issue of a finder query.
type FindKeyRow struct {
	ID  int64
	Key string
}

// FindKeysTx (a covering scan of the unique (connection, key) index) lists the connection's issues whose key starts with the (upper-cased) letters and whose number part has at
// least minDigits digits (an index range over the key, then a cheap length test) and that accept approves. accept sees
// the key's bytes (valid only during the call) so that rows that do not match cost no allocation. Every issue counts:
// Done ones, ones on no board, and ones fetched on their own.
func FindKeysTx(ctx context.Context, tx *sql.Tx, connID int64, letters string, minDigits int, digits string, accept func(key []byte) bool) ([]FindKeyRow, error) {
	q := `SELECT id, key FROM issue WHERE connection_id = ?`
	args := []any{connID}
	if letters != "" {
		lo := strings.ToUpper(letters)
		hi := lo[:len(lo)-1] + string(lo[len(lo)-1]+1)
		q += ` AND key >= ? AND key < ?`
		args = append(args, lo, hi)
	}
	// A number can match when it starts with the digits (exact, starts-with) or has exactly their length (close). Keys
	// hold one '-', so key LIKE '%-D%' is "the number starts with D". Filtering here keeps non-candidates from crossing
	// into Go, which is most of the cost of a one or two digit query.
	if digits != "" {
		q += ` AND (key LIKE ? OR length(key) - instr(key, '-') = ?)`
		args = append(args, "%-"+digits+"%", len(digits))
	} else {
		q += ` AND length(key) - instr(key, '-') >= ?`
		args = append(args, minDigits)
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, dbErr(err, "The tickets could not be searched.")
	}
	defer rows.Close()
	var out []FindKeyRow
	for rows.Next() {
		var id int64
		var key sql.RawBytes
		if err := rows.Scan(&id, &key); err != nil {
			return nil, dbErr(err, "The tickets could not be searched.")
		}
		if accept != nil && !accept(key) {
			continue
		}
		out = append(out, FindKeyRow{ID: id, Key: string(key)})
	}
	return out, dbErr(rows.Err(), "The tickets could not be searched.")
}

// FindContext is the small, connection-wide lookup that orders finder hits.
type FindContext struct {
	// Members: issue id → the live (not archived, configured) standups it is a current member of, in tab order.
	Members map[int64][]int64
	// DoneStatuses: standup id → status ids its lane mapping puts in done.
	DoneStatuses map[int64]map[string]bool
	// DoneCategory: status ids whose provider category is done (the fallback for statuses a standup does not map).
	DoneCategory map[string]bool
	// Tabs: live configured standup ids in tab order.
	Tabs []int64
	// Revs: standup id → its current rev (a cache key for the standup's lane mapping).
	Revs map[int64]int64
}

// LoadFindContextTx reads FindContext for a connection.
func LoadFindContextTx(ctx context.Context, tx *sql.Tx, connID int64) (FindContext, error) {
	fc := FindContext{Members: map[int64][]int64{}, DoneStatuses: map[int64]map[string]bool{}, DoneCategory: map[string]bool{}, Revs: map[int64]int64{}}
	rows, err := tx.QueryContext(ctx,
		`SELECT s.id, s.rev FROM standup s WHERE s.deleted_at IS NULL AND s.board_id IS NOT NULL ORDER BY s.position, s.id`)
	if err != nil {
		return fc, dbErr(err, "The standups could not be read.")
	}
	for rows.Next() {
		var id, rev int64
		if err := rows.Scan(&id, &rev); err != nil {
			_ = rows.Close()
			return fc, dbErr(err, "The standups could not be read.")
		}
		fc.Tabs = append(fc.Tabs, id)
		fc.Revs[id] = rev
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return fc, dbErr(err, "The standups could not be read.")
	}

	rows, err = tx.QueryContext(ctx,
		`SELECT si.issue_id, si.standup_id
		   FROM standup_issue si
		   JOIN standup s ON s.id = si.standup_id AND s.deleted_at IS NULL AND s.board_id IS NOT NULL
		   JOIN issue i ON i.id = si.issue_id AND i.connection_id = ?
		  ORDER BY s.position, s.id`, connID)
	if err != nil {
		return fc, dbErr(err, "The standups could not be read.")
	}
	for rows.Next() {
		var issue, standup int64
		if err := rows.Scan(&issue, &standup); err != nil {
			_ = rows.Close()
			return fc, dbErr(err, "The standups could not be read.")
		}
		fc.Members[issue] = append(fc.Members[issue], standup)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return fc, dbErr(err, "The standups could not be read.")
	}

	rows, err = tx.QueryContext(ctx, `SELECT standup_id, status_id FROM standup_status_lane WHERE lane = 'done'`)
	if err != nil {
		return fc, dbErr(err, "The lane mapping could not be read.")
	}
	for rows.Next() {
		var standup int64
		var status string
		if err := rows.Scan(&standup, &status); err != nil {
			_ = rows.Close()
			return fc, dbErr(err, "The lane mapping could not be read.")
		}
		if fc.DoneStatuses[standup] == nil {
			fc.DoneStatuses[standup] = map[string]bool{}
		}
		fc.DoneStatuses[standup][status] = true
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return fc, dbErr(err, "The lane mapping could not be read.")
	}

	rows, err = tx.QueryContext(ctx, `SELECT id FROM status WHERE connection_id = ? AND category = 'done'`, connID)
	if err != nil {
		return fc, dbErr(err, "The statuses could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fc, dbErr(err, "The statuses could not be read.")
		}
		fc.DoneCategory[id] = true
	}
	return fc, dbErr(rows.Err(), "The statuses could not be read.")
}

// HitFact is what a finder hit row needs of one issue, before it is placed in a standup's lane mapping.
type HitFact struct {
	LocalID        int64
	Key            string
	Summary        string
	TypeKind       string
	StatusID       string
	StatusName     string // the provider's status name (a standup's own mapping name wins in the builder)
	StatusCategory string // new|indeterminate|done|undefined
	AssigneeID     string
	AssigneeName   string
	Flagged        bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StatusChanges  []model.StatusChange
}

// LoadHitFactsTx loads the facts of the given issues (unknown ids are absent from the result).
func LoadHitFactsTx(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]HitFact, error) {
	out := make(map[int64]HitFact, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	in := placeholders(len(ids))
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id, i.key, i.summary, COALESCE(ty.kind, 'other'), i.status_id, COALESCE(st.name, ''), COALESCE(st.category, 'undefined'),
		       COALESCE(i.assignee_account_id, ''), COALESCE(p.display_name, ''), i.flagged, i.created_at, i.updated_at
		  FROM issue i
		  LEFT JOIN issue_type ty ON ty.connection_id = i.connection_id AND ty.id = i.issue_type_id
		  LEFT JOIN status st ON st.connection_id = i.connection_id AND st.id = i.status_id
		  LEFT JOIN person p ON p.connection_id = i.connection_id AND p.account_id = i.assignee_account_id
		 WHERE i.id IN (`+in+`)`, args...)
	if err != nil {
		return nil, dbErr(err, "The tickets could not be read.")
	}
	for rows.Next() {
		var f HitFact
		var flagged int
		var created, updated int64
		if err := rows.Scan(&f.LocalID, &f.Key, &f.Summary, &f.TypeKind, &f.StatusID, &f.StatusName, &f.StatusCategory,
			&f.AssigneeID, &f.AssigneeName, &flagged, &created, &updated); err != nil {
			_ = rows.Close()
			return nil, dbErr(err, "The tickets could not be read.")
		}
		f.Flagged = flagged != 0
		f.CreatedAt, f.UpdatedAt = store.FromMs(created), store.FromMs(updated)
		out[f.LocalID] = f
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, dbErr(err, "The tickets could not be read.")
	}

	rows, err = tx.QueryContext(ctx,
		`SELECT issue_id, history_id, changed_at, from_status_id, to_status_id FROM status_change
		  WHERE issue_id IN (`+in+`) ORDER BY issue_id, changed_at, history_id`, args...)
	if err != nil {
		return nil, dbErr(err, "The status history could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var id, at int64
		var c model.StatusChange
		if err := rows.Scan(&id, &c.ID, &at, &c.From, &c.To); err != nil {
			return nil, dbErr(err, "The status history could not be read.")
		}
		c.At = store.FromMs(at)
		f := out[id]
		f.StatusChanges = append(f.StatusChanges, c)
		out[id] = f
	}
	return out, dbErr(rows.Err(), "The status history could not be read.")
}

// PersonRow is one speaker of one standup (the `@` list).
type PersonRow struct {
	StandupID   int64
	AccountID   string
	DisplayName string
}

// SpeakersTx lists the included speakers of every live configured standup, in tab order then speaking order.
func SpeakersTx(ctx context.Context, tx *sql.Tx) ([]PersonRow, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT sp.standup_id, sp.account_id, sp.display_name
		   FROM standup_speaker sp JOIN standup s ON s.id = sp.standup_id
		  WHERE sp.included = 1 AND s.deleted_at IS NULL AND s.board_id IS NOT NULL
		  ORDER BY s.position, s.id, sp.position`)
	if err != nil {
		return nil, dbErr(err, "The speakers could not be read.")
	}
	defer rows.Close()
	var out []PersonRow
	for rows.Next() {
		var r PersonRow
		if err := rows.Scan(&r.StandupID, &r.AccountID, &r.DisplayName); err != nil {
			return nil, dbErr(err, "The speakers could not be read.")
		}
		out = append(out, r)
	}
	return out, dbErr(rows.Err(), "The speakers could not be read.")
}

// ActiveByAssigneeTx counts, per standup and assignee, the board tickets whose lane in that standup is a working lane
// (not done, hidden or backlog). The assignee "" is the unassigned.
func ActiveByAssigneeTx(ctx context.Context, tx *sql.Tx) (map[int64]map[string]int, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT si.standup_id, COALESCE(i.assignee_account_id, ''), count(*)
		   FROM standup_issue si
		   JOIN standup s ON s.id = si.standup_id AND s.deleted_at IS NULL AND s.board_id IS NOT NULL
		   JOIN issue i ON i.id = si.issue_id
		   JOIN standup_status_lane sl ON sl.standup_id = si.standup_id AND sl.status_id = i.status_id
		  WHERE si.role = 'board' AND sl.lane NOT IN ('done', 'hidden', 'backlog')
		  GROUP BY si.standup_id, i.assignee_account_id`)
	if err != nil {
		return nil, dbErr(err, "The ticket counts could not be read.")
	}
	defer rows.Close()
	out := map[int64]map[string]int{}
	for rows.Next() {
		var standup int64
		var account string
		var n int
		if err := rows.Scan(&standup, &account, &n); err != nil {
			return nil, dbErr(err, "The ticket counts could not be read.")
		}
		if out[standup] == nil {
			out[standup] = map[string]int{}
		}
		out[standup][account] = n
	}
	return out, dbErr(rows.Err(), "The ticket counts could not be read.")
}

// StatusIDsTx returns the current status id of each issue: one query, the ids passed as a JSON array (json_each) so a
// thousand candidates cost one bound parameter.
func StatusIDsTx(ctx context.Context, tx *sql.Tx, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	arr, err := json.Marshal(ids)
	if err != nil {
		return nil, dbErr(err, "The tickets could not be read.")
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, status_id FROM issue WHERE id IN (SELECT value FROM json_each(?))`, string(arr))
	if err != nil {
		return nil, dbErr(err, "The tickets could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, dbErr(err, "The tickets could not be read.")
		}
		out[id] = st
	}
	return out, dbErr(rows.Err(), "The tickets could not be read.")
}

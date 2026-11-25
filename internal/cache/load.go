package cache

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// LoadCurrentTx builds the live snapshot of a standup (Q1–Q5, data-model.md §4.1) inside tx, as of asOf:
// Members = board role (lanes not hidden/backlog) in rank order then backlog role in rank order; History has full status
// history and flag changes, and comments/commits/PR events ≥ the pulse window start (asOf, cfg.PulseDays); People,
// Statuses, Sprints (active per standup_sync_state.active_sprint_ids), BacklogTotal, DevActivity, LastSyncedAt and the
// last finished run's Warnings. IssueURLPrefix is left "" (the app sets it). Live = true.
func LoadCurrentTx(ctx context.Context, tx *sql.Tx, cfg model.EffectiveConfig, asOf time.Time) (model.BoardSnapshot, error) {
	snap := model.BoardSnapshot{
		StandupID:    cfg.StandupID,
		AsOf:         asOf,
		Live:         true,
		Config:       cfg,
		Members:      []model.Member{},
		History:      map[string]model.IssueHistory{},
		People:       map[string]model.Person{},
		Statuses:     map[string]model.Status{},
		Sprints:      []model.Sprint{},
		BacklogTotal: -1,
		DevActivity:  "unknown",
		Warnings:     []string{},
	}

	// connID is 0 when the standup has no connection (no rows match then).
	var connNull sql.NullInt64
	err := tx.QueryRowContext(ctx,
		`SELECT s.connection_id, COALESCE(c.dev_activity_state, 'unknown')
		   FROM standup s LEFT JOIN connection c ON c.id = s.connection_id
		  WHERE s.id = ?`, cfg.StandupID).Scan(&connNull, &snap.DevActivity)
	if errors.Is(err, sql.ErrNoRows) {
		return snap, apperr.New(apperr.NotFound, "This standup no longer exists.")
	}
	if err != nil {
		return snap, dbErr(err, "The standup could not be read.")
	}
	connID := connNull.Int64

	members, sourceByLocal, err := loadMembers(ctx, tx, cfg.StandupID)
	if err != nil {
		return snap, err
	}
	snap.Members = members

	snap.History, err = loadHistory(ctx, tx, cfg.StandupID, pulseWindowStart(asOf, cfg.PulseDays), sourceByLocal)
	if err != nil {
		return snap, err
	}
	snap.People, err = loadPeople(ctx, tx, connID, cfg.StandupID)
	if err != nil {
		return snap, err
	}
	snap.Statuses, err = loadStatuses(ctx, tx, connID, cfg.StandupID)
	if err != nil {
		return snap, err
	}

	st, err := loadSyncState(ctx, tx, cfg.StandupID)
	if err != nil {
		return snap, err
	}
	snap.BacklogTotal = st.BacklogTotal
	snap.LastSyncedAt = st.LastSuccessAt

	snap.Sprints, err = loadActiveSprints(ctx, tx, connID, st.ActiveSprintIDs)
	if err != nil {
		return snap, err
	}
	snap.Warnings, err = loadLastWarnings(ctx, tx, cfg.StandupID)
	if err != nil {
		return snap, err
	}
	return snap, nil
}

// pulseWindowStart is local midnight, in asOf's location, of (asOf's local date − (days − 1)). Calendar arithmetic
// (time.Date normalises the day), so it is DST-safe.
func pulseWindowStart(asOf time.Time, days int) time.Time {
	if days < 1 {
		days = 1
	}
	y, m, d := asOf.Date()
	return time.Date(y, m, d-(days-1), 0, 0, 0, 0, asOf.Location())
}

// loadMembers runs Q1 (board role, lane not hidden/backlog) and Q5 (backlog role), each in rank order. It also returns
// the source id of every member by local id, which the history loader keys on.
func loadMembers(ctx context.Context, tx *sql.Tx, standupID int64) ([]model.Member, map[int64]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id, i.source_id, i.key, i.summary, i.space_id, i.issue_type_id, i.status_id,
		       i.assignee_account_id, i.flagged, i.rank, i.parent_key, i.sprint_ids,
		       i.created_at, i.updated_at, i.resolved_at, i.source_meta,
		       COALESCE(t.kind, ''), COALESCE(sl.status_name, ''), si.role, si.rank_index
		  FROM standup_issue si
		  JOIN issue i ON i.id = si.issue_id
		  LEFT JOIN standup_status_lane sl ON sl.standup_id = si.standup_id AND sl.status_id = i.status_id
		  LEFT JOIN issue_type t ON t.connection_id = i.connection_id AND t.id = i.issue_type_id
		 WHERE si.standup_id = ?
		   AND ((si.role = 'board' AND sl.lane NOT IN ('hidden', 'backlog')) OR si.role = 'backlog')
		 ORDER BY CASE si.role WHEN 'board' THEN 0 ELSE 1 END, si.rank_index, i.id`, standupID)
	if err != nil {
		return nil, nil, dbErr(err, "The tickets could not be read.")
	}
	defer rows.Close()

	members := []model.Member{}
	sourceByLocal := map[int64]string{}
	for rows.Next() {
		var (
			m                      model.Member
			is                     model.Issue
			assignee, rank, parent sql.NullString
			sprintIDs, sourceMeta  string
			flagged                int
			created, updated       int64
			resolved               sql.NullInt64
			statusName             string
		)
		if err := rows.Scan(&m.LocalID, &is.SourceID, &is.Key, &is.Summary, &is.SpaceID, &is.TypeID, &is.StatusID,
			&assignee, &flagged, &rank, &parent, &sprintIDs, &created, &updated, &resolved, &sourceMeta,
			&m.TypeKind, &statusName, &m.Role, &m.RankIndex); err != nil {
			return nil, nil, dbErr(err, "The tickets could not be read.")
		}
		is.AssigneeID = assignee.String
		is.Flagged = flagged != 0
		is.Rank = rank.String
		is.ParentKey = parent.String
		is.SprintIDs = decodeStrings(sprintIDs)
		is.CreatedAt = store.FromMs(created)
		is.UpdatedAt = store.FromMs(updated)
		is.ResolvedAt = msTime(resolved)
		is.Meta = decodeMap(sourceMeta)
		m.Issue = is
		m.StatusName = statusName
		members = append(members, m)
		sourceByLocal[m.LocalID] = is.SourceID
	}
	if err := rows.Err(); err != nil {
		return nil, nil, dbErr(err, "The tickets could not be read.")
	}
	return members, sourceByLocal, nil
}

// loadPeople returns the people referenced by the standup: assignees, comment authors and speakers.
func loadPeople(ctx context.Context, tx *sql.Tx, connID, standupID int64) (map[string]model.Person, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT p.account_id, p.display_name, p.active, COALESCE(p.account_type, 'user')
		  FROM person p
		 WHERE p.connection_id = ? AND p.account_id IN (
		       SELECT i.assignee_account_id FROM issue i JOIN standup_issue si ON si.issue_id = i.id
		        WHERE si.standup_id = ? AND i.assignee_account_id IS NOT NULL
		       UNION
		       SELECT c.author_account_id FROM comment c JOIN standup_issue si ON si.issue_id = c.issue_id
		        WHERE si.standup_id = ? AND c.author_account_id IS NOT NULL
		       UNION
		       SELECT account_id FROM standup_speaker WHERE standup_id = ?)`,
		connID, standupID, standupID, standupID)
	if err != nil {
		return nil, dbErr(err, "The people could not be read.")
	}
	defer rows.Close()
	out := map[string]model.Person{}
	for rows.Next() {
		var p model.Person
		var active int
		if err := rows.Scan(&p.AccountID, &p.DisplayName, &active, &p.Kind); err != nil {
			return nil, dbErr(err, "The people could not be read.")
		}
		p.Active = active != 0
		out[p.AccountID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The people could not be read.")
	}
	return out, nil
}

// loadStatuses returns the statuses the standup maps, the members are in, or the history moved through.
func loadStatuses(ctx context.Context, tx *sql.Tx, connID, standupID int64) (map[string]model.Status, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.id, s.name, s.category, s.blocked_hint
		  FROM status s
		 WHERE s.connection_id = ? AND (
		       s.id IN (SELECT status_id FROM standup_status_lane WHERE standup_id = ?)
		    OR s.id IN (SELECT i.status_id FROM issue i JOIN standup_issue si ON si.issue_id = i.id WHERE si.standup_id = ?)
		    OR s.id IN (SELECT c.from_status_id FROM status_change c JOIN standup_issue si ON si.issue_id = c.issue_id WHERE si.standup_id = ?)
		    OR s.id IN (SELECT c.to_status_id FROM status_change c JOIN standup_issue si ON si.issue_id = c.issue_id WHERE si.standup_id = ?))`,
		connID, standupID, standupID, standupID, standupID)
	if err != nil {
		return nil, dbErr(err, "The statuses could not be read.")
	}
	defer rows.Close()
	out := map[string]model.Status{}
	for rows.Next() {
		var s model.Status
		var hint int
		if err := rows.Scan(&s.ID, &s.Name, &s.Category, &hint); err != nil {
			return nil, dbErr(err, "The statuses could not be read.")
		}
		s.BlockedHint = hint != 0
		out[s.ID] = s
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The statuses could not be read.")
	}
	return out, nil
}

// loadActiveSprints returns the sprints named by the sync state's active sprint ids (never all active sprints).
func loadActiveSprints(ctx context.Context, tx *sql.Tx, connID int64, ids []string) ([]model.Sprint, error) {
	out := []model.Sprint{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{connID}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, board_id, name, state, start_at, end_at, complete_at
		  FROM sprint
		 WHERE connection_id = ? AND id IN (`+placeholders(len(ids))+`)
		 ORDER BY start_at, id`, args...)
	if err != nil {
		return nil, dbErr(err, "The sprints could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var s model.Sprint
		var start, end, complete sql.NullInt64
		if err := rows.Scan(&s.ID, &s.BoardID, &s.Name, &s.State, &start, &end, &complete); err != nil {
			return nil, dbErr(err, "The sprints could not be read.")
		}
		s.StartAt, s.EndAt, s.CompleteAt = msTime(start), msTime(end), msTime(complete)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The sprints could not be read.")
	}
	return out, nil
}

// loadLastWarnings returns the warnings of the newest finished run of the standup (running runs are ignored).
func loadLastWarnings(ctx context.Context, tx *sql.Tx, standupID int64) ([]string, error) {
	var warnings string
	err := tx.QueryRowContext(ctx, `
		SELECT warnings FROM sync_run
		 WHERE standup_id = ? AND status <> 'running'
		 ORDER BY started_at DESC, id DESC LIMIT 1`, standupID).Scan(&warnings)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, dbErr(err, "The sync warnings could not be read.")
	}
	return decodeStrings(warnings), nil
}

// Tabs is Q6 for live standups by position.
func Tabs(ctx context.Context, q *sql.DB) ([]TabRow, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT s.id, s.name, COALESCE(s.space_key, ''), s.position, s.rev, s.board_id IS NOT NULL,
		       COALESCE(ss.needs_sync, 1), ss.last_success_at,
		       (SELECT count(*) FROM standup_issue si
		          JOIN issue i ON i.id = si.issue_id
		          JOIN standup_status_lane sl ON sl.standup_id = si.standup_id AND sl.status_id = i.status_id
		         WHERE si.standup_id = s.id AND si.role = 'board' AND sl.lane NOT IN ('done','backlog','hidden')) AS active_count,
		       (SELECT count(*) FROM standup_speaker sp WHERE sp.standup_id = s.id AND sp.included = 1) AS speaker_count
		  FROM standup s LEFT JOIN standup_sync_state ss ON ss.standup_id = s.id
		 WHERE s.deleted_at IS NULL
		 ORDER BY s.position, s.id`)
	if err != nil {
		return nil, dbErr(err, "The standups could not be read.")
	}
	defer rows.Close()
	out := []TabRow{}
	for rows.Next() {
		var t TabRow
		var configured, needs int
		var lastSuccess sql.NullInt64
		if err := rows.Scan(&t.ID, &t.Name, &t.SpaceKey, &t.Position, &t.Rev, &configured, &needs, &lastSuccess,
			&t.ActiveCount, &t.SpeakerCount); err != nil {
			return nil, dbErr(err, "The standups could not be read.")
		}
		t.Configured = configured != 0
		t.NeedsSync = needs != 0
		t.LastSuccessAt = msTime(lastSuccess)
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The standups could not be read.")
	}
	return out, nil
}

// GetSyncState reads standup_sync_state (absent → NeedsSync true, BacklogTotal -1).
func GetSyncState(ctx context.Context, q *sql.DB, standupID int64) (SyncState, error) {
	return loadSyncState(ctx, q, standupID)
}

// loadSyncState is GetSyncState on any querier (used inside the snapshot transaction).
func loadSyncState(ctx context.Context, q querier, standupID int64) (SyncState, error) {
	st := SyncState{StandupID: standupID, NeedsSync: true, BacklogTotal: -1, ActiveSprintIDs: []string{}}
	var needs int
	var attempt, success, full, from sql.NullInt64
	var backlog int64
	var sprints string
	err := q.QueryRowContext(ctx, `
		SELECT needs_sync, last_attempt_at, last_success_at, last_full_at, history_from, backlog_total, active_sprint_ids
		  FROM standup_sync_state WHERE standup_id = ?`, standupID).
		Scan(&needs, &attempt, &success, &full, &from, &backlog, &sprints)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, dbErr(err, "The sync state could not be read.")
	}
	st.NeedsSync = needs != 0
	st.LastAttemptAt = msTime(attempt)
	st.LastSuccessAt = msTime(success)
	st.LastFullAt = msTime(full)
	st.HistoryFrom = msTime(from)
	st.BacklogTotal = int(backlog)
	st.ActiveSprintIDs = decodeStrings(sprints)
	return st, nil
}

// stampChunk bounds the number of host parameters in one IssueStamps query.
const stampChunk = 500

// IssueStamps returns cached stamps keyed by source id for the given ids (missing ids are absent).
func IssueStamps(ctx context.Context, q *sql.DB, connID int64, sourceIDs []string) (map[string]IssueStamp, error) {
	out := map[string]IssueStamp{}
	for start := 0; start < len(sourceIDs); start += stampChunk {
		chunk := sourceIDs[start:min(start+stampChunk, len(sourceIDs))]
		args := []any{connID}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := q.QueryContext(ctx, `
			SELECT i.source_id, i.id, i.updated_at, COALESCE(i.dev_fingerprint, ''), i.dev_checked_at,
			       (SELECT MAX(c.changed_at) FROM status_change c WHERE c.issue_id = i.id)
			  FROM issue i
			 WHERE i.connection_id = ? AND i.source_id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, dbErr(err, "The issue stamps could not be read.")
		}
		for rows.Next() {
			var sourceID string
			var st IssueStamp
			var updated int64
			var devChecked, lastStatus sql.NullInt64
			if err := rows.Scan(&sourceID, &st.LocalID, &updated, &st.DevFingerprint, &devChecked, &lastStatus); err != nil {
				_ = rows.Close()
				return nil, dbErr(err, "The issue stamps could not be read.")
			}
			st.UpdatedAt = store.FromMs(updated)
			st.DevCheckedAt = msTime(devChecked)
			st.LastStatusAt = msTime(lastStatus)
			out[sourceID] = st
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, dbErr(err, "The issue stamps could not be read.")
		}
	}
	return out, nil
}

// MemberSourceIDs returns the source ids of the standup's current members (standup_issue) with their role.
func MemberSourceIDs(ctx context.Context, q *sql.DB, standupID int64) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT i.source_id, si.role
		  FROM standup_issue si JOIN issue i ON i.id = si.issue_id
		 WHERE si.standup_id = ?`, standupID)
	if err != nil {
		return nil, dbErr(err, "The members could not be read.")
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sourceID, role string
		if err := rows.Scan(&sourceID, &role); err != nil {
			return nil, dbErr(err, "The members could not be read.")
		}
		out[sourceID] = role
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err, "The members could not be read.")
	}
	return out, nil
}

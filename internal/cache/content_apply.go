package cache

// The one per-issue apply shared by the sync apply (jira-sync.md §4.6 step 3) and the one-issue apply after a light
// write or a ticket Refresh (§4.8): issue row (never overwritten by an older fetch), merged change tables, comments with
// bodies, and the content tables (ADR 0015, data-model.md §2.5a). Never deletes a record row: comments and attachments
// that disappear are tombstoned (deleted_at / removed_at). WP-T6.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// IssueApply reports what ApplyIssueDetailTx changed, so the caller can name the changed parts (ticket:updated) and
// react to a status change (transition cache).
type IssueApply struct {
	IssueID int64
	Created bool // the issue row did not exist before
	Skipped bool // the stored row was newer: issue row and content untouched, change and comment rows merged

	Status, Assignee, Flag bool // the issue row's value differs from before
	Comments               bool // a comment was added, edited, restored or tombstoned
	Description            bool // the description changed
	Fields                 bool // environment, a field value, a link, a subtask, the parent or the watcher count changed
	Attachments            bool // an attachment was added or removed
	FirstContent           bool // the issue had no content row before this apply (the first content it ever got)
}

// maxImageBytes mirrors internal/media.MaxImageBytes (cache may not import media): larger images are file cards.
const maxImageBytes = 25 << 20

// imageMimes mirrors internal/media.ImageMimes.
var imageMimes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true,
}

// parentFieldID is the synthetic issue_field_value row that keeps the parent's display snapshot (the issue_link
// relation check admits only link and subtask). LoadTicketTx reads it back as Ticket.Parent and never as a field.
const parentFieldID = "parent"

// ApplyIssueDetailTx upserts one issue by (connection_id, source_id) with everything a source returned for it.
//
//   - Issue row: skipped when the incoming updated_at is strictly older than the stored one (an equal or newer fetch
//     replaces it). A zero incoming updated_at never counts as older.
//   - status_change, assignee_change, flag_change: merged by natural key, always (they are append-only).
//   - comments: merged by id; an edit (newer updated_at) replaces body and visibility, an empty incoming body never
//     erases a stored one; a comment the source did not return is tombstoned (deleted_at) only when the fetch was
//     complete (d.Content != nil, the issue row not skipped, and at least CommentTotal comments returned, or a total of
//     0); a returning comment clears its tombstone.
//   - content (when d.Content != nil and the row was not skipped): replaced only when content_hash changed.
//
// hydrated_at = at; last_seen_at is set on insert only; dev columns and removed_at are not touched.
func ApplyIssueDetailTx(ctx context.Context, tx *sql.Tx, connID int64, d model.IssueDetail, at time.Time) (IssueApply, error) {
	var res IssueApply
	meta := "{}"
	if len(d.Meta) > 0 {
		b, err := json.Marshal(d.Meta)
		if err != nil {
			return res, dbErr(err, "The issues could not be saved.")
		}
		meta = string(b)
	}
	sprints := d.SprintIDs
	if sprints == nil {
		sprints = []string{}
	}

	var (
		oldID                  int64
		oldUpdated             int64
		oldStatus, oldAssignee string
		oldFlagged             int
		exists                 bool
	)
	err := tx.QueryRowContext(ctx,
		`SELECT id, updated_at, status_id, COALESCE(assignee_account_id, ''), flagged
		   FROM issue WHERE connection_id = ? AND source_id = ?`, connID, d.SourceID).
		Scan(&oldID, &oldUpdated, &oldStatus, &oldAssignee, &oldFlagged)
	switch {
	case err == nil:
		exists = true
	case errors.Is(err, sql.ErrNoRows):
	default:
		return res, dbErr(err, "The issues could not be read.")
	}

	incoming := store.Ms(d.UpdatedAt)
	res.Skipped = exists && !d.UpdatedAt.IsZero() && incoming < oldUpdated
	res.Created = !exists

	switch {
	case res.Skipped:
		res.IssueID = oldID
	default:
		err := tx.QueryRowContext(ctx,
			`INSERT INTO issue (connection_id, source_id, key, space_id, summary, issue_type_id, status_id,
			   assignee_account_id, flagged, rank, parent_key, sprint_ids, created_at, updated_at, resolved_at,
			   comment_total, hydrated_at, last_seen_at, source_meta)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(connection_id, source_id) DO UPDATE SET key = excluded.key, space_id = excluded.space_id,
			   summary = excluded.summary, issue_type_id = excluded.issue_type_id, status_id = excluded.status_id,
			   assignee_account_id = excluded.assignee_account_id, flagged = excluded.flagged, rank = excluded.rank,
			   parent_key = excluded.parent_key, sprint_ids = excluded.sprint_ids, created_at = excluded.created_at,
			   updated_at = excluded.updated_at, resolved_at = excluded.resolved_at,
			   comment_total = excluded.comment_total, hydrated_at = excluded.hydrated_at,
			   source_meta = excluded.source_meta
			 RETURNING id`,
			connID, d.SourceID, d.Key, d.SpaceID, d.Summary, d.TypeID, d.StatusID,
			nullStr(d.AssigneeID), boolInt(d.Flagged), nullStr(d.Rank), nullStr(d.ParentKey), encodeStrings(sprints),
			store.Ms(d.CreatedAt), incoming, nullMs(d.ResolvedAt),
			d.CommentTotal, store.Ms(at), store.Ms(at), meta).Scan(&res.IssueID)
		if err != nil {
			return res, dbErr(err, "The issues could not be saved.")
		}
		if exists {
			res.Status = oldStatus != d.StatusID
			res.Assignee = oldAssignee != d.AssigneeID
			res.Flag = (oldFlagged != 0) != d.Flagged
		}
	}
	id := res.IssueID

	for _, c := range d.StatusChanges {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO status_change (issue_id, history_id, changed_at, from_status_id, to_status_id)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(issue_id, history_id) DO UPDATE SET changed_at = excluded.changed_at,
			 from_status_id = excluded.from_status_id, to_status_id = excluded.to_status_id`,
			id, c.ID, store.Ms(c.At), c.From, c.To); err != nil {
			return res, dbErr(err, "The issue history could not be saved.")
		}
	}
	for _, c := range d.AssigneeChanges {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO assignee_change (issue_id, history_id, changed_at, from_account_id, to_account_id)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(issue_id, history_id) DO UPDATE SET changed_at = excluded.changed_at,
			 from_account_id = excluded.from_account_id, to_account_id = excluded.to_account_id`,
			id, c.ID, store.Ms(c.At), nullStr(c.From), nullStr(c.To)); err != nil {
			return res, dbErr(err, "The issue history could not be saved.")
		}
	}
	for _, c := range d.FlagChanges {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO flag_change (issue_id, history_id, changed_at, flagged) VALUES (?, ?, ?, ?)
			 ON CONFLICT(issue_id, history_id) DO UPDATE SET changed_at = excluded.changed_at,
			 flagged = excluded.flagged`,
			id, c.ID, store.Ms(c.At), boolInt(c.Flagged)); err != nil {
			return res, dbErr(err, "The issue history could not be saved.")
		}
	}

	// A comment is tombstoned only when the provider returned the whole list: a short or early-ended page (fewer comments
	// than the provider's total) must not hide the comments it did not reach. A total of 0 means "none".
	complete := d.Content != nil && !res.Skipped && (d.CommentTotal == 0 || len(d.Comments) >= d.CommentTotal)
	changed, err := applyCommentsTx(ctx, tx, id, d.Comments, complete, at)
	if err != nil {
		return res, err
	}
	res.Comments = changed

	if d.Content != nil && !res.Skipped {
		if err := applyContentTx(ctx, tx, id, d.Content, at, &res); err != nil {
			return res, err
		}
	}
	return res, nil
}

// applyCommentsTx merges the comments of one issue and reports whether anything changed.
func applyCommentsTx(ctx context.Context, tx *sql.Tx, issueID int64, comments []model.Comment, complete bool, at time.Time) (bool, error) {
	type stored struct {
		updated int64
		deleted bool
		body    bool
	}
	have := map[string]stored{}
	rows, err := tx.QueryContext(ctx,
		`SELECT comment_id, updated_at, deleted_at IS NOT NULL, body_adf IS NOT NULL FROM comment WHERE issue_id = ?`, issueID)
	if err != nil {
		return false, dbErr(err, "The comments could not be read.")
	}
	for rows.Next() {
		var cid string
		var s stored
		if err := rows.Scan(&cid, &s.updated, &s.deleted, &s.body); err != nil {
			_ = rows.Close()
			return false, dbErr(err, "The comments could not be read.")
		}
		have[cid] = s
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return false, dbErr(err, "The comments could not be read.")
	}

	changed := false
	seen := make(map[string]bool, len(comments))
	for _, c := range comments {
		seen[c.ID] = true
		updated := c.UpdatedAt
		if updated.IsZero() {
			updated = c.At
		}
		var body, vtype, vvalue any
		if !c.Body.IsEmpty() {
			body = string(c.Body.Raw)
		}
		if c.Visibility != nil && (c.Visibility.Type == "role" || c.Visibility.Type == "group") {
			vtype, vvalue = c.Visibility.Type, nullStr(c.Visibility.Value)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO comment (issue_id, comment_id, author_account_id, created_at, updated_at, body_adf,
			                      visibility_type, visibility_value, deleted_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
			 ON CONFLICT(issue_id, comment_id) DO UPDATE SET
			   author_account_id = excluded.author_account_id, created_at = excluded.created_at,
			   updated_at = MAX(excluded.updated_at, comment.updated_at),
			   body_adf = CASE WHEN excluded.body_adf IS NOT NULL AND excluded.updated_at >= comment.updated_at
			                   THEN excluded.body_adf ELSE comment.body_adf END,
			   visibility_type = CASE WHEN excluded.body_adf IS NOT NULL AND excluded.updated_at >= comment.updated_at
			                          THEN excluded.visibility_type ELSE comment.visibility_type END,
			   visibility_value = CASE WHEN excluded.body_adf IS NOT NULL AND excluded.updated_at >= comment.updated_at
			                           THEN excluded.visibility_value ELSE comment.visibility_value END,
			   deleted_at = NULL`,
			issueID, c.ID, nullStr(c.AuthorID), store.Ms(c.At), store.Ms(updated), body, vtype, vvalue); err != nil {
			return false, dbErr(err, "The issue history could not be saved.")
		}
		old, known := have[c.ID]
		switch {
		case !known, old.deleted, store.Ms(updated) > old.updated, body != nil && !old.body:
			changed = true
		}
	}
	if complete {
		var gone []string
		for cid, s := range have {
			if !seen[cid] && !s.deleted {
				gone = append(gone, cid)
			}
		}
		sort.Strings(gone)
		for _, cid := range gone {
			if _, err := tx.ExecContext(ctx,
				`UPDATE comment SET deleted_at = ? WHERE issue_id = ? AND comment_id = ?`, store.Ms(at), issueID, cid); err != nil {
				return false, dbErr(err, "The issue history could not be saved.")
			}
			changed = true
		}
	}
	return changed, nil
}

// contentHash is the content hash to compare: the source's when it gave one, else a SHA-256 of the content.
func contentHash(c *model.IssueContent) string {
	if c.Hash != "" {
		return c.Hash
	}
	cp := *c
	cp.Hash = ""
	b, err := json.Marshal(cp)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func richString(r *model.RichText) any {
	if r == nil || r.IsEmpty() {
		return nil
	}
	return string(r.Raw)
}

// applyContentTx replaces issue_content, issue_field_value and issue_link, and upserts attachments, when the content
// hash differs from the stored one (or no content row exists yet).
func applyContentTx(ctx context.Context, tx *sql.Tx, issueID int64, c *model.IssueContent, at time.Time, res *IssueApply) error {
	hash := contentHash(c)
	var oldHash string
	var oldDesc, oldEnv sql.NullString
	var oldWatchers int
	err := tx.QueryRowContext(ctx,
		`SELECT content_hash, description_adf, environment_adf, watchers FROM issue_content WHERE issue_id = ?`, issueID).
		Scan(&oldHash, &oldDesc, &oldEnv, &oldWatchers)
	hadRow := err == nil
	res.FirstContent = !hadRow
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dbErr(err, "The ticket content could not be read.")
	}
	if hadRow && oldHash == hash && hash != "" {
		return nil
	}

	desc, env := richString(c.Description), richString(c.Environment)
	if !sameNullable(oldDesc, desc) {
		res.Description = true
	}
	if !sameNullable(oldEnv, env) || (hadRow && oldWatchers != c.Watchers) {
		res.Fields = true
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO issue_content (issue_id, description_adf, environment_adf, watchers, content_hash, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(issue_id) DO UPDATE SET description_adf = excluded.description_adf,
		   environment_adf = excluded.environment_adf, watchers = excluded.watchers,
		   content_hash = excluded.content_hash, updated_at = excluded.updated_at`,
		issueID, desc, env, c.Watchers, hash, store.Ms(at)); err != nil {
		return dbErr(err, "The ticket content could not be saved.")
	}

	if err := replaceFieldValuesTx(ctx, tx, issueID, c, res); err != nil {
		return err
	}
	if err := replaceLinksTx(ctx, tx, issueID, c, res); err != nil {
		return err
	}
	return upsertAttachmentsTx(ctx, tx, issueID, c.Attachments, at, res)
}

func sameNullable(old sql.NullString, now any) bool {
	if now == nil {
		return !old.Valid
	}
	return old.Valid && old.String == now.(string)
}

// replaceFieldValuesTx swaps the issue's field values (and the parent snapshot) for the incoming ones.
func replaceFieldValuesTx(ctx context.Context, tx *sql.Tx, issueID int64, c *model.IssueContent, res *IssueApply) error {
	old := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT field_id, value_json FROM issue_field_value WHERE issue_id = ?`, issueID)
	if err != nil {
		return dbErr(err, "The ticket content could not be read.")
	}
	for rows.Next() {
		var fid, v string
		if err := rows.Scan(&fid, &v); err != nil {
			_ = rows.Close()
			return dbErr(err, "The ticket content could not be read.")
		}
		old[fid] = v
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return dbErr(err, "The ticket content could not be read.")
	}

	next := map[string]string{}
	kinds := map[string]string{}
	for _, fv := range c.Fields {
		if fv.FieldID == "" || fv.FieldID == parentFieldID {
			continue
		}
		b, err := json.Marshal(fv)
		if err != nil {
			return dbErr(err, "The ticket content could not be saved.")
		}
		next[fv.FieldID] = string(b)
		kinds[fv.FieldID] = fv.Kind
	}
	if c.Parent != nil {
		b, err := json.Marshal(model.FieldValue{FieldID: parentFieldID, Kind: model.FieldUnknown, Raw: mustJSON(c.Parent)})
		if err != nil {
			return dbErr(err, "The ticket content could not be saved.")
		}
		next[parentFieldID] = string(b)
		kinds[parentFieldID] = model.FieldUnknown
	}
	if len(old) != len(next) {
		res.Fields = true
	} else {
		for k, v := range next {
			if old[k] != v {
				res.Fields = true
				break
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_field_value WHERE issue_id = ?`, issueID); err != nil {
		return dbErr(err, "The ticket content could not be saved.")
	}
	ids := make([]string, 0, len(next))
	for k := range next {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	for _, fid := range ids {
		kind := kinds[fid]
		if kind == "" {
			kind = model.FieldUnknown
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_field_value (issue_id, field_id, kind, value_json) VALUES (?, ?, ?, ?)`,
			issueID, fid, kind, next[fid]); err != nil {
			return dbErr(err, "The ticket content could not be saved.")
		}
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// replaceLinksTx swaps the issue's links and subtasks for the incoming ones.
func replaceLinksTx(ctx context.Context, tx *sql.Tx, issueID int64, c *model.IssueContent, res *IssueApply) error {
	type linkRow struct {
		id, relation, label                   string
		position                              int
		otherSource, otherKey, otherSummary   string
		otherStatus, otherType, otherAssignee any
	}
	var next []linkRow
	for i, l := range c.Links {
		id := l.ID
		if id == "" {
			id = "link:" + l.OtherSourceID
		}
		next = append(next, linkRow{id: id, relation: "link", label: l.Label, position: i,
			otherSource: l.OtherSourceID, otherKey: l.OtherKey, otherSummary: l.OtherSummary,
			otherStatus: nullStr(l.OtherStatusID), otherType: nullStr(l.OtherTypeID)})
	}
	for i, s := range c.Subtasks {
		next = append(next, linkRow{id: "sub:" + s.SourceID, relation: "subtask", position: i,
			otherSource: s.SourceID, otherKey: s.Key, otherSummary: s.Summary,
			otherStatus: nullStr(s.StatusID), otherType: nullStr(s.TypeID), otherAssignee: nullStr(s.AssigneeID)})
	}

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM issue_link WHERE issue_id = ?`, issueID).Scan(&n); err != nil {
		return dbErr(err, "The ticket content could not be read.")
	}
	if n != len(next) {
		res.Fields = true
	} else if n > 0 {
		// Same count: compare the rows in key order.
		old := map[string]string{}
		rows, err := tx.QueryContext(ctx,
			`SELECT link_id, label || '|' || position || '|' || other_source_id || '|' || other_key || '|' || other_summary
			        || '|' || COALESCE(other_status_id, '') || '|' || COALESCE(other_type_id, '')
			        || '|' || COALESCE(other_assignee_account_id, '')
			   FROM issue_link WHERE issue_id = ?`, issueID)
		if err != nil {
			return dbErr(err, "The ticket content could not be read.")
		}
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				_ = rows.Close()
				return dbErr(err, "The ticket content could not be read.")
			}
			old[k] = v
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return dbErr(err, "The ticket content could not be read.")
		}
		for _, l := range next {
			sig := l.label + "|" + itoa(l.position) + "|" + l.otherSource + "|" + l.otherKey + "|" + l.otherSummary +
				"|" + anyStr(l.otherStatus) + "|" + anyStr(l.otherType) + "|" + anyStr(l.otherAssignee)
			if old[l.id] != sig {
				res.Fields = true
				break
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM issue_link WHERE issue_id = ?`, issueID); err != nil {
		return dbErr(err, "The ticket content could not be saved.")
	}
	for _, l := range next {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO issue_link (issue_id, link_id, relation, label, position, other_source_id, other_key,
			                         other_summary, other_status_id, other_type_id, other_assignee_account_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(issue_id, link_id) DO UPDATE SET relation = excluded.relation, label = excluded.label,
			   position = excluded.position, other_source_id = excluded.other_source_id,
			   other_key = excluded.other_key, other_summary = excluded.other_summary,
			   other_status_id = excluded.other_status_id, other_type_id = excluded.other_type_id,
			   other_assignee_account_id = excluded.other_assignee_account_id`,
			issueID, l.id, l.relation, l.label, l.position, l.otherSource, l.otherKey, l.otherSummary,
			l.otherStatus, l.otherType, l.otherAssignee); err != nil {
			return dbErr(err, "The ticket content could not be saved.")
		}
	}
	return nil
}

func anyStr(v any) string {
	s, _ := v.(string)
	return s
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// upsertAttachmentsTx upserts attachment metadata by (issue_id, source_id), keeping the row id and every cache column,
// and tombstones the ones that vanished (removed_at; the cached paths are cleared so the media orphan sweep reclaims the
// files). A returning attachment clears removed_at.
func upsertAttachmentsTx(ctx context.Context, tx *sql.Tx, issueID int64, atts []model.Attachment, at time.Time, res *IssueApply) error {
	live := map[string]bool{}
	had := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT source_id, removed_at IS NULL FROM attachment WHERE issue_id = ?`, issueID)
	if err != nil {
		return dbErr(err, "The attachments could not be read.")
	}
	for rows.Next() {
		var sid string
		var active bool
		if err := rows.Scan(&sid, &active); err != nil {
			_ = rows.Close()
			return dbErr(err, "The attachments could not be read.")
		}
		had[sid] = true
		live[sid] = active
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return dbErr(err, "The attachments could not be read.")
	}

	incoming := map[string]bool{}
	for _, a := range atts {
		if a.ID == "" {
			continue
		}
		incoming[a.ID] = true
		isImage := imageMimes[a.Mime] && a.Bytes <= maxImageBytes
		var w, h any
		if a.Width > 0 && a.Height > 0 {
			w, h = a.Width, a.Height
		}
		mime := a.Mime
		if mime == "" {
			mime = "application/octet-stream"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO attachment (issue_id, source_id, media_file_id, name, mime, bytes, author_account_id, created_at,
			                         is_image, width, height)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(issue_id, source_id) DO UPDATE SET
			   media_file_id = COALESCE(excluded.media_file_id, attachment.media_file_id),
			   name = excluded.name, mime = excluded.mime, bytes = excluded.bytes,
			   author_account_id = excluded.author_account_id, created_at = excluded.created_at,
			   is_image = excluded.is_image,
			   width = COALESCE(excluded.width, attachment.width), height = COALESCE(excluded.height, attachment.height),
			   removed_at = NULL`,
			issueID, a.ID, nullStr(a.MediaFileID), a.Name, mime, a.Bytes, nullStr(a.AuthorID), store.Ms(a.CreatedAt),
			boolInt(isImage), w, h); err != nil {
			return dbErr(err, "The attachments could not be saved.")
		}
		if !had[a.ID] || !live[a.ID] {
			res.Attachments = true
		}
	}
	for sid, active := range live {
		if !active || incoming[sid] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE attachment SET removed_at = ?, file_path = NULL, thumb_path = NULL, cached_bytes = 0
			  WHERE issue_id = ? AND source_id = ?`, store.Ms(at), issueID, sid); err != nil {
			return dbErr(err, "The attachments could not be saved.")
		}
		res.Attachments = true
	}
	return nil
}

// ApplyFieldDefsTx upserts the connection's field registry (never deletes an entry). A kind outside the known set is
// stored as unknown.
func ApplyFieldDefsTx(ctx context.Context, tx *sql.Tx, connID int64, defs []model.FieldDef, at time.Time) error {
	known := map[string]bool{}
	for _, k := range model.FieldKinds {
		known[k] = true
	}
	for _, d := range defs {
		if d.ID == "" {
			continue
		}
		kind := d.Kind
		if !known[kind] {
			kind = model.FieldUnknown
		}
		schema := "{}"
		if len(d.Schema) > 0 && string(d.Schema) != "null" {
			schema = string(d.Schema)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO field_def (connection_id, field_id, name, kind, custom, custom_type, schema_json, fetched_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(connection_id, field_id) DO UPDATE SET name = excluded.name, kind = excluded.kind,
			   custom = excluded.custom, custom_type = excluded.custom_type, schema_json = excluded.schema_json,
			   fetched_at = excluded.fetched_at`,
			connID, d.ID, d.Name, kind, boolInt(d.Custom), nullStr(d.CustomType), schema, store.Ms(at)); err != nil {
			return dbErr(err, "The field list could not be saved.")
		}
	}
	return nil
}

// ApplyIssueDeltaTx applies the known effect of a successful write to the issue row only (status, assignee, flag; nil =
// unchanged). It writes no change-table row and leaves updated_at alone, so the next sync, whose fetch carries the real
// updated time, replaces the row and merges the real changelog entries. Unknown issue → apperr not_found.
func ApplyIssueDeltaTx(ctx context.Context, tx *sql.Tx, issueID int64, statusID, assigneeID *string, flagged *bool) (IssueApply, error) {
	res := IssueApply{IssueID: issueID}
	var oldStatus, oldAssignee string
	var oldFlagged int
	err := tx.QueryRowContext(ctx,
		`SELECT status_id, COALESCE(assignee_account_id, ''), flagged FROM issue WHERE id = ?`, issueID).
		Scan(&oldStatus, &oldAssignee, &oldFlagged)
	if errors.Is(err, sql.ErrNoRows) {
		return res, apperr.New(apperr.NotFound, "That ticket no longer exists.")
	}
	if err != nil {
		return res, dbErr(err, "The ticket could not be read.")
	}
	if statusID != nil && *statusID != oldStatus {
		if _, err := tx.ExecContext(ctx, `UPDATE issue SET status_id = ? WHERE id = ?`, *statusID, issueID); err != nil {
			return res, dbErr(err, "The ticket could not be saved.")
		}
		res.Status = true
	}
	if assigneeID != nil && *assigneeID != oldAssignee {
		if _, err := tx.ExecContext(ctx, `UPDATE issue SET assignee_account_id = ? WHERE id = ?`,
			nullStr(*assigneeID), issueID); err != nil {
			return res, dbErr(err, "The ticket could not be saved.")
		}
		res.Assignee = true
	}
	if flagged != nil && *flagged != (oldFlagged != 0) {
		if _, err := tx.ExecContext(ctx, `UPDATE issue SET flagged = ? WHERE id = ?`, boolInt(*flagged), issueID); err != nil {
			return res, dbErr(err, "The ticket could not be saved.")
		}
		res.Flag = true
	}
	return res, nil
}

// LiveStandupIDsTx lists the live (not archived) standups that have the issue as a current member, ascending.
func LiveStandupIDsTx(ctx context.Context, tx *sql.Tx, issueID int64) ([]int64, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT si.standup_id FROM standup_issue si JOIN standup s ON s.id = si.standup_id
		  WHERE si.issue_id = ? AND s.deleted_at IS NULL ORDER BY si.standup_id`, issueID)
	if err != nil {
		return nil, dbErr(err, "The standups could not be read.")
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, dbErr(err, "The standups could not be read.")
		}
		out = append(out, id)
	}
	return out, dbErr(rows.Err(), "The standups could not be read.")
}

// DropTransitionCacheTx deletes the issue's cached transitions (the status changed).
func DropTransitionCacheTx(ctx context.Context, tx *sql.Tx, issueID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM transition_cache WHERE issue_id = ?`, issueID); err != nil {
		return dbErr(err, "The cached transitions could not be cleared.")
	}
	return nil
}

// IssueKeyTx returns the key of a local issue; unknown → apperr not_found.
func IssueKeyTx(ctx context.Context, tx *sql.Tx, issueID int64) (string, error) {
	var key string
	err := tx.QueryRowContext(ctx, `SELECT key FROM issue WHERE id = ?`, issueID).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", apperr.New(apperr.NotFound, "That ticket no longer exists.")
	}
	return key, dbErr(err, "The ticket could not be read.")
}

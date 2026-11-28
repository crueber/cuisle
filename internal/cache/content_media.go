package cache

// What sync's media phase reads and writes about attachments (jira-sync.md §2.14). The bytes and the path columns
// belong to internal/media; this file only selects targets and records what a download taught us. WP-T6.

import (
	"context"
	"database/sql"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// MediaTarget is one attachment the media phase should fetch.
type MediaTarget struct {
	IssueID int64            // local issue id
	Att     model.Attachment // LocalID is the attachment row id
}

// mediaChunk bounds the host parameters of one query.
const mediaChunk = 400

// MediaTargets lists the image attachments of the given issues (provider ids) that have no thumbnail yet and are not
// removed, oldest row first. These are what the media phase downloads.
func MediaTargets(ctx context.Context, q *sql.DB, connID int64, issueSourceIDs []string) ([]MediaTarget, error) {
	return selectAttachments(ctx, q, connID, issueSourceIDs, `a.is_image = 1 AND a.thumb_path IS NULL AND a.removed_at IS NULL`)
}

// MediaIDTargets lists the attachments of the given issues whose media id is still unknown, for the issues whose
// description, environment, comments or rich fields name a media id that no attachment carries yet (jira-sync §2.14
// step 2). Attachments with a known media id are not listed.
func MediaIDTargets(ctx context.Context, q *sql.DB, connID int64, issueSourceIDs []string) ([]MediaTarget, error) {
	var out []MediaTarget
	for start := 0; start < len(issueSourceIDs); start += mediaChunk {
		chunk := issueSourceIDs[start:min(start+mediaChunk, len(issueSourceIDs))]
		args := make([]any, 0, 1+len(chunk))
		args = append(args, connID)
		for _, id := range chunk {
			args = append(args, id)
		}
		// Only inspect issues that actually have an attachment missing media_file_id.
		rows, err := q.QueryContext(ctx, `
			SELECT i.id, c.description_adf, c.environment_adf
			  FROM issue i JOIN issue_content c ON c.issue_id = i.id
			 WHERE i.connection_id = ? AND i.source_id IN (`+placeholders(len(chunk))+`)
			   AND EXISTS (SELECT 1 FROM attachment a WHERE a.issue_id = i.id AND a.media_file_id IS NULL AND a.removed_at IS NULL)`, args...)
		if err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		type docs struct {
			id   int64
			refs *docRefs
		}
		var issues []docs
		byID := map[int64]*docRefs{}
		idArgs := make([]any, 0)
		for rows.Next() {
			var id int64
			var desc, env sql.NullString
			if err := rows.Scan(&id, &desc, &env); err != nil {
				_ = rows.Close()
				return nil, dbErr(err, "The attachments could not be read.")
			}
			r := newDocRefs()
			r.add([]byte(desc.String))
			r.add([]byte(env.String))
			issues = append(issues, docs{id: id, refs: r})
			byID[id] = r
			idArgs = append(idArgs, id)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		if len(issues) == 0 {
			continue
		}
		ph := placeholders(len(idArgs))
		// Batch-load comments for all candidate issues.
		cr, err := q.QueryContext(ctx, `SELECT issue_id, body_adf FROM comment WHERE issue_id IN (`+ph+`) AND deleted_at IS NULL AND body_adf IS NOT NULL`, idArgs...)
		if err != nil {
			return nil, dbErr(err, "The comments could not be read.")
		}
		for cr.Next() {
			var id int64
			var b string
			if err := cr.Scan(&id, &b); err != nil {
				_ = cr.Close()
				return nil, dbErr(err, "The comments could not be read.")
			}
			byID[id].add([]byte(b))
		}
		err = cr.Err()
		_ = cr.Close()
		if err != nil {
			return nil, dbErr(err, "The comments could not be read.")
		}
		// Batch-load rich paragraph custom fields for all candidate issues.
		fr, err := q.QueryContext(ctx, `SELECT issue_id, value_json FROM issue_field_value WHERE issue_id IN (`+ph+`) AND kind = 'paragraph'`, idArgs...)
		if err != nil {
			return nil, dbErr(err, "The ticket content could not be read.")
		}
		for fr.Next() {
			var id int64
			var b string
			if err := fr.Scan(&id, &b); err != nil {
				_ = fr.Close()
				return nil, dbErr(err, "The ticket content could not be read.")
			}
			byID[id].add([]byte(b))
		}
		err = fr.Err()
		_ = fr.Close()
		if err != nil {
			return nil, dbErr(err, "The ticket content could not be read.")
		}
		// Batch-load already-known media_file_ids for all candidate issues.
		known := map[int64]map[string]bool{}
		kr, err := q.QueryContext(ctx, `SELECT issue_id, media_file_id FROM attachment WHERE issue_id IN (`+ph+`) AND removed_at IS NULL AND media_file_id IS NOT NULL`, idArgs...)
		if err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		for kr.Next() {
			var id int64
			var m string
			if err := kr.Scan(&id, &m); err != nil {
				_ = kr.Close()
				return nil, dbErr(err, "The attachments could not be read.")
			}
			if known[id] == nil {
				known[id] = map[string]bool{}
			}
			known[id][m] = true
		}
		err = kr.Err()
		_ = kr.Close()
		if err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		var unresolvedIDs []any
		for _, d := range issues {
			for _, m := range d.refs.mediaIDs {
				if !known[d.id][m] {
					unresolvedIDs = append(unresolvedIDs, d.id)
					break
				}
			}
		}
		if len(unresolvedIDs) == 0 {
			continue
		}
		qArgs := make([]any, 0, 1+len(unresolvedIDs))
		qArgs = append(qArgs, connID)
		qArgs = append(qArgs, unresolvedIDs...)
		ts, err := queryAttachments(ctx, q, `i.connection_id = ? AND a.issue_id IN (`+placeholders(len(unresolvedIDs))+`) AND a.media_file_id IS NULL AND a.removed_at IS NULL`, qArgs...)
		if err != nil {
			return nil, err
		}
		out = append(out, ts...)
	}
	return out, nil
}

func queryAttachments(ctx context.Context, q *sql.DB, where string, args ...any) ([]MediaTarget, error) {
	const cols = `SELECT a.id, a.issue_id, a.source_id, COALESCE(a.media_file_id, ''), a.name, a.mime, a.bytes,
	                     COALESCE(a.author_account_id, ''), a.created_at
	                FROM attachment a JOIN issue i ON i.id = a.issue_id `
	rows, err := q.QueryContext(ctx, cols+` WHERE `+where+` ORDER BY a.id`, args...)
	if err != nil {
		return nil, dbErr(err, "The attachments could not be read.")
	}
	defer rows.Close()
	var out []MediaTarget
	for rows.Next() {
		var t MediaTarget
		var created int64
		if err := rows.Scan(&t.Att.LocalID, &t.IssueID, &t.Att.ID, &t.Att.MediaFileID, &t.Att.Name, &t.Att.Mime,
			&t.Att.Bytes, &t.Att.AuthorID, &created); err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		t.Att.CreatedAt = store.FromMs(created)
		out = append(out, t)
	}
	return out, dbErr(rows.Err(), "The attachments could not be read.")
}

// selectAttachments reads attachments (joined to their issue) matching where. With issueSourceIDs it is limited to those
// provider issue ids of the connection; without, where must pin the issue itself.
func selectAttachments(ctx context.Context, q *sql.DB, connID int64, issueSourceIDs []string, where string) ([]MediaTarget, error) {
	const cols = `SELECT a.id, a.issue_id, a.source_id, COALESCE(a.media_file_id, ''), a.name, a.mime, a.bytes,
	                     COALESCE(a.author_account_id, ''), a.created_at
	                FROM attachment a JOIN issue i ON i.id = a.issue_id `
	var out []MediaTarget
	scan := func(rows *sql.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var t MediaTarget
			var created int64
			if err := rows.Scan(&t.Att.LocalID, &t.IssueID, &t.Att.ID, &t.Att.MediaFileID, &t.Att.Name, &t.Att.Mime,
				&t.Att.Bytes, &t.Att.AuthorID, &created); err != nil {
				return dbErr(err, "The attachments could not be read.")
			}
			t.Att.CreatedAt = store.FromMs(created)
			out = append(out, t)
		}
		return dbErr(rows.Err(), "The attachments could not be read.")
	}
	if issueSourceIDs == nil {
		rows, err := q.QueryContext(ctx, cols+` WHERE i.connection_id = ? AND `+where+` ORDER BY a.id`, connID)
		if err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		return out, scan(rows)
	}
	for start := 0; start < len(issueSourceIDs); start += mediaChunk {
		chunk := issueSourceIDs[start:min(start+mediaChunk, len(issueSourceIDs))]
		args := []any{connID}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := q.QueryContext(ctx, cols+` WHERE i.connection_id = ? AND i.source_id IN (`+placeholders(len(chunk))+`) AND `+where+` ORDER BY a.id`, args...)
		if err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		if err := scan(rows); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// SetAttachmentMediaID records the media id learned while fetching an attachment, unless one is already known.
func SetAttachmentMediaID(ctx context.Context, w *sql.DB, attachmentID int64, mediaFileID string) error {
	if mediaFileID == "" {
		return nil
	}
	_, err := w.ExecContext(ctx, `UPDATE attachment SET media_file_id = ? WHERE id = ? AND media_file_id IS NULL`, mediaFileID, attachmentID)
	return dbErr(err, "The attachment could not be saved.")
}

// SetAttachmentFetchError records the code of the last failed media fetch ("" clears it).
func SetAttachmentFetchError(ctx context.Context, w *sql.DB, attachmentID int64, code string) error {
	_, err := w.ExecContext(ctx, `UPDATE attachment SET fetch_error = ? WHERE id = ?`, nullStr(code), attachmentID)
	return dbErr(err, "The attachment could not be saved.")
}

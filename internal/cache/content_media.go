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
		args := []any{connID}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := q.QueryContext(ctx, `
			SELECT i.id, c.description_adf, c.environment_adf
			  FROM issue i JOIN issue_content c ON c.issue_id = i.id
			 WHERE i.connection_id = ? AND i.source_id IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		type docs struct {
			id         int64
			desc, env  sql.NullString
			unresolved bool
		}
		var issues []docs
		for rows.Next() {
			var d docs
			if err := rows.Scan(&d.id, &d.desc, &d.env); err != nil {
				_ = rows.Close()
				return nil, dbErr(err, "The attachments could not be read.")
			}
			issues = append(issues, d)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, dbErr(err, "The attachments could not be read.")
		}
		for _, d := range issues {
			refs := newDocRefs()
			refs.add([]byte(d.desc.String))
			refs.add([]byte(d.env.String))
			cr, err := q.QueryContext(ctx, `SELECT body_adf FROM comment WHERE issue_id = ? AND deleted_at IS NULL AND body_adf IS NOT NULL`, d.id)
			if err != nil {
				return nil, dbErr(err, "The comments could not be read.")
			}
			for cr.Next() {
				var b string
				if err := cr.Scan(&b); err != nil {
					_ = cr.Close()
					return nil, dbErr(err, "The comments could not be read.")
				}
				refs.add([]byte(b))
			}
			err = cr.Err()
			_ = cr.Close()
			if err != nil {
				return nil, dbErr(err, "The comments could not be read.")
			}
			if len(refs.mediaIDs) == 0 {
				continue
			}
			known := map[string]bool{}
			kr, err := q.QueryContext(ctx, `SELECT media_file_id FROM attachment WHERE issue_id = ? AND removed_at IS NULL AND media_file_id IS NOT NULL`, d.id)
			if err != nil {
				return nil, dbErr(err, "The attachments could not be read.")
			}
			for kr.Next() {
				var m string
				if err := kr.Scan(&m); err != nil {
					_ = kr.Close()
					return nil, dbErr(err, "The attachments could not be read.")
				}
				known[m] = true
			}
			err = kr.Err()
			_ = kr.Close()
			if err != nil {
				return nil, dbErr(err, "The attachments could not be read.")
			}
			for _, m := range refs.mediaIDs {
				if !known[m] {
					d.unresolved = true
				}
			}
			if !d.unresolved {
				continue
			}
			ts, err := selectAttachments(ctx, q, connID, nil, `a.issue_id = `+itoa(int(d.id))+` AND a.media_file_id IS NULL AND a.removed_at IS NULL`)
			if err != nil {
				return nil, err
			}
			out = append(out, ts...)
		}
	}
	return out, nil
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

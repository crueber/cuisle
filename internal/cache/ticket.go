package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// LoadTicket reads one issue for the ticket modal (data-model.md Q9) in its own reader transaction on r.
// Unknown issue → apperr not_found. No network.
func LoadTicket(ctx context.Context, r *sql.DB, issueID int64) (Ticket, error) {
	tx, err := r.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Ticket{}, apperr.Wrap(apperr.DB, "Could not read the ticket.", err)
	}
	defer func() { _ = tx.Rollback() }()
	return LoadTicketTx(ctx, tx, issueID)
}

// maxIssueRefs bounds the keys looked up for inline cards.
const maxIssueRefs = 200

// LoadTicketTx is LoadTicket inside the caller's reader transaction (so the app can read the standup snapshot and the
// ticket consistently): the issue and its content, the field values and registry with the rail preferences, the
// comments, attachments with their cache state, links and subtasks resolved against local issues, the parent, the
// draft, the issue's complete history, and the people and issues the content references.
func LoadTicketTx(ctx context.Context, tx *sql.Tx, issueID int64) (Ticket, error) {
	t := Ticket{
		LocalID: issueID, Watchers: -1,
		Fields: []TicketField{}, Registry: []TicketFieldDef{}, Comments: []model.Comment{}, Attachments: []TicketAttachment{},
		Links: []TicketLink{}, Draft: model.NewComment{Mentions: []model.Mention{}},
		People: map[string]model.Person{}, Issues: map[string]TicketRef{},
	}
	if err := loadTicketIssue(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	if err := loadTicketFields(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	if err := loadTicketRegistry(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	if err := loadTicketComments(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	if err := loadTicketAttachments(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	if err := loadTicketLinks(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	if err := loadTicketDraft(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	if err := loadTicketHistory(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	if err := loadTicketRefs(ctx, tx, &t); err != nil {
		return Ticket{}, err
	}
	return t, nil
}

func loadTicketIssue(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	var (
		is                           model.Issue
		assignee, rank, parent       sql.NullString
		sprintIDs, sourceMeta        string
		flagged                      int
		created, updated, hydrated   int64
		resolved, removed, contentAt sql.NullInt64
		desc, env                    sql.NullString
		watchers                     sql.NullInt64
	)
	err := tx.QueryRowContext(ctx, `
		SELECT i.connection_id, i.source_id, i.key, i.space_id, i.summary, i.issue_type_id, i.status_id,
		       i.assignee_account_id, i.flagged, i.rank, i.parent_key, i.sprint_ids,
		       i.created_at, i.updated_at, i.resolved_at, i.source_meta, i.hydrated_at, i.removed_at,
		       COALESCE(ty.kind, 'other'),
		       c.description_adf, c.environment_adf, c.watchers, c.updated_at
		  FROM issue i
		  LEFT JOIN issue_content c ON c.issue_id = i.id
		  LEFT JOIN issue_type ty ON ty.connection_id = i.connection_id AND ty.id = i.issue_type_id
		 WHERE i.id = ?`, t.LocalID).
		Scan(&t.ConnectionID, &is.SourceID, &is.Key, &is.SpaceID, &is.Summary, &is.TypeID, &is.StatusID,
			&assignee, &flagged, &rank, &parent, &sprintIDs,
			&created, &updated, &resolved, &sourceMeta, &hydrated, &removed,
			&t.TypeKind, &desc, &env, &watchers, &contentAt)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.NotFound, "This ticket is not in Cuisle.")
	}
	if err != nil {
		return dbErr(err, "The ticket could not be read.")
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
	t.Issue = is
	t.HydratedAt = store.FromMs(hydrated)
	t.RemovedAt = msTime(removed)
	if desc.Valid && desc.String != "" {
		t.Description = &model.RichText{Format: model.RichTextADF, Raw: json.RawMessage(desc.String)}
	}
	if env.Valid && env.String != "" {
		t.Environment = &model.RichText{Format: model.RichTextADF, Raw: json.RawMessage(env.String)}
	}
	if watchers.Valid {
		t.Watchers = int(watchers.Int64)
	}
	t.ContentUpdatedAt = msTime(contentAt)
	return nil
}

func loadTicketFields(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT v.field_id, v.kind, v.value_json, COALESCE(d.name, ''), COALESCE(d.custom, 0)
		  FROM issue_field_value v
		  LEFT JOIN field_def d ON d.connection_id = ? AND d.field_id = v.field_id
		 WHERE v.issue_id = ?
		 ORDER BY v.field_id`, t.ConnectionID, t.LocalID)
	if err != nil {
		return dbErr(err, "The ticket fields could not be read.")
	}
	defer rows.Close()
	var parentRaw string
	for rows.Next() {
		var id, kind, value, name string
		var custom int
		if err := rows.Scan(&id, &kind, &value, &name, &custom); err != nil {
			return dbErr(err, "The ticket fields could not be read.")
		}
		if id == parentFieldID {
			parentRaw = value
			continue
		}
		var fv model.FieldValue
		if err := json.Unmarshal([]byte(value), &fv); err != nil {
			fv = model.FieldValue{Kind: model.FieldUnknown, Raw: json.RawMessage(value)}
		}
		fv.FieldID, fv.Kind = id, kind
		t.Fields = append(t.Fields, TicketField{Value: fv, Name: name, Custom: custom != 0})
	}
	if err := rows.Err(); err != nil {
		return dbErr(err, "The ticket fields could not be read.")
	}
	if parentRaw != "" {
		t.Parent = parentLink(parentRaw)
	}
	return nil
}

// parentLink decodes the stored parent snapshot (an issue_field_value row, see parentFieldID).
func parentLink(value string) *TicketLink {
	var fv model.FieldValue
	var ref model.IssueRef
	if json.Unmarshal([]byte(value), &fv) != nil || json.Unmarshal(fv.Raw, &ref) != nil || ref.Key == "" {
		return nil
	}
	return &TicketLink{
		Relation: "parent", OtherSourceID: ref.SourceID, OtherKey: ref.Key, OtherSummary: ref.Summary,
		OtherStatusID: ref.StatusID, OtherTypeID: ref.TypeID, OtherAssigneeID: ref.AssigneeID,
	}
}

func loadTicketRegistry(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT d.field_id, d.name, d.kind, d.custom, COALESCE(d.custom_type, ''), d.schema_json,
		       p.position, COALESCE(p.hidden, 0)
		  FROM field_def d
		  LEFT JOIN ticket_field_pref p ON p.connection_id = d.connection_id AND p.field_id = d.field_id
		 WHERE d.connection_id = ?
		 ORDER BY d.field_id`, t.ConnectionID)
	if err != nil {
		return dbErr(err, "The field list could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var d TicketFieldDef
		var custom, hidden int
		var schema string
		var pos sql.NullInt64
		if err := rows.Scan(&d.Def.ID, &d.Def.Name, &d.Def.Kind, &custom, &d.Def.CustomType, &schema, &pos, &hidden); err != nil {
			return dbErr(err, "The field list could not be read.")
		}
		d.Def.Custom = custom != 0
		if schema != "" && schema != "{}" {
			d.Def.Schema = json.RawMessage(schema)
		}
		d.Pinned = -1
		if pos.Valid {
			d.Pinned = int(pos.Int64)
		}
		d.Hidden = hidden != 0
		t.Registry = append(t.Registry, d)
	}
	return dbErr(rows.Err(), "The field list could not be read.")
}

func loadTicketComments(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT comment_id, COALESCE(author_account_id, ''), created_at, updated_at, body_adf, visibility_type, visibility_value
		  FROM comment WHERE issue_id = ? AND deleted_at IS NULL ORDER BY created_at, comment_id`, t.LocalID)
	if err != nil {
		return dbErr(err, "The comments could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var c model.Comment
		var created, updated int64
		var body, vtype, vvalue sql.NullString
		if err := rows.Scan(&c.ID, &c.AuthorID, &created, &updated, &body, &vtype, &vvalue); err != nil {
			return dbErr(err, "The comments could not be read.")
		}
		c.At = store.FromMs(created)
		c.UpdatedAt = store.FromMs(updated)
		if updated == 0 {
			c.UpdatedAt = c.At
		}
		if body.Valid && body.String != "" {
			c.Body = model.RichText{Format: model.RichTextADF, Raw: json.RawMessage(body.String)}
		}
		if vtype.Valid {
			c.Visibility = &model.Visibility{Type: vtype.String, Value: vvalue.String}
		}
		t.Comments = append(t.Comments, c)
	}
	return dbErr(rows.Err(), "The comments could not be read.")
}

func loadTicketAttachments(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, source_id, COALESCE(media_file_id, ''), name, mime, bytes, COALESCE(author_account_id, ''), created_at,
		       is_image, COALESCE(width, 0), COALESCE(height, 0),
		       thumb_path IS NOT NULL, file_path IS NOT NULL, COALESCE(fetch_error, '')
		  FROM attachment WHERE issue_id = ? AND removed_at IS NULL
		 ORDER BY is_image DESC, created_at DESC, id DESC`, t.LocalID)
	if err != nil {
		return dbErr(err, "The attachments could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var a TicketAttachment
		var created int64
		var isImage, hasThumb, hasFile bool
		if err := rows.Scan(&a.LocalID, &a.ID, &a.MediaFileID, &a.Name, &a.Mime, &a.Bytes, &a.AuthorID, &created,
			&isImage, &a.Width, &a.Height, &hasThumb, &hasFile, &a.FetchError); err != nil {
			return dbErr(err, "The attachments could not be read.")
		}
		a.CreatedAt = store.FromMs(created)
		a.IsImage, a.HasThumb, a.HasFile = isImage, hasThumb, hasFile
		t.Attachments = append(t.Attachments, a)
	}
	return dbErr(rows.Err(), "The attachments could not be read.")
}

func loadTicketLinks(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT l.relation, l.label, l.position, l.other_source_id, l.other_key, l.other_summary,
		       COALESCE(l.other_status_id, ''), COALESCE(l.other_type_id, ''), COALESCE(l.other_assignee_account_id, ''),
		       COALESCE(ot.kind, ''), COALESCE(o.id, 0), COALESCE(o.summary, ''), COALESCE(o.status_id, ''),
		       COALESCE(o.assignee_account_id, '')
		  FROM issue_link l
		  LEFT JOIN issue o ON o.connection_id = ? AND o.source_id = l.other_source_id
		  LEFT JOIN issue_type ot ON ot.connection_id = ? AND ot.id = COALESCE(l.other_type_id, o.issue_type_id)
		 WHERE l.issue_id = ?
		 ORDER BY l.relation, l.label, l.position, l.link_id`, t.ConnectionID, t.ConnectionID, t.LocalID)
	if err != nil {
		return dbErr(err, "The linked issues could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var l TicketLink
		if err := rows.Scan(&l.Relation, &l.Label, &l.Position, &l.OtherSourceID, &l.OtherKey, &l.OtherSummary,
			&l.OtherStatusID, &l.OtherTypeID, &l.OtherAssigneeID, &l.OtherTypeKind, &l.OtherIssueID, &l.LiveSummary,
			&l.LiveStatusID, &l.LiveAssigneeID); err != nil {
			return dbErr(err, "The linked issues could not be read.")
		}
		t.Links = append(t.Links, l)
	}
	if err := rows.Err(); err != nil {
		return dbErr(err, "The linked issues could not be read.")
	}

	// The parent: the stored snapshot (when the content was synced), else just the key; both resolve locally.
	if t.Parent == nil && t.Issue.ParentKey != "" {
		t.Parent = &TicketLink{Relation: "parent", OtherKey: t.Issue.ParentKey}
	}
	if t.Parent != nil {
		return resolveParent(ctx, tx, t)
	}
	return nil
}

// resolveParent fills the local id and live fields of the parent link from the synced issue with that key.
func resolveParent(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	p := t.Parent
	var typeID, typeKind string
	err := tx.QueryRowContext(ctx, `
		SELECT o.id, o.source_id, o.summary, o.status_id, o.issue_type_id,
		       COALESCE(o.assignee_account_id, ''), COALESCE(it.kind, '')
		  FROM issue o
		  LEFT JOIN issue_type it ON it.connection_id = o.connection_id AND it.id = o.issue_type_id
		 WHERE o.connection_id = ? AND o.key = ?`, t.ConnectionID, p.OtherKey).
		Scan(&p.OtherIssueID, &p.OtherSourceID, &p.LiveSummary, &p.LiveStatusID, &typeID, &p.LiveAssigneeID, &typeKind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dbErr(err, "The parent could not be read.")
	}
	if errors.Is(err, sql.ErrNoRows) {
		typeID = p.OtherTypeID
	}
	if p.OtherTypeID == "" {
		p.OtherTypeID = typeID
	}
	if typeKind != "" {
		p.OtherTypeKind = typeKind
		return nil
	}
	if typeID == "" {
		return nil
	}
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT kind FROM issue_type WHERE connection_id = ? AND id = ?`, t.ConnectionID, typeID).Scan(&kind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dbErr(err, "The parent could not be read.")
	}
	p.OtherTypeKind = kind
	return nil
}

func loadTicketDraft(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	var text, mentions string
	err := tx.QueryRowContext(ctx, `SELECT body_text, mentions_json FROM ticket_draft WHERE issue_id = ?`, t.LocalID).Scan(&text, &mentions)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return dbErr(err, "The comment draft could not be read.")
	}
	t.Draft.Text = text
	var ms []model.Mention
	if json.Unmarshal([]byte(mentions), &ms) == nil && ms != nil {
		t.Draft.Mentions = ms
	}
	return nil
}

// loadTicketHistory reads every recorded event of the issue (the header pulse windows it later).
func loadTicketHistory(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	id := t.LocalID
	rows, err := tx.QueryContext(ctx,
		`SELECT history_id, changed_at, from_status_id, to_status_id FROM status_change WHERE issue_id = ? ORDER BY changed_at, history_id`, id)
	if err != nil {
		return dbErr(err, "The status history could not be read.")
	}
	for rows.Next() {
		var c model.StatusChange
		var at int64
		if err := rows.Scan(&c.ID, &at, &c.From, &c.To); err != nil {
			_ = rows.Close()
			return dbErr(err, "The status history could not be read.")
		}
		c.At = store.FromMs(at)
		t.History.StatusChanges = append(t.History.StatusChanges, c)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return dbErr(err, "The status history could not be read.")
	}

	rows, err = tx.QueryContext(ctx,
		`SELECT history_id, changed_at, COALESCE(from_account_id, ''), COALESCE(to_account_id, '')
		   FROM assignee_change WHERE issue_id = ? ORDER BY changed_at, history_id`, id)
	if err != nil {
		return dbErr(err, "The assignee history could not be read.")
	}
	for rows.Next() {
		var c model.AssigneeChange
		var at int64
		if err := rows.Scan(&c.ID, &at, &c.From, &c.To); err != nil {
			_ = rows.Close()
			return dbErr(err, "The assignee history could not be read.")
		}
		c.At = store.FromMs(at)
		t.History.AssigneeChanges = append(t.History.AssigneeChanges, c)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return dbErr(err, "The assignee history could not be read.")
	}

	rows, err = tx.QueryContext(ctx,
		`SELECT history_id, changed_at, flagged FROM flag_change WHERE issue_id = ? ORDER BY changed_at, history_id`, id)
	if err != nil {
		return dbErr(err, "The flag history could not be read.")
	}
	for rows.Next() {
		var c model.FlagChange
		var at int64
		var flagged int
		if err := rows.Scan(&c.ID, &at, &flagged); err != nil {
			_ = rows.Close()
			return dbErr(err, "The flag history could not be read.")
		}
		c.At = store.FromMs(at)
		c.Flagged = flagged != 0
		t.History.FlagChanges = append(t.History.FlagChanges, c)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return dbErr(err, "The flag history could not be read.")
	}

	// Comment times include tombstoned comments: a deleted comment keeps its pulse mark.
	rows, err = tx.QueryContext(ctx,
		`SELECT comment_id, COALESCE(author_account_id, ''), created_at FROM comment WHERE issue_id = ? ORDER BY created_at, comment_id`, id)
	if err != nil {
		return dbErr(err, "The comments could not be read.")
	}
	for rows.Next() {
		var c model.Comment
		var at int64
		if err := rows.Scan(&c.ID, &c.AuthorID, &at); err != nil {
			_ = rows.Close()
			return dbErr(err, "The comments could not be read.")
		}
		c.At = store.FromMs(at)
		t.History.Comments = append(t.History.Comments, c)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return dbErr(err, "The comments could not be read.")
	}

	rows, err = tx.QueryContext(ctx,
		`SELECT commit_id, repo, authored_at, is_merge FROM dev_commit WHERE issue_id = ? ORDER BY authored_at, commit_id`, id)
	if err != nil {
		return dbErr(err, "The commits could not be read.")
	}
	for rows.Next() {
		var e model.DevEvent
		var at int64
		var merge int
		if err := rows.Scan(&e.ID, &e.Repo, &at, &merge); err != nil {
			_ = rows.Close()
			return dbErr(err, "The commits could not be read.")
		}
		e.Kind, e.At, e.Merge = model.DevCommit, store.FromMs(at), merge != 0
		t.History.Dev = append(t.History.Dev, e)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return dbErr(err, "The commits could not be read.")
	}

	rows, err = tx.QueryContext(ctx,
		`SELECT pr_key, status, at, COALESCE(title, ''), COALESCE(branch, '') FROM dev_pr_event WHERE issue_id = ? ORDER BY at, pr_key, status`, id)
	if err != nil {
		return dbErr(err, "The pull requests could not be read.")
	}
	for rows.Next() {
		var e model.DevEvent
		var at int64
		if err := rows.Scan(&e.ID, &e.Status, &at, &e.Title, &e.Branch); err != nil {
			_ = rows.Close()
			return dbErr(err, "The pull requests could not be read.")
		}
		e.Kind, e.At = model.DevPR, store.FromMs(at)
		if i := strings.LastIndex(e.ID, "#"); i >= 0 {
			e.Repo = e.ID[:i]
		}
		t.History.Dev = append(t.History.Dev, e)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return dbErr(err, "The pull requests could not be read.")
	}
	sort.SliceStable(t.History.Dev, func(i, j int) bool { return t.History.Dev[i].At.Before(t.History.Dev[j].At) })
	return nil
}

// loadTicketRefs fills People and Issues with what the ticket references.
func loadTicketRefs(ctx context.Context, tx *sql.Tx, t *Ticket) error {
	refs := newDocRefs()
	if t.Description != nil {
		refs.add(t.Description.Raw)
	}
	if t.Environment != nil {
		refs.add(t.Environment.Raw)
	}
	for _, c := range t.Comments {
		refs.add(c.Body.Raw)
	}
	for _, f := range t.Fields {
		if f.Value.Rich != nil {
			refs.add(f.Value.Rich.Raw)
		}
	}

	accounts := refs.accounts
	add := func(id string) {
		if id != "" {
			accounts[id] = true
		}
	}
	add(t.Issue.AssigneeID)
	for _, c := range t.History.AssigneeChanges {
		add(c.From)
		add(c.To)
	}
	for _, c := range t.History.Comments {
		add(c.AuthorID)
	}
	for _, a := range t.Attachments {
		add(a.AuthorID)
	}
	for _, f := range t.Fields {
		for _, u := range f.Value.Users {
			add(u)
		}
	}
	for _, l := range t.Links {
		add(l.OtherAssigneeID)
		add(l.LiveAssigneeID)
	}
	if t.Parent != nil {
		add(t.Parent.OtherAssigneeID)
	}
	ids := sortedKeys(accounts)
	if len(ids) > 0 {
		args := []any{t.ConnectionID}
		for _, id := range ids {
			args = append(args, id)
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT account_id, display_name, active, COALESCE(account_type, 'user')
			  FROM person WHERE connection_id = ? AND account_id IN (`+placeholders(len(ids))+`)`, args...)
		if err != nil {
			return dbErr(err, "The people could not be read.")
		}
		for rows.Next() {
			var p model.Person
			var active int
			if err := rows.Scan(&p.AccountID, &p.DisplayName, &active, &p.Kind); err != nil {
				_ = rows.Close()
				return dbErr(err, "The people could not be read.")
			}
			p.Active = active != 0
			t.People[p.AccountID] = p
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return dbErr(err, "The people could not be read.")
		}
	}

	keys := refs.keys
	for _, l := range t.Links {
		if l.OtherKey != "" {
			keys[l.OtherKey] = true
		}
	}
	if t.Parent != nil && t.Parent.OtherKey != "" {
		keys[t.Parent.OtherKey] = true
	}
	delete(keys, t.Issue.Key)
	klist := sortedKeys(keys)
	if len(klist) > maxIssueRefs {
		klist = klist[:maxIssueRefs]
	}
	if len(klist) == 0 {
		return nil
	}
	args := []any{t.ConnectionID}
	for _, k := range klist {
		args = append(args, k)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT i.id, i.key, i.summary, i.status_id, COALESCE(ty.kind, 'other')
		  FROM issue i
		  LEFT JOIN issue_type ty ON ty.connection_id = i.connection_id AND ty.id = i.issue_type_id
		 WHERE i.connection_id = ? AND i.key IN (`+placeholders(len(klist))+`)`, args...)
	if err != nil {
		return dbErr(err, "The linked issues could not be read.")
	}
	defer rows.Close()
	for rows.Next() {
		var r TicketRef
		if err := rows.Scan(&r.LocalID, &r.Key, &r.Summary, &r.StatusID, &r.TypeKind); err != nil {
			return dbErr(err, "The linked issues could not be read.")
		}
		t.Issues[r.Key] = r
	}
	return dbErr(rows.Err(), "The linked issues could not be read.")
}

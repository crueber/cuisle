package app

// Plumbing of the ticket modal (WP-T9): the local issue lookup, the TicketDetail builder shared by every service
// method, the per-issue locks and Refresh cooldown, the offline verdict, and the small SQL helpers for drafts, the
// transitions cache and the field rail preferences (the cache package has the tables but, by ownership, no helpers for
// them). Nothing here touches the network.

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/view"
)

// refreshCooldown is the minimum time between two Refreshes of one issue (jira-sync.md §3.1).
const refreshCooldown = 10 * time.Second

// issueRef is the local facts about an issue every ticket call needs.
type issueRef struct {
	ID           int64
	ConnectionID int64
	SourceID     string
	Key          string
	StatusID     string
}

func notFoundTicket() error { return apperr.New(apperr.NotFound, "This ticket is not in Cuisle.") }

// issueRef reads the issue row.
func (d *Deps) issueRef(ctx context.Context, issueID int64) (issueRef, error) {
	ref := issueRef{ID: issueID}
	err := d.DB.R.QueryRowContext(ctx,
		`SELECT connection_id, source_id, key, status_id FROM issue WHERE id = ?`, issueID).
		Scan(&ref.ConnectionID, &ref.SourceID, &ref.Key, &ref.StatusID)
	if err == sql.ErrNoRows {
		return issueRef{}, notFoundTicket()
	}
	if err != nil {
		return issueRef{}, apperr.Wrap(apperr.DB, "The ticket could not be read.", err)
	}
	return ref, nil
}

// ── locks, cooldown, offline verdict ─────────────────────────────────────────────

// lockIssue takes the in-flight lock of an issue (one write or Refresh at a time); ok=false means one is running.
func (d *Deps) lockIssue(issueID int64) (release func(), ok bool) {
	d.ticketMu.Lock()
	defer d.ticketMu.Unlock()
	if d.busy == nil {
		d.busy = map[int64]struct{}{}
	}
	if _, in := d.busy[issueID]; in {
		return nil, false
	}
	d.busy[issueID] = struct{}{}
	return func() {
		d.ticketMu.Lock()
		delete(d.busy, issueID)
		d.ticketMu.Unlock()
	}, true
}

func errBusy() error {
	return apperr.New(apperr.Conflict, "Another change to this ticket is still in progress.")
}

// refreshWait returns the time left before issueID may be refreshed again (0 = now) and, when it may, records the
// attempt so the next one is held off for refreshCooldown.
func (d *Deps) refreshWait(issueID int64) time.Duration {
	now := d.now()
	d.ticketMu.Lock()
	defer d.ticketMu.Unlock()
	if d.refreshAt == nil {
		d.refreshAt = map[int64]time.Time{}
	}
	if last, ok := d.refreshAt[issueID]; ok {
		if left := refreshCooldown - now.Sub(last); left > 0 {
			return left
		}
	}
	d.refreshAt[issueID] = now
	return 0
}

// noteNet records what the last ticket network call said about connectivity. The modal reads it as Can.Reason
// "offline" (Connection.State has no offline value). A cancelled call says nothing.
func (d *Deps) noteNet(err error) {
	switch {
	case err == nil:
		d.offlineAt.Store(0)
	case apperr.Is(err, apperr.Offline):
		d.offlineAt.Store(d.now().UnixMilli())
	}
}

// isOffline is the verdict the modal shows. Any later successful source call clears it (noteNet(nil), also from the
// autocomplete and search paths); so does a sync that finished OK after the verdict was reached.
func (d *Deps) isOffline(ctx context.Context) bool {
	since := d.offlineAt.Load()
	if since == 0 {
		return false
	}
	if list, err := d.Sync.Status(ctx); err == nil {
		for _, s := range list {
			if s.LastStatus == "ok" && s.LastSuccessAt.UnixMilli() > since {
				d.offlineAt.CompareAndSwap(since, 0)
				return false
			}
		}
	}
	return true
}

// ── building the modal payload ──────────────────────────────────────────────────

// writesOf is what the connection's source supports. The stored source meta is passed so Flag (which needs the
// discovered Flagged field) is reported correctly.
func (d *Deps) writesOf(row cache.ConnRow) view.TicketWrites {
	src, err := source.Open(model.Connection{ID: row.ID, Source: row.Source, BaseURL: row.BaseURL, Account: row.Account}, "",
		d.srcOpts(row.SourceMeta, false))
	if err != nil {
		return view.TicketWrites{}
	}
	w := src.Capabilities().Writes
	return view.TicketWrites{Transition: w.Transition, Assign: w.Assign, Flag: w.Flag, Comment: w.Comment}
}

// ticketDetail is the modal payload of an issue as seen from a standup: one reader transaction (config, snapshot,
// ticket, fields in use), no network.
func (d *Deps) ticketDetail(ctx context.Context, standupID, issueID int64, refreshFailed bool) (view.TicketDetail, error) {
	if _, err := d.loadLive(ctx, standupID); err != nil {
		return view.TicketDetail{}, err
	}
	asOf := d.viewTime()
	var (
		snap  model.BoardSnapshot
		tk    cache.Ticket
		inUse map[string]bool
	)
	err := d.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		cfg, err := settings.EffectiveTx(ctx, tx, standupID)
		if err != nil {
			return err
		}
		if snap, err = cache.LoadCurrentTx(ctx, tx, cfg, asOf); err != nil {
			return err
		}
		if tk, err = cache.LoadTicketTx(ctx, tx, issueID); err != nil {
			return err
		}
		inUse, err = cache.FieldsInUseTx(ctx, tx, issueID)
		return err
	})
	if err != nil {
		return view.TicketDetail{}, err
	}
	opts := view.TicketOptions{AuthState: "missing", RefreshFailed: refreshFailed, FieldsInUse: inUse}
	if row, ok, err := d.connRow(ctx); err != nil {
		return view.TicketDetail{}, err
	} else if ok {
		snap.IssueURLPrefix = source.IssueURLPrefix(row.Source, row.BaseURL)
		opts.SelfAccountID, opts.AuthState, opts.Writes = row.AccountID, row.AuthState, d.writesOf(row)
	}
	detail := view.BuildTicketWith(snap, tk, prefsOf(tk.Registry), asOf, opts)
	if detail.Can.Reason == "" && d.isOffline(ctx) {
		detail.Can.Reason = "offline" // writes stay offered by capability; the modal disables them while this is set
	}
	return detail, nil
}

// ── field rail preferences ──────────────────────────────────────────────────────

// prefsOf folds the registry's per-connection preferences into the Configure / BuildTicket shape. Pinned: every field
// with a position (hidden ones keep it and carry Hidden), in order. Hidden: every hidden field. Available: custom
// fields that are not pinned, A to Z.
func prefsOf(reg []cache.TicketFieldDef) view.TicketFieldPrefs {
	p := view.TicketFieldPrefs{Pinned: []view.FieldPref{}, Hidden: []view.FieldPref{}, Available: []view.FieldOption{}}
	for _, e := range reg {
		fp := view.FieldPref{FieldID: e.Def.ID, Name: e.Def.Name, Kind: e.Def.Kind, Position: e.Pinned, Hidden: e.Hidden}
		if e.Pinned >= 0 {
			p.Pinned = append(p.Pinned, fp)
		}
		if e.Hidden {
			p.Hidden = append(p.Hidden, fp)
		}
		if e.Def.Custom && e.Pinned < 0 {
			p.Available = append(p.Available, view.FieldOption{FieldID: e.Def.ID, Name: e.Def.Name, Kind: e.Def.Kind, Custom: true})
		}
	}
	byName := func(a, b string, ai, bi string) bool {
		if la, lb := strings.ToLower(a), strings.ToLower(b); la != lb {
			return la < lb
		}
		return ai < bi
	}
	sort.SliceStable(p.Pinned, func(i, j int) bool { return p.Pinned[i].Position < p.Pinned[j].Position })
	sort.SliceStable(p.Hidden, func(i, j int) bool {
		return byName(p.Hidden[i].Name, p.Hidden[j].Name, p.Hidden[i].FieldID, p.Hidden[j].FieldID)
	})
	sort.SliceStable(p.Available, func(i, j int) bool {
		return byName(p.Available[i].Name, p.Available[j].Name, p.Available[i].FieldID, p.Available[j].FieldID)
	})
	return p
}

// fieldPrefs reads the registry of the connection with its preferences.
func (d *Deps) fieldPrefs(ctx context.Context, connID int64) (view.TicketFieldPrefs, error) {
	rows, err := d.DB.R.QueryContext(ctx, `
		SELECT d.field_id, d.name, d.kind, d.custom, p.position, COALESCE(p.hidden, 0)
		  FROM field_def d
		  LEFT JOIN ticket_field_pref p ON p.connection_id = d.connection_id AND p.field_id = d.field_id
		 WHERE d.connection_id = ?`, connID)
	if err != nil {
		return view.TicketFieldPrefs{}, apperr.Wrap(apperr.DB, "The field list could not be read.", err)
	}
	defer rows.Close()
	var reg []cache.TicketFieldDef
	for rows.Next() {
		var e cache.TicketFieldDef
		var custom, hidden int
		var pos sql.NullInt64
		if err := rows.Scan(&e.Def.ID, &e.Def.Name, &e.Def.Kind, &custom, &pos, &hidden); err != nil {
			return view.TicketFieldPrefs{}, apperr.Wrap(apperr.DB, "The field list could not be read.", err)
		}
		e.Def.Custom, e.Hidden, e.Pinned = custom != 0, hidden != 0, -1
		if pos.Valid {
			e.Pinned = int(pos.Int64)
		}
		reg = append(reg, e)
	}
	if err := rows.Err(); err != nil {
		return view.TicketFieldPrefs{}, apperr.Wrap(apperr.DB, "The field list could not be read.", err)
	}
	return prefsOf(reg), nil
}

// editFieldPrefs runs fn in a write transaction after checking the field exists in the registry when mustExist is set.
// It does not bump standup revs: the rail is built per Get, so the next open already follows the preference.
func (d *Deps) editFieldPrefs(ctx context.Context, connID int64, fieldID string, mustExist bool, fn func(tx *sql.Tx) error) error {
	return d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if mustExist {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM field_def WHERE connection_id = ? AND field_id = ?`, connID, fieldID).Scan(&n); err != nil {
				return apperr.Wrap(apperr.DB, "The field list could not be read.", err)
			}
			if n == 0 {
				return apperr.New(apperr.NotFound, "That field is not known.")
			}
		}
		return fn(tx)
	})
}

// renumber rewrites the pinned positions to 0..n-1 in the given order.
func renumber(ctx context.Context, tx *sql.Tx, connID int64, order []string) error {
	for i, id := range order {
		if _, err := tx.ExecContext(ctx, `UPDATE ticket_field_pref SET position = ? WHERE connection_id = ? AND field_id = ?`, i, connID, id); err != nil {
			return apperr.Wrap(apperr.DB, "The preference could not be saved.", err)
		}
	}
	return nil
}

// pinnedOrder lists the pinned field ids in position order.
func pinnedOrder(ctx context.Context, tx *sql.Tx, connID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT field_id FROM ticket_field_pref WHERE connection_id = ? AND position IS NOT NULL ORDER BY position, field_id`, connID)
	if err != nil {
		return nil, apperr.Wrap(apperr.DB, "The preference could not be read.", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, apperr.Wrap(apperr.DB, "The preference could not be read.", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ── drafts ──────────────────────────────────────────────────────────────────────

const maxCommentRunes = 32000

// checkComment validates composer input: some text, within the size limit, mentions inside the text.
func checkComment(in view.CommentInput) error {
	n := len([]rune(in.Text))
	if n > maxCommentRunes {
		return apperr.Validationf("text", "That comment is too long.")
	}
	for _, m := range in.Mentions {
		if m.Start < 0 || m.End <= m.Start || m.End > n || m.AccountID == "" {
			return apperr.Validationf("mentions", "A mention is out of range.")
		}
	}
	return nil
}

func toNewComment(in view.CommentInput) model.NewComment {
	ms := make([]model.Mention, 0, len(in.Mentions))
	for _, m := range in.Mentions {
		ms = append(ms, model.Mention{Start: m.Start, End: m.End, AccountID: m.AccountID, Name: m.Name})
	}
	return model.NewComment{Text: in.Text, Mentions: ms}
}

func (d *Deps) readDraft(ctx context.Context, issueID int64) (view.CommentInput, error) {
	out := view.CommentInput{Mentions: []view.Mention{}}
	var text, mentions string
	err := d.DB.R.QueryRowContext(ctx, `SELECT body_text, mentions_json FROM ticket_draft WHERE issue_id = ?`, issueID).Scan(&text, &mentions)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return view.CommentInput{}, apperr.Wrap(apperr.DB, "The comment draft could not be read.", err)
	}
	out.Text = text
	var ms []view.Mention
	if json.Unmarshal([]byte(mentions), &ms) == nil && ms != nil {
		out.Mentions = ms
	}
	return out, nil
}

func deleteDraftTx(ctx context.Context, tx *sql.Tx, issueID int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_draft WHERE issue_id = ?`, issueID); err != nil {
		return apperr.Wrap(apperr.DB, "The comment draft could not be removed.", err)
	}
	return nil
}

func (d *Deps) writeDraft(ctx context.Context, issueID int64, in view.CommentInput) error {
	return d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if in.Text == "" {
			return deleteDraftTx(ctx, tx, issueID)
		}
		ms := in.Mentions
		if ms == nil {
			ms = []view.Mention{}
		}
		raw, err := json.Marshal(ms)
		if err != nil {
			return apperr.Wrap(apperr.Internal, "The comment draft could not be saved.", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ticket_draft (issue_id, body_text, mentions_json, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(issue_id) DO UPDATE SET body_text = excluded.body_text, mentions_json = excluded.mentions_json,
			                                    updated_at = excluded.updated_at`,
			issueID, in.Text, string(raw), store.Ms(d.now()))
		if err != nil {
			return apperr.Wrap(apperr.DB, "The comment draft could not be saved.", err)
		}
		return nil
	})
}

// ── the transitions cache ───────────────────────────────────────────────────────

func (d *Deps) readTransitionCache(ctx context.Context, issueID int64) ([]model.Transition, time.Time, bool, error) {
	var raw string
	var at int64
	err := d.DB.R.QueryRowContext(ctx, `SELECT json, fetched_at FROM transition_cache WHERE issue_id = ?`, issueID).Scan(&raw, &at)
	if err == sql.ErrNoRows {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, apperr.Wrap(apperr.DB, "The transitions could not be read.", err)
	}
	var list []model.Transition
	if json.Unmarshal([]byte(raw), &list) != nil {
		return nil, time.Time{}, false, nil
	}
	return list, time.UnixMilli(at), true, nil
}

func (d *Deps) writeTransitionCache(ctx context.Context, issueID int64, list []model.Transition) {
	raw, err := json.Marshal(list)
	if err != nil {
		return
	}
	err = d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO transition_cache (issue_id, json, fetched_at) VALUES (?, ?, ?)
			ON CONFLICT(issue_id) DO UPDATE SET json = excluded.json, fetched_at = excluded.fetched_at`,
			issueID, string(raw), store.Ms(d.now()))
		return err
	})
	if err != nil {
		d.log().Warn("could not cache the transitions", "err", err)
	}
}

// laneResolver maps a status to a lane the way the standup does: the standup's status mapping, else the status
// category (new todo, indeterminate prog, done done). The standup is the first live one that has the issue.
func (d *Deps) laneResolver(ctx context.Context, connID, issueID int64) func(statusID string) string {
	mapped := map[string]string{}
	_ = d.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		ids, err := cache.LiveStandupIDsTx(ctx, tx, issueID)
		if err != nil || len(ids) == 0 {
			return err
		}
		cfg, err := settings.EffectiveTx(ctx, tx, ids[0])
		if err != nil {
			return err
		}
		for _, sl := range cfg.StatusLanes {
			mapped[sl.StatusID] = sl.Lane
		}
		return nil
	})
	return func(statusID string) string {
		switch l := mapped[statusID]; l {
		case model.LaneTodo, model.LaneProg, model.LaneReview, model.LaneQA, model.LaneDone:
			return l
		}
		var cat string
		_ = d.DB.R.QueryRowContext(ctx, `SELECT category FROM status WHERE connection_id = ? AND id = ?`, connID, statusID).Scan(&cat)
		switch cat {
		case "indeterminate":
			return model.LaneProg
		case "done":
			return model.LaneDone
		}
		return model.LaneTodo
	}
}

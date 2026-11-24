package app

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/store"
	syncpkg "github.com/crueber/cuisle/internal/sync"
	"github.com/crueber/cuisle/internal/view"
)

// Ticket modal surface (ADR 0015, ux-spec §14, architecture.md §3.2). Method sets are frozen. Get, Draft and SaveDraft
// are local only. The only network calls are Transitions (the Status combobox opening), the four writes, Refresh,
// EnsureMedia, OpenAttachment and SearchMentions, each an explicit user action (AGENTS.md invariant 2). Nothing here
// logs comment text, descriptions or field values.

// TicketService serves the ticket modal.
type TicketService struct{ d *Deps }

// Get = view.BuildTicketWith over cache.LoadTicket; local only, zero network.
func (s *TicketService) Get(ctx context.Context, standupID int64, issueID int64) (out view.TicketDetail, err error) {
	defer s.d.guard("Get", &err)
	return s.d.ticketDetail(ctx, standupID, issueID, false)
}

// Transitions is a NETWORK call (Status combobox opened); cached per issue; offline → cached list with Offline=true.
func (s *TicketService) Transitions(ctx context.Context, issueID int64) (out view.TransitionList, err error) {
	defer s.d.guard("Transitions", &err)
	d := s.d
	ref, err := d.issueRef(ctx, issueID)
	if err != nil {
		return view.TransitionList{}, err
	}
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return view.TransitionList{}, err
	}
	if !ok {
		return view.TransitionList{}, apperr.New(apperr.AuthMissing, "Connect to Jira to change the status.")
	}
	if err := authError(row.AuthState); err != nil {
		return view.TransitionList{}, err
	}
	if !d.writesOf(row).Transition {
		return view.TransitionList{Items: []view.TransitionOption{}}, nil
	}
	src, err := d.openSource(row, false)
	if err != nil {
		return view.TransitionList{}, err
	}
	list, err := src.Transitions(source.WithInteractive(ctx), ref.SourceID)
	offline := false
	var fetched time.Time
	if err != nil {
		if ctx.Err() != nil {
			return view.TransitionList{}, ctx.Err()
		}
		d.noteNet(err)
		if !apperr.Is(err, apperr.Offline) {
			d.noteSourceError(ctx, row, err)
			return view.TransitionList{}, apperr.From(err)
		}
		var has bool
		if list, fetched, has, err = d.readTransitionCache(ctx, issueID); err != nil {
			return view.TransitionList{}, err
		}
		_ = has
		offline = true
	} else {
		d.noteNet(nil)
		d.writeTransitionCache(ctx, issueID, list)
		fetched = d.now()
	}
	lane := d.laneResolver(ctx, ref.ConnectionID, issueID)
	url := source.IssueURLPrefix(row.Source, row.BaseURL) + ref.Key
	out = view.TransitionList{Items: make([]view.TransitionOption, 0, len(list)), FetchedAt: store.Ms(fetched), Offline: offline}
	for _, t := range list {
		out.Items = append(out.Items, view.TransitionOption{
			ID: t.ID, Name: t.Name, ToStatusName: t.ToStatusName, ToLane: lane(t.ToStatusID), NeedsScreen: t.NeedsScreen, URL: url,
		})
	}
	return out, nil
}

// authError is the typed refusal for a connection that cannot talk to the source ("" and "ok" are fine).
func authError(state string) error {
	switch state {
	case "", "ok":
		return nil
	case "missing":
		return apperr.New(apperr.AuthMissing, "Connect to Jira to continue.")
	}
	return apperr.New(apperr.AuthInvalid, "Jira rejected the token. Reconnect to continue.")
}

// writeSpec describes one of the four light writes.
type writeSpec struct {
	action  string // transition|assign|flag|comment (the ticket:write_failed action)
	allowed func(view.TicketWrites) bool
	send    func(ctx context.Context, src source.Source, ref issueRef) error
	// delta is the known local effect, applied when the follow-up refresh fails. May be nil.
	delta   func(ctx context.Context, d *Deps, ref issueRef) syncpkg.IssueDelta
	comment bool // a successful write deletes the draft
}

// write is the one path of every light write (architecture.md §3.2 "Light writes"): per-issue lock, one send with the
// interactive lane, never a retry; on success GetIssue then the one-issue apply in a transaction; on a failed refresh
// the known delta plus the refresh_failed warning.
func (s *TicketService) write(ctx context.Context, standupID, issueID int64, spec writeSpec) (view.TicketDetail, error) {
	d := s.d
	ref, err := d.issueRef(ctx, issueID)
	if err != nil {
		return view.TicketDetail{}, err
	}
	fail := func(err error) (view.TicketDetail, error) {
		ae := apperr.From(err)
		d.emit(EvTicketWriteFailed, TicketWriteFailed{
			IssueID: issueID, Key: ref.Key, Action: spec.action, ErrorCode: string(ae.Code), ErrorMessage: ae.Message,
		})
		return view.TicketDetail{}, ae
	}
	// The standup must still exist before anything is sent: the answer is built from it, and a write whose result
	// cannot be shown must not happen.
	if _, err := d.loadLive(ctx, standupID); err != nil {
		return fail(err)
	}
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return view.TicketDetail{}, err
	}
	if !ok {
		return fail(apperr.New(apperr.AuthMissing, "Connect to Jira to make changes."))
	}
	if err := authError(row.AuthState); err != nil {
		return fail(err)
	}
	if !spec.allowed(d.writesOf(row)) {
		return fail(apperr.New(apperr.Unsupported, "This connection can't make that change."))
	}
	release, free := d.lockIssue(issueID)
	if !free {
		return fail(errBusy())
	}
	defer release()
	src, err := d.openSource(row, false)
	if err != nil {
		return fail(err)
	}

	if err := spec.send(source.WithInteractive(ctx), src, ref); err != nil {
		if ctx.Err() == nil {
			d.noteNet(err)
			d.noteSourceError(ctx, row, err)
		}
		return fail(err)
	}
	d.noteNet(nil)

	// The write is done: finish the local side even if the caller went away.
	pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	var delta syncpkg.IssueDelta
	if spec.delta != nil {
		delta = spec.delta(pctx, d, ref) // before the apply drops the transitions cache
	}
	refreshFailed := false
	applied, err := d.refreshAndApply(pctx, src, row, ref)
	if err != nil {
		refreshFailed = true
		d.log().Debug("refresh after a write failed", "issue", ref.Key, "err", err)
		if delta != (syncpkg.IssueDelta{}) {
			if applied, err = d.Sync.ApplyIssueDelta(pctx, issueID, delta); err != nil {
				d.log().Warn("could not apply the local delta", "issue", ref.Key, "err", err)
			}
		}
	}
	if spec.comment {
		if err := d.DB.WriteTx(pctx, func(tx *sql.Tx) error { return deleteDraftTx(pctx, tx, issueID) }); err != nil {
			d.log().Warn("could not remove the sent draft", "err", err)
		}
	}
	d.emitIssueUpdated(applied, ref, "write")
	return d.ticketDetail(ctx, standupID, issueID, refreshFailed)
}

// refreshAndApply is GetIssue + the one-issue apply.
func (d *Deps) refreshAndApply(ctx context.Context, src source.Source, row cache.ConnRow, ref issueRef) (syncpkg.IssueApplied, error) {
	detail, err := src.GetIssue(source.WithInteractive(ctx), ref.SourceID)
	if err != nil {
		d.noteSourceError(ctx, row, err)
		return syncpkg.IssueApplied{}, err
	}
	return d.Sync.ApplyIssue(ctx, row.ID, detail)
}

// emitIssueUpdated tells the UI an issue changed locally: ticket:updated and one data:changed per affected standup.
func (d *Deps) emitIssueUpdated(a syncpkg.IssueApplied, ref issueRef, cause string) {
	changed, standups := a.Changed, a.StandupIDs
	if changed == nil {
		changed = []string{}
	}
	if standups == nil {
		standups = []int64{}
	}
	d.emit(syncpkg.EvTicketUpdated, syncpkg.TicketUpdated{IssueID: ref.ID, Key: ref.Key, StandupIDs: standups, Cause: cause, Changed: changed})
	for _, id := range standups {
		d.emit(syncpkg.EvDataChanged, syncpkg.DataChanged{StandupID: id, What: "view"})
	}
}

// Transition writes, refreshes the issue and applies it (one-issue apply).
func (s *TicketService) Transition(ctx context.Context, standupID, issueID int64, transitionID string) (out view.TicketDetail, err error) {
	defer s.d.guard("Transition", &err)
	if strings.TrimSpace(transitionID) == "" || len(transitionID) > 64 {
		return view.TicketDetail{}, apperr.Validationf("transitionId", "Choose a transition.")
	}
	return s.write(ctx, standupID, issueID, writeSpec{
		action:  "transition",
		allowed: func(w view.TicketWrites) bool { return w.Transition },
		send: func(ctx context.Context, src source.Source, ref issueRef) error {
			return src.Transition(ctx, ref.SourceID, transitionID)
		},
		delta: func(ctx context.Context, d *Deps, ref issueRef) syncpkg.IssueDelta {
			list, _, ok, _ := d.readTransitionCache(ctx, ref.ID)
			if ok {
				for _, t := range list {
					if t.ID == transitionID && t.ToStatusID != "" {
						to := t.ToStatusID
						return syncpkg.IssueDelta{StatusID: &to}
					}
				}
			}
			return syncpkg.IssueDelta{}
		},
	})
}

// Assign sets the assignee ("" = unassign).
func (s *TicketService) Assign(ctx context.Context, standupID, issueID int64, accountID string) (out view.TicketDetail, err error) {
	defer s.d.guard("Assign", &err)
	if len(accountID) > 128 {
		return view.TicketDetail{}, apperr.Validationf("accountId", "That is not a person.")
	}
	return s.write(ctx, standupID, issueID, writeSpec{
		action:  "assign",
		allowed: func(w view.TicketWrites) bool { return w.Assign },
		send: func(ctx context.Context, src source.Source, ref issueRef) error {
			return src.Assign(ctx, ref.SourceID, accountID)
		},
		delta: func(context.Context, *Deps, issueRef) syncpkg.IssueDelta {
			return syncpkg.IssueDelta{AssigneeID: &accountID}
		},
	})
}

// SetFlag flags or unflags the issue.
func (s *TicketService) SetFlag(ctx context.Context, standupID, issueID int64, flagged bool) (out view.TicketDetail, err error) {
	defer s.d.guard("SetFlag", &err)
	return s.write(ctx, standupID, issueID, writeSpec{
		action:  "flag",
		allowed: func(w view.TicketWrites) bool { return w.Flag },
		send: func(ctx context.Context, src source.Source, ref issueRef) error {
			return src.SetFlag(ctx, ref.SourceID, flagged)
		},
		delta: func(context.Context, *Deps, issueRef) syncpkg.IssueDelta {
			return syncpkg.IssueDelta{Flagged: &flagged}
		},
	})
}

// AddComment posts a comment; on success the draft is deleted.
func (s *TicketService) AddComment(ctx context.Context, standupID, issueID int64, in view.CommentInput) (out view.TicketDetail, err error) {
	defer s.d.guard("AddComment", &err)
	if strings.TrimSpace(in.Text) == "" {
		return view.TicketDetail{}, apperr.Validationf("text", "Write a comment first.")
	}
	if err := checkComment(in); err != nil {
		return view.TicketDetail{}, err
	}
	nc := toNewComment(in)
	return s.write(ctx, standupID, issueID, writeSpec{
		action:  "comment",
		allowed: func(w view.TicketWrites) bool { return w.Comment },
		send: func(ctx context.Context, src source.Source, ref issueRef) error {
			_, err := src.AddComment(ctx, ref.SourceID, nc)
			return err
		},
		comment: true,
	})
}

// Refresh is a NETWORK call (Refresh button / R): GetIssue + one-issue apply; 10 s per-issue cooldown → cooldown.
func (s *TicketService) Refresh(ctx context.Context, standupID, issueID int64) (out view.TicketDetail, err error) {
	defer s.d.guard("Refresh", &err)
	d := s.d
	ref, err := d.issueRef(ctx, issueID)
	if err != nil {
		return view.TicketDetail{}, err
	}
	if _, err := d.loadLive(ctx, standupID); err != nil {
		return view.TicketDetail{}, err
	}
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return view.TicketDetail{}, err
	}
	if !ok {
		return view.TicketDetail{}, apperr.New(apperr.AuthMissing, "Connect to Jira to refresh.")
	}
	if err := authError(row.AuthState); err != nil {
		return view.TicketDetail{}, err
	}
	if left := d.refreshWait(issueID); left > 0 {
		secs := (left + time.Second - 1) / time.Second
		return view.TicketDetail{}, apperr.New(apperr.Cooldown, "Refreshed a moment ago. Try again in "+secs.String()+".").
			WithRetryIn(left.Milliseconds() + 1)
	}
	release, free := d.lockIssue(issueID)
	if !free {
		return view.TicketDetail{}, errBusy()
	}
	defer release()
	src, err := d.openSource(row, false)
	if err != nil {
		return view.TicketDetail{}, err
	}
	applied, err := d.refreshAndApply(ctx, src, row, ref)
	if err != nil {
		if ctx.Err() != nil {
			return view.TicketDetail{}, ctx.Err()
		}
		d.noteNet(err)
		return view.TicketDetail{}, apperr.From(err)
	}
	d.noteNet(nil)
	d.emitIssueUpdated(applied, ref, "write")
	d.fetchThumbsAsync(src, row, ref)
	return d.ticketDetail(ctx, standupID, issueID, false)
}

// Draft returns the stored comment draft (empty text when none).
func (s *TicketService) Draft(ctx context.Context, issueID int64) (out view.CommentInput, err error) {
	defer s.d.guard("Draft", &err)
	return s.d.readDraft(ctx, issueID)
}

// SaveDraft autosaves the draft; empty text deletes it.
func (s *TicketService) SaveDraft(ctx context.Context, issueID int64, in view.CommentInput) (out SaveAck, err error) {
	defer s.d.guard("SaveDraft", &err)
	if err := checkComment(in); err != nil {
		return SaveAck{}, err
	}
	if _, err := s.d.issueRef(ctx, issueID); err != nil {
		return SaveAck{}, err
	}
	if err := s.d.writeDraft(ctx, issueID, in); err != nil {
		return SaveAck{}, err
	}
	return SaveAck{SavedAt: store.Ms(s.d.now())}, nil
}

// SearchMentions backs "@" in the composer: the people Cuisle already knows first (the board's, then cached people),
// then the source's user search for a non-empty query (a Jira-backed combobox typing trigger).
func (s *TicketService) SearchMentions(ctx context.Context, standupID int64, query string) (out []UserOption, err error) {
	defer s.d.guard("SearchMentions", &err)
	d := s.d
	out = []UserOption{}
	st, err := d.loadLive(ctx, standupID)
	if err != nil {
		return nil, err
	}
	row, ok, err := d.connRow(ctx)
	if err != nil || !ok {
		return out, err
	}
	speakers, err := d.Settings.Speakers(ctx, standupID)
	if err != nil {
		return nil, err
	}
	onBoard := make(map[string]bool, len(speakers))
	for _, sp := range speakers {
		onBoard[sp.AccountID] = true
	}
	query = strings.TrimSpace(query)
	seen := map[string]bool{}
	var people []model.Person
	add := func(list []model.Person) {
		for _, p := range list {
			if seen[p.AccountID] || !p.Active && !onBoard[p.AccountID] {
				continue
			}
			seen[p.AccountID] = true
			people = append(people, p)
		}
	}
	board, err := d.boardPeople(ctx, row, standupID)
	if err != nil {
		return nil, err
	}
	if query == "" {
		add(board)
	} else {
		lq := strings.ToLower(query)
		var match []model.Person
		for _, p := range board {
			if strings.Contains(strings.ToLower(p.DisplayName), lq) {
				match = append(match, p)
			}
		}
		add(match)
		cached, err := cache.SearchPeople(ctx, d.DB.R, row.ID, query, userLimit)
		if err != nil {
			return nil, err
		}
		add(cached)
		if st.SpaceKey != "" {
			ictx := source.WithInteractive(ctx)
			_, err := d.live(ictx, row, func(src source.Source) error {
				found, err := src.SearchUsers(ictx, st.SpaceKey, query)
				if err != nil {
					return err
				}
				if err := cache.UpsertPeople(ctx, d.DB.W, row.ID, found, d.now()); err != nil {
					d.log().Warn("could not cache people", "err", err)
				}
				add(found)
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	for _, p := range people {
		if len(out) >= userLimit {
			break
		}
		out = append(out, UserOption{AccountID: p.AccountID, DisplayName: p.DisplayName, Initials: view.Initials(p.DisplayName), OnBoard: onBoard[p.AccountID]})
	}
	return out, nil
}

// ── Ticket field preferences (Configure › Ticket fields, ux-spec §14.8; per connection) ──

// fieldConn is the connection id the preferences belong to; ok=false when nothing is connected yet.
func (d *Deps) fieldConn(ctx context.Context) (int64, bool, error) {
	row, ok, err := d.connRow(ctx)
	return row.ID, ok, err
}

func (s *ConnectionService) TicketFields(ctx context.Context) (out view.TicketFieldPrefs, err error) {
	defer s.d.guard("TicketFields", &err)
	id, ok, err := s.d.fieldConn(ctx)
	if err != nil || !ok {
		return emptyFieldPrefs(), err
	}
	return s.d.fieldPrefs(ctx, id)
}

func (s *ConnectionService) PinTicketField(ctx context.Context, fieldID string) (out view.TicketFieldPrefs, err error) {
	defer s.d.guard("PinTicketField", &err)
	d := s.d
	id, ok, err := d.fieldConn(ctx)
	if err != nil {
		return emptyFieldPrefs(), err
	}
	if !ok {
		return emptyFieldPrefs(), apperr.New(apperr.AuthMissing, "Connect to Jira to pin fields.")
	}
	err = d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		var custom int
		if err := tx.QueryRowContext(ctx, `SELECT custom FROM field_def WHERE connection_id = ? AND field_id = ?`, id, fieldID).Scan(&custom); err != nil {
			if err == sql.ErrNoRows {
				return apperr.New(apperr.NotFound, "That field is not known.")
			}
			return apperr.Wrap(apperr.DB, "The field list could not be read.", err)
		}
		if custom == 0 {
			return apperr.Validationf("fieldId", "Only custom fields can be pinned.")
		}
		order, err := pinnedOrder(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, f := range order {
			if f == fieldID {
				return nil // already pinned
			}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ticket_field_pref (connection_id, field_id, position, hidden) VALUES (?, ?, ?, 0)
			ON CONFLICT(connection_id, field_id) DO UPDATE SET position = excluded.position`, id, fieldID, len(order))
		if err != nil {
			return apperr.Wrap(apperr.DB, "The preference could not be saved.", err)
		}
		return nil
	})
	if err != nil {
		return emptyFieldPrefs(), err
	}
	return d.fieldPrefs(ctx, id)
}

func (s *ConnectionService) UnpinTicketField(ctx context.Context, fieldID string) (out view.TicketFieldPrefs, err error) {
	defer s.d.guard("UnpinTicketField", &err)
	d := s.d
	id, ok, err := d.fieldConn(ctx)
	if err != nil || !ok {
		return emptyFieldPrefs(), err
	}
	err = d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_field_pref WHERE connection_id = ? AND field_id = ? AND hidden = 0`, id, fieldID); err != nil {
			return apperr.Wrap(apperr.DB, "The preference could not be saved.", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ticket_field_pref SET position = NULL WHERE connection_id = ? AND field_id = ? AND hidden = 1`, id, fieldID); err != nil {
			return apperr.Wrap(apperr.DB, "The preference could not be saved.", err)
		}
		order, err := pinnedOrder(ctx, tx, id)
		if err != nil {
			return err
		}
		return renumber(ctx, tx, id, order)
	})
	if err != nil {
		return emptyFieldPrefs(), err
	}
	return d.fieldPrefs(ctx, id)
}

func (s *ConnectionService) MoveTicketField(ctx context.Context, fieldID string, toPosition int) (out SaveAck, err error) {
	defer s.d.guard("MoveTicketField", &err)
	d := s.d
	id, ok, err := d.fieldConn(ctx)
	if err != nil {
		return SaveAck{}, err
	}
	if !ok {
		return SaveAck{}, apperr.New(apperr.NotFound, "That field is not pinned.")
	}
	err = d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		order, err := pinnedOrder(ctx, tx, id)
		if err != nil {
			return err
		}
		rest := make([]string, 0, len(order))
		found := false
		for _, f := range order {
			if f == fieldID {
				found = true
				continue
			}
			rest = append(rest, f)
		}
		if !found {
			return apperr.New(apperr.NotFound, "That field is not pinned.")
		}
		pos := min(max(toPosition, 0), len(rest))
		next := append(append(append([]string{}, rest[:pos]...), fieldID), rest[pos:]...)
		return renumber(ctx, tx, id, next)
	})
	if err != nil {
		return SaveAck{}, err
	}
	return SaveAck{SavedAt: store.Ms(d.now())}, nil
}

func (s *ConnectionService) SetTicketFieldHidden(ctx context.Context, fieldID string, hidden bool) (out view.TicketFieldPrefs, err error) {
	defer s.d.guard("SetTicketFieldHidden", &err)
	d := s.d
	id, ok, err := d.fieldConn(ctx)
	if err != nil {
		return emptyFieldPrefs(), err
	}
	if !ok {
		return emptyFieldPrefs(), apperr.New(apperr.NotFound, "That field is not known.")
	}
	err = d.editFieldPrefs(ctx, id, fieldID, hidden, func(tx *sql.Tx) error {
		if hidden {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO ticket_field_pref (connection_id, field_id, position, hidden) VALUES (?, ?, NULL, 1)
				ON CONFLICT(connection_id, field_id) DO UPDATE SET hidden = 1`, id, fieldID)
			if err != nil {
				return apperr.Wrap(apperr.DB, "The preference could not be saved.", err)
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ticket_field_pref WHERE connection_id = ? AND field_id = ? AND position IS NULL`, id, fieldID); err != nil {
			return apperr.Wrap(apperr.DB, "The preference could not be saved.", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE ticket_field_pref SET hidden = 0 WHERE connection_id = ? AND field_id = ?`, id, fieldID); err != nil {
			return apperr.Wrap(apperr.DB, "The preference could not be saved.", err)
		}
		return nil
	})
	if err != nil {
		return emptyFieldPrefs(), err
	}
	return d.fieldPrefs(ctx, id)
}

func emptyFieldPrefs() view.TicketFieldPrefs {
	return view.TicketFieldPrefs{Pinned: []view.FieldPref{}, Hidden: []view.FieldPref{}, Available: []view.FieldOption{}}
}

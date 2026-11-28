package app

// Shared helpers of the services: clocks, the panic guard, error mapping, the connection/source plumbing and the
// view/config builders every service (and the sink BuildFunc) goes through. Nothing here touches the network except
// openSource's callers; every network call in this package is behind an explicit user trigger (AGENTS.md section 3).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/store"
	syncpkg "github.com/crueber/cuisle/internal/sync"
	"github.com/crueber/cuisle/internal/view"
)

// SystemClock is the production sync.Clock: wall time in the process's local zone.
type SystemClock struct{}

func (SystemClock) Now() time.Time           { return time.Now() }
func (SystemClock) Location() *time.Location { return time.Local }

// ── small accessors ─────────────────────────────────────────────────────────────

func (d *Deps) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// now is the wall clock used for timestamps the app itself records.
func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	if d.Clock != nil {
		return d.Clock.Now()
	}
	return time.Now()
}

// viewTime is "now" in the viewer's zone: view.Build derives today's local date from its location.
func (d *Deps) viewTime() time.Time {
	if d.Clock != nil {
		return d.Clock.Now().In(d.Clock.Location())
	}
	return d.now().In(time.Local)
}

func (d *Deps) location() *time.Location {
	if d.Clock != nil {
		return d.Clock.Location()
	}
	return time.Local
}

func (d *Deps) emit(name string, payload any) {
	if d.Emitter != nil {
		d.Emitter.Emit(name, payload)
	}
}

// guard is deferred at the top of every bound method: a panic is logged with its value and stack and becomes a
// typed internal error (architecture.md section 3.4). The service keeps working.
func (d *Deps) guard(method string, errp *error) {
	if r := recover(); r != nil {
		d.log().Error("panic in service method", "method", method, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
		*errp = apperr.New(apperr.Internal, "Something went wrong.")
	}
}

// srcOpts returns the adapter options with the shared HTTP client filled in and the stored provider meta.
func (d *Deps) srcOpts(meta map[string]any, full bool) source.Options {
	o := d.SourceOpts
	if o.HTTP == nil {
		o.HTTP = d.HTTP
	}
	if o.Logger == nil {
		o.Logger = d.Logger
	}
	if o.Now == nil {
		o.Now = d.now
	}
	o.Meta = meta
	o.Full = full
	return o
}

func saveAck(a settings.Ack) SaveAck {
	return SaveAck{Rev: a.Rev, SavedAt: store.Ms(a.SavedAt), NeedsSync: a.NeedsSync}
}

func ackResult(a settings.Ack, err error) (SaveAck, error) {
	if err != nil {
		return SaveAck{}, err
	}
	return saveAck(a), nil
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

func ticketWrites(src source.Source) view.TicketWrites {
	w := src.Capabilities().Writes
	return view.TicketWrites{Transition: w.Transition, Assign: w.Assign, Flag: w.Flag, Comment: w.Comment}
}

func errStandupGone() error { return apperr.New(apperr.NotFound, "That standup no longer exists.") }

// ── connection and source ───────────────────────────────────────────────────────

// connRow returns the single connection, ok=false when none exists.
func (d *Deps) connRow(ctx context.Context) (cache.ConnRow, bool, error) {
	row, err := cache.GetConnection(ctx, d.DB.R)
	if err != nil {
		if apperr.Is(err, apperr.NotFound) {
			return cache.ConnRow{}, false, nil
		}
		return cache.ConnRow{}, false, err
	}
	return row, true, nil
}

func (d *Deps) capabilities(kind, baseURL, account string) source.Capabilities {
	var caps source.Capabilities
	if kind != "" {
		if src, err := source.Open(model.Connection{Source: kind, BaseURL: baseURL, Account: account}, "", d.srcOpts(nil, false)); err == nil {
			caps = src.Capabilities()
		}
	}
	if caps.AuthFields == nil {
		caps.AuthFields = []string{}
	}
	return caps
}

// connection maps the stored connection row to the bound type. No network.
func (d *Deps) connection(ctx context.Context) (Connection, error) {
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return Connection{}, err
	}
	if !ok {
		return Connection{State: "missing", DevActivity: "unknown", Capabilities: d.capabilities("", "", "")}, nil
	}
	return d.connectionOf(row), nil
}

func (d *Deps) connectionOf(row cache.ConnRow) Connection {
	state := row.AuthState
	if state == "" {
		state = "unknown"
	}
	dev := row.DevActivityState
	if dev == "" {
		dev = "unknown"
	}
	return Connection{
		State: state, Source: row.Source, BaseURL: row.BaseURL, Account: row.Account, DisplayName: row.DisplayName,
		DevActivity: dev, Capabilities: d.capabilities(row.Source, row.BaseURL, row.Account),
	}
}

// openSource builds the adapter for row with the token read from the keychain (never logged, never kept).
func (d *Deps) openSource(row cache.ConnRow, full bool) (source.Source, error) {
	secret, err := d.Keyring.Get(secrets.ConnectionAccount(row.Source, row.BaseURL, row.Account))
	switch {
	case err == nil:
	case errors.Is(err, secrets.ErrNotFound):
		return nil, apperr.New(apperr.AuthMissing, "Connect to Jira to continue.")
	case errors.Is(err, secrets.ErrUnavailable):
		return nil, apperr.Wrap(apperr.KeychainUnavailable, "The keychain is not available.", err)
	default:
		return nil, apperr.Wrap(apperr.KeychainUnavailable, "The keychain could not be read.", err)
	}
	return source.Open(model.Connection{ID: row.ID, Source: row.Source, BaseURL: row.BaseURL, Account: row.Account},
		secret, d.srcOpts(row.SourceMeta, full))
}

// requireSource loads the single connection, verifies its auth state and opens its source adapter.
func (d *Deps) requireSource(ctx context.Context, missingMsg string) (source.Source, cache.ConnRow, error) {
	row, ok, err := d.connRow(ctx)
	if err != nil {
		return nil, cache.ConnRow{}, err
	}
	if !ok {
		return nil, cache.ConnRow{}, apperr.New(apperr.AuthMissing, missingMsg)
	}
	if err := authError(row.AuthState); err != nil {
		return nil, cache.ConnRow{}, err
	}
	src, err := d.openSource(row, false)
	if err != nil {
		return nil, cache.ConnRow{}, err
	}
	return src, row, nil
}

// setAuth persists the connection's auth state and tells the UI.
func (d *Deps) setAuth(ctx context.Context, row cache.ConnRow, state string) {
	if err := cache.SetAuthState(context.WithoutCancel(ctx), d.DB.W, row.ID, state, d.now()); err != nil {
		d.log().Warn("could not save the connection state", "state", state, "err", err)
	}
	d.emit(syncpkg.EvConnectionAuth, syncpkg.ConnectionAuth{State: state})
}

// noteSourceError records what a source error says about the connection (a rejected token turns it invalid).
func (d *Deps) noteSourceError(ctx context.Context, row cache.ConnRow, err error) {
	if apperr.Is(err, apperr.AuthInvalid) && row.AuthState != "invalid" {
		d.setAuth(ctx, row, "invalid")
	}
}

// live runs fn against the source when the connection is usable. It returns used=true when fn succeeded. Any
// failure (offline, rejected token, rate limit, ...) returns used=false so the caller answers from the caches;
// only the caller's own cancellation is an error. A missing, invalid or token-less connection never touches the
// network (jira-sync.md section 4.7).
func (d *Deps) live(ctx context.Context, row cache.ConnRow, fn func(src source.Source) error) (used bool, err error) {
	if row.AuthState == "invalid" || row.AuthState == "missing" {
		return false, nil
	}
	src, err := d.openSource(row, false)
	if err != nil {
		if apperr.Is(err, apperr.AuthMissing) {
			d.setAuth(ctx, row, "missing")
		}
		d.log().Debug("source unavailable, answering from the cache", "err", err)
		return false, nil
	}
	if err := fn(src); err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		d.noteNet(err)
		d.noteSourceError(ctx, row, err)
		d.log().Debug("source call failed, answering from the cache", "err", err)
		return false, nil
	}
	d.noteNet(nil)
	return true, nil
}

// ── views and configs ───────────────────────────────────────────────────────────

// loadLive returns a live (not archived) standup or not_found.
func (d *Deps) loadLive(ctx context.Context, id int64) (settings.Standup, error) {
	st, _, err := d.Settings.Get(ctx, id)
	if err != nil {
		return settings.Standup{}, err
	}
	if !st.DeletedAt.IsZero() {
		return settings.Standup{}, errStandupGone()
	}
	return st, nil
}

// snapshot loads the effective config and the live snapshot of a standup in one read transaction.
func (d *Deps) snapshot(ctx context.Context, id int64, asOf time.Time) (model.BoardSnapshot, error) {
	var snap model.BoardSnapshot
	err := d.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		cfg, err := settings.EffectiveTx(ctx, tx, id)
		if err != nil {
			return err
		}
		snap, err = cache.LoadCurrentTx(ctx, tx, cfg, asOf)
		return err
	})
	if err != nil {
		return model.BoardSnapshot{}, err
	}
	if row, ok, err := d.connRow(ctx); err == nil && ok {
		snap.IssueURLPrefix = source.IssueURLPrefix(row.Source, row.BaseURL)
	}
	return snap, nil
}

// buildViewCore is view.Build over the live snapshot, without the sync status.
func (d *Deps) buildViewCore(ctx context.Context, id int64) (view.StandupView, settings.Standup, error) {
	st, err := d.loadLive(ctx, id)
	if err != nil {
		return view.StandupView{}, st, err
	}
	asOf := d.viewTime()
	snap, err := d.snapshot(ctx, id, asOf)
	if err != nil {
		return view.StandupView{}, st, err
	}
	return view.Build(snap, asOf), st, nil
}

// buildView is the StandupView with the sync status filled in.
func (d *Deps) buildView(ctx context.Context, id int64) (view.StandupView, error) {
	v, st, err := d.buildViewCore(ctx, id)
	if err != nil {
		return view.StandupView{}, err
	}
	v.Sync, err = d.syncStatusOf(ctx, id, st.NeedsSync)
	if err != nil {
		return view.StandupView{}, err
	}
	return v, nil
}

func toSyncStatus(s syncpkg.Status) view.SyncStatus {
	warnings := s.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	return view.SyncStatus{
		StandupID: s.StandupID, RunID: s.RunID, State: s.State, Mode: s.Mode, Phase: s.Phase, Done: s.Done, Total: s.Total,
		LastSuccessAt: store.Ms(s.LastSuccessAt), LastAttemptAt: store.Ms(s.LastAttemptAt), LastStatus: s.LastStatus,
		Warnings: warnings, ErrorCode: s.ErrorCode, ErrorMessage: s.ErrorMessage, NeedsSync: s.NeedsSync,
	}
}

func (d *Deps) syncStatuses(ctx context.Context) ([]view.SyncStatus, error) {
	list, err := d.Sync.Status(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]view.SyncStatus, 0, len(list))
	for _, s := range list {
		out = append(out, toSyncStatus(s))
	}
	return out, nil
}

// syncStatusOf is one standup's status; the orchestrator omits drafts, which get an idle status.
func (d *Deps) syncStatusOf(ctx context.Context, id int64, needsSync bool) (view.SyncStatus, error) {
	all, err := d.syncStatuses(ctx)
	if err != nil {
		return view.SyncStatus{}, err
	}
	for _, s := range all {
		if s.StandupID == id {
			return s, nil
		}
	}
	return view.SyncStatus{StandupID: id, State: "idle", Warnings: []string{}, NeedsSync: needsSync}, nil
}

// standupConfig is the Configure form model of one live standup.
func (d *Deps) standupConfig(ctx context.Context, id int64) (view.StandupConfig, error) {
	st, err := d.loadLive(ctx, id)
	if err != nil {
		return view.StandupConfig{}, err
	}
	cfg, err := d.Settings.Effective(ctx, id)
	if err != nil {
		return view.StandupConfig{}, err
	}
	unmapped, err := d.Settings.UnmappedStatuses(ctx, id)
	if err != nil {
		return view.StandupConfig{}, err
	}
	in := view.ConfigInput{Position: st.Position, NeedsSync: st.NeedsSync, UnmappedStatuses: unmapped}
	if st.ExtraFilterOK != nil {
		in.FilterCheck = &view.FilterCheck{
			OK: *st.ExtraFilterOK, Errors: splitFilterErrors(st.ExtraFilterError), CheckedAt: store.Ms(st.FilterCheckedAt),
		}
	}
	if !st.Draft() {
		active, err := d.activeByAccount(ctx, id)
		if err != nil {
			return view.StandupConfig{}, err
		}
		in.ActiveByAccount = active
	}
	return view.BuildConfig(cfg, in), nil
}

func splitFilterErrors(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "; ")
}

// activeByAccount counts the active tickets per assignee from the current board membership without building StandupView.
func (d *Deps) activeByAccount(ctx context.Context, id int64) (map[string]int, error) {
	var out map[string]int
	err := d.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = cache.StandupActiveByAssigneeTx(ctx, tx, id)
		return err
	})
	return out, err
}

func (d *Deps) tabs(ctx context.Context) ([]view.StandupTab, error) {
	rows, err := cache.Tabs(ctx, d.DB.R)
	if err != nil {
		return nil, err
	}
	out := make([]view.StandupTab, 0, len(rows))
	for _, r := range rows {
		out = append(out, view.StandupTab{
			ID: r.ID, Rev: r.Rev, Name: r.Name, SpaceKey: r.SpaceKey, Position: r.Position, ActiveCount: r.ActiveCount,
			SpeakerCount: r.SpeakerCount, Configured: r.Configured, NeedsSync: r.NeedsSync, LastSyncedAt: store.Ms(r.LastSuccessAt),
		})
	}
	return out, nil
}

// saveSourceMeta persists the adapter's bookkeeping (Jira: field ids) into connection.source_meta after a call that
// may have discovered it outside a sync (a board pick). The dev-activity state is carried over from the stored row.
// A failure only costs a rediscovery at the next sync, so it is logged, not returned.
func (d *Deps) saveSourceMeta(ctx context.Context, row cache.ConnRow, meta map[string]any) {
	if len(meta) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	if cur, err := cache.GetConnection(ctx, d.DB.R); err == nil {
		row = cur
	}
	if err := d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		return cache.SetConnectionMetaTx(ctx, tx, row.ID, meta, row.DevActivityState, row.DevCheckedAt)
	}); err != nil {
		d.log().Warn("could not save the source meta", "err", err)
	}
}

package sync

import (
	"context"
	"database/sql"
	"errors"
	"runtime/debug"
	gosync "sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
)

// Warning codes carried in sync_run.warnings and sync:finished.warnings.
const (
	WarnDevUnavailable = "dev_activity_unavailable"
	WarnDevPartial     = "dev_activity_partial"
)

// runner is the state of one run goroutine (private to this package; the frozen run struct stays untouched).
type runner struct {
	o         *Orchestrator
	r         *run
	requested string
	prog      *progressReporter

	conn cache.ConnRow
	cfg  model.EffectiveConfig
	src  source.Source

	mode     string // effective mode (after promotion)
	f        *Fetched
	scanned  int
	warnings []string
	applied  ApplyResult
	stamps   map[string]cache.IssueStamp // cached issue stamps of the scanned ids
	devFP    map[string]string           // source id → dev fingerprint the scan carried (absent = unknown), jira-sync §2.11

	fieldDefs []model.FieldDef // the field registry the content phase read (stored with the apply)
}

// outcome is how a run ended.
type outcome struct {
	status string // ok|partial|cancelled|failed
	err    error
}

func (rn *runner) warn(w string) {
	for _, have := range rn.warnings {
		if have == w {
			return
		}
	}
	rn.warnings = append(rn.warnings, w)
}

func (rn *runner) logf(ctx context.Context, level, phase, msg string) {
	err := cache.LogRun(context.WithoutCancel(ctx), rn.o.opts.DB.W, rn.r.id, rn.o.opts.Clock.Now(), level, phase, msg)
	if err != nil {
		rn.o.opts.Logger.Debug("sync: log line not saved", "run", rn.r.id, "err", err)
	}
}

// execute runs one sync to its end and finalises it. Panics become a failed run with apperr internal.
func (o *Orchestrator) execute(ctx context.Context, r *run, requested string) {
	defer o.wg.Done()
	// jira-sync.md section 1: a run is limited to runTimeout; the deadline ends it as a failed run with apperr timeout.
	ctx, stop := context.WithTimeout(ctx, runTimeout)
	defer stop()
	rn := &runner{o: o, r: r, requested: requested, mode: r.mode, warnings: []string{}}
	rn.prog = newProgressReporter(o, r)

	var out outcome
	func() {
		defer func() {
			if p := recover(); p != nil {
				o.opts.Logger.Error("sync: run panicked", "run", r.id, "standup", r.standupID, "panic", p, "stack", string(debug.Stack()))
				r.cancel() // aborts a transaction the panic may have left open
				out = outcome{status: StatusFailed, err: apperr.New(apperr.Internal, "Something went wrong while syncing.")}
			}
		}()
		out = rn.run(ctx)
	}()
	// finish releases the standup in a defer of its own; a panic out of it (an emitter, a hook) is logged here so it
	// cannot take the process down.
	defer func() {
		if p := recover(); p != nil {
			o.opts.Logger.Error("sync: finishing the run panicked", "run", r.id, "panic", p, "stack", string(debug.Stack()))
		}
	}()
	rn.finish(ctx, out)
	// Phase 9 runs after sync:finished and never changes the run's status (jira-sync.md §4.3).
	if (out.status == StatusOK || out.status == StatusPartial) && rn.f != nil && rn.src != nil {
		rn.mediaPhase(rn.f)
	}
}

// runTimeout is the limit for one sync run (jira-sync.md section 1). A var so tests can shorten it.
var runTimeout = 5 * time.Minute

// stopped is the outcome of a run whose context ended: the deadline is a failed run (timeout), a cancel is cancelled.
func stopped(ctx context.Context) outcome {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return outcome{status: StatusFailed, err: apperr.New(apperr.Timeout, "Sync took too long and was stopped. Try again.")}
	}
	return outcome{status: StatusCancelled}
}

// run is phases 0–7.
func (rn *runner) run(ctx context.Context) outcome {
	o := rn.o

	select {
	case o.sem <- struct{}{}:
		defer func() { <-o.sem }()
	case <-ctx.Done():
		return stopped(ctx)
	}

	if err := rn.load(ctx); err != nil {
		return rn.failed(ctx, err)
	}
	if err := rn.preflight(ctx); err != nil {
		return rn.failed(ctx, err)
	}
	if err := rn.syncPhases(ctx); err != nil {
		return rn.failed(ctx, err)
	}

	// Phase 7. A cancel that arrives while the transaction runs does not abort it: the apply is short and atomic.
	if ctx.Err() != nil {
		return stopped(ctx)
	}
	rn.prog.report(PhaseApply, 0, 1)
	actx, acancel := context.WithCancel(context.WithoutCancel(ctx))
	defer acancel()
	res, updates, err := o.applyWith(actx, rn.f, &applyExtras{FieldDefs: rn.fieldDefs})
	if err != nil {
		return rn.failed(ctx, err)
	}
	rn.applied = res
	for _, u := range updates {
		o.emit(EvTicketUpdated, u)
	}
	rn.prog.report(PhaseApply, 1, 1)
	rn.logf(ctx, "info", PhaseApply, "applied")

	if rn.f.Partial {
		return outcome{status: StatusPartial}
	}
	return outcome{status: StatusOK}
}

// failed maps an error to cancelled (the run was cancelled) or failed.
func (rn *runner) failed(ctx context.Context, err error) outcome {
	if ctx.Err() != nil {
		return stopped(ctx)
	}
	return outcome{status: StatusFailed, err: err}
}

// load reads the run's inputs: connection, effective config, and re-checks the promotions known before the run.
func (rn *runner) load(ctx context.Context) error {
	o := rn.o
	conn, err := cache.GetConnection(ctx, o.opts.DB.R)
	if err != nil {
		return err
	}
	rn.conn = conn
	cfg, err := settings.New(o.opts.DB, o.opts.Clock.Now).Effective(ctx, rn.r.standupID)
	if err != nil {
		return err
	}
	if cfg.BoardID == "" {
		return apperr.New(apperr.Validation, "Choose a board for this standup before syncing.")
	}
	rn.cfg = cfg

	state, err := cache.GetSyncState(ctx, o.opts.DB.R, rn.r.standupID)
	if err != nil {
		return err
	}
	if rn.requested == ModeFull || preKnownFull(state, rn.r.started) {
		rn.promote(ctx, ModeFull)
	}
	return nil
}

// promote makes the run full and records it on the sync_run row.
func (rn *runner) promote(ctx context.Context, mode string) {
	if rn.mode == mode {
		return
	}
	rn.mode = mode
	o := rn.o
	o.mu.Lock()
	rn.r.status.Mode = mode
	o.mu.Unlock()
	wctx := context.WithoutCancel(ctx)
	err := o.opts.DB.WriteTx(wctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(wctx, `UPDATE sync_run SET mode = ? WHERE id = ?`, mode, rn.r.id)
		return err
	})
	if err != nil {
		o.opts.Logger.Debug("sync: run mode not saved", "run", rn.r.id, "err", err)
	}
}

// preflight is phase 0: keychain token, open the source, check the credentials.
func (rn *runner) preflight(ctx context.Context) error {
	o := rn.o
	account := secrets.ConnectionAccount(rn.conn.Source, rn.conn.BaseURL, rn.conn.Account)
	secret, err := o.opts.Keyring.Get(account)
	switch {
	case errors.Is(err, secrets.ErrNotFound):
		return apperr.New(apperr.AuthMissing, "No Jira token is saved. Connect to Jira to sync.")
	case errors.Is(err, secrets.ErrUnavailable):
		return apperr.New(apperr.KeychainUnavailable, "The system keychain is not available.")
	case err != nil:
		return apperr.Wrap(apperr.KeychainUnavailable, "The system keychain could not be read.", err)
	}

	so := o.opts.SourceOpts
	so.Meta = rn.conn.SourceMeta
	so.Full = rn.mode == ModeFull // the adapter refreshes its reference data (jira-sync.md §2.2, §2.13)
	if so.Logger == nil {
		so.Logger = o.opts.Logger
	}
	src, err := o.opts.OpenSource(model.Connection{
		ID: rn.conn.ID, Source: rn.conn.Source, BaseURL: rn.conn.BaseURL, Account: rn.conn.Account,
	}, secret, so)
	if err != nil {
		return err
	}
	rn.src = src

	if _, err := src.Test(ctx); err != nil {
		return err
	}
	if rn.conn.AuthState != "ok" {
		now := o.opts.Clock.Now()
		if err := cache.SetAuthState(context.WithoutCancel(ctx), o.opts.DB.W, rn.conn.ID, "ok", now); err != nil {
			o.opts.Logger.Debug("sync: auth state not saved", "err", err)
		} else {
			o.emit(EvConnectionAuth, ConnectionAuth{State: "ok"})
		}
	}
	return nil
}

// syncPhases is phases 1–6: config, scan, hydrate, dev. It leaves rn.f ready for apply.
func (rn *runner) syncPhases(ctx context.Context) error {
	f := &Fetched{
		RunID: rn.r.id, StandupID: rn.r.standupID, ConnectionID: rn.conn.ID, StartedAt: rn.r.started,
		Warnings: []string{},
	}
	rn.f = f

	lanes, err := rn.configPhase(ctx, f)
	if err != nil {
		return err
	}
	members, err := rn.scanPhase(ctx, f, lanes)
	if err != nil {
		return err
	}
	if err := rn.hydratePhase(ctx, f, members); err != nil {
		return err
	}
	if err := rn.contentPhase(ctx, f); err != nil {
		return err
	}
	if err := rn.devPhase(ctx, f, members, lanes); err != nil {
		return err
	}

	f.Mode = rn.mode
	f.SourceMeta = rn.src.Meta()
	f.Warnings = append([]string{}, rn.warnings...)
	return nil
}

// finish finalises the sync_run row, publishes the events, runs AfterSync and releases the standup.
func (rn *runner) finish(ctx context.Context, out outcome) {
	o, r := rn.o, rn.r
	fctx := context.WithoutCancel(ctx)

	// Trim runs after the standup is released (this defer is registered first, so it runs last): it holds the writer
	// and must not delay Wait, a Start of the same standup or the release itself.
	trimAfter := false
	defer func() {
		if !trimAfter {
			return
		}
		// data-model.md section 5 / jira-sync.md 4.3 phase 7: trim operational rows after a good run. It never
		// changes the run's status.
		if err := cache.Trim(fctx, o.opts.DB.W, o.opts.Clock.Now()); err != nil {
			o.opts.Logger.Warn("sync: trimming old operational rows failed", "run", r.id, "err", err)
		}
	}()

	// However this function ends (an emitter or hook that panics included), the standup is released and Wait wakes.
	defer func() {
		o.mu.Lock()
		if o.runs[r.standupID] == r {
			delete(o.runs, r.standupID)
		}
		o.mu.Unlock()
		close(r.done)
	}()

	requests, throttled := 0, 0
	if rn.src != nil {
		requests, throttled = rn.src.Requests()
	}
	hydrated := 0
	if rn.f != nil && out.status != StatusCancelled && out.status != StatusFailed {
		hydrated = rn.applied.Hydrated
	}
	if out.status == StatusCancelled {
		rn.logf(fctx, "info", "", "cancelled")
	}
	warnings := append([]string{}, rn.warnings...)
	finishedAt := o.opts.Clock.Now()

	var code, msg string
	if out.status == StatusFailed {
		e := apperr.From(out.err)
		code, msg = string(e.Code), e.Message
		o.opts.Logger.Debug("sync: run failed", "run", r.id, "code", code, "detail", e.Detail)
		rn.logf(fctx, "error", "", "failed: "+code)
	}

	run := cache.Run{
		ID: r.id, Status: out.status, FinishedAt: finishedAt, IssuesScanned: rn.scanned, IssuesHydrated: hydrated,
		Requests: requests, Throttled: throttled, ErrorCode: code, ErrorMessage: msg, Warnings: warnings,
	}
	if err := cache.FinishRun(fctx, o.opts.DB.W, run); err != nil {
		o.opts.Logger.Error("sync: could not finalise the run", "run", r.id, "err", err)
	}

	// An auth failure marks the connection before the run's own events.
	switch code {
	case string(apperr.AuthInvalid), string(apperr.AuthMissing):
		state := "invalid"
		if code == string(apperr.AuthMissing) {
			state = "missing"
		}
		if err := cache.SetAuthState(fctx, o.opts.DB.W, rn.conn.ID, state, finishedAt); err != nil {
			o.opts.Logger.Error("sync: could not save the sign-in state", "err", err)
		}
		o.emit(EvConnectionAuth, ConnectionAuth{State: state})
	}

	final := Status{
		StandupID: r.standupID, RunID: r.id, State: stateIdle, Mode: rn.mode, LastStatus: out.status,
		Warnings: warnings, ErrorCode: code, ErrorMessage: msg,
	}
	if out.status == StatusFailed {
		final.State = stateFailed
	}
	o.mu.Lock()
	o.last[r.standupID] = final
	r.status = final // not "running" any more: a new Start may begin while the events below go out
	o.mu.Unlock()

	o.emit(EvSyncFinished, SyncFinished{
		RunID: r.id, StandupID: r.standupID, Status: out.status, ErrorCode: code, ErrorMessage: msg,
		Warnings: warnings, FinishedAt: finishedAt.UnixMilli(),
		Stats: SyncStats{Issues: rn.scanned, Requests: requests, Ms: finishedAt.Sub(r.started).Milliseconds()},
	})
	if out.status == StatusOK || out.status == StatusPartial {
		o.emit(EvDataChanged, DataChanged{StandupID: r.standupID, What: "view"})
		if rn.applied.ConfigChanged {
			o.emit(EvDataChanged, DataChanged{StandupID: r.standupID, What: "config"})
		}
		rn.afterSync(fctx)
		trimAfter = true
	}
}

func (rn *runner) afterSync(ctx context.Context) {
	o := rn.o
	if o.opts.AfterSync == nil {
		return
	}
	defer func() {
		if p := recover(); p != nil {
			o.opts.Logger.Error("sync: AfterSync panicked", "run", rn.r.id, "panic", p, "stack", string(debug.Stack()))
		}
	}()
	o.opts.AfterSync(ctx, rn.r.standupID, rn.r.id)
}

// parallel runs fns concurrently. The first failure cancels the rest; it returns the most telling error:
// the parent's cancellation, else the first error that is not a consequence of that internal cancel.
func parallel(ctx context.Context, log func(p any, stack string), fns ...func(ctx context.Context) error) error {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make([]error, len(fns))
	var wg gosync.WaitGroup
	for i, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					if log != nil {
						log(p, string(debug.Stack()))
					}
					errs[i] = apperr.New(apperr.Internal, "Something went wrong while syncing.")
					cancel()
				}
			}()
			if err := fn(cctx); err != nil {
				errs[i] = err
				cancel()
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	var first error
	for _, err := range errs {
		if err == nil {
			continue
		}
		if first == nil {
			first = err
		}
		if !errors.Is(err, context.Canceled) {
			return err
		}
	}
	return first
}

func (rn *runner) logPanic(p any, stack string) {
	rn.o.opts.Logger.Error("sync: worker panicked", "run", rn.r.id, "panic", p, "stack", stack)
}

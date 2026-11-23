package sync

import (
	"context"
	"log/slog"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
)

// Status.State values.
const (
	stateIdle    = "idle"
	stateRunning = "running"
	stateFailed  = "failed"
)

// fullEvery is how old the last full sync may get before an incremental request is promoted (jira-sync.md §4.1).
const fullEvery = 24 * time.Hour

type realClock struct{}

func (realClock) Now() time.Time           { return time.Now() }
func (realClock) Location() *time.Location { return time.Local }

// New creates an orchestrator (no goroutines until Start).
func New(opts Options) *Orchestrator {
	if opts.MaxParallel <= 0 {
		opts.MaxParallel = 2
	}
	if opts.HydrateConcurrency <= 0 {
		opts.HydrateConcurrency = 4
	}
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = 100 * time.Millisecond
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.OpenSource == nil {
		opts.OpenSource = source.Open
	}
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	return &Orchestrator{
		opts: opts,
		runs: map[int64]*run{},
		last: map[int64]Status{},
		sem:  make(chan struct{}, opts.MaxParallel),

		starting: map[int64]chan struct{}{},
	}
}

func (o *Orchestrator) emit(name string, payload any) {
	if o.opts.Emitter == nil {
		return
	}
	// A panicking emitter must not strand a run (or the caller of Start): log it and carry on.
	defer func() {
		if p := recover(); p != nil {
			o.opts.Logger.Error("sync: event emitter panicked", "event", name, "panic", p)
		}
	}()
	o.opts.Emitter.Emit(name, payload)
}

// Start begins a run (mode incremental|full; trigger manual|open|config) and returns its id immediately. A standup
// already running returns the existing run id. Promotion to full per jira-sync.md §4.1. Archived standups and drafts →
// apperr validation. auth_invalid connections → apperr auth_invalid without a run.
func (o *Orchestrator) Start(ctx context.Context, standupID int64, mode, trigger string) (int64, error) {
	switch mode {
	case "":
		mode = ModeIncremental
	case ModeIncremental, ModeFull:
	default:
		return 0, apperr.New(apperr.Validation, "Unknown sync mode.")
	}
	switch trigger {
	case "":
		trigger = TriggerManual
	case TriggerManual, TriggerOpen, TriggerConfig:
	default:
		return 0, apperr.New(apperr.Validation, "Unknown sync trigger.")
	}

	if id, ok := o.runningID(standupID); ok {
		return id, nil
	}

	st, _, err := settings.New(o.opts.DB, o.opts.Clock.Now).Get(ctx, standupID)
	if err != nil {
		return 0, err
	}
	if !st.DeletedAt.IsZero() {
		return 0, apperr.New(apperr.Validation, "That standup is archived. Restore it before syncing.")
	}
	if st.Draft() {
		return 0, apperr.New(apperr.Validation, "Choose a board for this standup before syncing.")
	}
	conn, err := cache.GetConnection(ctx, o.opts.DB.R)
	if err != nil {
		return 0, err
	}
	if conn.AuthState == "invalid" {
		return 0, apperr.New(apperr.AuthInvalid, "Jira rejected the token. Reconnect to sync.")
	}
	state, err := cache.GetSyncState(ctx, o.opts.DB.R, standupID)
	if err != nil {
		return 0, err
	}

	t := runStart(o.opts.Clock.Now(), state)
	effective := mode
	if preKnownFull(state, t) {
		effective = ModeFull
	}

	// Reserve the standup's slot under the lock, write the sync_run row without it, then publish the run id. A second
	// Start for the same standup that finds the reservation waits for the id instead of blocking every other caller.
	var r *run
	var runCtx context.Context
	for r == nil {
		o.mu.Lock()
		if o.closed {
			o.mu.Unlock()
			return 0, apperr.New(apperr.Internal, "Cuisle is shutting down.")
		}
		if cur, ok := o.runs[standupID]; ok && cur.status.State == stateRunning {
			if cur.id != 0 {
				id := cur.id
				o.mu.Unlock()
				return id, nil
			}
			ready := o.starting[standupID]
			o.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return 0, apperr.Wrap(apperr.Cancelled, "Cancelled.", ctx.Err())
			}
		}
		var cancel context.CancelFunc
		runCtx, cancel = context.WithCancel(context.Background())
		r = &run{
			standupID: standupID, mode: effective, trigger: trigger, started: t,
			cancel: cancel, done: make(chan struct{}),
			status: Status{StandupID: standupID, State: stateRunning, Mode: effective},
		}
		o.runs[standupID] = r
		if o.starting == nil { // an Orchestrator built without New
			o.starting = map[int64]chan struct{}{}
		}
		o.starting[standupID] = make(chan struct{})
		o.wg.Add(1)
		o.mu.Unlock()
	}

	id, err := o.startRun(ctx, r, standupID, effective, trigger, t)
	if err != nil {
		return 0, err
	}

	o.emit(EvSyncStarted, SyncStarted{RunID: id, StandupID: standupID, Mode: effective, Trigger: trigger})
	go o.execute(runCtx, r, mode)
	return id, nil
}

// startRun writes the sync_run row for the reserved run r and publishes its id. Any failure, a panic out of the cache
// included (it is re-raised), releases the reservation: the standup's slot is freed, waiters wake and the wait group
// is settled, so nothing is left stuck for Wait or Shutdown.
func (o *Orchestrator) startRun(ctx context.Context, r *run, standupID int64, mode, trigger string, t time.Time) (id int64, err error) {
	released := false
	release := func() {
		released = true
		o.mu.Lock()
		ready := o.starting[standupID]
		delete(o.starting, standupID)
		if o.runs[standupID] == r {
			delete(o.runs, standupID)
		}
		o.mu.Unlock()
		close(ready)
		r.cancel()
		close(r.done)
		o.wg.Done()
	}
	defer func() {
		if p := recover(); p != nil {
			if !released {
				release()
			}
			panic(p)
		}
	}()

	id, err = cache.StartRun(ctx, o.opts.DB.W, standupID, mode, trigger, t)
	if err != nil {
		release()
		return 0, err
	}
	o.mu.Lock()
	ready := o.starting[standupID]
	delete(o.starting, standupID)
	r.id = id
	r.status.RunID = id
	o.mu.Unlock()
	close(ready)
	return id, nil
}

// runStart is T: now, truncated to ms, and strictly after the previous attempt of the standup (span CHECK valid_to > valid_from).
func runStart(now time.Time, state cache.SyncState) time.Time {
	t := now.Truncate(time.Millisecond)
	if !state.LastAttemptAt.IsZero() && !t.After(state.LastAttemptAt) {
		t = state.LastAttemptAt.Add(time.Millisecond)
	}
	return t
}

// preKnownFull reports the promotions that are known before the run starts (jira-sync.md §4.1).
func preKnownFull(state cache.SyncState, t time.Time) bool {
	return state.NeedsSync || state.LastSuccessAt.IsZero() || state.LastFullAt.IsZero() || t.Sub(state.LastFullAt) > fullEvery
}

func (o *Orchestrator) runningID(standupID int64) (int64, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r, ok := o.runs[standupID]; ok && r.status.State == stateRunning && r.id != 0 {
		return r.id, true
	}
	return 0, false
}

// Cancel cancels a standup's run (no-op when idle).
func (o *Orchestrator) Cancel(standupID int64) error {
	o.mu.Lock()
	r, ok := o.runs[standupID]
	running := ok && r.status.State == stateRunning
	o.mu.Unlock()
	if running {
		r.cancel()
	}
	return nil
}

// Status returns every standup's status known to the orchestrator merged with standup_sync_state, by standup id.
func (o *Orchestrator) Status(ctx context.Context) ([]Status, error) {
	list, err := settings.New(o.opts.DB, o.opts.Clock.Now).List(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(list))
	for _, s := range list {
		if s.Draft() {
			continue
		}
		state, err := cache.GetSyncState(ctx, o.opts.DB.R, s.ID)
		if err != nil {
			return nil, err
		}

		o.mu.Lock()
		last, hasLast := o.last[s.ID]
		var live *Status
		if r, ok := o.runs[s.ID]; ok && r.status.State == stateRunning {
			cp := r.status
			live = &cp
		}
		o.mu.Unlock()
		if !hasLast {
			last = o.lastFromDB(ctx, s.ID)
		}

		cur := Status{
			StandupID: s.ID, State: stateIdle, RunID: last.RunID, Mode: last.Mode,
			LastSuccessAt: state.LastSuccessAt, LastAttemptAt: state.LastAttemptAt, NeedsSync: state.NeedsSync,
			LastStatus: last.LastStatus, Warnings: last.Warnings, ErrorCode: last.ErrorCode, ErrorMessage: last.ErrorMessage,
		}
		if last.State == stateFailed {
			cur.State = stateFailed
		}
		if live != nil {
			cur.State, cur.RunID, cur.Mode = stateRunning, live.RunID, live.Mode
			cur.Phase, cur.Done, cur.Total = live.Phase, live.Done, live.Total
		}
		if cur.Warnings == nil {
			cur.Warnings = []string{}
		}
		out = append(out, cur)
	}
	return out, nil
}

// lastFromDB rebuilds the last finished run's outcome after a restart.
func (o *Orchestrator) lastFromDB(ctx context.Context, standupID int64) Status {
	runs, err := cache.RecentRuns(ctx, o.opts.DB.R, standupID, 5)
	if err != nil {
		o.opts.Logger.Debug("sync: reading recent runs failed", "standup", standupID, "err", err)
		return Status{StandupID: standupID}
	}
	for _, r := range runs {
		if r.Status == "running" {
			continue
		}
		st := Status{
			StandupID: standupID, RunID: r.ID, Mode: r.Mode, State: stateIdle, LastStatus: r.Status,
			Warnings: r.Warnings, ErrorCode: r.ErrorCode, ErrorMessage: r.ErrorMessage,
		}
		if r.Status == StatusFailed {
			st.State = stateFailed
		}
		return st
	}
	return Status{StandupID: standupID}
}

// Wait blocks until the standup has no run (tests).
func (o *Orchestrator) Wait(standupID int64) {
	for {
		o.mu.Lock()
		r, ok := o.runs[standupID]
		o.mu.Unlock()
		if !ok {
			return
		}
		<-r.done
	}
}

// Shutdown cancels every run and waits (bounded by ctx).
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.mu.Lock()
	o.closed = true // a Start after this returns an error; reservations made before it are cancelled below
	for _, r := range o.runs {
		r.cancel()
	}
	o.mu.Unlock()

	waited := make(chan struct{})
	go func() {
		o.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		return nil
	case <-ctx.Done():
		return apperr.Wrap(apperr.Timeout, "Sync did not stop in time.", ctx.Err())
	}
}

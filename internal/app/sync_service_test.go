package app_test

// SyncService passes through to the orchestrator (architecture.md section 3.2, jira-sync.md section 4).
//
// SPEC-NOTE: (Start) Start returns the run id immediately (the run is asynchronous) and never reports a run's own
// failure as an error: offline, rejected tokens mid-run and cancellation surface through Status, sync:finished and the
// run history. The only errors Start returns are rejections before a run exists: validation (bad mode, draft or
// archived standup), not_found/validation for an unknown standup, and auth_invalid while the connection is already
// invalid (no run is created).
//
// SPEC-NOTE: (trigger) SyncService.Start always records trigger "manual"; a never-synced standup asked for an
// incremental sync runs as full (promotion, jira-sync 4.1).

import (
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func eventIndex(evs []emitted, name string) int {
	for i, e := range evs {
		if e.Name == name {
			return i
		}
	}
	return -1
}

func TestSyncStart_RunsAManualFullSyncForANeverSyncedStandup(t *testing.T) {
	ta := newTestApp(t)
	ta.connect()
	cfg := ta.newStandup(ta.Fx.scrumProject, ta.Fx.scrum)
	ta.Em.reset()

	run, err := ta.Svc.Sync.Start(ctx, cfg.ID, "incremental")
	noErr(t, err, "Start")
	if run <= 0 {
		t.Fatalf("run id = %d, want > 0", run)
	}
	ta.Orch.Wait(cfg.ID)

	runs := ta.runs()
	if len(runs) != 1 || runs[0].ID != run || runs[0].StandupID != cfg.ID || runs[0].Trigger != "manual" || runs[0].Mode != "full" || runs[0].Status != "ok" {
		t.Errorf("runs = %+v, want one ok manual full run with id %d (an incremental request is promoted for a never-synced standup)", runs, run)
	}
	evs := ta.Em.all()
	started, finished := eventIndex(evs, "sync:started"), eventIndex(evs, "sync:finished")
	if started < 0 || finished < started {
		t.Fatalf("events = %+v, want sync:started before sync:finished", evs)
	}
	sp, fp := evs[started].Payload, evs[finished].Payload
	if int64(sp["runId"].(float64)) != run || int64(sp["standupId"].(float64)) != cfg.ID || sp["trigger"] != "manual" {
		t.Errorf("sync:started = %+v", sp)
	}
	if int64(fp["runId"].(float64)) != run || fp["status"] != "ok" {
		t.Errorf("sync:finished = %+v, want status ok for run %d", fp, run)
	}
	if v := ta.view(cfg.ID); len(v.Tickets) == 0 || v.LastSyncedAt != ta.Clock.ms() {
		t.Errorf("the synced view = %d tickets, lastSyncedAt %d", len(v.Tickets), v.LastSyncedAt)
	}
}

func TestSyncStart_WhileRunningReturnsTheExistingRunID(t *testing.T) {
	ta := newTestApp(t, withLatency(40*time.Millisecond))
	ta.connect()
	cfg := ta.newStandup(ta.Fx.scrumProject, ta.Fx.scrum)

	first, err := ta.Svc.Sync.Start(ctx, cfg.ID, "full")
	noErr(t, err, "Start")
	second, err := ta.Svc.Sync.Start(ctx, cfg.ID, "full")
	noErr(t, err, "Start (again)")
	if first != second {
		t.Errorf("a second Start while running returned run %d, want the existing run %d", second, first)
	}
	ta.Orch.Wait(cfg.ID)
	if n := ta.runRows(); n != 1 {
		t.Errorf("%d runs recorded, want 1", n)
	}
}

func TestSyncCancel_EndsTheRunCancelledAndKeepsTheBoardUntouched(t *testing.T) {
	ta := newTestApp(t, withLatency(100*time.Millisecond))
	ta.connect()
	cfg := ta.newStandup(ta.Fx.scrumProject, ta.Fx.scrum)
	tables := []string{"issue", "standup_issue", "status_change", "standup_issue_span"}
	before := storetest.Checksum(t, ta.DB, tables...)
	ta.Em.reset()

	_, err := ta.Svc.Sync.Start(ctx, cfg.ID, "full")
	noErr(t, err, "Start")
	noErr(t, ta.Svc.Sync.Cancel(ctx, cfg.ID), "Cancel")
	ta.Orch.Wait(cfg.ID)

	if after := storetest.Checksum(t, ta.DB, tables...); after != before {
		t.Errorf("a cancelled sync changed the record tables")
	}
	st, err := ta.Svc.Sync.Status(ctx)
	noErr(t, err, "Status")
	if len(st) != 1 || st[0].State != "idle" || st[0].LastStatus != "cancelled" || st[0].LastSuccessAt != 0 {
		t.Errorf("Status = %+v, want idle, lastStatus cancelled, never succeeded", st)
	}
	evs := ta.Em.named("sync:finished")
	if len(evs) != 1 || evs[0].Payload["status"] != "cancelled" {
		t.Errorf("sync:finished events = %+v, want one with status cancelled", evs)
	}
	if v := ta.view(cfg.ID); len(v.Tickets) != 0 || v.LastSyncedAt != 0 {
		t.Errorf("view after a cancelled first sync = %d tickets, lastSyncedAt %d, want an untouched empty board", len(v.Tickets), v.LastSyncedAt)
	}
}

func TestSyncCancel_IdleStandupIsANoOp(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	noErr(t, ta.Svc.Sync.Cancel(ctx, id), "Cancel(idle)")
}

func TestSyncStart_RejectsBadRequestsBeforeAnyRun(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	draft, err := ta.Svc.Config.Create(ctx)
	noErr(t, err, "Create draft")
	archived := ta.kanbanStandup()
	_, err = ta.Svc.Config.Delete(ctx, archived)
	noErr(t, err, "Delete")

	runs := ta.runRows()
	mark := ta.networkCalls()
	ta.Fake.ResetRequests()

	_, err = ta.Svc.Sync.Start(ctx, id, "weekly")
	wantCode(t, err, apperr.Validation, "Start(bad mode)")
	_, err = ta.Svc.Sync.Start(ctx, draft.ID, "full")
	wantCode(t, err, apperr.Validation, "Start(draft)")
	_, err = ta.Svc.Sync.Start(ctx, archived, "full")
	wantCode(t, err, apperr.Validation, "Start(archived)")
	_, err = ta.Svc.Sync.Start(ctx, 4242, "full")
	wantCodeOneOf(t, err, "Start(unknown)", apperr.NotFound, apperr.Validation)

	ta.assertNoNetwork(mark, "rejected Start calls")
	if got := ta.runRows(); got != runs {
		t.Errorf("rejected Start calls created %d runs", got-runs)
	}
}

func TestSyncStatus_ReportsPendingAndLastSuccess(t *testing.T) {
	ta := newTestApp(t)
	st, err := ta.Svc.Sync.Status(ctx)
	noErr(t, err, "Status(empty)")
	if st == nil || len(st) != 0 {
		t.Errorf("Status with no standups = %#v, want a non-nil empty slice", st)
	}

	ta.connect()
	cfg := ta.newStandup(ta.Fx.scrumProject, ta.Fx.scrum)
	st, _ = ta.Svc.Sync.Status(ctx)
	if len(st) != 1 || st[0].StandupID != cfg.ID || !st[0].NeedsSync || st[0].LastSuccessAt != 0 || st[0].State != "idle" || st[0].Warnings == nil {
		t.Errorf("Status before the first sync = %+v, want idle, needsSync, never succeeded, non-nil warnings", st)
	}
	ta.syncNow(cfg.ID)
	st, _ = ta.Svc.Sync.Status(ctx)
	if len(st) != 1 || st[0].NeedsSync || st[0].LastStatus != "ok" || st[0].LastSuccessAt != ta.Clock.ms() || st[0].LastAttemptAt != ta.Clock.ms() || st[0].RunID == 0 {
		t.Errorf("Status after a sync = %+v, want ok at the fixed clock and no pending sync", st)
	}
}

func TestSyncStart_OfflineFailsTheRunAndKeepsTheCachedView(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	tickets := len(ta.view(id).Tickets)
	ta.Em.reset()
	ta.Net.down.Store(true)

	_, err := ta.Svc.Sync.Start(ctx, id, "incremental")
	noErr(t, err, "Start(offline) returns a run; the failure is reported through status and events")
	ta.Orch.Wait(id)

	st, _ := ta.Svc.Sync.Status(ctx)
	if len(st) != 1 || st[0].LastStatus != "failed" || st[0].ErrorCode != "offline" || st[0].State == "running" || st[0].ErrorMessage == "" {
		t.Errorf("Status = %+v, want failed with errorCode offline", st)
	}
	evs := ta.Em.named("sync:finished")
	if len(evs) != 1 || evs[0].Payload["status"] != "failed" || evs[0].Payload["errorCode"] != "offline" {
		t.Errorf("sync:finished = %+v, want failed/offline", evs)
	}
	if v := ta.view(id); len(v.Tickets) != tickets {
		t.Errorf("the cached view lost tickets after an offline sync: %d -> %d", tickets, len(v.Tickets))
	}
	if ta.config(id).NeedsSync {
		t.Errorf("an offline sync must not invent a pending-sync flag")
	}
}

func TestSyncStart_RejectedTokenMidRunFailsAuthInvalidAndSuppressesFurtherRuns(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	ta.Em.reset()
	ta.Fake.SetUnauthorized(true)

	_, err := ta.Svc.Sync.Start(ctx, id, "incremental")
	noErr(t, err, "Start")
	ta.Orch.Wait(id)
	st, _ := ta.Svc.Sync.Status(ctx)
	if len(st) != 1 || st[0].LastStatus != "failed" || st[0].ErrorCode != "auth_invalid" {
		t.Errorf("Status = %+v, want failed/auth_invalid", st)
	}
	evs := ta.Em.named("connection:auth")
	if len(evs) == 0 || evs[len(evs)-1].Payload["state"] != "invalid" {
		t.Errorf("connection:auth events = %+v, want invalid", evs)
	}
	runs := ta.runRows()
	_, err = ta.Svc.Sync.Start(ctx, id, "incremental")
	wantCode(t, err, apperr.AuthInvalid, "Start with an invalid connection")
	if got := ta.runRows(); got != runs {
		t.Errorf("a suppressed sync created a run")
	}
}

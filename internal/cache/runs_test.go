package cache_test

// Tests for sync_run / sync_log bookkeeping in internal/cache (WP-20; data-model.md §2.7).
//
// SPEC-NOTE: StartRun stamps standup_sync_state.last_attempt_at with an upsert: when the standup has no state row yet,
// one is created with the column defaults (needs_sync 1, backlog_total -1). An existing row keeps every other column.
// SPEC-NOTE: FinishRun only writes the outcome (status, finished_at, counters, error, warnings); standup_id, mode,
// trigger and started_at stay as StartRun wrote them, whatever the zero values in the passed Run are. Empty ErrorCode /
// ErrorMessage are stored as NULL and read back as "". Nil Warnings are stored as "[]" and read back as an empty
// non-nil slice.
// SPEC-NOTE: RecentRuns orders by started_at descending (ties: higher id first) and returns a non-nil empty slice
// when there are none.

import (
	"reflect"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/store"
)

func rdStart(t *testing.T, db *store.DB, standup int64, mode, trigger, at string) int64 {
	t.Helper()
	id, err := cache.StartRun(rdCtx, db.W, standup, mode, trigger, rdTS(at))
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	return id
}

func rdRuns(t *testing.T, db *store.DB, standup int64, n int) []cache.Run {
	t.Helper()
	runs, err := cache.RecentRuns(rdCtx, db.R, standup, n)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	return runs
}

func TestStartRun_InsertsRunningRowWithStartedAt(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	id := rdStart(t, db, rdSA, "incremental", "manual", "2026-03-18T10:00:00Z")
	if id <= 0 {
		t.Fatalf("StartRun returned id %d, want > 0", id)
	}
	runs := rdRuns(t, db, rdSA, 10)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want 1", runs)
	}
	r := runs[0]
	if r.ID != id || r.StandupID != rdSA || r.Mode != "incremental" || r.Trigger != "manual" || r.Status != "running" {
		t.Errorf("run = %+v, want id %d, standup 1, incremental/manual, running", r, id)
	}
	rdSame(t, "StartedAt", r.StartedAt, rdTS("2026-03-18T10:00:00Z"))
	rdZero(t, "FinishedAt", r.FinishedAt)
	if r.IssuesScanned != 0 || r.IssuesHydrated != 0 || r.Requests != 0 || r.Throttled != 0 || r.ErrorCode != "" || r.ErrorMessage != "" {
		t.Errorf("counters of a new run = %+v, want zeros", r)
	}
	if r.Warnings == nil || len(r.Warnings) != 0 {
		t.Errorf("Warnings = %#v, want an empty non-nil slice", r.Warnings)
	}
}

func TestStartRun_StampsLastAttemptAndKeepsTheRestOfTheSyncState(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db) // standup A has a full sync state row
	rdStart(t, db, rdSA, "full", "open", "2026-03-18T21:00:00Z")

	s, err := cache.GetSyncState(rdCtx, db.R, rdSA)
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	rdSame(t, "LastAttemptAt", s.LastAttemptAt, rdTS("2026-03-18T21:00:00Z"))
	rdSame(t, "LastSuccessAt (unchanged)", s.LastSuccessAt, rdTS("2026-03-18T18:30:00Z"))
	rdSame(t, "HistoryFrom (unchanged)", s.HistoryFrom, rdTS("2026-03-01T09:00:00Z"))
	if s.NeedsSync || s.BacklogTotal != 37 || !reflect.DeepEqual(s.ActiveSprintIDs, []string{"S42"}) {
		t.Errorf("state = %+v, want NeedsSync false, BacklogTotal 37, [S42] unchanged", s)
	}
}

func TestStartRun_WithoutSyncStateRow_CreatesOneWithDefaults(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db) // standup D (archived) has no sync state row
	if n := rdCount(t, db, "standup_sync_state", "standup_id = ?", rdSD); n != 0 {
		t.Fatalf("precondition: standup D has %d sync state rows, want 0", n)
	}
	rdStart(t, db, rdSD, "full", "config", "2026-03-18T21:00:00Z")

	if n := rdCount(t, db, "standup_sync_state", "standup_id = ?", rdSD); n != 1 {
		t.Fatalf("sync state rows for D = %d, want 1 after StartRun", n)
	}
	s, err := cache.GetSyncState(rdCtx, db.R, rdSD)
	if err != nil {
		t.Fatalf("GetSyncState: %v", err)
	}
	rdSame(t, "LastAttemptAt", s.LastAttemptAt, rdTS("2026-03-18T21:00:00Z"))
	rdZero(t, "LastSuccessAt", s.LastSuccessAt)
	if !s.NeedsSync || s.BacklogTotal != -1 {
		t.Errorf("state = %+v, want the defaults NeedsSync true, BacklogTotal -1", s)
	}
}

func TestFinishRun_WritesOutcomeAndCounters_WarningsRoundTrip(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	id := rdStart(t, db, rdSA, "full", "open", "2026-03-18T10:00:00Z")

	// Only the outcome fields are set: StandupID/Mode/Trigger/StartedAt are left zero on purpose.
	err := cache.FinishRun(rdCtx, db.W, cache.Run{
		ID: id, Status: "partial", FinishedAt: rdTS("2026-03-18T10:02:30Z"),
		IssuesScanned: 120, IssuesHydrated: 17, Requests: 64, Throttled: 3,
		ErrorCode: "rate_limited", ErrorMessage: "Jira asked us to slow down.",
		Warnings: []string{"dev_activity_unavailable", "throttled"},
	})
	if err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	runs := rdRuns(t, db, rdSA, 10)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v, want 1", runs)
	}
	r := runs[0]
	if r.Status != "partial" || r.IssuesScanned != 120 || r.IssuesHydrated != 17 || r.Requests != 64 || r.Throttled != 3 {
		t.Errorf("run = %+v, want partial with the counters 120/17/64/3", r)
	}
	if r.ErrorCode != "rate_limited" || r.ErrorMessage != "Jira asked us to slow down." {
		t.Errorf("ErrorCode/ErrorMessage = %q/%q", r.ErrorCode, r.ErrorMessage)
	}
	if want := []string{"dev_activity_unavailable", "throttled"}; !reflect.DeepEqual(r.Warnings, want) {
		t.Errorf("Warnings = %v, want %v", r.Warnings, want)
	}
	rdSame(t, "FinishedAt", r.FinishedAt, rdTS("2026-03-18T10:02:30Z"))
	// untouched by FinishRun
	if r.StandupID != rdSA || r.Mode != "full" || r.Trigger != "open" {
		t.Errorf("StandupID/Mode/Trigger = %d/%q/%q, want 1/full/open (kept from StartRun)", r.StandupID, r.Mode, r.Trigger)
	}
	rdSame(t, "StartedAt (kept)", r.StartedAt, rdTS("2026-03-18T10:00:00Z"))
}

func TestFinishRun_OKRunWithoutErrorOrWarnings_ReadsBackEmpty(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	id := rdStart(t, db, rdSA, "incremental", "manual", "2026-03-18T10:00:00Z")
	if err := cache.FinishRun(rdCtx, db.W, cache.Run{ID: id, Status: "ok", FinishedAt: rdTS("2026-03-18T10:00:20Z"), IssuesScanned: 5}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}
	r := rdRuns(t, db, rdSA, 1)[0]
	if r.Status != "ok" || r.IssuesScanned != 5 || r.ErrorCode != "" || r.ErrorMessage != "" {
		t.Errorf("run = %+v", r)
	}
	if r.Warnings == nil || len(r.Warnings) != 0 {
		t.Errorf("Warnings = %#v, want an empty non-nil slice", r.Warnings)
	}
	if n := rdCount(t, db, "sync_run", "error_code IS NULL AND error_message IS NULL AND warnings = '[]'"); n != 1 {
		t.Errorf("sync_run row should store NULL error columns and '[]' warnings (matching rows: %d)", n)
	}
}

func TestFinishRun_StatusesFailedAndCancelledAreAccepted(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			defer rdRecover(t)
			db := rdOpen(t)
			rdSeedBase(t, db)
			id := rdStart(t, db, rdSA, "incremental", "manual", "2026-03-18T10:00:00Z")
			if err := cache.FinishRun(rdCtx, db.W, cache.Run{ID: id, Status: status, FinishedAt: rdTS("2026-03-18T10:00:05Z"), ErrorCode: "offline", ErrorMessage: "No network."}); err != nil {
				t.Fatalf("FinishRun(%s): %v", status, err)
			}
			if r := rdRuns(t, db, rdSA, 1)[0]; r.Status != status || r.ErrorCode != "offline" {
				t.Errorf("run = %+v, want status %s", r, status)
			}
		})
	}
}

func TestLogRun_AppendsLinesInOrder(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	id := rdStart(t, db, rdSA, "full", "manual", "2026-03-18T10:00:00Z")

	lines := []struct {
		at                    string
		level, phase, message string
	}{
		{"2026-03-18T10:00:01Z", "info", "scan", "scanned 12 issues"},
		{"2026-03-18T10:00:02Z", "warn", "dev", "dev activity unavailable"},
		{"2026-03-18T10:00:03Z", "error", "apply", "rolled back"},
	}
	for _, l := range lines {
		if err := cache.LogRun(rdCtx, db.W, id, rdTS(l.at), l.level, l.phase, l.message); err != nil {
			t.Fatalf("LogRun(%s): %v", l.message, err)
		}
	}
	rows, err := db.R.QueryContext(rdCtx, "SELECT run_id, at, level, phase, message FROM sync_log ORDER BY id")
	if err != nil {
		t.Fatalf("query sync_log: %v", err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var runID, atMs int64
		var level, phase, msg string
		if err := rows.Scan(&runID, &atMs, &level, &phase, &msg); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if i >= len(lines) {
			t.Fatalf("more sync_log rows than lines logged")
		}
		l := lines[i]
		if runID != id || atMs != rdMs(l.at) || level != l.level || phase != l.phase || msg != l.message {
			t.Errorf("sync_log[%d] = run %d at %d %s/%s %q, want run %d at %d %s/%s %q", i, runID, atMs, level, phase, msg, id, rdMs(l.at), l.level, l.phase, l.message)
		}
		i++
	}
	if i != len(lines) {
		t.Errorf("sync_log rows = %d, want %d", i, len(lines))
	}
}

func TestRecentRuns_NewestFirstAndLimitedToN(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	// started out of order on purpose: order is by started_at, not by id.
	a2 := rdStart(t, db, rdSA, "incremental", "manual", "2026-03-18T11:00:00Z")
	a1 := rdStart(t, db, rdSA, "incremental", "manual", "2026-03-18T10:00:00Z")
	a3 := rdStart(t, db, rdSA, "full", "open", "2026-03-18T12:00:00Z")
	b := rdStart(t, db, rdSB, "incremental", "config", "2026-03-18T11:30:00Z")

	ids := func(rs []cache.Run) []int64 {
		out := []int64{}
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	if got, want := ids(rdRuns(t, db, rdSA, 10)), []int64{a3, a2, a1}; !reflect.DeepEqual(got, want) {
		t.Errorf("runs of standup A = %v, want %v", got, want)
	}
	if got, want := ids(rdRuns(t, db, rdSA, 2)), []int64{a3, a2}; !reflect.DeepEqual(got, want) {
		t.Errorf("runs of standup A, n=2 = %v, want %v", got, want)
	}
	if got, want := ids(rdRuns(t, db, rdSB, 10)), []int64{b}; !reflect.DeepEqual(got, want) {
		t.Errorf("runs of standup B = %v, want %v", got, want)
	}
}

func TestRecentRuns_StandupIDZeroMeansAllStandups(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	a1 := rdStart(t, db, rdSA, "incremental", "manual", "2026-03-18T10:00:00Z")
	b := rdStart(t, db, rdSB, "incremental", "config", "2026-03-18T11:30:00Z")
	a2 := rdStart(t, db, rdSA, "full", "open", "2026-03-18T12:00:00Z")

	all := rdRuns(t, db, 0, 10)
	got := []int64{}
	for _, r := range all {
		got = append(got, r.ID)
	}
	if want := []int64{a2, b, a1}; !reflect.DeepEqual(got, want) {
		t.Errorf("all runs = %v, want %v (newest first across standups)", got, want)
	}
	if all[1].StandupID != rdSB || all[1].Trigger != "config" || all[1].Mode != "incremental" {
		t.Errorf("run for standup B = %+v", all[1])
	}
	if n := len(rdRuns(t, db, 0, 2)); n != 2 {
		t.Errorf("RecentRuns(0, 2) returned %d rows, want 2", n)
	}
}

func TestRecentRuns_NoRuns_ReturnsEmptyNonNilSlice(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	runs := rdRuns(t, db, rdSA, 5)
	if runs == nil || len(runs) != 0 {
		t.Errorf("RecentRuns = %#v, want an empty non-nil slice", runs)
	}
}

func TestRecentRuns_TimesRoundTripAtMillisecondPrecision(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	at := time.Date(2026, 3, 18, 10, 0, 0, 123_000_000, time.UTC)
	id, err := cache.StartRun(rdCtx, db.W, rdSA, "incremental", "manual", at)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if r := rdRuns(t, db, rdSA, 1)[0]; r.ID != id || !r.StartedAt.Equal(at) {
		t.Errorf("StartedAt = %v, want %v (Unix ms)", r.StartedAt, at)
	}
}

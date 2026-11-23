package store_test

// Tests for Ms/FromMs, WeeklyBackup and MarkInterruptedRuns.
//
// SPEC-NOTE: WeeklyBackup's age is "now - mtime of cuisle.db.bak-weekly" (contract: "missing or older than 7 days at
// now"). The file's real mtime is wall-clock, but tests inject a fake `now`, so the tests os.Chtimes the written file
// to the injected time before each later call. That is deterministic and also holds for an implementation that keeps
// its own marker instead of reading mtime. "Older than 7 days" is strict: +6 days -> false, +8 days -> true.
// SPEC-NOTE: MarkInterruptedRuns also sets sync_run.finished_at = Ms(now) (a failed run with no end time would be
// odd) using the `now` argument, not the DB clock; outbox rows only need status 'failed'.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
)

// ---- Ms / FromMs ---------------------------------------------------------------------------------------------

func TestMs_TableOfInstants(t *testing.T) {
	defer recoverStub(t)
	la := time.FixedZone("PST", -8*3600)
	cases := []struct {
		name string
		in   time.Time
		want int64
	}{
		{"zero time is 0", time.Time{}, 0},
		{"unix epoch instant is 0", time.Unix(0, 0).UTC(), 0},
		{"UTC instant", time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC), 1795435200000},
		{"same instant in another zone", time.Date(2026, 11, 23, 4, 0, 0, 0, la), 1795435200000},
		{"milliseconds kept", time.Date(2026, 11, 23, 12, 0, 0, 123_000_000, time.UTC), 1795435200123},
		{"sub-millisecond truncated", time.Date(2026, 11, 23, 12, 0, 0, 123_999_999, time.UTC), 1795435200123},
		{"before 1970", time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC), -1000},
	}
	for _, c := range cases {
		if got := store.Ms(c.in); got != c.want {
			t.Errorf("%s: Ms = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestFromMs_ZeroIsZeroTime(t *testing.T) {
	defer recoverStub(t)
	if got := store.FromMs(0); !got.IsZero() {
		t.Errorf("FromMs(0) = %v, want the zero time", got)
	}
}

func TestMs_FromMs_RoundTrip(t *testing.T) {
	defer recoverStub(t)
	for _, ms := range []int64{1795435200000, 1795435200123, 1, -1000, 4102444800000} {
		got := store.FromMs(ms)
		if got.UnixMilli() != ms {
			t.Errorf("FromMs(%d).UnixMilli() = %d", ms, got.UnixMilli())
		}
		if back := store.Ms(got); back != ms {
			t.Errorf("Ms(FromMs(%d)) = %d", ms, back)
		}
	}
	t0 := time.Date(2026, 11, 23, 12, 0, 0, 456_000_000, time.UTC)
	if got := store.FromMs(store.Ms(t0)); !got.Equal(t0) {
		t.Errorf("FromMs(Ms(t)) = %v, want %v", got, t0)
	}
}

// ---- WeeklyBackup ---------------------------------------------------------------------------------------------

const seedStandup = `INSERT INTO standup(id, name, position, created_at, updated_at) VALUES (1, 'Platform', 0, 1, 1);`
const seedHistory = `INSERT INTO standup_sync_state(standup_id, history_from) VALUES (1, 1000);`

func weeklyPath(dir string) string { return filepath.Join(dir, "cuisle.db.bak-weekly") }

func TestWeeklyBackup_NoHistory_WritesNothing(t *testing.T) {
	defer recoverStub(t)
	db, dir := openTemp(t)
	wrote, err := db.WeeklyBackup(ctx, fixedNow)
	if err != nil || wrote {
		t.Errorf("empty DB: WeeklyBackup = %v, %v; want false, nil", wrote, err)
	}
	// A standup whose sync state has no history_from yet is not "history" either.
	mustExec(t, db.W, seedStandup)
	mustExec(t, db.W, "INSERT INTO standup_sync_state(standup_id) VALUES (1)")
	wrote, err = db.WeeklyBackup(ctx, fixedNow)
	if err != nil || wrote {
		t.Errorf("history_from NULL: WeeklyBackup = %v, %v; want false, nil", wrote, err)
	}
	if fileExists(weeklyPath(dir)) {
		t.Error("cuisle.db.bak-weekly exists although no standup has history")
	}
}

func TestWeeklyBackup_WithHistory_WritesAConsistentCopy(t *testing.T) {
	defer recoverStub(t)
	db, dir := openTemp(t)
	mustExec(t, db.W, seedStandup+seedHistory)
	wrote, err := db.WeeklyBackup(ctx, fixedNow)
	if err != nil || !wrote {
		t.Fatalf("WeeklyBackup = %v, %v; want true, nil", wrote, err)
	}
	bak := rawDB(t, weeklyPath(dir))
	if n := countRows(t, bak, "standup"); n != 1 {
		t.Errorf("backup has %d standups, want 1", n)
	}
	if v, err := store.SchemaVersion(ctx, bak); err != nil || v != headVersion {
		t.Errorf("backup SchemaVersion = %d, %v; want %d, nil", v, err, headVersion)
	}
	assertIntegrity(t, bak)
}

func TestWeeklyBackup_WithinSevenDays_IsSkipped(t *testing.T) {
	defer recoverStub(t)
	db, dir := openTemp(t)
	mustExec(t, db.W, seedStandup+seedHistory)
	if wrote, err := db.WeeklyBackup(ctx, fixedNow); err != nil || !wrote {
		t.Fatalf("first call = %v, %v; want true, nil", wrote, err)
	}
	if err := os.Chtimes(weeklyPath(dir), fixedNow, fixedNow); err != nil {
		t.Fatal(err)
	}
	// New data after the backup must not be copied yet.
	mustExec(t, db.W, "INSERT INTO standup(id, name, position, created_at, updated_at) VALUES (2, 'Second', 1, 1, 1)")
	wrote, err := db.WeeklyBackup(ctx, fixedNow.Add(6*24*time.Hour))
	if err != nil || wrote {
		t.Errorf("6 days later = %v, %v; want false, nil", wrote, err)
	}
	bak := rawDB(t, weeklyPath(dir))
	if n := countRows(t, bak, "standup"); n != 1 {
		t.Errorf("backup standups = %d, want 1 (untouched)", n)
	}
}

func TestWeeklyBackup_AfterEightDays_ReplacesTheCopy(t *testing.T) {
	defer recoverStub(t)
	db, dir := openTemp(t)
	mustExec(t, db.W, seedStandup+seedHistory)
	if wrote, err := db.WeeklyBackup(ctx, fixedNow); err != nil || !wrote {
		t.Fatalf("first call = %v, %v; want true, nil", wrote, err)
	}
	if err := os.Chtimes(weeklyPath(dir), fixedNow, fixedNow); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db.W, "INSERT INTO standup(id, name, position, created_at, updated_at) VALUES (2, 'Second', 1, 1, 1)")
	wrote, err := db.WeeklyBackup(ctx, fixedNow.Add(8*24*time.Hour))
	if err != nil || !wrote {
		t.Fatalf("8 days later = %v, %v; want true, nil (VACUUM INTO refuses an existing file, so the old copy must be replaced)", wrote, err)
	}
	bak := rawDB(t, weeklyPath(dir))
	if n := countRows(t, bak, "standup"); n != 2 {
		t.Errorf("replaced backup has %d standups, want 2", n)
	}
	if m := glob(t, filepath.Join(dir, "cuisle.db.bak-*")); len(m) != 1 {
		t.Errorf("backup files = %v; exactly one cuisle.db.bak-weekly is kept", m)
	}
}

func TestWeeklyBackup_ReplacesAStaleUnreadableFile(t *testing.T) {
	defer recoverStub(t)
	db, dir := openTemp(t)
	mustExec(t, db.W, seedStandup+seedHistory)
	if err := os.WriteFile(weeklyPath(dir), []byte("stale junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := fixedNow.Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(weeklyPath(dir), old, old); err != nil {
		t.Fatal(err)
	}
	wrote, err := db.WeeklyBackup(ctx, fixedNow)
	if err != nil || !wrote {
		t.Fatalf("WeeklyBackup = %v, %v; want true, nil", wrote, err)
	}
	bak := rawDB(t, weeklyPath(dir))
	if n := countRows(t, bak, "standup"); n != 1 {
		t.Errorf("backup standups = %d, want 1", n)
	}
}

func TestWeeklyBackup_InMemoryDB_WritesNothing(t *testing.T) {
	defer recoverStub(t)
	db, err := store.OpenMemory(ctx, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	mustExec(t, db.W, seedStandup+seedHistory)
	wrote, err := db.WeeklyBackup(ctx, fixedNow)
	if err != nil || wrote {
		t.Errorf("in-memory WeeklyBackup = %v, %v; want false, nil (no Path, nowhere to write)", wrote, err)
	}
}

// ---- MarkInterruptedRuns ---------------------------------------------------------------------------------------

func seedRunsAndOutbox(t *testing.T, db *store.DB) {
	t.Helper()
	mustExec(t, db.W, seedStandup)
	mustExec(t, db.W, `INSERT INTO sync_run(id, standup_id, mode, trigger, status, started_at, finished_at, error_code) VALUES
		(1, 1, 'incremental', 'manual', 'running',   100, NULL, NULL),
		(2, 1, 'full',        'open',   'running',   200, NULL, NULL),
		(3, 1, 'incremental', 'manual', 'ok',        300, 310,  NULL),
		(4, 1, 'incremental', 'manual', 'failed',    400, 410,  'offline'),
		(5, 1, 'incremental', 'config', 'partial',   500, 510,  NULL),
		(6, 1, 'incremental', 'manual', 'cancelled', 600, 610,  NULL)`)
	mustExec(t, db.W, `INSERT INTO sink(id, kind, name, created_at, updated_at) VALUES (1, 'file', 'Nightly', 1, 1)`)
	mustExec(t, db.W, `INSERT INTO sink_outbox(id, sink_id, standup_id, cause, payload_kind, idempotency_key, payload, status, created_at, updated_at) VALUES
		(1, 1, 1, 'manual', 'current', 'k1', '{}', 'delivering', 1, 1),
		(2, 1, 1, 'manual', 'current', 'k2', '{}', 'pending',    1, 1),
		(3, 1, 1, 'manual', 'current', 'k3', '{}', 'delivered',  1, 1),
		(4, 1, 1, 'manual', 'current', 'k4', '{}', 'failed',     1, 1),
		(5, 1, 1, 'manual', 'current', 'k5', '{}', 'dead',       1, 1)`)
}

func TestMarkInterruptedRuns_FlipsRunningRunsAndDeliveringRows(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	seedRunsAndOutbox(t, db)
	now := fixedNow.Add(time.Hour)

	n, err := db.MarkInterruptedRuns(ctx, now)
	if err != nil {
		t.Fatalf("MarkInterruptedRuns: %v", err)
	}
	if n != 3 {
		t.Errorf("rows changed = %d, want 3 (2 running runs + 1 delivering outbox row)", n)
	}

	for _, id := range []int{1, 2} {
		var status, code string
		var finished int64
		err := db.W.QueryRowContext(ctx, "SELECT status, COALESCE(error_code,''), COALESCE(finished_at,0) FROM sync_run WHERE id=?", id).Scan(&status, &code, &finished)
		if err != nil {
			t.Fatal(err)
		}
		if status != "failed" || code != "interrupted" {
			t.Errorf("run %d = (%s, %s), want (failed, interrupted)", id, status, code)
		}
		if finished != now.UnixMilli() {
			t.Errorf("run %d finished_at = %d, want %d (the now argument)", id, finished, now.UnixMilli())
		}
	}
	wantRuns := map[int]string{3: "ok", 4: "failed", 5: "partial", 6: "cancelled"}
	for id, want := range wantRuns {
		if got := scalarStr(t, db.W, "SELECT status FROM sync_run WHERE id=?", id); got != want {
			t.Errorf("run %d status = %s, want %s (untouched)", id, got, want)
		}
	}
	if got := scalarStr(t, db.W, "SELECT COALESCE(error_code,'') FROM sync_run WHERE id=4"); got != "offline" {
		t.Errorf("run 4 error_code = %q, want offline (untouched)", got)
	}

	wantOutbox := map[int]string{1: "failed", 2: "pending", 3: "delivered", 4: "failed", 5: "dead"}
	for id, want := range wantOutbox {
		if got := scalarStr(t, db.W, "SELECT status FROM sink_outbox WHERE id=?", id); got != want {
			t.Errorf("outbox %d status = %s, want %s", id, got, want)
		}
	}
}

func TestMarkInterruptedRuns_SecondCallChangesNothing(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	seedRunsAndOutbox(t, db)
	if _, err := db.MarkInterruptedRuns(ctx, fixedNow); err != nil {
		t.Fatal(err)
	}
	n, err := db.MarkInterruptedRuns(ctx, fixedNow)
	if err != nil || n != 0 {
		t.Errorf("second call = %d, %v; want 0, nil", n, err)
	}
}

func TestMarkInterruptedRuns_CleanDatabaseReturnsZero(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	n, err := db.MarkInterruptedRuns(ctx, fixedNow)
	if err != nil || n != 0 {
		t.Errorf("clean DB = %d, %v; want 0, nil", n, err)
	}
}

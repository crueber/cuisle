package storetest_test

// Tests for the shared test helper package (the contract every other package's tests rely on).
//
// SPEC-NOTE: storetest.Checksum tests rely only on: stable across calls, equal for two DBs holding identical rows
// (inserted in the same order), different once a row of a named table changes, and unaffected by changes to tables
// that were not named.
// SPEC-NOTE: Exec/Count report failure through tb (Fatalf/Errorf+FailNow); the test uses a recording TB to check it.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func recoverStub(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("panic (stub not implemented?): %v", r)
	}
}

func TestOpen_ReturnsAMigratedMemoryDB(t *testing.T) {
	defer recoverStub(t)
	db := storetest.Open(t, nil)
	if db.Path != "" {
		t.Errorf("Path = %q, want empty (in-memory)", db.Path)
	}
	if v, err := store.SchemaVersion(t.Context(), db.W); err != nil || v < 1 {
		t.Errorf("SchemaVersion = %d, %v; want >= 1", v, err)
	}
	if n := storetest.Count(t, db, "ui_state", ""); n != 1 {
		t.Errorf("ui_state rows = %d, want 1 (seeded by 0001)", n)
	}
}

func TestOpen_UsesTheGivenClock(t *testing.T) {
	defer recoverStub(t)
	fixed := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	db := storetest.Open(t, func() time.Time { return fixed })
	if got := db.Now(); !got.Equal(fixed) {
		t.Errorf("db.Now() = %v, want %v", got, fixed)
	}
}

func TestOpen_NilClockFallsBackToWallTime(t *testing.T) {
	defer recoverStub(t)
	db := storetest.Open(t, nil)
	if d := time.Since(db.Now()); d < -time.Minute || d > time.Minute {
		t.Errorf("db.Now() is %v away from time.Now()", d)
	}
}

func TestOpenFile_ReturnsAMigratedFileDBInsideTheTestTempDir(t *testing.T) {
	defer recoverStub(t)
	db := storetest.OpenFile(t, nil)
	if db.Path == "" {
		t.Fatal("Path is empty; OpenFile must return a file DB")
	}
	if filepath.Base(db.Path) != "cuisle.db" {
		t.Errorf("file name = %q, want cuisle.db", filepath.Base(db.Path))
	}
	if _, err := os.Stat(db.Path); err != nil {
		t.Errorf("DB file missing: %v", err)
	}
	if got := storetest.Count(t, db, "schema_migration", ""); got < 1 {
		t.Errorf("schema_migration rows = %d, want >= 1", got)
	}
	var mode string
	if err := db.W.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q, %v; want wal", mode, err)
	}
}

func TestOpenFile_TwoCallsGetIndependentDatabases(t *testing.T) {
	defer recoverStub(t)
	a := storetest.OpenFile(t, nil)
	b := storetest.OpenFile(t, nil)
	if a.Path == b.Path {
		t.Fatal("OpenFile returned the same path twice")
	}
	storetest.Exec(t, a, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)")
	if n := storetest.Count(t, b, "app_setting", ""); n != 0 {
		t.Errorf("second DB sees %d rows from the first", n)
	}
}

func TestExec_RunsStatementsWithArgsAndMultipleStatements(t *testing.T) {
	defer recoverStub(t)
	db := storetest.Open(t, nil)
	storetest.Exec(t, db, "INSERT INTO app_setting(key,value,updated_at) VALUES (?, ?, ?)", "theme", "dark", 7)
	if got := storetest.Count(t, db, "app_setting", "key = ? AND value = ?", "theme", "dark"); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
	storetest.Exec(t, db, `INSERT INTO app_setting(key,value,updated_at) VALUES ('a','1',1);
		INSERT INTO app_setting(key,value,updated_at) VALUES ('b','2',1);`)
	if got := storetest.Count(t, db, "app_setting", ""); got != 3 {
		t.Errorf("rows = %d, want 3", got)
	}
}

func TestCount_WhereClauseWithArguments(t *testing.T) {
	defer recoverStub(t)
	db := storetest.Open(t, nil)
	storetest.Exec(t, db, `INSERT INTO app_setting(key,value,updated_at) VALUES ('a','1',1), ('b','2',2), ('c','2',3)`)
	cases := []struct {
		where string
		args  []any
		want  int
	}{
		{"", nil, 3},
		{"value = '2'", nil, 2},
		{"value = ?", []any{"2"}, 2},
		{"value = ? AND updated_at > ?", []any{"2", 2}, 1},
		{"key = 'zzz'", nil, 0},
	}
	for _, c := range cases {
		if got := storetest.Count(t, db, "app_setting", c.where, c.args...); got != c.want {
			t.Errorf("Count(where %q) = %d, want %d", c.where, got, c.want)
		}
	}
}

func TestChecksum_StableEqualForEqualDataAndSensitiveToChanges(t *testing.T) {
	defer recoverStub(t)
	seed := `INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1), ('text_size','large',2);
		INSERT INTO sink(id,kind,name,created_at,updated_at) VALUES (1,'file','Nightly',1,1);`
	a := storetest.Open(t, nil)
	b := storetest.Open(t, nil)
	storetest.Exec(t, a, seed)
	storetest.Exec(t, b, seed)

	h1 := storetest.Checksum(t, a, "app_setting", "sink")
	if h1 == "" {
		t.Fatal("empty checksum")
	}
	if h2 := storetest.Checksum(t, a, "app_setting", "sink"); h2 != h1 {
		t.Errorf("checksum not stable: %q then %q", h1, h2)
	}
	if hb := storetest.Checksum(t, b, "app_setting", "sink"); hb != h1 {
		t.Errorf("identical data hashed differently: %q vs %q", h1, hb)
	}

	storetest.Exec(t, a, "UPDATE app_setting SET value='light' WHERE key='theme'")
	if h3 := storetest.Checksum(t, a, "app_setting", "sink"); h3 == h1 {
		t.Error("checksum did not change after an UPDATE of a named table")
	}
	storetest.Exec(t, a, "UPDATE app_setting SET value='dark' WHERE key='theme'")
	if h4 := storetest.Checksum(t, a, "app_setting", "sink"); h4 != h1 {
		t.Error("checksum did not return to the original after the data was restored")
	}
	storetest.Exec(t, a, "INSERT INTO app_setting(key,value,updated_at) VALUES ('extra','x',3)")
	if h5 := storetest.Checksum(t, a, "app_setting", "sink"); h5 == h1 {
		t.Error("checksum did not change after an INSERT")
	}
}

func TestChecksum_IgnoresTablesThatWereNotNamed(t *testing.T) {
	defer recoverStub(t)
	db := storetest.Open(t, nil)
	storetest.Exec(t, db, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)")
	before := storetest.Checksum(t, db, "app_setting")
	storetest.Exec(t, db, "INSERT INTO sink(id,kind,name,created_at,updated_at) VALUES (1,'file','Nightly',1,1)")
	if after := storetest.Checksum(t, db, "app_setting"); after != before {
		t.Error("checksum of app_setting changed when only the sink table changed")
	}
	if storetest.Checksum(t, db, "app_setting", "sink") == before {
		t.Error("adding a table to the checksum must change it when that table has rows")
	}
}

// recordingTB captures failures instead of failing the real test.
type recordingTB struct {
	testing.TB
	failed bool
}

func (r *recordingTB) Fatalf(string, ...any) { r.failed = true; runtime.Goexit() }
func (r *recordingTB) Fatal(...any)          { r.failed = true; runtime.Goexit() }
func (r *recordingTB) Errorf(string, ...any) { r.failed = true }
func (r *recordingTB) Error(...any)          { r.failed = true }
func (r *recordingTB) FailNow()              { r.failed = true; runtime.Goexit() }
func (r *recordingTB) Fail()                 { r.failed = true }

func runRecording(t *testing.T, fn func(tb testing.TB)) (failed bool) {
	t.Helper()
	r := &recordingTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(r)
	}()
	<-done
	return r.failed
}

func TestExec_FailsTheTestOnBadSQL(t *testing.T) {
	defer recoverStub(t)
	db := storetest.Open(t, nil)
	if !runRecording(t, func(tb testing.TB) { storetest.Exec(tb, db, "THIS IS NOT SQL") }) {
		t.Error("Exec swallowed an SQL error; it must fail tb")
	}
	if !runRecording(t, func(tb testing.TB) {
		// violates CHECK (name length)
		storetest.Exec(tb, db, "INSERT INTO standup(name, position, created_at, updated_at) VALUES ('', 0, 1, 1)")
	}) {
		t.Error("Exec swallowed a constraint violation")
	}
	if runRecording(t, func(tb testing.TB) { storetest.Exec(tb, db, "SELECT 1") }) {
		t.Error("Exec failed on valid SQL")
	}
}

func TestCount_FailsTheTestOnUnknownTable(t *testing.T) {
	defer recoverStub(t)
	db := storetest.Open(t, nil)
	if !runRecording(t, func(tb testing.TB) { storetest.Count(tb, db, "no_such_table", "") }) {
		t.Error("Count on a missing table must fail tb")
	}
}

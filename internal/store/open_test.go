package store_test

// Tests for store.Open / OpenMemory / Close / SchemaVersion (architecture.md §6.3, data-model.md §3).
//
// SPEC-NOTE: schema_migration.applied_at is stored as Unix milliseconds from Options.Now (store.Ms(opts.Now())),
// consistent with the data-model §1 timestamp convention. Tests inject a fixed clock to assert it.
// SPEC-NOTE: schema_migration.name is Migration.Name, i.e. the part of "NNNN_name.sql" between "NNNN_" and ".sql"
// (so "0001_init.sql" is stored as "init").

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

// the 33 tables of 0001_init.sql plus the runner-owned schema_migration.
var tablesOf0001 = []string{
	"app_setting", "assignee_change", "attachment", "board", "board_column", "board_column_status", "comment",
	"connection", "dev_commit", "dev_pr_event", "field_def", "flag_change", "issue", "issue_content",
	"issue_field_value", "issue_link", "issue_type", "person", "schema_migration", "sink", "sink_outbox", "space",
	"sprint", "standup", "standup_column_map", "standup_config_history", "standup_issue", "standup_issue_span",
	"standup_lane", "standup_settings", "standup_speaker", "standup_speaker_removed", "standup_status_lane",
	"standup_sync_state", "standup_ui_state", "status", "status_change", "sync_log", "sync_run", "ticket_draft",
	"ticket_field_pref", "transition_cache", "ui_state",
}

// headVersion is the newest embedded migration (0007_zoom).
const headVersion = 7

func TestOpen_FreshFile_ReportsMigrationAndNoRecovery(t *testing.T) {
	defer recoverStub(t)
	_, info := openAt(t, t.TempDir(), store.Options{})
	if info.SchemaVersion != headVersion {
		t.Errorf("SchemaVersion = %d, want %d", info.SchemaVersion, headVersion)
	}
	if !reflect.DeepEqual(info.Migrated, []int{1, 2, 3, 4, 5, 6, 7}) {
		t.Errorf("Migrated = %v, want [1 2 3 4 5 6 7]", info.Migrated)
	}
	if info.BackupPath != "" {
		t.Errorf("BackupPath = %q, want empty (nothing to back up on a new file)", info.BackupPath)
	}
	if info.Recovered || info.RestoredFrom != "" {
		t.Errorf("Recovered=%v RestoredFrom=%q, want false/empty", info.Recovered, info.RestoredFrom)
	}
}

func TestOpen_FreshFile_WriterPragmas(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	want := []struct{ pragma, want string }{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"synchronous", "1"}, // NORMAL
		{"busy_timeout", "5000"},
		{"temp_store", "2"},   // MEMORY
		{"auto_vacuum", "2"},  // INCREMENTAL, set when the file is created
		{"user_version", "7"}, // headVersion
		{"query_only", "0"},
	}
	for _, c := range want {
		if got := scalarStr(t, db.W, "PRAGMA "+c.pragma); got != c.want {
			t.Errorf("W PRAGMA %s = %q, want %q", c.pragma, got, c.want)
		}
	}
}

func TestOpen_FreshFile_PragmasOnEveryReaderConnection(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	cctx, cancel := context_withTimeout(5 * time.Second) // a pool smaller than 4 would block here, not hang the suite
	defer cancel()
	var conns []*sql.Conn
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		c, err := db.R.Conn(cctx)
		if err != nil {
			t.Fatalf("could not hold reader connection #%d simultaneously: %v", i+1, err)
		}
		conns = append(conns, c)
	}
	want := []struct{ pragma, want string }{
		{"journal_mode", "wal"},
		{"foreign_keys", "1"},
		{"synchronous", "1"},
		{"busy_timeout", "5000"},
		{"temp_store", "2"},
		{"query_only", "1"},
	}
	for i, c := range conns {
		for _, w := range want {
			if got := scalarStr(t, c, "PRAGMA "+w.pragma); got != w.want {
				t.Errorf("R conn %d: PRAGMA %s = %q, want %q", i, w.pragma, got, w.want)
			}
		}
		if _, err := c.ExecContext(cctx, "INSERT INTO app_setting(key,value,updated_at) VALUES ('x','y',1)"); err == nil {
			t.Errorf("R conn %d accepted a write; reader connections must be query_only", i)
		}
	}
}

func TestOpen_PoolSizes(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	if got := db.W.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("W max open connections = %d, want 1", got)
	}
	if got := db.R.Stats().MaxOpenConnections; got != 4 {
		t.Errorf("R max open connections = %d, want 4", got)
	}
}

func TestOpen_FreshFile_CreatesEveryTableOf0001(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	if got := tableNames(t, db.W); !reflect.DeepEqual(got, tablesOf0001) {
		t.Errorf("tables (W) = %v\nwant %v", got, tablesOf0001)
	}
	if got := tableNames(t, db.R); !reflect.DeepEqual(got, tablesOf0001) {
		t.Errorf("tables (R) = %v\nwant %v", got, tablesOf0001)
	}
	// 0001 seeds the single ui_state row.
	if n := countRows(t, db.R, "ui_state"); n != 1 {
		t.Errorf("ui_state rows = %d, want 1", n)
	}
}

func TestOpen_FreshFile_RecordsSha256Checksum(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	if got, want := columnNames(t, db.W, "schema_migration"), []string{"version", "name", "checksum", "applied_at"}; !reflect.DeepEqual(got, want) {
		t.Errorf("schema_migration columns = %v, want %v", got, want)
	}
	if n := countRows(t, db.W, "schema_migration"); n != headVersion {
		t.Fatalf("schema_migration rows = %d, want %d", n, headVersion)
	}
	h := head1(t)
	var version int
	var name, checksum string
	var appliedAt int64
	if err := db.W.QueryRowContext(ctx, "SELECT version, name, checksum, applied_at FROM schema_migration WHERE version = 1").Scan(&version, &name, &checksum, &appliedAt); err != nil {
		t.Fatal(err)
	}
	if version != 1 || name != "init" {
		t.Errorf("row = (%d, %q), want (1, %q)", version, name, "init")
	}
	if checksum != h.Checksum || len(checksum) != 64 {
		t.Errorf("checksum = %q, want sha256 of 0001_init.sql %q", checksum, h.Checksum)
	}
	if appliedAt != fixedNow.UnixMilli() {
		t.Errorf("applied_at = %d, want %d (Options.Now in ms)", appliedAt, fixedNow.UnixMilli())
	}
}

func TestOpen_FreshFile_SchemaVersionMatchesUserVersion(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	for name, q := range map[string]*sql.DB{"W": db.W, "R": db.R} {
		got, err := store.SchemaVersion(ctx, q)
		if err != nil || got != headVersion {
			t.Errorf("SchemaVersion(%s) = %d, %v; want %d, nil", name, got, err, headVersion)
		}
	}
}

func TestSchemaVersion_EmptyDatabaseIsZero(t *testing.T) {
	defer recoverStub(t)
	raw, _ := freshRaw(t) // no schema_migration table at all
	got, err := store.SchemaVersion(ctx, raw)
	if err != nil || got != 0 {
		t.Errorf("SchemaVersion(empty) = %d, %v; want 0, nil", got, err)
	}
}

func TestOpen_PopulatesDBFields(t *testing.T) {
	defer recoverStub(t)
	dir := t.TempDir()
	db, _ := openAt(t, dir, store.Options{})
	if want := filepath.Join(dir, "cuisle.db"); db.Path != want {
		t.Errorf("Path = %q, want %q", db.Path, want)
	}
	if db.Log == nil {
		t.Error("Log is nil; a nil Options.Logger must become a discarding logger")
	}
	if db.Now == nil || !db.Now().Equal(fixedNow) {
		t.Error("Now must be Options.Now when provided")
	}
}

func TestOpen_ZeroOptionsDefaultsClockAndLogger(t *testing.T) {
	defer recoverStub(t)
	db, _, err := store.Open(ctx, filepath.Join(t.TempDir(), "cuisle.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if db.Now == nil || db.Log == nil {
		t.Fatalf("Now=%v Log=%v; both must default when Options leaves them nil", db.Now != nil, db.Log != nil)
	}
	if d := time.Since(db.Now()); d < -time.Minute || d > time.Minute {
		t.Errorf("default clock is not time.Now (off by %v)", d)
	}
}

func TestOpen_ReopenExisting_KeepsDataWithoutMigrationOrBackup(t *testing.T) {
	defer recoverStub(t)
	dir := t.TempDir()
	db, _ := openAt(t, dir, store.Options{})
	mustExec(t, db.W, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)")
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, info := openAt(t, dir, store.Options{})
	if len(info.Migrated) != 0 || info.BackupPath != "" || info.Recovered || info.SchemaVersion != headVersion {
		t.Errorf("reopen info = %+v; want SchemaVersion at head, nothing migrated, no backup, not recovered", info)
	}
	if got := scalarStr(t, db2.R, "SELECT value FROM app_setting WHERE key='theme'"); got != "dark" {
		t.Errorf("theme after reopen = %q, want dark", got)
	}
	if m := glob(t, filepath.Join(dir, "cuisle.db.*")); len(m) != 0 {
		t.Errorf("unexpected side files after a no-op reopen: %v", m)
	}
}

func TestClose_CheckpointsAndTruncatesWAL(t *testing.T) {
	defer recoverStub(t)
	db, dir := openTemp(t)
	mustExec(t, db.W, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)")
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "cuisle.db-wal")); err == nil && fi.Size() != 0 {
		t.Errorf("cuisle.db-wal is %d bytes after Close; wal_checkpoint(TRUNCATE) should leave it empty or gone", fi.Size())
	}
	// All committed data lives in the main file now.
	raw := rawDB(t, filepath.Join(dir, "cuisle.db"))
	if got := scalarStr(t, raw, "SELECT value FROM app_setting WHERE key='theme'"); got != "dark" {
		t.Errorf("theme read from the main file = %q, want dark", got)
	}
}

func TestOpen_NewerSchema_RefusedWithDBError(t *testing.T) {
	defer recoverStub(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "cuisle.db")
	db, _ := openAt(t, dir, store.Options{})
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	raw := rawDB(t, path)
	mustExec(t, raw, "INSERT INTO schema_migration(version,name,checksum,applied_at) VALUES (99,'from_the_future','x',1)")
	raw.Close()

	got, _, err := store.Open(ctx, path, store.Options{Now: clockAt(fixedNow)})
	if err == nil {
		got.Close(ctx)
		t.Fatal("Open succeeded on a database created by a newer Cuisle")
	}
	if got != nil {
		t.Error("Open returned a non-nil *DB together with the error; the DB must not be opened")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error %T is not *apperr.Error", err)
	}
	if ae.Code != apperr.DB || ae.Message != "This data was created by a newer Cuisle" {
		t.Errorf("error = {%s, %q}; want {db, %q}", ae.Code, ae.Message, "This data was created by a newer Cuisle")
	}
	// Nothing was moved, restored or backed up: the user's file is left alone.
	if m := glob(t, filepath.Join(dir, "cuisle.db.*")); len(m) != 0 {
		t.Errorf("refusing a newer DB must not create side files, found %v", m)
	}
	raw2 := rawDB(t, path)
	if n := countRows(t, raw2, "schema_migration"); n != headVersion+1 {
		t.Errorf("schema_migration rows = %d, want %d (file untouched)", n, headVersion+1)
	}
}

func TestOpenMemory_IsMigratedAndPathless(t *testing.T) {
	defer recoverStub(t)
	db, err := store.OpenMemory(ctx, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if db.Path != "" {
		t.Errorf("Path = %q, want empty for an in-memory DB", db.Path)
	}
	if v, err := store.SchemaVersion(ctx, db.W); err != nil || v != headVersion {
		t.Errorf("SchemaVersion = %d, %v; want %d, nil", v, err, headVersion)
	}
	if got := tableNames(t, db.W); !reflect.DeepEqual(got, tablesOf0001) {
		t.Errorf("tables = %v, want %v", got, tablesOf0001)
	}
}

func TestOpenMemory_WriterAndReaderShareOneDatabase(t *testing.T) {
	defer recoverStub(t)
	db, err := store.OpenMemory(ctx, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	mustExec(t, db.W, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)")
	if got := scalarStr(t, db.R, "SELECT value FROM app_setting WHERE key='theme'"); got != "dark" {
		t.Errorf("reader sees %q, want the writer's committed row", got)
	}
}

func TestOpenMemory_EachCallIsPrivate(t *testing.T) {
	defer recoverStub(t)
	a, err := store.OpenMemory(ctx, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(ctx)
	b, err := store.OpenMemory(ctx, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	mustExec(t, a.W, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)")
	if n := countRows(t, b.R, "app_setting"); n != 0 {
		t.Errorf("second in-memory DB sees %d rows from the first; they must be isolated", n)
	}
}

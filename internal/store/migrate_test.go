package store_test

// Tests for store.Migrate and store.LoadMigrations (data-model.md §3: the test list there is mandatory).
//
// SPEC-NOTE: LoadMigrations parses the files that sit directly in the FS it is given (it does not descend into
// subdirectories); Open is expected to call it on fs.Sub(Migrations, "migrations"). Tests use a root-level
// fstest.MapFS and fs.Sub(store.Migrations, "migrations"); an implementation that also tolerates the embed root is
// fine but not required. Files that do not match NNNN_name.sql (README.md, directories) are ignored.
// SPEC-NOTE: Migration.Name is the text between "NNNN_" and ".sql" ("0001_init.sql" -> "init").
// SPEC-NOTE: "non-empty DB" (backup rule) means the DB already has applied migrations (schema_migration rows). A
// brand-new file never gets a backup. The returned backup path is filepath.Join(backupDir, "cuisle.db.bak-v<N>")
// with N = the highest version applied before this run. "2 newest" means the two highest N; pruning touches only
// cuisle.db.bak-v* (never cuisle.db.bak-weekly or *.corrupt-*).
// SPEC-NOTE: testdata/v<N>.sql files are DATA ONLY and are applied on top of Migrate(list[:N]); the test loops over
// every testdata/v*.sql, migrates to head = embedded migrations + one synthetic extra migration, and asserts counts.
// Adding v2.sql when 0002 ships needs no test change.
// SPEC-NOTE: a checksum mismatch never rewrites the stored checksum, and in non-Dev mode pending migrations still run.
// SPEC-NOTE: a migration failure rolls back only that file; earlier files stay applied. The returned `applied` on
// error is not asserted.

import (
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

const goldenPath = "testdata/schema.golden.sql"

// embeddedList builds the embedded migration list by hand (independent of LoadMigrations).
func embeddedList(t testing.TB) []store.Migration {
	t.Helper()
	entries, err := store.Migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Migration
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") || len(name) < 6 {
			continue
		}
		v, err := strconv.Atoi(name[:4])
		if err != nil {
			continue
		}
		b, err := store.Migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, store.Migration{
			Version: v, Name: strings.TrimSuffix(name[5:], ".sql"), SQL: string(b), Checksum: sha(b),
			FKOff: strings.HasPrefix(string(b), "-- cuisle:fk-off\n"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}

func migrate(t testing.TB, w *sql.DB, list []store.Migration, dir string, opts store.Options) ([]int, string, error) {
	t.Helper()
	if opts.Now == nil {
		opts.Now = clockAt(fixedNow)
	}
	return store.Migrate(ctx, w, list, dir, opts)
}

func mustMigrate(t testing.TB, w *sql.DB, list []store.Migration, dir string) ([]int, string) {
	t.Helper()
	applied, backup, err := migrate(t, w, list, dir, store.Options{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return applied, backup
}

// rowCounts returns the row count of every table except the runner's schema_migration.
func rowCounts(t testing.TB, q *sql.DB) map[string]int {
	t.Helper()
	m := map[string]int{}
	for _, tb := range tableNames(t, q) {
		if tb == "schema_migration" {
			continue
		}
		m[tb] = countRows(t, q, tb)
	}
	return m
}

func appliedVersions(t testing.TB, q *sql.DB) []int {
	t.Helper()
	rows, err := q.QueryContext(ctx, "SELECT version FROM schema_migration ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func backupFiles(t testing.TB, dir string) []string {
	t.Helper()
	m := glob(t, filepath.Join(dir, "cuisle.db.bak-v*"))
	for i := range m {
		m[i] = filepath.Base(m[i])
	}
	sort.Strings(m)
	return m
}

// ---- schema golden --------------------------------------------------------------------------------------------

func compareGolden(t *testing.T, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	wantB, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden missing, run `go test ./internal/store -update` (%v)", err)
	}
	want := string(wantB)
	if got == want {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) || i < len(wl); i++ {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			t.Fatalf("schema differs from %s at line %d (run -update only if the change is deliberate)\n got: %s\nwant: %s", goldenPath, i+1, g, w)
		}
	}
}

func TestSchema_FreshOpenMatchesGolden(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	compareGolden(t, dumpSchema(t, db.W))
}

func TestSchema_FreshMigrateMatchesGolden(t *testing.T) {
	defer recoverStub(t)
	raw, _ := freshRaw(t)
	mustMigrate(t, raw, embeddedList(t), "")
	compareGolden(t, dumpSchema(t, raw))
}

// ---- Migrate: basics ------------------------------------------------------------------------------------------

func TestMigrate_FreshDB_AppliesAllInOrderAndRecordsThem(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	list := []store.Migration{head1(t), mig2(), mig3()}
	applied, backup, err := migrate(t, raw, list, dir, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(applied, []int{1, 2, 3}) {
		t.Errorf("applied = %v, want [1 2 3]", applied)
	}
	if backup != "" {
		t.Errorf("backup = %q; an empty DB must not be backed up", backup)
	}
	if got := appliedVersions(t, raw); !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("schema_migration versions = %v", got)
	}
	if got := scalarInt(t, raw, "PRAGMA user_version"); got != 3 {
		t.Errorf("user_version = %d, want 3", got)
	}
	if v, err := store.SchemaVersion(ctx, raw); err != nil || v != 3 {
		t.Errorf("SchemaVersion = %d, %v; want 3, nil", v, err)
	}
	for _, c := range []string{"note", "flag_note"} {
		if !hasColumn(t, raw, "issue", c) {
			t.Errorf("issue.%s missing after migration", c)
		}
	}
	// Name, checksum and applied_at are stored per file.
	var name, checksum string
	var appliedAt int64
	if err := raw.QueryRowContext(ctx, "SELECT name, checksum, applied_at FROM schema_migration WHERE version=2").Scan(&name, &checksum, &appliedAt); err != nil {
		t.Fatal(err)
	}
	if name != "add_note" || checksum != mig2().Checksum || appliedAt != fixedNow.UnixMilli() {
		t.Errorf("row 2 = (%q, %q, %d), want (add_note, %s, %d)", name, checksum, appliedAt, mig2().Checksum, fixedNow.UnixMilli())
	}
}

func TestMigrate_Twice_IsANoOp(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	h := newRecHandler()
	opts := store.Options{Logger: slog.New(h)}
	list := []store.Migration{head1(t), mig2()}
	if _, _, err := migrate(t, raw, list, dir, opts); err != nil {
		t.Fatal(err)
	}
	seedV1(t, raw)
	before := rowCounts(t, raw)
	rowsBefore := countRows(t, raw, "schema_migration")

	applied, backup, err := migrate(t, raw, list, dir, opts)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(applied) != 0 || backup != "" {
		t.Errorf("second Migrate = applied %v, backup %q; want nothing", applied, backup)
	}
	if got := rowCounts(t, raw); !reflect.DeepEqual(got, before) {
		t.Errorf("row counts changed by a no-op Migrate:\n got %v\nwant %v", got, before)
	}
	if n := countRows(t, raw, "schema_migration"); n != rowsBefore {
		t.Errorf("schema_migration rows = %d, want %d", n, rowsBefore)
	}
	if files := backupFiles(t, dir); len(files) != 0 {
		t.Errorf("backup written although nothing was pending: %v", files)
	}
	if w := h.count(slog.LevelWarn); w != 0 {
		t.Errorf("%d warnings logged on an untampered re-run, want 0", w)
	}
}

func TestMigrate_AppliedAtComesFromOptionsNow(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	at := fixedNow.AddDate(0, 0, 3)
	if _, _, err := store.Migrate(ctx, raw, []store.Migration{head1(t)}, dir, store.Options{Now: clockAt(at)}); err != nil {
		t.Fatal(err)
	}
	if got := scalarInt(t, raw, "SELECT applied_at FROM schema_migration WHERE version=1"); got != at.UnixMilli() {
		t.Errorf("applied_at = %d, want %d", got, at.UnixMilli())
	}
}

// ---- Migrate: checksum ----------------------------------------------------------------------------------------

func tampered(m store.Migration) store.Migration { m.Checksum = "0000tampered"; return m }

func TestMigrate_TamperedChecksum_WarnsOutsideDev(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, dir)

	h := newRecHandler()
	applied, _, err := migrate(t, raw, []store.Migration{tampered(head1(t))}, dir, store.Options{Logger: slog.New(h)})
	if err != nil {
		t.Fatalf("non-dev checksum mismatch must not fail: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("applied = %v, want none", applied)
	}
	if h.count(slog.LevelWarn) < 1 {
		t.Error("no warn-level log record for the checksum mismatch")
	}
	if got := scalarStr(t, raw, "SELECT checksum FROM schema_migration WHERE version=1"); got != head1(t).Checksum {
		t.Errorf("stored checksum rewritten to %q; the original must stay", got)
	}
}

func TestMigrate_TamperedChecksum_StillAppliesPendingOutsideDev(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, dir)
	h := newRecHandler()
	applied, _, err := migrate(t, raw, []store.Migration{tampered(head1(t)), mig2()}, dir, store.Options{Logger: slog.New(h)})
	if err != nil || !reflect.DeepEqual(applied, []int{2}) {
		t.Fatalf("Migrate = %v, %v; want [2], nil", applied, err)
	}
	if h.count(slog.LevelWarn) < 1 {
		t.Error("no warn-level log record for the checksum mismatch")
	}
}

func TestMigrate_TamperedChecksum_IsAnErrorInDev(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, dir)
	_, _, err := migrate(t, raw, []store.Migration{tampered(head1(t)), mig2()}, dir, store.Options{Dev: true})
	if err == nil {
		t.Fatal("Dev=true must fail on a checksum mismatch")
	}
	if hasColumn(t, raw, "issue", "note") {
		t.Error("migration 2 was applied despite the dev-mode checksum failure")
	}
}

// ---- Migrate: newer DB ----------------------------------------------------------------------------------------

func TestMigrate_DBNewerThanBinary_IsRefusedWithDBError(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t), mig2()}, dir)

	_, backup, err := migrate(t, raw, []store.Migration{head1(t)}, dir, store.Options{})
	if err == nil {
		t.Fatal("Migrate accepted a DB whose schema is newer than the binary")
	}
	ae := apperr.From(err)
	if ae.Code != apperr.DB || ae.Message != "This data was created by a newer Cuisle" {
		t.Errorf("error = {%s, %q}, want {db, %q}", ae.Code, ae.Message, "This data was created by a newer Cuisle")
	}
	if backup != "" || len(backupFiles(t, dir)) != 0 {
		t.Errorf("a refused DB must not be backed up or touched (backup %q, files %v)", backup, backupFiles(t, dir))
	}
	if got := appliedVersions(t, raw); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("schema_migration = %v, want [1 2] untouched", got)
	}
}

// ---- Migrate: pre-migration backups ---------------------------------------------------------------------------

func TestMigrate_PendingOnNonEmptyDB_WritesBackupOfThePreviousVersion(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, dir)
	seedV1(t, raw)

	applied, backup, err := migrate(t, raw, []store.Migration{head1(t), mig2()}, dir, store.Options{})
	if err != nil || !reflect.DeepEqual(applied, []int{2}) {
		t.Fatalf("Migrate = %v, %v; want [2], nil", applied, err)
	}
	want := filepath.Join(dir, "cuisle.db.bak-v1")
	if backup != want {
		t.Fatalf("backup = %q, want %q", backup, want)
	}
	bak := rawDB(t, want)
	if v, err := store.SchemaVersion(ctx, bak); err != nil || v != 1 {
		t.Errorf("backup SchemaVersion = %d, %v; want 1 (taken BEFORE the migration)", v, err)
	}
	if hasColumn(t, bak, "issue", "note") {
		t.Error("backup already contains the new column")
	}
	if n := countRows(t, bak, "issue"); n != 2 {
		t.Errorf("backup has %d issues, want 2", n)
	}
	assertIntegrity(t, bak)
}

func TestMigrate_SeveralPending_BackupIsNamedForTheHighestAppliedVersion(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, dir)
	applied, backup, err := migrate(t, raw, []store.Migration{head1(t), mig2(), mig3()}, dir, store.Options{})
	if err != nil || !reflect.DeepEqual(applied, []int{2, 3}) {
		t.Fatalf("Migrate = %v, %v; want [2 3], nil", applied, err)
	}
	if want := filepath.Join(dir, "cuisle.db.bak-v1"); backup != want {
		t.Errorf("backup = %q, want %q", backup, want)
	}
	if got := backupFiles(t, dir); !reflect.DeepEqual(got, []string{"cuisle.db.bak-v1"}) {
		t.Errorf("backup files = %v, want only bak-v1 (one backup per run)", got)
	}
}

func TestMigrate_Backups_KeepOnlyTheTwoNewestAndLeaveOtherFilesAlone(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	weekly := filepath.Join(dir, "cuisle.db.bak-weekly")
	corrupt := filepath.Join(dir, "cuisle.db.corrupt-1700000000000")
	for _, p := range []string{weekly, corrupt} {
		if err := os.WriteFile(p, []byte("keep me"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list := []store.Migration{head1(t)}
	mustMigrate(t, raw, list, dir)
	steps := []struct {
		add  store.Migration
		want []string
	}{
		{mig2(), []string{"cuisle.db.bak-v1"}},
		{mig3(), []string{"cuisle.db.bak-v1", "cuisle.db.bak-v2"}},
		{mig4(), []string{"cuisle.db.bak-v2", "cuisle.db.bak-v3"}}, // v1 pruned
	}
	for _, s := range steps {
		list = append(list, s.add)
		mustMigrate(t, raw, list, dir)
		if got := backupFiles(t, dir); !reflect.DeepEqual(got, s.want) {
			t.Fatalf("after migrating to v%d backups = %v, want %v", s.add.Version, got, s.want)
		}
	}
	for _, p := range []string{weekly, corrupt} {
		if b, err := os.ReadFile(p); err != nil || string(b) != "keep me" {
			t.Errorf("%s was touched by backup pruning (err %v)", filepath.Base(p), err)
		}
	}
	// The newest backup is a real v3 database.
	bak := rawDB(t, filepath.Join(dir, "cuisle.db.bak-v3"))
	if v, err := store.SchemaVersion(ctx, bak); err != nil || v != 3 {
		t.Errorf("bak-v3 SchemaVersion = %d, %v; want 3", v, err)
	}
}

func TestMigrate_ReplacesAStaleBackupOfTheSameVersion(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, dir)
	stale := filepath.Join(dir, "cuisle.db.bak-v1")
	if err := os.WriteFile(stale, []byte("stale junk from an earlier crashed attempt"), 0o644); err != nil {
		t.Fatal(err)
	}
	// VACUUM INTO refuses an existing target, so Migrate has to remove it first.
	_, backup, err := migrate(t, raw, []store.Migration{head1(t), mig2()}, dir, store.Options{})
	if err != nil || backup != stale {
		t.Fatalf("Migrate = backup %q, %v; want %q, nil", backup, err, stale)
	}
	bak := rawDB(t, stale)
	if v, err := store.SchemaVersion(ctx, bak); err != nil || v != 1 {
		t.Errorf("replaced backup SchemaVersion = %d, %v; want 1", v, err)
	}
}

func TestMigrate_NoBackupOnAnEmptyDB(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	_, backup, err := migrate(t, raw, []store.Migration{head1(t), mig2()}, dir, store.Options{})
	if err != nil || backup != "" {
		t.Fatalf("Migrate = backup %q, %v; want \"\", nil", backup, err)
	}
	if files := backupFiles(t, dir); len(files) != 0 {
		t.Errorf("backup files on a brand-new DB: %v", files)
	}
}

func TestMigrate_NoBackupWhenBackupDirIsEmpty(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, "")
	_, backup, err := migrate(t, raw, []store.Migration{head1(t), mig2()}, "", store.Options{})
	if err != nil || backup != "" {
		t.Fatalf("Migrate = backup %q, %v; want \"\", nil", backup, err)
	}
	if files := backupFiles(t, dir); len(files) != 0 {
		t.Errorf("backup written although backupDir == \"\": %v", files)
	}
	if !hasColumn(t, raw, "issue", "note") {
		t.Error("migration 2 was not applied")
	}
}

// ---- Migrate: atomicity ---------------------------------------------------------------------------------------

func TestMigrate_FailingFile_RollsBackThatFileOnly(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t), mig2()}, dir)
	bad := synth(3, "broken", "ALTER TABLE issue ADD COLUMN half_done TEXT;\nTHIS IS NOT SQL;\n")

	_, _, err := migrate(t, raw, []store.Migration{head1(t), mig2(), bad}, dir, store.Options{})
	if err == nil {
		t.Fatal("Migrate succeeded on a broken migration")
	}
	if hasColumn(t, raw, "issue", "half_done") {
		t.Error("the statement before the failure stayed applied; the file must be one transaction")
	}
	if got := appliedVersions(t, raw); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("schema_migration = %v, want [1 2]", got)
	}
	if got := scalarInt(t, raw, "PRAGMA user_version"); got != 2 {
		t.Errorf("user_version = %d, want 2", got)
	}
	if !hasColumn(t, raw, "issue", "note") {
		t.Error("earlier migrations must stay applied")
	}
	// The connection is still usable (no dangling transaction).
	mustExec(t, raw, "INSERT INTO app_setting(key,value,updated_at) VALUES ('after','failure',1)")
}

// ---- Migrate: fk-off table rebuilds ---------------------------------------------------------------------------

// A classic table rebuild of `standup`. With foreign keys ON, DROP TABLE standup cascades into every child table.
const rebuildStandupSQL = `-- cuisle:fk-off
CREATE TABLE standup_new (
  id INTEGER PRIMARY KEY,
  connection_id INTEGER REFERENCES connection(id) ON DELETE SET NULL,
  name TEXT NOT NULL,
  position INTEGER NOT NULL,
  rev INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER
) STRICT;
INSERT INTO standup_new (id, connection_id, name, position, rev, created_at, updated_at, deleted_at)
  SELECT id, connection_id, name, position, rev, created_at, updated_at, deleted_at FROM standup;
DROP TABLE standup;
ALTER TABLE standup_new RENAME TO standup;
`

func TestMigrate_FKOffRebuild_KeepsChildRowsAndRestoresForeignKeys(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, dir)
	seedV1(t, raw)
	before := rowCounts(t, raw)
	if before["standup_settings"] != 2 || before["standup_issue_span"] != 3 {
		t.Fatalf("fixture drift: %v", before)
	}

	rebuild := synth(2, "rebuild_standup", rebuildStandupSQL)
	if !rebuild.FKOff {
		t.Fatal("test fixture bug: expected FKOff")
	}
	applied, _, err := migrate(t, raw, []store.Migration{head1(t), rebuild}, dir, store.Options{})
	if err != nil || !reflect.DeepEqual(applied, []int{2}) {
		t.Fatalf("Migrate = %v, %v; want [2], nil", applied, err)
	}
	if after := rowCounts(t, raw); !reflect.DeepEqual(after, before) {
		t.Errorf("child rows lost in the rebuild (foreign keys were not switched off for the file):\n got %v\nwant %v", after, before)
	}
	if hasColumn(t, raw, "standup", "board_id") || !hasColumn(t, raw, "standup", "position") {
		t.Errorf("standup was not rebuilt: columns %v", columnNames(t, raw, "standup"))
	}
	if got := scalarInt(t, raw, "PRAGMA foreign_keys"); got != 1 {
		t.Errorf("foreign_keys = %d after an fk-off migration, want 1 (restored)", got)
	}
	assertIntegrity(t, raw)
}

func TestMigrate_FKOff_ViolationFailsAndRollsBack(t *testing.T) {
	defer recoverStub(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, []store.Migration{head1(t)}, dir)
	dangling := synth(2, "dangling", "-- cuisle:fk-off\nINSERT INTO standup_settings(standup_id) VALUES (999);\n")

	_, _, err := migrate(t, raw, []store.Migration{head1(t), dangling}, dir, store.Options{})
	if err == nil {
		t.Fatal("an fk-off migration that leaves a dangling reference must fail (PRAGMA foreign_key_check before commit)")
	}
	if n := countRows(t, raw, "standup_settings"); n != 0 {
		t.Errorf("standup_settings rows = %d, want 0 (rolled back)", n)
	}
	if got := appliedVersions(t, raw); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("schema_migration = %v, want [1]", got)
	}
	if got := scalarInt(t, raw, "PRAGMA foreign_keys"); got != 1 {
		t.Errorf("foreign_keys = %d after a failed fk-off migration, want 1 (restored)", got)
	}
}

// ---- Record tables survive a migration --------------------------------------------------------------------------

// Counts of the seed in testdata/v1.sql for the families that must never lose a row (hard-coded so that fixture
// drift is noticed).
var v1RecordCounts = map[string]int{
	"issue": 2, "status_change": 3, "assignee_change": 2, "flag_change": 2, "comment": 3, "dev_commit": 2,
	"dev_pr_event": 2, "standup_issue": 2, "standup_issue_span": 3, "standup_config_history": 2, "person": 2,
	"sprint": 1, "status": 2, "issue_type": 1,
}

func seedFile(t testing.TB, q *sql.DB, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, q, string(b))
}

func TestMigrate_RecordTablesSurviveEveryHistoricalVersion(t *testing.T) {
	defer recoverStub(t)
	files, err := filepath.Glob(filepath.Join("testdata", "v*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no testdata/v<N>.sql fixtures found (%v)", err)
	}
	head := embeddedList(t)
	extra := synth(len(head)+1, "add_note", addNoteSQL) // a column on `issue`, applied on top of the real head
	full := append(append([]store.Migration{}, head...), extra)

	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), ".sql")
		n, err := strconv.Atoi(strings.TrimPrefix(base, "v"))
		if err != nil || n < 1 || n > len(head) {
			t.Errorf("%s: bad fixture name or version beyond head", f)
			continue
		}
		t.Run(base, func(t *testing.T) {
			defer recoverStub(t)
			raw, dir := freshRaw(t)
			mustMigrate(t, raw, head[:n], dir)
			seedFile(t, raw, f)
			before := rowCounts(t, raw)
			for tb, want := range v1RecordCounts {
				if n == 1 && before[tb] != want {
					t.Fatalf("fixture drift: %s has %d rows, want %d", tb, before[tb], want)
				}
			}

			applied, backup, err := migrate(t, raw, full, dir, store.Options{})
			if err != nil {
				t.Fatalf("Migrate to head: %v", err)
			}
			if len(applied) == 0 || applied[len(applied)-1] != extra.Version {
				t.Errorf("applied = %v, want it to end with %d", applied, extra.Version)
			}
			// Existing tables keep their rows; tables a later migration creates start empty.
			after := rowCounts(t, raw)
			for tb, n := range after {
				if b, ok := before[tb]; (ok && n != b) || (!ok && n != 0) {
					t.Errorf("row counts changed by migration:\n got %v\nwant %v", after, before)
					break
				}
			}
			assertIntegrity(t, raw)
			if !hasColumn(t, raw, "issue", "note") {
				t.Error("synthetic column missing")
			}
			if n := scalarInt(t, raw, "SELECT count(*) FROM issue WHERE note IS NOT NULL"); n != 0 {
				t.Errorf("%d issues have a non-NULL note, want 0", n)
			}

			// 0003 backfills comment.updated_at from created_at for rows synced before content existed.
			if n < 3 {
				if bad := scalarInt(t, raw, "SELECT count(*) FROM comment WHERE updated_at != created_at"); bad != 0 {
					t.Errorf("%d comments have updated_at != created_at after 0003, want 0", bad)
				}
				if bad := scalarInt(t, raw, "SELECT count(*) FROM standup_sync_state WHERE needs_sync = 0"); bad != 0 {
					t.Errorf("%d standups have needs_sync = 0 after 0003, want 0", bad)
				}
			}

			// A few values, not just counts.
			if got := scalarStr(t, raw, "SELECT from_status_id || '>' || to_status_id FROM status_change WHERE history_id='h2'"); got != "10>11" {
				t.Errorf("status_change h2 = %q, want 10>11", got)
			}
			var vf, vt sql.NullInt64
			if err := raw.QueryRowContext(ctx, "SELECT valid_from, valid_to FROM standup_issue_span WHERE issue_id=1 AND valid_from=3000").Scan(&vf, &vt); err != nil {
				t.Fatal(err)
			}
			if vf.Int64 != 3000 || vt.Valid {
				t.Errorf("open span = (%v, %v), want (3000, NULL)", vf, vt)
			}
			if got := scalarStr(t, raw, "SELECT key FROM issue WHERE id=2 AND removed_at=4000"); got != "MOB-2" {
				t.Errorf("removed issue = %q, want MOB-2 with removed_at kept", got)
			}
			if got := scalarStr(t, raw, "SELECT fingerprint FROM standup_config_history WHERE valid_to IS NULL"); got != "fp-b" {
				t.Errorf("current config history = %q, want fp-b", got)
			}

			// The pre-migration backup holds the same data at the old version.
			if want := filepath.Join(dir, fmt.Sprintf("cuisle.db.bak-v%d", n)); backup != want {
				t.Fatalf("backup = %q, want %q", backup, want)
			}
			bak := rawDB(t, backup)
			if got := rowCounts(t, bak); !reflect.DeepEqual(got, before) {
				t.Errorf("backup row counts differ from the pre-migration DB:\n got %v\nwant %v", got, before)
			}
		})
	}
}

// 0004 adds nullable title and branch to dev_pr_event without touching a row, and forgets the dev fingerprints so
// the next sync fills them in.
func TestMigrate_0004_DevBranchTitle_KeepsRowsAndResetsFingerprints(t *testing.T) {
	defer recoverStub(t)
	head := embeddedList(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, head[:3], dir)
	seedFile(t, raw, filepath.Join("testdata", "v3.sql"))
	mustExec(t, raw, "UPDATE issue SET dev_fingerprint = 'fp-dev', dev_checked_at = 3500 WHERE id = 1")
	before := rowCounts(t, raw)

	applied, _, err := migrate(t, raw, head[:4], dir, store.Options{})
	if err != nil || !reflect.DeepEqual(applied, []int{4}) {
		t.Fatalf("Migrate = %v, %v; want [4], nil", applied, err)
	}
	if got := rowCounts(t, raw); !reflect.DeepEqual(got, before) {
		t.Errorf("row counts changed by 0004:\n got %v\nwant %v", got, before)
	}
	for _, c := range []string{"title", "branch"} {
		if !hasColumn(t, raw, "dev_pr_event", c) {
			t.Errorf("dev_pr_event.%s missing", c)
		}
		if n := scalarInt(t, raw, "SELECT count(*) FROM dev_pr_event WHERE "+c+" IS NOT NULL"); n != 0 {
			t.Errorf("%d existing PR rows have a non-NULL %s, want 0", n, c)
		}
	}
	if got := scalarStr(t, raw, "SELECT pr_key || '/' || status || '/' || at FROM dev_pr_event ORDER BY at LIMIT 1"); got != "svc#12/OPEN/1770" {
		t.Errorf("first PR event = %q, want svc#12/OPEN/1770 (kept)", got)
	}
	if n := scalarInt(t, raw, "SELECT count(*) FROM issue WHERE dev_fingerprint IS NOT NULL"); n != 0 {
		t.Errorf("%d issues keep a dev fingerprint, want 0 (re-read once)", n)
	}
	if n := scalarInt(t, raw, "SELECT dev_checked_at FROM issue WHERE id = 1"); n != 3500 {
		t.Errorf("dev_checked_at = %d, want 3500 (untouched)", n)
	}
	assertIntegrity(t, raw)
}

// 0006 rebuilds standup_lane, standup_column_map and standup_status_lane to widen their lane CHECKs (custom lanes).
// Every row and value is kept (lane_order included), the foreign key from status lanes to the column map still
// cascades, custom lane ids are accepted only in their c<N> form, and the new settings columns start at their defaults.
func TestMigrate_0006_CustomLanes_KeepsConfigRowsAndWidensLaneChecks(t *testing.T) {
	defer recoverStub(t)
	head := embeddedList(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, head[:4], dir)
	seedFile(t, raw, filepath.Join("testdata", "v4.sql"))
	before := rowCounts(t, raw)
	const lanesQ = "SELECT group_concat(lane || ':' || walk_position || ':' || limit_days, ',') FROM (SELECT * FROM standup_lane ORDER BY standup_id, walk_position)"
	const colsQ = "SELECT group_concat(column_name || ':' || position || ':' || lane || ':' || auto || ':' || COALESCE(lane_order, -1), ',') FROM standup_column_map"
	const statusQ = "SELECT group_concat(status_id || ':' || status_name || ':' || column_name || ':' || lane, ',') FROM standup_status_lane"
	wantLanes, wantCols, wantStatus := scalarStr(t, raw, lanesQ), scalarStr(t, raw, colsQ), scalarStr(t, raw, statusQ)

	applied, _, err := migrate(t, raw, head[:6], dir, store.Options{})
	if err != nil || !reflect.DeepEqual(applied, []int{5, 6}) {
		t.Fatalf("Migrate = %v, %v; want [5 6], nil (0005 is the board-grouping migration)", applied, err)
	}
	if got := rowCounts(t, raw); !reflect.DeepEqual(got, before) {
		t.Errorf("row counts changed by 0005–0006:\n got %v\nwant %v", got, before)
	}
	for q, want := range map[string]string{lanesQ: wantLanes, colsQ: wantCols, statusQ: wantStatus} {
		if got := scalarStr(t, raw, q); got != want {
			t.Errorf("%s\n got %q\nwant %q", q, got, want)
		}
	}
	if n := scalarInt(t, raw, "SELECT count(*) FROM standup_lane WHERE label IS NOT NULL OR hue IS NOT NULL"); n != 0 {
		t.Errorf("%d lanes have a label or hue after 0006, want 0 (defaults)", n)
	}
	if got := scalarStr(t, raw, "SELECT lane_seq || ':' || COALESCE(done_label, '-') FROM standup_settings WHERE standup_id = 1"); got != "0:-" {
		t.Errorf("settings lane_seq:done_label = %q, want 0:-", got)
	}
	assertIntegrity(t, raw)

	// Custom lanes fit; malformed ids, a custom lane without a label or hue, and a label on nothing else are refused.
	mustExec(t, raw, "INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days, label, hue) VALUES (1, 'c12', 4, 0, 'Design', 3)")
	mustExec(t, raw, "INSERT INTO standup_column_map (standup_id, column_name, position, lane) VALUES (1, 'Design', 5, 'c12')")
	for _, bad := range []string{
		"INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days, label, hue) VALUES (1, 'c0', 5, 0, 'X', 1)",
		"INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days, label, hue) VALUES (1, 'c1a', 5, 0, 'X', 1)",
		"INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days, label, hue) VALUES (1, 'done', 5, 0, 'X', NULL)",
		"INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days, label, hue) VALUES (1, 'c3', 5, 0, NULL, 1)",
		"INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days, label, hue) VALUES (1, 'c4', 5, 0, 'X', NULL)",
		"INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days, label, hue) VALUES (1, 'c5', 5, 0, 'X', 9)",
		"UPDATE standup_lane SET hue = 1 WHERE standup_id = 1 AND lane = 'qa'",
		"INSERT INTO standup_column_map (standup_id, column_name, position, lane) VALUES (1, 'Bad', 6, 'custom')",
	} {
		if _, err := raw.ExecContext(ctx, bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
	// The status-lane foreign key to the rebuilt column map still cascades on update.
	mustExec(t, raw, "PRAGMA foreign_keys=ON")
	mustExec(t, raw, "UPDATE standup_column_map SET column_name = 'Doing' WHERE standup_id = 1 AND column_name = 'In Progress'")
	if got := scalarStr(t, raw, "SELECT column_name FROM standup_status_lane WHERE status_id = '10'"); got != "Doing" {
		t.Errorf("status lane column after rename = %q, want Doing (ON UPDATE CASCADE)", got)
	}
}

// 0005 adds the per-standup Whole-board grouping to standup_ui_state: existing rows default to 'assignee' (today's
// board), nothing is dropped, and the CHECK rejects any other value.
func TestMigrate_0005_BoardGroup_ExistingRowsDefaultToAssignee(t *testing.T) {
	defer recoverStub(t)
	head := embeddedList(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, head[:4], dir)
	seedFile(t, raw, filepath.Join("testdata", "v4.sql"))
	before := rowCounts(t, raw)
	if before["standup_ui_state"] == 0 {
		t.Fatal("fixture drift: v4.sql must seed a standup_ui_state row")
	}

	applied, _, err := migrate(t, raw, head[:5], dir, store.Options{})
	if err != nil || !reflect.DeepEqual(applied, []int{5}) {
		t.Fatalf("Migrate = %v, %v; want [5], nil", applied, err)
	}
	if got := rowCounts(t, raw); !reflect.DeepEqual(got, before) {
		t.Errorf("row counts changed by 0005:\n got %v\nwant %v", got, before)
	}
	if !hasColumn(t, raw, "standup_ui_state", "board_group") {
		t.Fatal("standup_ui_state.board_group missing")
	}
	if n := scalarInt(t, raw, "SELECT count(*) FROM standup_ui_state WHERE board_group = 'assignee'"); int(n) != before["standup_ui_state"] {
		t.Errorf("%d rows default to assignee, want all %d", n, before["standup_ui_state"])
	}
	if got := scalarStr(t, raw, "SELECT last_person FROM standup_ui_state WHERE standup_id = 1"); got != "acc-ada" {
		t.Errorf("last_person = %q, want acc-ada (kept)", got)
	}
	if _, err := raw.Exec("UPDATE standup_ui_state SET board_group = 'columns' WHERE standup_id = 1"); err == nil {
		t.Error("board_group accepted 'columns', want the CHECK to reject it")
	}
	mustExec(t, raw, "UPDATE standup_ui_state SET board_group = 'status' WHERE standup_id = 1")
	assertIntegrity(t, raw)
}

// 0007 adds the per-standup zoom to standup_ui_state: existing rows default to 100 (today's screen), nothing is dropped,
// and the CHECK rejects a percentage that is not one of the nine steps.
func TestMigrate_0007_Zoom_ExistingRowsDefaultTo100(t *testing.T) {
	defer recoverStub(t)
	head := embeddedList(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, head[:4], dir)
	seedFile(t, raw, filepath.Join("testdata", "v4.sql"))
	mustMigrate(t, raw, head[:6], dir)
	before := rowCounts(t, raw)
	if before["standup_ui_state"] == 0 {
		t.Fatal("fixture drift: v4.sql must seed a standup_ui_state row")
	}

	applied, _, err := migrate(t, raw, head[:7], dir, store.Options{})
	if err != nil || !reflect.DeepEqual(applied, []int{7}) {
		t.Fatalf("Migrate = %v, %v; want [7], nil", applied, err)
	}
	if got := rowCounts(t, raw); !reflect.DeepEqual(got, before) {
		t.Errorf("row counts changed by 0007:\n got %v\nwant %v", got, before)
	}
	if !hasColumn(t, raw, "standup_ui_state", "zoom") {
		t.Fatal("standup_ui_state.zoom missing")
	}
	if n := scalarInt(t, raw, "SELECT count(*) FROM standup_ui_state WHERE zoom = 100"); int(n) != before["standup_ui_state"] {
		t.Errorf("%d rows default to 100, want all %d", n, before["standup_ui_state"])
	}
	if got := scalarStr(t, raw, "SELECT last_person FROM standup_ui_state WHERE standup_id = 1"); got != "acc-ada" {
		t.Errorf("last_person = %q, want acc-ada (kept)", got)
	}
	if _, err := raw.Exec("UPDATE standup_ui_state SET zoom = 105 WHERE standup_id = 1"); err == nil {
		t.Error("zoom accepted 105, want the CHECK to reject it")
	}
	mustExec(t, raw, "UPDATE standup_ui_state SET zoom = 150 WHERE standup_id = 1")
	assertIntegrity(t, raw)
}

// 0008 adds the issue generation: an empty table, three triggers. Nothing is dropped; the counter moves for an issue
// that is added, removed, or whose key, summary or status changes, and for nothing else.
func TestMigrate_0008_IssueGen_TriggersMoveTheCounterOnlyForRealIssueChanges(t *testing.T) {
	defer recoverStub(t)
	head := embeddedList(t)
	raw, dir := freshRaw(t)
	mustMigrate(t, raw, head[:4], dir)
	seedFile(t, raw, filepath.Join("testdata", "v4.sql"))
	mustMigrate(t, raw, head[:7], dir)
	before := rowCounts(t, raw)

	applied, _, err := migrate(t, raw, head[:8], dir, store.Options{})
	if err != nil || !reflect.DeepEqual(applied, []int{8}) {
		t.Fatalf("Migrate = %v, %v; want [8], nil", applied, err)
	}
	after := rowCounts(t, raw)
	if n := after["issue_gen"]; n != 0 {
		t.Errorf("issue_gen has %d rows after the migration, want 0 (it starts empty)", n)
	}
	delete(after, "issue_gen")
	if !reflect.DeepEqual(after, before) {
		t.Errorf("row counts changed by 0008:\n got %v\nwant %v", after, before)
	}
	gen := func() int64 { return scalarInt(t, raw, "SELECT coalesce(max(n), 0) FROM issue_gen") }
	if gen() != 0 {
		t.Fatalf("generation = %d, want 0", gen())
	}
	step := func(what, sql string, wantMove bool) {
		t.Helper()
		was := gen()
		mustExec(t, raw, sql)
		if moved := gen() != was; moved != wantMove {
			t.Errorf("%s: generation %d -> %d, moved = %v, want %v", what, was, gen(), moved, wantMove)
		}
	}
	step("summary changes", "UPDATE issue SET summary = summary || '!' WHERE id = 1", true)
	step("same summary written again", "UPDATE issue SET summary = summary WHERE id = 1", false)
	step("status changes", "UPDATE issue SET status_id = status_id || 'x' WHERE id = 1", true)
	step("key changes", "UPDATE issue SET key = key || 'x' WHERE id = 1", true)
	step("last_seen_at and removed_at", "UPDATE issue SET last_seen_at = 99, removed_at = 98 WHERE id = 1", false)
	step("a settings write", "UPDATE ui_state SET last_standup_id = last_standup_id", false)
	assertIntegrity(t, raw)
}

// ---- LoadMigrations -------------------------------------------------------------------------------------------

func mapFS(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func TestLoadMigrations_SortsAndParsesVersionNameSQLAndChecksum(t *testing.T) {
	defer recoverStub(t)
	files := map[string]string{
		"0003_add_c.sql": "SELECT 3;\n",
		"0001_init.sql":  "SELECT 1;\n",
		"0002_add_b.sql": "SELECT 2;\n",
		"README.md":      "not a migration",
		"notes.txt":      "ignored",
	}
	got, err := store.LoadMigrations(mapFS(files))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d migrations, want 3 (non-matching files ignored): %+v", len(got), got)
	}
	want := []struct {
		v    int
		name string
		sql  string
	}{{1, "init", "SELECT 1;\n"}, {2, "add_b", "SELECT 2;\n"}, {3, "add_c", "SELECT 3;\n"}}
	for i, w := range want {
		m := got[i]
		if m.Version != w.v || m.Name != w.name || m.SQL != w.sql {
			t.Errorf("[%d] = {%d %q %q}, want {%d %q %q}", i, m.Version, m.Name, m.SQL, w.v, w.name, w.sql)
		}
		if m.Checksum != sha([]byte(w.sql)) {
			t.Errorf("[%d] checksum = %q, want sha256 hex %q", i, m.Checksum, sha([]byte(w.sql)))
		}
		if m.FKOff {
			t.Errorf("[%d] FKOff = true without the marker", i)
		}
	}
}

func TestLoadMigrations_TenFilesStaySortedNumerically(t *testing.T) {
	defer recoverStub(t)
	files := map[string]string{}
	for i := 1; i <= 10; i++ {
		files[fmt.Sprintf("%04d_step_%d.sql", i, i)] = fmt.Sprintf("SELECT %d;", i)
	}
	got, err := store.LoadMigrations(mapFS(files))
	if err != nil || len(got) != 10 {
		t.Fatalf("LoadMigrations = %d items, %v", len(got), err)
	}
	for i, m := range got {
		if m.Version != i+1 {
			t.Errorf("[%d].Version = %d", i, m.Version)
		}
	}
}

func TestLoadMigrations_FKOffMarkerMustBeTheFirstLine(t *testing.T) {
	defer recoverStub(t)
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"first line", "-- cuisle:fk-off\nCREATE TABLE x(a);\n", true},
		{"marker only, no newline", "-- cuisle:fk-off", true},
		{"second line", "-- note\n-- cuisle:fk-off\nSELECT 1;\n", false},
		{"after a blank line", "\n-- cuisle:fk-off\nSELECT 1;\n", false},
		{"after SQL", "SELECT 1;\n-- cuisle:fk-off\n", false},
		{"absent", "SELECT 1;\n", false},
	}
	for _, c := range cases {
		got, err := store.LoadMigrations(mapFS(map[string]string{"0001_x.sql": c.sql}))
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: LoadMigrations = %v, %v", c.name, got, err)
		}
		if got[0].FKOff != c.want {
			t.Errorf("%s: FKOff = %v, want %v", c.name, got[0].FKOff, c.want)
		}
	}
}

func TestLoadMigrations_RejectsBadNumbering(t *testing.T) {
	defer recoverStub(t)
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"gap", map[string]string{"0001_a.sql": "x", "0003_c.sql": "x"}},
		{"gap in the middle of three", map[string]string{"0001_a.sql": "x", "0002_b.sql": "x", "0004_d.sql": "x"}},
		{"does not start at 1", map[string]string{"0002_b.sql": "x"}},
		{"duplicate version", map[string]string{"0001_a.sql": "x", "0001_b.sql": "x"}},
	}
	for _, c := range cases {
		if got, err := store.LoadMigrations(mapFS(c.files)); err == nil {
			t.Errorf("%s: no error, got %+v", c.name, got)
		}
	}
}

func TestLoadMigrations_EmbeddedSet(t *testing.T) {
	defer recoverStub(t)
	sub, err := fs.Sub(store.Migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadMigrations(sub)
	if err != nil {
		t.Fatal(err)
	}
	want := embeddedList(t)
	if len(got) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("embedded migrations = %+v\nwant %+v", got, want)
	}
	if got[0].Version != 1 || got[0].Name != "init" || got[0].FKOff {
		t.Errorf("first migration = %+v, want version 1, name init, no fk-off", got[0])
	}
	if got[0].Checksum != head1(t).Checksum || got[0].SQL != head1(t).SQL {
		t.Error("0001 checksum/SQL do not match the embedded file bytes")
	}
}

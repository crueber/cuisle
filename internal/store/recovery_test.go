package store_test

// Tests for corruption recovery in store.Open (architecture.md §6.3).
//
// SPEC-NOTE: "corrupt" includes a file SQLite cannot even open ("file is not a database": the first statement fails
// before PRAGMA quick_check can answer), not only a quick_check result other than "ok". Verified against modernc:
// an 8 KB garbage file errors on the first statement.
// SPEC-NOTE: the renamed file is "<path>.corrupt-<unixms>" where unixms comes from Options.Now (store.Ms(now)),
// so tests can assert the exact name with an injected clock.
// SPEC-NOTE: "newest .bak" is the most recently modified file matching <path>.bak-* (bak-v<N> and bak-weekly alike).
// The tests make mtime order, version order and name order agree wherever more than one candidate exists, so any
// of those readings passes. *.corrupt-* files are never restore candidates.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
)

func garbage(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 3)
	}
	return b
}

// makeBackup opens a throwaway real DB, stores theme=<theme> and writes a VACUUM INTO copy at dst.
func makeBackup(t *testing.T, dst, theme string, mtime time.Time) {
	t.Helper()
	src, _ := openTemp(t)
	mustExec(t, src.W, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme', ?, 1)", theme)
	vacuumCopy(t, src.W, dst)
	if err := os.Chtimes(dst, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func corruptDB(t *testing.T, dir string) (path string, junk []byte) {
	t.Helper()
	path = filepath.Join(dir, "cuisle.db")
	junk = garbage(8192)
	if err := os.WriteFile(path, junk, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, junk
}

func TestOpen_CorruptFile_NoBackup_StartsEmptyAndKeepsTheCorruptFile(t *testing.T) {
	defer recoverStub(t)
	dir := t.TempDir()
	_, junk := corruptDB(t, dir)

	db, info := openAt(t, dir, store.Options{})
	if !info.Recovered {
		t.Fatal("OpenInfo.Recovered = false for a corrupt file")
	}
	if info.RestoredFrom != "" {
		t.Errorf("RestoredFrom = %q, want empty (no backup existed)", info.RestoredFrom)
	}
	if info.SchemaVersion != headVersion || !reflect.DeepEqual(info.Migrated, []int{1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Errorf("info = %+v; want an empty DB migrated to head", info)
	}
	corrupt := filepath.Join(dir, "cuisle.db.corrupt-"+itoa(fixedNow.UnixMilli()))
	b, err := os.ReadFile(corrupt)
	if err != nil {
		t.Fatalf("the corrupt file must be kept as %s: %v", filepath.Base(corrupt), err)
	}
	if !bytes.Equal(b, junk) {
		t.Error("the renamed file's bytes changed")
	}
	if got := tableNames(t, db.W); !reflect.DeepEqual(got, tablesOf0001) {
		t.Errorf("recovered DB tables = %v", got)
	}
	if n := countRows(t, db.W, "app_setting"); n != 0 {
		t.Errorf("app_setting rows = %d, want 0 (empty start)", n)
	}
	// And it is a normal working database afterwards.
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	db2, info2 := openAt(t, dir, store.Options{})
	if info2.Recovered || info2.SchemaVersion != headVersion {
		t.Errorf("reopen after recovery: %+v; want a healthy DB at head", info2)
	}
	_ = db2
}

func TestOpen_CorruptFile_RestoresFromTheBackup(t *testing.T) {
	defer recoverStub(t)
	dir := t.TempDir()
	bak := filepath.Join(dir, "cuisle.db.bak-v1")
	makeBackup(t, bak, "dark", fixedNow.Add(-time.Hour))
	corruptDB(t, dir)

	db, info := openAt(t, dir, store.Options{})
	if !info.Recovered || info.RestoredFrom != bak {
		t.Errorf("info = %+v; want Recovered with RestoredFrom %q", info, bak)
	}
	if info.SchemaVersion != headVersion {
		t.Errorf("SchemaVersion = %d, want %d", info.SchemaVersion, headVersion)
	}
	if got := scalarStr(t, db.R, "SELECT value FROM app_setting WHERE key='theme'"); got != "dark" {
		t.Errorf("theme after restore = %q, want dark (from the backup)", got)
	}
	if !fileExists(filepath.Join(dir, "cuisle.db.corrupt-"+itoa(fixedNow.UnixMilli()))) {
		t.Error("corrupt file was not kept")
	}
	if !fileExists(bak) {
		t.Error("the backup used for restoring must stay in place")
	}
}

func TestOpen_CorruptFile_RestoresFromTheNewestBackup(t *testing.T) {
	defer recoverStub(t)
	dir := t.TempDir()
	// bak-v1 < bak-weekly in mtime, and "bak-v1" < "bak-weekly" by name: every reading of "newest" agrees.
	makeBackup(t, filepath.Join(dir, "cuisle.db.bak-v1"), "old", fixedNow.Add(-48*time.Hour))
	weekly := filepath.Join(dir, "cuisle.db.bak-weekly")
	makeBackup(t, weekly, "new", fixedNow.Add(-time.Hour))
	corruptDB(t, dir)

	db, info := openAt(t, dir, store.Options{})
	if info.RestoredFrom != weekly {
		t.Errorf("RestoredFrom = %q, want %q", info.RestoredFrom, weekly)
	}
	if got := scalarStr(t, db.R, "SELECT value FROM app_setting WHERE key='theme'"); got != "new" {
		t.Errorf("theme = %q, want new", got)
	}
}

func TestOpen_CorruptFile_PriorCorruptFilesAreNotBackups(t *testing.T) {
	defer recoverStub(t)
	dir := t.TempDir()
	older := filepath.Join(dir, "cuisle.db.corrupt-1700000000000")
	if err := os.WriteFile(older, garbage(4096), 0o644); err != nil {
		t.Fatal(err)
	}
	corruptDB(t, dir)

	_, info := openAt(t, dir, store.Options{})
	if !info.Recovered || info.RestoredFrom != "" {
		t.Errorf("info = %+v; a previous *.corrupt-* file must not be used as a restore source", info)
	}
	if !fileExists(older) {
		t.Error("the earlier corrupt file was removed")
	}
}

func TestOpen_HealthyFile_IsNotTreatedAsCorrupt(t *testing.T) {
	defer recoverStub(t)
	dir := t.TempDir()
	db, _ := openAt(t, dir, store.Options{})
	mustExec(t, db.W, insertTheme)
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// A backup lying around must not tempt Open into restoring over a healthy file.
	makeBackup(t, filepath.Join(dir, "cuisle.db.bak-v1"), "stale", fixedNow)

	db2, info := openAt(t, dir, store.Options{})
	if info.Recovered || info.RestoredFrom != "" {
		t.Errorf("info = %+v, want a normal open", info)
	}
	if got := scalarStr(t, db2.R, "SELECT value FROM app_setting WHERE key='theme'"); got != "dark" {
		t.Errorf("theme = %q, want dark (the live file)", got)
	}
	if m := glob(t, filepath.Join(dir, "cuisle.db.corrupt-*")); len(m) != 0 {
		t.Errorf("healthy file renamed: %v", m)
	}
}

package store_test

// WP-38 review fixes: backup hardening, WriteTx rollback on panic, and a data folder whose name has URI characters.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
)

func TestOpen_CorruptFile_SkipsTmpAndCorruptBackupsAndFallsBack(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "cuisle.db.bak-v1")
	makeBackup(t, good, "good", fixedNow.Add(-48*time.Hour))

	// A newer backup that is damaged, and the newest file of all: a half-written snapshot.
	bad := filepath.Join(dir, "cuisle.db.bak-weekly")
	if err := os.WriteFile(bad, garbage(8192), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bad, fixedNow.Add(-time.Hour), fixedNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "cuisle.db.bak-v2.tmp")
	makeBackup(t, tmp, "tmp", fixedNow.Add(-time.Minute))
	corruptDB(t, dir)

	db, info := openAt(t, dir, store.Options{})
	if info.RestoredFrom != good {
		t.Errorf("RestoredFrom = %q, want the healthy older backup %q", info.RestoredFrom, good)
	}
	if got := scalarStr(t, db.R, "SELECT value FROM app_setting WHERE key='theme'"); got != "good" {
		t.Errorf("theme = %q, want good", got)
	}
}

func TestOpen_CorruptFile_EveryBackupDamaged_StartsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cuisle.db.bak-v1"), garbage(4096), 0o644); err != nil {
		t.Fatal(err)
	}
	corruptDB(t, dir)
	_, info := openAt(t, dir, store.Options{})
	if !info.Recovered || info.RestoredFrom != "" {
		t.Errorf("info = %+v, want Recovered with nothing restored", info)
	}
}

func TestWriteTx_PanicRollsBackAndReleasesTheWriter(t *testing.T) {
	db, _ := openTemp(t)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not reach the caller")
			}
		}()
		_ = db.WriteTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)"); err != nil {
				t.Errorf("insert: %v", err)
			}
			panic("boom")
		})
	}()

	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := db.WriteTx(wctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(wctx, "INSERT INTO app_setting(key,value,updated_at) VALUES ('density','tight',1)")
		return err
	})
	if err != nil {
		t.Fatalf("WriteTx after a panic: %v (writer pinned?)", err)
	}
	if n := countRows(t, db.R, "app_setting"); n != 1 {
		t.Errorf("app_setting rows = %d, want 1 (the panicking transaction rolled back)", n)
	}
}

func TestOpen_DataFolderWithURICharacters(t *testing.T) {
	name := "my data #1 100% ready?"
	if runtime.GOOS == "windows" {
		name = "my data #1 100% ready" // '?' is not allowed in Windows file names
	}
	dir := filepath.Join(t.TempDir(), name)
	db, _ := openAt(t, dir, store.Options{})
	mustExec(t, db.W, "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)")
	if err := db.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !fileExists(filepath.Join(dir, "cuisle.db")) {
		t.Fatalf("the database file is not at the requested path; folder holds %v", glob(t, filepath.Join(dir, "*")))
	}
	db2, info := openAt(t, dir, store.Options{})
	if info.Recovered {
		t.Error("reopen reported recovery")
	}
	if got := scalarStr(t, db2.R, "SELECT value FROM app_setting WHERE key='theme'"); got != "dark" {
		t.Errorf("theme after reopen = %q, want dark", got)
	}
}

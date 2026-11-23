package store

// WP-38B: newestBackup returns context errors, skips empty files, and leaves no -wal/-shm sidecars behind.

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
)

// writeWALDB creates a small healthy database in WAL mode (as the app's own files are), closed cleanly.
func writeWALDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", fileDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE t (x INTEGER)", "INSERT INTO t VALUES (1)"} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// A clean close checkpoints and removes the sidecars; make sure the fixture starts without them.
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}

func sidecars(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, pat := range []string{"*-wal", "*-shm"} {
		m, err := filepath.Glob(filepath.Join(dir, pat))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m...)
	}
	return out
}

func TestNewestBackup_SkipsEmptyFilesAndLeavesNoSidecars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cuisle.db")
	good := filepath.Join(dir, "cuisle.db.bak-v1")
	writeWALDB(t, good)
	if err := os.Chtimes(good, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "cuisle.db.bak-weekly")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := newestBackup(context.Background(), path)
	if err != nil {
		t.Fatalf("newestBackup: %v", err)
	}
	if got != good {
		t.Errorf("newestBackup = %q, want the non-empty %q", got, good)
	}
	if fi, err := os.Stat(empty); err != nil || fi.Size() != 0 {
		t.Errorf("the empty file was touched: %v, %v", fi, err)
	}
	if sc := sidecars(t, dir); len(sc) != 0 {
		t.Errorf("checking the backups left sidecars: %v", sc)
	}
}

func TestNewestBackup_OnlyEmptyFiles_IsNone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cuisle.db.bak-v1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := newestBackup(context.Background(), filepath.Join(dir, "cuisle.db"))
	if err != nil || got != "" {
		t.Errorf("newestBackup = %q, %v; want none", got, err)
	}
}

func TestNewestBackup_CancelledContextIsAnErrorNotASkip(t *testing.T) {
	dir := t.TempDir()
	writeWALDB(t, filepath.Join(dir, "cuisle.db.bak-v1"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := newestBackup(ctx, filepath.Join(dir, "cuisle.db"))
	if err == nil {
		t.Fatalf("newestBackup = %q with a cancelled context, want an error", got)
	}
	if !errors.Is(err, context.Canceled) && !apperr.Is(err, apperr.Cancelled) {
		t.Errorf("error = %v, want cancellation", err)
	}
}

func TestNewestBackup_UnreadableFolderIsAnError(t *testing.T) {
	_, err := newestBackup(context.Background(), filepath.Join(t.TempDir(), "missing", "cuisle.db"))
	if !apperr.Is(err, apperr.DB) {
		t.Errorf("error = %v, want a db error", err)
	}
}

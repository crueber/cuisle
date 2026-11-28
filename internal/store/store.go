package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
)

// Open opens (creating if needed) the DB at path: pragmas and pools, PRAGMA quick_check (corruption → rename to
// <path>.corrupt-<unixms>, restore the newest <path>.bak-* if any, else start empty), then Migrate.
// A schema newer than the binary → apperr db "This data was created by a newer Cuisle" (DB not opened).
func Open(ctx context.Context, path string, opts Options) (*DB, OpenInfo, error) {
	var info OpenInfo
	log, now := opts.logger(), opts.clock()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, info, apperr.Wrap(apperr.DB, "The data folder could not be created.", err)
	}
	if err := checkOrRecover(ctx, path, log, now, &info); err != nil {
		return nil, info, err
	}

	w, err := sql.Open("sqlite", writerDSN(fileDSN(path)))
	if err != nil {
		return nil, info, apperr.Wrap(apperr.DB, "The database could not be opened.", err)
	}
	w.SetMaxOpenConns(1)
	list, err := LoadMigrations(mustSub())
	if err != nil {
		_ = w.Close()
		return nil, info, err
	}
	applied, backup, err := Migrate(ctx, w, list, dir, opts)
	if err != nil {
		_ = w.Close()
		return nil, info, err
	}
	info.Migrated = applied
	info.BackupPath = backup

	r, err := sql.Open("sqlite", readerDSN(fileDSN(path)))
	if err != nil {
		_ = w.Close()
		return nil, info, apperr.Wrap(apperr.DB, "The database could not be opened.", err)
	}
	r.SetMaxOpenConns(4)

	info.SchemaVersion, err = SchemaVersion(ctx, w)
	if err != nil {
		_ = closePools(r, w)
		return nil, info, err
	}
	return &DB{W: w, R: r, Path: path, Log: log, Now: now}, info, nil
}

// OpenMemory opens a private, migrated in-memory DB (shared-cache name unique per call). For tests and benches.
func OpenMemory(ctx context.Context, opts Options) (*DB, error) {
	name := fmt.Sprintf("file:cuisle-mem-%d?mode=memory&cache=shared", memSeq.Add(1))
	w, err := sql.Open("sqlite", writerDSN(name))
	if err != nil {
		return nil, apperr.Wrap(apperr.DB, "The database could not be opened.", err)
	}
	w.SetMaxOpenConns(1)
	list, err := LoadMigrations(mustSub())
	if err != nil {
		_ = w.Close()
		return nil, err
	}
	if _, _, err := Migrate(ctx, w, list, "", opts); err != nil {
		_ = w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", readerDSN(name))
	if err != nil {
		_ = w.Close()
		return nil, apperr.Wrap(apperr.DB, "The database could not be opened.", err)
	}
	r.SetMaxOpenConns(4)
	return &DB{W: w, R: r, Path: "", Log: opts.logger(), Now: opts.clock()}, nil
}

var memSeq atomic.Int64

// Close runs PRAGMA optimize and wal_checkpoint(TRUNCATE), then closes both pools.
func (db *DB) Close(ctx context.Context) error {
	var errs []error
	if db.Path != "" {
		if _, err := db.W.ExecContext(ctx, "PRAGMA optimize"); err != nil {
			errs = append(errs, err)
		}
		if _, err := db.W.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, closeErr(db.R), closeErr(db.W))
	return errors.Join(errs...)
}

// WriteTx runs fn in BEGIN IMMEDIATE on W; commits when fn returns nil, rolls back otherwise.
// Errors that are not *apperr.Error are wrapped as apperr db (retryable).
func (db *DB) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.W.BeginTx(ctx, nil)
	if err != nil {
		return txErr(err, "The data could not be saved.")
	}
	// A panic in fn must not leave the single writer connection inside an open transaction: roll back, then let
	// the panic continue to the caller's recover.
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		done = true
		_ = tx.Rollback()
		return txErr(err, "The data could not be saved.")
	}
	done = true
	if err := tx.Commit(); err != nil {
		return txErr(err, "The data could not be saved.")
	}
	return nil
}

// ReadTx runs fn in a deferred read transaction on R (one WAL snapshot for all queries inside).
func (db *DB) ReadTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.R.BeginTx(ctx, nil)
	if err != nil {
		return txErr(err, "The data could not be read.")
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return txErr(err, "The data could not be read.")
	}
	return nil
}

// Ms converts a time to Unix ms UTC (0 for the zero time). FromMs is the inverse (0 → zero time).
func Ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func FromMs(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// ---- helpers --------------------------------------------------------------------------------------------------

func (o Options) clock() func() time.Time {
	if o.Now == nil {
		return time.Now
	}
	return o.Now
}

func (o Options) logger() *slog.Logger {
	if o.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return o.Logger
}

func mustSub() fs.FS {
	sub, err := fs.Sub(Migrations, "migrations")
	if err != nil {
		panic(err) // the embed root always contains migrations/
	}
	return sub
}

// fileDSN is the SQLite URI for a file path. SQLite decodes %XX in URIs and the driver splits at the first '?', so the
// characters that would otherwise end the path ('%', '?', '#') are escaped.
func fileDSN(path string) string {
	return "file:" + strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
}

// dsnWith appends query parameters to a file or memory DSN.
func dsnWith(base string, params []string) string {
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + strings.Join(params, "&")
}

// writerDSN: every pooled connection gets the pragmas. auto_vacuum is first so it applies before the first table.
func writerDSN(base string) string {
	params := []string{
		"_pragma=auto_vacuum(INCREMENTAL)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(1)",
		"_pragma=busy_timeout(5000)",
		"_pragma=temp_store(MEMORY)",
		"_txlock=immediate",
	}
	return dsnWith(base, params)
}

func readerDSN(base string) string {
	return dsnWith(base, []string{
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=foreign_keys(1)",
		"_pragma=busy_timeout(5000)",
		"_pragma=temp_store(MEMORY)",
		"_pragma=query_only(1)",
	})
}

func closeErr(db *sql.DB) error {
	if err := db.Close(); err != nil {
		return err
	}
	return nil
}

func closePools(r, w *sql.DB) error {
	return errors.Join(closeErr(r), closeErr(w))
}

// txErr keeps *apperr.Error values unchanged and wraps everything else as a retryable db error.
func txErr(err error, msg string) error {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return apperr.Wrap(apperr.Cancelled, "Cancelled.", err)
	}
	return apperr.Wrap(apperr.DB, msg, err)
}

// isNotADatabase reports the SQLite errors that mean the file is damaged (not merely busy or unreadable).
func isNotADatabase(err error) bool {
	s := err.Error()
	return strings.Contains(s, "file is not a database") || strings.Contains(s, "database disk image is malformed")
}

// quickCheck reports whether the file at path is damaged. A missing file is healthy and left untouched.
func quickCheck(ctx context.Context, path string) (corrupt bool, err error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return quickCheckDSN(ctx, fileDSN(path))
}

// quickCheckReadOnly is quickCheck for a file that must not be touched (a backup candidate): mode=ro&immutable=1
// opens it without locks and without creating -wal or -shm sidecars. A missing file is an error, not an empty database.
func quickCheckReadOnly(ctx context.Context, path string) (corrupt bool, err error) {
	return quickCheckDSN(ctx, dsnWith(fileDSN(path), []string{"mode=ro", "immutable=1"}))
}

func quickCheckDSN(ctx context.Context, dsn string) (corrupt bool, err error) {
	probe, err := sql.Open("sqlite", dsn)
	if err != nil {
		return false, apperr.Wrap(apperr.DB, "The database could not be opened.", err)
	}
	defer func() { _ = probe.Close() }()
	probe.SetMaxOpenConns(1)
	var res string
	if err := probe.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&res); err != nil {
		if isNotADatabase(err) {
			return true, nil
		}
		return false, apperr.Wrap(apperr.DB, "The database could not be checked.", err)
	}
	return res != "ok", nil
}

// checkOrRecover runs quick_check; on corruption it renames the file (and its -wal) to <path>.corrupt-<ms>, then
// restores the newest <path>.bak-* if there is one.
func checkOrRecover(ctx context.Context, path string, log *slog.Logger, now func() time.Time, info *OpenInfo) error {
	corrupt, err := quickCheck(ctx, path)
	if err != nil {
		return err
	}
	if !corrupt {
		return nil
	}
	corruptPath := fmt.Sprintf("%s.corrupt-%d", path, Ms(now()))
	if err := os.Rename(path, corruptPath); err != nil {
		return apperr.Wrap(apperr.DB, "The damaged database could not be set aside.", err)
	}
	if _, err := os.Stat(path + "-wal"); err == nil {
		if err := os.Rename(path+"-wal", corruptPath+"-wal"); err != nil {
			return apperr.Wrap(apperr.DB, "The damaged database could not be set aside.", err)
		}
	}
	_ = os.Remove(path + "-shm")
	info.Recovered = true
	log.Warn("database is damaged; kept the damaged file and recovering", "kept", corruptPath)

	bak, err := newestBackup(ctx, path)
	if err != nil {
		return err
	}
	if bak == "" {
		log.Warn("no backup found; starting with an empty database")
		return nil
	}
	tmp := path + ".restore-tmp"
	if err := copyFileSync(bak, tmp); err != nil {
		_ = os.Remove(tmp)
		return apperr.Wrap(apperr.DB, "The backup could not be restored.", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return apperr.Wrap(apperr.DB, "The backup could not be restored.", err)
	}
	info.RestoredFrom = bak
	log.Warn("restored the database from a backup", "backup", bak)
	return nil
}

func copyFileSync(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// newestBackup returns the most recently modified healthy <base>.bak-* file next to path ("" if none). Half-written
// *.tmp files and empty files are ignored, and a backup that fails quick_check falls back to the next newest. A
// cancelled context or an I/O error is returned (not skipped, which could silently restore an older backup).
// Candidates are opened read-only so checking them leaves no sidecar files behind.
func newestBackup(ctx context.Context, path string) (string, error) {
	dir, base := filepath.Dir(path), filepath.Base(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", apperr.Wrap(apperr.DB, "The backup folder could not be read.", err)
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var cands []candidate
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, base+".bak-") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		fi, err := e.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue // removed since the listing
		}
		if err != nil {
			return "", apperr.Wrap(apperr.DB, "A backup could not be read.", err)
		}
		if fi.Size() == 0 {
			continue
		}
		cands = append(cands, candidate{filepath.Join(dir, name), fi.ModTime()})
	}
	sort.Slice(cands, func(i, j int) bool {
		if !cands[i].mod.Equal(cands[j].mod) {
			return cands[i].mod.After(cands[j].mod)
		}
		return cands[i].path > cands[j].path
	})
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return "", txErr(err, "Checking the backups was interrupted.")
		}
		corrupt, err := quickCheckReadOnly(ctx, c.path)
		if err != nil {
			return "", err
		}
		if corrupt {
			continue
		}
		return c.path, nil
	}
	return "", nil
}

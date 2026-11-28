package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
)

const fkOffMarker = "-- cuisle:fk-off"

const schemaMigrationDDL = `CREATE TABLE IF NOT EXISTS schema_migration (
  version    INTEGER PRIMARY KEY,
  name       TEXT    NOT NULL,
  checksum   TEXT    NOT NULL,
  applied_at INTEGER NOT NULL
) STRICT`

var migrationFileRE = regexp.MustCompile(`^(\d{4})_(.+)\.sql$`)

// LoadMigrations parses the files NNNN_name.sql in fsys (sorted, contiguous from 1; a gap is an error).
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, apperr.Wrap(apperr.Internal, "The database schema could not be read.", err)
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFileRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		b, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, apperr.Wrap(apperr.Internal, "The database schema could not be read.", err)
		}
		s := string(b)
		out = append(out, Migration{
			Version:  v,
			Name:     m[2],
			SQL:      s,
			Checksum: sha256Hex(b),
			FKOff:    isFKOff(s),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, apperr.Wrap(apperr.Internal, "The database schema is misnumbered.",
				fmt.Errorf("migration %04d_%s is at position %d; numbering must be contiguous from 0001", m.Version, m.Name, i+1))
		}
	}
	return out, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// isFKOff reports whether the marker is the first line of the file.
func isFKOff(s string) bool {
	return s == fkOffMarker || strings.HasPrefix(s, fkOffMarker+"\n")
}

// Migrate applies pending migrations from list on w (data-model.md §3): creates schema_migration; refuses a newer DB;
// checksum mismatch → warn (dev: error); when anything is pending and the DB is non-empty and backupDir != "",
// VACUUM INTO <backupDir>/cuisle.db.bak-v<max applied> keeping the 2 newest; each file in its own BEGIN IMMEDIATE with
// schema_migration row and PRAGMA user_version; FK-off files run with foreign_keys=OFF then foreign_key_check.
// Running it twice is a no-op. Returns the versions applied and the backup path.
func Migrate(ctx context.Context, w *sql.DB, list []Migration, backupDir string, opts Options) (applied []int, backup string, err error) {
	log, now := opts.logger(), opts.clock()

	// Everything below runs on one pinned connection: foreign_keys cannot change inside a transaction, and the
	// writer pool has a single connection anyway.
	conn, err := w.Conn(ctx)
	if err != nil {
		return nil, "", apperr.Wrap(apperr.DB, "The database could not be upgraded.", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, schemaMigrationDDL); err != nil {
		return nil, "", apperr.Wrap(apperr.DB, "The database could not be upgraded.", err)
	}
	existing, err := appliedChecksums(ctx, conn)
	if err != nil {
		return nil, "", err
	}
	maxApplied, maxEmbedded := 0, 0
	for v := range existing {
		maxApplied = max(maxApplied, v)
	}
	for _, m := range list {
		maxEmbedded = max(maxEmbedded, m.Version)
	}
	if maxApplied > maxEmbedded {
		return nil, "", &apperr.Error{Code: apperr.DB, Message: "This data was created by a newer Cuisle"}
	}

	sorted := append([]Migration(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Version < sorted[j].Version })
	var pending []Migration
	for _, m := range sorted {
		sum, ok := existing[m.Version]
		if !ok {
			pending = append(pending, m)
			continue
		}
		if sum != m.Checksum {
			if opts.Dev {
				return nil, "", apperr.Wrap(apperr.DB, "The database schema does not match this build.",
					fmt.Errorf("migration %04d checksum mismatch", m.Version))
			}
			log.Warn("migration checksum mismatch; keeping the stored checksum", "version", m.Version)
		}
	}
	if len(pending) == 0 {
		return nil, "", nil
	}

	if maxApplied > 0 && backupDir != "" {
		backup = filepath.Join(backupDir, fmt.Sprintf("cuisle.db.bak-v%d", maxApplied))
		if err := writeSnapshot(ctx, conn, backup, "The backup could not be written."); err != nil {
			return nil, "", err
		}
		if err := pruneVersionedBackups(backupDir, 2); err != nil {
			return nil, "", err
		}
	} else {
		backup = ""
	}

	for _, m := range pending {
		if err := applyOne(ctx, conn, m, now()); err != nil {
			return applied, backup, err
		}
		applied = append(applied, m.Version)
		log.Info("migration applied", "version", m.Version, "name", m.Name)
	}
	return applied, backup, nil
}

func appliedChecksums(ctx context.Context, conn *sql.Conn) (map[int]string, error) {
	rows, err := conn.QueryContext(ctx, "SELECT version, checksum FROM schema_migration")
	if err != nil {
		return nil, apperr.Wrap(apperr.DB, "The database could not be upgraded.", err)
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var v int
		var sum string
		if err := rows.Scan(&v, &sum); err != nil {
			return nil, apperr.Wrap(apperr.DB, "The database could not be upgraded.", err)
		}
		out[v] = sum
	}
	if err := rows.Err(); err != nil {
		return nil, apperr.Wrap(apperr.DB, "The database could not be upgraded.", err)
	}
	return out, nil
}

// applyOne runs one migration file in its own BEGIN IMMEDIATE transaction on conn.
func applyOne(ctx context.Context, conn *sql.Conn, m Migration, at time.Time) (err error) {
	dbErr := func(e error) error {
		return apperr.Wrap(apperr.DB, "The database could not be upgraded.", fmt.Errorf("migration %04d: %w", m.Version, e))
	}
	if m.FKOff {
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return dbErr(err)
		}
		defer func() {
			if _, rerr := conn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON"); rerr != nil && err == nil {
				err = dbErr(rerr)
			}
		}()
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return dbErr(err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if _, err := conn.ExecContext(ctx, m.SQL); err != nil {
		return dbErr(err)
	}
	if m.FKOff {
		rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
		if err != nil {
			return dbErr(err)
		}
		bad := rows.Next()
		rowsErr := rows.Err()
		_ = rows.Close()
		if rowsErr != nil {
			return dbErr(rowsErr)
		}
		if bad {
			return dbErr(fmt.Errorf("foreign_key_check reports dangling references"))
		}
	}
	if _, err := conn.ExecContext(ctx,
		"INSERT INTO schema_migration(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
		m.Version, m.Name, m.Checksum, Ms(at)); err != nil {
		return dbErr(err)
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
		return dbErr(err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return dbErr(err)
	}
	committed = true
	return nil
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// writeSnapshot writes a consistent copy of the database to target, replacing any file already there.
// It writes target+".tmp" first and renames, so a crash never leaves a half-written file under the backup's name.
func writeSnapshot(ctx context.Context, ex sqlExecer, target, errMsg string) error {
	tmp := target + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return apperr.Wrap(apperr.DB, errMsg, err)
	}
	if _, err := ex.ExecContext(ctx, "VACUUM INTO ?", tmp); err != nil {
		_ = os.Remove(tmp)
		return apperr.Wrap(apperr.DB, errMsg, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return apperr.Wrap(apperr.DB, errMsg, err)
	}
	return nil
}

// pruneVersionedBackups keeps the `keep` newest cuisle.db.bak-v<N> files in dir. Other backups are not touched.
func pruneVersionedBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return apperr.Wrap(apperr.DB, "The backup folder could not be read.", err)
	}
	type versioned struct {
		v    int
		path string
	}
	var all []versioned
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "cuisle.db.bak-v"))
		if err != nil || !strings.HasPrefix(e.Name(), "cuisle.db.bak-v") {
			continue
		}
		all = append(all, versioned{n, filepath.Join(dir, e.Name())})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v })
	for _, b := range all[min(keep, len(all)):] {
		if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
			return apperr.Wrap(apperr.DB, "An old backup could not be removed.", err)
		}
	}
	return nil
}

// SchemaVersion returns max(schema_migration.version), 0 for an empty DB.
func SchemaVersion(ctx context.Context, q *sql.DB) (int, error) {
	var tables int
	if err := q.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migration'").Scan(&tables); err != nil {
		return 0, apperr.Wrap(apperr.DB, "The database could not be read.", err)
	}
	if tables == 0 {
		return 0, nil
	}
	var v int
	if err := q.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migration").Scan(&v); err != nil {
		return 0, apperr.Wrap(apperr.DB, "The database could not be read.", err)
	}
	return v, nil
}

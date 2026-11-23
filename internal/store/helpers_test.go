package store_test

// Shared helpers for the internal/store test suite (package store_test).
//
// None of these helpers call a store.* function other than the constants/vars (store.Migrations), so a
// failure in a test points at the behaviour under test, not at a helper. Raw database/sql with the pure-Go
// driver ("sqlite", modernc) is used wherever a test needs to inspect or tamper with a file independently
// of the implementation.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
)

// update is the one -update flag of this test binary (regenerates testdata/schema.golden.sql).
var update = flag.Bool("update", false, "regenerate testdata/schema.golden.sql from a freshly migrated database")

var ctx = context.Background()

// fixedNow is the injected clock used by tests that care about timestamps.
var fixedNow = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func clockAt(t time.Time) func() time.Time { return func() time.Time { return t } }

// recoverStub turns the Wave 0 `panic("WP-02")` stubs (and any other panic) into a clean per-test failure
// instead of aborting the whole test binary. Use as the first statement: `defer recoverStub(t)`.
func recoverStub(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("panic (stub not implemented?): %v", r)
	}
}

// ---- migration fixtures -------------------------------------------------------------------------------------

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// head1 is migration 0001 exactly as embedded (built by hand so Migrate tests do not depend on LoadMigrations).
func head1(t testing.TB) store.Migration {
	t.Helper()
	b, err := store.Migrations.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read embedded 0001: %v", err)
	}
	return store.Migration{Version: 1, Name: "init", SQL: string(b), Checksum: sha(b)}
}

// synth builds a synthetic migration (checksum = sha256 of its SQL text).
func synth(version int, name, sqlText string) store.Migration {
	return store.Migration{Version: version, Name: name, SQL: sqlText, Checksum: sha([]byte(sqlText)), FKOff: strings.HasPrefix(sqlText, "-- cuisle:fk-off\n")}
}

const addNoteSQL = "ALTER TABLE issue ADD COLUMN note TEXT;\n"

func mig2() store.Migration { return synth(2, "add_note", addNoteSQL) }
func mig3() store.Migration {
	return synth(3, "add_flag_note", "ALTER TABLE issue ADD COLUMN flag_note TEXT;\n")
}
func mig4() store.Migration {
	return synth(4, "add_other_note", "ALTER TABLE issue ADD COLUMN other_note TEXT;\n")
}

// ---- raw database access ------------------------------------------------------------------------------------

// rawDB opens a single-connection raw pool on path with foreign keys on (what Migrate sees as `w`).
func rawDB(t testing.TB, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("sql.Open(%s): %v", path, err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// freshRaw is a raw one-connection DB on an empty file <tmp>/cuisle.db; it returns the DB and its directory.
func freshRaw(t testing.TB) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	return rawDB(t, filepath.Join(dir, "cuisle.db")), dir
}

func mustExec(t testing.TB, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, stmt string, args ...any) {
	t.Helper()
	if _, err := q.ExecContext(ctx, stmt, args...); err != nil {
		t.Fatalf("exec failed: %v\nSQL: %s", err, stmt)
	}
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scalarInt(t testing.TB, q queryRower, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func scalarStr(t testing.TB, q queryRower, query string, args ...any) string {
	t.Helper()
	var s string
	if err := q.QueryRowContext(ctx, query, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return s
}

func countRows(t testing.TB, q queryRower, table string) int {
	t.Helper()
	return int(scalarInt(t, q, "SELECT count(*) FROM "+table))
}

// tableNames lists user tables (excluding sqlite_* internals) in name order.
func tableNames(t testing.TB, q *sql.DB) []string {
	t.Helper()
	rows, err := q.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, n)
	}
	return out
}

func columnNames(t testing.TB, q *sql.DB, table string) []string {
	t.Helper()
	rows, err := q.QueryContext(ctx, "SELECT name FROM pragma_table_info('"+table+"') ORDER BY cid")
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, n)
	}
	return out
}

func hasColumn(t testing.TB, q *sql.DB, table, col string) bool {
	t.Helper()
	for _, c := range columnNames(t, q, table) {
		if c == col {
			return true
		}
	}
	return false
}

// assertIntegrity fails unless foreign_key_check returns no rows and integrity_check returns "ok".
func assertIntegrity(t testing.TB, q *sql.DB) {
	t.Helper()
	rows, err := q.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	var viol []string
	for rows.Next() {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			rows.Close()
			t.Fatalf("scan foreign_key_check: %v", err)
		}
		viol = append(viol, fmt.Sprintf("%s(rowid %d)->%s", table, rowid.Int64, parent))
	}
	rows.Close()
	if len(viol) > 0 {
		t.Errorf("foreign_key_check violations: %v", viol)
	}
	if got := scalarStr(t, q, "PRAGMA integrity_check"); got != "ok" {
		t.Errorf("integrity_check = %q, want ok", got)
	}
}

// ---- schema dump (golden) ------------------------------------------------------------------------------------

var lineComment = regexp.MustCompile(`--[^\n]*`)

// normalizeSQL drops "-- comments" and collapses all whitespace, so the dump is insensitive to formatting.
func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(lineComment.ReplaceAllString(s, "")), " ")
}

// dumpSchema returns every CREATE statement (tables and indexes, normalised, ordered by object name). The
// runner-owned schema_migration table is excluded (its text is the runner's, asserted via table_info instead),
// as are sqlite_* internals and auto-indexes (NULL sql).
func dumpSchema(t testing.TB, q *sql.DB) string {
	t.Helper()
	rows, err := q.QueryContext(ctx, `SELECT sql FROM sqlite_master
		WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migration' ORDER BY name`)
	if err != nil {
		t.Fatalf("dump schema: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan schema: %v", err)
		}
		b.WriteString(normalizeSQL(s))
		b.WriteString(";\n")
	}
	return b.String()
}

// ---- opening the real thing ----------------------------------------------------------------------------------

// openAt opens the store at <dir>/cuisle.db with a fixed clock and closes it at cleanup (errors ignored).
func openAt(t *testing.T, dir string, opts store.Options) (*store.DB, store.OpenInfo) {
	t.Helper()
	if opts.Now == nil {
		opts.Now = clockAt(fixedNow)
	}
	db, info, err := store.Open(ctx, filepath.Join(dir, "cuisle.db"), opts)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(ctx) })
	return db, info
}

// openTemp opens a fresh file DB in a new temp dir.
func openTemp(t *testing.T) (*store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, _ := openAt(t, dir, store.Options{})
	return db, dir
}

// vacuumCopy writes a consistent copy of an open DB to dst with VACUUM INTO (never a raw file copy).
func vacuumCopy(t testing.TB, src *sql.DB, dst string) {
	t.Helper()
	mustExec(t, src, "VACUUM INTO '"+strings.ReplaceAll(dst, "'", "''")+"'")
}

// seedV1 applies testdata/v1.sql (data only) to q.
func seedV1(t testing.TB, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "v1.sql"))
	if err != nil {
		t.Fatalf("read testdata/v1.sql: %v", err)
	}
	mustExec(t, q, string(b))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func glob(t testing.TB, pattern string) []string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// ---- recording slog handler (levels, not text) --------------------------------------------------------------

type recHandler struct {
	mu   *sync.Mutex
	recs *[]slog.Record
}

func newRecHandler() *recHandler { return &recHandler{mu: &sync.Mutex{}, recs: &[]slog.Record{}} }

func (h *recHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	*h.recs = append(*h.recs, r.Clone())
	return nil
}
func (h *recHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recHandler) WithGroup(string) slog.Handler      { return h }
func (h *recHandler) count(level slog.Level) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range *h.recs {
		if r.Level == level {
			n++
		}
	}
	return n
}

func context_withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, d)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

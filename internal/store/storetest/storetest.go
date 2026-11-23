// Package storetest gives other packages' tests a migrated database.
// FROZEN CONTRACT. Implementation: WP-02.
package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
)

// Open returns a migrated in-memory DB closed by tb.Cleanup. now may be nil (time.Now).
func Open(tb testing.TB, now func() time.Time) *store.DB {
	tb.Helper()
	db, err := store.OpenMemory(context.Background(), store.Options{Now: now})
	if err != nil {
		tb.Fatalf("storetest.Open: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// OpenFile returns a migrated file DB in tb.TempDir() (for crash/backup/bench tests).
func OpenFile(tb testing.TB, now func() time.Time) *store.DB {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "cuisle.db")
	db, _, err := store.Open(context.Background(), path, store.Options{Now: now})
	if err != nil {
		tb.Fatalf("storetest.OpenFile: %v", err)
	}
	tb.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// Exec runs SQL statements (seed data) on db.W, failing tb on error.
func Exec(tb testing.TB, db *store.DB, stmt string, args ...any) {
	tb.Helper()
	if _, err := db.W.ExecContext(context.Background(), stmt, args...); err != nil {
		tb.Fatalf("storetest.Exec: %v\nSQL: %s", err, stmt)
	}
}

// Count returns SELECT count(*) FROM table [WHERE where].
func Count(tb testing.TB, db *store.DB, table, where string, args ...any) int {
	tb.Helper()
	q := "SELECT count(*) FROM " + table
	if where != "" {
		q += " WHERE " + where
	}
	var n int
	if err := db.W.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		tb.Fatalf("storetest.Count(%q): %v", q, err)
	}
	return n
}

// Checksum returns a stable hash of all rows of the named tables (for "DB byte-identical after cancel" tests).
// Rows are read in the table's natural order, so equal data inserted in the same order hashes equally.
func Checksum(tb testing.TB, db *store.DB, tables ...string) string {
	tb.Helper()
	h := sha256.New()
	ctx := context.Background()
	for _, table := range tables {
		fmt.Fprintf(h, "table %s\n", table)
		rows, err := db.W.QueryContext(ctx, "SELECT * FROM "+table)
		if err != nil {
			tb.Fatalf("storetest.Checksum(%s): %v", table, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			tb.Fatalf("storetest.Checksum(%s) columns: %v", table, err)
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				_ = rows.Close()
				tb.Fatalf("storetest.Checksum(%s) scan: %v", table, err)
			}
			for _, v := range vals {
				fmt.Fprintf(h, "%T=%v\x1f", v, v)
			}
			fmt.Fprint(h, "\n")
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			tb.Fatalf("storetest.Checksum(%s) rows: %v", table, err)
		}
		_ = rows.Close()
	}
	return hex.EncodeToString(h.Sum(nil))
}

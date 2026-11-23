// Package store opens the SQLite database (modernc, WAL), runs migrations and owns backups (architecture.md §6.3,
// data-model.md §3). It holds NO business rules.
// FROZEN CONTRACT: types and signatures. Implementation: WP-02. migrations/0001_init.sql is orchestrator-owned.
package store

import (
	"database/sql"
	"embed"

	_ "modernc.org/sqlite" // driver "sqlite" (pure Go)

	"log/slog"
	"time"
)

//go:embed migrations/*.sql
var Migrations embed.FS

// DB is the pair of pools. W: 1 connection, _txlock=immediate. R: 4 connections, query_only=ON.
// Pragmas on every connection: journal_mode=WAL, synchronous=NORMAL, foreign_keys=ON, busy_timeout=5000,
// temp_store=MEMORY. auto_vacuum=INCREMENTAL is set when the file is created (before the first migration).
type DB struct {
	W    *sql.DB
	R    *sql.DB
	Path string // "" for an in-memory test DB
	Log  *slog.Logger
	Now  func() time.Time
}

type Options struct {
	Logger *slog.Logger     // nil = discard
	Now    func() time.Time // nil = time.Now
	Dev    bool             // dev builds: a migration checksum mismatch is an error instead of a warning
}

// OpenInfo reports what Open had to do.
type OpenInfo struct {
	SchemaVersion int    // after migration
	Migrated      []int  // versions applied by this Open
	BackupPath    string // pre-migration backup written, "" if none
	Recovered     bool   // quick_check failed: file renamed to cuisle.db.corrupt-<ts> and restored/started empty (settings-loss banner)
	RestoredFrom  string // backup used for recovery, "" if started empty
}

// Migration is one embedded file.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string // sha256 hex of the file bytes
	FKOff    bool   // first line is "-- cuisle:fk-off"
}

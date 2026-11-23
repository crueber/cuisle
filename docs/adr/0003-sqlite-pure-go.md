# ADR 0003 — All state in SQLite via a pure-Go driver

Status: Accepted

## Context
All settings and the Jira cache must live in one local database that survives restarts and opens instantly.
Driver options: `mattn/go-sqlite3` (cgo) or `modernc.org/sqlite` (pure Go, transpiled SQLite).

## Decision
One file `cuisle.db`, **`modernc.org/sqlite`** through `database/sql`.
Pragmas: `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`, `temp_store=MEMORY`,
`auto_vacuum=INCREMENTAL`. Two pools: writer (1 connection, `BEGIN IMMEDIATE`) and reader (4 connections, `query_only`).
`STRICT` tables. Migrations are embedded, numbered, forward-only, with a pre-migration backup (`data-model.md` §3).
Config tables never reference synced-data tables. Synced facts are a permanent, provider-neutral record (ADR 0011, 0012):
they are migrated in place like config, not dropped and rebuilt.

## Consequences
- No C toolchain needed for the database; the Windows build is fully `CGO_ENABLED=0`; `go test ./internal/...` runs
  without cgo on any OS. (Wails itself still needs cgo on macOS/Linux, so this does not make the whole app cross-compilable.)
- modernc is somewhat slower than the C library; irrelevant at this data size (view build < 15 ms for 400 issues).
- Binary grows by ~6–8 MB.
- A single writer connection serialises all writes: autosave latency is bounded by the longest write transaction (the
  sync apply, < 150 ms typical). Accepted; autosave shows "Saving…" only after 250 ms.

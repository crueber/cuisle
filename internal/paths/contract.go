// Package paths resolves per-OS data and log directories (architecture.md §6.4).
// FROZEN CONTRACT: types and signatures. Implementation: WP-01.
package paths

// EnvDataDir overrides both the data and the log directory (tests, bench, --fake-jira).
const EnvDataDir = "CUISLE_DATA_DIR"

// DBFile is the database file name inside Paths.Data.
const DBFile = "cuisle.db"

type Paths struct {
	Data string // DB and backups
	Logs string
}

// Env abstracts the process environment for tests.
type Env struct {
	GOOS   string              // runtime.GOOS
	Home   string              // user home dir
	Getenv func(string) string // os.Getenv
}

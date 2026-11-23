// Package logx sets up slog with a size-rotating file and secret redaction (architecture.md §6.2).
// FROZEN CONTRACT: types and signatures. Implementation: WP-01.
package logx

import "log/slog"

// EnvLevel selects the level: "debug" or "info" (default).
const EnvLevel = "CUISLE_LOG"

type Options struct {
	Dir      string     // log directory; file is <Dir>/cuisle.log
	Level    slog.Level // from LevelFromEnv
	Dev      bool       // also write text to stderr
	MaxBytes int64      // rotate at this size (default 5 MB)
	MaxFiles int        // rotated files kept (default 3)
}

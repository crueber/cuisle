package cache

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// dbErr keeps *apperr.Error values and wraps everything else as a retryable db error.
func dbErr(err error, msg string) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return apperr.Wrap(apperr.Cancelled, "Cancelled.", err)
	}
	return apperr.Wrap(apperr.DB, msg, err)
}

// withWriteTx runs fn in one immediate write transaction on the writer pool.
func withWriteTx(ctx context.Context, w *sql.DB, fn func(tx *sql.Tx) error) error {
	return (&store.DB{W: w}).WriteTx(ctx, fn)
}

// nullMs stores a time as Unix ms, or NULL for the zero time.
func nullMs(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return store.Ms(t)
}

// nullStr stores "" as NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// msTime reads a nullable Unix ms column; NULL reads as the zero time.
func msTime(n sql.NullInt64) time.Time {
	return store.FromMs(n.Int64)
}

// boolInt is the 0/1 form of a boolean column.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// encodeMap is the JSON text of a meta map; nil is "{}".
func encodeMap(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// decodeMap reads a JSON object column; anything unreadable is an empty, non-nil map.
func decodeMap(s string) map[string]any {
	out := map[string]any{}
	if s == "" {
		return out
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

// encodeStrings is the JSON text of a string list; nil is "[]".
func encodeStrings(l []string) string {
	if l == nil {
		return "[]"
	}
	b, err := json.Marshal(l)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// decodeStrings reads a JSON array column; anything unreadable is an empty, non-nil slice.
func decodeStrings(s string) []string {
	out := []string{}
	if s == "" {
		return out
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

// likeArg turns a user query into a LIKE pattern: substring match with % and _ taken literally (ESCAPE '\').
func likeArg(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// limitArg maps a row limit to SQLite's LIMIT: n <= 0 means no limit.
func limitArg(n int) int {
	if n <= 0 {
		return -1
	}
	return n
}

// placeholders returns "?,?,…" for n arguments.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

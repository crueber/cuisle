package settings

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store"
)

// AppSettings returns every app_setting with AppDefaults filled in for missing keys.
func (s *Store) AppSettings(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(AppDefaults))
	for k, v := range AppDefaults {
		out[k] = v
	}
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT key, value FROM app_setting`)
		if err != nil {
			return dbErr(err)
		}
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return dbErr(err)
			}
			out[k] = v
		}
		return dbErr(rows.Err())
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// validAppSetting checks one key/value pair. Field is the key for a bad value, and "key" for an unknown key.
func validAppSetting(key, value string) error {
	ok := false
	switch key {
	case KeyTheme:
		ok = value == "light" || value == "dark" || value == "system"
	case KeyTextSize:
		ok = value == "standard" || value == "large"
	case KeySyncOnOpen:
		ok = value == "stale" || value == "always" || value == "never"
	case KeySyncStaleHours:
		n, err := strconv.Atoi(value)
		ok = err == nil && strconv.Itoa(n) == value && n >= 1 && n <= 48
	case KeyHistoryRetentionDays:
		ok = value == "0" // reserved: 0 = keep forever is the only v1 behaviour (data-model §5.2)
	default:
		return apperr.Validationf("key", "Unknown setting %q.", key)
	}
	if !ok {
		return apperr.Validationf(key, "That value is not allowed for this setting.")
	}
	return nil
}

// SetAppSetting validates (theme light|dark|system; text_size standard|large; sync_on_open stale|always|never;
// sync_stale_hours integer 1–48; unknown keys → validation) and upserts. Ack.Rev is 0.
func (s *Store) SetAppSetting(ctx context.Context, key, value string) (Ack, error) {
	if err := validAppSetting(key, value); err != nil {
		return Ack{}, err
	}
	now := s.Now()
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO app_setting (key, value, updated_at) VALUES (?, ?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			key, value, store.Ms(now))
		return dbErr(err)
	})
	if err != nil {
		return Ack{}, err
	}
	return Ack{SavedAt: now}, nil
}

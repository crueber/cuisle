package settings

import (
	"context"
	"database/sql"
	"encoding/json"
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

// Keys added by F12 (architecture.md §6.4). Their defaults are applied by the readers, not by AppDefaults (frozen):
// comment order "newest", cache cap 1024 MiB, cleanup offer 2048 MiB, request rate 8/s.
const (
	KeyTicketCommentOrder    = "ticket.comment_order"
	KeyMediaCacheMB          = "media.cache_max_mb"
	KeyStorageCleanupOfferMB = "storage.cleanup_offer_mb"
	KeyJiraMaxRPS            = "jira.max_rps"
	// KeyUpdateCheckDaily is "Check for updates daily" (ADR 0016): "on" (the reader's default) or "off".
	KeyUpdateCheckDaily = "update.check_daily"
	// F14 (the command palette, ux-spec §15.7): the last opened tickets (a JSON array of local issue ids, most recent
	// first) and how often each command ran (a JSON object command id → count). "" = none.
	KeyPaletteRecentTickets = "palette.recent_tickets"
	KeyPaletteCommandUsage  = "palette.command_usage"
)

// Caps of the palette settings. The UI keeps 8 recents; the validator is more tolerant so a newer or older build never
// has its write rejected over a count.
const (
	paletteRecentMax = 50
	paletteUsageMax  = 400
	paletteMaxBytes  = 16 << 10
)

// validPaletteRecent: "" or a JSON array of at most 50 positive integers.
func validPaletteRecent(v string) bool {
	if v == "" {
		return true
	}
	if len(v) > paletteMaxBytes {
		return false
	}
	var ids []int64
	if err := json.Unmarshal([]byte(v), &ids); err != nil || ids == nil || len(ids) > paletteRecentMax {
		return false
	}
	for _, id := range ids {
		if id <= 0 {
			return false
		}
	}
	return true
}

// validPaletteUsage: "" or a JSON object of at most 400 entries, command id (1 to 80 characters) → non-negative integer.
func validPaletteUsage(v string) bool {
	if v == "" {
		return true
	}
	if len(v) > paletteMaxBytes {
		return false
	}
	var m map[string]int64
	if err := json.Unmarshal([]byte(v), &m); err != nil || m == nil || len(m) > paletteUsageMax {
		return false
	}
	for id, n := range m {
		if id == "" || len(id) > 80 || n < 0 {
			return false
		}
	}
	return true
}

// UpdateCheckDaily reads KeyUpdateCheckDaily: on unless it is stored as "off".
func (s *Store) UpdateCheckDaily(ctx context.Context) bool {
	var v string
	if err := s.DB.R.QueryRowContext(ctx, `SELECT value FROM app_setting WHERE key = ?`, KeyUpdateCheckDaily).Scan(&v); err != nil {
		return true
	}
	return v != "off"
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
	case KeyTicketCommentOrder:
		ok = value == "newest" || value == "oldest"
	case KeyMediaCacheMB: // MiB; the image cache cap (ux-spec §14.11)
		n, err := strconv.Atoi(value)
		ok = err == nil && strconv.Itoa(n) == value && n >= 64 && n <= 1<<20
	case KeyStorageCleanupOfferMB: // MiB; Disconnect offers to delete content above this (the UI steps 1 to 64 GB)
		n, err := strconv.Atoi(value)
		ok = err == nil && strconv.Itoa(n) == value && n >= 1024 && n <= 64*1024
	case KeyJiraMaxRPS: // hidden: the sustained request rate cap (jira-sync §3.1)
		n, err := strconv.Atoi(value)
		ok = err == nil && strconv.Itoa(n) == value && n >= 1 && n <= 20
	case KeyUpdateCheckDaily:
		ok = value == "on" || value == "off"
	case KeyPaletteRecentTickets:
		ok = validPaletteRecent(value)
	case KeyPaletteCommandUsage:
		ok = validPaletteUsage(value)
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

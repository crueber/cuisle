package sink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/store"
)

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// ListConfigs returns all sinks by id.
func ListConfigs(ctx context.Context, db *store.DB) ([]Config, error) {
	rows, err := db.R.QueryContext(ctx, `SELECT id, kind, name, standup_id, config, send_on, enabled, created_at,
		updated_at FROM sink ORDER BY id`)
	if err != nil {
		return nil, dbErr(ctx, err)
	}
	defer rows.Close()
	out := []Config{}
	for rows.Next() {
		c, err := scanConfig(rows)
		if err != nil {
			return nil, dbErr(ctx, err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(ctx, err)
	}
	return out, nil
}

func scanConfig(r rowScanner) (Config, error) {
	var c Config
	var standup sql.NullInt64
	var cfg string
	var enabled, created, updated int64
	if err := r.Scan(&c.ID, &c.Kind, &c.Name, &standup, &cfg, &c.SendOn, &enabled, &created, &updated); err != nil {
		return c, err
	}
	c.StandupID = standup.Int64
	c.Enabled = enabled != 0
	c.CreatedAt = store.FromMs(created)
	c.UpdatedAt = store.FromMs(updated)
	c.Config = map[string]string{}
	if err := json.Unmarshal([]byte(cfg), &c.Config); err != nil {
		return c, apperr.Wrap(apperr.Internal, "The sink configuration is unreadable.", err)
	}
	return c, nil
}

// SaveConfig validates against the kind's schema (Validate), name 1–80, sendOn manual|after_sync; inserts or updates;
// the secret goes to the keychain under secrets.SinkAccount(id) (never the DB).
func SaveConfig(ctx context.Context, db *store.DB, kr secrets.Keyring, in ConfigInput) (Config, error) {
	kind, ok := Get(in.Kind)
	if !ok {
		return Config{}, apperr.Validationf("kind", "Choose a sink type.")
	}
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 80 {
		return Config{}, apperr.Validationf("name", "Name must be 1 to 80 characters.")
	}
	if in.SendOn != "manual" && in.SendOn != "after_sync" {
		return Config{}, apperr.Validationf("sendOn", "Choose manual or after sync.")
	}
	if err := kind.Validate(in.Config); err != nil {
		return Config{}, apperr.From(err)
	}
	cfgJSON, err := json.Marshal(nonNil(in.Config))
	if err != nil {
		return Config{}, apperr.Wrap(apperr.Internal, "The sink configuration could not be saved.", err)
	}
	standup := sql.NullInt64{Int64: in.StandupID, Valid: in.StandupID != 0}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	now := clock(db)().UnixMilli()

	var out Config
	err = db.WriteTx(ctx, func(tx *sql.Tx) error {
		id := in.ID
		var created int64
		if id == 0 {
			res, err := tx.ExecContext(ctx, `INSERT INTO sink(kind,name,standup_id,config,send_on,enabled,created_at,
				updated_at) VALUES(?,?,?,?,?,?,?,?)`,
				in.Kind, name, standup, string(cfgJSON), in.SendOn, enabled, now, now)
			if err != nil {
				return err
			}
			if id, err = res.LastInsertId(); err != nil {
				return err
			}
			created = now
		} else {
			err := tx.QueryRowContext(ctx, `SELECT created_at FROM sink WHERE id=?`, id).Scan(&created)
			if errors.Is(err, sql.ErrNoRows) {
				return apperr.New(apperr.NotFound, "The sink was not found.")
			}
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE sink SET kind=?,name=?,standup_id=?,config=?,send_on=?,enabled=?,
				updated_at=? WHERE id=?`,
				in.Kind, name, standup, string(cfgJSON), in.SendOn, enabled, now, id); err != nil {
				return err
			}
		}
		account := secrets.SinkAccount(id)
		hasSecret, err := applySecret(kr, account, in.Secret)
		if err != nil {
			return err // rolls the row back too
		}
		out = Config{
			ID: id, Kind: in.Kind, Name: name, StandupID: in.StandupID, Config: copyMap(in.Config),
			HasSecret: hasSecret, SendOn: in.SendOn, Enabled: in.Enabled,
			CreatedAt: store.FromMs(created), UpdatedAt: store.FromMs(now),
		}
		return nil
	})
	if err != nil {
		return Config{}, err
	}
	return out, nil
}

// applySecret stores (non-empty), deletes (empty) or leaves (nil) the keychain item and reports whether one exists.
func applySecret(kr secrets.Keyring, account string, secret *string) (bool, error) {
	if secret == nil {
		_, err := kr.Get(account)
		return err == nil, nil
	}
	if *secret == "" {
		if err := kr.Delete(account); err != nil {
			return false, apperr.Wrap(apperr.KeychainUnavailable, "The keychain could not remove the secret.", err)
		}
		return false, nil
	}
	if err := kr.Set(account, *secret); err != nil {
		return false, apperr.Wrap(apperr.KeychainUnavailable, "The keychain could not store the secret.", err)
	}
	return true, nil
}

// DeleteConfig deletes the sink (outbox rows cascade) and its keychain item.
func DeleteConfig(ctx context.Context, db *store.DB, kr secrets.Keyring, id int64) error {
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM sink WHERE id=?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return apperr.New(apperr.NotFound, "The sink was not found.")
		}
		// Keychain last: if it fails the transaction rolls back and the sink stays, with its secret.
		if _, err := applySecret(kr, secrets.SinkAccount(id), ptr("")); err != nil {
			return err
		}
		return nil
	})
}

// Outbox returns the newest limit rows of a sink.
func Outbox(ctx context.Context, db *store.DB, sinkID int64, limit int) ([]OutboxItem, error) {
	rows, err := db.R.QueryContext(ctx, `SELECT id, sink_id, standup_id, cause, payload_kind, status, attempts,
		last_error_code, last_error, receipt, created_at, delivered_at
		FROM sink_outbox WHERE sink_id=? ORDER BY id DESC LIMIT ?`, sinkID, limit)
	if err != nil {
		return nil, dbErr(ctx, err)
	}
	defer rows.Close()
	out := []OutboxItem{}
	for rows.Next() {
		var it OutboxItem
		var code, msg, receipt sql.NullString
		var delivered sql.NullInt64
		var created int64
		if err := rows.Scan(&it.ID, &it.SinkID, &it.StandupID, &it.Cause, &it.PayloadKind, &it.Status, &it.Attempts,
			&code, &msg, &receipt, &created, &delivered); err != nil {
			return nil, dbErr(ctx, err)
		}
		it.ErrorCode, it.Error, it.Receipt = code.String, msg.String, receipt.String
		it.CreatedAt = store.FromMs(created)
		if delivered.Valid {
			it.DeliveredAt = store.FromMs(delivered.Int64)
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(ctx, err)
	}
	return out, nil
}

// clock returns the database clock (time.Now when the DB has none).
func clock(db *store.DB) func() time.Time {
	if db.Now == nil {
		return time.Now
	}
	return db.Now
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func ptr(s string) *string { return &s }

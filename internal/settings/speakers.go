package settings

import (
	"cmp"
	"context"
	"database/sql"
	"math/rand"
	"slices"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// speakerRetention is how long an unseen auto speaker is kept before it is pruned.
const speakerRetention = 30 * 24 * time.Hour

// loadSpeakersTx returns every standup_speaker row by position.
func loadSpeakersTx(ctx context.Context, tx *sql.Tx, id int64) ([]Speaker, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT account_id, display_name, position, included, added_manually, user_touched, last_seen_at, created_at
		 FROM standup_speaker WHERE standup_id = ? ORDER BY position, account_id`, id)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var out []Speaker
	for rows.Next() {
		var sp Speaker
		var included, manual, touched int
		var seen sql.NullInt64
		var created int64
		if err := rows.Scan(&sp.AccountID, &sp.DisplayName, &sp.Position, &included, &manual, &touched,
			&seen, &created); err != nil {
			return nil, dbErr(err)
		}
		sp.Included = included == 1
		sp.Manual = manual == 1
		sp.UserTouched = touched == 1
		if seen.Valid {
			sp.LastSeenAt = store.FromMs(seen.Int64)
		}
		sp.CreatedAt = store.FromMs(created)
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err)
	}
	return out, nil
}

// accountOrderTx returns the account ids of a standup's speakers by position.
func accountOrderTx(ctx context.Context, tx *sql.Tx, id int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT account_id FROM standup_speaker WHERE standup_id = ? ORDER BY position, account_id`, id)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var acc string
		if err := rows.Scan(&acc); err != nil {
			return nil, dbErr(err)
		}
		out = append(out, acc)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr(err)
	}
	return out, nil
}

// writePositionsTx sets position = index for every account in order (dense positions).
func writePositionsTx(ctx context.Context, tx *sql.Tx, id int64, order []string) error {
	for i, acc := range order {
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup_speaker SET position = ? WHERE standup_id = ? AND account_id = ?`, i, id, acc); err != nil {
			return dbErr(err)
		}
	}
	return nil
}

// speakerManualTx returns whether the speaker was added manually; not_found when the speaker is not on the standup.
func speakerManualTx(ctx context.Context, tx *sql.Tx, id int64, accountID string) (bool, error) {
	var manual int
	err := tx.QueryRowContext(ctx,
		`SELECT added_manually FROM standup_speaker WHERE standup_id = ? AND account_id = ?`,
		id, accountID).Scan(&manual)
	if err == sql.ErrNoRows {
		return false, apperr.New(apperr.NotFound, "That person is not on this standup.")
	}
	if err != nil {
		return false, dbErr(err)
	}
	return manual == 1, nil
}

// Speakers returns standup_speaker rows by position.
func (s *Store) Speakers(ctx context.Context, id int64) ([]Speaker, error) {
	var out []Speaker
	err := s.DB.ReadTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := standupBoardTx(ctx, tx, id); err != nil {
			return err
		}
		var err error
		out, err = loadSpeakersTx(ctx, tx, id)
		return err
	})
	return out, err
}

// SetSpeakerIncluded toggles included and sets user_touched.
func (s *Store) SetSpeakerIncluded(ctx context.Context, id int64, accountID string, included bool) (Ack, error) {
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		if _, err := speakerManualTx(ctx, tx, id, accountID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup_speaker SET included = ?, user_touched = 1 WHERE standup_id = ? AND account_id = ?`,
			boolInt(included), id, accountID); err != nil {
			return dbErr(err)
		}
		return nil
	})
}

// MoveSpeaker moves one speaker to toPosition (clamped), dense positions, user_touched.
func (s *Store) MoveSpeaker(ctx context.Context, id int64, accountID string, toPosition int) (Ack, error) {
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		order, err := accountOrderTx(ctx, tx, id)
		if err != nil {
			return err
		}
		idx := slices.Index(order, accountID)
		if idx < 0 {
			return apperr.New(apperr.NotFound, "That person is not on this standup.")
		}
		order = slices.Delete(order, idx, idx+1)
		to := max(0, min(toPosition, len(order)))
		order = slices.Insert(order, to, accountID)
		if err := writePositionsTx(ctx, tx, id, order); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup_speaker SET user_touched = 1 WHERE standup_id = ? AND account_id = ?`,
			id, accountID); err != nil {
			return dbErr(err)
		}
		return nil
	})
}

// SortSpeakers: "az" by display name; "active" by active desc (active map; ties by name); "shuffle" → with
// reshuffle_daily off rewrites positions randomly (rnd), with it on stores a new shuffle_seed for today.
func (s *Store) SortSpeakers(ctx context.Context, id int64, mode string, active map[string]int, rnd func() int64) ([]Speaker, Ack, error) {
	switch mode {
	case "az", "active", "shuffle":
	default:
		return nil, Ack{}, apperr.Validationf("mode", "Choose a sort: A–Z, active or shuffle.")
	}
	if rnd == nil {
		rnd = rand.Int63
	}
	var out []Speaker
	ack, err := s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		speakers, err := loadSpeakersTx(ctx, tx, id)
		if err != nil {
			return err
		}
		switch mode {
		case "az":
			slices.SortFunc(speakers, func(a, b Speaker) int {
				return cmp.Or(strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)),
					strings.Compare(a.AccountID, b.AccountID))
			})
		case "active":
			slices.SortFunc(speakers, func(a, b Speaker) int {
				return cmp.Or(cmp.Compare(active[b.AccountID], active[a.AccountID]),
					strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)),
					strings.Compare(a.AccountID, b.AccountID))
			})
		case "shuffle":
			var daily int
			if err := tx.QueryRowContext(ctx,
				`SELECT reshuffle_daily FROM standup_settings WHERE standup_id = ?`, id).Scan(&daily); err != nil && err != sql.ErrNoRows {
				return dbErr(err)
			}
			if daily == 1 {
				if _, err := tx.ExecContext(ctx,
					`UPDATE standup_settings SET shuffle_seed = ?, shuffle_date = ? WHERE standup_id = ?`,
					rnd(), s.Now().Format("2006-01-02"), id); err != nil {
					return dbErr(err)
				}
				out = speakers
				return nil
			}
			for i := len(speakers) - 1; i > 0; i-- {
				j := int(uint64(rnd()) % uint64(i+1))
				speakers[i], speakers[j] = speakers[j], speakers[i]
			}
		}
		order := make([]string, len(speakers))
		for i, sp := range speakers {
			order[i] = sp.AccountID
		}
		if err := writePositionsTx(ctx, tx, id, order); err != nil {
			return err
		}
		out, err = loadSpeakersTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, Ack{}, err
	}
	return out, ack, nil
}

// AddSpeaker appends a manual speaker (added_manually=1, included=1); existing → included=1, user_touched.
func (s *Store) AddSpeaker(ctx context.Context, id int64, p model.Person) ([]Speaker, Ack, error) {
	if p.AccountID == "" {
		return nil, Ack{}, apperr.Validationf("accountId", "Choose someone to add.")
	}
	var out []Speaker
	ack, err := s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM standup_speaker_removed WHERE standup_id = ? AND account_id = ?`, id, p.AccountID); err != nil {
			return dbErr(err)
		}
		var n int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM standup_speaker WHERE standup_id = ? AND account_id = ?`,
			id, p.AccountID).Scan(&n); err != nil {
			return dbErr(err)
		}
		if n > 0 {
			if _, err := tx.ExecContext(ctx,
				`UPDATE standup_speaker SET included = 1, user_touched = 1 WHERE standup_id = ? AND account_id = ?`,
				id, p.AccountID); err != nil {
				return dbErr(err)
			}
		} else {
			var maxPos sql.NullInt64
			if err := tx.QueryRowContext(ctx,
				`SELECT MAX(position) FROM standup_speaker WHERE standup_id = ?`, id).Scan(&maxPos); err != nil {
				return dbErr(err)
			}
			pos := int64(0)
			if maxPos.Valid {
				pos = maxPos.Int64 + 1
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually,
				 user_touched, last_seen_at, created_at) VALUES (?, ?, ?, ?, 1, 1, 0, NULL, ?)`,
				id, p.AccountID, p.DisplayName, pos, store.Ms(s.Now())); err != nil {
				return dbErr(err)
			}
		}
		var err error
		out, err = loadSpeakersTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, Ack{}, err
	}
	return out, ack, nil
}

// RemoveSpeaker takes anyone off the standup's speaker list. Someone who has tickets on the board is remembered in
// standup_speaker_removed so the next sync does not add them back; AddSpeaker undoes that.
func (s *Store) RemoveSpeaker(ctx context.Context, id int64, accountID string) (Ack, error) {
	return s.writeStandup(ctx, id, func(tx *sql.Tx) error {
		if _, err := speakerManualTx(ctx, tx, id, accountID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM standup_speaker WHERE standup_id = ? AND account_id = ?`, id, accountID); err != nil {
			return dbErr(err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO standup_speaker_removed (standup_id, account_id, removed_at) VALUES (?, ?, ?)
			 ON CONFLICT(standup_id, account_id) DO UPDATE SET removed_at = excluded.removed_at`,
			id, accountID, store.Ms(s.Now())); err != nil {
			return dbErr(err)
		}
		order, err := accountOrderTx(ctx, tx, id)
		if err != nil {
			return err
		}
		return writePositionsTx(ctx, tx, id, order)
	})
}

// clearSpeakersTx empties a standup's speaker list and its removed list (the board changed).
func clearSpeakersTx(ctx context.Context, tx *sql.Tx, id int64) error {
	for _, q := range []string{
		`DELETE FROM standup_speaker WHERE standup_id = ?`,
		`DELETE FROM standup_speaker_removed WHERE standup_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return dbErr(err)
		}
	}
	return nil
}

// removedSpeakersTx returns the account ids removed from a standup's speaker list.
func removedSpeakersTx(ctx context.Context, tx *sql.Tx, id int64) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT account_id FROM standup_speaker_removed WHERE standup_id = ?`, id)
	if err != nil {
		return nil, dbErr(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var acc string
		if err := rows.Scan(&acc); err != nil {
			return nil, dbErr(err)
		}
		out[acc] = true
	}
	return out, dbErr(rows.Err())
}

// EnsureDailySeed stores shuffle_date=today, shuffle_seed=seed when reshuffle_daily=1 and shuffle_date ≠ today.
// Does not bump rev. Returns whether it wrote.
func (s *Store) EnsureDailySeed(ctx context.Context, id int64, today string, seed int64) (bool, error) {
	var wrote bool
	err := s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := standupBoardTx(ctx, tx, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE standup_settings SET shuffle_seed = ?, shuffle_date = ?
			 WHERE standup_id = ? AND reshuffle_daily = 1 AND (shuffle_date IS NULL OR shuffle_date <> ?)`,
			seed, today, id, today)
		if err != nil {
			return dbErr(err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return dbErr(err)
		}
		wrote = n > 0
		return nil
	})
	return wrote, err
}

// MergeSpeakersTx (sync apply step 6): append unseen assignees (never ones in standup_speaker_removed) (included=1, auto), refresh display names and
// last_seen_at for seen, delete auto rows (user_touched=0, added_manually=0) unseen for 30 days. Returns changed.
func MergeSpeakersTx(ctx context.Context, tx *sql.Tx, id int64, seen []model.Person, now time.Time) (bool, error) {
	current, err := loadSpeakersTx(ctx, tx, id)
	if err != nil {
		return false, err
	}
	removed, err := removedSpeakersTx(ctx, tx, id)
	if err != nil {
		return false, err
	}
	people := make(map[string]model.Person, len(seen))
	var seenOrder []string
	for _, p := range seen {
		if _, dup := people[p.AccountID]; dup || removed[p.AccountID] {
			continue
		}
		people[p.AccountID] = p
		seenOrder = append(seenOrder, p.AccountID)
	}

	nowMs := store.Ms(now)
	cutoff := store.Ms(now.Add(-speakerRetention))
	changed := false
	inList := make(map[string]bool, len(current))
	var kept []Speaker
	var pruned []string
	seenNow := make(map[string]string) // seen account → display name to store
	for _, sp := range current {
		inList[sp.AccountID] = true
		if p, ok := people[sp.AccountID]; ok {
			if p.DisplayName != "" && p.DisplayName != sp.DisplayName {
				sp.DisplayName = p.DisplayName
				changed = true
			}
			seenNow[sp.AccountID] = sp.DisplayName
			kept = append(kept, sp)
			continue
		}
		if !sp.Manual && !sp.UserTouched && !sp.LastSeenAt.IsZero() && store.Ms(sp.LastSeenAt) < cutoff {
			pruned = append(pruned, sp.AccountID)
			changed = true
			continue
		}
		kept = append(kept, sp)
	}

	order := make([]string, 0, len(kept)+len(seenOrder))
	for _, sp := range kept {
		order = append(order, sp.AccountID)
	}
	for _, acc := range seenOrder {
		if inList[acc] {
			continue
		}
		changed = true
		order = append(order, acc)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually,
			 user_touched, last_seen_at, created_at) VALUES (?, ?, ?, ?, 1, 0, 0, ?, ?)`,
			id, acc, people[acc].DisplayName, len(order)-1, nowMs, nowMs); err != nil {
			return false, dbErr(err)
		}
	}
	for _, acc := range pruned {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM standup_speaker WHERE standup_id = ? AND account_id = ?`, id, acc); err != nil {
			return false, dbErr(err)
		}
	}
	for acc, name := range seenNow {
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup_speaker SET display_name = ?, last_seen_at = ? WHERE standup_id = ? AND account_id = ?`,
			name, nowMs, id, acc); err != nil {
			return false, dbErr(err)
		}
	}
	if err := writePositionsTx(ctx, tx, id, order); err != nil {
		return false, err
	}
	return changed, nil
}

package app

import (
	"context"
	"database/sql"
	"errors"
	gosync "sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/settings"
)

// deleteContent removes the synced content of a connection (ux-spec §14.11) and keeps every record and history row:
// issues, change tables, comment rows (bodies only are cleared), member spans, dev events and attachment rows stay.
// Standups are flagged as needing a sync so a later reconnect fills the content again. Then the media cache is cleared
// and the database is vacuumed.
func (d *Deps) deleteContent(ctx context.Context, connID int64) error {
	d.wipeMu.Lock()
	defer d.wipeMu.Unlock()

	drainCtx, drainCancel := context.WithTimeout(ctx, 5*time.Second)
	defer drainCancel()

	// Stop any running syncs, background thumbnail fetches, and in-flight ticket writes/refreshes so they don't write
	// back into the wiped tables or block VACUUM.
	if d.Sync != nil {
		resumeSync, err := d.Sync.PauseAndCancel(drainCtx)
		defer resumeSync()
		if err != nil {
			return err
		}
	}

	d.ticketMu.Lock()
	d.bgPaused++
	cancel := d.bgCancel
	d.bgCtx, d.bgCancel = nil, nil
	d.ticketMu.Unlock()
	defer func() {
		d.ticketMu.Lock()
		d.bgPaused--
		d.ticketMu.Unlock()
	}()
	if cancel != nil {
		cancel()
	}
	if err := waitWG(drainCtx, &d.bg); err != nil {
		return err
	}
	if err := waitWG(drainCtx, &d.ticketWG); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return apperr.Wrap(apperr.Cancelled, "Cancelled.", err)
	}
	mine := `(SELECT id FROM issue WHERE connection_id = ?)`
	err := d.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM issue_content WHERE issue_id IN ` + mine,
			`DELETE FROM issue_field_value WHERE issue_id IN ` + mine,
			`DELETE FROM issue_link WHERE issue_id IN ` + mine,
			`DELETE FROM transition_cache WHERE issue_id IN ` + mine,
			`DELETE FROM ticket_draft WHERE issue_id IN ` + mine,
			`UPDATE comment SET body_adf = NULL, visibility_type = NULL, visibility_value = NULL WHERE issue_id IN ` + mine,
		} {
			if _, err := tx.ExecContext(ctx, q, connID); err != nil {
				return apperr.Wrap(apperr.DB, "The synced content could not be deleted.", err)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM standup`)
		if err != nil {
			return apperr.Wrap(apperr.DB, "The synced content could not be deleted.", err)
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return apperr.Wrap(apperr.DB, "The synced content could not be deleted.", err)
			}
			ids = append(ids, id)
		}
		if err := rows.Close(); err != nil {
			return apperr.Wrap(apperr.DB, "The synced content could not be deleted.", err)
		}
		for _, id := range ids {
			if err := settings.SetNeedsSyncTx(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if d.Media != nil {
		if err := d.Media.Clear(ctx); err != nil {
			return err
		}
	}
	if _, err := d.DB.W.ExecContext(ctx, `VACUUM`); err != nil {
		d.log().Warn("vacuum after deleting content failed", "err", err)
	}
	return nil
}

func waitWG(ctx context.Context, wg *waitGroup) error {
	err := wg.WaitContext(ctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return apperr.Wrap(apperr.Cancelled, "Cancelled.", err)
	}
	return apperr.Wrap(apperr.Timeout, "Background work did not stop in time.", err)
}

// waitGroup is a cancellable counter without sync.WaitGroup's reuse restriction after a timed-out Wait.
type waitGroup struct {
	mu    gosync.Mutex
	count int
	zero  chan struct{}
}

func (g *waitGroup) Add(delta int) {
	g.mu.Lock()
	g.count += delta
	if g.count < 0 {
		g.mu.Unlock()
		panic("app: negative waitGroup counter")
	}
	if g.count == 0 && g.zero != nil {
		close(g.zero)
		g.zero = nil
	}
	g.mu.Unlock()
}

func (g *waitGroup) Done() { g.Add(-1) }

func (g *waitGroup) Wait() { _ = g.WaitContext(context.Background()) }

func (g *waitGroup) WaitContext(ctx context.Context) error {
	g.mu.Lock()
	if g.count == 0 {
		g.mu.Unlock()
		return nil
	}
	if g.zero == nil {
		g.zero = make(chan struct{})
	}
	ch := g.zero
	g.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

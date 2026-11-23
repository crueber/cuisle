package sink

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/export"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/store"
)

type DispatcherOptions struct {
	Logger *slog.Logger
	Now    func() time.Time
	Sleep  func(d time.Duration)          // backoff inside one job; tests pass a no-op
	Notify func(Event)                    // may be nil
	Lookup func(kind string) (Sink, bool) // nil = Get
}

const (
	maxAttempts  = 8 // data-model.md §2.8: dead after 8 attempts in total
	triesPerJob  = 3 // one delivery job runs at most this many tries
	statusDead   = "dead"
	statusFailed = "failed"
)

// backoffs[i] is the sleep after failed try i+1 within one job (1 s, then 4 s; none after the last try).
var backoffs = []time.Duration{time.Second, 4 * time.Second}

type dispatcher struct {
	db     *store.DB
	kr     secrets.Keyring
	build  BuildFunc
	log    *slog.Logger
	now    func() time.Time
	sleep  func(time.Duration) // nil = a plain timer
	notify func(Event)
	lookup func(kind string) (Sink, bool)

	// jobMu serialises delivery jobs so one outbox row is never attempted by two jobs at once.
	// It is taken only inside runJob, so Flush/AfterSync can call Enqueue/runJob without nesting it.
	jobMu sync.Mutex
}

// NewDispatcher returns the outbox dispatcher (data-model.md §2.8 rules: 3 tries per job, failed → next explicit
// trigger, dead after 8 attempts total, idempotency key UNIQUE).
func NewDispatcher(db *store.DB, kr secrets.Keyring, build BuildFunc, opts DispatcherOptions) Dispatcher {
	d := &dispatcher{db: db, kr: kr, build: build, log: opts.Logger, now: opts.Now, sleep: opts.Sleep,
		notify: opts.Notify, lookup: opts.Lookup}
	if d.log == nil {
		d.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.now == nil {
		d.now = time.Now
	}
	if d.lookup == nil {
		d.lookup = Get
	}
	// Startup hook: a row left `delivering` was interrupted; it becomes `failed` (no delivery is attempted here).
	_, err := db.W.ExecContext(context.Background(),
		`UPDATE sink_outbox SET status='failed', updated_at=? WHERE status='delivering'`, d.now().UnixMilli())
	if err != nil {
		d.log.Error("sink outbox: could not reset interrupted deliveries", "err", err)
	}
	return d
}

// Enqueue freezes the payload in sink_outbox and runs one delivery job (≤ 3 tries, 1 s / 4 s backoff) before
// returning. A failed job is not an error: the row records it and Notify reports it.
func (d *dispatcher) Enqueue(ctx context.Context, sinkID, standupID int64, req Request, cause, key string) (int64, error) {
	if cause != "manual" && cause != "after_sync" {
		return 0, apperr.Validationf("cause", "Unknown send cause.")
	}
	if req.Kind != "current" && req.Kind != "range" {
		return 0, apperr.Validationf("kind", "Unknown payload kind.")
	}
	s, err := d.loadSink(ctx, sinkID)
	if err != nil {
		return 0, err
	}
	if key == "" {
		if key, err = newKey(); err != nil {
			return 0, err
		}
	}
	if id, found, err := d.outboxByKey(ctx, key); err != nil || found {
		return id, err
	}
	if _, ok := d.lookup(s.kind); !ok {
		return 0, apperr.New(apperr.Unsupported, "This sink type is not available in this build.")
	}
	_, raw, _, _, err := d.build(ctx, standupID, req)
	if err != nil {
		return 0, fmt.Errorf("build payload: %w", err)
	}
	ms := d.now().UnixMilli()
	var asOf sql.NullInt64
	var from, to sql.NullString
	if req.Kind == "current" {
		asOf = sql.NullInt64{Int64: ms, Valid: true}
	} else {
		from = sql.NullString{String: req.From, Valid: true}
		to = sql.NullString{String: req.To, Valid: true}
	}
	res, err := d.db.W.ExecContext(ctx, `INSERT INTO sink_outbox(sink_id,standup_id,cause,payload_kind,as_of,range_from,
		range_to,idempotency_key,payload,status,attempts,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,'pending',0,?,?) ON CONFLICT(idempotency_key) DO NOTHING`,
		sinkID, standupID, cause, req.Kind, asOf, from, to, key, string(raw), ms, ms)
	if err != nil {
		return 0, dbErr(ctx, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// A concurrent enqueue with the same key won the race; return that row without delivering again.
		id, _, err := d.outboxByKey(ctx, key)
		return id, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, dbErr(ctx, err)
	}
	return id, d.runJob(ctx, id)
}

// Flush attempts pending/failed rows of a sink, oldest first (explicit triggers only). Dead rows are skipped.
func (d *dispatcher) Flush(ctx context.Context, sinkID int64) error {
	if _, err := d.loadSink(ctx, sinkID); err != nil {
		return err
	}
	rows, err := d.db.W.QueryContext(ctx,
		`SELECT id FROM sink_outbox WHERE sink_id=? AND status IN ('pending','failed') ORDER BY id`, sinkID)
	if err != nil {
		return dbErr(ctx, err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return dbErr(ctx, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return dbErr(ctx, err)
	}
	for _, id := range ids {
		if err := d.runJob(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// Retry re-attempts one row, including dead rows (a dead row that fails again stays dead).
func (d *dispatcher) Retry(ctx context.Context, outboxID int64) error {
	var status string
	err := d.db.W.QueryRowContext(ctx, `SELECT status FROM sink_outbox WHERE id=?`, outboxID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.New(apperr.NotFound, "The delivery was not found.")
	}
	if err != nil {
		return dbErr(ctx, err)
	}
	if status == "delivered" {
		return apperr.New(apperr.Conflict, "This delivery has already been sent.")
	}
	return d.runJob(ctx, outboxID)
}

// AfterSync enqueues for every enabled after_sync sink covering standupID, flushing each sink's older failed rows
// first. A failing sink never fails the sync; only a cancelled context is returned.
func (d *dispatcher) AfterSync(ctx context.Context, standupID, runID int64) error {
	rows, err := d.db.W.QueryContext(ctx, `SELECT id FROM sink
		WHERE send_on='after_sync' AND enabled=1 AND (standup_id IS NULL OR standup_id=?) ORDER BY id`, standupID)
	if err != nil {
		return dbErr(ctx, err)
	}
	var sinks []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return dbErr(ctx, err)
		}
		sinks = append(sinks, id)
	}
	if err := rows.Close(); err != nil {
		return dbErr(ctx, err)
	}
	for _, sinkID := range sinks {
		if err := d.Flush(ctx, sinkID); err != nil {
			if ctx.Err() != nil {
				return err
			}
			d.log.Warn("sink flush after sync failed", "sink", sinkID, "code", apperr.CodeOf(err))
		}
		key := fmt.Sprintf("sync:%d:%d", runID, sinkID)
		if _, err := d.Enqueue(ctx, sinkID, standupID, Request{Kind: "current"}, "after_sync", key); err != nil {
			if ctx.Err() != nil {
				return err
			}
			d.log.Warn("sink enqueue after sync failed", "sink", sinkID, "code", apperr.CodeOf(err))
		}
	}
	return nil
}

// runJob makes one delivery job for an outbox row: up to triesPerJob tries, bounded by the 8-attempt limit.
// It records the outcome on the row and calls Notify exactly once.
func (d *dispatcher) runJob(ctx context.Context, outboxID int64) error {
	d.jobMu.Lock()
	defer d.jobMu.Unlock()

	row, err := d.loadRow(ctx, outboxID)
	if err != nil {
		return err
	}
	if row.status == "delivered" {
		return nil
	}
	s, err := d.loadSink(ctx, row.sinkID)
	if err != nil {
		return err
	}
	kind, ok := d.lookup(s.kind)
	if !ok {
		return apperr.New(apperr.Unsupported, "This sink type is not available in this build.")
	}
	var doc export.Document
	if err := json.Unmarshal([]byte(row.payload), &doc); err != nil {
		return apperr.Wrap(apperr.Internal, "The stored export is unreadable.", err)
	}
	standupKey := doc.Standup.Space.Key
	if standupKey == "" {
		standupKey = fmt.Sprintf("standup-%d", row.standupID)
	}
	date := ""
	if len(doc.Snapshots) > 0 {
		date = doc.Snapshots[0].Date
	}
	secret, err := d.secret(row.sinkID, kind)
	if err != nil {
		return err
	}

	budget := maxAttempts - row.attempts
	if budget > triesPerJob {
		budget = triesPerJob
	}
	if budget < 1 {
		budget = 1 // a dead row retried explicitly gets one attempt
	}
	attempts := row.attempts
	var receipt Receipt
	var lastErr *apperr.Error
	for try := 0; try < budget; try++ {
		attempts++
		if _, err := d.db.W.ExecContext(ctx,
			`UPDATE sink_outbox SET status='delivering', attempts=?, updated_at=? WHERE id=?`,
			attempts, d.now().UnixMilli(), row.id); err != nil {
			return dbErr(ctx, err)
		}
		receipt, err = kind.Deliver(ctx, Delivery{
			Key:        row.key,
			Attempt:    attempts,
			Config:     s.config,
			Secret:     secret,
			Document:   doc,
			Raw:        []byte(row.payload),
			StandupKey: standupKey,
			Date:       date,
		})
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = apperr.From(err)
		d.log.Debug("sink delivery attempt failed", "outbox", row.id, "sink", row.sinkID, "attempt", attempts,
			"code", lastErr.Code, "detail", lastErr.Detail)
		if try < budget-1 {
			if ctx.Err() != nil {
				lastErr = apperr.From(ctx.Err())
				break
			}
			if !d.wait(ctx, backoffs[try]) {
				lastErr = apperr.From(ctx.Err())
				break
			}
		}
	}

	// The final status is written even when the caller's context was cancelled, so a row is never stranded in
	// 'delivering'.
	ctx = context.WithoutCancel(ctx)
	if lastErr == nil {
		return d.finish(ctx, row, attempts, receipt)
	}
	status := statusFailed
	if attempts >= maxAttempts {
		status = statusDead
	}
	if _, err := d.db.W.ExecContext(ctx, `UPDATE sink_outbox SET status=?, attempts=?, last_error_code=?,
		last_error=?, updated_at=? WHERE id=?`,
		status, attempts, string(lastErr.Code), lastErr.Message, d.now().UnixMilli(), row.id); err != nil {
		return dbErr(ctx, err)
	}
	d.report(Event{
		OutboxID: row.id, SinkID: row.sinkID, StandupID: row.standupID, Kind: row.kind,
		ErrorCode: string(lastErr.Code), ErrorMessage: lastErr.Message, Attempts: attempts,
		WillRetry: status != statusDead,
	})
	return nil
}

// wait sleeps d between tries and returns false as soon as ctx ends. A custom Sleep hook (tests) runs in its own
// goroutine so a blocking hook cannot hold the job past cancellation.
func (d *dispatcher) wait(ctx context.Context, dur time.Duration) bool {
	if d.sleep == nil {
		t := time.NewTimer(dur)
		defer t.Stop()
		select {
		case <-t.C:
			return true
		case <-ctx.Done():
			return false
		}
	}
	done := make(chan struct{})
	go func() {
		d.sleep(dur)
		close(done)
	}()
	select {
	case <-done:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

// finish marks a successful job delivered and reports it.
func (d *dispatcher) finish(ctx context.Context, row outboxRow, attempts int, rc Receipt) error {
	ms := d.now().UnixMilli()
	if _, err := d.db.W.ExecContext(ctx, `UPDATE sink_outbox SET status='delivered', attempts=?, receipt=?,
		last_error_code=NULL, last_error=NULL, delivered_at=?, updated_at=? WHERE id=?`,
		attempts, rc.Ref, ms, ms, row.id); err != nil {
		return dbErr(ctx, err)
	}
	d.report(Event{
		Delivered: true, OutboxID: row.id, SinkID: row.sinkID, StandupID: row.standupID, Kind: row.kind,
		Ref: rc.Ref, Attempts: attempts,
	})
	return nil
}

func (d *dispatcher) report(ev Event) {
	if d.notify != nil {
		d.notify(ev)
	}
}

// secret reads sink:<id> from the keychain. An absent item is an empty secret. An unavailable keychain fails the
// job only when the kind needs a secret.
func (d *dispatcher) secret(sinkID int64, kind Sink) (string, error) {
	v, err := d.kr.Get(secrets.SinkAccount(sinkID))
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, secrets.ErrNotFound):
		return "", nil
	case kind.Capabilities().NeedsSecret:
		return "", apperr.Wrap(apperr.KeychainUnavailable, "The keychain is not available.", err)
	default:
		d.log.Debug("sink secret unavailable; continuing without it", "sink", sinkID, "err", err)
		return "", nil
	}
}

type sinkRow struct {
	id     int64
	kind   string
	config map[string]string
}

func (d *dispatcher) loadSink(ctx context.Context, id int64) (sinkRow, error) {
	var r sinkRow
	var cfg string
	err := d.db.W.QueryRowContext(ctx, `SELECT id, kind, config FROM sink WHERE id=?`, id).
		Scan(&r.id, &r.kind, &cfg)
	if errors.Is(err, sql.ErrNoRows) {
		return r, apperr.New(apperr.NotFound, "The sink was not found.")
	}
	if err != nil {
		return r, dbErr(ctx, err)
	}
	r.config = map[string]string{}
	if err := json.Unmarshal([]byte(cfg), &r.config); err != nil {
		return r, apperr.Wrap(apperr.Internal, "The sink configuration is unreadable.", err)
	}
	return r, nil
}

type outboxRow struct {
	id        int64
	sinkID    int64
	standupID int64
	kind      string
	key       string
	payload   string
	status    string
	attempts  int
}

func (d *dispatcher) loadRow(ctx context.Context, id int64) (outboxRow, error) {
	var r outboxRow
	err := d.db.W.QueryRowContext(ctx, `SELECT id, sink_id, standup_id, payload_kind, idempotency_key, payload,
		status, attempts FROM sink_outbox WHERE id=?`, id).
		Scan(&r.id, &r.sinkID, &r.standupID, &r.kind, &r.key, &r.payload, &r.status, &r.attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return r, apperr.New(apperr.NotFound, "The delivery was not found.")
	}
	if err != nil {
		return r, dbErr(ctx, err)
	}
	return r, nil
}

func (d *dispatcher) outboxByKey(ctx context.Context, key string) (int64, bool, error) {
	var id int64
	err := d.db.W.QueryRowContext(ctx, `SELECT id FROM sink_outbox WHERE idempotency_key=?`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, dbErr(ctx, err)
	}
	return id, true, nil
}

// dbErr maps a database error: cancellation stays cancelled, anything else is a retryable db error.
func dbErr(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return apperr.Wrap(apperr.Cancelled, "Cancelled.", err)
	}
	return apperr.Wrap(apperr.DB, "The delivery could not be recorded.", err)
}

// newKey returns a random idempotency key for a manual send.
func newKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", apperr.Wrap(apperr.Internal, "A delivery key could not be created.", err)
	}
	return hex.EncodeToString(b), nil
}

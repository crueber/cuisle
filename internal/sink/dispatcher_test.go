package sink_test

// SPEC-NOTE (WP-21 dispatcher), readings pinned by these tests where data-model.md §2.8 / architecture.md §2.3 are silent:
//
//  1. A delivery job that fails is NOT an error for Enqueue/Flush/Retry/AfterSync. The row records the failure and
//     Notify reports it (SinkService.Send "returns outbox id"; failure surfaces through the sink:failed event and the
//     outbox row). Errors are returned only when the job could not even be set up (unknown sink, build failure, DB).
//  2. One Enqueue / Flush row / Retry is ONE delivery job: up to 3 tries with Sleep(1s) after try 1 and Sleep(4s) after
//     try 2 (no sleep after the last try), and exactly one Notify per job. Backoff restarts per job.
//  3. Delivery.Attempt is cumulative and equals the outbox `attempts` value after incrementing (1, 2, 3, then 4, 5, 6 ...).
//  4. "Dead after 8 attempts" means the 8th failed attempt is the last: Enqueue (tries 1-3), Flush (tries 4-6), Flush
//     (tries 7-8 only, with a single 1s sleep) -> dead, Notify WillRetry=false, and no 9th Deliver ever happens via
//     Flush. Retry(outboxID) re-attempts dead rows (a dead row that fails again stays dead).
//  5. Retries use the frozen payload: BuildFunc is called exactly once per Enqueue (never by Flush/Retry). Delivery.Raw
//     is the stored payload bytes; Delivery.Document is decoded from them; Delivery.StandupKey is Document.Standup.Space.Key
//     (or "standup-<standupID>" when empty) and Delivery.Date is Document.Snapshots[0].Date, because sink_outbox has no
//     columns for them. The fake BuildFunc returns values that agree with the document so first attempts and retries match.
//  6. A duplicate idempotency key on Enqueue returns the existing outbox id and does not deliver (or build-and-store)
//     again, regardless of the existing row's status.
//  7. NewDispatcher is the startup hook that resets `delivering` rows to `failed` (no delivery is attempted).
//  8. AfterSync always builds Request{Kind:"current"}, uses cause "after_sync" and key "sync:<runID>:<sinkID>", flushes the
//     sink's older failed/pending rows first, and returns nil even when a delivery fails (the sync must not fail).
//     Sinks are processed in ascending sink id.
//  9. Enqueue's DispatcherOptions.Lookup (nil = sink.Get) resolves the adapter kind of a sink row.
// 10. The delivered row stores Receipt.Ref in `receipt` and the clock's now (ms) in delivered_at.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/export"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/sink"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

const fakeKind = "faketest-wp21"

var fixedNow = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

const (
	insertStandupSQL = `INSERT INTO standup(id,name,position,created_at,updated_at) VALUES(?,?,?,?,?)`
	insertSinkSQL    = `INSERT INTO sink(id,kind,name,standup_id,config,send_on,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`
	insertOutboxSQL  = `INSERT INTO sink_outbox(id,sink_id,standup_id,cause,payload_kind,idempotency_key,payload,status,attempts,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`
)

// ---- fakes -----------------------------------------------------------------

type memKeyring struct {
	mu    sync.Mutex
	items map[string]string
	err   error // returned by every call when set
}

func newKeyring() *memKeyring { return &memKeyring{items: map[string]string{}} }

func (k *memKeyring) Get(a string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return "", k.err
	}
	v, ok := k.items[a]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (k *memKeyring) Set(a, s string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return k.err
	}
	k.items[a] = s
	return nil
}
func (k *memKeyring) Delete(a string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.err != nil {
		return k.err
	}
	delete(k.items, a)
	return nil
}
func (k *memKeyring) has(a string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.items[a]
	return ok
}

// fakeSink is a programmable sink.Sink. failNext>0: the next N Deliver calls fail; -1: fail forever.
type fakeSink struct {
	mu       sync.Mutex
	kind     string
	calls    []sink.Delivery
	failNext int
}

func (f *fakeSink) Kind() string        { return f.kind }
func (f *fakeSink) DisplayName() string { return "Fake" }
func (f *fakeSink) Capabilities() sink.Capabilities {
	return sink.Capabilities{Current: true, Formats: []string{"json"}, Idempotent: true}
}
func (f *fakeSink) ConfigSchema() []sink.Field {
	return []sink.Field{{Key: "target", Label: "Target", Type: "text", Required: true}}
}
func (f *fakeSink) Validate(c map[string]string) error {
	if strings.TrimSpace(c["target"]) == "" {
		return apperr.Validationf("target", "Target is required.")
	}
	return nil
}
func (f *fakeSink) Deliver(_ context.Context, d sink.Delivery) (sink.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, d)
	if f.failNext != 0 {
		if f.failNext > 0 {
			f.failNext--
		}
		return sink.Receipt{}, apperr.New(apperr.SinkFailed, "fake sink exploded")
	}
	return sink.Receipt{Ref: "ref:" + d.Key, Detail: "ok"}, nil
}
func (f *fakeSink) setFail(n int) { f.mu.Lock(); f.failNext = n; f.mu.Unlock() }
func (f *fakeSink) count() int    { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }
func (f *fakeSink) keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.Key)
	}
	return out
}
func (f *fakeSink) call(i int) sink.Delivery { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[i] }

// registerFake registers a fresh fake under fakeKind in the global registry (SaveConfig resolves kinds via sink.Get).
func registerFake(t testing.TB) *fakeSink {
	t.Helper()
	f := &fakeSink{kind: fakeKind}
	sink.Register(f)
	return f
}

// ---- environment -----------------------------------------------------------

type buildCall struct {
	StandupID int64
	Req       sink.Request
}

type env struct {
	t      *testing.T
	db     *store.DB
	kr     *memKeyring
	fs     *fakeSink
	now    time.Time
	mu     sync.Mutex
	sleeps []time.Duration
	events []sink.Event
	builds []buildCall
	d      sink.Dispatcher
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newEnvNoDispatcher opens a DB with standups 1 and 2 and registers the fake; call start() to build the dispatcher.
func newEnvNoDispatcher(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, kr: newKeyring(), now: fixedNow}
	e.fs = registerFake(t)
	e.db = storetest.Open(t, func() time.Time { return e.now })
	ms := fixedNow.UnixMilli()
	storetest.Exec(t, e.db, insertStandupSQL, 1, "Alpha", 1, ms, ms)
	storetest.Exec(t, e.db, insertStandupSQL, 2, "Beta", 2, ms, ms)
	return e
}

func (e *env) build(_ context.Context, standupID int64, req sink.Request) (export.Document, []byte, string, string, error) {
	e.mu.Lock()
	e.builds = append(e.builds, buildCall{standupID, req})
	n := len(e.builds)
	e.mu.Unlock()
	doc := export.Document{
		Schema:    export.Schema,
		Kind:      req.Kind,
		Generator: export.Generator{Name: "Cuisle", Version: fmt.Sprintf("build-%d", n)},
		Standup:   export.StandupRef{ID: standupID, Name: "S", Space: export.SpaceRef{Key: "MOB"}},
		Snapshots: []export.Snapshot{{Date: "2026-11-23"}},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		e.t.Fatal(err)
	}
	return doc, raw, "MOB", "2026-11-23", nil
}

func (e *env) options(notify func(sink.Event)) sink.DispatcherOptions {
	return sink.DispatcherOptions{
		Logger: discardLogger(),
		Now:    func() time.Time { return e.now },
		Sleep:  func(d time.Duration) { e.mu.Lock(); e.sleeps = append(e.sleeps, d); e.mu.Unlock() },
		Notify: notify,
	}
}

func (e *env) start() *env {
	e.d = sink.NewDispatcher(e.db, e.kr, e.build, e.options(func(ev sink.Event) {
		e.mu.Lock()
		e.events = append(e.events, ev)
		e.mu.Unlock()
	}))
	return e
}

func newEnv(t *testing.T) *env { return newEnvNoDispatcher(t).start() }

func (e *env) addSink(id int64, standupID int64, sendOn string, enabled bool) {
	e.t.Helper()
	var sid any
	if standupID != 0 {
		sid = standupID
	}
	en := 0
	if enabled {
		en = 1
	}
	ms := fixedNow.UnixMilli()
	storetest.Exec(e.t, e.db, insertSinkSQL, id, fakeKind, fmt.Sprintf("Sink %d", id), sid, `{"target":"x"}`, sendOn, en, ms, ms)
}

func (e *env) addOutbox(id, sinkID, standupID int64, key, status string, attempts int) {
	e.t.Helper()
	payload := `{"schema":"cuisle.export.v1","generator":{"name":"Cuisle","version":"seeded"}}`
	ms := fixedNow.UnixMilli() - 1000 + id
	storetest.Exec(e.t, e.db, insertOutboxSQL, id, sinkID, standupID, "manual", "current", key, payload, status, attempts, ms, ms)
}

type obRow struct {
	ID        int64
	SinkID    int64
	StandupID int64
	Cause     string
	Kind      string
	Key       string
	Payload   string
	Status    string
	Attempts  int
	ErrCode   string
	Err       string
	Receipt   string
	CreatedAt int64
	Delivered sql.NullInt64
}

func (e *env) row(id int64) obRow {
	e.t.Helper()
	var r obRow
	var code, msg, rec sql.NullString
	err := e.db.W.QueryRow(`SELECT id,sink_id,standup_id,cause,payload_kind,idempotency_key,payload,status,attempts,
		last_error_code,last_error,receipt,created_at,delivered_at FROM sink_outbox WHERE id=?`, id).
		Scan(&r.ID, &r.SinkID, &r.StandupID, &r.Cause, &r.Kind, &r.Key, &r.Payload, &r.Status, &r.Attempts,
			&code, &msg, &rec, &r.CreatedAt, &r.Delivered)
	if err != nil {
		e.t.Fatalf("read outbox row %d: %v", id, err)
	}
	r.ErrCode, r.Err, r.Receipt = code.String, msg.String, rec.String
	return r
}

func (e *env) rowCount() int { return storetest.Count(e.t, e.db, "sink_outbox", "") }

func (e *env) resetRecords() {
	e.mu.Lock()
	e.sleeps, e.events = nil, nil
	e.mu.Unlock()
}

func (e *env) eventList() []sink.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sink.Event(nil), e.events...)
}
func (e *env) sleepList() []time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]time.Duration(nil), e.sleeps...)
}
func (e *env) buildCount() int { e.mu.Lock(); defer e.mu.Unlock(); return len(e.builds) }

var cur = sink.Request{Kind: "current"}

func equalDurations(a, b []time.Duration) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// ---- tests -----------------------------------------------------------------

func TestRegistry_RegisterGetKinds_Smoke(t *testing.T) {
	f := registerFake(t)
	got, ok := sink.Get(fakeKind)
	if !ok || got != sink.Sink(f) {
		t.Fatalf("Get(%q) = %v, %v", fakeKind, got, ok)
	}
	found := false
	for _, s := range sink.Kinds() {
		if s.Kind() == fakeKind {
			found = true
		}
	}
	if !found {
		t.Error("Kinds() does not list the registered fake")
	}
}

func TestEnqueue_Success_DeliversOnceAndMarksDelivered(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	if err := e.kr.Set("sink:1", "s3cret"); err != nil {
		t.Fatal(err)
	}

	id, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "key-1")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if id <= 0 {
		t.Fatalf("outbox id = %d", id)
	}
	if e.fs.count() != 1 {
		t.Fatalf("Deliver calls = %d, want 1", e.fs.count())
	}
	d := e.fs.call(0)
	if d.Key != "key-1" || d.Attempt != 1 {
		t.Errorf("Delivery key/attempt = %q/%d, want key-1/1", d.Key, d.Attempt)
	}
	if d.Secret != "s3cret" {
		t.Errorf("Delivery.Secret = %q, want the keyring value", d.Secret)
	}
	if d.Config["target"] != "x" {
		t.Errorf("Delivery.Config = %v", d.Config)
	}
	if d.StandupKey != "MOB" || d.Date != "2026-11-23" {
		t.Errorf("StandupKey/Date = %q/%q", d.StandupKey, d.Date)
	}
	if d.Document.Schema != export.Schema || d.Document.Generator.Version != "build-1" {
		t.Errorf("Delivery.Document = %+v", d.Document)
	}
	r := e.row(id)
	if string(d.Raw) != r.Payload || !strings.Contains(r.Payload, `"build-1"`) {
		t.Errorf("Raw %q must equal the stored payload %q", d.Raw, r.Payload)
	}
	if r.Status != "delivered" || r.Attempts != 1 || r.Receipt != "ref:key-1" {
		t.Errorf("row = %+v", r)
	}
	if r.Cause != "manual" || r.Kind != "current" || r.SinkID != 1 || r.StandupID != 1 || r.Key != "key-1" {
		t.Errorf("row metadata = %+v", r)
	}
	if r.CreatedAt != fixedNow.UnixMilli() || !r.Delivered.Valid || r.Delivered.Int64 != fixedNow.UnixMilli() {
		t.Errorf("timestamps created=%d delivered=%v, want %d", r.CreatedAt, r.Delivered, fixedNow.UnixMilli())
	}
	if len(e.sleepList()) != 0 {
		t.Errorf("sleeps = %v, want none on first-try success", e.sleepList())
	}
	evs := e.eventList()
	if len(evs) != 1 {
		t.Fatalf("events = %+v, want exactly one", evs)
	}
	ev := evs[0]
	if !ev.Delivered || ev.OutboxID != id || ev.SinkID != 1 || ev.StandupID != 1 || ev.Kind != "current" ||
		ev.Ref != "ref:key-1" || ev.Attempts != 1 {
		t.Errorf("event = %+v", ev)
	}
	bs := e.buildCount()
	if bs != 1 {
		t.Errorf("builds = %d, want 1", bs)
	}
	if e.builds[0].StandupID != 1 || e.builds[0].Req != cur {
		t.Errorf("build call = %+v", e.builds[0])
	}
}

func TestEnqueue_RangeRequest_PassesRequestToBuildAndStoresKind(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	req := sink.Request{Kind: "range", From: "2026-11-02", To: "2026-11-06"}
	id, err := e.d.Enqueue(context.Background(), 1, 1, req, "manual", "r1")
	if err != nil {
		t.Fatal(err)
	}
	if e.builds[0].Req != req {
		t.Errorf("build req = %+v, want %+v", e.builds[0].Req, req)
	}
	var kind, from, to sql.NullString
	if err := e.db.W.QueryRow(`SELECT payload_kind, range_from, range_to FROM sink_outbox WHERE id=?`, id).Scan(&kind, &from, &to); err != nil {
		t.Fatal(err)
	}
	if kind.String != "range" || from.String != "2026-11-02" || to.String != "2026-11-06" {
		t.Errorf("stored kind/from/to = %q/%q/%q", kind.String, from.String, to.String)
	}
}

func TestEnqueue_EmptyKey_GeneratesUniqueKeys(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	id1, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "")
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Fatal("two manual sends with empty key must create two rows")
	}
	k1, k2 := e.row(id1).Key, e.row(id2).Key
	if k1 == "" || k2 == "" || k1 == k2 {
		t.Errorf("generated keys must be non-empty and distinct: %q %q", k1, k2)
	}
	if got := e.fs.keys(); !reflect.DeepEqual(got, []string{k1, k2}) {
		t.Errorf("Delivery.Key = %v, want the stored keys [%s %s]", got, k1, k2)
	}
	if e.fs.count() != 2 {
		t.Errorf("Deliver calls = %d, want 2", e.fs.count())
	}
}

func TestEnqueue_DuplicateKey_ReturnsExistingWithoutRedelivering(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	id1, err := e.d.Enqueue(context.Background(), 1, 1, cur, "after_sync", "sync:9:1")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := e.d.Enqueue(context.Background(), 1, 1, cur, "after_sync", "sync:9:1")
	if err != nil {
		t.Fatalf("duplicate Enqueue must not error: %v", err)
	}
	if id2 != id1 {
		t.Errorf("duplicate key returned id %d, want existing %d", id2, id1)
	}
	if e.fs.count() != 1 {
		t.Errorf("Deliver calls = %d, want 1 (no second delivery)", e.fs.count())
	}
	if n := e.rowCount(); n != 1 {
		t.Errorf("outbox rows = %d, want 1", n)
	}
	if !strings.Contains(e.row(id1).Payload, `"build-1"`) {
		t.Errorf("frozen payload was replaced: %s", e.row(id1).Payload)
	}
}

func TestEnqueue_DuplicateKeyOfFailedRow_DoesNotRedeliver(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.fs.setFail(-1)
	id1, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "dup")
	if err != nil {
		t.Fatal(err)
	}
	before := e.fs.count()
	id2, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "dup")
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id1 || e.fs.count() != before || e.rowCount() != 1 {
		t.Errorf("id2=%d (want %d), calls %d -> %d, rows=%d", id2, id1, before, e.fs.count(), e.rowCount())
	}
}

func TestEnqueue_FailingSink_ThreeTriesWithBackoffThenFailed(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.fs.setFail(-1)

	id, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "f1")
	if err != nil {
		t.Fatalf("a failed delivery job is not an Enqueue error: %v", err)
	}
	if e.fs.count() != 3 {
		t.Fatalf("Deliver calls = %d, want 3 tries in one job", e.fs.count())
	}
	for i := 0; i < 3; i++ {
		if a := e.fs.call(i).Attempt; a != i+1 {
			t.Errorf("call %d Attempt = %d, want %d", i, a, i+1)
		}
	}
	if got, want := e.sleepList(), []time.Duration{time.Second, 4 * time.Second}; !equalDurations(got, want) {
		t.Errorf("sleeps = %v, want %v (no sleep after the last try)", got, want)
	}
	r := e.row(id)
	if r.Status != "failed" || r.Attempts != 3 {
		t.Errorf("row status/attempts = %s/%d, want failed/3", r.Status, r.Attempts)
	}
	if r.ErrCode != string(apperr.SinkFailed) || !strings.Contains(r.Err, "fake sink exploded") {
		t.Errorf("row error = %q / %q", r.ErrCode, r.Err)
	}
	if r.Delivered.Valid || r.Receipt != "" {
		t.Errorf("failed row must have no delivered_at/receipt: %+v", r)
	}
	evs := e.eventList()
	if len(evs) != 1 {
		t.Fatalf("events = %+v, want one per job", evs)
	}
	ev := evs[0]
	if ev.Delivered || !ev.WillRetry || ev.Attempts != 3 || ev.OutboxID != id || ev.SinkID != 1 || ev.StandupID != 1 ||
		ev.ErrorCode != string(apperr.SinkFailed) || ev.ErrorMessage == "" || ev.Kind != "current" {
		t.Errorf("event = %+v", ev)
	}
}

func TestEnqueue_SecondTrySucceeds_DeliveredAfterOneBackoff(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.fs.setFail(1)
	id, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "s2")
	if err != nil {
		t.Fatal(err)
	}
	r := e.row(id)
	if r.Status != "delivered" || r.Attempts != 2 {
		t.Errorf("row = %s/%d, want delivered/2", r.Status, r.Attempts)
	}
	if got := e.sleepList(); !equalDurations(got, []time.Duration{time.Second}) {
		t.Errorf("sleeps = %v, want [1s]", got)
	}
	evs := e.eventList()
	if len(evs) != 1 || !evs[0].Delivered || evs[0].Attempts != 2 {
		t.Errorf("events = %+v", evs)
	}
}

func TestEnqueue_FailedRow_NoFurtherAttemptWithoutTrigger(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.fs.setFail(-1)
	if _, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "nt"); err != nil {
		t.Fatal(err)
	}
	calls, sleeps, events := e.fs.count(), len(e.sleepList()), len(e.eventList())
	e.now = e.now.Add(72 * time.Hour) // time passes; nothing may react to it (no timers)
	for i := 0; i < 20; i++ {
		runtime.Gosched()
	}
	if e.fs.count() != calls || len(e.sleepList()) != sleeps || len(e.eventList()) != events {
		t.Errorf("activity without a trigger: calls %d->%d sleeps %d->%d events %d->%d",
			calls, e.fs.count(), sleeps, len(e.sleepList()), events, len(e.eventList()))
	}
}

func TestEnqueue_UnknownSink_NotFound(t *testing.T) {
	e := newEnv(t)
	_, err := e.d.Enqueue(context.Background(), 99, 1, cur, "manual", "x")
	if !apperr.Is(err, apperr.NotFound) {
		t.Fatalf("err = %v (code %q), want not_found", err, apperr.CodeOf(err))
	}
	if e.rowCount() != 0 || e.fs.count() != 0 {
		t.Error("nothing may be written or delivered for an unknown sink")
	}
}

func TestEnqueue_BuildError_ReturnedAndNoRowWritten(t *testing.T) {
	e := newEnvNoDispatcher(t)
	e.addSink(1, 0, "manual", true)
	boom := apperr.New(apperr.Internal, "build failed")
	d := sink.NewDispatcher(e.db, e.kr, func(context.Context, int64, sink.Request) (export.Document, []byte, string, string, error) {
		return export.Document{}, nil, "", "", boom
	}, e.options(nil))
	_, err := d.Enqueue(context.Background(), 1, 1, cur, "manual", "b")
	if err == nil || !errors.Is(err, boom) && !apperr.Is(err, apperr.Internal) {
		t.Fatalf("err = %v, want the build error", err)
	}
	if e.rowCount() != 0 || e.fs.count() != 0 {
		t.Errorf("rows=%d calls=%d, want 0/0", e.rowCount(), e.fs.count())
	}
}

func TestEnqueue_NilNotify_DoesNotPanic(t *testing.T) {
	e := newEnvNoDispatcher(t)
	e.addSink(1, 0, "manual", true)
	d := sink.NewDispatcher(e.db, e.kr, e.build, e.options(nil))
	if _, err := d.Enqueue(context.Background(), 1, 1, cur, "manual", "nn"); err != nil {
		t.Fatal(err)
	}
	e.fs.setFail(-1)
	if _, err := d.Enqueue(context.Background(), 1, 1, cur, "manual", "nn2"); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueue_UsesLookupOptionForSinkKind(t *testing.T) {
	e := newEnvNoDispatcher(t)
	const kind = "lookup-only-wp21"
	local := &fakeSink{kind: kind}
	ms := fixedNow.UnixMilli()
	storetest.Exec(t, e.db, insertSinkSQL, 1, kind, "L", nil, `{"target":"x"}`, "manual", 1, ms, ms)
	opts := e.options(nil)
	asked := ""
	opts.Lookup = func(k string) (sink.Sink, bool) {
		asked = k
		if k == kind {
			return local, true
		}
		return nil, false
	}
	d := sink.NewDispatcher(e.db, e.kr, e.build, opts)
	id, err := d.Enqueue(context.Background(), 1, 1, cur, "manual", "lk")
	if err != nil {
		t.Fatal(err)
	}
	if asked != kind || local.count() != 1 || e.row(id).Status != "delivered" {
		t.Errorf("asked=%q calls=%d status=%s", asked, local.count(), e.row(id).Status)
	}
}

func TestFlush_RetriesFailedRows_OldestFirstUsingFrozenPayload(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.fs.setFail(-1)
	id1, _ := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "k1")
	id2, _ := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "k2")
	if e.fs.count() != 6 || e.buildCount() != 2 {
		t.Fatalf("setup: calls=%d builds=%d", e.fs.count(), e.buildCount())
	}
	e.fs.setFail(0) // sink recovers
	e.resetRecords()

	if err := e.d.Flush(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if e.buildCount() != 2 {
		t.Errorf("Flush must not rebuild payloads (builds=%d)", e.buildCount())
	}
	if got := e.fs.keys()[6:]; !reflect.DeepEqual(got, []string{"k1", "k2"}) {
		t.Errorf("flush order = %v, want oldest first [k1 k2]", got)
	}
	first := e.fs.call(6)
	if first.Document.Generator.Version != "build-1" || !strings.Contains(string(first.Raw), `"build-1"`) {
		t.Errorf("retry saw %q / %s, want the frozen first payload", first.Document.Generator.Version, first.Raw)
	}
	if first.Attempt != 4 {
		t.Errorf("retry Delivery.Attempt = %d, want cumulative 4", first.Attempt)
	}
	if first.StandupKey != "MOB" || first.Date != "2026-11-23" {
		t.Errorf("retry StandupKey/Date = %q/%q", first.StandupKey, first.Date)
	}
	for _, id := range []int64{id1, id2} {
		r := e.row(id)
		if r.Status != "delivered" || r.Attempts != 4 {
			t.Errorf("row %d = %s/%d, want delivered/4", id, r.Status, r.Attempts)
		}
	}
	evs := e.eventList()
	if len(evs) != 2 || !evs[0].Delivered || !evs[1].Delivered {
		t.Errorf("events = %+v, want one delivered event per row", evs)
	}
}

func TestFlush_OnlyTouchesGivenSink(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.addSink(2, 0, "manual", true)
	e.addOutbox(1, 1, 1, "s1-old", "failed", 3)
	e.addOutbox(2, 2, 1, "s2-old", "failed", 3)
	if err := e.d.Flush(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if got := e.fs.keys(); !reflect.DeepEqual(got, []string{"s2-old"}) {
		t.Errorf("delivered keys = %v, want only s2-old", got)
	}
	if e.row(1).Status != "failed" || e.row(2).Status != "delivered" {
		t.Errorf("statuses = %s / %s", e.row(1).Status, e.row(2).Status)
	}
}

func TestFlush_AttemptsPendingAndFailed_SkipsDeliveredAndDead(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.addOutbox(1, 1, 1, "done", "delivered", 1)
	e.addOutbox(2, 1, 1, "dead", "dead", 8)
	e.addOutbox(3, 1, 1, "pend", "pending", 0)
	e.addOutbox(4, 1, 1, "fail", "failed", 3)
	if err := e.d.Flush(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if got := e.fs.keys(); !reflect.DeepEqual(got, []string{"pend", "fail"}) {
		t.Errorf("delivered keys = %v, want [pend fail] in id order", got)
	}
	if e.row(2).Status != "dead" || e.row(2).Attempts != 8 || e.row(1).Attempts != 1 {
		t.Error("dead and delivered rows must be untouched")
	}
}

func TestFlush_FailingSink_ReturnsNilAndKeepsFailed(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.addOutbox(1, 1, 1, "fail", "failed", 3)
	e.fs.setFail(-1)
	if err := e.d.Flush(context.Background(), 1); err != nil {
		t.Fatalf("Flush must not return delivery failures: %v", err)
	}
	if r := e.row(1); r.Status != "failed" || r.Attempts != 6 {
		t.Errorf("row = %s/%d, want failed/6", r.Status, r.Attempts)
	}
}

func TestFlush_DerivesStandupKeyFromDocumentOrFallsBackToStandupID(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	// seeded payload has no space key and no snapshots
	e.addOutbox(1, 1, 1, "seeded", "failed", 3)
	if err := e.d.Flush(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	d := e.fs.call(0)
	if d.StandupKey != "standup-1" {
		t.Errorf("StandupKey = %q, want standup-1 fallback", d.StandupKey)
	}
	if d.Document.Generator.Version != "seeded" || !strings.Contains(string(d.Raw), `"seeded"`) {
		t.Errorf("Document/Raw not taken from the stored payload: %+v / %s", d.Document.Generator, d.Raw)
	}
}

func TestDispatcher_EightAttempts_RowBecomesDeadAndStops(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.fs.setFail(-1)
	id, err := e.d.Enqueue(context.Background(), 1, 1, cur, "manual", "dd")
	if err != nil {
		t.Fatal(err)
	}
	if r := e.row(id); r.Status != "failed" || r.Attempts != 3 {
		t.Fatalf("after job 1: %s/%d", r.Status, r.Attempts)
	}

	e.resetRecords()
	if err := e.d.Flush(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if r := e.row(id); r.Status != "failed" || r.Attempts != 6 {
		t.Fatalf("after job 2: %s/%d, want failed/6", r.Status, r.Attempts)
	}
	if got := e.sleepList(); !equalDurations(got, []time.Duration{time.Second, 4 * time.Second}) {
		t.Errorf("job 2 sleeps = %v, want [1s 4s]", got)
	}
	if evs := e.eventList(); len(evs) != 1 || !evs[0].WillRetry || evs[0].Attempts != 6 {
		t.Errorf("job 2 events = %+v", evs)
	}

	e.resetRecords()
	if err := e.d.Flush(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	r := e.row(id)
	if r.Status != "dead" || r.Attempts != 8 {
		t.Fatalf("after job 3: %s/%d, want dead/8", r.Status, r.Attempts)
	}
	if e.fs.count() != 8 {
		t.Errorf("total Deliver calls = %d, want exactly 8", e.fs.count())
	}
	if got := e.sleepList(); !equalDurations(got, []time.Duration{time.Second}) {
		t.Errorf("job 3 sleeps = %v, want [1s] (tries 7 and 8 only)", got)
	}
	evs := e.eventList()
	if len(evs) != 1 || evs[0].Delivered || evs[0].WillRetry || evs[0].Attempts != 8 || evs[0].OutboxID != id {
		t.Errorf("job 3 events = %+v, want one failure with WillRetry=false", evs)
	}

	// Flush skips dead rows.
	if err := e.d.Flush(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if e.fs.count() != 8 || e.row(id).Status != "dead" {
		t.Errorf("Flush touched a dead row: calls=%d status=%s", e.fs.count(), e.row(id).Status)
	}
}

func TestRetry_DeadRow_ReattemptsAndDelivers(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.addOutbox(1, 1, 1, "dead-key", "dead", 8)
	if err := e.d.Retry(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if e.fs.count() != 1 {
		t.Fatalf("Deliver calls = %d, want 1", e.fs.count())
	}
	if a := e.fs.call(0).Attempt; a != 9 {
		t.Errorf("Attempt = %d, want cumulative 9", a)
	}
	r := e.row(1)
	if r.Status != "delivered" || r.Attempts != 9 || r.Receipt != "ref:dead-key" {
		t.Errorf("row = %+v", r)
	}
	if evs := e.eventList(); len(evs) != 1 || !evs[0].Delivered {
		t.Errorf("events = %+v", evs)
	}
}

func TestRetry_DeadRowStillFailing_StaysDeadWithoutRetryFlag(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.addOutbox(1, 1, 1, "dead-key", "dead", 8)
	e.fs.setFail(-1)
	if err := e.d.Retry(context.Background(), 1); err != nil {
		t.Fatalf("Retry must not surface delivery failures: %v", err)
	}
	if e.fs.count() < 1 {
		t.Fatal("Retry did not attempt delivery")
	}
	if r := e.row(1); r.Status != "dead" || r.Attempts <= 8 {
		t.Errorf("row = %s/%d, want dead with attempts > 8", r.Status, r.Attempts)
	}
	evs := e.eventList()
	if len(evs) != 1 || evs[0].Delivered || evs[0].WillRetry {
		t.Errorf("events = %+v, want one failure with WillRetry=false", evs)
	}
}

func TestRetry_FailedRow_Delivers(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	e.addOutbox(1, 1, 1, "fk", "failed", 3)
	if err := e.d.Retry(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if r := e.row(1); r.Status != "delivered" || r.Attempts != 4 {
		t.Errorf("row = %s/%d, want delivered/4", r.Status, r.Attempts)
	}
}

func TestRetry_UnknownOutboxID_NotFound(t *testing.T) {
	e := newEnv(t)
	if err := e.d.Retry(context.Background(), 404); !apperr.Is(err, apperr.NotFound) {
		t.Errorf("err = %v, want not_found", err)
	}
}

func TestNewDispatcher_ResetsDeliveringRowsToFailed(t *testing.T) {
	e := newEnvNoDispatcher(t)
	e.addSink(1, 0, "manual", true)
	e.addOutbox(1, 1, 1, "interrupted", "delivering", 2)
	e.addOutbox(2, 1, 1, "fine", "delivered", 1)
	e.start()
	if r := e.row(1); r.Status != "failed" || r.Attempts != 2 {
		t.Errorf("delivering row = %s/%d, want failed/2", r.Status, r.Attempts)
	}
	if e.row(2).Status != "delivered" {
		t.Error("delivered row must be untouched")
	}
	if e.fs.count() != 0 {
		t.Error("startup must not deliver anything (no trigger)")
	}
}

func TestAfterSync_EnqueuesOnlyForEnabledAfterSyncSinksCoveringStandup(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "after_sync", true)  // all standups
	e.addSink(2, 1, "after_sync", true)  // this standup
	e.addSink(3, 2, "after_sync", true)  // another standup
	e.addSink(4, 0, "after_sync", false) // disabled
	e.addSink(5, 0, "manual", true)      // manual only

	if err := e.d.AfterSync(context.Background(), 1, 7); err != nil {
		t.Fatal(err)
	}
	if e.rowCount() != 2 {
		t.Fatalf("outbox rows = %d, want 2", e.rowCount())
	}
	if got := e.fs.keys(); !reflect.DeepEqual(got, []string{"sync:7:1", "sync:7:2"}) {
		t.Errorf("delivered keys = %v, want [sync:7:1 sync:7:2]", got)
	}
	for sinkID, key := range map[int64]string{1: "sync:7:1", 2: "sync:7:2"} {
		var id int64
		if err := e.db.W.QueryRow(`SELECT id FROM sink_outbox WHERE idempotency_key=?`, key).Scan(&id); err != nil {
			t.Fatalf("no outbox row for %s: %v", key, err)
		}
		r := e.row(id)
		if r.SinkID != sinkID || r.StandupID != 1 || r.Cause != "after_sync" || r.Kind != "current" || r.Status != "delivered" {
			t.Errorf("row for %s = %+v", key, r)
		}
	}
	for _, b := range e.builds {
		if b.StandupID != 1 || b.Req != cur {
			t.Errorf("build call = %+v, want standup 1 / current", b)
		}
	}
}

func TestAfterSync_SameRunTwice_DeliversExactlyOnce(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "after_sync", true)
	for i := 0; i < 2; i++ {
		if err := e.d.AfterSync(context.Background(), 1, 7); err != nil {
			t.Fatal(err)
		}
	}
	if e.fs.count() != 1 || e.rowCount() != 1 {
		t.Errorf("calls=%d rows=%d, want 1/1 (unique key per sync run)", e.fs.count(), e.rowCount())
	}
	if err := e.d.AfterSync(context.Background(), 1, 8); err != nil {
		t.Fatal(err)
	}
	if e.fs.count() != 2 || e.rowCount() != 2 {
		t.Errorf("next run: calls=%d rows=%d, want 2/2", e.fs.count(), e.rowCount())
	}
}

func TestAfterSync_FlushesOlderFailedRowsBeforeNewDelivery(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "after_sync", true)
	e.addOutbox(1, 1, 1, "older-failed", "failed", 3)
	if err := e.d.AfterSync(context.Background(), 1, 7); err != nil {
		t.Fatal(err)
	}
	if got := e.fs.keys(); !reflect.DeepEqual(got, []string{"older-failed", "sync:7:1"}) {
		t.Errorf("delivery order = %v, want older failed row first", got)
	}
	if e.row(1).Status != "delivered" {
		t.Errorf("older row status = %s", e.row(1).Status)
	}
}

func TestAfterSync_FailingSink_ReturnsNilAndLeavesRowFailed(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "after_sync", true)
	e.fs.setFail(-1)
	if err := e.d.AfterSync(context.Background(), 1, 7); err != nil {
		t.Fatalf("a failing sink must not fail the sync: %v", err)
	}
	var id int64
	if err := e.db.W.QueryRow(`SELECT id FROM sink_outbox WHERE idempotency_key='sync:7:1'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if r := e.row(id); r.Status != "failed" || r.Attempts != 3 {
		t.Errorf("row = %s/%d, want failed/3", r.Status, r.Attempts)
	}
	if evs := e.eventList(); len(evs) != 1 || evs[0].Delivered || !evs[0].WillRetry {
		t.Errorf("events = %+v", evs)
	}
}

func TestAfterSync_NoMatchingSinks_NoWorkAndNoBuild(t *testing.T) {
	e := newEnv(t)
	e.addSink(1, 0, "manual", true)
	if err := e.d.AfterSync(context.Background(), 1, 7); err != nil {
		t.Fatal(err)
	}
	if e.buildCount() != 0 || e.fs.count() != 0 || e.rowCount() != 0 {
		t.Errorf("builds=%d calls=%d rows=%d, want all 0", e.buildCount(), e.fs.count(), e.rowCount())
	}
}

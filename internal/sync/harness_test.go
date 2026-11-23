package sync

// harness_test.go is orchestrator-owned SHARED TEST INFRASTRUCTURE for orchestrator_test.go (WP-29) and
// apply_test.go (WP-30). Implementers of those work packages must not edit it; if a helper is wrong, say so in
// docs/wp-notes/WP-NN.md and the orchestrator will fix it.
//
// What it builds: a migrated temp DB (storetest.Open), a fake clock (America/Chicago), a recording Emitter, an
// in-memory secrets.Keyring holding jiratest.Token, a connection row that points at a jiratest server, N standups
// bound to the fake's MOB board through settings (Create, SetSpace, SetBoard), and an Orchestrator wired with
// OpenSource = source.Open after source.Register(jira.Kind, jira.New, jira.IssueURL).
//
// SPEC-NOTE (all tests in this package): SourceOpts is handed to OpenSource unchanged (HTTP, Logger and Now are
// preserved; the orchestrator only adds Meta from connection.source_meta). SourceOpts.Now is the real time.Now on
// purpose: a frozen clock inside the Jira client would feed its 429 gate. The tests rely on SourceOpts.HTTP to gate,
// fail and cancel requests deterministically instead of sleeping or using the fake's global fault options (which would
// also break the setup calls).
// SPEC-NOTE (all tests in this package): Wait(standupID) returns only after the run is completely finished: sync_run
// finalised, sync:finished and data:changed emitted, and AfterSync (if any) returned. Tests read events and hook
// records right after Wait without sleeping.
// SPEC-NOTE (all tests in this package): every sync advances the fake clock first (default 1 hour) because spans are
// keyed on the run's started_at and have CHECK (valid_to > valid_from); the run's T is clock.Now() when Start is
// called. The fake Jira's own Now is fixed at 2026-11-23 15:00 America/Chicago.
//
// The mini fixture (miniFixture) is a hand-written Fixture, so tests that mutate and count do not depend on the
// generated testdata/fixture.json. The default fixture is used by the tests that say so, found by project key "MOB".

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	gosync "sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

// ---------------------------------------------------------------------------------------------------------------
// panics and basics

// recoverStub turns Wave 0 `panic("WP-NN")` stubs into a per-test failure instead of aborting the test binary.
// Use as the first statement of every test: `defer recoverStub(t)`.
func recoverStub(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("panic (stub not implemented?): %v", r)
	}
}

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

var chicago = mustLoc("America/Chicago")

// baseTime is "Monday 23 Nov 2026, 15:00 Chicago" (after the 11:00 local time at which the fake places day-0 events).
func baseTime() time.Time { return time.Date(2026, 11, 23, 15, 0, 0, 0, chicago) }

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func ms(t time.Time) int64 { return t.UnixMilli() }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ---------------------------------------------------------------------------------------------------------------
// fake clock

type fakeClock struct {
	mu  gosync.Mutex
	now time.Time
	loc *time.Location
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *fakeClock) Location() *time.Location { return c.loc }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------------------------------------------
// recording emitter

type emitted struct {
	Name    string
	Payload any
}

type recEmitter struct {
	mu   gosync.Mutex
	ev   []emitted
	hook func(name string, payload any)
}

func (r *recEmitter) Emit(name string, payload any) {
	r.mu.Lock()
	r.ev = append(r.ev, emitted{name, payload})
	h := r.hook
	r.mu.Unlock()
	if h != nil {
		h(name, payload)
	}
}

func (r *recEmitter) setHook(h func(name string, payload any)) {
	r.mu.Lock()
	r.hook = h
	r.mu.Unlock()
}

func (r *recEmitter) all() []emitted {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]emitted(nil), r.ev...)
}

func (r *recEmitter) names() []string {
	var out []string
	for _, e := range r.all() {
		out = append(out, e.Name)
	}
	return out
}

func (r *recEmitter) reset() {
	r.mu.Lock()
	r.ev = nil
	r.mu.Unlock()
}

// index returns the position of the first event with that name at or after from, or -1.
func (r *recEmitter) index(name string, from int) int {
	for i, e := range r.all() {
		if i >= from && e.Name == name {
			return i
		}
	}
	return -1
}

// Payload decoders accept both value and pointer payloads.
func asStarted(p any) (SyncStarted, bool) {
	switch v := p.(type) {
	case SyncStarted:
		return v, true
	case *SyncStarted:
		return *v, v != nil
	}
	return SyncStarted{}, false
}
func asProgress(p any) (SyncProgress, bool) {
	switch v := p.(type) {
	case SyncProgress:
		return v, true
	case *SyncProgress:
		return *v, v != nil
	}
	return SyncProgress{}, false
}
func asFinished(p any) (SyncFinished, bool) {
	switch v := p.(type) {
	case SyncFinished:
		return v, true
	case *SyncFinished:
		return *v, v != nil
	}
	return SyncFinished{}, false
}
func asDataChanged(p any) (DataChanged, bool) {
	switch v := p.(type) {
	case DataChanged:
		return v, true
	case *DataChanged:
		return *v, v != nil
	}
	return DataChanged{}, false
}
func asConnAuth(p any) (ConnectionAuth, bool) {
	switch v := p.(type) {
	case ConnectionAuth:
		return v, true
	case *ConnectionAuth:
		return *v, v != nil
	}
	return ConnectionAuth{}, false
}

func (r *recEmitter) started() (out []SyncStarted) {
	for _, e := range r.all() {
		if e.Name == EvSyncStarted {
			if p, ok := asStarted(e.Payload); ok {
				out = append(out, p)
			}
		}
	}
	return
}
func (r *recEmitter) progress() (out []SyncProgress) {
	for _, e := range r.all() {
		if e.Name == EvSyncProgress {
			if p, ok := asProgress(e.Payload); ok {
				out = append(out, p)
			}
		}
	}
	return
}
func (r *recEmitter) finished() (out []SyncFinished) {
	for _, e := range r.all() {
		if e.Name == EvSyncFinished {
			if p, ok := asFinished(e.Payload); ok {
				out = append(out, p)
			}
		}
	}
	return
}
func (r *recEmitter) dataChanged() (out []DataChanged) {
	for _, e := range r.all() {
		if e.Name == EvDataChanged {
			if p, ok := asDataChanged(e.Payload); ok {
				out = append(out, p)
			}
		}
	}
	return
}
func (r *recEmitter) connAuth() (out []ConnectionAuth) {
	for _, e := range r.all() {
		if e.Name == EvConnectionAuth {
			if p, ok := asConnAuth(e.Payload); ok {
				out = append(out, p)
			}
		}
	}
	return
}

// ---------------------------------------------------------------------------------------------------------------
// in-memory keyring

type memKeyring struct {
	mu   gosync.Mutex
	m    map[string]string
	gets []string
}

func newMemKeyring() *memKeyring { return &memKeyring{m: map[string]string{}} }

func (k *memKeyring) Get(account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.gets = append(k.gets, account)
	v, ok := k.m[account]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (k *memKeyring) Set(account, secret string) error {
	k.mu.Lock()
	k.m[account] = secret
	k.mu.Unlock()
	return nil
}
func (k *memKeyring) Delete(account string) error {
	k.mu.Lock()
	delete(k.m, account)
	k.mu.Unlock()
	return nil
}
func (k *memKeyring) getAccounts() []string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]string(nil), k.gets...)
}

var _ secrets.Keyring = (*memKeyring)(nil)

// ---------------------------------------------------------------------------------------------------------------
// gating HTTP transport (deterministic mid-run control; no sleeps)

type gate struct {
	match func(*http.Request) bool

	reached  chan struct{} // closed on the first matching request
	arrive   chan struct{} // one token per matching request (buffered)
	open     chan struct{} // closed by Release
	step     chan struct{} // ReleaseOne
	mu       gosync.Mutex
	arrivals int
	blocked  int
	maxBlock int
	once1    gosync.Once
	once2    gosync.Once
}

func newGate(match func(*http.Request) bool) *gate {
	return &gate{match: match, reached: make(chan struct{}), arrive: make(chan struct{}, 256), open: make(chan struct{}), step: make(chan struct{})}
}

func (g *gate) wait(ctx context.Context) error {
	g.mu.Lock()
	g.arrivals++
	g.blocked++
	if g.blocked > g.maxBlock {
		g.maxBlock = g.blocked
	}
	g.mu.Unlock()
	g.once1.Do(func() { close(g.reached) })
	select {
	case g.arrive <- struct{}{}:
	default:
	}
	defer func() {
		g.mu.Lock()
		g.blocked--
		g.mu.Unlock()
	}()
	select {
	case <-g.open:
		return nil
	case <-g.step:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release lets every blocked and future matching request through.
func (g *gate) Release() { g.once2.Do(func() { close(g.open) }) }

// ReleaseOne lets exactly one blocked request through (blocks until one is waiting, up to 10 s).
func (g *gate) ReleaseOne(t testing.TB) {
	t.Helper()
	select {
	case g.step <- struct{}{}:
	case <-time.After(10 * time.Second):
		t.Fatalf("ReleaseOne: no request was waiting at the gate")
	}
}

// waitArrivals blocks until n matching requests have arrived in total (consumes arrival tokens), up to 15 s.
func (g *gate) waitArrivals(t testing.TB, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-g.arrive:
		case <-time.After(15 * time.Second):
			t.Fatalf("only %d of %d expected requests reached the gate", i, n)
		}
	}
}

func (g *gate) counts() (arrivals, blocked, maxBlocked int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.arrivals, g.blocked, g.maxBlock
}

// waitReached fails the test when no matching request arrives within 15 s.
func (g *gate) waitReached(t testing.TB) {
	t.Helper()
	select {
	case <-g.reached:
	case <-time.After(15 * time.Second):
		t.Fatalf("the run never reached the gated request")
	}
}

// fault makes every request whose path starts with prefix answer code (Jira-style error JSON) without reaching the fake.
// It applies to the orchestrator's sources only (setup uses a plain client), so it cannot break the setup calls.
type fault struct {
	prefix string
	code   int
	hits   int
}

type hookTransport struct {
	base   http.RoundTripper
	mu     gosync.Mutex
	gates  []*gate
	faults []*fault
}

// failRequests installs a fault; faultHits(f) reports how many requests it answered.
func (e *env) failRequests(prefix string, code int) *fault {
	f := &fault{prefix: prefix, code: code}
	e.gt.mu.Lock()
	e.gt.faults = append(e.gt.faults, f)
	e.gt.mu.Unlock()
	return f
}

func (e *env) faultHits(f *fault) int {
	e.gt.mu.Lock()
	defer e.gt.mu.Unlock()
	return f.hits
}

func (h *hookTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	gs := append([]*gate(nil), h.gates...)
	var hit *fault
	for _, f := range h.faults {
		if strings.HasPrefix(r.URL.Path, f.prefix) {
			f.hits++
			hit = f
			break
		}
	}
	h.mu.Unlock()
	if hit != nil {
		body := `{"errorMessages":["Request failed by the test harness"],"errors":{}}`
		return &http.Response{StatusCode: hit.code, Status: strconv.Itoa(hit.code) + " " + http.StatusText(hit.code),
			Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	}
	for _, g := range gs {
		if g.match(r) {
			if err := g.wait(r.Context()); err != nil {
				return nil, err
			}
		}
	}
	return h.base.RoundTrip(r)
}

// gate installs a gate on every request that matches; the request blocks until Release/ReleaseOne or ctx cancel.
func (e *env) gate(match func(*http.Request) bool) *gate {
	g := newGate(match)
	e.gt.mu.Lock()
	e.gt.gates = append(e.gt.gates, g)
	e.gt.mu.Unlock()
	return g
}

func pathHasPrefix(prefix string) func(*http.Request) bool {
	return func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, prefix) }
}

// ---------------------------------------------------------------------------------------------------------------
// the fake source wrapper (per-issue dev-activity faults) and open recording

type openCall struct {
	Conn    model.Connection
	Secret  string
	Meta    map[string]any
	HasHTTP bool
}

type sourceWrap struct {
	source.Source
	e *env
}

func (w *sourceWrap) DevActivity(ctx context.Context, sourceID, prev string) (model.DevActivity, error) {
	w.e.mu.Lock()
	failing := w.e.devFail[sourceID]
	w.e.mu.Unlock()
	if failing != nil {
		return model.DevActivity{}, failing
	}
	return w.Source.DevActivity(ctx, sourceID, prev)
}

// ---------------------------------------------------------------------------------------------------------------
// environment

type envOpts struct {
	fx          *jiratest.Fixture // nil = miniFixture() with the assignee-history prep
	srv         jiratest.Options  // Now and Loc are set by the harness
	noToken     bool              // nothing in the keychain
	standups    int               // default 1
	maxParallel int
	noPrep      bool // keep the fixture untouched (default fixture is never prepped)
}

type afterCall struct {
	StandupID, RunID int64
	EventsSeen       []string
	TxFreeErr        error // error from a WriteTx attempted inside the hook (non-nil = hook ran inside a transaction)
}

type env struct {
	t         *testing.T
	db        *store.DB
	fx        *jiratest.Fixture
	srv       *jiratest.Server
	clock     *fakeClock
	em        *recEmitter
	kr        *memKeyring
	st        *settings.Store
	o         *Orchestrator
	gt        *hookTransport
	connID    int64
	account   string // keychain account
	email     string
	sid       int64   // first standup
	sids      []int64 // every standup created by the harness
	boardID   string
	project   jiratest.FxProject
	curT      time.Time // started_at (T) of the latest sync started through the helpers
	mu        gosync.Mutex
	devFail   map[string]error
	opened    []openCall
	after     []afterCall
	changeSeq int
}

const testEmail = "me@acme.com"

// newEnv builds everything. Call `defer recoverStub(t)` in the test first.
func newEnv(t *testing.T, eo envOpts) *env {
	t.Helper()
	clk := &fakeClock{now: baseTime(), loc: chicago}
	db := storetest.Open(t, clk.Now)

	fx := eo.fx
	prep := !eo.noPrep && fx == nil
	if fx == nil {
		fx = miniFixture()
	}
	so := eo.srv
	so.Now = baseTime()
	so.Loc = chicago
	srv := jiratest.NewServer(t, fx, so)

	source.Register(jira.Kind, jira.New, jira.IssueURL)

	e := &env{t: t, db: db, fx: fx, srv: srv, clock: clk, em: &recEmitter{}, kr: newMemKeyring(),
		st: settings.New(db, clk.Now), email: testEmail, devFail: map[string]error{}}
	e.gt = &hookTransport{base: &http.Transport{DisableKeepAlives: true}}
	t.Cleanup(func() { e.gt.base.(*http.Transport).CloseIdleConnections() })

	// connection row + token
	storetest.Exec(t, db, `INSERT INTO connection (source, base_url, account, account_id, display_name, auth_state, verified_at, created_at)
		VALUES ('jira', ?, ?, 'u-me', 'Me', 'ok', ?, ?)`, srv.URL, testEmail, ms(clk.Now()), ms(clk.Now()))
	e.connID = qInt(t, db, `SELECT id FROM connection WHERE base_url = ?`, srv.URL)
	e.account = secrets.ConnectionAccount(jira.Kind, srv.URL, testEmail)
	if !eo.noToken {
		_ = e.kr.Set(e.account, jiratest.Token)
	}

	board, proj, ok := svcBoard(fx)
	if !ok {
		t.Fatalf("fixture has no board whose project key is MOB")
	}
	e.boardID = strconv.Itoa(board.ID)
	e.project = proj

	if prep {
		e.prepAssigneeHistory()
	}

	e.o = New(Options{
		DB: db, Keyring: e.kr, Emitter: e.em, Clock: clk, Logger: discardLogger(),
		SourceOpts: source.Options{HTTP: &http.Client{Transport: e.gt}, Logger: discardLogger(), Now: time.Now},
		OpenSource: func(conn model.Connection, secret string, opts source.Options) (source.Source, error) {
			e.mu.Lock()
			e.opened = append(e.opened, openCall{Conn: conn, Secret: secret, Meta: opts.Meta, HasHTTP: opts.HTTP != nil})
			e.mu.Unlock()
			s, err := source.Open(conn, secret, opts)
			if err != nil {
				return nil, err
			}
			return &sourceWrap{Source: s, e: e}, nil
		},
		AfterSync:   e.afterHook,
		MaxParallel: eo.maxParallel,
		// A tiny interval means nothing is coalesced away in tests that count progress events.
		ProgressInterval: time.Nanosecond,
	})

	n := eo.standups
	if n == 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		e.addStandup()
	}
	e.sid = e.sids[0]
	srv.ResetRequests()
	return e
}

func (e *env) afterHook(ctx context.Context, standupID, runID int64) {
	seen := e.em.names()
	cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// A write transaction can only start when the apply transaction has ended (the writer pool has one connection).
	txErr := e.db.WriteTx(cctx, func(tx *sql.Tx) error { return nil })
	e.mu.Lock()
	e.after = append(e.after, afterCall{StandupID: standupID, RunID: runID, EventsSeen: seen, TxFreeErr: txErr})
	e.mu.Unlock()
}

func (e *env) afterCalls() []afterCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]afterCall(nil), e.after...)
}

func (e *env) openCalls() []openCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]openCall(nil), e.opened...)
}

func (e *env) failDev(sourceID string, err error) {
	e.mu.Lock()
	if err == nil {
		delete(e.devFail, sourceID)
	} else {
		e.devFail[sourceID] = err
	}
	e.mu.Unlock()
}

// openDirect opens an adapter straight on the fake, outside the orchestrator (setup only).
func (e *env) openDirect() source.Source {
	e.t.Helper()
	s, err := jira.New(model.Connection{ID: e.connID, Source: jira.Kind, BaseURL: e.srv.URL, Account: e.email},
		jiratest.Token, source.Options{Logger: discardLogger()})
	if err != nil {
		e.t.Fatalf("jira.New: %v", err)
	}
	return s
}

// addStandup creates one standup bound to the MOB board: settings.Create, SetSpace, SetBoard.
func (e *env) addStandup() int64 {
	e.t.Helper()
	ctx := context.Background()
	id, err := e.st.Create(ctx)
	if err != nil {
		e.t.Fatalf("settings.Create: %v", err)
	}
	if _, err := e.st.SetSpace(ctx, id, model.Space{ID: e.project.ID, Key: e.project.Key, Name: e.project.Name}); err != nil {
		e.t.Fatalf("settings.SetSpace: %v", err)
	}
	src := e.openDirect()
	cfg, err := src.BoardConfig(ctx, e.boardID)
	if err != nil {
		e.t.Fatalf("BoardConfig: %v", err)
	}
	statuses, _, err := src.Reference(ctx)
	if err != nil {
		e.t.Fatalf("Reference: %v", err)
	}
	if _, err := e.st.SetBoard(ctx, id, cfg, statuses); err != nil {
		e.t.Fatalf("settings.SetBoard: %v", err)
	}
	e.sids = append(e.sids, id)
	return id
}

// ---------------------------------------------------------------------------------------------------------------
// running syncs

// syncAfter advances the clock by d, starts a run on the first standup and waits for it. Returns the run id.
func (e *env) syncAfter(d time.Duration, mode string) int64 {
	e.t.Helper()
	return e.syncStandup(e.sid, d, mode)
}

// sync is syncAfter(1h).
func (e *env) sync(mode string) int64 { e.t.Helper(); return e.syncAfter(time.Hour, mode) }

func (e *env) syncStandup(sid int64, d time.Duration, mode string) int64 {
	e.t.Helper()
	e.clock.Advance(d)
	e.curT = e.clock.Now()
	id, err := e.o.Start(context.Background(), sid, mode, TriggerManual)
	if err != nil {
		e.t.Fatalf("Start(%d, %s): %v", sid, mode, err)
	}
	e.o.Wait(sid)
	return id
}

// T is the started_at of the latest sync started through the helpers.
func (e *env) T() time.Time { return e.curT }

type runRow struct {
	ID, StandupID                        int64
	Mode, Trigger, Status                string
	StartedAt, FinishedAt                int64
	Scanned, Hydrated, Requests, Throttl int64
	ErrorCode, ErrorMessage              string
	Warnings                             []string
}

func (e *env) run(runID int64) runRow {
	e.t.Helper()
	var r runRow
	var fin sql.NullInt64
	var code, msg sql.NullString
	var warnJSON string
	err := e.db.R.QueryRowContext(context.Background(), `SELECT id, standup_id, mode, trigger, status, started_at, finished_at,
		issues_scanned, issues_hydrated, requests, throttled, error_code, error_message, warnings FROM sync_run WHERE id = ?`, runID).
		Scan(&r.ID, &r.StandupID, &r.Mode, &r.Trigger, &r.Status, &r.StartedAt, &fin, &r.Scanned, &r.Hydrated,
			&r.Requests, &r.Throttl, &code, &msg, &warnJSON)
	if err != nil {
		e.t.Fatalf("read sync_run %d: %v", runID, err)
	}
	r.FinishedAt, r.ErrorCode, r.ErrorMessage = fin.Int64, code.String, msg.String
	if warnJSON != "" {
		_ = json.Unmarshal([]byte(warnJSON), &r.Warnings)
	}
	return r
}

func (e *env) mustOK(runID int64) runRow {
	e.t.Helper()
	r := e.run(runID)
	if r.Status != StatusOK {
		e.t.Fatalf("run %d status = %q (error %q: %s, warnings %v), want ok", runID, r.Status, r.ErrorCode, r.ErrorMessage, r.Warnings)
	}
	return r
}

func (e *env) requests() []jiratest.Request { return e.srv.Requests() }

// ---------------------------------------------------------------------------------------------------------------
// request log classification

func countPath(reqs []jiratest.Request, pred func(path string) bool) int {
	n := 0
	for _, r := range reqs {
		if pred(r.Path) {
			n++
		}
	}
	return n
}

func exactPath(p string) func(string) bool { return func(s string) bool { return s == p } }

var (
	isMyself  = exactPath("/rest/api/3/myself")
	isField   = exactPath("/rest/api/3/field")
	isStatus  = exactPath("/rest/api/3/status")
	isSearch  = exactPath("/rest/api/3/search/jql")
	isCount   = exactPath("/rest/api/3/search/approximate-count")
	isBulk    = exactPath("/rest/api/3/changelog/bulkfetch")
	isSummary = exactPath("/rest/dev-status/latest/issue/summary")
	isDetail  = exactPath("/rest/dev-status/latest/issue/detail")
)

func isBoardConfig(p string) bool {
	return strings.HasPrefix(p, "/rest/agile/1.0/board/") && strings.HasSuffix(p, "/configuration")
}
func isSprintList(p string) bool {
	return strings.HasPrefix(p, "/rest/agile/1.0/board/") && strings.HasSuffix(p, "/sprint")
}
func isCommentTopUp(p string) bool {
	return strings.HasPrefix(p, "/rest/api/3/issue/") && strings.Contains(p, "/comment")
}
func isPerIssueChangelog(p string) bool {
	return strings.HasPrefix(p, "/rest/api/3/issue/") && strings.HasSuffix(p, "/changelog")
}
func isDevStatus(p string) bool { return strings.HasPrefix(p, "/rest/dev-status/") }

// ---------------------------------------------------------------------------------------------------------------
// SQL helpers (reads on the reader pool)

func qInt(t testing.TB, db *store.DB, q string, args ...any) int64 {
	t.Helper()
	var v sql.NullInt64
	if err := db.R.QueryRowContext(context.Background(), q, args...).Scan(&v); err != nil {
		t.Fatalf("query %q %v: %v", q, args, err)
	}
	return v.Int64
}

func qStr(t testing.TB, db *store.DB, q string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := db.R.QueryRowContext(context.Background(), q, args...).Scan(&v); err != nil {
		t.Fatalf("query %q %v: %v", q, args, err)
	}
	return v.String
}

// qIsNull reports whether the single value the query returns is SQL NULL (the query must return exactly one row).
func qIsNull(t testing.TB, db *store.DB, q string, args ...any) bool {
	t.Helper()
	var v any
	if err := db.R.QueryRowContext(context.Background(), q, args...).Scan(&v); err != nil {
		t.Fatalf("query %q %v: %v", q, args, err)
	}
	return v == nil
}

// qCol returns the first column of every row as strings (NULL → "<null>").
func qCol(t testing.TB, db *store.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.R.QueryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("query %q %v: %v", q, args, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan %q: %v", q, err)
		}
		if v.Valid {
			out = append(out, v.String)
		} else {
			out = append(out, "<null>")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows %q: %v", q, err)
	}
	return out
}

func (e *env) qInt(q string, args ...any) int64  { e.t.Helper(); return qInt(e.t, e.db, q, args...) }
func (e *env) qStr(q string, args ...any) string { e.t.Helper(); return qStr(e.t, e.db, q, args...) }
func (e *env) qIsNull(q string, args ...any) bool {
	e.t.Helper()
	return qIsNull(e.t, e.db, q, args...)
}
func (e *env) qCol(q string, args ...any) []string { e.t.Helper(); return qCol(e.t, e.db, q, args...) }
func (e *env) count(table, where string, args ...any) int {
	e.t.Helper()
	return storetest.Count(e.t, e.db, table, where, args...)
}

func (e *env) issueID(key string) int64 {
	e.t.Helper()
	return e.qInt(`SELECT id FROM issue WHERE key = ?`, key)
}

func (e *env) rev(sid int64) int64 {
	e.t.Helper()
	return e.qInt(`SELECT rev FROM standup WHERE id = ?`, sid)
}

// historyTables are the config and record tables (plus the caches written by the apply step) that a run which never
// applies must leave byte-identical. sync_run, sync_log, standup_sync_state and connection are excluded on purpose:
// StartRun writes them, and a 401 flips connection.auth_state.
var historyTables = []string{
	"app_setting", "standup", "standup_settings", "standup_lane", "standup_column_map", "standup_status_lane",
	"standup_speaker", "ui_state", "standup_ui_state", "board", "board_column", "board_column_status",
	"status", "issue_type", "sprint", "person", "issue", "status_change", "assignee_change", "flag_change",
	"comment", "dev_commit", "dev_pr_event", "standup_issue", "standup_issue_span", "standup_config_history", "sink",
}

func (e *env) checksum() string {
	e.t.Helper()
	return storetest.Checksum(e.t, e.db, historyTables...)
}

func (e *env) assertNothingApplied(before string, revBefore int64) {
	e.t.Helper()
	if after := e.checksum(); after != before {
		e.t.Errorf("config/record tables changed although the run did not apply")
	}
	if rev := e.rev(e.sid); rev != revBefore {
		e.t.Errorf("standup.rev = %d, want %d (unchanged)", rev, revBefore)
	}
	if !e.qIsNull(`SELECT last_success_at FROM standup_sync_state WHERE standup_id = ?`, e.sid) {
		e.t.Errorf("last_success_at was set by a run that did not apply")
	}
	if n := e.qInt(`SELECT needs_sync FROM standup_sync_state WHERE standup_id = ?`, e.sid); n != 1 {
		e.t.Errorf("needs_sync = %d, want 1 (still needs a sync)", n)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// membership, spans, lanes (what the view shows, read straight from the cache tables; no internal/view)

type memberRow struct{ Key, Role, Lane, Assignee string }

func (e *env) members(sid int64) []memberRow {
	e.t.Helper()
	rows, err := e.db.R.QueryContext(context.Background(), `
		SELECT i.key, si.role, COALESCE(sl.lane, ''), COALESCE(i.assignee_account_id, '')
		FROM standup_issue si
		JOIN issue i ON i.id = si.issue_id
		LEFT JOIN standup_status_lane sl ON sl.standup_id = si.standup_id AND sl.status_id = i.status_id
		WHERE si.standup_id = ? ORDER BY i.key`, sid)
	if err != nil {
		e.t.Fatalf("members: %v", err)
	}
	defer rows.Close()
	var out []memberRow
	for rows.Next() {
		var m memberRow
		if err := rows.Scan(&m.Key, &m.Role, &m.Lane, &m.Assignee); err != nil {
			e.t.Fatalf("members scan: %v", err)
		}
		out = append(out, m)
	}
	return out
}

func memberKeys(ms []memberRow, role string) []string {
	var out []string
	for _, m := range ms {
		if role == "" || m.Role == role {
			out = append(out, m.Key)
		}
	}
	sort.Strings(out)
	return out
}

type spanRow struct {
	Role     string
	From, To int64 // To == 0: open
}

func (e *env) spans(sid int64, key string) []spanRow {
	e.t.Helper()
	rows, err := e.db.R.QueryContext(context.Background(), `
		SELECT s.role, s.valid_from, COALESCE(s.valid_to, 0)
		FROM standup_issue_span s JOIN issue i ON i.id = s.issue_id
		WHERE s.standup_id = ? AND i.key = ? ORDER BY s.valid_from, s.role`, sid, key)
	if err != nil {
		e.t.Fatalf("spans: %v", err)
	}
	defer rows.Close()
	var out []spanRow
	for rows.Next() {
		var s spanRow
		if err := rows.Scan(&s.Role, &s.From, &s.To); err != nil {
			e.t.Fatalf("spans scan: %v", err)
		}
		out = append(out, s)
	}
	return out
}

// devTargets returns the source ids jira-sync.md §2.11 says are dev targets for the standup, from the DB state at the
// time of the call: board-role members whose lane is prog, review or qa, plus board-role members that changed status
// within the last 28 days and whose dev_checked_at is NULL or older than that change. ignoreChecked treats every
// dev_checked_at as NULL (use it after a first run, whose checks have already been stamped).
func (e *env) devTargets(sid int64, ignoreChecked bool) []string {
	e.t.Helper()
	window := ms(e.clock.Now().Add(-28 * 24 * time.Hour))
	checked := `(i.dev_checked_at IS NULL OR i.dev_checked_at < (SELECT max(changed_at) FROM status_change WHERE issue_id = i.id))`
	if ignoreChecked {
		checked = `1`
	}
	out := e.qCol(`
		SELECT i.source_id FROM standup_issue si
		JOIN issue i ON i.id = si.issue_id
		LEFT JOIN standup_status_lane sl ON sl.standup_id = si.standup_id AND sl.status_id = i.status_id
		WHERE si.standup_id = ? AND si.role = 'board' AND (
			sl.lane IN ('prog', 'review', 'qa')
			OR (EXISTS (SELECT 1 FROM status_change sc WHERE sc.issue_id = i.id AND sc.changed_at >= ?) AND `+checked+`))
		ORDER BY i.source_id`, sid, window)
	return out
}

// ---------------------------------------------------------------------------------------------------------------
// fixtures

// svcBoard finds the scrum board of project MOB.
func svcBoard(fx *jiratest.Fixture) (jiratest.FxBoard, jiratest.FxProject, bool) {
	for _, b := range fx.Boards {
		if b.ProjectKey != "MOB" {
			continue
		}
		for _, p := range fx.Projects {
			if p.Key == "MOB" {
				return b, p, true
			}
		}
	}
	return jiratest.FxBoard{}, jiratest.FxProject{}, false
}

func defaultFixture(t *testing.T) *jiratest.Fixture {
	t.Helper()
	fx, err := jiratest.ParseFixture(jiratest.DefaultFixture)
	if err != nil {
		t.Fatalf("ParseFixture(DefaultFixture): %v", err)
	}
	return fx
}

// miniFixture: one scrum board (MOB, id 1, filter 9001, sprint 42), five columns To Do, In Progress, Code Review,
// QA, Done, three people, seven issues in the sprint (MOB-1..7, rank order = this order) and three backlog issues.
//
//	MOB-1 Story  Iris   In Progress  2 commits (days -2, -1), 1 comment
//	MOB-2 Bug    Silas  Code Review  1 commit, 1 open PR
//	MOB-3 Story  Noor   QA           2 comments
//	MOB-4 Story  Iris   To Do        (never transitioned: no status_change rows)
//	MOB-5 Task   Silas  Done         (done today)
//	MOB-6 Task   nobody  To Do        (never transitioned)
//	MOB-7 Bug    Noor   In Progress  flagged since 2 days ago
//
// Backlog: MOB-100, MOB-101, MOB-102 (all To Do, unassigned).
func miniFixture() *jiratest.Fixture {
	cols := []jiratest.FxColumn{
		{Name: "To Do", Statuses: []string{"To Do"}},
		{Name: "In Progress", Statuses: []string{"In Progress"}},
		{Name: "Code Review", Statuses: []string{"Code Review"}},
		{Name: "QA", Statuses: []string{"QA"}},
		{Name: "Done", Statuses: []string{"Done"}},
	}
	h := func(pairs ...any) []jiratest.FxHistory {
		var out []jiratest.FxHistory
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, jiratest.FxHistory{Status: pairs[i].(string), Days: pairs[i+1].(int)})
		}
		return out
	}
	return &jiratest.Fixture{
		Projects: []jiratest.FxProject{{ID: "10001", Key: "MOB", Name: "Saffron Mobile"}},
		Boards: []jiratest.FxBoard{{ID: 1, Name: "MOB board", Type: "scrum", ProjectKey: "MOB", FilterID: 9001, Columns: cols,
			Sprint: &jiratest.FxSprint{ID: 42, Name: "Sprint 42", StartDay: -13, EndDay: 0}}},
		Users: []jiratest.FxUser{
			{AccountID: "u-iris", DisplayName: "Iris Calloway"},
			{AccountID: "u-silas", DisplayName: "Silas Marchetti"},
			{AccountID: "u-noor", DisplayName: "Noor Abadi"},
		},
		Issues: []jiratest.FxIssue{
			{Key: "MOB-1", Type: "Story", Summary: "Rate limiter", Assignee: "u-iris", Board: 1, InSprint: true,
				History: h("To Do", 4, "In Progress", 3), Commits: []int{-2, -1}, Comments: []int{-1}},
			{Key: "MOB-2", Type: "Bug", Summary: "Token refresh", Assignee: "u-silas", Board: 1, InSprint: true,
				History: h("To Do", 5, "In Progress", 3, "Code Review", 2), Commits: []int{-3},
				PRs: []jiratest.FxPR{{ID: 7, Day: -1, Status: "OPEN"}}},
			{Key: "MOB-3", Type: "Story", Summary: "Audit export", Assignee: "u-noor", Board: 1, InSprint: true,
				History: h("To Do", 6, "In Progress", 3, "Code Review", 2, "QA", 1), Comments: []int{-1, 0}},
			{Key: "MOB-4", Type: "Story", Summary: "Dark mode", Assignee: "u-iris", Board: 1, InSprint: true,
				History: h("To Do", 2)},
			{Key: "MOB-5", Type: "Task", Summary: "Retire cron", Assignee: "u-silas", Board: 1, InSprint: true,
				History: h("To Do", 6, "In Progress", 4, "Done", 1)},
			{Key: "MOB-6", Type: "Task", Summary: "Docs sweep", Board: 1, InSprint: true,
				History: h("To Do", 1)},
			{Key: "MOB-7", Type: "Bug", Summary: "Flaky webhook", Assignee: "u-noor", Board: 1, InSprint: true,
				FlaggedDays: [][2]int{{-2, 0}}, History: h("To Do", 3, "In Progress", 2)},
		},
		Backlog: []jiratest.FxBacklog{
			{Key: "MOB-100", Type: "Story", Summary: "Idea one", Board: 1},
			{Key: "MOB-101", Type: "Task", Summary: "Idea two", Board: 1},
			{Key: "MOB-102", Type: "Story", Summary: "Idea three", Board: 1},
		},
	}
}

// prepAssigneeHistory adds past assignee changes to the mini dataset (before the first sync): MOB-2 Iris→Silas,
// MOB-4 unassigned→Iris, MOB-6 Noor→unassigned. Current assignees are unchanged.
func (e *env) prepAssigneeHistory() {
	e.t.Helper()
	past := time.Date(2026, 11, 20, 9, 0, 0, 0, chicago)
	e.srv.Mutate(func(ds *jiratest.Dataset) {
		add := func(key, from, to string, at time.Time) {
			is := findIssue(ds, key)
			if is == nil {
				e.t.Errorf("prep: no issue %s", key)
				return
			}
			if !is.Created.IsZero() { // never before the issue exists
				at = is.Created.Add(at.Sub(past))
			}
			is.Changelog = append(is.Changelog, jiratest.DSChange{ID: e.nextChangeID(), At: at,
				FieldID: "assignee", Field: "assignee", From: from, To: to})
		}
		add("MOB-2", "u-iris", "u-silas", past)
		add("MOB-4", "", "u-iris", past.Add(time.Hour))
		add("MOB-6", "u-noor", "", past.Add(2*time.Hour))
	})
}

func (e *env) nextChangeID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.changeSeq++
	return strconv.Itoa(80000 + e.changeSeq)
}

// ---------------------------------------------------------------------------------------------------------------
// dataset mutations that keep the fake consistent (field + changelog entry + bumped updated)

func findIssue(ds *jiratest.Dataset, key string) *jiratest.DSIssue {
	for _, is := range ds.Issues {
		if is.Key == key {
			return is
		}
	}
	return nil
}

func statusIDByName(ds *jiratest.Dataset, name string) string {
	for _, s := range ds.Statuses {
		if s.Name == name {
			return s.ID
		}
	}
	return ""
}

func (e *env) mutateIssue(key string, fn func(ds *jiratest.Dataset, is *jiratest.DSIssue)) {
	e.t.Helper()
	e.srv.Mutate(func(ds *jiratest.Dataset) {
		is := findIssue(ds, key)
		if is == nil {
			e.t.Errorf("mutate: fake has no issue %s", key)
			return
		}
		fn(ds, is)
	})
}

// moveIssue changes the status of key to the named status at the given time.
func (e *env) moveIssue(key, toStatus string, at time.Time) {
	e.t.Helper()
	e.mutateIssue(key, func(ds *jiratest.Dataset, is *jiratest.DSIssue) {
		to := statusIDByName(ds, toStatus)
		if to == "" {
			e.t.Errorf("mutate: fake has no status %q", toStatus)
			return
		}
		is.Changelog = append(is.Changelog, jiratest.DSChange{ID: e.nextChangeID(), At: at, FieldID: "status", Field: "status",
			From: is.StatusID, To: to})
		is.StatusID = to
		is.Updated = at
		is.Resolved = time.Time{}
		for _, s := range ds.Statuses {
			if s.ID == to && s.Category == "done" {
				is.Resolved = at
			}
		}
	})
}

// reassign changes the assignee of key ("" = unassigned).
func (e *env) reassign(key, to string, at time.Time) {
	e.t.Helper()
	e.mutateIssue(key, func(ds *jiratest.Dataset, is *jiratest.DSIssue) {
		is.Changelog = append(is.Changelog, jiratest.DSChange{ID: e.nextChangeID(), At: at, FieldID: "assignee", Field: "assignee",
			From: is.Assignee, To: to})
		is.Assignee = to
		is.Updated = at
	})
}

// setFlag flags or unflags key.
func (e *env) setFlag(key string, on bool, at time.Time) {
	e.t.Helper()
	e.mutateIssue(key, func(ds *jiratest.Dataset, is *jiratest.DSIssue) {
		from, to := "", "Impediment"
		if !on {
			from, to = "Impediment", ""
		}
		is.Changelog = append(is.Changelog, jiratest.DSChange{ID: e.nextChangeID(), At: at, FieldID: jiratest.FlaggedFieldID,
			Field: "Flagged", From: from, To: to})
		is.Flagged = on
		is.Updated = at
	})
}

// leaveSprint removes key from every sprint (membership changes; with bumpUpdated it is re-hydrated too).
func (e *env) leaveSprint(key string, at time.Time) {
	e.t.Helper()
	e.mutateIssue(key, func(ds *jiratest.Dataset, is *jiratest.DSIssue) {
		is.SprintIDs = nil
		is.Updated = at
	})
}

// joinSprint puts key back into the board's active sprint.
func (e *env) joinSprint(key string, at time.Time) {
	e.t.Helper()
	e.mutateIssue(key, func(ds *jiratest.Dataset, is *jiratest.DSIssue) {
		is.SprintIDs = []int{ds.Boards[0].Sprints[0].ID}
		is.Updated = at
	})
}

// addComment adds a comment by author at time at.
func (e *env) addComment(key, author string, at time.Time) string {
	e.t.Helper()
	id := e.nextChangeID()
	e.mutateIssue(key, func(ds *jiratest.Dataset, is *jiratest.DSIssue) {
		is.Comments = append(is.Comments, jiratest.DSComment{ID: id, At: at, Author: author})
		is.Updated = at
	})
	return id
}

// ---------------------------------------------------------------------------------------------------------------
// small assertion helpers

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hasCode(err error, code apperr.Code) bool { return err != nil && apperr.CodeOf(err) == code }

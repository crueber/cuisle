package jira

// WP-T0 tests for the per-connection limiter wiring (client_limiter.go, policy_lanes.go): jira-sync §3.1, ADR 0015,
// docs/testing.md "Ticket refresh and throttling". The limiter's own mechanics are tested in limiter/.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/source/jira/limiter"
)

// ── transport bypass ────────────────────────────────────────────────────────────────────────────

// bypassChecker is the innermost transport. A request that reaches it without holding a limiter permit is a bypass:
// the permit is held from Acquire until the response body is closed, so lim().Stats().InFlight is ≥ 1 for every
// request that was admitted, and 0 for one that skipped the limiter.
type bypassChecker struct {
	base http.RoundTripper
	lim  func() *limiter.Limiter
	mu   sync.Mutex
	seen int
	bad  []string
}

func (b *bypassChecker) RoundTrip(r *http.Request) (*http.Response, error) {
	admitted := b.lim().Stats().InFlight >= 1
	b.mu.Lock()
	b.seen++
	if !admitted {
		b.bad = append(b.bad, r.Method+" "+r.URL.Path)
	}
	b.mu.Unlock()
	return b.base.RoundTrip(r)
}

func (b *bypassChecker) result() (seen int, bad []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seen, append([]string(nil), b.bad...)
}

func TestBypass_CheckerFlagsARequestThatSkipsTheLimiter(t *testing.T) {
	// The negative control: the same checker, reached through a plain http.Client, must complain.
	srv := newWP06Server(t)
	chk := &bypassChecker{base: &http.Transport{DisableKeepAlives: true},
		lim: func() *limiter.Limiter { return limiter.New(limiter.Config{}) }}
	resp, err := (&http.Client{Transport: chk}).Get(srv.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if seen, bad := chk.result(); seen != 1 || len(bad) != 1 {
		t.Fatalf("checker seen=%d bad=%v, want it to flag the unlimited request", seen, bad)
	}
}

func TestBypass_EveryAdapterRequestPassesTheLimiter(t *testing.T) {
	fx, err := jiratest.ParseFixture(jiratest.DefaultFixture)
	if err != nil {
		t.Fatal(err)
	}
	loc := time.UTC
	srv := jiratest.NewServer(t, fx, jiratest.Options{Now: time.Date(2026, 11, 23, 15, 0, 0, 0, loc), Loc: loc})
	conn := model.Connection{ID: 7, Source: Kind, BaseURL: srv.URL, Account: "dev@example.test"}
	pol, _ := newWP06Policy(t, PolicyOptions{})
	_ = DefaultPolicy() // make sure there is a default to restore (SetDefaultPolicy would otherwise restore nil)
	restore := SetDefaultPolicy(pol)
	t.Cleanup(restore)

	chk := &bypassChecker{base: &http.Transport{DisableKeepAlives: true}, lim: func() *limiter.Limiter { return LimiterFor(conn) }}
	src, err := New(conn, jiratest.Token, source.Options{HTTP: &http.Client{Transport: chk}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var boardID, spaceKey, issueID string
	for _, b := range fx.Boards {
		if b.ProjectKey == "MOB" {
			boardID, spaceKey = strconv.Itoa(b.ID), b.ProjectKey
		}
	}
	if boardID == "" {
		t.Fatal("fixture has no MOB board")
	}
	_, _ = src.Test(ctx)
	_, _ = src.SearchSpaces(ctx, "m")
	_, _ = src.SearchBoards(ctx, spaceKey, "")
	_, _ = src.BoardConfig(ctx, boardID)
	_, _ = src.ActiveSprints(ctx, boardID)
	_, _, _ = src.Reference(ctx)
	_, _ = src.SearchUsers(ctx, spaceKey, "")
	_, _, _ = src.ValidateFilter(ctx, "project = MOB")
	srv.Mutate(func(ds *jiratest.Dataset) {
		for _, is := range ds.Issues {
			if is.ProjectKey == "MOB" && issueID == "" {
				issueID = is.ID
			}
		}
	})
	_, _ = src.Hydrate(ctx, []string{issueID}, nil)
	_, _ = src.DevActivity(ctx, issueID, "")
	a := src.(*Adapter)
	_ = a.c.GetJSON(ctx, "/rest/api/3/myself", nil, nil)
	_ = a.c.PostJSON(WithWrite(ctx), "/rest/api/3/jql/parse", nil, map[string]any{"queries": []string{"x"}}, nil)

	seen, bad := chk.result()
	if seen < 10 {
		t.Fatalf("only %d requests reached the network; the test is not exercising the adapter", seen)
	}
	if len(bad) != 0 {
		t.Fatalf("%d requests reached http.Client without a limiter permit: %v", len(bad), bad)
	}
	st := LimiterFor(conn).Stats()
	if got := st.Requests[0] + st.Requests[1] + st.Requests[2]; got != int64(seen) {
		t.Fatalf("limiter granted %d permits for %d requests on the wire", got, seen)
	}
	if st.InFlight != 0 {
		t.Fatalf("%d permits leaked", st.InFlight)
	}
	if st.Requests[limiter.Sync] == 0 || st.Requests[limiter.Interactive] == 0 {
		t.Fatalf("requests per lane = %v: existing adapter calls are sync, a marked write is interactive", st.Requests)
	}
}

func TestBypass_RedirectHopsAreEachLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/b", http.StatusFound) })
	mux.HandleFunc("/b", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{}")) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var c *Client
	chk := &bypassChecker{base: &http.Transport{DisableKeepAlives: true}, lim: func() *limiter.Limiter { return c.Limiter() }}
	c, _, _ = newWP06Client(t, srv.URL, PolicyOptions{}, func(o *ClientOptions) { o.HTTP = &http.Client{Transport: chk} })
	if err := c.GetJSON(context.Background(), "/a", nil, nil); err != nil {
		t.Fatal(err)
	}
	if seen, bad := chk.result(); seen != 2 || len(bad) != 0 {
		t.Fatalf("seen=%d bad=%v, want both hops limited", seen, bad)
	}
	if st := c.Limiter().Stats(); st.Requests[limiter.Sync] != 2 || st.InFlight != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestBypass_RetriesAreEachLimited(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 503}, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "1"}}, wp06Resp{})
	var c *Client
	chk := &bypassChecker{base: &http.Transport{DisableKeepAlives: true}, lim: func() *limiter.Limiter { return c.Limiter() }}
	c, _, _ = newWP06Client(t, srv.URL, PolicyOptions{}, func(o *ClientOptions) { o.HTTP = &http.Client{Transport: chk} })
	if err := c.GetJSON(context.Background(), "/p", nil, nil); err != nil {
		t.Fatal(err)
	}
	if seen, bad := chk.result(); seen != 3 || len(bad) != 0 {
		t.Fatalf("seen=%d bad=%v, want 3 limited attempts", seen, bad)
	}
}

func TestBypass_AClientBuiltFromAnAlreadyLimitedHTTPClientDoesNotLimitTwice(t *testing.T) {
	// Double wrapping would deadlock at Concurrency 1: the outer permit would wait for the inner one.
	srv := newWP06Server(t)
	pol, _ := newWP06Policy(t, PolicyOptions{Concurrency: 1})
	c1 := NewClient(srv.URL, wp06Account, wp06Token, ClientOptions{HTTP: &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}, Policy: pol})
	c2 := NewClient(srv.URL, wp06Account, wp06Token, ClientOptions{HTTP: c1.http, Policy: pol})
	done := make(chan error, 1)
	go func() { done <- c2.GetJSON(context.Background(), "/p", nil, nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: the transport was wrapped twice")
	}
}

// ── registry ────────────────────────────────────────────────────────────────────────────────────

func TestRegistry_OneLimiterPerConnectionSharedByAllClients(t *testing.T) {
	pol, _ := newWP06Policy(t, PolicyOptions{})
	_ = DefaultPolicy() // make sure there is a default to restore (SetDefaultPolicy would otherwise restore nil)
	restore := SetDefaultPolicy(pol)
	t.Cleanup(restore)
	conn := model.Connection{ID: 3, Source: Kind, BaseURL: "http://127.0.0.1:1/", Account: "Me@Acme.com"}
	a1, err := New(conn, "tok", source.Options{})
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := New(conn, "tok", source.Options{})
	l1, l2 := a1.(*Adapter).c.Limiter(), a2.(*Adapter).c.Limiter()
	if l1 != l2 {
		t.Fatal("two Adapters of one connection must share one limiter")
	}
	if LimiterFor(conn) != l1 {
		t.Fatal("LimiterFor(conn) must return the limiter the connection's clients use")
	}
	same := conn
	same.BaseURL, same.Account = "http://127.0.0.1:1", "me@acme.com" // trailing slash and case do not make a new connection
	if LimiterFor(same) != l1 {
		t.Fatal("the key must be normalised")
	}
	other := conn
	other.BaseURL = "http://127.0.0.1:2"
	if LimiterFor(other) == l1 {
		t.Fatal("a different site must get its own limiter")
	}
}

func TestRegistry_LimiterIsScopedToThePolicyItWasBuiltFor(t *testing.T) {
	// SetDefaultPolicy (tests) swaps the numbers; a stale limiter must not outlive the policy it was built from.
	p1, _ := newWP06Policy(t, PolicyOptions{RatePerSecond: 5})
	p2, _ := newWP06Policy(t, PolicyOptions{RatePerSecond: 6})
	c1 := NewClient("http://127.0.0.1:9", "a", "t", ClientOptions{Policy: p1})
	c1b := NewClient("http://127.0.0.1:9", "a", "t", ClientOptions{Policy: p1})
	c2 := NewClient("http://127.0.0.1:9", "a", "t", ClientOptions{Policy: p2})
	if c1.Limiter() != c1b.Limiter() {
		t.Fatal("same policy and connection: one limiter")
	}
	if c1.Limiter() == c2.Limiter() {
		t.Fatal("a different policy must not reuse the limiter")
	}
	if r := c1.Limiter().Stats().MaxRate; r != 5 {
		t.Fatalf("limiter max rate = %v, want the policy's 5", r)
	}
	if r := c2.Limiter().Stats().MaxRate; r != 6 {
		t.Fatalf("limiter max rate = %v, want the policy's 6", r)
	}
}

func TestPolicy_DefaultsFollowTheSpecTable(t *testing.T) {
	// jira-sync §3.1: 8 req/s, burst 16, 4 in flight (2 for media).
	c := NewClient("http://127.0.0.1:9", "a", "t", ClientOptions{Policy: NewPolicy(PolicyOptions{})})
	if st := c.Limiter().Stats(); st.MaxRate != 8 || st.Rate != 8 {
		t.Fatalf("default rate = %v (max %v), want 8", st.Rate, st.MaxRate)
	}
}

// ── lanes ───────────────────────────────────────────────────────────────────────────────────────

func TestLanes_ContextMarksSelectTheLane(t *testing.T) {
	srv := newWP06Server(t)
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	for _, ctx := range []context.Context{
		context.Background(), // existing calls: sync
		WithInteractive(context.Background()),
		WithMedia(context.Background()),
		WithWrite(context.Background()),
	} {
		if err := c.PostJSON(ctx, "/p", nil, map[string]any{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if st := c.Limiter().Stats(); st.Requests != [3]int64{2, 1, 1} {
		t.Fatalf("requests per lane = %v, want interactive 2 (marked + write), sync 1, media 1", st.Requests)
	}
}

// ── 429 and the shared gate ─────────────────────────────────────────────────────────────────────

func TestGate_A429SeenByOneClientPausesEveryOtherClientOfTheConnection(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "3"}}, wp06Resp{})
	c1, s, pol := newWP06Client(t, srv.URL, PolicyOptions{Max429Retries: 1}, nil)
	_ = c1.GetJSON(context.Background(), "/p", nil, nil) // 429 → retried once after the gate wait
	before := len(s.significant())
	c2 := NewClient(srv.URL, wp06Account, wp06Token, ClientOptions{HTTP: &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}, Policy: pol})
	if c2.Limiter() != c1.Limiter() {
		t.Fatal("clients of one connection share one limiter")
	}
	// Close the gate again as a honoured 429 does, then let the *other* client's interactive request run into it.
	c1.Limiter().CloseGate(time.Now().Add(4*time.Second + 250*time.Millisecond))
	if err := c2.GetJSON(WithInteractive(context.Background()), "/p", nil, nil); err != nil {
		t.Fatal(err)
	}
	got := s.significant()
	if len(got) != before+1 {
		t.Fatalf("sleeps = %v: the second client must wait out the shared gate exactly once", got)
	}
	wp06AssertNear(t, got[len(got)-1], 4*time.Second+250*time.Millisecond)
}

func TestGate_429HalvesTheConnectionRateAndSyncRequestsStillRetry(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "3"}}, wp06Resp{})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	if err := c.GetJSON(context.Background(), "/p", nil, nil); err != nil {
		t.Fatal(err)
	}
	if srv.hits() != 2 {
		t.Fatalf("hits = %d, want the sync request retried once", srv.hits())
	}
	if got := s.significant(); len(got) != 1 {
		t.Fatalf("sleeps = %v, want one gate wait", got)
	} else {
		wp06AssertNear(t, got[0], 3*time.Second+250*time.Millisecond)
	}
	if r := c.Limiter().Stats().Rate; r != 5e5 { // 1e6 halved
		t.Fatalf("rate = %v, want halved", r)
	}
}

func TestSignals_NearLimitResponseHalvesTheRate(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Header: map[string]string{"X-RateLimit-NearLimit": "true"}})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{RatePerSecond: 8}, nil)
	if err := c.GetJSON(context.Background(), "/p", nil, nil); err != nil {
		t.Fatal(err)
	}
	if r := c.Limiter().Stats().Rate; r != 4 {
		t.Fatalf("rate = %v, want 4", r)
	}
}

// ── writes ──────────────────────────────────────────────────────────────────────────────────────

func TestWrites_429IsNotRetriedAndCarriesRetryInMs(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "6"}}, wp06Resp{})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	err := c.PostJSON(WithWrite(context.Background()), "/rest/api/3/issue/MOB-1/transitions", nil, map[string]any{}, nil)
	if he := wp06HTTPErr(t, err); he.Status != 429 {
		t.Fatalf("status = %d", he.Status)
	}
	if srv.hits() != 1 {
		t.Fatalf("hits = %d, want 1: writes are never retried", srv.hits())
	}
	if got := s.significant(); len(got) != 0 {
		t.Fatalf("sleeps = %v: a write must return at once", got)
	}
	var ae *apperr.Error
	if !errors.As(MapWriteError(err), &ae) || ae.Code != apperr.RateLimited || ae.RetryInMs != 6000 {
		t.Fatalf("MapWriteError = %+v, want rate_limited with retryInMs 6000", ae)
	}
	if req, thr := c.Counts(); req != 1 || thr != 1 {
		t.Fatalf("Counts = (%d, %d)", req, thr)
	}
}

func TestWrites_429WithoutHeadersFallsBackToFiveSeconds(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	err := c.PostJSON(WithWrite(context.Background()), "/w", nil, map[string]any{}, nil)
	var ae *apperr.Error
	if !errors.As(MapWriteError(err), &ae) || ae.RetryInMs != 5000 {
		t.Fatalf("MapWriteError = %+v, want retryInMs 5000", ae)
	}
}

func TestWrites_WhileTheGateIsClosedFailAtOnceWithoutTouchingTheNetwork(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "6"}}, wp06Resp{})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	_ = c.PostJSON(WithWrite(context.Background()), "/w", nil, map[string]any{}, nil) // 429: closes the gate
	err := c.PostJSON(WithWrite(context.Background()), "/w", nil, map[string]any{}, nil)
	if srv.hits() != 1 {
		t.Fatalf("hits = %d, want 1: the second write must not reach Jira", srv.hits())
	}
	var ae *apperr.Error
	if !errors.As(MapWriteError(err), &ae) || ae.Code != apperr.RateLimited {
		t.Fatalf("MapWriteError = %v, want rate_limited", err)
	}
	if ae.RetryInMs <= 0 || ae.RetryInMs > 6250 {
		t.Fatalf("retryInMs = %d, want the remaining gate (0, 6250]", ae.RetryInMs)
	}
	if got := s.significant(); len(got) != 0 {
		t.Fatalf("sleeps = %v: a write is refused, never queued behind the gate", got)
	}
	// A sync read, by contrast, waits the gate out and goes through.
	if err := c.GetJSON(context.Background(), "/r", nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := s.significant(); len(got) != 1 {
		t.Fatalf("sleeps = %v, want the read to wait one gate", got)
	}
}

func TestWrites_5xxIsNotRetriedAndMapsToSourceHTTP(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 503}, wp06Resp{})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	err := c.PostJSON(WithWrite(context.Background()), "/w", nil, map[string]any{}, nil)
	if srv.hits() != 1 {
		t.Fatalf("hits = %d, want 1", srv.hits())
	}
	if got := apperr.CodeOf(MapWriteError(err)); got != apperr.SourceHTTP {
		t.Fatalf("code = %q, want source_http", got)
	}
	if got := s.significant(); len(got) != 0 {
		t.Fatalf("sleeps = %v: no backoff for a write", got)
	}
}

func TestWrites_TransportErrorIsNotRetried(t *testing.T) {
	srv := newWP06Server(t)
	rt := &wp06FlakyRT{next: &http.Transport{DisableKeepAlives: true}, failures: -1}
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, func(o *ClientOptions) { o.HTTP = &http.Client{Transport: rt} })
	err := c.PostJSON(WithWrite(context.Background()), "/w", nil, map[string]any{}, nil)
	if err == nil || rt.count() != 1 {
		t.Fatalf("err = %v, attempts = %d, want one failed attempt", err, rt.count())
	}
	if got := s.significant(); len(got) != 0 {
		t.Fatalf("sleeps = %v: no backoff for a write", got)
	}
}

func TestWrites_SuccessIsUnchanged(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Body: `{"id":"10001"}`})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	var out struct {
		ID string `json:"id"`
	}
	if err := c.PostJSON(WithWrite(context.Background()), "/w", nil, map[string]any{"a": 1}, &out); err != nil || out.ID != "10001" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	if MapWriteError(nil) != nil {
		t.Fatal("MapWriteError(nil) must be nil")
	}
}

func TestMapWriteError_NonRateLimitErrorsPassThroughMapError(t *testing.T) {
	err := &HTTPError{Status: 403, Method: "POST", Path: "/w"}
	if got, want := apperr.CodeOf(MapWriteError(err)), apperr.CodeOf(MapError(err)); got != want {
		t.Fatalf("code = %q, want %q", got, want)
	}
}

// ── per-issue serialisation ─────────────────────────────────────────────────────────────────────

func TestIssueLock_SecondWriterOnTheSameIssueGetsConflict(t *testing.T) {
	c := NewClient("http://127.0.0.1:9", "a", "t", ClientOptions{Policy: NewPolicy(PolicyOptions{})})
	rel, err := c.TryLockIssue("MOB-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.TryLockIssue("MOB-1")
	if got := apperr.CodeOf(err); got != apperr.Conflict {
		t.Fatalf("code = %q (err %v), want conflict", got, err)
	}
	c2 := NewClient("http://127.0.0.1:9", "a", "t", ClientOptions{Policy: c.pol}) // same connection: same lock table
	if _, err := c2.TryLockIssue("MOB-1"); apperr.CodeOf(err) != apperr.Conflict {
		t.Fatalf("a second Client of the connection must see the lock: %v", err)
	}
	rel()
	rel2, err := c.TryLockIssue("MOB-1")
	if err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	rel2()
}

// ── request timeout vs limiter waits ────────────────────────────────────────────────────────────

// The 30 s http.Client.Timeout is a per-request budget for talking to Jira. Time spent queued behind the limiter, the
// 429 gate or the policy slot is not talking to Jira and must not eat it: a Retry-After of 40 s is honoured, not turned
// into a timeout and a backoff retry. (Real timers on purpose: this is the Sleep == nil path.)
func TestTimeout_WaitingForTheLimiterDoesNotSpendTheRequestTimeout(t *testing.T) {
	srv := newWP06Server(t)
	pol := NewPolicy(PolicyOptions{RatePerSecond: 1e6})
	var logs bytes.Buffer
	c := NewClient(srv.URL, wp06Account, wp06Token, ClientOptions{
		HTTP:   &http.Client{Timeout: 100 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}},
		Policy: pol,
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	c.Limiter().CloseGate(time.Now().Add(600 * time.Millisecond)) // six times the request timeout
	start := time.Now()
	if err := c.GetJSON(context.Background(), "/p", nil, nil); err != nil {
		t.Fatalf("GetJSON: %v: the gate wait was charged to the request timeout", err)
	}
	if srv.hits() != 1 {
		t.Fatalf("hits = %d, want 1", srv.hits())
	}
	// A timeout during the wait would be retried after a 250-750 ms backoff and still end in one hit; the log shows it
	// (the elapsed bound is generous so a loaded CI machine does not trip it).
	if strings.Contains(logs.String(), "transport failure") || time.Since(start) > 1200*time.Millisecond {
		t.Fatalf("the request timed out while waiting for the gate and was retried (took %v): %s", time.Since(start), logs.String())
	}
}

func TestTimeout_StillBoundsTheExchangeWithJiraAndIsRetriedAsBefore(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-r.Context().Done() // never answers
	}))
	t.Cleanup(srv.Close)
	pol := NewPolicy(PolicyOptions{RatePerSecond: 1e6, BaseBackoff: time.Millisecond})
	c := NewClient(srv.URL, wp06Account, wp06Token, ClientOptions{
		HTTP:   &http.Client{Timeout: 50 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}},
		Policy: pol,
	})
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	if got := apperr.CodeOf(MapError(err)); got != apperr.Timeout {
		t.Fatalf("code = %q (err %v), want timeout", got, err)
	}
	if n := hits.Load(); n != 4 { // 1 + 3 retries, as for any transport failure
		t.Fatalf("hits = %d, want 4", n)
	}
	if st := c.Limiter().Stats(); st.InFlight != 0 {
		t.Fatalf("%d permits leaked after timeouts", st.InFlight)
	}
}

func TestTimeout_AClientBuiltFromALimitedHTTPClientKeepsTheTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(srv.Close)
	pol := NewPolicy(PolicyOptions{RatePerSecond: 1e6, BaseBackoff: time.Millisecond, MaxRetries5xx: 1})
	c1 := NewClient(srv.URL, wp06Account, wp06Token, ClientOptions{
		HTTP: &http.Client{Timeout: 50 * time.Millisecond, Transport: &http.Transport{DisableKeepAlives: true}}, Policy: pol})
	c2 := NewClient(srv.URL, wp06Account, wp06Token, ClientOptions{HTTP: c1.http, Policy: pol})
	err := c2.GetJSON(context.Background(), "/p", nil, nil)
	if got := apperr.CodeOf(MapError(err)); got != apperr.Timeout {
		t.Fatalf("code = %q (err %v), want timeout", got, err)
	}
}

// ── policy slot ordering ────────────────────────────────────────────────────────────────────────

func TestPolicySlot_FailedSlotWaitReleasesTheLimiterPermit(t *testing.T) {
	srv := newWP06Server(t)
	c, _, pol := newWP06Client(t, srv.URL, PolicyOptions{Concurrency: 1}, nil)
	hold, err := pol.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	var failed atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		failed.Store(c.GetJSON(ctx, "/p", nil, nil) != nil)
	}()
	cancel()
	<-done
	if !failed.Load() {
		t.Fatal("request succeeded despite a held policy slot")
	}
	if st := c.Limiter().Stats(); st.InFlight != 0 {
		t.Fatalf("limiter in flight = %d: the permit leaked", st.InFlight)
	}
}

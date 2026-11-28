package jira

// Per-connection rate limiter wiring (WP-T0, jira-sync §3.1, ADR 0015, docs/wp-notes/F12-freeze.md decision 2).
//
// How it attaches: the Client's http.Client gets a limiter.Transport (NewClient → limit). Every request any code path
// makes through c.http, including retries, redirect hops and the raw downloads of later work packages, therefore takes a
// limiter permit before it can reach the network; there is no call site to forget. The contract types Client,
// ClientOptions, Policy and Adapter are frozen, so nothing is added to them: the limiter is found through the
// transport (Client.Limiter) and through the registry below (LimiterFor).
//
// Request path:   http.Client → limiter.Transport (lane, token, concurrency, gate) → Policy.slot (process-wide
// in-flight cap) → countRT (sync_run.requests) → the caller's transport.

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source/jira/limiter"
)

// limiterEntry is one registry slot: the limiter plus the Policy whose numbers built it.
type limiterEntry struct {
	pol *Policy
	lim *limiter.Limiter
}

var limiters = struct {
	mu sync.Mutex
	m  map[string]limiterEntry
}{m: map[string]limiterEntry{}}

// connectionKey identifies a connection for the registry. F12 decision 2 says "connection ID, or base URL while the
// connection is not saved yet"; Adapter.New (frozen in contract.go) passes the Client only the site and account, and
// connection.base_url is UNIQUE (0001_init.sql), so site + account names the same connection either way. The key is
// normalised (case, trailing slash) and is never logged.
func connectionKey(baseURL, account string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(baseURL), "/")) + "|" + strings.ToLower(strings.TrimSpace(account))
}

// limiterFor returns the connection's limiter, creating it from pol's numbers on first use. An entry is tied to the
// Policy it was built from: a different Policy (SetDefaultPolicy in tests, or a changed jira.max_rps) gets a fresh
// limiter, so stale numbers and stale gate state never leak across policies or reused test-server ports.
func limiterFor(key string, pol *Policy) *limiter.Limiter {
	limiters.mu.Lock()
	defer limiters.mu.Unlock()
	if e, ok := limiters.m[key]; ok && e.pol == pol {
		return e.lim
	}
	l := limiter.New(pol.limiterConfig())
	limiters.m[key] = limiterEntry{pol: pol, lim: l}
	return l
}

// LimiterFor returns the limiter shared by every Client and Adapter of conn (under the process-wide DefaultPolicy). Use
// it for Diagnostics (Stats) and to subscribe to throttle events; requests must not be paced through it by hand.
func LimiterFor(conn model.Connection) *limiter.Limiter {
	return limiterFor(connectionKey(conn.BaseURL, conn.Account), DefaultPolicy())
}

// limiterConfig derives the limiter's numbers from the Policy, so SetDefaultPolicy (the tests' fast policy, a future
// jira.max_rps) keeps working unchanged: rate = RatePerSecond (burst = twice that), concurrency = Concurrency (media
// half of it, at least 1), gate budget = MaxGate, jitter and sleeping as the policy defines them.
func (p *Policy) limiterConfig() limiter.Config {
	return limiter.Config{
		MaxRate:          p.opts.RatePerSecond,
		Concurrency:      p.opts.Concurrency,
		MediaConcurrency: max(1, p.opts.Concurrency/2),
		MaxGate:          p.opts.MaxGate,
		Jitter:           p.opts.Jitter,
		Clock:            policyClock{p},
		// The Policy's gate and sleeper stay the way a 429 is waited out (client and sync tests pin both fake-Sleep styles,
		// and real-time Retry-After handling): the limiter closes the Policy gate as it closes its own, and waits for a
		// closed gate through Policy.waitGateUntil (honouring PolicyOptions.Sleep and never bypassing until even if
		// OnGate runs after bump). The limiter's own gate stays per connection, for fail-fast writes, Stats and lane
		// order; the Policy gate is process-wide, as before.
		ManualGate: true, // Client.do closes the gate after its retry and cumulative-wait budgets (a Retry-After beyond MaxGate closes none)
		OnGate:     p.closeGate,
		WaitGate:   p.waitGateUntil,
	}
}

// policyClock is the limiter's clock: real wall time, with waits going through PolicyOptions.Sleep when the policy has
// one, so tests that inject a fake Sleep see (and control) every wait the limiter makes, exactly like the retry waits.
type policyClock struct{ pol *Policy }

func (policyClock) Now() time.Time { return time.Now() }

func (c policyClock) TimerAt(at time.Time) (<-chan time.Time, func()) {
	d := time.Until(at)
	if c.pol.opts.Sleep == nil {
		t := time.NewTimer(d)
		return t.C, func() { t.Stop() }
	}
	ch := make(chan time.Time, 1)
	done := make(chan struct{})
	var once sync.Once
	go func() { // a one-shot wait, not a ticker: it ends when Sleep returns or stop is called
		if d <= 0 || c.pol.opts.Sleep(done, d) {
			ch <- time.Now()
		}
	}()
	return ch, func() { once.Do(func() { close(done) }) }
}

// countRT counts the requests that actually reach the network (sync_run.requests, Client.Counts): after the limiter and
// the policy slot, so a request that was cancelled or refused while waiting is not counted.
type countRT struct {
	base http.RoundTripper
	n    func()
}

func (r *countRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.n()
	return r.base.RoundTrip(req)
}

// limit wraps c.http's transport so that nothing sent through it skips the connection's limiter. An http.Client that
// was already limited (a Client built from another Client's http.Client) is unwrapped first, never limited twice: a
// second wrap would wait on its own permit.
func (c *Client) limit(account string) {
	hc := *c.http
	base := hc.Transport
	// http.Client.Timeout starts before RoundTrip, so it would count the time spent waiting for the limiter (a 429 gate of
	// 40 s, an AIMD-slowed queue) as time talking to Jira and turn a polite wait into a timeout plus backoff retries. The
	// transport applies the same budget after the permit and slot are granted instead.
	timeout := hc.Timeout
	hc.Timeout = 0
	if lt, ok := base.(*limiter.Transport); ok {
		base = lt.Base
		if timeout == 0 {
			timeout = lt.Timeout
		}
		if ct, ok := base.(*countRT); ok {
			base = ct.base
		}
	}
	if base == nil {
		base = http.DefaultTransport
	}
	hc.Transport = &limiter.Transport{
		Base:    &countRT{base: base, n: c.countRequest},
		Limiter: limiterFor(connectionKey(c.base, account), c.pol),
		Slot:    c.pol.slot,
		Timeout: timeout,
	}
	c.http = &hc
}

func (c *Client) countRequest() {
	c.mu.Lock()
	c.reqs++
	c.mu.Unlock()
}

// Limiter returns this Client's connection limiter (shared with every other Client of the connection).
func (c *Client) Limiter() *limiter.Limiter {
	if lt, ok := c.http.Transport.(*limiter.Transport); ok {
		return lt.Limiter
	}
	return nil // unreachable: NewClient always installs the transport
}

// TryLockIssue takes the per-issue write lock of the connection (jira-sync §3.1: one write or Refresh in flight per
// issue). A second caller gets an apperr `conflict` and must not send anything. Call release when the write (or the
// refresh) is finished, success or not; it is idempotent.
func (c *Client) TryLockIssue(issueKey string) (release func(), err error) {
	rel, ok := c.Limiter().TryLockIssue(issueKey)
	if !ok {
		return nil, apperr.New(apperr.Conflict, "Another change to this issue is already in progress. Try again in a moment.")
	}
	return rel, nil
}

// MapWriteError is MapError for the error of a request made with WithWrite: a 429 (from Jira, or refused locally while
// the connection's gate is closed) becomes `rate_limited` carrying retryInMs, the wait the UI shows as "Jira is
// rate-limiting · try again in 6s" (jira-sync §3.1). Every other error maps exactly as MapError does.
func MapWriteError(err error) error {
	if err == nil {
		return nil
	}
	mapped := MapError(err)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusTooManyRequests {
		return mapped
	}
	if ae, ok := errors.AsType[*apperr.Error](mapped); ok && ae.Code == apperr.RateLimited {
		d := he.RetryAfter
		if d <= 0 {
			d = defaultRetryAfter
		}
		return ae.WithRetryIn((d + time.Millisecond - 1).Milliseconds())
	}
	return mapped
}

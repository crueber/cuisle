package jira

// WP-06 tests for policy.go: NewPolicy defaults, Acquire (semaphore, 429 gate, cancellation), Close429, Backoff,
// RetryAfter. Helpers prefixed wp06 are shared by client_test.go and errors_test.go (same package).
//
// SPEC-NOTE: PolicyOptions has no clock, so Acquire compares the 429 gate with the wall clock (time.Now). The fake
// Sleep used here records the requested duration and then "advances time" by clearing Policy.gate (via
// p.mu.TryLock, so an implementation that holds p.mu while sleeping simply skips the reset). Consequence for
// implementers: Acquire must call Sleep for the gate, then proceed once the gate is cleared; it may re-check in a
// loop, but a loop that never exits when Sleep returns true without time passing will trip the call cap below.
// SPEC-NOTE: Close429 jitter "0–500 ms" is Jitter()×500ms, so with Jitter fixed at 0.5 the added wait is
// retryAfter + 250 ms. Backoff(n) = BaseBackoff·2^(n−1)·(0.5+Jitter()), so Jitter 0.5 gives factor exactly 1.0.
// SPEC-NOTE: The token bucket cannot be timed (no injectable clock in the contract). Tests only pin that a fresh
// default policy (10/s) does not make the first Acquire sleep (bucket starts full, burst >= 1). Client tests use
// RatePerSecond 1e6 and ignore sleeps shorter than 1 ms so bucket waits cannot disturb exact assertions.
// SPEC-NOTE: A gate that is already in the past (Close429 called with an old "now") makes Acquire not sleep, and a
// later Close429 with a shorter wait never shortens an existing gate.
// SPEC-NOTE: Past X-RateLimit-Reset: RetryAfter returns a non-negative duration (0 or the 5 s fallback, either is
// accepted).

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"
)

// ── shared helpers (WP-06) ───────────────────────────────────────────────────

// wp06Sleeper is a fake PolicyOptions.Sleep. It never blocks.
type wp06Sleeper struct {
	t       *testing.T
	mu      sync.Mutex
	durs    []time.Duration
	pol     *Policy
	tripped bool
}

const wp06SleepCap = 50

func (s *wp06Sleeper) Sleep(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return false
	default:
	}
	s.mu.Lock()
	s.durs = append(s.durs, d)
	n := len(s.durs)
	trip := n > wp06SleepCap && !s.tripped
	if trip {
		s.tripped = true
	}
	pol := s.pol
	s.mu.Unlock()
	if n > wp06SleepCap {
		if trip {
			s.t.Errorf("Sleep called more than %d times: Acquire must Sleep for the gate once and then proceed (fake Sleep clears the gate to simulate elapsed time)", wp06SleepCap)
		}
		return false
	}
	if pol != nil && pol.mu.TryLock() {
		pol.gate = time.Time{}
		pol.mu.Unlock()
	}
	return true
}

// all returns every recorded sleep.
func (s *wp06Sleeper) all() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.durs...)
}

// significant returns recorded sleeps of at least 1 ms (ignores token-bucket micro waits).
func (s *wp06Sleeper) significant() []time.Duration {
	var out []time.Duration
	for _, d := range s.all() {
		if d >= time.Millisecond {
			out = append(out, d)
		}
	}
	return out
}

// newWP06Policy builds a Policy with the recording sleeper, Jitter 0.5 (unless set) and a very high rate.
func newWP06Policy(t *testing.T, opts PolicyOptions) (*Policy, *wp06Sleeper) {
	t.Helper()
	s := &wp06Sleeper{t: t}
	if opts.Jitter == nil {
		opts.Jitter = func() float64 { return 0.5 }
	}
	if opts.RatePerSecond == 0 {
		opts.RatePerSecond = 1e6
	}
	opts.Sleep = s.Sleep
	p := NewPolicy(opts)
	s.mu.Lock()
	s.pol = p
	s.mu.Unlock()
	return p, s
}

// wp06AssertNear asserts want-1s < got <= want (wall-clock gate sleeps lose the few ms that elapsed).
func wp06AssertNear(t *testing.T, got, want time.Duration) {
	t.Helper()
	if got > want || got <= want-time.Second {
		t.Fatalf("sleep = %v, want in (%v, %v]", got, want-time.Second, want)
	}
}

// ── NewPolicy ────────────────────────────────────────────────────────────────

func TestPolicy_NewPolicy_FillsDefaults(t *testing.T) {
	p := NewPolicy(PolicyOptions{})
	o := p.opts
	if o.Concurrency != 4 || o.RatePerSecond != 10 || o.Max429Retries != 5 || o.MaxGate != 120*time.Second ||
		o.MaxRetries5xx != 3 || o.BaseBackoff != 500*time.Millisecond {
		t.Fatalf("defaults = %+v", o)
	}
}

func TestPolicy_NewPolicy_KeepsExplicitOptions(t *testing.T) {
	p := NewPolicy(PolicyOptions{Concurrency: 2, RatePerSecond: 7, Max429Retries: 1, MaxGate: time.Second,
		MaxRetries5xx: 9, BaseBackoff: time.Millisecond})
	o := p.opts
	if o.Concurrency != 2 || o.RatePerSecond != 7 || o.Max429Retries != 1 || o.MaxGate != time.Second ||
		o.MaxRetries5xx != 9 || o.BaseBackoff != time.Millisecond {
		t.Fatalf("options = %+v", o)
	}
}

// ── Backoff / Close429 ───────────────────────────────────────────────────────

func TestPolicy_Backoff_DoublesPerAttemptWithJitterFactor(t *testing.T) {
	tests := []struct {
		name   string
		jitter float64
		base   time.Duration
		n      int
		want   time.Duration
	}{
		{"attempt 1 factor 1.0", 0.5, 500 * time.Millisecond, 1, 500 * time.Millisecond},
		{"attempt 2 factor 1.0", 0.5, 500 * time.Millisecond, 2, time.Second},
		{"attempt 3 factor 1.0", 0.5, 500 * time.Millisecond, 3, 2 * time.Second},
		{"attempt 4 factor 1.0", 0.5, 500 * time.Millisecond, 4, 4 * time.Second},
		{"jitter 0 is factor 0.5", 0, 500 * time.Millisecond, 1, 250 * time.Millisecond},
		{"jitter 0.75 is factor 1.25", 0.75, 500 * time.Millisecond, 1, 625 * time.Millisecond},
		{"custom base", 0.5, 100 * time.Millisecond, 3, 400 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := tc.jitter
			p := NewPolicy(PolicyOptions{BaseBackoff: tc.base, Jitter: func() float64 { return j }})
			if got := p.Backoff(tc.n); got != tc.want {
				t.Fatalf("Backoff(%d) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}

func TestPolicy_Close429_ReturnsRetryAfterPlusJitterUpTo500ms(t *testing.T) {
	tests := []struct {
		name       string
		jitter     float64
		retryAfter time.Duration
		want       time.Duration
	}{
		{"jitter 0", 0, 3 * time.Second, 3 * time.Second},
		{"jitter 0.25", 0.25, 3 * time.Second, 3*time.Second + 125*time.Millisecond},
		{"jitter 0.5", 0.5, 3 * time.Second, 3*time.Second + 250*time.Millisecond},
		{"zero retry-after still jitters", 0.5, 0, 250 * time.Millisecond},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := tc.jitter
			p := NewPolicy(PolicyOptions{Jitter: func() float64 { return j }})
			if got := p.Close429(time.Now(), tc.retryAfter); got != tc.want {
				t.Fatalf("Close429 = %v, want %v", got, tc.want)
			}
		})
	}
}

// ── Acquire: gate ────────────────────────────────────────────────────────────

func TestPolicy_Acquire_WaitsForClosedGate(t *testing.T) {
	p, s := newWP06Policy(t, PolicyOptions{})
	want := p.Close429(time.Now(), 3*time.Second) // 3.25 s
	release, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	got := s.significant()
	if len(got) != 1 {
		t.Fatalf("sleeps = %v, want exactly one gate sleep", got)
	}
	wp06AssertNear(t, got[0], want)
}

func TestPolicy_Acquire_GateAlreadyPassedDoesNotSleep(t *testing.T) {
	p, s := newWP06Policy(t, PolicyOptions{})
	p.Close429(time.Now().Add(-time.Hour), time.Second)
	release, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	if got := s.significant(); len(got) != 0 {
		t.Fatalf("sleeps = %v, want none for a gate in the past", got)
	}
}

func TestPolicy_Acquire_LaterGateIsNotShortenedByEarlierOne(t *testing.T) {
	p, s := newWP06Policy(t, PolicyOptions{})
	now := time.Now()
	p.Close429(now, 10*time.Second) // gate = now + 10.25 s
	p.Close429(now, time.Second)    // must not pull the gate in
	release, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	got := s.significant()
	if len(got) != 1 {
		t.Fatalf("sleeps = %v, want exactly one", got)
	}
	wp06AssertNear(t, got[0], 10*time.Second+250*time.Millisecond)
}

func TestPolicy_Acquire_FreshDefaultPolicyDoesNotSleep(t *testing.T) {
	s := &wp06Sleeper{t: t}
	p := NewPolicy(PolicyOptions{Sleep: s.Sleep, Jitter: func() float64 { return 0.5 }}) // default 10 requests/s
	s.pol = p
	release, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	if got := s.all(); len(got) != 0 {
		t.Fatalf("sleeps = %v, want none on a fresh token bucket", got)
	}
}

func TestPolicy_Acquire_GateSleepAbortedByContextReturnsContextError(t *testing.T) {
	p, _ := newWP06Policy(t, PolicyOptions{})
	p.Close429(time.Now(), 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := p.Acquire(ctx)
	if err == nil {
		release()
		t.Fatal("Acquire succeeded with a cancelled context and a closed gate")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// ── Acquire: semaphore ───────────────────────────────────────────────────────

func TestPolicy_Acquire_LimitsConcurrencyToConfiguredSlots(t *testing.T) {
	p, _ := newWP06Policy(t, PolicyOptions{Concurrency: 4})
	releases := make([]func(), 0, 4)
	for i := 0; i < 4; i++ {
		release, err := p.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, release)
	}
	// A fifth slot must not be available.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if release, err := p.Acquire(ctx); err == nil {
		release()
		t.Fatal("fifth Acquire succeeded with Concurrency 4")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("fifth Acquire err = %v, want context.DeadlineExceeded", err)
	}
	// Releasing one frees exactly one.
	releases[0]()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	release, err := p.Acquire(ctx2)
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	release()
	for _, r := range releases[1:] {
		r()
	}
}

func TestPolicy_Acquire_DefaultConcurrencyIsFour(t *testing.T) {
	p := NewPolicy(PolicyOptions{RatePerSecond: 1e6})
	var releases []func()
	for i := 0; i < 4; i++ {
		release, err := p.Acquire(context.Background())
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases = append(releases, release)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if release, err := p.Acquire(ctx); err == nil {
		release()
		t.Fatal("fifth Acquire succeeded with the default policy")
	}
	for _, r := range releases {
		r()
	}
}

func TestPolicy_Acquire_ReleaseFreesSlotForReuse(t *testing.T) {
	p, _ := newWP06Policy(t, PolicyOptions{Concurrency: 1})
	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		release, err := p.Acquire(ctx)
		if err != nil {
			cancel()
			t.Fatalf("iteration %d: %v", i, err)
		}
		release()
		cancel()
	}
}

func TestPolicy_Acquire_ContextCancelledBeforeCallWhileSlotsFull(t *testing.T) {
	p, _ := newWP06Policy(t, PolicyOptions{Concurrency: 1})
	hold, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := p.Acquire(ctx)
	if err == nil {
		release()
		t.Fatal("Acquire succeeded with a cancelled context and no free slot")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestPolicy_Acquire_ContextCancelledWhileWaitingReturnsContextError(t *testing.T) {
	p, _ := newWP06Policy(t, PolicyOptions{Concurrency: 1})
	hold, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := make(chan error, 1)
	go func() {
		release, err := p.Acquire(ctx)
		if err == nil {
			release()
		}
		res <- err
	}()
	cancel()
	select {
	case err := <-res:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not return after the context was cancelled")
	}
}

// ── RetryAfter ───────────────────────────────────────────────────────────────

func TestRetryAfter_Table(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	plus2 := time.FixedZone("plus2", 2*3600)
	tests := []struct {
		name       string
		retryAfter string
		reset      string
		want       time.Duration
	}{
		{"seconds", "3", "", 3 * time.Second},
		{"zero seconds", "0", "", 0},
		{"two minutes", "120", "", 2 * time.Minute},
		{"nothing at all is 5 s", "", "", 5 * time.Second},
		{"garbage retry-after and no reset is 5 s", "soon", "", 5 * time.Second},
		{"reset RFC 3339 UTC", "", now.Add(30 * time.Second).Format(time.RFC3339), 30 * time.Second},
		{"reset RFC 3339 with offset", "", now.Add(30 * time.Second).In(plus2).Format(time.RFC3339), 30 * time.Second},
		{"reset epoch seconds", "", strconv.FormatInt(now.Add(45*time.Second).Unix(), 10), 45 * time.Second},
		{"retry-after beats reset", "2", now.Add(90 * time.Second).Format(time.RFC3339), 2 * time.Second},
		{"garbage retry-after falls back to reset", "garbage", strconv.FormatInt(now.Add(10*time.Second).Unix(), 10), 10 * time.Second},
		{"garbage reset is 5 s", "", "garbage", 5 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RetryAfter(tc.retryAfter, tc.reset, now); got != tc.want {
				t.Fatalf("RetryAfter(%q, %q) = %v, want %v", tc.retryAfter, tc.reset, got, tc.want)
			}
		})
	}
}

func TestRetryAfter_PastResetIsNeverNegative(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	for _, reset := range []string{
		now.Add(-time.Minute).Format(time.RFC3339),
		strconv.FormatInt(now.Add(-time.Minute).Unix(), 10),
	} {
		if got := RetryAfter("", reset, now); got < 0 {
			t.Fatalf("RetryAfter(\"\", %q) = %v, want >= 0", reset, got)
		}
	}
}

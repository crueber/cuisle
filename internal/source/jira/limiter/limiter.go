// Package limiter is the per-connection Jira request limiter of jira-sync §3.1 (ADR 0015). It is stdlib-only and knows
// nothing about Jira beyond the rate-limit headers: one Limiter is shared by every standup and goroutine of a
// connection, and every request goes through it (see Transport).
//
// What it enforces (all numbers are the §3.1 defaults, see Defaults):
//
//   - a token bucket: 8 req/s sustained, burst 16;
//   - concurrency: 4 requests in flight, at most 2 of them media downloads;
//   - priority lanes: Interactive > Sync > Media. A waiting interactive request takes the next token or slot; media
//     runs only when no sync or interactive request waits;
//   - AIMD: X-RateLimit-NearLimit, or Remaining/Limit < 0.2 from either header family, halves the rate (floor 1 req/s);
//     the rate then recovers +1 req/s per 30 s without a signal, up to the configured maximum;
//   - a global gate: a 429 closes the gate for all lanes until Retry-After (fallbacks) plus 0-500 ms jitter, and halves
//     the rate;
//   - per-issue write serialisation (TryLockIssue).
//
// There are no tickers, workers or background goroutines. Recovery is computed lazily from timestamps whenever the
// limiter is touched, and a waiter parks on a one-shot timer of the injected Clock (or on a state-change broadcast).
package limiter

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

// Lane is a priority class. A lower value is served first.
type Lane int

const (
	// Interactive: writes, transitions, ticket Refresh, combobox search, Connect, validate (jira-sync §3.1).
	Interactive Lane = iota
	// Sync: plan, scan, hydrate, comments, dev-status. The default lane.
	Sync
	// Media: attachment and thumbnail downloads.
	Media
	numLanes
)

func (l Lane) String() string {
	switch l {
	case Interactive:
		return "interactive"
	case Sync:
		return "sync"
	case Media:
		return "media"
	}
	return fmt.Sprintf("lane(%d)", int(l))
}

// Config holds the limiter's numbers. A zero field takes its default (see Defaults); every default cites jira-sync §3.1.
type Config struct {
	MaxRate          float64             // sustained req/s at full speed. §3.1: 8 (hidden setting jira.max_rps, 1-20)
	Burst            float64             // bucket capacity at full speed. §3.1: 16 (it shrinks in proportion when the rate is halved)
	MinRate          float64             // AIMD floor. §3.1: 1 req/s
	RecoverStep      float64             // req/s regained per RecoverEvery without a signal. §3.1: +1
	RecoverEvery     time.Duration       // §3.1: 30 s
	HalveCooldown    time.Duration       // responses of one burst window halve once: signals within this of a halving only restart recovery. 1 s (Jira's burst window, §3.1 header example t=1); not in the spec
	Concurrency      int                 // in flight in total. §3.1: 4
	MediaConcurrency int                 // in flight for media (counted in Concurrency too). §3.1: 2
	MaxGate          time.Duration       // a Retry-After beyond this closes no gate (the caller fails instead). jira-sync §3: 120 s
	Jitter           func() float64      // [0,1) factor of the 0-500 ms gate jitter. nil = math/rand
	Clock            Clock               // nil = RealClock
	OnThrottle       func(ThrottleEvent) // called (outside the lock) when a 429 closes the gate; feeds sync:throttled {resumeAt, reason}

	// OnGate is called (outside the lock, inside Observe, before the 429 response reaches the caller) each time a 429
	// closes or extends the gate, with the gate's end. WaitGate, if set, replaces the limiter's own timer for waiting out
	// a closed gate: it must block until that gate is open or ctx ends. Both are optional bridges for a host that owns
	// another gate and sleeper (the jira Policy's, whose tests pin its behaviour); a limiter without them waits on its
	// Clock. After WaitGate returns nil the waiter proceeds without waiting for that same gate again.
	OnGate   func(until time.Time)
	WaitGate func(ctx context.Context, until time.Time) error

	// ManualGate: Observe(429) halves the rate but leaves the gate to the host, which closes it with CloseGate once it has
	// decided to honour that Retry-After (the jira client applies its retry and cumulative-wait budgets first, and a
	// Retry-After beyond MaxGate must leave the gate open). Without it a 429 closes the gate by itself.
	ManualGate bool
}

// Defaults returns the §3.1 numbers.
func Defaults() Config {
	return Config{
		MaxRate: 8, Burst: 16, MinRate: 1, RecoverStep: 1, RecoverEvery: 30 * time.Second, HalveCooldown: time.Second,
		Concurrency: 4, MediaConcurrency: 2, MaxGate: 120 * time.Second,
	}
}

// maxGateJitter is §3.1's "+ 0-500 ms jitter".
const maxGateJitter = 500 * time.Millisecond

// tokenEpsilon absorbs float rounding so a token that is 1e-9 short does not cost another timer.
const tokenEpsilon = 1e-9

// eventRing is how many recent throttle events Stats keeps (diagnostics).
const eventRing = 32

func (c Config) filled() Config {
	d := Defaults()
	if c.MaxRate <= 0 {
		c.MaxRate = d.MaxRate
	}
	if c.Burst <= 0 {
		c.Burst = 2 * c.MaxRate // §3.1: burst is twice the rate (8 → 16)
	}
	if c.MinRate <= 0 {
		c.MinRate = d.MinRate
	}
	c.MinRate = min(c.MinRate, c.MaxRate)
	if c.RecoverStep <= 0 {
		c.RecoverStep = d.RecoverStep
	}
	if c.RecoverEvery <= 0 {
		c.RecoverEvery = d.RecoverEvery
	}
	if c.HalveCooldown <= 0 {
		c.HalveCooldown = d.HalveCooldown
	}
	if c.Concurrency <= 0 {
		c.Concurrency = d.Concurrency
	}
	if c.MediaConcurrency <= 0 {
		c.MediaConcurrency = d.MediaConcurrency
	}
	c.MediaConcurrency = min(c.MediaConcurrency, c.Concurrency)
	if c.MaxGate <= 0 {
		c.MaxGate = d.MaxGate
	}
	if c.Jitter == nil {
		c.Jitter = defaultJitter
	}
	if c.Clock == nil {
		c.Clock = RealClock{}
	}
	return c
}

// ThrottleEvent is one slow-down decision, for diagnostics and sync:throttled.
type ThrottleEvent struct {
	At       time.Time
	ResumeAt time.Time // when the gate opens; zero for a slow-down that closes no gate
	Reason   string    // "rate_limited" (a 429) or "near_limit" (a header signal)
	Rate     float64   // req/s after the decision
}

// Limiter paces the requests of one connection. The zero value is not usable; call New.
type Limiter struct {
	cfg Config
	clk Clock

	mu         sync.Mutex
	tokens     float64
	last       time.Time // last refill
	rate       float64   // current req/s (AIMD)
	recoverAt  time.Time // recovery steps count from here; zero while the rate is at the maximum
	lastHalve  time.Time
	gate       time.Time
	inflight   int
	media      int
	queues     [numLanes][]*waiter
	changed    chan struct{} // closed and replaced on every state change a waiter may care about
	requests   [numLanes]int64
	throttles  int64
	nearLimits int64
	events     []ThrottleEvent
	issues     map[string]*issueLock
	lockSeq    uint64
	onThrottle func(ThrottleEvent) // Config.OnThrottle, replaceable with SetOnThrottle
}

type waiter struct {
	lane       Lane
	passedGate time.Time // the gate end this waiter already slept out
}

type issueLock struct{ id uint64 } // non-zero-size, so each lock has its own identity

// New builds a Limiter. cfg's zero fields take the §3.1 defaults.
func New(cfg Config) *Limiter {
	cfg = cfg.filled()
	l := &Limiter{cfg: cfg, clk: cfg.Clock, rate: cfg.MaxRate, changed: make(chan struct{}), issues: map[string]*issueLock{},
		onThrottle: cfg.OnThrottle}
	l.tokens = cfg.Burst // a fresh connection may burst at once
	l.last = l.clk.Now()
	return l
}

// Permit is one in-flight request slot. Release it exactly when the response body is closed or the request failed;
// extra calls are no-ops.
type Permit struct {
	l    *Limiter
	lane Lane
	once sync.Once
}

// Release frees the slot.
func (p *Permit) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() { p.l.release(p.lane) })
}

// AcquireOption tunes one Acquire call.
type AcquireOption func(*acquireOpts)

type acquireOpts struct{ failFast bool }

// FailFastOnGate makes Acquire return a *GateError at once while a 429 gate is closed, instead of waiting it out. It
// is for writes, which are never retried and report "try again in Ns" (ADR 0015).
func FailFastOnGate() AcquireOption { return func(o *acquireOpts) { o.failFast = true } }

// GateError is returned by a fail-fast Acquire while the 429 gate is closed.
type GateError struct {
	ResumeAt time.Time
	RetryIn  time.Duration
}

func (e *GateError) Error() string {
	return "jira is rate-limiting; retry in " + e.RetryIn.Round(time.Millisecond).String()
}

// Acquire waits for the gate, a token and a concurrency slot in lane order and returns a Permit. It returns
// ctx.Err() if ctx ends first (the waiter leaves the queue), or a *GateError when FailFastOnGate is set and the gate is
// closed. It sends nothing itself.
func (l *Limiter) Acquire(ctx context.Context, lane Lane, opts ...AcquireOption) (*Permit, error) {
	var o acquireOpts
	for _, fn := range opts {
		fn(&o)
	}
	if lane < 0 || lane >= numLanes {
		lane = Sync
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := &waiter{lane: lane}
	queued := false
	leave := func() { // l.mu held
		if queued {
			l.dequeue(w)
			queued = false
			l.bump()
		}
	}
	for {
		l.mu.Lock()
		now := l.clk.Now()
		l.advance(now)
		gateOpen := !l.gate.After(now) || !l.gate.After(w.passedGate)
		if !gateOpen && o.failFast {
			ge := &GateError{ResumeAt: l.gate, RetryIn: l.gate.Sub(now)}
			leave()
			l.mu.Unlock()
			return nil, ge
		}
		if !queued {
			l.queues[lane] = append(l.queues[lane], w)
			queued = true
			l.bump() // a new head may need to re-plan its wait
		}
		var deadline time.Time // zero = wait for a state change only
		var waitGate time.Time
		hook := false // wait for the gate through cfg.WaitGate instead of a timer
		if l.isHead(w) {
			switch {
			case !gateOpen:
				deadline, waitGate = l.gate, l.gate
				if l.cfg.WaitGate != nil {
					deadline, hook = time.Time{}, true
				}
			case !l.hasSlot(lane):
				// a Release wakes us
			case l.tokens < 1-tokenEpsilon:
				deadline = now.Add(max(time.Duration(math.Ceil((1-l.tokens)/l.rate*float64(time.Second))), time.Microsecond))
			default:
				l.tokens = max(l.tokens-1, 0)
				l.inflight++
				if lane == Media {
					l.media++
				}
				l.requests[lane]++
				l.dequeue(w)
				l.bump() // the next waiter is the new head
				l.mu.Unlock()
				return &Permit{l: l, lane: lane}, nil
			}
		}
		ev := l.changed
		l.mu.Unlock()

		if hook {
			if err := l.cfg.WaitGate(ctx, waitGate); err != nil {
				l.mu.Lock()
				leave()
				l.mu.Unlock()
				return nil, err
			}
			w.passedGate = waitGate
			continue
		}
		var timerC <-chan time.Time
		stop := func() {}
		if !deadline.IsZero() {
			timerC, stop = l.clk.TimerAt(deadline)
		}
		select {
		case <-ev:
		case <-timerC:
			if !waitGate.IsZero() {
				w.passedGate = waitGate // slept the whole gate: do not wait for it again, even if the clock lags
			}
		case <-ctx.Done():
			stop()
			l.mu.Lock()
			leave()
			l.mu.Unlock()
			return nil, ctx.Err()
		}
		stop()
	}
}

// isHead reports whether w is the first waiter of the highest non-empty lane. l.mu held.
func (l *Limiter) isHead(w *waiter) bool {
	for lane := Lane(0); lane < numLanes; lane++ {
		if len(l.queues[lane]) > 0 {
			return l.queues[lane][0] == w
		}
	}
	return false
}

func (l *Limiter) hasSlot(lane Lane) bool {
	if l.inflight >= l.cfg.Concurrency {
		return false
	}
	return lane != Media || l.media < l.cfg.MediaConcurrency
}

func (l *Limiter) dequeue(w *waiter) {
	q := l.queues[w.lane]
	for i, x := range q {
		if x == w {
			l.queues[w.lane] = append(q[:i:i], q[i+1:]...)
			return
		}
	}
}

func (l *Limiter) bump() {
	close(l.changed)
	l.changed = make(chan struct{})
}

func (l *Limiter) release(lane Lane) {
	l.mu.Lock()
	l.inflight--
	if lane == Media {
		l.media--
	}
	l.bump()
	l.mu.Unlock()
}

// capacity is the bucket size at the current rate: Burst at full speed, shrinking in proportion when slowed.
func (l *Limiter) capacity() float64 { return max(1, l.cfg.Burst*l.rate/l.cfg.MaxRate) }

// advance refills the bucket and applies the lazy AIMD recovery up to now. l.mu held.
func (l *Limiter) advance(now time.Time) {
	if el := now.Sub(l.last); el > 0 {
		l.tokens = min(l.tokens+el.Seconds()*l.rate, l.capacity())
		l.last = now
	}
	if l.rate < l.cfg.MaxRate && !l.recoverAt.IsZero() {
		if steps := int(now.Sub(l.recoverAt) / l.cfg.RecoverEvery); steps > 0 {
			l.rate = min(l.cfg.MaxRate, l.rate+float64(steps)*l.cfg.RecoverStep)
			l.recoverAt = l.recoverAt.Add(time.Duration(steps) * l.cfg.RecoverEvery)
			if l.rate >= l.cfg.MaxRate {
				l.recoverAt = time.Time{}
			}
			l.bump() // a faster rate shortens the head's wait
		}
	}
}

// Observe feeds a response's status and headers to the limiter: a 429 closes the gate (unless Config.ManualGate) and
// halves the rate; a near-limit signal halves the rate. Transport calls it for every response.
func (l *Limiter) Observe(status int, h http.Header) {
	var fire func()
	l.mu.Lock()
	now := l.clk.Now()
	l.advance(now)
	switch {
	case status == http.StatusTooManyRequests:
		var resume time.Time
		if ra := RetryAfter(h, now); ra <= l.cfg.MaxGate && !l.cfg.ManualGate {
			until := now.Add(ra + time.Duration(l.cfg.Jitter()*float64(maxGateJitter)))
			if until.After(l.gate) {
				l.gate = until
			}
			resume = l.gate
		}
		l.throttles++
		l.halve(now)
		ev := ThrottleEvent{At: now, ResumeAt: resume, Reason: "rate_limited", Rate: l.rate}
		l.record(ev)
		if !resume.IsZero() {
			fire = l.gateHooks(resume, ev)
		}
		l.bump()
	case Signal(h):
		l.nearLimits++
		if l.halve(now) {
			l.record(ThrottleEvent{At: now, Reason: "near_limit", Rate: l.rate})
		}
		l.bump()
	}
	l.mu.Unlock()
	if fire != nil {
		fire()
	}
}

// halve applies one multiplicative decrease unless the previous one is within HalveCooldown; either way the recovery
// clock restarts. It reports whether the rate changed. l.mu held.
func (l *Limiter) halve(now time.Time) bool {
	l.recoverAt = now
	if !l.lastHalve.IsZero() && now.Sub(l.lastHalve) < l.cfg.HalveCooldown {
		return false
	}
	l.lastHalve = now
	nr := max(l.cfg.MinRate, l.rate/2)
	if nr == l.rate {
		return false
	}
	l.rate = nr
	l.tokens = min(l.tokens, l.capacity())
	return true
}

func (l *Limiter) record(ev ThrottleEvent) {
	l.events = append(l.events, ev)
	if len(l.events) > eventRing {
		l.events = l.events[len(l.events)-eventRing:]
	}
}

// gateHooks returns the host callbacks to run, outside the lock, for a gate that now ends at resume: OnGate first (the
// host's gate must be closed before the caller sees the 429), then OnThrottle. nil when there is nothing to call.
func (l *Limiter) gateHooks(resume time.Time, ev ThrottleEvent) func() {
	onGate, onThrottle := l.cfg.OnGate, l.onThrottle // l.mu is held by every caller
	if onGate == nil && onThrottle == nil {
		return nil
	}
	ev.ResumeAt = resume
	return func() {
		if onGate != nil {
			onGate(resume)
		}
		if onThrottle != nil {
			onThrottle(ev)
		}
	}
}

// SetOnThrottle installs (or, with nil, removes) the callback that Config.OnThrottle sets at construction. Whoever owns
// the event stream (the app, for sync:throttled {resumeAt, reason}) attaches it to the connection's already-built
// limiter with it: jira.LimiterFor(conn).SetOnThrottle(fn). fn runs outside the limiter's lock.
func (l *Limiter) SetOnThrottle(fn func(ThrottleEvent)) {
	l.mu.Lock()
	l.onThrottle = fn
	l.mu.Unlock()
}

// CloseGate holds the gate for every lane until at least until; it never shortens an existing gate. It is how a host
// with Config.ManualGate closes the gate after its own budget decision (the jira client does, per 429 it will honour).
// OnGate and OnThrottle run when the gate was extended.
func (l *Limiter) CloseGate(until time.Time) {
	l.mu.Lock()
	var fire func()
	if until.After(l.gate) {
		l.gate = until
		ev := ThrottleEvent{At: l.clk.Now(), ResumeAt: until, Reason: "rate_limited", Rate: l.rate}
		for i := len(l.events) - 1; i >= 0; i-- { // give diagnostics the resume time of the 429 that caused this gate
			if l.events[i].Reason == "rate_limited" {
				if l.events[i].ResumeAt.IsZero() {
					l.events[i].ResumeAt = until
				}
				break
			}
		}
		fire = l.gateHooks(until, ev)
		l.bump()
	}
	l.mu.Unlock()
	if fire != nil {
		fire()
	}
}

// TryLockIssue takes the per-issue write lock (jira-sync §3.1: one write or Refresh in flight per issue). It returns
// ok=false when the issue is already locked; the caller reports `conflict`. release is idempotent and cannot free a
// lock that someone else took after the first release.
func (l *Limiter) TryLockIssue(key string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, held := l.issues[key]; held {
		return nil, false
	}
	l.lockSeq++
	mine := &issueLock{id: l.lockSeq}
	l.issues[key] = mine
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.issues[key] == mine {
				delete(l.issues, key)
			}
			l.mu.Unlock()
		})
	}, true
}

// Stats is a snapshot for Diagnostics.
type Stats struct {
	Rate          float64 // current req/s (after AIMD and lazy recovery)
	MaxRate       float64
	GateUntil     time.Time // zero or in the past when open
	InFlight      int
	MediaInFlight int
	Waiting       [3]int   // by Lane
	Requests      [3]int64 // granted permits by Lane, since the limiter was created
	Throttles     int64    // 429 responses seen
	NearLimit     int64    // near-limit signals seen
	Events        []ThrottleEvent
}

// Stats returns a snapshot (and applies pending lazy recovery).
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.advance(l.clk.Now())
	st := Stats{Rate: l.rate, MaxRate: l.cfg.MaxRate, GateUntil: l.gate, InFlight: l.inflight, MediaInFlight: l.media,
		Requests: l.requests, Throttles: l.throttles, NearLimit: l.nearLimits, Events: append([]ThrottleEvent(nil), l.events...)}
	for i := range l.queues {
		st.Waiting[i] = len(l.queues[i])
	}
	return st
}

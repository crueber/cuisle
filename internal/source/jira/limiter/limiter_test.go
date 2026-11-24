package limiter

// Limiter tests (jira-sync §3.1, docs/testing.md "Ticket refresh and throttling"). Every test runs on a FakeClock: there
// are no real sleeps, and a waiter parks on a fake timer until the test advances time.

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)

// newTest builds a limiter on a fake clock. cfg's zero fields take the §3.1 defaults, Jitter is fixed at 0.5.
func newTest(t *testing.T, cfg Config) (*Limiter, *FakeClock) {
	t.Helper()
	fc := NewFakeClock(t0)
	cfg.Clock = fc
	if cfg.Jitter == nil {
		cfg.Jitter = func() float64 { return 0.5 }
	}
	return New(cfg), fc
}

func mustAcquire(t *testing.T, l *Limiter, lane Lane, opts ...AcquireOption) *Permit {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := l.Acquire(ctx, lane, opts...)
	if err != nil {
		t.Fatalf("Acquire(%v): %v", lane, err)
	}
	return p
}

// result is what an asynchronous Acquire delivers; at is the fake time of the grant.
type result struct {
	p   *Permit
	err error
	at  time.Time
}

func acquireAsync(l *Limiter, fc *FakeClock, lane Lane, opts ...AcquireOption) <-chan result {
	ch := make(chan result, 1)
	go func() {
		p, err := l.Acquire(context.Background(), lane, opts...)
		ch <- result{p, err, fc.Now()}
	}()
	return ch
}

func waitWaiting(t *testing.T, l *Limiter, lane Lane, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for l.Stats().Waiting[lane] < n {
		if time.Now().After(deadline) {
			t.Fatalf("only %d waiters in lane %v, want %d", l.Stats().Waiting[lane], lane, n)
		}
		runtime.Gosched()
	}
}

func got(t *testing.T, ch <-chan result) result {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not return")
		return result{}
	}
}

// gotAny waits for exactly one of chans to deliver (goroutines enqueue in no fixed order, so FIFO position between
// concurrently started callers is not the call order) and returns its index.
func gotAny(t *testing.T, chans []<-chan result) (int, result) {
	t.Helper()
	cases := make([]reflect.SelectCase, 0, len(chans)+1)
	for _, ch := range chans {
		cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)})
	}
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(time.After(5 * time.Second))})
	i, v, _ := reflect.Select(cases)
	if i == len(chans) {
		t.Fatal("no Acquire returned")
	}
	return i, v.Interface().(result)
}

func notYet(t *testing.T, ch <-chan result, what string) {
	t.Helper()
	// Give a wrongly released waiter every chance to run before concluding it is still parked.
	for i := 0; i < 200; i++ {
		runtime.Gosched()
	}
	select {
	case r := <-ch:
		t.Fatalf("%s was released (err %v) but must still wait", what, r.err)
	default:
	}
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// ── defaults ────────────────────────────────────────────────────────────────────────────────────

func TestDefaults_MatchSpecTable(t *testing.T) {
	c := Defaults()
	if c.MaxRate != 8 || c.Burst != 16 || c.MinRate != 1 || c.RecoverStep != 1 || c.RecoverEvery != 30*time.Second ||
		c.Concurrency != 4 || c.MediaConcurrency != 2 || c.MaxGate != 120*time.Second || c.HalveCooldown != time.Second {
		t.Fatalf("Defaults() = %+v", c)
	}
}

func TestLaneNamesAndOrder(t *testing.T) {
	if !(Interactive < Sync && Sync < Media) {
		t.Fatalf("lane order must be interactive > sync > media (lower value = higher priority)")
	}
	for l, want := range map[Lane]string{Interactive: "interactive", Sync: "sync", Media: "media"} {
		if l.String() != want {
			t.Errorf("%d.String() = %q, want %q", l, l.String(), want)
		}
	}
}

// ── token bucket ────────────────────────────────────────────────────────────────────────────────

func TestBucket_BurstIsImmediateThenSustainedRate(t *testing.T) {
	l, fc := newTest(t, Config{Concurrency: 100})
	for i := 0; i < 16; i++ {
		mustAcquire(t, l, Sync).Release()
	}
	if !fc.Now().Equal(t0) {
		t.Fatalf("the burst of 16 must not wait, clock moved to %v", fc.Now().Sub(t0))
	}
	ch := acquireAsync(l, fc, Sync)
	fc.WaitPending(1, 5*time.Second)
	notYet(t, ch, "request 17")
	fc.Advance(124 * time.Millisecond) // one token takes 125 ms at 8 req/s
	notYet(t, ch, "request 17 after 124 ms")
	fc.Advance(time.Millisecond)
	if r := got(t, ch); r.err != nil {
		t.Fatalf("request 17: %v", r.err)
	}
}

func TestBucket_200RequestSyncNeverExceedsRatePlusBurst(t *testing.T) {
	l, fc := newTest(t, Config{Concurrency: 100})
	fc.SetAutoAdvance(true) // a lone caller: waiting for a token advances the fake time by exactly the wait
	var at []time.Duration
	for i := 0; i < 200; i++ {
		mustAcquire(t, l, Sync).Release()
		at = append(at, fc.Now().Sub(t0))
	}
	for i, d := range at {
		// By time d the bucket can have issued at most Burst + rate·d requests: request i+1 needs i+1 ≤ 16 + 8·d.
		if limit := 16 + 8*d.Seconds() + 1e-6; float64(i+1) > limit {
			t.Fatalf("request %d was sent at %v: %d requests > burst 16 + 8/s·%v", i+1, d, i+1, d)
		}
	}
	// And it is not needlessly slow: 200 requests take (200-16)/8 = 23 s.
	if total := at[len(at)-1]; total < 22900*time.Millisecond || total > 23100*time.Millisecond {
		t.Fatalf("200 requests took %v of fake time, want about 23 s", total)
	}
}

func TestBucket_IdleTimeRefillsOnlyToTheBurst(t *testing.T) {
	l, fc := newTest(t, Config{Concurrency: 100})
	for i := 0; i < 16; i++ {
		mustAcquire(t, l, Sync).Release()
	}
	fc.Advance(time.Hour)
	for i := 0; i < 16; i++ {
		mustAcquire(t, l, Sync).Release()
	}
	ch := acquireAsync(l, fc, Sync)
	fc.WaitPending(1, 5*time.Second)
	notYet(t, ch, "the 17th request after an idle hour")
	fc.Advance(125 * time.Millisecond)
	got(t, ch)
}

// ── priority lanes ──────────────────────────────────────────────────────────────────────────────

func TestLanes_InteractiveBehindHundredSyncRequestsGoesOutWithinOneTokenInterval(t *testing.T) {
	l, fc := newTest(t, Config{MaxRate: 10, Burst: 1, Concurrency: 1000})
	mustAcquire(t, l, Sync).Release() // drains the only burst token
	const n = 100
	syncCh := make([]<-chan result, n)
	for i := range syncCh {
		syncCh[i] = acquireAsync(l, fc, Sync)
	}
	waitWaiting(t, l, Sync, n)
	fc.WaitPending(1, 5*time.Second)
	inter := acquireAsync(l, fc, Interactive)
	waitWaiting(t, l, Interactive, 1)

	fc.Advance(100 * time.Millisecond) // exactly one token interval at 10 req/s
	r := got(t, inter)
	if r.err != nil {
		t.Fatalf("interactive: %v", r.err)
	}
	if r.at.Sub(t0) > 100*time.Millisecond {
		t.Fatalf("interactive went out %v after the drain, want within one token interval (100 ms)", r.at.Sub(t0))
	}
	if st := l.Stats(); st.Requests[Sync] != 1 || st.Waiting[Sync] != n {
		t.Fatalf("a sync request overtook the interactive one: requests %v, waiting %v", st.Requests, st.Waiting)
	}
	r.p.Release()

	// The sync queue then drains in FIFO order at the token rate.
	fc.Advance(100 * time.Millisecond)
	idx, first := gotAny(t, syncCh)
	if first.err != nil {
		t.Fatal(first.err)
	}
	for i, ch := range syncCh {
		if i == idx {
			continue
		}
		select {
		case <-ch:
			t.Fatalf("sync request %d went out in the same token interval as request %d", i, idx)
		default:
		}
	}
	first.p.Release()
}

func TestLanes_MediaNeverStartsWhileSyncRequestsWait(t *testing.T) {
	l, fc := newTest(t, Config{MaxRate: 10, Burst: 1, Concurrency: 100, MediaConcurrency: 100})
	mustAcquire(t, l, Sync).Release()
	media := acquireAsync(l, fc, Media)
	waitWaiting(t, l, Media, 1)
	syncs := []<-chan result{acquireAsync(l, fc, Sync), acquireAsync(l, fc, Sync)}
	waitWaiting(t, l, Sync, 2)

	fc.Advance(100 * time.Millisecond)
	i0, s0 := gotAny(t, syncs)
	s0.p.Release()
	notYet(t, media, "media while a second sync request waits")
	fc.Advance(100 * time.Millisecond)
	s1 := got(t, syncs[1-i0])
	s1.p.Release()
	notYet(t, media, "media in the same instant the last sync request left")
	fc.Advance(100 * time.Millisecond)
	got(t, media)
}

func TestLanes_InteractiveTakesTheNextConcurrencySlot(t *testing.T) {
	l, fc := newTest(t, Config{Concurrency: 1})
	hold := mustAcquire(t, l, Sync)
	syncCh := acquireAsync(l, fc, Sync)
	waitWaiting(t, l, Sync, 1)
	inter := acquireAsync(l, fc, Interactive)
	waitWaiting(t, l, Interactive, 1)
	hold.Release()
	r := got(t, inter)
	notYet(t, syncCh, "sync request while the interactive one holds the slot")
	r.p.Release()
	got(t, syncCh)
}

// ── concurrency ─────────────────────────────────────────────────────────────────────────────────

func TestConcurrency_FourInFlightTwoForMedia(t *testing.T) {
	l, fc := newTest(t, Config{MaxRate: 1e6})
	var held []*Permit
	for i := 0; i < 4; i++ {
		held = append(held, mustAcquire(t, l, Sync))
	}
	if st := l.Stats(); st.InFlight != 4 {
		t.Fatalf("in flight = %d, want 4", st.InFlight)
	}
	fifth := acquireAsync(l, fc, Sync)
	waitWaiting(t, l, Sync, 1)
	notYet(t, fifth, "the fifth request")
	held[0].Release()
	got(t, fifth).p.Release()
	for _, p := range held[1:] {
		p.Release()
	}

	// Media: at most 2 at once even though 4 slots are free.
	m1, m2 := mustAcquire(t, l, Media), mustAcquire(t, l, Media)
	m3 := acquireAsync(l, fc, Media)
	waitWaiting(t, l, Media, 1)
	notYet(t, m3, "the third media download")
	s := mustAcquire(t, l, Sync) // a sync request still fits next to the media downloads
	s.Release()
	m1.Release()
	got(t, m3).p.Release()
	m2.Release()
}

func TestPermit_ReleaseIsIdempotent(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 1, MaxRate: 1e6})
	p := mustAcquire(t, l, Sync)
	p.Release()
	p.Release()
	if st := l.Stats(); st.InFlight != 0 {
		t.Fatalf("in flight = %d after a double release", st.InFlight)
	}
	a := mustAcquire(t, l, Sync)
	if st := l.Stats(); st.InFlight != 1 {
		t.Fatalf("in flight = %d, want 1", st.InFlight)
	}
	a.Release()
}

// ── cancellation ────────────────────────────────────────────────────────────────────────────────

func TestAcquire_ContextCancelRemovesTheWaiter(t *testing.T) {
	l, fc := newTest(t, Config{Concurrency: 1})
	hold := mustAcquire(t, l, Sync)
	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { _, err := l.Acquire(ctx, Sync); errc <- err }()
	waitWaiting(t, l, Sync, 1)
	cancel()
	select {
	case err := <-errc:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not wake the waiter")
	}
	if st := l.Stats(); st.Waiting[Sync] != 0 {
		t.Fatalf("waiters = %d after cancel", st.Waiting[Sync])
	}
	hold.Release()
	mustAcquire(t, l, Sync).Release() // the abandoned waiter does not hold the slot
	if n := fc.Pending(); n != 0 {
		t.Fatalf("%d fake timers left registered", n)
	}
}

func TestAcquire_AlreadyCancelledContextSendsNothing(t *testing.T) {
	l, _ := newTest(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Acquire(ctx, Sync); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if st := l.Stats(); st.Requests[Sync] != 0 || st.InFlight != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestAcquire_CancelledWaiterHandsTheTurnToTheNext(t *testing.T) {
	l, fc := newTest(t, Config{MaxRate: 10, Burst: 1, Concurrency: 100})
	mustAcquire(t, l, Sync).Release()
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := l.Acquire(ctx, Sync); first <- err }()
	waitWaiting(t, l, Sync, 1)
	second := acquireAsync(l, fc, Sync)
	waitWaiting(t, l, Sync, 2)
	cancel()
	<-first
	fc.Advance(100 * time.Millisecond)
	got(t, second)
}

// ── AIMD slow-down ──────────────────────────────────────────────────────────────────────────────

func TestAIMD_NearLimitHeaderHalvesTheRate(t *testing.T) {
	l, _ := newTest(t, Config{})
	l.Observe(200, hdr("X-RateLimit-NearLimit", "true"))
	if r := l.Stats().Rate; r != 4 {
		t.Fatalf("rate = %v, want 4 (8 halved)", r)
	}
	if st := l.Stats(); st.NearLimit != 1 {
		t.Fatalf("near-limit count = %d", st.NearLimit)
	}
}

func TestAIMD_FloorIsOnePerSecond(t *testing.T) {
	l, fc := newTest(t, Config{})
	for i := 0; i < 10; i++ {
		l.Observe(200, hdr("X-RateLimit-NearLimit", "true"))
		fc.Advance(2 * time.Second) // beyond the halving cooldown, far below the 30 s recovery step
	}
	if r := l.Stats().Rate; r != 1 {
		t.Fatalf("rate = %v, want the 1 req/s floor", r)
	}
}

func TestAIMD_RecoversOnePerSecondEvery30SecondsUpToTheMax(t *testing.T) {
	l, fc := newTest(t, Config{})
	l.Observe(200, hdr("X-RateLimit-NearLimit", "true")) // 8 → 4
	for _, step := range []struct {
		after time.Duration
		want  float64
	}{
		{29 * time.Second, 4}, {time.Second, 5}, {29 * time.Second, 5}, {time.Second, 6}, {60 * time.Second, 8},
		{time.Hour, 8},
	} {
		fc.Advance(step.after)
		if r := l.Stats().Rate; r != step.want {
			t.Fatalf("after +%v: rate = %v, want %v", step.after, r, step.want)
		}
	}
}

func TestAIMD_AnotherSignalRestartsTheRecoveryClock(t *testing.T) {
	l, fc := newTest(t, Config{})
	l.Observe(200, hdr("X-RateLimit-NearLimit", "true")) // 8 → 4
	fc.Advance(20 * time.Second)
	l.Observe(200, hdr("X-RateLimit-NearLimit", "true")) // 4 → 2
	fc.Advance(29 * time.Second)
	if r := l.Stats().Rate; r != 2 {
		t.Fatalf("rate = %v, want 2 (the 30 s clock restarted at the last signal)", r)
	}
	fc.Advance(time.Second)
	if r := l.Stats().Rate; r != 3 {
		t.Fatalf("rate = %v, want 3", r)
	}
}

func TestAIMD_SignalsOfOneBurstWindowHalveOnce(t *testing.T) {
	l, fc := newTest(t, Config{})
	for i := 0; i < 4; i++ { // four responses of the same per-second window
		l.Observe(200, hdr("X-RateLimit-NearLimit", "true"))
		fc.Advance(100 * time.Millisecond)
	}
	if r := l.Stats().Rate; r != 4 {
		t.Fatalf("rate = %v, want 4 (one halving per %v window)", r, time.Second)
	}
}

func TestAIMD_SlowedBucketPaysTheLowerRate(t *testing.T) {
	l, fc := newTest(t, Config{Concurrency: 100})
	l.Observe(200, hdr("X-RateLimit-NearLimit", "true")) // 4 req/s, burst 8
	for i := 0; i < 8; i++ {
		mustAcquire(t, l, Sync).Release()
	}
	ch := acquireAsync(l, fc, Sync)
	fc.WaitPending(1, 5*time.Second)
	fc.Advance(249 * time.Millisecond)
	notYet(t, ch, "the 9th request at 4 req/s")
	fc.Advance(time.Millisecond)
	got(t, ch)
}

func TestAIMD_RemainingOverLimitBelowTwentyPercentHalves(t *testing.T) {
	for _, tc := range []struct {
		name string
		h    http.Header
		want float64
	}{
		{"X-RateLimit 19/100", hdr("X-RateLimit-Limit", "100", "X-RateLimit-Remaining", "19"), 4},
		{"X-RateLimit 20/100 is fine", hdr("X-RateLimit-Limit", "100", "X-RateLimit-Remaining", "20"), 8},
		{"X-RateLimit 0/100", hdr("X-RateLimit-Limit", "100", "X-RateLimit-Remaining", "0"), 4},
		{"remaining without limit", hdr("X-RateLimit-Remaining", "1"), 8},
		{"garbage", hdr("X-RateLimit-Limit", "x", "X-RateLimit-Remaining", "y"), 8},
		{"structured r/q 10/100", hdr("RateLimit", `"jira-burst-based";r=10;t=1`, "RateLimit-Policy", `"jira-burst-based";q=100;w=1`), 4},
		{"structured r/q 19/100", hdr("RateLimit", `"jira-burst-based";r=19;t=1`, "RateLimit-Policy", `"jira-burst-based";q=100;w=1`), 4},
		{"structured r/q 20/100 is fine", hdr("RateLimit", `"jira-burst-based";r=20;t=1`, "RateLimit-Policy", `"jira-burst-based";q=100;w=1`), 8},
		{"structured with q inline", hdr("RateLimit", `"p";r=5;q=100;t=1`), 4},
		{"structured, second policy is the tight one", hdr("RateLimit", `"a";r=90;t=1, "b";r=2;t=1`, "RateLimit-Policy", `"a";q=100;w=1, "b";q=50;w=60`), 4},
		{"structured without a quota", hdr("RateLimit", `"p";r=1;t=1`), 8},
		{"legacy limit/remaining keys", hdr("RateLimit", "limit=100, remaining=10, reset=3"), 4},
		{"NearLimit false", hdr("X-RateLimit-NearLimit", "false"), 8},
		{"NearLimit TRUE", hdr("X-RateLimit-NearLimit", "TRUE"), 4},
		{"no headers", http.Header{}, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := newTest(t, Config{})
			l.Observe(200, tc.h)
			if r := l.Stats().Rate; r != tc.want {
				t.Fatalf("rate = %v, want %v", r, tc.want)
			}
		})
	}
}

// ── 429 ─────────────────────────────────────────────────────────────────────────────────────────

func TestGate_429PausesEveryLaneUntilRetryAfterPlusJitter(t *testing.T) {
	var events []ThrottleEvent
	var mu sync.Mutex
	l, fc := newTest(t, Config{OnThrottle: func(e ThrottleEvent) { mu.Lock(); events = append(events, e); mu.Unlock() }})
	l.Observe(429, hdr("Retry-After", "3"))
	wantResume := t0.Add(3*time.Second + 250*time.Millisecond) // jitter 0.5 × 500 ms
	if st := l.Stats(); !st.GateUntil.Equal(wantResume) {
		t.Fatalf("gate until %v, want %v", st.GateUntil, wantResume)
	}
	mu.Lock()
	if len(events) != 1 || !events[0].ResumeAt.Equal(wantResume) || events[0].Reason != "rate_limited" {
		t.Fatalf("throttle events = %+v", events)
	}
	mu.Unlock()

	chs := map[Lane]<-chan result{}
	for _, lane := range []Lane{Interactive, Sync, Media} {
		chs[lane] = acquireAsync(l, fc, lane)
	}
	waitWaiting(t, l, Interactive, 1)
	waitWaiting(t, l, Sync, 1)
	waitWaiting(t, l, Media, 1)
	fc.Advance(3 * time.Second)
	for lane, ch := range chs {
		notYet(t, ch, "lane "+lane.String()+" 3 s into the gate")
	}
	fc.Advance(250 * time.Millisecond)
	for lane, ch := range chs {
		r := got(t, ch)
		if r.err != nil || r.at.Before(wantResume) {
			t.Fatalf("lane %v: err %v at %v, want a send no earlier than %v", lane, r.err, r.at, wantResume)
		}
		r.p.Release()
	}
}

func TestGate_429HalvesTheRate(t *testing.T) {
	l, _ := newTest(t, Config{})
	l.Observe(429, hdr("Retry-After", "1"))
	if r := l.Stats().Rate; r != 4 {
		t.Fatalf("rate = %v, want 4", r)
	}
	if st := l.Stats(); st.Throttles != 1 {
		t.Fatalf("throttles = %d", st.Throttles)
	}
}

func TestGate_RetryAfterFallbacksAndLimits(t *testing.T) {
	reset := t0.Add(7 * time.Second)
	for _, tc := range []struct {
		name string
		h    http.Header
		want time.Duration // gate length without the 250 ms jitter; <0 = no gate
	}{
		{"Retry-After seconds", hdr("Retry-After", "2"), 2 * time.Second},
		{"Retry-After zero", hdr("Retry-After", "0"), 0},
		{"X-RateLimit-Reset RFC 3339", hdr("X-RateLimit-Reset", reset.Format(time.RFC3339)), 7 * time.Second},
		{"X-RateLimit-Reset epoch seconds", hdr("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10)), 7 * time.Second},
		{"X-RateLimit-Reset in the past", hdr("X-RateLimit-Reset", t0.Add(-time.Hour).Format(time.RFC3339)), 0},
		{"structured RateLimit t=", hdr("RateLimit", `"jira-burst-based";r=0;t=4`), 4 * time.Second},
		{"Retry-After wins over the others", hdr("Retry-After", "1", "X-RateLimit-Reset", reset.Format(time.RFC3339), "RateLimit", `"x";r=0;t=9`), time.Second},
		{"nothing: 5 s", http.Header{}, 5 * time.Second},
		{"unparsable: 5 s", hdr("Retry-After", "soon"), 5 * time.Second},
		{"beyond MaxGate: gate stays open", hdr("Retry-After", "3600"), -1},
		{"exactly MaxGate", hdr("Retry-After", "120"), 120 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := newTest(t, Config{})
			l.Observe(429, tc.h)
			st := l.Stats()
			if tc.want < 0 {
				if st.GateUntil.After(t0) {
					t.Fatalf("gate closed until %v, want it left open", st.GateUntil)
				}
				if st.Rate != 4 {
					t.Fatalf("rate = %v: a 429 halves the rate even when the gate stays open", st.Rate)
				}
				return
			}
			if want := t0.Add(tc.want + 250*time.Millisecond); !st.GateUntil.Equal(want) {
				t.Fatalf("gate until +%v, want +%v", st.GateUntil.Sub(t0), want.Sub(t0))
			}
		})
	}
}

func TestGate_NeverShortenedByAnEarlierResponse(t *testing.T) {
	l, _ := newTest(t, Config{})
	l.Observe(429, hdr("Retry-After", "10"))
	l.Observe(429, hdr("Retry-After", "1"))
	if st := l.Stats(); !st.GateUntil.Equal(t0.Add(10*time.Second + 250*time.Millisecond)) {
		t.Fatalf("gate until +%v", st.GateUntil.Sub(t0))
	}
}

func TestGate_FailFastReturnsGateErrorWithRetryIn(t *testing.T) {
	l, fc := newTest(t, Config{})
	l.Observe(429, hdr("Retry-After", "6"))
	fc.Advance(time.Second)
	_, err := l.Acquire(context.Background(), Interactive, FailFastOnGate())
	var ge *GateError
	if !errors.As(err, &ge) {
		t.Fatalf("err = %v, want *GateError", err)
	}
	if ge.RetryIn != 5250*time.Millisecond || !ge.ResumeAt.Equal(t0.Add(6250*time.Millisecond)) {
		t.Fatalf("GateError = %+v", ge)
	}
	if st := l.Stats(); st.Waiting[Interactive] != 0 || st.InFlight != 0 || st.Requests[Interactive] != 0 {
		t.Fatalf("a fail-fast refusal must leave no trace: %+v", st)
	}
	fc.Advance(5250 * time.Millisecond)
	mustAcquire(t, l, Interactive, FailFastOnGate()).Release() // gate over: fail-fast waits for nothing
}

func TestGate_SleepOnceThenProceedEvenIfTheClockDidNotMove(t *testing.T) {
	// A caller-supplied sleeper that returns without time passing (the jira policy's fake Sleep) must not spin.
	l, fc := newTest(t, Config{})
	fc.SetTimersFireImmediately(true) // timers fire at once, the clock stays put
	l.Observe(429, hdr("Retry-After", "2"))
	done := make(chan struct{})
	go func() { mustAcquire(t, l, Sync).Release(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire spun on a gate its sleeper had already waited out")
	}
	if n := fc.Started(); n != 1 {
		t.Fatalf("timers started = %d, want exactly one gate wait", n)
	}
}

func TestGate_ExtendedWhileWaitingWaitsAgain(t *testing.T) {
	l, fc := newTest(t, Config{})
	l.Observe(429, hdr("Retry-After", "1"))
	ch := acquireAsync(l, fc, Sync)
	fc.WaitPending(1, 5*time.Second)
	l.Observe(429, hdr("Retry-After", "5")) // a newer 429 while parked
	fc.Advance(1250 * time.Millisecond)
	notYet(t, ch, "a waiter whose gate was extended")
	fc.Advance(5 * time.Second)
	got(t, ch)
}

// ── per-issue serialisation ─────────────────────────────────────────────────────────────────────

func TestIssueLock_OneWriterPerIssue(t *testing.T) {
	l, _ := newTest(t, Config{})
	rel, ok := l.TryLockIssue("MOB-1")
	if !ok || rel == nil {
		t.Fatal("first lock refused")
	}
	if _, ok := l.TryLockIssue("MOB-1"); ok {
		t.Fatal("second lock of the same issue granted")
	}
	rel2, ok := l.TryLockIssue("MOB-2")
	if !ok {
		t.Fatal("another issue must not be blocked")
	}
	rel2()
	rel()
	rel() // idempotent: must not unlock somebody else's lock
	rel3, ok := l.TryLockIssue("MOB-1")
	if !ok {
		t.Fatal("lock not free after release")
	}
	rel() // a stale release must not free the new holder's lock
	if _, ok := l.TryLockIssue("MOB-1"); ok {
		t.Fatal("a stale release freed a lock held by someone else")
	}
	rel3()
}

func TestIssueLock_ConcurrentCallersExactlyOneWins(t *testing.T) {
	l, _ := newTest(t, Config{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := l.TryLockIssue("MOB-9"); ok { // never released
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d goroutines got the lock, want 1", wins)
	}
}

// ── statistics and hygiene ──────────────────────────────────────────────────────────────────────

func TestStats_CountsRequestsPerLaneAndKeepsRecentEvents(t *testing.T) {
	l, fc := newTest(t, Config{Concurrency: 100})
	for _, lane := range []Lane{Interactive, Sync, Sync, Media} {
		mustAcquire(t, l, lane).Release()
	}
	for i := 0; i < 40; i++ {
		l.Observe(429, hdr("Retry-After", "1"))
		fc.Advance(2 * time.Second)
	}
	st := l.Stats()
	if st.Requests != [3]int64{1, 2, 1} {
		t.Fatalf("requests per lane = %v", st.Requests)
	}
	if st.Throttles != 40 || len(st.Events) == 0 || len(st.Events) > 32 {
		t.Fatalf("throttles %d, events kept %d (want a bounded ring)", st.Throttles, len(st.Events))
	}
	if last := st.Events[len(st.Events)-1]; last.Reason != "rate_limited" || last.ResumeAt.IsZero() {
		t.Fatalf("last event = %+v", last)
	}
}

func TestNoBackgroundGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	l := New(Config{MaxRate: 1e6}) // real clock
	for i := 0; i < 50; i++ {
		p, err := l.Acquire(context.Background(), Lane(i%3))
		if err != nil {
			t.Fatal(err)
		}
		p.Release()
	}
	l.Observe(200, hdr("X-RateLimit-NearLimit", "true"))
	l.Observe(429, hdr("Retry-After", "1"))
	runtime.Gosched()
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines %d → %d: the limiter must not start tickers or workers", before, after)
	}
}

func TestConcurrentUseUnderRace(t *testing.T) {
	l := New(Config{MaxRate: 1e6, Concurrency: 3, MediaConcurrency: 1})
	var wg sync.WaitGroup
	var mu sync.Mutex
	inflight, peak := 0, 0
	for g := 0; g < 12; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 40; i++ {
				p, err := l.Acquire(context.Background(), Lane((g+i)%3))
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				inflight++
				peak = max(peak, inflight)
				mu.Unlock()
				runtime.Gosched()
				mu.Lock()
				inflight--
				mu.Unlock()
				p.Release()
				l.Observe(200, http.Header{})
			}
		}()
	}
	wg.Wait()
	if peak > 3 {
		t.Fatalf("peak in flight = %d, want ≤ 3", peak)
	}
}

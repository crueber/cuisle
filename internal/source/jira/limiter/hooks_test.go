package limiter

// Tests of the host bridges (Config.ManualGate, CloseGate, OnGate, WaitGate) the jira client uses to keep its Policy's
// gate and sleeper in charge of how a 429 is waited out.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestManualGate_ObserveHalvesButLeavesTheGateToTheHost(t *testing.T) {
	var gates []time.Time
	l, _ := newTest(t, Config{ManualGate: true, OnGate: func(u time.Time) { gates = append(gates, u) }})
	l.Observe(429, hdr("Retry-After", "3"))
	st := l.Stats()
	if st.GateUntil.After(t0) || len(gates) != 0 {
		t.Fatalf("gate until %v, OnGate calls %v: with ManualGate a 429 must not close the gate by itself", st.GateUntil, gates)
	}
	if st.Rate != 4 || st.Throttles != 1 {
		t.Fatalf("rate %v throttles %d: a 429 still halves the rate and is counted", st.Rate, st.Throttles)
	}
	if len(st.Events) != 1 || !st.Events[0].ResumeAt.IsZero() {
		t.Fatalf("events = %+v", st.Events)
	}
}

func TestManualGate_CloseGateClosesForEveryLaneAndFiresTheHooksOnce(t *testing.T) {
	var gates []time.Time
	var evs []ThrottleEvent
	var mu sync.Mutex
	l, fc := newTest(t, Config{ManualGate: true,
		OnGate:     func(u time.Time) { mu.Lock(); gates = append(gates, u); mu.Unlock() },
		OnThrottle: func(e ThrottleEvent) { mu.Lock(); evs = append(evs, e); mu.Unlock() }})
	l.Observe(429, hdr("Retry-After", "3"))
	until := t0.Add(3250 * time.Millisecond)
	l.CloseGate(until)
	l.CloseGate(t0.Add(time.Second)) // shorter: ignored, no hooks
	mu.Lock()
	if len(gates) != 1 || !gates[0].Equal(until) || len(evs) != 1 || !evs[0].ResumeAt.Equal(until) || evs[0].Reason != "rate_limited" {
		t.Fatalf("OnGate %v, OnThrottle %+v", gates, evs)
	}
	mu.Unlock()
	if st := l.Stats(); !st.GateUntil.Equal(until) || !st.Events[0].ResumeAt.Equal(until) {
		t.Fatalf("stats = %+v: the diagnostics event must learn its resume time", st)
	}
	ch := acquireAsync(l, fc, Sync)
	fc.WaitPending(1, 5*time.Second)
	notYet(t, ch, "a request during the gate")
	fc.Advance(3250 * time.Millisecond)
	got(t, ch)
}

func TestSetOnThrottle_AttachesToABuiltLimiterAndCanBeRemoved(t *testing.T) {
	l, _ := newTest(t, Config{})
	var evs []ThrottleEvent
	l.SetOnThrottle(func(e ThrottleEvent) { evs = append(evs, e) })
	l.Observe(429, hdr("Retry-After", "2"))
	if len(evs) != 1 || evs[0].Reason != "rate_limited" || !evs[0].ResumeAt.Equal(t0.Add(2250*time.Millisecond)) {
		t.Fatalf("events = %+v", evs)
	}
	l.SetOnThrottle(nil)
	l.Observe(429, hdr("Retry-After", "9"))
	if len(evs) != 1 {
		t.Fatalf("callback ran after removal: %+v", evs)
	}
}

func TestGateHooks_AutoModeCallsOnGateBeforeObserveReturns(t *testing.T) {
	var gate time.Time
	l, _ := newTest(t, Config{OnGate: func(u time.Time) { gate = u }})
	l.Observe(429, hdr("Retry-After", "2"))
	if want := t0.Add(2250 * time.Millisecond); !gate.Equal(want) {
		t.Fatalf("OnGate(%v), want %v", gate, want)
	}
}

func TestWaitGate_ReplacesTheTimerAndIsOnlyAskedOncePerGate(t *testing.T) {
	var calls []time.Time
	var mu sync.Mutex
	l, fc := newTest(t, Config{WaitGate: func(_ context.Context, until time.Time) error {
		mu.Lock()
		calls = append(calls, until)
		mu.Unlock()
		return nil // the host's sleeper "waited"; the fake clock did not move
	}})
	l.Observe(429, hdr("Retry-After", "2"))
	ch := acquireAsync(l, fc, Sync)
	r := got(t, ch)
	r.p.Release()
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 || !calls[0].Equal(t0.Add(2250*time.Millisecond)) {
		t.Fatalf("WaitGate calls = %v, want one for the 2.25 s gate", calls)
	}
	if fc.Started() != 0 {
		t.Fatalf("%d clock timers started: WaitGate replaces the gate timer", fc.Started())
	}
}

func TestWaitGate_ErrorLeavesTheQueueAndIsReturned(t *testing.T) {
	boom := errors.New("sleeper aborted")
	l, fc := newTest(t, Config{WaitGate: func(context.Context, time.Time) error { return boom }})
	l.Observe(429, hdr("Retry-After", "2"))
	ch := make(chan error, 1)
	go func() { _, err := l.Acquire(context.Background(), Sync); ch <- err }()
	select {
	case err := <-ch:
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire hung")
	}
	if st := l.Stats(); st.Waiting[Sync] != 0 {
		t.Fatalf("waiters = %d", st.Waiting[Sync])
	}
	_ = fc
}

func TestWaitGate_InteractiveGoesFirstWhenTheGateOpens(t *testing.T) {
	open := make(chan struct{})
	l, fc := newTest(t, Config{Concurrency: 1, WaitGate: func(ctx context.Context, _ time.Time) error {
		select {
		case <-open:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	l.Observe(429, hdr("Retry-After", "2"))
	syncCh := acquireAsync(l, fc, Sync)
	waitWaiting(t, l, Sync, 1)
	inter := acquireAsync(l, fc, Interactive)
	waitWaiting(t, l, Interactive, 1)
	close(open)
	r := got(t, inter)
	notYet(t, syncCh, "the sync request while the interactive one holds the only slot")
	r.p.Release()
	got(t, syncCh).p.Release()
}

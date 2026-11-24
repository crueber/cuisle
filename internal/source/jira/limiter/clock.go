package limiter

import (
	"sort"
	"sync"
	"time"
)

// Clock is the limiter's only source of time. Production uses RealClock (or an adapter over the jira policy's Sleep);
// tests use FakeClock so no test sleeps for real. A Clock must not start goroutines that outlive the timer.
type Clock interface {
	Now() time.Time
	// TimerAt returns a channel that receives once the clock reaches at (at once if it already has), and a stop function
	// that releases the timer. stop is safe to call after the timer fired, and more than once. Timers are absolute so a
	// waiter that computed its deadline from Now() cannot lose time between computing it and arming the timer.
	TimerAt(at time.Time) (c <-chan time.Time, stop func())
}

// RealClock is the wall clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) TimerAt(at time.Time) (<-chan time.Time, func()) {
	t := time.NewTimer(time.Until(at))
	return t.C, func() { t.Stop() }
}

// FakeClock is a manually driven Clock for tests (here, and in the jira package's tests). Time only moves through
// Advance, or by itself when SetAutoAdvance(true) makes every timer jump the clock forward to its deadline.
type FakeClock struct {
	mu        sync.Mutex
	now       time.Time
	timers    []*fakeTimer
	seq       int
	started   int
	auto      bool
	immediate bool
	changed   chan struct{} // replaced and closed whenever a timer is added, so WaitPending needs no polling
}

type fakeTimer struct {
	at   time.Time
	seq  int
	c    chan time.Time
	done bool
}

// NewFakeClock starts a fake clock at start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, changed: make(chan struct{})}
}

// SetAutoAdvance makes every timer advance the clock to its deadline and fire at once: a lone caller "sleeps" for
// exactly the wait, deterministically. It is meant for single-goroutine tests.
func (f *FakeClock) SetAutoAdvance(on bool) {
	f.mu.Lock()
	f.auto = on
	f.mu.Unlock()
}

// SetTimersFireImmediately makes every timer fire at once without moving the clock: a sleeper that returns early,
// like the fake Sleep functions of the jira policy tests.
func (f *FakeClock) SetTimersFireImmediately(on bool) {
	f.mu.Lock()
	f.immediate = on
	f.mu.Unlock()
}

func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After is TimerAt(Now()+d).
func (f *FakeClock) After(d time.Duration) (<-chan time.Time, func()) {
	return f.TimerAt(f.Now().Add(d))
}

func (f *FakeClock) TimerAt(at time.Time) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	t := &fakeTimer{c: make(chan time.Time, 1), seq: f.seq}
	f.seq++
	switch {
	case f.immediate || !at.After(f.now):
		t.done = true
		t.c <- f.now
	case f.auto:
		f.now = at
		t.done = true
		t.c <- f.now
	default:
		t.at = at
		f.timers = append(f.timers, t)
	}
	close(f.changed)
	f.changed = make(chan struct{})
	return t.c, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.removeLocked(t)
	}
}

func (f *FakeClock) removeLocked(t *fakeTimer) {
	for i, x := range f.timers {
		if x == t {
			f.timers = append(f.timers[:i], f.timers[i+1:]...)
			return
		}
	}
}

// Advance moves the clock forward by d, firing every timer that comes due, in deadline order.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	target := f.now.Add(d)
	sort.SliceStable(f.timers, func(i, j int) bool { return f.timers[i].at.Before(f.timers[j].at) })
	for len(f.timers) > 0 && !f.timers[0].at.After(target) {
		t := f.timers[0]
		f.timers = f.timers[1:]
		if t.at.After(f.now) {
			f.now = t.at
		}
		t.done = true
		t.c <- f.now
	}
	f.now = target
}

// Pending is the number of timers that have neither fired nor been stopped.
func (f *FakeClock) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

// Started is the number of timers ever created (fired, stopped or pending).
func (f *FakeClock) Started() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

// WaitPending blocks until at least n timers are pending (a waiter has parked) or timeout passes; it reports which.
// It is how a test knows a goroutine reached its wait before the test advances time.
func (f *FakeClock) WaitPending(n int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		ok, ch := len(f.timers) >= n, f.changed
		f.mu.Unlock()
		if ok {
			return true
		}
		select {
		case <-ch:
		case <-deadline.C:
			return false
		}
	}
}

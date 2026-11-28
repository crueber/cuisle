package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
)

type fakeDayClock struct {
	mu  sync.Mutex
	now time.Time
	loc *time.Location
}

func (c *fakeDayClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeDayClock) Location() *time.Location { return c.loc }

func (c *fakeDayClock) Set(t time.Time) {
	c.mu.Lock()
	c.now = t
	c.mu.Unlock()
}

func TestDayTimer_IgnoresBackwardClockJump(t *testing.T) {
	clk := &fakeDayClock{
		now: time.Date(2026, 11, 27, 23, 59, 0, 0, time.UTC),
		loc: time.UTC,
	}
	em := &recEmit{}
	var rolls atomic.Int32
	done := make(chan struct{}, 4)

	d := startDayTimer(clk, em, func() {
		rolls.Add(1)
		done <- struct{}{}
	})
	defer d.Stop()

	// Normal forward roll across midnight emits day:rolled once.
	clk.Set(time.Date(2026, 11, 28, 0, 0, 1, 0, time.UTC))
	d.fire()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onRoll did not fire on forward roll")
	}

	em.mu.Lock()
	if len(em.ev) != 1 || em.ev[0] != app.EvDayRolled {
		t.Fatalf("events after forward roll = %v, want [%s]", em.ev, app.EvDayRolled)
	}
	if got := em.pl[0].(app.DayRolled).Today; got != "2026-11-28" {
		t.Fatalf("DayRolled.Today = %q, want 2026-11-28", got)
	}
	em.mu.Unlock()

	// Backward clock jump (e.g. NTP adjustment before midnight) must not emit a spurious day:rolled.
	clk.Set(time.Date(2026, 11, 27, 23, 59, 30, 0, time.UTC))
	d.fire()

	em.mu.Lock()
	evCount := len(em.ev)
	em.mu.Unlock()
	if evCount != 1 {
		t.Fatalf("events after backward clock jump = %d, want 1", evCount)
	}
	if got := rolls.Load(); got != 1 {
		t.Fatalf("onRoll count after backward clock jump = %d, want 1", got)
	}
	d.mu.Lock()
	last := d.lastDay
	d.mu.Unlock()
	if last != "2026-11-28" {
		t.Fatalf("lastDay = %q, want 2026-11-28", last)
	}

	// Next genuine forward rollover (to 2026-11-29) emits day:rolled normally.
	clk.Set(time.Date(2026, 11, 29, 0, 0, 1, 0, time.UTC))
	d.fire()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onRoll did not fire on next genuine rollover")
	}

	em.mu.Lock()
	if len(em.ev) != 2 || em.ev[1] != app.EvDayRolled {
		t.Fatalf("events after next rollover = %v, want 2 day:rolled events", em.ev)
	}
	if got := em.pl[1].(app.DayRolled).Today; got != "2026-11-29" {
		t.Fatalf("second DayRolled.Today = %q, want 2026-11-29", got)
	}
	em.mu.Unlock()
	if got := rolls.Load(); got != 2 {
		t.Fatalf("onRoll count after next rollover = %d, want 2", got)
	}
}

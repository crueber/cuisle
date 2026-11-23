package main

import (
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/app"
	syncpkg "github.com/crueber/cuisle/internal/sync"
)

// dayTimer is the one timer the app is allowed (architecture.md section 3.3): a single time.AfterFunc to the next
// local midnight that emits day:rolled and re-arms itself. It never starts network work; the UI just re-requests
// the current view.
type dayTimer struct {
	mu      sync.Mutex
	clock   syncpkg.Clock
	emitter syncpkg.Emitter
	timer   *time.Timer
	stopped bool
}

func startDayTimer(clock syncpkg.Clock, emitter syncpkg.Emitter) *dayTimer {
	d := &dayTimer{clock: clock, emitter: emitter}
	d.mu.Lock()
	d.arm()
	d.mu.Unlock()
	return d
}

// nextLocalMidnight is the first local midnight strictly after now (calendar arithmetic, so DST-safe).
func nextLocalMidnight(now time.Time) time.Time {
	y, m, day := now.Date()
	return time.Date(y, m, day+1, 0, 0, 0, 0, now.Location())
}

// arm schedules the next fire. The caller holds d.mu.
func (d *dayTimer) arm() {
	now := d.clock.Now().In(d.clock.Location())
	d.timer = time.AfterFunc(nextLocalMidnight(now).Sub(now), d.fire)
}

func (d *dayTimer) fire() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	today := d.clock.Now().In(d.clock.Location()).Format("2006-01-02")
	d.emitter.Emit(app.EvDayRolled, app.DayRolled{Today: today})
	d.arm()
}

// Stop cancels the timer; it is safe to call more than once.
func (d *dayTimer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
	}
}

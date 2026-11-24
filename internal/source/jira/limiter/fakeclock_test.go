package limiter

import (
	"testing"
	"time"
)

func TestFakeClock_AdvanceFiresTimersInOrderAndStopCancels(t *testing.T) {
	fc := NewFakeClock(t0)
	a, _ := fc.After(3 * time.Second)
	b, _ := fc.After(time.Second)
	c, stopC := fc.After(2 * time.Second)
	if fc.Pending() != 3 {
		t.Fatalf("pending = %d", fc.Pending())
	}
	stopC()
	if fc.Pending() != 2 {
		t.Fatalf("pending after stop = %d", fc.Pending())
	}
	fc.Advance(time.Second)
	select {
	case at := <-b:
		if !at.Equal(t0.Add(time.Second)) {
			t.Fatalf("fired at %v", at)
		}
	default:
		t.Fatal("1 s timer did not fire")
	}
	select {
	case <-a:
		t.Fatal("3 s timer fired early")
	default:
	}
	fc.Advance(5 * time.Second)
	select {
	case <-a:
	default:
		t.Fatal("3 s timer did not fire")
	}
	select {
	case <-c:
		t.Fatal("stopped timer fired")
	default:
	}
	if !fc.Now().Equal(t0.Add(6 * time.Second)) {
		t.Fatalf("now = %v", fc.Now())
	}
}

func TestFakeClock_ZeroDurationFiresAtOnceAndAutoAdvanceMovesTime(t *testing.T) {
	fc := NewFakeClock(t0)
	z, _ := fc.After(0)
	select {
	case <-z:
	default:
		t.Fatal("zero timer must fire at once")
	}
	fc.SetAutoAdvance(true)
	ch, _ := fc.After(5 * time.Second)
	select {
	case <-ch:
	default:
		t.Fatal("auto-advance timer must fire at once")
	}
	if !fc.Now().Equal(t0.Add(5 * time.Second)) {
		t.Fatalf("now = %v", fc.Now())
	}
}

func TestFakeClock_WaitPendingTimesOut(t *testing.T) {
	fc := NewFakeClock(t0)
	if fc.WaitPending(1, 20*time.Millisecond) {
		t.Fatal("WaitPending reported a timer that does not exist")
	}
	fc.After(time.Second)
	if !fc.WaitPending(1, 20*time.Millisecond) {
		t.Fatal("WaitPending missed an existing timer")
	}
}

func TestRealClockTimerFires(t *testing.T) {
	var c Clock = RealClock{}
	ch, stop := c.TimerAt(c.Now().Add(time.Millisecond))
	defer stop()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("real timer never fired")
	}
	if c.Now().IsZero() {
		t.Fatal("zero now")
	}
}

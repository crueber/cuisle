package sync

// WP-38B: a panic out of cache.StartRun releases the reservation, and no orchestrator state outlives the orchestrator.

import (
	"context"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store"
)

func TestOrchestrator_StartRunPanicReleasesTheReservation(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	ctx := context.Background()

	// A nil writer makes cache.StartRun panic after the slot has been reserved.
	good := e.o.opts.DB
	e.o.opts.DB = &store.DB{W: nil, R: good.R, Path: good.Path, Log: good.Log, Now: good.Now}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Start did not re-panic")
			}
		}()
		_, _ = e.o.Start(ctx, e.sid, ModeIncremental, TriggerManual)
	}()
	e.o.opts.DB = good

	e.o.mu.Lock()
	runs, starting := len(e.o.runs), len(e.o.starting)
	e.o.mu.Unlock()
	if runs != 0 || starting != 0 {
		t.Fatalf("after the panic: %d runs and %d reservations left, want none", runs, starting)
	}

	done := make(chan struct{})
	go func() {
		e.o.Wait(e.sid)
		sctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := e.o.Shutdown(sctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait or Shutdown is stuck after a panicking Start")
	}
}

func TestOrchestrator_StartAfterAFailedStartStillWorks(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	good := e.o.opts.DB
	e.o.opts.DB = &store.DB{W: nil, R: good.R, Path: good.Path, Log: good.Log, Now: good.Now}
	func() {
		defer func() { _ = recover() }()
		_, _ = e.o.Start(context.Background(), e.sid, ModeIncremental, TriggerManual)
	}()
	e.o.opts.DB = good
	e.clock.Advance(time.Hour)
	e.mustOK(e.sync(ModeIncremental))
}

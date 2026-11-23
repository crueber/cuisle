package sync

// WP-38 review fixes for the orchestrator: Trim after a good run, Start after Shutdown, a panicking emitter, the
// per-run deadline, and the slot reservation around the sync_run write.

import (
	"context"
	"net/http"
	gosync "sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func TestOrchestrator_TrimsOperationalRowsAfterAGoodRun(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	for i := 0; i < 60; i++ {
		storetest.Exec(t, e.db, `INSERT INTO sync_run (standup_id, mode, trigger, status, started_at, finished_at)
			VALUES (?, 'incremental', 'manual', 'ok', ?, ?)`, e.sid, ms(baseTime())-int64(i+1)*1000, ms(baseTime())-int64(i+1)*1000+10)
	}
	id := e.sync(ModeIncremental)
	e.mustOK(id)
	// Trim runs after the standup is released (WP-38B), so Wait no longer implies it; Shutdown waits for the goroutine.
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.o.Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if n := qInt(t, e.db, `SELECT count(*) FROM sync_run WHERE standup_id = ?`, e.sid); n != 50 {
		t.Errorf("sync_run rows = %d, want 50 (Trim keeps the newest 50 per standup)", n)
	}
	if n := qInt(t, e.db, `SELECT count(*) FROM sync_run WHERE id = ?`, id); n != 1 {
		t.Errorf("the run that just finished was trimmed away")
	}
}

func TestOrchestrator_StartAfterShutdownIsRefused(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	if err := e.o.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	e.clock.Advance(time.Hour)
	_, err := e.o.Start(ctx, e.sid, ModeIncremental, TriggerManual)
	if err == nil {
		t.Fatal("Start after Shutdown succeeded")
	}
	if got := apperr.From(err); got.Message != "Cuisle is shutting down." {
		t.Errorf("message = %q, want %q", got.Message, "Cuisle is shutting down.")
	}
	if n := qInt(t, e.db, `SELECT count(*) FROM sync_run`); n != 0 {
		t.Errorf("sync_run rows = %d, want 0", n)
	}
}

func TestOrchestrator_PanickingEmitterDoesNotHangWait(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.em.setHook(func(name string, _ any) {
		if name == EvSyncFinished {
			panic("emitter boom")
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.sync(ModeIncremental)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Wait hung after the emitter panicked")
	}
	// The slot is free again and the run was finalised.
	e.em.setHook(nil)
	id := e.sync(ModeIncremental)
	e.mustOK(id)
}

func TestOrchestrator_RunDeadlineIsAFailedRunWithTimeoutCode(t *testing.T) {
	defer recoverStub(t)
	old := runTimeout
	runTimeout = 200 * time.Millisecond
	t.Cleanup(func() { runTimeout = old })

	e := newEnv(t, envOpts{})
	g := e.gate(func(*http.Request) bool { return true })
	t.Cleanup(g.Release)
	id := e.sync(ModeIncremental)
	r := e.run(id)
	if r.Status != StatusFailed || r.ErrorCode != string(apperr.Timeout) {
		t.Errorf("run = status %q code %q, want failed/%s", r.Status, r.ErrorCode, apperr.Timeout)
	}
}

func TestOrchestrator_ConcurrentStartsShareOneRun(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	g := e.gate(func(*http.Request) bool { return true })
	e.clock.Advance(time.Hour)

	const n = 8
	ids := make([]int64, n)
	errs := make([]error, n)
	var wg gosync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids[i], errs[i] = e.o.Start(context.Background(), e.sid, ModeIncremental, TriggerManual)
		}()
	}
	wg.Wait()
	g.Release()
	e.o.Wait(e.sid)
	for i := range ids {
		if errs[i] != nil {
			t.Fatalf("Start #%d: %v", i, errs[i])
		}
		if ids[i] == 0 || ids[i] != ids[0] {
			t.Errorf("ids = %v, want one non-zero run id for every caller", ids)
			break
		}
	}
	if c := qInt(t, e.db, `SELECT count(*) FROM sync_run`); c != 1 {
		t.Errorf("sync_run rows = %d, want 1", c)
	}
}

package sync

import (
	"context"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
)

func TestPauseAndCancel_PreventsMediaContextAfterFinishAndBlocksStartUntilResumed(t *testing.T) {
	defer recoverStub(t)

	t.Run("post_finish_media_context_refused", func(t *testing.T) {
		e := newEnv(t, envOpts{fx: defaultFixture(t)})
		store := newFakeMedia(e)

		inAfter := make(chan struct{})
		releaseAfter := make(chan struct{})
		origAfter := e.o.opts.AfterSync
		e.o.opts.AfterSync = func(ctx context.Context, standupID, runID int64) {
			if origAfter != nil {
				origAfter(ctx, standupID, runID)
			}
			close(inAfter)
			<-releaseAfter
		}

		ctx := context.Background()
		if _, err := e.o.Start(ctx, e.sid, ModeFull, TriggerManual); err != nil {
			t.Fatalf("Start: %v", err)
		}

		select {
		case <-inAfter:
		case <-time.After(15 * time.Second):
			t.Fatal("run never reached AfterSync")
		}

		type pauseRes struct {
			resume func()
			err    error
		}
		pausedCh := make(chan pauseRes, 1)
		go func() {
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			resume, err := e.o.PauseAndCancel(pctx)
			pausedCh <- pauseRes{resume: resume, err: err}
		}()

		// Wait until PauseAndCancel has incremented o.paused before releasing AfterSync so mediaPhase is guaranteed
		// to enter mediaContext() after PauseAndCancel took effect (inspect o.paused directly so we don't accidentally
		// start a second run if Start wins the race before PauseAndCancel locks o.mu).
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			e.o.mu.Lock()
			p := e.o.paused
			e.o.mu.Unlock()
			if p > 0 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if _, err := e.o.Start(ctx, e.sid, ModeIncremental, TriggerManual); !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("Start while paused err = %v, want conflict", err)
		}

		close(releaseAfter)

		var pr pauseRes
		select {
		case pr = <-pausedCh:
		case <-time.After(10 * time.Second):
			t.Fatal("PauseAndCancel hung")
		}
		if pr.err != nil {
			t.Fatalf("PauseAndCancel: %v", pr.err)
		}
		defer pr.resume()

		// Because PauseAndCancel set paused > 0 before finish returned, mediaContext() refused to start the phase.
		if p, th, ev := store.counts(); p != 0 || th != 0 || ev != 0 {
			t.Errorf("media store counts = (%d, %d, %d), want (0, 0, 0)", p, th, ev)
		}
		if _, err := e.o.Start(ctx, e.sid, ModeIncremental, TriggerManual); !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("Start before resume() err = %v, want conflict", err)
		}

		// Calling resume twice must be safe (OnceFunc) and re-allow Start.
		pr.resume()
		pr.resume()

		e.clock.Advance(time.Hour)
		if _, err := e.o.Start(ctx, e.sid, ModeIncremental, TriggerManual); err != nil {
			t.Fatalf("Start after resume(): %v", err)
		}
		e.o.Wait(e.sid)
		e.o.WaitMedia()
	})

	t.Run("cancels_running_media_phase_and_blocks_start_until_resumed", func(t *testing.T) {
		e := newEnv(t, envOpts{fx: defaultFixture(t)})
		store := newFakeMedia(e)
		g := e.gate(pathHasPrefix("/media/file/"))
		t.Cleanup(g.Release)

		e.mustOK(e.sync(ModeFull))
		g.waitReached(t)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		resume, err := e.o.PauseAndCancel(ctx)
		if err != nil {
			t.Fatalf("PauseAndCancel: %v", err)
		}

		if p, _, ev := store.counts(); p != 0 || ev != 0 {
			t.Errorf("Put = %d, Evict = %d after PauseAndCancel, want 0 and 0", p, ev)
		}
		if _, err := e.o.Start(context.Background(), e.sid, ModeIncremental, TriggerManual); !apperr.Is(err, apperr.Conflict) {
			t.Fatalf("Start while paused err = %v, want conflict", err)
		}

		resume()
		e.clock.Advance(time.Hour)
		if _, err := e.o.Start(context.Background(), e.sid, ModeIncremental, TriggerManual); err != nil {
			t.Fatalf("Start after resume(): %v", err)
		}
		e.o.Wait(e.sid)
		g.Release()
		e.o.WaitMedia()
	})

	t.Run("timeout_returns_error_and_valid_resume", func(t *testing.T) {
		e := newEnv(t, envOpts{fx: defaultFixture(t)})
		inAfter := make(chan struct{})
		releaseAfter := make(chan struct{})
		origAfter := e.o.opts.AfterSync
		e.o.opts.AfterSync = func(ctx context.Context, standupID, runID int64) {
			if origAfter != nil {
				origAfter(ctx, standupID, runID)
			}
			close(inAfter)
			<-releaseAfter
		}

		ctx := context.Background()
		if _, err := e.o.Start(ctx, e.sid, ModeFull, TriggerManual); err != nil {
			t.Fatalf("Start: %v", err)
		}
		select {
		case <-inAfter:
		case <-time.After(15 * time.Second):
			t.Fatal("run never reached AfterSync")
		}

		tctx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		resume, err := e.o.PauseAndCancel(tctx)
		if !apperr.Is(err, apperr.Timeout) {
			close(releaseAfter)
			if resume != nil {
				resume()
			}
			t.Fatalf("PauseAndCancel err = %v, want timeout", err)
		}
		if resume == nil {
			close(releaseAfter)
			t.Fatal("PauseAndCancel returned nil resume on timeout")
		}
		if _, err := e.o.Start(ctx, e.sid, ModeIncremental, TriggerManual); !apperr.Is(err, apperr.Conflict) {
			close(releaseAfter)
			resume()
			t.Fatalf("Start while paused after timeout err = %v, want conflict", err)
		}

		close(releaseAfter)
		e.o.WaitMedia()
		resume()

		e.clock.Advance(time.Hour)
		if _, err := e.o.Start(ctx, e.sid, ModeIncremental, TriggerManual); err != nil {
			t.Fatalf("Start after resume(): %v", err)
		}
		e.o.Wait(e.sid)
		e.o.WaitMedia()
	})
}

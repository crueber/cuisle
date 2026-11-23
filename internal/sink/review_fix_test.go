package sink_test

import (
	"context"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/sink"
)

// A caller that cancels mid-job must not strand the row in 'delivering'.
func TestReviewFix_CancelDuringBackoffLeavesRowFailed(t *testing.T) {
	tests := []struct {
		name  string
		sleep func(cancel context.CancelFunc, release <-chan struct{}) func(time.Duration)
	}{
		{"cancel inside the sleep hook", func(cancel context.CancelFunc, _ <-chan struct{}) func(time.Duration) {
			return func(time.Duration) { cancel() }
		}},
		{"hook blocks until released", func(cancel context.CancelFunc, release <-chan struct{}) func(time.Duration) {
			return func(time.Duration) { cancel(); <-release }
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnvNoDispatcher(t)
			e.addSink(1, 1, "manual", true)
			e.fs.setFail(-1)
			ctx, cancel := context.WithCancel(context.Background())
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			opts := e.options(nil)
			opts.Sleep = tc.sleep(cancel, release)
			d := sink.NewDispatcher(e.db, e.kr, e.build, opts)

			done := make(chan struct{})
			var id int64
			go func() {
				defer close(done)
				id, _ = d.Enqueue(ctx, 1, 1, cur, "manual", "k-cancel")
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Enqueue did not return after cancellation")
			}
			if id == 0 {
				t.Fatal("no outbox id")
			}
			if r := e.row(id); r.Status != "failed" {
				t.Fatalf("status = %q, want failed (attempts %d)", r.Status, r.Attempts)
			}
			if got := e.fs.count(); got != 1 {
				t.Fatalf("Deliver calls = %d, want 1 (no tries after cancel)", got)
			}
		})
	}
}

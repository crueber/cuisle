package sync_test

// Orchestrator-owned test infrastructure: integration tests use a fast Jira request policy (no 10/s bucket, no real
// backoff sleeps) so runs aren't throttled. Until WP-06 lands, NewPolicy panics; the default policy is kept then.

import (
	"os"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/source/jira"
)

func TestMain(m *testing.M) {
	restore := func() {}
	func() {
		defer func() { _ = recover() }()
		p := jira.NewPolicy(jira.PolicyOptions{
			RatePerSecond: 1e6,
			BaseBackoff:   time.Millisecond,
			Sleep: func(done <-chan struct{}, d time.Duration) bool {
				if d > 50*time.Millisecond {
					d = 50 * time.Millisecond // honour gates briefly; Retry-After tests still see ordering
				}
				t := time.NewTimer(d)
				defer t.Stop()
				select {
				case <-done:
					return false
				case <-t.C:
					return true
				}
			},
		})
		restore = jira.SetDefaultPolicy(p)
	}()
	code := m.Run()
	restore()
	os.Exit(code)
}

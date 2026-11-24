package jira

// Request marks for the per-connection limiter (jira-sync §3.1 lanes, ADR 0015). They travel in the context, so the
// source.Source interface does not change: an adapter method (or a caller that holds a context) marks the requests it
// makes, and limiter.Transport reads the mark.
//
//	unmarked          Sync lane: plan, scan, hydrate, comments, dev-status (all existing calls)
//	WithInteractive   Interactive lane: ticket Refresh, combobox search, Connect, validate
//	WithMedia         Media lane: attachment and thumbnail downloads (run only when no sync request waits)
//	WithWrite         Interactive lane + never retried + refused at once while the 429 gate is closed
//
// Mark only the context of the request that should carry the mark: a write operation that first reads (for example the
// transitions list) marks the context of the POST, not of the read.

import (
	"context"

	"github.com/crueber/cuisle/internal/source/jira/limiter"
)

// WithInteractive marks requests made with the returned context as Interactive.
func WithInteractive(ctx context.Context) context.Context {
	return limiter.WithLane(ctx, limiter.Interactive)
}

// WithMedia marks requests made with the returned context as Media (lowest priority, at most 2 in flight).
func WithMedia(ctx context.Context) context.Context { return limiter.WithLane(ctx, limiter.Media) }

// WithWrite marks a write: Interactive lane, attempted once (a 429 or 5xx returns at once; see MapWriteError), and
// refused without sending while the connection's 429 gate is closed.
func WithWrite(ctx context.Context) context.Context { return limiter.WithWrite(ctx) }

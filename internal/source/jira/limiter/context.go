package limiter

import "context"

type ctxKey int

const (
	laneKey ctxKey = iota
	writeKey
)

// WithLane marks every request made with the returned context as belonging to lane. Without a mark a request is in
// the Sync lane, so all existing adapter calls (plan, scan, hydrate, comments, dev-status) are sync traffic.
func WithLane(ctx context.Context, lane Lane) context.Context {
	return context.WithValue(ctx, laneKey, lane)
}

// LaneFrom returns the lane a context was marked with (Sync when unmarked or out of range). A write is Interactive.
func LaneFrom(ctx context.Context) Lane {
	if l, ok := ctx.Value(laneKey).(Lane); ok && l >= 0 && l < numLanes {
		return l
	}
	if IsWrite(ctx) {
		return Interactive
	}
	return Sync
}

// WithWrite marks requests as writes (transition, assign, flag, comment): Interactive lane, never retried by the
// client, and refused at once (GateError) instead of queued while a 429 gate is closed (ADR 0015).
func WithWrite(ctx context.Context) context.Context {
	return context.WithValue(ctx, writeKey, true)
}

// IsWrite reports whether ctx carries the write mark.
func IsWrite(ctx context.Context) bool {
	w, _ := ctx.Value(writeKey).(bool)
	return w
}

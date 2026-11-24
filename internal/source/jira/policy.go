package jira

import (
	"context"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// jira-sync §3.1: 4 requests in flight and 8 req/s sustained (the limiter makes the burst twice the rate: 16). The rate
	// is the hidden setting jira.max_rps (1-20); AIMD can only lower it at run time, never above this.
	defaultConcurrency = 4
	defaultRate        = 8.0
	defaultMax429      = 5
	defaultMaxGate     = 120 * time.Second
	defaultMax5xx      = 3
	defaultBackoff     = 500 * time.Millisecond
	defaultRetryAfter  = 5 * time.Second
	maxGateJitter      = 500 * time.Millisecond
)

// NewPolicy fills zero options with defaults (4, 8/s, 5, 120 s, 3, 500 ms; jira-sync §3 and §3.1).
func NewPolicy(opts PolicyOptions) *Policy {
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	if opts.RatePerSecond <= 0 {
		opts.RatePerSecond = defaultRate
	}
	if opts.Max429Retries <= 0 {
		opts.Max429Retries = defaultMax429
	}
	if opts.MaxGate <= 0 {
		opts.MaxGate = defaultMaxGate
	}
	if opts.MaxRetries5xx <= 0 {
		opts.MaxRetries5xx = defaultMax5xx
	}
	if opts.BaseBackoff <= 0 {
		opts.BaseBackoff = defaultBackoff
	}
	if opts.Jitter == nil {
		opts.Jitter = rand.Float64
	}
	return &Policy{
		opts:   opts,
		sem:    make(chan struct{}, opts.Concurrency),
		tokens: math.Max(1, opts.RatePerSecond),
	}
}

// Acquire waits for the 429 gate, a token-bucket token and a semaphore slot. release must be called exactly once.
//
// Clients no longer call it: since WP-T0 the per-connection limiter (package limiter) owns the token bucket, the 429
// gate and the priority lanes, and every Client takes only slot (gate + semaphore, process-wide). Acquire stays as the
// policy's own contract.
func (p *Policy) Acquire(ctx context.Context) (release func(), err error) {
	if err := p.waitGate(ctx); err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		wait := p.takeToken(time.Now())
		if wait <= 0 {
			break
		}
		if err := p.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
	return p.takeSlot(ctx)
}

// slot waits for the policy gate and a semaphore slot, with no token bucket: the connection's limiter paces requests.
// It is the limiter.Transport Slot hook of every Client, the process-wide in-flight cap across connections.
func (p *Policy) slot(ctx context.Context) (release func(), err error) {
	if err := p.waitGate(ctx); err != nil {
		return nil, err
	}
	return p.takeSlot(ctx)
}

// waitGate sleeps until the policy's 429 gate (Close429) is open.
func (p *Policy) waitGate(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		wait := p.gateWait(time.Now())
		if wait <= 0 {
			return nil
		}
		if err := p.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (p *Policy) takeSlot(ctx context.Context) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var once sync.Once
	return func() { once.Do(func() { <-p.sem }) }, nil
}

// gateWait returns how long the 429 gate still stays closed at now (0 when open).
func (p *Policy) gateWait(now time.Time) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gate.After(now) {
		return p.gate.Sub(now)
	}
	return 0
}

// takeToken consumes a token when one is available and returns 0; otherwise it returns the wait for the next one.
func (p *Policy) takeToken(now time.Time) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	burst := math.Max(1, p.opts.RatePerSecond)
	if p.last.IsZero() {
		p.last = now
	}
	if elapsed := now.Sub(p.last).Seconds(); elapsed > 0 {
		p.tokens = math.Min(burst, p.tokens+elapsed*p.opts.RatePerSecond)
		p.last = now
	}
	if p.tokens >= 1 {
		p.tokens--
		return 0
	}
	return time.Duration((1 - p.tokens) / p.opts.RatePerSecond * float64(time.Second))
}

// sleep waits for d through PolicyOptions.Sleep (or a timer). It returns the context error when ctx ends first.
// It must never be called while p.mu is held.
func (p *Policy) sleep(ctx context.Context, d time.Duration) error {
	if d > 0 {
		ok := true
		if p.opts.Sleep != nil {
			ok = p.opts.Sleep(ctx.Done(), d)
		} else {
			t := time.NewTimer(d)
			select {
			case <-t.C:
			case <-ctx.Done():
				t.Stop()
				ok = false
			}
		}
		if !ok {
			if err := ctx.Err(); err != nil {
				return err
			}
			return context.Canceled
		}
	}
	return ctx.Err()
}

// Close429 closes the global gate until now+retryAfter+jitter(0–500 ms); returns the wait added.
func (p *Policy) Close429(now time.Time, retryAfter time.Duration) time.Duration {
	wait := p.wait429(retryAfter)
	p.closeGate(now.Add(wait))
	return wait
}

// wait429 returns retryAfter plus jitter(0–500 ms) without touching the gate.
func (p *Policy) wait429(retryAfter time.Duration) time.Duration {
	return retryAfter + time.Duration(p.opts.Jitter()*float64(maxGateJitter))
}

// closeGate holds the process-wide gate until at least until.
func (p *Policy) closeGate(until time.Time) {
	p.mu.Lock()
	if until.After(p.gate) {
		p.gate = until
	}
	p.mu.Unlock()
}

// Backoff returns the wait before 5xx retry attempt n (1-based): BaseBackoff·2^(n−1)·jitter(0.5–1.5).
func (p *Policy) Backoff(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > 30 {
		n = 30
	}
	base := float64(p.opts.BaseBackoff) * float64(int64(1)<<(n-1))
	return time.Duration(base * (0.5 + p.opts.Jitter()))
}

// RetryAfter parses Retry-After (seconds) or, failing that, X-RateLimit-Reset (RFC 3339 or epoch s); fallback 5 s.
func RetryAfter(retryAfter, rateLimitReset string, now time.Time) time.Duration {
	if s := strings.TrimSpace(retryAfter); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	if s := strings.TrimSpace(rateLimitReset); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return nonNegative(t.Sub(now))
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return nonNegative(time.Unix(n, 0).Sub(now))
		}
	}
	return defaultRetryAfter
}

func nonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

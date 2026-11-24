package limiter

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

func defaultJitter() float64 { return rand.Float64() }

// Transport is an http.RoundTripper that sends every request through a Limiter. Wrapping the transport (rather than
// asking each call site to remember) makes a bypass structurally impossible: nothing that uses the wrapped
// http.Client can reach the network without a permit, whatever code path or retry loop issued it.
//
// The lane and write mark come from the request context (WithLane, WithWrite). The permit is held until the response
// body is closed (or the request failed), so Concurrency counts requests whose body is still being read.
type Transport struct {
	Base    http.RoundTripper // nil = http.DefaultTransport
	Limiter *Limiter
	// Slot, if set, runs after the limiter grants a permit and before the request is sent; the returned release runs with
	// the permit. The jira client uses it for the process-wide policy slot, so the limiter's lane order is decided first.
	Slot func(ctx context.Context) (release func(), err error)
	// Timeout bounds the exchange with the server: it starts once the permit and slot are granted and ends when the
	// response body is closed (0 = none). It replaces http.Client.Timeout, which starts before RoundTrip and would charge
	// the time spent queued here (a 429 gate, a slowed bucket) to the request, turning a polite wait into a timeout.
	// The error is context.DeadlineExceeded, as http.Client.Timeout's is.
	Timeout time.Duration
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	var opts []AcquireOption
	if IsWrite(ctx) {
		opts = append(opts, FailFastOnGate())
	}
	permit, err := t.Limiter.Acquire(ctx, LaneFrom(ctx), opts...)
	if err != nil {
		return nil, err
	}
	var slotRelease func()
	if t.Slot != nil {
		if slotRelease, err = t.Slot(ctx); err != nil {
			permit.Release()
			return nil, err
		}
	}
	cancel := func() {}
	if t.Timeout > 0 {
		tctx, c := context.WithTimeout(ctx, t.Timeout)
		cancel = c
		req = req.WithContext(tctx) // a shallow copy: RoundTrip must not modify the caller's request
	}
	release := func() {
		cancel()
		if slotRelease != nil {
			slotRelease()
		}
		permit.Release()
	}
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	handedOver := false
	defer func() { // an error, or a panic in Base or Observe, must not leak the permit and slot
		if !handedOver {
			release()
		}
	}()
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	t.Limiter.Observe(resp.StatusCode, resp.Header)
	resp.Body = &releaseBody{ReadCloser: resp.Body, release: release}
	handedOver = true // the body's Close releases from here on
	return resp, nil
}

// releaseBody frees the permit when the body is closed.
type releaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *releaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

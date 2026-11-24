package limiter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubRT is the innermost transport: it records what reached it and answers from fn.
type stubRT struct {
	mu   sync.Mutex
	seen []*http.Request
	fn   func(*http.Request) (*http.Response, error)
}

func (s *stubRT) RoundTrip(r *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.seen = append(s.seen, r)
	s.mu.Unlock()
	if s.fn != nil {
		return s.fn(r)
	}
	return respond(200, nil), nil
}

func respond(status int, h http.Header) *http.Response {
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader("{}")), ContentLength: 2}
}

func request(t *testing.T, ctx context.Context) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTransport_PermitIsHeldUntilTheBodyIsClosed(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 1, MaxRate: 1e6})
	tr := &Transport{Base: &stubRT{}, Limiter: l}
	resp, err := tr.RoundTrip(request(t, context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if st := l.Stats(); st.InFlight != 1 {
		t.Fatalf("in flight = %d while the body is open, want 1", st.InFlight)
	}
	_ = resp.Body.Close()
	_ = resp.Body.Close() // twice is fine
	if st := l.Stats(); st.InFlight != 0 {
		t.Fatalf("in flight = %d after Close, want 0", st.InFlight)
	}
}

func TestTransport_PermitIsReleasedOnATransportError(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 1, MaxRate: 1e6})
	boom := errors.New("connection reset")
	tr := &Transport{Base: &stubRT{fn: func(*http.Request) (*http.Response, error) { return nil, boom }}, Limiter: l}
	if _, err := tr.RoundTrip(request(t, context.Background())); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if st := l.Stats(); st.InFlight != 0 {
		t.Fatalf("in flight = %d after an error", st.InFlight)
	}
}

func TestTransport_PanickingBaseDoesNotLeakThePermitOrSlot(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 1, MaxRate: 1e6})
	slots := 0
	tr := &Transport{Base: &stubRT{fn: func(*http.Request) (*http.Response, error) { panic("boom") }}, Limiter: l,
		Slot: func(context.Context) (func(), error) { slots++; return func() { slots-- }, nil }}
	func() {
		defer func() { _ = recover() }()
		_, _ = tr.RoundTrip(request(t, context.Background()))
	}()
	if st := l.Stats(); st.InFlight != 0 || slots != 0 {
		t.Fatalf("in flight %d, slots held %d after a panic", st.InFlight, slots)
	}
}

func TestTransport_LaneComesFromTheRequestContext(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 100, MaxRate: 1e6})
	tr := &Transport{Base: &stubRT{}, Limiter: l}
	for _, ctx := range []context.Context{
		context.Background(),
		WithLane(context.Background(), Interactive),
		WithLane(context.Background(), Media),
		WithWrite(context.Background()),
	} {
		resp, err := tr.RoundTrip(request(t, ctx))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	// default → sync; Interactive; Media; WithWrite → interactive
	if st := l.Stats(); st.Requests != [3]int64{2, 1, 1} {
		t.Fatalf("requests per lane = %v, want [2 1 1]", st.Requests)
	}
}

func TestTransport_ObservesResponseHeaders(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 100, MaxRate: 8})
	tr := &Transport{Base: &stubRT{fn: func(*http.Request) (*http.Response, error) {
		return respond(200, hdr("X-RateLimit-NearLimit", "true")), nil
	}}, Limiter: l}
	resp, err := tr.RoundTrip(request(t, context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if r := l.Stats().Rate; r != 4 {
		t.Fatalf("rate = %v, want 4 after a NearLimit response", r)
	}
}

func TestTransport_429ClosesTheGateEvenForARawCaller(t *testing.T) {
	l, _ := newTest(t, Config{})
	tr := &Transport{Base: &stubRT{fn: func(*http.Request) (*http.Response, error) {
		return respond(429, hdr("Retry-After", "4")), nil
	}}, Limiter: l}
	resp, err := tr.RoundTrip(request(t, context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if st := l.Stats(); !st.GateUntil.Equal(t0.Add(4*time.Second + 250*time.Millisecond)) {
		t.Fatalf("gate until +%v", st.GateUntil.Sub(t0))
	}
}

func TestTransport_WritesFailFastWhileTheGateIsClosed(t *testing.T) {
	l, _ := newTest(t, Config{})
	l.Observe(429, hdr("Retry-After", "6"))
	base := &stubRT{}
	tr := &Transport{Base: base, Limiter: l}
	_, err := tr.RoundTrip(request(t, WithWrite(context.Background())))
	var ge *GateError
	if !errors.As(err, &ge) {
		t.Fatalf("err = %v, want *GateError", err)
	}
	if len(base.seen) != 0 {
		t.Fatal("a refused write reached the network")
	}
}

func TestTransport_SlotErrorReleasesTheLimiterPermit(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 1, MaxRate: 1e6})
	slotErr := errors.New("no slot")
	base := &stubRT{}
	tr := &Transport{Base: base, Limiter: l, Slot: func(context.Context) (func(), error) { return nil, slotErr }}
	if _, err := tr.RoundTrip(request(t, context.Background())); !errors.Is(err, slotErr) {
		t.Fatalf("err = %v", err)
	}
	if st := l.Stats(); st.InFlight != 0 {
		t.Fatalf("in flight = %d after a failed slot", st.InFlight)
	}
	if len(base.seen) != 0 {
		t.Fatal("request reached the network without a slot")
	}
}

func TestTransport_SlotIsReleasedWithTheBody(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 1, MaxRate: 1e6})
	released := 0
	tr := &Transport{Base: &stubRT{}, Limiter: l, Slot: func(context.Context) (func(), error) {
		return func() { released++ }, nil
	}}
	resp, err := tr.RoundTrip(request(t, context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if released != 0 {
		t.Fatal("slot released before the body was closed")
	}
	_ = resp.Body.Close()
	_ = resp.Body.Close()
	if released != 1 {
		t.Fatalf("slot released %d times, want once", released)
	}
}

func TestTransport_CancelledContextNeverReachesTheNetwork(t *testing.T) {
	l, _ := newTest(t, Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	base := &stubRT{}
	tr := &Transport{Base: base, Limiter: l}
	if _, err := tr.RoundTrip(request(t, ctx)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(base.seen) != 0 {
		t.Fatal("reached the network")
	}
}

func TestTransport_NilBaseUsesTheDefaultTransport(t *testing.T) {
	tr := &Transport{Limiter: New(Config{MaxRate: 1e6})}
	r := request(t, context.Background())
	r.URL.Host = "127.0.0.1:1" // refused quickly; we only care that it did not panic on a nil base
	if resp, err := tr.RoundTrip(r); err == nil {
		_ = resp.Body.Close()
	}
}

func TestTransport_TimeoutBoundsTheExchangeAndReleasesThePermit(t *testing.T) {
	l, _ := newTest(t, Config{Concurrency: 1, MaxRate: 1e6})
	base := &stubRT{fn: func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done() // a server that never answers
		return nil, r.Context().Err()
	}}
	tr := &Transport{Base: base, Limiter: l, Timeout: 20 * time.Millisecond}
	orig := request(t, context.Background())
	if _, err := tr.RoundTrip(orig); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if _, has := orig.Context().Deadline(); has {
		t.Fatal("RoundTrip modified the caller's request")
	}
	if st := l.Stats(); st.InFlight != 0 {
		t.Fatalf("in flight = %d after a timeout", st.InFlight)
	}
}

func TestTransport_TimeoutStartsAfterTheWaitAndEndsWithTheBody(t *testing.T) {
	l, fc := newTest(t, Config{MaxRate: 8, Burst: 1, Concurrency: 100})
	mustAcquire(t, l, Sync).Release() // drain: the next request waits 125 ms of fake time for its token
	base := &stubRT{}
	tr := &Transport{Base: base, Limiter: l, Timeout: time.Hour}
	type out struct {
		resp *http.Response
		err  error
	}
	ch := make(chan out, 1)
	go func() { r, err := tr.RoundTrip(request(t, context.Background())); ch <- out{r, err} }()
	fc.WaitPending(1, 5*time.Second)
	if n := len(base.seen); n != 0 {
		t.Fatalf("%d requests sent before the token", n)
	}
	fc.Advance(125 * time.Millisecond)
	o := <-ch
	if o.err != nil {
		t.Fatal(o.err)
	}
	dl, has := base.seen[0].Context().Deadline()
	if !has || time.Until(dl) < 50*time.Minute {
		t.Fatalf("deadline %v (has %v): the hour must start after the wait, on the real clock", dl, has)
	}
	if base.seen[0].Context().Err() != nil {
		t.Fatal("exchange context ended before the body was closed")
	}
	_ = o.resp.Body.Close()
	if base.seen[0].Context().Err() == nil {
		t.Fatal("closing the body must end the exchange context")
	}
}

// ── context helpers ─────────────────────────────────────────────────────────────────────────────

func TestContextMarks(t *testing.T) {
	bg := context.Background()
	if LaneFrom(bg) != Sync || IsWrite(bg) {
		t.Fatal("an unmarked context is a sync-lane read")
	}
	if LaneFrom(WithLane(bg, Media)) != Media || LaneFrom(WithLane(bg, Interactive)) != Interactive {
		t.Fatal("WithLane lost the lane")
	}
	w := WithWrite(bg)
	if !IsWrite(w) || LaneFrom(w) != Interactive {
		t.Fatalf("a write is an interactive request that is never retried (lane %v, write %v)", LaneFrom(w), IsWrite(w))
	}
	if IsWrite(WithLane(w, Interactive)) != true {
		t.Fatal("marking the lane after WithWrite must not clear the write mark")
	}
	if LaneFrom(WithLane(bg, Lane(99))) != Sync {
		t.Fatal("an out-of-range lane falls back to sync")
	}
}

func TestRetryAfterAndSignalHelpers(t *testing.T) {
	if d := RetryAfter(hdr("Retry-After", "7"), t0); d != 7*time.Second {
		t.Fatalf("RetryAfter = %v", d)
	}
	if d := RetryAfter(hdr("Retry-After", t0.Add(9*time.Second).UTC().Format(http.TimeFormat)), t0); d != 9*time.Second {
		t.Fatalf("RetryAfter(http-date) = %v", d)
	}
	if d := RetryAfter(http.Header{}, t0); d != 5*time.Second {
		t.Fatalf("RetryAfter fallback = %v", d)
	}
	if Signal(hdr("X-RateLimit-NearLimit", "true")) != true || Signal(http.Header{}) {
		t.Fatal("Signal")
	}
}

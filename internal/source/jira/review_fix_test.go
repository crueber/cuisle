package jira

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

func TestReviewFix_429HugeRetryAfterIsRateLimitedAndLeavesGateOpen(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "3600"}})
	c, s, pol := newWP06Client(t, srv.URL, PolicyOptions{MaxGate: 10 * time.Second}, nil)

	start := time.Now()
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	if got := apperr.CodeOf(MapError(err)); got != apperr.RateLimited {
		t.Fatalf("code = %q, want rate_limited (err %v)", got, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %v, want a quick failure", time.Since(start))
	}
	if srv.hits() != 1 {
		t.Fatalf("hits = %d, want 1", srv.hits())
	}
	if gw := pol.gateWait(time.Now()); gw != 0 {
		t.Fatalf("gate stayed closed for %v", gw)
	}
	release, err := pol.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release()
	if got := s.significant(); len(got) != 0 {
		t.Fatalf("Acquire slept %v after the failed 429", got)
	}
}

func TestReviewFix_429OverBudgetDoesNotCloseGate(t *testing.T) {
	// 4 s + 0.25 s jitter per window; the third window crosses 10 s and must not close the gate.
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "4"}})
	c, _, pol := newWP06Client(t, srv.URL, PolicyOptions{MaxGate: 10 * time.Second}, nil)
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	if got := apperr.CodeOf(MapError(err)); got != apperr.RateLimited {
		t.Fatalf("code = %q", got)
	}
	if gw := pol.gateWait(time.Now()); gw != 0 {
		t.Fatalf("gate closed for %v after the budget was exceeded", gw)
	}
}

func TestReviewFix_5xxRetryAfterCappedAtMaxGate(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 503, Header: map[string]string{"Retry-After": "3600"}})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{MaxGate: 10 * time.Second}, nil)
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	if got := apperr.CodeOf(MapError(err)); got != apperr.SourceHTTP {
		t.Fatalf("code = %q, want source_http", got)
	}
	sleeps := s.significant()
	if len(sleeps) == 0 {
		t.Fatal("expected backoff sleeps")
	}
	for _, d := range sleeps {
		if d > 10*time.Second {
			t.Fatalf("slept %v, want at most MaxGate (10s); all %v", d, sleeps)
		}
	}
}

func TestReviewFix_ReserveGateCountsUnionOfConcurrentWindows(t *testing.T) {
	pol, _ := newWP06Policy(t, PolicyOptions{MaxGate: 5 * time.Second})
	c := NewClient("http://127.0.0.1:1", "a", "t", ClientOptions{Policy: pol})
	t0 := time.Now()

	// Two requests sent together both got 429 for the same 4 s window: counted once.
	if !c.reserveGate(t0, t0, 4*time.Second) {
		t.Fatal("first window rejected")
	}
	if !c.reserveGate(t0, t0.Add(10*time.Millisecond), 4*time.Second) {
		t.Fatal("concurrent 429 for the same window must not double count")
	}
	if got := time.Duration(c.gateMs) * time.Millisecond; got > 4100*time.Millisecond {
		t.Fatalf("gate total = %v, want about 4s", got)
	}
	// A request sent after the window was closed opens a new window and counts in full: 4 + 4 > 5.
	if c.reserveGate(t0.Add(time.Second), t0.Add(time.Second), 4*time.Second) {
		t.Fatal("a later window must count in full and exceed the budget")
	}
}

func redirectFixture(t *testing.T) (origin *httptest.Server, other *httptest.Server, otherAuth *atomic.Value) {
	t.Helper()
	otherAuth = &atomic.Value{}
	otherAuth.Store("")
	other = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherAuth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(other.Close)
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/landing", http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	return origin, other, otherAuth
}

func TestReviewFix_RedirectToOtherHostIsRefused(t *testing.T) {
	tests := []struct {
		name string
		mod  func(*ClientOptions)
	}{
		{"default client", nil},
		{"caller client without CheckRedirect", func(o *ClientOptions) { o.HTTP = &http.Client{} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			origin, _, otherAuth := redirectFixture(t)
			pol, _ := newWP06Policy(t, PolicyOptions{})
			co := ClientOptions{Policy: pol}
			if tc.mod != nil {
				tc.mod(&co)
			}
			c := NewClient(origin.URL, "me@acme.com", "secret", co)
			err := c.GetJSON(context.Background(), "/p", nil, nil)
			if err == nil {
				t.Fatal("expected the redirect to be refused")
			}
			if got := otherAuth.Load().(string); got != "" {
				t.Fatalf("Authorization %q reached the other host", got)
			}
		})
	}
}

func TestReviewFix_SameHostRedirectIsFollowed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/new", http.StatusFound) })
	mux.HandleFunc("/new", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{}")) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	pol, _ := newWP06Policy(t, PolicyOptions{})
	c := NewClient(srv.URL, "a", "t", ClientOptions{Policy: pol})
	if err := c.GetJSON(context.Background(), "/old", nil, nil); err != nil {
		t.Fatalf("same-host redirect: %v", err)
	}
}

func TestReviewFix_RefuseUnsafeRedirectDowngrade(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{"https to http same host", "https://acme.atlassian.net/a", "http://acme.atlassian.net/b", true},
		{"https to other host", "https://acme.atlassian.net/a", "https://evil.example/b", true},
		{"https same host", "https://acme.atlassian.net/a", "https://acme.atlassian.net/b", false},
		{"http to https same host", "http://127.0.0.1:8080/a", "https://127.0.0.1:8080/b", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from, _ := http.NewRequest(http.MethodGet, tc.from, nil)
			to, _ := http.NewRequest(http.MethodGet, tc.to, nil)
			err := refuseUnsafeRedirect(to, []*http.Request{from})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestReviewFix_NewRefusesPlainHTTPUnlessLoopback(t *testing.T) {
	tests := []struct {
		base    string
		wantErr bool
	}{
		{"https://acme.atlassian.net", false},
		{"http://acme.atlassian.net", true},
		{"HTTP://acme.atlassian.net", true},
		{"http://192.168.1.5:8080", true},
		{"http://127.0.0.1:9999", false},
		{"http://localhost:9999", false},
		{"http://[::1]:9999", false},
	}
	for _, tc := range tests {
		t.Run(tc.base, func(t *testing.T) {
			_, err := New(model.Connection{BaseURL: tc.base, Account: "a"}, "tok", source.Options{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && apperr.CodeOf(err) != apperr.Validation {
				t.Fatalf("code = %q, want validation", apperr.CodeOf(err))
			}
		})
	}
}

func TestReviewFix_OversizedResponseIsClearErrorNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		chunk := []byte(strings.Repeat("x", 1<<20))
		_, _ = w.Write([]byte(`{"a":"`))
		for i := 0; i < 17; i++ {
			_, _ = w.Write(chunk)
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	t.Cleanup(srv.Close)
	pol, s := newWP06Policy(t, PolicyOptions{})
	c := NewClient(srv.URL, "a", "t", ClientOptions{Policy: pol})
	var out map[string]any
	err := c.GetJSON(context.Background(), "/p", nil, &out)
	if err == nil {
		t.Fatal("expected an error")
	}
	mapped := MapError(err)
	var ae *apperr.Error
	if !errors.As(mapped, &ae) || ae.Code != apperr.SourceHTTP || ae.Message != "Jira returned more data than expected." {
		t.Fatalf("mapped = %#v", mapped)
	}
	if hits.Load() != 1 || len(s.significant()) != 0 {
		t.Fatalf("hits = %d sleeps = %v, want a single un-retried request", hits.Load(), s.significant())
	}
}

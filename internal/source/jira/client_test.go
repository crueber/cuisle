package jira

// WP-06 tests for client.go: NewClient/GetJSON/PostJSON/Counts, Time.UnmarshalJSON, retry behaviour under the
// request policy (jira-sync.md §1 and §3), error shape, cancellation and credential hygiene (architecture.md §6.2).
// Endpoint methods (Test/Search*/Plan/Scan/Hydrate/DevActivity) are NOT covered here.
//
// SPEC-NOTE: Counts() requests counts every HTTP attempt (retries included); throttled counts every 429 response
// seen, including the last one that exhausts the retries.
// SPEC-NOTE: Max429Retries is the number of RETRIES, so Max429Retries 2 means 3 requests in total and the default (5)
// means 6; the final 429 is returned as *HTTPError{Status: 429}. Likewise 5xx: 3 retries = 4 requests with sleeps
// of exactly 0.5 s, 1 s, 2 s (Jitter 0.5).
// SPEC-NOTE: The 429 gate wait is observed as the Sleep passed to PolicyOptions.Sleep, once per 429 (either from
// Acquire or from the client, but not both). Through the client the sleep is asserted as a range because the
// gate is compared with the wall clock: want-1s < d <= want, where want = Retry-After + Jitter×500ms. For
// X-RateLimit-Reset (RFC 3339, second precision) the range is wider.
// SPEC-NOTE: The cumulative gate budget case uses MaxGate 10 s and Retry-After 4 s, so the third 429 trips it
// whether jitter is counted or not (4/8/12 vs 4.25/8.5/12.75): exactly 3 requests, and MapError gives rate_limited.
// SPEC-NOTE: Retry-After on a 5xx replaces the exponential backoff for that attempt; asserted as 7 s <= d < 8 s for
// Retry-After: 7 (an implementation may add jitter or use max(backoff, Retry-After)).
// SPEC-NOTE: Context errors (cancel, deadline) are never retried: one server hit, error satisfies errors.Is.
// SPEC-NOTE: A transport "connection reset" is retried like a 5xx (3 retries, backoff 0.5/1/2 s) and is finally
// returned as-is (errors.Is(err, syscall.ECONNRESET)), not as *HTTPError. Whether "connection refused" is retried
// is NOT pinned; it only has to map to offline.
// SPEC-NOTE: When ClientOptions.UserAgent is empty the User-Agent still starts with "Cuisle/" (never Go's default).
// SPEC-NOTE: Logging: at Info the log contains no email, no JQL/query value and no credential; at Debug it still
// contains no token, no base64 credential and no "Basic " string.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
)

const (
	wp06Account = "me@acme.com"
	wp06Token   = "secret-token"
)

func wp06BasicAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(wp06Account+":"+wp06Token))
}

// ── scripted server ──────────────────────────────────────────────────────────

type wp06Resp struct {
	Status  int               // 0 = 200
	Header  map[string]string // extra headers
	Body    string            // "" = "{}" for 2xx, empty otherwise
	NonJSON bool              // send text/html instead of application/json
}

type wp06Req struct {
	Method, Path, RawQuery string
	Header                 http.Header
	Body                   string
}

// wp06Server replays script responses in order (the last one repeats) and records every request.
type wp06Server struct {
	*httptest.Server
	mu     sync.Mutex
	script []wp06Resp
	reqs   []wp06Req
}

func newWP06Server(t *testing.T, script ...wp06Resp) *wp06Server {
	t.Helper()
	s := &wp06Server{script: script}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Server.Close)
	return s
}

func (s *wp06Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	idx := len(s.reqs)
	s.reqs = append(s.reqs, wp06Req{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Header: r.Header.Clone(), Body: string(body)})
	var resp wp06Resp
	switch {
	case len(s.script) == 0:
	case idx < len(s.script):
		resp = s.script[idx]
	default:
		resp = s.script[len(s.script)-1]
	}
	s.mu.Unlock()
	status := resp.Status
	if status == 0 {
		status = 200
	}
	out := resp.Body
	if out == "" && status/100 == 2 {
		out = "{}"
	}
	ct := "application/json"
	if resp.NonJSON {
		ct = "text/html"
	}
	w.Header().Set("Content-Type", ct)
	for k, v := range resp.Header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, out)
}

func (s *wp06Server) hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func (s *wp06Server) requests() []wp06Req {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]wp06Req(nil), s.reqs...)
}

// ── blocking server (concurrency, cancellation) ──────────────────────────────

type wp06Block struct {
	srv      *httptest.Server
	entered  chan struct{}
	proceed  chan struct{}
	once     sync.Once
	inflight int32
	max      int32
	hits     int32
}

func newWP06Block(t *testing.T) *wp06Block {
	t.Helper()
	b := &wp06Block{entered: make(chan struct{}, 256), proceed: make(chan struct{})}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&b.inflight, 1)
		for {
			m := atomic.LoadInt32(&b.max)
			if n <= m || atomic.CompareAndSwapInt32(&b.max, m, n) {
				break
			}
		}
		atomic.AddInt32(&b.hits, 1)
		select {
		case b.entered <- struct{}{}:
		default:
		}
		select {
		case <-b.proceed:
		case <-r.Context().Done():
		}
		atomic.AddInt32(&b.inflight, -1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	// Cleanups run LIFO: release blocked handlers first, then close the server.
	t.Cleanup(b.srv.Close)
	t.Cleanup(b.release)
	return b
}

func (b *wp06Block) release() { b.once.Do(func() { close(b.proceed) }) }

func (b *wp06Block) waitEntered(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-b.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d requests reached the server", i, n)
		}
	}
}

// ── client construction ──────────────────────────────────────────────────────

// newWP06Client builds a client over a recording policy. HTTP is a keep-alive-free transport so the transport's own
// GET replay cannot skew request counts. mod may override ClientOptions (e.g. HTTP, Logger, UserAgent).
func newWP06Client(t *testing.T, base string, popts PolicyOptions, mod func(*ClientOptions)) (*Client, *wp06Sleeper, *Policy) {
	t.Helper()
	pol, s := newWP06Policy(t, popts)
	co := ClientOptions{
		HTTP:   &http.Client{Transport: &http.Transport{DisableKeepAlives: true}},
		Policy: pol,
	}
	if mod != nil {
		mod(&co)
	}
	return NewClient(base, wp06Account, wp06Token, co), s, pol
}

func wp06Fatalf(t *testing.T, err error, format string) {
	t.Helper()
	if err != nil {
		t.Fatalf(format, err)
	}
}

func wp06HTTPErr(t *testing.T, err error) *HTTPError {
	t.Helper()
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %T (%v), want *HTTPError", err, err)
	}
	return he
}

// ── Time ─────────────────────────────────────────────────────────────────────

func TestTime_UnmarshalJSON_Formats(t *testing.T) {
	utc := func(y int, mo time.Month, d, h, mi, s, ms int) time.Time {
		return time.Date(y, mo, d, h, mi, s, ms*1_000_000, time.UTC)
	}
	tests := []struct {
		name string
		in   string
		want time.Time // zero = expect zero time
	}{
		{"offset without colon (+0000)", `"2026-11-23T09:15:02.123+0000"`, utc(2026, 11, 23, 9, 15, 2, 123)},
		{"Z suffix", `"2026-11-23T09:15:02.123Z"`, utc(2026, 11, 23, 9, 15, 2, 123)},
		{"offset with colon (+00:00)", `"2026-11-23T09:15:02.123+00:00"`, utc(2026, 11, 23, 9, 15, 2, 123)},
		{"positive offset is converted", `"2026-11-23T09:15:02.123+0100"`, utc(2026, 11, 23, 8, 15, 2, 123)},
		{"negative offset is converted", `"2026-11-23T09:15:02.123-0500"`, utc(2026, 11, 23, 14, 15, 2, 123)},
		{"no fractional seconds", `"2026-11-23T09:15:02+0000"`, utc(2026, 11, 23, 9, 15, 2, 0)},
		{"epoch milliseconds number", `1763889302123`, time.UnixMilli(1763889302123)},
		{"epoch milliseconds numeric string", `"1763889302123"`, time.UnixMilli(1763889302123)},
		{"null is zero", `null`, time.Time{}},
		{"empty string is zero", `""`, time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got Time
			if err := json.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.in, err)
			}
			if tc.want.IsZero() {
				if !got.IsZero() {
					t.Fatalf("got %v, want zero time", got.Time)
				}
				return
			}
			if !got.Time.Equal(tc.want) {
				t.Fatalf("got %v, want %v", got.Time.UTC(), tc.want.UTC())
			}
		})
	}
}

func TestTime_UnmarshalJSON_NullAndEmptyResetAPreviousValue(t *testing.T) {
	for _, in := range []string{`null`, `""`} {
		got := Time{Time: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)}
		if err := got.UnmarshalJSON([]byte(in)); err != nil {
			t.Fatalf("UnmarshalJSON(%s): %v", in, err)
		}
		if !got.IsZero() {
			t.Fatalf("after %s got %v, want zero", in, got.Time)
		}
	}
}

func TestTime_UnmarshalJSON_RejectsGarbage(t *testing.T) {
	for _, in := range []string{`"yesterday"`, `"2026-13-45T99:99:99Z"`, `true`, `{}`, `[1]`} {
		var got Time
		if err := json.Unmarshal([]byte(in), &got); err == nil {
			t.Fatalf("Unmarshal(%s) = %v, want an error", in, got.Time)
		}
	}
}

func TestTime_DecodesInsideStructWithNullAndMissingFields(t *testing.T) {
	var dto struct {
		Created  Time `json:"created"`
		Updated  Time `json:"updated"`
		Resolved Time `json:"resolutiondate"`
		Missing  Time `json:"missing"`
	}
	in := `{"created":"2026-11-23T09:15:02.123+0000","updated":1763889302123,"resolutiondate":null,"unknown":{"a":1}}`
	if err := json.Unmarshal([]byte(in), &dto); err != nil {
		t.Fatal(err)
	}
	if !dto.Created.Time.Equal(time.Date(2026, 11, 23, 9, 15, 2, 123_000_000, time.UTC)) {
		t.Fatalf("created = %v", dto.Created.Time)
	}
	if !dto.Updated.Time.Equal(time.UnixMilli(1763889302123)) {
		t.Fatalf("updated = %v", dto.Updated.Time)
	}
	if !dto.Resolved.IsZero() || !dto.Missing.IsZero() {
		t.Fatalf("null/missing must stay zero: %v %v", dto.Resolved.Time, dto.Missing.Time)
	}
}

// ── headers, bodies, query ───────────────────────────────────────────────────

func TestClient_GetJSON_SendsBasicAuthAndHeaders(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Body: `{"accountId":"abc"}`})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, func(o *ClientOptions) {
		o.HTTP = nil // default http.Client
		o.UserAgent = "Cuisle/1.2.3 (darwin)"
	})
	var out struct {
		AccountID string `json:"accountId"`
	}
	wp06Fatalf(t, c.GetJSON(context.Background(), "/rest/api/3/myself", nil, &out), "GetJSON: %v")
	if out.AccountID != "abc" {
		t.Fatalf("decoded %+v", out)
	}
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("hits = %d", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodGet || r.Path != "/rest/api/3/myself" {
		t.Fatalf("request = %s %s", r.Method, r.Path)
	}
	if got := r.Header.Get("Authorization"); got != wp06BasicAuth() {
		t.Fatalf("Authorization = %q, want %q", got, wp06BasicAuth())
	}
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := r.Header.Get("User-Agent"); got != "Cuisle/1.2.3 (darwin)" {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestClient_GetJSON_EmptyUserAgentOptionStillIdentifiesCuisle(t *testing.T) {
	srv := newWP06Server(t)
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	wp06Fatalf(t, c.GetJSON(context.Background(), "/x", nil, nil), "GetJSON: %v")
	ua := srv.requests()[0].Header.Get("User-Agent")
	if !strings.HasPrefix(ua, "Cuisle/") {
		t.Fatalf("User-Agent = %q, want a Cuisle/ prefix", ua)
	}
}

func TestClient_GetJSON_PassesQueryValuesEncoded(t *testing.T) {
	srv := newWP06Server(t)
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	q := url.Values{}
	q.Set("jql", `project = "A B" AND text ~ "x&y=z"`)
	q.Set("maxResults", "50")
	q.Add("expand", "a")
	q.Add("expand", "b")
	wp06Fatalf(t, c.GetJSON(context.Background(), "/rest/api/3/search", q, nil), "GetJSON: %v")
	r := srv.requests()[0]
	got, err := url.ParseQuery(r.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	if got.Get("jql") != q.Get("jql") || got.Get("maxResults") != "50" || strings.Join(got["expand"], ",") != "a,b" {
		t.Fatalf("query = %v", got)
	}
	if r.Path != "/rest/api/3/search" {
		t.Fatalf("path = %q", r.Path)
	}
}

func TestClient_GetJSON_NoQueryMeansNoQueryString(t *testing.T) {
	srv := newWP06Server(t)
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, nil), "GetJSON: %v")
	if rq := srv.requests()[0].RawQuery; rq != "" {
		t.Fatalf("RawQuery = %q, want empty", rq)
	}
}

func TestClient_GetJSON_DecodesIgnoringUnknownFields(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Body: `{"id":"7","name":"Board","extra":{"deep":[1,2,3]},"updated":"2026-11-23T09:15:02.123+0000"}`})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	var out struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Updated Time   `json:"updated"`
	}
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, &out), "GetJSON: %v")
	if out.ID != "7" || out.Name != "Board" || out.Updated.IsZero() {
		t.Fatalf("decoded %+v", out)
	}
}

func TestClient_GetJSON_NilOutDiscardsBody(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Body: `{"anything":true}`})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, nil), "GetJSON with nil out: %v")
	if srv.hits() != 1 {
		t.Fatalf("hits = %d", srv.hits())
	}
}

func TestClient_GetJSON_InvalidJSONBodyIsAnError(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Body: `{not json`})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	var out map[string]any
	if err := c.GetJSON(context.Background(), "/p", nil, &out); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestClient_PostJSON_SendsJSONBodyAndDecodesResponse(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Body: `{"issues":[{"id":"1"}],"nextPageToken":"tok"}`})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, func(o *ClientOptions) { o.HTTP = nil })
	body := map[string]any{"jql": "filter = 10000 ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 1000}
	var out struct {
		Issues        []struct{ ID string } `json:"issues"`
		NextPageToken string                `json:"nextPageToken"`
	}
	q := url.Values{"x": {"1"}}
	wp06Fatalf(t, c.PostJSON(context.Background(), "/rest/api/3/search/jql", q, body, &out), "PostJSON: %v")
	if len(out.Issues) != 1 || out.Issues[0].ID != "1" || out.NextPageToken != "tok" {
		t.Fatalf("decoded %+v", out)
	}
	r := srv.requests()[0]
	if r.Method != http.MethodPost || r.Path != "/rest/api/3/search/jql" || r.RawQuery != "x=1" {
		t.Fatalf("request = %s %s?%s", r.Method, r.Path, r.RawQuery)
	}
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := r.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q", got)
	}
	if got := r.Header.Get("Authorization"); got != wp06BasicAuth() {
		t.Fatalf("Authorization = %q", got)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(r.Body), &sent); err != nil {
		t.Fatalf("body %q is not JSON: %v", r.Body, err)
	}
	if sent["jql"] != body["jql"] || sent["maxResults"] != float64(1000) {
		t.Fatalf("sent body = %v", sent)
	}
}

func TestClient_PostJSON_RetriesResendSameBodyAndAuth(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first wp06Resp
	}{
		{"after 429", wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "1"}}},
		{"after 503", wp06Resp{Status: 503}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newWP06Server(t, tc.first, wp06Resp{Body: `{"ok":true}`})
			c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
			body := map[string]any{"jql": "id in (1,2,3)", "maxResults": 100}
			var out struct {
				OK bool `json:"ok"`
			}
			wp06Fatalf(t, c.PostJSON(context.Background(), "/rest/api/3/search/jql", nil, body, &out), "PostJSON: %v")
			if !out.OK {
				t.Fatal("second response not decoded")
			}
			reqs := srv.requests()
			if len(reqs) != 2 {
				t.Fatalf("hits = %d, want 2", len(reqs))
			}
			if reqs[0].Body == "" || reqs[0].Body != reqs[1].Body {
				t.Fatalf("retry body differs: %q vs %q", reqs[0].Body, reqs[1].Body)
			}
			for i, r := range reqs {
				if r.Method != http.MethodPost || r.Header.Get("Authorization") != wp06BasicAuth() ||
					r.Header.Get("Content-Type") != "application/json" {
					t.Fatalf("attempt %d lost method/auth/content-type: %s %v", i, r.Method, r.Header)
				}
			}
		})
	}
}

// ── HTTPError shape ──────────────────────────────────────────────────────────

func TestClient_HTTPError_CarriesStatusMessagesAndLoginReason(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{
		Status: 400,
		Header: map[string]string{"X-Seraph-LoginReason": "AUTHENTICATED_FAILED"},
		Body:   `{"errorMessages":["first problem","second problem"],"errors":{"jql":"third problem"}}`,
	})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	err := c.PostJSON(context.Background(), "/rest/api/3/search/jql", nil, map[string]any{"jql": "bad"}, nil)
	he := wp06HTTPErr(t, err)
	if he.Status != 400 || he.Method != http.MethodPost || he.Path != "/rest/api/3/search/jql" {
		t.Fatalf("HTTPError = %+v", he)
	}
	if len(he.Messages) < 3 || he.Messages[0] != "first problem" || he.Messages[1] != "second problem" ||
		!strings.Contains(strings.Join(he.Messages, "|"), "third problem") {
		t.Fatalf("Messages = %v, want errorMessages in order then errors{} values", he.Messages)
	}
	if he.LoginReason != "AUTHENTICATED_FAILED" {
		t.Fatalf("LoginReason = %q", he.LoginReason)
	}
	if he.NonJSON {
		t.Fatal("NonJSON set for a JSON error body")
	}
	if want := "POST /rest/api/3/search/jql: HTTP 400"; he.Error() != want {
		t.Fatalf("Error() = %q, want %q", he.Error(), want)
	}
	if ae := wp06AsApp(t, MapError(err)); ae.Code != apperr.FilterInvalid || !strings.Contains(ae.Message, "first problem") {
		t.Fatalf("mapped = %+v", ae)
	}
}

func TestClient_HTTPError_NonJSONBodyIsFlagged(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 404, NonJSON: true, Body: "<html>Not here</html>"})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	err := c.GetJSON(context.Background(), "/rest/dev-status/latest/issue/summary", nil, nil)
	he := wp06HTTPErr(t, err)
	if he.Status != 404 || !he.NonJSON || len(he.Messages) != 0 {
		t.Fatalf("HTTPError = %+v", he)
	}
}

// ── 429 ──────────────────────────────────────────────────────────────────────

func TestClient_429_HonoursRetryAfterSecondsThroughTheGate(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "2"}}, wp06Resp{Body: `{"ok":true}`})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	var out struct {
		OK bool `json:"ok"`
	}
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, &out), "GetJSON: %v")
	if !out.OK || srv.hits() != 2 {
		t.Fatalf("ok=%v hits=%d", out.OK, srv.hits())
	}
	got := s.significant()
	if len(got) != 1 {
		t.Fatalf("sleeps = %v, want exactly one gate wait", got)
	}
	wp06AssertNear(t, got[0], 2*time.Second+250*time.Millisecond)
	if req, thr := c.Counts(); req != 2 || thr != 1 {
		t.Fatalf("Counts = (%d, %d), want (2, 1)", req, thr)
	}
}

func TestClient_429_FallsBackToXRateLimitReset(t *testing.T) {
	// The handler computes the reset at request time so the test needs no clock injection.
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("X-RateLimit-Reset", time.Now().Add(4*time.Second).UTC().Format(time.RFC3339))
			w.WriteHeader(429)
			return
		}
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(srv.Close)
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, nil), "GetJSON: %v")
	got := s.significant()
	if len(got) != 1 {
		t.Fatalf("sleeps = %v, want exactly one gate wait", got)
	}
	// reset is truncated to whole seconds: remaining is in (3, 4] s, plus 250 ms jitter.
	if got[0] < 2500*time.Millisecond || got[0] > 4250*time.Millisecond {
		t.Fatalf("gate wait = %v, want about 3–4.25 s from X-RateLimit-Reset", got[0])
	}
}

func TestClient_429_FallsBackToFiveSecondsWithoutHeaders(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429}, wp06Resp{})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, nil), "GetJSON: %v")
	got := s.significant()
	if len(got) != 1 {
		t.Fatalf("sleeps = %v, want exactly one gate wait", got)
	}
	wp06AssertNear(t, got[0], 5*time.Second+250*time.Millisecond)
}

func TestClient_429_RetriesUpToMax429RetriesThenReturnsHTTPError(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "1"}})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{Max429Retries: 2}, nil)
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	he := wp06HTTPErr(t, err)
	if he.Status != 429 {
		t.Fatalf("status = %d", he.Status)
	}
	if srv.hits() != 3 {
		t.Fatalf("hits = %d, want 3 (1 + 2 retries)", srv.hits())
	}
	if req, thr := c.Counts(); req != 3 || thr != 3 {
		t.Fatalf("Counts = (%d, %d), want (3, 3)", req, thr)
	}
	if got := apperr.CodeOf(MapError(err)); got != apperr.RateLimited {
		t.Fatalf("mapped code = %q, want rate_limited", got)
	}
}

func TestClient_429_DefaultAllowsFiveRetries(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "1"}})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	if he := wp06HTTPErr(t, err); he.Status != 429 {
		t.Fatalf("status = %d", he.Status)
	}
	if srv.hits() != 6 {
		t.Fatalf("hits = %d, want 6 (1 + 5 retries)", srv.hits())
	}
}

func TestClient_429_CumulativeGateOverBudgetFailsAsRateLimited(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "4"}})
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{MaxGate: 10 * time.Second}, nil)
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := apperr.CodeOf(MapError(err)); got != apperr.RateLimited {
		t.Fatalf("mapped code = %q, want rate_limited (err %v)", got, err)
	}
	if srv.hits() != 3 {
		t.Fatalf("hits = %d, want 3 (the third 429 exceeds the 10 s budget)", srv.hits())
	}
	if req, thr := c.Counts(); req != 3 || thr != 3 {
		t.Fatalf("Counts = (%d, %d), want (3, 3)", req, thr)
	}
}

func TestClient_Counts_RequestsAndThrottled(t *testing.T) {
	srv := newWP06Server(t,
		wp06Resp{}, // ok
		wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "1"}}, // throttled
		wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "1"}}, // throttled
		wp06Resp{},            // ok after retries
		wp06Resp{Status: 404}, // plain failure
	)
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	if req, thr := c.Counts(); req != 0 || thr != 0 {
		t.Fatalf("fresh Counts = (%d, %d)", req, thr)
	}
	wp06Fatalf(t, c.GetJSON(context.Background(), "/a", nil, nil), "first: %v")
	if req, thr := c.Counts(); req != 1 || thr != 0 {
		t.Fatalf("after 1 ok: Counts = (%d, %d), want (1, 0)", req, thr)
	}
	wp06Fatalf(t, c.GetJSON(context.Background(), "/b", nil, nil), "second: %v")
	if req, thr := c.Counts(); req != 4 || thr != 2 {
		t.Fatalf("after 429,429,ok: Counts = (%d, %d), want (4, 2)", req, thr)
	}
	_ = c.GetJSON(context.Background(), "/c", nil, nil)
	if req, thr := c.Counts(); req != 5 || thr != 2 {
		t.Fatalf("after a 404: Counts = (%d, %d), want (5, 2)", req, thr)
	}
}

// ── 5xx and transport retries ────────────────────────────────────────────────

func TestClient_5xx_RetriesThreeTimesWithExponentialBackoff(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := newWP06Server(t, wp06Resp{Status: status})
			c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
			err := c.GetJSON(context.Background(), "/p", nil, nil)
			he := wp06HTTPErr(t, err)
			if he.Status != status {
				t.Fatalf("status = %d", he.Status)
			}
			if srv.hits() != 4 {
				t.Fatalf("hits = %d, want 4 (1 + 3 retries)", srv.hits())
			}
			want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
			got := s.significant()
			if len(got) != len(want) {
				t.Fatalf("sleeps = %v, want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("sleeps = %v, want %v", got, want)
				}
			}
			if req, thr := c.Counts(); req != 4 || thr != 0 {
				t.Fatalf("Counts = (%d, %d), want (4, 0)", req, thr)
			}
			ae := wp06AsApp(t, MapError(err))
			if ae.Code != apperr.SourceHTTP || !ae.Retryable {
				t.Fatalf("mapped = %+v, want retryable source_http", ae)
			}
		})
	}
}

func TestClient_5xx_RecoversWhenALaterAttemptSucceeds(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 503}, wp06Resp{Status: 502}, wp06Resp{Body: `{"ok":true}`})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	var out struct {
		OK bool `json:"ok"`
	}
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, &out), "GetJSON: %v")
	if !out.OK || srv.hits() != 3 {
		t.Fatalf("ok=%v hits=%d", out.OK, srv.hits())
	}
	got := s.significant()
	if len(got) != 2 || got[0] != 500*time.Millisecond || got[1] != time.Second {
		t.Fatalf("sleeps = %v, want [500ms 1s]", got)
	}
}

func TestClient_5xx_HonoursRetryAfterHeader(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Status: 503, Header: map[string]string{"Retry-After": "7"}}, wp06Resp{})
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, nil), "GetJSON: %v")
	got := s.significant()
	if len(got) != 1 {
		t.Fatalf("sleeps = %v, want one", got)
	}
	if got[0] < 7*time.Second || got[0] >= 8*time.Second {
		t.Fatalf("sleep = %v, want 7 s <= d < 8 s", got[0])
	}
	if _, thr := c.Counts(); thr != 0 {
		t.Fatalf("throttled = %d, a 503 is not a 429", thr)
	}
}

func TestClient_4xx_IsNotRetried(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			srv := newWP06Server(t, wp06Resp{Status: status})
			c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
			err := c.GetJSON(context.Background(), "/p", nil, nil)
			if he := wp06HTTPErr(t, err); he.Status != status {
				t.Fatalf("status = %d", he.Status)
			}
			if srv.hits() != 1 {
				t.Fatalf("hits = %d, want 1", srv.hits())
			}
			if got := s.significant(); len(got) != 0 {
				t.Fatalf("sleeps = %v, want none", got)
			}
		})
	}
}

// wp06FlakyRT fails the first `failures` round trips (all of them when failures < 0) with a connection reset.
type wp06FlakyRT struct {
	next     http.RoundTripper
	mu       sync.Mutex
	failures int
	attempts int
}

func (f *wp06FlakyRT) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.attempts++
	fail := f.failures != 0
	if f.failures > 0 {
		f.failures--
	}
	f.mu.Unlock()
	if fail {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	}
	return f.next.RoundTrip(req)
}

func (f *wp06FlakyRT) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
}

func TestClient_ConnectionReset_IsRetriedWithBackoffThenSucceeds(t *testing.T) {
	srv := newWP06Server(t, wp06Resp{Body: `{"ok":true}`})
	rt := &wp06FlakyRT{next: &http.Transport{DisableKeepAlives: true}, failures: 2}
	c, s, _ := newWP06Client(t, srv.URL, PolicyOptions{}, func(o *ClientOptions) { o.HTTP = &http.Client{Transport: rt} })
	var out struct {
		OK bool `json:"ok"`
	}
	wp06Fatalf(t, c.GetJSON(context.Background(), "/p", nil, &out), "GetJSON: %v")
	if !out.OK || rt.count() != 3 || srv.hits() != 1 {
		t.Fatalf("ok=%v attempts=%d serverHits=%d", out.OK, rt.count(), srv.hits())
	}
	got := s.significant()
	if len(got) != 2 || got[0] != 500*time.Millisecond || got[1] != time.Second {
		t.Fatalf("sleeps = %v, want [500ms 1s]", got)
	}
}

func TestClient_ConnectionReset_GivesUpAfterThreeRetriesAndReturnsTransportError(t *testing.T) {
	rt := &wp06FlakyRT{failures: -1}
	c, s, _ := newWP06Client(t, "http://jira.invalid", PolicyOptions{}, func(o *ClientOptions) { o.HTTP = &http.Client{Transport: rt} })
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	var he *HTTPError
	if errors.As(err, &he) {
		t.Fatalf("transport failure surfaced as *HTTPError: %v", err)
	}
	if !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("err = %v, want it to wrap ECONNRESET", err)
	}
	if rt.count() != 4 {
		t.Fatalf("attempts = %d, want 4 (1 + 3 retries)", rt.count())
	}
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
	got := s.significant()
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("sleeps = %v, want %v", got, want)
	}
}

// ── transport failures map to offline ────────────────────────────────────────

func TestClient_ConnectionRefused_MapsToOffline(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listens here any more
	c, _, _ := newWP06Client(t, "http://"+addr, PolicyOptions{}, nil)
	err = c.GetJSON(context.Background(), "/rest/api/3/myself", nil, nil)
	if err == nil {
		t.Fatal("expected an error dialling a closed port")
	}
	ae := wp06AsApp(t, MapError(err))
	if ae.Code != apperr.Offline {
		t.Fatalf("code = %q, want offline (err %v)", ae.Code, err)
	}
	for _, s := range []string{err.Error(), ae.Message, ae.Detail, ae.Error()} {
		if strings.Contains(s, wp06Token) {
			t.Fatalf("%q contains the token", s)
		}
	}
}

func TestClient_UntrustedTLSCertificate_MapsToOffline(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(srv.Close)
	// HTTP is a plain client that does not trust the httptest certificate.
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, nil)
	err := c.GetJSON(context.Background(), "/p", nil, nil)
	if err == nil {
		t.Fatal("expected a TLS verification error")
	}
	if got := apperr.CodeOf(MapError(err)); got != apperr.Offline {
		t.Fatalf("code = %q, want offline (err %v)", got, err)
	}
}

// ── cancellation ─────────────────────────────────────────────────────────────

func TestClient_ContextCancel_AbortsInFlightRequest(t *testing.T) {
	b := newWP06Block(t)
	c, s, _ := newWP06Client(t, b.srv.URL, PolicyOptions{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := make(chan error, 1)
	go func() { res <- c.GetJSON(ctx, "/slow", nil, nil) }()
	b.waitEntered(t, 1)
	cancel()
	select {
	case err := <-res:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if got := apperr.CodeOf(MapError(err)); got != apperr.Cancelled {
			t.Fatalf("mapped code = %q, want cancelled", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetJSON did not return promptly after cancel")
	}
	if h := atomic.LoadInt32(&b.hits); h != 1 {
		t.Fatalf("server hits = %d, a cancelled request must not be retried", h)
	}
	if got := s.significant(); len(got) != 0 {
		t.Fatalf("sleeps = %v, want none after cancel", got)
	}
}

func TestClient_ContextDeadline_AbortsAndIsNotRetried(t *testing.T) {
	b := newWP06Block(t)
	c, _, _ := newWP06Client(t, b.srv.URL, PolicyOptions{}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := c.PostJSON(ctx, "/slow", nil, map[string]any{"a": 1}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("deadline was not honoured promptly")
	}
	if got := apperr.CodeOf(MapError(err)); got != apperr.Timeout {
		t.Fatalf("mapped code = %q, want timeout", got)
	}
	if h := atomic.LoadInt32(&b.hits); h != 1 {
		t.Fatalf("server hits = %d, want 1 (no retry after the deadline)", h)
	}
}

func TestClient_ContextCancelledWhileWaitingForSlot_SendsNothing(t *testing.T) {
	srv := newWP06Server(t)
	c, _, pol := newWP06Client(t, srv.URL, PolicyOptions{Concurrency: 1}, nil)
	hold, err := pol.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- c.GetJSON(ctx, "/p", nil, nil) }()
	cancel()
	select {
	case err := <-res:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("GetJSON did not return while waiting for a slot")
	}
	if srv.hits() != 0 {
		t.Fatalf("hits = %d, want 0", srv.hits())
	}
}

// ── concurrency ──────────────────────────────────────────────────────────────

// wp06RunBlocked runs calls concurrently against a blocking server and asserts exactly `limit` reach it at once.
func wp06RunBlocked(t *testing.T, b *wp06Block, limit int, calls []func() error) {
	t.Helper()
	errs := make(chan error, len(calls))
	for _, call := range calls {
		go func() { errs <- call() }()
	}
	b.waitEntered(t, limit)
	select {
	case <-b.entered:
		t.Fatalf("more than %d requests are in flight at once", limit)
	case <-time.After(100 * time.Millisecond):
	}
	if got := atomic.LoadInt32(&b.inflight); got != int32(limit) {
		t.Fatalf("in flight = %d, want %d", got, limit)
	}
	b.release()
	for range calls {
		select {
		case err := <-errs:
			if err != nil {
				t.Errorf("call failed: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("calls did not finish after the server released them")
		}
	}
	if m := atomic.LoadInt32(&b.max); m != int32(limit) {
		t.Fatalf("max in flight = %d, want %d", m, limit)
	}
	if h := atomic.LoadInt32(&b.hits); h != int32(len(calls)) {
		t.Fatalf("hits = %d, want %d", h, len(calls))
	}
}

func TestClient_Concurrency_CapsInFlightRequestsAtFour(t *testing.T) {
	b := newWP06Block(t)
	c, _, _ := newWP06Client(t, b.srv.URL, PolicyOptions{Concurrency: 4}, nil)
	var calls []func() error
	for i := 0; i < 10; i++ {
		calls = append(calls, func() error { return c.GetJSON(context.Background(), "/p", nil, nil) })
	}
	wp06RunBlocked(t, b, 4, calls)
}

func TestClient_Concurrency_PolicyIsSharedAcrossClients(t *testing.T) {
	b := newWP06Block(t)
	c1, _, pol := newWP06Client(t, b.srv.URL, PolicyOptions{Concurrency: 4}, nil)
	c2 := NewClient(b.srv.URL, wp06Account, wp06Token, ClientOptions{
		HTTP:   &http.Client{Transport: &http.Transport{DisableKeepAlives: true}},
		Policy: pol,
	})
	var calls []func() error
	for i := 0; i < 5; i++ {
		calls = append(calls,
			func() error { return c1.GetJSON(context.Background(), "/p", nil, nil) },
			func() error { return c2.PostJSON(context.Background(), "/p", nil, map[string]any{}, nil) },
		)
	}
	wp06RunBlocked(t, b, 4, calls)
}

func TestClient_Policy_SlotIsReleasedOnEveryOutcome(t *testing.T) {
	// One handler, three behaviours by path.
	mux := http.NewServeMux()
	mux.HandleFunc("/notfound", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	mux.HandleFunc("/broken", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	mux.HandleFunc("/fine", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "{}") })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, _, pol := newWP06Client(t, srv.URL, PolicyOptions{Concurrency: 1}, nil)
	bad := &http.Client{Transport: &wp06FlakyRT{failures: -1}}
	cBad := NewClient(srv.URL, wp06Account, wp06Token, ClientOptions{HTTP: bad, Policy: pol})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = c.GetJSON(context.Background(), "/notfound", nil, nil) // 4xx
		_ = c.GetJSON(context.Background(), "/broken", nil, nil)   // exhausted 5xx
		_ = cBad.GetJSON(context.Background(), "/x", nil, nil)     // transport error, exhausted
		if err := c.GetJSON(context.Background(), "/fine", nil, nil); err != nil {
			t.Errorf("final request: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a failed request kept its policy slot (Concurrency 1 deadlocked)")
	}
}

// ── credential hygiene ───────────────────────────────────────────────────────

func TestClient_Errors_NeverContainTokenOrRawBody(t *testing.T) {
	const marker = "RAWBODY-MARKER-9f3a"
	// The server echoes the Authorization header into the body, the way a debugging proxy might.
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		leak := marker + " " + auth + " " + wp06Token
		if strings.HasPrefix(r.URL.Path, "/json") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(statusFor(r.URL.Path))
			_ = json.NewEncoder(w).Encode(map[string]any{"debug": leak, "errorMessages": []string{}})
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(statusFor(r.URL.Path))
		_, _ = io.WriteString(w, leak)
	}))
	t.Cleanup(echo.Close)
	b64 := base64.StdEncoding.EncodeToString([]byte(wp06Account + ":" + wp06Token))
	forbidden := []string{wp06Token, b64, "Basic ", marker}

	for _, tc := range []struct {
		name, method, path string
	}{
		{"500 text body", "GET", "/text500"},
		{"500 json body", "GET", "/json500"},
		{"400 on search json body", "POST", "/json400/rest/api/3/search/jql"},
		{"401 text body", "GET", "/text401"},
		{"403 text body", "GET", "/text403"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, _ := newWP06Client(t, echo.URL, PolicyOptions{}, nil)
			var err error
			if tc.method == "POST" {
				err = c.PostJSON(context.Background(), tc.path, nil, map[string]any{"jql": "x"}, nil)
			} else {
				err = c.GetJSON(context.Background(), tc.path, nil, nil)
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			ae := wp06AsApp(t, MapError(err))
			var he *HTTPError
			if errors.As(err, &he) {
				for _, m := range he.Messages {
					forbiddenCheck(t, "HTTPError.Messages", m, forbidden)
				}
			}
			forbiddenCheck(t, "err.Error()", err.Error(), forbidden)
			forbiddenCheck(t, "apperr Message", ae.Message, forbidden)
			forbiddenCheck(t, "apperr Detail", ae.Detail, forbidden)
			forbiddenCheck(t, "apperr Error()", ae.Error(), forbidden)
		})
	}
}

func statusFor(path string) int {
	switch {
	case strings.Contains(path, "500"):
		return 500
	case strings.Contains(path, "400"):
		return 400
	case strings.Contains(path, "401"):
		return 401
	case strings.Contains(path, "403"):
		return 403
	}
	return 500
}

func forbiddenCheck(t *testing.T, what, s string, forbidden []string) {
	t.Helper()
	for _, f := range forbidden {
		if strings.Contains(s, f) {
			t.Fatalf("%s %q contains forbidden %q", what, s, f)
		}
	}
}

type wp06SafeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *wp06SafeBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *wp06SafeBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// wp06ExerciseClient issues successful, retried (429, 503), failed and POST requests so every logging path runs.
func wp06ExerciseClient(t *testing.T, level slog.Level) *wp06SafeBuf {
	t.Helper()
	srv := newWP06Server(t,
		wp06Resp{},
		wp06Resp{Status: 429, Header: map[string]string{"Retry-After": "1"}}, wp06Resp{},
		wp06Resp{Status: 503}, wp06Resp{},
		wp06Resp{Status: 404},
		wp06Resp{},
	)
	buf := &wp06SafeBuf{}
	log := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level}))
	c, _, _ := newWP06Client(t, srv.URL, PolicyOptions{}, func(o *ClientOptions) { o.Logger = log })
	q := url.Values{"jql": {"project = SECRETJQLVALUE"}}
	_ = c.GetJSON(context.Background(), "/rest/api/3/myself", nil, nil)
	_ = c.GetJSON(context.Background(), "/rest/api/3/search", q, nil)
	_ = c.GetJSON(context.Background(), "/rest/api/3/search", q, nil)
	_ = c.GetJSON(context.Background(), "/rest/api/3/issue/ABC-1", nil, nil)
	_ = c.PostJSON(context.Background(), "/rest/api/3/search/jql", nil, map[string]any{"jql": "project = SECRETJQLVALUE"}, nil)
	return buf
}

func TestClient_Logging_DebugNeverContainsTokenOrAuthorization(t *testing.T) {
	buf := wp06ExerciseClient(t, slog.LevelDebug)
	b64 := base64.StdEncoding.EncodeToString([]byte(wp06Account + ":" + wp06Token))
	out := buf.String()
	for _, f := range []string{wp06Token, b64, "Basic "} {
		if strings.Contains(out, f) {
			t.Fatalf("debug log contains %q:\n%s", f, out)
		}
	}
}

func TestClient_Logging_InfoNeverContainsEmailOrQueryValues(t *testing.T) {
	buf := wp06ExerciseClient(t, slog.LevelInfo)
	b64 := base64.StdEncoding.EncodeToString([]byte(wp06Account + ":" + wp06Token))
	out := buf.String()
	for _, f := range []string{wp06Token, b64, "Basic ", wp06Account, "SECRETJQLVALUE"} {
		if strings.Contains(out, f) {
			t.Fatalf("info log contains %q:\n%s", f, out)
		}
	}
}

package jira

// WP-19 tests for scan.go (Scan) plus the shared wp19 harness used by plan_test.go, hydrate_test.go and dev_test.go
// (jira-sync.md §2.8 scan field set, approximate count; §3 error mapping). All HTTP goes through a recording httptest
// server because the assertions need request bodies and query parameters, which jiratest does not log.
// Helpers are prefixed wp19; every test starts with `defer wp19Guard(t)` so a stub panic fails that test only.
//
// SPEC-NOTE (harness): the adapter is built with New() (no I/O) and then gets a client whose policy has a very high
// rate and a no-op Sleep, so 5xx backoff cannot slow tests; now is fixed at wp19Now. Unrouted requests fail the test
// ("unexpected request"), which also pins "no extra calls". Field ids come from source.Options.Meta (sprint
// customfield_10020, rank customfield_10019, flagged customfield_10021, fetched at wp19Now), so Hydrate must not call
// /rest/api/3/field.
// SPEC-NOTE (Scan): the request body is {"jql": q.Expr, "fields": ["updated"], "maxResults": N} plus "nextPageToken"
// on follow-up pages. N is exactly 1000 for an unlimited query; for a query with Limit it may be anything in
// [Limit, 1000]. Pages are followed while the response has a nextPageToken and fewer than Limit items are read; the
// result is cut to exactly Limit items (the first ones, in server order). Items carry SourceID (= issue id), Key and
// UpdatedAt (fields.updated).
// SPEC-NOTE (Scan count): when q.Count, POST /rest/api/3/search/approximate-count has the body {"jql": <Expr without
// the trailing " ORDER BY Rank ASC">} and the response {"count": N} becomes ApproxTotal. A failed count (any non-2xx)
// is not an error: ApproxTotal == -1 and Items are still returned. When q.Count is false there is no count request
// and ApproxTotal is -1 ("unknown", never the misleading zero value).
// SPEC-NOTE (Scan errors): errors are mapped like everywhere else (MapError): 400 -> filter_invalid carrying Jira's
// first message, 401 -> auth_invalid.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

var wp19Now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

func wp19Guard(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("panicked (not implemented?): %v", r)
	}
}

// ── recording server ─────────────────────────────────────────────────────────

type wp19Req struct {
	Method, Path string
	Query        url.Values
	Body         string
}

func (r wp19Req) json(t *testing.T) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Errorf("request body is not a JSON object: %v (%q)", err, r.Body)
	}
	return m
}

type wp19Resp struct {
	Status  int // 0 = 200
	Body    string
	NonJSON bool
}

type wp19Route func(r wp19Req) wp19Resp

type wp19Srv struct {
	*httptest.Server
	mu       sync.Mutex
	reqs     []wp19Req
	routes   map[string]wp19Route // "METHOD /path"
	inflight int32
	maxInfl  int32
}

func newWP19Server(t *testing.T, routes map[string]wp19Route) *wp19Srv {
	t.Helper()
	s := &wp19Srv{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&s.inflight, 1)
		defer atomic.AddInt32(&s.inflight, -1)
		for {
			m := atomic.LoadInt32(&s.maxInfl)
			if n <= m || atomic.CompareAndSwapInt32(&s.maxInfl, m, n) {
				break
			}
		}
		b, _ := io.ReadAll(r.Body)
		req := wp19Req{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: string(b)}
		s.mu.Lock()
		s.reqs = append(s.reqs, req)
		s.mu.Unlock()
		route, ok := s.routes[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			http.Error(w, `{"errorMessages":["no route"]}`, http.StatusNotFound)
			return
		}
		resp := route(req)
		ct := "application/json"
		if resp.NonJSON {
			ct = "text/html"
		}
		w.Header().Set("Content-Type", ct)
		st := resp.Status
		if st == 0 {
			st = 200
		}
		w.WriteHeader(st)
		_, _ = io.WriteString(w, resp.Body)
	}))
	t.Cleanup(s.Server.Close)
	return s
}

func (s *wp19Srv) all() []wp19Req {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]wp19Req(nil), s.reqs...)
}

func (s *wp19Srv) to(method, path string) []wp19Req {
	var out []wp19Req
	for _, r := range s.all() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func wp19Ok(body string) wp19Route { return func(wp19Req) wp19Resp { return wp19Resp{Body: body} } }
func wp19Fail(status int, body string) wp19Route {
	return func(wp19Req) wp19Resp { return wp19Resp{Status: status, Body: body} }
}

func wp19Testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wp19Meta() map[string]any {
	return map[string]any{
		"sprintFieldId":          "customfield_10020",
		"rankFieldId":            "customfield_10019",
		"flaggedFieldId":         "customfield_10021",
		"fieldsFetchedAt":        wp19Now.Format(time.RFC3339Nano),
		"devSummaryFieldChecked": true,
	}
}

func newWP19Adapter(t *testing.T, base string, meta map[string]any) *Adapter {
	t.Helper()
	src, err := New(model.Connection{ID: 1, Source: Kind, BaseURL: base, Account: "me@acme.example"}, "tok-123",
		source.Options{HTTP: &http.Client{}, Now: func() time.Time { return wp19Now }, Meta: meta})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a, ok := src.(*Adapter)
	if !ok {
		t.Fatalf("New returned %T, want *Adapter", src)
	}
	pol := NewPolicy(PolicyOptions{
		RatePerSecond: 1e6,
		Sleep:         func(<-chan struct{}, time.Duration) bool { return true },
		Jitter:        func() float64 { return 0.5 },
	})
	a.c = NewClient(base, "me@acme.example", "tok-123", ClientOptions{HTTP: &http.Client{}, Policy: pol, Now: func() time.Time { return wp19Now }})
	return a
}

func wp19JSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func wp19AssertCode(t *testing.T, err error, want apperr.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want code %s", want)
	}
	if got := apperr.CodeOf(err); got != want {
		t.Fatalf("code = %s (%v), want %s", got, err, want)
	}
}

var wp19IDsRe = regexp.MustCompile(`(?i)\bid\s+in\s*\(([^)]*)\)`)

// wp19IDsFromJQL extracts the ids of an `id in (…)` clause (digits only, any separator/quoting).
func wp19IDsFromJQL(t *testing.T, jql string) []string {
	t.Helper()
	m := wp19IDsRe.FindStringSubmatch(jql)
	if m == nil {
		t.Errorf("jql %q has no `id in (…)` clause", jql)
		return nil
	}
	return regexp.MustCompile(`\d+`).FindAllString(m[1], -1)
}

func wp19StrSlice(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s, _ := e.(string)
		out = append(out, s)
	}
	return out
}

func wp19SameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, c := range m {
		if c != 0 {
			return false
		}
	}
	return true
}

// ── Scan helpers ─────────────────────────────────────────────────────────────

const wp19OrderBy = " ORDER BY Rank ASC"

// wp19ScanPage builds a search/jql response of issues with ids from..from+n-1 (key MOB-<id>).
func wp19ScanPage(from, n int, nextToken string) string {
	var sb strings.Builder
	sb.WriteString(`{"issues":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		id := from + i
		fmt.Fprintf(&sb, `{"expand":"","id":"%d","self":"x","key":"MOB-%d","fields":{"updated":"2026-11-%02dT10:00:00.000+0000"}}`, id, id, 1+(id%20))
	}
	sb.WriteString(`]`)
	if nextToken != "" {
		fmt.Fprintf(&sb, `,"nextPageToken":%q`, nextToken)
	} else {
		sb.WriteString(`,"isLast":true`)
	}
	sb.WriteString(`}`)
	return sb.String()
}

func wp19Query(expr string, limit int, count bool) source.Query {
	return source.Query{Name: "backlog", Role: model.RoleBacklog, Limit: limit, Expr: expr, Count: count}
}

// ── Scan ─────────────────────────────────────────────────────────────────────

func TestScan_SendsDocumentedBodyAndMapsItemsInOrder(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Ok(`{"issues":[
			{"id":"10005","key":"MOB-5","fields":{"updated":"2026-11-20T10:00:00.000+0000"}},
			{"id":"10001","key":"MOB-1","fields":{"updated":"2026-11-21T08:30:15.250+0100"}},
			{"id":"10003","key":"MOB-3","fields":{"updated":"2026-11-19T00:00:00.000Z"}}],"isLast":true}`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	expr := "filter = 9001 AND sprint in (42)" + wp19OrderBy
	res, err := a.Scan(context.Background(), source.Query{Name: "sprint", Role: model.RoleBoard, Expr: expr})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	reqs := srv.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1 (no count request without Count)", len(reqs))
	}
	body := reqs[0].json(t)
	if body["jql"] != expr {
		t.Errorf("jql = %v, want %q", body["jql"], expr)
	}
	if f := wp19StrSlice(body["fields"]); len(f) != 1 || f[0] != "updated" {
		t.Errorf("fields = %v, want [updated]", body["fields"])
	}
	if body["maxResults"] != float64(1000) {
		t.Errorf("maxResults = %v, want 1000", body["maxResults"])
	}
	if _, has := body["nextPageToken"]; has && body["nextPageToken"] != "" {
		t.Errorf("first page carries nextPageToken %v", body["nextPageToken"])
	}
	want := []source.ScanItem{
		{SourceID: "10005", Key: "MOB-5", UpdatedAt: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)},
		{SourceID: "10001", Key: "MOB-1", UpdatedAt: time.Date(2026, 11, 21, 7, 30, 15, 250_000_000, time.UTC)},
		{SourceID: "10003", Key: "MOB-3", UpdatedAt: time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)},
	}
	if len(res.Items) != len(want) {
		t.Fatalf("items = %+v", res.Items)
	}
	for i, w := range want {
		g := res.Items[i]
		if g.SourceID != w.SourceID || g.Key != w.Key || !g.UpdatedAt.Equal(w.UpdatedAt) {
			t.Errorf("item[%d] = %+v, want %+v", i, g, w)
		}
	}
	if res.ApproxTotal != -1 {
		t.Errorf("ApproxTotal = %d, want -1 (no count requested)", res.ApproxTotal)
	}
}

func TestScan_FollowsNextPageTokenUntilNoToken(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": func(r wp19Req) wp19Resp {
			switch tok, _ := r.json(t)["nextPageToken"].(string); tok {
			case "":
				return wp19Resp{Body: wp19ScanPage(100, 3, "tok-1")}
			case "tok-1":
				return wp19Resp{Body: wp19ScanPage(103, 3, "tok-2")}
			case "tok-2":
				return wp19Resp{Body: wp19ScanPage(106, 2, "")}
			}
			t.Errorf("unknown token in %s", r.Body)
			return wp19Resp{Status: 400, Body: `{"errorMessages":["bad token"]}`}
		},
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	res, err := a.Scan(context.Background(), source.Query{Name: "open", Role: model.RoleBoard, Expr: "filter = 1" + wp19OrderBy})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Items) != 8 {
		t.Fatalf("items = %d, want 8", len(res.Items))
	}
	for i, it := range res.Items {
		if want := fmt.Sprint(100 + i); it.SourceID != want || it.Key != "MOB-"+want {
			t.Errorf("item[%d] = %+v, want id %s", i, it, want)
		}
	}
	reqs := srv.all()
	if len(reqs) != 3 {
		t.Fatalf("requests = %d, want 3", len(reqs))
	}
	for i, wantTok := range []string{"", "tok-1", "tok-2"} {
		body := reqs[i].json(t)
		if tok, _ := body["nextPageToken"].(string); tok != wantTok {
			t.Errorf("request %d nextPageToken = %q, want %q", i, tok, wantTok)
		}
		if body["jql"] != "filter = 1"+wp19OrderBy {
			t.Errorf("request %d jql = %v (must be identical on every page)", i, body["jql"])
		}
		if f := wp19StrSlice(body["fields"]); len(f) != 1 || f[0] != "updated" {
			t.Errorf("request %d fields = %v", i, body["fields"])
		}
	}
}

func TestScan_StopsAtLimitAndCutsTheLastPage(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": func(r wp19Req) wp19Resp {
			switch tok, _ := r.json(t)["nextPageToken"].(string); tok {
			case "":
				return wp19Resp{Body: wp19ScanPage(200, 3, "tok-1")}
			case "tok-1":
				return wp19Resp{Body: wp19ScanPage(203, 3, "tok-2")}
			}
			t.Errorf("Scan asked for a page beyond the limit: %s", r.Body)
			return wp19Resp{Body: wp19ScanPage(206, 3, "tok-3")}
		},
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	res, err := a.Scan(context.Background(), wp19Query("filter = 1"+wp19OrderBy, 5, false))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Items) != 5 {
		t.Fatalf("items = %d, want exactly Limit 5", len(res.Items))
	}
	for i, it := range res.Items {
		if want := fmt.Sprint(200 + i); it.SourceID != want {
			t.Errorf("item[%d] = %s, want %s (first five in server order)", i, it.SourceID, want)
		}
	}
	if n := len(srv.all()); n != 2 {
		t.Fatalf("requests = %d, want 2", n)
	}
	mr, _ := srv.all()[0].json(t)["maxResults"].(float64)
	if mr < 5 || mr > 1000 {
		t.Errorf("maxResults = %v, want within [5, 1000]", mr)
	}
}

func TestScan_LimitReachedOnPageBoundaryMakesNoFurtherRequest(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Ok(wp19ScanPage(300, 3, "tok-more")),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	res, err := a.Scan(context.Background(), wp19Query("filter = 1"+wp19OrderBy, 3, false))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(res.Items))
	}
	if n := len(srv.all()); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}

func TestScan_Count_PostsApproximateCountWithoutOrderBy(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql":               wp19Ok(wp19ScanPage(400, 2, "")),
		"POST /rest/api/3/search/approximate-count": wp19Ok(`{"count":137}`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	expr := "filter = 9001 AND status in (10001, 10002) AND (sprint is EMPTY OR sprint not in openSprints())" + wp19OrderBy
	res, err := a.Scan(context.Background(), wp19Query(expr, 50, true))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.ApproxTotal != 137 {
		t.Errorf("ApproxTotal = %d, want 137", res.ApproxTotal)
	}
	if len(res.Items) != 2 {
		t.Errorf("items = %d, want 2", len(res.Items))
	}
	counts := srv.to("POST", "/rest/api/3/search/approximate-count")
	if len(counts) != 1 {
		t.Fatalf("count requests = %d, want 1", len(counts))
	}
	body := counts[0].json(t)
	wantJQL := "filter = 9001 AND status in (10001, 10002) AND (sprint is EMPTY OR sprint not in openSprints())"
	if body["jql"] != wantJQL {
		t.Errorf("count jql = %q, want %q (no ORDER BY)", body["jql"], wantJQL)
	}
	if len(body) != 1 {
		t.Errorf("count body = %v, want only jql", body)
	}
}

func TestScan_Count_FailureYieldsMinusOneWithoutError(t *testing.T) {
	defer wp19Guard(t)
	for _, status := range []int{400, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			defer wp19Guard(t)
			srv := newWP19Server(t, map[string]wp19Route{
				"POST /rest/api/3/search/jql":               wp19Ok(wp19ScanPage(500, 4, "")),
				"POST /rest/api/3/search/approximate-count": wp19Fail(status, `{"errorMessages":["nope"]}`),
			})
			a := newWP19Adapter(t, srv.URL, wp19Meta())
			res, err := a.Scan(context.Background(), wp19Query("filter = 1"+wp19OrderBy, 50, true))
			if err != nil {
				t.Fatalf("Scan: %v, want nil (a count failure only makes the total unknown)", err)
			}
			if res.ApproxTotal != -1 {
				t.Errorf("ApproxTotal = %d, want -1", res.ApproxTotal)
			}
			if len(res.Items) != 4 {
				t.Errorf("items = %d, want 4", len(res.Items))
			}
		})
	}
}

func TestScan_EmptyResultIsNotAnError(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Ok(`{"issues":[],"isLast":true}`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	res, err := a.Scan(context.Background(), source.Query{Name: "open", Role: model.RoleBoard, Expr: "filter = 1" + wp19OrderBy})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Items) != 0 || res.ApproxTotal != -1 {
		t.Fatalf("result = %+v", res)
	}
}

func TestScan_InvalidJQLIsFilterInvalidWithJirasMessage(t *testing.T) {
	defer wp19Guard(t)
	const msg = "Error in the JQL Query: The operator '=' is not supported by the 'foo' field. (line 1, character 8)"
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Fail(400, `{"errorMessages":["`+msg+`"],"errors":{}}`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	_, err := a.Scan(context.Background(), source.Query{Name: "open", Role: model.RoleBoard, Expr: "filter = 1 AND foo = 2" + wp19OrderBy})
	wp19AssertCode(t, err, apperr.FilterInvalid)
	if !strings.Contains(err.Error(), "Error in the JQL Query") {
		t.Errorf("message = %q, want Jira's message", err.Error())
	}
}

func TestScan_UnauthorizedIsAuthInvalid(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Fail(401, `{}`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	_, err := a.Scan(context.Background(), source.Query{Name: "open", Role: model.RoleBoard, Expr: "filter = 1" + wp19OrderBy})
	wp19AssertCode(t, err, apperr.AuthInvalid)
}

func wp19MetaString(m map[string]any, k string) string { s, _ := m[k].(string); return s }

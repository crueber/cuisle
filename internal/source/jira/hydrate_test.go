package jira

// WP-19 tests for hydrate.go (Hydrate): issue batches, field mapping to model.IssueDetail, changelog bulkfetch with
// its fallbacks, complete comment fetches (F12), comment/email hygiene, people, progress and output order (jira-sync.md §2.8–§2.10,
// §4.3; architecture.md R6). Harness and conventions: see scan_test.go.
//
// SPEC-NOTE (F12, was: explicit field list, batches of 100): Hydrate sends POST /rest/api/3/search/jql with `jql` =
// `id in (<ids>)` (ids are digits; the separator is free), `maxResults` 50 and `fields` = exactly
// ["*all", "-comment", "-worklog"] in that order (jira-sync §2.8, ADR 0015: every field comes with the page; the sprint,
// rank and flagged ids are therefore NOT listed any more). A batch is ≤ 50 ids; if a response carries nextPageToken it
// is followed. Besides the search the adapter reads GET /rest/api/3/field once (registry, see hydrate_content_test.go)
// and, per issue, the comment endpoint; the old "Hydrate must not call /rest/api/3/field" rule of scan_test.go is gone.
// SPEC-NOTE: Output order follows the input ids; issues the server does not return are simply absent. The test server
// answers every batch in REVERSED order to prove this. At most 4 requests are in flight at once (policy semaphore).
// SPEC-NOTE: Mapping: Issue{SourceID=id, Key, Summary, TypeID, StatusID, AssigneeID ("" when null), Flagged (flagged
// field is a non-empty array), Rank (rank field string), ParentKey (parent.key), SprintIDs (sprint field ids as strings,
// field order), CreatedAt/UpdatedAt/ResolvedAt (zero when null)}; Type{ID,Name,Kind=IssueTypeKind}; Status{ID,Name,
// Category=statusCategory.key,BlockedHint=StatusBlockedHint}; Assignee *Person (nil when unassigned) with Kind "user"
// for accountType atlassian, "app" for app, "customer" for customer; Sprints = the sprint field entries with BoardID
// from boardId (as a string) and zero times when absent. Issue.SpaceID is not asserted (no project field is read).
// SPEC-NOTE: Changelog: POST /rest/api/3/changelog/bulkfetch body {"issueIdsOrKeys": [≤500 ids], "fieldIds":
// ["status","assignee","<flagged id>"] (set; order free), "maxResults": 1000, "nextPageToken"?}; pages are followed and
// histories of the same issue from different pages are merged. History id -> Change.ID. Changes are returned oldest
// first (the fake answers oldest first, and a stable sort by time is equally fine). Assignee from/to are account ids,
// null/"" = unassigned. Flag items match by fieldId == flagged id OR field == "Flagged"; Flagged after the change is
// (to != null && to != ""). Items of other fields are ignored. `created` may be ISO-8601 or epoch milliseconds.
// SPEC-NOTE: Fallbacks: a 400 or 404 on bulkfetch -> exactly one retry of the same batch WITHOUT fieldIds (everything is
// filtered client-side); if that fails too -> per issue GET /rest/api/3/issue/{id}/changelog?maxResults=100 with startAt
// paging (response {"startAt","maxResults","total","isLast","values":[{id,created,items}]}; continue while the page
// is not the last: startAt = items read so far).
// SPEC-NOTE (F12): Progress phases: "issues" (total = number of ids), "changelog" (total = number of hydrated issues),
// "comments" (F12: total = number of hydrated issues, because every issue gets a complete comment fetch; it was "only
// the issues that need a top-up, reported only when > 0") and optionally "content". Per phase `total` is constant,
// `done` is non-decreasing in call order, ≤ total, and the last call reaches done == total. Units are issues.
// SPEC-NOTE (F12, was: embedded comments + 35-day top-up): comment bodies ARE stored now (ADR 0015); they stay out of
// logs and exports (AGENTS invariant 12), and author e-mail addresses are never retained. The search excludes `comment`
// and a `comment` key in a hit is ignored. For every hydrated issue the adapter pages GET
// /rest/api/3/issue/{id}/comment?orderBy=created&maxResults=100&startAt=n until the list is complete; the full contract
// (bodies, visibility, edited, short pages, empty page) is in hydrate_content_test.go.
// SPEC-NOTE: Issue types seen while hydrating are returned by a later Reference() call (types map).

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

const wp19Sentinel = "SENTINEL-COMMENT-BODY-4711"

func wp19TS(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000-0700") }

type wp19Cmt struct {
	ID         string
	At         time.Time
	Author     string
	AuthorType string
}

func wp19CmtJSON(c wp19Cmt) map[string]any {
	at := c.AuthorType
	if at == "" {
		at = "atlassian"
	}
	return map[string]any{
		"id": c.ID, "created": wp19TS(c.At),
		"author": map[string]any{"accountId": c.Author, "displayName": "Name of " + c.Author, "emailAddress": c.Author + "@acme.example", "accountType": at},
		"body": map[string]any{"type": "doc", "version": 1, "content": []any{
			map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": wp19Sentinel}}}}},
	}
}

type wp19Iss struct {
	TypeID, TypeName string
	Total            int
	Embedded         []wp19Cmt
}

// wp19IssueJSON is a minimal but realistic search hit for id with the standard custom field ids.
func wp19IssueJSON(id string, o wp19Iss) map[string]any {
	if o.TypeID == "" {
		o.TypeID, o.TypeName = "10002", "Story"
	}
	emb := []any{}
	for _, c := range o.Embedded {
		emb = append(emb, wp19CmtJSON(c))
	}
	return map[string]any{
		"id": id, "key": "MOB-" + id,
		"fields": map[string]any{
			"summary":   "Issue " + id,
			"issuetype": map[string]any{"id": o.TypeID, "name": o.TypeName, "subtask": false, "hierarchyLevel": 0},
			"status":    map[string]any{"id": "10002", "name": "In Progress", "statusCategory": map[string]any{"key": "indeterminate"}},
			"assignee":  map[string]any{"accountId": "u-iris", "displayName": "Iris Calloway", "active": true, "accountType": "atlassian"},
			"created":   "2026-11-01T09:00:00.000+0000", "updated": "2026-11-20T10:00:00.000+0000", "resolutiondate": nil,
			"comment":           map[string]any{"total": o.Total, "maxResults": len(emb), "startAt": 0, "comments": emb},
			"customfield_10020": []any{}, "customfield_10019": "0|i" + id + ":", "customfield_10021": nil,
		},
	}
}

// wp19SearchRoute answers `id in (...)` searches from gen, newest-first (reversed), with optional page capping.
func wp19SearchRoute(t *testing.T, gen func(id string) map[string]any, pageCap int) wp19Route {
	return func(r wp19Req) wp19Resp {
		body := r.json(t)
		jql, _ := body["jql"].(string)
		ids := wp19IDsFromJQL(t, jql)
		rev := make([]string, len(ids))
		for i, id := range ids {
			rev[len(ids)-1-i] = id
		}
		off := 0
		if tok, _ := body["nextPageToken"].(string); tok != "" {
			fmt.Sscanf(tok, "off-%d", &off)
		}
		end := len(rev)
		if pageCap > 0 && off+pageCap < end {
			end = off + pageCap
		}
		issues := []any{}
		for _, id := range rev[off:end] {
			issues = append(issues, gen(id))
		}
		resp := map[string]any{"issues": issues}
		if end < len(rev) {
			resp["nextPageToken"] = fmt.Sprintf("off-%d", end)
		} else {
			resp["isLast"] = true
		}
		return wp19Resp{Body: wp19JSON(t, resp)}
	}
}

type wp19Hist struct {
	ID      string
	Created any // string or number as sent on the wire
	Items   []map[string]any
}

func wp19Item(fieldID, field string, from, to any) map[string]any {
	return map[string]any{"fieldId": fieldID, "field": field, "fieldtype": "jira", "from": from, "fromString": nil, "to": to, "toString": nil}
}

func wp19DefaultHist(id string) []wp19Hist {
	return []wp19Hist{{ID: "h-" + id, Created: wp19TS(wp19Now.Add(-72 * time.Hour)), Items: []map[string]any{wp19Item("status", "status", "10001", "10002")}}}
}

func wp19Histories(hs []wp19Hist) []any {
	out := []any{}
	for _, h := range hs {
		out = append(out, map[string]any{"id": h.ID, "created": h.Created, "author": map[string]any{"accountId": "u-x"}, "items": h.Items})
	}
	return out
}

// wp19BulkRoute answers bulkfetch with hist(id) for every requested id (single page).
func wp19BulkRoute(t *testing.T, hist func(id string) []wp19Hist) wp19Route {
	return func(r wp19Req) wp19Resp {
		ids := wp19StrSlice(r.json(t)["issueIdsOrKeys"])
		logs := []any{}
		for _, id := range ids {
			logs = append(logs, map[string]any{"issueId": id, "changeHistories": wp19Histories(hist(id))})
		}
		return wp19Resp{Body: wp19JSON(t, map[string]any{"issueChangeLogs": logs})}
	}
}

func wp19DefaultGen(id string) map[string]any { return wp19IssueJSON(id, wp19Iss{}) }

func wp19HydrateServer(t *testing.T) *wp19Srv {
	return newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql":          wp19SearchRoute(t, wp19DefaultGen, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, wp19DefaultHist),
	})
}

func wp19IDs(from, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprint(from + i)
	}
	return out
}

// wp19Progress records progress callbacks (the callback may be invoked from several goroutines).
type wp19Progress struct {
	mu    sync.Mutex
	calls []wp19ProgressCall
}

type wp19ProgressCall struct {
	Phase       string
	Done, Total int
}

func (p *wp19Progress) fn() source.ProgressFunc {
	return func(phase string, done, total int) {
		p.mu.Lock()
		p.calls = append(p.calls, wp19ProgressCall{phase, done, total})
		p.mu.Unlock()
	}
}

func (p *wp19Progress) phase(name string) []wp19ProgressCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []wp19ProgressCall
	for _, c := range p.calls {
		if c.Phase == name {
			out = append(out, c)
		}
	}
	return out
}

func wp19Day(offsetDays float64) time.Time {
	return wp19Now.Add(-time.Duration(offsetDays * float64(24*time.Hour)))
}

// ── F12 harness ──────────────────────────────────────────────────────────────
//
// SPEC-NOTE (F12 harness): scan_test.go's wp19 server routes by exact path, but Hydrate now makes one comment request
// per issue and one /field request. newF12Server is the same recording server with two additions: the route keys
// "GET /rest/api/3/issue/{id}/comment" and "GET /rest/api/3/issue/{id}" match any id (the handler reads the id from
// wp19Req.Path), and these defaults are added unless the test supplies its own: GET /rest/api/3/field answers
// testdata/wp18_fields.json (sprint, rank, flagged and a few more) and every comment request answers an empty list.
// Unrouted requests still fail the test, so "no extra calls" stays pinned.

var f12IssuePathRe = regexp.MustCompile(`^/rest/api/3/issue/([^/]+)(/comment)?$`)

func f12Lookup(routes map[string]wp19Route, method, path string) (wp19Route, bool) {
	if r, ok := routes[method+" "+path]; ok {
		return r, true
	}
	if method == "GET" {
		if m := f12IssuePathRe.FindStringSubmatch(path); m != nil {
			key := "GET /rest/api/3/issue/{id}"
			if m[2] == "/comment" {
				key += "/comment"
			}
			r, ok := routes[key]
			return r, ok
		}
	}
	return nil, false
}

func f12IssueIDOf(path string) string {
	if m := f12IssuePathRe.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return ""
}

func newF12Server(t *testing.T, routes map[string]wp19Route) *wp19Srv {
	t.Helper()
	all := map[string]wp19Route{}
	for k, v := range routes {
		all[k] = v
	}
	if _, ok := all["GET /rest/api/3/field"]; !ok {
		all["GET /rest/api/3/field"] = wp19Ok(wp19Testdata(t, "wp18_fields.json"))
	}
	if _, ok := all["GET /rest/api/3/issue/{id}/comment"]; !ok {
		all["GET /rest/api/3/issue/{id}/comment"] = wp19Ok(`{"startAt":0,"maxResults":100,"total":0,"comments":[]}`)
	}
	s := &wp19Srv{routes: all}
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
		route, ok := f12Lookup(all, r.Method, r.URL.Path)
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

// comments returns the requests to GET /rest/api/3/issue/<id>/comment.
func (s *wp19Srv) comments(id string) []wp19Req {
	return s.to("GET", "/rest/api/3/issue/"+id+"/comment")
}

// f12CommentRoute serves comments (ascending by created, as orderBy=created asks) honouring startAt and maxResults;
// pageCap > 0 caps a page below maxResults (a server that returns short pages). Bodies are ADF paragraphs carrying
// wp19Sentinel; every comment has `updated` == `created`.
func f12CommentRoute(t *testing.T, all []wp19Cmt, pageCap int) wp19Route {
	return func(r wp19Req) wp19Resp {
		start, _ := strconv.Atoi(r.Query.Get("startAt"))
		limit, _ := strconv.Atoi(r.Query.Get("maxResults"))
		if limit <= 0 {
			limit = 50
		}
		if pageCap > 0 && limit > pageCap {
			limit = pageCap
		}
		start = min(start, len(all))
		end := min(start+limit, len(all))
		page := []any{}
		for _, c := range all[start:end] {
			page = append(page, wp19CmtJSON(c))
		}
		return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": start, "maxResults": limit, "total": len(all), "comments": page})}
	}
}

// ── batching, order, concurrency ─────────────────────────────────────────────

func TestHydrate_EmptyInputMakesNoRequests(t *testing.T) {
	defer wp19Guard(t)
	srv := newF12Server(t, map[string]wp19Route{})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if n := len(srv.all()); n != 0 {
		t.Fatalf("requests = %d, want 0", n)
	}
}

// F12: batches are 50 ids / maxResults 50 (jira-sync §2.8), was 100.
func TestHydrate_BatchesIssueSearchesBy50AndKeepsInputOrder(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19HydrateServer(t)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	ids := make([]string, 250)
	for i := range ids {
		ids[i] = fmt.Sprint(20000 + (i*37)%250) // a permutation of 20000..20249
	}
	got, err := a.Hydrate(context.Background(), ids, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got) != 250 {
		t.Fatalf("details = %d, want 250", len(got))
	}
	for i, d := range got {
		if d.SourceID != ids[i] || d.Key != "MOB-"+ids[i] {
			t.Fatalf("detail[%d] = %s/%s, want id %s (output follows input order)", i, d.SourceID, d.Key, ids[i])
		}
	}
	searches := srv.to("POST", "/rest/api/3/search/jql")
	if len(searches) != 5 { // F12
		t.Fatalf("search POSTs = %d, want 5 (5×50)", len(searches))
	}
	var sizes []int
	var union []string
	for _, r := range searches {
		body := r.json(t)
		jql, _ := body["jql"].(string)
		if !strings.HasPrefix(strings.TrimSpace(jql), "id in") {
			t.Errorf("jql = %q, want an `id in (...)` query", jql)
		}
		batch := wp19IDsFromJQL(t, jql)
		sizes = append(sizes, len(batch))
		union = append(union, batch...)
		if body["maxResults"] != float64(50) { // F12
			t.Errorf("maxResults = %v, want 50", body["maxResults"])
		}
	}
	sort.Ints(sizes)
	if fmt.Sprint(sizes) != "[50 50 50 50 50]" { // F12
		t.Errorf("batch sizes = %v, want [50 50 50 50 50]", sizes)
	}
	if !wp19SameSet(union, ids) {
		t.Errorf("the batches do not cover exactly the input ids")
	}
	if m := srv.maxInfl; m > 4 {
		t.Errorf("max requests in flight = %d, want ≤ 4", m)
	}
	bulks := srv.to("POST", "/rest/api/3/changelog/bulkfetch")
	if len(bulks) != 1 {
		t.Fatalf("bulkfetch POSTs = %d, want 1 (250 ≤ 500)", len(bulks))
	}
	if b := wp19StrSlice(bulks[0].json(t)["issueIdsOrKeys"]); !wp19SameSet(b, ids) {
		t.Errorf("bulkfetch ids = %d entries, want the 250 hydrated ids", len(b))
	}
}

// F12: the hydrate projection is everything except comments and worklog (jira-sync §2.8); comments come from §2.10.
func TestHydrate_RequestsEverythingExceptCommentsAndWorklog(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19HydrateServer(t)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	if _, err := a.Hydrate(context.Background(), []string{"10001"}, nil); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	body := srv.to("POST", "/rest/api/3/search/jql")[0].json(t)
	want := []string{"*all", "-comment", "-worklog"} // F12
	if got := wp19StrSlice(body["fields"]); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("fields = %v, want exactly %v in that order", got, want)
	}
}

// F12: search batches of 50.
func TestHydrate_ChangelogBatchesBy500AndSearchBatchesBy50(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19HydrateServer(t)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	ids := wp19IDs(30000, 501)
	got, err := a.Hydrate(context.Background(), ids, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got) != 501 {
		t.Fatalf("details = %d, want 501", len(got))
	}
	if n := len(srv.to("POST", "/rest/api/3/search/jql")); n != 11 { // F12
		t.Errorf("search POSTs = %d, want 11 (10×50 + 1)", n)
	}
	bulks := srv.to("POST", "/rest/api/3/changelog/bulkfetch")
	if len(bulks) != 2 {
		t.Fatalf("bulkfetch POSTs = %d, want 2 (500 + 1)", len(bulks))
	}
	var sizes []int
	var union []string
	for _, r := range bulks {
		b := wp19StrSlice(r.json(t)["issueIdsOrKeys"])
		sizes = append(sizes, len(b))
		union = append(union, b...)
	}
	sort.Ints(sizes)
	if fmt.Sprint(sizes) != "[1 500]" {
		t.Errorf("bulkfetch batch sizes = %v, want [1 500]", sizes)
	}
	if !wp19SameSet(union, ids) {
		t.Errorf("bulkfetch batches do not cover exactly the input ids")
	}
}

func TestHydrate_FollowsNextPageTokenOfAnIssueSearch(t *testing.T) {
	defer wp19Guard(t)
	srv := newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql":          wp19SearchRoute(t, wp19DefaultGen, 30), // F12: 100 ids -> 2 batches of 50, each 30 + 20
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, wp19DefaultHist),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	ids := wp19IDs(40000, 100)
	got, err := a.Hydrate(context.Background(), ids, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got) != 100 {
		t.Fatalf("details = %d, want 100 (second page must be fetched)", len(got))
	}
	for i, d := range got {
		if d.SourceID != ids[i] {
			t.Fatalf("detail[%d] = %s, want %s", i, d.SourceID, ids[i])
		}
	}
	searches := srv.to("POST", "/rest/api/3/search/jql")
	if len(searches) != 4 { // F12
		t.Fatalf("search POSTs = %d, want 4 (2 batches × 2 pages)", len(searches))
	}
	var tokens []string
	for _, r := range searches {
		tok, _ := r.json(t)["nextPageToken"].(string)
		tokens = append(tokens, tok)
	}
	sort.Strings(tokens)
	if strings.Join(tokens, "|") != "||off-30|off-30" { // F12: two first pages, two follow-ups
		t.Errorf("tokens = %q, want two first pages and two with off-30", tokens)
	}
}

func TestHydrate_SearchUnauthorizedIsAuthInvalid(t *testing.T) {
	defer wp19Guard(t)
	srv := newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Fail(401, `{}`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	_, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
	wp19AssertCode(t, err, apperr.AuthInvalid)
}

// ── field mapping (golden: testdata/wp19_issue_full.json) ────────────────────

func wp19FullServer(t *testing.T, hist []wp19Hist) *wp19Srv {
	return newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Ok(wp19Testdata(t, "wp19_issue_full.json")),
		// F12: comments come from the comment endpoint (the golden's embedded `comment` key must be ignored)
		"GET /rest/api/3/issue/{id}/comment": f12RawCommentRoute(t, []map[string]any{
			{"id": "70001", "created": "2026-11-21T08:00:00.000+0000", "updated": "2026-11-21T08:00:00.000+0000",
				"author": map[string]any{"accountId": "u-silas", "displayName": "Silas Lee", "emailAddress": "silas.lee@acme.example", "accountType": "atlassian"},
				"body":   f12Doc("alpha")},
			{"id": "70002", "created": "2026-11-22T08:00:00.000+0000", "updated": "2026-11-22T08:00:00.000+0000",
				"author": map[string]any{"accountId": "bot-automation", "displayName": "Automation for Jira", "accountType": "app"},
				"body":   f12Doc("beta")},
		}, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, func(id string) []wp19Hist {
			if id == "10390" {
				return hist
			}
			return nil
		}),
	})
}

func wp19FullHistory() []wp19Hist {
	return []wp19Hist{
		{ID: "h1", Created: "2026-11-06T09:00:00.000+0000", Items: []map[string]any{
			wp19Item("status", "status", "10001", "10002"),
			wp19Item("assignee", "assignee", nil, "u-iris"),
		}},
		{ID: "h2", Created: "2026-11-12T11:30:00.000+0000", Items: []map[string]any{
			wp19Item("assignee", "assignee", "u-iris", nil),
			wp19Item("priority", "priority", "3", "2"),
		}},
		{ID: "h3", Created: "2026-11-13T08:00:00.000+0000", Items: []map[string]any{wp19Item("assignee", "assignee", nil, "u-silas")}},
		{ID: "h4", Created: "2026-11-14T14:45:10.000+0000", Items: []map[string]any{wp19Item("customfield_10021", "Flagged", nil, "10019")}},
		{ID: "h5", Created: "2026-11-16T09:00:00.000+0000", Items: []map[string]any{wp19Item("customfield_10021", "Flagged", "10019", nil)}},
		{ID: "h6", Created: "2026-11-17T09:00:00.000+0000", Items: []map[string]any{{"field": "Flagged", "from": "", "to": "10019"}}}, // legacy: no fieldId
		{ID: "h7", Created: "2026-11-18T09:00:00.000+0000", Items: []map[string]any{wp19Item("customfield_10021", "Flagged", "10019", "")}},
	}
}

func TestHydrate_MapsIssueFieldsFromARealisticResponse(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19FullServer(t, wp19FullHistory())
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), []string{"10390"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("details = %d", len(got))
	}
	d := got[0]
	if d.SourceID != "10390" || d.Key != "MOB-1390" || d.Summary != "Rework the retry budget" {
		t.Errorf("issue = %+v", d.Issue)
	}
	if d.TypeID != "10002" || d.StatusID != "10002" || d.AssigneeID != "u-iris" {
		t.Errorf("TypeID/StatusID/AssigneeID = %q/%q/%q", d.TypeID, d.StatusID, d.AssigneeID)
	}
	if !d.Flagged {
		t.Error("Flagged = false, want true (flagged field is a non-empty array)")
	}
	if d.Rank != "0|i0001b:" {
		t.Errorf("Rank = %q", d.Rank)
	}
	if d.ParentKey != "MOB-1300" {
		t.Errorf("ParentKey = %q", d.ParentKey)
	}
	if strings.Join(d.SprintIDs, ",") != "41,42" {
		t.Errorf("SprintIDs = %v, want [41 42]", d.SprintIDs)
	}
	if !d.CreatedAt.Equal(time.Date(2026, 11, 5, 9, 15, 2, 123_000_000, time.UTC)) {
		t.Errorf("CreatedAt = %v", d.CreatedAt)
	}
	if !d.UpdatedAt.Equal(time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("UpdatedAt = %v", d.UpdatedAt)
	}
	if !d.ResolvedAt.IsZero() {
		t.Errorf("ResolvedAt = %v, want zero (null)", d.ResolvedAt)
	}
	if d.Type != (model.IssueType{ID: "10002", Name: "Story", Kind: "story"}) {
		t.Errorf("Type = %+v", d.Type)
	}
	if d.Status != (model.Status{ID: "10002", Name: "Blocked", Category: "indeterminate", BlockedHint: true}) {
		t.Errorf("Status = %+v", d.Status)
	}
	if d.Assignee == nil || *d.Assignee != (model.Person{AccountID: "u-iris", DisplayName: "Iris Calloway", Active: true, Kind: "user"}) {
		t.Errorf("Assignee = %+v", d.Assignee)
	}

	if len(d.Sprints) != 2 {
		t.Fatalf("Sprints = %+v", d.Sprints)
	}
	s41, s42 := d.Sprints[0], d.Sprints[1]
	if s41.ID != "41" || s41.BoardID != "1" || s41.Name != "Sprint 41" || s41.State != "closed" ||
		!s41.StartAt.Equal(time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)) ||
		!s41.EndAt.Equal(time.Date(2026, 10, 27, 17, 0, 0, 0, time.UTC)) ||
		!s41.CompleteAt.Equal(time.Date(2026, 10, 27, 16, 30, 0, 0, time.UTC)) {
		t.Errorf("Sprints[0] = %+v", s41)
	}
	if s42.ID != "42" || s42.State != "active" || s42.Name != "Sprint 42" || !s42.CompleteAt.IsZero() ||
		!s42.EndAt.Equal(time.Date(2026, 11, 24, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("Sprints[1] = %+v", s42)
	}

	// F12: the complete comment list is always fetched from the comment endpoint (one page here)
	if n := len(srv.to("GET", "/rest/api/3/issue/10390/comment")); n != 1 {
		t.Errorf("comment requests = %d, want 1", n)
	}
	if d.CommentTotal != 2 || len(d.Comments) != 2 {
		t.Fatalf("CommentTotal/Comments = %d/%+v", d.CommentTotal, d.Comments)
	}
	if c := d.Comments[0]; c.ID != "70001" || c.AuthorID != "u-silas" || !c.At.Equal(time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("Comments[0] = %+v", c)
	}
	if c := d.Comments[1]; c.ID != "70002" || c.AuthorID != "bot-automation" || !c.At.Equal(time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("Comments[1] = %+v", c)
	}

	// people: assignee + both comment authors, once each
	byID := map[string]model.Person{}
	for _, p := range d.People {
		if _, dup := byID[p.AccountID]; dup {
			t.Errorf("person %s listed twice: %+v", p.AccountID, d.People)
		}
		byID[p.AccountID] = p
	}
	if len(byID) != 3 {
		t.Fatalf("People = %+v, want u-iris, u-silas, bot-automation", d.People)
	}
	if p := byID["u-iris"]; p.DisplayName != "Iris Calloway" || p.Kind != "user" || !p.Active {
		t.Errorf("u-iris = %+v", p)
	}
	if p := byID["u-silas"]; p.DisplayName != "Silas Lee" || p.Kind != "user" {
		t.Errorf("u-silas = %+v", p)
	}
	if p := byID["bot-automation"]; p.DisplayName != "Automation for Jira" || p.Kind != "app" {
		t.Errorf("bot-automation = %+v", p)
	}

	// changelog
	if len(d.StatusChanges) != 1 || d.StatusChanges[0].ID != "h1" || d.StatusChanges[0].From != "10001" || d.StatusChanges[0].To != "10002" ||
		!d.StatusChanges[0].At.Equal(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("StatusChanges = %+v", d.StatusChanges)
	}
	wantAssignee := []struct{ id, from, to string }{{"h1", "", "u-iris"}, {"h2", "u-iris", ""}, {"h3", "", "u-silas"}}
	if len(d.AssigneeChanges) != len(wantAssignee) {
		t.Fatalf("AssigneeChanges = %+v", d.AssigneeChanges)
	}
	for i, w := range wantAssignee {
		g := d.AssigneeChanges[i]
		if g.ID != w.id || g.From != w.from || g.To != w.to {
			t.Errorf("AssigneeChanges[%d] = %+v, want %+v", i, g, w)
		}
	}
	if !d.AssigneeChanges[1].At.Equal(time.Date(2026, 11, 12, 11, 30, 0, 0, time.UTC)) {
		t.Errorf("AssigneeChanges[1].At = %v", d.AssigneeChanges[1].At)
	}
	wantFlags := []struct {
		id      string
		flagged bool
	}{{"h4", true}, {"h5", false}, {"h6", true}, {"h7", false}}
	if len(d.FlagChanges) != len(wantFlags) {
		t.Fatalf("FlagChanges = %+v, want %d entries (by fieldId, or by field name \"Flagged\")", d.FlagChanges, len(wantFlags))
	}
	for i, w := range wantFlags {
		if g := d.FlagChanges[i]; g.ID != w.id || g.Flagged != w.flagged {
			t.Errorf("FlagChanges[%d] = %+v, want %+v", i, g, w)
		}
	}
	if !d.FlagChanges[0].At.Equal(time.Date(2026, 11, 14, 14, 45, 10, 0, time.UTC)) {
		t.Errorf("FlagChanges[0].At = %v", d.FlagChanges[0].At)
	}
}

// F12: comment bodies are now stored locally (AGENTS invariant 12 keeps them out of logs and exports); e-mail
// addresses of authors are still never retained. The "bodies are kept" half is asserted in hydrate_content_test.go.
func TestHydrate_NeverRetainsEmailAddresses(t *testing.T) {
	defer wp19Guard(t)
	srv := newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql":          wp19SearchRoute(t, wp19DefaultGen, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, wp19DefaultHist),
		"GET /rest/api/3/issue/{id}/comment": f12CommentRoute(t, []wp19Cmt{
			{ID: "c1", At: wp19Day(2), Author: "u-silas"}, {ID: "c2", At: wp19Day(1), Author: "u-iris"},
		}, 0),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got) != 1 || len(got[0].Comments) != 2 || len(got[0].People) < 2 {
		t.Fatalf("precondition: want 2 comments and the people seen, got %+v", got)
	}
	dump := fmt.Sprintf("%+v", got)
	for _, secret := range []string{"@acme.example", "emailAddress"} {
		if strings.Contains(dump, secret) {
			t.Errorf("decoded details contain %q:\n%s", secret, dump)
		}
	}
}

func TestHydrate_UnassignedIssueWithNoOptionalFields(t *testing.T) {
	defer wp19Guard(t)
	srv := newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Ok(`{"isLast":true,"issues":[{"id":"10500","key":"MOB-500","fields":{
			"summary":"Subtask of nothing","issuetype":{"id":"10005","name":"Sub-task","subtask":true,"hierarchyLevel":-1},
			"status":{"id":"10007","name":"Done","statusCategory":{"id":3,"key":"done"}},
			"assignee":null,"created":"2026-10-01T09:00:00.000+0000","updated":"2026-11-02T09:00:00.000+0000",
			"resolutiondate":"2026-11-02T08:59:59.000+0000","parent":null,
			"comment":{"total":0,"comments":[]},
			"customfield_10020":null,"customfield_10021":[]}},
			{"id":"10501","key":"MOB-501","fields":{"summary":"An epic","issuetype":{"id":"10000","name":"Epic","subtask":false,"hierarchyLevel":1},
			"status":{"id":"10001","name":"To Do","statusCategory":{"key":"new"}},"assignee":null,
			"created":"2026-10-01T09:00:00.000+0000","updated":"2026-11-02T09:00:00.000+0000",
			"comment":{"total":0,"comments":[]}}}]}`),
		"POST /rest/api/3/changelog/bulkfetch": wp19Ok(`{"issueChangeLogs":[]}`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), []string{"10500", "10501"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("details = %d", len(got))
	}
	d := got[0]
	if d.Assignee != nil || d.AssigneeID != "" {
		t.Errorf("Assignee/AssigneeID = %+v/%q, want nil/empty", d.Assignee, d.AssigneeID)
	}
	if d.Flagged || d.ParentKey != "" || d.Rank != "" || len(d.SprintIDs) != 0 || len(d.Sprints) != 0 {
		t.Errorf("optional fields not empty: %+v", d)
	}
	if !d.ResolvedAt.Equal(time.Date(2026, 11, 2, 8, 59, 59, 0, time.UTC)) {
		t.Errorf("ResolvedAt = %v", d.ResolvedAt)
	}
	if d.Type.Kind != "subtask" || d.Status.Category != "done" || d.Status.BlockedHint {
		t.Errorf("Type/Status = %+v/%+v", d.Type, d.Status)
	}
	if len(d.People) != 0 || len(d.Comments) != 0 || len(d.StatusChanges) != 0 || len(d.AssigneeChanges) != 0 || len(d.FlagChanges) != 0 {
		t.Errorf("expected no people/comments/changes: %+v", d)
	}
	if got[1].Type.Kind != "epic" || got[1].Assignee != nil {
		t.Errorf("second = %+v", got[1])
	}
}

func TestHydrate_RecordsIssueTypesForALaterReference(t *testing.T) {
	defer wp19Guard(t)
	srv := newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19SearchRoute(t, func(id string) map[string]any {
			if id == "10002" {
				return wp19IssueJSON(id, wp19Iss{TypeID: "10004", TypeName: "Bug"})
			}
			return wp19IssueJSON(id, wp19Iss{TypeID: "10002", TypeName: "Story"})
		}, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, func(string) []wp19Hist { return nil }),
		"GET /rest/api/3/status":               wp19Ok(`[{"id":"10002","name":"In Progress","statusCategory":{"key":"indeterminate"}}]`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	if _, err := a.Hydrate(context.Background(), []string{"10001", "10002", "10003"}, nil); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	_, types, err := a.Reference(context.Background())
	if err != nil {
		t.Fatalf("Reference: %v", err)
	}
	got := map[string]model.IssueType{}
	for _, ty := range types {
		got[ty.ID] = ty
	}
	if len(types) != 2 || got["10002"] != (model.IssueType{ID: "10002", Name: "Story", Kind: "story"}) ||
		got["10004"] != (model.IssueType{ID: "10004", Name: "Bug", Kind: "bug"}) {
		t.Fatalf("types = %+v, want Story(10002) and Bug(10004) once each", types)
	}
}

// ── changelog ────────────────────────────────────────────────────────────────

func TestHydrate_ChangelogRequestShape(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19HydrateServer(t)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	if _, err := a.Hydrate(context.Background(), []string{"10001", "10002"}, nil); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	bulks := srv.to("POST", "/rest/api/3/changelog/bulkfetch")
	if len(bulks) != 1 {
		t.Fatalf("bulkfetch POSTs = %d", len(bulks))
	}
	body := bulks[0].json(t)
	if ids := wp19StrSlice(body["issueIdsOrKeys"]); !wp19SameSet(ids, []string{"10001", "10002"}) {
		t.Errorf("issueIdsOrKeys = %v", ids)
	}
	if f := wp19StrSlice(body["fieldIds"]); !wp19SameSet(f, []string{"status", "assignee", "customfield_10021"}) {
		t.Errorf("fieldIds = %v, want status, assignee, customfield_10021", f)
	}
	if body["maxResults"] != float64(1000) {
		t.Errorf("maxResults = %v, want 1000", body["maxResults"])
	}
	if tok, _ := body["nextPageToken"].(string); tok != "" {
		t.Errorf("first page nextPageToken = %q", tok)
	}
}

func TestHydrate_ChangelogFollowsNextPageTokenAndMergesPerIssue(t *testing.T) {
	defer wp19Guard(t)
	ms := func(tm time.Time) any { return tm.UnixMilli() }
	srv := newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19SearchRoute(t, wp19DefaultGen, 0),
		"POST /rest/api/3/changelog/bulkfetch": func(r wp19Req) wp19Resp {
			switch tok, _ := r.json(t)["nextPageToken"].(string); tok {
			case "":
				return wp19Resp{Body: wp19JSON(t, map[string]any{
					"nextPageToken": "bf-2",
					"issueChangeLogs": []any{
						map[string]any{"issueId": "10001", "changeHistories": wp19Histories([]wp19Hist{
							{ID: "a1", Created: ms(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)), Items: []map[string]any{wp19Item("status", "status", "10001", "10002")}}})},
						map[string]any{"issueId": "10002", "changeHistories": wp19Histories([]wp19Hist{
							{ID: "b1", Created: ms(time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)), Items: []map[string]any{wp19Item("status", "status", "10001", "10003")}}})},
					}})}
			case "bf-2":
				return wp19Resp{Body: wp19JSON(t, map[string]any{
					"issueChangeLogs": []any{
						map[string]any{"issueId": "10001", "changeHistories": wp19Histories([]wp19Hist{
							{ID: "a2", Created: ms(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)), Items: []map[string]any{wp19Item("status", "status", "10002", "10003")}}})},
					}})}
			}
			t.Errorf("unknown token: %s", r.Body)
			return wp19Resp{Status: 400, Body: `{}`}
		},
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), []string{"10001", "10002"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if n := len(srv.to("POST", "/rest/api/3/changelog/bulkfetch")); n != 2 {
		t.Fatalf("bulkfetch POSTs = %d, want 2 (follow nextPageToken)", n)
	}
	if tok, _ := srv.to("POST", "/rest/api/3/changelog/bulkfetch")[1].json(t)["nextPageToken"].(string); tok != "bf-2" {
		t.Errorf("second request token = %q", tok)
	}
	var ids1 []string
	for _, c := range got[0].StatusChanges {
		ids1 = append(ids1, c.ID)
	}
	if strings.Join(ids1, ",") != "a1,a2" {
		t.Errorf("10001 status changes = %v, want [a1 a2] (merged across pages)", ids1)
	}
	if !got[0].StatusChanges[1].At.Equal(time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("epoch-ms created decoded as %v", got[0].StatusChanges[1].At)
	}
	if len(got[1].StatusChanges) != 1 || got[1].StatusChanges[0].ID != "b1" || got[1].StatusChanges[0].To != "10003" {
		t.Errorf("10002 status changes = %+v", got[1].StatusChanges)
	}
}

func TestHydrate_ChangelogRetriesWithoutFieldIdsOnRejection(t *testing.T) {
	defer wp19Guard(t)
	for _, status := range []int{400, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			defer wp19Guard(t)
			srv := newF12Server(t, map[string]wp19Route{
				"POST /rest/api/3/search/jql": wp19SearchRoute(t, wp19DefaultGen, 0),
				"POST /rest/api/3/changelog/bulkfetch": func(r wp19Req) wp19Resp {
					if _, has := r.json(t)["fieldIds"]; has {
						return wp19Resp{Status: status, Body: `{"errorMessages":["Invalid field id customfield_10021"]}`}
					}
					return wp19BulkRoute(t, func(id string) []wp19Hist {
						return []wp19Hist{
							{ID: "x1-" + id, Created: "2026-11-06T09:00:00.000+0000", Items: []map[string]any{
								wp19Item("priority", "priority", "3", "2"), // must be filtered out client-side
								wp19Item("status", "status", "10001", "10002"),
							}},
							{ID: "x2-" + id, Created: "2026-11-07T09:00:00.000+0000", Items: []map[string]any{wp19Item("customfield_10021", "Flagged", nil, "10019")}},
							{ID: "x3-" + id, Created: "2026-11-08T09:00:00.000+0000", Items: []map[string]any{wp19Item("labels", "labels", "", "a")}},
						}
					})(r)
				},
			})
			a := newWP19Adapter(t, srv.URL, wp19Meta())
			got, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
			if err != nil {
				t.Fatalf("Hydrate: %v", err)
			}
			bulks := srv.to("POST", "/rest/api/3/changelog/bulkfetch")
			if len(bulks) != 2 {
				t.Fatalf("bulkfetch POSTs = %d, want exactly 2 (with, then without fieldIds)", len(bulks))
			}
			if _, has := bulks[0].json(t)["fieldIds"]; !has {
				t.Error("first request has no fieldIds")
			}
			if _, has := bulks[1].json(t)["fieldIds"]; has {
				t.Error("retry still carries fieldIds")
			}
			if n := len(srv.to("GET", "/rest/api/3/issue/10001/changelog")); n != 0 {
				t.Errorf("per-issue changelog requests = %d, want 0", n)
			}
			d := got[0]
			if len(d.StatusChanges) != 1 || d.StatusChanges[0].ID != "x1-10001" || d.StatusChanges[0].To != "10002" {
				t.Errorf("StatusChanges = %+v", d.StatusChanges)
			}
			if len(d.FlagChanges) != 1 || d.FlagChanges[0].ID != "x2-10001" || !d.FlagChanges[0].Flagged {
				t.Errorf("FlagChanges = %+v", d.FlagChanges)
			}
			if len(d.AssigneeChanges) != 0 {
				t.Errorf("AssigneeChanges = %+v, want none", d.AssigneeChanges)
			}
		})
	}
}

func TestHydrate_ChangelogFallsBackToPerIssuePagedEndpoint(t *testing.T) {
	defer wp19Guard(t)
	page := func(startAt, total int, last bool, hs []wp19Hist) wp19Resp {
		return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": startAt, "maxResults": 100, "total": total, "isLast": last, "values": wp19Histories(hs)})}
	}
	srv := newF12Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql":          wp19SearchRoute(t, wp19DefaultGen, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19Fail(400, `{"errorMessages":["not supported"]}`),
		"GET /rest/api/3/issue/10001/changelog": func(r wp19Req) wp19Resp {
			if r.Query.Get("startAt") == "2" {
				return page(2, 3, true, []wp19Hist{
					{ID: "p3", Created: "2026-11-09T09:00:00.000+0000", Items: []map[string]any{wp19Item("assignee", "assignee", "u-silas", nil)}}})
			}
			return page(0, 3, false, []wp19Hist{
				{ID: "p1", Created: "2026-11-06T09:00:00.000+0000", Items: []map[string]any{wp19Item("status", "status", "10001", "10002")}},
				{ID: "p2", Created: "2026-11-07T09:00:00.000+0000", Items: []map[string]any{wp19Item("priority", "priority", "3", "2")}},
			})
		},
		"GET /rest/api/3/issue/10002/changelog": func(r wp19Req) wp19Resp {
			return page(0, 1, true, []wp19Hist{
				{ID: "q1", Created: "2026-11-08T09:00:00.000+0000", Items: []map[string]any{wp19Item("customfield_10021", "Flagged", nil, "10019")}}})
		},
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), []string{"10001", "10002"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if n := len(srv.to("POST", "/rest/api/3/changelog/bulkfetch")); n != 2 {
		t.Errorf("bulkfetch POSTs = %d, want 2 (with, then without fieldIds) before the per-issue fallback", n)
	}
	for _, r := range srv.to("GET", "/rest/api/3/issue/10001/changelog") {
		if r.Query.Get("maxResults") != "100" {
			t.Errorf("per-issue maxResults = %q, want 100", r.Query.Get("maxResults"))
		}
	}
	if n := len(srv.to("GET", "/rest/api/3/issue/10001/changelog")); n != 2 {
		t.Errorf("issue 10001 changelog requests = %d, want 2 (startAt paging)", n)
	}
	if n := len(srv.to("GET", "/rest/api/3/issue/10002/changelog")); n != 1 {
		t.Errorf("issue 10002 changelog requests = %d, want 1", n)
	}
	d1, d2 := got[0], got[1]
	if len(d1.StatusChanges) != 1 || d1.StatusChanges[0].ID != "p1" || d1.StatusChanges[0].From != "10001" || d1.StatusChanges[0].To != "10002" {
		t.Errorf("10001 StatusChanges = %+v", d1.StatusChanges)
	}
	if len(d1.AssigneeChanges) != 1 || d1.AssigneeChanges[0].ID != "p3" || d1.AssigneeChanges[0].From != "u-silas" || d1.AssigneeChanges[0].To != "" {
		t.Errorf("10001 AssigneeChanges = %+v (second page must be read)", d1.AssigneeChanges)
	}
	if len(d2.FlagChanges) != 1 || d2.FlagChanges[0].ID != "q1" || !d2.FlagChanges[0].Flagged {
		t.Errorf("10002 FlagChanges = %+v", d2.FlagChanges)
	}
}

// ── comments ─────────────────────────────────────────────────────────────────
// F12: the three top-up tests (embedded comments + 35-day horizon) are gone: comments are fetched completely for every
// hydrated issue (see hydrate_content_test.go: paging, bodies, visibility, edited).

// ── progress ─────────────────────────────────────────────────────────────────

// F12: every hydrated issue gets its complete comment list, so the comments phase total is the number of issues.
func TestHydrate_ReportsProgressPerPhaseMonotonically(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19HydrateServer(t)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	ids := wp19IDs(20000, 250)
	var prog wp19Progress
	got, err := a.Hydrate(context.Background(), ids, prog.fn())
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got) != 250 {
		t.Fatalf("details = %d", len(got))
	}
	prog.mu.Lock()
	for _, c := range prog.calls {
		switch c.Phase {
		case source.PhaseIssues, source.PhaseChangelog, source.PhaseComments, source.PhaseContent:
		default:
			t.Errorf("unexpected phase %q (Hydrate reports issues, changelog, comments and content only)", c.Phase)
		}
	}
	prog.mu.Unlock()
	for phase, wantTotal := range map[string]int{source.PhaseIssues: 250, source.PhaseChangelog: 250, source.PhaseComments: 250} { // F12
		calls := prog.phase(phase)
		if len(calls) == 0 {
			t.Errorf("no progress reported for phase %q", phase)
			continue
		}
		prev := 0
		for i, c := range calls {
			if c.Total != wantTotal {
				t.Errorf("phase %s call %d: total = %d, want %d", phase, i, c.Total, wantTotal)
			}
			if c.Done < prev || c.Done > c.Total {
				t.Errorf("phase %s call %d: done = %d after %d (total %d): must be monotonic and <= total", phase, i, c.Done, prev, c.Total)
			}
			prev = c.Done
		}
		if last := calls[len(calls)-1]; last.Done != last.Total {
			t.Errorf("phase %s: last call = %d/%d, want done == total", phase, last.Done, last.Total)
		}
	}
}

// F12: was "...NoCommentsPhaseWithoutTopUps": the comments phase is now always reported (total = issues).
func TestHydrate_NilProgressIsAllowedAndCommentsPhaseCoversEveryIssue(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19HydrateServer(t)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	var prog wp19Progress
	if _, err := a.Hydrate(context.Background(), []string{"10001", "10002"}, prog.fn()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	c := prog.phase(source.PhaseComments)
	if len(c) == 0 || c[len(c)-1].Total != 2 || c[len(c)-1].Done != 2 {
		t.Errorf("comments phase = %+v, want a final 2/2", c)
	}
	srv2 := wp19HydrateServer(t)
	a2 := newWP19Adapter(t, srv2.URL, wp19Meta())
	if _, err := a2.Hydrate(context.Background(), []string{"10001"}, nil); err != nil {
		t.Fatalf("Hydrate with nil progress: %v", err)
	}
}

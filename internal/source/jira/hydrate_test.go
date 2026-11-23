package jira

// WP-19 tests for hydrate.go (Hydrate): issue batches, field mapping to model.IssueDetail, changelog bulkfetch with
// its fallbacks, comment top-ups, comment-body hygiene, people, progress and output order (jira-sync.md §2.8–§2.10,
// §4.3; architecture.md R6). Harness and conventions: see scan_test.go.
//
// SPEC-NOTE: Hydrate sends POST /rest/api/3/search/jql with `jql` = `id in (<ids>)` (ids are digits; the separator is
// free), `maxResults` 100 and `fields` = exactly {summary, issuetype, status, assignee, created, updated,
// resolutiondate, parent, comment, customfield_10020, customfield_10019, customfield_10021} (order free; the three
// custom ids come from Meta). A batch is ≤ 100 ids; if a response carries nextPageToken it is followed.
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
// SPEC-NOTE: Progress phases: "issues" (total = number of ids), "changelog" (total = number of hydrated issues), and
// "comments" (total = number of issues that need a top-up; reported only when > 0). Per phase `total` is constant,
// `done` is non-decreasing in call order, ≤ total, and the last call reaches done == total. Units are issues.
// SPEC-NOTE: Comments: embedded comments are Comment{ID, At (created), AuthorID (author.accountId)}; CommentTotal =
// comment.total. A top-up GET /rest/api/3/issue/{id}/comment?orderBy=-created&maxResults=100&startAt=n happens only
// when comment.total > len(embedded) AND the oldest embedded comment is NOT older than 35 days (tests use 34 d and
// 36 d). It stops at the first page whose oldest comment is older than 35 d (or when total is exhausted). Top-up
// results are merged into Comments de-duplicated by id. No bodies are retained: the decoded struct, printed with %+v,
// never contains the body text or e-mail addresses.
// SPEC-NOTE: Issue types seen while hydrating are returned by a later Reference() call (types map).

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
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
	return newWP19Server(t, map[string]wp19Route{
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

// ── batching, order, concurrency ─────────────────────────────────────────────

func TestHydrate_EmptyInputMakesNoRequests(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if n := len(srv.all()); n != 0 {
		t.Fatalf("requests = %d, want 0", n)
	}
}

func TestHydrate_BatchesIssueSearchesBy100AndKeepsInputOrder(t *testing.T) {
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
	if len(searches) != 3 {
		t.Fatalf("search POSTs = %d, want 3 (100+100+50)", len(searches))
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
		if body["maxResults"] != float64(100) {
			t.Errorf("maxResults = %v, want 100", body["maxResults"])
		}
	}
	sort.Ints(sizes)
	if fmt.Sprint(sizes) != "[50 100 100]" {
		t.Errorf("batch sizes = %v, want [50 100 100]", sizes)
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

func TestHydrate_RequestsExactlyTheDocumentedFields(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19HydrateServer(t)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	if _, err := a.Hydrate(context.Background(), []string{"10001"}, nil); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	body := srv.to("POST", "/rest/api/3/search/jql")[0].json(t)
	want := []string{"summary", "issuetype", "status", "assignee", "created", "updated", "resolutiondate", "parent", "comment",
		"customfield_10020", "customfield_10019", "customfield_10021"}
	if got := wp19StrSlice(body["fields"]); !wp19SameSet(got, want) {
		t.Errorf("fields = %v, want %v", got, want)
	}
}

func TestHydrate_ChangelogBatchesBy500AndSearchBatchesBy100(t *testing.T) {
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
	if n := len(srv.to("POST", "/rest/api/3/search/jql")); n != 6 {
		t.Errorf("search POSTs = %d, want 6 (5×100 + 1)", n)
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
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql":          wp19SearchRoute(t, wp19DefaultGen, 60), // 100 ids -> 60 + 40
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
	if len(searches) != 2 {
		t.Fatalf("search POSTs = %d, want 2", len(searches))
	}
	var tokens []string
	for _, r := range searches {
		tok, _ := r.json(t)["nextPageToken"].(string)
		tokens = append(tokens, tok)
	}
	sort.Strings(tokens)
	if tokens[0] != "" || tokens[1] != "off-60" {
		t.Errorf("tokens = %q, want one first page and one with off-60", tokens)
	}
}

func TestHydrate_SearchUnauthorizedIsAuthInvalid(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Fail(401, `{}`),
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	_, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
	wp19AssertCode(t, err, apperr.AuthInvalid)
}

// ── field mapping (golden: testdata/wp19_issue_full.json) ────────────────────

func wp19FullServer(t *testing.T, hist []wp19Hist) *wp19Srv {
	return newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Ok(wp19Testdata(t, "wp19_issue_full.json")),
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

	// embedded comments are complete (total 2 == len), so no top-up request
	if n := len(srv.to("GET", "/rest/api/3/issue/10390/comment")); n != 0 {
		t.Errorf("comment top-up requests = %d, want 0", n)
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

func TestHydrate_NeverRetainsCommentBodiesOrEmailAddresses(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19SearchRoute(t, func(id string) map[string]any {
			return wp19IssueJSON(id, wp19Iss{Total: 3, Embedded: []wp19Cmt{{ID: "c1", At: wp19Day(2), Author: "u-silas"}}})
		}, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, wp19DefaultHist),
		"GET /rest/api/3/issue/10001/comment": func(wp19Req) wp19Resp {
			return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": 0, "maxResults": 100, "total": 3, "comments": []any{
				wp19CmtJSON(wp19Cmt{ID: "c3", At: wp19Day(1), Author: "u-iris"}),
				wp19CmtJSON(wp19Cmt{ID: "c2", At: wp19Day(1.5), Author: "u-silas"}),
				wp19CmtJSON(wp19Cmt{ID: "c1", At: wp19Day(2), Author: "u-silas"}),
			}})}
		},
	})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if n := len(srv.to("GET", "/rest/api/3/issue/10001/comment")); n != 1 {
		t.Fatalf("top-up requests = %d, want 1 (the sentinel must be present in the top-up response too)", n)
	}
	dump := fmt.Sprintf("%+v", got)
	for _, secret := range []string{wp19Sentinel, "@acme.example", "emailAddress"} {
		if strings.Contains(dump, secret) {
			t.Errorf("decoded details contain %q:\n%s", secret, dump)
		}
	}
}

func TestHydrate_UnassignedIssueWithNoOptionalFields(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
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
	srv := newWP19Server(t, map[string]wp19Route{
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
	srv := newWP19Server(t, map[string]wp19Route{
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
			srv := newWP19Server(t, map[string]wp19Route{
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
	srv := newWP19Server(t, map[string]wp19Route{
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

func wp19CommentServer(t *testing.T, emb []wp19Cmt, total int, topup wp19Route) *wp19Srv {
	return newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19SearchRoute(t, func(id string) map[string]any {
			return wp19IssueJSON(id, wp19Iss{Total: total, Embedded: emb})
		}, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, func(string) []wp19Hist { return nil }),
		"GET /rest/api/3/issue/10001/comment":  topup,
	})
}

func TestHydrate_CommentTopUp_FetchesNewestFirstAndMergesWithoutDuplicates(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19CommentServer(t, []wp19Cmt{{ID: "c1", At: wp19Day(2), Author: "u-silas"}}, 3,
		func(wp19Req) wp19Resp {
			return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": 0, "maxResults": 100, "total": 3, "comments": []any{
				wp19CmtJSON(wp19Cmt{ID: "c3", At: wp19Day(1), Author: "u-iris"}),
				wp19CmtJSON(wp19Cmt{ID: "c2", At: wp19Day(1.5), Author: "u-silas"}),
				wp19CmtJSON(wp19Cmt{ID: "c1", At: wp19Day(2), Author: "u-silas"}),
			}})}
		})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	reqs := srv.to("GET", "/rest/api/3/issue/10001/comment")
	if len(reqs) != 1 {
		t.Fatalf("top-up requests = %d, want 1", len(reqs))
	}
	for k, want := range map[string]string{"orderBy": "-created", "maxResults": "100", "startAt": "0"} {
		if got := reqs[0].Query.Get(k); got != want {
			t.Errorf("top-up query %s = %q, want %q (all: %v)", k, got, want, reqs[0].Query)
		}
	}
	d := got[0]
	if d.CommentTotal != 3 {
		t.Errorf("CommentTotal = %d, want 3", d.CommentTotal)
	}
	ids := map[string]string{}
	for _, c := range d.Comments {
		if _, dup := ids[c.ID]; dup {
			t.Errorf("comment %s listed twice: %+v", c.ID, d.Comments)
		}
		ids[c.ID] = c.AuthorID
	}
	if len(ids) != 3 || ids["c1"] != "u-silas" || ids["c2"] != "u-silas" || ids["c3"] != "u-iris" {
		t.Errorf("Comments = %+v, want c1,c2,c3 with their authors", d.Comments)
	}
}

func TestHydrate_CommentTopUp_OnlyWhenTotalExceedsEmbeddedAndOldestEmbeddedIsWithin35Days(t *testing.T) {
	defer wp19Guard(t)
	tests := []struct {
		name         string
		embedded     []wp19Cmt
		total        int
		wantRequests int
	}{
		{"oldest embedded 34 days old: top up", []wp19Cmt{{ID: "c1", At: wp19Day(34), Author: "u-silas"}, {ID: "c2", At: wp19Day(3), Author: "u-silas"}}, 5, 1},
		{"oldest embedded 36 days old: already past the horizon", []wp19Cmt{{ID: "c1", At: wp19Day(36), Author: "u-silas"}, {ID: "c2", At: wp19Day(3), Author: "u-silas"}}, 5, 0},
		{"total equals embedded count", []wp19Cmt{{ID: "c1", At: wp19Day(2), Author: "u-silas"}, {ID: "c2", At: wp19Day(1), Author: "u-silas"}}, 2, 0},
		{"no comments at all", nil, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			srv := wp19CommentServer(t, tc.embedded, tc.total, func(wp19Req) wp19Resp {
				return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": 0, "maxResults": 100, "total": tc.total, "comments": []any{}})}
			})
			a := newWP19Adapter(t, srv.URL, wp19Meta())
			got, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
			if err != nil {
				t.Fatalf("Hydrate: %v", err)
			}
			if n := len(srv.to("GET", "/rest/api/3/issue/10001/comment")); n != tc.wantRequests {
				t.Errorf("top-up requests = %d, want %d", n, tc.wantRequests)
			}
			if got[0].CommentTotal != tc.total {
				t.Errorf("CommentTotal = %d, want %d", got[0].CommentTotal, tc.total)
			}
		})
	}
}

func TestHydrate_CommentTopUp_PagesUntilAPageReachesPast35Days(t *testing.T) {
	defer wp19Guard(t)
	mk := func(prefix string, n int, base float64, last time.Time) []any {
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			at := wp19Now.Add(-time.Duration(base*24*float64(time.Hour)) - time.Duration(i)*time.Minute)
			if i == n-1 && !last.IsZero() {
				at = last
			}
			out = append(out, wp19CmtJSON(wp19Cmt{ID: fmt.Sprintf("%s-%03d", prefix, i), At: at, Author: "u-silas"}))
		}
		return out
	}
	srv := wp19CommentServer(t, []wp19Cmt{{ID: "c-emb", At: wp19Day(3), Author: "u-iris"}}, 250,
		func(r wp19Req) wp19Resp {
			switch r.Query.Get("startAt") {
			case "0":
				return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": 0, "maxResults": 100, "total": 250, "comments": mk("pa", 100, 1, time.Time{})})}
			case "100":
				return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": 100, "maxResults": 100, "total": 250, "comments": mk("pb", 100, 2, wp19Day(36))})}
			}
			t.Errorf("unexpected comment page: %v", r.Query)
			return wp19Resp{Body: `{"comments":[]}`}
		})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	reqs := srv.to("GET", "/rest/api/3/issue/10001/comment")
	if len(reqs) != 2 {
		t.Fatalf("comment requests = %d, want 2 (the second page's oldest is 36 d old: stop)", len(reqs))
	}
	if reqs[0].Query.Get("startAt") != "0" || reqs[1].Query.Get("startAt") != "100" {
		t.Errorf("startAt sequence = %q, %q, want 0 then 100", reqs[0].Query.Get("startAt"), reqs[1].Query.Get("startAt"))
	}
	have := map[string]bool{}
	for _, c := range got[0].Comments {
		have[c.ID] = true
	}
	for i := 0; i < 100; i++ {
		if !have[fmt.Sprintf("pa-%03d", i)] {
			t.Fatalf("comment pa-%03d missing", i)
		}
	}
	for i := 0; i < 99; i++ {
		if !have[fmt.Sprintf("pb-%03d", i)] {
			t.Fatalf("comment pb-%03d missing", i)
		}
	}
	if !have["c-emb"] {
		t.Error("embedded comment c-emb was dropped")
	}
	if got[0].CommentTotal != 250 {
		t.Errorf("CommentTotal = %d, want 250", got[0].CommentTotal)
	}
}

// ── progress ─────────────────────────────────────────────────────────────────

func TestHydrate_ReportsProgressPerPhaseMonotonically(t *testing.T) {
	defer wp19Guard(t)
	needTopUp := map[string]bool{"20007": true, "20123": true}
	topup := func(id string) wp19Route {
		return func(wp19Req) wp19Resp {
			return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": 0, "maxResults": 100, "total": 3, "comments": []any{
				wp19CmtJSON(wp19Cmt{ID: "t3-" + id, At: wp19Day(1), Author: "u-iris"}),
				wp19CmtJSON(wp19Cmt{ID: "t2-" + id, At: wp19Day(2), Author: "u-iris"}),
				wp19CmtJSON(wp19Cmt{ID: "t1-" + id, At: wp19Day(3), Author: "u-iris"}),
			}})}
		}
	}
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19SearchRoute(t, func(id string) map[string]any {
			if needTopUp[id] {
				return wp19IssueJSON(id, wp19Iss{Total: 3, Embedded: []wp19Cmt{{ID: "e-" + id, At: wp19Day(3), Author: "u-iris"}}})
			}
			return wp19IssueJSON(id, wp19Iss{})
		}, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, wp19DefaultHist),
		"GET /rest/api/3/issue/20007/comment":  topup("20007"),
		"GET /rest/api/3/issue/20123/comment":  topup("20123"),
	})
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
		if c.Phase != source.PhaseIssues && c.Phase != source.PhaseChangelog && c.Phase != source.PhaseComments {
			t.Errorf("unexpected phase %q (Hydrate reports issues, changelog, comments only)", c.Phase)
		}
	}
	prog.mu.Unlock()
	for phase, wantTotal := range map[string]int{source.PhaseIssues: 250, source.PhaseChangelog: 250, source.PhaseComments: 2} {
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
				t.Errorf("phase %s call %d: done = %d after %d (total %d): must be monotonic and ≤ total", phase, i, c.Done, prev, c.Total)
			}
			prev = c.Done
		}
		if last := calls[len(calls)-1]; last.Done != last.Total {
			t.Errorf("phase %s: last call = %d/%d, want done == total", phase, last.Done, last.Total)
		}
	}
}

func TestHydrate_NilProgressIsAllowedAndNoCommentsPhaseWithoutTopUps(t *testing.T) {
	defer wp19Guard(t)
	srv := wp19HydrateServer(t)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	var prog wp19Progress
	if _, err := a.Hydrate(context.Background(), []string{"10001", "10002"}, prog.fn()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if c := prog.phase(source.PhaseComments); len(c) != 0 {
		t.Errorf("comments phase reported without any top-up: %+v", c)
	}
	srv2 := wp19HydrateServer(t)
	a2 := newWP19Adapter(t, srv2.URL, wp19Meta())
	if _, err := a2.Hydrate(context.Background(), []string{"10001"}, nil); err != nil {
		t.Fatalf("Hydrate with nil progress: %v", err)
	}
}

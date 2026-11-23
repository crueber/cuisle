package jiratest

// Tests for WP-07: the fake Jira HTTP server (endpoints of jira-sync.md §2 other than issue search).
// This file also owns the helpers shared by faults_test.go, fixture_test.go and jql_test.go.
//
// SPEC-NOTE (guard): every test installs guard() so a panicking stub (WP-07/WP-08 "not implemented") shows up as a
// per-test failure instead of killing the whole test binary and hiding every other result.
//
// SPEC-NOTE (ids): ids are never hard-coded. The contract says both `"10001"…` and `e.g. "10390"`, so tests read issue
// and status ids from the live Dataset and assert only "numeric, unique".
//
// SPEC-NOTE (log timing): Requests() is read after the client received the response. The fake must therefore record
// a request in the log no later than it finishes the response (log before the handler returns). Tests poll with
// runtime.Gosched (bounded) rather than sleeping, so a tiny lag is tolerated, but a missing entry fails.
//
// SPEC-NOTE (wire types): ids in JSON (board id, filter.id, status ids) are compared as strings after normalising
// numbers and strings alike; timestamps are accepted as ISO-8601 (offset with or without colon, optional millis) or
// epoch milliseconds (jira-sync §1 "Decoding").
//
// SPEC-NOTE (picks beyond the doc): unknown board or unknown issue id answers 404; sprint state=closed answers 200 with
// no values (the fake only knows the active sprint); board search accepts the project id as projectKeyOrId; an empty
// email is accepted as long as the token is right; changelog bulkfetch omits histories that have no item matching
// fieldIds; project name is the standup's `name` (e.g. "Platform"), not the `space` label.
//
// SPEC-NOTE (assignable search): FxUser has no project link, so GET user/assignable/search ignores `project` for
// filtering and filters only by `query` (case-insensitive substring of displayName). Users with an empty AccountType
// are served as "atlassian"; "app" users are served as "app" (the adapter does the filtering).

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"
)

// ── shared helpers ──────────────────────────────────────────────────────────────────────────────

func guard(t *testing.T) {
	t.Helper()
	if r := recover(); r != nil {
		t.Fatalf("panicked (stub not implemented?): %v", r)
	}
}

func sub(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		defer guard(t)
		fn(t)
	})
}

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

var (
	nyc = mustLoc("America/New_York")
	// 22:30 EST on 2026-11-23 (03:30 UTC on the 24th). Today's 10:00 and 11:00 local are in the past.
	testNow = time.Date(2026, 11, 23, 22, 30, 0, 0, nyc)
)

// dayStr is the local calendar date "n days from today" (n = -1 is yesterday) as yyyy-mm-dd.
func dayStr(now time.Time, loc *time.Location, n int) string {
	return now.In(loc).AddDate(0, 0, n).Format("2006-01-02")
}

func localDate(tm time.Time, loc *time.Location) string { return tm.In(loc).Format("2006-01-02") }

func assertLocalDayHour(t *testing.T, label string, tm time.Time, now time.Time, loc *time.Location, day, hour int) {
	t.Helper()
	l := tm.In(loc)
	if got, want := l.Format("2006-01-02"), dayStr(now, loc, day); got != want {
		t.Errorf("%s: local date = %s, want %s (day %d)", label, got, want, day)
	}
	if l.Hour() != hour || l.Minute() != 0 || l.Second() != 0 {
		t.Errorf("%s: local time = %s, want %02d:00:00", label, l.Format("15:04:05"), hour)
	}
}

// tinyFixture is the small hand-built fixture used by every server test.
//
//	board 1 "MOB board"  scrum,  filter 9001, sprint 42 active (-13..0): MOB-1..MOB-5 then backlog MOB-6, MOB-7
//	board 2 "MOB Kanban" kanban, filter 9002, subQuery:                  MOB-10 (Task), MOB-11 (Sub-task), MOB-12 (Epic)
//	board 3 "PLAT board"  scrum without a sprint, filter 9003:           PLAT-1
func tinyFixture() *Fixture {
	cols := []FxColumn{
		{Name: "Selected for Development", Statuses: []string{"To Do"}},
		{Name: "In Progress", Statuses: []string{"In Progress"}},
		{Name: "Done", Statuses: []string{"Done"}},
	}
	return &Fixture{
		Projects: []FxProject{
			{ID: "10001", Key: "MOB", Name: "Saffron Mobile"},
			{ID: "10002", Key: "PLAT", Name: "Platform"},
			{ID: "10003", Key: "WEB", Name: "Web & Checkout"},
		},
		Boards: []FxBoard{
			{ID: 1, Name: "MOB board", Type: "scrum", ProjectKey: "MOB", FilterID: 9001, Columns: cols,
				Sprint: &FxSprint{ID: 42, Name: "Sprint 42", StartDay: -13, EndDay: 0}},
			{ID: 2, Name: "MOB Kanban", Type: "kanban", ProjectKey: "MOB", FilterID: 9002,
				SubQuery: "resolution = Unresolved", Columns: cols},
			{ID: 3, Name: "PLAT board", Type: "scrum", ProjectKey: "PLAT", FilterID: 9003, Columns: cols},
		},
		Users: []FxUser{
			{AccountID: "u-ann", DisplayName: "Ann Archer"},
			{AccountID: "u-bob", DisplayName: "Bob Brown"},
			{AccountID: "u-cy", DisplayName: "Cy Cole"},
			{AccountID: "u-bot", DisplayName: "Build Bot", AccountType: "app"},
		},
		Issues: []FxIssue{
			{Key: "MOB-1", Type: "Story", Summary: "Login page", Assignee: "u-ann", Board: 1, InSprint: true,
				History: []FxHistory{{Status: "To Do", Days: 3}, {Status: "In Progress", Days: 2}},
				Commits: []int{-1, 0, 0}, PRs: []FxPR{{ID: 12, Day: -1, Status: "MERGED"}}, Comments: []int{-3, -1, 0}},
			{Key: "MOB-2", Type: "Bug", Summary: "Crash on save", Board: 1, InSprint: true,
				History: []FxHistory{{Status: "To Do", Days: 1}}},
			{Key: "MOB-3", Type: "Task", Summary: "Write docs", Assignee: "u-bob", Board: 1, InSprint: true,
				History:  []FxHistory{{Status: "To Do", Days: 4}, {Status: "In Progress", Days: 3}, {Status: "Done", Days: 1}},
				Comments: []int{-5}},
			{Key: "MOB-4", Type: "Story", Summary: "Not in the sprint", Assignee: "u-cy", Board: 1,
				History: []FxHistory{{Status: "To Do", Days: 5}}},
			{Key: "MOB-5", Type: "Bug", Summary: "Flagged one", Assignee: "u-ann", Board: 1, InSprint: true,
				FlaggedDays: [][2]int{{-1, 0}},
				History:     []FxHistory{{Status: "In Progress", Days: 2}}},
			{Key: "MOB-10", Type: "Task", Summary: "Kanban task", Board: 2,
				History: []FxHistory{{Status: "To Do", Days: 2}}},
			{Key: "MOB-11", Type: "Sub-task", Summary: "Kanban subtask", Assignee: "u-bob", Board: 2,
				History: []FxHistory{{Status: "In Progress", Days: 1}}},
			{Key: "MOB-12", Type: "Epic", Summary: "Kanban epic", Board: 2,
				History: []FxHistory{{Status: "To Do", Days: 1}}},
			{Key: "PLAT-1", Type: "Task", Summary: "Ops thing", Board: 3,
				History: []FxHistory{{Status: "To Do", Days: 1}}},
		},
		Backlog: []FxBacklog{
			{Key: "MOB-6", Type: "Story", Summary: "Backlog one", Board: 1},
			{Key: "MOB-7", Type: "Task", Summary: "Backlog two", Board: 1},
		},
	}
}

// newTiny starts a server over tinyFixture with Now/Loc fixed (opts' other fields are kept).
func newTiny(t *testing.T, opts Options) *Server {
	t.Helper()
	opts.Now = testNow
	opts.Loc = nyc
	return NewServer(t, tinyFixture(), opts)
}

func basicAuth(token string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("dev@example.com:"+token))
}

var httpc = &http.Client{
	Timeout:   10 * time.Second,
	Transport: &http.Transport{DisableKeepAlives: true},
}

// rawCall performs one request; auth == "" sends no Authorization header.
func rawCall(t *testing.T, s *Server, method, path string, body any, auth string) (int, http.Header, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.URL+path, rd)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := httpc.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s %s: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header, b
}

// call is an authorised request whose JSON body (if any) is decoded into a generic value.
func call(t *testing.T, s *Server, method, path string, body any) (int, any) {
	t.Helper()
	code, _, b := rawCall(t, s, method, path, body, basicAuth(Token))
	var v any
	if len(bytes.TrimSpace(b)) > 0 {
		if err := json.Unmarshal(b, &v); err != nil {
			v = nil
		}
	}
	return code, v
}

func get(t *testing.T, s *Server, path string) (int, any) {
	t.Helper()
	return call(t, s, http.MethodGet, path, nil)
}

func post(t *testing.T, s *Server, path string, body any) (int, any) {
	t.Helper()
	return call(t, s, http.MethodPost, path, body)
}

func mustOK(t *testing.T, code int, v any, what string) any {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200 (body %v)", what, code, v)
	}
	return v
}

// jget walks maps by string key and slices by int index; missing → nil.
func jget(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[k]
		case int:
			a, ok := v.([]any)
			if !ok || k < 0 || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func jarr(v any) []any { a, _ := v.([]any); return a }

// jstr renders a JSON scalar as a string (numbers without exponent), nil → "".
func jstr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		return fmt.Sprint(x)
	}
}

func jnum(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func hasKey(v any, k string) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m[k]
	return ok
}

// parseJiraTime accepts ISO-8601 variants or epoch milliseconds (number or numeric string).
func parseJiraTime(t *testing.T, v any) time.Time {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return time.UnixMilli(int64(x))
	case string:
		if ms, err := strconv.ParseInt(x, 10, 64); err == nil {
			return time.UnixMilli(ms)
		}
		for _, layout := range []string{
			"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano, time.RFC3339,
			"2006-01-02T15:04:05.000Z0700", "2006-01-02T15:04:05Z0700",
		} {
			if tm, err := time.Parse(layout, x); err == nil {
				return tm
			}
		}
		t.Fatalf("unparsable timestamp %q", x)
	}
	t.Fatalf("not a timestamp: %#v", v)
	return time.Time{}
}

func withDS(s *Server, fn func(ds *Dataset)) { s.Mutate(fn) }

func issueByKey(ds *Dataset, key string) *DSIssue {
	for _, is := range ds.Issues {
		if is.Key == key {
			return is
		}
	}
	return nil
}

func statusIDByName(ds *Dataset, name string) string {
	for _, st := range ds.Statuses {
		if st.Name == name {
			return st.ID
		}
	}
	return ""
}

func issueID(t *testing.T, s *Server, key string) string {
	t.Helper()
	var id string
	withDS(s, func(ds *Dataset) {
		if is := issueByKey(ds, key); is != nil {
			id = is.ID
		}
	})
	if id == "" {
		t.Fatalf("issue %s not in dataset", key)
	}
	return id
}

// waitLog returns the request log once it holds at least n entries (bounded busy-wait, no sleeping).
func waitLog(t *testing.T, s *Server, n int) []Request {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		reqs := s.Requests()
		if len(reqs) >= n {
			return reqs
		}
		if time.Now().After(deadline) {
			t.Fatalf("request log has %d entries, want at least %d: %+v", len(reqs), n, reqs)
		}
		runtime.Gosched()
	}
}

func sortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func keysOf(v any) []string {
	var out []string
	for _, is := range jarr(v) {
		out = append(out, jstr(jget(is, "key")))
	}
	return out
}

// ── lifecycle, auth, request log ────────────────────────────────────────────────────────────────

func TestNewServer_ServesOnLoopback(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	if !strings.HasPrefix(s.URL, "http://127.0.0.1:") {
		t.Fatalf("URL = %q, want http://127.0.0.1:<port>", s.URL)
	}
	code, _ := get(t, s, "/rest/api/3/myself")
	mustOK(t, code, nil, "myself")
}

func TestStart_WithoutTB_ServesThenCloseStops(t *testing.T) {
	defer guard(t)
	fx := tinyFixture()
	s, err := Start(fx, Options{Now: testNow, Loc: nyc})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	code, _ := get(t, s, "/rest/api/3/myself")
	mustOK(t, code, nil, "myself")
	s.Close()
	req, _ := http.NewRequest(http.MethodGet, s.URL+"/rest/api/3/myself", nil)
	req.Header.Set("Authorization", basicAuth(Token))
	if resp, err := httpc.Do(req); err == nil {
		resp.Body.Close()
		t.Fatalf("request after Close succeeded with %d, want a connection error", resp.StatusCode)
	}
}

func TestAuth_BasicWithTokenAccepted_AnyEmail(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	for _, email := range []string{"dev@example.com", "someone.else@corp.test", ""} {
		auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(email+":"+Token))
		if code, _, _ := rawCall(t, s, http.MethodGet, "/rest/api/3/myself", nil, auth); code != http.StatusOK {
			t.Errorf("email %q with the right token: status %d, want 200", email, code)
		}
	}
}

func TestAuth_WrongMissingOrMalformedCredentials401(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	cases := []struct{ name, auth string }{
		{"wrong token", basicAuth("not-the-token")},
		{"no header", ""},
		{"bearer scheme", "Bearer " + Token},
		{"garbage basic", "Basic !!!not-base64!!!"},
		{"token only no colon", "Basic " + base64.StdEncoding.EncodeToString([]byte(Token))},
	}
	for _, c := range cases {
		sub(t, c.name, func(t *testing.T) {
			if code, _, _ := rawCall(t, s, http.MethodGet, "/rest/api/3/myself", nil, c.auth); code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", code)
			}
		})
	}
}

func TestAuth_EveryEndpointRequiresIt(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-1")
	cases := []struct{ method, path string }{
		{"GET", "/rest/api/3/myself"},
		{"GET", "/rest/api/3/field"},
		{"GET", "/rest/api/3/project/search?query="},
		{"GET", "/rest/agile/1.0/board?projectKeyOrId=MOB"},
		{"GET", "/rest/agile/1.0/board/1/configuration"},
		{"GET", "/rest/agile/1.0/board/1/sprint?state=active"},
		{"GET", "/rest/api/3/status"},
		{"POST", "/rest/api/3/changelog/bulkfetch"},
		{"GET", "/rest/api/3/issue/" + id + "/comment"},
		{"GET", "/rest/dev-status/latest/issue/summary?issueId=" + id},
		{"GET", "/rest/dev-status/latest/issue/detail?issueId=" + id + "&applicationType=GitHub&dataType=repository"},
		{"GET", "/rest/api/3/user/assignable/search?project=MOB&query="},
	}
	for _, c := range cases {
		sub(t, c.method+" "+c.path, func(t *testing.T) {
			var body any
			if c.method == "POST" {
				body = map[string]any{"issueIdsOrKeys": []string{id}}
			}
			if code, _, _ := rawCall(t, s, c.method, c.path, body, ""); code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated status = %d, want 401", code)
			}
		})
	}
}

func TestSetUnauthorized_FlipsEveryRequestTo401AndBack(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	if code, _ := get(t, s, "/rest/api/3/myself"); code != 200 {
		t.Fatalf("before: %d", code)
	}
	s.SetUnauthorized(true)
	for _, p := range []string{"/rest/api/3/myself", "/rest/api/3/status", "/rest/agile/1.0/board/1/configuration"} {
		if code, _ := get(t, s, p); code != http.StatusUnauthorized {
			t.Errorf("GET %s while unauthorized = %d, want 401 (valid credentials must not help)", p, code)
		}
	}
	s.SetUnauthorized(false)
	if code, _ := get(t, s, "/rest/api/3/myself"); code != 200 {
		t.Fatalf("after reset: %d, want 200", code)
	}
}

func TestRequestLog_RecordsMethodPathQueryStatusInOrder(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	s.ResetRequests()
	get(t, s, "/rest/api/3/myself")
	get(t, s, "/rest/api/3/project/search?query=mob&maxResults=5")
	rawCall(t, s, http.MethodGet, "/rest/api/3/status", nil, basicAuth("wrong"))
	get(t, s, "/rest/agile/1.0/board/999/configuration")

	reqs := waitLog(t, s, 4)
	if len(reqs) != 4 {
		t.Fatalf("log has %d entries, want 4: %+v", len(reqs), reqs)
	}
	want := []struct {
		method, path string
		status       int
	}{
		{"GET", "/rest/api/3/myself", 200},
		{"GET", "/rest/api/3/project/search", 200},
		{"GET", "/rest/api/3/status", 401},
		{"GET", "/rest/agile/1.0/board/999/configuration", 404},
	}
	for i, w := range want {
		if reqs[i].Method != w.method || reqs[i].Path != w.path || reqs[i].Status != w.status {
			t.Errorf("entry %d = %s %s -> %d, want %s %s -> %d", i, reqs[i].Method, reqs[i].Path, reqs[i].Status, w.method, w.path, w.status)
		}
	}
	q, err := url.ParseQuery(reqs[1].Query)
	if err != nil || q.Get("query") != "mob" || q.Get("maxResults") != "5" {
		t.Errorf("entry 1 Query = %q, want the raw query string with query=mob&maxResults=5", reqs[1].Query)
	}
}

func TestRequestLog_ResetAndCopySemantics(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	get(t, s, "/rest/api/3/myself")
	reqs := waitLog(t, s, 1)
	reqs[0].Path = "/tampered"
	if again := s.Requests(); len(again) == 0 || again[0].Path == "/tampered" {
		t.Fatalf("Requests() must return a copy; modifying it changed the log: %+v", again)
	}
	s.ResetRequests()
	if n := len(s.Requests()); n != 0 {
		t.Fatalf("after ResetRequests the log has %d entries", n)
	}
	get(t, s, "/rest/api/3/status")
	reqs = waitLog(t, s, 1)
	if len(reqs) != 1 || reqs[0].Path != "/rest/api/3/status" {
		t.Fatalf("log after reset = %+v, want just the status request", reqs)
	}
}

func TestDataset_ReturnsLivePointer(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	ds := s.Dataset()
	if ds == nil || len(ds.Issues) != 11 {
		t.Fatalf("Dataset() = %+v, want 11 issues (9 board + 2 backlog)", ds)
	}
	s.Mutate(func(d *Dataset) {
		if d != ds {
			t.Errorf("Mutate must hand out the same *Dataset that Dataset() returns")
		}
	})
}

// ── /myself, /field ─────────────────────────────────────────────────────────────────────────────

func TestMyself_ReturnsAccountFields(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/api/3/myself")
	mustOK(t, code, v, "myself")
	if jstr(jget(v, "accountId")) == "" || jstr(jget(v, "displayName")) == "" {
		t.Errorf("myself = %v, want accountId and displayName", v)
	}
	if jget(v, "active") != true {
		t.Errorf("myself.active = %v, want true", jget(v, "active"))
	}
}

func TestField_ReportsSprintRankAndFlaggedFields(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/api/3/field")
	mustOK(t, code, v, "field")
	byID := map[string]any{}
	for _, f := range jarr(v) {
		byID[jstr(jget(f, "id"))] = f
	}
	if f := byID[SprintFieldID]; f == nil || jstr(jget(f, "schema", "custom")) != "com.pyxis.greenhopper.jira:gh-sprint" {
		t.Errorf("sprint field %s = %v, want schema.custom com.pyxis.greenhopper.jira:gh-sprint", SprintFieldID, f)
	}
	if f := byID[RankFieldID]; f == nil || jstr(jget(f, "schema", "custom")) != "com.pyxis.greenhopper.jira:gh-lexo-rank" {
		t.Errorf("rank field %s = %v, want schema.custom com.pyxis.greenhopper.jira:gh-lexo-rank", RankFieldID, f)
	}
	if f := byID[FlaggedFieldID]; f == nil || jstr(jget(f, "name")) != "Flagged" {
		t.Errorf("flagged field %s = %v, want name \"Flagged\"", FlaggedFieldID, f)
	}
}

// ── project and board search ────────────────────────────────────────────────────────────────────

func TestProjectSearch_FiltersByKeyOrNameCaseInsensitive(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	cases := []struct {
		query string
		want  []string
	}{
		{"", []string{"MOB", "PLAT", "WEB"}},
		{"mob", []string{"MOB"}},
		{"PLAT", []string{"PLAT"}}, // the key and the name "Platform"
		{"web & checkout", []string{"WEB"}},
		{"zzz", nil},
	}
	for _, c := range cases {
		sub(t, "query="+c.query, func(t *testing.T) {
			code, v := get(t, s, "/rest/api/3/project/search?maxResults=20&query="+url.QueryEscape(c.query))
			mustOK(t, code, v, "project/search")
			var keys []string
			for _, p := range jarr(jget(v, "values")) {
				keys = append(keys, jstr(jget(p, "key")))
				if jstr(jget(p, "id")) == "" || jstr(jget(p, "name")) == "" {
					t.Errorf("project %v lacks id or name", p)
				}
			}
			if got := sortedStrings(keys); !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Fatalf("keys = %v, want %v", got, c.want)
			}
		})
	}
}

func TestProjectSearch_HonoursMaxResults(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/api/3/project/search?maxResults=2")
	mustOK(t, code, v, "project/search")
	if n := len(jarr(jget(v, "values"))); n != 2 {
		t.Fatalf("values = %d, want 2", n)
	}
}

func TestBoardSearch_FiltersByProjectAndName(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	cases := []struct {
		query string
		want  []string // board names, sorted
	}{
		{"projectKeyOrId=MOB", []string{"MOB Kanban", "MOB board"}},
		{"projectKeyOrId=PLAT", []string{"PLAT board"}},
		{"projectKeyOrId=10001", []string{"MOB Kanban", "MOB board"}}, // project id works too
		{"projectKeyOrId=MOB&name=kanban", []string{"MOB Kanban"}},
		{"projectKeyOrId=MOB&name=BOARD", []string{"MOB board"}},
		{"projectKeyOrId=MOB&name=nothing", nil},
		{"projectKeyOrId=WEB", nil},
	}
	for _, c := range cases {
		sub(t, c.query, func(t *testing.T) {
			code, v := get(t, s, "/rest/agile/1.0/board?maxResults=50&orderBy=name&"+c.query)
			mustOK(t, code, v, "board")
			var names []string
			for _, b := range jarr(jget(v, "values")) {
				names = append(names, jstr(jget(b, "name")))
				if jstr(jget(b, "id")) == "" || jstr(jget(b, "type")) == "" {
					t.Errorf("board %v lacks id or type", b)
				}
			}
			if got := sortedStrings(names); !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Fatalf("boards = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBoardSearch_ReportsTypeAndId(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/agile/1.0/board?projectKeyOrId=MOB&name=kanban")
	mustOK(t, code, v, "board")
	b := jget(v, "values", 0)
	if jstr(jget(b, "id")) != "2" || jstr(jget(b, "type")) != "kanban" || jstr(jget(b, "name")) != "MOB Kanban" {
		t.Fatalf("board = %v, want id 2 / kanban / MOB Kanban", b)
	}
}

// ── board configuration, sprint, statuses ───────────────────────────────────────────────────────

func statusNamesByID(t *testing.T, s *Server) map[string]string {
	t.Helper()
	code, v := get(t, s, "/rest/api/3/status")
	mustOK(t, code, v, "status")
	m := map[string]string{}
	for _, st := range jarr(v) {
		m[jstr(jget(st, "id"))] = jstr(jget(st, "name"))
	}
	return m
}

func TestBoardConfiguration_ScrumShape(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/agile/1.0/board/1/configuration")
	mustOK(t, code, v, "configuration")
	if jstr(jget(v, "type")) != "scrum" {
		t.Errorf("type = %v, want scrum", jget(v, "type"))
	}
	if jstr(jget(v, "filter", "id")) != "9001" {
		t.Errorf("filter.id = %v, want 9001", jget(v, "filter", "id"))
	}
	if q := jstr(jget(v, "subQuery", "query")); q != "" {
		t.Errorf("scrum board subQuery.query = %q, want empty/absent", q)
	}
	if got := strings.TrimPrefix(jstr(jget(v, "ranking", "rankCustomFieldId")), "customfield_"); got != strings.TrimPrefix(RankFieldID, "customfield_") {
		t.Errorf("ranking.rankCustomFieldId = %v, want the rank field %s (number or customfield_ form)", jget(v, "ranking", "rankCustomFieldId"), RankFieldID)
	}
	names := statusNamesByID(t, s)
	cols := jarr(jget(v, "columnConfig", "columns"))
	if len(cols) != 3 {
		t.Fatalf("columns = %d, want 3: %v", len(cols), cols)
	}
	wantCols := []struct{ name, status string }{{"Selected for Development", "To Do"}, {"In Progress", "In Progress"}, {"Done", "Done"}}
	for i, w := range wantCols {
		if got := jstr(jget(cols[i], "name")); got != w.name {
			t.Errorf("column %d name = %q, want %q", i, got, w.name)
		}
		sts := jarr(jget(cols[i], "statuses"))
		if len(sts) != 1 {
			t.Errorf("column %d has %d statuses, want 1", i, len(sts))
			continue
		}
		if got := names[jstr(jget(sts[0], "id"))]; got != w.status {
			t.Errorf("column %d status id %v resolves to %q, want %q (ids must match GET /status)", i, jget(sts[0], "id"), got, w.status)
		}
	}
}

func TestBoardConfiguration_KanbanCarriesSubQuery(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/agile/1.0/board/2/configuration")
	mustOK(t, code, v, "configuration")
	if jstr(jget(v, "type")) != "kanban" || jstr(jget(v, "filter", "id")) != "9002" {
		t.Errorf("config = %v, want kanban with filter 9002", v)
	}
	if got := jstr(jget(v, "subQuery", "query")); got != "resolution = Unresolved" {
		t.Errorf("subQuery.query = %q, want %q", got, "resolution = Unresolved")
	}
}

func TestBoardConfiguration_UnknownBoard404(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	if code, _ := get(t, s, "/rest/agile/1.0/board/999/configuration"); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestSprint_ActiveForScrumBoard(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/agile/1.0/board/1/sprint?state=active&maxResults=50")
	mustOK(t, code, v, "sprint")
	vals := jarr(jget(v, "values"))
	if len(vals) != 1 {
		t.Fatalf("sprints = %d, want 1: %v", len(vals), v)
	}
	sp := vals[0]
	if jstr(jget(sp, "id")) != "42" || jstr(jget(sp, "name")) != "Sprint 42" || jstr(jget(sp, "state")) != "active" {
		t.Errorf("sprint = %v, want id 42 / Sprint 42 / active", sp)
	}
	var dsp DSSprint
	withDS(s, func(ds *Dataset) { dsp = ds.Boards[0].Sprints[0] })
	if got := parseJiraTime(t, jget(sp, "startDate")); !got.Equal(dsp.Start) {
		t.Errorf("startDate = %v, want %v", got, dsp.Start)
	}
	if got := parseJiraTime(t, jget(sp, "endDate")); !got.Equal(dsp.End) {
		t.Errorf("endDate = %v, want %v", got, dsp.End)
	}
}

func TestSprint_ScrumBoardWithoutSprintIsEmpty(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/agile/1.0/board/3/sprint?state=active")
	mustOK(t, code, v, "sprint")
	if n := len(jarr(jget(v, "values"))); n != 0 {
		t.Fatalf("values = %d, want 0", n)
	}
}

func TestSprint_OnlyActiveStateIsServed(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/agile/1.0/board/1/sprint?state=closed")
	mustOK(t, code, v, "sprint closed")
	if n := len(jarr(jget(v, "values"))); n != 0 {
		t.Fatalf("closed sprints = %d, want 0 (the fake only knows the active one)", n)
	}
}

func TestSprint_KanbanBoardAnswers400(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/agile/1.0/board/2/sprint?state=active")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (board does not support sprints)", code)
	}
	if len(jarr(jget(v, "errorMessages"))) == 0 {
		t.Errorf("400 body = %v, want errorMessages[]", v)
	}
}

func TestSprint_UnknownBoard404(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	if code, _ := get(t, s, "/rest/agile/1.0/board/999/sprint?state=active"); code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestStatus_ListsStatusesWithCategories(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/api/3/status")
	mustOK(t, code, v, "status")
	got := map[string]string{}
	ids := map[string]bool{}
	for _, st := range jarr(v) {
		id := jstr(jget(st, "id"))
		if id == "" || ids[id] {
			t.Errorf("status id %q empty or repeated", id)
		}
		ids[id] = true
		got[jstr(jget(st, "name"))] = jstr(jget(st, "statusCategory", "key"))
	}
	want := map[string]string{"To Do": "new", "In Progress": "indeterminate", "Done": "done"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
}

// ── changelog bulkfetch ─────────────────────────────────────────────────────────────────────────

type fetchedChange struct{ Issue, FieldID, Field, From, To, At string }

// fetchAllChanges follows nextPageToken until absent (bounded) and flattens the items.
func fetchAllChanges(t *testing.T, s *Server, issues []string, fieldIDs []string) (changes []fetchedChange, pages int) {
	t.Helper()
	token := ""
	seenHistory := map[string]bool{}
	for pages < 100 {
		body := map[string]any{"issueIdsOrKeys": issues, "maxResults": 1000}
		if fieldIDs != nil {
			body["fieldIds"] = fieldIDs
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		code, v := post(t, s, "/rest/api/3/changelog/bulkfetch", body)
		mustOK(t, code, v, "changelog/bulkfetch")
		pages++
		for _, ic := range jarr(jget(v, "issueChangeLogs")) {
			issueID := jstr(jget(ic, "issueId"))
			for _, h := range jarr(jget(ic, "changeHistories")) {
				hid := jstr(jget(h, "id"))
				if hid == "" {
					t.Fatalf("history without id: %v", h)
				}
				if k := issueID + "/" + hid; seenHistory[k] {
					t.Fatalf("history %s repeated across or within pages", k)
				} else {
					seenHistory[k] = true
				}
				at := parseJiraTime(t, jget(h, "created")).UTC().Format(time.RFC3339)
				items := jarr(jget(h, "items"))
				if len(items) == 0 {
					t.Errorf("history %s has no items; histories without a requested field must be omitted", hid)
				}
				for _, it := range items {
					changes = append(changes, fetchedChange{issueID, jstr(jget(it, "fieldId")), jstr(jget(it, "field")),
						jstr(jget(it, "from")), jstr(jget(it, "to")), at})
				}
			}
		}
		token = jstr(jget(v, "nextPageToken"))
		if token == "" {
			return changes, pages
		}
	}
	t.Fatalf("bulkfetch did not terminate within 100 pages")
	return nil, pages
}

func expectedChanges(s *Server, filter func(DSChange) bool) []fetchedChange {
	var out []fetchedChange
	withDS(s, func(ds *Dataset) {
		for _, is := range ds.Issues {
			for _, ch := range is.Changelog {
				if filter == nil || filter(ch) {
					out = append(out, fetchedChange{is.ID, ch.FieldID, ch.Field, ch.From, ch.To, ch.At.UTC().Format(time.RFC3339)})
				}
			}
		}
	})
	return out
}

func sortChanges(c []fetchedChange) []fetchedChange {
	out := append([]fetchedChange(nil), c...)
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i]) < fmt.Sprint(out[j]) })
	return out
}

func allIssueIDs(s *Server) []string {
	var ids []string
	withDS(s, func(ds *Dataset) {
		for _, is := range ds.Issues {
			ids = append(ids, is.ID)
		}
	})
	return ids
}

func TestBulkFetch_ReturnsEveryChangeOfTheRequestedIssues(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	got, _ := fetchAllChanges(t, s, allIssueIDs(s), nil)
	want := expectedChanges(s, nil)
	if len(want) < 4 {
		t.Fatalf("dataset has only %d changes; fixture/Materialize broken", len(want))
	}
	if !reflect.DeepEqual(sortChanges(got), sortChanges(want)) {
		t.Fatalf("changes mismatch\n got:  %v\n want: %v", sortChanges(got), sortChanges(want))
	}
}

func TestBulkFetch_FieldIdsFilterRestrictsItems(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	ids := allIssueIDs(s)

	status, _ := fetchAllChanges(t, s, ids, []string{"status"})
	wantStatus := expectedChanges(s, func(c DSChange) bool { return c.FieldID == "status" })
	if len(wantStatus) == 0 || !reflect.DeepEqual(sortChanges(status), sortChanges(wantStatus)) {
		t.Fatalf("fieldIds=[status]\n got:  %v\n want: %v", sortChanges(status), sortChanges(wantStatus))
	}
	for _, c := range status {
		if c.FieldID != "status" {
			t.Errorf("fieldIds=[status] returned item %+v", c)
		}
	}

	both, _ := fetchAllChanges(t, s, ids, []string{"status", FlaggedFieldID})
	wantBoth := expectedChanges(s, func(c DSChange) bool { return c.FieldID == "status" || c.FieldID == FlaggedFieldID })
	if !reflect.DeepEqual(sortChanges(both), sortChanges(wantBoth)) {
		t.Fatalf("fieldIds=[status,flagged]\n got:  %v\n want: %v", sortChanges(both), sortChanges(wantBoth))
	}
	flagged := 0
	for _, c := range both {
		if c.FieldID == FlaggedFieldID {
			flagged++
			if c.Field != "Flagged" {
				t.Errorf("flag item field = %q, want Flagged", c.Field)
			}
		}
	}
	if flagged == 0 {
		t.Errorf("no flag change returned for the flagged issue MOB-5")
	}
}

func TestBulkFetch_AcceptsKeysAndIdsAndReportsNumericIssueId(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id3 := issueID(t, s, "MOB-3")
	got, _ := fetchAllChanges(t, s, []string{"MOB-3"}, []string{"status"})
	if len(got) != 2 {
		t.Fatalf("MOB-3 by key: %d status changes, want 2 (To Do->In Progress->Done): %v", len(got), got)
	}
	for _, c := range got {
		if c.Issue != id3 {
			t.Errorf("issueId = %q, want the numeric id %q (not the key)", c.Issue, id3)
		}
	}
	byID, _ := fetchAllChanges(t, s, []string{id3}, []string{"status"})
	if !reflect.DeepEqual(sortChanges(byID), sortChanges(got)) {
		t.Errorf("by id %v differs from by key %v", byID, got)
	}
	if only, _ := fetchAllChanges(t, s, []string{"MOB-3"}, nil); len(only) < 2 {
		t.Errorf("unfiltered MOB-3 changes = %d", len(only))
	}
	for _, c := range func() []fetchedChange { x, _ := fetchAllChanges(t, s, []string{"MOB-2"}, nil); return x }() {
		if c.Issue != issueID(t, s, "MOB-2") {
			t.Errorf("asked only for MOB-2 but got changes of issue %q", c.Issue)
		}
	}
}

func TestBulkFetch_StatusItemsCarryStatusIds(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	names := statusNamesByID(t, s)
	got, _ := fetchAllChanges(t, s, []string{"MOB-3"}, []string{"status"})
	sort.Slice(got, func(i, j int) bool { return got[i].At < got[j].At })
	if len(got) != 2 {
		t.Fatalf("changes = %v", got)
	}
	steps := [][2]string{{"To Do", "In Progress"}, {"In Progress", "Done"}}
	for i, c := range got {
		if names[c.From] != steps[i][0] || names[c.To] != steps[i][1] {
			t.Errorf("change %d = %s -> %s (ids %s -> %s), want %s -> %s", i, names[c.From], names[c.To], c.From, c.To, steps[i][0], steps[i][1])
		}
		if c.Field != "status" {
			t.Errorf("change %d field = %q, want status", i, c.Field)
		}
	}
}

func TestBulkFetch_PagingWithTokensYieldsEverythingOnce(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{PageSize: 1})
	got, pages := fetchAllChanges(t, s, allIssueIDs(s), nil)
	if pages < 2 {
		t.Fatalf("PageSize=1 produced %d page(s); want several", pages)
	}
	want := expectedChanges(s, nil)
	if !reflect.DeepEqual(sortChanges(got), sortChanges(want)) {
		t.Fatalf("paged union differs\n got:  %v\n want: %v", sortChanges(got), sortChanges(want))
	}
}

func TestBulkFetch_SeesChangesAddedByMutate(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	before, _ := fetchAllChanges(t, s, []string{"MOB-4"}, []string{"status"})
	s.Mutate(func(ds *Dataset) {
		is := issueByKey(ds, "MOB-4")
		from := is.StatusID
		to := statusIDByName(ds, "In Progress")
		is.StatusID = to
		is.Changelog = append(is.Changelog, DSChange{ID: "99001", At: ds.Now.Add(-time.Hour), FieldID: "status", Field: "status", From: from, To: to})
	})
	after, _ := fetchAllChanges(t, s, []string{"MOB-4"}, []string{"status"})
	if len(after) != len(before)+1 {
		t.Fatalf("changes before=%d after=%d, want one more after Mutate", len(before), len(after))
	}
}

// ── comments ────────────────────────────────────────────────────────────────────────────────────

func TestComments_NewestFirstWithTotalAndAuthor(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-1")
	code, v := get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=-created&maxResults=100&startAt=0")
	mustOK(t, code, v, "comments")
	if jnum(jget(v, "total")) != 3 {
		t.Fatalf("total = %v, want 3", jget(v, "total"))
	}
	cs := jarr(jget(v, "comments"))
	if len(cs) != 3 {
		t.Fatalf("comments = %d, want 3", len(cs))
	}
	var prev time.Time
	for i, c := range cs {
		at := parseJiraTime(t, jget(c, "created"))
		if i > 0 && at.After(prev) {
			t.Errorf("comment %d (%v) is newer than comment %d (%v); orderBy=-created means newest first", i, at, i-1, prev)
		}
		prev = at
		if jstr(jget(c, "id")) == "" || jstr(jget(c, "author", "accountId")) == "" {
			t.Errorf("comment %v lacks id or author.accountId", c)
		}
	}
	// MOB-1 comments are on days -3, -1, 0 (11:00 local): newest first.
	assertLocalDayHour(t, "newest comment", parseJiraTime(t, jget(cs[0], "created")), testNow, nyc, 0, 11)
	assertLocalDayHour(t, "oldest comment", parseJiraTime(t, jget(cs[2], "created")), testNow, nyc, -3, 11)
}

func TestComments_PagesWithStartAtAndMaxResults(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-1")
	code, v := get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=-created&maxResults=2&startAt=0")
	mustOK(t, code, v, "page 1")
	first := jarr(jget(v, "comments"))
	if len(first) != 2 || jnum(jget(v, "total")) != 3 {
		t.Fatalf("page 1: %d comments total %v, want 2 of 3", len(first), jget(v, "total"))
	}
	code, v = get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=-created&maxResults=2&startAt=2")
	mustOK(t, code, v, "page 2")
	second := jarr(jget(v, "comments"))
	if len(second) != 1 {
		t.Fatalf("page 2: %d comments, want 1", len(second))
	}
	if jstr(jget(second[0], "id")) == jstr(jget(first[0], "id")) || jstr(jget(second[0], "id")) == jstr(jget(first[1], "id")) {
		t.Errorf("page 2 repeats a comment of page 1")
	}
}

func TestComments_IssueWithoutCommentsAndUnknownIssue(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/api/3/issue/"+issueID(t, s, "MOB-2")+"/comment")
	mustOK(t, code, v, "comments of MOB-2")
	if jnum(jget(v, "total")) != 0 || len(jarr(jget(v, "comments"))) != 0 {
		t.Errorf("MOB-2 comments = %v, want none", v)
	}
	if code, _ := get(t, s, "/rest/api/3/issue/99999999/comment"); code != http.StatusNotFound {
		t.Errorf("unknown issue status = %d, want 404", code)
	}
}

func TestComments_SeesCommentsAddedByMutate(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-2")
	s.Mutate(func(ds *Dataset) {
		is := issueByKey(ds, "MOB-2")
		is.Comments = append(is.Comments, DSComment{ID: "88001", At: ds.Now.Add(-time.Minute), Author: "u-ann"})
	})
	code, v := get(t, s, "/rest/api/3/issue/"+id+"/comment?orderBy=-created")
	mustOK(t, code, v, "comments")
	if jnum(jget(v, "total")) != 1 || jstr(jget(v, "comments", 0, "id")) != "88001" || jstr(jget(v, "comments", 0, "author", "accountId")) != "u-ann" {
		t.Fatalf("comments after Mutate = %v, want the added comment 88001 by u-ann", v)
	}
}

// ── dev-status ──────────────────────────────────────────────────────────────────────────────────

func TestDevStatus_SummaryCountsCommitsAndPRs(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-1") // 3 commits (days -1, 0, 0), 1 merged PR on day -1
	code, v := get(t, s, "/rest/dev-status/latest/issue/summary?issueId="+id)
	mustOK(t, code, v, "dev summary")
	sum := jget(v, "summary")
	if jnum(jget(sum, "repository", "overall", "count")) != 3 {
		t.Errorf("repository.overall.count = %v, want 3", jget(sum, "repository", "overall", "count"))
	}
	if !hasKey(jget(sum, "repository", "byInstanceType"), "GitHub") {
		t.Errorf("repository.byInstanceType = %v, want a GitHub key", jget(sum, "repository", "byInstanceType"))
	}
	if jnum(jget(sum, "pullrequest", "overall", "count")) != 1 {
		t.Errorf("pullrequest.overall.count = %v, want 1", jget(sum, "pullrequest", "overall", "count"))
	}
	if jstr(jget(sum, "pullrequest", "overall", "state")) != "MERGED" || jnum(jget(sum, "pullrequest", "overall", "stateCount")) != 1 {
		t.Errorf("pullrequest.overall state/stateCount = %v/%v, want MERGED/1",
			jget(sum, "pullrequest", "overall", "state"), jget(sum, "pullrequest", "overall", "stateCount"))
	}
	if !hasKey(jget(sum, "pullrequest", "byInstanceType"), "GitHub") {
		t.Errorf("pullrequest.byInstanceType = %v, want a GitHub key", jget(sum, "pullrequest", "byInstanceType"))
	}
	var lastCommit time.Time
	withDS(s, func(ds *Dataset) {
		for _, c := range issueByKey(ds, "MOB-1").Commits {
			if c.At.After(lastCommit) {
				lastCommit = c.At
			}
		}
	})
	if got := parseJiraTime(t, jget(sum, "repository", "overall", "lastUpdated")); !got.Equal(lastCommit) {
		t.Errorf("repository.overall.lastUpdated = %v, want the newest commit %v", got, lastCommit)
	}
}

func TestDevStatus_SummaryForIssueWithoutActivityIsZero(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/dev-status/latest/issue/summary?issueId="+issueID(t, s, "MOB-2"))
	mustOK(t, code, v, "dev summary")
	sum := jget(v, "summary")
	if jnum(jget(sum, "repository", "overall", "count")) != 0 || jnum(jget(sum, "pullrequest", "overall", "count")) != 0 {
		t.Errorf("summary for an issue without activity = %v, want zero counts", sum)
	}
	if m, _ := jget(sum, "repository", "byInstanceType").(map[string]any); len(m) != 0 {
		t.Errorf("repository.byInstanceType = %v for an issue without commits, want an empty object", m)
	}
}

func TestDevStatus_DetailRepositoryListsCommits(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-1")
	code, v := get(t, s, "/rest/dev-status/latest/issue/detail?issueId="+id+"&applicationType=GitHub&dataType=repository")
	mustOK(t, code, v, "dev detail repository")
	var gotIDs, gotAts []string
	for _, d := range jarr(jget(v, "detail")) {
		for _, r := range jarr(jget(d, "repositories")) {
			if jstr(jget(r, "name")) == "" {
				t.Errorf("repository without name: %v", r)
			}
			for _, c := range jarr(jget(r, "commits")) {
				gotIDs = append(gotIDs, jstr(jget(c, "id")))
				gotAts = append(gotAts, parseJiraTime(t, jget(c, "authorTimestamp")).UTC().Format(time.RFC3339))
				if _, ok := jget(c, "merge").(bool); !ok {
					t.Errorf("commit %v lacks boolean merge", c)
				}
			}
		}
	}
	var wantIDs, wantAts []string
	withDS(s, func(ds *Dataset) {
		for _, c := range issueByKey(ds, "MOB-1").Commits {
			wantIDs = append(wantIDs, c.SHA)
			wantAts = append(wantAts, c.At.UTC().Format(time.RFC3339))
		}
	})
	if len(wantIDs) != 3 || !reflect.DeepEqual(sortedStrings(gotIDs), sortedStrings(wantIDs)) || !reflect.DeepEqual(sortedStrings(gotAts), sortedStrings(wantAts)) {
		t.Fatalf("commits ids %v at %v, want ids %v at %v", gotIDs, gotAts, wantIDs, wantAts)
	}
}

func TestDevStatus_DetailPullRequestListsPRs(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-1")
	code, v := get(t, s, "/rest/dev-status/latest/issue/detail?issueId="+id+"&applicationType=GitHub&dataType=pullrequest")
	mustOK(t, code, v, "dev detail pullrequest")
	var prs []any
	for _, d := range jarr(jget(v, "detail")) {
		prs = append(prs, jarr(jget(d, "pullRequests"))...)
	}
	if len(prs) != 1 {
		t.Fatalf("pull requests = %d, want 1: %v", len(prs), v)
	}
	pr := prs[0]
	if !strings.Contains(jstr(jget(pr, "id")), "12") {
		t.Errorf("pr id = %v, want one containing 12", jget(pr, "id"))
	}
	if jstr(jget(pr, "status")) != "MERGED" {
		t.Errorf("pr status = %v, want MERGED", jget(pr, "status"))
	}
	assertLocalDayHour(t, "pr lastUpdate", parseJiraTime(t, jget(pr, "lastUpdate")), testNow, nyc, -1, 11)
	if jstr(jget(pr, "repositoryName")) == "" {
		t.Errorf("pr lacks repositoryName: %v", pr)
	}
}

func TestDevStatus_UnknownApplicationTypeHasNoCommits(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/dev-status/latest/issue/detail?issueId="+issueID(t, s, "MOB-1")+"&applicationType=bitbucket&dataType=repository")
	mustOK(t, code, v, "dev detail bitbucket")
	n := 0
	for _, d := range jarr(jget(v, "detail")) {
		for _, r := range jarr(jget(d, "repositories")) {
			n += len(jarr(jget(r, "commits")))
		}
	}
	if n != 0 {
		t.Fatalf("bitbucket commits = %d, want 0 (the fake only has GitHub data)", n)
	}
}

func TestDevStatus_SeesCommitsAddedByMutate(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	id := issueID(t, s, "MOB-2")
	s.Mutate(func(ds *Dataset) {
		is := issueByKey(ds, "MOB-2")
		is.Commits = append(is.Commits, DSCommit{SHA: "deadbeef", Repo: "svc-web", At: ds.Now.Add(-2 * time.Hour)})
	})
	code, v := get(t, s, "/rest/dev-status/latest/issue/summary?issueId="+id)
	mustOK(t, code, v, "dev summary")
	if jnum(jget(v, "summary", "repository", "overall", "count")) != 1 {
		t.Fatalf("summary after Mutate = %v, want repository count 1", v)
	}
	code, v = get(t, s, "/rest/dev-status/latest/issue/detail?issueId="+id+"&applicationType=GitHub&dataType=repository")
	mustOK(t, code, v, "dev detail")
	if jstr(jget(v, "detail", 0, "repositories", 0, "commits", 0, "id")) != "deadbeef" {
		t.Fatalf("detail after Mutate = %v, want the commit deadbeef", v)
	}
}

// ── assignable users ────────────────────────────────────────────────────────────────────────────

func TestAssignableSearch_FiltersByQueryAndShapesUsers(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	cases := []struct {
		query string
		want  []string
	}{
		{"", []string{"u-ann", "u-bob", "u-bot", "u-cy"}},
		{"an", []string{"u-ann"}},
		{"BO", []string{"u-bob", "u-bot"}}, // "Bob Brown", "Build Bot"
		{"zzz", nil},
	}
	for _, c := range cases {
		sub(t, "query="+c.query, func(t *testing.T) {
			code, v := get(t, s, "/rest/api/3/user/assignable/search?project=MOB&maxResults=20&query="+url.QueryEscape(c.query))
			mustOK(t, code, v, "assignable")
			var ids []string
			for _, u := range jarr(v) {
				ids = append(ids, jstr(jget(u, "accountId")))
				if jstr(jget(u, "displayName")) == "" || jget(u, "active") != true {
					t.Errorf("user %v lacks displayName or active=true", u)
				}
				wantType := "atlassian"
				if jstr(jget(u, "accountId")) == "u-bot" {
					wantType = "app"
				}
				if got := jstr(jget(u, "accountType")); got != wantType {
					t.Errorf("user %v accountType = %q, want %q", u, got, wantType)
				}
			}
			if got := sortedStrings(ids); !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Fatalf("accountIds = %v, want %v", got, c.want)
			}
		})
	}
}

func TestAssignableSearch_HonoursMaxResults(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := get(t, s, "/rest/api/3/user/assignable/search?project=MOB&maxResults=2")
	mustOK(t, code, v, "assignable")
	if n := len(jarr(v)); n != 2 {
		t.Fatalf("users = %d, want 2", n)
	}
}

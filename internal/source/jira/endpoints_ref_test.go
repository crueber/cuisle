package jira

// WP-18 tests for endpoints_ref.go: Test, SearchSpaces, SearchBoards, BoardConfig, ActiveSprints, Reference,
// SearchUsers, ValidateFilter, IssueTypeKind, StatusBlockedHint (jira-sync.md §2.1–§2.7, §2.12, §2.8 last paragraph;
// data-model.md §2.4). All HTTP goes through a recording httptest server (it needs request bodies and exact query
// parameters, which jiratest does not log); canned Jira JSON lives in testdata/wp18_*.json or inline.
// Helpers are prefixed wp18; every test starts with `defer wp18Guard(t)` so a stub panic fails that test only.
//
// SPEC-NOTE: The adapter under test is built with New() (no I/O) and then gets a client whose policy has a very high
// rate and a no-op Sleep, so 5xx backoff and the token bucket cannot slow tests. now is fixed at wp18Now.
// SPEC-NOTE: Unrouted requests make the test fail ("unexpected request"), so extra calls (e.g. a /field call on a fresh
// Meta, a sprint probe for a scrum board) are caught.
// SPEC-NOTE: Errors are returned already mapped (MapError) as *apperr.Error: 401 and 403 on /myself -> auth_invalid,
// 404 on a board path -> not_found, 401 elsewhere -> auth_invalid. The §2.1 variant "200 with
// X-Seraph-LoginReason: AUTHENTICATED_FAILED" is NOT tested: Client.GetJSON cannot surface response headers.
// SPEC-NOTE: Space.Meta["style"] holds the project `style` string ("classic"|"next-gen"), nothing else is asserted.
// SPEC-NOTE: SearchBoards: the first request carries no startAt (or "0"); later pages carry startAt = boards read so
// far (50, 100, 150). Paging stops at 200 boards read even when the server keeps saying isLast=false, stops on
// isLast=true, and never happens when `name` is non-empty. Board.SpaceID is not asserted.
// SPEC-NOTE: A "simple" board is probed with GET /rest/agile/1.0/board/{id}/sprint (any query): 200 -> Kind "scrum",
// 400 or 404 -> Kind "kanban"; RawType keeps "simple". scrum/kanban boards cause no probe request.
// SPEC-NOTE: BoardConfig: Column.Position is 0-based in response order; a column with no statuses has empty StatusIDs.
// Board.Meta is non-nil with "filterId" (any type, compared via fmt.Sprint to "9001"), "subQuery" (string; absent or
// "" when the board has none) and "rankFieldId". Jira sends ranking.rankCustomFieldId as the NUMBER 10019 and the
// adapter must expose the field id string "customfield_10019" (a string that already starts with "customfield_" is
// kept). Board.ID is the id passed in. type "simple" is normalised through the sprint probe like in SearchBoards.
// SPEC-NOTE: ActiveSprints: Sprint.BoardID is the boardID argument; 400/404 -> (len 0, nil error); other statuses are
// mapped errors (401 -> auth_invalid).
// SPEC-NOTE: Reference: GET /rest/api/3/status is requested on every call; GET /rest/api/3/field only when
// Meta.FieldsFetchedAt is zero or older than 24 h at wp18Now (tests use 23 h and 25 h, never the boundary). After a
// refetch Meta() has fieldsFetchedAt == wp18Now (RFC 3339, UTC); without one it keeps the previous value. Flagged:
// name == "Flagged" wins; otherwise a field whose clauseNames contains "Flagged"; none -> Meta() has no
// flaggedFieldId (the other ids are still set and no error is returned). Reference returns no issue types before any
// Hydrate. Status order is the response order.
// SPEC-NOTE: SearchUsers: params project=<spaceKey>, query=<text>, maxResults=20. Only active accounts with
// accountType "atlassian" survive; result Person{AccountID, DisplayName, Active: true, Kind: "user"}.
// SPEC-NOTE: ValidateFilter: POST /rest/api/3/jql/parse?validation=strict body {"queries":[expr]}; ok == (no errors in
// queries[0].errors) and errs are those strings verbatim. An ORDER BY (case-insensitive, any whitespace) returns
// ok=false, a non-empty message list and a nil error without any request.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

var wp18Now = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// wp18Guard turns a stub panic into a failure of the current test (use: defer wp18Guard(t)).
func wp18Guard(t *testing.T) {
	if r := recover(); r != nil {
		t.Fatalf("panicked (not implemented?): %v", r)
	}
}

type wp18Req struct {
	Method, Path string
	Query        url.Values
	Body         string
}

func (r wp18Req) json(t *testing.T) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Errorf("request body is not a JSON object: %v (%q)", err, r.Body)
	}
	return m
}

type wp18Resp struct {
	Status  int // 0 = 200
	Body    string
	Header  map[string]string
	NonJSON bool
}

type wp18Route func(r wp18Req) wp18Resp

type wp18Srv struct {
	*httptest.Server
	mu     sync.Mutex
	reqs   []wp18Req
	routes map[string]wp18Route // "METHOD /path"
}

func newWP18Server(t *testing.T, routes map[string]wp18Route) *wp18Srv {
	t.Helper()
	s := &wp18Srv{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req := wp18Req{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: string(b)}
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
		for k, v := range resp.Header {
			w.Header().Set(k, v)
		}
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

func (s *wp18Srv) all() []wp18Req {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]wp18Req(nil), s.reqs...)
}

func (s *wp18Srv) to(method, path string) []wp18Req {
	var out []wp18Req
	for _, r := range s.all() {
		if r.Method == method && r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func wp18Ok(body string) wp18Route { return func(wp18Req) wp18Resp { return wp18Resp{Body: body} } }
func wp18Fail(status int, body string) wp18Route {
	return func(wp18Req) wp18Resp { return wp18Resp{Status: status, Body: body} }
}

func wp18Testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newWP18Adapter builds an Adapter against base with a fast policy and the fixed clock. meta is source.Options.Meta.
func newWP18Adapter(t *testing.T, base string, meta map[string]any) *Adapter {
	t.Helper()
	src, err := New(model.Connection{ID: 1, Source: Kind, BaseURL: base, Account: "me@acme.example"}, "tok-123",
		source.Options{HTTP: &http.Client{}, Now: func() time.Time { return wp18Now }, Meta: meta})
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
	a.c = NewClient(base, "me@acme.example", "tok-123", ClientOptions{HTTP: &http.Client{}, Policy: pol, Now: func() time.Time { return wp18Now }})
	return a
}

func wp18WantQuery(t *testing.T, got url.Values, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("query params = %v, want exactly %v", got, want)
	}
	for k, v := range want {
		if g := got.Get(k); g != v {
			t.Errorf("query %q = %q, want %q (all: %v)", k, g, v, got)
		}
	}
}

func wp18MetaString(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func wp18AssertCode(t *testing.T, err error, want apperr.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want code %s", want)
	}
	if got := apperr.CodeOf(err); got != want {
		t.Fatalf("code = %s (%v), want %s", got, err, want)
	}
}

// ── Test (GET /myself) ───────────────────────────────────────────────────────

func TestEndpoints_Test_ReadsAccountIdAndDisplayName(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/api/3/myself": wp18Ok(`{"accountId":"5b10ac8d82e05b22cc7d4ef5","displayName":"Iris Calloway","emailAddress":"iris@acme.example","active":true,"accountType":"atlassian"}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	acc, err := a.Test(context.Background())
	if err != nil {
		t.Fatalf("Test: %v", err)
	}
	if acc != (model.Account{ID: "5b10ac8d82e05b22cc7d4ef5", DisplayName: "Iris Calloway"}) {
		t.Fatalf("account = %+v", acc)
	}
	if n := len(srv.all()); n != 1 {
		t.Fatalf("requests = %d, want 1", n)
	}
}

func TestEndpoints_Test_RejectedCredentialsAreAuthInvalid(t *testing.T) {
	defer wp18Guard(t)
	for _, tc := range []struct {
		name   string
		status int
		header map[string]string
	}{
		{"401", 401, nil},
		{"403 on myself", 403, nil},
		{"403 AUTHENTICATED_FAILED", 403, map[string]string{"X-Seraph-LoginReason": "AUTHENTICATED_FAILED"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer wp18Guard(t)
			srv := newWP18Server(t, map[string]wp18Route{
				"GET /rest/api/3/myself": func(wp18Req) wp18Resp {
					return wp18Resp{Status: tc.status, Body: `{"message":"Client must be authenticated to access this resource."}`, Header: tc.header}
				},
			})
			a := newWP18Adapter(t, srv.URL, nil)
			_, err := a.Test(context.Background())
			wp18AssertCode(t, err, apperr.AuthInvalid)
			if strings.Contains(err.Error(), "tok-123") {
				t.Fatalf("error leaks the token: %v", err)
			}
		})
	}
}

// ── SearchSpaces ─────────────────────────────────────────────────────────────

func TestEndpoints_SearchSpaces_SendsDocumentedParams(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/api/3/project/search": wp18Ok(`{"values":[],"isLast":true}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	if _, err := a.SearchSpaces(context.Background(), "serv"); err != nil {
		t.Fatalf("SearchSpaces: %v", err)
	}
	reqs := srv.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	wp18WantQuery(t, reqs[0].Query, map[string]string{
		"query": "serv", "maxResults": "20", "orderBy": "name", "typeKey": "software", "status": "live",
	})
}

func TestEndpoints_SearchSpaces_EmptyQueryKeepsTheOtherParams(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/api/3/project/search": wp18Ok(`{"values":[],"isLast":true}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	if _, err := a.SearchSpaces(context.Background(), ""); err != nil {
		t.Fatalf("SearchSpaces: %v", err)
	}
	q := srv.all()[0].Query
	if q.Get("query") != "" || q.Get("maxResults") != "20" || q.Get("orderBy") != "name" || q.Get("typeKey") != "software" || q.Get("status") != "live" {
		t.Fatalf("query = %v", q)
	}
}

func TestEndpoints_SearchSpaces_MapsProjectsWithStyleMeta(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/api/3/project/search": wp18Ok(`{"values":[
			{"id":"10001","key":"MOB","name":"Saffron Mobile","style":"classic","projectTypeKey":"software","self":"x","avatarUrls":{}},
			{"id":"10002","key":"WEB","name":"Web Team","style":"next-gen","projectTypeKey":"software"}],"isLast":true}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	got, err := a.SearchSpaces(context.Background(), "")
	if err != nil {
		t.Fatalf("SearchSpaces: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("spaces = %+v", got)
	}
	want := []struct{ id, key, name, style string }{
		{"10001", "MOB", "Saffron Mobile", "classic"},
		{"10002", "WEB", "Web Team", "next-gen"},
	}
	for i, w := range want {
		g := got[i]
		if g.ID != w.id || g.Key != w.key || g.Name != w.name {
			t.Errorf("space[%d] = %+v, want %+v", i, g, w)
		}
		if s, _ := g.Meta["style"].(string); s != w.style {
			t.Errorf("space[%d].Meta[style] = %v, want %q", i, g.Meta["style"], w.style)
		}
	}
}

// ── SearchBoards ─────────────────────────────────────────────────────────────

func wp18BoardsPage(from, n int, last bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `{"maxResults":50,"startAt":%d,"isLast":%t,"values":[`, from, last)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `{"id":%d,"name":"Board %03d","type":"scrum"}`, 1000+from+i, from+i)
	}
	sb.WriteString("]}")
	return sb.String()
}

func TestEndpoints_SearchBoards_WithName_SendsParamsAndDoesNotPage(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board": wp18Ok(wp18BoardsPage(0, 3, false)), // claims more pages
	})
	a := newWP18Adapter(t, srv.URL, nil)
	got, err := a.SearchBoards(context.Background(), "MOB", "plat")
	if err != nil {
		t.Fatalf("SearchBoards: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("boards = %d, want 3", len(got))
	}
	reqs := srv.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1 (name given: no paging)", len(reqs))
	}
	wp18WantQuery(t, reqs[0].Query, map[string]string{
		"projectKeyOrId": "MOB", "name": "plat", "maxResults": "50", "orderBy": "name",
	})
}

func TestEndpoints_SearchBoards_EmptyName_OmitsNameAndStopsOnIsLast(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board": func(r wp18Req) wp18Resp {
			if r.Query.Get("startAt") == "50" {
				return wp18Resp{Body: wp18BoardsPage(50, 10, true)}
			}
			return wp18Resp{Body: wp18BoardsPage(0, 50, false)}
		},
	})
	a := newWP18Adapter(t, srv.URL, nil)
	got, err := a.SearchBoards(context.Background(), "MOB", "")
	if err != nil {
		t.Fatalf("SearchBoards: %v", err)
	}
	if len(got) != 60 {
		t.Fatalf("boards = %d, want 60", len(got))
	}
	reqs := srv.all()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for i, r := range reqs {
		if _, has := r.Query["name"]; has {
			t.Errorf("request %d carries name=%q, want it omitted", i, r.Query.Get("name"))
		}
		if r.Query.Get("projectKeyOrId") != "MOB" || r.Query.Get("maxResults") != "50" || r.Query.Get("orderBy") != "name" {
			t.Errorf("request %d query = %v", i, r.Query)
		}
	}
	if s := reqs[0].Query.Get("startAt"); s != "" && s != "0" {
		t.Errorf("first startAt = %q, want absent or 0", s)
	}
	if s := reqs[1].Query.Get("startAt"); s != "50" {
		t.Errorf("second startAt = %q, want 50", s)
	}
	if got[0].ID != "1000" || got[0].Name != "Board 000" || got[0].Kind != "scrum" || got[0].RawType != "scrum" {
		t.Errorf("first board = %+v", got[0])
	}
}

func TestEndpoints_SearchBoards_EmptyName_StopsAt200BoardsEvenWhenNotLast(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board": func(r wp18Req) wp18Resp {
			from := 0
			fmt.Sscanf(r.Query.Get("startAt"), "%d", &from)
			return wp18Resp{Body: wp18BoardsPage(from, 50, false)} // always more
		},
	})
	a := newWP18Adapter(t, srv.URL, nil)
	got, err := a.SearchBoards(context.Background(), "MOB", "")
	if err != nil {
		t.Fatalf("SearchBoards: %v", err)
	}
	if len(got) != 200 {
		t.Fatalf("boards = %d, want 200", len(got))
	}
	reqs := srv.all()
	if len(reqs) != 4 {
		t.Fatalf("requests = %d, want 4 (50 x 4 = 200 read, then stop)", len(reqs))
	}
	for i, wantStart := range []string{"", "50", "100", "150"} {
		got := reqs[i].Query.Get("startAt")
		if i == 0 && got == "0" {
			got = ""
		}
		if got != wantStart {
			t.Errorf("request %d startAt = %q, want %q", i, got, wantStart)
		}
	}
}

func TestEndpoints_SearchBoards_SimpleBoardIsNormalisedBySprintProbe(t *testing.T) {
	defer wp18Guard(t)
	for _, tc := range []struct {
		name   string
		probe  int
		want   string
		answer string
	}{
		{"sprint endpoint answers 200 -> scrum", 200, "scrum", `{"values":[]}`},
		{"sprint endpoint answers 400 -> kanban", 400, "kanban", `{"errorMessages":["The board does not support sprints"]}`},
		{"sprint endpoint answers 404 -> kanban", 404, "kanban", `{"errorMessages":["Board does not exist or you do not have permission to see it."]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer wp18Guard(t)
			srv := newWP18Server(t, map[string]wp18Route{
				"GET /rest/agile/1.0/board":          wp18Ok(`{"isLast":true,"values":[{"id":7,"name":"Team board","type":"simple"}]}`),
				"GET /rest/agile/1.0/board/7/sprint": wp18Fail(tc.probe, tc.answer),
			})
			a := newWP18Adapter(t, srv.URL, nil)
			got, err := a.SearchBoards(context.Background(), "TEAM", "team")
			if err != nil {
				t.Fatalf("SearchBoards: %v", err)
			}
			if len(got) != 1 || got[0].ID != "7" || got[0].Name != "Team board" {
				t.Fatalf("boards = %+v", got)
			}
			if got[0].Kind != tc.want || got[0].RawType != "simple" {
				t.Fatalf("Kind/RawType = %q/%q, want %q/simple", got[0].Kind, got[0].RawType, tc.want)
			}
		})
	}
}

func TestEndpoints_SearchBoards_ScrumAndKanbanNeedNoProbe(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board": wp18Ok(`{"isLast":true,"values":[{"id":1,"name":"A","type":"scrum"},{"id":2,"name":"B","type":"kanban"}]}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	got, err := a.SearchBoards(context.Background(), "MOB", "x")
	if err != nil {
		t.Fatalf("SearchBoards: %v", err)
	}
	if len(got) != 2 || got[0].Kind != "scrum" || got[1].Kind != "kanban" || got[0].RawType != "scrum" || got[1].RawType != "kanban" {
		t.Fatalf("boards = %+v", got)
	}
	if n := len(srv.all()); n != 1 {
		t.Fatalf("requests = %d, want 1 (no sprint probe)", n)
	}
}

// ── BoardConfig ──────────────────────────────────────────────────────────────

func TestEndpoints_BoardConfig_MapsColumnsInOrderWithStatusIds(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board/1/configuration": wp18Ok(wp18Testdata(t, "wp18_board_config.json")),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	cfg, err := a.BoardConfig(context.Background(), "1")
	if err != nil {
		t.Fatalf("BoardConfig: %v", err)
	}
	want := []model.Column{
		{Name: "Backlog", Position: 0, StatusIDs: nil},
		{Name: "Selected for Development", Position: 1, StatusIDs: []string{"10001"}},
		{Name: "In Progress", Position: 2, StatusIDs: []string{"10002", "10003"}},
		{Name: "Done", Position: 3, StatusIDs: []string{"10005"}},
	}
	if len(cfg.Columns) != len(want) {
		t.Fatalf("columns = %+v", cfg.Columns)
	}
	for i, w := range want {
		g := cfg.Columns[i]
		if g.Name != w.Name || g.Position != w.Position || strings.Join(g.StatusIDs, ",") != strings.Join(w.StatusIDs, ",") {
			t.Errorf("column[%d] = %+v, want %+v", i, g, w)
		}
	}
	if cfg.Board.ID != "1" || cfg.Board.Name != "MOB board" || cfg.Board.Kind != "scrum" || cfg.Board.RawType != "scrum" {
		t.Errorf("board = %+v", cfg.Board)
	}
	if n := len(srv.all()); n != 1 {
		t.Errorf("requests = %d, want 1 (scrum needs no probe)", n)
	}
}

func TestEndpoints_BoardConfig_MetaCarriesFilterSubQueryAndRankField(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board/1/configuration": wp18Ok(wp18Testdata(t, "wp18_board_config.json")),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	cfg, err := a.BoardConfig(context.Background(), "1")
	if err != nil {
		t.Fatalf("BoardConfig: %v", err)
	}
	m := cfg.Board.Meta
	if m == nil {
		t.Fatal("Board.Meta is nil")
	}
	if got := fmt.Sprint(m["filterId"]); got != "9001" {
		t.Errorf("Meta[filterId] = %v, want 9001", m["filterId"])
	}
	if got := wp18MetaString(m, "subQuery"); got != "fixVersion in unreleasedVersions() OR fixVersion is EMPTY" {
		t.Errorf("Meta[subQuery] = %q", got)
	}
	if got := wp18MetaString(m, "rankFieldId"); got != "customfield_10019" {
		t.Errorf("Meta[rankFieldId] = %v, want customfield_10019", m["rankFieldId"])
	}
}

func TestEndpoints_BoardConfig_WithoutSubQueryHasNoSubQueryText(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board/2/configuration": wp18Ok(`{"id":2,"name":"Flow","type":"kanban","filter":{"id":"9002"},
			"columnConfig":{"columns":[{"name":"Open","statuses":[{"id":"1"}]}]},"ranking":{"rankCustomFieldId":10019}}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	cfg, err := a.BoardConfig(context.Background(), "2")
	if err != nil {
		t.Fatalf("BoardConfig: %v", err)
	}
	if cfg.Board.Kind != "kanban" || cfg.Board.RawType != "kanban" {
		t.Errorf("Kind/RawType = %q/%q", cfg.Board.Kind, cfg.Board.RawType)
	}
	if s := wp18MetaString(cfg.Board.Meta, "subQuery"); s != "" {
		t.Errorf("Meta[subQuery] = %q, want absent/empty", s)
	}
	if got := fmt.Sprint(cfg.Board.Meta["filterId"]); got != "9002" {
		t.Errorf("Meta[filterId] = %v", cfg.Board.Meta["filterId"])
	}
}

func TestEndpoints_BoardConfig_KeepsRankFieldIdThatIsAlreadyAFieldId(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board/2/configuration": wp18Ok(`{"id":2,"name":"Flow","type":"kanban","filter":{"id":"9002"},
			"columnConfig":{"columns":[]},"ranking":{"rankCustomFieldId":"customfield_10040"}}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	cfg, err := a.BoardConfig(context.Background(), "2")
	if err != nil {
		t.Fatalf("BoardConfig: %v", err)
	}
	if got := wp18MetaString(cfg.Board.Meta, "rankFieldId"); got != "customfield_10040" {
		t.Errorf("Meta[rankFieldId] = %v", cfg.Board.Meta["rankFieldId"])
	}
}

func TestEndpoints_BoardConfig_SimpleBoardNormalisedByProbe(t *testing.T) {
	defer wp18Guard(t)
	for _, tc := range []struct {
		probe int
		want  string
	}{{200, "scrum"}, {404, "kanban"}, {400, "kanban"}} {
		t.Run(fmt.Sprintf("probe %d", tc.probe), func(t *testing.T) {
			defer wp18Guard(t)
			srv := newWP18Server(t, map[string]wp18Route{
				"GET /rest/agile/1.0/board/7/configuration": wp18Ok(`{"id":7,"name":"Team","type":"simple","filter":{"id":"9007"},"columnConfig":{"columns":[{"name":"Open","statuses":[{"id":"1"}]}]},"ranking":{"rankCustomFieldId":10019}}`),
				"GET /rest/agile/1.0/board/7/sprint":        wp18Fail(tc.probe, `{"errorMessages":["x"]}`),
			})
			a := newWP18Adapter(t, srv.URL, nil)
			cfg, err := a.BoardConfig(context.Background(), "7")
			if err != nil {
				t.Fatalf("BoardConfig: %v", err)
			}
			if cfg.Board.Kind != tc.want || cfg.Board.RawType != "simple" {
				t.Fatalf("Kind/RawType = %q/%q, want %q/simple", cfg.Board.Kind, cfg.Board.RawType, tc.want)
			}
		})
	}
}

func TestEndpoints_BoardConfig_MissingBoardIsNotFound(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board/99/configuration": wp18Fail(404, `{"errorMessages":["Board does not exist or you do not have permission to see it."]}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	_, err := a.BoardConfig(context.Background(), "99")
	wp18AssertCode(t, err, apperr.NotFound)
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("message = %q, want it to say the board no longer exists", err.Error())
	}
}

// ── ActiveSprints ────────────────────────────────────────────────────────────

func TestEndpoints_ActiveSprints_MapsSprintsAndTimes(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board/1/sprint": wp18Ok(`{"maxResults":50,"startAt":0,"isLast":true,"values":[
			{"id":42,"self":"x","state":"active","name":"Sprint 42","startDate":"2026-11-10T09:00:00.000Z","endDate":"2026-11-24T17:00:00.000+0100","originBoardId":1},
			{"id":43,"state":"active","name":"Sprint 43","startDate":"2026-11-10T09:00:00.000Z","endDate":"2026-11-24T17:00:00.000Z","completeDate":"2026-11-24T16:00:00.000Z","originBoardId":1}]}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	got, err := a.ActiveSprints(context.Background(), "1")
	if err != nil {
		t.Fatalf("ActiveSprints: %v", err)
	}
	reqs := srv.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if reqs[0].Query.Get("state") != "active" || reqs[0].Query.Get("maxResults") != "50" {
		t.Errorf("query = %v, want state=active&maxResults=50", reqs[0].Query)
	}
	if len(got) != 2 {
		t.Fatalf("sprints = %+v", got)
	}
	s := got[0]
	if s.ID != "42" || s.BoardID != "1" || s.Name != "Sprint 42" || s.State != "active" {
		t.Errorf("sprint[0] = %+v", s)
	}
	if !s.StartAt.Equal(time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("StartAt = %v", s.StartAt)
	}
	if !s.EndAt.Equal(time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC)) {
		t.Errorf("EndAt = %v, want 2026-11-24T16:00Z (offset converted)", s.EndAt)
	}
	if !s.CompleteAt.IsZero() {
		t.Errorf("CompleteAt = %v, want zero (absent)", s.CompleteAt)
	}
	if !got[1].CompleteAt.Equal(time.Date(2026, 11, 24, 16, 0, 0, 0, time.UTC)) || got[1].ID != "43" {
		t.Errorf("sprint[1] = %+v", got[1])
	}
}

func TestEndpoints_ActiveSprints_BoardWithoutSprintsYieldsEmptyAndNoError(t *testing.T) {
	defer wp18Guard(t)
	for _, status := range []int{400, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			defer wp18Guard(t)
			srv := newWP18Server(t, map[string]wp18Route{
				"GET /rest/agile/1.0/board/2/sprint": wp18Fail(status, `{"errorMessages":["The board does not support sprints"]}`),
			})
			a := newWP18Adapter(t, srv.URL, nil)
			got, err := a.ActiveSprints(context.Background(), "2")
			if err != nil {
				t.Fatalf("ActiveSprints: %v, want nil error for a kanban board", err)
			}
			if len(got) != 0 {
				t.Fatalf("sprints = %+v, want empty", got)
			}
		})
	}
}

func TestEndpoints_ActiveSprints_UnauthorizedIsAuthInvalid(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/agile/1.0/board/1/sprint": wp18Fail(401, `{}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	_, err := a.ActiveSprints(context.Background(), "1")
	wp18AssertCode(t, err, apperr.AuthInvalid)
}

// ── Reference ────────────────────────────────────────────────────────────────

func wp18FieldRoutes(t *testing.T, fieldsJSON string) map[string]wp18Route {
	return map[string]wp18Route{
		"GET /rest/api/3/field":  wp18Ok(fieldsJSON),
		"GET /rest/api/3/status": wp18Ok(wp18Testdata(t, "wp18_statuses.json")),
	}
}

func TestEndpoints_Reference_DiscoversFieldIdsIntoMeta(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, wp18FieldRoutes(t, wp18Testdata(t, "wp18_fields.json")))
	a := newWP18Adapter(t, srv.URL, nil)
	if _, _, err := a.Reference(context.Background()); err != nil {
		t.Fatalf("Reference: %v", err)
	}
	m := a.Meta()
	for k, want := range map[string]string{
		"sprintFieldId":  "customfield_10020",
		"rankFieldId":    "customfield_10019",
		"flaggedFieldId": "customfield_10021",
	} {
		if got := wp18MetaString(m, k); got != want {
			t.Errorf("Meta[%s] = %v, want %s", k, m[k], want)
		}
	}
	if got := wp18MetaString(m, "fieldsFetchedAt"); got == "" {
		t.Errorf("Meta[fieldsFetchedAt] missing: %v", m)
	} else if ts, err := time.Parse(time.RFC3339Nano, got); err != nil || !ts.Equal(wp18Now) {
		t.Errorf("Meta[fieldsFetchedAt] = %q (%v), want %v", got, err, wp18Now)
	}
	if n := len(srv.to("GET", "/rest/api/3/field")); n != 1 {
		t.Errorf("/field requests = %d, want 1", n)
	}
}

func TestEndpoints_Reference_FlaggedFallsBackToClauseNames(t *testing.T) {
	defer wp18Guard(t)
	fields := `[
	  {"id":"customfield_10019","name":"Rank","custom":true,"clauseNames":["cf[10019]","Rank"],"schema":{"custom":"com.pyxis.greenhopper.jira:gh-lexo-rank"}},
	  {"id":"customfield_10020","name":"Sprint","custom":true,"clauseNames":["cf[10020]","Sprint"],"schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}},
	  {"id":"customfield_10050","name":"Markiert","custom":true,"clauseNames":["cf[10050]","Flagged"],"schema":{"type":"array","items":"option"}}]`
	srv := newWP18Server(t, wp18FieldRoutes(t, fields))
	a := newWP18Adapter(t, srv.URL, nil)
	if _, _, err := a.Reference(context.Background()); err != nil {
		t.Fatalf("Reference: %v", err)
	}
	if got := wp18MetaString(a.Meta(), "flaggedFieldId"); got != "customfield_10050" {
		t.Fatalf("flaggedFieldId = %q, want customfield_10050 (clauseNames fallback)", got)
	}
}

func TestEndpoints_Reference_FlaggedByNameBeatsClauseNameMatch(t *testing.T) {
	defer wp18Guard(t)
	fields := `[
	  {"id":"customfield_10050","name":"Markiert","custom":true,"clauseNames":["cf[10050]","Flagged"],"schema":{"type":"array"}},
	  {"id":"customfield_10051","name":"Flagged","custom":true,"clauseNames":["cf[10051]"],"schema":{"type":"array"}}]`
	srv := newWP18Server(t, wp18FieldRoutes(t, fields))
	a := newWP18Adapter(t, srv.URL, nil)
	if _, _, err := a.Reference(context.Background()); err != nil {
		t.Fatalf("Reference: %v", err)
	}
	if got := wp18MetaString(a.Meta(), "flaggedFieldId"); got != "customfield_10051" {
		t.Fatalf("flaggedFieldId = %q, want customfield_10051 (name match wins)", got)
	}
}

func TestEndpoints_Reference_MissingFlaggedFieldIsNotAnError(t *testing.T) {
	defer wp18Guard(t)
	fields := `[
	  {"id":"customfield_10019","name":"Rank","custom":true,"clauseNames":["cf[10019]"],"schema":{"custom":"com.pyxis.greenhopper.jira:gh-lexo-rank"}},
	  {"id":"customfield_10020","name":"Sprint","custom":true,"clauseNames":["cf[10020]"],"schema":{"custom":"com.pyxis.greenhopper.jira:gh-sprint"}}]`
	srv := newWP18Server(t, wp18FieldRoutes(t, fields))
	a := newWP18Adapter(t, srv.URL, nil)
	if _, _, err := a.Reference(context.Background()); err != nil {
		t.Fatalf("Reference: %v", err)
	}
	m := a.Meta()
	if got := wp18MetaString(m, "flaggedFieldId"); got != "" {
		t.Errorf("flaggedFieldId = %q, want empty", got)
	}
	if wp18MetaString(m, "sprintFieldId") != "customfield_10020" || wp18MetaString(m, "rankFieldId") != "customfield_10019" {
		t.Errorf("other ids missing: %v", m)
	}
}

func TestEndpoints_Reference_FieldsRefetchedOnlyWhenStaleOver24h(t *testing.T) {
	defer wp18Guard(t)
	old := map[string]any{
		"sprintFieldId": "customfield_OLD1", "rankFieldId": "customfield_OLD2", "flaggedFieldId": "customfield_OLD3",
	}
	meta := func(age time.Duration) map[string]any {
		m := map[string]any{}
		for k, v := range old {
			m[k] = v
		}
		if age >= 0 {
			m["fieldsFetchedAt"] = wp18Now.Add(-age).Format(time.RFC3339Nano)
		}
		return m
	}
	tests := []struct {
		name        string
		meta        map[string]any
		wantRefetch bool
	}{
		{"fetched 23 h ago is fresh", meta(23 * time.Hour), false},
		{"fetched 25 h ago is stale", meta(25 * time.Hour), true},
		{"never fetched", meta(-1), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp18Guard(t)
			routes := wp18FieldRoutes(t, wp18Testdata(t, "wp18_fields.json"))
			srv := newWP18Server(t, routes)
			a := newWP18Adapter(t, srv.URL, tc.meta)
			if _, _, err := a.Reference(context.Background()); err != nil {
				t.Fatalf("Reference: %v", err)
			}
			fieldReqs := len(srv.to("GET", "/rest/api/3/field"))
			if n := len(srv.to("GET", "/rest/api/3/status")); n != 1 {
				t.Errorf("/status requests = %d, want 1 (every call)", n)
			}
			m := a.Meta()
			if tc.wantRefetch {
				if fieldReqs != 1 {
					t.Errorf("/field requests = %d, want 1", fieldReqs)
				}
				if got := wp18MetaString(m, "sprintFieldId"); got != "customfield_10020" {
					t.Errorf("sprintFieldId = %q, want refreshed customfield_10020", got)
				}
				if ts, err := time.Parse(time.RFC3339Nano, wp18MetaString(m, "fieldsFetchedAt")); err != nil || !ts.Equal(wp18Now) {
					t.Errorf("fieldsFetchedAt = %v (%v), want %v", m["fieldsFetchedAt"], err, wp18Now)
				}
				return
			}
			if fieldReqs != 0 {
				t.Errorf("/field requests = %d, want 0 (fresh)", fieldReqs)
			}
			for k, want := range old {
				if got := wp18MetaString(m, k); got != want {
					t.Errorf("Meta[%s] = %q, want preserved %q", k, got, want)
				}
			}
			if ts, err := time.Parse(time.RFC3339Nano, wp18MetaString(m, "fieldsFetchedAt")); err != nil || !ts.Equal(wp18Now.Add(-23*time.Hour)) {
				t.Errorf("fieldsFetchedAt = %v (%v), want unchanged (23 h before now)", m["fieldsFetchedAt"], err)
			}
		})
	}
}

func TestEndpoints_Reference_MapsStatusesWithCategoryAndBlockedHint(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, wp18FieldRoutes(t, wp18Testdata(t, "wp18_fields.json")))
	a := newWP18Adapter(t, srv.URL, nil)
	statuses, types, err := a.Reference(context.Background())
	if err != nil {
		t.Fatalf("Reference: %v", err)
	}
	want := []model.Status{
		{ID: "10001", Name: "To Do", Category: "new", BlockedHint: false},
		{ID: "10002", Name: "In Progress", Category: "indeterminate", BlockedHint: false},
		{ID: "10003", Name: "Blocked", Category: "indeterminate", BlockedHint: true},
		{ID: "10004", Name: "Waiting - On Hold", Category: "indeterminate", BlockedHint: true},
		{ID: "10005", Name: "Done", Category: "done", BlockedHint: false},
		{ID: "10006", Name: "Legacy", Category: "undefined", BlockedHint: false},
	}
	if len(statuses) != len(want) {
		t.Fatalf("statuses = %+v", statuses)
	}
	for i, w := range want {
		if statuses[i] != w {
			t.Errorf("status[%d] = %+v, want %+v", i, statuses[i], w)
		}
	}
	if len(types) != 0 {
		t.Errorf("types = %+v, want none before any Hydrate", types)
	}
}

// ── SearchUsers ──────────────────────────────────────────────────────────────

func TestEndpoints_SearchUsers_SendsParamsAndKeepsActiveAtlassianAccounts(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"GET /rest/api/3/user/assignable/search": wp18Ok(`[
		  {"accountId":"u-iris","displayName":"Iris Calloway","active":true,"accountType":"atlassian","emailAddress":"p@acme.example"},
		  {"accountId":"u-gone","displayName":"Former Employee","active":false,"accountType":"atlassian"},
		  {"accountId":"bot-1","displayName":"Automation","active":true,"accountType":"app"},
		  {"accountId":"cust-1","displayName":"Customer Carl","active":true,"accountType":"customer"},
		  {"accountId":"u-silas","displayName":"Silas Lee","active":true,"accountType":"atlassian"}]`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	got, err := a.SearchUsers(context.Background(), "MOB", "ma")
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	reqs := srv.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	wp18WantQuery(t, reqs[0].Query, map[string]string{"project": "MOB", "query": "ma", "maxResults": "20"})
	want := []model.Person{
		{AccountID: "u-iris", DisplayName: "Iris Calloway", Active: true, Kind: "user"},
		{AccountID: "u-silas", DisplayName: "Silas Lee", Active: true, Kind: "user"},
	}
	if len(got) != len(want) {
		t.Fatalf("people = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("person[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// ── ValidateFilter ───────────────────────────────────────────────────────────

func TestEndpoints_ValidateFilter_ValidExpressionIsOk(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"POST /rest/api/3/jql/parse": wp18Ok(`{"queries":[{"query":"labels = standup","structure":{},"errors":[]}]}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	ok, errs, err := a.ValidateFilter(context.Background(), "labels = standup")
	if err != nil || !ok || len(errs) != 0 {
		t.Fatalf("ok=%v errs=%v err=%v, want ok with no errors", ok, errs, err)
	}
	reqs := srv.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if got := reqs[0].Query.Get("validation"); got != "strict" {
		t.Errorf("validation = %q, want strict", got)
	}
	body := reqs[0].json(t)
	qs, _ := body["queries"].([]any)
	if len(qs) != 1 || qs[0] != "labels = standup" {
		t.Errorf("body queries = %v, want [\"labels = standup\"]", body["queries"])
	}
}

func TestEndpoints_ValidateFilter_ReturnsJiraErrorsFromFirstQuery(t *testing.T) {
	defer wp18Guard(t)
	srv := newWP18Server(t, map[string]wp18Route{
		"POST /rest/api/3/jql/parse": wp18Ok(`{"queries":[{"query":"lable = x","errors":["Field 'lable' does not exist or you do not have permission to view it.","Second message"]}]}`),
	})
	a := newWP18Adapter(t, srv.URL, nil)
	ok, errs, err := a.ValidateFilter(context.Background(), "lable = x")
	if err != nil {
		t.Fatalf("ValidateFilter: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	if len(errs) != 2 || errs[0] != "Field 'lable' does not exist or you do not have permission to view it." || errs[1] != "Second message" {
		t.Fatalf("errs = %q", errs)
	}
}

func TestEndpoints_ValidateFilter_OrderByIsRejectedLocallyWithoutRequests(t *testing.T) {
	defer wp18Guard(t)
	for _, expr := range []string{
		"labels = standup ORDER BY created",
		"labels = standup order by created DESC",
		"labels = standup  Order   By\tRank",
	} {
		t.Run(expr, func(t *testing.T) {
			defer wp18Guard(t)
			srv := newWP18Server(t, map[string]wp18Route{}) // any request is an error
			a := newWP18Adapter(t, srv.URL, nil)
			ok, errs, err := a.ValidateFilter(context.Background(), expr)
			if err != nil {
				t.Fatalf("ValidateFilter: %v, want nil error", err)
			}
			if ok || len(errs) == 0 {
				t.Fatalf("ok=%v errs=%v, want ok=false with a message", ok, errs)
			}
			if n := len(srv.all()); n != 0 {
				t.Fatalf("requests = %d, want 0", n)
			}
		})
	}
}

// ── IssueTypeKind / StatusBlockedHint ────────────────────────────────────────

func TestIssueTypeKind_Table(t *testing.T) {
	defer wp18Guard(t)
	tests := []struct {
		name      string
		typeName  string
		subtask   bool
		hierarchy int
		want      string
	}{
		{"subtask flag", "Sub-task", true, -1, "subtask"},
		{"subtask flag beats a known name", "Story", true, 0, "subtask"},
		{"subtask flag beats hierarchy", "Epic", true, 1, "subtask"},
		{"hierarchy level 1 is epic", "Epic", false, 1, "epic"},
		{"hierarchy level 1 beats the name", "Story", false, 1, "epic"},
		{"story", "Story", false, 0, "story"},
		{"bug", "Bug", false, 0, "bug"},
		{"task", "Task", false, 0, "task"},
		{"spike", "Spike", false, 0, "spike"},
		{"name match is case-insensitive", "BUG", false, 0, "bug"},
		{"lower case", "story", false, 0, "story"},
		{"unknown name", "Improvement", false, 0, "other"},
		{"partial name is not a match", "Technical Task", false, 0, "other"},
		{"epic name at level 0 is other", "Epic", false, 0, "other"},
		{"sub-task level without flag falls back to name", "Task", false, -1, "task"},
		{"empty name", "", false, 0, "other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp18Guard(t)
			if got := IssueTypeKind(tc.typeName, tc.subtask, tc.hierarchy); got != tc.want {
				t.Errorf("IssueTypeKind(%q, %v, %d) = %q, want %q", tc.typeName, tc.subtask, tc.hierarchy, got, tc.want)
			}
		})
	}
}

func TestStatusBlockedHint_Table(t *testing.T) {
	defer wp18Guard(t)
	tests := []struct {
		name string
		want bool
	}{
		{"Blocked", true},
		{"BLOCKED", true},
		{"blocked", true},
		{"Impeded", true},
		{"On Hold", true},
		{"on hold", true},
		{"Blocked by QA", true},
		{"Waiting - On Hold", true},
		{"Not impeded", true},
		{"Awaiting Blocker", false},
		{"Unblocked", false},
		{"Roadblocked", false},
		{"On-Hold", false},
		{"Onhold", false},
		{"Holding", false},
		{"Hold", false},
		{"In Progress", false},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp18Guard(t)
			if got := StatusBlockedHint(tc.name); got != tc.want {
				t.Errorf("StatusBlockedHint(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

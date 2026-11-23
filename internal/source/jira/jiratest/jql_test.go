package jiratest

// Tests for WP-08: the JQL subset (ParseJQL, Match) and the search handlers (POST /rest/api/3/search/jql,
// /search/approximate-count, /jql/parse). Query shapes are exactly those of jira-sync.md §2.8.
// ParseJQL/Match tests run on a hand-built Dataset (no Materialize dependency).
//
// SPEC-NOTE (openSprints): openSprints() means sprints that are NOT closed (state active or future), as in Jira.
// An issue is "in openSprints()" when any of its SprintIDs is such a sprint.
//
// SPEC-NOTE (NOT IN): `sprint not in openSprints()` is the exact negation of `sprint in openSprints()`, so an issue
// with no sprint at all also matches it (Jira semantics: NOT IN includes EMPTY). The §2.8 backlog expression
// `(sprint is EMPTY OR sprint not in openSprints())` is pinned in full as well.
//
// SPEC-NOTE (status lists): `status in (…)` takes unquoted ids (10001) and quoted values; a quoted value matches a
// status id or a status NAME, case-insensitively. `id in (…)` takes unquoted or quoted issue ids.
//
// SPEC-NOTE (updated): `updated >= -Nd` means Updated >= Dataset.Now minus N*24h (Dataset.Now, never the wall clock;
// the hand-built Dataset uses a Now in 2030 so a wall-clock implementation fails). The boundary is INCLUSIVE: an issue
// updated exactly N days before Now matches (pinned in TestJQLMatch_UpdatedUsesDatasetNowAndIncludesTheBoundary); the
// table-driven cases avoid exact boundaries.
//
// SPEC-NOTE (grammar): keywords (AND, OR, NOT, IN, IS, EMPTY, ORDER BY, ASC, DESC, Rank) are case-insensitive; AND binds
// tighter than OR; NOT binds tighter than AND; parentheses group. `ORDER BY Rank` without direction = ASC. Anything
// outside the documented subset (other fields, other operators, other ORDER BY keys, unbalanced parentheses, trailing
// junk, non-numeric filter ids, a clause after ORDER BY) is an error.
//
// SPEC-NOTE (search wire format): response {issues:[{id,key,fields:{…}}], nextPageToken?, isLast?}; the last page has no
// nextPageToken (absent or ""); errors answer 400 {errorMessages:[…]}. A projection of ["updated"] yields objects with
// exactly the keys id, key, fields and fields with exactly the key updated. Hydrate fields follow jira-sync §2.8.
// approximate-count answers {"count": N}. jql/parse answers {queries:[{query, errors:[string…]}]} index-aligned with the
// request; errors is empty or absent for a parsable query.

import (
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// jqlDataset: two scrum boards (filters 9001 and 9002), sprints 41 closed / 42 active (board 1) and 50 future (board 2).
func jqlDataset() *Dataset {
	now := time.Date(2030, 6, 15, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	h := time.Hour
	mk := func(id, key string, board int, status string, sprints []int, upd time.Duration, rank string) *DSIssue {
		return &DSIssue{ID: id, Key: key, BoardID: board, StatusID: status, SprintIDs: sprints, Updated: ago(upd), Created: ago(upd + h), Rank: rank}
	}
	return &Dataset{
		Now: now,
		Boards: []DSBoard{
			{FxBoard: FxBoard{ID: 1, Name: "A", Type: "scrum", FilterID: 9001},
				Sprints: []DSSprint{{ID: 41, BoardID: 1, State: "closed"}, {ID: 42, BoardID: 1, State: "active"}}},
			{FxBoard: FxBoard{ID: 2, Name: "B", Type: "scrum", FilterID: 9002},
				Sprints: []DSSprint{{ID: 50, BoardID: 2, State: "future"}}},
		},
		Statuses: []DSStatus{{ID: "1", Name: "To Do", Category: "new"}, {ID: "2", Name: "In Progress", Category: "indeterminate"},
			{ID: "3", Name: "Code Review", Category: "indeterminate"}, {ID: "4", Name: "Done", Category: "done"}},
		Issues: []*DSIssue{
			mk("101", "A-1", 1, "1", []int{42}, 30*h, "0|a0"),
			mk("102", "A-2", 1, "2", []int{42}, 14*24*h+2*h, "0|a1"),
			mk("103", "A-3", 1, "3", []int{41, 42}, 16*24*h+h, "0|a2"),
			mk("104", "A-4", 1, "4", []int{41}, 20*24*h, "0|a3"),
			mk("105", "A-5", 1, "1", nil, 30*24*h, "0|a4"),
			mk("106", "A-6", 1, "2", nil, 50*h, "0|a5"),
			mk("201", "B-1", 2, "1", []int{50}, 80*h, "0|b0"),
			mk("202", "B-2", 2, "4", nil, 10*24*h, "0|b1"),
		},
	}
}

func matchKeys(t *testing.T, ds *Dataset, q *JQL) []string {
	t.Helper()
	var keys []string
	for _, is := range ds.Issues {
		if q.Match(ds, is) {
			keys = append(keys, is.Key)
		}
	}
	return keys
}

func TestJQLMatch_QueryShapes(t *testing.T) {
	defer guard(t)
	ds := jqlDataset()
	cases := []struct {
		name, jql string
		want      []string
	}{
		{"filter board 1", `filter = 9001`, []string{"A-1", "A-2", "A-3", "A-4", "A-5", "A-6"}},
		{"filter board 2", `filter = 9002`, []string{"B-1", "B-2"}},
		{"filter unknown", `filter = 1234`, nil},
		{"sprint in one", `sprint in (42)`, []string{"A-1", "A-2", "A-3"}},
		{"sprint in closed", `sprint in (41)`, []string{"A-3", "A-4"}},
		{"sprint in several", `sprint in (41, 50)`, []string{"A-3", "A-4", "B-1"}},
		{"sprint in openSprints", `sprint in openSprints()`, []string{"A-1", "A-2", "A-3", "B-1"}},
		{"sprint is EMPTY", `sprint is EMPTY`, []string{"A-5", "A-6", "B-2"}},
		{"sprint not in openSprints", `sprint not in openSprints()`, []string{"A-4", "A-5", "A-6", "B-2"}},
		{"status ids", `status in (2)`, []string{"A-2", "A-6"}},
		{"status ids list", `status in ( 1 ,2 )`, []string{"A-1", "A-2", "A-5", "A-6", "B-1"}},
		{"status quoted names", `status in ("In Progress", "Done")`, []string{"A-2", "A-4", "A-6", "B-2"}},
		{"status name case-insensitive", `status in ("in progress")`, []string{"A-2", "A-6"}},
		{"status mixed id and name", `status in (1, "Code Review")`, []string{"A-1", "A-3", "A-5", "B-1"}},
		{"status quoted id", `status in ("4")`, []string{"A-4", "B-2"}},
		{"id in", `id in (101, 103, 202)`, []string{"A-1", "A-3", "B-2"}},
		{"id in quoted", `id in ("101","106")`, []string{"A-1", "A-6"}},
		{"updated 15d", `updated >= -15d`, []string{"A-1", "A-2", "A-6", "B-1", "B-2"}},
		{"updated 3d", `updated >= -3d`, []string{"A-1", "A-6"}},
		{"updated 40d is everything", `updated >= -40d`, []string{"A-1", "A-2", "A-3", "A-4", "A-5", "A-6", "B-1", "B-2"}},
		{"and", `filter = 9001 AND status in (1,2) AND sprint in (42)`, []string{"A-1", "A-2"}},
		{"or", `sprint in (50) OR status in (4)`, []string{"A-4", "B-1", "B-2"}},
		{"and binds tighter than or", `status in (1) OR status in (2) AND sprint is EMPTY`, []string{"A-1", "A-5", "A-6", "B-1"}},
		{"parentheses override", `(status in (1) OR status in (2)) AND sprint is EMPTY`, []string{"A-5", "A-6"}},
		{"not status", `NOT status in (1,2,3)`, []string{"A-4", "B-2"}},
		{"not parenthesised", `NOT (sprint is EMPTY)`, []string{"A-1", "A-2", "A-3", "A-4", "B-1"}},
		{"and not", `filter = 9001 AND NOT sprint in openSprints()`, []string{"A-4", "A-5", "A-6"}},
		{"nested parentheses", `filter = 9001 AND ((status in (1) AND sprint is EMPTY) OR status in (4))`, []string{"A-4", "A-5"}},
		{"lowercase keywords", `filter = 9001 and status in (2) or sprint is empty`, []string{"A-2", "A-5", "A-6", "B-2"}},
		{"extra whitespace and newlines", "filter  =  9001\n AND\tstatus in (1)", []string{"A-1", "A-5"}},
		// jira-sync §2.8 table, with ids substituted
		{"§2.8 sprint query", `filter = 9001 AND sprint in (42) ORDER BY Rank ASC`, []string{"A-1", "A-2", "A-3"}},
		{"§2.8 open (scrum, board scope)", `filter = 9001 AND status in (1,2,3) AND (sprint in openSprints() OR status in (2,3)) ORDER BY Rank ASC`,
			[]string{"A-1", "A-2", "A-3", "A-6"}},
		{"§2.8 open (kanban)", `filter = 9002 AND status in (1,2,3) ORDER BY Rank ASC`, []string{"B-1"}},
		{"§2.8 done", `filter = 9001 AND status in (4) AND (updated >= -15d OR sprint in (42)) ORDER BY Rank ASC`, nil},
		{"§2.8 done with sprint clause", `filter = 9001 AND status in (4, 3) AND (updated >= -15d OR sprint in (42)) ORDER BY Rank ASC`, []string{"A-3"}},
		{"§2.8 backlog (scrum)", `filter = 9001 AND status in (1, 4) AND (sprint is EMPTY OR sprint not in openSprints()) ORDER BY Rank ASC`,
			[]string{"A-4", "A-5"}},
		{"§2.8 backlog (kanban)", `filter = 9002 AND status in (4) ORDER BY Rank ASC`, []string{"B-2"}},
		{"order by is ignored by Match", `status in (1) ORDER BY Rank DESC`, []string{"A-1", "A-5", "B-1"}},
	}
	for _, c := range cases {
		sub(t, c.name, func(t *testing.T) {
			q, err := ParseJQL(c.jql)
			if err != nil || q == nil {
				t.Fatalf("ParseJQL(%q) = %v, %v; want a query", c.jql, q, err)
			}
			got := matchKeys(t, ds, q)
			if !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Fatalf("%q matches %v, want %v", c.jql, got, c.want)
			}
		})
	}
}

func TestJQLMatch_UpdatedUsesDatasetNowAndIncludesTheBoundary(t *testing.T) {
	defer guard(t)
	ds := jqlDataset()
	exact := &DSIssue{ID: "301", Key: "X-1", BoardID: 1, StatusID: "1", Updated: ds.Now.Add(-15 * 24 * time.Hour)}
	q, err := ParseJQL(`updated >= -15d`)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Match(ds, exact) {
		t.Errorf("an issue updated exactly 15d before Dataset.Now must match `updated >= -15d`")
	}
	older := &DSIssue{ID: "302", Key: "X-2", BoardID: 1, StatusID: "1", Updated: ds.Now.Add(-15*24*time.Hour - time.Minute)}
	if q.Match(ds, older) {
		t.Errorf("an issue updated 15d and a minute before Dataset.Now must not match")
	}
}

func TestParseJQL_OrderByRank(t *testing.T) {
	defer guard(t)
	cases := []struct {
		jql        string
		order, dsc bool
	}{
		{`filter = 9001`, false, false},
		{`filter = 9001 ORDER BY Rank ASC`, true, false},
		{`filter = 9001 ORDER BY Rank DESC`, true, true},
		{`filter = 9001 ORDER BY Rank`, true, false},
		{`filter = 9001 order by rank desc`, true, true},
		{"filter = 9001\nORDER   BY  Rank  ASC", true, false},
	}
	for _, c := range cases {
		sub(t, c.jql, func(t *testing.T) {
			q, err := ParseJQL(c.jql)
			if err != nil {
				t.Fatalf("ParseJQL: %v", err)
			}
			if q.OrderByRank != c.order || q.Desc != c.dsc {
				t.Fatalf("OrderByRank/Desc = %v/%v, want %v/%v", q.OrderByRank, q.Desc, c.order, c.dsc)
			}
		})
	}
}

func TestParseJQL_UnsupportedSyntaxIsAnError(t *testing.T) {
	defer guard(t)
	for _, in := range []string{
		`assignee = currentUser()`,
		`project = MOB`,
		`status = Done`,
		`labels in (a)`,
		`filter = abc`,
		`filter =`,
		`filter`,
		`(status in (1)`,
		`status in (1))`,
		`status in (1) AND`,
		`AND status in (1)`,
		`status in (1) status in (2)`,
		`sprint in openSprints(`,
		`sprint in closedSprints()`,
		`updated >= -15x`,
		`ORDER BY created`,
		`filter = 9001 ORDER BY created ASC`,
		`filter = 9001 ORDER BY Rank ASC AND status in (1)`,
		`filter = 9001 ORDER BY Rank SIDEWAYS`,
		`foo bar baz`,
	} {
		sub(t, in, func(t *testing.T) {
			q, err := ParseJQL(in)
			if err == nil {
				t.Fatalf("ParseJQL(%q) = %+v, want an error", in, q)
			}
			if strings.TrimSpace(err.Error()) == "" {
				t.Errorf("error for %q has an empty message", in)
			}
		})
	}
}

// ── HTTP: POST /rest/api/3/search/jql ───────────────────────────────────────────────────────────

const board1Keys = "MOB-1,MOB-2,MOB-3,MOB-4,MOB-5,MOB-6,MOB-7" // rank order of filter 9001 in tinyFixture

func search(t *testing.T, s *Server, body map[string]any) (int, any) {
	t.Helper()
	return post(t, s, "/rest/api/3/search/jql", body)
}

func reverse(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// searchAll follows nextPageToken (bounded) and returns the keys of each page.
func searchAll(t *testing.T, s *Server, body map[string]any) [][]string {
	t.Helper()
	var pages [][]string
	token := ""
	for len(pages) < 50 {
		b := map[string]any{}
		for k, v := range body {
			b[k] = v
		}
		if token != "" {
			b["nextPageToken"] = token
		}
		code, v := search(t, s, b)
		mustOK(t, code, v, "search/jql")
		pages = append(pages, keysOf(jget(v, "issues")))
		token = jstr(jget(v, "nextPageToken"))
		if token == "" {
			if jget(v, "isLast") == false {
				t.Fatalf("last page says isLast=false: %v", v)
			}
			return pages
		}
	}
	t.Fatalf("search did not terminate within 50 pages")
	return nil
}

func flatten(pages [][]string) []string {
	var out []string
	for _, p := range pages {
		out = append(out, p...)
	}
	return out
}

func TestSearch_ReturnsFilterMatchesInRankOrder(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	want := strings.Split(board1Keys, ",")
	code, v := search(t, s, map[string]any{"jql": "filter = 9001 ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 1000})
	mustOK(t, code, v, "search")
	if got := keysOf(jget(v, "issues")); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	code, v = search(t, s, map[string]any{"jql": "filter = 9001 ORDER BY Rank DESC", "fields": []string{"updated"}, "maxResults": 1000})
	mustOK(t, code, v, "search desc")
	if got := keysOf(jget(v, "issues")); !reflect.DeepEqual(got, reverse(want)) {
		t.Fatalf("DESC keys = %v, want %v", got, reverse(want))
	}
}

func TestSearch_SprintScopeAndMutateVisibility(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	q := map[string]any{"jql": "filter = 9001 AND sprint in (42) ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 100}
	code, v := search(t, s, q)
	mustOK(t, code, v, "search")
	if got, want := keysOf(jget(v, "issues")), []string{"MOB-1", "MOB-2", "MOB-3", "MOB-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sprint 42 keys = %v, want %v", got, want)
	}
	s.Mutate(func(ds *Dataset) { issueByKey(ds, "MOB-4").SprintIDs = []int{42} }) // pulled into the sprint
	code, v = search(t, s, q)
	mustOK(t, code, v, "search after mutate")
	if got, want := keysOf(jget(v, "issues")), []string{"MOB-1", "MOB-2", "MOB-3", "MOB-4", "MOB-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys after Mutate = %v, want %v", got, want)
	}
}

func TestSearch_BacklogQueryUsesStatusIdsAndSprintClauses(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	var todo, doneID string
	withDS(s, func(ds *Dataset) { todo, doneID = statusIDByName(ds, "To Do"), statusIDByName(ds, "Done") })
	jql := "filter = 9001 AND status in (" + todo + ") AND (sprint is EMPTY OR sprint not in openSprints()) ORDER BY Rank ASC"
	code, v := search(t, s, map[string]any{"jql": jql, "fields": []string{"updated"}, "maxResults": 50})
	mustOK(t, code, v, "backlog search")
	if got, want := keysOf(jget(v, "issues")), []string{"MOB-4", "MOB-6", "MOB-7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("backlog keys = %v, want %v", got, want)
	}
	code, v = search(t, s, map[string]any{"jql": "filter = 9001 AND status in (" + doneID + ")", "fields": []string{"updated"}, "maxResults": 50})
	mustOK(t, code, v, "done search")
	if got, want := keysOf(jget(v, "issues")), []string{"MOB-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("done keys = %v, want %v", got, want)
	}
}

func TestSearch_UpdatedProjectionReturnsOnlyIdKeyAndUpdated(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := search(t, s, map[string]any{"jql": "filter = 9001 ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 1000})
	mustOK(t, code, v, "scan")
	issues := jarr(jget(v, "issues"))
	if len(issues) != 7 {
		t.Fatalf("issues = %d, want 7", len(issues))
	}
	updated := map[string]time.Time{}
	ids := map[string]string{}
	withDS(s, func(ds *Dataset) {
		for _, is := range ds.Issues {
			updated[is.Key], ids[is.Key] = is.Updated, is.ID
		}
	})
	for _, is := range issues {
		m := is.(map[string]any)
		if len(m) != 3 || !hasKey(m, "id") || !hasKey(m, "key") || !hasKey(m, "fields") {
			t.Errorf("issue object keys = %v, want exactly id, key, fields", m)
		}
		f := m["fields"].(map[string]any)
		if len(f) != 1 || !hasKey(f, "updated") {
			t.Errorf("fields = %v, want exactly updated", f)
			continue
		}
		key := jstr(m["key"])
		if jstr(m["id"]) != ids[key] {
			t.Errorf("%s id = %v, want %s", key, m["id"], ids[key])
		}
		if got := parseJiraTime(t, f["updated"]); !got.Equal(updated[key]) {
			t.Errorf("%s updated = %v, want %v", key, got, updated[key])
		}
	}
}

func TestSearch_HydrateFieldsFollowJiraShapes(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	var ids []string
	var svc3 DSIssue
	withDS(s, func(ds *Dataset) {
		for _, k := range []string{"MOB-1", "MOB-2", "MOB-3", "MOB-5", "MOB-6"} {
			ids = append(ids, issueByKey(ds, k).ID)
		}
		svc3 = *issueByKey(ds, "MOB-3")
	})
	fields := []string{"summary", "issuetype", "status", "assignee", "created", "updated", "resolutiondate", "parent", "comment",
		SprintFieldID, RankFieldID, FlaggedFieldID}
	code, v := search(t, s, map[string]any{"jql": "id in (" + strings.Join(ids, ",") + ")", "fields": fields, "maxResults": 100})
	mustOK(t, code, v, "hydrate")
	byKey := map[string]any{}
	for _, is := range jarr(jget(v, "issues")) {
		byKey[jstr(jget(is, "key"))] = is
	}
	if len(byKey) != 5 {
		t.Fatalf("hydrated %d issues, want 5: %v", len(byKey), keysOf(jget(v, "issues")))
	}

	svc1 := jget(byKey["MOB-1"], "fields")
	if jstr(jget(svc1, "summary")) != "Login page" {
		t.Errorf("MOB-1 summary = %v", jget(svc1, "summary"))
	}
	if jstr(jget(svc1, "issuetype", "name")) != "Story" || jstr(jget(svc1, "issuetype", "id")) == "" || jget(svc1, "issuetype", "subtask") != false {
		t.Errorf("MOB-1 issuetype = %v, want Story with id and subtask=false", jget(svc1, "issuetype"))
	}
	if _, ok := jget(svc1, "issuetype", "hierarchyLevel").(float64); !ok {
		t.Errorf("MOB-1 issuetype.hierarchyLevel = %v, want a number", jget(svc1, "issuetype", "hierarchyLevel"))
	}
	if jstr(jget(svc1, "status", "name")) != "In Progress" || jstr(jget(svc1, "status", "id")) == "" ||
		jstr(jget(svc1, "status", "statusCategory", "key")) != "indeterminate" {
		t.Errorf("MOB-1 status = %v, want In Progress / indeterminate with id", jget(svc1, "status"))
	}
	if jstr(jget(svc1, "assignee", "accountId")) != "u-ann" || jstr(jget(svc1, "assignee", "displayName")) != "Ann Archer" ||
		jget(svc1, "assignee", "active") != true || jstr(jget(svc1, "assignee", "accountType")) != "atlassian" {
		t.Errorf("MOB-1 assignee = %v, want u-ann / Ann Archer / active / atlassian", jget(svc1, "assignee"))
	}
	if jget(jget(byKey["MOB-2"], "fields"), "assignee") != nil {
		t.Errorf("unassigned MOB-2 assignee = %v, want null", jget(jget(byKey["MOB-2"], "fields"), "assignee"))
	}
	if jget(svc1, "created") == nil || jget(svc1, "updated") == nil {
		t.Errorf("MOB-1 lacks created/updated: %v", svc1)
	}
	if jget(svc1, "resolutiondate") != nil {
		t.Errorf("unresolved MOB-1 resolutiondate = %v, want null", jget(svc1, "resolutiondate"))
	}
	if got := parseJiraTime(t, jget(jget(byKey["MOB-3"], "fields"), "resolutiondate")); !got.Equal(svc3.Resolved) {
		t.Errorf("MOB-3 resolutiondate = %v, want %v", got, svc3.Resolved)
	}
	if jget(svc1, "parent") != nil {
		if _, isMap := jget(svc1, "parent").(map[string]any); !isMap {
			t.Errorf("parent = %v, want null or an object", jget(svc1, "parent"))
		}
	}

	// comments: total plus embedded {id, created, author.accountId}
	if jnum(jget(svc1, "comment", "total")) != 3 {
		t.Errorf("MOB-1 comment.total = %v, want 3", jget(svc1, "comment", "total"))
	}
	cs := jarr(jget(svc1, "comment", "comments"))
	if len(cs) == 0 || len(cs) > 3 {
		t.Errorf("MOB-1 embedded comments = %d, want 1..3", len(cs))
	}
	for _, c := range cs {
		if jstr(jget(c, "id")) == "" || jstr(jget(c, "author", "accountId")) == "" || jget(c, "created") == nil {
			t.Errorf("embedded comment %v lacks id/created/author.accountId", c)
		}
	}
	if jnum(jget(jget(byKey["MOB-2"], "fields"), "comment", "total")) != 0 {
		t.Errorf("MOB-2 comment.total = %v, want 0", jget(jget(byKey["MOB-2"], "fields"), "comment", "total"))
	}

	// sprint field: [{id,name,state,startDate,endDate,boardId}]
	sp := jarr(jget(svc1, SprintFieldID))
	if len(sp) != 1 || jstr(jget(sp[0], "id")) != "42" || jstr(jget(sp[0], "name")) != "Sprint 42" || jstr(jget(sp[0], "state")) != "active" ||
		jstr(jget(sp[0], "boardId")) != "1" || jget(sp[0], "startDate") == nil || jget(sp[0], "endDate") == nil {
		t.Errorf("MOB-1 sprint field = %v, want [Sprint 42 active board 1 with dates]", jget(svc1, SprintFieldID))
	}
	if n := len(jarr(jget(jget(byKey["MOB-6"], "fields"), SprintFieldID))); n != 0 {
		t.Errorf("backlog MOB-6 sprint field has %d entries, want null/empty", n)
	}

	// rank field is the string rank
	var rank string
	withDS(s, func(ds *Dataset) { rank = issueByKey(ds, "MOB-1").Rank })
	if got, ok := jget(svc1, RankFieldID).(string); !ok || got != rank {
		t.Errorf("MOB-1 rank field = %v, want %q", jget(svc1, RankFieldID), rank)
	}

	// flagged field: non-empty array when flagged
	if len(jarr(jget(jget(byKey["MOB-5"], "fields"), FlaggedFieldID))) == 0 {
		t.Errorf("flagged MOB-5 flagged field = %v, want a non-empty array", jget(jget(byKey["MOB-5"], "fields"), FlaggedFieldID))
	}
	if len(jarr(jget(svc1, FlaggedFieldID))) != 0 {
		t.Errorf("unflagged MOB-1 flagged field = %v, want null/empty", jget(svc1, FlaggedFieldID))
	}
}

func TestSearch_NextPageTokenPagingCoversEverythingOnce(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	want := strings.Split(board1Keys, ",")
	pages := searchAll(t, s, map[string]any{"jql": "filter = 9001 ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 3})
	if len(pages) != 3 || len(pages[0]) != 3 || len(pages[1]) != 3 || len(pages[2]) != 1 {
		t.Fatalf("page sizes = %v, want 3,3,1", pages)
	}
	if got := flatten(pages); !reflect.DeepEqual(got, want) {
		t.Fatalf("paged keys = %v, want %v", got, want)
	}
}

func TestSearch_PageExactlyFullHasNoNextToken(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := search(t, s, map[string]any{"jql": "filter = 9001 ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 7})
	mustOK(t, code, v, "search")
	if len(jarr(jget(v, "issues"))) != 7 {
		t.Fatalf("issues = %d, want 7", len(jarr(jget(v, "issues"))))
	}
	if tok := jstr(jget(v, "nextPageToken")); tok != "" {
		t.Fatalf("nextPageToken = %q on the final page, want absent", tok)
	}
}

func TestSearch_TokenIsOpaqueNonEmptyStringAndPagesAreDisjoint(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := search(t, s, map[string]any{"jql": "filter = 9001 ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 2})
	mustOK(t, code, v, "page 1")
	tok, ok := jget(v, "nextPageToken").(string)
	if !ok || tok == "" {
		t.Fatalf("nextPageToken = %v, want a non-empty string", jget(v, "nextPageToken"))
	}
	if _, err := strconv.Atoi(tok); err == nil {
		t.Errorf("token %q is a bare integer; it must be opaque (base64 of an offset)", tok)
	}
	code, v2 := search(t, s, map[string]any{"jql": "filter = 9001 ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 2, "nextPageToken": tok})
	mustOK(t, code, v2, "page 2")
	if got, want := keysOf(jget(v2, "issues")), []string{"MOB-3", "MOB-4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page 2 = %v, want %v", got, want)
	}
}

func TestSearch_ServerPageSizeCapsMaxResults(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{PageSize: 2})
	pages := searchAll(t, s, map[string]any{"jql": "filter = 9001 ORDER BY Rank ASC", "fields": []string{"updated"}, "maxResults": 100})
	if len(pages) != 4 || len(pages[0]) != 2 || len(pages[3]) != 1 {
		t.Fatalf("page sizes = %v, want 2,2,2,1 (PageSize caps maxResults=100)", pages)
	}
	if got, want := flatten(pages), strings.Split(board1Keys, ","); !reflect.DeepEqual(got, want) {
		t.Fatalf("paged keys = %v, want %v", got, want)
	}
}

func TestSearch_BadRequestsAnswer400(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	cases := []struct {
		name string
		body map[string]any
	}{
		{"unsupported jql", map[string]any{"jql": "assignee = currentUser()", "fields": []string{"updated"}}},
		{"garbage jql", map[string]any{"jql": "((", "fields": []string{"updated"}}},
		{"garbage token", map[string]any{"jql": "filter = 9001", "fields": []string{"updated"}, "nextPageToken": "!!!not-a-token!!!"}},
	}
	for _, c := range cases {
		sub(t, c.name, func(t *testing.T) {
			code, v := search(t, s, c.body)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", code)
			}
			if len(jarr(jget(v, "errorMessages"))) == 0 {
				t.Errorf("body = %v, want errorMessages[]", v)
			}
		})
	}
}

func TestSearch_RequiresAuthAndIsLogged(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	body := map[string]any{"jql": "filter = 9001", "fields": []string{"updated"}}
	if code, _, _ := rawCall(t, s, http.MethodPost, "/rest/api/3/search/jql", body, ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated search = %d, want 401", code)
	}
	s.ResetRequests()
	search(t, s, body)
	log := waitLog(t, s, 1)
	if log[0].Method != "POST" || log[0].Path != "/rest/api/3/search/jql" || log[0].Status != 200 {
		t.Errorf("log entry = %+v, want POST /rest/api/3/search/jql 200", log[0])
	}
}

// ── approximate-count and jql/parse ─────────────────────────────────────────────────────────────

func TestApproximateCount_CountsMatchesIgnoringPaging(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{PageSize: 2}) // paging caps must not affect the count
	var todo string
	withDS(s, func(ds *Dataset) { todo = statusIDByName(ds, "To Do") })
	cases := []struct {
		jql  string
		want int
	}{
		{"filter = 9001 AND status in (" + todo + ") AND (sprint is EMPTY OR sprint not in openSprints())", 3}, // MOB-4, MOB-6, MOB-7
		{"filter = 9001", 7},
		{"filter = 9002", 3},
		{"filter = 4242", 0},
	}
	for _, c := range cases {
		sub(t, c.jql, func(t *testing.T) {
			code, v := post(t, s, "/rest/api/3/search/approximate-count", map[string]any{"jql": c.jql})
			mustOK(t, code, v, "approximate-count")
			if got := jnum(jget(v, "count")); got != c.want || !hasKey(v, "count") {
				t.Fatalf("count = %v, want %d (body %v)", jget(v, "count"), c.want, v)
			}
		})
	}
}

func TestApproximateCount_BadJQLIs400(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	code, v := post(t, s, "/rest/api/3/search/approximate-count", map[string]any{"jql": "project = MOB"})
	if code != http.StatusBadRequest || len(jarr(jget(v, "errorMessages"))) == 0 {
		t.Fatalf("status %d body %v, want 400 with errorMessages", code, v)
	}
}

func TestJQLParse_ReportsErrorsPerQueryInOrder(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	queries := []string{"status in (1)", "assignee = nobody", "filter = 9001 AND sprint in openSprints()", "(("}
	code, v := post(t, s, "/rest/api/3/jql/parse?validation=strict", map[string]any{"queries": queries})
	mustOK(t, code, v, "jql/parse")
	qs := jarr(jget(v, "queries"))
	if len(qs) != 4 {
		t.Fatalf("queries = %d, want 4 (aligned with the request): %v", len(qs), v)
	}
	wantBad := []bool{false, true, false, true}
	for i, bad := range wantBad {
		errs := jarr(jget(qs[i], "errors"))
		if bad && len(errs) == 0 {
			t.Errorf("query %d %q: no errors, want at least one", i, queries[i])
		}
		if !bad && len(errs) != 0 {
			t.Errorf("query %d %q: errors %v, want none", i, queries[i], errs)
		}
		for _, e := range errs {
			if s, ok := e.(string); !ok || strings.TrimSpace(s) == "" {
				t.Errorf("query %d error %#v is not a non-empty string", i, e)
			}
		}
	}
}

func TestJQLParse_RequiresAuth(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	if code, _, _ := rawCall(t, s, http.MethodPost, "/rest/api/3/jql/parse?validation=strict", map[string]any{"queries": []string{"status in (1)"}}, ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated jql/parse = %d, want 401", code)
	}
}

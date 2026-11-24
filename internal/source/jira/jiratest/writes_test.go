package jiratest

// WP-T5 tests: the fake Jira's write endpoints (jira-sync.md §2.15 and §6 "Content and writes"; ADR 0015):
//
//	GET  /rest/api/3/issue/{id}/transitions?expand=transitions.fields
//	POST /rest/api/3/issue/{id}/transitions          {"transition":{"id":"…"}}            -> 204
//	PUT  /rest/api/3/issue/{id}/assignee             {"accountId":"…"|null}               -> 204
//	PUT  /rest/api/3/issue/{id}                      {"fields":{<Flagged>:[{"value":"Impediment"}]|null}} -> 204
//	POST /rest/api/3/issue/{id}/comment              {"body":<ADF>}                       -> 201 {"id":"…"}
//
// Helpers (guard, call, withDS, issueByKey, statusIDByName, testNow, nyc, basicAuth, rawCall, jget, jarr, jstr,
// waitLog) come from server_test.go. Names here are prefixed wt5.
//
// SPEC-NOTE (workflow): the frozen Dataset has no workflow, so the fake derives one: from the issue's current status,
// one transition per OTHER status in Dataset.Statuses (never to the current status). The transition's name is the
// target status name; its id is any non-empty string, unique within one response (tests never assume an id format and
// find transitions by to.name). Every listed transition has isAvailable == true. Screens, by target status name:
// "Done" -> hasScreen true with fields {"resolution": {required:true, hasDefaultValue:false}} (needs a screen);
// "Code Review" -> hasScreen true with only optional fields (required:false) (an optional-only screen);
// every other target -> hasScreen false and no fields. These tests use the default fixture (To Do, In Progress,
// Code Review, QA, Done), because the small tinyFixture has no Code Review.
// SPEC-NOTE (fields): `fields` of a transition appear when the request carries expand=transitions.fields.
// SPEC-NOTE (writes mutate): a successful write mutates the Dataset and appends exactly one DSChange (status, assignee,
// Flagged) or one DSComment (comment). The entry is stamped Dataset.Now (the fake clock, Options.Now), the issue's
// Updated becomes Dataset.Now, and the new change/comment id is a non-empty string unique across the whole dataset.
// A comment's author is the account that GET /myself reports. The appended change is visible through
// POST changelog/bulkfetch (so a refresh sees real history).
// SPEC-NOTE (rejections): all rejections leave the Dataset untouched (status, assignee, flag, changelog and comment
// counts, Updated). POSTing a transition that needs a screen (Done) directly -> 400 with a non-empty `errors{}` object
// naming the field ("resolution"), the way Jira reports a missing screen field. An unknown transition id, a transition
// to the current status, or a body without transition.id -> 400 with a non-empty errorMessages. PUT assignee with an
// unknown accountId -> 400. PUT /issue/{id} with any field other than the Flagged field -> 400 (only the flag is a
// light write). POST comment without a `body` object -> 400. An unknown issue -> 404 on every endpoint.
// SPEC-NOTE (faults): the existing fault options apply to the write endpoints unchanged: Throttle answers 429 with
// Retry-After, Fail answers its code before the handler runs, SetUnauthorized answers 401; none of them mutate.

import (
	"net/http"
	"testing"
	"time"
)

func wt5Server(t *testing.T, opts Options) *Server {
	t.Helper()
	fx, err := ParseFixture(DefaultFixture)
	if err != nil {
		t.Fatalf("ParseFixture(DefaultFixture): %v", err)
	}
	opts.Now = testNow
	opts.Loc = nyc
	return NewServer(t, fx, opts)
}

type wt5Snap struct {
	ID, Key, StatusID, Assignee string
	Flagged                     bool
	Changes, Comments           int
	Updated                     time.Time
}

func wt5Snapshot(s *Server, key string) wt5Snap {
	var out wt5Snap
	withDS(s, func(ds *Dataset) {
		is := issueByKey(ds, key)
		if is == nil {
			return
		}
		out = wt5Snap{ID: is.ID, Key: is.Key, StatusID: is.StatusID, Assignee: is.Assignee, Flagged: is.Flagged,
			Changes: len(is.Changelog), Comments: len(is.Comments), Updated: is.Updated}
	})
	return out
}

// wt5Pick returns the key of the first board issue in the given status that is unflagged and assigned.
func wt5Pick(t *testing.T, s *Server, statusName string) string {
	t.Helper()
	key := ""
	withDS(s, func(ds *Dataset) {
		want := statusIDByName(ds, statusName)
		for _, is := range ds.Issues {
			if is.StatusID == want && is.Assignee != "" && !is.Flagged {
				key = is.Key
				return
			}
		}
	})
	if key == "" {
		t.Fatalf("no unflagged assigned issue in status %q in the default fixture", statusName)
	}
	return key
}

// wt5TransitionID lists the transitions of the issue and returns the id of the one whose to.name is target.
func wt5TransitionID(t *testing.T, s *Server, issue, target string) string {
	t.Helper()
	code, v := get(t, s, "/rest/api/3/issue/"+issue+"/transitions?expand=transitions.fields")
	mustOK(t, code, v, "list transitions")
	for _, tr := range jarr(jget(v, "transitions")) {
		if jstr(jget(tr, "to", "name")) == target {
			return jstr(jget(tr, "id"))
		}
	}
	t.Fatalf("no transition to %q offered for %s: %v", target, issue, v)
	return ""
}

// wt5Want404 wants Jira's error shape (the mux's plain "404 page not found" for an unrouted path does not count).
func wt5Want404(t *testing.T, code int, v any) {
	t.Helper()
	if code != http.StatusNotFound || len(jarr(jget(v, "errorMessages"))) == 0 {
		t.Errorf("status %d body %v, want 404 with a non-empty errorMessages", code, v)
	}
}

func wt5Put(t *testing.T, s *Server, path string, body any) (int, any) {
	t.Helper()
	return call(t, s, http.MethodPut, path, body)
}

func wt5Unchanged(t *testing.T, s *Server, key string, before wt5Snap) {
	t.Helper()
	if after := wt5Snapshot(s, key); after != before {
		t.Errorf("a rejected write changed the dataset: before %+v, after %+v", before, after)
	}
}

func wt5AllChangeIDs(s *Server) map[string]int {
	ids := map[string]int{}
	withDS(s, func(ds *Dataset) {
		for _, is := range ds.Issues {
			for _, c := range is.Changelog {
				ids[c.ID]++
			}
		}
	})
	return ids
}

func wt5LastChange(t *testing.T, s *Server, key string) DSChange {
	t.Helper()
	var c DSChange
	var n int
	withDS(s, func(ds *Dataset) {
		is := issueByKey(ds, key)
		n = len(is.Changelog)
		if n > 0 {
			c = is.Changelog[n-1]
		}
	})
	if n == 0 {
		t.Fatalf("%s has no changelog", key)
	}
	return c
}

func wt5Now(s *Server) time.Time {
	var now time.Time
	withDS(s, func(ds *Dataset) { now = ds.Now })
	return now
}

func wt5CheckNewChange(t *testing.T, s *Server, key string, before wt5Snap, fieldID, field, from, to string) {
	t.Helper()
	after := wt5Snapshot(s, key)
	if after.Changes != before.Changes+1 {
		t.Fatalf("changelog length = %d, want %d (exactly one appended entry)", after.Changes, before.Changes+1)
	}
	c := wt5LastChange(t, s, key)
	if c.FieldID != fieldID || c.Field != field || c.From != from || c.To != to {
		t.Errorf("new change = {FieldID %q Field %q From %q To %q}, want {%q %q %q %q}",
			c.FieldID, c.Field, c.From, c.To, fieldID, field, from, to)
	}
	if now := wt5Now(s); !c.At.Equal(now) {
		t.Errorf("new change At = %v, want Dataset.Now %v", c.At, now)
	}
	if !after.Updated.Equal(wt5Now(s)) {
		t.Errorf("issue Updated = %v, want Dataset.Now %v", after.Updated, wt5Now(s))
	}
	if c.ID == "" {
		t.Errorf("new change has an empty id")
	}
	if n := wt5AllChangeIDs(s)[c.ID]; n != 1 {
		t.Errorf("new change id %q occurs %d times in the dataset, want exactly once", c.ID, n)
	}
	// A refresh must see it through the existing bulkfetch endpoint.
	code, v := post(t, s, "/rest/api/3/changelog/bulkfetch", map[string]any{
		"issueIdsOrKeys": []string{key}, "fieldIds": []string{fieldID}, "maxResults": 1000,
	})
	mustOK(t, code, v, "bulkfetch after write")
	found := false
	for _, lg := range jarr(jget(v, "issueChangeLogs")) {
		for _, h := range jarr(jget(lg, "changeHistories")) {
			if jstr(jget(h, "id")) == c.ID {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("changelog/bulkfetch does not return the new change %q: %v", c.ID, v)
	}
}

// ── transitions: list ───────────────────────────────────────────────────────────────────────────

func TestTransitionsList_OffersEveryOtherStatusWithScreenFlags(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	snap := wt5Snapshot(s, key)
	var statusNames map[string]string // id -> name
	withDS(s, func(ds *Dataset) {
		statusNames = map[string]string{}
		for _, st := range ds.Statuses {
			statusNames[st.ID] = st.Name
		}
	})

	for _, ref := range []string{snap.ID, key} { // by id and by key
		code, v := get(t, s, "/rest/api/3/issue/"+ref+"/transitions?expand=transitions.fields")
		mustOK(t, code, v, "GET transitions "+ref)
		trs := jarr(jget(v, "transitions"))
		if len(trs) != len(statusNames)-1 {
			t.Fatalf("%s: %d transitions, want one per other status (%d)", ref, len(trs), len(statusNames)-1)
		}
		seenTo, seenID := map[string]bool{}, map[string]bool{}
		for _, tr := range trs {
			id, name := jstr(jget(tr, "id")), jstr(jget(tr, "name"))
			toID, toName := jstr(jget(tr, "to", "id")), jstr(jget(tr, "to", "name"))
			if id == "" || name == "" || seenID[id] {
				t.Errorf("transition id %q name %q: want non-empty and unique", id, name)
			}
			seenID[id] = true
			if toID == snap.StatusID {
				t.Errorf("transition %q goes to the current status", name)
			}
			if statusNames[toID] != toName || toName == "" {
				t.Errorf("transition %q to {id %q name %q}: not a dataset status", name, toID, toName)
			}
			seenTo[toName] = true
			if avail, ok := jget(tr, "isAvailable").(bool); !ok || !avail {
				t.Errorf("transition to %q: isAvailable = %v, want true", toName, jget(tr, "isAvailable"))
			}
			hasScreen, _ := jget(tr, "hasScreen").(bool)
			fields, _ := jget(tr, "fields").(map[string]any)
			switch toName {
			case "Done":
				if !hasScreen {
					t.Errorf("transition to Done: hasScreen = false, want true")
				}
				if jget(fields, "resolution", "required") != true || jget(fields, "resolution", "hasDefaultValue") != false {
					t.Errorf("transition to Done: fields = %v, want resolution required without a default", fields)
				}
			case "Code Review":
				if !hasScreen || len(fields) == 0 {
					t.Errorf("transition to Code Review: hasScreen %v fields %v, want a screen with optional fields", hasScreen, fields)
				}
				for fid, f := range fields {
					if jget(f, "required") != false {
						t.Errorf("Code Review field %q: required = %v, want false", fid, jget(f, "required"))
					}
				}
			default:
				if hasScreen || len(fields) != 0 {
					t.Errorf("transition to %q: hasScreen %v fields %v, want no screen", toName, hasScreen, fields)
				}
			}
		}
		for id, name := range statusNames {
			if id != snap.StatusID && !seenTo[name] {
				t.Errorf("%s: no transition to %q", ref, name)
			}
		}
	}
	wt5Unchanged(t, s, key, snap)
}

func TestTransitionsList_UnknownIssue_404(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	code, v := get(t, s, "/rest/api/3/issue/99999999/transitions?expand=transitions.fields")
	wt5Want404(t, code, v)
}

// ── transitions: perform ────────────────────────────────────────────────────────────────────────

func TestTransitionPost_NoScreen_ChangesStatusAppendsChangelog(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	id := wt5TransitionID(t, s, key, "QA")
	var qa string
	withDS(s, func(ds *Dataset) { qa = statusIDByName(ds, "QA") })

	code, v := post(t, s, "/rest/api/3/issue/"+before.ID+"/transitions", map[string]any{"transition": map[string]any{"id": id}})
	if code != http.StatusNoContent {
		t.Fatalf("POST transition: status = %d (%v), want 204", code, v)
	}
	after := wt5Snapshot(s, key)
	if after.StatusID != qa {
		t.Errorf("StatusID = %q, want QA %q", after.StatusID, qa)
	}
	wt5CheckNewChange(t, s, key, before, "status", "status", before.StatusID, qa)

	// The workflow now offers the old status and no longer the new one.
	_ = wt5TransitionID(t, s, key, "In Progress")
	_, v = get(t, s, "/rest/api/3/issue/"+key+"/transitions?expand=transitions.fields")
	for _, tr := range jarr(jget(v, "transitions")) {
		if jstr(jget(tr, "to", "name")) == "QA" {
			t.Errorf("transitions still offer the status the issue is now in")
		}
	}
}

func TestTransitionPost_OptionalOnlyScreen_IsAccepted(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	id := wt5TransitionID(t, s, key, "Code Review")
	var cr string
	withDS(s, func(ds *Dataset) { cr = statusIDByName(ds, "Code Review") })

	code, v := post(t, s, "/rest/api/3/issue/"+before.ID+"/transitions", map[string]any{"transition": map[string]any{"id": id}})
	if code != http.StatusNoContent {
		t.Fatalf("POST optional-only-screen transition: status = %d (%v), want 204", code, v)
	}
	wt5CheckNewChange(t, s, key, before, "status", "status", before.StatusID, cr)
}

func TestTransitionPost_RequiredScreenFieldMissing_400AndUnchanged(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	id := wt5TransitionID(t, s, key, "Done")

	code, v := post(t, s, "/rest/api/3/issue/"+before.ID+"/transitions", map[string]any{"transition": map[string]any{"id": id}})
	if code != http.StatusBadRequest {
		t.Fatalf("POST needs-screen transition: status = %d (%v), want 400", code, v)
	}
	errs, _ := jget(v, "errors").(map[string]any)
	if _, ok := errs["resolution"]; !ok {
		t.Errorf("errors = %v, want an entry for the missing field \"resolution\"", errs)
	}
	wt5Unchanged(t, s, key, before)
}

func TestTransitionPost_BadRequests_400AndUnchanged(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	cases := []struct {
		name string
		body any
	}{
		{"unknown transition id", map[string]any{"transition": map[string]any{"id": "no-such-transition"}}},
		{"no transition object", map[string]any{}},
		{"transition without id", map[string]any{"transition": map[string]any{}}},
	}
	for _, c := range cases {
		sub(t, c.name, func(t *testing.T) {
			code, v := post(t, s, "/rest/api/3/issue/"+before.ID+"/transitions", c.body)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d (%v), want 400", code, v)
			}
			if len(jarr(jget(v, "errorMessages"))) == 0 {
				t.Errorf("errorMessages is empty: %v", v)
			}
			wt5Unchanged(t, s, key, before)
		})
	}
}

func TestTransitionPost_UnknownIssue_404(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	code, v := post(t, s, "/rest/api/3/issue/99999999/transitions", map[string]any{"transition": map[string]any{"id": "1"}})
	wt5Want404(t, code, v)
}

// ── assign ──────────────────────────────────────────────────────────────────────────────────────

func TestAssignPut_User_SetsAssigneeAppendsChangelog(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	target := ""
	withDS(s, func(ds *Dataset) {
		for _, u := range ds.Users {
			if u.AccountID != before.Assignee {
				target = u.AccountID
				return
			}
		}
	})
	code, v := wt5Put(t, s, "/rest/api/3/issue/"+before.ID+"/assignee", map[string]any{"accountId": target})
	if code != http.StatusNoContent {
		t.Fatalf("PUT assignee: status = %d (%v), want 204", code, v)
	}
	if after := wt5Snapshot(s, key); after.Assignee != target {
		t.Errorf("Assignee = %q, want %q", after.Assignee, target)
	}
	wt5CheckNewChange(t, s, key, before, "assignee", "assignee", before.Assignee, target)
}

func TestAssignPut_NullAccountId_Unassigns(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "To Do")
	before := wt5Snapshot(s, key)
	code, v := wt5Put(t, s, "/rest/api/3/issue/"+before.ID+"/assignee", map[string]any{"accountId": nil})
	if code != http.StatusNoContent {
		t.Fatalf("PUT assignee null: status = %d (%v), want 204", code, v)
	}
	if after := wt5Snapshot(s, key); after.Assignee != "" {
		t.Errorf("Assignee = %q, want unassigned", after.Assignee)
	}
	wt5CheckNewChange(t, s, key, before, "assignee", "assignee", before.Assignee, "")
}

func TestAssignPut_UnknownAccount_400AndUnchanged(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	code, v := wt5Put(t, s, "/rest/api/3/issue/"+before.ID+"/assignee", map[string]any{"accountId": "u-nobody-here"})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d (%v), want 400", code, v)
	}
	wt5Unchanged(t, s, key, before)
}

func TestAssignPut_UnknownIssue_404(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	code, v := wt5Put(t, s, "/rest/api/3/issue/99999999/assignee", map[string]any{"accountId": nil})
	wt5Want404(t, code, v)
}

// ── flag ────────────────────────────────────────────────────────────────────────────────────────

func TestFlagPut_ImpedimentThenNull_FlagsAndUnflags(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	path := "/rest/api/3/issue/" + before.ID

	code, v := wt5Put(t, s, path, map[string]any{"fields": map[string]any{FlaggedFieldID: []any{map[string]any{"value": "Impediment"}}}})
	if code != http.StatusNoContent {
		t.Fatalf("PUT flag: status = %d (%v), want 204", code, v)
	}
	if after := wt5Snapshot(s, key); !after.Flagged {
		t.Errorf("Flagged = false after the flag write")
	}
	wt5CheckNewChange(t, s, key, before, FlaggedFieldID, "Flagged", "", "Impediment")

	flagged := wt5Snapshot(s, key)
	code, v = wt5Put(t, s, path, map[string]any{"fields": map[string]any{FlaggedFieldID: nil}})
	if code != http.StatusNoContent {
		t.Fatalf("PUT unflag: status = %d (%v), want 204", code, v)
	}
	if after := wt5Snapshot(s, key); after.Flagged {
		t.Errorf("Flagged = true after the unflag write")
	}
	wt5CheckNewChange(t, s, key, flagged, FlaggedFieldID, "Flagged", "Impediment", "")
}

func TestFlagPut_OtherField_400AndUnchanged(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	code, v := wt5Put(t, s, "/rest/api/3/issue/"+before.ID, map[string]any{"fields": map[string]any{"summary": "renamed"}})
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d (%v), want 400 (only the Flagged field is writable)", code, v)
	}
	wt5Unchanged(t, s, key, before)
}

func TestFlagPut_UnknownIssue_404(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	code, v := wt5Put(t, s, "/rest/api/3/issue/99999999", map[string]any{"fields": map[string]any{FlaggedFieldID: nil}})
	wt5Want404(t, code, v)
}

// ── comment ─────────────────────────────────────────────────────────────────────────────────────

func wt5ADF(text string) map[string]any {
	return map[string]any{"body": map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}},
	}}}
}

func TestCommentPost_Creates201WithIdAndListsIt(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	_, me := get(t, s, "/rest/api/3/myself")
	myID := jstr(jget(me, "accountId"))
	_, listBefore := get(t, s, "/rest/api/3/issue/"+before.ID+"/comment")
	totalBefore := jnum(jget(listBefore, "total"))

	code, v := post(t, s, "/rest/api/3/issue/"+before.ID+"/comment", wt5ADF("Looks good"))
	if code != http.StatusCreated {
		t.Fatalf("POST comment: status = %d (%v), want 201", code, v)
	}
	newID := jstr(jget(v, "id"))
	if newID == "" {
		t.Fatalf("response has no id: %v", v)
	}
	after := wt5Snapshot(s, key)
	if after.Comments != before.Comments+1 {
		t.Fatalf("comment count = %d, want %d", after.Comments, before.Comments+1)
	}
	var last DSComment
	seen := map[string]int{}
	withDS(s, func(ds *Dataset) {
		is := issueByKey(ds, key)
		last = is.Comments[len(is.Comments)-1]
		for _, i := range ds.Issues {
			for _, c := range i.Comments {
				seen[c.ID]++
			}
		}
	})
	if last.ID != newID {
		t.Errorf("last comment id = %q, want the response id %q", last.ID, newID)
	}
	if seen[newID] != 1 {
		t.Errorf("comment id %q occurs %d times across the dataset, want once", newID, seen[newID])
	}
	if last.Author != myID {
		t.Errorf("comment author = %q, want the /myself account %q", last.Author, myID)
	}
	if !last.At.Equal(wt5Now(s)) || !after.Updated.Equal(wt5Now(s)) {
		t.Errorf("comment At %v / issue Updated %v, want Dataset.Now %v", last.At, after.Updated, wt5Now(s))
	}
	if after.Changes != before.Changes || after.StatusID != before.StatusID {
		t.Errorf("a comment must not touch status or changelog: before %+v after %+v", before, after)
	}
	_, listAfter := get(t, s, "/rest/api/3/issue/"+before.ID+"/comment")
	if got := jnum(jget(listAfter, "total")); got != totalBefore+1 {
		t.Errorf("comment list total = %d, want %d", got, totalBefore+1)
	}
	found := false
	for _, c := range jarr(jget(listAfter, "comments")) {
		if jstr(jget(c, "id")) == newID {
			found = true
		}
	}
	if !found {
		t.Errorf("GET comments does not list the new comment %q", newID)
	}
}

func TestCommentPost_WithoutBody_400AndUnchanged(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	before := wt5Snapshot(s, key)
	for name, body := range map[string]any{"empty object": map[string]any{}, "null body": map[string]any{"body": nil}} {
		sub(t, name, func(t *testing.T) {
			code, v := post(t, s, "/rest/api/3/issue/"+before.ID+"/comment", body)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d (%v), want 400", code, v)
			}
			wt5Unchanged(t, s, key, before)
		})
	}
}

func TestCommentPost_UnknownIssue_404(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	code, v := post(t, s, "/rest/api/3/issue/99999999/comment", wt5ADF("x"))
	wt5Want404(t, code, v)
}

// ── log and faults ──────────────────────────────────────────────────────────────────────────────

func TestWrites_RequestLogRecordsMethodPathAndStatus(t *testing.T) {
	defer guard(t)
	s := wt5Server(t, Options{})
	key := wt5Pick(t, s, "In Progress")
	snap := wt5Snapshot(s, key)
	s.ResetRequests()
	wt5Put(t, s, "/rest/api/3/issue/"+snap.ID+"/assignee", map[string]any{"accountId": nil})
	post(t, s, "/rest/api/3/issue/"+snap.ID+"/comment", wt5ADF("hello"))
	wt5Put(t, s, "/rest/api/3/issue/"+snap.ID, map[string]any{"fields": map[string]any{FlaggedFieldID: nil}})
	log := waitLog(t, s, 3)
	want := []Request{
		{Method: "PUT", Path: "/rest/api/3/issue/" + snap.ID + "/assignee", Status: 204},
		{Method: "POST", Path: "/rest/api/3/issue/" + snap.ID + "/comment", Status: 201},
		{Method: "PUT", Path: "/rest/api/3/issue/" + snap.ID, Status: 204},
	}
	if len(log) != len(want) {
		t.Fatalf("log has %d entries, want %d: %+v", len(log), len(want), log)
	}
	for i, w := range want {
		if log[i].Method != w.Method || log[i].Path != w.Path || log[i].Status != w.Status {
			t.Errorf("log[%d] = %s %s %d, want %s %s %d", i, log[i].Method, log[i].Path, log[i].Status, w.Method, w.Path, w.Status)
		}
	}
}

func TestWrites_FaultsApplyAndLeaveDatasetUntouched(t *testing.T) {
	defer guard(t)
	probe := func(t *testing.T, s *Server, snap wt5Snap) (int, http.Header) {
		t.Helper()
		code, hdr, _ := rawCall(t, s, http.MethodPut, "/rest/api/3/issue/"+snap.ID+"/assignee", map[string]any{"accountId": nil}, basicAuth(Token))
		return code, hdr
	}
	t.Run("throttle answers 429 with Retry-After", func(t *testing.T) {
		defer guard(t)
		s := wt5Server(t, Options{Throttle: &Throttle{Every: 1, RetryAfter: 3 * time.Second}})
		key := wt5Pick(t, s, "In Progress")
		snap := wt5Snapshot(s, key)
		code, hdr := probe(t, s, snap)
		if code != http.StatusTooManyRequests || hdr.Get("Retry-After") != "3" {
			t.Errorf("status %d Retry-After %q, want 429 and \"3\"", code, hdr.Get("Retry-After"))
		}
		wt5Unchanged(t, s, key, snap)
	})
	t.Run("fail injects its code on write paths, then recovers after Count", func(t *testing.T) {
		defer guard(t)
		s := wt5Server(t, Options{Fail: []FailStatus{{PathPrefix: "/rest/api/3/issue/", Code: 503, Count: 1}}})
		key := wt5Pick(t, s, "In Progress")
		snap := wt5Snapshot(s, key)
		if code, _ := probe(t, s, snap); code != http.StatusServiceUnavailable {
			t.Fatalf("first write: status = %d, want injected 503", code)
		}
		wt5Unchanged(t, s, key, snap)
		if code, _ := probe(t, s, snap); code != http.StatusNoContent {
			t.Errorf("second write: status = %d, want 204 once the Count is used up", code)
		}
	})
	t.Run("unauthorized flips writes to 401", func(t *testing.T) {
		defer guard(t)
		s := wt5Server(t, Options{})
		key := wt5Pick(t, s, "In Progress")
		snap := wt5Snapshot(s, key)
		s.SetUnauthorized(true)
		if code, _ := probe(t, s, snap); code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", code)
		}
		wt5Unchanged(t, s, key, snap)
	})
	t.Run("bad token on a write is 401", func(t *testing.T) {
		defer guard(t)
		s := wt5Server(t, Options{})
		key := wt5Pick(t, s, "In Progress")
		snap := wt5Snapshot(s, key)
		code, _, _ := rawCall(t, s, http.MethodPost, "/rest/api/3/issue/"+snap.ID+"/comment", wt5ADF("x"), basicAuth("wrong"))
		if code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", code)
		}
		wt5Unchanged(t, s, key, snap)
	})
}

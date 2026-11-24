package jiratest

// WP-T6 tests: the two F12 fault options (Options.RefreshFails, Options.StaleChangelog; jira-sync.md §6 "Fault
// injection", docs/wp-notes/F12-freeze.md "Amendments before WP-T6").
//
// SPEC-NOTE (RefreshFails): GET /rest/api/3/issue/{id} answers 500 (Jira's error shape) for an issue that has had a
// successful write (transition, assign, flag, comment) since the server started. Other issues, other endpoints
// (search, comments, changelog) and issues that were never written are unaffected. Without the option nothing changes.
// A rejected write does not arm it. The fault stays armed for that issue (it does not clear by itself).
// SPEC-NOTE (StaleChangelog): the write applies as usual (dataset, status code), but POST changelog/bulkfetch leaves out
// the entries those writes created until the next Mutate call, which serves them again. Older entries and other issues'
// entries are unaffected. A comment write adds no changelog entry, so there is nothing to hide for it.
// SPEC-NOTE (Mutate): Mutate is the only thing that re-serves the entries, so these tests read the dataset through
// Dataset() instead of Mutate.
// SPEC-NOTE (both): the options work together; each write appends exactly the one DSChange the T5 handlers always did.

import (
	"net/http"
	"testing"
)

func f12Assign(t *testing.T, s *Server, key, to string) {
	t.Helper()
	code, v := wt5Put(t, s, "/rest/api/3/issue/"+key+"/assignee", map[string]any{"accountId": to})
	if code != http.StatusNoContent {
		t.Fatalf("assign %s: %d %v", key, code, v)
	}
}

func f12Changes(t *testing.T, s *Server, id string) []string {
	t.Helper()
	code, v := post(t, s, "/rest/api/3/changelog/bulkfetch", map[string]any{"issueIdsOrKeys": []string{id}})
	mustOK(t, code, v, "bulkfetch")
	var ids []string
	for _, log := range jarr(jget(v, "issueChangeLogs")) {
		for _, h := range jarr(jget(log, "changeHistories")) {
			ids = append(ids, jstr(jget(h, "id")))
		}
	}
	return ids
}

func f12Has(list []string, id string) bool {
	for _, x := range list {
		if x == id {
			return true
		}
	}
	return false
}

func TestRefreshFails_IssueGetAnswers500OnlyAfterASuccessfulWriteToThatIssue(t *testing.T) {
	guard(t)
	s := wt5Server(t, Options{RefreshFails: true})
	a := wt5Pick(t, s, "In Progress")
	var b string
	withDS(s, func(ds *Dataset) {
		for _, is := range ds.Issues {
			if is.Key != a && is.Assignee != "" {
				b = is.Key
				return
			}
		}
	})

	if code, v := get(t, s, "/rest/api/3/issue/"+a); code != http.StatusOK {
		t.Fatalf("before any write: GET issue = %d %v, want 200", code, v)
	}
	f12Assign(t, s, a, "u-iris")
	code, v := get(t, s, "/rest/api/3/issue/"+a)
	if code != http.StatusInternalServerError || len(jarr(jget(v, "errorMessages"))) == 0 {
		t.Errorf("after the write: GET issue = %d %v, want 500 with Jira's error shape", code, v)
	}
	if code, _ := get(t, s, "/rest/api/3/issue/"+b); code != http.StatusOK {
		t.Errorf("an issue that was never written: GET = %d, want 200", code)
	}
	if code, _ := get(t, s, "/rest/api/3/issue/"+a+"/comment"); code != http.StatusOK {
		t.Errorf("comments of the written issue: %d, want 200 (only GET issue fails)", code)
	}
	if code, _ := post(t, s, "/rest/api/3/changelog/bulkfetch", map[string]any{"issueIdsOrKeys": []string{a}}); code != http.StatusOK {
		t.Errorf("bulkfetch: %d, want 200", code)
	}
	// The numeric id reaches the same issue.
	if code, _ := get(t, s, "/rest/api/3/issue/"+issueID(t, s, a)); code != http.StatusInternalServerError {
		t.Errorf("GET by numeric id after the write = %d, want 500", code)
	}
}

func TestRefreshFails_ARejectedWriteDoesNotArmItAndWithoutTheOptionNothingFails(t *testing.T) {
	guard(t)
	s := wt5Server(t, Options{RefreshFails: true})
	key := wt5Pick(t, s, "In Progress")
	if code, _ := wt5Put(t, s, "/rest/api/3/issue/"+key+"/assignee", map[string]any{"accountId": "no-such-user"}); code != http.StatusBadRequest {
		t.Fatalf("rejected assign = %d, want 400", code)
	}
	if code, _ := get(t, s, "/rest/api/3/issue/"+key); code != http.StatusOK {
		t.Errorf("GET issue after a rejected write = %d, want 200", code)
	}

	plain := wt5Server(t, Options{})
	k2 := wt5Pick(t, plain, "In Progress")
	f12Assign(t, plain, k2, "u-iris")
	if code, _ := get(t, plain, "/rest/api/3/issue/"+k2); code != http.StatusOK {
		t.Errorf("without RefreshFails: GET issue after a write = %d, want 200", code)
	}
}

func TestStaleChangelog_BulkfetchOmitsTheWritesEntryUntilTheNextMutate(t *testing.T) {
	guard(t)
	s := wt5Server(t, Options{StaleChangelog: true})
	key := wt5Pick(t, s, "In Progress")
	id := issueID(t, s, key)
	older := f12Changes(t, s, id)

	f12Assign(t, s, key, "u-iris")
	entry := f12LastChange(s, key)
	if entry.ID == "" || !f12Has(f12AllChangeIDs(s), entry.ID) {
		t.Fatalf("the write did not append its changelog entry to the dataset: %+v", entry)
	}
	now := f12Changes(t, s, id)
	if f12Has(now, entry.ID) {
		t.Errorf("bulkfetch served the stale entry %s: %v", entry.ID, now)
	}
	if len(now) != len(older) {
		t.Errorf("bulkfetch entries = %v, want the %d older ones only", now, len(older))
	}
	if code, v := get(t, s, "/rest/api/3/issue/"+key); code != http.StatusOK || jstr(jget(v, "fields", "assignee", "accountId")) != "u-iris" {
		t.Errorf("GET issue = %d %v, want 200 with the new assignee (only the changelog lags)", code, v)
	}

	s.Mutate(func(ds *Dataset) {})
	if after := f12Changes(t, s, id); !f12Has(after, entry.ID) {
		t.Errorf("after Mutate bulkfetch still omits %s: %v", entry.ID, after)
	}
}

func TestStaleChangelog_ArmsOnlyForTheWrittenIssueAndOnlyWithTheOption(t *testing.T) {
	guard(t)
	s := wt5Server(t, Options{StaleChangelog: true})
	a := wt5Pick(t, s, "In Progress")
	var b string
	withDS(s, func(ds *Dataset) {
		for _, is := range ds.Issues {
			if is.Key != a && is.Assignee != "" && len(is.Changelog) > 0 {
				b = is.Key
				return
			}
		}
	})
	bBefore := f12Changes(t, s, issueID(t, s, b))
	f12Assign(t, s, a, "u-iris")
	if got := f12Changes(t, s, issueID(t, s, b)); len(got) != len(bBefore) {
		t.Errorf("another issue's changelog changed: %v vs %v", got, bBefore)
	}

	plain := wt5Server(t, Options{})
	k := wt5Pick(t, plain, "In Progress")
	f12Assign(t, plain, k, "u-iris")
	if got := f12Changes(t, plain, issueID(t, plain, k)); !f12Has(got, f12LastChange(plain, k).ID) {
		t.Errorf("without StaleChangelog the new entry must be served: %v", got)
	}
}

func TestBothF12Faults_WorkTogether(t *testing.T) {
	guard(t)
	s := wt5Server(t, Options{RefreshFails: true, StaleChangelog: true})
	key := wt5Pick(t, s, "In Progress")
	id := issueID(t, s, key)
	f12Assign(t, s, key, "u-iris")
	if code, _ := get(t, s, "/rest/api/3/issue/"+key); code != http.StatusInternalServerError {
		t.Errorf("GET issue = %d, want 500", code)
	}
	if f12Has(f12Changes(t, s, id), f12LastChange(s, key).ID) {
		t.Errorf("the stale entry was served")
	}
}

// f12LastChange and f12AllChangeIDs read the dataset without Mutate (which would re-serve the stale entries).
func f12LastChange(s *Server, key string) DSChange {
	for _, is := range s.Dataset().Issues {
		if is.Key == key && len(is.Changelog) > 0 {
			return is.Changelog[len(is.Changelog)-1]
		}
	}
	return DSChange{}
}

func f12AllChangeIDs(s *Server) []string {
	var out []string
	for _, is := range s.Dataset().Issues {
		for _, c := range is.Changelog {
			out = append(out, c.ID)
		}
	}
	return out
}

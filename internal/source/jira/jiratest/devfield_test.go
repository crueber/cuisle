package jiratest

// The Development summary field (jira-sync §2.11): registry entry, per-issue value in search results derived from the
// same data as the dev-status summary endpoint, one stale issue in the default fixture, and the NoDevField option.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func devFieldOf(t *testing.T, s *Server, key string) (string, bool) {
	t.Helper()
	code, v := post(t, s, "/rest/api/3/search/jql", map[string]any{
		"jql": "id in (" + issueID(t, s, key) + ")", "fields": []string{"updated", DevSummaryFieldID},
	})
	mustOK(t, code, v, "search")
	issues, _ := jget(v, "issues").([]any)
	if len(issues) != 1 {
		t.Fatalf("search for %s returned %d issues", key, len(issues))
	}
	f, _ := jget(issues[0], "fields").(map[string]any)
	raw, ok := f[DevSummaryFieldID]
	if !ok {
		return "", false
	}
	s2, _ := raw.(string)
	return s2, true
}

func TestDevSummaryField_IsInTheRegistryAndOmittedWithNoDevField(t *testing.T) {
	defer guard(t)
	for _, no := range []bool{false, true} {
		s := newTiny(t, Options{NoDevField: no})
		code, v := get(t, s, "/rest/api/3/field")
		mustOK(t, code, v, "field")
		found := false
		for _, f := range v.([]any) {
			if jstr(jget(f, "id")) == DevSummaryFieldID {
				found = strings.HasSuffix(jstr(jget(f, "schema", "custom")), ":devsummarycf")
			}
		}
		if found == no {
			t.Errorf("NoDevField=%v: registry has the development field = %v", no, found)
		}
		_, present := devFieldOf(t, s, "MOB-1")
		if present == no {
			t.Errorf("NoDevField=%v: search result carries the development field = %v", no, present)
		}
	}
}

func TestDevSummaryField_EmbedsTheSameSummaryTheEndpointServes(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	val, ok := devFieldOf(t, s, "MOB-1")
	if !ok {
		t.Fatal("no development field on MOB-1")
	}
	i := strings.Index(val, "json=")
	if i < 0 {
		t.Fatalf("value %q has no json=", val)
	}
	var v struct {
		CachedValue struct {
			Errors  []any          `json:"errors"`
			Summary map[string]any `json:"summary"`
		} `json:"cachedValue"`
		IsStale bool `json:"isStale"`
	}
	if err := json.NewDecoder(strings.NewReader(val[i+5:])).Decode(&v); err != nil {
		t.Fatalf("embedded json: %v", err)
	}
	if v.IsStale || len(v.CachedValue.Errors) != 0 {
		t.Errorf("fresh issue served stale=%v errors=%v", v.IsStale, v.CachedValue.Errors)
	}
	code, sum := get(t, s, "/rest/dev-status/latest/issue/summary?issueId="+issueID(t, s, "MOB-1"))
	mustOK(t, code, sum, "dev summary")
	// compare through JSON so numbers and nesting have one shape
	var want map[string]any
	b, _ := json.Marshal(jget(sum, "summary"))
	_ = json.Unmarshal(b, &want)
	if !reflect.DeepEqual(v.CachedValue.Summary, want) {
		t.Errorf("field summary = %v\nendpoint summary = %v", v.CachedValue.Summary, want)
	}
}

func TestDevSummaryField_NoActivityIsEmptyObjectAndTheStaleIssueIsFlagged(t *testing.T) {
	defer guard(t)
	s := newTiny(t, Options{})
	if val, _ := devFieldOf(t, s, "MOB-2"); val != "{}" { // MOB-2 has no commits or pull requests
		t.Errorf("no-activity value = %q, want {}", val)
	}
	withDS(s, func(ds *Dataset) { issueByKey(ds, "MOB-1").DevStale = true })
	val, _ := devFieldOf(t, s, "MOB-1")
	if !strings.Contains(val, `"isStale":true`) {
		t.Errorf("DevStale issue served %q, want isStale true", val)
	}
}

func TestDevSummaryField_DefaultFixtureServesExactlyOneStaleIssue(t *testing.T) {
	defer guard(t)
	fx, err := ParseFixture(DefaultFixture)
	if err != nil {
		t.Fatal(err)
	}
	ds := Materialize(fx, testNow, nil)
	stale := 0
	for _, is := range ds.Issues {
		if is.DevStale {
			stale++
			if is.Key != StaleDevKey {
				t.Errorf("stale issue %s, want %s", is.Key, StaleDevKey)
			}
		}
	}
	if stale != 1 {
		t.Errorf("stale issues = %d, want 1", stale)
	}
}

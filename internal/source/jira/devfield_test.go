package jira

// Tests for devfield.go and the scan-carried dev fingerprint (jira-sync.md §2.11): the Development summary custom field
// in search results yields the same fingerprint as the dev-status summary endpoint, so DevActivity can skip the
// per-ticket summary request. Harness and conventions: see scan_test.go and dev_test.go.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

const wp19DevField = "customfield_10000"

// devFieldString wraps a dev-status summary body the way search results embed it: a string with the JSON after "json=".
func devFieldString(t *testing.T, summaryBody string, stale bool, errs string) json.RawMessage {
	t.Helper()
	var sum struct {
		Summary json.RawMessage `json:"summary"`
	}
	if err := json.Unmarshal([]byte(summaryBody), &sum); err != nil {
		t.Fatal(err)
	}
	if errs == "" {
		errs = "[]"
	}
	cached := `{"cachedValue":{"errors":` + errs + `,"summary":` + string(sum.Summary) + `},"isStale":` + map[bool]string{true: "true", false: "false"}[stale] + `}`
	s := `{pullrequest={dataType=pullrequest, state=MERGED, stateCount=1}, repository={dataType=repository, count=3}, json=` + cached + `}`
	b, _ := json.Marshal(s)
	return b
}

func TestParseDevField_FingerprintEqualsTheSummaryEndpointsFingerprint(t *testing.T) {
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub", "bitbucket"})
	h, ok := parseDevField(devFieldString(t, summary, false, ""))
	if !ok {
		t.Fatal("a fresh field must parse")
	}
	if want := DevFingerprint(3, wp19RepoLast, 1, wp19PRLast, "MERGED"); h.fingerprint != want {
		t.Errorf("fingerprint = %s, want %s (the summary-based one)", h.fingerprint, want)
	}
	if strings.Join(h.repoTypes, ",") != "GitHub" || strings.Join(h.prTypes, ",") != "GitHub,bitbucket" {
		t.Errorf("instance types = %v / %v, want [GitHub] / [GitHub bitbucket] (sorted keys, passed back verbatim)", h.repoTypes, h.prTypes)
	}
}

func TestParseDevField_NoActivityIsTheZeroFingerprint(t *testing.T) {
	zero := DevFingerprint(0, "", 0, "", "")
	for name, raw := range map[string]string{"empty object": `"{}"`, "null": `null`, "blank": `""`} {
		h, ok := parseDevField(json.RawMessage(raw))
		if !ok || h.fingerprint != zero || len(h.repoTypes)+len(h.prTypes) != 0 {
			t.Errorf("%s: hint %+v ok=%v, want the zero-activity fingerprint %s and no types", name, h, ok, zero)
		}
	}
}

func TestParseDevField_UntrustworthyValuesAreUnknown(t *testing.T) {
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	for name, raw := range map[string]json.RawMessage{
		"stale":              devFieldString(t, summary, true, ""),
		"errors reported":    devFieldString(t, summary, false, `[{"message":"boom"}]`),
		"not a string":       json.RawMessage(`{"count":3}`),
		"no embedded json":   json.RawMessage(`"{pullrequest={dataType=pullrequest}}"`),
		"malformed json":     json.RawMessage(`"{json={\"cachedValue\":{\"summary\":"`),
		"no summary":         json.RawMessage(`"{json={\"cachedValue\":{\"errors\":[]},\"isStale\":false}}"`),
		"no cached value":    json.RawMessage(`"{json={\"isStale\":false}}"`),
		"json that is a num": json.RawMessage(`"{json=42}"`),
		"empty raw":          json.RawMessage(``),
	} {
		if h, ok := parseDevField(raw); ok {
			t.Errorf("%s: parsed to %+v, want unknown (the summary endpoint is the fallback)", name, h)
		}
	}
}

// scanItems runs a board-role Scan against a search response with one issue per entry of fields (nil = field absent).
func devScan(t *testing.T, meta map[string]any, fields map[string]string) (*Adapter, *wp19Srv, source.ScanResult) {
	t.Helper()
	var issues []string
	for _, id := range []string{"10001", "10002", "10003", "10004"} {
		f := `"updated":"2026-11-20T10:00:00.000+0000"`
		if v, ok := fields[id]; ok {
			f += `,"` + wp19DevField + `":` + v
		}
		issues = append(issues, `{"id":"`+id+`","key":"MOB-`+id[len(id)-1:]+`","fields":{`+f+`}}`)
	}
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Ok(`{"issues":[` + strings.Join(issues, ",") + `],"isLast":true}`),
	})
	a := newWP19Adapter(t, srv.URL, meta)
	res, err := a.Scan(context.Background(), source.Query{Name: "sprint", Role: model.RoleBoard, Expr: "filter = 1"})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return a, srv, res
}

func devMeta() map[string]any {
	m := wp19Meta()
	m["devSummaryFieldId"] = wp19DevField
	return m
}

func TestScan_RequestsTheDevelopmentFieldAndCarriesFingerprints(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	quote := func(raw json.RawMessage) string { return string(raw) }
	_, srv, res := devScan(t, devMeta(), map[string]string{
		"10001": quote(devFieldString(t, summary, false, "")),
		"10002": `"{}"`,
		"10003": quote(devFieldString(t, summary, true, "")), // stale
		// 10004: the field is absent from the response
	})
	body := srv.all()[0].json(t)
	fields, _ := body["fields"].([]any)
	if len(fields) != 2 || fields[0] != "updated" || fields[1] != wp19DevField {
		t.Errorf("scan fields = %v, want [updated %s]", fields, wp19DevField)
	}
	want := map[string]string{
		"10001": DevFingerprint(3, wp19RepoLast, 1, wp19PRLast, "MERGED"),
		"10002": DevFingerprint(0, "", 0, "", ""),
		"10003": "", // stale → unknown
		"10004": "", // absent → unknown
	}
	if len(res.Items) != 4 {
		t.Fatalf("items = %d, want 4", len(res.Items))
	}
	for _, it := range res.Items {
		if it.DevFingerprint != want[it.SourceID] {
			t.Errorf("item %s DevFingerprint = %q, want %q", it.SourceID, it.DevFingerprint, want[it.SourceID])
		}
		if it.UpdatedAt.IsZero() {
			t.Errorf("item %s lost its updated stamp", it.SourceID)
		}
	}
}

func TestScan_BacklogQueriesDoNotRequestTheDevelopmentField(t *testing.T) {
	defer wp19Guard(t)
	srv := newWP19Server(t, map[string]wp19Route{
		"POST /rest/api/3/search/jql": wp19Ok(`{"issues":[{"id":"1","key":"MOB-1","fields":{"updated":"2026-11-20T10:00:00.000+0000"}}],"isLast":true}`),
	})
	a := newWP19Adapter(t, srv.URL, devMeta())
	if _, err := a.Scan(context.Background(), wp19Query("filter = 1", 50, false)); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	fields, _ := srv.all()[0].json(t)["fields"].([]any)
	if len(fields) != 1 || fields[0] != "updated" {
		t.Errorf("backlog scan fields = %v, want [updated]", fields)
	}
}

func TestScan_NoDevelopmentFieldOnTheSite_RequestsOnlyUpdated(t *testing.T) {
	defer wp19Guard(t)
	_, srv, res := devScan(t, wp19Meta(), nil) // wp19Meta: field ids discovered, checked, none for development
	fields, _ := srv.all()[0].json(t)["fields"].([]any)
	if len(fields) != 1 || fields[0] != "updated" {
		t.Errorf("scan fields = %v, want [updated]", fields)
	}
	for _, it := range res.Items {
		if it.DevFingerprint != "" {
			t.Errorf("item %s DevFingerprint = %q, want empty", it.SourceID, it.DevFingerprint)
		}
	}
}

func TestScan_MetaWithoutTheDevelopmentFieldDiscoversItOnce(t *testing.T) {
	defer wp19Guard(t)
	meta := wp19Meta()
	delete(meta, "devSummaryFieldChecked") // a connection saved before this field existed
	srv := newWP19Server(t, map[string]wp19Route{
		"GET /rest/api/3/field": wp19Ok(`[
			{"id":"customfield_10020","name":"Sprint","custom":true,"schema":{"type":"array","custom":"com.pyxis.greenhopper.jira:gh-sprint"}},
			{"id":"` + wp19DevField + `","name":"Development","custom":true,"schema":{"type":"any","custom":"com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf"}}]`),
		"POST /rest/api/3/search/jql": wp19Ok(`{"issues":[],"isLast":true}`),
	})
	a := newWP19Adapter(t, srv.URL, meta)
	for range 2 {
		if _, err := a.Scan(context.Background(), source.Query{Name: "sprint", Role: model.RoleBoard, Expr: "filter = 1"}); err != nil {
			t.Fatalf("Scan: %v", err)
		}
	}
	if n := len(srv.to("GET", "/rest/api/3/field")); n != 1 {
		t.Errorf("/field requests = %d, want 1 (discovered once)", n)
	}
	m := a.Meta()
	if wp19MetaString(m, "devSummaryFieldId") != wp19DevField || m["devSummaryFieldChecked"] != true {
		t.Errorf("Meta = %v, want the development field id and the checked marker persisted", m)
	}
	if got := srv.to("POST", "/rest/api/3/search/jql")[0].json(t)["fields"]; len(got.([]any)) != 2 {
		t.Errorf("first scan fields = %v, want updated plus the development field", got)
	}
}

func TestRefFindDevSummary_MatchesBySchemaSuffix(t *testing.T) {
	var fields []refField
	if err := json.Unmarshal([]byte(`[
		{"id":"customfield_1","name":"Development","schema":{"custom":"x:something-else"}},
		{"id":"customfield_2","name":"Development","schema":{"custom":"com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf"}}]`), &fields); err != nil {
		t.Fatal(err)
	}
	if got := refFindDevSummary(fields); got != "customfield_2" {
		t.Errorf("dev summary field = %q, want customfield_2", got)
	}
	if got := refFindDevSummary(nil); got != "" {
		t.Errorf("dev summary field of an empty list = %q, want empty", got)
	}
}

// ── DevActivity with a scan hint ────────────────────────────────────────────────────────────────

// hintedAdapter scans one issue with a usable field and returns the adapter plus the field's fingerprint.
func hintedAdapter(t *testing.T, routes map[string]wp19Route, summary string) (*Adapter, *wp19Srv, string) {
	t.Helper()
	routes["POST /rest/api/3/search/jql"] = wp19Ok(`{"issues":[{"id":"10001","key":"MOB-1","fields":{"updated":"2026-11-20T10:00:00.000+0000","` +
		wp19DevField + `":` + string(devFieldString(t, summary, false, "")) + `}}],"isLast":true}`)
	srv := newWP19Server(t, routes)
	a := newWP19Adapter(t, srv.URL, devMeta())
	res, err := a.Scan(context.Background(), source.Query{Name: "sprint", Role: model.RoleBoard, Expr: "filter = 1"})
	if err != nil || len(res.Items) != 1 || res.Items[0].DevFingerprint == "" {
		t.Fatalf("Scan: %+v, %v", res, err)
	}
	return a, srv, res.Items[0].DevFingerprint
}

func TestDevActivity_ScanHintEqualToPrevious_MakesNoRequest(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	a, srv, fp := hintedAdapter(t, map[string]wp19Route{}, summary) // dev-status is unrouted: any call fails the test
	got, err := a.DevActivity(context.Background(), "10001", fp)
	if err != nil {
		t.Fatalf("DevActivity: %v", err)
	}
	if got.Changed || got.Fingerprint != fp || len(got.Events) != 0 {
		t.Errorf("DevActivity = %+v, want unchanged with fingerprint %s", got, fp)
	}
	if n := len(srv.to("GET", wp19DevSum)) + len(srv.to("GET", wp19DevDet)); n != 0 {
		t.Errorf("dev-status requests = %d, want 0", n)
	}
}

func TestDevActivity_ScanHintChanged_GoesStraightToDetailWithoutSummary(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	routes := wp19DevRoutes(t, summary)
	delete(routes, "GET "+wp19DevSum) // a summary call fails the test
	a, srv, fp := hintedAdapter(t, routes, summary)
	got, err := a.DevActivity(context.Background(), "10001", "an-older-fingerprint")
	if err != nil {
		t.Fatalf("DevActivity: %v", err)
	}
	if !got.Changed || got.Fingerprint != fp {
		t.Errorf("DevActivity = %+v, want changed with the scan fingerprint %s", got, fp)
	}
	if len(got.Events) == 0 {
		t.Error("no events from the detail calls")
	}
	if n := len(srv.to("GET", wp19DevDet)); n != 2 {
		t.Errorf("detail requests = %d, want 2 (GitHub repository and pullrequest)", n)
	}
	if v := wp19MetaString(a.Meta(), "devActivity"); v != "ok" {
		t.Errorf("Meta[devActivity] = %q, want ok after a successful detail call", v)
	}
}

func TestDevActivity_ScanHintChanged_DetailProbeFailureMarksDevUnavailable(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	a, srv, _ := hintedAdapter(t, map[string]wp19Route{"GET " + wp19DevDet: wp19Fail(404, `{"errorMessages":["nope"]}`)}, summary)
	_, err := a.DevActivity(context.Background(), "10001", "")
	if !source.IsUnsupported(err) {
		t.Fatalf("err = %v, want unsupported (the first detail call is the probe)", err)
	}
	if a.Capabilities().HasDevActivity {
		t.Error("HasDevActivity = true after the probe failed")
	}
	if v := wp19MetaString(a.Meta(), "devActivity"); v != "unavailable" {
		t.Errorf("Meta[devActivity] = %q, want unavailable", v)
	}
	before := len(srv.all())
	if _, err := a.DevActivity(context.Background(), "10001", ""); !source.IsUnsupported(err) || len(srv.all()) != before {
		t.Errorf("later call: err %v, %d new requests, want unsupported and none", err, len(srv.all())-before)
	}
}

func TestDevActivity_ScanHintIgnoredWhileDevStatusIsKnownUnavailable(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	routes := wp19DevRoutes(t, summary)
	routes["POST /rest/api/3/search/jql"] = wp19Ok(`{"issues":[{"id":"10001","key":"MOB-1","fields":{"updated":"2026-11-20T10:00:00.000+0000","` +
		wp19DevField + `":` + string(devFieldString(t, summary, false, "")) + `}}],"isLast":true}`)
	srv := newWP19Server(t, routes)
	meta := devMeta()
	meta["devActivity"] = "unavailable" // the persisted state: the re-probe must be a real request
	a := newWP19Adapter(t, srv.URL, meta)
	res, err := a.Scan(context.Background(), source.Query{Name: "sprint", Role: model.RoleBoard, Expr: "filter = 1"})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("Scan: %+v, %v", res, err)
	}
	if _, err := a.DevActivity(context.Background(), "10001", res.Items[0].DevFingerprint); err != nil {
		t.Fatalf("DevActivity: %v", err)
	}
	if n := len(srv.to("GET", wp19DevSum)); n != 1 {
		t.Errorf("summary requests = %d, want 1 (the re-probe)", n)
	}
}

func TestDevActivity_WithoutAHint_UsesTheSummaryPath(t *testing.T) {
	defer wp19Guard(t)
	summary := wp19DevSummary(t, 3, wp19RepoLast, 1, wp19PRLast, "MERGED", []string{"GitHub"}, []string{"GitHub"})
	srv := newWP19Server(t, wp19DevRoutes(t, summary))
	a := newWP19Adapter(t, srv.URL, devMeta())
	prev := DevFingerprint(3, wp19RepoLast, 1, wp19PRLast, "MERGED")
	if _, err := a.DevActivity(context.Background(), "10001", prev); err != nil {
		t.Fatalf("DevActivity: %v", err)
	}
	if n := len(srv.to("GET", wp19DevSum)); n != 1 {
		t.Errorf("summary requests = %d, want 1 (no scan hint for this issue)", n)
	}
}

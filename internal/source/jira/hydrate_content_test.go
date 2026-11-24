package jira

// WP-T4 tests for the content side of Adapter.Hydrate (jira-sync.md §2.2 value decoding, §2.8 hydrate projection,
// §2.10 comments, ADR 0015, AGENTS invariant 12). Harness: hydrate_test.go (newF12Server, f12CommentRoute) on top of
// scan_test.go (wp19). The pre-F12 expectations (projection, batch size, top-ups, progress) are updated in
// hydrate_test.go with // F12 markers.
//
// SPEC-NOTE (registry source): Hydrate decodes every field through the field registry. It obtains it with one
// GET /rest/api/3/field per Adapter, shared with Fields(): any number of Fields()/Hydrate() calls on one Adapter make
// exactly one /field request. Keys the registry does not know are decoded as kind `unknown` (raw value kept).
// SPEC-NOTE (unknown values): FieldValue.Raw is the provider value verbatim, valid JSON, so a string value is kept
// WITH its quotes (`"ACC-77"`); the view unquotes it. Text stays empty for unknown kinds. (jira-sync §2.2 "strings
// unquoted" is read as "shown without quotes", because Raw is a json.RawMessage.)
// SPEC-NOTE (value shapes): option → Text (.value, else .name); options/versions/components/labels/groups → Items;
// cascade → Items [parent, child] (no child → [parent]); user(s) → Users account ids and a person upsert into
// IssueDetail.People (display name kept, e-mail never); group/version/project/team → Text (project .name; team .name,
// else .title); paragraph → Rich{Format "adf", Raw verbatim}; number → Number (0 is a value, not empty); date → Text
// as sent ("YYYY-MM-DD"); datetime → Text that parses (RFC 3339) to the same instant (Jira's "+0000" offset may be
// normalised); url/text → Text. Empty means not set and yields NO FieldValue: null, "", [], {} and objects without a
// displayable member (an option with only an id).
// SPEC-NOTE (time tracking): `timetracking` is ONE FieldValue{FieldID "timetracking", Kind unknown} whose Raw is the
// provider object (originalEstimateSeconds, remainingEstimateSeconds, timeSpentSeconds are read from it).
// SPEC-NOTE (excluded): summary, status, assignee, issuetype, created, updated, resolutiondate, parent, comment,
// attachment, issuelinks, subtasks, description, environment, worklog, watches, votes, the sprint/rank/flagged ids,
// lastViewed, statuscategorychangedate, workratio, progress, aggregateprogress and any field whose schema.custom ends
// in :devsummarycf never appear in Content.Fields.
// SPEC-NOTE (order and hash): Content.Fields is sorted by FieldID (bytewise ascending) so the hash is deterministic.
// Content.Hash is a non-empty string that is identical for two hydrations of identical provider data and changes when
// the description, an environment, a field value, an attachment, a link, a subtask or the parent changes. Comments
// are not part of the content hash.
// SPEC-NOTE (comments): after the issue search, for EVERY hydrated issue the adapter reads GET
// /rest/api/3/issue/{id}/comment?orderBy=created&maxResults=100&startAt=n, advancing startAt by the number of comments
// received, until startAt >= total or an empty page arrives (never an endless loop). Comment{ID, At = created,
// UpdatedAt = updated (= created when absent), AuthorID, Body = RichText{Format "adf", Raw verbatim} (zero when the
// response has none), Visibility = &{type, value} (nil when absent)}. IssueDetail.CommentTotal = the endpoint's total,
// Comments = the complete list in the order read. App-account comments are kept (pulse exclusion is the cache's job);
// their authors land in People with Kind "app". The `comment` key of the search hit is ignored.
// SPEC-NOTE (people from fields): users found in user fields are added to People once.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

const (
	f12DescSentinel  = "SENTINEL-DESCRIPTION-8842"
	f12FieldSentinel = "SENTINEL-FIELD-TEXT-5531"
)

func f12Doc(text string) map[string]any {
	return map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}}
}

func f12RawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// f12SameRaw compares two JSON documents structurally.
func f12SameRaw(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

func f12RichIs(r *model.RichText, want any, t *testing.T) bool {
	if r == nil {
		return false
	}
	return r.Format == model.RichTextADF && f12SameRaw(r.Raw, f12RawJSON(t, want))
}

// f12Issue is a search hit like wp19IssueJSON but without the `comment` key (the hydrate projection excludes it),
// carrying extra fields.
func f12Issue(id string, extra map[string]any) map[string]any {
	is := wp19IssueJSON(id, wp19Iss{})
	fields := is["fields"].(map[string]any)
	delete(fields, "comment")
	for k, v := range extra {
		fields[k] = v
	}
	return is
}

func f12Hydrate(t *testing.T, srv *wp19Srv, ids ...string) []model.IssueDetail {
	t.Helper()
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(context.Background(), ids, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got) != len(ids) {
		t.Fatalf("Hydrate returned %d details, want %d", len(got), len(ids))
	}
	return got
}

func f12ContentOf(t *testing.T, d model.IssueDetail) *model.IssueContent {
	t.Helper()
	if d.Content == nil {
		t.Fatalf("IssueDetail %s has Content == nil (Capabilities.Content issues must carry content)", d.Key)
	}
	return d.Content
}

func f12ContentServer(t *testing.T, registry string, gen func(id string) map[string]any, extra map[string]wp19Route) *wp19Srv {
	t.Helper()
	routes := map[string]wp19Route{
		"POST /rest/api/3/search/jql":          wp19SearchRoute(t, gen, 0),
		"POST /rest/api/3/changelog/bulkfetch": wp19BulkRoute(t, wp19DefaultHist),
	}
	if registry != "" {
		routes["GET /rest/api/3/field"] = wp19Ok(registry)
	}
	for k, v := range extra {
		routes[k] = v
	}
	return newF12Server(t, routes)
}

// ── field values per kind ───────────────────────────────────────────────────────────────────────

func f12Ptr(f float64) *float64 { return &f }

type f12ValRow struct {
	name     string
	id       string
	schema   string // JSON
	value    any    // wire value
	want     *model.FieldValue
	wantTime time.Time // datetime rows: the instant Text must parse to
}

func f12User(id, name string) map[string]any {
	return map[string]any{"accountId": id, "displayName": name, "emailAddress": id + "@authors.example", "active": true, "accountType": "atlassian"}
}

func f12ValueRows(t *testing.T) []f12ValRow {
	cf := func(typ, custom string) string {
		return fmt.Sprintf(`{"type":%q,"custom":%q}`, typ, f12CT+custom)
	}
	arr := func(items, custom string) string {
		return fmt.Sprintf(`{"type":"array","items":%q,"custom":%q}`, items, f12CT+custom)
	}
	adf := f12Doc("Acceptance criteria")
	rows := []f12ValRow{
		{name: "text", id: "customfield_30001", schema: cf("string", "textfield"), value: "Cooking", want: &model.FieldValue{Kind: model.FieldText, Text: "Cooking"}},
		{name: "paragraph", id: "customfield_30002", schema: cf("string", "textarea"), value: adf, want: &model.FieldValue{Kind: model.FieldParagraph}},
		{name: "number", id: "customfield_30003", schema: cf("number", "float"), value: 5.5, want: &model.FieldValue{Kind: model.FieldNumber, Number: f12Ptr(5.5)}},
		{name: "number zero is set", id: "customfield_30004", schema: cf("number", "float"), value: 0, want: &model.FieldValue{Kind: model.FieldNumber, Number: f12Ptr(0)}},
		{name: "date", id: "duedate", schema: `{"type":"date","system":"duedate"}`, value: "2026-12-01", want: &model.FieldValue{Kind: model.FieldDate, Text: "2026-12-01"}},
		{name: "datetime", id: "customfield_30005", schema: cf("datetime", "datetime"), value: "2026-11-20T09:30:00.000+0000",
			want: &model.FieldValue{Kind: model.FieldDateTime}, wantTime: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)},
		{name: "datetime with offset", id: "customfield_30006", schema: cf("datetime", "datetime"), value: "2026-11-20T09:30:00.000+0100",
			want: &model.FieldValue{Kind: model.FieldDateTime}, wantTime: time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)},
		{name: "url", id: "customfield_30007", schema: cf("string", "url"), value: "https://example.com/spec", want: &model.FieldValue{Kind: model.FieldURL, Text: "https://example.com/spec"}},
		{name: "option value", id: "customfield_30008", schema: cf("option", "select"), value: map[string]any{"value": "Red", "id": "1"}, want: &model.FieldValue{Kind: model.FieldOption, Text: "Red"}},
		{name: "option name (priority)", id: "priority", schema: `{"type":"priority","system":"priority"}`, value: map[string]any{"name": "High", "id": "2"}, want: &model.FieldValue{Kind: model.FieldOption, Text: "High"}},
		{name: "options", id: "customfield_30009", schema: arr("option", "multiselect"), value: []any{map[string]any{"value": "A"}, map[string]any{"value": "B"}}, want: &model.FieldValue{Kind: model.FieldOptions, Items: []string{"A", "B"}}},
		{name: "cascade", id: "customfield_30010", schema: cf("option-with-child", "cascadingselect"), value: map[string]any{"value": "EU", "child": map[string]any{"value": "Berlin"}}, want: &model.FieldValue{Kind: model.FieldCascade, Items: []string{"EU", "Berlin"}}},
		{name: "cascade without child", id: "customfield_30011", schema: cf("option-with-child", "cascadingselect"), value: map[string]any{"value": "EU"}, want: &model.FieldValue{Kind: model.FieldCascade, Items: []string{"EU"}}},
		{name: "user", id: "reporter", schema: `{"type":"user","system":"reporter"}`, value: f12User("u-silas", "Silas Lee"), want: &model.FieldValue{Kind: model.FieldUser, Users: []string{"u-silas"}}},
		{name: "users", id: "customfield_30012", schema: arr("user", "multiuserpicker"), value: []any{f12User("u-noor", "Noor Abadi"), f12User("u-otto", "Otto Brandt")}, want: &model.FieldValue{Kind: model.FieldUsers, Users: []string{"u-noor", "u-otto"}}},
		{name: "group", id: "customfield_30013", schema: cf("group", "grouppicker"), value: map[string]any{"name": "jira-devs"}, want: &model.FieldValue{Kind: model.FieldGroup, Text: "jira-devs"}},
		{name: "groups", id: "customfield_30014", schema: arr("group", "multigrouppicker"), value: []any{map[string]any{"name": "a"}, map[string]any{"name": "b"}}, want: &model.FieldValue{Kind: model.FieldGroups, Items: []string{"a", "b"}}},
		{name: "version", id: "customfield_30015", schema: cf("version", "version"), value: map[string]any{"name": "1.0"}, want: &model.FieldValue{Kind: model.FieldVersion, Text: "1.0"}},
		{name: "versions", id: "fixVersions", schema: `{"type":"array","items":"version","system":"fixVersions"}`, value: []any{map[string]any{"name": "2.0"}, map[string]any{"name": "2.1"}}, want: &model.FieldValue{Kind: model.FieldVersions, Items: []string{"2.0", "2.1"}}},
		{name: "components", id: "components", schema: `{"type":"array","items":"component","system":"components"}`, value: []any{map[string]any{"name": "ios"}, map[string]any{"name": "api"}}, want: &model.FieldValue{Kind: model.FieldComponents, Items: []string{"ios", "api"}}},
		{name: "labels", id: "labels", schema: `{"type":"array","items":"string","system":"labels"}`, value: []any{"ux", "units"}, want: &model.FieldValue{Kind: model.FieldLabels, Items: []string{"ux", "units"}}},
		{name: "project", id: "project", schema: `{"type":"project","system":"project"}`, value: map[string]any{"key": "MOB", "name": "Saffron Mobile"}, want: &model.FieldValue{Kind: model.FieldProject, Text: "Saffron Mobile"}},
		{name: "team name", id: "customfield_30016", schema: cf("team", "atlassian-team"), value: map[string]any{"id": "t-1", "name": "Platform"}, want: &model.FieldValue{Kind: model.FieldTeam, Text: "Platform"}},
		{name: "team title", id: "customfield_30017", schema: `{"type":"any","custom":"com.atlassian.teams:rm-teams-custom-field-team"}`, value: map[string]any{"id": "t-2", "title": "Growth"}, want: &model.FieldValue{Kind: model.FieldTeam, Text: "Growth"}},
		{name: "unknown object", id: "customfield_30018", schema: `{"type":"any","custom":"com.tempoplugin.tempo-accounts:accounts.customfield"}`, value: map[string]any{"id": 7, "name": "ACME-OPS"}, want: &model.FieldValue{Kind: model.FieldUnknown, Raw: json.RawMessage(`{"id":7,"name":"ACME-OPS"}`)}},
		{name: "unknown string", id: "customfield_30019", schema: `{"type":"any","custom":"com.example:thing"}`, value: "ACC-77", want: &model.FieldValue{Kind: model.FieldUnknown, Raw: json.RawMessage(`"ACC-77"`)}},
		{name: "unknown number", id: "customfield_30020", schema: `{"type":"any","custom":"com.example:count"}`, value: 42, want: &model.FieldValue{Kind: model.FieldUnknown, Raw: json.RawMessage(`42`)}},
		{name: "unknown array", id: "customfield_30021", schema: `{"type":"array","items":"json","custom":"com.example:list"}`, value: []any{1, 2}, want: &model.FieldValue{Kind: model.FieldUnknown, Raw: json.RawMessage(`[1,2]`)}},
		{name: "key without a registry entry", id: "customfield_99999", schema: "", value: "mystery", want: &model.FieldValue{Kind: model.FieldUnknown, Raw: json.RawMessage(`"mystery"`)}},
		// not set: no FieldValue at all
		{name: "empty text", id: "customfield_31001", schema: cf("string", "textfield"), value: ""},
		{name: "null text", id: "customfield_31002", schema: cf("string", "textfield"), value: nil},
		{name: "null paragraph", id: "customfield_31003", schema: cf("string", "textarea"), value: nil},
		{name: "empty options", id: "customfield_31004", schema: arr("option", "multicheckboxes"), value: []any{}},
		{name: "option without a displayable member", id: "customfield_31005", schema: cf("option", "select"), value: map[string]any{"id": "5", "self": "x"}},
		{name: "null user", id: "customfield_31006", schema: cf("user", "userpicker"), value: nil},
		{name: "empty users", id: "customfield_31007", schema: arr("user", "multiuserpicker"), value: []any{}},
		{name: "cascade without a displayable member", id: "customfield_31008", schema: cf("option-with-child", "cascadingselect"), value: map[string]any{"id": "1"}},
		{name: "empty labels", id: "customfield_31009", schema: `{"type":"array","items":"string","system":"labels2"}`, value: []any{}},
		{name: "null number", id: "customfield_31010", schema: cf("number", "float"), value: nil},
		{name: "null unknown", id: "customfield_31011", schema: `{"type":"any","custom":"com.example:thing"}`, value: nil},
		{name: "empty-string unknown", id: "customfield_31012", schema: `{"type":"any","custom":"com.example:thing"}`, value: ""},
		{name: "empty-array unknown", id: "customfield_31013", schema: `{"type":"any","custom":"com.example:thing"}`, value: []any{}},
		{name: "empty-object unknown", id: "customfield_31014", schema: `{"type":"any","custom":"com.example:thing"}`, value: map[string]any{}},
		{name: "empty group object", id: "customfield_31015", schema: cf("group", "grouppicker"), value: map[string]any{}},
	}
	return rows
}

func f12RegistryOf(rows []f12ValRow, extra ...string) string {
	var defs []string
	for _, r := range rows {
		if r.schema == "" {
			continue
		}
		defs = append(defs, fmt.Sprintf(`{"id":%q,"key":%q,"name":%q,"custom":%t,"schema":%s}`, r.id, r.id, "Name of "+r.id, strings.HasPrefix(r.id, "customfield_"), r.schema))
	}
	defs = append(defs, extra...)
	return "[" + strings.Join(defs, ",") + "]"
}

func TestHydrate_DecodesFieldValuesPerKindAndDropsEmpties(t *testing.T) {
	defer wp19Guard(t)
	rows := f12ValueRows(t)
	fields := map[string]any{}
	for _, r := range rows {
		fields[r.id] = r.value
	}
	// excluded keys, all with values that would otherwise be displayable
	excluded := map[string]any{
		"votes": map[string]any{"votes": 2, "hasVoted": false}, "lastViewed": "2026-11-20T10:00:00.000+0000",
		"statuscategorychangedate": "2026-11-19T10:00:00.000+0000", "workratio": 50,
		"progress": map[string]any{"progress": 1, "total": 2}, "aggregateprogress": map[string]any{"progress": 1, "total": 2},
		"worklog": map[string]any{"total": 1}, "comment": map[string]any{"total": 1, "comments": []any{}},
		"watches": map[string]any{"watchCount": 4}, "customfield_30090": []any{map[string]any{"x": 1}},
		"description": f12Doc("d"), "environment": f12Doc("e"),
		"attachment": []any{}, "issuelinks": []any{}, "subtasks": []any{},
		"customfield_10021": []any{map[string]any{"value": "Impediment"}}, "customfield_10019": "0|i:", "customfield_10020": []any{},
		"timetracking": map[string]any{"originalEstimate": "1d", "originalEstimateSeconds": 28800, "remainingEstimateSeconds": 14400, "timeSpentSeconds": 14400},
	}
	for k, v := range excluded {
		fields[k] = v
	}
	reg := f12RegistryOf(rows,
		`{"id":"customfield_30090","name":"Development","custom":true,"schema":{"type":"any","custom":"com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf"}}`,
		`{"id":"timetracking","name":"Time tracking","custom":false,"schema":{"type":"timetracking","system":"timetracking"}}`,
		`{"id":"customfield_10019","name":"Rank","custom":true,"schema":{"type":"any","custom":"com.pyxis.greenhopper.jira:gh-lexo-rank"}}`,
		`{"id":"customfield_10020","name":"Sprint","custom":true,"schema":{"type":"array","items":"json","custom":"com.pyxis.greenhopper.jira:gh-sprint"}}`,
		`{"id":"customfield_10021","name":"Flagged","custom":true,"schema":{"type":"array","items":"option","custom":"`+f12CT+`multicheckboxes"}}`)
	srv := f12ContentServer(t, reg, func(id string) map[string]any { return f12Issue(id, fields) }, nil)
	d := f12Hydrate(t, srv, "10001")[0]
	c := f12ContentOf(t, d)

	got := map[string]model.FieldValue{}
	var order []string
	for _, fv := range c.Fields {
		if _, dup := got[fv.FieldID]; dup {
			t.Errorf("field %s listed twice", fv.FieldID)
		}
		got[fv.FieldID] = fv
		order = append(order, fv.FieldID)
	}
	if !sort.StringsAreSorted(order) {
		t.Errorf("Content.Fields order = %v, want sorted by FieldID", order)
	}
	wantIDs := map[string]bool{"timetracking": true}
	for _, r := range rows {
		if r.want == nil {
			if fv, present := got[r.id]; present {
				t.Errorf("%s: field %s is empty on the wire but decoded to %+v (not set = no value)", r.name, r.id, fv)
			}
			continue
		}
		wantIDs[r.id] = true
		fv, ok := got[r.id]
		if !ok {
			t.Errorf("%s: field %s missing from Content.Fields", r.name, r.id)
			continue
		}
		w := *r.want
		if fv.Kind != w.Kind || fv.Text != w.Text && w.Kind != model.FieldDateTime || strings.Join(fv.Items, "\x00") != strings.Join(w.Items, "\x00") ||
			strings.Join(fv.Users, "\x00") != strings.Join(w.Users, "\x00") {
			t.Errorf("%s: %s = %+v, want kind %s text %q items %v users %v", r.name, r.id, fv, w.Kind, w.Text, w.Items, w.Users)
		}
		if (fv.Number == nil) != (w.Number == nil) || fv.Number != nil && *fv.Number != *w.Number {
			t.Errorf("%s: Number = %v, want %v", r.name, fv.Number, w.Number)
		}
		if w.Raw != nil && !f12SameRaw(fv.Raw, w.Raw) {
			t.Errorf("%s: Raw = %s, want %s", r.name, fv.Raw, w.Raw)
		}
		if w.Kind == model.FieldParagraph && !f12RichIs(fv.Rich, f12ADFValue(), t) {
			t.Errorf("%s: Rich = %+v, want the ADF document verbatim", r.name, fv.Rich)
		}
		if !r.wantTime.IsZero() {
			at, err := time.Parse(time.RFC3339Nano, fv.Text)
			if err != nil || !at.Equal(r.wantTime) {
				t.Errorf("%s: Text = %q, want an RFC 3339 instant equal to %v (parse err %v)", r.name, fv.Text, r.wantTime, err)
			}
		}
	}
	// everything else (excluded keys, empties) must be absent: only the expected ids remain
	for id := range got {
		if !wantIDs[id] {
			t.Errorf("unexpected field %s in Content.Fields: %+v", id, got[id])
		}
	}
	// time tracking: one unknown-kind value with the fixed id
	tt, ok := got["timetracking"]
	if !ok || tt.Kind != model.FieldUnknown {
		t.Fatalf("timetracking = %+v (present %v), want kind unknown", tt, ok)
	}
	var raw map[string]any
	if err := json.Unmarshal(tt.Raw, &raw); err != nil || raw["originalEstimateSeconds"] != float64(28800) ||
		raw["remainingEstimateSeconds"] != float64(14400) || raw["timeSpentSeconds"] != float64(14400) {
		t.Errorf("timetracking Raw = %s (%v), want the three *Seconds members", tt.Raw, err)
	}
}

func f12ADFValue() any { return f12Doc("Acceptance criteria") }

func TestHydrate_UsersInFieldsJoinPeopleOnceWithoutEmail(t *testing.T) {
	defer wp19Guard(t)
	fields := map[string]any{
		"reporter":          f12User("u-silas", "Silas Lee"),
		"customfield_30012": []any{f12User("u-silas", "Silas Lee"), f12User("u-noor", "Noor Abadi")},
	}
	reg := `[{"id":"reporter","name":"Reporter","custom":false,"schema":{"type":"user","system":"reporter"}},
		{"id":"customfield_30012","name":"Reviewers","custom":true,"schema":{"type":"array","items":"user","custom":"` + f12CT + `multiuserpicker"}}]`
	srv := f12ContentServer(t, reg, func(id string) map[string]any { return f12Issue(id, fields) }, nil)
	d := f12Hydrate(t, srv, "10001")[0]
	f12ContentOf(t, d)
	count := map[string]int{}
	for _, p := range d.People {
		count[p.AccountID]++
	}
	if count["u-silas"] != 1 || count["u-noor"] != 1 || count["u-iris"] != 1 {
		t.Errorf("People = %+v, want u-iris (assignee), u-silas and u-noor once each", d.People)
	}
	for _, p := range d.People {
		if p.AccountID == "u-silas" && (p.DisplayName != "Silas Lee" || p.Kind != "user" || !p.Active) {
			t.Errorf("u-silas = %+v", p)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", d), "@authors.example") {
		t.Errorf("an e-mail address was retained: %+v", d)
	}
}

// ── description, environment, attachments, links, subtasks, parent, watchers ───────────────────

func TestHydrate_DescriptionAndEnvironmentAreStoredAsRawAdf(t *testing.T) {
	defer wp19Guard(t)
	desc := f12Doc(f12DescSentinel)
	env := f12Doc("iOS 18.2")
	srv := f12ContentServer(t, "", func(id string) map[string]any {
		switch id {
		case "10001":
			return f12Issue(id, map[string]any{"description": desc, "environment": env})
		case "10002":
			return f12Issue(id, map[string]any{"description": nil, "environment": nil})
		}
		return f12Issue(id, nil) // keys absent
	}, nil)
	got := f12Hydrate(t, srv, "10001", "10002", "10003")
	c := f12ContentOf(t, got[0])
	if !f12RichIs(c.Description, desc, t) || !f12RichIs(c.Environment, env, t) {
		t.Errorf("Description/Environment = %+v / %+v, want RichText{adf, the documents verbatim}", c.Description, c.Environment)
	}
	for i, d := range got[1:] {
		c := f12ContentOf(t, d)
		if c.Description != nil || c.Environment != nil {
			t.Errorf("issue %d: Description/Environment = %+v / %+v, want nil (empty)", i+1, c.Description, c.Environment)
		}
	}
}

func TestHydrate_AttachmentMetadata(t *testing.T) {
	defer wp19Guard(t)
	atts := []any{
		map[string]any{"id": "20001", "filename": "unit-toggle-v3.png", "mimeType": "image/png", "size": 182_231,
			"created": "2026-11-18T09:15:02.123+0000", "author": f12User("u-iris", "Iris Calloway"),
			"content": "https://acme.atlassian.net/rest/api/3/attachment/content/20001", "thumbnail": "https://acme.atlassian.net/rest/api/3/attachment/thumbnail/20001"},
		map[string]any{"id": "20004", "filename": "release-notes.pdf", "mimeType": "application/pdf", "size": 401,
			"created": "2026-11-19T10:00:00.000+0000", "author": map[string]any{"accountId": "u-noor"}},
	}
	srv := f12ContentServer(t, "", func(id string) map[string]any { return f12Issue(id, map[string]any{"attachment": atts}) }, nil)
	c := f12ContentOf(t, f12Hydrate(t, srv, "10001")[0])
	if len(c.Attachments) != 2 {
		t.Fatalf("Attachments = %+v", c.Attachments)
	}
	a, b := c.Attachments[0], c.Attachments[1]
	if a.ID != "20001" || a.Name != "unit-toggle-v3.png" || a.Mime != "image/png" || a.Bytes != 182_231 || a.AuthorID != "u-iris" ||
		!a.CreatedAt.Equal(time.Date(2026, 11, 18, 9, 15, 2, 123_000_000, time.UTC)) {
		t.Errorf("Attachments[0] = %+v", a)
	}
	if b.ID != "20004" || b.Name != "release-notes.pdf" || b.Mime != "application/pdf" || b.Bytes != 401 || b.AuthorID != "u-noor" ||
		!b.CreatedAt.Equal(time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("Attachments[1] = %+v", b)
	}
	for i, x := range c.Attachments {
		if x.MediaFileID != "" || x.LocalID != 0 || x.Width != 0 || x.Height != 0 {
			t.Errorf("Attachments[%d] = %+v: MediaFileID, LocalID, Width and Height are learned later and stay zero here", i, x)
		}
	}
}

func f12IssueRef(id, key, summary, status, typ string) map[string]any {
	return map[string]any{"id": id, "key": key, "fields": map[string]any{
		"summary": summary, "status": map[string]any{"id": status}, "issuetype": map[string]any{"id": typ}}}
}

func TestHydrate_LinksSubtasksParentAndWatchers(t *testing.T) {
	defer wp19Guard(t)
	blocks := map[string]any{"name": "Blocks", "inward": "is blocked by", "outward": "blocks"}
	relates := map[string]any{"name": "Relates", "inward": "relates to", "outward": "relates to"}
	fields := map[string]any{
		"issuelinks": []any{
			map[string]any{"id": "50001", "type": blocks, "inwardIssue": f12IssueRef("10428", "MOB-1428", "Cooking mode", "10002", "10003")},
			map[string]any{"id": "50002", "type": blocks, "outwardIssue": f12IssueRef("10440", "MOB-1440", "Drag meals", "10001", "10002")},
			map[string]any{"id": "50003", "type": relates, "outwardIssue": f12IssueRef("10411", "MOB-1411", "Scale amounts", "10004", "10002")},
		},
		"subtasks": []any{
			f12IssueRef("10436", "MOB-1436", "Timer survives", "10002", "10005"),
			f12IssueRef("10456", "MOB-1456", "Onboarding", "10007", "10005"),
		},
		"parent":  f12IssueRef("10300", "MOB-1300", "The epic", "10002", "10000"),
		"watches": map[string]any{"watchCount": 4, "isWatching": false},
	}
	srv := f12ContentServer(t, "", func(id string) map[string]any {
		if id == "10002" {
			return f12Issue(id, nil)
		}
		return f12Issue(id, fields)
	}, nil)
	got := f12Hydrate(t, srv, "10001", "10002")
	c := f12ContentOf(t, got[0])
	wantLinks := []model.IssueLink{
		{ID: "50001", TypeName: "Blocks", Label: "is blocked by", Outward: false, OtherSourceID: "10428", OtherKey: "MOB-1428", OtherSummary: "Cooking mode", OtherStatusID: "10002", OtherTypeID: "10003"},
		{ID: "50002", TypeName: "Blocks", Label: "blocks", Outward: true, OtherSourceID: "10440", OtherKey: "MOB-1440", OtherSummary: "Drag meals", OtherStatusID: "10001", OtherTypeID: "10002"},
		{ID: "50003", TypeName: "Relates", Label: "relates to", Outward: true, OtherSourceID: "10411", OtherKey: "MOB-1411", OtherSummary: "Scale amounts", OtherStatusID: "10004", OtherTypeID: "10002"},
	}
	if fmt.Sprintf("%+v", c.Links) != fmt.Sprintf("%+v", wantLinks) {
		t.Errorf("Links = %+v\nwant   %+v", c.Links, wantLinks)
	}
	wantSubs := []model.IssueRef{
		{SourceID: "10436", Key: "MOB-1436", Summary: "Timer survives", StatusID: "10002", TypeID: "10005"},
		{SourceID: "10456", Key: "MOB-1456", Summary: "Onboarding", StatusID: "10007", TypeID: "10005"},
	}
	if fmt.Sprintf("%+v", c.Subtasks) != fmt.Sprintf("%+v", wantSubs) {
		t.Errorf("Subtasks = %+v\nwant     %+v", c.Subtasks, wantSubs)
	}
	if c.Parent == nil || c.Parent.SourceID != "10300" || c.Parent.Key != "MOB-1300" || c.Parent.Summary != "The epic" || c.Parent.TypeID != "10000" {
		t.Errorf("Parent = %+v, want MOB-1300 'The epic' type 10000", c.Parent)
	}
	if got[0].ParentKey != "MOB-1300" {
		t.Errorf("Issue.ParentKey = %q, want MOB-1300 (unchanged)", got[0].ParentKey)
	}
	if c.Watchers != 4 {
		t.Errorf("Watchers = %d, want 4", c.Watchers)
	}
	// an issue with none of it
	e := f12ContentOf(t, got[1])
	if len(e.Links) != 0 || len(e.Subtasks) != 0 || e.Parent != nil || len(e.Attachments) != 0 {
		t.Errorf("empty issue content = %+v, want no links, subtasks, parent or attachments", e)
	}
	if e.Watchers != -1 {
		t.Errorf("Watchers without a watches field = %d, want -1 (unknown)", e.Watchers)
	}
}

// ── content hash ────────────────────────────────────────────────────────────────────────────────

func TestHydrate_ContentHashIsStableAndTracksContent(t *testing.T) {
	defer wp19Guard(t)
	reg := `[{"id":"customfield_30001","name":"Train","custom":true,"schema":{"type":"string","custom":"` + f12CT + `textfield"}}]`
	base := map[string]any{
		"description":       f12Doc("one"),
		"environment":       f12Doc("env"),
		"customfield_30001": "R-1",
		"attachment":        []any{map[string]any{"id": "1", "filename": "a.png", "mimeType": "image/png", "size": 10, "created": "2026-11-18T09:00:00.000+0000", "author": map[string]any{"accountId": "u-iris"}}},
		"issuelinks":        []any{},
		"subtasks":          []any{},
	}
	hashFor := func(t *testing.T, mutate func(m map[string]any)) string {
		t.Helper()
		fields := map[string]any{}
		for k, v := range base {
			fields[k] = v
		}
		if mutate != nil {
			mutate(fields)
		}
		srv := f12ContentServer(t, reg, func(id string) map[string]any { return f12Issue(id, fields) }, nil)
		return f12ContentOf(t, f12Hydrate(t, srv, "10001")[0]).Hash
	}
	h1 := hashFor(t, nil)
	if h1 == "" {
		t.Fatal("Content.Hash is empty")
	}
	if h2 := hashFor(t, nil); h2 != h1 {
		t.Errorf("hash of identical data differs between hydrations: %q vs %q", h1, h2)
	}
	changes := map[string]func(m map[string]any){
		"description": func(m map[string]any) { m["description"] = f12Doc("two") },
		"environment": func(m map[string]any) { m["environment"] = nil },
		"field value": func(m map[string]any) { m["customfield_30001"] = "R-2" },
		"attachment":  func(m map[string]any) { m["attachment"] = []any{} },
		"link": func(m map[string]any) {
			m["issuelinks"] = []any{map[string]any{"id": "9", "type": map[string]any{"name": "Blocks", "inward": "is blocked by", "outward": "blocks"}, "outwardIssue": f12IssueRef("1", "X-1", "s", "1", "1")}}
		},
		"subtask": func(m map[string]any) { m["subtasks"] = []any{f12IssueRef("1", "X-2", "s", "1", "1")} },
		"parent":  func(m map[string]any) { m["parent"] = f12IssueRef("1", "X-3", "s", "1", "1") },
	}
	for name, mut := range changes {
		if h := hashFor(t, mut); h == h1 {
			t.Errorf("changing the %s did not change Content.Hash (%q)", name, h)
		}
	}
}

// ── the field registry request ──────────────────────────────────────────────────────────────────

func TestHydrate_FetchesTheFieldRegistryOncePerAdapterSharedWithFields(t *testing.T) {
	defer wp19Guard(t)
	// the default registry (testdata/wp18_fields.json) knows customfield_10016 as a number
	srv := f12ContentServer(t, "", func(id string) map[string]any { return f12Issue(id, map[string]any{"customfield_10016": 3}) }, nil)
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	first, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
	if err != nil || len(first) != 1 {
		t.Fatalf("Hydrate #1: %v (%d)", err, len(first))
	}
	c := f12ContentOf(t, first[0])
	if len(c.Fields) != 1 || c.Fields[0].FieldID != "customfield_10016" || c.Fields[0].Kind != model.FieldNumber ||
		c.Fields[0].Number == nil || *c.Fields[0].Number != 3 {
		t.Fatalf("Fields = %+v, want customfield_10016 decoded through the registry as number 3", c.Fields)
	}
	if _, err := a.Hydrate(context.Background(), []string{"10001"}, nil); err != nil {
		t.Fatalf("Hydrate #2: %v", err)
	}
	if defs, err := a.Fields(context.Background()); err != nil || len(defs) == 0 {
		t.Fatalf("Fields: %v (%d defs)", err, len(defs))
	}
	if n := len(srv.to("GET", "/rest/api/3/field")); n != 1 {
		t.Errorf("GET /field requests = %d, want 1 (two Hydrates and a Fields on one Adapter)", n)
	}
}

// ── comments ────────────────────────────────────────────────────────────────────────────────────

func f12Cmt(id string, created time.Time, updated any, author, authorType string, body any, vis any) map[string]any {
	m := map[string]any{"id": id, "created": wp19TS(created)}
	if author != "" {
		m["author"] = map[string]any{"accountId": author, "displayName": "Name of " + author, "emailAddress": author + "@authors.example", "accountType": authorType}
	}
	if u, ok := updated.(time.Time); ok {
		m["updated"] = wp19TS(u)
	}
	if body != nil {
		m["body"] = body
	}
	if vis != nil {
		m["visibility"] = vis
	}
	return m
}

// f12RawCommentRoute pages a prepared comment list like f12CommentRoute.
func f12RawCommentRoute(t *testing.T, all []map[string]any, pageCap int) wp19Route {
	return func(r wp19Req) wp19Resp {
		var start, limit int
		fmt.Sscan(r.Query.Get("startAt"), &start)
		fmt.Sscan(r.Query.Get("maxResults"), &limit)
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
			page = append(page, c)
		}
		return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": start, "maxResults": limit, "total": len(all), "comments": page})}
	}
}

func TestHydrate_CommentsCarryBodiesVisibilityAndEditedState(t *testing.T) {
	defer wp19Guard(t)
	c1 := wp19Day(5)
	c2 := wp19Day(4)
	c3 := wp19Day(3)
	c4 := wp19Day(2)
	body := f12Doc(wp19Sentinel)
	all := []map[string]any{
		f12Cmt("c1", c1, c1, "u-silas", "atlassian", body, nil),
		f12Cmt("c2", c2, c2, "u-iris", "atlassian", f12Doc("internal"), map[string]any{"type": "role", "value": "Developers", "identifier": "10360"}),
		f12Cmt("c3", c3, wp19Day(1), "bot-automation", "app", f12Doc("edited later"), nil),
		f12Cmt("c4", c4, nil, "", "", nil, map[string]any{"type": "group", "value": "jira-staff"}), // no updated, no author, no body
	}
	srv := f12ContentServer(t, "", func(id string) map[string]any { return f12Issue(id, nil) },
		map[string]wp19Route{"GET /rest/api/3/issue/{id}/comment": f12RawCommentRoute(t, all, 0)})
	d := f12Hydrate(t, srv, "10001")[0]
	if d.CommentTotal != 4 || len(d.Comments) != 4 {
		t.Fatalf("CommentTotal/Comments = %d/%d, want 4/4", d.CommentTotal, len(d.Comments))
	}
	cs := d.Comments
	if cs[0].ID != "c1" || cs[0].AuthorID != "u-silas" || !cs[0].At.Equal(c1) || !cs[0].UpdatedAt.Equal(c1) || cs[0].Visibility != nil {
		t.Errorf("c1 = %+v", cs[0])
	}
	if !f12RichIs(&cs[0].Body, body, t) {
		t.Errorf("c1 body = %+v, want the ADF document verbatim (bodies are stored since ADR 0015)", cs[0].Body)
	}
	if cs[1].Visibility == nil || *cs[1].Visibility != (model.Visibility{Type: "role", Value: "Developers"}) {
		t.Errorf("c2 Visibility = %+v, want role/Developers", cs[1].Visibility)
	}
	if !cs[2].UpdatedAt.After(cs[2].At) || !cs[2].UpdatedAt.Equal(wp19Day(1)) || !cs[2].At.Equal(c3) {
		t.Errorf("c3 At/UpdatedAt = %v/%v, want created %v and edited %v", cs[2].At, cs[2].UpdatedAt, c3, wp19Day(1))
	}
	if cs[3].AuthorID != "" || !cs[3].Body.IsEmpty() || !cs[3].UpdatedAt.Equal(cs[3].At) {
		t.Errorf("c4 = %+v, want no author, empty body, UpdatedAt == At (updated absent)", cs[3])
	}
	if cs[3].Visibility == nil || *cs[3].Visibility != (model.Visibility{Type: "group", Value: "jira-staff"}) {
		t.Errorf("c4 Visibility = %+v, want group/jira-staff", cs[3].Visibility)
	}
	people := map[string]model.Person{}
	for _, p := range d.People {
		people[p.AccountID] = p
	}
	if p := people["bot-automation"]; p.Kind != "app" || p.DisplayName != "Name of bot-automation" {
		t.Errorf("bot-automation = %+v, want Kind app (its comment is kept, authors seen)", p)
	}
	if p := people["u-silas"]; p.Kind != "user" {
		t.Errorf("u-silas = %+v", p)
	}
	if strings.Contains(fmt.Sprintf("%+v", d.People), "@authors.example") {
		t.Errorf("People retain an e-mail address: %+v", d.People)
	}
}

func f12ManyComments(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		at := wp19Now.Add(-time.Duration(n-i) * time.Hour)
		out[i] = f12Cmt(fmt.Sprintf("m%04d", i), at, at, "u-silas", "atlassian", f12Doc(fmt.Sprintf("comment %d", i)), nil)
	}
	return out
}

func TestHydrate_CommentsOverAHundredPageByStartAt(t *testing.T) {
	defer wp19Guard(t)
	all := f12ManyComments(250)
	srv := f12ContentServer(t, "", func(id string) map[string]any { return f12Issue(id, nil) },
		map[string]wp19Route{"GET /rest/api/3/issue/{id}/comment": f12RawCommentRoute(t, all, 0)})
	d := f12Hydrate(t, srv, "10001")[0]
	reqs := srv.comments("10001")
	if len(reqs) != 3 {
		t.Fatalf("comment requests = %d, want 3 (100 + 100 + 50)", len(reqs))
	}
	var starts []string
	for i, r := range reqs {
		if r.Query.Get("orderBy") != "created" || r.Query.Get("maxResults") != "100" {
			t.Errorf("request %d query = %v, want orderBy=created maxResults=100", i, r.Query)
		}
		starts = append(starts, r.Query.Get("startAt"))
	}
	sort.Strings(starts)
	if strings.Join(starts, ",") != "0,100,200" {
		t.Errorf("startAt values = %v, want 0, 100, 200", starts)
	}
	if d.CommentTotal != 250 || len(d.Comments) != 250 {
		t.Fatalf("CommentTotal/Comments = %d/%d, want 250/250", d.CommentTotal, len(d.Comments))
	}
	for i, c := range d.Comments {
		if c.ID != fmt.Sprintf("m%04d", i) {
			t.Fatalf("Comments[%d] = %s, want m%04d (order read, oldest first)", i, c.ID, i)
		}
	}
	if d.Comments[249].Body.IsEmpty() {
		t.Error("last comment has no body")
	}
}

func TestHydrate_CommentPagingAdvancesByWhatWasReceived(t *testing.T) {
	defer wp19Guard(t)
	all := f12ManyComments(100)
	srv := f12ContentServer(t, "", func(id string) map[string]any { return f12Issue(id, nil) },
		map[string]wp19Route{"GET /rest/api/3/issue/{id}/comment": f12RawCommentRoute(t, all, 40)}) // pages of 40 although 100 asked
	d := f12Hydrate(t, srv, "10001")[0]
	var starts []string
	for _, r := range srv.comments("10001") {
		starts = append(starts, r.Query.Get("startAt"))
	}
	if strings.Join(starts, ",") != "0,40,80" {
		t.Errorf("startAt sequence = %v, want 0,40,80 (advance by the page length, not by 100)", starts)
	}
	if len(d.Comments) != 100 {
		t.Errorf("Comments = %d, want 100", len(d.Comments))
	}
}

func TestHydrate_CommentPagingStopsOnAnEmptyPage(t *testing.T) {
	defer wp19Guard(t)
	all := f12ManyComments(2)
	lying := func(r wp19Req) wp19Resp { // claims 5 but only has 2
		inner := f12RawCommentRoute(t, all, 0)(r)
		var m map[string]any
		_ = json.Unmarshal([]byte(inner.Body), &m)
		m["total"] = 5
		return wp19Resp{Body: wp19JSON(t, m)}
	}
	srv := f12ContentServer(t, "", func(id string) map[string]any { return f12Issue(id, nil) },
		map[string]wp19Route{"GET /rest/api/3/issue/{id}/comment": lying})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	got, err := a.Hydrate(ctx, []string{"10001"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if len(got[0].Comments) != 2 {
		t.Errorf("Comments = %d, want the 2 that exist", len(got[0].Comments))
	}
	if n := len(srv.comments("10001")); n > 3 {
		t.Errorf("comment requests = %d, want the loop to stop at the empty page (≤ 3)", n)
	}
}

func TestHydrate_EveryHydratedIssueGetsItsOwnCommentFetch(t *testing.T) {
	defer wp19Guard(t)
	srv := f12ContentServer(t, "", func(id string) map[string]any { return f12Issue(id, nil) },
		map[string]wp19Route{"GET /rest/api/3/issue/{id}/comment": func(r wp19Req) wp19Resp {
			id := f12IssueIDOf(r.Path)
			at := wp19Day(1)
			return wp19Resp{Body: wp19JSON(t, map[string]any{"startAt": 0, "maxResults": 100, "total": 1,
				"comments": []any{f12Cmt("c-"+id, at, at, "u-silas", "atlassian", f12Doc("for "+id), nil)}})}
		}})
	ids := wp19IDs(10001, 7)
	got := f12Hydrate(t, srv, ids...)
	for i, id := range ids {
		if n := len(srv.comments(id)); n != 1 {
			t.Errorf("issue %s: comment requests = %d, want 1 (even though the search could have embedded some)", id, n)
		}
		if len(got[i].Comments) != 1 || got[i].Comments[0].ID != "c-"+id {
			t.Errorf("issue %s: Comments = %+v, want its own c-%s", id, got[i].Comments, id)
		}
	}
}

func TestHydrate_TheSearchHitsEmbeddedCommentKeyIsIgnored(t *testing.T) {
	defer wp19Guard(t)
	srv := f12ContentServer(t, "", func(id string) map[string]any {
		return f12Issue(id, map[string]any{"comment": map[string]any{"total": 1, "comments": []any{
			f12Cmt("embedded", wp19Day(1), wp19Day(1), "u-silas", "atlassian", f12Doc("EMBEDDED"), nil)}}})
	}, nil)
	d := f12Hydrate(t, srv, "10001")[0]
	f12ContentOf(t, d)
	if d.CommentTotal != 0 || len(d.Comments) != 0 {
		t.Errorf("CommentTotal/Comments = %d/%+v, want the (empty) comment endpoint to win", d.CommentTotal, d.Comments)
	}
}

func TestHydrate_CommentEndpointErrorsAreMapped(t *testing.T) {
	defer wp19Guard(t)
	for status, want := range map[int]apperr.Code{401: apperr.AuthInvalid, 404: apperr.NotFound} {
		srv := f12ContentServer(t, "", func(id string) map[string]any { return f12Issue(id, nil) },
			map[string]wp19Route{"GET /rest/api/3/issue/{id}/comment": wp19Fail(status, `{"errorMessages":["x"]}`)})
		a := newWP19Adapter(t, srv.URL, wp19Meta())
		_, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
		wp19AssertCode(t, err, want)
	}
}

// ── hygiene: bodies and field text are stored, never logged ─────────────────────────────────────

func TestHydrate_StoresContentButNeverLogsIt(t *testing.T) {
	defer wp19Guard(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	reg := `[{"id":"customfield_30001","name":"Train","custom":true,"schema":{"type":"string","custom":"` + f12CT + `textfield"}}]`
	srv := f12ContentServer(t, reg, func(id string) map[string]any {
		return f12Issue(id, map[string]any{"description": f12Doc(f12DescSentinel), "customfield_30001": f12FieldSentinel})
	}, map[string]wp19Route{"GET /rest/api/3/issue/{id}/comment": f12RawCommentRoute(t,
		[]map[string]any{f12Cmt("c1", wp19Day(1), wp19Day(1), "u-silas", "atlassian", f12Doc(wp19Sentinel), nil)}, 0)})

	src, err := New(model.Connection{ID: 1, Source: Kind, BaseURL: srv.URL, Account: "me@acme.example"}, "tok-123",
		source.Options{HTTP: &http.Client{}, Now: func() time.Time { return wp19Now }, Meta: wp19Meta(), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	a := src.(*Adapter)
	pol := NewPolicy(PolicyOptions{RatePerSecond: 1e6, Sleep: func(<-chan struct{}, time.Duration) bool { return true }, Jitter: func() float64 { return 0.5 }})
	a.c = NewClient(srv.URL, "me@acme.example", "tok-123", ClientOptions{HTTP: &http.Client{}, Policy: pol, Logger: logger, Now: func() time.Time { return wp19Now }})
	got, err := a.Hydrate(context.Background(), []string{"10001"}, nil)
	if err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	// precondition: the content really is retained, otherwise "not logged" would hold vacuously
	c := f12ContentOf(t, got[0])
	if c.Description == nil || !strings.Contains(string(c.Description.Raw), f12DescSentinel) {
		t.Fatalf("Description = %+v, want the sentinel text retained", c.Description)
	}
	if len(got[0].Comments) != 1 || !strings.Contains(string(got[0].Comments[0].Body.Raw), wp19Sentinel) {
		t.Fatalf("Comments = %+v, want the body retained", got[0].Comments)
	}
	if len(c.Fields) != 1 || c.Fields[0].Text != f12FieldSentinel {
		t.Fatalf("Fields = %+v, want the text field retained", c.Fields)
	}
	logs := buf.String()
	for _, secret := range []string{f12DescSentinel, f12FieldSentinel, wp19Sentinel, "tok-123", "@authors.example"} {
		if strings.Contains(logs, secret) {
			t.Errorf("the log contains %q (AGENTS invariant 12: stored, never logged):\n%s", secret, logs)
		}
	}
}

func TestHydrate_ContentOfSeveralIssuesDoesNotMix(t *testing.T) {
	defer wp19Guard(t)
	reg := `[{"id":"labels","name":"Labels","custom":false,"schema":{"type":"array","items":"string","system":"labels"}}]`
	srv := f12ContentServer(t, reg, func(id string) map[string]any {
		return f12Issue(id, map[string]any{"description": f12Doc("description of " + id), "labels": []any{"l-" + id}})
	}, nil)
	ids := wp19IDs(10001, 120) // three search batches of ≤ 50
	got := f12Hydrate(t, srv, ids...)
	for i, id := range ids {
		c := f12ContentOf(t, got[i])
		if !f12RichIs(c.Description, f12Doc("description of "+id), t) {
			t.Fatalf("issue %s Description = %+v", id, c.Description)
		}
		if len(c.Fields) != 1 || c.Fields[0].FieldID != "labels" || len(c.Fields[0].Items) != 1 || c.Fields[0].Items[0] != "l-"+id {
			t.Fatalf("issue %s Fields = %+v", id, c.Fields)
		}
	}
}

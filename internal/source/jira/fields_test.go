package jira

// WP-T4 tests for Adapter.Fields (jira-sync.md §2.2 "Field registry", ADR 0015): GET /rest/api/3/field entries become
// model.FieldDef rows with a neutral Kind. Harness: hydrate_test.go (newF12Server) and scan_test.go (wp19).
//
// SPEC-NOTE (kind table): the table below is jira-sync §2.2 literally, one row per rule and per kind of
// model.FieldKinds, in the "first matching rule wins" order of the doc. Rows beyond the doc's examples (a field with
// no schema, schema type "any", an array of strings that is not labels, rank, sprint, time tracking) all fall under
// "anything else → unknown". Flagged (array/option, multicheckboxes) is therefore `options`; the Rank field
// (schema type "any", gh-lexo-rank) and the Sprint field (array of json, gh-sprint) are `unknown`.
// SPEC-NOTE (component): the doc has no rule that produces the singular kind `component` (only `components` from
// array/component). A bare schema type "component" is therefore `unknown`, and TestFields_EveryKindButComponentIsProduced
// pins that gap so a later rule is a deliberate decision.
// SPEC-NOTE (def fields): FieldDef.ID = id, Name = name, Custom = the response's `custom`, CustomType = schema.custom
// ("" for system fields), Schema = schema.verbatim (compared as JSON). Output order = response order, one FieldDef per
// response entry (entries without an id are skipped). Fields() performs exactly one GET /rest/api/3/field and returns
// the same registry from memory on a second call; errors are mapped like everywhere (401 auth_invalid, 403
// forbidden, 5xx source_http after retries).
// SPEC-NOTE (team): `schema.type == "team"`, or schema.custom (suffix match is not required, the full key is the
// contract) `com.atlassian.teams:rm-teams-custom-field-team` or
// `com.atlassian.jira.plugin.system.customfieldtypes:atlassian-team` with ANY schema.type, is `team`.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

const f12CT = "com.atlassian.jira.plugin.system.customfieldtypes:"

type f12FieldRow struct {
	name   string
	id     string
	custom bool
	schema string // JSON, "" = the entry has no schema member at all
	want   string
}

func f12FieldRows() []f12FieldRow {
	return []f12FieldRow{
		// text
		{"summary is text", "summary", false, `{"type":"string","system":"summary"}`, model.FieldText},
		{"textfield is text", "customfield_20001", true, `{"type":"string","custom":"` + f12CT + `textfield","customId":20001}`, model.FieldText},
		{"epic name is text", "customfield_20002", true, `{"type":"string","custom":"com.pyxis.greenhopper.jira:gh-epic-label","customId":20002}`, model.FieldText},
		// paragraph (ADF)
		{"description is paragraph", "description", false, `{"type":"string","system":"description"}`, model.FieldParagraph},
		{"environment is paragraph", "environment", false, `{"type":"string","system":"environment"}`, model.FieldParagraph},
		{"textarea is paragraph", "customfield_20003", true, `{"type":"string","custom":"` + f12CT + `textarea","customId":20003}`, model.FieldParagraph},
		// url
		{"url custom is url", "customfield_20004", true, `{"type":"string","custom":"` + f12CT + `url","customId":20004}`, model.FieldURL},
		// number
		{"float is number", "customfield_10016", true, `{"type":"number","custom":"` + f12CT + `float","customId":10016}`, model.FieldNumber},
		{"time in seconds is number", "aggregatetimespent", false, `{"type":"number","system":"aggregatetimespent"}`, model.FieldNumber},
		// date, datetime
		{"duedate is date", "duedate", false, `{"type":"date","system":"duedate"}`, model.FieldDate},
		{"datepicker is date", "customfield_20005", true, `{"type":"date","custom":"` + f12CT + `datepicker","customId":20005}`, model.FieldDate},
		{"datetime picker is datetime", "customfield_20006", true, `{"type":"datetime","custom":"` + f12CT + `datetime","customId":20006}`, model.FieldDateTime},
		{"created is datetime", "created", false, `{"type":"datetime","system":"created"}`, model.FieldDateTime},
		// option(s), cascade
		{"select is option", "customfield_20007", true, `{"type":"option","custom":"` + f12CT + `select","customId":20007}`, model.FieldOption},
		{"radiobuttons is option", "customfield_20008", true, `{"type":"option","custom":"` + f12CT + `radiobuttons","customId":20008}`, model.FieldOption},
		{"multiselect is options", "customfield_20009", true, `{"type":"array","items":"option","custom":"` + f12CT + `multiselect","customId":20009}`, model.FieldOptions},
		{"multicheckboxes is options", "customfield_20010", true, `{"type":"array","items":"option","custom":"` + f12CT + `multicheckboxes","customId":20010}`, model.FieldOptions},
		{"flagged is options", "customfield_10021", true, `{"type":"array","items":"option","custom":"` + f12CT + `multicheckboxes","customId":10021}`, model.FieldOptions},
		{"cascadingselect is cascade", "customfield_20011", true, `{"type":"option-with-child","custom":"` + f12CT + `cascadingselect","customId":20011}`, model.FieldCascade},
		{"priority is option", "priority", false, `{"type":"priority","system":"priority"}`, model.FieldOption},
		{"resolution is option", "resolution", false, `{"type":"resolution","system":"resolution"}`, model.FieldOption},
		{"issuetype is option", "issuetype", false, `{"type":"issuetype","system":"issuetype"}`, model.FieldOption},
		{"status is option", "status", false, `{"type":"status","system":"status"}`, model.FieldOption},
		{"security level is option", "security", false, `{"type":"securitylevel","system":"security"}`, model.FieldOption},
		// user(s), group(s)
		{"reporter is user", "reporter", false, `{"type":"user","system":"reporter"}`, model.FieldUser},
		{"userpicker is user", "customfield_20012", true, `{"type":"user","custom":"` + f12CT + `userpicker","customId":20012}`, model.FieldUser},
		{"multiuserpicker is users", "customfield_20013", true, `{"type":"array","items":"user","custom":"` + f12CT + `multiuserpicker","customId":20013}`, model.FieldUsers},
		{"request participants are users", "customfield_20014", true, `{"type":"array","items":"user","custom":"com.atlassian.servicedesk:sd-request-participants","customId":20014}`, model.FieldUsers},
		{"grouppicker is group", "customfield_20015", true, `{"type":"group","custom":"` + f12CT + `grouppicker","customId":20015}`, model.FieldGroup},
		{"multigrouppicker is groups", "customfield_20016", true, `{"type":"array","items":"group","custom":"` + f12CT + `multigrouppicker","customId":20016}`, model.FieldGroups},
		// version(s), component(s)
		{"version picker is version", "customfield_20017", true, `{"type":"version","custom":"` + f12CT + `version","customId":20017}`, model.FieldVersion},
		{"fixVersions is versions", "fixVersions", false, `{"type":"array","items":"version","system":"fixVersions"}`, model.FieldVersions},
		{"versions (affects) is versions", "versions", false, `{"type":"array","items":"version","system":"versions"}`, model.FieldVersions},
		{"components is components", "components", false, `{"type":"array","items":"component","system":"components"}`, model.FieldComponents},
		// labels
		{"system labels is labels", "labels", false, `{"type":"array","items":"string","system":"labels"}`, model.FieldLabels},
		{"custom labels is labels", "customfield_20018", true, `{"type":"array","items":"string","custom":"` + f12CT + `labels","customId":20018}`, model.FieldLabels},
		// project, team
		{"project is project", "project", false, `{"type":"project","system":"project"}`, model.FieldProject},
		{"type team is team", "customfield_20019", true, `{"type":"team","custom":"` + f12CT + `atlassian-team","customId":20019}`, model.FieldTeam},
		{"teams custom key with type any is team", "customfield_20020", true, `{"type":"any","custom":"com.atlassian.teams:rm-teams-custom-field-team","customId":20020}`, model.FieldTeam},
		{"atlassian-team custom key with type any is team", "customfield_20021", true, `{"type":"any","custom":"` + f12CT + `atlassian-team","customId":20021}`, model.FieldTeam},
		// unknown
		{"tempo account is unknown", "customfield_20022", true, `{"type":"any","custom":"com.tempoplugin.tempo-accounts:accounts.customfield","customId":20022}`, model.FieldUnknown},
		{"assets object is unknown", "customfield_20023", true, `{"type":"array","items":"json","custom":"com.atlassian.jira.plugins.cmdb:cmdb-object-cftype","customId":20023}`, model.FieldUnknown},
		{"service desk organizations are unknown", "customfield_20024", true, `{"type":"array","items":"sd-customerorganization","custom":"com.atlassian.servicedesk:sd-customer-organizations","customId":20024}`, model.FieldUnknown},
		{"service desk request type is unknown", "customfield_20025", true, `{"type":"sd-servicerequest","custom":"com.atlassian.servicedesk:vp-origin","customId":20025}`, model.FieldUnknown},
		{"type any without custom is unknown", "customfield_20026", true, `{"type":"any","customId":20026}`, model.FieldUnknown},
		{"array of string that is not labels is unknown", "customfield_20027", true, `{"type":"array","items":"string","custom":"` + f12CT + `textfield","customId":20027}`, model.FieldUnknown},
		{"rank is unknown", "customfield_10019", true, `{"type":"any","custom":"com.pyxis.greenhopper.jira:gh-lexo-rank","customId":10019}`, model.FieldUnknown},
		{"sprint is unknown", "customfield_10020", true, `{"type":"array","items":"json","custom":"com.pyxis.greenhopper.jira:gh-sprint","customId":10020}`, model.FieldUnknown},
		{"time tracking is unknown", "timetracking", false, `{"type":"timetracking","system":"timetracking"}`, model.FieldUnknown},
		{"bare component type is unknown", "customfield_20028", true, `{"type":"component","customId":20028}`, model.FieldUnknown},
		{"no schema at all is unknown", "customfield_20029", true, ``, model.FieldUnknown},
		{"future type is unknown", "customfield_20030", true, `{"type":"hologram","custom":"com.example:hologram","customId":20030}`, model.FieldUnknown},
	}
}

func f12FieldsResponse(rows []f12FieldRow) string {
	var out []string
	for i, r := range rows {
		entry := fmt.Sprintf(`{"id":%q,"key":%q,"name":%q,"custom":%t,"orderable":true,"navigable":true,"searchable":true,"clauseNames":[%q]`,
			r.id, r.id, fmt.Sprintf("Field %02d", i), r.custom, r.id)
		if r.schema != "" {
			entry += `,"schema":` + r.schema
		}
		out = append(out, entry+"}")
	}
	return "[" + strings.Join(out, ",") + "]"
}

func f12FieldsAdapter(t *testing.T, body string) (*Adapter, *wp19Srv) {
	t.Helper()
	srv := newF12Server(t, map[string]wp19Route{"GET /rest/api/3/field": wp19Ok(body)})
	return newWP19Adapter(t, srv.URL, wp19Meta()), srv
}

func TestFields_MapsEverySchemaToItsNeutralKind(t *testing.T) {
	defer wp19Guard(t)
	rows := f12FieldRows()
	a, srv := f12FieldsAdapter(t, f12FieldsResponse(rows))
	got, err := a.Fields(context.Background())
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(got) != len(rows) {
		t.Fatalf("FieldDefs = %d, want one per response entry (%d)", len(got), len(rows))
	}
	for i, r := range rows {
		d := got[i]
		if d.ID != r.id {
			t.Fatalf("FieldDefs[%d].ID = %q, want %q (response order)", i, d.ID, r.id)
		}
		if d.Kind != r.want {
			t.Errorf("%s: Kind = %q, want %q", r.name, d.Kind, r.want)
		}
	}
	if n := len(srv.to("GET", "/rest/api/3/field")); n != 1 {
		t.Errorf("GET /field requests = %d, want 1", n)
	}
}

func TestFields_EveryKindButComponentIsProduced(t *testing.T) {
	defer wp19Guard(t)
	rows := f12FieldRows()
	a, _ := f12FieldsAdapter(t, f12FieldsResponse(rows))
	got, err := a.Fields(context.Background())
	if err != nil || len(got) == 0 {
		t.Fatalf("Fields: %v (%d defs)", err, len(got))
	}
	seen := map[string]bool{}
	for _, d := range got {
		seen[d.Kind] = true
		if !f12ValidKind(d.Kind) {
			t.Errorf("%s has Kind %q which is not in model.FieldKinds", d.ID, d.Kind)
		}
	}
	var missing []string
	for _, k := range model.FieldKinds {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if strings.Join(missing, ",") != model.FieldComponent {
		t.Errorf("kinds never produced = %v, want exactly [component] (see SPEC-NOTE component)", missing)
	}
}

func f12ValidKind(k string) bool {
	for _, v := range model.FieldKinds {
		if v == k {
			return true
		}
	}
	return false
}

func TestFields_CarriesNameCustomCustomTypeAndSchemaVerbatim(t *testing.T) {
	defer wp19Guard(t)
	body := `[
		{"id":"customfield_10103","key":"customfield_10103","name":"Platform","custom":true,
		 "schema":{"type":"option","custom":"` + f12CT + `select","customId":10103}},
		{"id":"duedate","key":"duedate","name":"Due date","custom":false,"schema":{"type":"date","system":"duedate"}},
		{"id":"customfield_10199","name":"No schema","custom":true}
	]`
	a, _ := f12FieldsAdapter(t, body)
	got, err := a.Fields(context.Background())
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("FieldDefs = %+v", got)
	}
	cf, sys, none := got[0], got[1], got[2]
	if cf.ID != "customfield_10103" || cf.Name != "Platform" || !cf.Custom || cf.CustomType != f12CT+"select" {
		t.Errorf("custom field = %+v", cf)
	}
	if !f12JSONEqual(cf.Schema, `{"type":"option","custom":"`+f12CT+`select","customId":10103}`) {
		t.Errorf("custom Schema = %s, want the schema verbatim", cf.Schema)
	}
	if sys.ID != "duedate" || sys.Name != "Due date" || sys.Custom || sys.CustomType != "" || sys.Kind != model.FieldDate {
		t.Errorf("system field = %+v, want Custom false and CustomType empty", sys)
	}
	if !f12JSONEqual(sys.Schema, `{"type":"date","system":"duedate"}`) {
		t.Errorf("system Schema = %s", sys.Schema)
	}
	if none.Kind != model.FieldUnknown || none.CustomType != "" {
		t.Errorf("field without schema = %+v, want unknown", none)
	}
	if len(none.Schema) != 0 && string(none.Schema) != "null" {
		t.Errorf("field without schema keeps Schema %s, want empty", none.Schema)
	}
}

func f12JSONEqual(raw json.RawMessage, want string) bool {
	var a, b any
	if json.Unmarshal(raw, &a) != nil || json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

func TestFields_SkipsEntriesWithoutAnIdAndKeepsResponseOrder(t *testing.T) {
	defer wp19Guard(t)
	a, _ := f12FieldsAdapter(t, `[
		{"id":"zeta","name":"Zeta","custom":false,"schema":{"type":"string","system":"zeta"}},
		{"name":"No id","custom":true,"schema":{"type":"string","custom":"`+f12CT+`textfield"}},
		{"id":"alpha","name":"Alpha","custom":false,"schema":{"type":"number","system":"alpha"}}]`)
	got, err := a.Fields(context.Background())
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	if len(got) != 2 || got[0].ID != "zeta" || got[1].ID != "alpha" {
		t.Fatalf("FieldDefs = %+v, want zeta then alpha (order of the response, entry without id skipped)", got)
	}
}

func TestFields_SecondCallAnswersFromMemory(t *testing.T) {
	defer wp19Guard(t)
	a, srv := f12FieldsAdapter(t, f12FieldsResponse(f12FieldRows()[:3]))
	first, err := a.Fields(context.Background())
	if err != nil || len(first) != 3 {
		t.Fatalf("first Fields: %v (%d)", err, len(first))
	}
	second, err := a.Fields(context.Background())
	if err != nil || len(second) != 3 {
		t.Fatalf("second Fields: %v (%d)", err, len(second))
	}
	if n := len(srv.to("GET", "/rest/api/3/field")); n != 1 {
		t.Errorf("GET /field requests after two calls = %d, want 1", n)
	}
}

func TestFields_MapsErrors(t *testing.T) {
	defer wp19Guard(t)
	tests := []struct {
		name   string
		status int
		want   apperr.Code
	}{
		{"401", 401, apperr.AuthInvalid},
		{"403", 403, apperr.Forbidden},
		{"500", 500, apperr.SourceHTTP},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer wp19Guard(t)
			srv := newF12Server(t, map[string]wp19Route{"GET /rest/api/3/field": wp19Fail(tc.status, `{"errorMessages":["nope"]}`)})
			a := newWP19Adapter(t, srv.URL, wp19Meta())
			_, err := a.Fields(context.Background())
			wp19AssertCode(t, err, tc.want)
		})
	}
}

func TestFields_DoesNotLeakTheTokenInErrors(t *testing.T) {
	defer wp19Guard(t)
	srv := newF12Server(t, map[string]wp19Route{"GET /rest/api/3/field": wp19Fail(500, `{"errorMessages":["boom"]}`)})
	a := newWP19Adapter(t, srv.URL, wp19Meta())
	_, err := a.Fields(context.Background())
	wp19AssertCode(t, err, apperr.SourceHTTP)
	if s := fmt.Sprintf("%v %+v", err, err); strings.Contains(s, "tok-123") {
		t.Errorf("error leaks the token: %s", s)
	}
}

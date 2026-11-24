package view

// Rail tests for BuildTicket (ux-spec §14.6, architecture.md "Rail folding is done in Go"): groups and their order,
// Not-set folding, Assignee always a row, custom fields per prefs, value rendering per kind, development and dates.
// The SPEC-NOTEs of ticket_test.go apply.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
)

// railTicket is MOB-1 with a registry and values for the standard fields.
func railTicket(t *testing.T, w *world) cache.Ticket {
	t.Helper()
	tk := tkOf(t, w, "MOB-1")
	tk.Issue.SprintIDs = []string{"s-42"}
	tk.Watchers = 4
	tk.Registry = []cache.TicketFieldDef{
		tkDef("reporter", "Reporter", model.FieldUser, false), tkDef("priority", "Priority", model.FieldOption, false),
		tkDef("duedate", "Due date", model.FieldDate, false), tkDef("labels", "Labels", model.FieldLabels, false),
		tkDef("components", "Components", model.FieldComponents, false), tkDef("fixVersions", "Fix versions", model.FieldVersions, false),
		tkDef("timetracking", "Time tracking", model.FieldUnknown, false),
		tkDef("customfield_10016", "Story point estimate", model.FieldNumber, true),
	}
	tk.Fields = []cache.TicketField{
		tkField("reporter", model.FieldUser, func(f *model.FieldValue) { f.Users = []string{"a2"} }),
		tkField("priority", model.FieldOption, func(f *model.FieldValue) { f.Text = "High" }),
		tkField("duedate", model.FieldDate, func(f *model.FieldValue) { f.Text = "2026-12-04" }),
		tkField("labels", model.FieldLabels, func(f *model.FieldValue) { f.Items = []string{"units", "recipes"} }),
		tkField("fixVersions", model.FieldVersions, func(f *model.FieldValue) { f.Items = []string{"2026.12"} }),
		tkField("timetracking", model.FieldUnknown, func(f *model.FieldValue) {
			f.Raw = json.RawMessage(`{"originalEstimate":"1d","timeSpent":"4h","originalEstimateSeconds":28800,"timeSpentSeconds":14400}`)
		}),
		tkField("customfield_10016", model.FieldNumber, func(f *model.FieldValue) { f.Number = f64(5) }),
	}
	return tk
}

func TestRail_GroupsInOrderAndEmptyGroupsAreOmitted(t *testing.T) {
	w := mainWorld()
	d := tkBuild(t, w, railTicket(t, w), TicketFieldPrefs{}, TicketOptions{})
	if got := strings.Join(groupIDs(d), ","); got != "state,details,dates" {
		t.Errorf("groups = %s, want state,details,dates (no custom values and no dev events: those groups are omitted)", got)
	}
	for _, g := range d.Rail {
		want := map[string]string{RailState: "State", RailDetails: "Details", RailDates: "Dates"}[g.ID]
		if g.Label != want {
			t.Errorf("group %s label = %q, want %q", g.ID, g.Label, want)
		}
	}

	// With custom values and dev events the five groups appear in the documented order.
	w2 := mainWorld()
	tk := railTicket(t, w2)
	tk.Registry = append(tk.Registry, tkDef("customfield_20001", "Zeta", model.FieldText, true))
	tk.Fields = append(tk.Fields, tkField("customfield_20001", model.FieldText, func(f *model.FieldValue) { f.Text = "z" }))
	tk.History = tkOf(t, w2, "MOB-2").History
	d = tkBuild(t, w2, tk, TicketFieldPrefs{}, TicketOptions{})
	if got := strings.Join(groupIDs(d), ","); got != "state,details,custom,development,dates" {
		t.Errorf("groups = %s", got)
	}
	if g, _ := tkGroup(d, RailCustom); g.Label != "Custom fields" {
		t.Errorf("custom label = %q", g.Label)
	}
	if g, _ := tkGroup(d, RailDevelopment); g.Label != "Development" {
		t.Errorf("development label = %q", g.Label)
	}
}

func TestRail_StateGroupStatusAssigneeFlag(t *testing.T) {
	w := mainWorld()
	d := tkBuild(t, w, railTicket(t, w), TicketFieldPrefs{}, TicketOptions{SelfAccountID: "a1"})
	g, _ := tkGroup(d, RailState)
	if got := strings.Join(rowIDs(g), ","); got != "status,assignee,flagged" {
		t.Fatalf("state rows = %s", got)
	}
	st := g.Rows[0]
	if st.Label != "Status" || st.Kind != RailKindStatus || st.Edit != "status" || st.Text != "In Progress" {
		t.Errorf("status row = %+v", st)
	}
	as := g.Rows[1]
	if as.Label != "Assignee" || as.Kind != model.FieldUser || as.Edit != "assignee" || len(as.Users) != 1 ||
		as.Users[0] != (PersonRef{AccountID: "a1", Name: "Iris Calloway", Initials: "IC", Self: true}) {
		t.Errorf("assignee row = %+v", as)
	}
	if len(g.NotSet) != 0 {
		t.Errorf("state NotSet = %v, want none", g.NotSet)
	}
}

func TestRail_AnEmptyAssigneeStillHasARow(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-10") // unassigned
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	as := tkRow(t, d, RailState, "assignee")
	if as.Edit != "assignee" || as.Users == nil || len(as.Users) != 0 {
		t.Errorf("unassigned row = %+v, want an editable row with no users", as)
	}
	if g, _ := tkGroup(d, RailState); len(g.NotSet) != 0 {
		t.Errorf("state NotSet = %v: the assignee is never folded", g.NotSet)
	}
}

func TestRail_DetailsRowsInRailOrderWithValueRendering(t *testing.T) {
	w := mainWorld()
	tk := railTicket(t, w)
	tk.Environment = rich("iOS 18")
	tk.Parent = &cache.TicketLink{Relation: "parent", OtherKey: "MOB-100", OtherSummary: "Recipe units v2", OtherTypeKind: "epic"}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{SelfAccountID: "a1"})
	g, _ := tkGroup(d, RailDetails)
	if got := strings.Join(rowIDs(g), ","); got != "reporter,priority,parent,sprint,customfield_10016,duedate,labels,fixVersions,timetracking,environment" {
		t.Fatalf("details rows = %s", got)
	}
	if got := strings.Join(g.NotSet, "|"); got != "Components" {
		t.Errorf("details NotSet = %q, want Components (the only empty registry field)", got)
	}

	rep := tkRow(t, d, RailDetails, "reporter")
	if rep.Label != "Reporter" || rep.Kind != model.FieldUser || rep.Edit != "" || len(rep.Users) != 1 || rep.Users[0].Name != "Silas Webb" || rep.Users[0].Initials != "SW" || rep.Users[0].Self {
		t.Errorf("reporter = %+v", rep)
	}
	if r := tkRow(t, d, RailDetails, "priority"); r.Label != "Priority" || r.Kind != model.FieldOption || r.Text != "High" || r.Mono {
		t.Errorf("priority = %+v", r)
	}
	if r := tkRow(t, d, RailDetails, "parent"); r.Label != "Parent" || r.Kind != RailKindParent || r.Text != "MOB-100" || r.Sub != "Recipe units v2" || !r.Mono {
		t.Errorf("parent = %+v", r)
	}
	if r := tkRow(t, d, RailDetails, "sprint"); r.Label != "Sprint" || r.Kind != RailKindSprint || r.Text != "Sprint s-42" || r.Sub != "ends 28 Nov" {
		t.Errorf("sprint = %+v", r)
	}
	if r := tkRow(t, d, RailDetails, "customfield_10016"); r.Label != "Points" || r.Kind != model.FieldNumber || r.Number == nil || *r.Number != 5 || !r.Mono {
		t.Errorf("points = %+v", r)
	}
	if r := tkRow(t, d, RailDetails, "duedate"); r.Label != "Due" || r.Kind != model.FieldDate || r.Text != "4 Dec 2026" || !r.Mono {
		t.Errorf("due = %+v", r)
	}
	if r := tkRow(t, d, RailDetails, "labels"); r.Label != "Labels" || r.Kind != model.FieldLabels || !reflect.DeepEqual(r.Items, []string{"units", "recipes"}) {
		t.Errorf("labels = %+v", r)
	}
	if r := tkRow(t, d, RailDetails, "fixVersions"); r.Label != "Fix version" || r.Kind != model.FieldVersions || !reflect.DeepEqual(r.Items, []string{"2026.12"}) || !r.Mono {
		t.Errorf("fix versions = %+v", r)
	}
	tm := tkRow(t, d, RailDetails, "timetracking")
	if tm.Label != "Time" || tm.Kind != RailKindTime || tm.Text != "4h of 8h" || tm.Progress == nil || *tm.Progress != 0.5 || !tm.Mono {
		t.Errorf("time = %+v", tm)
	}
	env := tkRow(t, d, RailDetails, "environment")
	if env.Label != "Environment" || env.Kind != model.FieldParagraph || env.Rich == nil || len(env.Rich.Nodes) == 0 {
		t.Errorf("environment = %+v", env)
	}
}

func TestRail_NotSetFoldingInRailOrderAndRowlessWhenEmpty(t *testing.T) {
	w := mainWorld()
	tk := railTicket(t, w)
	tk.Issue.SprintIDs = nil
	tk.Fields = nil // every registry field is now empty
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, ok := tkGroup(d, RailDetails)
	if !ok || len(g.Rows) != 0 {
		t.Fatalf("details group = %+v, want present with no rows", g)
	}
	want := "Reporter|Priority|Parent|Sprint|Points|Due|Labels|Components|Fix version|Time|Environment"
	if got := strings.Join(g.NotSet, "|"); got != want {
		t.Errorf("details NotSet = %q\n want %q", got, want)
	}
	dates, _ := tkGroup(d, RailDates)
	if got := strings.Join(dates.NotSet, "|"); got != "Resolved" {
		t.Errorf("dates NotSet = %q, want Resolved (Watchers is known, so a row)", got)
	}
}

func TestRail_NotSetOnlyListsWhatTheSiteHasAndWhatApplies(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-11") // an epic; empty registry
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, _ := tkGroup(d, RailDetails)
	if got := strings.Join(g.NotSet, "|"); got != "Sprint|Environment" {
		t.Errorf("details NotSet = %q, want Sprint|Environment: no registry fields, and an epic has no Parent", got)
	}

	w.snap.Config.BoardType = "kanban"
	d = tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, _ = tkGroup(d, RailDetails)
	if got := strings.Join(g.NotSet, "|"); got != "Environment" {
		t.Errorf("kanban details NotSet = %q, want Environment", got)
	}

	// Sprints that are not active are unnamed: neither a row nor a Not-set entry.
	w2 := mainWorld()
	tk2 := tkOf(t, w2, "MOB-1")
	tk2.Issue.SprintIDs = []string{"s-old"}
	d = tkBuild(t, w2, tk2, TicketFieldPrefs{}, TicketOptions{})
	g, _ = tkGroup(d, RailDetails)
	for _, id := range rowIDs(g) {
		if id == "sprint" {
			t.Errorf("a sprint row for a sprint that is not active")
		}
	}
	for _, l := range g.NotSet {
		if l == "Sprint" {
			t.Errorf("Sprint listed as not set although the issue has a sprint")
		}
	}
}

func TestRail_CustomFieldsFollowThePrefs(t *testing.T) {
	w := mainWorld()
	tk := railTicket(t, w)
	text := func(v string) func(*model.FieldValue) { return func(f *model.FieldValue) { f.Text = v } }
	tk.Registry = append(tk.Registry,
		tkDef("customfield_20001", "zeta", model.FieldText, true), tkDef("customfield_20002", "Team", model.FieldText, true),
		tkDef("customfield_20003", "Secret", model.FieldText, true), tkDef("customfield_20004", "Alpha", model.FieldText, true),
		tkDef("customfield_20005", "Workaround", model.FieldParagraph, true), tkDef("customfield_20006", "Beta", model.FieldText, true),
		tkDef("customfield_20007", "Empty custom", model.FieldText, true), tkDef("customfield_20008", "Nobody uses it", model.FieldText, true))
	tk.Fields = append(tk.Fields,
		tkField("customfield_20001", model.FieldText, text("z")), tkField("customfield_20002", model.FieldText, text("Saffron")),
		tkField("customfield_20003", model.FieldText, text("hidden value")), tkField("customfield_20004", model.FieldText, text("a")),
		tkField("customfield_20006", model.FieldText, text("b")))
	prefs := TicketFieldPrefs{
		Pinned: []FieldPref{
			{FieldID: "customfield_20002", Name: "Team", Kind: model.FieldText, Position: 0},
			{FieldID: "customfield_20005", Name: "Workaround", Kind: model.FieldParagraph, Position: 1},
		},
		Hidden: []FieldPref{{FieldID: "customfield_20003", Name: "Secret", Kind: model.FieldText, Position: -1, Hidden: true}},
	}
	// Workaround (pinned) is used by nobody else, and it is still listed; "Nobody uses it" is not pinned and is not listed;
	// "Empty custom" is filled in on another issue of the space, so it is.
	d := tkBuild(t, w, tk, prefs, TicketOptions{FieldsInUse: map[string]bool{"customfield_20007": true, "customfield_20001": true}})
	g, ok := tkGroup(d, RailCustom)
	if !ok {
		t.Fatalf("no custom group")
	}
	// Pinned first in pinned order, then the remaining non-hidden non-empty ones A-Z (case-insensitive); the story-points
	// field lives in Details; hidden and empty ones have no row.
	if got := strings.Join(rowIDs(g), ","); got != "customfield_20002,customfield_20004,customfield_20006,customfield_20001" {
		t.Errorf("custom rows = %s, want Team, Alpha, Beta, zeta", got)
	}
	if g.Rows[0].Label != "Team" || g.Rows[1].Label != "Alpha" || g.Rows[3].Label != "zeta" || g.Rows[0].Text != "Saffron" {
		t.Errorf("custom rows = %+v", g.Rows)
	}
	if got := strings.Join(g.NotSet, "|"); got != "Workaround|Empty custom" {
		t.Errorf("custom NotSet = %q, want the empty pinned field first, then the other empty custom fields A-Z (hidden ones never appear)", got)
	}
}

func TestRail_CustomNotSetIsDrivenByTheRegistryExceptFieldsModelledElsewhere(t *testing.T) {
	w := mainWorld()
	tk := railTicket(t, w)
	def := func(id, name, typ string) cache.TicketFieldDef {
		d := tkDef(id, name, model.FieldText, true)
		d.Def.CustomType = typ
		return d
	}
	tk.Registry = append(tk.Registry,
		def("customfield_30001", "Risk", ""), def("customfield_30002", "audience", ""), def("customfield_30003", "Budget", ""),
		def("customfield_30004", "Sprint", "com.pyxis.greenhopper.jira:gh-sprint"),
		def("customfield_30005", "Rank", "com.pyxis.greenhopper.jira:gh-lexo-rank"),
		def("customfield_30006", "Flagged", "com.atlassian.jira.plugin.system.customfieldtypes:multicheckboxes"),
		def("customfield_30007", "Development", "com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf"),
		def("customfield_30008", "Secret", ""))
	prefs := TicketFieldPrefs{Hidden: []FieldPref{{FieldID: "customfield_30008", Hidden: true, Position: -1}}}
	inUse := map[string]bool{}
	for _, d := range tk.Registry {
		inUse[d.Def.ID] = true // every field is filled in somewhere in the space
	}
	d := tkBuild(t, w, tk, prefs, TicketOptions{FieldsInUse: inUse})
	g, ok := tkGroup(d, RailCustom)
	if !ok || len(g.Rows) != 0 {
		t.Fatalf("custom group = %+v, want present (Not set only) with no rows", g)
	}
	if got := strings.Join(g.NotSet, "|"); got != "audience|Budget|Risk" {
		t.Errorf("custom NotSet = %q, want audience|Budget|Risk: A-Z, no sprint/rank/flagged/development, no hidden, no story points", got)
	}
}

func TestRail_CustomNotSetIsBoundedToFieldsUsedElsewhereInTheSpace(t *testing.T) {
	w := mainWorld()
	tk := railTicket(t, w)
	tk.Registry = append(tk.Registry,
		tkDef("customfield_40001", "Used by others", model.FieldText, true),
		tkDef("customfield_40002", "Used by nobody", model.FieldText, true),
		tkDef("customfield_40003", "Pinned and unused", model.FieldText, true))
	prefs := TicketFieldPrefs{Pinned: []FieldPref{{FieldID: "customfield_40003", Name: "Pinned and unused", Position: 0}}}

	// Unknown usage (nil): only pinned empty fields are listed.
	d := tkBuild(t, w, tk, prefs, TicketOptions{})
	g, _ := tkGroup(d, RailCustom)
	if got := strings.Join(g.NotSet, "|"); got != "Pinned and unused" {
		t.Errorf("nil FieldsInUse: NotSet = %q, want only the pinned empty field", got)
	}
	// Known usage: the pinned field first, then only the unpinned ones some other issue of the space has filled in.
	d = tkBuild(t, w, tk, prefs, TicketOptions{FieldsInUse: map[string]bool{"customfield_40001": true}})
	g, _ = tkGroup(d, RailCustom)
	if got := strings.Join(g.NotSet, "|"); got != "Pinned and unused|Used by others" {
		t.Errorf("NotSet = %q, want Pinned and unused|Used by others (Used by nobody is dropped)", got)
	}
}

func TestRail_AHiddenFieldIsAbsentEverywhere(t *testing.T) {
	w := mainWorld()
	tk := railTicket(t, w)
	prefs := TicketFieldPrefs{Hidden: []FieldPref{{FieldID: "priority", Hidden: true, Position: -1}, {FieldID: "components", Hidden: true, Position: -1}}}
	d := tkBuild(t, w, tk, prefs, TicketOptions{})
	g, _ := tkGroup(d, RailDetails)
	for _, id := range rowIDs(g) {
		if id == "priority" {
			t.Errorf("hidden priority has a row")
		}
	}
	for _, l := range g.NotSet {
		if l == "Components" || l == "Priority" {
			t.Errorf("hidden field %q listed as not set", l)
		}
	}
}

func TestRail_ValueRenderingForEveryFieldKind(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	type fc struct {
		id, name, kind string
		mut            func(*model.FieldValue)
	}
	cases := []fc{
		{"c_text", "A text", model.FieldText, func(f *model.FieldValue) { f.Text = "units-v2" }},
		{"c_para", "B paragraph", model.FieldParagraph, func(f *model.FieldValue) { f.Rich = rich("Release note") }},
		{"c_num", "C number", model.FieldNumber, func(f *model.FieldValue) { f.Number = f64(20) }},
		{"c_date", "D date", model.FieldDate, func(f *model.FieldValue) { f.Text = "2026-12-07" }},
		{"c_dt", "E datetime", model.FieldDateTime, func(f *model.FieldValue) { f.Text = "2026-12-01T09:00:00Z" }},
		{"c_url", "F url", model.FieldURL, func(f *model.FieldValue) { f.Text = "https://docs.example.com/recipes/units-v2" }},
		{"c_opt", "G option", model.FieldOption, func(f *model.FieldValue) { f.Text = "High" }},
		{"c_opts", "H options", model.FieldOptions, func(f *model.FieldValue) { f.Items = []string{"iOS", "Android"} }},
		{"c_casc", "I cascade", model.FieldCascade, func(f *model.FieldValue) { f.Items = []string{"North America", "United States"} }},
		{"c_user", "J user", model.FieldUser, func(f *model.FieldValue) { f.Users = []string{"a1"} }},
		{"c_users", "K users", model.FieldUsers, func(f *model.FieldValue) { f.Users = []string{"a2", "nobody"} }},
		{"c_group", "L group", model.FieldGroup, func(f *model.FieldValue) { f.Text = "mobile-leads" }},
		{"c_groups", "M groups", model.FieldGroups, func(f *model.FieldValue) { f.Items = []string{"a", "b"} }},
		{"c_ver", "N version", model.FieldVersion, func(f *model.FieldValue) { f.Text = "3.8" }},
		{"c_vers", "O versions", model.FieldVersions, func(f *model.FieldValue) { f.Items = []string{"3.9"} }},
		{"c_comp", "P component", model.FieldComponent, func(f *model.FieldValue) { f.Text = "Recipe page" }},
		{"c_comps", "Q components", model.FieldComponents, func(f *model.FieldValue) { f.Items = []string{"Recipe page"} }},
		{"c_labels", "R labels", model.FieldLabels, func(f *model.FieldValue) { f.Items = []string{"x"} }},
		{"c_proj", "S project", model.FieldProject, func(f *model.FieldValue) { f.Text = "Saffron Mobile" }},
		{"c_team", "T team", model.FieldTeam, func(f *model.FieldValue) { f.Text = "Mobile Core" }},
		{"c_unk", "U unknown", model.FieldUnknown, func(f *model.FieldValue) { f.Raw = json.RawMessage(`{"lexo":"0|i0001r:"}`) }},
	}
	for _, c := range cases {
		tk.Registry = append(tk.Registry, tkDef(c.id, c.name, c.kind, true))
		tk.Fields = append(tk.Fields, tkField(c.id, c.kind, c.mut))
	}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, _ := tkGroup(d, RailCustom)
	if len(g.Rows) != len(cases) {
		t.Fatalf("custom rows = %d, want %d: %v", len(g.Rows), len(cases), rowIDs(g))
	}
	row := func(id string) RailRow { return tkRow(t, d, RailCustom, id) }

	if r := row("c_text"); r.Kind != model.FieldText || r.Text != "units-v2" || r.Mono {
		t.Errorf("text = %+v", r)
	}
	if r := row("c_para"); r.Kind != model.FieldParagraph || r.Rich == nil || r.Text != "" || len(findNodes(*r.Rich, adf.NodeParagraph)) == 0 {
		t.Errorf("paragraph = %+v", r)
	}
	if r := row("c_num"); r.Kind != model.FieldNumber || r.Number == nil || *r.Number != 20 || !r.Mono {
		t.Errorf("number = %+v", r)
	}
	if r := row("c_date"); r.Kind != model.FieldDate || r.Text != "7 Dec 2026" || !r.Mono {
		t.Errorf("date = %+v", r)
	}
	if r := row("c_dt"); r.Kind != model.FieldDateTime || r.Text != "1 Dec 2026 03:00" || !r.Mono {
		t.Errorf("datetime = %+v", r)
	}
	if r := row("c_url"); r.Kind != model.FieldURL || r.URL != "https://docs.example.com/recipes/units-v2" || r.URLLabel != "docs.example.com › units-v2" || r.Text != "" {
		t.Errorf("url = %+v", r)
	}
	if r := row("c_opt"); r.Kind != model.FieldOption || r.Text != "High" {
		t.Errorf("option = %+v", r)
	}
	if r := row("c_opts"); r.Kind != model.FieldOptions || !reflect.DeepEqual(r.Items, []string{"iOS", "Android"}) {
		t.Errorf("options = %+v", r)
	}
	if r := row("c_casc"); r.Kind != model.FieldCascade || !reflect.DeepEqual(r.Items, []string{"North America", "United States"}) {
		t.Errorf("cascade = %+v", r)
	}
	if r := row("c_user"); r.Kind != model.FieldUser || len(r.Users) != 1 || r.Users[0].Name != "Iris Calloway" {
		t.Errorf("user = %+v", r)
	}
	if r := row("c_users"); r.Kind != model.FieldUsers || len(r.Users) != 2 || r.Users[0].Name != "Silas Webb" || r.Users[1].AccountID != "nobody" || r.Users[1].Name == "" {
		t.Errorf("users = %+v", r)
	}
	for _, id := range []string{"c_group", "c_ver", "c_comp", "c_proj", "c_team"} {
		if r := row(id); r.Text == "" {
			t.Errorf("%s = %+v, want Text set", id, r)
		}
	}
	for _, id := range []string{"c_groups", "c_vers", "c_comps", "c_labels"} {
		if r := row(id); len(r.Items) == 0 {
			t.Errorf("%s = %+v, want Items set", id, r)
		}
	}
	if r := row("c_unk"); r.Kind != model.FieldUnknown || r.Raw != `{"lexo":"0|i0001r:"}` || !r.Mono {
		t.Errorf("unknown = %+v", r)
	}
	// Custom rows keep the field's own name as the label.
	if r := row("c_users"); r.Label != "K users" {
		t.Errorf("label = %q", r.Label)
	}
}

func TestRail_TimeTrackingVariants(t *testing.T) {
	w := mainWorld()
	for _, c := range []struct {
		raw      string
		text     string
		progress *float64
	}{
		{`{"originalEstimateSeconds":28800,"timeSpentSeconds":14400}`, "4h of 8h", f64(0.5)},
		{`{"originalEstimateSeconds":28800,"timeSpentSeconds":36000}`, "10h of 8h", f64(1)},
		{`{"timeSpentSeconds":5400}`, "1.5h", nil},
		{`{"originalEstimateSeconds":3600}`, "0h of 1h", f64(0)},
		{`{"originalEstimateSeconds":28800,"timeSpentSeconds":1800}`, "30m of 8h", f64(1800.0 / 28800.0)},
	} {
		tk := tkOf(t, w, "MOB-1")
		tk.Registry = []cache.TicketFieldDef{tkDef("timetracking", "Time tracking", model.FieldUnknown, false)}
		tk.Fields = []cache.TicketField{tkField("timetracking", model.FieldUnknown, func(f *model.FieldValue) { f.Raw = json.RawMessage(c.raw) })}
		d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
		r := tkRow(t, d, RailDetails, "timetracking")
		if r.Text != c.text || (c.progress == nil) != (r.Progress == nil) || (c.progress != nil && *r.Progress != *c.progress) {
			t.Errorf("%s: text %q progress %v, want %q %v", c.raw, r.Text, r.Progress, c.text, c.progress)
		}
	}
	// A time-tracking value with nothing in it is not set.
	tk := tkOf(t, w, "MOB-1")
	tk.Registry = []cache.TicketFieldDef{tkDef("timetracking", "Time tracking", model.FieldUnknown, false)}
	tk.Fields = []cache.TicketField{tkField("timetracking", model.FieldUnknown, func(f *model.FieldValue) { f.Raw = json.RawMessage(`{}`) })}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, _ := tkGroup(d, RailDetails)
	for _, id := range rowIDs(g) {
		if id == "timetracking" {
			t.Errorf("an empty time-tracking value has a row")
		}
	}
	if !contains(g.NotSet, "Time") {
		t.Errorf("NotSet = %v, want Time", g.NotSet)
	}
}

func TestRail_DevelopmentGroupAndSummary(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-2") // 2 commits + 1 merge commit on Nov 24, an open PR on Nov 25 09:00
	tk.History.Dev = append(tk.History.Dev,
		model.DevEvent{Kind: model.DevPR, ID: "mob#6", Repo: "mob", At: nv(22, 9, 0), Status: "OPEN"},
		model.DevEvent{Kind: model.DevPR, ID: "mob#6", Repo: "mob", At: nv(23, 9, 0), Status: "MERGED"})
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, ok := tkGroup(d, RailDevelopment)
	if !ok {
		t.Fatalf("no development group")
	}
	if got := strings.Join(rowIDs(g), ","); got != "dev.commits,dev.pr.7,dev.pr.6" {
		t.Fatalf("development rows = %s (commits, then PRs newest update first; no branch row without a known branch)", got)
	}
	c := g.Rows[0]
	if c.Label != "Commits" || c.Kind != RailKindCommits || c.Text != "2 commits" || c.Sub != "last 1d" {
		t.Errorf("commits row = %+v (merge commits are not counted)", c)
	}
	// Rows synced before migration 0004 have no title: the repository is the text, as before.
	if p := g.Rows[1]; p.Label != "#7" || p.Kind != RailKindPR || p.Text != "mob" || p.Sub != "" || !reflect.DeepEqual(p.Items, []string{"OPEN"}) {
		t.Errorf("pr 7 = %+v", p)
	}
	if p := g.Rows[2]; p.Label != "#6" || !reflect.DeepEqual(p.Items, []string{"MERGED"}) {
		t.Errorf("pr 6 = %+v, want its latest status MERGED", p)
	}
	if d.Dev == nil || d.Dev.Commits != 2 || d.Dev.LastCommitAt != nv(24, 11, 0).UnixMilli() || d.Dev.Branch != "" || len(d.Dev.PRs) != 2 ||
		d.Dev.PRs[0] != (DevPR{ID: "7", Title: "mob", Repo: "mob", Status: "OPEN"}) || d.Dev.PRs[1].Status != "MERGED" {
		t.Errorf("dev = %+v", d.Dev)
	}

	// One commit, a few minutes ago: singular and short age.
	one := tkOf(t, w, "MOB-1")
	one.History.Dev = []model.DevEvent{{Kind: model.DevCommit, ID: "x", Repo: "mob", At: nv(25, 11, 40)}}
	d = tkBuild(t, w, one, TicketFieldPrefs{}, TicketOptions{})
	if r := tkRow(t, d, RailDevelopment, "dev.commits"); r.Text != "1 commit" || r.Sub != "last 20m" {
		t.Errorf("commits row = %+v", r)
	}
}

func TestRail_DevelopmentBranchAndPullRequestTitles(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-2")
	tk.History.Dev = append(tk.History.Dev,
		// PR 6: opened with its title and branch, later merged by an observation that lacks both (kept from the first).
		model.DevEvent{Kind: model.DevPR, ID: "mob#6", Repo: "mob", At: nv(22, 9, 0), Status: "OPEN", Title: "Older work", Branch: "feature/MOB-2-older"},
		model.DevEvent{Kind: model.DevPR, ID: "mob#6", Repo: "mob", At: nv(23, 9, 0), Status: "MERGED"},
		// PR 7 is the newest update: its branch is the Branch row.
		model.DevEvent{Kind: model.DevPR, ID: "svc#7", Repo: "mob", At: nv(25, 10, 0), Status: "OPEN", Title: "Versioned image URLs", Branch: "feature/MOB-2-versioned-images"})
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, _ := tkGroup(d, RailDevelopment)
	if got := strings.Join(rowIDs(g), ","); got != "dev.branch,dev.commits,dev.pr.7,dev.pr.6" {
		t.Fatalf("development rows = %s, want the Branch row first", got)
	}
	if b := g.Rows[0]; b.Label != "Branch" || b.Kind != RailKindBranch || b.Text != "feature/MOB-2-versioned-images" || !b.Mono {
		t.Errorf("branch row = %+v", b)
	}
	if p := g.Rows[2]; p.Label != "#7" || p.Text != "Versioned image URLs" || p.Sub != "mob" || !reflect.DeepEqual(p.Items, []string{"OPEN"}) {
		t.Errorf("pr 7 = %+v, want the title as text and the repository as secondary", p)
	}
	if p := g.Rows[3]; p.Text != "Older work" || p.Sub != "mob" || !reflect.DeepEqual(p.Items, []string{"MERGED"}) {
		t.Errorf("pr 6 = %+v, want the title kept from its earlier observation and the latest status", p)
	}
	if d.Dev == nil || d.Dev.Branch != "feature/MOB-2-versioned-images" || d.Dev.PRs[0] != (DevPR{ID: "7", Title: "Versioned image URLs", Repo: "mob", Status: "OPEN"}) {
		t.Errorf("dev = %+v", d.Dev)
	}

	// A hidden branch row stays hidden; the summary still carries the branch.
	if d := tkBuild(t, w, tk, TicketFieldPrefs{Hidden: []FieldPref{{FieldID: "dev.branch", Hidden: true, Position: -1}}}, TicketOptions{}); d.Dev.Branch == "" {
		t.Errorf("Dev.Branch empty with the row hidden")
	}

	// A commit-only ticket (no pull request, so no branch) has no Branch row.
	one := tkOf(t, w, "MOB-1")
	one.History.Dev = []model.DevEvent{{Kind: model.DevCommit, ID: "x", Repo: "mob", At: nv(25, 11, 40)}}
	d = tkBuild(t, w, one, TicketFieldPrefs{}, TicketOptions{})
	if g, _ := tkGroup(d, RailDevelopment); strings.Contains(strings.Join(rowIDs(g), ","), "dev.branch") {
		t.Errorf("branch row without a known branch: %v", rowIDs(g))
	}
}

func TestRail_NoDevelopmentGroupWithoutEventsOrWhenTheDevApiIsUnavailable(t *testing.T) {
	w := mainWorld()
	if d := tkBuild(t, w, tkOf(t, w, "MOB-1"), TicketFieldPrefs{}, TicketOptions{}); d.Dev != nil {
		t.Errorf("Dev = %+v for an issue without dev events", d.Dev)
	} else if _, ok := tkGroup(d, RailDevelopment); ok {
		t.Errorf("development group for an issue without dev events")
	}
	w.snap.DevActivity = "unavailable"
	d := tkBuild(t, w, tkOf(t, w, "MOB-2"), TicketFieldPrefs{}, TicketOptions{})
	if d.Dev != nil {
		t.Errorf("Dev = %+v although the dev API is unavailable", d.Dev)
	}
	if _, ok := tkGroup(d, RailDevelopment); ok {
		t.Errorf("development group although the dev API is unavailable")
	}
}

func TestRail_DatesGroup(t *testing.T) {
	w := mainWorld()
	tk := railTicket(t, w)
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, _ := tkGroup(d, RailDates)
	if got := strings.Join(rowIDs(g), ","); got != "created,updated,watches" {
		t.Fatalf("dates rows = %s", got)
	}
	if r := g.Rows[0]; r.Label != "Created" || r.Kind != model.FieldDateTime || r.Text != "2026-11-01" || !r.Mono {
		t.Errorf("created = %+v", r)
	}
	if r := g.Rows[1]; r.Label != "Updated" || r.Text != "2026-11-18" {
		t.Errorf("updated = %+v", r)
	}
	if r := g.Rows[2]; r.Label != "Watchers" || r.Kind != RailKindWatchers || r.Number == nil || *r.Number != 4 || !r.Mono {
		t.Errorf("watchers = %+v", r)
	}
	if got := strings.Join(g.NotSet, "|"); got != "Resolved" {
		t.Errorf("dates NotSet = %q", got)
	}

	// Resolved set, watchers unknown (-1): a Resolved row and no Watchers row, not listed as not set either.
	tk.Issue.ResolvedAt = nv(24, 15, 0)
	tk.Watchers = -1
	d = tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	g, _ = tkGroup(d, RailDates)
	if got := strings.Join(rowIDs(g), ","); got != "created,updated,resolved" || len(g.NotSet) != 0 {
		t.Errorf("dates rows = %s notSet %v", got, g.NotSet)
	}
	if r := g.Rows[2]; r.Label != "Resolved" || r.Text != "2026-11-24" {
		t.Errorf("resolved = %+v", r)
	}
}

func TestReadableRaw_UnknownValuesReadAsTheirNameWhenTheyHaveOne(t *testing.T) {
	for _, c := range []struct{ raw, want string }{
		{`{"id":7,"name":"ACME-OPS"}`, "ACME-OPS"},
		{`{"id":7,"value":"Gold","self":"x"}`, "Gold"},
		{`{"title":"Escalation"}`, "Escalation"},
		{`{"displayName":"Iris Calloway","accountId":"x"}`, "Iris Calloway"},
		{`{"key":"OPS-1"}`, "OPS-1"},
		{`{"name":"","value":"v"}`, "v"},
		{`[{"name":"A"},{"value":"B"}]`, "A, B"},
		{`["x","y"]`, "x, y"},
		{`"plain"`, "plain"},
		{`42`, "42"},
		{`true`, "true"},
		{`{"lexo":"0|i0001r:"}`, ""},
		{`[{"name":"A"},{"lexo":"z"}]`, ""}, // one unreadable member: show the raw value instead of a partial reading
		{`{"id":7}`, ""},
		{`not json`, ""},
	} {
		if got := readableRaw(json.RawMessage(c.raw)); got != c.want {
			t.Errorf("readableRaw(%s) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestRail_UnknownFieldWithAReadableValueIsTextNotRaw(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	tk.Registry = append(tk.Registry, tkDef("c_ops", "Ops team", model.FieldUnknown, true))
	tk.Fields = append(tk.Fields, tkField("c_ops", model.FieldUnknown, func(f *model.FieldValue) { f.Raw = json.RawMessage(`{"id":7,"name":"ACME-OPS"}`) }))
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	r := tkRow(t, d, RailCustom, "c_ops")
	if r.Kind != model.FieldUnknown || r.Text != "ACME-OPS" || r.Raw != "" || r.Mono {
		t.Errorf("ops row = %+v", r)
	}
}

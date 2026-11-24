package view

// Tests for view.BuildTicket / BuildTicketWith (WP-T6; architecture.md §3.2 "Rules for view.BuildTicket", ux-spec §14).
// Everything is hand-built on the shared fixtures of fixtures_test.go (main world: Wednesday 2026-11-25 12:00 Chicago).
//
// SPEC-NOTE: the frozen BuildTicket signature cannot carry the connected account, the source's capabilities, the
// connection state or "the refresh failed". BuildTicketWith takes them (TicketOptions); BuildTicket is BuildTicketWith with
// the zero options, so nobody is Self and Can is {Reason:"read_only"}. The caller adds nothing else: refresh_failed comes
// from TicketOptions.RefreshFailed.
// SPEC-NOTE: Row.Lane is the standup's lane of the issue's status when that is todo|prog|review|qa|done; for an issue
// whose status maps to the backlog or hidden lane, or to no lane, it is taken from the status category (new → todo,
// indeterminate → prog, done → done; unknown → todo) so the modal always has a lane swatch.
// SPEC-NOTE: Row is computed over TicketPulseDays (21), not the standup's PulseDays; with a 21-day standup it equals the
// board row (TestBuildTicket_RowEqualsTheBoardRow...).
// SPEC-NOTE: Not set. A Details field is listed in NotSet when the connection's registry has it (reporter, priority,
// duedate, labels, components, fixVersions, timetracking), when it is a rail-only row (Parent unless the issue is an epic,
// Sprint on a scrum board, Environment), or when it is the story-points field. The registry drives the Custom group too
// (cache.Ticket.Registry, data-model Q9, testing.md "Empty-field folding"): its Not set lists the empty pinned fields first
// (pinned order), then every other custom field of the registry without a value that some OTHER issue of the space has
// filled in (TicketOptions.FieldsInUse, cache.FieldsInUseTx), A-Z, except hidden ones, the story points
// and the fields the sync models elsewhere (sprint, rank, flagged, development summary). Resolved is listed when empty;
// Watchers only when known.
// SPEC-NOTE: Sprint resolves through snap.Sprints (the active sprints); an issue whose only sprints are not active gets
// neither a Sprint row nor a Not-set entry (the name is unknown, so claiming "not set" would be false).
// SPEC-NOTE: the Development group has no branch row and a PR's Title is its repository: the dev model stores neither a
// branch nor a PR title. Commits exclude merge commits; PRs show their latest status.
// SPEC-NOTE: "last 2h"-style ages: under a minute "just now", under an hour minutes, under a day hours, else days.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
)

// ---- helpers --------------------------------------------------------------------------------------------------

func tkMember(t *testing.T, w *world, key string) model.Member {
	t.Helper()
	for _, m := range w.snap.Members {
		if m.Issue.Key == key {
			return m
		}
	}
	t.Fatalf("no member %s", key)
	return model.Member{}
}

// tkOf builds the cache.Ticket of a member of the world: its issue and its history, nothing else.
func tkOf(t *testing.T, w *world, key string) cache.Ticket {
	t.Helper()
	m := tkMember(t, w, key)
	people := map[string]model.Person{}
	for k, p := range w.snap.People {
		people[k] = p
	}
	return cache.Ticket{
		LocalID: m.LocalID, ConnectionID: 1, Issue: m.Issue, TypeKind: m.TypeKind, HydratedAt: nv(25, 11, 58), Watchers: -1,
		Fields: []cache.TicketField{}, Registry: []cache.TicketFieldDef{}, Comments: []model.Comment{},
		Attachments: []cache.TicketAttachment{}, Links: []cache.TicketLink{},
		History: w.snap.History[m.Issue.SourceID], People: people, Issues: map[string]cache.TicketRef{},
		Draft: model.NewComment{Mentions: []model.Mention{}},
	}
}

func tkBuild(t *testing.T, w *world, tk cache.Ticket, prefs TicketFieldPrefs, opts TicketOptions) TicketDetail {
	t.Helper()
	return BuildTicketWith(w.snap, tk, prefs, w.snap.AsOf, opts)
}

func tkGroup(d TicketDetail, id string) (RailGroup, bool) {
	for _, g := range d.Rail {
		if g.ID == id {
			return g, true
		}
	}
	return RailGroup{}, false
}

func tkRow(t *testing.T, d TicketDetail, group, field string) RailRow {
	t.Helper()
	g, ok := tkGroup(d, group)
	if !ok {
		t.Fatalf("no %s group in %v", group, groupIDs(d))
	}
	for _, r := range g.Rows {
		if r.FieldID == field {
			return r
		}
	}
	t.Fatalf("group %s has no row %s (rows: %v)", group, field, rowIDs(g))
	return RailRow{}
}

func groupIDs(d TicketDetail) []string {
	var out []string
	for _, g := range d.Rail {
		out = append(out, g.ID)
	}
	return out
}

func rowIDs(g RailGroup) []string {
	var out []string
	for _, r := range g.Rows {
		out = append(out, r.FieldID)
	}
	return out
}

func tkField(id, kind string, mut func(*model.FieldValue)) cache.TicketField {
	fv := model.FieldValue{FieldID: id, Kind: kind}
	if mut != nil {
		mut(&fv)
	}
	return cache.TicketField{Value: fv}
}

func tkDef(id, name, kind string, custom bool) cache.TicketFieldDef {
	return cache.TicketFieldDef{Def: model.FieldDef{ID: id, Name: name, Kind: kind, Custom: custom}, Pinned: -1}
}

func rich(s string) *model.RichText {
	b, _ := json.Marshal(map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": s}}}}})
	return &model.RichText{Format: model.RichTextADF, Raw: b}
}

func walk(nodes []adf.Node, fn func(adf.Node)) {
	for _, n := range nodes {
		fn(n)
		walk(n.C, fn)
	}
}

func findNodes(d adf.Doc, typ string) []adf.Node {
	var out []adf.Node
	walk(d.Nodes, func(n adf.Node) {
		if n.T == typ {
			out = append(out, n)
		}
	})
	return out
}

func f64(v float64) *float64 { return &v }

// ---- header ---------------------------------------------------------------------------------------------------

func TestBuildTicket_HeaderIdentityTimesAndLinks(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	if d.IssueID != 1 || d.StandupID != w.snap.StandupID || d.AsOf != w.snap.AsOf.UnixMilli() {
		t.Errorf("ids/asOf = %d/%d/%d", d.IssueID, d.StandupID, d.AsOf)
	}
	if d.SyncedAt != nv(25, 11, 58).UnixMilli() || d.UpdatedAt != tk.Issue.UpdatedAt.UnixMilli() {
		t.Errorf("syncedAt/updatedAt = %d/%d", d.SyncedAt, d.UpdatedAt)
	}
	if want := urlPrefix + "MOB-1"; d.URL != want || d.Row.URL != want {
		t.Errorf("url = %q / row %q, want %q", d.URL, d.Row.URL, want)
	}
	if d.PulseDays != TicketPulseDays || len(d.Axis) != TicketPulseDays || d.Axis[len(d.Axis)-1].Date != "2026-11-25" || d.Axis[0].Date != "2026-11-05" {
		t.Errorf("pulseDays %d, axis %d [%v..%v]", d.PulseDays, len(d.Axis), d.Axis[0], d.Axis[len(d.Axis)-1])
	}
	if d.Row.ID != 1 || d.Row.Key != "MOB-1" || d.Row.Lane != model.LaneProg || d.Row.DaysInLane != 7 || !d.Row.OverLimit || d.Row.Blocked {
		t.Errorf("row = %+v", d.Row)
	}
	if d.LimitDays != 5 || d.BlockedDays != 0 {
		t.Errorf("limit/blocked days = %d/%d, want 5/0", d.LimitDays, d.BlockedDays)
	}
	if d.Parent != nil || d.Description != nil || d.NewSince == 0 {
		t.Errorf("parent %v description %v newSince %d", d.Parent, d.Description, d.NewSince)
	}
}

func TestBuildTicket_RowEqualsTheBoardRowAtTwentyOneDays(t *testing.T) {
	w := mainWorld()
	w.snap.Config.PulseDays = TicketPulseDays
	board := Build(w.snap, w.snap.AsOf)
	if len(board.Tickets) == 0 {
		t.Fatalf("no board rows")
	}
	for _, row := range board.Tickets {
		tk := tkOf(t, w, row.Key)
		d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
		got, want := d.Row, row
		if !reflect.DeepEqual(got, want) {
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(want)
			t.Errorf("%s: the modal row differs from the board row\n got %s\nwant %s", row.Key, gj, wj)
		}
	}
}

func TestBuildTicket_BlockedDaysAndFlagRow(t *testing.T) {
	w := mainWorld()
	d := tkBuild(t, w, tkOf(t, w, "MOB-3"), TicketFieldPrefs{}, TicketOptions{})
	if !d.Row.Blocked || d.BlockedDays != 2 || d.LimitDays != 3 || d.Row.Lane != model.LaneQA {
		t.Errorf("blocked %v days %d limit %d lane %s", d.Row.Blocked, d.BlockedDays, d.LimitDays, d.Row.Lane)
	}
	flag := tkRow(t, d, RailState, "flagged")
	if flag.Kind != RailKindFlag || flag.Edit != "flag" || flag.Text != "Flagged" || flag.Sub != "2d" || flag.Label != "Flag" {
		t.Errorf("flag row = %+v", flag)
	}
	d = tkBuild(t, w, tkOf(t, w, "MOB-1"), TicketFieldPrefs{}, TicketOptions{})
	if f := tkRow(t, d, RailState, "flagged"); f.Text != "Not flagged" || f.Sub != "" {
		t.Errorf("unflagged row = %+v", f)
	}
}

func TestBuildTicket_AnIssueThatIsNotAMemberUsesTheStandupsLanesAndItsOwnHistory(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	// Not a member of the snapshot at all: a different id and key, a status the standup maps, no history.
	tk.LocalID = 777
	tk.Issue.SourceID, tk.Issue.Key = "10777", "OTH-5"
	tk.Issue.StatusID = sReview
	tk.Issue.CreatedAt = nv(20, 9, 0)
	tk.Issue.UpdatedAt = nv(20, 9, 0)
	tk.History = model.IssueHistory{}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	if d.Row.Lane != model.LaneReview || d.LimitDays != 2 || d.Row.StatusName != "In Review" {
		t.Errorf("lane %q limit %d status %q, want review/2/In Review", d.Row.Lane, d.LimitDays, d.Row.StatusName)
	}
	if len(d.Row.Pulse.Marks) != 0 || len(d.Row.Pulse.Blocked) != 0 {
		t.Errorf("pulse marks/blocked = %v/%v, want none for an issue without history", d.Row.Pulse.Marks, d.Row.Pulse.Blocked)
	}
	// The built row comes from the ticket's own history even though the snapshot has another.
	tk2 := tkOf(t, w, "MOB-2")
	tk2.History.Comments = nil
	tk2.History.Dev = nil
	d2 := tkBuild(t, w, tk2, TicketFieldPrefs{}, TicketOptions{})
	if len(d2.Row.Pulse.Marks) != 0 {
		t.Errorf("marks = %v: BuildTicket must use the ticket's history, not the snapshot's", d2.Row.Pulse.Marks)
	}
}

func TestBuildTicket_LaneFallbackForBacklogHiddenAndUnmappedStatuses(t *testing.T) {
	w := mainWorld()
	for _, c := range []struct {
		key, want string
	}{{"MOB-7", model.LaneTodo}, {"MOB-6", model.LaneTodo}, {"MOB-8", model.LaneTodo}} {
		d := tkBuild(t, w, tkOf(t, w, c.key), TicketFieldPrefs{}, TicketOptions{})
		if d.Row.Lane != c.want {
			t.Errorf("%s: lane = %q, want %q (status category fallback)", c.key, d.Row.Lane, c.want)
		}
		if d.Row.Pulse.Segs == nil || d.Row.Pulse.Marks == nil || d.Row.Pulse.Blocked == nil {
			t.Errorf("%s: pulse lists must not be nil", c.key)
		}
	}
}

func TestBuildTicket_EveryListIsNonNilForAMinimalTicket(t *testing.T) {
	w := mainWorld()
	tk := cache.Ticket{LocalID: 1, Issue: tkMember(t, w, "MOB-1").Issue, TypeKind: "story"} // every list and map nil
	d := BuildTicket(w.snap, tk, TicketFieldPrefs{}, w.snap.AsOf)
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, bad := range []string{`"attachments":null`, `"subtasks":null`, `"linkGroups":null`, `"comments":null`, `"warnings":null`,
		`"rail":null`, `"axis":null`, `"mentions":null`, `"segs":null`, `"marks":null`, `"blocked":null`, `"rows":null`, `"notSet":null`,
		`"items":null`, `"users":null`} {
		if strings.Contains(s, bad) {
			t.Errorf("JSON contains %s", bad)
		}
	}
	for _, g := range d.Rail {
		if g.Rows == nil || g.NotSet == nil {
			t.Errorf("group %s has a nil list", g.ID)
		}
		for _, r := range g.Rows {
			if r.Items == nil || r.Users == nil {
				t.Errorf("row %s has a nil list", r.FieldID)
			}
		}
	}
}

func TestBuildTicket_IsDeterministic(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-2")
	tk.Fields = []cache.TicketField{tkField("labels", model.FieldLabels, func(f *model.FieldValue) { f.Items = []string{"b", "a"} })}
	a, _ := json.Marshal(tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{}))
	b, _ := json.Marshal(tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{}))
	if string(a) != string(b) {
		t.Errorf("two builds differ")
	}
}

// ---- newSince -------------------------------------------------------------------------------------------------

func TestBuildTicket_NewSinceIsTheStartOfThePreviousWorkingDay(t *testing.T) {
	cases := []struct {
		name  string
		asOf  time.Time
		start time.Time
	}{
		{"wednesday", ct(time.November, 25, 12, 0), ct(time.November, 24, 0, 0)},
		{"tuesday", ct(time.November, 24, 8, 30), ct(time.November, 23, 0, 0)},
		{"monday", ct(time.November, 23, 9, 0), ct(time.November, 20, 0, 0)},
		{"sunday", ct(time.November, 22, 23, 59), ct(time.November, 20, 0, 0)},
		{"saturday", ct(time.November, 21, 0, 1), ct(time.November, 20, 0, 0)},
		{"monday after the DST change", time.Date(2026, 3, 9, 10, 0, 0, 0, chi), time.Date(2026, 3, 6, 0, 0, 0, 0, chi)},
	}
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	for _, c := range cases {
		d := BuildTicketWith(w.snap, tk, TicketFieldPrefs{}, c.asOf, TicketOptions{})
		if d.NewSince != c.start.UnixMilli() {
			t.Errorf("%s: newSince = %v, want %v", c.name, time.UnixMilli(d.NewSince).In(chi), c.start)
		}
	}
}

// ---- description, comments, attachments, links ---------------------------------------------------------------

func tkDescription() *model.RichText {
	doc := map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "paragraph", "content": []any{
			map[string]any{"type": "inlineCard", "attrs": map[string]any{"url": "https://acme.atlassian.net/browse/MOB-2"}},
			map[string]any{"type": "inlineCard", "attrs": map[string]any{"url": "https://other.example.com/x/y"}},
			map[string]any{"type": "mention", "attrs": map[string]any{"id": "a1", "text": "@Iris Calloway"}},
			map[string]any{"type": "mention", "attrs": map[string]any{"id": "a2", "text": "@Silas Webb"}},
			map[string]any{"type": "date", "attrs": map[string]any{"timestamp": "1793000000000"}}, // 2026-10-26: past
			map[string]any{"type": "date", "attrs": map[string]any{"timestamp": "1796000000000"}}, // 2026-11-30: future
		}},
		map[string]any{"type": "mediaSingle", "attrs": map[string]any{"layout": "center"}, "content": []any{
			map[string]any{"type": "media", "attrs": map[string]any{"id": "uuid-1", "type": "file", "alt": "whatever.png"}}}},
		map[string]any{"type": "mediaSingle", "attrs": map[string]any{"layout": "center"}, "content": []any{
			map[string]any{"type": "media", "attrs": map[string]any{"id": "uuid-gone", "type": "file", "alt": "named.png"}}}},
	}}
	b, _ := json.Marshal(doc)
	return &model.RichText{Format: model.RichTextADF, Raw: b}
}

func tkAttachments() []cache.TicketAttachment {
	mk := func(local int64, id, media, name, mime string, image, thumb, file bool, author string, created time.Time) cache.TicketAttachment {
		return cache.TicketAttachment{
			Attachment: model.Attachment{LocalID: local, ID: id, MediaFileID: media, Name: name, Mime: mime, Bytes: 2048,
				AuthorID: author, CreatedAt: created, Width: 640, Height: 480},
			IsImage: image, HasThumb: thumb, HasFile: file,
		}
	}
	return []cache.TicketAttachment{
		mk(201, "a1", "uuid-1", "shot.png", "image/png", true, true, true, "a1", nv(20, 9, 0)),
		mk(202, "a2", "", "named.png", "image/png", true, true, false, "a2", nv(19, 9, 0)),
		mk(203, "a3", "", "spec.pdf", "application/pdf", false, false, false, "a2", nv(18, 9, 0)),
	}
}

func TestBuildTicket_DescriptionResolvesCardsMentionsDatesAndMedia(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	tk.Description = tkDescription()
	tk.Attachments = tkAttachments()
	tk.Issues = map[string]cache.TicketRef{"MOB-2": {LocalID: 2, Key: "MOB-2", Summary: "Summary of MOB-2", StatusID: sReview, TypeKind: "bug"}}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{SelfAccountID: "a1"})
	if d.Description == nil {
		t.Fatalf("Description is nil")
	}

	cards := findNodes(*d.Description, adf.NodeCard)
	if len(cards) != 2 {
		t.Fatalf("cards = %d, want 2", len(cards))
	}
	if a := cards[0].A; a == nil || a.IssueID != 2 || a.Key != "MOB-2" || a.Summary != "Summary of MOB-2" || a.TypeKind != "bug" ||
		a.StatusName != "In Review" || a.StatusLozenge != model.LaneReview {
		t.Errorf("synced card = %+v", cards[0].A)
	}
	if a := cards[1].A; a == nil || a.IssueID != 0 || a.Host != "other.example.com" {
		t.Errorf("external card = %+v", cards[1].A)
	}

	ments := findNodes(*d.Description, adf.NodeMention)
	if len(ments) != 2 || !ments[0].A.Self || ments[1].A.Self {
		t.Errorf("mentions = %+v, want the first (a1) to be Self and the second not", ments)
	}
	dates := findNodes(*d.Description, adf.NodeDate)
	if len(dates) != 2 || !dates[0].A.Past || dates[1].A.Past {
		t.Errorf("dates = %+v, want the first past and the second not", dates)
	}

	media := findNodes(*d.Description, adf.NodeMedia)
	if len(media) != 2 {
		t.Fatalf("media nodes = %d, want 2", len(media))
	}
	if a := media[0].A; a.Missing || a.AttachmentID != "201" || a.Cached != adf.CachedFull {
		t.Errorf("media by id = %+v, want attachment 201 cached full", a)
	}
	if a := media[1].A; a.Missing || a.AttachmentID != "202" || a.Cached != adf.CachedThumb {
		t.Errorf("media by alt/name = %+v, want attachment 202 (named.png) cached thumb", a)
	}
	if contains(d.Warnings, WarnMediaUnresolved) {
		t.Errorf("warnings = %v, want no media_unresolved when every media node resolved", d.Warnings)
	}

	// Nothing matches the second node any more: unresolved, with the warning.
	tk.Attachments = tk.Attachments[:1]
	d = tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	media = findNodes(*d.Description, adf.NodeMedia)
	if !media[1].A.Missing || !contains(d.Warnings, WarnMediaUnresolved) {
		t.Errorf("unresolved media: %+v, warnings %v", media[1].A, d.Warnings)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestBuildTicket_AttachmentViews(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	tk.Attachments = tkAttachments()
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	if len(d.Attachments) != 3 {
		t.Fatalf("attachments = %d", len(d.Attachments))
	}
	a := d.Attachments[0]
	if a.ID != 201 || a.Name != "shot.png" || a.Ext != "PNG" || a.Mime != "image/png" || a.Bytes != 2048 || !a.Image ||
		a.Width != 640 || a.Height != 480 || a.Cached != "full" || a.URL != "/media/201" || a.ThumbURL != "/media/201/thumb" ||
		a.AuthorInitials != "IC" || a.AuthorName != "Iris Calloway" || a.CreatedAt != nv(20, 9, 0).UnixMilli() {
		t.Errorf("attachment 0 = %+v", a)
	}
	if d.Attachments[1].Cached != "thumb" || d.Attachments[2].Cached != "none" || d.Attachments[2].Image || d.Attachments[2].Ext != "PDF" {
		t.Errorf("attachments 1/2 = %+v / %+v", d.Attachments[1], d.Attachments[2])
	}
}

func TestBuildTicket_SubtasksLinksAndParent(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	tk.Links = []cache.TicketLink{
		{Relation: "link", Label: "blocks", OtherSourceID: "10002", OtherKey: "MOB-2", OtherSummary: "Stale", OtherStatusID: sTodo, OtherTypeID: "t-bug",
			OtherTypeKind: "bug", OtherIssueID: 2, LiveSummary: "Summary of MOB-2", LiveStatusID: sReview, LiveAssigneeID: "a2"},
		{Relation: "link", Label: "blocks", Position: 1, OtherSourceID: "9", OtherKey: "OTH-9", OtherSummary: "Elsewhere", OtherStatusID: sDone, OtherTypeKind: "task"},
		{Relation: "link", Label: "blocks", Position: 2, OtherSourceID: "8", OtherKey: "OTH-8", OtherSummary: "Snapshot", OtherStatusID: sDone, OtherTypeKind: "task", OtherAssigneeID: "a1"},
		{Relation: "link", Label: "relates to", OtherSourceID: "10003", OtherKey: "MOB-3", OtherSummary: "Summary of MOB-3", OtherStatusID: sQA, OtherTypeKind: "task", OtherIssueID: 3, LiveSummary: "Summary of MOB-3", LiveStatusID: sQA},
		{Relation: "subtask", OtherSourceID: "10010", OtherKey: "MOB-10", OtherSummary: "Sub A", OtherStatusID: sDone, OtherTypeKind: "subtask", OtherIssueID: 10, LiveSummary: "Sub A", LiveStatusID: sDone, LiveAssigneeID: "a1", OtherAssigneeID: "a2"}, // the live assignee wins over a stale snapshot
		{Relation: "subtask", Position: 1, OtherSourceID: "10011", OtherKey: "MOB-11", OtherSummary: "Sub B", OtherStatusID: sTodo, OtherTypeKind: "subtask"},
	}
	tk.Parent = &cache.TicketLink{Relation: "parent", OtherKey: "MOB-100", OtherSummary: "Epic", OtherTypeKind: "epic", OtherIssueID: 0}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})

	if len(d.Subtasks) != 2 || d.SubtasksDone != 1 {
		t.Fatalf("subtasks = %d done %d, want 2 and 1", len(d.Subtasks), d.SubtasksDone)
	}
	s := d.Subtasks[0]
	if s.IssueID != 10 || s.Key != "MOB-10" || s.Summary != "Sub A" || s.TypeKind != "subtask" || s.StatusName != "Done" || s.StatusLozenge != model.LaneDone ||
		s.AssigneeInitials != "IC" || s.URL != urlPrefix+"MOB-10" || !s.Done {
		t.Errorf("subtask 0 = %+v", s)
	}
	if s := d.Subtasks[1]; s.IssueID != 0 || s.StatusLozenge != model.LaneTodo || s.Done || s.AssigneeInitials != "" {
		t.Errorf("subtask 1 = %+v", s)
	}

	if len(d.LinkGroups) != 2 || d.LinkGroups[0].Label != "BLOCKS" || d.LinkGroups[1].Label != "RELATES TO" || len(d.LinkGroups[0].Rows) != 3 {
		t.Fatalf("link groups = %+v", d.LinkGroups)
	}
	r := d.LinkGroups[0].Rows[0]
	if r.IssueID != 2 || r.Summary != "Summary of MOB-2" || r.StatusName != "In Review" || r.StatusLozenge != model.LaneReview || r.TypeKind != "bug" || r.AssigneeInitials != "SW" {
		t.Errorf("synced link row = %+v (live summary and status win over the stored snapshot)", r)
	}
	if r := d.LinkGroups[0].Rows[1]; r.IssueID != 0 || r.Summary != "Elsewhere" || r.StatusLozenge != model.LaneDone || !r.Done || r.AssigneeInitials != "" {
		t.Errorf("unsynced link row = %+v", r)
	}
	if r := d.LinkGroups[0].Rows[2]; r.IssueID != 0 || r.AssigneeInitials != "IC" {
		t.Errorf("unsynced link row with a stored assignee = %+v, want the snapshot's initials", r)
	}

	if r := d.LinkGroups[1].Rows[0]; r.IssueID != 3 || r.AssigneeInitials != "" {
		t.Errorf("synced but unassigned link row = %+v, want no initials", r)
	}

	if d.Parent == nil || d.Parent.Key != "MOB-100" || d.Parent.Summary != "Epic" || d.Parent.TypeKind != "epic" || d.Parent.IssueID != 0 {
		t.Errorf("parent = %+v", d.Parent)
	}
	tk.Parent.OtherIssueID, tk.Parent.LiveSummary = 55, "Live epic"
	if d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{}); d.Parent.IssueID != 55 || d.Parent.Summary != "Live epic" {
		t.Errorf("synced parent = %+v", d.Parent)
	}
}

func TestBuildTicket_CommentsViews(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	tk.People["a9"] = model.Person{AccountID: "a9", DisplayName: "Zed Quinn", Active: true, Kind: "user"}
	tk.Comments = []model.Comment{
		{ID: "k1", At: nv(20, 9, 0), UpdatedAt: nv(20, 9, 0), AuthorID: "a1", Body: *rich("first")},
		{ID: "k2", At: nv(22, 9, 0), UpdatedAt: nv(23, 10, 0), AuthorID: "a2", Body: *rich("second (edited)"),
			Visibility: &model.Visibility{Type: "role", Value: "Developers"}},
		{ID: "k3", At: nv(24, 9, 0), UpdatedAt: nv(24, 9, 0), AuthorID: "", Body: model.RichText{}},
		{ID: "k4", At: nv(25, 9, 0), UpdatedAt: nv(25, 9, 0), AuthorID: "ghost", Body: *rich("who")},
	}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{SelfAccountID: "a1"})
	if len(d.Comments) != 4 || d.Comments[0].ID != "k1" || d.Comments[3].ID != "k4" {
		t.Fatalf("comments = %+v (oldest first)", d.Comments)
	}
	c := d.Comments[0]
	if c.Author.AccountID != "a1" || c.Author.Name != "Iris Calloway" || c.Author.Initials != "IC" || !c.Author.Self || c.Edited || c.Visibility != "" ||
		c.CreatedAt != nv(20, 9, 0).UnixMilli() || c.UpdatedAt != nv(20, 9, 0).UnixMilli() {
		t.Errorf("comment 0 = %+v", c)
	}
	if len(c.Body.Nodes) == 0 {
		t.Errorf("comment 0 body is empty: %+v", c.Body)
	}
	c = d.Comments[1]
	if !c.Edited || c.Visibility != "Developers only" || c.Author.Self || c.Author.Initials != "SW" || c.UpdatedAt != nv(23, 10, 0).UnixMilli() {
		t.Errorf("comment 1 = %+v", c)
	}
	if c := d.Comments[2]; c.Author.AccountID != "" || c.Author.Name == "" || c.Body.Nodes == nil {
		t.Errorf("comment 2 (unknown author, empty body) = %+v", c)
	}
	if c := d.Comments[3]; c.Author.AccountID != "ghost" || c.Author.Name == "" || c.Author.Self {
		t.Errorf("comment 3 (account not in People) = %+v", c)
	}
}

func TestBuildTicket_DraftCarriesTextAndMentions(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	tk.Draft = model.NewComment{Text: "hi @Iris", Mentions: []model.Mention{{Start: 3, End: 8, AccountID: "a1", Name: "Iris Calloway"}}}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	if d.Draft.Text != "hi @Iris" || len(d.Draft.Mentions) != 1 || d.Draft.Mentions[0] != (Mention{Start: 3, End: 8, AccountID: "a1", Name: "Iris Calloway"}) {
		t.Errorf("draft = %+v", d.Draft)
	}
}

// ---- Can and warnings -----------------------------------------------------------------------------------------

func TestCanFor_CapabilityAndConnectionState(t *testing.T) {
	all := TicketWrites{Transition: true, Assign: true, Flag: true, Comment: true}
	for _, c := range []struct {
		name  string
		w     TicketWrites
		state string
		want  TicketCaps
	}{
		{"everything", all, "ok", TicketCaps{Transition: true, Assign: true, Flag: true, Comment: true}},
		{"state unknown to the caller counts as ok", all, "", TicketCaps{Transition: true, Assign: true, Flag: true, Comment: true}},
		{"no flag field discovered", TicketWrites{Transition: true, Assign: true, Comment: true}, "ok", TicketCaps{Transition: true, Assign: true, Comment: true}},
		{"token rejected", all, "invalid", TicketCaps{Reason: "auth"}},
		{"token missing", all, "missing", TicketCaps{Reason: "auth"}},
		{"read-only source", TicketWrites{}, "ok", TicketCaps{Reason: "read_only"}},
	} {
		if got := CanFor(c.w, c.state); got != c.want {
			t.Errorf("%s: CanFor = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestBuildTicket_CanComesFromTheOptions(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	if d := BuildTicket(w.snap, tk, TicketFieldPrefs{}, w.snap.AsOf); d.Can != (TicketCaps{Reason: "read_only"}) {
		t.Errorf("BuildTicket Can = %+v, want read_only (no capabilities known)", d.Can)
	}
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{Writes: TicketWrites{Transition: true, Assign: true, Flag: true, Comment: true}, AuthState: "ok"})
	if d.Can != (TicketCaps{Transition: true, Assign: true, Flag: true, Comment: true}) {
		t.Errorf("Can = %+v", d.Can)
	}
	d = tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{Writes: TicketWrites{Transition: true}, AuthState: "invalid"})
	if d.Can != (TicketCaps{Reason: "auth"}) {
		t.Errorf("Can with a rejected token = %+v", d.Can)
	}
}

func TestBuildTicket_Warnings(t *testing.T) {
	w := mainWorld()
	tk := tkOf(t, w, "MOB-1")
	if d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{}); d.Warnings == nil || len(d.Warnings) != 0 {
		t.Errorf("warnings = %#v, want an empty non-nil list", d.Warnings)
	}
	tk.RemovedAt = nv(24, 9, 0)
	d := tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{RefreshFailed: true})
	if !contains(d.Warnings, WarnNotOnBoard) || !contains(d.Warnings, WarnRefreshFailed) || len(d.Warnings) != 2 {
		t.Errorf("warnings = %v, want not_on_board and refresh_failed", d.Warnings)
	}
	// A comment body with an unresolvable media node also warns, once.
	tk.RemovedAt = time.Time{}
	tk.Comments = []model.Comment{{ID: "k", At: nv(24, 9, 0), AuthorID: "a1", Body: *tkDescription()}, {ID: "k2", At: nv(24, 10, 0), AuthorID: "a1", Body: *tkDescription()}}
	d = tkBuild(t, w, tk, TicketFieldPrefs{}, TicketOptions{})
	n := 0
	for _, x := range d.Warnings {
		if x == WarnMediaUnresolved {
			n++
		}
	}
	if n != 1 {
		t.Errorf("warnings = %v, want media_unresolved exactly once", d.Warnings)
	}
}

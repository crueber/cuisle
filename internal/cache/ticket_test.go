package cache_test

// Tests for cache.LoadTicket (WP-T6, data-model.md §4.1 Q9, architecture.md §3.2 "Rules for view.BuildTicket"):
// everything the ticket modal needs for one issue, in one reader transaction, without network.
//
// SPEC-NOTE: Comments come back oldest first (created_at, comment_id) and without tombstoned rows; History.Comments
// keeps the tombstoned ones (their pulse mark stays, testing.md "Comments").
// SPEC-NOTE: a comment row whose updated_at is 0 (a row written before 0003 or by a metadata-only source) reads back
// with UpdatedAt = At.
// SPEC-NOTE: Ticket.People holds only the accounts the ticket references: assignee, assignee-change accounts, comment
// authors (tombstoned ones included, for the pulse's app filter), attachment authors, user/users field values,
// subtask/link assignees and accounts mentioned in the description, environment, comments or rich fields.
// SPEC-NOTE: Ticket.Issues holds the synced issues named by an issue key inside a description, environment, comment or
// rich field (the inline-card resolver) plus the targets of links, subtasks and the parent.
// SPEC-NOTE: the synthetic "parent" field row never appears in Ticket.Fields.

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// ctSeedTicket seeds the standard records (issue 1 = MOB-1) and the ticket content of issue 1 by raw SQL.
func ctSeedTicket(t *testing.T) *store.DB {
	t.Helper()
	db := rdOpen(t)
	rdSeedAll(t, db)

	desc := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"see "},{"type":"inlineCard","attrs":{"url":"https://acme.atlassian.net/browse/MOB-2"}},{"type":"mention","attrs":{"id":"acc-zed","text":"@Zed"}}]}]}`
	env := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"iOS"}]}]}`
	rdInsert(t, db, "issue_content", "issue_id,description_adf,environment_adf,watchers,content_hash,updated_at",
		1, desc, env, 4, "h", rdMs("2026-03-18T18:30:00Z"))

	fd := "connection_id,field_id,name,kind,custom,fetched_at"
	f := rdMs("2026-03-18T18:00:00Z")
	rdInsert(t, db, "field_def", fd, 1, "priority", "Priority", "option", 0, f)
	rdInsert(t, db, "field_def", fd, 1, "reporter", "Reporter", "user", 0, f)
	rdInsert(t, db, "field_def", fd, 1, "customfield_10016", "Story point estimate", "number", 1, f)
	rdInsert(t, db, "field_def", fd, 1, "customfield_10200", "Reviewers", "users", 1, f)
	rdInsert(t, db, "field_def", fd, 1, "customfield_10300", "Workaround", "paragraph", 1, f)
	rdInsert(t, db, "ticket_field_pref", "connection_id,field_id,position,hidden", 1, "customfield_10200", 0, 0)
	rdInsert(t, db, "ticket_field_pref", "connection_id,field_id,position,hidden", 1, "customfield_10300", nil, 1)

	fv := "issue_id,field_id,kind,value_json"
	rdInsert(t, db, "issue_field_value", fv, 1, "priority", "option", `{"fieldId":"priority","kind":"option","text":"High"}`)
	rdInsert(t, db, "issue_field_value", fv, 1, "reporter", "user", `{"fieldId":"reporter","kind":"user","users":["acc-bob"]}`)
	rdInsert(t, db, "issue_field_value", fv, 1, "customfield_10016", "number", `{"fieldId":"customfield_10016","kind":"number","number":5}`)
	rdInsert(t, db, "issue_field_value", fv, 1, "customfield_10200", "users", `{"fieldId":"customfield_10200","kind":"users","users":["acc-bob","acc-bot"]}`)
	rdInsert(t, db, "issue_field_value", fv, 1, "parent", "unknown", `{"fieldId":"parent","kind":"unknown","raw":{"SourceID":"9100","Key":"MOB-100","Summary":"Epic snapshot","StatusID":"12","TypeID":"T1"}}`)

	lk := "issue_id,link_id,relation,label,position,other_source_id,other_key,other_summary,other_status_id,other_type_id,other_assignee_account_id"
	rdInsert(t, db, "issue_link", lk, 1, "l2", "link", "relates to", 1, "9099", "OTH-9", "Not synced", "11", "T1", nil)
	rdInsert(t, db, "issue_link", lk, 1, "l1", "link", "blocks", 0, "9002", "MOB-2", "Stale summary", "10", "T2", "acc-zed")
	rdInsert(t, db, "issue_link", lk, 1, "sub:9004", "subtask", "", 0, "9004", "MOB-4", "Sub", "16", "T3", "acc-bob")

	at := "issue_id,source_id,media_file_id,name,mime,bytes,author_account_id,created_at,is_image,width,height,thumb_path,file_path,fetch_error,removed_at"
	rdInsert(t, db, "attachment", at, 1, "a1", "uuid-1", "old.png", "image/png", 10, "acc-ann", rdMs("2026-03-10T10:00:00Z"), 1, 640, 480, "t1", "f1", nil, nil)
	rdInsert(t, db, "attachment", at, 1, "a2", nil, "new.png", "image/png", 20, "acc-bob", rdMs("2026-03-12T10:00:00Z"), 1, nil, nil, "t2", nil, nil, nil)
	rdInsert(t, db, "attachment", at, 1, "a3", nil, "spec.pdf", "application/pdf", 30, "acc-bob", rdMs("2026-03-14T10:00:00Z"), 0, nil, nil, nil, nil, nil, nil)
	rdInsert(t, db, "attachment", at, 1, "a4", nil, "broken.png", "image/png", 40, nil, rdMs("2026-03-11T10:00:00Z"), 1, nil, nil, nil, nil, "media_unavailable", nil)
	rdInsert(t, db, "attachment", at, 1, "a5", nil, "gone.png", "image/png", 50, nil, rdMs("2026-03-16T10:00:00Z"), 1, nil, nil, nil, nil, nil, rdMs("2026-03-17T10:00:00Z"))

	rdExec(t, db, `UPDATE comment SET body_adf = ?, updated_at = ?, visibility_type='role', visibility_value='Developers' WHERE issue_id=1 AND comment_id='c1'`,
		`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"acc-zed","text":"@Zed"}}]}]}`, rdMs("2026-03-06T09:00:00Z"))
	rdExec(t, db, `UPDATE comment SET body_adf = ? WHERE issue_id=1 AND comment_id='c4'`, `{"type":"doc","version":1,"content":[]}`)
	rdExec(t, db, `UPDATE comment SET deleted_at = ? WHERE issue_id=1 AND comment_id='c5'`, rdMs("2026-03-17T09:00:00Z"))

	rdInsert(t, db, "assignee_change", "issue_id,history_id,changed_at,from_account_id,to_account_id", 1, "a1", rdMs("2026-03-04T10:00:00Z"), nil, "acc-ann")
	rdInsert(t, db, "ticket_draft", "issue_id,body_text,mentions_json,updated_at", 1, "hello @Bob", `[{"start":6,"end":10,"accountId":"acc-bob","name":"Bob Ray"}]`, rdMs("2026-03-18T10:00:00Z"))
	return db
}

func ctLoad(t *testing.T, db *store.DB, id int64) cache.Ticket {
	t.Helper()
	tk, err := cache.LoadTicket(context.Background(), db.R, id)
	if err != nil {
		t.Fatalf("LoadTicket(%d): %v", id, err)
	}
	return tk
}

func TestLoadTicket_UnknownIssueIsNotFound(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	_, err := cache.LoadTicket(context.Background(), db.R, 4242)
	if !apperr.Is(err, apperr.NotFound) {
		t.Errorf("err = %v, want not_found", err)
	}
}

func TestLoadTicket_IssueContentAndHeaderFields(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)
	if tk.LocalID != 1 || tk.ConnectionID != 1 || tk.Issue.Key != "MOB-1" || tk.Issue.SourceID != "9001" {
		t.Errorf("identity = %d/%d/%+v", tk.LocalID, tk.ConnectionID, tk.Issue)
	}
	if tk.TypeKind != "story" {
		t.Errorf("TypeKind = %q, want story", tk.TypeKind)
	}
	if tk.Issue.AssigneeID != "acc-ann" || tk.Issue.ParentKey != "MOB-100" || len(tk.Issue.SprintIDs) != 2 {
		t.Errorf("issue = %+v", tk.Issue)
	}
	rdSame(t, "HydratedAt", tk.HydratedAt, rdTS("2026-03-18T18:30:00Z"))
	rdZero(t, "RemovedAt", tk.RemovedAt)
	if tk.Description == nil || !strings.Contains(string(tk.Description.Raw), "inlineCard") || tk.Description.Format != model.RichTextADF {
		t.Errorf("Description = %+v", tk.Description)
	}
	if tk.Environment == nil || !strings.Contains(string(tk.Environment.Raw), "iOS") {
		t.Errorf("Environment = %+v", tk.Environment)
	}
	if tk.Watchers != 4 {
		t.Errorf("Watchers = %d, want 4", tk.Watchers)
	}
	rdSame(t, "ContentUpdatedAt", tk.ContentUpdatedAt, rdTS("2026-03-18T18:30:00Z"))
}

func TestLoadTicket_NoContentRowYetAndTombstonedIssue(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 2) // MOB-2: synced before content existed
	if tk.Description != nil || tk.Environment != nil {
		t.Errorf("Description/Environment = %+v/%+v, want nil", tk.Description, tk.Environment)
	}
	if tk.Watchers != -1 {
		t.Errorf("Watchers = %d, want -1 (unknown)", tk.Watchers)
	}
	rdZero(t, "ContentUpdatedAt", tk.ContentUpdatedAt)
	if tk.Fields == nil || tk.Registry == nil || tk.Comments == nil || tk.Attachments == nil || tk.Links == nil ||
		tk.People == nil || tk.Issues == nil || tk.Draft.Mentions == nil {
		t.Errorf("a list or map is nil: %+v", tk)
	}
	if tk.Parent != nil {
		t.Errorf("Parent = %+v, want nil for an issue without a parent key", tk.Parent)
	}
	if tk.Draft.Text != "" || len(tk.Draft.Mentions) != 0 {
		t.Errorf("Draft = %+v, want empty", tk.Draft)
	}

	gone := ctLoad(t, db, 11) // left every standup
	rdSame(t, "RemovedAt", gone.RemovedAt, rdTS("2026-03-14T09:00:00Z"))
}

func TestLoadTicket_FieldValuesJoinTheRegistryAndPrefs(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)

	byID := map[string]cache.TicketField{}
	for _, f := range tk.Fields {
		byID[f.Value.FieldID] = f
	}
	if _, ok := byID["parent"]; ok {
		t.Errorf("the synthetic parent row leaked into Fields")
	}
	if len(tk.Fields) != 4 {
		t.Fatalf("Fields = %d, want 4", len(tk.Fields))
	}
	if f := byID["customfield_10016"]; f.Name != "Story point estimate" || !f.Custom || f.Value.Number == nil || *f.Value.Number != 5 {
		t.Errorf("points field = %+v", f)
	}
	if f := byID["priority"]; f.Name != "Priority" || f.Custom || f.Value.Text != "High" {
		t.Errorf("priority field = %+v", f)
	}

	reg := map[string]cache.TicketFieldDef{}
	for _, d := range tk.Registry {
		reg[d.Def.ID] = d
	}
	if len(reg) != 5 {
		t.Fatalf("Registry = %d entries, want 5", len(reg))
	}
	if d := reg["customfield_10200"]; d.Pinned != 0 || d.Hidden || !d.Def.Custom || d.Def.Kind != model.FieldUsers || d.Def.Name != "Reviewers" {
		t.Errorf("pinned entry = %+v", d)
	}
	if d := reg["customfield_10300"]; d.Pinned != -1 || !d.Hidden {
		t.Errorf("hidden entry = %+v, want Pinned -1 and Hidden", d)
	}
	if d := reg["priority"]; d.Pinned != -1 || d.Hidden {
		t.Errorf("plain entry = %+v, want Pinned -1 and not Hidden", d)
	}
}

func TestLoadTicket_CommentsOldestFirstWithoutTombstonesAndHistoryKeepsThem(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)

	var ids []string
	for _, c := range tk.Comments {
		ids = append(ids, c.ID)
	}
	if got := strings.Join(ids, ","); got != "c00,c0,c1,c2,c3,c4,c6" {
		t.Errorf("comment ids = %s, want c00,c0,c1,c2,c3,c4,c6 (c5 is tombstoned)", got)
	}
	var c1 model.Comment
	for _, c := range tk.Comments {
		if c.ID == "c1" {
			c1 = c
		}
	}
	if c1.Body.IsEmpty() || c1.Body.Format != model.RichTextADF || !strings.Contains(string(c1.Body.Raw), "acc-zed") {
		t.Errorf("c1 body = %+v", c1.Body)
	}
	if c1.Visibility == nil || c1.Visibility.Type != "role" || c1.Visibility.Value != "Developers" {
		t.Errorf("c1 visibility = %+v", c1.Visibility)
	}
	rdSame(t, "c1 UpdatedAt", c1.UpdatedAt, rdTS("2026-03-06T09:00:00Z"))
	for _, c := range tk.Comments {
		if c.ID == "c0" {
			rdSame(t, "c0 UpdatedAt (row value 0)", c.UpdatedAt, c.At)
			if c.Visibility != nil || !c.Body.IsEmpty() {
				t.Errorf("c0 = %+v, want no visibility and no body", c)
			}
		}
	}

	hist := map[string]bool{}
	for _, c := range tk.History.Comments {
		hist[c.ID] = true
	}
	if !hist["c5"] || len(tk.History.Comments) != 8 {
		t.Errorf("History.Comments = %d entries (c5 kept: %v), want 8 incl. the tombstoned comment", len(tk.History.Comments), hist["c5"])
	}
}

func TestLoadTicket_AttachmentsImagesFirstThenFilesNewestFirstWithCacheState(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)
	var names []string
	for _, a := range tk.Attachments {
		names = append(names, a.Name)
	}
	if got := strings.Join(names, ","); got != "new.png,broken.png,old.png,spec.pdf" {
		t.Fatalf("attachments = %s, want new.png,broken.png,old.png,spec.pdf (images first, newest first; removed hidden)", got)
	}
	by := map[string]cache.TicketAttachment{}
	for _, a := range tk.Attachments {
		by[a.Name] = a
	}
	if a := by["old.png"]; !a.IsImage || !a.HasThumb || !a.HasFile || a.Width != 640 || a.Height != 480 || a.MediaFileID != "uuid-1" ||
		a.ID != "a1" || a.Mime != "image/png" || a.Bytes != 10 || a.AuthorID != "acc-ann" || a.LocalID == 0 || a.FetchError != "" {
		t.Errorf("old.png = %+v", a)
	}
	if a := by["new.png"]; !a.HasThumb || a.HasFile || a.Width != 0 {
		t.Errorf("new.png = %+v, want thumb only", a)
	}
	if a := by["broken.png"]; a.HasThumb || a.HasFile || a.FetchError != "media_unavailable" {
		t.Errorf("broken.png = %+v", a)
	}
	if a := by["spec.pdf"]; a.IsImage || a.HasThumb {
		t.Errorf("spec.pdf = %+v, want a plain file", a)
	}
}

func TestLoadTicket_LinksAndSubtasksResolveAgainstLocalIssues(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)
	if len(tk.Links) != 3 {
		t.Fatalf("Links = %d, want 3", len(tk.Links))
	}
	// relation 'link' first, by label ("blocks" < "relates to"), then the subtask.
	if tk.Links[0].Label != "blocks" || tk.Links[1].Label != "relates to" || tk.Links[2].Relation != "subtask" {
		t.Errorf("order = %q, %q, %q", tk.Links[0].Label, tk.Links[1].Label, tk.Links[2].Relation)
	}
	l := tk.Links[0]
	if l.Relation != "link" || l.OtherKey != "MOB-2" || l.OtherIssueID != 2 || l.LiveSummary != "Summary of MOB-2" || l.LiveStatusID != "11" ||
		l.OtherSummary != "Stale summary" || l.OtherStatusID != "10" || l.OtherTypeID != "T2" || l.OtherTypeKind != "bug" ||
		l.OtherAssigneeID != "acc-zed" || l.OtherSourceID != "9002" {
		t.Errorf("synced link = %+v", l)
	}
	u := tk.Links[1]
	if u.OtherIssueID != 0 || u.LiveSummary != "" || u.OtherSummary != "Not synced" || u.OtherTypeKind != "story" {
		t.Errorf("unsynced link = %+v", u)
	}
	s := tk.Links[2]
	if s.OtherIssueID != 4 || s.OtherKey != "MOB-4" || s.OtherTypeKind != "subtask" || s.OtherAssigneeID != "acc-bob" {
		t.Errorf("subtask = %+v", s)
	}
}

func TestLoadTicket_ParentFromTheSnapshotOrTheParentKey(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)
	if tk.Parent == nil || tk.Parent.Relation != "parent" || tk.Parent.OtherKey != "MOB-100" || tk.Parent.OtherSummary != "Epic snapshot" ||
		tk.Parent.OtherIssueID != 0 || tk.Parent.OtherTypeKind != "story" {
		t.Errorf("Parent = %+v", tk.Parent)
	}

	// The parent is a synced issue: it resolves to the local id and its live summary.
	rdExec(t, db, "UPDATE issue SET parent_key = 'MOB-3' WHERE id = 1")
	rdExec(t, db, "DELETE FROM issue_field_value WHERE issue_id = 1 AND field_id = 'parent'")
	tk = ctLoad(t, db, 1)
	if tk.Parent == nil || tk.Parent.OtherIssueID != 3 || tk.Parent.LiveSummary != "Summary of MOB-3" || tk.Parent.OtherKey != "MOB-3" {
		t.Errorf("Parent from parent_key = %+v, want MOB-3 resolved locally", tk.Parent)
	}
}

func TestLoadTicket_DraftHistoryAndPeople(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)

	if tk.Draft.Text != "hello @Bob" || len(tk.Draft.Mentions) != 1 {
		t.Fatalf("Draft = %+v", tk.Draft)
	}
	if m := tk.Draft.Mentions[0]; m.Start != 6 || m.End != 10 || m.AccountID != "acc-bob" || m.Name != "Bob Ray" {
		t.Errorf("draft mention = %+v", m)
	}

	if n := len(tk.History.StatusChanges); n != 2 || tk.History.StatusChanges[0].ID != "h1" || tk.History.StatusChanges[1].ID != "h2" {
		t.Errorf("StatusChanges = %+v, want h1 then h2 (oldest first, never window-bounded)", tk.History.StatusChanges)
	}
	if n := len(tk.History.AssigneeChanges); n != 1 || tk.History.AssigneeChanges[0].To != "acc-ann" || tk.History.AssigneeChanges[0].From != "" {
		t.Errorf("AssigneeChanges = %+v", tk.History.AssigneeChanges)
	}
	if len(tk.History.FlagChanges) != 0 {
		t.Errorf("FlagChanges = %+v, want none", tk.History.FlagChanges)
	}
	// Dev events are not window-bounded here and keep merge commits: 3 commits + 3 PR events, oldest first.
	if len(tk.History.Dev) != 6 {
		t.Fatalf("Dev = %d events, want 6", len(tk.History.Dev))
	}
	for i := 1; i < len(tk.History.Dev); i++ {
		if tk.History.Dev[i].At.Before(tk.History.Dev[i-1].At) {
			t.Errorf("Dev not oldest first at %d", i)
		}
	}
	merges := 0
	for _, e := range tk.History.Dev {
		if e.Kind == model.DevCommit && e.Merge {
			merges++
		}
		if e.Kind == model.DevPR && e.Repo != "svc-api" {
			t.Errorf("PR repo = %q, want svc-api", e.Repo)
		}
	}
	if merges != 1 {
		t.Errorf("merge commits = %d, want 1", merges)
	}

	want := []string{"acc-ann", "acc-bob", "acc-bot", "acc-zed"}
	var got []string
	for id := range tk.People {
		got = append(got, id)
	}
	if len(got) != len(want) {
		t.Fatalf("People = %v, want exactly %v", got, want)
	}
	for _, id := range want {
		if p, ok := tk.People[id]; !ok || p.DisplayName == "" {
			t.Errorf("People[%s] = %+v, %v", id, p, ok)
		}
	}
	if p := tk.People["acc-bot"]; p.Kind != "app" {
		t.Errorf("acc-bot kind = %q, want app", p.Kind)
	}
	if p := tk.People["acc-zed"]; p.Active {
		t.Errorf("acc-zed active = true, want false")
	}
}

func TestLoadTicket_IssuesMapHoldsOnlySyncedIssuesNamedByTheContent(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)
	if r, ok := tk.Issues["MOB-2"]; !ok || r.LocalID != 2 || r.Summary != "Summary of MOB-2" || r.StatusID != "11" || r.TypeKind != "bug" || r.Key != "MOB-2" {
		t.Errorf("Issues[MOB-2] = %+v, %v (named by the inline card and a link)", r, ok)
	}
	if _, ok := tk.Issues["MOB-4"]; !ok {
		t.Errorf("Issues lacks the subtask MOB-4")
	}
	if _, ok := tk.Issues["MOB-7"]; ok {
		t.Errorf("Issues holds MOB-7, which nothing on the ticket names")
	}
	if _, ok := tk.Issues["OTH-9"]; ok {
		t.Errorf("Issues holds the unsynced OTH-9")
	}
}

func ctSum(t *testing.T, db *store.DB) string {
	t.Helper()
	var all string
	for _, tbl := range []string{"issue", "comment", "attachment", "issue_content", "issue_field_value"} {
		all += rdChecksum(t, db, tbl)
	}
	return all
}

func TestLoadTicketTx_RunsInsideTheCallersReaderTransactionAndWritesNothing(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	before := ctSum(t, db)
	var tk cache.Ticket
	err := db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tk, err = cache.LoadTicketTx(context.Background(), tx, 1)
		return err
	})
	if err != nil || tk.Issue.Key != "MOB-1" {
		t.Fatalf("LoadTicketTx: %+v, %v", tk.Issue, err)
	}
	if after := ctSum(t, db); after != before {
		t.Errorf("LoadTicketTx changed the database")
	}
	_ = time.Second
}

func TestLoadTicket_LinkedIssuesCarryTheTargetsCurrentAssignee(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	tk := ctLoad(t, db, 1)
	// blocks MOB-2: synced and unassigned, so no live assignee although the stored snapshot names acc-zed.
	if l := tk.Links[0]; l.OtherIssueID != 2 || l.LiveAssigneeID != "" || l.OtherAssigneeID != "acc-zed" {
		t.Errorf("link to the unassigned MOB-2 = %+v", l)
	}
	// relates to OTH-9: not synced, so nothing is resolved.
	if l := tk.Links[1]; l.OtherIssueID != 0 || l.LiveAssigneeID != "" {
		t.Errorf("link to the unsynced OTH-9 = %+v", l)
	}
	// subtask MOB-4: synced, assigned to acc-bob.
	if l := tk.Links[2]; l.OtherIssueID != 4 || l.LiveAssigneeID != "acc-bob" {
		t.Errorf("subtask MOB-4 = %+v, want live assignee acc-bob", l)
	}

	// MOB-2 gets an assignee who is not otherwise referenced: the link shows them, and their name is loaded.
	rdInsert(t, db, "person", "connection_id,account_id,display_name,active,account_type,fetched_at", 1, "acc-new", "Nia New", 1, "user", rdMs("2026-03-18T18:00:00Z"))
	rdExec(t, db, "UPDATE issue SET assignee_account_id = 'acc-new' WHERE id = 2")
	tk = ctLoad(t, db, 1)
	if l := tk.Links[0]; l.LiveAssigneeID != "acc-new" {
		t.Errorf("link to MOB-2 after assignment = %+v, want live assignee acc-new", l)
	}
	if p, ok := tk.People["acc-new"]; !ok || p.DisplayName != "Nia New" {
		t.Errorf("People[acc-new] = %+v, %v; want Nia New (loaded for the link row)", p, ok)
	}
}

func TestApplyDevTx_StoresPullRequestTitleAndBranchAndNeverBlanksThem(t *testing.T) {
	defer rdRecover(t)
	db := ctSeedTicket(t)
	ctx := context.Background()
	apply := func(evs ...model.DevEvent) {
		t.Helper()
		tx, err := db.W.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := cache.ApplyDevTx(ctx, tx, 1, model.DevActivity{Events: evs}); err != nil {
			_ = tx.Rollback()
			t.Fatalf("ApplyDevTx: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	at := rdTS("2026-03-20T10:00:00Z")
	pr := func(title, branch string) model.DevEvent {
		return model.DevEvent{Kind: model.DevPR, ID: "svc-api#31", Repo: "svc-api", At: at, Status: "OPEN", Title: title, Branch: branch}
	}
	find := func() (model.DevEvent, int) {
		t.Helper()
		var found model.DevEvent
		n := 0
		for _, e := range ctLoad(t, db, 1).History.Dev {
			if e.Kind == model.DevPR && e.ID == "svc-api#31" {
				found = e
				n++
			}
		}
		return found, n
	}

	apply(pr("", "")) // an older adapter response: no title, no branch
	if e, n := find(); n != 1 || e.Title != "" || e.Branch != "" {
		t.Errorf("event = %+v (%d rows), want one row without title or branch", e, n)
	}
	apply(pr("Versioned image URLs", "feature/MOB-1-versioned-images")) // the same natural key fills the NULLs
	if e, n := find(); n != 1 || e.Title != "Versioned image URLs" || e.Branch != "feature/MOB-1-versioned-images" {
		t.Errorf("event = %+v (%d rows), want the title and branch filled in place", e, n)
	}
	apply(pr("", "")) // a response without them never blanks what is known
	if e, _ := find(); e.Title != "Versioned image URLs" || e.Branch != "feature/MOB-1-versioned-images" {
		t.Errorf("event = %+v, want the title and branch kept", e)
	}
	apply(pr("Versioned image URLs (renamed)", "")) // a renamed PR updates its title
	if e, n := find(); n != 1 || e.Title != "Versioned image URLs (renamed)" || e.Branch != "feature/MOB-1-versioned-images" {
		t.Errorf("event = %+v (%d rows), want the new title and the kept branch", e, n)
	}
}

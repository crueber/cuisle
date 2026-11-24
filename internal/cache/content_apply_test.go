package cache_test

// Tests for the per-issue apply of WP-T6 (ADR 0015, data-model.md §2.5a, jira-sync.md §4.6 step 3 and §4.8):
// comment bodies, tombstones, content replacement by hash, attachment upserts, never-overwrite-newer and the delta.
//
// SPEC-NOTE: ApplyIssueDetailTx reports Skipped when the incoming updated_at is strictly older than the stored one; an
// equal updated_at replaces the row (a re-hydration of an unchanged issue is harmless and refreshes hydrated_at).
// SPEC-NOTE: a comment the source did not return is tombstoned only after a complete fetch (d.Content != nil).
// SPEC-NOTE: the parent's display snapshot is kept as an issue_field_value row with the synthetic field id "parent"
// (issue_link only admits link and subtask); LoadTicket reads it as Ticket.Parent, never as a field.
// SPEC-NOTE: prefix "ct" marks the helpers of this file and ticket_test.go.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

func ctDB(t *testing.T) *store.DB {
	t.Helper()
	db := rdOpen(t)
	rdInsert(t, db, "connection", "id,source,base_url,account,account_id,display_name,auth_state,created_at",
		1, "jira", "https://acme.atlassian.net", "me@acme.com", "acc-me", "Me", "ok", rdMs("2026-03-01T10:00:00Z"))
	return db
}

func ctRich(s string) model.RichText {
	b, _ := json.Marshal(map[string]any{"type": "doc", "version": 1, "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": s}}}}})
	return model.RichText{Format: model.RichTextADF, Raw: b}
}

var ctT0 = rdTS("2026-03-18T10:00:00Z")

// ctDetail is a hydrated issue with content, two comments (one restricted) and an image attachment.
func ctDetail() model.IssueDetail {
	desc := ctRich("Description v1")
	env := ctRich("iOS 18")
	return model.IssueDetail{
		Issue: model.Issue{SourceID: "9001", Key: "MOB-1", SpaceID: "100", Summary: "Toggle units", TypeID: "T1",
			StatusID: "12", AssigneeID: "acc-ann", Rank: "a", ParentKey: "MOB-100", SprintIDs: []string{"S42"},
			CreatedAt: ctT0.Add(-72 * time.Hour), UpdatedAt: ctT0.Add(-1 * time.Hour)},
		Type:   model.IssueType{ID: "T1", Name: "Story", Kind: "story"},
		Status: model.Status{ID: "12", Name: "In Progress", Category: "indeterminate"},
		Comments: []model.Comment{
			{ID: "c1", At: ctT0.Add(-48 * time.Hour), UpdatedAt: ctT0.Add(-48 * time.Hour), AuthorID: "acc-ann", Body: ctRich("first")},
			{ID: "c2", At: ctT0.Add(-24 * time.Hour), UpdatedAt: ctT0.Add(-24 * time.Hour), AuthorID: "acc-bob", Body: ctRich("second"),
				Visibility: &model.Visibility{Type: "role", Value: "Developers"}},
		},
		CommentTotal: 2,
		Content: &model.IssueContent{
			Description: &desc, Environment: &env, Watchers: 3, Hash: "h1",
			Attachments: []model.Attachment{
				{ID: "a1", Name: "shot.png", Mime: "image/png", Bytes: 1200, AuthorID: "acc-ann", CreatedAt: ctT0.Add(-30 * time.Hour), MediaFileID: "uuid-1"},
				{ID: "a2", Name: "spec.pdf", Mime: "application/pdf", Bytes: 4000, AuthorID: "acc-bob", CreatedAt: ctT0.Add(-20 * time.Hour)},
			},
			Fields: []model.FieldValue{
				{FieldID: "priority", Kind: model.FieldOption, Text: "High"},
				{FieldID: "labels", Kind: model.FieldLabels, Items: []string{"ux", "units"}},
			},
			Links: []model.IssueLink{{ID: "l1", Label: "blocks", Outward: true, OtherSourceID: "9002", OtherKey: "MOB-2",
				OtherSummary: "Other", OtherStatusID: "11", OtherTypeID: "T2"}},
			Subtasks: []model.IssueRef{{SourceID: "9004", Key: "MOB-4", Summary: "Sub", StatusID: "16", TypeID: "T3", AssigneeID: "acc-bob"}},
			Parent:   &model.IssueRef{SourceID: "9100", Key: "MOB-100", Summary: "Epic", StatusID: "12", TypeID: "T1"},
		},
	}
}

func ctApply(t *testing.T, db *store.DB, d model.IssueDetail, at time.Time) cache.IssueApply {
	t.Helper()
	var res cache.IssueApply
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		res, err = cache.ApplyIssueDetailTx(context.Background(), tx, 1, d, at)
		return err
	})
	if err != nil {
		t.Fatalf("ApplyIssueDetailTx: %v", err)
	}
	return res
}

func ctStr(t *testing.T, db *store.DB, q string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := db.W.QueryRowContext(context.Background(), q, args...).Scan(&s); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return s.String
}

func ctInt(t *testing.T, db *store.DB, q string, args ...any) int64 {
	t.Helper()
	var n sql.NullInt64
	if err := db.W.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n.Int64
}

func TestApplyIssueDetail_StoresCommentsWithBodiesAndVisibility(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	res := ctApply(t, db, ctDetail(), ctT0)
	if !res.Created || res.Skipped || !res.Comments {
		t.Fatalf("result = %+v, want Created, not Skipped, Comments", res)
	}
	if n := rdCount(t, db, "comment", "issue_id = ?", res.IssueID); n != 2 {
		t.Fatalf("comment rows = %d, want 2", n)
	}
	if body := ctStr(t, db, "SELECT body_adf FROM comment WHERE comment_id='c1'"); !strings.Contains(body, "first") {
		t.Errorf("c1 body = %q, want the ADF JSON with 'first'", body)
	}
	if vt, vv := ctStr(t, db, "SELECT visibility_type FROM comment WHERE comment_id='c2'"), ctStr(t, db, "SELECT visibility_value FROM comment WHERE comment_id='c2'"); vt != "role" || vv != "Developers" {
		t.Errorf("c2 visibility = %q/%q, want role/Developers", vt, vv)
	}
	if vt := ctStr(t, db, "SELECT visibility_type FROM comment WHERE comment_id='c1'"); vt != "" {
		t.Errorf("c1 visibility_type = %q, want NULL", vt)
	}
	if u := ctInt(t, db, "SELECT updated_at FROM comment WHERE comment_id='c1'"); u != ctT0.Add(-48*time.Hour).UnixMilli() {
		t.Errorf("c1 updated_at = %d, want created_at", u)
	}
}

func TestApplyIssueDetail_EditedCommentReplacesBodyAndKeepsNewerOnes(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	ctApply(t, db, ctDetail(), ctT0)

	d := ctDetail()
	d.Comments[0].Body = ctRich("first (edited)")
	d.Comments[0].UpdatedAt = ctT0.Add(-2 * time.Hour)
	d.UpdatedAt = ctT0
	res := ctApply(t, db, d, ctT0.Add(time.Minute))
	if !res.Comments {
		t.Errorf("Comments = false after an edit")
	}
	if body := ctStr(t, db, "SELECT body_adf FROM comment WHERE comment_id='c1'"); !strings.Contains(body, "edited") {
		t.Errorf("body = %q, want the edited text", body)
	}
	if u := ctInt(t, db, "SELECT updated_at FROM comment WHERE comment_id='c1'"); u != ctT0.Add(-2*time.Hour).UnixMilli() {
		t.Errorf("updated_at = %d, want the edit time", u)
	}

	// An older copy of the comment (a stale fetch) must not bring the old text back.
	stale := ctDetail()
	stale.UpdatedAt = ctT0
	ctApply(t, db, stale, ctT0.Add(2*time.Minute))
	if body := ctStr(t, db, "SELECT body_adf FROM comment WHERE comment_id='c1'"); !strings.Contains(body, "edited") {
		t.Errorf("a stale fetch replaced the edited body: %q", body)
	}
}

func TestApplyIssueDetail_CommentMissingFromACompleteFetchIsTombstonedNotDeleted(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	res := ctApply(t, db, ctDetail(), ctT0)

	d := ctDetail()
	d.Comments = d.Comments[:1] // c2 was deleted in Jira
	d.CommentTotal = 1          // the provider says there is one comment now: the list is complete
	d.UpdatedAt = ctT0
	r2 := ctApply(t, db, d, ctT0.Add(time.Minute))
	if !r2.Comments {
		t.Errorf("Comments = false although a comment disappeared")
	}
	if n := rdCount(t, db, "comment", "issue_id = ?", res.IssueID); n != 2 {
		t.Fatalf("comment rows = %d, want 2 (the row is kept)", n)
	}
	if at := ctInt(t, db, "SELECT deleted_at FROM comment WHERE comment_id='c2'"); at != ctT0.Add(time.Minute).UnixMilli() {
		t.Errorf("c2 deleted_at = %d, want the apply time", at)
	}
	if at := ctInt(t, db, "SELECT deleted_at FROM comment WHERE comment_id='c1'"); at != 0 {
		t.Errorf("c1 deleted_at = %d, want NULL", at)
	}

	// The comment returns (restored): the tombstone is cleared.
	d3 := ctDetail()
	d3.UpdatedAt = ctT0.Add(time.Hour)
	ctApply(t, db, d3, ctT0.Add(2*time.Hour))
	if at := ctInt(t, db, "SELECT deleted_at FROM comment WHERE comment_id='c2'"); at != 0 {
		t.Errorf("c2 deleted_at = %d after it returned, want NULL", at)
	}
}

func TestApplyIssueDetail_AShortOrEarlyEndedCommentPageNeverTombstones(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	ctApply(t, db, ctDetail(), ctT0)

	// The provider says 2 comments but the pages ended after 1: c2 was not reached, not deleted.
	d := ctDetail()
	d.Comments = d.Comments[:1]
	d.CommentTotal = 2
	d.UpdatedAt = ctT0
	r := ctApply(t, db, d, ctT0.Add(time.Minute))
	if r.Comments {
		t.Errorf("Comments = true for an unchanged short page")
	}
	if n := rdCount(t, db, "comment", "deleted_at IS NOT NULL"); n != 0 {
		t.Errorf("tombstoned comments = %d after a short page, want 0", n)
	}

	// A total of 0 means the provider has no comments: the stored ones are tombstoned.
	e := ctDetail()
	e.Comments = nil
	e.CommentTotal = 0
	e.UpdatedAt = ctT0
	ctApply(t, db, e, ctT0.Add(2*time.Minute))
	if n := rdCount(t, db, "comment", "deleted_at IS NOT NULL"); n != 2 {
		t.Errorf("tombstoned comments = %d after a complete empty list, want 2", n)
	}
}

func TestApplyIssueDetail_ReportsTheFirstContentAnIssueEverGets(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	meta := ctDetail()
	meta.Content = nil // a metadata-only apply: the issue exists, with no content row
	meta.UpdatedAt = ctT0
	if r := ctApply(t, db, meta, ctT0); r.FirstContent {
		t.Errorf("FirstContent = true without content")
	}
	d := ctDetail()
	d.UpdatedAt = ctT0
	if r := ctApply(t, db, d, ctT0.Add(time.Minute)); !r.FirstContent || r.Created {
		t.Errorf("result = %+v, want FirstContent on an existing issue without a content row", r)
	}
	d.Content.Hash = "h2"
	if r := ctApply(t, db, d, ctT0.Add(2*time.Minute)); r.FirstContent {
		t.Errorf("FirstContent = true on the second content apply")
	}
}

func TestFieldsInUse_FieldsWithAValueOnAnotherIssueOfTheSameSpace(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	mk := func(src, key, space string, fields ...string) int64 {
		d := ctDetail()
		d.SourceID, d.Key, d.SpaceID = src, key, space
		d.Content.Hash = key
		d.Content.Fields = nil
		for _, f := range fields {
			d.Content.Fields = append(d.Content.Fields, model.FieldValue{FieldID: f, Kind: model.FieldText, Text: "x"})
		}
		return ctApply(t, db, d, ctT0).IssueID
	}
	me := mk("1", "MOB-1", "100", "own_only", "shared")
	mk("2", "MOB-2", "100", "shared", "other_one")
	mk("3", "MOB-3", "200", "elsewhere") // another space
	var got map[string]bool
	err := db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		got, err = cache.FieldsInUseTx(context.Background(), tx, me)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	// The ticket's own values do not count; neither do other spaces. The parent snapshot row is a field row too.
	for _, want := range []string{"shared", "other_one"} {
		if !got[want] {
			t.Errorf("FieldsInUse lacks %q: %v", want, got)
		}
	}
	for _, bad := range []string{"own_only", "elsewhere"} {
		if got[bad] {
			t.Errorf("FieldsInUse holds %q: %v", bad, got)
		}
	}
	err = db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		_, err := cache.FieldsInUseTx(context.Background(), tx, 4242)
		return err
	})
	if !apperr.Is(err, apperr.NotFound) {
		t.Errorf("unknown issue: err = %v, want not_found", err)
	}
}

func TestApplyIssueDetail_MetadataOnlySourceNeverTombstonesOrErasesBodies(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	ctApply(t, db, ctDetail(), ctT0)

	// A source without Capabilities.Content: no Content, truncated comment list, empty bodies.
	d := ctDetail()
	d.Content = nil
	d.UpdatedAt = ctT0
	d.Comments = []model.Comment{{ID: "c1", At: ctT0.Add(-48 * time.Hour), AuthorID: "acc-ann"}}
	ctApply(t, db, d, ctT0.Add(time.Minute))
	if at := ctInt(t, db, "SELECT deleted_at FROM comment WHERE comment_id='c2'"); at != 0 {
		t.Errorf("c2 was tombstoned by a metadata-only fetch (deleted_at=%d)", at)
	}
	if body := ctStr(t, db, "SELECT body_adf FROM comment WHERE comment_id='c1'"); !strings.Contains(body, "first") {
		t.Errorf("a body-less upsert erased the stored body: %q", body)
	}
	if n := rdCount(t, db, "issue_content", ""); n != 1 {
		t.Errorf("issue_content rows = %d, want the earlier row kept", n)
	}
}

func TestApplyIssueDetail_FillsCommentRowsLeftByMigration0003(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	// What migration 0003 leaves for a comment synced before content existed: updated_at = created_at, no body.
	rdIssue(t, db, 1, "9001", "MOB-1", "T1", "12", "acc-ann", 0, "a", "MOB-100", `[]`, "2026-03-01T09:00:00Z", "2026-03-17T12:00:00Z", nil)
	created := ctT0.Add(-48 * time.Hour)
	rdInsert(t, db, "comment", "issue_id,comment_id,author_account_id,created_at,updated_at", 1, "c1", "acc-ann", created.UnixMilli(), created.UnixMilli())

	d := ctDetail()
	d.UpdatedAt = rdTS("2026-03-17T12:00:00Z") // equal to the stored row: allowed
	ctApply(t, db, d, ctT0)
	if body := ctStr(t, db, "SELECT body_adf FROM comment WHERE comment_id='c1'"); !strings.Contains(body, "first") {
		t.Errorf("the migrated comment row did not get its body: %q", body)
	}
	if n := rdCount(t, db, "comment", ""); n != 2 {
		t.Errorf("comment rows = %d, want 2 (c1 filled, c2 added)", n)
	}
}

func TestApplyIssueDetail_StoresContentTables(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	res := ctApply(t, db, ctDetail(), ctT0)
	if !res.Description || !res.Fields || !res.Attachments {
		t.Errorf("result = %+v, want Description, Fields and Attachments set on first apply", res)
	}
	id := res.IssueID
	if d := ctStr(t, db, "SELECT description_adf FROM issue_content WHERE issue_id=?", id); !strings.Contains(d, "Description v1") {
		t.Errorf("description_adf = %q", d)
	}
	if e := ctStr(t, db, "SELECT environment_adf FROM issue_content WHERE issue_id=?", id); !strings.Contains(e, "iOS 18") {
		t.Errorf("environment_adf = %q", e)
	}
	if w := ctInt(t, db, "SELECT watchers FROM issue_content WHERE issue_id=?", id); w != 3 {
		t.Errorf("watchers = %d, want 3", w)
	}
	if h := ctStr(t, db, "SELECT content_hash FROM issue_content WHERE issue_id=?", id); h != "h1" {
		t.Errorf("content_hash = %q, want the source's hash h1", h)
	}
	if n := rdCount(t, db, "issue_field_value", "issue_id=? AND field_id <> 'parent'", id); n != 2 {
		t.Errorf("field value rows = %d, want 2", n)
	}
	var fv model.FieldValue
	if err := json.Unmarshal([]byte(ctStr(t, db, "SELECT value_json FROM issue_field_value WHERE issue_id=? AND field_id='labels'", id)), &fv); err != nil || len(fv.Items) != 2 {
		t.Errorf("labels value = %+v, err %v", fv, err)
	}
	if n := rdCount(t, db, "issue_link", "issue_id=? AND relation='link'", id); n != 1 {
		t.Errorf("link rows = %d, want 1", n)
	}
	if lid := ctStr(t, db, "SELECT link_id FROM issue_link WHERE issue_id=? AND relation='subtask'", id); lid != "sub:9004" {
		t.Errorf("subtask link_id = %q, want sub:9004", lid)
	}
	if n := rdCount(t, db, "attachment", "issue_id=?", id); n != 2 {
		t.Errorf("attachment rows = %d, want 2", n)
	}
	if v := ctInt(t, db, "SELECT is_image FROM attachment WHERE source_id='a1'"); v != 1 {
		t.Errorf("a1 is_image = %d, want 1", v)
	}
	if v := ctInt(t, db, "SELECT is_image FROM attachment WHERE source_id='a2'"); v != 0 {
		t.Errorf("a2 (pdf) is_image = %d, want 0", v)
	}
	if m := ctStr(t, db, "SELECT media_file_id FROM attachment WHERE source_id='a1'"); m != "uuid-1" {
		t.Errorf("a1 media_file_id = %q", m)
	}
	if n := rdCount(t, db, "issue_field_value", "issue_id=? AND field_id='parent'", id); n != 1 {
		t.Errorf("parent snapshot rows = %d, want 1", n)
	}
}

func TestApplyIssueDetail_ContentIsReplacedOnlyWhenTheHashChanges(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	res := ctApply(t, db, ctDetail(), ctT0)
	id := res.IssueID
	rdExec(t, db, "UPDATE issue_field_value SET value_json = 'sentinel' WHERE issue_id=? AND field_id='priority'", id)

	// Same hash: nothing is rewritten, nothing is reported.
	same := ctDetail()
	same.UpdatedAt = ctT0
	r2 := ctApply(t, db, same, ctT0.Add(time.Hour))
	if r2.Description || r2.Fields || r2.Attachments {
		t.Errorf("unchanged hash reported %+v", r2)
	}
	if v := ctStr(t, db, "SELECT value_json FROM issue_field_value WHERE issue_id=? AND field_id='priority'", id); v != "sentinel" {
		t.Errorf("a same-hash apply rewrote the content tables (priority now %q)", v)
	}
	if u := ctInt(t, db, "SELECT updated_at FROM issue_content WHERE issue_id=?", id); u != ctT0.UnixMilli() {
		t.Errorf("issue_content.updated_at = %d, want unchanged %d", u, ctT0.UnixMilli())
	}

	// New hash + new description: replaced, and only the description is reported (fields are equal).
	chg := ctDetail()
	chg.UpdatedAt = ctT0.Add(time.Minute)
	nd := ctRich("Description v2")
	chg.Content.Description = &nd
	chg.Content.Hash = "h2"
	r3 := ctApply(t, db, chg, ctT0.Add(2*time.Hour))
	if !r3.Description {
		t.Errorf("Description = false after the description changed")
	}
	if r3.Attachments {
		t.Errorf("Attachments = true although the attachments are the same")
	}
	if d := ctStr(t, db, "SELECT description_adf FROM issue_content WHERE issue_id=?", id); !strings.Contains(d, "v2") {
		t.Errorf("description_adf = %q, want v2", d)
	}
	if v := ctStr(t, db, "SELECT value_json FROM issue_field_value WHERE issue_id=? AND field_id='priority'", id); v == "sentinel" {
		t.Errorf("a changed hash did not replace the field values")
	}
	if u := ctInt(t, db, "SELECT updated_at FROM issue_content WHERE issue_id=?", id); u != ctT0.Add(2*time.Hour).UnixMilli() {
		t.Errorf("issue_content.updated_at = %d, want the apply time", u)
	}
}

func TestApplyIssueDetail_ReplacesFieldValuesAndLinksPerIssueAndDropsGoneOnes(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	res := ctApply(t, db, ctDetail(), ctT0)

	d := ctDetail()
	d.UpdatedAt = ctT0
	d.Content.Hash = "h2"
	d.Content.Fields = d.Content.Fields[:1] // labels cleared
	d.Content.Links = nil
	d.Content.Subtasks = nil
	d.Content.Parent = nil
	r := ctApply(t, db, d, ctT0.Add(time.Hour))
	if !r.Fields {
		t.Errorf("Fields = false after fields and links changed")
	}
	if n := rdCount(t, db, "issue_field_value", "issue_id=?", res.IssueID); n != 1 {
		t.Errorf("field value rows = %d, want 1", n)
	}
	if n := rdCount(t, db, "issue_link", "issue_id=?", res.IssueID); n != 0 {
		t.Errorf("link rows = %d, want 0", n)
	}
}

func TestApplyIssueDetail_AttachmentsAreUpsertedKeepingRowIdAndCacheColumns(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	res := ctApply(t, db, ctDetail(), ctT0)
	a1 := ctInt(t, db, "SELECT id FROM attachment WHERE source_id='a1'")
	rdExec(t, db, "UPDATE attachment SET thumb_path='1/1.thumb.jpg', file_path='1/1.orig', cached_bytes=1200, width=640, height=480, last_viewed_at=5 WHERE id=?", a1)

	d := ctDetail()
	d.UpdatedAt = ctT0
	d.Content.Hash = "h2"
	d.Content.Attachments[0].Name = "shot-renamed.png"
	d.Content.Attachments[0].MediaFileID = "" // the source does not know it this time
	d.Content.Attachments = append(d.Content.Attachments, model.Attachment{ID: "a3", Name: "new.png", Mime: "image/png", Bytes: 10, CreatedAt: ctT0})
	r := ctApply(t, db, d, ctT0.Add(time.Hour))
	if !r.Attachments {
		t.Errorf("Attachments = false after one was added")
	}
	if got := ctInt(t, db, "SELECT id FROM attachment WHERE source_id='a1'"); got != a1 {
		t.Errorf("a1 row id changed from %d to %d", a1, got)
	}
	if n := ctStr(t, db, "SELECT name FROM attachment WHERE id=?", a1); n != "shot-renamed.png" {
		t.Errorf("name = %q, want the new name", n)
	}
	if tp := ctStr(t, db, "SELECT thumb_path FROM attachment WHERE id=?", a1); tp != "1/1.thumb.jpg" {
		t.Errorf("thumb_path = %q, want kept", tp)
	}
	if fp := ctStr(t, db, "SELECT file_path FROM attachment WHERE id=?", a1); fp != "1/1.orig" {
		t.Errorf("file_path = %q, want kept", fp)
	}
	if w := ctInt(t, db, "SELECT width FROM attachment WHERE id=?", a1); w != 640 {
		t.Errorf("width = %d, want the decoded 640 kept", w)
	}
	if m := ctStr(t, db, "SELECT media_file_id FROM attachment WHERE id=?", a1); m != "uuid-1" {
		t.Errorf("media_file_id = %q, want the learned id kept", m)
	}
	if n := rdCount(t, db, "attachment", "issue_id=?", res.IssueID); n != 3 {
		t.Errorf("attachment rows = %d, want 3", n)
	}
}

func TestApplyIssueDetail_VanishedAttachmentIsTombstonedAndItsCachedFilesReleased(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	ctApply(t, db, ctDetail(), ctT0)
	rdExec(t, db, "UPDATE attachment SET thumb_path='t', file_path='f', cached_bytes=10 WHERE source_id='a1'")

	d := ctDetail()
	d.UpdatedAt = ctT0
	d.Content.Hash = "h2"
	d.Content.Attachments = d.Content.Attachments[1:] // a1 removed in Jira
	r := ctApply(t, db, d, ctT0.Add(time.Hour))
	if !r.Attachments {
		t.Errorf("Attachments = false after one vanished")
	}
	if n := rdCount(t, db, "attachment", "source_id='a1'"); n != 1 {
		t.Fatalf("a1 rows = %d, want the row kept", n)
	}
	if at := ctInt(t, db, "SELECT removed_at FROM attachment WHERE source_id='a1'"); at != ctT0.Add(time.Hour).UnixMilli() {
		t.Errorf("removed_at = %d, want the apply time", at)
	}
	if fp, tp, cb := ctStr(t, db, "SELECT file_path FROM attachment WHERE source_id='a1'"), ctStr(t, db, "SELECT thumb_path FROM attachment WHERE source_id='a1'"), ctInt(t, db, "SELECT cached_bytes FROM attachment WHERE source_id='a1'"); fp != "" || tp != "" || cb != 0 {
		t.Errorf("cache columns = %q/%q/%d, want cleared so the orphan sweep reclaims the files", fp, tp, cb)
	}

	// It comes back: removed_at is cleared.
	d2 := ctDetail()
	d2.UpdatedAt = ctT0.Add(time.Minute)
	d2.Content.Hash = "h3"
	ctApply(t, db, d2, ctT0.Add(2*time.Hour))
	if at := ctInt(t, db, "SELECT removed_at FROM attachment WHERE source_id='a1'"); at != 0 {
		t.Errorf("removed_at = %d after it returned, want NULL", at)
	}
}

func TestApplyIssueDetail_NeverOverwritesANewerIssueRowButMergesChangeAndCommentRows(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	newer := ctDetail()
	newer.Summary = "Newer summary"
	newer.UpdatedAt = ctT0
	ctApply(t, db, newer, ctT0)

	older := ctDetail()
	older.Summary = "Older summary"
	older.StatusID = "11"
	older.UpdatedAt = ctT0.Add(-time.Hour)
	older.StatusChanges = []model.StatusChange{{ID: "h9", At: ctT0.Add(-90 * time.Minute), From: "10", To: "11"}}
	older.Comments = append(older.Comments, model.Comment{ID: "c3", At: ctT0.Add(-3 * time.Hour), UpdatedAt: ctT0.Add(-3 * time.Hour), AuthorID: "acc-ann", Body: ctRich("third")})
	older.Content.Hash = "h-old"
	nd := ctRich("Old description")
	older.Content.Description = &nd
	res := ctApply(t, db, older, ctT0.Add(time.Hour))
	if !res.Skipped {
		t.Fatalf("Skipped = false for an older fetch")
	}
	if s := ctStr(t, db, "SELECT summary FROM issue WHERE source_id='9001'"); s != "Newer summary" {
		t.Errorf("summary = %q, the older fetch overwrote the newer row", s)
	}
	if st := ctStr(t, db, "SELECT status_id FROM issue WHERE source_id='9001'"); st != "12" {
		t.Errorf("status_id = %q, want 12 kept", st)
	}
	if n := rdCount(t, db, "status_change", "history_id='h9'"); n != 1 {
		t.Errorf("the older fetch's change row was not merged")
	}
	if n := rdCount(t, db, "comment", "comment_id='c3'"); n != 1 {
		t.Errorf("the older fetch's extra comment was not merged")
	}
	if d := ctStr(t, db, "SELECT description_adf FROM issue_content WHERE issue_id=?", res.IssueID); strings.Contains(d, "Old description") {
		t.Errorf("the older fetch's content replaced the newer content")
	}
	if at := ctInt(t, db, "SELECT deleted_at FROM comment WHERE comment_id='c2'"); at != 0 {
		t.Errorf("a skipped (stale) fetch tombstoned a comment")
	}
}

func TestApplyIssueDetail_EqualUpdatedAtReplacesTheRow(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	a := ctDetail()
	a.UpdatedAt = ctT0
	ctApply(t, db, a, ctT0)
	b := ctDetail()
	b.UpdatedAt = ctT0
	b.Summary = "Same instant, new summary"
	res := ctApply(t, db, b, ctT0.Add(time.Hour))
	if res.Skipped {
		t.Errorf("Skipped = true for an equal updated_at")
	}
	if s := ctStr(t, db, "SELECT summary FROM issue WHERE source_id='9001'"); s != b.Summary {
		t.Errorf("summary = %q", s)
	}
}

func TestApplyIssueDetail_ReportsWhichIssueColumnsChanged(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	first := ctDetail()
	first.UpdatedAt = ctT0
	ctApply(t, db, first, ctT0)
	d := ctDetail()
	d.UpdatedAt = ctT0.Add(time.Minute)
	d.StatusID, d.AssigneeID, d.Flagged = "14", "", true
	res := ctApply(t, db, d, ctT0.Add(time.Hour))
	if !res.Status || !res.Assignee || !res.Flag || res.Created {
		t.Errorf("result = %+v, want Status, Assignee and Flag", res)
	}
}

func ctStrPtr(s string) *string { return &s }
func ctBoolPtr(b bool) *bool    { return &b }

func TestApplyIssueDelta_UpdatesTheIssueRowOnlyAndLeavesUpdatedAtAlone(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	first := ctDetail()
	first.UpdatedAt = ctT0
	res := ctApply(t, db, first, ctT0)
	before := ctInt(t, db, "SELECT updated_at FROM issue WHERE id=?", res.IssueID)
	changes := rdCount(t, db, "status_change", "") + rdCount(t, db, "assignee_change", "") + rdCount(t, db, "flag_change", "")

	var out cache.IssueApply
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		out, err = cache.ApplyIssueDeltaTx(context.Background(), tx, res.IssueID, ctStrPtr("14"), ctStrPtr(""), ctBoolPtr(true))
		return err
	})
	if err != nil {
		t.Fatalf("ApplyIssueDeltaTx: %v", err)
	}
	if !out.Status || !out.Assignee || !out.Flag {
		t.Errorf("result = %+v, want all three changed", out)
	}
	if st := ctStr(t, db, "SELECT status_id FROM issue WHERE id=?", res.IssueID); st != "14" {
		t.Errorf("status_id = %q, want 14", st)
	}
	if a := ctStr(t, db, "SELECT assignee_account_id FROM issue WHERE id=?", res.IssueID); a != "" {
		t.Errorf("assignee = %q, want NULL (unassigned)", a)
	}
	if f := ctInt(t, db, "SELECT flagged FROM issue WHERE id=?", res.IssueID); f != 1 {
		t.Errorf("flagged = %d, want 1", f)
	}
	if u := ctInt(t, db, "SELECT updated_at FROM issue WHERE id=?", res.IssueID); u != before {
		t.Errorf("updated_at changed from %d to %d: the healing sync would be skipped as older", before, u)
	}
	if n := rdCount(t, db, "status_change", "") + rdCount(t, db, "assignee_change", "") + rdCount(t, db, "flag_change", ""); n != changes {
		t.Errorf("change rows went from %d to %d: the delta must not invent history", changes, n)
	}

	// Nil leaves a column alone; an unknown issue is not_found.
	err = db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		out, err = cache.ApplyIssueDeltaTx(context.Background(), tx, res.IssueID, nil, nil, nil)
		return err
	})
	if err != nil || out.Status || out.Assignee || out.Flag {
		t.Errorf("nil delta: %+v, %v", out, err)
	}
	err = db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := cache.ApplyIssueDeltaTx(context.Background(), tx, 999, ctStrPtr("1"), nil, nil)
		return err
	})
	if !apperr.Is(err, apperr.NotFound) {
		t.Errorf("unknown issue: err = %v, want not_found", err)
	}
}

func TestApplyFieldDefs_UpsertsAndStoresUnknownKindsAsUnknown(t *testing.T) {
	defer rdRecover(t)
	db := ctDB(t)
	defs := []model.FieldDef{
		{ID: "customfield_1", Name: "Points", Kind: model.FieldNumber, Custom: true, CustomType: "x:float", Schema: json.RawMessage(`{"type":"number"}`)},
		{ID: "priority", Name: "Priority", Kind: "weird"},
	}
	apply := func(defs []model.FieldDef) {
		t.Helper()
		if err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
			return cache.ApplyFieldDefsTx(context.Background(), tx, 1, defs, ctT0)
		}); err != nil {
			t.Fatalf("ApplyFieldDefsTx: %v", err)
		}
	}
	apply(defs)
	if k := ctStr(t, db, "SELECT kind FROM field_def WHERE field_id='priority'"); k != "unknown" {
		t.Errorf("kind = %q, want unknown", k)
	}
	if c := ctInt(t, db, "SELECT custom FROM field_def WHERE field_id='customfield_1'"); c != 1 {
		t.Errorf("custom = %d", c)
	}
	defs[0].Name = "Story points"
	apply(defs[:1])
	if n := ctStr(t, db, "SELECT name FROM field_def WHERE field_id='customfield_1'"); n != "Story points" {
		t.Errorf("name = %q after re-apply", n)
	}
	if n := rdCount(t, db, "field_def", ""); n != 2 {
		t.Errorf("field_def rows = %d, want 2 (never deleted)", n)
	}
}

func TestLiveStandupIDsAndDropTransitionCache(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedBase(t, db)
	rdSeedRecords(t, db)
	rdExec(t, db, "INSERT INTO standup_issue (standup_id, issue_id, role, rank_index) VALUES (?, 1, 'board', 0)", rdSB)
	rdExec(t, db, "INSERT INTO standup_issue (standup_id, issue_id, role, rank_index) VALUES (?, 1, 'board', 0)", rdSD)
	rdExec(t, db, "INSERT INTO transition_cache (issue_id, json, fetched_at) VALUES (1, '[]', 1)")
	var ids []int64
	err := db.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		ids, err = cache.LiveStandupIDsTx(context.Background(), tx, 1)
		if err != nil {
			return err
		}
		return cache.DropTransitionCacheTx(context.Background(), tx, 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != rdSA || ids[1] != rdSB {
		t.Errorf("live standups = %v, want [%d %d] (the archived standup %d excluded)", ids, rdSA, rdSB, rdSD)
	}
	if n := rdCount(t, db, "transition_cache", ""); n != 0 {
		t.Errorf("transition_cache rows = %d, want 0", n)
	}
}

package sync

// Tests for the content and media phases of WP-T6 (ADR 0015, jira-sync.md §2.14, §4.3 phases 5 and 9, §4.6 step 3):
// what a sync stores about ticket content, when it rewrites it, what it announces, and how the media phase behaves.
//
// SPEC-NOTE: the content phase has no requests of its own. The adapter fetches descriptions, fields, links, attachments
// and complete comments inside Hydrate; the orchestrator stores them with the apply and reads the field registry from the
// adapter's cache (zero requests). The request budgets of budget_test.go therefore hold unchanged.
// SPEC-NOTE: ticket:updated {cause:"sync"} is emitted after the apply commits, once per hydrated issue that changed, and
// not at all when nothing changed. StandupIDs are the live standups that have the issue as a current member then.
// SPEC-NOTE: the media phase runs after sync:finished inside the run's goroutine, so Wait(standupID) returns before it
// ends. These tests wait with mediaDone (Orchestrator.WaitMedia), which Shutdown waits on too.
// SPEC-NOTE: media requests are not part of sync_run.requests (the count is taken at finish, before the phase). A fetched
// image costs two requests on the media lane (the 303 and the pre-signed download); learning a media id costs the same.

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	_ "image/jpeg" // decoders for the sizes the fake store records
	_ "image/png"
	"io"
	"net/http"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
)

func asTicketUpdated(p any) (TicketUpdated, bool) {
	switch v := p.(type) {
	case TicketUpdated:
		return v, true
	case *TicketUpdated:
		return *v, v != nil
	}
	return TicketUpdated{}, false
}

func asMediaReady(p any) (MediaReady, bool) {
	switch v := p.(type) {
	case MediaReady:
		return v, true
	case *MediaReady:
		return *v, v != nil
	}
	return MediaReady{}, false
}

func (r *recEmitter) ticketUpdates() (out []TicketUpdated) {
	for _, e := range r.all() {
		if e.Name == EvTicketUpdated {
			if p, ok := asTicketUpdated(e.Payload); ok {
				out = append(out, p)
			}
		}
	}
	return
}

func (r *recEmitter) mediaReady() (out []MediaReady) {
	for _, e := range r.all() {
		if e.Name == EvMediaReady {
			if p, ok := asMediaReady(e.Payload); ok {
				out = append(out, p)
			}
		}
	}
	return
}

// mediaDone waits for every run goroutine, media phases included.
func mediaDone(e *env) { e.o.WaitMedia() }

func TestContent_FirstFullSyncStoresEverythingAboutMOB1421(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	e.mustOK(e.sync(ModeFull))
	id := e.issueID("MOB-1421")

	if d := e.qStr(`SELECT description_adf FROM issue_content WHERE issue_id = ?`, id); !strings.Contains(d, "Unit toggle between metric and US cups") {
		t.Errorf("description_adf = %.80q", d)
	}
	if e.qIsNull(`SELECT environment_adf FROM issue_content WHERE issue_id = ?`, id) {
		t.Errorf("environment_adf is NULL")
	}
	if w := e.qInt(`SELECT watchers FROM issue_content WHERE issue_id = ?`, id); w != 3 {
		t.Errorf("watchers = %d, want 3", w)
	}
	if n := e.count("issue_field_value", "issue_id = ?", id); n < 15 {
		t.Errorf("field value rows = %d, want the custom and standard fields (≥ 15)", n)
	}
	if n := e.count("issue_link", "issue_id = ? AND relation = 'link'", id); n != 3 {
		t.Errorf("link rows = %d, want 3", n)
	}
	if n := e.count("issue_link", "issue_id = ? AND relation = 'subtask'", id); n != 3 {
		t.Errorf("subtask rows = %d, want 3", n)
	}
	if n := e.count("attachment", "issue_id = ?", id); n != 4 {
		t.Errorf("attachment rows = %d, want 4", n)
	}
	if n := e.count("attachment", "issue_id = ? AND is_image = 1", id); n != 3 {
		t.Errorf("image attachments = %d, want 3", n)
	}
	if n := e.count("field_def", "connection_id = ?", e.connID); n < 30 {
		t.Errorf("field_def rows = %d, want the registry (≥ 30)", n)
	}
	if k := e.qStr(`SELECT kind FROM field_def WHERE field_id = 'customfield_10103'`); k != "option" {
		t.Errorf("field_def customfield_10103 kind = %q, want option", k)
	}

	// Every comment has its body; the restricted one carries its visibility; one was edited.
	total := e.count("comment", "issue_id = ?", id)
	if total == 0 {
		t.Fatalf("no comments stored")
	}
	if n := e.count("comment", "issue_id = ? AND body_adf IS NOT NULL", id); n != total {
		t.Errorf("comments with a body = %d of %d", n, total)
	}
	if n := e.count("comment", "issue_id = ? AND visibility_type = 'role' AND visibility_value = 'Developers'", id); n != 1 {
		t.Errorf("restricted comments = %d, want 1", n)
	}
	if n := e.count("comment", "issue_id = ? AND updated_at > created_at", id); n < 1 {
		t.Errorf("edited comments = %d, want at least 1", n)
	}
	if n := e.count("comment", "deleted_at IS NOT NULL"); n != 0 {
		t.Errorf("tombstoned comments = %d after a first sync", n)
	}
}

func TestContent_TheFirstContentOfAnIssueIsNotAnnouncedAndANoChangeSyncAnnouncesNothing(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	r := e.mustOK(e.sync(ModeFull))
	if r.Hydrated < 50 {
		t.Fatalf("hydrated %d issues", r.Hydrated)
	}
	if ups := e.em.ticketUpdates(); len(ups) != 0 {
		t.Errorf("a first sync emitted %d ticket:updated events, want none (new issues, first content)", len(ups))
	}

	// The state a database has after migration 0003: issues exist, their content rows do not. The sync that fills the
	// content announces nothing about it; a real status change on one of them is still announced, alone.
	e.db.W.ExecContext(context.Background(), `DELETE FROM issue_content`)
	e.moveIssue("MOB-1421", "QA", e.clock.Now())
	e.em.reset()
	e.mustOK(e.sync(ModeFull))
	if n := e.count("issue_content", ""); n < 50 {
		t.Fatalf("issue_content rows = %d after the refill", n)
	}
	ups := e.em.ticketUpdates()
	if len(ups) != 1 || ups[0].Key != "MOB-1421" || ups[0].Cause != CauseSync || !equalStrings(ups[0].Changed, []string{ChangedStatus}) ||
		len(ups[0].StandupIDs) != 1 || ups[0].StandupIDs[0] != e.sid {
		t.Errorf("ticket:updated = %+v, want exactly MOB-1421 with [status] (its content is first content, not news)", ups)
	}

	e.em.reset()
	r2 := e.mustOK(e.sync(ModeIncremental))
	if r2.Hydrated != 0 {
		t.Fatalf("a no-change incremental hydrated %d issues", r2.Hydrated)
	}
	if n := len(e.em.ticketUpdates()); n != 0 {
		t.Errorf("ticket:updated events after a no-change sync = %d, want 0", n)
	}
}

func TestContent_ANewCommentIsTheOnlyChangeAnnounced(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeFull))
	e.addComment("MOB-2", "u-iris", e.clock.Now())
	e.em.reset()
	e.mustOK(e.sync(ModeIncremental))
	ups := e.em.ticketUpdates()
	if len(ups) != 1 || ups[0].Key != "MOB-2" || !equalStrings(ups[0].Changed, []string{ChangedComments}) {
		t.Errorf("ticket:updated = %+v, want exactly MOB-2 with [comments]", ups)
	}
}

func TestContent_AnUnchangedContentHashIsNotRewrittenWhileTheIssueRowIs(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	e.mustOK(e.sync(ModeFull))
	keys := e.mutateIssues(budgetChangedIssues) // summary + updated change; the content does not
	var before []int64
	for _, k := range keys {
		before = append(before, e.qInt(`SELECT updated_at FROM issue_content WHERE issue_id = ?`, e.issueID(k)))
	}
	e.em.reset()
	r := e.mustOK(e.sync(ModeIncremental))
	if r.Hydrated != int64(len(keys)) {
		t.Fatalf("hydrated = %d, want %d", r.Hydrated, len(keys))
	}
	for i, k := range keys {
		id := e.issueID(k)
		if got := e.qInt(`SELECT updated_at FROM issue_content WHERE issue_id = ?`, id); got != before[i] {
			t.Errorf("%s: issue_content was rewritten although its content hash is unchanged", k)
		}
		if s := e.qStr(`SELECT summary FROM issue WHERE id = ?`, id); !strings.HasSuffix(s, "(edited)") {
			t.Errorf("%s: the issue row was not refreshed (summary %q)", k, s)
		}
	}
	if ups := e.em.ticketUpdates(); len(ups) != 0 {
		t.Errorf("ticket:updated = %+v, want none: a summary edit is not a content change", ups)
	}
}

func TestContent_ACommentDeletedInJiraIsTombstonedNotRemoved(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{})
	e.mustOK(e.sync(ModeFull))
	id := e.issueID("MOB-3")
	before := e.count("comment", "issue_id = ?", id)
	if before < 2 {
		t.Fatalf("MOB-3 has %d comments, want 2", before)
	}
	e.mutateIssue("MOB-3", func(ds *jiratest.Dataset, is *jiratest.DSIssue) {
		is.Comments = is.Comments[:len(is.Comments)-1]
		is.Updated = e.clock.Now()
	})
	e.em.reset()
	e.mustOK(e.sync(ModeIncremental))
	if n := e.count("comment", "issue_id = ?", id); n != before {
		t.Errorf("comment rows = %d, want %d (the row is kept)", n, before)
	}
	if n := e.count("comment", "issue_id = ? AND deleted_at IS NOT NULL", id); n != 1 {
		t.Errorf("tombstoned comments = %d, want 1", n)
	}
	ups := e.em.ticketUpdates()
	if len(ups) != 1 || !equalStrings(ups[0].Changed, []string{ChangedComments}) {
		t.Errorf("ticket:updated = %+v, want [comments] for the tombstone", ups)
	}
}

func TestContent_AnIncrementalWithNoChangesFetchesNoCommentsFieldsOrMedia(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	e.mustOK(e.sync(ModeFull))
	e.srv.ResetRequests()
	e.mustOK(e.sync(ModeIncremental))
	reqs := e.requests()
	if n := countPath(reqs, isCommentTopUp); n != 0 {
		t.Errorf("comment requests = %d, want 0", n)
	}
	if n := countPath(reqs, isField); n != 0 {
		t.Errorf("field requests = %d, want 0 (the registry is cached)", n)
	}
	if n := countPath(reqs, func(p string) bool { return strings.Contains(p, "/attachment/") || strings.HasPrefix(p, "/media/") }); n != 0 {
		t.Errorf("attachment/media requests = %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------------------------------------------
// media phase

// fakeMedia is a MediaStore that records its calls and does what internal/media does to the attachment row, so the next
// sync sees the thumbnail (sync tests may not import internal/media).
type fakeMedia struct {
	e *env

	mu       gosync.Mutex
	puts     []int64
	thumbs   []int64
	evicts   int
	cached   string           // what Put reports ("full" by default)
	putErr   map[string]error // by attachment name
	putBytes int
}

func newFakeMedia(e *env) *fakeMedia {
	m := &fakeMedia{e: e, cached: "full", putErr: map[string]error{}}
	e.o.opts.Media = m
	return m
}

func (m *fakeMedia) Put(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) (string, error) {
	b, _ := io.ReadAll(r)
	m.mu.Lock()
	m.puts = append(m.puts, att.LocalID)
	m.putBytes += len(b)
	err := m.putErr[att.Name]
	cached := m.cached
	m.mu.Unlock()
	if err != nil {
		return "none", err
	}
	if cached == "full" {
		// What internal/media does to the row: the decoded size, the paths and the cached bytes.
		var w, h any
		if cfg, _, derr := image.DecodeConfig(bytes.NewReader(b)); derr == nil {
			w, h = cfg.Width, cfg.Height
		}
		_, _ = m.e.db.W.ExecContext(ctx, `UPDATE attachment SET thumb_path = 't', file_path = 'f', cached_bytes = ?, width = ?, height = ? WHERE id = ?`,
			len(b), w, h, att.LocalID)
	}
	return cached, nil
}

func (m *fakeMedia) PutThumb(ctx context.Context, att model.Attachment, connectionID int64, r io.Reader) error {
	_, _ = io.Copy(io.Discard, r)
	m.mu.Lock()
	m.thumbs = append(m.thumbs, att.LocalID)
	m.mu.Unlock()
	_, _ = m.e.db.W.ExecContext(ctx, `UPDATE attachment SET thumb_path = 't' WHERE id = ?`, att.LocalID)
	return nil
}

func (m *fakeMedia) Evict(ctx context.Context) (int64, error) {
	m.mu.Lock()
	m.evicts++
	m.mu.Unlock()
	return 0, nil
}

func (m *fakeMedia) counts() (puts, thumbs, evicts int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.puts), len(m.thumbs), m.evicts
}

func mediaPaths(p string) bool {
	return strings.HasPrefix(p, "/rest/api/3/attachment/content/") || strings.HasPrefix(p, "/media/file/")
}

func TestMedia_ImagesAreFetchedAfterSyncFinishedThumbnailsLandMediaIdsAreLearnedAndTheCacheIsEvicted(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	store := newFakeMedia(e)
	lim := jira.LimiterFor(model.Connection{BaseURL: e.srv.URL, Account: e.email})
	before := lim.Stats()

	runID := e.sync(ModeFull)
	r := e.mustOK(runID)
	mediaDone(e)

	images := int(e.qInt(`SELECT count(*) FROM attachment WHERE is_image = 1 AND removed_at IS NULL`))
	if images < 3 {
		t.Fatalf("test setup: %d image attachments, want at least the 3 of MOB-1421", images)
	}
	puts, thumbs, evicts := store.counts()
	if puts != images || thumbs != 0 {
		t.Errorf("Put calls = %d (thumb fallbacks %d), want %d", puts, thumbs, images)
	}
	if evicts != 1 {
		t.Errorf("Evict calls = %d, want exactly one after the phase", evicts)
	}
	if n := e.count("attachment", "is_image = 1 AND thumb_path IS NULL AND removed_at IS NULL"); n != 0 {
		t.Errorf("%d images still without a thumbnail", n)
	}
	// media_file_id learned from the redirect (exact uuid of the overlay).
	if m := e.qStr(`SELECT media_file_id FROM attachment WHERE source_id = '20001'`); m != "3f1c9a52-7d4e-4b6a-9c2e-0a1b2c3d4e51" {
		t.Errorf("media_file_id of 20001 = %q", m)
	}
	// Step 2: the PDF is named by the description's mediaGroup, so its media id is learned too (without a Put).
	if m := e.qStr(`SELECT media_file_id FROM attachment WHERE source_id = '20004'`); m != "3f1c9a52-7d4e-4b6a-9c2e-0a1b2c3d4e54" {
		t.Errorf("media_file_id of the pdf 20004 = %q, want it learned in the media phase", m)
	}
	if e.qInt(`SELECT file_path IS NULL FROM attachment WHERE source_id = '20004'`) != 1 {
		t.Errorf("the pdf was downloaded into the cache; non-images are metadata only")
	}

	ready := e.em.mediaReady()
	if len(ready) != images {
		t.Errorf("media:ready events = %d, want %d", len(ready), images)
	}
	for _, m := range ready {
		if m.Cached != "full" || m.AttachmentID == 0 || m.IssueID == 0 {
			t.Errorf("media:ready = %+v", m)
		}
	}
	names := e.em.names()
	fin := indexOf(names, EvSyncFinished)
	for i, n := range names {
		if (n == EvMediaReady) && i < fin {
			t.Errorf("media:ready came before sync:finished")
		}
	}
	prog := e.em.progress()
	last := prog[len(prog)-1]
	if last.Phase != PhaseMedia || last.Done != last.Total || last.Total < images {
		t.Errorf("last progress = %+v, want the media phase complete", last)
	}

	// Requests: two per fetched image and two for the pdf, all on the media lane, none counted in the run.
	reqs := e.requests()
	t.Logf("full sync with the media phase: %d sync requests + %d media requests (%d images + 1 media-id lookup, 2 requests each)",
		r.Requests, len(reqs)-int(r.Requests), images)
	if n, want := countPath(reqs, func(p string) bool { return strings.HasPrefix(p, "/rest/api/3/attachment/content/") }), images+1; n != want {
		t.Errorf("attachment content requests = %d, want %d", n, want)
	}
	if n, want := countPath(reqs, func(p string) bool { return strings.HasPrefix(p, "/media/file/") }), images+1; n != want {
		t.Errorf("media file requests = %d, want %d", n, want)
	}
	st := lim.Stats()
	if got, want := st.Requests[2]-before.Requests[2], int64(2*(images+1)); got != want {
		t.Errorf("media-lane permits = %d, want %d", got, want)
	}
	if int(r.Requests) != len(reqs)-2*(images+1) {
		t.Errorf("sync_run.requests = %d, want the %d sync requests without the %d media ones", r.Requests, len(reqs)-2*(images+1), 2*(images+1))
	}

	// A second full sync downloads nothing: every image has its thumbnail.
	e.srv.ResetRequests()
	e.em.reset()
	e.mustOK(e.sync(ModeFull))
	mediaDone(e)
	if n := countPath(e.requests(), func(p string) bool { return strings.HasPrefix(p, "/media/file/") }); n != 0 {
		t.Errorf("the second sync fetched %d media files, want 0", n)
	}
	if p, _, _ := store.counts(); p != images {
		t.Errorf("Put calls after the second sync = %d, want still %d", p, images)
	}
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func TestMedia_WithoutAMediaStoreThereIsNoMediaPhase(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	e.mustOK(e.sync(ModeFull))
	mediaDone(e)
	if n := countPath(e.requests(), mediaPaths); n != 0 {
		t.Errorf("media requests = %d without a MediaStore", n)
	}
	for _, p := range e.em.progress() {
		if p.Phase == PhaseMedia {
			t.Fatalf("progress for the media phase without a MediaStore: %+v", p)
		}
	}
	if len(e.em.mediaReady()) != 0 {
		t.Errorf("media:ready emitted without a MediaStore")
	}
}

func TestMedia_FailuresNeverFailTheSyncAndAreRecordedOnTheAttachment(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	store := newFakeMedia(e)
	f := e.failRequests("/rest/api/3/attachment/content/", http.StatusNotFound)

	r := e.mustOK(e.sync(ModeFull))
	mediaDone(e)
	if r.Status != StatusOK || len(r.Warnings) != 0 && contains(r.Warnings, "media") {
		t.Errorf("run = %+v", r)
	}
	if e.faultHits(f) == 0 {
		t.Fatalf("the fault never hit")
	}
	if p, _, ev := store.counts(); p != 0 || ev != 1 {
		t.Errorf("Put calls = %d, Evict calls = %d; want 0 puts and one eviction", p, ev)
	}
	if n := e.count("attachment", "is_image = 1 AND fetch_error = 'media_unavailable'"); n < 3 {
		t.Errorf("attachments with fetch_error media_unavailable = %d, want every image", n)
	}
	if len(e.em.mediaReady()) != 0 {
		t.Errorf("media:ready emitted for failed downloads")
	}
	// The sync's data is intact.
	if n := e.count("issue_content", ""); n == 0 {
		t.Errorf("no content stored")
	}
}

func TestMedia_AStoreFailureForOneImageDoesNotStopTheOthers(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	store := newFakeMedia(e)
	store.putErr["share-preview.png"] = apperr.New(apperr.MediaUnavailable, "boom")
	e.mustOK(e.sync(ModeFull))
	mediaDone(e)
	if n := e.count("attachment", "source_id = '20002' AND fetch_error = 'media_unavailable'"); n != 1 {
		t.Errorf("the failing image has no fetch_error")
	}
	if n := e.count("attachment", "source_id = '20001' AND thumb_path IS NOT NULL"); n != 1 {
		t.Errorf("another image did not land")
	}
	if _, _, ev := store.counts(); ev != 1 {
		t.Errorf("Evict calls = %d, want 1", ev)
	}
}

func TestMedia_AnImageTheStoreCannotDecodeFallsBackToTheProviderThumbnail(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	store := newFakeMedia(e)
	store.cached = "none"
	e.mustOK(e.sync(ModeFull))
	mediaDone(e)
	puts, thumbs, _ := store.counts()
	if puts == 0 || thumbs != puts {
		t.Errorf("puts = %d, thumb fallbacks = %d, want one thumbnail per put", puts, thumbs)
	}
	for _, m := range e.em.mediaReady() {
		if m.Cached != "thumb" {
			t.Errorf("media:ready cached = %q, want thumb", m.Cached)
		}
	}
	if n := countPath(e.requests(), func(p string) bool { return strings.HasPrefix(p, "/rest/api/3/attachment/thumbnail/") }); n != puts {
		t.Errorf("thumbnail requests = %d, want %d", n, puts)
	}
}

func TestMedia_ShutdownCancelsARunningMediaPhase(t *testing.T) {
	defer recoverStub(t)
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	store := newFakeMedia(e)
	g := e.gate(pathHasPrefix("/media/file/"))
	e.mustOK(e.sync(ModeFull)) // returns once the run is finished; the media phase is stuck at the gate
	g.waitReached(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := e.o.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v (the media phase did not stop)", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Shutdown took %v", d)
	}
	if p, _, ev := store.counts(); p != 0 || ev != 0 {
		t.Errorf("Put calls = %d, Evict calls = %d after a cancelled phase, want 0 and 0", p, ev)
	}
	g.Release()
	_ = sql.ErrNoRows
}

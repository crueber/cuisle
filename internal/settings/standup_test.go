package settings

// WP-16 tests: standup CRUD, archive/restore, move, Update validation, SetSpace, filter checks, Effective.
// Run only this WP's tests with:
//   go test -tags purego ./internal/settings -run 'TestStandup|TestEffective|TestAppSettings|TestSetAppSetting|TestUIState'  (WP-16 incl. app_settings/uistate)
//
// SPEC-NOTE: Every mutation bumps standup.rev by exactly 1 and returns it in Ack.Rev. Archive bumps BEFORE setting
// deleted_at and Restore clears deleted_at BEFORE bumping (BumpRevTx only matches live rows), so both bump too.
// SPEC-NOTE: DiscardDraft re-densifies the positions of the remaining standups (data-model §1: ordered lists are dense).
// SPEC-NOTE: Update only sets needs_sync when scope/extraFilter actually CHANGE value (product-spec §2.2: no
// unnecessary syncs). Saving the same value again leaves the flag alone. Ack.NeedsSync always reports the current flag.
// SPEC-NOTE: Standup.FilterCheckedAt is zero whenever extra_filter_ok is NULL (never checked, or the check was cleared by
// an extraFilter change); otherwise it is the time of the last SetFilterCheck. SetFilterCheck's effect on rev is not tested.
// SPEC-NOTE: CopySettingsFrom onto a kanban standup never leaves done_window='sprint' (the source's value becomes 14d),
// because done_window=sprint is invalid on kanban boards.
// SPEC-NOTE: SetConnectionAll also updates archived standups (so a Restore later has a connection).
// SPEC-NOTE: Duplicate copies the source (space, board, scope, extra filter) as well as settings, lanes, map, speakers.

import (
	"context"
	"database/sql"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

// ── helpers (prefix w16; WP-17 has its own w17 set so the WPs stay independent) ──

type w16Clock struct{ t time.Time }

func (c *w16Clock) Now() time.Time          { return c.t }
func (c *w16Clock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// A fixed non-UTC zone whose local date (2026-11-23) differs from the UTC date (2026-11-24).
var w16Zone = time.FixedZone("UTC-8", -8*3600)

func w16Start() time.Time { return time.Date(2026, 11, 23, 22, 30, 0, 0, w16Zone) }

type w16Env struct {
	t   *testing.T
	s   *Store
	db  *store.DB
	clk *w16Clock
	ctx context.Context
}

func newW16(t *testing.T) *w16Env {
	t.Helper()
	clk := &w16Clock{t: w16Start()}
	db := storetest.Open(t, clk.Now)
	return &w16Env{t: t, s: New(db, clk.Now), db: db, clk: clk, ctx: context.Background()}
}

func w16p[T any](v T) *T { return &v }

func (e *w16Env) exec(q string, args ...any) { e.t.Helper(); storetest.Exec(e.t, e.db, q, args...) }

func (e *w16Env) int(q string, args ...any) int64 {
	e.t.Helper()
	var n sql.NullInt64
	if err := e.db.W.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatalf("query %q: %v", q, err)
	}
	return n.Int64
}

func (e *w16Env) str(q string, args ...any) string {
	e.t.Helper()
	var n sql.NullString
	if err := e.db.W.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatalf("query %q: %v", q, err)
	}
	return n.String
}

func (e *w16Env) isNull(q string, args ...any) bool {
	e.t.Helper()
	var n sql.NullString
	if err := e.db.W.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatalf("query %q: %v", q, err)
	}
	return !n.Valid
}

func (e *w16Env) rev(id int64) int64 {
	e.t.Helper()
	return e.int(`SELECT rev FROM standup WHERE id = ?`, id)
}

func (e *w16Env) needsSync(id int64) bool {
	e.t.Helper()
	return e.int(`SELECT needs_sync FROM standup_sync_state WHERE standup_id = ?`, id) == 1
}

func (e *w16Env) setNeedsSync(id int64, v int) {
	e.t.Helper()
	e.exec(`UPDATE standup_sync_state SET needs_sync = ? WHERE standup_id = ?`, v, id)
}

func (e *w16Env) create() int64 {
	e.t.Helper()
	id, err := e.s.Create(e.ctx)
	if err != nil {
		e.t.Fatalf("Create: %v", err)
	}
	return id
}

func (e *w16Env) createN(n int) []int64 {
	e.t.Helper()
	ids := make([]int64, n)
	for i := range ids {
		ids[i] = e.create()
		e.clk.Advance(time.Minute)
	}
	return ids
}

func (e *w16Env) rename(id int64, name string) {
	e.t.Helper()
	e.exec(`UPDATE standup SET name = ? WHERE id = ?`, name, id)
}

func (e *w16Env) seedConnection(id int64) {
	e.t.Helper()
	e.exec(`INSERT INTO connection (id, source, base_url, account, created_at) VALUES (?, 'jira', ?, 'me@acme.com', 1000)`,
		id, "https://acme"+string(rune('a'+id))+".atlassian.net")
}

// setBoard gives the standup a space and board (scrum or kanban) directly in SQL.
func (e *w16Env) setBoard(id int64, boardType string) {
	e.t.Helper()
	scope := "sprint"
	if boardType == "kanban" {
		scope = "board"
	}
	e.exec(`UPDATE standup SET space_id='10001', space_key='MOB', space_name='Service Desk',
	        board_id='B1', board_name='MOB board', board_type=?, board_meta='{"filterId":9001}', scope=? WHERE id=?`,
		boardType, scope, id)
}

func (e *w16Env) seedMapping(id int64) {
	e.t.Helper()
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'To Do', 0, 'todo', 1)`, id)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'Doing', 1, 'prog', 0)`, id)
	e.exec(`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (?, '10', 'Open', 'To Do', 'todo')`, id)
	e.exec(`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (?, '11', 'Doing', 'Doing', 'prog')`, id)
}

func (e *w16Env) seedSpeakers(id int64) {
	e.t.Helper()
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually, user_touched, last_seen_at, created_at)
	        VALUES (?, 'acc-a', 'Ada', 0, 1, 0, 0, 5000, 1000)`, id)
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually, user_touched, last_seen_at, created_at)
	        VALUES (?, 'acc-b', 'Ben', 1, 0, 0, 1, 5000, 1000)`, id)
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually, user_touched, last_seen_at, created_at)
	        VALUES (?, 'acc-c', 'Cy', 2, 1, 1, 0, NULL, 1000)`, id)
}

func (e *w16Env) order(includeArchived bool) []int64 {
	e.t.Helper()
	list, err := e.s.List(e.ctx, includeArchived)
	if err != nil {
		e.t.Fatalf("List: %v", err)
	}
	ids := make([]int64, len(list))
	for i, st := range list {
		ids[i] = st.ID
	}
	return ids
}

func (e *w16Env) get(id int64) (Standup, Settings) {
	e.t.Helper()
	st, se, err := e.s.Get(e.ctx, id)
	if err != nil {
		e.t.Fatalf("Get(%d): %v", id, err)
	}
	return st, se
}

func (e *w16Env) assertAck(id, before int64, ack Ack) {
	e.t.Helper()
	if ack.Rev != before+1 {
		e.t.Errorf("Ack.Rev = %d, want %d (previous rev + 1)", ack.Rev, before+1)
	}
	if got := e.rev(id); got != ack.Rev {
		e.t.Errorf("stored rev = %d, Ack.Rev = %d; they must match", got, ack.Rev)
	}
	if !ack.SavedAt.Equal(e.clk.Now()) {
		e.t.Errorf("Ack.SavedAt = %v, want the clock's now %v", ack.SavedAt, e.clk.Now())
	}
}

func w16RequireValidation(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want validation error on %q, got nil", field)
	}
	if !apperr.Is(err, apperr.Validation) {
		t.Fatalf("want code validation, got %v", err)
	}
	if got := apperr.From(err).Field; got != field {
		t.Fatalf("validation Field = %q, want %q", got, field)
	}
}

func w16RequireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

type w16LaneRow struct {
	Lane     string
	Position int
	Limit    int
}

func (e *w16Env) lanes(id int64) []w16LaneRow {
	e.t.Helper()
	rows, err := e.db.W.QueryContext(e.ctx, `SELECT lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY walk_position`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []w16LaneRow
	for rows.Next() {
		var r w16LaneRow
		if err := rows.Scan(&r.Lane, &r.Position, &r.Limit); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// dump returns every row of a query as joined strings (for copy-equality assertions).
func (e *w16Env) dump(q string, args ...any) []string {
	e.t.Helper()
	rows, err := e.db.W.QueryContext(e.ctx, q, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			e.t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			if v.Valid {
				parts[i] = v.String
			} else {
				parts[i] = "<NULL>"
			}
		}
		out = append(out, strings.Join(parts, "|"))
	}
	return out
}

const w16LaneDump = `SELECT lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY lane`
const w16MapDump = `SELECT column_name, position, lane, auto FROM standup_column_map WHERE standup_id = ? ORDER BY column_name`
const w16StatusDump = `SELECT status_id, status_name, column_name, lane FROM standup_status_lane WHERE standup_id = ? ORDER BY status_id`
const w16SpeakerDump = `SELECT account_id, display_name, position, included, added_manually, user_touched FROM standup_speaker WHERE standup_id = ? ORDER BY position`

// ── Create / Get / List ──

func TestStandupCreate_Defaults(t *testing.T) {
	e := newW16(t)
	id := e.create()
	st, se := e.get(id)

	if st.ID != id || st.Name != "New standup" || st.Position != 0 {
		t.Errorf("standup = %+v, want id %d name 'New standup' position 0", st, id)
	}
	if !st.Draft() || st.BoardID != "" || st.SpaceID != "" || st.BoardType != "" {
		t.Errorf("a new standup must be a draft with no space/board: %+v", st)
	}
	if st.Scope != "sprint" || st.ExtraFilter != "" || st.ExtraFilterOK != nil || st.ExtraFilterError != "" {
		t.Errorf("scope/filter defaults wrong: %+v", st)
	}
	if st.Rev != 1 {
		t.Errorf("Rev = %d, want 1", st.Rev)
	}
	if !st.NeedsSync {
		t.Error("a new standup must have needs_sync=true")
	}
	if !st.DeletedAt.IsZero() || !st.FilterCheckedAt.IsZero() {
		t.Errorf("DeletedAt/FilterCheckedAt must be zero: %v %v", st.DeletedAt, st.FilterCheckedAt)
	}
	if !st.CreatedAt.Equal(e.clk.Now()) || !st.UpdatedAt.Equal(e.clk.Now()) {
		t.Errorf("CreatedAt/UpdatedAt = %v/%v, want %v", st.CreatedAt, st.UpdatedAt, e.clk.Now())
	}
	wantSettings := Settings{
		RowsMode: "fit", UnassignedPosition: "last", ReshuffleDaily: false, ShuffleSeed: 0, ShuffleDate: "",
		PulseDays: 21, ShowCommits: true, ShowPRs: true, ShowComments: true, ShadeWeekends: true,
		DoneWindow: "sprint", BacklogRows: 12,
	}
	if se != wantSettings {
		t.Errorf("settings = %+v, want %+v", se, wantSettings)
	}
	wantLanes := []w16LaneRow{{"qa", 0, 3}, {"review", 1, 2}, {"prog", 2, 5}, {"todo", 3, 10}}
	if got := e.lanes(id); !reflect.DeepEqual(got, wantLanes) {
		t.Errorf("lanes = %v, want %v (walk order qa, review, prog, todo; limits 3/2/5/10)", got, wantLanes)
	}
	if n := e.int(`SELECT count(*) FROM standup_sync_state WHERE standup_id = ? AND needs_sync = 1`, id); n != 1 {
		t.Errorf("standup_sync_state rows with needs_sync=1: %d, want 1", n)
	}
}

func TestStandupCreate_MatchesDefaultLanesVariable(t *testing.T) {
	e := newW16(t)
	id := e.create()
	got := e.lanes(id)
	if len(got) != len(DefaultLanes) {
		t.Fatalf("lanes = %v, DefaultLanes = %v", got, DefaultLanes)
	}
	for i, d := range DefaultLanes {
		if got[i].Lane != d.Lane || got[i].Position != d.Position || got[i].Limit != d.LimitDays {
			t.Errorf("lane %d = %+v, DefaultLanes has %+v", i, got[i], d)
		}
	}
}

func TestStandupCreate_PositionIsLastAndDense(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3)
	for i, id := range ids {
		st, _ := e.get(id)
		if st.Position != i {
			t.Errorf("standup %d position = %d, want %d", id, st.Position, i)
		}
	}
	// An archived standup does not occupy a slot: the next create goes to the end of the LIVE order.
	if _, err := e.s.Archive(e.ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	next := e.create()
	if st, _ := e.get(next); st.Position != 2 {
		t.Errorf("after archiving one of three, a new standup has position %d, want 2", st.Position)
	}
}

func TestStandupCreate_UsesTheSingleConnection(t *testing.T) {
	e := newW16(t)
	e.seedConnection(7)
	id := e.create()
	if st, _ := e.get(id); st.ConnectionID != 7 {
		t.Errorf("ConnectionID = %d, want 7 (the only connection)", st.ConnectionID)
	}
}

func TestStandupCreate_WithoutConnectionLeavesNone(t *testing.T) {
	e := newW16(t)
	id := e.create()
	if st, _ := e.get(id); st.ConnectionID != 0 {
		t.Errorf("ConnectionID = %d, want 0 (none)", st.ConnectionID)
	}
}

func TestStandupGet_UnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	_, _, err := e.s.Get(e.ctx, 999)
	w16RequireCode(t, err, apperr.NotFound)
}

func TestStandupGet_ReturnsArchivedStandup(t *testing.T) {
	e := newW16(t)
	id := e.create()
	if _, err := e.s.Archive(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	st, _ := e.get(id)
	if st.DeletedAt.IsZero() {
		t.Error("Get on an archived standup must return it with DeletedAt set")
	}
}

func TestStandupList_ExcludesArchivedUnlessAsked(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3)
	if _, err := e.s.Archive(e.ctx, ids[1]); err != nil {
		t.Fatal(err)
	}
	if got, want := e.order(false), []int64{ids[0], ids[2]}; !reflect.DeepEqual(got, want) {
		t.Errorf("List(false) = %v, want %v", got, want)
	}
	if got, want := e.order(true), []int64{ids[0], ids[2], ids[1]}; !reflect.DeepEqual(got, want) {
		t.Errorf("List(true) = %v, want live by position then archived at the end: %v", got, want)
	}
}

func TestStandupList_EmptyDatabase(t *testing.T) {
	e := newW16(t)
	if got := e.order(true); len(got) != 0 {
		t.Errorf("List on empty DB = %v, want none", got)
	}
}

// ── Duplicate ──

func TestStandupDuplicate_CopiesEverythingAndPlacesAfterOriginal(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3) // A, B, C
	a, b, c := ids[0], ids[1], ids[2]
	e.rename(a, "Alpha")
	e.setBoard(a, "scrum")
	e.exec(`UPDATE standup SET extra_filter = 'labels = x' WHERE id = ?`, a)
	e.exec(`UPDATE standup_settings SET rows_mode='roomy', unassigned_position='first', reshuffle_daily=1, pulse_days=14,
	        show_commits=0, show_prs=0, show_comments=0, shade_weekends=0, done_window='14d', backlog_rows=7 WHERE standup_id = ?`, a)
	e.exec(`UPDATE standup_lane SET walk_position = 0, limit_days = 4 WHERE standup_id = ? AND lane = 'prog'`, a)
	e.exec(`UPDATE standup_lane SET walk_position = 2, limit_days = 0 WHERE standup_id = ? AND lane = 'qa'`, a)
	e.seedMapping(a)
	e.seedSpeakers(a)
	e.setNeedsSync(a, 0)
	aRev := e.rev(a)

	e.clk.Advance(time.Hour)
	copyID, err := e.s.Duplicate(e.ctx, a)
	if err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	if copyID == a || copyID == 0 {
		t.Fatalf("Duplicate returned id %d", copyID)
	}
	cp, cpSettings := e.get(copyID)
	orig, origSettings := e.get(a)

	if cp.Name != "Alpha (copy)" {
		t.Errorf("name = %q, want 'Alpha (copy)'", cp.Name)
	}
	if got, want := e.order(false), []int64{a, copyID, b, c}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want the copy right after the original: %v", got, want)
	}
	for i, id := range e.order(false) {
		if st, _ := e.get(id); st.Position != i {
			t.Errorf("standup %d position = %d, want dense %d", id, st.Position, i)
		}
	}
	if !cp.NeedsSync {
		t.Error("the copy must have needs_sync=true")
	}
	if cp.Rev != 1 {
		t.Errorf("copy Rev = %d, want 1", cp.Rev)
	}
	if !cp.DeletedAt.IsZero() {
		t.Error("the copy must be live")
	}
	if !cp.CreatedAt.Equal(e.clk.Now()) {
		t.Errorf("copy CreatedAt = %v, want now %v", cp.CreatedAt, e.clk.Now())
	}
	// source
	if cp.SpaceID != "10001" || cp.SpaceKey != "MOB" || cp.SpaceName != "Service Desk" || cp.BoardID != "B1" ||
		cp.BoardName != "MOB board" || cp.BoardType != "scrum" || cp.Scope != "sprint" || cp.ExtraFilter != "labels = x" {
		t.Errorf("source fields not copied: %+v", cp)
	}
	if !reflect.DeepEqual(cp.BoardMeta, orig.BoardMeta) || cp.BoardMeta["filterId"] != float64(9001) {
		t.Errorf("BoardMeta = %v, want %v", cp.BoardMeta, orig.BoardMeta)
	}
	// settings (shuffle seed/date are per-standup state, not compared)
	cpSettings.ShuffleSeed, cpSettings.ShuffleDate = 0, ""
	origSettings.ShuffleSeed, origSettings.ShuffleDate = 0, ""
	if cpSettings != origSettings {
		t.Errorf("settings = %+v, want %+v", cpSettings, origSettings)
	}
	// lanes, map, status lanes, speakers
	for _, q := range []string{w16LaneDump, w16MapDump, w16StatusDump, w16SpeakerDump} {
		want, got := e.dump(q, a), e.dump(q, copyID)
		if len(want) == 0 {
			t.Fatalf("test seed produced no rows for %q", q)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("copied rows differ for %q:\n got %v\nwant %v", q, got, want)
		}
	}
	// the original is untouched
	if e.rev(a) != aRev || e.needsSync(a) {
		t.Errorf("Duplicate must not modify the original (rev %d→%d, needs_sync %v)", aRev, e.rev(a), e.needsSync(a))
	}
}

func TestStandupDuplicate_UnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	_, err := e.s.Duplicate(e.ctx, 999)
	w16RequireCode(t, err, apperr.NotFound)
}

func TestStandupDuplicate_OfLastStandupAppendsAtEnd(t *testing.T) {
	e := newW16(t)
	ids := e.createN(2)
	copyID, err := e.s.Duplicate(e.ctx, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if got, want := e.order(false), []int64{ids[0], ids[1], copyID}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// ── CopySettingsFrom ──

func w16SeedCustom(e *w16Env, id int64) {
	e.exec(`UPDATE standup_settings SET rows_mode='roomy', unassigned_position='skip', reshuffle_daily=1, pulse_days=7,
	        show_commits=0, show_prs=0, show_comments=0, shade_weekends=0, done_window='yesterday', backlog_rows=3 WHERE standup_id = ?`, id)
	e.exec(`UPDATE standup_lane SET walk_position = 0, limit_days = 0  WHERE standup_id = ? AND lane = 'todo'`, id)
	e.exec(`UPDATE standup_lane SET walk_position = 1, limit_days = 9  WHERE standup_id = ? AND lane = 'prog'`, id)
	e.exec(`UPDATE standup_lane SET walk_position = 2, limit_days = 99 WHERE standup_id = ? AND lane = 'review'`, id)
	e.exec(`UPDATE standup_lane SET walk_position = 3, limit_days = 1  WHERE standup_id = ? AND lane = 'qa'`, id)
}

func TestStandupCopySettingsFrom_CopiesSettingsLanesAndLimitsOnly(t *testing.T) {
	e := newW16(t)
	ids := e.createN(2)
	target, from := ids[0], ids[1]
	e.rename(from, "Source")
	e.setBoard(from, "scrum")
	e.exec(`UPDATE standup SET extra_filter = 'labels = from', scope = 'board' WHERE id = ?`, from)
	w16SeedCustom(e, from)
	e.seedMapping(from)
	e.seedSpeakers(from)

	e.exec(`UPDATE standup SET space_id='20002', space_key='PLAT', space_name='Ops', board_id='B2', board_name='Ops board',
	        board_type='scrum', extra_filter='labels = target', name='Target' WHERE id = ?`, target)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'Backlog', 0, 'backlog', 1)`, target)
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, created_at) VALUES (?, 'zed', 'Zed', 0, 1000)`, target)
	e.setNeedsSync(target, 0)

	targetBefore, _ := e.get(target)
	mapBefore := e.dump(w16MapDump, target)
	speakersBefore := e.dump(w16SpeakerDump, target)
	fromLanes := e.dump(w16LaneDump, from)
	fromBefore, fromSettings := e.get(from)
	before := e.rev(target)

	e.clk.Advance(time.Minute)
	ack, err := e.s.CopySettingsFrom(e.ctx, target, from)
	if err != nil {
		t.Fatalf("CopySettingsFrom: %v", err)
	}
	e.assertAck(target, before, ack)
	if ack.NeedsSync {
		t.Error("copying view settings changes no source: Ack.NeedsSync must stay false")
	}

	got, gotSettings := e.get(target)
	fromSettings.ShuffleSeed, fromSettings.ShuffleDate = 0, ""
	gotSettings.ShuffleSeed, gotSettings.ShuffleDate = 0, ""
	if gotSettings != fromSettings {
		t.Errorf("settings = %+v, want the source's %+v", gotSettings, fromSettings)
	}
	if lanes := e.dump(w16LaneDump, target); !reflect.DeepEqual(lanes, fromLanes) {
		t.Errorf("lanes = %v, want the source's %v", lanes, fromLanes)
	}
	// source fields untouched
	if got.Name != "Target" || got.SpaceID != "20002" || got.BoardID != "B2" || got.BoardName != "Ops board" ||
		got.ExtraFilter != "labels = target" || got.Scope != targetBefore.Scope {
		t.Errorf("source fields of the target changed: %+v", got)
	}
	if !reflect.DeepEqual(e.dump(w16MapDump, target), mapBefore) {
		t.Error("the target's column map must not change")
	}
	if !reflect.DeepEqual(e.dump(w16SpeakerDump, target), speakersBefore) {
		t.Error("the target's speakers must not change")
	}
	// the source standup is untouched
	if after, _ := e.get(from); after.Rev != fromBefore.Rev {
		t.Errorf("source rev changed %d → %d", fromBefore.Rev, after.Rev)
	}
}

func TestStandupCopySettingsFrom_KanbanTargetNeverGetsSprintDoneWindow(t *testing.T) {
	e := newW16(t)
	ids := e.createN(2)
	target, from := ids[0], ids[1]
	e.setBoard(target, "kanban")
	e.exec(`UPDATE standup_settings SET done_window='14d' WHERE standup_id = ?`, target)
	e.exec(`UPDATE standup_settings SET done_window='sprint', pulse_days=28 WHERE standup_id = ?`, from)

	if _, err := e.s.CopySettingsFrom(e.ctx, target, from); err != nil {
		t.Fatal(err)
	}
	_, se := e.get(target)
	if se.PulseDays != 28 {
		t.Errorf("pulse_days = %d, want the copied 28", se.PulseDays)
	}
	if se.DoneWindow != "14d" {
		t.Errorf("done_window = %q, want '14d' (sprint is invalid on kanban)", se.DoneWindow)
	}
}

func TestStandupCopySettingsFrom_UnknownIDsAreNotFound(t *testing.T) {
	e := newW16(t)
	id := e.create()
	_, err := e.s.CopySettingsFrom(e.ctx, id, 999)
	w16RequireCode(t, err, apperr.NotFound)
	_, err = e.s.CopySettingsFrom(e.ctx, 999, id)
	w16RequireCode(t, err, apperr.NotFound)
}

// ── DiscardDraft ──

func TestStandupDiscardDraft_DeletesDraftWithNoSpace(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3)
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, created_at) VALUES (?, 'x', 'X', 0, 1)`, ids[1])
	if err := e.s.DiscardDraft(e.ctx, ids[1]); err != nil {
		t.Fatalf("DiscardDraft: %v", err)
	}
	if n := e.int(`SELECT count(*) FROM standup WHERE id = ?`, ids[1]); n != 0 {
		t.Error("the draft row must be hard-deleted")
	}
	for _, tbl := range []string{"standup_settings", "standup_lane", "standup_sync_state", "standup_speaker"} {
		if n := e.int(`SELECT count(*) FROM `+tbl+` WHERE standup_id = ?`, ids[1]); n != 0 {
			t.Errorf("%s still has %d rows for the discarded draft", tbl, n)
		}
	}
	if got, want := e.order(true), []int64{ids[0], ids[2]}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	for i, id := range e.order(false) {
		if st, _ := e.get(id); st.Position != i {
			t.Errorf("standup %d position = %d, want dense %d", id, st.Position, i)
		}
	}
}

func TestStandupDiscardDraft_IsANoOpOnceASpaceIsChosen(t *testing.T) {
	e := newW16(t)
	id := e.create()
	e.exec(`UPDATE standup SET space_id='10001', space_key='MOB', space_name='Service Desk' WHERE id = ?`, id)
	rev := e.rev(id)
	if err := e.s.DiscardDraft(e.ctx, id); err != nil {
		t.Fatalf("DiscardDraft must not error on a non-draft: %v", err)
	}
	if n := e.int(`SELECT count(*) FROM standup WHERE id = ?`, id); n != 1 {
		t.Error("a standup with a space chosen must survive DiscardDraft")
	}
	if e.rev(id) != rev {
		t.Error("a no-op DiscardDraft must not bump rev")
	}
}

func TestStandupDiscardDraft_IsANoOpWithABoard(t *testing.T) {
	e := newW16(t)
	id := e.create()
	e.setBoard(id, "scrum")
	if err := e.s.DiscardDraft(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	if n := e.int(`SELECT count(*) FROM standup WHERE id = ?`, id); n != 1 {
		t.Error("a standup with a board must survive DiscardDraft")
	}
}

// ── Archive / Restore (no purge) ──

func w16SeedRecords(e *w16Env, standupID int64) {
	e.exec(`INSERT OR IGNORE INTO connection (id, source, base_url, account, created_at) VALUES (1, 'jira', 'https://acme.atlassian.net', 'me@acme.com', 1000)`)
	e.exec(`INSERT INTO issue (id, connection_id, source_id, key, space_id, summary, issue_type_id, status_id, created_at, updated_at, hydrated_at, last_seen_at)
	        VALUES (500, 1, '9001', 'MOB-1', '10001', 'Fix it', '1', '10', 1000, 2000, 2000, 2000)`)
	e.exec(`INSERT INTO standup_issue (standup_id, issue_id, role, rank_index) VALUES (?, 500, 'board', 0)`, standupID)
	e.exec(`INSERT INTO standup_issue_span (standup_id, issue_id, role, valid_from, valid_to) VALUES (?, 500, 'board', 1000, 2000)`, standupID)
	e.exec(`INSERT INTO standup_issue_span (standup_id, issue_id, role, valid_from, valid_to) VALUES (?, 500, 'board', 3000, NULL)`, standupID)
	e.exec(`INSERT INTO standup_config_history (standup_id, valid_from, valid_to, fingerprint, config_json) VALUES (?, 1000, 3000, 'f1', '{}')`, standupID)
	e.exec(`INSERT INTO standup_config_history (standup_id, valid_from, valid_to, fingerprint, config_json) VALUES (?, 3000, NULL, 'f2', '{}')`, standupID)
}

func TestStandupArchive_SetsDeletedAtKeepsRecordsAndDensifiesPositions(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3)
	a, b, c := ids[0], ids[1], ids[2]
	w16SeedRecords(e, b)
	e.seedSpeakers(b)
	e.seedMapping(b)
	before := e.rev(b)

	ack, err := e.s.Archive(e.ctx, b)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	e.assertAck(b, before, ack)

	st, _ := e.get(b)
	if !st.DeletedAt.Equal(e.clk.Now()) {
		t.Errorf("DeletedAt = %v, want %v", st.DeletedAt, e.clk.Now())
	}
	if got, want := e.order(false), []int64{a, c}; !reflect.DeepEqual(got, want) {
		t.Errorf("live order = %v, want %v", got, want)
	}
	for i, id := range e.order(false) {
		if got, _ := e.get(id); got.Position != i {
			t.Errorf("live standup %d position = %d, want dense %d", id, got.Position, i)
		}
	}
	// no purge: everything the standup recorded or configured survives
	for _, tc := range []struct {
		table string
		want  int64
	}{
		{"standup_issue_span", 2}, {"standup_config_history", 2}, {"standup_issue", 1}, {"standup_speaker", 3},
		{"standup_column_map", 2}, {"standup_status_lane", 2}, {"standup_settings", 1}, {"standup_lane", 4}, {"standup_sync_state", 1},
	} {
		if got := e.int(`SELECT count(*) FROM `+tc.table+` WHERE standup_id = ?`, b); got != tc.want {
			t.Errorf("%s rows after Archive = %d, want %d (history is never purged)", tc.table, got, tc.want)
		}
	}
	if n := e.int(`SELECT count(*) FROM issue WHERE id = 500`); n != 1 {
		t.Error("issue rows must survive Archive")
	}
}

func TestStandupArchive_AlreadyArchivedOrUnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	id := e.create()
	if _, err := e.s.Archive(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.Archive(e.ctx, id)
	w16RequireCode(t, err, apperr.NotFound)
	_, err = e.s.Archive(e.ctx, 999)
	w16RequireCode(t, err, apperr.NotFound)
}

func TestStandupRestore_AppendsAtEndOfLiveOrderAndKeepsRecords(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3)
	a, b, c := ids[0], ids[1], ids[2]
	w16SeedRecords(e, b)
	if _, err := e.s.Archive(e.ctx, b); err != nil {
		t.Fatal(err)
	}
	e.clk.Advance(time.Hour)
	before := e.rev(b)

	ack, err := e.s.Restore(e.ctx, b)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	e.assertAck(b, before, ack)

	st, _ := e.get(b)
	if !st.DeletedAt.IsZero() {
		t.Error("Restore must clear DeletedAt")
	}
	if got, want := e.order(false), []int64{a, c, b}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want the restored standup at the end: %v", got, want)
	}
	for i, id := range e.order(false) {
		if got, _ := e.get(id); got.Position != i {
			t.Errorf("standup %d position = %d, want dense %d", id, got.Position, i)
		}
	}
	if n := e.int(`SELECT count(*) FROM standup_issue_span WHERE standup_id = ?`, b); n != 2 {
		t.Errorf("spans = %d, want 2", n)
	}
	if n := e.int(`SELECT count(*) FROM standup_config_history WHERE standup_id = ?`, b); n != 2 {
		t.Errorf("config history rows = %d, want 2", n)
	}
	// once restored the standup is editable again
	if _, err := e.s.Update(e.ctx, b, Patch{PulseDays: w16p(14)}); err != nil {
		t.Errorf("Update after Restore: %v", err)
	}
}

func TestStandupRestore_UnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	_, err := e.s.Restore(e.ctx, 999)
	w16RequireCode(t, err, apperr.NotFound)
}

func TestStandupArchive_MutatingAnArchivedStandupIsNotFound(t *testing.T) {
	e := newW16(t)
	id := e.create()
	if _, err := e.s.Archive(e.ctx, id); err != nil {
		t.Fatal(err)
	}
	_, err := e.s.Update(e.ctx, id, Patch{Name: w16p("x")})
	w16RequireCode(t, err, apperr.NotFound)
	_, err = e.s.Move(e.ctx, id, 0)
	w16RequireCode(t, err, apperr.NotFound)
}

// ── Move ──

func TestStandupMove_RewritesDensePositions(t *testing.T) {
	cases := []struct {
		name  string
		which int // index into the created ids
		to    int
		want  []int // expected order as indexes into ids
	}{
		{"first_to_third", 0, 2, []int{1, 2, 0, 3}},
		{"last_to_first", 3, 0, []int{3, 0, 1, 2}},
		{"clamped_high", 1, 99, []int{0, 2, 3, 1}},
		{"clamped_low", 2, -3, []int{2, 0, 1, 3}},
		{"one_step_down", 1, 2, []int{0, 2, 1, 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW16(t)
			ids := e.createN(4)
			before := e.rev(ids[tc.which])
			ack, err := e.s.Move(e.ctx, ids[tc.which], tc.to)
			if err != nil {
				t.Fatalf("Move: %v", err)
			}
			e.assertAck(ids[tc.which], before, ack)
			var want []int64
			for _, i := range tc.want {
				want = append(want, ids[i])
			}
			if got := e.order(false); !reflect.DeepEqual(got, want) {
				t.Errorf("order = %v, want %v", got, want)
			}
			for i, id := range want {
				if st, _ := e.get(id); st.Position != i {
					t.Errorf("standup %d position = %d, want dense %d", id, st.Position, i)
				}
			}
		})
	}
}

func TestStandupMove_IgnoresArchivedStandups(t *testing.T) {
	e := newW16(t)
	ids := e.createN(4)
	if _, err := e.s.Archive(e.ctx, ids[3]); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Move(e.ctx, ids[0], 99); err != nil {
		t.Fatal(err)
	}
	if got, want := e.order(false), []int64{ids[1], ids[2], ids[0]}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	for i, id := range e.order(false) {
		if st, _ := e.get(id); st.Position != i {
			t.Errorf("standup %d position = %d, want dense %d", id, st.Position, i)
		}
	}
}

func TestStandupMove_UnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	_, err := e.s.Move(e.ctx, 999, 0)
	w16RequireCode(t, err, apperr.NotFound)
}

// ── Update ──

func TestStandupUpdate_RejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name  string
		patch Patch
		field string
	}{
		{"name_empty", Patch{Name: w16p("")}, "name"},
		{"name_whitespace_only", Patch{Name: w16p("   \t ")}, "name"},
		{"name_81_chars", Patch{Name: w16p(strings.Repeat("x", 81))}, "name"},
		{"name_81_runes", Patch{Name: w16p(strings.Repeat("é", 81))}, "name"},
		{"scope_unknown", Patch{Scope: w16p("everything")}, "scope"},
		{"scope_empty", Patch{Scope: w16p("")}, "scope"},
		{"extra_filter_order_by", Patch{ExtraFilter: w16p("labels = x ORDER BY created")}, "extraFilter"},
		{"extra_filter_order_by_lowercase", Patch{ExtraFilter: w16p("labels = x order by rank desc")}, "extraFilter"},
		{"extra_filter_order_by_odd_whitespace", Patch{ExtraFilter: w16p("labels = x\nORDER \t BY rank")}, "extraFilter"},
		{"rows_mode_unknown", Patch{RowsMode: w16p("tight")}, "rowsMode"},
		{"unassigned_unknown", Patch{UnassignedPosition: w16p("middle")}, "unassignedPosition"},
		{"pulse_0", Patch{PulseDays: w16p(0)}, "pulseDays"},
		{"pulse_6", Patch{PulseDays: w16p(6)}, "pulseDays"},
		{"pulse_10", Patch{PulseDays: w16p(10)}, "pulseDays"},
		{"pulse_29", Patch{PulseDays: w16p(29)}, "pulseDays"},
		{"pulse_negative", Patch{PulseDays: w16p(-7)}, "pulseDays"},
		{"done_window_unknown", Patch{DoneWindow: w16p("week")}, "doneWindow"},
		{"backlog_rows_negative", Patch{BacklogRows: w16p(-1)}, "backlogRows"},
		{"backlog_rows_51", Patch{BacklogRows: w16p(51)}, "backlogRows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW16(t)
			id := e.create()
			e.clk.Advance(time.Minute)
			before := e.rev(id)
			_, err := e.s.Update(e.ctx, id, tc.patch)
			w16RequireValidation(t, err, tc.field)
			if e.rev(id) != before {
				t.Error("a rejected patch must not bump rev")
			}
		})
	}
}

func TestStandupUpdate_AcceptsEveryValidValue(t *testing.T) {
	type check func(Standup, Settings) bool
	cases := []struct {
		name  string
		patch Patch
		ok    check
	}{
		{"name_1_char", Patch{Name: w16p("A")}, func(s Standup, _ Settings) bool { return s.Name == "A" }},
		{"name_80_chars", Patch{Name: w16p(strings.Repeat("x", 80))}, func(s Standup, _ Settings) bool { return s.Name == strings.Repeat("x", 80) }},
		{"name_80_multibyte_runes", Patch{Name: w16p(strings.Repeat("é", 80))}, func(s Standup, _ Settings) bool { return s.Name == strings.Repeat("é", 80) }},
		{"name_trimmed", Patch{Name: w16p("  Platform daily \t")}, func(s Standup, _ Settings) bool { return s.Name == "Platform daily" }},
		{"name_80_after_trim", Patch{Name: w16p("  " + strings.Repeat("x", 80) + "  ")}, func(s Standup, _ Settings) bool { return s.Name == strings.Repeat("x", 80) }},
		{"scope_board", Patch{Scope: w16p("board")}, func(s Standup, _ Settings) bool { return s.Scope == "board" }},
		{"scope_sprint", Patch{Scope: w16p("sprint")}, func(s Standup, _ Settings) bool { return s.Scope == "sprint" }},
		{"extra_filter_plain", Patch{ExtraFilter: w16p("labels = standup AND assignee is not EMPTY")}, func(s Standup, _ Settings) bool { return s.ExtraFilter == "labels = standup AND assignee is not EMPTY" }},
		{"extra_filter_word_order_is_fine", Patch{ExtraFilter: w16p(`summary ~ "order"`)}, func(s Standup, _ Settings) bool { return s.ExtraFilter == `summary ~ "order"` }},
		{"extra_filter_cleared", Patch{ExtraFilter: w16p("")}, func(s Standup, _ Settings) bool { return s.ExtraFilter == "" }},
		{"rows_roomy", Patch{RowsMode: w16p("roomy")}, func(_ Standup, s Settings) bool { return s.RowsMode == "roomy" }},
		{"rows_fit", Patch{RowsMode: w16p("fit")}, func(_ Standup, s Settings) bool { return s.RowsMode == "fit" }},
		{"unassigned_first", Patch{UnassignedPosition: w16p("first")}, func(_ Standup, s Settings) bool { return s.UnassignedPosition == "first" }},
		{"unassigned_last", Patch{UnassignedPosition: w16p("last")}, func(_ Standup, s Settings) bool { return s.UnassignedPosition == "last" }},
		{"unassigned_skip", Patch{UnassignedPosition: w16p("skip")}, func(_ Standup, s Settings) bool { return s.UnassignedPosition == "skip" }},
		{"reshuffle_on", Patch{ReshuffleDaily: w16p(true)}, func(_ Standup, s Settings) bool { return s.ReshuffleDaily }},
		{"pulse_7", Patch{PulseDays: w16p(7)}, func(_ Standup, s Settings) bool { return s.PulseDays == 7 }},
		{"pulse_14", Patch{PulseDays: w16p(14)}, func(_ Standup, s Settings) bool { return s.PulseDays == 14 }},
		{"pulse_21", Patch{PulseDays: w16p(21)}, func(_ Standup, s Settings) bool { return s.PulseDays == 21 }},
		{"pulse_28", Patch{PulseDays: w16p(28)}, func(_ Standup, s Settings) bool { return s.PulseDays == 28 }},
		{"commits_off", Patch{ShowCommits: w16p(false)}, func(_ Standup, s Settings) bool { return !s.ShowCommits && s.ShowPRs && s.ShowComments }},
		{"prs_off", Patch{ShowPRs: w16p(false)}, func(_ Standup, s Settings) bool { return s.ShowCommits && !s.ShowPRs && s.ShowComments }},
		{"comments_off", Patch{ShowComments: w16p(false)}, func(_ Standup, s Settings) bool { return s.ShowCommits && s.ShowPRs && !s.ShowComments }},
		{"weekends_off", Patch{ShadeWeekends: w16p(false)}, func(_ Standup, s Settings) bool { return !s.ShadeWeekends }},
		{"done_14d", Patch{DoneWindow: w16p("14d")}, func(_ Standup, s Settings) bool { return s.DoneWindow == "14d" }},
		{"done_yesterday", Patch{DoneWindow: w16p("yesterday")}, func(_ Standup, s Settings) bool { return s.DoneWindow == "yesterday" }},
		{"done_sprint", Patch{DoneWindow: w16p("sprint")}, func(_ Standup, s Settings) bool { return s.DoneWindow == "sprint" }},
		{"backlog_0", Patch{BacklogRows: w16p(0)}, func(_ Standup, s Settings) bool { return s.BacklogRows == 0 }},
		{"backlog_50", Patch{BacklogRows: w16p(50)}, func(_ Standup, s Settings) bool { return s.BacklogRows == 50 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW16(t)
			id := e.create()
			e.clk.Advance(time.Minute)
			before := e.rev(id)
			ack, err := e.s.Update(e.ctx, id, tc.patch)
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			e.assertAck(id, before, ack)
			st, se := e.get(id)
			if !tc.ok(st, se) {
				t.Errorf("patch not applied: standup %+v settings %+v", st, se)
			}
			if !st.UpdatedAt.Equal(e.clk.Now()) {
				t.Errorf("UpdatedAt = %v, want %v", st.UpdatedAt, e.clk.Now())
			}
		})
	}
}

func TestStandupUpdate_BooleansToggleBackAndForth(t *testing.T) {
	e := newW16(t)
	id := e.create()
	for _, v := range []bool{false, true, false} {
		_, err := e.s.Update(e.ctx, id, Patch{ReshuffleDaily: w16p(v), ShowCommits: w16p(v), ShowPRs: w16p(v), ShowComments: w16p(v), ShadeWeekends: w16p(v)})
		if err != nil {
			t.Fatal(err)
		}
		_, se := e.get(id)
		if se.ReshuffleDaily != v || se.ShowCommits != v || se.ShowPRs != v || se.ShowComments != v || se.ShadeWeekends != v {
			t.Errorf("after setting all booleans to %v: %+v", v, se)
		}
	}
}

func TestStandupUpdate_MultiFieldPatchAppliesAllAtOnce(t *testing.T) {
	e := newW16(t)
	id := e.create()
	before := e.rev(id)
	ack, err := e.s.Update(e.ctx, id, Patch{Name: w16p("Both"), PulseDays: w16p(28), RowsMode: w16p("roomy"), Scope: w16p("board")})
	if err != nil {
		t.Fatal(err)
	}
	if ack.Rev != before+1 {
		t.Errorf("a multi-field patch is one mutation: Ack.Rev = %d, want %d", ack.Rev, before+1)
	}
	st, se := e.get(id)
	if st.Name != "Both" || se.PulseDays != 28 || se.RowsMode != "roomy" || st.Scope != "board" {
		t.Errorf("not all fields applied: %+v %+v", st, se)
	}
}

func TestStandupUpdate_InvalidFieldInPatchChangesNothing(t *testing.T) {
	e := newW16(t)
	id := e.create()
	beforeSt, beforeSe := e.get(id)
	_, err := e.s.Update(e.ctx, id, Patch{Name: w16p("Renamed"), PulseDays: w16p(7), BacklogRows: w16p(99)})
	w16RequireValidation(t, err, "backlogRows")
	st, se := e.get(id)
	if st.Name != beforeSt.Name || se.PulseDays != beforeSe.PulseDays || st.Rev != beforeSt.Rev {
		t.Errorf("a patch with one invalid field must apply nothing: %+v %+v", st, se)
	}
}

func TestStandupUpdate_KanbanRejectsSprintScopeAndSprintDoneWindow(t *testing.T) {
	e := newW16(t)
	id := e.create()
	e.setBoard(id, "kanban")
	e.exec(`UPDATE standup_settings SET done_window = '14d' WHERE standup_id = ?`, id)

	_, err := e.s.Update(e.ctx, id, Patch{Scope: w16p("sprint")})
	w16RequireValidation(t, err, "scope")
	_, err = e.s.Update(e.ctx, id, Patch{DoneWindow: w16p("sprint")})
	w16RequireValidation(t, err, "doneWindow")

	for _, p := range []Patch{{Scope: w16p("board")}, {DoneWindow: w16p("14d")}, {DoneWindow: w16p("yesterday")}} {
		if _, err := e.s.Update(e.ctx, id, p); err != nil {
			t.Errorf("kanban must accept %+v: %v", p, err)
		}
	}
	st, se := e.get(id)
	if st.Scope != "board" || se.DoneWindow != "yesterday" {
		t.Errorf("state after valid kanban updates: %+v %+v", st, se)
	}
}

func TestStandupUpdate_ScrumAcceptsSprintScopeAndSprintDoneWindow(t *testing.T) {
	e := newW16(t)
	id := e.create()
	e.setBoard(id, "scrum")
	if _, err := e.s.Update(e.ctx, id, Patch{Scope: w16p("board")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Update(e.ctx, id, Patch{Scope: w16p("sprint"), DoneWindow: w16p("sprint")}); err != nil {
		t.Errorf("scrum must accept sprint scope and sprint done window: %v", err)
	}
}

func TestStandupUpdate_NeedsSyncOnlyForSourceChanges(t *testing.T) {
	cases := []struct {
		name      string
		patch     Patch
		wantFlag  bool
		setupFlag int
	}{
		{"scope_changes", Patch{Scope: w16p("board")}, true, 0},
		{"extra_filter_changes", Patch{ExtraFilter: w16p("labels = x")}, true, 0},
		{"scope_unchanged_value", Patch{Scope: w16p("sprint")}, false, 0},
		{"extra_filter_unchanged_value", Patch{ExtraFilter: w16p("")}, false, 0},
		{"name", Patch{Name: w16p("Renamed")}, false, 0},
		{"pulse_days", Patch{PulseDays: w16p(7)}, false, 0},
		{"rows_mode", Patch{RowsMode: w16p("roomy")}, false, 0},
		{"unassigned_position", Patch{UnassignedPosition: w16p("skip")}, false, 0},
		{"reshuffle", Patch{ReshuffleDaily: w16p(true)}, false, 0},
		{"marks_and_weekends", Patch{ShowCommits: w16p(false), ShowPRs: w16p(false), ShowComments: w16p(false), ShadeWeekends: w16p(false)}, false, 0},
		{"done_window", Patch{DoneWindow: w16p("yesterday")}, false, 0},
		{"backlog_rows", Patch{BacklogRows: w16p(3)}, false, 0},
		{"flag_already_set_is_reported", Patch{PulseDays: w16p(7)}, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW16(t)
			id := e.create()
			e.setNeedsSync(id, tc.setupFlag)
			ack, err := e.s.Update(e.ctx, id, tc.patch)
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			if ack.NeedsSync != tc.wantFlag {
				t.Errorf("Ack.NeedsSync = %v, want %v", ack.NeedsSync, tc.wantFlag)
			}
			if got := e.needsSync(id); got != tc.wantFlag {
				t.Errorf("stored needs_sync = %v, want %v", got, tc.wantFlag)
			}
		})
	}
}

func TestStandupUpdate_SourceChangeClearsTheFilterCheck(t *testing.T) {
	e := newW16(t)
	id := e.create()
	if _, err := e.s.Update(e.ctx, id, Patch{ExtraFilter: w16p("labels = a")}); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetFilterCheck(e.ctx, id, false, []string{"Field 'labels' does not exist"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.get(id); st.ExtraFilterOK == nil || *st.ExtraFilterOK {
		t.Fatalf("setup: filter check not stored: %+v", st)
	}
	if _, err := e.s.Update(e.ctx, id, Patch{ExtraFilter: w16p("labels = b")}); err != nil {
		t.Fatal(err)
	}
	st, _ := e.get(id)
	if st.ExtraFilterOK != nil || st.ExtraFilterError != "" || !st.FilterCheckedAt.IsZero() {
		t.Errorf("changing extraFilter must clear the check: ok=%v err=%q at=%v", st.ExtraFilterOK, st.ExtraFilterError, st.FilterCheckedAt)
	}
}

func TestStandupUpdate_ViewOnlyChangeKeepsTheFilterCheck(t *testing.T) {
	e := newW16(t)
	id := e.create()
	if err := e.s.SetFilterCheck(e.ctx, id, true, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Update(e.ctx, id, Patch{PulseDays: w16p(14)}); err != nil {
		t.Fatal(err)
	}
	st, _ := e.get(id)
	if st.ExtraFilterOK == nil || !*st.ExtraFilterOK {
		t.Errorf("a view-only change must keep the filter check: %+v", st.ExtraFilterOK)
	}
}

func TestStandupUpdate_UnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	_, err := e.s.Update(e.ctx, 999, Patch{Name: w16p("x")})
	w16RequireCode(t, err, apperr.NotFound)
}

func TestStandupUpdate_NoOtherStandupIsAffected(t *testing.T) {
	e := newW16(t)
	ids := e.createN(2)
	otherBefore, otherSe := e.get(ids[1])
	if _, err := e.s.Update(e.ctx, ids[0], Patch{Name: w16p("Changed"), PulseDays: w16p(28)}); err != nil {
		t.Fatal(err)
	}
	other, se := e.get(ids[1])
	if other.Rev != otherBefore.Rev || other.Name != otherBefore.Name || se != otherSe {
		t.Errorf("another standup changed: %+v %+v", other, se)
	}
}

// ── SetSpace ──

func TestStandupSetSpace_ClearsBoardAndMapping(t *testing.T) {
	e := newW16(t)
	id := e.create()
	e.setBoard(id, "kanban")
	e.seedMapping(id)
	e.exec(`UPDATE standup_settings SET pulse_days = 14 WHERE standup_id = ?`, id)
	e.setNeedsSync(id, 0)
	before := e.rev(id)

	e.clk.Advance(time.Minute)
	ack, err := e.s.SetSpace(e.ctx, id, model.Space{ID: "20002", Key: "PLAT", Name: "Operations"})
	if err != nil {
		t.Fatalf("SetSpace: %v", err)
	}
	e.assertAck(id, before, ack)
	if !ack.NeedsSync {
		t.Error("changing the space is a source change: Ack.NeedsSync must be true")
	}
	st, se := e.get(id)
	if st.SpaceID != "20002" || st.SpaceKey != "PLAT" || st.SpaceName != "Operations" {
		t.Errorf("space snapshot = %q/%q/%q", st.SpaceID, st.SpaceKey, st.SpaceName)
	}
	if st.BoardID != "" || st.BoardName != "" || st.BoardType != "" || len(st.BoardMeta) != 0 || !st.Draft() {
		t.Errorf("board must be cleared: %+v", st)
	}
	if n := e.int(`SELECT count(*) FROM standup_column_map WHERE standup_id = ?`, id); n != 0 {
		t.Errorf("column map rows = %d, want 0", n)
	}
	if n := e.int(`SELECT count(*) FROM standup_status_lane WHERE standup_id = ?`, id); n != 0 {
		t.Errorf("status lane rows = %d, want 0", n)
	}
	if !e.needsSync(id) {
		t.Error("stored needs_sync must be 1")
	}
	// settings and lanes are preserved
	if se.PulseDays != 14 || len(e.lanes(id)) != 4 {
		t.Errorf("settings/lanes must survive SetSpace: %+v, %d lanes", se, len(e.lanes(id)))
	}
}

func TestStandupSetSpace_UnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	_, err := e.s.SetSpace(e.ctx, 999, model.Space{ID: "1", Key: "A", Name: "A"})
	w16RequireCode(t, err, apperr.NotFound)
}

// ── SetFilterCheck ──

func TestStandupSetFilterCheck_StoresResult(t *testing.T) {
	e := newW16(t)
	id := e.create()
	if st, _ := e.get(id); st.ExtraFilterOK != nil || !st.FilterCheckedAt.IsZero() {
		t.Fatalf("a new standup has no filter check: %+v", st)
	}

	e.clk.Advance(time.Hour)
	if err := e.s.SetFilterCheck(e.ctx, id, false, []string{"Field 'foo' does not exist", "Unexpected token"}); err != nil {
		t.Fatalf("SetFilterCheck: %v", err)
	}
	st, _ := e.get(id)
	if st.ExtraFilterOK == nil || *st.ExtraFilterOK {
		t.Fatalf("ExtraFilterOK = %v, want false", st.ExtraFilterOK)
	}
	for _, msg := range []string{"Field 'foo' does not exist", "Unexpected token"} {
		if !strings.Contains(st.ExtraFilterError, msg) {
			t.Errorf("ExtraFilterError %q must contain %q", st.ExtraFilterError, msg)
		}
	}
	if !st.FilterCheckedAt.Equal(e.clk.Now()) {
		t.Errorf("FilterCheckedAt = %v, want %v", st.FilterCheckedAt, e.clk.Now())
	}

	e.clk.Advance(time.Hour)
	if err := e.s.SetFilterCheck(e.ctx, id, true, nil); err != nil {
		t.Fatal(err)
	}
	st, _ = e.get(id)
	if st.ExtraFilterOK == nil || !*st.ExtraFilterOK || st.ExtraFilterError != "" {
		t.Errorf("after an ok check: ok=%v err=%q", st.ExtraFilterOK, st.ExtraFilterError)
	}
	if !st.FilterCheckedAt.Equal(e.clk.Now()) {
		t.Errorf("FilterCheckedAt = %v, want %v", st.FilterCheckedAt, e.clk.Now())
	}
}

func TestStandupSetFilterCheck_DoesNotSetNeedsSync(t *testing.T) {
	e := newW16(t)
	id := e.create()
	e.setNeedsSync(id, 0)
	if err := e.s.SetFilterCheck(e.ctx, id, true, nil); err != nil {
		t.Fatal(err)
	}
	if e.needsSync(id) {
		t.Error("a validation result is not a source change")
	}
}

func TestStandupSetFilterCheck_UnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	w16RequireCode(t, e.s.SetFilterCheck(e.ctx, 999, true, nil), apperr.NotFound)
}

// ── SetConnectionAll ──

func TestStandupSetConnectionAll_FillsOnlyStandupsWithoutOne(t *testing.T) {
	e := newW16(t)
	ids := e.createN(3)
	archived := ids[2]
	if _, err := e.s.Archive(e.ctx, archived); err != nil {
		t.Fatal(err)
	}
	e.seedConnection(1)
	e.seedConnection(2)
	e.exec(`UPDATE standup SET connection_id = 2 WHERE id = ?`, ids[1])

	if err := e.s.SetConnectionAll(e.ctx, 1); err != nil {
		t.Fatalf("SetConnectionAll: %v", err)
	}
	for id, want := range map[int64]int64{ids[0]: 1, ids[1]: 2, archived: 1} {
		if st, _ := e.get(id); st.ConnectionID != want {
			t.Errorf("standup %d ConnectionID = %d, want %d", id, st.ConnectionID, want)
		}
	}
}

// ── Effective ──

func w16SeedRichStandup(e *w16Env) int64 {
	id := e.create()
	e.setBoard(id, "scrum")
	e.exec(`UPDATE standup SET scope = 'board', extra_filter = 'labels = x' WHERE id = ?`, id)
	if _, err := e.s.Update(e.ctx, id, Patch{
		PulseDays: w16p(14), RowsMode: w16p("roomy"), DoneWindow: w16p("14d"), BacklogRows: w16p(7),
		ReshuffleDaily: w16p(true), UnassignedPosition: w16p("first"), ShadeWeekends: w16p(false),
		ShowComments: w16p(false), ShowPRs: w16p(false), Name: w16p("Platform daily"),
	}); err != nil {
		e.t.Fatal(err)
	}
	e.exec(`UPDATE standup_settings SET shuffle_seed = 99, shuffle_date = '2026-11-23' WHERE standup_id = ?`, id)
	// walk order prog, todo, qa, review with limits 4, 0, 9, 1
	e.exec(`UPDATE standup_lane SET walk_position = 0, limit_days = 4 WHERE standup_id = ? AND lane = 'prog'`, id)
	e.exec(`UPDATE standup_lane SET walk_position = 1, limit_days = 0 WHERE standup_id = ? AND lane = 'todo'`, id)
	e.exec(`UPDATE standup_lane SET walk_position = 2, limit_days = 9 WHERE standup_id = ? AND lane = 'qa'`, id)
	e.exec(`UPDATE standup_lane SET walk_position = 3, limit_days = 1 WHERE standup_id = ? AND lane = 'review'`, id)
	// column map inserted out of order
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'Done', 3, 'done', 1)`, id)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'To Do', 0, 'todo', 1)`, id)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'Code Review', 2, 'review', 0)`, id)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'Doing', 1, 'prog', 1)`, id)
	e.exec(`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (?, '30', 'Closed', 'Done', 'done')`, id)
	e.exec(`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (?, '10', 'Open', 'To Do', 'todo')`, id)
	e.exec(`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (?, '20', 'In Review', 'Code Review', 'review')`, id)
	// speakers inserted out of order; display names are the snapshot in standup_speaker
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually, user_touched, created_at) VALUES (?, 'acc-c', 'Cy Third', 2, 1, 0, 0, 1000)`, id)
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually, user_touched, created_at) VALUES (?, 'acc-a', 'Ada First', 0, 0, 0, 1, 1000)`, id)
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, added_manually, user_touched, created_at) VALUES (?, 'acc-b', 'Ben Second', 1, 1, 1, 1, 1000)`, id)
	e.exec(`UPDATE standup_sync_state SET active_sprint_ids = '["101","102"]' WHERE standup_id = ?`, id)
	return id
}

func w16SortStatusLanes(c *model.EffectiveConfig) {
	sort.Slice(c.StatusLanes, func(i, j int) bool { return c.StatusLanes[i].StatusID < c.StatusLanes[j].StatusID })
}

func TestEffective_AssemblesEverythingInOrder(t *testing.T) {
	e := newW16(t)
	id := w16SeedRichStandup(e)

	got, err := e.s.Effective(e.ctx, id)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	want := model.EffectiveConfig{
		StandupID: id, Rev: e.rev(id), Name: "Platform daily",
		SpaceID: "10001", SpaceKey: "MOB", SpaceName: "Service Desk",
		BoardID: "B1", BoardName: "MOB board", BoardType: "scrum",
		Scope: "board", ExtraFilter: "labels = x",
		Lanes: []model.LaneConfig{
			{Lane: "prog", Position: 0, LimitDays: 4},
			{Lane: "todo", Position: 1, LimitDays: 0},
			{Lane: "qa", Position: 2, LimitDays: 9},
			{Lane: "review", Position: 3, LimitDays: 1},
		},
		Columns: []model.ColumnLane{
			{ColumnName: "To Do", Position: 0, Lane: "todo", Auto: true},
			{ColumnName: "Doing", Position: 1, Lane: "prog", Auto: true},
			{ColumnName: "Code Review", Position: 2, Lane: "review", Auto: false},
			{ColumnName: "Done", Position: 3, Lane: "done", Auto: true},
		},
		StatusLanes: []model.StatusLane{
			{StatusID: "10", StatusName: "Open", ColumnName: "To Do", Lane: "todo"},
			{StatusID: "20", StatusName: "In Review", ColumnName: "Code Review", Lane: "review"},
			{StatusID: "30", StatusName: "Closed", ColumnName: "Done", Lane: "done"},
		},
		Speakers: []model.SpeakerConfig{
			{AccountID: "acc-a", Name: "Ada First", Position: 0, Included: false, Manual: false},
			{AccountID: "acc-b", Name: "Ben Second", Position: 1, Included: true, Manual: true},
			{AccountID: "acc-c", Name: "Cy Third", Position: 2, Included: true, Manual: false},
		},
		UnassignedPosition: "first", ReshuffleDaily: true, ShuffleSeed: 99, ShuffleDate: "2026-11-23",
		PulseDays: 14, ShowCommits: true, ShowPRs: false, ShowComments: false, ShadeWeekends: false,
		RowsMode: "roomy", DoneWindow: "14d", BacklogRows: 7,
		ActiveSprintIDs: []string{"101", "102"},
	}
	w16SortStatusLanes(&got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Effective mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestEffective_DraftHasDefaultsAndEmptyLists(t *testing.T) {
	e := newW16(t)
	id := e.create()
	got, err := e.s.Effective(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.StandupID != id || got.Name != "New standup" || got.BoardID != "" || got.BoardType != "" || got.Scope != "sprint" {
		t.Errorf("draft identity: %+v", got)
	}
	if !reflect.DeepEqual(got.Lanes, DefaultLanes) {
		t.Errorf("Lanes = %+v, want DefaultLanes %+v", got.Lanes, DefaultLanes)
	}
	if len(got.Columns) != 0 || len(got.StatusLanes) != 0 || len(got.Speakers) != 0 || len(got.ActiveSprintIDs) != 0 {
		t.Errorf("a draft has no columns, status lanes, speakers or sprint ids: %+v", got)
	}
	if got.PulseDays != 21 || got.BacklogRows != 12 || got.RowsMode != "fit" || got.DoneWindow != "sprint" ||
		got.UnassignedPosition != "last" || !got.ShowCommits || !got.ShowPRs || !got.ShowComments || !got.ShadeWeekends || got.ReshuffleDaily {
		t.Errorf("draft settings: %+v", got)
	}
}

func TestEffective_ReflectsRevAfterMutations(t *testing.T) {
	e := newW16(t)
	id := e.create()
	ack, err := e.s.Update(e.ctx, id, Patch{PulseDays: w16p(28)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.s.Effective(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rev != ack.Rev || got.PulseDays != 28 {
		t.Errorf("Effective.Rev = %d (want %d), PulseDays = %d", got.Rev, ack.Rev, got.PulseDays)
	}
}

func TestEffective_TxVariantMatchesStoreVariant(t *testing.T) {
	e := newW16(t)
	id := w16SeedRichStandup(e)
	want, err := e.s.Effective(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var got model.EffectiveConfig
	err = e.db.ReadTx(e.ctx, func(tx *sql.Tx) error {
		var err error
		got, err = EffectiveTx(e.ctx, tx, id)
		return err
	})
	if err != nil {
		t.Fatalf("EffectiveTx: %v", err)
	}
	w16SortStatusLanes(&got)
	w16SortStatusLanes(&want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EffectiveTx != Effective:\n%+v\n%+v", got, want)
	}
}

func TestEffective_UnknownIsNotFound(t *testing.T) {
	e := newW16(t)
	_, err := e.s.Effective(e.ctx, 999)
	w16RequireCode(t, err, apperr.NotFound)
}

// ── every mutation bumps rev ──

func TestStandupEveryMutationBumpsRevByOne(t *testing.T) {
	mutations := []struct {
		name string
		run  func(e *w16Env, id, other int64) (Ack, error)
	}{
		{"Update", func(e *w16Env, id, _ int64) (Ack, error) { return e.s.Update(e.ctx, id, Patch{PulseDays: w16p(7)}) }},
		{"Move", func(e *w16Env, id, _ int64) (Ack, error) { return e.s.Move(e.ctx, id, 1) }},
		{"Archive", func(e *w16Env, id, _ int64) (Ack, error) { return e.s.Archive(e.ctx, id) }},
		{"SetSpace", func(e *w16Env, id, _ int64) (Ack, error) {
			return e.s.SetSpace(e.ctx, id, model.Space{ID: "9", Key: "Z", Name: "Zed"})
		}},
		{"CopySettingsFrom", func(e *w16Env, id, other int64) (Ack, error) { return e.s.CopySettingsFrom(e.ctx, id, other) }},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			e := newW16(t)
			ids := e.createN(2)
			e.clk.Advance(time.Minute)
			before := e.rev(ids[0])
			ack, err := m.run(e, ids[0], ids[1])
			if err != nil {
				t.Fatal(err)
			}
			e.assertAck(ids[0], before, ack)
		})
	}
}

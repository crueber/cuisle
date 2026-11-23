package settings

// WP-17 tests: AutoLane, SetBoard, column map merge, status lanes, SetColumnLane, UnmappedStatuses.
// Also owns the shared WP-17 helpers (prefix w17) used by lanes_test.go and speakers_test.go.
// Run only this WP's tests with:
//   go test -tags purego ./internal/settings -run 'TestAutoLane|TestSetBoard|TestReplaceBoardColumns|TestMergeColumns|TestRebuildStatusLanes|TestSetColumnLane|TestUnmappedStatuses|TestLanes|TestSetWalkOrder|TestSetLaneLimit|TestSpeakers|TestSetSpeakerIncluded|TestMoveSpeaker|TestSortSpeakers|TestAddSpeaker|TestRemoveSpeaker|TestEnsureDailySeed|TestMergeSpeakers'
//
// WP-17 tests never call Create/Get/Update/Effective (WP-16); they seed and assert through SQL.
//
// SPEC-NOTE: AutoLane follows jira-sync §2.5 literally and in table order. "Last column" means index == count-1.
// A column with no statuses is never "every status done" and never "every status new" (no vacuous truth).
// SPEC-NOTE: AutoLane fallthrough (first status has category "undefined", unknown status id, or a column with no
// statuses and no name match) is "todo": product-spec §2.1 says nothing is hidden, and jira-sync §5.2 draws unmapped
// statuses in the To Do tone. When the column is neither all-new nor all-done, the lane comes from the FIRST status's
// category (new → todo, indeterminate → prog), as the doc says "otherwise by the status category of the column's first status".
// SPEC-NOTE: "PR" matches only when the whole (case-insensitive) name equals "pr"; names that merely contain it
// ("Preparing", "Sprint Planning") do not.
// SPEC-NOTE: Non-patch parameters use these validation Fields: SetColumnLane → "lane" (bad lane) / "columnName"
// (unknown column); SetLaneLimit → "lane" / "days"; SetWalkOrder → "lanes"; Speaker methods → "accountId"; SortSpeakers → "mode".
// SPEC-NOTE: Unknown standup or unknown speaker/account → apperr not_found.
// SPEC-NOTE: SetBoard upserts the passed statuses into the status table (RebuildStatusLanesTx reads names from there),
// stores the board's RawType in board.type and its normalised Kind in standup.board_type, and sets board.config_fetched_at.
// SPEC-NOTE: MergeColumnsTx re-evaluates AutoLane for rows with auto=1 (their lane may change when columns move or
// statuses change), keeps auto=0 rows by name. "changed" is true when any column row was added, removed, re-laned or
// re-positioned.
// SPEC-NOTE: UnmappedStatuses lists statuses of the board's cached columns that have no standup_status_lane row because
// their column is absent from standup_column_map. Whether statuses of a column mapped to "hidden" count as unmapped is
// NOT specified and not tested (the docs are ambiguous: hidden is a mapping, yet the screen should say what is hidden).
// SPEC-NOTE: Status-lane rows exist for statuses of columns mapped to 'hidden' too (lane 'hidden').

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
	"github.com/crueber/cuisle/internal/store/storetest"
)

// ── shared WP-17 helpers ──

type w17Clock struct{ t time.Time }

func (c *w17Clock) Now() time.Time          { return c.t }
func (c *w17Clock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// Fixed non-UTC zone: local date 2026-11-23, UTC date 2026-11-24.
var w17Zone = time.FixedZone("UTC-8", -8*3600)

type w17Env struct {
	t   *testing.T
	s   *Store
	db  *store.DB
	clk *w17Clock
	ctx context.Context
	n   int64 // standups seeded so far
}

func newW17(t *testing.T) *w17Env {
	t.Helper()
	clk := &w17Clock{t: time.Date(2026, 11, 23, 22, 30, 0, 0, w17Zone)}
	db := storetest.Open(t, clk.Now)
	e := &w17Env{t: t, s: New(db, clk.Now), db: db, clk: clk, ctx: context.Background()}
	e.exec(`INSERT INTO connection (id, source, base_url, account, created_at) VALUES (1, 'jira', 'https://acme.atlassian.net', 'me@acme.com', 1000)`)
	return e
}

func w17p[T any](v T) *T { return &v }

func (e *w17Env) exec(q string, args ...any) { e.t.Helper(); storetest.Exec(e.t, e.db, q, args...) }

func (e *w17Env) int(q string, args ...any) int64 {
	e.t.Helper()
	var n sql.NullInt64
	if err := e.db.W.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatalf("query %q: %v", q, err)
	}
	return n.Int64
}

func (e *w17Env) str(q string, args ...any) string {
	e.t.Helper()
	var n sql.NullString
	if err := e.db.W.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatalf("query %q: %v", q, err)
	}
	return n.String
}

func (e *w17Env) isNull(q string, args ...any) bool {
	e.t.Helper()
	var n sql.NullString
	if err := e.db.W.QueryRowContext(e.ctx, q, args...).Scan(&n); err != nil {
		e.t.Fatalf("query %q: %v", q, err)
	}
	return !n.Valid
}

func (e *w17Env) dump(q string, args ...any) []string {
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

// seedStandup inserts a standup (connection 1) with settings, the 4 default lanes and a sync-state row with
// needs_sync = 0. boardType "" makes a draft (no board).
func (e *w17Env) seedStandup(boardType string) int64 {
	e.t.Helper()
	e.n++
	id := e.n
	scope := "sprint"
	if boardType == "kanban" {
		scope = "board"
	}
	if boardType == "" {
		e.exec(`INSERT INTO standup (id, connection_id, name, position, scope, created_at, updated_at) VALUES (?, 1, 'S', ?, 'sprint', 1000, 1000)`, id, id-1)
	} else {
		e.exec(`INSERT INTO standup (id, connection_id, name, position, space_id, space_key, space_name, board_id, board_name, board_type, scope, created_at, updated_at)
		        VALUES (?, 1, 'S', ?, '10001', 'MOB', 'Service', 'B1', 'Board', ?, ?, 1000, 1000)`, id, id-1, boardType, scope)
	}
	e.exec(`INSERT INTO standup_settings (standup_id) VALUES (?)`, id)
	e.exec(`INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days) VALUES (?, 'qa', 0, 3)`, id)
	e.exec(`INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days) VALUES (?, 'review', 1, 2)`, id)
	e.exec(`INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days) VALUES (?, 'prog', 2, 5)`, id)
	e.exec(`INSERT INTO standup_lane (standup_id, lane, walk_position, limit_days) VALUES (?, 'todo', 3, 10)`, id)
	e.exec(`INSERT INTO standup_sync_state (standup_id, needs_sync) VALUES (?, 0)`, id)
	return id
}

func (e *w17Env) rev(id int64) int64 {
	e.t.Helper()
	return e.int(`SELECT rev FROM standup WHERE id = ?`, id)
}

func (e *w17Env) needsSync(id int64) bool {
	e.t.Helper()
	return e.int(`SELECT needs_sync FROM standup_sync_state WHERE standup_id = ?`, id) == 1
}

func (e *w17Env) assertAck(id, before int64, ack Ack) {
	e.t.Helper()
	if ack.Rev != before+1 {
		e.t.Errorf("Ack.Rev = %d, want %d (previous rev + 1)", ack.Rev, before+1)
	}
	if got := e.rev(id); got != ack.Rev {
		e.t.Errorf("stored rev = %d, Ack.Rev = %d; they must match", got, ack.Rev)
	}
	if !ack.SavedAt.Equal(e.clk.Now()) {
		e.t.Errorf("Ack.SavedAt = %v, want %v", ack.SavedAt, e.clk.Now())
	}
}

func w17RequireValidation(t *testing.T, err error, field string) {
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

func w17RequireCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

// A board column with its statuses: the single source for model.Column, the statuses map and the cache rows.
type w17St struct{ ID, Name, Category string }
type w17Col struct {
	Name     string
	Statuses []w17St
}

func w17Build(spec []w17Col) (cols []model.Column, byID map[string]model.Status, list []model.Status) {
	byID = map[string]model.Status{}
	for i, c := range spec {
		col := model.Column{Name: c.Name, Position: i}
		for _, s := range c.Statuses {
			col.StatusIDs = append(col.StatusIDs, s.ID)
			st := model.Status{ID: s.ID, Name: s.Name, Category: s.Category}
			if _, dup := byID[s.ID]; !dup {
				list = append(list, st)
			}
			byID[s.ID] = st
		}
		cols = append(cols, col)
	}
	return
}

// seedCache writes the board and its columns/statuses into the autocomplete caches (what SetBoard / sync would do).
func (e *w17Env) seedCache(boardID string, spec []w17Col) {
	e.t.Helper()
	e.exec(`INSERT OR REPLACE INTO board (connection_id, id, name, type, space_id, source_meta, fetched_at) VALUES (1, ?, 'Board', 'scrum', '10001', '{}', 1000)`, boardID)
	e.exec(`DELETE FROM board_column WHERE connection_id = 1 AND board_id = ?`, boardID)
	for i, c := range spec {
		e.exec(`INSERT INTO board_column (connection_id, board_id, position, name) VALUES (1, ?, ?, ?)`, boardID, i, c.Name)
		for _, s := range c.Statuses {
			e.exec(`INSERT INTO board_column_status (connection_id, board_id, position, status_id) VALUES (1, ?, ?, ?)`, boardID, i, s.ID)
			e.exec(`INSERT OR REPLACE INTO status (connection_id, id, name, category) VALUES (1, ?, ?, ?)`, s.ID, s.Name, s.Category)
		}
	}
}

func (e *w17Env) merge(id int64, cols []model.Column, statuses map[string]model.Status) bool {
	e.t.Helper()
	var changed bool
	err := e.db.WriteTx(e.ctx, func(tx *sql.Tx) error {
		var err error
		changed, err = MergeColumnsTx(e.ctx, tx, id, cols, statuses)
		return err
	})
	if err != nil {
		e.t.Fatalf("MergeColumnsTx: %v", err)
	}
	return changed
}

func (e *w17Env) rebuild(id int64) {
	e.t.Helper()
	err := e.db.WriteTx(e.ctx, func(tx *sql.Tx) error { return RebuildStatusLanesTx(e.ctx, tx, id) })
	if err != nil {
		e.t.Fatalf("RebuildStatusLanesTx: %v", err)
	}
}

const w17MapDump = `SELECT column_name, position, lane, auto FROM standup_column_map WHERE standup_id = ? ORDER BY position`
const w17StatusDump = `SELECT status_id, status_name, column_name, lane FROM standup_status_lane WHERE standup_id = ? ORDER BY status_id`

// the six-column scrum board used by several tests
var w17Board = []w17Col{
	{"Backlog", []w17St{{"1", "Backlog", "new"}}},
	{"To Do", []w17St{{"2", "To Do", "new"}}},
	{"In Progress", []w17St{{"3", "In Progress", "indeterminate"}}},
	{"In Review", []w17St{{"4", "In Review", "indeterminate"}}},
	{"QA", []w17St{{"5", "In QA", "indeterminate"}}},
	{"Done", []w17St{{"6", "Done", "done"}}},
}

var w17BoardAutoMap = []string{
	"Backlog|0|backlog|1", "To Do|1|todo|1", "In Progress|2|prog|1", "In Review|3|review|1", "QA|4|qa|1", "Done|5|done|1",
}

var w17BoardStatusLanes = []string{
	"1|Backlog|Backlog|backlog", "2|To Do|To Do|todo", "3|In Progress|In Progress|prog",
	"4|In Review|In Review|review", "5|In QA|QA|qa", "6|Done|Done|done",
}

// ── AutoLane (pure; jira-sync §2.5) ──

func TestAutoLane_Rules(t *testing.T) {
	type c struct {
		name     string
		col      string
		statuses []string // categories, one status each; "?" = id missing from the statuses map
		index    int
		count    int
		want     string
	}
	mid := func(name, col string, statuses []string, want string) c { return c{name, col, statuses, 1, 5, want} }
	last := func(name, col string, statuses []string, want string) c { return c{name, col, statuses, 4, 5, want} }
	cases := []c{
		// rule 1: backlog
		mid("backlog", "Backlog", []string{"new"}, "backlog"),
		mid("backlog_upper", "BACKLOG", []string{"new"}, "backlog"),
		mid("backlog_contains", "Product Backlog", []string{"new"}, "backlog"),
		mid("backlog_sprint_backlog", "Sprint Backlog", []string{"new"}, "backlog"),
		last("backlog_wins_over_last_column", "Backlog", []string{"new"}, "backlog"),
		mid("backlog_wins_over_review", "Backlog Review", []string{"new"}, "backlog"),
		mid("backlog_wins_over_all_done", "Backlog", []string{"done"}, "backlog"),

		// rule 2: review / equals pr
		mid("review", "In Review", []string{"indeterminate"}, "review"),
		mid("review_code", "Code Review", []string{"indeterminate"}, "review"),
		mid("review_upper", "REVIEW", []string{"indeterminate"}, "review"),
		mid("review_in_parens", "Peer feedback (Review)", []string{"indeterminate"}, "review"),
		mid("pr_exact", "PR", []string{"indeterminate"}, "review"),
		mid("pr_lowercase", "pr", []string{"indeterminate"}, "review"),
		mid("pr_mixed_case", "Pr", []string{"indeterminate"}, "review"),
		last("pr_beats_last_column", "PR", []string{"indeterminate"}, "review"),
		mid("review_beats_all_done", "Review", []string{"done"}, "review"),
		mid("review_beats_qa_token", "Test Review", []string{"indeterminate"}, "review"),
		mid("contains_pr_but_not_equal_preparing", "Preparing", []string{"indeterminate"}, "prog"),
		mid("contains_pr_but_not_equal_sprint_planning", "Sprint Planning", []string{"new"}, "todo"),

		// rule 3: qa tokens, one row each
		mid("qa", "QA", []string{"indeterminate"}, "qa"),
		mid("qa_lowercase", "qa", []string{"indeterminate"}, "qa"),
		mid("qa_inside_name", "Awaiting QA", []string{"indeterminate"}, "qa"),
		mid("test", "Test", []string{"indeterminate"}, "qa"),
		mid("testing", "In Testing", []string{"indeterminate"}, "qa"),
		mid("verif_verification", "Verification", []string{"indeterminate"}, "qa"),
		mid("verif_verified", "Verified", []string{"indeterminate"}, "qa"),
		mid("uat", "UAT", []string{"indeterminate"}, "qa"),
		mid("uat_lowercase", "uat sign-off", []string{"indeterminate"}, "qa"),
		mid("ready_for_release", "Ready for Release", []string{"indeterminate"}, "qa"),
		mid("ready_for_release_case", "READY FOR RELEASE", []string{"new"}, "qa"),
		last("qa_beats_last_column", "QA", []string{"indeterminate"}, "qa"),
		last("ready_for_release_beats_last_column", "Ready for Release", []string{"indeterminate"}, "qa"),
		mid("qa_beats_all_done", "Testing", []string{"done"}, "qa"),
		mid("qa_beats_dev", "Dev Testing", []string{"indeterminate"}, "qa"),

		// rule 4: last column, or every status done
		last("last_column_done_category", "Closed", []string{"done"}, "done"),
		last("last_column_new_statuses", "Shipped", []string{"new"}, "done"),
		last("last_column_indeterminate", "Released", []string{"indeterminate"}, "done"),
		last("last_column_beats_in_progress_name", "In Progress", []string{"indeterminate"}, "done"),
		mid("all_done_not_last", "Closed", []string{"done"}, "done"),
		mid("all_done_two_statuses", "Resolved", []string{"done", "done"}, "done"),
		mid("all_done_beats_progress_name", "Doing", []string{"done"}, "done"),
		mid("not_all_done_is_not_done", "Wrapping Up", []string{"indeterminate", "done"}, "prog"),
		mid("empty_column_is_not_all_done", "Parking", nil, "todo"),

		// rule 5: progress / doing / dev (not "for dev")
		mid("progress", "In Progress", []string{"indeterminate"}, "prog"),
		mid("progress_upper", "WORK IN PROGRESS", []string{"indeterminate"}, "prog"),
		mid("progress_beats_new_category", "In Progress", []string{"new"}, "prog"),
		mid("doing", "Doing", []string{"new"}, "prog"),
		mid("development", "Development", []string{"new"}, "prog"),
		mid("in_dev", "In Dev", []string{"new"}, "prog"),
		mid("dev_review_is_review", "Dev Review", []string{"indeterminate"}, "review"),
		mid("ready_for_dev_new_is_todo", "Ready for Dev", []string{"new"}, "todo"),
		mid("ready_for_dev_upper_new_is_todo", "READY FOR DEV", []string{"new"}, "todo"),
		mid("ready_for_development_new_is_todo", "Ready for Development", []string{"new"}, "todo"),
		mid("selected_for_development_is_todo", "Selected for Development", []string{"new"}, "todo"),
		mid("ready_for_dev_indeterminate_by_category", "Ready for Dev", []string{"indeterminate"}, "prog"),

		// rule 6: every status new
		mid("todo", "To Do", []string{"new"}, "todo"),
		mid("todo_two_new", "Open", []string{"new", "new"}, "todo"),

		// rule 7: by the category of the first status
		mid("indeterminate", "Awaiting Input", []string{"indeterminate"}, "prog"),
		mid("indeterminate_first_then_new", "Triage", []string{"indeterminate", "new"}, "prog"),
		mid("new_first_then_indeterminate", "Triage", []string{"new", "indeterminate"}, "todo"),

		// fallthrough: nothing is hidden
		mid("undefined_category_falls_to_todo", "Odd", []string{"undefined"}, "todo"),
		mid("unknown_status_id_falls_to_todo", "Odd", []string{"?"}, "todo"),
		mid("empty_unrecognised_column_is_todo", "Odd", nil, "todo"),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			col := model.Column{Name: tc.col, Position: tc.index}
			statuses := map[string]model.Status{}
			for i, cat := range tc.statuses {
				id := string(rune('a' + i))
				col.StatusIDs = append(col.StatusIDs, id)
				if cat != "?" {
					statuses[id] = model.Status{ID: id, Name: "S" + id, Category: cat}
				}
			}
			if got := AutoLane(col, tc.index, tc.count, statuses); got != tc.want {
				t.Errorf("AutoLane(%q, index %d of %d, %v) = %q, want %q", tc.col, tc.index, tc.count, tc.statuses, got, tc.want)
			}
		})
	}
}

func TestAutoLane_AlwaysReturnsAKnownLane(t *testing.T) {
	valid := map[string]bool{"backlog": true, "todo": true, "prog": true, "review": true, "qa": true, "done": true, "hidden": true}
	names := []string{"", "x", "Backlog", "PR", "QA", "Done", "In Progress", "To Do", "???", "Ready for Dev"}
	cats := []string{"new", "indeterminate", "done", "undefined", "bogus"}
	for _, name := range names {
		for _, cat := range cats {
			for _, idx := range []int{0, 2, 4} {
				col := model.Column{Name: name, StatusIDs: []string{"a"}}
				got := AutoLane(col, idx, 5, map[string]model.Status{"a": {ID: "a", Category: cat}})
				if !valid[got] {
					t.Errorf("AutoLane(%q, %d, cat %q) = %q, not a lane id", name, idx, cat, got)
				}
			}
		}
	}
}

// ── SetBoard ──

func w17Config(kind, raw string, spec []w17Col) (model.BoardConfig, []model.Status) {
	cols, _, list := w17Build(spec)
	return model.BoardConfig{
		Board: model.Board{
			ID: "42", Name: "MOB board", Kind: kind, RawType: raw, SpaceID: "10001",
			Meta: map[string]any{"filterId": 9001, "subQuery": "fixVersion is EMPTY"},
		},
		Columns: cols,
	}, list
}

func TestSetBoard_RecordsBoardCachesColumnsAndMapsThem(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	cfg, statuses := w17Config("scrum", "simple", w17Board)
	before := e.rev(id)
	e.clk.Advance(time.Minute)

	ack, err := e.s.SetBoard(e.ctx, id, cfg, statuses)
	if err != nil {
		t.Fatalf("SetBoard: %v", err)
	}
	e.assertAck(id, before, ack)
	if !ack.NeedsSync || !e.needsSync(id) {
		t.Errorf("SetBoard is a source change: Ack.NeedsSync = %v, stored = %v", ack.NeedsSync, e.needsSync(id))
	}

	// the standup row
	if got := e.str(`SELECT board_id FROM standup WHERE id = ?`, id); got != "42" {
		t.Errorf("board_id = %q", got)
	}
	if got := e.str(`SELECT board_name FROM standup WHERE id = ?`, id); got != "MOB board" {
		t.Errorf("board_name = %q", got)
	}
	if got := e.str(`SELECT board_type FROM standup WHERE id = ?`, id); got != "scrum" {
		t.Errorf("board_type = %q, want the normalised kind 'scrum'", got)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(e.str(`SELECT board_meta FROM standup WHERE id = ?`, id)), &meta); err != nil {
		t.Fatalf("board_meta is not JSON: %v", err)
	}
	if meta["subQuery"] != "fixVersion is EMPTY" || meta["filterId"] != float64(9001) {
		t.Errorf("board_meta = %v", meta)
	}
	if got := e.str(`SELECT scope FROM standup WHERE id = ?`, id); got != "sprint" {
		t.Errorf("a scrum board keeps scope 'sprint', got %q", got)
	}
	if got := e.str(`SELECT done_window FROM standup_settings WHERE standup_id = ?`, id); got != "sprint" {
		t.Errorf("a scrum board keeps done_window 'sprint', got %q", got)
	}

	// the board cache: raw provider type, columns, column statuses
	if got := e.str(`SELECT type FROM board WHERE connection_id = 1 AND id = '42'`); got != "simple" {
		t.Errorf("board.type = %q, want the raw provider type 'simple'", got)
	}
	if got := e.str(`SELECT name FROM board WHERE connection_id = 1 AND id = '42'`); got != "MOB board" {
		t.Errorf("board.name = %q", got)
	}
	if got := e.str(`SELECT space_id FROM board WHERE connection_id = 1 AND id = '42'`); got != "10001" {
		t.Errorf("board.space_id = %q", got)
	}
	if e.isNull(`SELECT config_fetched_at FROM board WHERE connection_id = 1 AND id = '42'`) {
		t.Error("board.config_fetched_at must be set")
	}
	wantCols := []string{"0|Backlog", "1|To Do", "2|In Progress", "3|In Review", "4|QA", "5|Done"}
	if got := e.dump(`SELECT position, name FROM board_column WHERE connection_id = 1 AND board_id = '42' ORDER BY position`); !reflect.DeepEqual(got, wantCols) {
		t.Errorf("board_column = %v, want %v", got, wantCols)
	}
	wantColStatus := []string{"0|1", "1|2", "2|3", "3|4", "4|5", "5|6"}
	if got := e.dump(`SELECT position, status_id FROM board_column_status WHERE connection_id = 1 AND board_id = '42' ORDER BY position`); !reflect.DeepEqual(got, wantColStatus) {
		t.Errorf("board_column_status = %v, want %v", got, wantColStatus)
	}
	// statuses are recorded with names and categories
	wantStatus := []string{"1|Backlog|new", "2|To Do|new", "3|In Progress|indeterminate", "4|In Review|indeterminate", "5|In QA|indeterminate", "6|Done|done"}
	if got := e.dump(`SELECT id, name, category FROM status WHERE connection_id = 1 ORDER BY id`); !reflect.DeepEqual(got, wantStatus) {
		t.Errorf("status = %v, want %v", got, wantStatus)
	}

	// the column map and status lanes
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, w17BoardAutoMap) {
		t.Errorf("standup_column_map = %v\nwant %v", got, w17BoardAutoMap)
	}
	if got := e.dump(w17StatusDump, id); !reflect.DeepEqual(got, w17BoardStatusLanes) {
		t.Errorf("standup_status_lane = %v\nwant %v", got, w17BoardStatusLanes)
	}
}

func TestSetBoard_KanbanForcesBoardScopeAndFourteenDayDoneWindow(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	e.exec(`UPDATE standup_settings SET done_window = 'yesterday' WHERE standup_id = ?`, id)
	cfg, statuses := w17Config("kanban", "kanban", w17Board)

	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatalf("SetBoard: %v", err)
	}
	if got := e.str(`SELECT board_type FROM standup WHERE id = ?`, id); got != "kanban" {
		t.Errorf("board_type = %q", got)
	}
	if got := e.str(`SELECT scope FROM standup WHERE id = ?`, id); got != "board" {
		t.Errorf("scope = %q, want board", got)
	}
	if got := e.str(`SELECT done_window FROM standup_settings WHERE standup_id = ?`, id); got != "14d" {
		t.Errorf("done_window = %q, want 14d (forced for a new kanban board)", got)
	}
}

func TestSetBoard_ReplacingTheBoardReplacesCacheMapAndStatusLanes(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	cfg, statuses := w17Config("scrum", "scrum", w17Board)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}

	second := []w17Col{
		{"To Do", []w17St{{"2", "To Do", "new"}}},
		{"In Progress", []w17St{{"3", "In Progress", "indeterminate"}}},
		{"Released", []w17St{{"7", "Released", "done"}}},
	}
	cfg2, statuses2 := w17Config("scrum", "scrum", second)
	if _, err := e.s.SetBoard(e.ctx, id, cfg2, statuses2); err != nil {
		t.Fatal(err)
	}
	if got := e.dump(`SELECT position, name FROM board_column WHERE connection_id = 1 AND board_id = '42' ORDER BY position`); !reflect.DeepEqual(got, []string{"0|To Do", "1|In Progress", "2|Released"}) {
		t.Errorf("board_column = %v", got)
	}
	if n := e.int(`SELECT count(*) FROM board_column_status WHERE connection_id = 1 AND board_id = '42'`); n != 3 {
		t.Errorf("board_column_status rows = %d, want 3 (old rows replaced)", n)
	}
	wantMap := []string{"To Do|0|todo|1", "In Progress|1|prog|1", "Released|2|done|1"}
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, wantMap) {
		t.Errorf("column map = %v, want %v", got, wantMap)
	}
	wantLanes := []string{"2|To Do|To Do|todo", "3|In Progress|In Progress|prog", "7|Released|Released|done"}
	if got := e.dump(w17StatusDump, id); !reflect.DeepEqual(got, wantLanes) {
		t.Errorf("status lanes = %v, want %v", got, wantLanes)
	}
}

func TestSetBoard_KeepsUserChosenMappingByColumnName(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	cfg, statuses := w17Config("scrum", "scrum", w17Board)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetColumnLane(e.ctx, id, "In Review", "qa"); err != nil {
		t.Fatal(err)
	}
	// the same board is chosen again (e.g. the user re-picks it): the user's choice survives
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}
	if got := e.str(`SELECT lane || '|' || auto FROM standup_column_map WHERE standup_id = ? AND column_name = 'In Review'`, id); got != "qa|0" {
		t.Errorf("In Review = %q, want the user's qa|0", got)
	}
}

func TestSetBoard_KeepsLanes_SameBoardKeepsSpeakers_AnotherBoardResetsThem(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	lanes := e.dump(`SELECT lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY lane`, id)
	cfg, statuses := w17Config("scrum", "scrum", w17Board)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}
	if got := e.dump(`SELECT lane, walk_position, limit_days FROM standup_lane WHERE standup_id = ? ORDER BY lane`, id); !reflect.DeepEqual(got, lanes) {
		t.Errorf("lanes changed: %v → %v", lanes, got)
	}
	e.exec(`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, created_at) VALUES (?, 'acc-1', 'Ada', 0, 1000)`, id)
	e.exec(`INSERT INTO standup_speaker_removed (standup_id, account_id, removed_at) VALUES (?, 'acc-2', 1000)`, id)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil { // same board again
		t.Fatal(err)
	}
	if n := e.int(`SELECT count(*) FROM standup_speaker WHERE standup_id = ?`, id); n != 1 {
		t.Errorf("re-picking the same board: speakers = %d, want 1", n)
	}
	other := cfg
	other.Board.ID = "43"
	if _, err := e.s.SetBoard(e.ctx, id, other, statuses); err != nil {
		t.Fatal(err)
	}
	if n := e.int(`SELECT count(*) FROM standup_speaker WHERE standup_id = ?`, id); n != 0 {
		t.Errorf("another board: speakers = %d, want 0 (the next sync fills the list)", n)
	}
	if n := e.int(`SELECT count(*) FROM standup_speaker_removed WHERE standup_id = ?`, id); n != 0 {
		t.Errorf("another board: removed = %d, want 0", n)
	}
}

func TestMoveColumn_OrdersColumnsWithinTheirLane(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	board := append(append([]w17Col{}, w17Board[:3]...), w17Col{"Code Review", []w17St{{"7", "Code Review", "indeterminate"}}})
	board = append(board, w17Board[3:]...)
	cfg, statuses := w17Config("scrum", "scrum", board)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetColumnLane(e.ctx, id, "Code Review", "prog"); err != nil {
		t.Fatal(err)
	}
	order := func() map[string]int {
		eff, err := e.s.Effective(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, c := range eff.Columns {
			if c.Lane == "prog" {
				out[c.ColumnName] = c.LaneOrder
			}
		}
		return out
	}
	if got := order(); got["In Progress"] != 0 || got["Code Review"] != 1 {
		t.Fatalf("default order = %v, want board order", got)
	}
	if _, err := e.s.MoveColumn(e.ctx, id, "Code Review", 0); err != nil {
		t.Fatal(err)
	}
	if got := order(); got["Code Review"] != 0 || got["In Progress"] != 1 {
		t.Errorf("after move = %v, want Code Review first", got)
	}
	// A column refresh keeps the order; moving a column to another lane drops it from the old lane's order.
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}
	if got := order(); got["Code Review"] != 0 {
		t.Errorf("after refresh = %v, want the order kept", got)
	}
	if _, err := e.s.SetColumnLane(e.ctx, id, "Code Review", "review"); err != nil {
		t.Fatal(err)
	}
	if got := order(); len(got) != 1 || got["In Progress"] != 0 {
		t.Errorf("after lane change = %v, want only In Progress at 0", got)
	}
	if _, err := e.s.MoveColumn(e.ctx, id, "Nope", 0); err == nil {
		t.Error("MoveColumn of an unknown column: want an error")
	}
}

func TestSetBoard_UnknownStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	cfg, statuses := w17Config("scrum", "scrum", w17Board)
	_, err := e.s.SetBoard(e.ctx, 999, cfg, statuses)
	w17RequireCode(t, err, apperr.NotFound)
}

// ── ReplaceBoardColumnsTx ──

func TestReplaceBoardColumnsTx_ReplacesTheCacheRowsOfOneBoardOnly(t *testing.T) {
	e := newW17(t)
	e.seedCache("OTHER", []w17Col{{"Keep", []w17St{{"90", "Keep", "new"}}}})
	cfg, _ := w17Config("scrum", "scrum", w17Board)
	run := func(cfg model.BoardConfig) {
		err := e.db.WriteTx(e.ctx, func(tx *sql.Tx) error { return ReplaceBoardColumnsTx(e.ctx, tx, 1, cfg) })
		if err != nil {
			t.Fatalf("ReplaceBoardColumnsTx: %v", err)
		}
	}
	run(cfg)
	if n := e.int(`SELECT count(*) FROM board_column WHERE board_id = '42'`); n != 6 {
		t.Errorf("columns = %d, want 6", n)
	}
	if n := e.int(`SELECT count(*) FROM board WHERE connection_id = 1 AND id = '42'`); n != 1 {
		t.Errorf("board rows = %d, want 1", n)
	}
	// replace with a smaller config
	cfg.Columns = cfg.Columns[:2]
	run(cfg)
	if n := e.int(`SELECT count(*) FROM board_column WHERE board_id = '42'`); n != 2 {
		t.Errorf("columns after replace = %d, want 2", n)
	}
	if n := e.int(`SELECT count(*) FROM board_column_status WHERE board_id = '42'`); n != 2 {
		t.Errorf("column statuses after replace = %d, want 2", n)
	}
	if n := e.int(`SELECT count(*) FROM board_column WHERE board_id = 'OTHER'`); n != 1 {
		t.Errorf("another board's columns were touched: %d", n)
	}
	if n := e.int(`SELECT count(*) FROM board WHERE connection_id = 1 AND id = '42'`); n != 1 {
		t.Errorf("board rows after replace = %d, want 1", n)
	}
}

// ── MergeColumnsTx ──

func TestMergeColumns_FirstMergeAutoMapsEveryColumn(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedCache("B1", w17Board)
	cols, byID, _ := w17Build(w17Board)

	if changed := e.merge(id, cols, byID); !changed {
		t.Error("merging into an empty map must report changed")
	}
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, w17BoardAutoMap) {
		t.Errorf("column map = %v\nwant %v", got, w17BoardAutoMap)
	}
	if got := e.dump(w17StatusDump, id); !reflect.DeepEqual(got, w17BoardStatusLanes) {
		t.Errorf("status lanes = %v\nwant %v", got, w17BoardStatusLanes)
	}
}

func TestMergeColumns_SecondIdenticalMergeReportsNoChange(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedCache("B1", w17Board)
	cols, byID, _ := w17Build(w17Board)
	e.merge(id, cols, byID)
	if changed := e.merge(id, cols, byID); changed {
		t.Error("an identical merge must report changed=false")
	}
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, w17BoardAutoMap) {
		t.Errorf("column map = %v", got)
	}
}

func TestMergeColumns_KeepsUserChoicesByNameAndUpdatesPositions(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedCache("B1", w17Board)
	cols, byID, _ := w17Build(w17Board)
	e.merge(id, cols, byID)
	e.exec(`UPDATE standup_column_map SET lane = 'qa', auto = 0 WHERE standup_id = ? AND column_name = 'In Review'`, id)

	// columns reorder: "In Review" moves to the front
	reordered := []w17Col{w17Board[3], w17Board[0], w17Board[1], w17Board[2], w17Board[4], w17Board[5]}
	e.seedCache("B1", reordered)
	cols2, byID2, _ := w17Build(reordered)
	if changed := e.merge(id, cols2, byID2); !changed {
		t.Error("moving columns is a change")
	}
	want := []string{"In Review|0|qa|0", "Backlog|1|backlog|1", "To Do|2|todo|1", "In Progress|3|prog|1", "QA|4|qa|1", "Done|5|done|1"}
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("column map = %v\nwant %v", got, want)
	}
	// the status lane follows the user's choice
	if got := e.str(`SELECT lane FROM standup_status_lane WHERE standup_id = ? AND status_id = '4'`, id); got != "qa" {
		t.Errorf("status 4 lane = %q, want qa (the user's choice for In Review)", got)
	}
}

func TestMergeColumns_DeletesRemovedColumnsAndTheirStatusLanes(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedCache("B1", w17Board)
	cols, byID, _ := w17Build(w17Board)
	e.merge(id, cols, byID)

	fewer := []w17Col{w17Board[0], w17Board[1], w17Board[2], w17Board[5]} // In Review and QA removed
	e.seedCache("B1", fewer)
	cols2, byID2, _ := w17Build(fewer)
	if changed := e.merge(id, cols2, byID2); !changed {
		t.Error("removing columns is a change")
	}
	want := []string{"Backlog|0|backlog|1", "To Do|1|todo|1", "In Progress|2|prog|1", "Done|3|done|1"}
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("column map = %v\nwant %v", got, want)
	}
	if n := e.int(`SELECT count(*) FROM standup_status_lane WHERE standup_id = ? AND status_id IN ('4','5')`, id); n != 0 {
		t.Errorf("status lanes of removed columns remain: %d", n)
	}
	if n := e.int(`SELECT count(*) FROM standup_status_lane WHERE standup_id = ?`, id); n != 4 {
		t.Errorf("status lanes = %d, want 4", n)
	}
}

func TestMergeColumns_AutoMapsNewColumnsAndKeepsExistingOnes(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	three := []w17Col{w17Board[0], w17Board[1], w17Board[5]}
	e.seedCache("B1", three)
	cols, byID, _ := w17Build(three)
	e.merge(id, cols, byID)
	e.exec(`UPDATE standup_column_map SET lane = 'hidden', auto = 0 WHERE standup_id = ? AND column_name = 'To Do'`, id)

	e.seedCache("B1", w17Board)
	cols2, byID2, _ := w17Build(w17Board)
	if changed := e.merge(id, cols2, byID2); !changed {
		t.Error("new columns are a change")
	}
	want := []string{"Backlog|0|backlog|1", "To Do|1|hidden|0", "In Progress|2|prog|1", "In Review|3|review|1", "QA|4|qa|1", "Done|5|done|1"}
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("column map = %v\nwant %v", got, want)
	}
	// hidden columns still get status-lane rows, with lane hidden
	if got := e.str(`SELECT lane FROM standup_status_lane WHERE standup_id = ? AND status_id = '2'`, id); got != "hidden" {
		t.Errorf("status 2 lane = %q, want hidden", got)
	}
}

func TestMergeColumns_ReevaluatesAutoMappedColumnsWhenTheirPlaceChanges(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	// "Shipped" is the last column → done
	before := []w17Col{
		{"To Do", []w17St{{"2", "To Do", "new"}}},
		{"Shipped", []w17St{{"7", "Shipped", "new"}}},
	}
	e.seedCache("B1", before)
	cols, byID, _ := w17Build(before)
	e.merge(id, cols, byID)
	if got := e.str(`SELECT lane FROM standup_column_map WHERE standup_id = ? AND column_name = 'Shipped'`, id); got != "done" {
		t.Fatalf("setup: Shipped = %q, want done (last column)", got)
	}
	// a column is appended after it: Shipped is no longer last, so it maps by category (all new → todo)
	after := append(append([]w17Col{}, before...), w17Col{"Archive", []w17St{{"8", "Archive", "done"}}})
	e.seedCache("B1", after)
	cols2, byID2, _ := w17Build(after)
	if changed := e.merge(id, cols2, byID2); !changed {
		t.Error("an auto lane that changes is a change")
	}
	if got := e.str(`SELECT lane || '|' || auto FROM standup_column_map WHERE standup_id = ? AND column_name = 'Shipped'`, id); got != "todo|1" {
		t.Errorf("Shipped = %q, want todo|1 (auto rows are re-evaluated)", got)
	}
}

func TestMergeColumns_OnlyTouchesTheGivenStandup(t *testing.T) {
	e := newW17(t)
	a := e.seedStandup("scrum")
	b := e.seedStandup("scrum")
	e.seedCache("B1", w17Board)
	cols, byID, _ := w17Build(w17Board)
	e.merge(a, cols, byID)
	if n := e.int(`SELECT count(*) FROM standup_column_map WHERE standup_id = ?`, b); n != 0 {
		t.Errorf("another standup's map gained %d rows", n)
	}
	if n := e.int(`SELECT count(*) FROM standup_status_lane WHERE standup_id = ?`, b); n != 0 {
		t.Errorf("another standup's status lanes gained %d rows", n)
	}
}

// ── RebuildStatusLanesTx ──

func TestRebuildStatusLanes_RewritesFromMapBoardColumnsAndStatusNames(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	e.seedCache("B1", w17Board)
	for i, c := range w17Board {
		lane := []string{"backlog", "todo", "prog", "review", "qa", "done"}[i]
		e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, ?, ?, ?, 1)`, id, c.Name, i, lane)
	}
	// a stale row for a status that is in no board column
	e.exec(`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (?, '99', 'Gone', 'Done', 'done')`, id)

	e.rebuild(id)
	if got := e.dump(w17StatusDump, id); !reflect.DeepEqual(got, w17BoardStatusLanes) {
		t.Errorf("status lanes = %v\nwant %v (stale row must go)", got, w17BoardStatusLanes)
	}

	// remap two columns in the map and rebuild again
	e.exec(`UPDATE standup_column_map SET lane = 'hidden' WHERE standup_id = ? AND column_name = 'Backlog'`, id)
	e.exec(`UPDATE standup_column_map SET lane = 'review' WHERE standup_id = ? AND column_name = 'QA'`, id)
	e.rebuild(id)
	want := []string{
		"1|Backlog|Backlog|hidden", "2|To Do|To Do|todo", "3|In Progress|In Progress|prog",
		"4|In Review|In Review|review", "5|In QA|QA|review", "6|Done|Done|done",
	}
	if got := e.dump(w17StatusDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("status lanes after remap = %v\nwant %v", got, want)
	}
}

func TestRebuildStatusLanes_ColumnWithSeveralStatuses(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	spec := []w17Col{
		{"In Progress", []w17St{{"3", "In Progress", "indeterminate"}, {"31", "Coding", "indeterminate"}, {"32", "Pairing", "indeterminate"}}},
	}
	e.seedCache("B1", spec)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'In Progress', 0, 'prog', 1)`, id)
	e.rebuild(id)
	want := []string{"3|In Progress|In Progress|prog", "31|Coding|In Progress|prog", "32|Pairing|In Progress|prog"}
	if got := e.dump(w17StatusDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("status lanes = %v, want %v", got, want)
	}
}

func TestRebuildStatusLanes_DoesNotTouchOtherStandups(t *testing.T) {
	e := newW17(t)
	a := e.seedStandup("scrum")
	b := e.seedStandup("scrum")
	e.seedCache("B1", w17Board)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'To Do', 1, 'todo', 1)`, b)
	e.exec(`INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (?, '2', 'To Do', 'To Do', 'todo')`, b)
	e.rebuild(a)
	if got := e.dump(w17StatusDump, b); !reflect.DeepEqual(got, []string{"2|To Do|To Do|todo"}) {
		t.Errorf("another standup's status lanes changed: %v", got)
	}
}

// ── SetColumnLane ──

func w17SeedMapped(e *w17Env) int64 {
	id := e.seedStandup("")
	cfg, statuses := w17Config("scrum", "scrum", w17Board)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		e.t.Fatalf("SetBoard: %v", err)
	}
	e.exec(`UPDATE standup_sync_state SET needs_sync = 0 WHERE standup_id = ?`, id)
	return id
}

func TestSetColumnLane_SetsChoiceRebuildsStatusLanesAndNeedsSync(t *testing.T) {
	e := newW17(t)
	id := w17SeedMapped(e)
	before := e.rev(id)
	e.clk.Advance(time.Minute)

	ack, err := e.s.SetColumnLane(e.ctx, id, "In Progress", "review")
	if err != nil {
		t.Fatalf("SetColumnLane: %v", err)
	}
	e.assertAck(id, before, ack)
	if !ack.NeedsSync || !e.needsSync(id) {
		t.Errorf("remapping a column is a source change: Ack.NeedsSync = %v, stored = %v", ack.NeedsSync, e.needsSync(id))
	}
	if got := e.str(`SELECT lane || '|' || auto FROM standup_column_map WHERE standup_id = ? AND column_name = 'In Progress'`, id); got != "review|0" {
		t.Errorf("In Progress = %q, want review|0 (user-chosen)", got)
	}
	// rebuilt in the same call: the status lane follows immediately
	if got := e.str(`SELECT lane FROM standup_status_lane WHERE standup_id = ? AND status_id = '3'`, id); got != "review" {
		t.Errorf("status 3 lane = %q, want review", got)
	}
	// nothing else moved
	want := []string{"Backlog|0|backlog|1", "To Do|1|todo|1", "In Progress|2|review|0", "In Review|3|review|1", "QA|4|qa|1", "Done|5|done|1"}
	if got := e.dump(w17MapDump, id); !reflect.DeepEqual(got, want) {
		t.Errorf("column map = %v\nwant %v", got, want)
	}
}

func TestSetColumnLane_AcceptsEveryLane(t *testing.T) {
	for _, lane := range []string{"backlog", "todo", "prog", "review", "qa", "done", "hidden"} {
		t.Run(lane, func(t *testing.T) {
			e := newW17(t)
			id := w17SeedMapped(e)
			if _, err := e.s.SetColumnLane(e.ctx, id, "To Do", lane); err != nil {
				t.Fatalf("SetColumnLane(%s): %v", lane, err)
			}
			if got := e.str(`SELECT lane FROM standup_status_lane WHERE standup_id = ? AND status_id = '2'`, id); got != lane {
				t.Errorf("status lane = %q, want %q", got, lane)
			}
		})
	}
}

func TestSetColumnLane_RejectsInvalidInput(t *testing.T) {
	cases := []struct{ name, column, lane, field string }{
		{"unknown_lane", "To Do", "bogus", "lane"},
		{"empty_lane", "To Do", "", "lane"},
		{"uppercase_lane", "To Do", "QA", "lane"},
		{"unknown_column", "No Such Column", "qa", "columnName"},
		{"empty_column", "", "qa", "columnName"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newW17(t)
			id := w17SeedMapped(e)
			before := e.rev(id)
			mapBefore := e.dump(w17MapDump, id)
			_, err := e.s.SetColumnLane(e.ctx, id, tc.column, tc.lane)
			w17RequireValidation(t, err, tc.field)
			if e.rev(id) != before || !reflect.DeepEqual(e.dump(w17MapDump, id), mapBefore) || e.needsSync(id) {
				t.Error("a rejected call must change nothing (rev, map, needs_sync)")
			}
		})
	}
}

func TestSetColumnLane_UnknownStandupIsNotFound(t *testing.T) {
	e := newW17(t)
	_, err := e.s.SetColumnLane(e.ctx, 999, "To Do", "qa")
	w17RequireCode(t, err, apperr.NotFound)
}

func TestSetColumnLane_ChoiceSurvivesTheNextColumnMerge(t *testing.T) {
	e := newW17(t)
	id := w17SeedMapped(e)
	if _, err := e.s.SetColumnLane(e.ctx, id, "Done", "hidden"); err != nil {
		t.Fatal(err)
	}
	cols, byID, _ := w17Build(w17Board)
	e.merge(id, cols, byID)
	if got := e.str(`SELECT lane || '|' || auto FROM standup_column_map WHERE standup_id = ? AND column_name = 'Done'`, id); got != "hidden|0" {
		t.Errorf("Done = %q, want hidden|0 after a merge", got)
	}
}

// ── UnmappedStatuses ──

func TestUnmappedStatuses_ListsStatusesOfUnmappedColumnsSorted(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("scrum")
	spec := []w17Col{
		{"To Do", []w17St{{"1", "Open", "new"}, {"2", "Reopened", "new"}}},
		{"Parking", []w17St{{"3", "Zeta", "new"}, {"4", "Alpha", "new"}}},
		{"Done", []w17St{{"6", "Closed", "done"}}},
	}
	e.seedCache("B1", spec)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'To Do', 0, 'todo', 1)`, id)
	e.exec(`INSERT INTO standup_column_map (standup_id, column_name, position, lane, auto) VALUES (?, 'Done', 3, 'done', 1)`, id)
	e.rebuild(id)

	got, err := e.s.UnmappedStatuses(e.ctx, id)
	if err != nil {
		t.Fatalf("UnmappedStatuses: %v", err)
	}
	if want := []string{"Alpha", "Zeta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("UnmappedStatuses = %v, want %v (sorted)", got, want)
	}
}

func TestUnmappedStatuses_NoneWhenEverythingIsMapped(t *testing.T) {
	e := newW17(t)
	id := w17SeedMapped(e)
	got, err := e.s.UnmappedStatuses(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("UnmappedStatuses = %v, want none", got)
	}
}

func TestUnmappedStatuses_DraftHasNone(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	got, err := e.s.UnmappedStatuses(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a draft has no board, so no unmapped statuses: %v", got)
	}
}

func TestDuplicate_CopiesColumnOrderAndRemovedSpeakers(t *testing.T) {
	e := newW17(t)
	id := e.seedStandup("")
	cfg, statuses := w17Config("scrum", "scrum", w17Board)
	if _, err := e.s.SetBoard(e.ctx, id, cfg, statuses); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetColumnLane(e.ctx, id, "In Review", "prog"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.MoveColumn(e.ctx, id, "In Review", 0); err != nil {
		t.Fatal(err)
	}
	e.exec(`INSERT INTO standup_speaker_removed (standup_id, account_id, removed_at) VALUES (?, 'acc-x', 1000)`, id)
	copyID, err := e.s.Duplicate(e.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	q := `SELECT column_name || '|' || COALESCE(lane_order, -1) FROM standup_column_map WHERE standup_id = ? AND lane = 'prog' ORDER BY column_name`
	if got, want := e.dump(q, copyID), e.dump(q, id); !reflect.DeepEqual(got, want) {
		t.Errorf("copy column order = %v, want %v", got, want)
	}
	if n := e.int(`SELECT count(*) FROM standup_speaker_removed WHERE standup_id = ? AND account_id = 'acc-x'`, copyID); n != 1 {
		t.Errorf("copy removed speakers = %d, want 1", n)
	}
}

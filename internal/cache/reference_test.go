package cache_test

// Tests for the autocomplete caches (space, board, person) and Trim in internal/cache (WP-20; data-model.md §2.4, §5).
//
// SPEC-NOTE: Search* return a non-nil empty slice when nothing matches. Results are ordered by name (plain ascending;
// the fixtures use capitalised names so case-folding does not matter). The query is a case-insensitive substring match:
// spaces on key OR name, boards and people on name (boards and people have no key; Jira account ids are opaque).
// SPEC-NOTE: SearchBoards(spaceKey="") searches every space; a non-empty spaceKey keeps boards whose space_id belongs
// to the cached space with that key (same connection).
// SPEC-NOTE: Board.Kind is read back as the stored raw type for "scrum" and "kanban" (the cache has no sprint support
// information to normalise "simple"), and RawType always round-trips. A board's Meta and a space's Meta round-trip
// through JSON.
// SPEC-NOTE: SearchPeople returns inactive people too (with Active=false); filtering is the caller's choice.
// SPEC-NOTE: Trim ages are strict: a row exactly 30 days old (space/board caches) is kept; one older is deleted.
// A space or board is "referenced" when any standup row, archived or not, carries its id (standup.space_id /
// standup.board_id).
// SPEC-NOTE: Trim "clears the payload" of a delivered outbox row by setting payload to '' (the column is NOT NULL); the
// row itself stays until 90 days after created_at (dead rows: 180 days).

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store"
)

// ---- spaces ---------------------------------------------------------------------------------------------------

func rdTwoConnections(t *testing.T, db *store.DB) {
	t.Helper()
	rdInsert(t, db, "connection", "id,base_url,account,created_at", 1, "https://acme.atlassian.net", "chris@example.com", rdMs("2026-02-01T10:00:00Z"))
	rdInsert(t, db, "connection", "id,base_url,account,created_at", 2, "https://other.atlassian.net", "o@example.com", rdMs("2026-02-01T10:00:00Z"))
}

func rdSpaceNames(ss []model.Space) []string {
	out := []string{}
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return out
}

func rdSeedSpaces(t *testing.T, db *store.DB) {
	t.Helper()
	at := rdTS("2026-03-18T12:00:00Z")
	if err := cache.UpsertSpaces(rdCtx, db.W, 1, []model.Space{
		{ID: "10", Key: "MOB", Name: "Service Desk", Meta: map[string]any{"style": "classic"}},
		{ID: "11", Key: "PAY", Name: "Payments Platform", Meta: map[string]any{"style": "next-gen"}},
		{ID: "12", Key: "PLAT", Name: "Operations"},
	}, at); err != nil {
		t.Fatalf("UpsertSpaces: %v", err)
	}
	if err := cache.UpsertSpaces(rdCtx, db.W, 2, []model.Space{{ID: "13", Key: "SVX", Name: "Service Other"}}, at); err != nil {
		t.Fatalf("UpsertSpaces(2): %v", err)
	}
}

func TestUpsertSpaces_RoundTripsThroughSearchSpaces_AndStampsFetchedAt(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedSpaces(t, db)

	got, err := cache.SearchSpaces(rdCtx, db.R, 1, "", 50)
	if err != nil {
		t.Fatalf("SearchSpaces: %v", err)
	}
	if want := []string{"Operations", "Payments Platform", "Service Desk"}; !reflect.DeepEqual(rdSpaceNames(got), want) {
		t.Fatalf("empty query = %v, want all of connection 1 by name %v", rdSpaceNames(got), want)
	}
	svc := got[2]
	if svc.ID != "10" || svc.Key != "MOB" || svc.Meta["style"] != "classic" {
		t.Errorf("Service Desk = %+v, want id 10, key MOB, meta style classic", svc)
	}
	if got[1].Meta["style"] != "next-gen" {
		t.Errorf("Payments Platform meta = %v", got[1].Meta)
	}
	if n := rdCount(t, db, "space", "fetched_at = ?", rdMs("2026-03-18T12:00:00Z")); n != 4 {
		t.Errorf("space rows stamped with fetched_at = at: %d, want 4", n)
	}
}

func TestUpsertSpaces_SameIDAgain_UpdatesInsteadOfDuplicating(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedSpaces(t, db)
	if err := cache.UpsertSpaces(rdCtx, db.W, 1, []model.Space{{ID: "10", Key: "SDK", Name: "Service Desk Kit", Meta: map[string]any{"style": "next-gen"}}}, rdTS("2026-03-19T12:00:00Z")); err != nil {
		t.Fatalf("UpsertSpaces: %v", err)
	}
	if n := rdCount(t, db, "space", "connection_id = 1"); n != 3 {
		t.Errorf("space rows of connection 1 = %d, want 3", n)
	}
	got, _ := cache.SearchSpaces(rdCtx, db.R, 1, "sdk", 10)
	if len(got) != 1 || got[0].ID != "10" || got[0].Name != "Service Desk Kit" || got[0].Meta["style"] != "next-gen" {
		t.Errorf("SearchSpaces(sdk) = %+v, want the renamed space", got)
	}
	if n := rdCount(t, db, "space", "id = '10' AND fetched_at = ?", rdMs("2026-03-19T12:00:00Z")); n != 1 {
		t.Errorf("fetched_at was not refreshed")
	}
}

func TestSearchSpaces_MatchesKeyOrNameCaseInsensitively(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedSpaces(t, db)
	cases := []struct {
		query string
		want  []string
	}{
		{"pay", []string{"Payments Platform"}},                             // key PAY, name Payments
		{"PAY", []string{"Payments Platform"}},                             // case-insensitive
		{"desk", []string{"Service Desk"}},                                 // name only
		{"op", []string{"Operations"}},                                     // key PLAT and name Operations
		{"s", []string{"Operations", "Payments Platform", "Service Desk"}}, // ordered by name
		{"ZZZ", []string{}},                                                // nothing, non-nil
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			defer rdRecover(t)
			got, err := cache.SearchSpaces(rdCtx, db.R, 1, c.query, 50)
			if err != nil {
				t.Fatalf("SearchSpaces: %v", err)
			}
			if got == nil {
				t.Fatalf("SearchSpaces returned a nil slice")
			}
			if !reflect.DeepEqual(rdSpaceNames(got), c.want) {
				t.Errorf("SearchSpaces(%q) = %v, want %v", c.query, rdSpaceNames(got), c.want)
			}
		})
	}
}

func TestSearchSpaces_LimitAndConnectionScope(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedSpaces(t, db)

	got, _ := cache.SearchSpaces(rdCtx, db.R, 1, "", 2)
	if want := []string{"Operations", "Payments Platform"}; !reflect.DeepEqual(rdSpaceNames(got), want) {
		t.Errorf("limit 2 = %v, want the first two by name %v", rdSpaceNames(got), want)
	}
	// "service" is in the name of connection 1's Service Desk and connection 2's Service Other: only its own.
	got, _ = cache.SearchSpaces(rdCtx, db.R, 1, "service", 10)
	if want := []string{"Service Desk"}; !reflect.DeepEqual(rdSpaceNames(got), want) {
		t.Errorf("connection 1 'service' = %v, want %v", rdSpaceNames(got), want)
	}
	got, _ = cache.SearchSpaces(rdCtx, db.R, 2, "service", 10)
	if want := []string{"Service Other"}; !reflect.DeepEqual(rdSpaceNames(got), want) {
		t.Errorf("connection 2 'service' = %v, want %v", rdSpaceNames(got), want)
	}
}

// ---- boards ---------------------------------------------------------------------------------------------------

func rdBoardNames(bs []model.Board) []string {
	out := []string{}
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

func rdSeedBoards(t *testing.T, db *store.DB) {
	t.Helper()
	rdSeedSpaces(t, db)
	at := rdTS("2026-03-18T12:00:00Z")
	if err := cache.UpsertBoards(rdCtx, db.W, 1, []model.Board{
		{ID: "1", Name: "Service Board", Kind: "scrum", RawType: "scrum", SpaceID: "10", Meta: map[string]any{"filterId": float64(9001), "subQuery": "project = MOB"}},
		{ID: "2", Name: "Payments Kanban", Kind: "kanban", RawType: "kanban", SpaceID: "11"},
		{ID: "3", Name: "Ops Simple", Kind: "scrum", RawType: "simple", SpaceID: "12"},
		{ID: "4", Name: "Service Backlog", Kind: "scrum", RawType: "scrum", SpaceID: "10"},
	}, at); err != nil {
		t.Fatalf("UpsertBoards: %v", err)
	}
	if err := cache.UpsertBoards(rdCtx, db.W, 2, []model.Board{{ID: "5", Name: "Service Other Board", Kind: "scrum", RawType: "scrum", SpaceID: "13"}}, at); err != nil {
		t.Fatalf("UpsertBoards(2): %v", err)
	}
}

func TestUpsertBoards_RoundTripsThroughSearchBoards(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedBoards(t, db)

	got, err := cache.SearchBoards(rdCtx, db.R, 1, "", "", 50)
	if err != nil {
		t.Fatalf("SearchBoards: %v", err)
	}
	if want := []string{"Ops Simple", "Payments Kanban", "Service Backlog", "Service Board"}; !reflect.DeepEqual(rdBoardNames(got), want) {
		t.Fatalf("all boards of connection 1 = %v, want %v", rdBoardNames(got), want)
	}
	byName := map[string]model.Board{}
	for _, b := range got {
		byName[b.Name] = b
	}
	sb := byName["Service Board"]
	if sb.ID != "1" || sb.Kind != "scrum" || sb.RawType != "scrum" || sb.SpaceID != "10" {
		t.Errorf("Service Board = %+v", sb)
	}
	if !reflect.DeepEqual(sb.Meta, map[string]any{"filterId": float64(9001), "subQuery": "project = MOB"}) {
		t.Errorf("Service Board Meta = %v, want the filterId/subQuery map", sb.Meta)
	}
	if pk := byName["Payments Kanban"]; pk.Kind != "kanban" || pk.RawType != "kanban" || pk.SpaceID != "11" {
		t.Errorf("Payments Kanban = %+v", pk)
	}
	if os := byName["Ops Simple"]; os.RawType != "simple" {
		t.Errorf("Ops Simple RawType = %q, want simple", os.RawType)
	}
	if n := rdCount(t, db, "board", "type = 'simple'"); n != 1 {
		t.Errorf("the raw provider type must be stored in board.type (rows with 'simple': %d)", n)
	}
}

func TestUpsertBoards_SameIDAgain_Updates(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedBoards(t, db)
	if err := cache.UpsertBoards(rdCtx, db.W, 1, []model.Board{{ID: "1", Name: "Service Board 2", Kind: "scrum", RawType: "scrum", SpaceID: "10"}}, rdTS("2026-03-19T12:00:00Z")); err != nil {
		t.Fatalf("UpsertBoards: %v", err)
	}
	if n := rdCount(t, db, "board", "connection_id = 1"); n != 4 {
		t.Errorf("board rows of connection 1 = %d, want 4", n)
	}
	got, _ := cache.SearchBoards(rdCtx, db.R, 1, "", "board 2", 10)
	if want := []string{"Service Board 2"}; !reflect.DeepEqual(rdBoardNames(got), want) {
		t.Errorf("SearchBoards(board 2) = %v, want %v", rdBoardNames(got), want)
	}
}

func TestSearchBoards_FiltersBySpaceKeyAndNameCaseInsensitively(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedBoards(t, db)
	cases := []struct {
		name, spaceKey, query string
		want                  []string
	}{
		{"space only", "MOB", "", []string{"Service Backlog", "Service Board"}},
		{"another space", "PAY", "", []string{"Payments Kanban"}},
		{"space and name", "MOB", "BOARD", []string{"Service Board"}},
		{"space excludes name match", "MOB", "kanban", []string{}},
		{"any space, name", "", "service", []string{"Service Backlog", "Service Board"}},
		{"any space, case", "", "KANBAN", []string{"Payments Kanban"}},
		{"unknown space", "NOPE", "", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer rdRecover(t)
			got, err := cache.SearchBoards(rdCtx, db.R, 1, c.spaceKey, c.query, 50)
			if err != nil {
				t.Fatalf("SearchBoards: %v", err)
			}
			if got == nil {
				t.Fatalf("SearchBoards returned a nil slice")
			}
			if !reflect.DeepEqual(rdBoardNames(got), c.want) {
				t.Errorf("SearchBoards(%q, %q) = %v, want %v", c.spaceKey, c.query, rdBoardNames(got), c.want)
			}
		})
	}
}

func TestSearchBoards_LimitAndConnectionScope(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedBoards(t, db)
	got, _ := cache.SearchBoards(rdCtx, db.R, 1, "", "", 1)
	if want := []string{"Ops Simple"}; !reflect.DeepEqual(rdBoardNames(got), want) {
		t.Errorf("limit 1 = %v, want %v", rdBoardNames(got), want)
	}
	got, _ = cache.SearchBoards(rdCtx, db.R, 2, "", "", 10)
	if want := []string{"Service Other Board"}; !reflect.DeepEqual(rdBoardNames(got), want) {
		t.Errorf("connection 2 boards = %v, want %v", rdBoardNames(got), want)
	}
}

// ---- people ---------------------------------------------------------------------------------------------------

func rdPersonNames(ps []model.Person) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.DisplayName)
	}
	return out
}

func rdSeedPeople(t *testing.T, db *store.DB) {
	t.Helper()
	at := rdTS("2026-03-18T12:00:00Z")
	if err := cache.UpsertPeople(rdCtx, db.W, 1, []model.Person{
		{AccountID: "acc-ann", DisplayName: "Ann Lee", Active: true, Kind: "user"},
		{AccountID: "acc-bob", DisplayName: "Bob Ray", Active: true, Kind: "user"},
		{AccountID: "acc-bot", DisplayName: "Deploy Bot", Active: true, Kind: "app"},
		{AccountID: "acc-han", DisplayName: "Hannah Park", Active: true, Kind: "user"},
		{AccountID: "acc-zed", DisplayName: "Zed Old", Active: false, Kind: "user"},
	}, at); err != nil {
		t.Fatalf("UpsertPeople: %v", err)
	}
	if err := cache.UpsertPeople(rdCtx, db.W, 2, []model.Person{{AccountID: "acc-oth", DisplayName: "Ann Other", Active: true, Kind: "user"}}, at); err != nil {
		t.Fatalf("UpsertPeople(2): %v", err)
	}
}

func TestUpsertPeople_RoundTripsThroughSearchPeople(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedPeople(t, db)

	got, err := cache.SearchPeople(rdCtx, db.R, 1, "", 50)
	if err != nil {
		t.Fatalf("SearchPeople: %v", err)
	}
	if want := []string{"Ann Lee", "Bob Ray", "Deploy Bot", "Hannah Park", "Zed Old"}; !reflect.DeepEqual(rdPersonNames(got), want) {
		t.Fatalf("empty query = %v, want all of connection 1 by name %v", rdPersonNames(got), want)
	}
	want := map[string]model.Person{
		"Ann Lee":    {AccountID: "acc-ann", DisplayName: "Ann Lee", Active: true, Kind: "user"},
		"Deploy Bot": {AccountID: "acc-bot", DisplayName: "Deploy Bot", Active: true, Kind: "app"},
		"Zed Old":    {AccountID: "acc-zed", DisplayName: "Zed Old", Active: false, Kind: "user"},
	}
	for _, p := range got {
		if w, ok := want[p.DisplayName]; ok && p != w {
			t.Errorf("%s = %+v, want %+v", p.DisplayName, p, w)
		}
	}
}

func TestUpsertPeople_SameAccountAgain_UpdatesDisplayNameAndActive(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedPeople(t, db)
	if err := cache.UpsertPeople(rdCtx, db.W, 1, []model.Person{{AccountID: "acc-zed", DisplayName: "Zed Reborn", Active: true, Kind: "user"}}, rdTS("2026-03-19T12:00:00Z")); err != nil {
		t.Fatalf("UpsertPeople: %v", err)
	}
	if n := rdCount(t, db, "person", "connection_id = 1"); n != 5 {
		t.Errorf("person rows of connection 1 = %d, want 5", n)
	}
	got, _ := cache.SearchPeople(rdCtx, db.R, 1, "reborn", 10)
	if len(got) != 1 || got[0].AccountID != "acc-zed" || !got[0].Active {
		t.Errorf("SearchPeople(reborn) = %+v, want acc-zed active", got)
	}
}

func TestSearchPeople_MatchesDisplayNameCaseInsensitively_LimitAndConnectionScope(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdTwoConnections(t, db)
	rdSeedPeople(t, db)
	cases := []struct {
		query string
		limit int
		want  []string
	}{
		{"ann", 50, []string{"Ann Lee", "Hannah Park"}}, // substring, by name, not connection 2's "Ann Other"
		{"BOB", 50, []string{"Bob Ray"}},
		{"bot", 50, []string{"Deploy Bot"}},
		{"", 2, []string{"Ann Lee", "Bob Ray"}},
		{"ann", 1, []string{"Ann Lee"}},
		{"nobody", 50, []string{}},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			defer rdRecover(t)
			got, err := cache.SearchPeople(rdCtx, db.R, 1, c.query, c.limit)
			if err != nil {
				t.Fatalf("SearchPeople: %v", err)
			}
			if got == nil {
				t.Fatalf("SearchPeople returned a nil slice")
			}
			if !reflect.DeepEqual(rdPersonNames(got), c.want) {
				t.Errorf("SearchPeople(%q, %d) = %v, want %v", c.query, c.limit, rdPersonNames(got), c.want)
			}
		})
	}
}

// ---- Trim -----------------------------------------------------------------------------------------------------

func rdAgo(d time.Duration) int64 { return rdNow.Add(-d).UnixMilli() }

const rdDay = 24 * time.Hour

// rdSeedOperational adds operational rows on top of rdSeedAll (which already holds sync runs 1–4 for standups A/B).
//
//	space  '100' (referenced by the standups) 40d old → kept; 's-old' 30d+1s → deleted; 's-edge' exactly 30d → kept; 's-new' 5d → kept
//	board  '77' (referenced) 45d old with columns → kept; 'b-old' 31d with columns → deleted; 'b-new' 2d → kept
//	run    standup A: runs 101..160 started 72h+1h … 72h+60h ago (+ fixture runs 1,2,3 → 63 runs; the newest 50 stay)
//	       standup B: run 200 is 91d old (deleted), run 201 is 89d old (kept), fixture run 4 stays
//	outbox see rdSeedOutbox
func rdSeedOperational(t *testing.T, db *store.DB) {
	t.Helper()
	sp := "connection_id,id,key,name,fetched_at"
	rdInsert(t, db, "space", sp, 1, "100", "MOB", "Service", rdAgo(40*rdDay))
	rdInsert(t, db, "space", sp, 1, "s-old", "OLD", "Old space", rdAgo(30*rdDay+time.Second))
	rdInsert(t, db, "space", sp, 1, "s-edge", "EDG", "Edge space", rdAgo(30*rdDay))
	rdInsert(t, db, "space", sp, 1, "s-new", "NEW", "New space", rdAgo(5*rdDay))

	bd := "connection_id,id,name,type,space_id,fetched_at"
	rdInsert(t, db, "board", bd, 1, "77", "MOB board", "scrum", "100", rdAgo(45*rdDay))
	rdInsert(t, db, "board", bd, 1, "b-old", "Old board", "scrum", "s-old", rdAgo(31*rdDay))
	rdInsert(t, db, "board", bd, 1, "b-new", "New board", "kanban", "s-new", rdAgo(2*rdDay))
	for _, b := range []string{"77", "b-old"} {
		rdInsert(t, db, "board_column", "connection_id,board_id,position,name", 1, b, 0, "To Do")
		rdInsert(t, db, "board_column", "connection_id,board_id,position,name", 1, b, 1, "Done")
		rdInsert(t, db, "board_column_status", "connection_id,board_id,position,status_id", 1, b, 0, "11")
		rdInsert(t, db, "board_column_status", "connection_id,board_id,position,status_id", 1, b, 1, "16")
	}

	rc := "id,standup_id,mode,trigger,status,started_at,finished_at"
	for i := 1; i <= 60; i++ {
		started := rdAgo(72*time.Hour + time.Duration(i)*time.Hour)
		rdInsert(t, db, "sync_run", rc, 100+i, rdSA, "incremental", "manual", "ok", started, started+1000)
	}
	rdInsert(t, db, "sync_run", rc, 200, rdSB, "full", "manual", "ok", rdAgo(91*rdDay), rdAgo(91*rdDay)+1000)
	rdInsert(t, db, "sync_run", rc, 201, rdSB, "full", "manual", "ok", rdAgo(89*rdDay), rdAgo(89*rdDay)+1000)
	for _, id := range []int{101, 160, 200} {
		rdInsert(t, db, "sync_log", "run_id,at,level,phase,message", id, rdAgo(100*time.Hour), "info", "scan", "a line")
	}
}

// rdSeedOutbox inserts sink 1 and seven outbox rows (standup A). Keys are o1…o7.
//
//	o1 delivered 31d ago (created 32d)  → payload cleared, row kept
//	o2 delivered 29d ago                → untouched
//	o3 delivered, created 91d ago       → deleted
//	o4 failed, created 91d ago          → deleted
//	o5 dead, created 100d ago           → kept (dead rows live 180 days), payload kept
//	o6 dead, created 181d ago           → deleted
//	o7 pending, created 10d ago         → untouched
func rdSeedOutbox(t *testing.T, db *store.DB) {
	t.Helper()
	rdInsert(t, db, "sink", "id,kind,name,created_at,updated_at", 1, "file", "Folder", rdAgo(200*rdDay), rdAgo(200*rdDay))
	oc := "sink_id,standup_id,cause,payload_kind,as_of,idempotency_key,payload,status,attempts,created_at,updated_at,delivered_at"
	row := func(key, status string, created, delivered any, attempts int) {
		rdInsert(t, db, "sink_outbox", oc, 1, rdSA, "manual", "current", rdAgo(10*rdDay), key, `{"schema":"cuisle.export.v1","key":"`+key+`"}`, status, attempts, created, created, delivered)
	}
	row("o1", "delivered", rdAgo(32*rdDay), rdAgo(31*rdDay), 1)
	row("o2", "delivered", rdAgo(30*rdDay), rdAgo(29*rdDay), 1)
	row("o3", "delivered", rdAgo(91*rdDay), rdAgo(91*rdDay), 1)
	row("o4", "failed", rdAgo(91*rdDay), nil, 3)
	row("o5", "dead", rdAgo(100*rdDay), nil, 8)
	row("o6", "dead", rdAgo(181*rdDay), nil, 8)
	row("o7", "pending", rdAgo(10*rdDay), nil, 0)
}

func rdStrings(t *testing.T, db *store.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.R.QueryContext(rdCtx, query, args...)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func rdTrim(t *testing.T, db *store.DB) {
	t.Helper()
	if err := cache.Trim(rdCtx, db.W, rdNow); err != nil {
		t.Fatalf("Trim: %v", err)
	}
}

func TestTrim_DeletesOldUnreferencedSpaceAndBoardCaches(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	rdSeedOperational(t, db)
	rdTrim(t, db)

	if got, want := rdStrings(t, db, "SELECT id FROM space ORDER BY id"), []string{"100", "s-edge", "s-new"}; !reflect.DeepEqual(got, want) {
		t.Errorf("spaces after Trim = %v, want %v (s-old is >30d and unreferenced; '100' is old but referenced; s-edge is exactly 30d)", got, want)
	}
	if got, want := rdStrings(t, db, "SELECT id FROM board ORDER BY id"), []string{"77", "b-new"}; !reflect.DeepEqual(got, want) {
		t.Errorf("boards after Trim = %v, want %v (b-old is >30d and unreferenced; '77' is old but referenced)", got, want)
	}
	if n := rdCount(t, db, "board_column", "board_id = 'b-old'"); n != 0 {
		t.Errorf("board_column rows of the deleted board = %d, want 0", n)
	}
	if n := rdCount(t, db, "board_column_status", "board_id = 'b-old'"); n != 0 {
		t.Errorf("board_column_status rows of the deleted board = %d, want 0", n)
	}
	if n := rdCount(t, db, "board_column", "board_id = '77'"); n != 2 {
		t.Errorf("board_column rows of the kept board = %d, want 2", n)
	}
	if n := rdCount(t, db, "board_column_status", "board_id = '77'"); n != 2 {
		t.Errorf("board_column_status rows of the kept board = %d, want 2", n)
	}
}

func TestTrim_KeepsNewest50RunsPerStandupAndNothingOlderThan90Days(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	rdSeedOperational(t, db)
	if n := rdCount(t, db, "sync_run", "standup_id = ?", rdSA); n != 63 {
		t.Fatalf("precondition: standup A has %d runs, want 63", n)
	}
	rdTrim(t, db)

	if n := rdCount(t, db, "sync_run", "standup_id = ?", rdSA); n != 50 {
		t.Errorf("runs of standup A after Trim = %d, want exactly the newest 50", n)
	}
	// fixture runs 1,2,3 are the newest; of the extras 101..147 are newer than 148..160
	if n := rdCount(t, db, "sync_run", "id IN (1,2,3)"); n != 3 {
		t.Errorf("fixture runs 1-3 kept = %d, want 3", n)
	}
	if n := rdCount(t, db, "sync_run", "id BETWEEN 101 AND 147"); n != 47 {
		t.Errorf("extra runs 101..147 kept = %d, want 47", n)
	}
	if n := rdCount(t, db, "sync_run", "id BETWEEN 148 AND 160"); n != 0 {
		t.Errorf("extra runs 148..160 kept = %d, want 0 (beyond the newest 50)", n)
	}
	// standup B: the 91-day-old run goes although B has far fewer than 50 runs; the 89-day-old one stays.
	if got, want := rdStrings(t, db, "SELECT CAST(id AS TEXT) FROM sync_run WHERE standup_id = ? ORDER BY id", rdSB), []string{"201", "4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("runs of standup B after Trim = %v, want %v", got, want)
	}
}

func TestTrim_DeletesTheLogLinesOfDeletedRunsOnly(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	rdSeedOperational(t, db)
	rdTrim(t, db)

	if n := rdCount(t, db, "sync_log", "run_id = 2"); n != 2 {
		t.Errorf("log lines of kept run 2 = %d, want 2", n)
	}
	if n := rdCount(t, db, "sync_log", "run_id = 101"); n != 1 {
		t.Errorf("log lines of kept run 101 = %d, want 1", n)
	}
	if n := rdCount(t, db, "sync_log", "run_id IN (160, 200)"); n != 0 {
		t.Errorf("log lines of deleted runs 160 and 200 = %d, want 0", n)
	}
}

func TestTrim_SinkOutbox_ClearsPayloadAfter30DaysAndDeletesOldRows(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	rdSeedOutbox(t, db)
	rdTrim(t, db)

	if got, want := rdStrings(t, db, "SELECT idempotency_key FROM sink_outbox ORDER BY idempotency_key"), []string{"o1", "o2", "o5", "o7"}; !reflect.DeepEqual(got, want) {
		t.Errorf("outbox rows after Trim = %v, want %v (o3, o4 older than 90d; o6 dead older than 180d)", got, want)
	}
	payload := func(key string) string {
		var p string
		if err := db.R.QueryRowContext(rdCtx, "SELECT payload FROM sink_outbox WHERE idempotency_key = ?", key).Scan(&p); err != nil {
			t.Fatalf("read payload of %s: %v", key, err)
		}
		return p
	}
	if p := payload("o1"); p != "" {
		t.Errorf("o1 (delivered 31d ago) payload = %q, want it cleared to ''", p)
	}
	for _, k := range []string{"o2", "o5", "o7"} {
		if p := payload(k); p == "" {
			t.Errorf("%s payload was cleared, want it kept", k)
		}
	}
	// the cleared row keeps its bookkeeping
	if n := rdCount(t, db, "sink_outbox", "idempotency_key = 'o1' AND status = 'delivered' AND delivered_at IS NOT NULL"); n != 1 {
		t.Errorf("o1 lost its status or delivered_at")
	}
}

func TestTrim_NeverTouchesRecordConfigOrArchivedStandupTables(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	rdSeedOperational(t, db)
	rdSeedOutbox(t, db)

	tables := []string{
		// record tables (data-model §1)
		"issue", "status_change", "assignee_change", "flag_change", "comment", "dev_commit", "dev_pr_event",
		"standup_issue", "standup_issue_span", "standup_config_history", "person", "sprint", "status", "issue_type",
		// config tables, including the archived standup D
		"connection", "standup", "standup_settings", "standup_lane", "standup_column_map", "standup_status_lane",
		"standup_speaker", "standup_sync_state", "sink", "app_setting", "ui_state",
	}
	counts := map[string]int{}
	sums := map[string]string{}
	for _, tb := range tables {
		counts[tb] = rdCount(t, db, tb, "")
		sums[tb] = rdChecksum(t, db, tb)
	}
	if counts["issue"] == 0 || counts["comment"] == 0 || counts["standup_issue_span"] == 0 {
		t.Fatalf("precondition: the fixture must hold record rows (counts %v)", counts)
	}

	rdTrim(t, db)

	for _, tb := range tables {
		if got := rdCount(t, db, tb, ""); got != counts[tb] {
			t.Errorf("Trim changed %s: %d rows before, %d after", tb, counts[tb], got)
		}
		if got := rdChecksum(t, db, tb); got != sums[tb] {
			t.Errorf("Trim changed the content of %s", tb)
		}
	}
}

func TestTrim_ArchivedStandupKeepsItsRowSettingsSpansAndConfigHistory(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	rdSeedOperational(t, db)
	rdTrim(t, db)

	if n := rdCount(t, db, "standup", "id = ? AND deleted_at IS NOT NULL", rdSD); n != 1 {
		t.Errorf("archived standup row survived: %d, want 1", n)
	}
	if n := rdCount(t, db, "standup_settings", "standup_id = ?", rdSD); n != 1 {
		t.Errorf("archived standup settings = %d, want 1", n)
	}
	if n := rdCount(t, db, "standup_issue_span", "standup_id = ?", rdSD); n != 1 {
		t.Errorf("archived standup spans = %d, want 1", n)
	}
	if n := rdCount(t, db, "standup_config_history", "standup_id = ?", rdSD); n != 1 {
		t.Errorf("archived standup config history = %d, want 1", n)
	}
}

func TestTrim_IsIdempotent(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdSeedAll(t, db)
	rdSeedOperational(t, db)
	rdSeedOutbox(t, db)
	rdTrim(t, db)
	ops := []string{"space", "board", "board_column", "board_column_status", "sync_run", "sync_log", "sink_outbox"}
	first := map[string]string{}
	for _, tb := range ops {
		first[tb] = rdChecksum(t, db, tb)
	}
	rdTrim(t, db)
	for _, tb := range ops {
		if got := rdChecksum(t, db, tb); got != first[tb] {
			t.Errorf("a second Trim changed %s", tb)
		}
	}
}

func TestTrim_EmptyDatabase_Succeeds(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	if err := cache.Trim(rdCtx, db.W, rdNow); err != nil {
		t.Fatalf("Trim on an empty DB: %v", err)
	}
}

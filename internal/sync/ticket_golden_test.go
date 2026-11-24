package sync

// The end-to-end ticket golden of WP-T6 (testing.md "Golden ticket", F12-freeze decision 7): MOB-1421 of the default
// fake-Jira fixture, synced through the real stack (jiratest, the Jira adapter, the orchestrator, the store, cache.LoadTicket)
// and built by view.BuildTicketWith, is written to internal/app/testdata/ticket_mob1421.golden.json
// (go test ./internal/sync -run TestTicketGolden -update) and compared with the file on every run.
//
// The frozen mock fixture frontend/src/api/fixtures/ticket_mob1421.json was hand-built for the mockups and describes a
// different MOB-1421 than the fake Jira's content overlay (other links, subtasks, labels, people, field names). The two
// cannot be equal. TestTicketGolden_ShapeMatchesTheMockFixture therefore asserts what must agree (every key at every path,
// null versus list, per field of every object) and logs the data differences; docs/wp-notes/WP-T6.md lists them.
//
// SPEC-NOTE: the fake media store decodes the PNGs it is handed (as internal/media does), so the golden carries real
// sizes and "full" cache states for the three images; the pdf is metadata only ("none").
// SPEC-NOTE: the golden normalises only the fake server's base URL (a random port) to https://example.atlassian.net.
// Everything else (ids, times, the viewer's clock Monday 2026-11-23 16:00 America/Chicago) is deterministic.

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/view"
)

var update = flag.Bool("update", false, "rewrite internal/app/testdata/ticket_mob1421.golden.json")

const (
	goldenPath  = "../app/testdata/ticket_mob1421.golden.json"
	mockFixture = "../../frontend/src/api/fixtures/ticket_mob1421.json"
	fakeBase    = "https://example.atlassian.net"
)

// buildTicketDetail reads the ticket and the standup snapshot in one reader transaction (as TicketService.Get will) and
// builds the modal payload.
func buildTicketDetail(t *testing.T, e *env, key string) view.TicketDetail {
	t.Helper()
	d, _ := buildTicketAndBoard(t, e, key, 0)
	return d
}

// buildTicketAndBoard builds the modal payload and, from the very same snapshot, the board view (view.Build). pulseDays
// overrides the standup's PulseDays (0 = as configured); the snapshot's history is windowed by it.
func buildTicketAndBoard(t *testing.T, e *env, key string, pulseDays int) (view.TicketDetail, view.StandupView) {
	t.Helper()
	ctx := context.Background()
	cfg, err := settings.New(e.db, e.clock.Now).Effective(ctx, e.sid)
	if err != nil {
		t.Fatalf("Effective: %v", err)
	}
	if pulseDays > 0 {
		cfg.PulseDays = pulseDays
	}
	asOf := e.clock.Now().In(e.clock.Location())
	var snap model.BoardSnapshot
	var tk cache.Ticket
	var inUse map[string]bool
	err = e.db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		if snap, err = cache.LoadCurrentTx(ctx, tx, cfg, asOf); err != nil {
			return err
		}
		if tk, err = cache.LoadTicketTx(ctx, tx, e.issueID(key)); err != nil {
			return err
		}
		inUse, err = cache.FieldsInUseTx(ctx, tx, tk.LocalID)
		return err
	})
	if err != nil {
		t.Fatalf("load %s: %v", key, err)
	}
	snap.IssueURLPrefix = source.IssueURLPrefix(e.opened[0].Conn.Source, e.srv.URL)

	caps := oiOpen(t, e).Capabilities().Writes
	d := view.BuildTicketWith(snap, tk, view.TicketFieldPrefs{}, asOf, view.TicketOptions{
		SelfAccountID: "u-me", AuthState: "ok", FieldsInUse: inUse,
		Writes: view.TicketWrites{Transition: caps.Transition, Assign: caps.Assign, Flag: caps.Flag, Comment: caps.Comment},
	})
	return d, view.Build(snap, asOf)
}

func goldenEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, envOpts{fx: defaultFixture(t)})
	newFakeMedia(e)
	e.mustOK(e.sync(ModeFull))
	mediaDone(e)
	return e
}

func normalised(t *testing.T, e *env, d view.TicketDetail) []byte {
	t.Helper()
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(b), e.srv.URL, fakeBase) + "\n")
}

func TestTicketGolden_MOB1421(t *testing.T) {
	defer recoverStub(t)
	e := goldenEnv(t)
	got := normalised(t, e, buildTicketDetail(t, e, "MOB-1421"))
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden missing (%v): run `go test ./internal/sync -run TestTicketGolden -update`, review the diff and commit it", err)
	}
	if string(got) != string(want) {
		t.Errorf("the MOB-1421 ticket differs from %s (review with -update)\n%s", goldenPath, firstDiff(string(want), string(got)))
	}
}

func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("line %d:\n want %s\n  got %s", i+1, w, g)
		}
	}
	return ""
}

// The golden is what the screens render, so it must tell the truth about the synced data.
func TestTicketGolden_ContentOfMOB1421(t *testing.T) {
	defer recoverStub(t)
	e := goldenEnv(t)
	d := buildTicketDetail(t, e, "MOB-1421")
	if d.Row.Key != "MOB-1421" || d.Row.Lane != model.LaneProg || d.Row.DaysInLane != 2 || d.Row.Blocked {
		t.Errorf("row = %+v", d.Row)
	}
	if d.Description == nil || len(d.Description.Nodes) < 20 {
		t.Fatalf("description nodes = %v", d.Description)
	}
	if len(d.Attachments) != 4 || len(d.Subtasks) != 3 || len(d.LinkGroups) != 3 {
		t.Errorf("attachments %d subtasks %d link groups %d, want 4/3/3", len(d.Attachments), len(d.Subtasks), len(d.LinkGroups))
	}
	cached := map[string]string{}
	for _, a := range d.Attachments {
		cached[a.Name] = a.Cached
		if a.Image && (a.Width == 0 || a.Height == 0) {
			t.Errorf("%s has no decoded size", a.Name)
		}
	}
	if cached["unit-toggle-v3.png"] != "full" || cached["release-notes.pdf"] != "none" {
		t.Errorf("cached states = %v", cached)
	}
	var restricted int
	for _, c := range d.Comments {
		if c.Visibility == "Developers only" {
			restricted++
		}
	}
	if restricted != 1 || len(d.Comments) < 3 {
		t.Errorf("comments = %d restricted = %d", len(d.Comments), restricted)
	}
	if got := strings.Join(groupIDsOf(d), ","); got != "state,details,custom,development,dates" {
		t.Errorf("rail groups = %s", got)
	}
	if !d.Can.Transition || !d.Can.Assign || !d.Can.Flag || !d.Can.Comment || d.Can.Reason != "" {
		t.Errorf("can = %+v", d.Can)
	}
	// The overlay's deliberately dangling media id still resolves, by the node's alt name (jira-sync §2.14 step 3).
	if len(d.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", d.Warnings)
	}
	if d.Dev == nil || d.Dev.Commits == 0 {
		t.Errorf("dev = %+v, want the fixture's commits and PRs", d.Dev)
	}
}

func groupIDsOf(d view.TicketDetail) []string {
	var out []string
	for _, g := range d.Rail {
		out = append(out, g.ID)
	}
	return out
}

// The modal's header row is the board row. Through the real stack the two come from different loaders (the standup's
// windowed history versus the ticket's complete history), so equality here is what keeps them honest; it also backs the
// note that the board shows 2 days in lane for MOB-1421 where the hand-built mock says 3.
func TestTicketGolden_RowEqualsTheBoardRowThroughTheRealStack(t *testing.T) {
	defer recoverStub(t)
	e := goldenEnv(t)
	_, board := buildTicketAndBoard(t, e, "MOB-1421", view.TicketPulseDays)
	if len(board.Tickets) < 20 {
		t.Fatalf("board has %d tickets", len(board.Tickets))
	}
	for _, row := range board.Tickets {
		d, _ := buildTicketAndBoard(t, e, row.Key, view.TicketPulseDays)
		if !reflect.DeepEqual(d.Row, row) {
			gj, _ := json.Marshal(d.Row)
			wj, _ := json.Marshal(row)
			t.Errorf("%s: modal row differs from the board row\n modal %s\n board %s", row.Key, gj, wj)
		}
		if row.Key == "MOB-1421" && row.DaysInLane != 2 {
			t.Errorf("board days in lane for MOB-1421 = %d, want 2", row.DaysInLane)
		}
	}
}

func TestTicketGolden_OpeningATicketMakesNoRequestAndBuildsEveryIssue(t *testing.T) {
	defer recoverStub(t)
	e := goldenEnv(t)
	e.srv.ResetRequests()
	keys := e.qCol(`SELECT key FROM issue ORDER BY id`)
	if len(keys) < 50 {
		t.Fatalf("only %d issues", len(keys))
	}
	for _, k := range keys {
		d := buildTicketDetail(t, e, k)
		if d.Row.Key != k || d.Rail == nil || d.Comments == nil || d.Warnings == nil {
			t.Fatalf("%s: incomplete detail", k)
		}
		if _, err := json.Marshal(d); err != nil {
			t.Fatalf("%s: %v", k, err)
		}
	}
	if n := len(e.requests()); n != 0 {
		t.Errorf("building %d tickets made %d requests, want none", len(keys), n)
	}
}

// ---- shape comparison with the frozen mock fixture ---------------------------------------------------------------

// shapeOf maps every path of a JSON document to the set of kinds seen there. Documents the ADF renderer owns
// (description, comment bodies, rich values) are recorded as one leaf: their node shape is the adf package's contract.
func shapeOf(v any, path string, out map[string]map[string]bool) {
	add := func(kind string) {
		if out[path] == nil {
			out[path] = map[string]bool{}
		}
		out[path][kind] = true
	}
	switch x := v.(type) {
	case nil:
		add("null")
	case bool:
		add("bool")
	case float64:
		add("number")
	case string:
		add("string")
	case []any:
		add("array")
		for _, el := range x {
			shapeOf(el, path+"[]", out)
		}
	case map[string]any:
		add("object")
		for k, el := range x {
			if k == "description" || k == "body" || k == "rich" {
				if el == nil {
					shapeOf(el, path+"."+k, out)
				} else {
					shapeOf(map[string]any{}, path+"."+k, out) // an adf.Doc object
				}
				continue
			}
			shapeOf(el, path+"."+k, out)
		}
	}
}

func kinds(m map[string]bool) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, "|")
}

func TestTicketGolden_ShapeMatchesTheMockFixture(t *testing.T) {
	defer recoverStub(t)
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Skipf("golden missing: %v", err)
	}
	mock, err := os.ReadFile(mockFixture)
	if err != nil {
		t.Fatalf("mock fixture: %v", err)
	}
	var g, m any
	if err := json.Unmarshal(golden, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mock, &m); err != nil {
		t.Fatal(err)
	}
	gs, ms := map[string]map[string]bool{}, map[string]map[string]bool{}
	shapeOf(g, "", gs)
	shapeOf(m, "", ms)

	var paths []string
	seen := map[string]bool{}
	for p := range gs {
		seen[p] = true
		paths = append(paths, p)
	}
	for p := range ms {
		if !seen[p] {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		gk, gok := gs[p]
		mk, mok := ms[p]
		switch {
		case !gok:
			if !underNull(gs, p) {
				t.Errorf("path %s exists in the mock fixture only", p)
			}
		case !mok:
			if !underNull(ms, p) {
				t.Errorf("path %s exists in the Go golden only", p)
			}
		default:
			// A list that is empty on one side has no element paths there; only the container kind must agree, and a
			// null where the other side has a list is the nil-list bug this test exists to catch.
			if !compatibleKinds(gk, mk) {
				t.Errorf("path %s: Go golden has %s, mock fixture has %s", p, kinds(gk), kinds(mk))
			}
		}
	}
}

// underNull reports whether an ancestor of path is null in that document (an optional object that is absent).
func underNull(shape map[string]map[string]bool, path string) bool {
	for i := len(path) - 1; i > 0; i-- {
		if path[i] == '.' {
			if k, ok := shape[path[:i]]; ok && kinds(k) == "null" {
				return true
			}
		}
	}
	return false
}

// compatibleKinds compares the kinds seen at a path in the two documents after dropping "null" (an optional member that
// one dataset has and the other does not). The remaining kinds must be equal; a side that is only ever null is compatible
// with a scalar or an object but never with a list: a null where the other side has a list is the nil-list bug this test
// exists to catch.
func compatibleKinds(a, b map[string]bool) bool {
	drop := func(m map[string]bool) map[string]bool {
		out := map[string]bool{}
		for k := range m {
			if k != "null" {
				out[k] = true
			}
		}
		return out
	}
	da, db := drop(a), drop(b)
	if len(da) == 0 || len(db) == 0 {
		other := da
		if len(da) == 0 {
			other = db
		}
		return !other["array"]
	}
	return kinds(da) == kinds(db)
}

func TestTicketGolden_LogsTheDataDifferencesToTheMockFixture(t *testing.T) {
	defer recoverStub(t)
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Skipf("golden missing: %v", err)
	}
	mock, err := os.ReadFile(mockFixture)
	if err != nil {
		t.Fatalf("mock fixture: %v", err)
	}
	var g, m any
	_ = json.Unmarshal(golden, &g)
	_ = json.Unmarshal(mock, &m)
	var diffs []string
	diffValues("", g, m, &diffs)
	t.Logf("%d data differences between the Go golden and the mock fixture (fixture data, not rules; see docs/wp-notes/WP-T6.md):", len(diffs))
	for _, d := range diffs {
		t.Log(d)
	}
}

// diffValues lists the differing leaves, skipping ADF documents.
func diffValues(path string, g, m any, out *[]string) {
	switch gx := g.(type) {
	case map[string]any:
		mx, ok := m.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: object vs %T", path, m))
			return
		}
		keys := map[string]bool{}
		for k := range gx {
			keys[k] = true
		}
		for k := range mx {
			keys[k] = true
		}
		var ks []string
		for k := range keys {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		for _, k := range ks {
			if k == "description" || k == "body" || k == "rich" {
				continue
			}
			diffValues(path+"."+k, gx[k], mx[k], out)
		}
	case []any:
		mx, ok := m.([]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: list vs %T", path, m))
			return
		}
		if len(gx) != len(mx) {
			*out = append(*out, fmt.Sprintf("%s: %d entries vs %d", path, len(gx), len(mx)))
		}
		for i := 0; i < len(gx) && i < len(mx); i++ {
			diffValues(fmt.Sprintf("%s[%d]", path, i), gx[i], mx[i], out)
		}
	default:
		if fmt.Sprint(g) != fmt.Sprint(m) {
			*out = append(*out, fmt.Sprintf("%s: golden %v, mock %v", path, g, m))
		}
	}
}

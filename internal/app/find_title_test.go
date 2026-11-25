package app_test

// FindByKey's title tier (ux-spec §15.3): the summary is matched fuzzily, word by word, after the key tiers. End to end
// over the synced fake board plus bare issues with chosen summaries.

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/view"
)

// insertTitled adds a bare synced issue (no history, no membership) with the given summary and status.
func insertTitled(t *testing.T, ts *ticketApp, key, summary, statusID string) int64 {
	t.Helper()
	var conn int64
	if err := ts.DB.R.QueryRow(`SELECT id FROM connection`).Scan(&conn); err != nil {
		t.Fatal(err)
	}
	var id int64
	err := ts.DB.W.QueryRow(`INSERT INTO issue (connection_id, source_id, key, space_id, summary, issue_type_id, status_id, created_at, updated_at,
		hydrated_at, last_seen_at) SELECT ?, ?, ?, space_id, ?, issue_type_id, ?, created_at, updated_at, hydrated_at, last_seen_at
		FROM issue LIMIT 1 RETURNING id`, conn, "t"+key, key, summary, statusID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func statusOfCategory(t *testing.T, ts *ticketApp, category string) string {
	t.Helper()
	var st string
	if err := ts.DB.R.QueryRow(`SELECT id FROM status WHERE category = ? LIMIT 1`, category).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func keysOf(hits []view.TicketHit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.Key)
	}
	return out
}

func tierHits(f view.TicketFind, tier string) []view.TicketHit {
	var out []view.TicketHit
	for _, h := range f.Hits {
		if h.Tier == tier {
			out = append(out, h)
		}
	}
	return out
}

// inked is the summary's runes at the hit's TitleMatch indexes, the way the frontend's Hl walks [...summary].
func inked(h view.TicketHit) string {
	r := []rune(h.Summary)
	var b strings.Builder
	for _, i := range h.TitleMatch {
		b.WriteRune(r[i])
	}
	return b.String()
}

func TestFindByKey_TitleWordsAnyOrderPartialAndCase(t *testing.T) {
	ts := newTicketApp(t)
	ts.Fake.ResetRequests()
	mark := ts.networkCalls()
	var recipe view.TicketView
	for _, x := range ts.v.Tickets {
		if strings.HasPrefix(x.Summary, "Recipe card crops") {
			recipe = x
		}
	}
	if recipe.ID == 0 {
		t.Fatal("the fixture has no 'Recipe card crops…' ticket")
	}
	for _, q := range []string{"recipe card", "card recipe", "RECIPE CARD", "  rec  ca ", "recipe crops hero", "rcd"} {
		got, err := ts.Svc.Ticket.FindByKey(ctx, q, ts.standup, 20)
		noErr(t, err, "FindByKey "+q)
		var hit *view.TicketHit
		for i, h := range got.Hits {
			if h.IssueID == recipe.ID {
				hit = &got.Hits[i]
			}
		}
		if hit == nil || hit.Tier != "title" {
			t.Errorf("%q: %s not found as a title hit: %v", q, recipe.Key, keysOf(got.Hits))
			continue
		}
		if len(hit.Match) != 0 || len(hit.TitleMatch) == 0 {
			t.Errorf("%q: match %v title match %v", q, hit.Match, hit.TitleMatch)
		}
	}
	ts.assertNoNetwork(mark, "FindByKey by title")
	if len(ts.Fake.Requests()) != 0 {
		t.Errorf("FindByKey reached the fake Jira: %v", ts.requests())
	}

	got, err := ts.Svc.Ticket.FindByKey(ctx, "recipe card", ts.standup, 20)
	noErr(t, err, "FindByKey recipe card")
	h := got.Hits[0]
	if h.IssueID != recipe.ID || inked(h) != "Recipecard" {
		t.Errorf("recipe card: first hit %s inked %q, want %s and Recipecard", h.Key, inked(h), recipe.Key)
	}
	if h.Lane != recipe.Lane || h.StandupID != ts.standup || h.Removed || h.Summary != recipe.Summary {
		t.Errorf("hit not placed like the board row: %+v", h)
	}
	// the subsequence picks word starts
	got, err = ts.Svc.Ticket.FindByKey(ctx, "rcd", ts.standup, 20)
	noErr(t, err, "FindByKey rcd")
	for _, x := range got.Hits {
		if x.IssueID == recipe.ID && inked(x) != "Rcd" {
			t.Errorf("rcd inked %q in %q", inked(x), x.Summary)
		}
	}
	// a title that is not there
	got, err = ts.Svc.Ticket.FindByKey(ctx, "zebra crossing", ts.standup, 20)
	noErr(t, err, "FindByKey zebra crossing")
	if len(got.Hits) != 0 || got.TitleMore != 0 {
		t.Errorf("zebra crossing found %v", keysOf(got.Hits))
	}
}

func TestFindByKey_TitleSkippedForShortQueries_NumbersMatchTitlesContiguously(t *testing.T) {
	ts := newTicketApp(t)
	for _, q := range []string{"r", "a ", " e", "-", "--"} {
		got, err := ts.Svc.Ticket.FindByKey(ctx, q, ts.standup, 20)
		noErr(t, err, "FindByKey "+q)
		if len(got.Hits) != 0 {
			t.Errorf("%q found %v", q, keysOf(got.Hits))
		}
	}
	// numbers search titles too, but only as contiguous runs: "2024" and "14" find the summary that contains them,
	// "224" (digits spread across "2024") does not
	titled := insertTitled(t, ts, "MOB-9301", "Upgrade to node 2024 and 14 more", statusOfCategory(t, ts, "indeterminate"))
	for _, q := range []string{"2024", "14", "node 2024"} {
		got, err := ts.Svc.Ticket.FindByKey(ctx, q, ts.standup, 20)
		noErr(t, err, "FindByKey "+q)
		found := false
		for _, h := range got.Hits {
			if h.IssueID == titled && h.Tier == "title" {
				found = true
			}
		}
		if !found {
			t.Errorf("%q did not find MOB-9301 through its summary: %v", q, keysOf(got.Hits))
		}
	}
	got0, err := ts.Svc.Ticket.FindByKey(ctx, "224", ts.standup, 20)
	noErr(t, err, "FindByKey 224")
	for _, h := range got0.Hits {
		if h.IssueID == titled {
			t.Errorf("224 matched scattered digits in %s", h.Key)
		}
	}
	// two characters are enough
	got, err := ts.Svc.Ticket.FindByKey(ctx, "no", ts.standup, 20)
	noErr(t, err, "FindByKey no")
	if len(tierHits(got, "title")) == 0 {
		t.Error("a two-letter query finds no titles")
	}
}

func TestFindByKey_KeyShapedQueryListsKeysThenTitles(t *testing.T) {
	ts := newTicketApp(t)
	active := statusOfCategory(t, ts, "indeterminate")
	insertTitled(t, ts, "MOB-9302", "see mob1429 and mob-1429 notes", active)
	// "mob1429" names MOB-1429: the key comes first, then the summary that mentions it
	got, err := ts.Svc.Ticket.FindByKey(ctx, "mob1429", ts.standup, 20)
	noErr(t, err, "FindByKey mob1429")
	if len(got.Hits) == 0 || got.Hits[0].Key != "MOB-1429" || got.Hits[0].Tier != "exact" {
		t.Fatalf("hits %v", keysOf(got.Hits))
	}
	if th := tierHits(got, "title"); len(th) != 1 || th[0].Key != "MOB-9302" {
		t.Errorf("mob1429 title hits %v, want MOB-9302", keysOf(th))
	}
	// a key-shaped query that finds no key falls through to the titles
	insertTitled(t, ts, "MOB-9303", "Migrate to OAuth2 flow", active)
	got, err = ts.Svc.Ticket.FindByKey(ctx, "oauth2", ts.standup, 20)
	noErr(t, err, "FindByKey oauth2")
	if len(got.Hits) != 1 || got.Hits[0].Key != "MOB-9303" || got.Hits[0].Tier != "title" || inked(got.Hits[0]) != "OAuth2" {
		t.Errorf("oauth2: %+v", got.Hits)
	}
	// a project prefix that matches nothing: the digits do not find a ticket, the words may
	got, err = ts.Svc.Ticket.FindByKey(ctx, "zzz14", ts.standup, 20)
	noErr(t, err, "FindByKey zzz14")
	if len(got.Hits) != 0 {
		t.Errorf("zzz14 found %v", keysOf(got.Hits))
	}
}

func TestFindByKey_KeyTiersRankBeforeTitleAndAnIssueAppearsOnce(t *testing.T) {
	ts := newTicketApp(t)
	active := statusOfCategory(t, ts, "indeterminate")
	// "m 14" is several words, so it is a key query (m + 14) and a title query (m, 14)
	both := insertTitled(t, ts, "MOB-1499", "Migrate 14 users", active)      // starts with 14 AND its summary matches
	onlyTitle := insertTitled(t, ts, "MOB-9400", "Migrate 14 users", active) // its summary only
	got, err := ts.Svc.Ticket.FindByKey(ctx, "m 14", ts.standup, 20)
	noErr(t, err, "FindByKey m 14")
	seenTitle := false
	count := map[int64]int{}
	for _, h := range got.Hits {
		count[h.IssueID]++
		if h.Tier == "title" {
			seenTitle = true
		} else if seenTitle {
			t.Errorf("key hit %s (%s) after a title hit: %v", h.Key, h.Tier, keysOf(got.Hits))
		}
	}
	if !seenTitle {
		t.Fatalf("no title hit: %v", keysOf(got.Hits))
	}
	for id, n := range count {
		if n != 1 {
			t.Errorf("issue %d appears %d times", id, n)
		}
	}
	for _, h := range got.Hits {
		if h.IssueID == both && h.Tier != "prefix" {
			t.Errorf("MOB-1499 is %s, want its key tier", h.Tier)
		}
		if h.IssueID == onlyTitle && h.Tier != "title" {
			t.Errorf("MOB-9400 is %s, want title", h.Tier)
		}
	}
	if _, ok := count[onlyTitle]; !ok {
		t.Errorf("MOB-9400 missing: %v", keysOf(got.Hits))
	}
}

func TestFindByKey_TitleOrderScoreThenStandupDoneKey(t *testing.T) {
	ts := newTicketApp(t)
	active, done := statusOfCategory(t, ts, "indeterminate"), statusOfCategory(t, ts, "done")
	member := func(id int64) {
		if _, err := ts.DB.W.Exec(`INSERT INTO standup_issue (standup_id, issue_id, role, rank_index) VALUES (?, ?, 'board', 999)`, ts.standup, id); err != nil {
			t.Fatal(err)
		}
	}
	// identical summaries: only the in-tier order separates them
	dn := insertTitled(t, ts, "MOB-9502", "Zebra crossing", done)
	a2 := insertTitled(t, ts, "MOB-9501", "Zebra crossing", active)
	a1 := insertTitled(t, ts, "MOB-9500", "Zebra crossing", active)
	cur := insertTitled(t, ts, "MOB-9599", "Zebra crossing", active)
	member(cur)
	// a better match (a whole word) beats a worse one (a word start) even when it is done and on no board
	whole := insertTitled(t, ts, "MOB-9600", "Zebra", done)
	start := insertTitled(t, ts, "MOB-9601", "Zebrafish", active)
	got, err := ts.Svc.Ticket.FindByKey(ctx, "zebra", ts.standup, 20)
	noErr(t, err, "FindByKey zebra")
	var order []int64
	for _, h := range got.Hits {
		order = append(order, h.IssueID)
		if h.Tier != "title" {
			t.Errorf("%s tier %s", h.Key, h.Tier)
		}
	}
	// "Zebra" and "Zebra crossing" both hold the whole word; "Zebrafish" only a word start
	want := []int64{cur, a1, a2, dn, whole, start}
	// among equal scores: the current standup, then active before done, then key; "Zebra" (done, 9600) sorts after the
	// done 9502 by key
	if len(order) != len(want) {
		t.Fatalf("hits %v", keysOf(got.Hits))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v (current member, active by key, done by key, then the word start)", keysOf(got.Hits), want)
		}
	}
	// the word start ranks last although it is active and "Zebra" is done
	if got.Hits[len(got.Hits)-1].IssueID != start {
		t.Errorf("last hit %s", got.Hits[len(got.Hits)-1].Key)
	}
	_ = whole
}

func TestFindByKey_TitleLimitMoreAndRoom(t *testing.T) {
	ts := newTicketApp(t)
	active := statusOfCategory(t, ts, "indeterminate")
	for i := 0; i < 14; i++ {
		insertTitled(t, ts, "MOB-98"+strconv.Itoa(100 + i)[1:], "Quokka habitat", active)
	}
	got, err := ts.Svc.Ticket.FindByKey(ctx, "quokka", ts.standup, 20)
	noErr(t, err, "FindByKey quokka")
	if len(got.Hits) != 10 || got.TitleMore != 4 || got.PrefixMore != 0 {
		t.Errorf("hits %d, more %d; want 10 and 4", len(got.Hits), got.TitleMore)
	}
	got, err = ts.Svc.Ticket.FindByKey(ctx, "quokka", ts.standup, 3)
	noErr(t, err, "FindByKey quokka limit 3")
	if len(got.Hits) != 3 || got.TitleMore != 11 {
		t.Errorf("limit 3: hits %d, more %d", len(got.Hits), got.TitleMore)
	}
}

func TestFindByKey_TitleCoversDoneAndTicketsOnNoBoard(t *testing.T) {
	ts := newTicketApp(t)
	d := insertTitled(t, ts, "MOB-9700", "Wombat sanctuary tour", statusOfCategory(t, ts, "done"))
	got, err := ts.Svc.Ticket.FindByKey(ctx, "wombat tour", ts.standup, 20)
	noErr(t, err, "FindByKey wombat tour")
	if len(got.Hits) != 1 || got.Hits[0].IssueID != d {
		t.Fatalf("hits %v", keysOf(got.Hits))
	}
	if h := got.Hits[0]; !h.Done || !h.Removed || h.StandupID != ts.standup || inked(h) != "Wombattour" {
		t.Errorf("hit %+v inked %q", h, inked(h))
	}
}

func TestFindByKey_TitleIndexesAreRuneIndexes(t *testing.T) {
	ts := newTicketApp(t)
	// an emoji and accented letters before the match; the frontend slices [...summary]
	insertTitled(t, ts, "MOB-9800", "🍳 Crème brûlée soufflé", statusOfCategory(t, ts, "indeterminate"))
	for q, want := range map[string]string{"brûlée": "brûlée", "BRÛLÉE": "brûlée", "soufflé": "soufflé", "crème soufflé": "Crèmesoufflé"} {
		got, err := ts.Svc.Ticket.FindByKey(ctx, q, ts.standup, 20)
		noErr(t, err, "FindByKey "+q)
		if len(got.Hits) != 1 || got.Hits[0].Key != "MOB-9800" {
			t.Errorf("%q: hits %v", q, keysOf(got.Hits))
			continue
		}
		if g := inked(got.Hits[0]); g != want {
			t.Errorf("%q: inked %q, want %q", q, g, want)
		}
	}
}

// The title index is rebuilt after an issue is added or renamed (the issue generation, migration 0008).
func TestFindByKey_TitleIndexFollowsWrites(t *testing.T) {
	ts := newTicketApp(t)
	find := func(q string) []string {
		got, err := ts.Svc.Ticket.FindByKey(ctx, q, ts.standup, 20)
		noErr(t, err, "FindByKey "+q)
		return keysOf(got.Hits)
	}
	if got := find("axolotl"); len(got) != 0 {
		t.Fatalf("axolotl found %v before it exists", got)
	}
	id := insertTitled(t, ts, "MOB-9900", "Axolotl tank cleaning", statusOfCategory(t, ts, "indeterminate"))
	if got := find("axolotl"); len(got) != 1 || got[0] != "MOB-9900" {
		t.Fatalf("axolotl after insert: %v", got)
	}
	// a write through WriteTx (what sync apply and the ticket writes use) renames it
	err := ts.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE issue SET summary = 'Salamander tank cleaning' WHERE id = ?`, id)
		return err
	})
	noErr(t, err, "rename")
	if got := find("axolotl"); len(got) != 0 {
		t.Errorf("axolotl still found after the rename: %v", got)
	}
	if got := find("salamander"); len(got) != 1 || got[0] != "MOB-9900" {
		t.Errorf("salamander: %v", got)
	}
}

// The index follows the real write paths (no hand-made invalidation): FetchByKey storing an issue, a resync
// applying a summary the provider changed, and a transition applying a changed status.
func TestFindByKey_TitleIndexFollowsRealWritePaths(t *testing.T) {
	ts := newTicketApp(t)
	find := func(q string) view.TicketFind {
		got, err := ts.Svc.Ticket.FindByKey(ctx, q, ts.standup, 20)
		noErr(t, err, "FindByKey "+q)
		return got
	}
	has := func(f view.TicketFind, key string) *view.TicketHit {
		for i, h := range f.Hits {
			if h.Key == key {
				return &f.Hits[i]
			}
		}
		return nil
	}

	// FetchByKey stores a never-synced issue: its title is found afterwards, and not before
	key := unsyncedKey(t, ts)
	var summary string
	for _, is := range ts.Fx.fx.Issues {
		if is.Key == key {
			summary = is.Summary
		}
	}
	if has(find(summary), key) != nil {
		t.Fatalf("%s found by its title before it was fetched", key)
	}
	_, err := ts.Svc.Ticket.FetchByKey(ctx, ts.standup, key)
	noErr(t, err, "FetchByKey")
	if h := has(find(summary), key); h == nil || h.Tier != "title" || !h.Removed {
		t.Fatalf("after FetchByKey, %q finds %v", summary, keysOf(find(summary).Hits))
	}

	// a resync applies a summary the provider changed
	active := ts.Fx.activeKey
	ts.Fake.Mutate(func(ds *jiratest.Dataset) {
		for _, is := range ds.Issues {
			if is.Key == active {
				is.Summary = "Quartz mining"
			}
		}
	})
	if got := find("quartz mining"); len(got.Hits) != 0 {
		t.Fatalf("quartz found before the resync: %v", keysOf(got.Hits))
	}
	ts.syncNow(ts.standup)
	got := find("quartz mining")
	if len(got.Hits) != 1 || got.Hits[0].Key != active || inked(got.Hits[0]) != "Quartzmining" {
		t.Fatalf("quartz after the resync: %+v", got.Hits)
	}

	// a transition to a Done lane applies a new status: the finder's active-before-done order follows it
	issue := ts.idOf(active)
	b := insertTitled(t, ts, "MOB-9901", "Quartz mining", statusOfCategory(t, ts, "indeterminate"))
	if _, err := ts.DB.W.Exec(`INSERT INTO standup_issue (standup_id, issue_id, role, rank_index) VALUES (?, ?, 'board', 999)`, ts.standup, b); err != nil {
		t.Fatal(err)
	}
	if order := keysOf(find("quartz mining").Hits); len(order) != 2 || order[0] != active || order[1] != "MOB-9901" {
		t.Fatalf("before the transition: %v, want [%s MOB-9901] (both active, by key)", order, active)
	}
	list, err := ts.Svc.Ticket.Transitions(ctx, issue)
	noErr(t, err, "Transitions")
	var done *view.TransitionOption
	for i, it := range list.Items {
		if !it.NeedsScreen && it.ToLane == "done" {
			done = &list.Items[i]
		}
	}
	if done == nil {
		t.Skipf("the fixture offers no plain transition to a Done lane for %s: %+v", active, list.Items)
	}
	_, err = ts.Svc.Ticket.Transition(ctx, ts.standup, issue, optionID(t, ts, issue, *done))
	noErr(t, err, "Transition")
	if order := keysOf(find("quartz mining").Hits); len(order) != 2 || order[0] != "MOB-9901" || order[1] != active {
		t.Errorf("after the transition to Done: %v, want [MOB-9901 %s]", order, active)
	}
}

// Only writes that change an issue's key, summary or status rebuild the index: a settings save, the palette's recents
// and usage saves, UI state and a resync that finds nothing changed leave it alone.
func TestFindByKey_TitleIndexIsNotRebuiltByUnrelatedWrites(t *testing.T) {
	ts := newTicketApp(t)
	find := func() {
		t.Helper()
		got, err := ts.Svc.Ticket.FindByKey(ctx, "recipe card", ts.standup, 20)
		noErr(t, err, "FindByKey")
		if len(got.Hits) == 0 {
			t.Fatal("no hits")
		}
	}
	find()
	builds := ts.Deps.FindIndexBuilds()
	if builds == 0 {
		t.Fatal("the first title query did not build the index")
	}
	still := func(what string) {
		t.Helper()
		find()
		if n := ts.Deps.FindIndexBuilds(); n != builds {
			t.Fatalf("%s rebuilt the index (%d builds, was %d)", what, n, builds)
		}
	}
	_, err := ts.Svc.App.SetAppSetting(ctx, "palette.recent_tickets", "[1,2,3]")
	noErr(t, err, "SetAppSetting recents")
	still("saving the recents")
	_, err = ts.Svc.App.SetAppSetting(ctx, "palette.command_usage", `{"sync.resync":2}`)
	noErr(t, err, "SetAppSetting usage")
	still("saving the command usage")
	noErr(t, ts.Svc.App.SetUIState(ctx, app.UIStatePatch{}), "SetUIState")
	still("saving the UI state")
	ts.syncNow(ts.standup) // the fake is unchanged: every issue is rewritten with the same key, summary and status
	still("a resync that changed nothing")

	// and a real change does
	id := ts.idOf(ts.Fx.activeKey)
	err = ts.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE issue SET summary = summary || ' (edited)' WHERE id = ?`, id)
		return err
	})
	noErr(t, err, "edit")
	find()
	if n := ts.Deps.FindIndexBuilds(); n != builds+1 {
		t.Errorf("a renamed issue gave %d builds, want %d", n, builds+1)
	}
}

func TestByIDs_TitleMatchIsEmptyNotNull(t *testing.T) {
	ts := newTicketApp(t)
	hits, err := ts.Svc.Ticket.ByIDs(ctx, []int64{ts.issue}, ts.standup)
	noErr(t, err, "ByIDs")
	if len(hits) != 1 || hits[0].TitleMatch == nil || len(hits[0].TitleMatch) != 0 {
		t.Errorf("TitleMatch = %#v, want an empty non-nil slice", hits[0].TitleMatch)
	}
}

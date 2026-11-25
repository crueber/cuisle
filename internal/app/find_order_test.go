package app_test

// FindByKey ordering inside a tier, the starts-with cap and the limit (ux-spec §15.3), over bare issues added to the
// synced fake board.

import (
	"strconv"
	"testing"
)

// insertIssue adds a bare synced issue (no history, no membership) in the given status.
func insertIssue(t *testing.T, ts *ticketApp, key, statusID string) int64 {
	t.Helper()
	var conn int64
	if err := ts.DB.R.QueryRow(`SELECT id FROM connection`).Scan(&conn); err != nil {
		t.Fatal(err)
	}
	var id int64
	err := ts.DB.W.QueryRow(`INSERT INTO issue (connection_id, source_id, key, space_id, summary, issue_type_id, status_id, created_at, updated_at,
		hydrated_at, last_seen_at) SELECT ?, ?, ?, space_id, 'Extra ' || ?, issue_type_id, ?, created_at, updated_at, hydrated_at, last_seen_at
		FROM issue LIMIT 1 RETURNING id`, conn, "x"+key, key, key, statusID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFindByKey_OrderWithinATierLimitsAndMore(t *testing.T) {
	ts := newTicketApp(t)
	var doneSt, activeSt string
	if err := ts.DB.R.QueryRow(`SELECT id FROM status WHERE category = 'done' LIMIT 1`).Scan(&doneSt); err != nil {
		t.Fatal(err)
	}
	if err := ts.DB.R.QueryRow(`SELECT id FROM status WHERE category = 'indeterminate' LIMIT 1`).Scan(&activeSt); err != nil {
		t.Fatal(err)
	}
	a := insertIssue(t, ts, "MOB-9001", activeSt)
	d := insertIssue(t, ts, "MOB-9002", doneSt)
	c := insertIssue(t, ts, "MOB-9003", activeSt) // becomes a member of the current standup
	if _, err := ts.DB.W.Exec(`INSERT INTO standup_issue (standup_id, issue_id, role, rank_index) VALUES (?, ?, 'board', 999)`, ts.standup, c); err != nil {
		t.Fatal(err)
	}
	r, err := ts.Svc.Ticket.FindByKey(ctx, "900", ts.standup, 20)
	noErr(t, err, "FindByKey 900")
	var got []int64
	for _, h := range r.Hits {
		got = append(got, h.IssueID)
		if h.Tier != "prefix" {
			t.Errorf("%s tier %s", h.Key, h.Tier)
		}
	}
	// current standup first, then active before done
	if want := []int64{c, a, d}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("order = %v, want %v (member of current, active, done)", got, want)
	}
	if !r.Hits[2].Done || !r.Hits[2].Removed || r.Hits[0].Removed {
		t.Errorf("flags: %+v", r.Hits)
	}

	// 14 starts-with matches: ten shown, four more
	for i := 1; i <= 14; i++ {
		insertIssue(t, ts, "MOB-70"+strconv.Itoa(100 + i)[1:], activeSt)
	}
	r, err = ts.Svc.Ticket.FindByKey(ctx, "mob70", ts.standup, 20)
	noErr(t, err, "FindByKey mob70")
	prefix := 0
	for _, h := range r.Hits {
		if h.Tier == "prefix" {
			prefix++
		}
	}
	if prefix != 10 || r.PrefixMore != 4 {
		t.Errorf("prefix hits %d, more %d; want 10 and 4", prefix, r.PrefixMore)
	}
	// limit caps the whole answer
	r, err = ts.Svc.Ticket.FindByKey(ctx, "mob70", ts.standup, 3)
	noErr(t, err, "FindByKey limit 3")
	if len(r.Hits) != 3 {
		t.Errorf("limit 3 gave %d hits", len(r.Hits))
	}
}

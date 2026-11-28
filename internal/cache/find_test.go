package cache_test

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/crueber/cuisle/internal/cache"
)

func TestStandupActiveByAssigneeTx_ExcludesUnmappedAndNonWorking(t *testing.T) {
	db := rdOpen(t)
	rdSeedBase(t, db)
	// Add a custom working lane mapping too so custom lanes are verified as active.
	rdInsert(t, db, "standup_lane", "standup_id,lane,walk_position,limit_days,label,hue",
		rdSA, "c1", 4, 3, "Peer Review", 1)
	rdInsert(t, db, "standup_column_map", "standup_id,column_name,position,lane,auto",
		rdSA, "Peer Review", 7, "c1", 0)
	rdInsert(t, db, "standup_status_lane", "standup_id,status_id,status_name,column_name,lane",
		rdSA, "19", "Peer Review", "Peer Review", "c1")

	now := rdMs("2026-03-18T12:00:00Z")
	addIssue := func(id int64, key, statusID string, assignee any, role string) {
		rdInsert(t, db, "issue",
			"id,connection_id,source_id,key,summary,space_id,issue_type_id,status_id,assignee_account_id,created_at,updated_at,hydrated_at,last_seen_at",
			id, 1, "src-"+key, key, "summary "+key, "100", "1", statusID, assignee, now, now, now, now)
		rdInsert(t, db, "standup_issue", "standup_id,issue_id,role,rank_index", rdSA, id, role, int(id))
	}

	// Alice: 2 active (To Do + custom lane c1), plus done, hidden, backlog lane, backlog role, and unmapped status 18.
	addIssue(1, "MOB-1", "11", "acc-alice", "board")   // todo -> counted
	addIssue(2, "MOB-2", "19", "acc-alice", "board")   // c1 -> counted
	addIssue(3, "MOB-3", "16", "acc-alice", "board")   // done -> excluded
	addIssue(4, "MOB-4", "17", "acc-alice", "board")   // hidden -> excluded
	addIssue(5, "MOB-5", "10", "acc-alice", "board")   // backlog lane -> excluded
	addIssue(6, "MOB-6", "12", "acc-alice", "backlog") // backlog role -> excluded
	addIssue(7, "MOB-7", "18", "acc-alice", "board")   // unmapped status (no standup_status_lane) -> excluded

	// Bob: only an unmapped status on the board -> must NOT appear with active = 1.
	addIssue(8, "MOB-8", "18", "acc-bob", "board")

	// Unassigned active board ticket -> excluded from StandupActiveByAssigneeTx.
	addIssue(9, "MOB-9", "12", nil, "board")

	var got map[string]int
	var all map[int64]map[string]int
	err := db.ReadTx(rdCtx, func(tx *sql.Tx) error {
		var err error
		got, err = cache.StandupActiveByAssigneeTx(rdCtx, tx, rdSA)
		if err != nil {
			return err
		}
		all, err = cache.ActiveByAssigneeTx(rdCtx, tx)
		return err
	})
	if err != nil {
		t.Fatalf("ReadTx: %v", err)
	}

	want := map[string]int{"acc-alice": 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StandupActiveByAssigneeTx = %v, want %v", got, want)
	}
	// Must agree with ActiveByAssigneeTx for every non-empty assignee.
	delete(all[rdSA], "")
	if !reflect.DeepEqual(got, all[rdSA]) {
		t.Fatalf("StandupActiveByAssigneeTx = %v disagrees with ActiveByAssigneeTx = %v", got, all[rdSA])
	}
}

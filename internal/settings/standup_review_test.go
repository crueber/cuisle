package settings

// WP-38 review fix: DiscardDraft never hard-deletes a standup that has sync history.

import "testing"

func TestStandupDiscardDraft_KeepsADraftThatHasSyncRuns(t *testing.T) {
	e := newW16(t)
	id := e.create()
	e.exec(`INSERT INTO sync_run (standup_id, mode, trigger, status, started_at) VALUES (?, 'full', 'manual', 'failed', 1000)`, id)
	if err := e.s.DiscardDraft(e.ctx, id); err != nil {
		t.Fatalf("DiscardDraft: %v", err)
	}
	if n := e.int(`SELECT count(*) FROM standup WHERE id = ?`, id); n != 1 {
		t.Error("a standup with sync_run rows must survive DiscardDraft")
	}
	if n := e.int(`SELECT count(*) FROM sync_run WHERE standup_id = ?`, id); n != 1 {
		t.Error("the sync_run row must survive")
	}
}

package cache_test

// WP-38 review fixes: SaveConnection never rewrites or deletes a connection that owns history.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/store"
)

func rdInsertBareIssue(t *testing.T, db *store.DB, connID int64) {
	t.Helper()
	rdInsert(t, db, "issue",
		"id,connection_id,source_id,key,space_id,summary,issue_type_id,status_id,created_at,updated_at,hydrated_at,last_seen_at",
		1, connID, "10001", "MOB-1", "100", "A", "1", "11", 1000, 2000, 3000, 3000)
}

func TestSaveConnection_DifferentBaseURL_OwningOnlyIssues_IsRefusedAndKeepsTheIssues(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	id1 := rdMustSaveConn(t, db, rdConnRow())
	rdInsertBareIssue(t, db, id1)
	before := rdMustGetConn(t, db)

	other := rdConnRow()
	other.BaseURL = "https://other.atlassian.net"
	_, err := cache.SaveConnection(rdCtx, db.W, other)
	if !apperr.Is(err, apperr.Conflict) {
		t.Fatalf("SaveConnection = %v, want conflict", err)
	}
	want := "Cuisle is connected to acme.atlassian.net. Switching to another Jira site isn't supported yet."
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
	if n := rdCount(t, db, "issue", ""); n != 1 {
		t.Errorf("issue rows = %d, want 1 (never cascaded away)", n)
	}
	if got := rdMustGetConn(t, db); !reflect.DeepEqual(got, before) {
		t.Errorf("connection changed: %+v, want %+v", got, before)
	}
}

func TestSaveConnection_SameBaseURL_KeepsTheIDAndHistory(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	id1 := rdMustSaveConn(t, db, rdConnRow())
	rdInsertBareIssue(t, db, id1)

	again := rdConnRow()
	again.Account = "second@example.com"
	again.AccountID = "acc-second"
	again.DisplayName = "Second"
	id2 := rdMustSaveConn(t, db, again)
	if id2 != id1 {
		t.Errorf("id = %d, want %d", id2, id1)
	}
	got := rdMustGetConn(t, db)
	if got.Account != "second@example.com" || got.AccountID != "acc-second" || got.DisplayName != "Second" {
		t.Errorf("connection = %+v, want the new account", got)
	}
	if n := rdCount(t, db, "issue", ""); n != 1 {
		t.Errorf("issue rows = %d, want 1", n)
	}
}

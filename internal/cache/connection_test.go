package cache_test

// Tests for the connection helpers of internal/cache (WP-20; data-model.md §2.2).
//
// SPEC-NOTE: SaveConnection stores c.CreatedAt as given (the cache has no clock); on an update of an existing row
// created_at is kept (never overwritten by the second call's value). Tests only assert it is non-zero and unchanged.
// SPEC-NOTE: SaveConnection maps an empty AuthState / DevActivityState to the column default "unknown" and a nil
// SourceMeta to "{}" (read back as a non-nil empty map); zero VerifiedAt / DevCheckedAt are stored as NULL and read
// back as the zero time.
// SPEC-NOTE: SetAuthState with a state other than "ok" leaves verified_at untouched.

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/store"
)

func rdConnRow() cache.ConnRow {
	return cache.ConnRow{
		Source:           "jira",
		BaseURL:          "https://acme.atlassian.net",
		Account:          "chris@example.com",
		AccountID:        "acc-chris",
		DisplayName:      "Chris",
		AuthState:        "ok",
		VerifiedAt:       rdTS("2026-03-01T10:00:00Z"),
		SourceMeta:       map[string]any{"flaggedFieldId": "customfield_10021", "sprintFieldId": "customfield_10020"},
		DevActivityState: "ok",
		DevCheckedAt:     rdTS("2026-03-02T10:00:00Z"),
		CreatedAt:        rdTS("2026-02-01T10:00:00Z"),
	}
}

func rdMustSaveConn(t *testing.T, db *store.DB, c cache.ConnRow) int64 {
	t.Helper()
	id, err := cache.SaveConnection(rdCtx, db.W, c)
	if err != nil {
		t.Fatalf("SaveConnection(%s): %v", c.BaseURL, err)
	}
	return id
}

func rdMustGetConn(t *testing.T, db *store.DB) cache.ConnRow {
	t.Helper()
	got, err := cache.GetConnection(rdCtx, db.R)
	if err != nil {
		t.Fatalf("GetConnection: %v", err)
	}
	return got
}

func TestGetConnection_NoRow_ReturnsNotFound(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	_, err := cache.GetConnection(rdCtx, db.R)
	if err == nil {
		t.Fatal("GetConnection on an empty DB returned no error")
	}
	if !apperr.Is(err, apperr.NotFound) {
		t.Errorf("error code = %q, want %q (err: %v)", apperr.CodeOf(err), apperr.NotFound, err)
	}
}

func TestSaveConnection_Insert_RoundTripsEveryField(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	in := rdConnRow()
	id := rdMustSaveConn(t, db, in)
	if id <= 0 {
		t.Fatalf("SaveConnection returned id %d, want > 0", id)
	}
	got := rdMustGetConn(t, db)
	if got.ID != id {
		t.Errorf("ID = %d, want the returned %d", got.ID, id)
	}
	if got.Source != "jira" || got.BaseURL != in.BaseURL || got.Account != in.Account || got.AccountID != in.AccountID || got.DisplayName != in.DisplayName {
		t.Errorf("identity fields = %+v, want %+v", got, in)
	}
	if got.AuthState != "ok" || got.DevActivityState != "ok" {
		t.Errorf("AuthState/DevActivityState = %q/%q, want ok/ok", got.AuthState, got.DevActivityState)
	}
	rdSame(t, "VerifiedAt", got.VerifiedAt, in.VerifiedAt)
	rdSame(t, "DevCheckedAt", got.DevCheckedAt, in.DevCheckedAt)
	if !reflect.DeepEqual(got.SourceMeta, in.SourceMeta) {
		t.Errorf("SourceMeta = %v, want %v", got.SourceMeta, in.SourceMeta)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt is zero")
	}
	if n := rdCount(t, db, "connection", ""); n != 1 {
		t.Errorf("connection rows = %d, want 1", n)
	}
}

func TestSaveConnection_EmptyStatesAndMeta_GetDefaults(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	in := cache.ConnRow{Source: "jira", BaseURL: "https://acme.atlassian.net", Account: "chris@example.com", CreatedAt: rdTS("2026-02-01T10:00:00Z")}
	rdMustSaveConn(t, db, in)
	got := rdMustGetConn(t, db)
	if got.AuthState != "unknown" || got.DevActivityState != "unknown" {
		t.Errorf("AuthState/DevActivityState = %q/%q, want unknown/unknown", got.AuthState, got.DevActivityState)
	}
	if got.SourceMeta == nil || len(got.SourceMeta) != 0 {
		t.Errorf("SourceMeta = %#v, want an empty non-nil map", got.SourceMeta)
	}
	rdZero(t, "VerifiedAt", got.VerifiedAt)
	rdZero(t, "DevCheckedAt", got.DevCheckedAt)
	if got.AccountID != "" || got.DisplayName != "" {
		t.Errorf("AccountID/DisplayName = %q/%q, want empty (NULL)", got.AccountID, got.DisplayName)
	}
}

func TestSaveConnection_SameBaseURL_UpdatesTheRowInPlace(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	id1 := rdMustSaveConn(t, db, rdConnRow())
	created := rdMustGetConn(t, db).CreatedAt

	second := rdConnRow()
	second.Account = "new@example.com"
	second.AccountID = "acc-new"
	second.DisplayName = "New Name"
	second.AuthState = "invalid"
	second.SourceMeta = map[string]any{"flaggedFieldId": "customfield_99999"}
	second.CreatedAt = rdTS("2026-03-10T10:00:00Z") // must not replace the original created_at
	id2 := rdMustSaveConn(t, db, second)

	if id2 != id1 {
		t.Errorf("second SaveConnection returned id %d, want the same id %d", id2, id1)
	}
	if n := rdCount(t, db, "connection", ""); n != 1 {
		t.Fatalf("connection rows = %d, want 1", n)
	}
	got := rdMustGetConn(t, db)
	if got.Account != "new@example.com" || got.AccountID != "acc-new" || got.DisplayName != "New Name" || got.AuthState != "invalid" {
		t.Errorf("row after update = %+v", got)
	}
	if !reflect.DeepEqual(got.SourceMeta, second.SourceMeta) {
		t.Errorf("SourceMeta = %v, want %v", got.SourceMeta, second.SourceMeta)
	}
	rdSame(t, "CreatedAt kept", got.CreatedAt, created)
}

// WP-38 review fix: a different site never rewrites a connection that owns standups (it used to move the row in
// place, which re-pointed synced history at the wrong site). It is refused with apperr conflict and nothing changes.
func TestSaveConnection_DifferentBaseURL_WithStandups_IsRefused(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	id1 := rdMustSaveConn(t, db, rdConnRow())
	rdInsert(t, db, "standup", "id,connection_id,name,position,created_at,updated_at", 1, id1, "Platform", 0, rdMs("2026-02-01T10:00:00Z"), rdMs("2026-02-01T10:00:00Z"))

	moved := rdConnRow()
	moved.BaseURL = "https://moved.atlassian.net"
	if _, err := cache.SaveConnection(rdCtx, db.W, moved); !apperr.Is(err, apperr.Conflict) {
		t.Fatalf("SaveConnection error = %v, want conflict", err)
	}

	if n := rdCount(t, db, "connection", ""); n != 1 {
		t.Fatalf("connection rows = %d, want 1", n)
	}
	if got := rdMustGetConn(t, db); got.BaseURL != "https://acme.atlassian.net" || got.ID != id1 {
		t.Errorf("connection = %+v, want base_url acme.atlassian.net unchanged on id %d", got, id1)
	}
	if n := rdCount(t, db, "standup", "connection_id = ?", id1); n != 1 {
		t.Errorf("standups attached to the connection = %d, want 1 (never orphaned)", n)
	}
}

func TestSaveConnection_DifferentBaseURL_WithoutStandups_ReplacesTheRow(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	rdMustSaveConn(t, db, rdConnRow())

	other := rdConnRow()
	other.BaseURL = "https://other.atlassian.net"
	other.Account = "other@example.com"
	id := rdMustSaveConn(t, db, other)

	if n := rdCount(t, db, "connection", ""); n != 1 {
		t.Fatalf("connection rows = %d, want exactly 1 (v1 keeps one connection)", n)
	}
	got := rdMustGetConn(t, db)
	if got.ID != id || got.BaseURL != "https://other.atlassian.net" || got.Account != "other@example.com" {
		t.Errorf("connection = %+v, want the new site on id %d", got, id)
	}
}

func TestSetAuthState_OK_StampsVerifiedAt(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	in := rdConnRow()
	in.AuthState = "unknown"
	in.VerifiedAt = time.Time{}
	id := rdMustSaveConn(t, db, in)

	at := rdTS("2026-03-18T12:00:00Z")
	if err := cache.SetAuthState(rdCtx, db.W, id, "ok", at); err != nil {
		t.Fatalf("SetAuthState: %v", err)
	}
	got := rdMustGetConn(t, db)
	if got.AuthState != "ok" {
		t.Errorf("AuthState = %q, want ok", got.AuthState)
	}
	rdSame(t, "VerifiedAt", got.VerifiedAt, at)
}

func TestSetAuthState_NotOK_KeepsEarlierVerifiedAt(t *testing.T) {
	for _, state := range []string{"invalid", "missing", "unknown"} {
		t.Run(state, func(t *testing.T) {
			defer rdRecover(t)
			db := rdOpen(t)
			in := rdConnRow() // AuthState ok, VerifiedAt 2026-03-01T10:00Z
			id := rdMustSaveConn(t, db, in)

			if err := cache.SetAuthState(rdCtx, db.W, id, state, rdTS("2026-03-18T12:00:00Z")); err != nil {
				t.Fatalf("SetAuthState: %v", err)
			}
			got := rdMustGetConn(t, db)
			if got.AuthState != state {
				t.Errorf("AuthState = %q, want %q", got.AuthState, state)
			}
			rdSame(t, "VerifiedAt", got.VerifiedAt, in.VerifiedAt)
		})
	}
}

func TestSetConnectionMetaTx_UpdatesMetaDevStateAndCheckedAt(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	id := rdMustSaveConn(t, db, rdConnRow())

	meta := map[string]any{"flaggedFieldId": "customfield_10021", "rankFieldId": "customfield_10019", "fieldsFetchedAt": "2026-03-18T12:00:00Z"}
	at := rdTS("2026-03-18T12:30:00Z")
	err := db.WriteTx(rdCtx, func(tx *sql.Tx) error {
		return cache.SetConnectionMetaTx(rdCtx, tx, id, meta, "unavailable", at)
	})
	if err != nil {
		t.Fatalf("SetConnectionMetaTx: %v", err)
	}
	got := rdMustGetConn(t, db)
	if !reflect.DeepEqual(got.SourceMeta, meta) {
		t.Errorf("SourceMeta = %v, want %v", got.SourceMeta, meta)
	}
	if got.DevActivityState != "unavailable" {
		t.Errorf("DevActivityState = %q, want unavailable", got.DevActivityState)
	}
	rdSame(t, "DevCheckedAt", got.DevCheckedAt, at)
	// the other columns are untouched
	if got.AuthState != "ok" || got.Account != "chris@example.com" {
		t.Errorf("unrelated columns changed: %+v", got)
	}
}

func TestSetConnectionMetaTx_RolledBackTx_ChangesNothing(t *testing.T) {
	defer rdRecover(t)
	db := rdOpen(t)
	id := rdMustSaveConn(t, db, rdConnRow())
	before := rdMustGetConn(t, db)

	_ = db.WriteTx(rdCtx, func(tx *sql.Tx) error {
		if err := cache.SetConnectionMetaTx(rdCtx, tx, id, map[string]any{"x": "y"}, "unavailable", rdTS("2026-03-18T12:30:00Z")); err != nil {
			t.Fatalf("SetConnectionMetaTx: %v", err)
		}
		return apperr.New(apperr.Internal, "force rollback")
	})
	after := rdMustGetConn(t, db)
	if !reflect.DeepEqual(after.SourceMeta, before.SourceMeta) || after.DevActivityState != before.DevActivityState {
		t.Errorf("connection changed although the transaction rolled back: before %+v after %+v", before, after)
	}
}

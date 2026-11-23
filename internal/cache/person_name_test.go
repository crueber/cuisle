package cache_test

// Regression: comment authors arrive from Jira with an account id only. A partial person must never blank the stored
// display name, active flag or account type (found driving the real stack: a speaker lost her name after a sync).

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/store/storetest"
)

func TestPersonUpserts_PartialRecordKeepsStoredName(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	db := storetest.Open(t, func() time.Time { return now })
	storetest.Exec(t, db, `INSERT INTO connection (id, source, base_url, account, created_at) VALUES (1, 'jira', 'https://a.test', 'me', 0)`)
	full := []model.Person{{AccountID: "u-iris", DisplayName: "Iris Calloway", Active: true, Kind: "user"}}
	partial := []model.Person{{AccountID: "u-iris"}}

	if err := cache.UpsertPeople(ctx, db.W, 1, full, now); err != nil {
		t.Fatal(err)
	}
	if err := cache.UpsertPeople(ctx, db.W, 1, partial, now); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error { return cache.ApplyPeopleTx(ctx, tx, 1, partial, now) }); err != nil {
		t.Fatal(err)
	}
	var name, kind string
	var active int
	if err := db.R.QueryRow(`SELECT display_name, active, COALESCE(account_type,'') FROM person WHERE account_id='u-iris'`).Scan(&name, &active, &kind); err != nil {
		t.Fatal(err)
	}
	if name != "Iris Calloway" || active != 1 || kind != "user" {
		t.Fatalf("partial upsert clobbered person: name=%q active=%d kind=%q", name, active, kind)
	}
	// A full record still updates the name.
	if err := cache.UpsertPeople(ctx, db.W, 1, []model.Person{{AccountID: "u-iris", DisplayName: "Iris R.", Active: true, Kind: "user"}}, now); err != nil {
		t.Fatal(err)
	}
	_ = db.R.QueryRow(`SELECT display_name FROM person WHERE account_id='u-iris'`).Scan(&name)
	if name != "Iris R." {
		t.Fatalf("full upsert did not update the name: %q", name)
	}
}

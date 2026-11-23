package cache

// WP-38 review fix: a panic inside a cache write transaction rolls it back, so the single writer connection is free
// for the next write (and the panic still reaches the caller).

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/store/storetest"
)

func TestWithWriteTx_PanicRollsBackAndReleasesTheWriter(t *testing.T) {
	db := storetest.Open(t, time.Now)

	panicked := func() (p any) {
		defer func() { p = recover() }()
		_ = withWriteTx(context.Background(), db.W, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`INSERT INTO ui_state (id) VALUES (99)`); err != nil {
				t.Logf("insert: %v", err)
			}
			panic("boom")
		})
		return nil
	}()
	if panicked == nil {
		t.Fatal("the panic did not reach the caller")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := withWriteTx(ctx, db.W, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `SELECT 1`)
		return err
	})
	if err != nil {
		t.Fatalf("a write after the panic failed (writer pinned?): %v", err)
	}
}

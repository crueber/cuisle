package store_test

// Tests for DB.WriteTx / DB.ReadTx.

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
)

const insertTheme = "INSERT INTO app_setting(key,value,updated_at) VALUES ('theme','dark',1)"

func TestWriteTx_CommitsWhenFnReturnsNil(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insertTheme)
		return err
	})
	if err != nil {
		t.Fatalf("WriteTx: %v", err)
	}
	if n := countRows(t, db.R, "app_setting"); n != 1 {
		t.Errorf("rows visible to a reader after commit = %d, want 1", n)
	}
}

func TestWriteTx_RollsBackWhenFnReturnsError(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	sentinel := errors.New("boom")
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, insertTheme); err != nil {
			return err
		}
		return sentinel
	})
	if err == nil {
		t.Fatal("WriteTx returned nil although fn failed")
	}
	if n := countRows(t, db.W, "app_setting"); n != 0 {
		t.Errorf("rows after rollback = %d, want 0", n)
	}
	// The writer is usable again (the rolled-back tx released its connection and lock).
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, insertTheme); return err }); err != nil {
		t.Fatalf("WriteTx after a rollback: %v", err)
	}
}

func TestWriteTx_WrapsPlainErrorAsRetryableDBError(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	sentinel := errors.New("boom")
	err := db.WriteTx(ctx, func(tx *sql.Tx) error { return sentinel })
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("error %T is not *apperr.Error", err)
	}
	if ae.Code != apperr.DB || !ae.Retryable {
		t.Errorf("error = {code %s, retryable %v}, want {db, true}", ae.Code, ae.Retryable)
	}
	if ae.Message == "" {
		t.Error("wrapped error has an empty user message")
	}
	if !errors.Is(err, sentinel) {
		t.Error("wrapped error must keep the cause reachable via errors.Is")
	}
}

func TestWriteTx_WrapsSQLConstraintFailureAsDBError(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		// violates CHECK (length(trim(name)) BETWEEN 1 AND 80)
		_, err := tx.ExecContext(ctx, "INSERT INTO standup(name, position, created_at, updated_at) VALUES ('', 0, 1, 1)")
		return err
	})
	if !apperr.Is(err, apperr.DB) {
		t.Errorf("code = %q, want db (err: %v)", apperr.CodeOf(err), err)
	}
	if n := countRows(t, db.W, "standup"); n != 0 {
		t.Errorf("standup rows = %d, want 0", n)
	}
}

func TestWriteTx_PassesAppErrorsThroughUnchanged(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	want := apperr.Validationf("name", "Name is required.")
	err := db.WriteTx(ctx, func(tx *sql.Tx) error { return want })
	var got *apperr.Error
	if !errors.As(err, &got) || got != want {
		t.Errorf("WriteTx returned %v; an *apperr.Error from fn must pass through as the same value (code %s)", err, apperr.CodeOf(err))
	}
	if got != nil && got.Code != apperr.Validation {
		t.Errorf("code = %s, want validation (must not be re-wrapped as db)", got.Code)
	}
}

func TestWriteTx_ConcurrentReadModifyWriteLosesNoUpdates(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	mustExec(t, db.W, "INSERT INTO app_setting(key,value,updated_at) VALUES ('n','0',0)")
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- db.WriteTx(ctx, func(tx *sql.Tx) error {
				var v int
				if err := tx.QueryRowContext(ctx, "SELECT CAST(value AS INTEGER) FROM app_setting WHERE key='n'").Scan(&v); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, "UPDATE app_setting SET value=? WHERE key='n'", fmt.Sprint(v+1))
				return err
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("WriteTx: %v", err)
		}
	}
	if got := scalarStr(t, db.W, "SELECT value FROM app_setting WHERE key='n'"); got != fmt.Sprint(workers) {
		t.Errorf("counter = %s, want %d (writes must serialise)", got, workers)
	}
}

func TestReadTx_SeesOneSnapshotForAllQueries(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t) // a file DB: snapshots need WAL
	mustExec(t, db.W, insertTheme)

	var before, during int
	err := db.ReadTx(ctx, func(tx *sql.Tx) error {
		before = int(scalarInt(t, tx, "SELECT count(*) FROM app_setting")) // the snapshot is taken at this first read
		// A concurrent writer commits while the read transaction is open.
		if err := db.WriteTx(ctx, func(w *sql.Tx) error {
			_, err := w.ExecContext(ctx, "INSERT INTO app_setting(key,value,updated_at) VALUES ('text_size','large',2)")
			return err
		}); err != nil {
			return err
		}
		during = int(scalarInt(t, tx, "SELECT count(*) FROM app_setting"))
		return nil
	})
	if err != nil {
		t.Fatalf("ReadTx: %v", err)
	}
	if before != 1 || during != 1 {
		t.Errorf("counts inside one ReadTx = %d then %d, want 1 and 1 (one snapshot)", before, during)
	}
	if n := countRows(t, db.R, "app_setting"); n != 2 {
		t.Errorf("a new read sees %d rows, want 2", n)
	}
}

func TestReadTx_RejectsWrites(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	err := db.ReadTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, insertTheme)
		return err
	})
	if err == nil {
		t.Error("a write inside ReadTx succeeded; the reader pool is query_only")
	}
	if n := countRows(t, db.W, "app_setting"); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
}

func TestReadTx_ReturnsFnError(t *testing.T) {
	defer recoverStub(t)
	db, _ := openTemp(t)
	want := apperr.New(apperr.NotFound, "Nothing here.")
	err := db.ReadTx(ctx, func(tx *sql.Tx) error { return want })
	var got *apperr.Error
	if !errors.As(err, &got) || got != want {
		t.Errorf("ReadTx returned %v, want the fn's *apperr.Error unchanged", err)
	}
}

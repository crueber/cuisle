package app_test

// F14 finder budget (HANDOFF "Data and API"): TicketService.FindByKey over 5 000 synced issues, p95 under 5 ms. The
// dataset is dataset L (bench_test.go, about 4 400 issues over 5 standups) topped up to 5 000 with closed issues that
// are on no board, over a real file database. The same call is BenchmarkFindByKey/L (baseline in bench/baseline.txt).

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/store"
)

const findBenchIssues = 5000

// findQueries mix every path: digits only (all projects), letters + digits, a short prefix with many matches, an exact
// key, a transposition, and a miss.
var findQueries = []string{"1", "12", "123", "1429", "ben1-1", "ben2-345", "ben3 1", "b4", "ben5-1429", "9999", "7", "ben1-20"}

func seedFindDataset(tb testing.TB) (path string, standup int64, total int) {
	tb.Helper()
	path = filepath.Join(tb.TempDir(), "cuisle.db")
	standup = seedDatasetL(tb, path)
	ctx := context.Background()
	db := openBenchDB(tb, path)
	defer func() { _ = db.Close(ctx) }()
	if err := db.R.QueryRowContext(ctx, `SELECT count(*) FROM issue`).Scan(&total); err != nil {
		tb.Fatal(err)
	}
	var conn int64
	if err := db.R.QueryRowContext(ctx, `SELECT id FROM connection`).Scan(&conn); err != nil {
		tb.Fatal(err)
	}
	now := store.Ms(benchNow)
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		for n := total; n < findBenchIssues; n++ {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO issue (connection_id, source_id, key, space_id, summary, issue_type_id, status_id, created_at, updated_at,
				   hydrated_at, last_seen_at, removed_at) VALUES (?, ?, ?, '10000', 'Extra', '10001', ?, ?, ?, ?, ?, ?)`,
				conn, fmt.Sprintf("9%07d", n), fmt.Sprintf("BEN%d-%d", 6+n%3, 10000+n), bsDone, now, now, now, now, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
	return path, standup, findBenchIssues
}

func TestFindByKey_P95UnderFiveMsAt5kIssues(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds a 5 000 issue database")
	}
	path, standup, total := seedFindDataset(t)
	ctx := context.Background()
	db := openBenchDB(t, path)
	defer func() { _ = db.Close(ctx) }()
	svcs := app.NewServices(benchDeps(db))
	hits := 0
	for _, q := range findQueries { // warm up (statement cache, page cache)
		r, err := svcs.Ticket.FindByKey(ctx, q, standup, 20)
		if err != nil {
			t.Fatalf("FindByKey %q: %v", q, err)
		}
		hits += len(r.Hits)
	}
	if hits == 0 {
		t.Fatal("the benchmark queries find nothing: the dataset is not what the test assumes")
	}
	var times []time.Duration
	for round := 0; round < 25; round++ {
		for _, q := range findQueries {
			start := time.Now()
			if _, err := svcs.Ticket.FindByKey(ctx, q, standup, 20); err != nil {
				t.Fatal(err)
			}
			times = append(times, time.Since(start))
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	p95 := times[len(times)*95/100]
	t.Logf("FindByKey over %d issues: p50 %v p95 %v max %v (%d calls)", total, times[len(times)/2], p95, times[len(times)-1], len(times))
	if p95 >= 5*time.Millisecond {
		t.Errorf("p95 = %v, budget 5 ms", p95)
	}
}

// BenchmarkFindByKey is FindByKey over dataset L topped up to 5 000 issues, cycling the query mix.
func BenchmarkFindByKey(b *testing.B) {
	path, standup, _ := seedFindDataset(b)
	b.Run("L", func(b *testing.B) {
		ctx := context.Background()
		db := openBenchDB(b, path)
		defer func() { _ = db.Close(ctx) }()
		svcs := app.NewServices(benchDeps(db))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := svcs.Ticket.FindByKey(ctx, findQueries[i%len(findQueries)], standup, 20); err != nil {
				b.Fatal(err)
			}
		}
	})
}

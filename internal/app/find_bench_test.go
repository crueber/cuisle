package app_test

// F14 finder budget (HANDOFF "Data and API"): TicketService.FindByKey (key and title) over 5 000 synced issues, p95
// under 5 ms. The dataset is dataset L (bench_test.go, about 4 400 issues over 5 standups) topped up to 5 000 with
// closed issues that are on no board, over a real file database; every issue then gets a varied, realistic summary so
// that title queries match like they do on a real board. The same call is BenchmarkFindByKey/L (baseline in
// bench/baseline.txt).

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/store"
)

const findBenchIssues = 5000

// findQueries mix every path: digits only (all projects), letters + digits, a short prefix with many matches, an exact
// key, a transposition, and a miss; then the title paths: a two-letter query that matches most summaries, one word,
// several words in any order, a subsequence, and a miss.
var findQueries = []string{
	"1", "12", "123", "1429", "ben1-1", "ben2-345", "ben3 1", "b4", "ben5-1429", "9999", "7", "ben1-20",
	"re", "recipe", "card recipe", "swipe list", "rcd", "offline mode saved", "zzzq", "oauth2",
}

var summaryWords = []string{
	"recipe", "card", "crops", "hero", "photo", "swipe", "check", "items", "shopping", "list", "offline", "mode", "saved",
	"recipes", "meal", "plan", "templates", "busy", "weeks", "dark", "contrast", "ingredient", "checkboxes", "import",
	"web", "page", "layout", "tablets", "sync", "login", "timeout", "retry", "push", "notification", "badge", "search",
	"filter", "sort", "export", "share", "image", "upload", "crash", "scroll", "keyboard", "focus", "label", "button",
}

// varySummaries gives every issue a four to seven word summary drawn from summaryWords, deterministically.
func varySummaries(tb testing.TB, db *store.DB) {
	tb.Helper()
	ctx := context.Background()
	var ids []int64
	rows, err := db.R.QueryContext(ctx, `SELECT id FROM issue`)
	if err != nil {
		tb.Fatal(err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			tb.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		tb.Fatal(err)
	}
	_ = rows.Close()
	err = db.WriteTx(ctx, func(tx *sql.Tx) error {
		st, err := tx.PrepareContext(ctx, `UPDATE issue SET summary = ? WHERE id = ?`)
		if err != nil {
			return err
		}
		defer st.Close()
		for _, id := range ids {
			n, x := 4+int(id%4), id*2654435761
			var words []string
			for k := 0; k < n; k++ {
				x = x*6364136223846793005 + 1442695040888963407
				w := summaryWords[int(uint64(x)>>33)%len(summaryWords)]
				if k == 0 {
					w = strings.ToUpper(w[:1]) + w[1:]
				}
				words = append(words, w)
			}
			if _, err := st.ExecContext(ctx, strings.Join(words, " "), id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatal(err)
	}
}

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
	varySummaries(tb, db)
	return path, standup, findBenchIssues
}

func TestFindByKey_P95UnderFiveMsAt5kIssues(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds a 5 000 issue database")
	}
	// Timing budgets are enforced locally and on Forgejo, and are report-only on GitHub's shared runners (Chris's
	// decision for the perf budgets; AGENTS §8): there the p95 swings far past 5 ms with runner load.
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		t.Skip("timing budget is not enforced on GitHub's shared runners; BenchmarkFindByKey still runs")
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

// The title index is rebuilt only after an issue changes (migration 0008's generation), so the first title query after a
// sync that changed something pays one rebuild. This reports that cold cost at 5 000 issues beside the warm p95 and
// holds it to a sane bound (not enforced on GitHub's shared runners; it is a once-per-sync cost, not a per-keystroke one).
func TestFindByKey_ColdTitleIndexAfterAnIssueChangeAt5kIssues(t *testing.T) {
	if testing.Short() {
		t.Skip("seeds a 5 000 issue database")
	}
	path, standup, total := seedFindDataset(t)
	ctx := context.Background()
	db := openBenchDB(t, path)
	defer func() { _ = db.Close(ctx) }()
	svcs := app.NewServices(benchDeps(db))
	if _, err := svcs.Ticket.FindByKey(ctx, "recipe card", standup, 20); err != nil { // warm the statement and page caches
		t.Fatal(err)
	}
	var cold []time.Duration
	for i := 0; i < 20; i++ {
		err := db.WriteTx(ctx, func(tx *sql.Tx) error { // an issue changes: what a sync that found news does
			_, err := tx.ExecContext(ctx, `UPDATE issue SET summary = summary || '!' WHERE id = (SELECT min(id) FROM issue)`)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		r, err := svcs.Ticket.FindByKey(ctx, "recipe card", standup, 20)
		if err != nil {
			t.Fatal(err)
		}
		cold = append(cold, time.Since(start))
		if len(r.Hits) == 0 {
			t.Fatal("no hits")
		}
	}
	sort.Slice(cold, func(i, j int) bool { return cold[i] < cold[j] })
	t.Logf("first title query after an issue changed, %d issues: median %v max %v (%d runs)", total, cold[len(cold)/2], cold[len(cold)-1], len(cold))
	if os.Getenv("GITHUB_ACTIONS") != "true" && cold[len(cold)/2] >= 15*time.Millisecond {
		t.Errorf("cold median = %v, bound 15 ms", cold[len(cold)/2])
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

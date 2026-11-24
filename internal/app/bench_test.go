package app_test

// WP-35 benchmarks (architecture.md section 6.1), over a real file database seeded with dataset "L": 5 standups,
// 400 issues in the visible one, a 28-day pulse and about three years of history (data-model.md section 5.1: ~1 000
// issues a year per board with their change, comment and dev rows).
//
//	BenchmarkBootstrap/L      store.Open (migrate check) + AppService.Bootstrap + Close         budget <= 60 ms
//	BenchmarkStandupView/L    cache.LoadCurrentTx + view.Build (what StandupService.View runs)  budget <= 15 ms
//
// The pure view.Build benchmark (budget <= 2 MB/op as well) is BenchmarkStandupView in internal/view; the load half
// (cache.LoadCurrentTx) allocates on top of that. BenchmarkConfigUpdate is in internal/settings.
//
// The seeder writes the old record rows with prepared statements and the live board through the cache helpers the
// sync apply step uses, so the data has exactly the shape a real sync leaves. It runs once per benchmark (the
// sub-benchmark keeps the parent, and so the temp dir, to one invocation).
//
// TestWriteBenchDataset writes the same dataset into CUISLE_BENCH_SEED_DIR (as cuisle.db) so that
// `cuisle --bench-startup` can be run against dataset L; it is skipped otherwise.

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/app"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/paths"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/store"
	syncpkg "github.com/crueber/cuisle/internal/sync"
)

var benchLoc = time.FixedZone("CST", -6*3600)

// benchNow is "now" for every dataset-L benchmark: Monday 2026-11-23 09:30 local.
var benchNow = time.Date(2026, 11, 23, 9, 30, 0, 0, benchLoc)

type benchClock struct{}

func (benchClock) Now() time.Time           { return benchNow }
func (benchClock) Location() *time.Location { return benchLoc }

const (
	benchStandups   = 5
	benchBoardIssue = 400 // board-role issues in the visible standup (plus a 60-row backlog slice)
	benchBacklog    = 60
	benchOtherBoard = 80
	benchOldIssues  = 3000 // closed, left the board: ~1 000 a year for 3 years, visible standup
	benchOldOthers  = 600
)

var (
	bsTodo   = "10001"
	bsProg   = "10002"
	bsReview = "10003"
	bsQA     = "10004"
	bsDone   = "10005"
	bsBack   = "10006"
)

// seedDatasetL builds the dataset in a new database at dbPath and returns the visible (first) standup id.
func seedDatasetL(tb testing.TB, dbPath string) int64 {
	tb.Helper()
	ctx := context.Background()
	clock := func() time.Time { return benchNow }
	db, _, err := store.Open(ctx, dbPath, store.Options{Now: clock})
	if err != nil {
		tb.Fatalf("seed: open: %v", err)
	}
	defer func() { _ = db.Close(ctx) }()

	st := settings.New(db, clock)
	ids := make([]int64, benchStandups)
	for i := range ids {
		if ids[i], err = st.Create(ctx); err != nil {
			tb.Fatalf("seed: create standup: %v", err)
		}
	}

	T := benchNow.Add(-90 * time.Minute) // the last sync
	err = db.WriteTx(ctx, func(tx *sql.Tx) error {
		return seedTx(ctx, tx, ids, T)
	})
	if err != nil {
		tb.Fatalf("seed: %v", err)
	}
	if _, err := db.W.ExecContext(ctx, `UPDATE ui_state SET last_standup_id = ? WHERE id = 1`, ids[0]); err != nil {
		tb.Fatalf("seed: ui_state: %v", err)
	}
	return ids[0]
}

func seedTx(ctx context.Context, tx *sql.Tx, ids []int64, T time.Time) error {
	res, err := tx.ExecContext(ctx,
		`INSERT INTO connection (base_url, account, account_id, display_name, auth_state, created_at)
		 VALUES ('https://bench.atlassian.net', 'bench@example.com', 'acct-bench', 'Bench', 'ok', ?)`, store.Ms(T))
	if err != nil {
		return err
	}
	connID, _ := res.LastInsertId()
	if _, err := tx.ExecContext(ctx, `UPDATE standup SET connection_id = ?`, connID); err != nil {
		return err
	}

	statuses := []model.Status{
		{ID: bsTodo, Name: "To Do", Category: "new"}, {ID: bsProg, Name: "In Progress", Category: "indeterminate"},
		{ID: bsReview, Name: "In Review", Category: "indeterminate"}, {ID: bsQA, Name: "QA", Category: "indeterminate"},
		{ID: bsDone, Name: "Done", Category: "done"}, {ID: bsBack, Name: "Backlog", Category: "new"},
	}
	if err := cache.ApplyStatusesTx(ctx, tx, connID, statuses); err != nil {
		return err
	}
	if err := cache.ApplyIssueTypesTx(ctx, tx, connID, []model.IssueType{{ID: "10001", Name: "Task", Kind: "task"}}); err != nil {
		return err
	}
	var people []model.Person
	for i := 0; i < 12; i++ {
		people = append(people, model.Person{AccountID: fmt.Sprintf("p%d", i), DisplayName: fmt.Sprintf("Person%d Tester", i), Active: true, Kind: "user"})
	}
	if err := cache.ApplyPeopleTx(ctx, tx, connID, people, T); err != nil {
		return err
	}

	columns := []struct{ name, lane, status string }{
		{"Backlog", "backlog", bsBack}, {"To Do", "todo", bsTodo}, {"In Progress", "prog", bsProg},
		{"In Review", "review", bsReview}, {"QA", "qa", bsQA}, {"Done", "done", bsDone},
	}
	for k, sid := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE standup SET name = ?, space_id = '10000', space_key = 'BEN', space_name = 'Bench',
			   board_id = ?, board_name = ?, board_type = 'scrum', scope = 'board' WHERE id = ?`,
			fmt.Sprintf("Standup %d", k+1), fmt.Sprint(100+k), fmt.Sprintf("Board %d", k+1), sid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE standup_settings SET pulse_days = 28, done_window = '14d' WHERE standup_id = ?`, sid); err != nil {
			return err
		}
		for i, c := range columns {
			if _, err := tx.ExecContext(ctx, `INSERT INTO standup_column_map (standup_id, column_name, position, lane) VALUES (?, ?, ?, ?)`, sid, c.name, i, c.lane); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO standup_status_lane (standup_id, status_id, status_name, column_name, lane) VALUES (?, ?, ?, ?, ?)`, sid, c.status, c.name, c.name, c.lane); err != nil {
				return err
			}
		}
		for i := 0; i < 12; i++ {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO standup_speaker (standup_id, account_id, display_name, position, included, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
				sid, fmt.Sprintf("p%d", i), fmt.Sprintf("Person%d Tester", i), i, store.Ms(T)); err != nil {
				return err
			}
		}
	}

	old, err := newOldRows(ctx, tx)
	if err != nil {
		return err
	}
	defer old.close()

	for k, sid := range ids {
		nBoard, nBack, nOld := benchOtherBoard, 20, benchOldOthers
		if k == 0 {
			nBoard, nBack, nOld = benchBoardIssue, benchBacklog, benchOldIssues
		}
		var refs []cache.MemberRef
		for i := 0; i < nBoard+nBack; i++ {
			backlog := i >= nBoard
			d := liveIssue(k, i, backlog, T)
			id, err := cache.ApplyIssueTx(ctx, tx, connID, d, T)
			if err != nil {
				return err
			}
			if ev := liveDev(i); len(ev) > 0 {
				if err := cache.ApplyDevTx(ctx, tx, id, model.DevActivity{Events: ev}); err != nil {
					return err
				}
			}
			role, rank := model.RoleBoard, i
			if backlog {
				role, rank = model.RoleBacklog, i-nBoard
			}
			refs = append(refs, cache.MemberRef{IssueID: id, Role: role, RankIndex: rank})
		}
		if err := cache.ApplyMembersTx(ctx, tx, sid, refs, T.AddDate(-3, 0, 0)); err != nil {
			return err
		}
		if err := old.add(ctx, connID, sid, k, nOld, T); err != nil {
			return err
		}
		if err := cache.WriteSyncStateTx(ctx, tx, sid, T, true, nBack*10, nil); err != nil {
			return err
		}
		cfg, err := settings.EffectiveTx(ctx, tx, sid)
		if err != nil {
			return err
		}
		doc, fp := cache.ConfigJSON(cfg)
		if _, err := cache.RecordConfigTx(ctx, tx, sid, doc, fp, T.AddDate(-3, 0, 0)); err != nil {
			return err
		}
	}
	return nil
}

// liveIssue is one issue on the board now. A third are "ancient": created about three years ago with a long
// to-do/in-progress ping-pong, so the status history carries three years. The rest walk forward from 30-50 days ago.
func liveIssue(k, i int, backlog bool, T time.Time) model.IssueDetail {
	key := fmt.Sprintf("BEN%d-%d", k+1, i+1)
	assignee := fmt.Sprintf("p%d", i%12)
	if i%13 == 0 {
		assignee = ""
	}
	path := []string{bsTodo, bsProg, bsReview, bsQA, bsDone}
	d := model.IssueDetail{Issue: model.Issue{
		SourceID: fmt.Sprintf("%d%05d", k+1, i+1), Key: key, SpaceID: "10000", Summary: "Benchmark ticket " + key,
		TypeID: "10001", StatusID: bsTodo, AssigneeID: assignee, Rank: fmt.Sprintf("0|%06d:", i), UpdatedAt: T,
	}}
	if backlog {
		d.StatusID = bsBack
		d.CreatedAt = T.AddDate(0, 0, -60)
		return d
	}
	base := T.AddDate(0, 0, -(30 + i%20))
	d.CreatedAt = base
	var prev = bsTodo
	chg := func(at time.Time, to string) {
		d.StatusChanges = append(d.StatusChanges, model.StatusChange{ID: fmt.Sprintf("h%d", len(d.StatusChanges)+1), At: at, From: prev, To: to})
		prev = to
	}
	if i%3 == 0 {
		d.CreatedAt = T.AddDate(0, 0, -(1090 - i%30))
		for m := 0; m < 12; m++ {
			to := bsProg
			if m%2 == 1 {
				to = bsTodo
			}
			chg(d.CreatedAt.AddDate(0, 0, 60*(m+1)), to)
		}
	}
	steps := 1 + i%5
	for m := 1; m < steps; m++ {
		chg(base.AddDate(0, 0, 5*m), path[m])
	}
	d.StatusID = prev
	if prev == bsDone {
		d.ResolvedAt = T.AddDate(0, 0, -(1 + i%12)) // inside the 14-day done window, so it is shown
		d.StatusChanges[len(d.StatusChanges)-1].At = d.ResolvedAt
	}
	author := assignee
	if author == "" {
		author = "p0"
	}
	for c := 0; c < 3+i%4; c++ {
		d.Comments = append(d.Comments, model.Comment{ID: fmt.Sprintf("c%d", c+1), At: T.AddDate(0, 0, -(c*4 + i%5)), AuthorID: author})
	}
	d.CommentTotal = len(d.Comments)
	if i%7 == 0 {
		d.FlagChanges = []model.FlagChange{{ID: "f1", At: T.AddDate(0, 0, -3), Flagged: true}}
		d.Flagged = true
	}
	return d
}

func liveDev(i int) []model.DevEvent {
	if i%3 != 0 {
		return nil
	}
	var ev []model.DevEvent
	for c := 0; c < 4; c++ {
		ev = append(ev, model.DevEvent{Kind: model.DevCommit, ID: fmt.Sprintf("sha-%d-%d", i, c), Repo: "mob", At: benchNow.AddDate(0, 0, -(c*5 + i%4))})
	}
	ev = append(ev, model.DevEvent{Kind: model.DevPR, ID: fmt.Sprintf("svc#%d", i), Repo: "mob", At: benchNow.AddDate(0, 0, -6), Status: "OPEN"})
	return ev
}

// oldRows bulk-inserts the closed history (issues that left every board) with prepared statements.
type oldRows struct {
	issue, change, comment, commit, pr, span *sql.Stmt
}

func newOldRows(ctx context.Context, tx *sql.Tx) (*oldRows, error) {
	o := &oldRows{}
	var err error
	prep := func(dst **sql.Stmt, q string) {
		if err == nil {
			*dst, err = tx.PrepareContext(ctx, q)
		}
	}
	prep(&o.issue, `INSERT INTO issue (connection_id, source_id, key, space_id, summary, issue_type_id, status_id, assignee_account_id,
	  rank, created_at, updated_at, resolved_at, comment_total, hydrated_at, last_seen_at, removed_at)
	  VALUES (?, ?, ?, '10000', 'Old ticket', '10001', ?, ?, ?, ?, ?, ?, 2, ?, ?, ?) RETURNING id`)
	prep(&o.change, `INSERT INTO status_change (issue_id, history_id, changed_at, from_status_id, to_status_id) VALUES (?, ?, ?, ?, ?)`)
	prep(&o.comment, `INSERT INTO comment (issue_id, comment_id, author_account_id, created_at) VALUES (?, ?, ?, ?)`)
	prep(&o.commit, `INSERT INTO dev_commit (issue_id, commit_id, repo, authored_at, is_merge) VALUES (?, ?, 'svc', ?, 0)`)
	prep(&o.pr, `INSERT INTO dev_pr_event (issue_id, pr_key, status, at) VALUES (?, ?, 'MERGED', ?)`)
	prep(&o.span, `INSERT INTO standup_issue_span (standup_id, issue_id, role, valid_from, valid_to) VALUES (?, ?, 'board', ?, ?)`)
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (o *oldRows) close() {
	for _, s := range []*sql.Stmt{o.issue, o.change, o.comment, o.commit, o.pr, o.span} {
		if s != nil {
			_ = s.Close()
		}
	}
}

func (o *oldRows) add(ctx context.Context, connID, standupID int64, k, n int, T time.Time) error {
	path := []string{bsTodo, bsProg, bsReview, bsQA, bsDone}
	for i := 0; i < n; i++ {
		// Spread over 3 years, finishing between 3 years and 3 weeks ago.
		done := T.AddDate(0, 0, -(21 + i*1070/n))
		created := done.AddDate(0, 0, -20)
		var id int64
		if err := o.issue.QueryRowContext(ctx, connID, fmt.Sprintf("9%d%05d", k+1, i+1), fmt.Sprintf("OLD%d-%d", k+1, i+1), bsDone,
			fmt.Sprintf("p%d", i%12), fmt.Sprintf("1|%06d:", i), store.Ms(created), store.Ms(done), store.Ms(done),
			store.Ms(done), store.Ms(done), store.Ms(done.Add(time.Hour))).Scan(&id); err != nil {
			return err
		}
		for m := 1; m < len(path); m++ {
			at := created.AddDate(0, 0, 4*m)
			if _, err := o.change.ExecContext(ctx, id, fmt.Sprintf("h%d", m), store.Ms(at), path[m-1], path[m]); err != nil {
				return err
			}
		}
		for c := 0; c < 2; c++ {
			if _, err := o.comment.ExecContext(ctx, id, fmt.Sprintf("c%d", c), fmt.Sprintf("p%d", i%12), store.Ms(created.AddDate(0, 0, 3+c))); err != nil {
				return err
			}
		}
		for c := 0; c < 3; c++ {
			if _, err := o.commit.ExecContext(ctx, id, fmt.Sprintf("sha%d", c), store.Ms(created.AddDate(0, 0, 5+c))); err != nil {
				return err
			}
		}
		if _, err := o.pr.ExecContext(ctx, id, fmt.Sprintf("svc#%d", i), store.Ms(done)); err != nil {
			return err
		}
		if _, err := o.span.ExecContext(ctx, standupID, id, store.Ms(created), store.Ms(done.Add(time.Hour))); err != nil {
			return err
		}
	}
	return nil
}

// benchDeps wires the services over the file database at dbPath the way cmd/cuisle does, minus network and window.
func benchDeps(db *store.DB) *app.Deps {
	d := &app.Deps{
		DB: db, Settings: settings.New(db, func() time.Time { return benchNow }), Keyring: secrets.NewMemory(),
		Emitter: nopEmitter{}, Clock: benchClock{}, Logger: slog.New(slog.NewTextHandler(discard{}, nil)),
		Paths: paths.Paths{}, Now: func() time.Time { return benchNow },
	}
	d.Sync = syncpkg.New(syncpkg.Options{DB: db, Keyring: d.Keyring, Emitter: d.Emitter, Clock: d.Clock, Logger: d.Logger})
	return d
}

type nopEmitter struct{}

func (nopEmitter) Emit(string, any) {}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func openBenchDB(b testing.TB, dbPath string) *store.DB {
	b.Helper()
	db, _, err := store.Open(context.Background(), dbPath, store.Options{Now: func() time.Time { return benchNow }, Logger: slog.New(slog.NewTextHandler(discard{}, nil))})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	return db
}

// checkDatasetL fails the benchmark when the seeded database does not produce a dataset-L view (an empty view would
// make every number meaningless).
func checkDatasetL(b *testing.B, boot app.Bootstrap) {
	b.Helper()
	v := boot.View
	if v == nil {
		b.Fatal("Bootstrap carries no view: the seeded standup was not picked")
	}
	hist := 0
	for _, t := range v.Tickets {
		if len(t.Pulse.Segs) > 1 {
			hist++
		}
	}
	if len(boot.Standups) != benchStandups || len(v.Tickets) < 380 || v.PulseDays != 28 || len(v.Axis) != 28 || hist < 100 {
		b.Fatalf("not dataset L: standups=%d tickets=%d pulseDays=%d axis=%d multi-segment=%d",
			len(boot.Standups), len(v.Tickets), v.PulseDays, len(v.Axis), hist)
	}
}

func seededDB(b *testing.B) (string, int64) {
	b.Helper()
	path := filepath.Join(b.TempDir(), "cuisle.db")
	start := time.Now()
	id := seedDatasetL(b, path)
	b.Logf("seeded dataset L in %v", time.Since(start).Round(time.Millisecond))
	return path, id
}

// BenchmarkBootstrap is the Go side of a cold start: open the file database (pragmas + migration check) and build the
// Bootstrap, including the visible standup's view. Budget (architecture 6.1): <= 60 ms/op.
func BenchmarkBootstrap(b *testing.B) {
	path, _ := seededDB(b)
	b.Run("L", func(b *testing.B) {
		ctx := context.Background()
		{
			db := openBenchDB(b, path)
			boot, err := app.NewServices(benchDeps(db)).App.Bootstrap(ctx)
			if err != nil {
				b.Fatalf("Bootstrap: %v", err)
			}
			checkDatasetL(b, boot)
			_ = db.Close(ctx)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			db := openBenchDB(b, path)
			if _, err := app.NewServices(benchDeps(db)).App.Bootstrap(ctx); err != nil {
				b.Fatalf("Bootstrap: %v", err)
			}
			b.StopTimer()
			_ = db.Close(ctx)
			b.StartTimer()
		}
	})
}

// BenchmarkStandupView is StandupService.View on dataset L: cache.LoadCurrentTx + view.Build together (the pure
// Build alone is BenchmarkStandupView in internal/view). Budget: <= 15 ms and <= 2 MB per op.
func BenchmarkStandupView(b *testing.B) {
	path, id := seededDB(b)
	b.Run("L", func(b *testing.B) {
		ctx := context.Background()
		db := openBenchDB(b, path)
		defer func() { _ = db.Close(ctx) }()
		svcs := app.NewServices(benchDeps(db))
		boot, err := svcs.App.Bootstrap(ctx)
		if err != nil {
			b.Fatalf("Bootstrap: %v", err)
		}
		checkDatasetL(b, boot)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := svcs.Standup.View(ctx, id); err != nil {
				b.Fatalf("View: %v", err)
			}
		}
	})
}

// TestWriteBenchDataset seeds dataset L into $CUISLE_BENCH_SEED_DIR/cuisle.db (for `cuisle --bench-startup`).
func TestWriteBenchDataset(t *testing.T) {
	dir := os.Getenv("CUISLE_BENCH_SEED_DIR")
	if dir == "" {
		t.Skip("set CUISLE_BENCH_SEED_DIR to write dataset L there")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	seedDatasetL(t, filepath.Join(dir, paths.DBFile))
}

// BenchmarkTicketDetail is TicketService.Get on dataset L (snapshot, ticket, fields in use and BuildTicketWith in one
// call; the dataset has no synced content, so this is the floor). Budget: <= 25 ms per op (architecture §6.1).
func BenchmarkTicketDetail(b *testing.B) {
	path, id := seededDB(b)
	b.Run("L", func(b *testing.B) {
		ctx := context.Background()
		db := openBenchDB(b, path)
		defer func() { _ = db.Close(ctx) }()
		svcs := app.NewServices(benchDeps(db))
		v, err := svcs.Standup.View(ctx, id)
		if err != nil || len(v.Tickets) == 0 {
			b.Fatalf("View: %v (%d tickets)", err, len(v.Tickets))
		}
		issue := v.Tickets[len(v.Tickets)/2].ID
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := svcs.Ticket.Get(ctx, id, issue); err != nil {
				b.Fatalf("Get: %v", err)
			}
		}
	})
}

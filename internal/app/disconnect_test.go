package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	gosync "sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/media"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/secrets"
	"github.com/crueber/cuisle/internal/settings"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira"
	"github.com/crueber/cuisle/internal/source/jira/jiratest"
	"github.com/crueber/cuisle/internal/store/storetest"
	syncpkg "github.com/crueber/cuisle/internal/sync"
)

type stubMediaSource struct {
	source.Source
	fetchCalls int
	onFetch    func(context.Context) (source.MediaBlob, error)
}

func (s *stubMediaSource) Capabilities() source.Capabilities {
	return source.Capabilities{Content: true}
}

func (s *stubMediaSource) FetchMedia(ctx context.Context, _ model.Attachment, _ bool) (source.MediaBlob, error) {
	s.fetchCalls++
	if s.onFetch != nil {
		return s.onFetch(ctx)
	}
	return source.MediaBlob{Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestFetchThumbsAsync_SkipsWhileBgPaused(t *testing.T) {
	db := storetest.Open(t, time.Now)
	mc := media.New(filepath.Join(t.TempDir(), "media"), db, func() int64 { return 1 << 20 }, media.Options{})
	d := &Deps{DB: db, Media: mc}
	src := &stubMediaSource{}

	d.ticketMu.Lock()
	d.bgPaused++
	d.ticketMu.Unlock()

	d.fetchThumbsAsync(src, cache.ConnRow{ID: 1}, issueRef{SourceID: "100"})
	d.bg.Wait()

	if d.bgCtx != nil {
		t.Fatalf("fetchThumbsAsync created bgCtx while bgPaused > 0")
	}
	if src.fetchCalls != 0 {
		t.Fatalf("FetchMedia calls = %d while paused, want 0", src.fetchCalls)
	}
}

type memSecrets struct {
	mu gosync.Mutex
	m  map[string]string
}

func (k *memSecrets) Get(account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[account]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (k *memSecrets) Set(account, secret string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[account] = secret
	return nil
}
func (k *memSecrets) Delete(account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.m, account)
	return nil
}

type testClock struct {
	mu  gosync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *testClock) Location() *time.Location { return time.UTC }
func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type mediaHookCache struct {
	media.Cache
	onClear func(context.Context) error
}

func (m *mediaHookCache) Clear(ctx context.Context) error {
	if m.onClear != nil {
		if err := m.onClear(ctx); err != nil {
			return err
		}
	}
	return m.Cache.Clear(ctx)
}

type gatingRT struct {
	inner   http.RoundTripper
	match   func(*http.Request) bool
	reached chan struct{}
	release chan struct{}
	once1   gosync.Once
	once2   gosync.Once
}

func newGatingRT(match func(*http.Request) bool) *gatingRT {
	return &gatingRT{
		inner:   http.DefaultTransport.(*http.Transport).Clone(),
		match:   match,
		reached: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *gatingRT) RoundTrip(r *http.Request) (*http.Response, error) {
	if g.match != nil && g.match(r) {
		g.once1.Do(func() { close(g.reached) })
		select {
		case <-g.release:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}
	return g.inner.RoundTrip(r)
}

func (g *gatingRT) Release() { g.once2.Do(func() { close(g.release) }) }

type disconnectEnv struct {
	d      *Deps
	connID int64
	sid    int64
	clk    *testClock
	gate   *gatingRT
	srv    *jiratest.Server
}

func newDisconnectEnv(t *testing.T, gateMatch func(*http.Request) bool, mcWrap func(media.Cache) media.Cache) *disconnectEnv {
	t.Helper()
	source.Register(jira.Kind, jira.New, jira.IssueURL)

	clk := &testClock{now: time.Date(2026, 11, 23, 15, 0, 0, 0, time.UTC)}
	db := storetest.Open(t, clk.Now)
	fx, err := jiratest.ParseFixture(jiratest.DefaultFixture)
	if err != nil {
		t.Fatal(err)
	}
	srv := jiratest.NewServer(t, fx, jiratest.Options{Now: clk.Now(), Loc: time.UTC})

	kr := &memSecrets{}
	acct := secrets.ConnectionAccount(jira.Kind, srv.URL, "me@example.com")
	if err := kr.Set(acct, jiratest.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`INSERT INTO connection (source, base_url, account, account_id, display_name, auth_state, verified_at, created_at)
		VALUES ('jira', ?, 'me@example.com', 'u-me', 'Me', 'ok', ?, ?)`, srv.URL, clk.Now().UnixMilli(), clk.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var connID int64
	if err := db.R.QueryRow(`SELECT id FROM connection WHERE base_url = ?`, srv.URL).Scan(&connID); err != nil {
		t.Fatal(err)
	}

	st := settings.New(db, clk.Now)
	ctx := context.Background()
	sid, err := st.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var proj jiratest.FxProject
	var board jiratest.FxBoard
	for _, p := range fx.Projects {
		if p.Key == "MOB" {
			proj = p
			break
		}
	}
	for _, b := range fx.Boards {
		if b.ProjectKey == "MOB" {
			board = b
			break
		}
	}
	if proj.ID == "" || board.ID == 0 {
		t.Fatal("default fixture missing MOB project or board")
	}
	if _, err := st.SetSpace(ctx, sid, model.Space{ID: proj.ID, Key: proj.Key, Name: proj.Name}); err != nil {
		t.Fatal(err)
	}
	directSrc, err := jira.New(model.Connection{ID: connID, Source: jira.Kind, BaseURL: srv.URL, Account: "me@example.com"},
		jiratest.Token, source.Options{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := directSrc.BoardConfig(ctx, strconv.Itoa(board.ID))
	if err != nil {
		t.Fatal(err)
	}
	statuses, _, err := directSrc.Reference(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetBoard(ctx, sid, cfg, statuses); err != nil {
		t.Fatal(err)
	}

	gate := newGatingRT(gateMatch)
	t.Cleanup(func() {
		gate.Release()
		gate.inner.(*http.Transport).CloseIdleConnections()
	})
	httpClient := &http.Client{Transport: gate}
	logger := slog.New(slog.DiscardHandler)
	srcOpts := source.Options{HTTP: httpClient, Logger: logger, Now: time.Now}

	var mc media.Cache = media.New(filepath.Join(t.TempDir(), "media"), db, func() int64 { return 64 << 20 }, media.Options{Now: clk.Now, Logger: logger})
	if mcWrap != nil {
		mc = mcWrap(mc)
	}

	orch := syncpkg.New(syncpkg.Options{
		DB: db, Keyring: kr, Clock: clk, Logger: logger, SourceOpts: srcOpts, Media: NewMediaStore(mc),
	})
	t.Cleanup(func() { _ = orch.Shutdown(context.Background()) })

	d := &Deps{
		DB: db, Settings: st, Keyring: kr, Sync: orch, Clock: clk, Logger: logger,
		SourceOpts: srcOpts, HTTP: httpClient, Now: clk.Now, Media: mc,
	}
	return &disconnectEnv{d: d, connID: connID, sid: sid, clk: clk, gate: gate, srv: srv}
}

func TestDeleteContent_CancelsMediaPhaseAndBlocksConcurrentSyncStart(t *testing.T) {
	ctx := context.Background()
	env := newDisconnectEnv(t, func(r *http.Request) bool {
		return strings.HasPrefix(r.URL.Path, "/media/file/")
	}, nil)

	if _, err := env.d.Sync.Start(ctx, env.sid, syncpkg.ModeFull, syncpkg.TriggerManual); err != nil {
		t.Fatalf("Start: %v", err)
	}
	env.d.Sync.Wait(env.sid)

	select {
	case <-env.gate.reached:
	case <-time.After(15 * time.Second):
		t.Fatal("media phase did not reach /media/file/ gate")
	}

	// Hold an in-flight ticket lock so deleteContent pauses inside its drain step after PauseAndCancel.
	var issueID int64
	if err := env.d.DB.R.QueryRow(`SELECT id FROM issue LIMIT 1`).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	rel, ok := env.d.lockIssue(issueID)
	if !ok {
		t.Fatal("initial lockIssue failed")
	}

	delErr := make(chan error, 1)
	go func() {
		delErr <- env.d.deleteContent(ctx, env.connID)
	}()

	// Wait until deleteContent has completed PauseAndCancel and set bgPaused (which happens right after PauseAndCancel).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if relTmp, ok := env.d.lockIssue(issueID + 1); !ok {
			break
		} else {
			relTmp()
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := env.d.Sync.Start(ctx, env.sid, syncpkg.ModeIncremental, syncpkg.TriggerManual); !apperr.Is(err, apperr.Conflict) {
		rel()
		t.Fatalf("concurrent Start while deleteContent is draining err = %v, want conflict", err)
	}
	if relTmp, ok := env.d.lockIssue(issueID + 1); ok {
		relTmp()
		rel()
		t.Fatal("lockIssue succeeded while deleteContent was draining")
	}

	// Releasing the ticket lock allows deleteContent to finish draining and wipe content.
	rel()
	select {
	case err := <-delErr:
		if err != nil {
			t.Fatalf("deleteContent: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deleteContent hung")
	}

	var contentCount int
	if err := env.d.DB.R.QueryRow(`SELECT COUNT(*) FROM issue_content`).Scan(&contentCount); err != nil {
		t.Fatal(err)
	}
	if contentCount != 0 {
		t.Fatalf("issue_content count = %d after deleteContent, want 0", contentCount)
	}

	// After deleteContent returns, both lockIssue and Sync.Start work again.
	rel2, ok := env.d.lockIssue(issueID)
	if !ok {
		t.Fatal("lockIssue after deleteContent returned false")
	}
	rel2()

	env.gate.Release()
	env.clk.Advance(time.Hour)
	if _, err := env.d.Sync.Start(ctx, env.sid, syncpkg.ModeIncremental, syncpkg.TriggerManual); err != nil {
		t.Fatalf("Sync.Start after deleteContent: %v", err)
	}
	env.d.Sync.Wait(env.sid)
	env.d.Sync.WaitMedia()
}

func TestDeleteContent_CancelsAndWaitsFetchThumbsAsyncBeforeMediaClear(t *testing.T) {
	ctx := context.Background()
	fetchEntered := make(chan struct{})
	fetchExited := make(chan struct{})
	var exitedBeforeClear atomic.Bool

	env := newDisconnectEnv(t, nil, func(inner media.Cache) media.Cache {
		return &mediaHookCache{
			Cache: inner,
			onClear: func(context.Context) error {
				select {
				case <-fetchExited:
					exitedBeforeClear.Store(true)
				default:
				}
				return nil
			},
		}
	})

	if _, err := env.d.Sync.Start(ctx, env.sid, syncpkg.ModeFull, syncpkg.TriggerManual); err != nil {
		t.Fatalf("Start: %v", err)
	}
	env.d.Sync.Wait(env.sid)
	env.d.Sync.WaitMedia()

	// Clear thumb_path on one image attachment so fetchThumbsAsync sees a pending MediaTarget.
	var sourceID string
	if err := env.d.DB.R.QueryRow(`SELECT i.source_id FROM issue i JOIN attachment a ON a.issue_id = i.id WHERE a.is_image = 1 LIMIT 1`).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := env.d.DB.W.Exec(`UPDATE attachment SET thumb_path = NULL, file_path = NULL`); err != nil {
		t.Fatal(err)
	}

	src := &stubMediaSource{
		onFetch: func(fctx context.Context) (source.MediaBlob, error) {
			close(fetchEntered)
			<-fctx.Done()
			close(fetchExited)
			return source.MediaBlob{}, fctx.Err()
		},
	}

	env.d.fetchThumbsAsync(src, cache.ConnRow{ID: env.connID}, issueRef{SourceID: sourceID})
	select {
	case <-fetchEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("fetchThumbsAsync did not enter FetchMedia")
	}

	if err := env.d.deleteContent(ctx, env.connID); err != nil {
		t.Fatalf("deleteContent: %v", err)
	}
	if !exitedBeforeClear.Load() {
		t.Fatal("Media.Clear ran before FetchMedia exited")
	}
}

func TestDeleteContent_DrainsInFlightLockIssueAndSerializesConcurrentCalls(t *testing.T) {
	ctx := context.Background()
	inClear := make(chan struct{}, 2)
	allowClear := make(chan struct{})
	var activeClears, maxConcurrentClears, totalClears atomic.Int32

	env := newDisconnectEnv(t, nil, func(inner media.Cache) media.Cache {
		return &mediaHookCache{
			Cache: inner,
			onClear: func(context.Context) error {
				cur := activeClears.Add(1)
				for {
					prev := maxConcurrentClears.Load()
					if cur <= prev || maxConcurrentClears.CompareAndSwap(prev, cur) {
						break
					}
				}
				totalClears.Add(1)
				inClear <- struct{}{}
				<-allowClear
				activeClears.Add(-1)
				return nil
			},
		}
	})

	if _, err := env.d.Sync.Start(ctx, env.sid, syncpkg.ModeFull, syncpkg.TriggerManual); err != nil {
		t.Fatalf("Start: %v", err)
	}
	env.d.Sync.Wait(env.sid)
	env.d.Sync.WaitMedia()

	var issueID int64
	if err := env.d.DB.R.QueryRow(`SELECT id FROM issue LIMIT 1`).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	rel, ok := env.d.lockIssue(issueID)
	if !ok {
		t.Fatal("lockIssue failed")
	}

	err1 := make(chan error, 1)
	err2 := make(chan error, 1)
	go func() { err1 <- env.d.deleteContent(ctx, env.connID) }()

	// Wait until call 1 has paused new lockIssue calls and is waiting on ticketWG.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if relTmp, ok := env.d.lockIssue(issueID + 1); !ok {
			break
		} else {
			relTmp()
		}
		time.Sleep(time.Millisecond)
	}
	if relTmp, ok := env.d.lockIssue(issueID + 1); ok {
		relTmp()
		rel()
		close(allowClear)
		t.Fatal("new lockIssue succeeded while paused")
	}

	// Start concurrent deleteContent #2; it must serialize behind #1 and not enter Clear yet.
	go func() { err2 <- env.d.deleteContent(ctx, env.connID) }()

	select {
	case <-inClear:
		rel()
		close(allowClear)
		t.Fatal("Media.Clear ran before in-flight lockIssue released")
	case <-time.After(25 * time.Millisecond):
	}

	// Releasing lockIssue allows call #1 to reach Clear; #2 must still wait for #1 to finish.
	rel()
	select {
	case <-inClear:
	case <-time.After(5 * time.Second):
		close(allowClear)
		t.Fatal("call #1 never reached Media.Clear")
	}
	select {
	case <-inClear:
		close(allowClear)
		t.Fatal("call #2 entered Media.Clear concurrently with call #1")
	case <-time.After(25 * time.Millisecond):
	}

	close(allowClear)
	select {
	case err := <-err1:
		if err != nil {
			t.Fatalf("deleteContent #1: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deleteContent #1 timed out")
	}
	select {
	case <-inClear:
	case <-time.After(5 * time.Second):
		t.Fatal("call #2 never reached Media.Clear")
	}
	select {
	case err := <-err2:
		if err != nil {
			t.Fatalf("deleteContent #2: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deleteContent #2 timed out")
	}
	if totalClears.Load() != 2 || maxConcurrentClears.Load() != 1 {
		t.Fatalf("totalClears=%d maxConcurrent=%d, want 2 and 1", totalClears.Load(), maxConcurrentClears.Load())
	}
}

func TestDeleteContent_DrainTimeoutAndCancelReturnCleanErrors(t *testing.T) {
	ctx := context.Background()
	env := newDisconnectEnv(t, nil, nil)

	if _, err := env.d.Sync.Start(ctx, env.sid, syncpkg.ModeFull, syncpkg.TriggerManual); err != nil {
		t.Fatalf("Start: %v", err)
	}
	env.d.Sync.Wait(env.sid)
	env.d.Sync.WaitMedia()

	var issueID int64
	if err := env.d.DB.R.QueryRow(`SELECT id FROM issue LIMIT 1`).Scan(&issueID); err != nil {
		t.Fatal(err)
	}

	// Subtest 1: Timeout waiting for an in-flight ticket write.
	rel, ok := env.d.lockIssue(issueID)
	if !ok {
		t.Fatal("lockIssue failed")
	}
	tctx, tcancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer tcancel()
	err := env.d.deleteContent(tctx, env.connID)
	if !apperr.Is(err, apperr.Timeout) {
		rel()
		t.Fatalf("deleteContent on timed-out drain err = %v, want timeout", err)
	}
	rel()

	// Content was not wiped, and locks/syncs are unpaused again.
	var contentCount int
	if err := env.d.DB.R.QueryRow(`SELECT COUNT(*) FROM issue_content`).Scan(&contentCount); err != nil {
		t.Fatal(err)
	}
	if contentCount == 0 {
		t.Fatal("issue_content was wiped despite drain timeout")
	}
	rel2, ok := env.d.lockIssue(issueID)
	if !ok {
		t.Fatal("lockIssue remained paused after drain timeout")
	}
	rel2()

	// Subtest 2: Cancelled context while draining an in-flight ticket write.
	rel3, ok := env.d.lockIssue(issueID)
	if !ok {
		t.Fatal("lockIssue failed")
	}
	cctx, ccancel := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() { errCh <- env.d.deleteContent(cctx, env.connID) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if relTmp, ok := env.d.lockIssue(issueID + 1); !ok {
			break
		} else {
			relTmp()
		}
		time.Sleep(time.Millisecond)
	}
	ccancel()
	select {
	case err = <-errCh:
	case <-time.After(5 * time.Second):
		rel3()
		t.Fatal("deleteContent on cancelled drain timed out")
	}
	rel3()
	if !apperr.Is(err, apperr.Cancelled) {
		t.Fatalf("deleteContent on cancelled drain err = %v, want cancelled", err)
	}
	if err := env.d.DB.R.QueryRow(`SELECT COUNT(*) FROM issue_content`).Scan(&contentCount); err != nil {
		t.Fatal(err)
	}
	if contentCount == 0 {
		t.Fatal("issue_content was wiped despite cancelled drain")
	}

	// Subtest 3: enterTicketOp (used by FetchByKey) is refused while paused and drained before the wipe.
	relOp, ok := env.d.enterTicketOp()
	if !ok {
		t.Fatal("enterTicketOp failed while idle")
	}
	tctx2, tcancel2 := context.WithTimeout(ctx, 40*time.Millisecond)
	defer tcancel2()
	if err := env.d.deleteContent(tctx2, env.connID); !apperr.Is(err, apperr.Timeout) {
		relOp()
		t.Fatalf("deleteContent with active enterTicketOp err = %v, want timeout", err)
	}
	relOp()
}

package sync

// The media phase (ADR 0015, jira-sync.md §2.14 and §4.3 phase 9): after the record apply and after sync:finished, the
// image attachments of the hydrated issues that have no thumbnail yet are downloaded through Source.FetchMedia (the
// connection limiter's media lane) into Options.Media, which derives the thumbnail and stores the original; then the
// cache is evicted to its cap. It runs in a pool of two, is cancelled by Shutdown, never changes the run's status, and
// never fails: a download that fails sets attachment.fetch_error and is tried again by the next sync that hydrates the
// issue. WP-T6.
//
// The phase runs inside the run's goroutine after finish, so Wait(standupID) returns before it ends; WaitMedia and
// Shutdown wait for it (Shutdown cancels it). No content is logged.

import (
	"context"
	"runtime/debug"
	gosync "sync"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/cache"
)

// mediaPool is the number of concurrent downloads (jira-sync §2.14).
const mediaWorkers = 2

// mediaContext returns a context for one media phase, cancelled by Shutdown or PauseAndCancel. ok is false once the
// orchestrator is closed or paused.
func (o *Orchestrator) mediaContext() (ctx context.Context, done func(), ok bool) {
	// Registered while o.mu is held: Shutdown and PauseAndCancel set closed/paused under o.mu and cancel the
	// registered phases, so a phase either sees closed/paused here or is cancelled there, never neither.
	o.mu.Lock()
	if o.closed || o.paused > 0 {
		o.mu.Unlock()
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	key := &cancel
	if o.mediaCancels == nil {
		o.mediaCancels = map[*context.CancelFunc]struct{}{}
	}
	o.mediaCancels[key] = struct{}{}
	o.mu.Unlock()
	return ctx, func() {
		cancel()
		o.mu.Lock()
		delete(o.mediaCancels, key)
		o.mu.Unlock()
	}, true
}

// WaitMedia blocks until every run goroutine has ended, media phases included (tests; Wait(standupID) returns as soon as
// the run is released, which is before its media phase ends). Shutdown waits for the same goroutines.
func (o *Orchestrator) WaitMedia() { o.wg.Wait() }

// PauseAndCancel prevents new runs and media phases from starting, cancels every active run and post-sync media phase,
// and waits for all run goroutines to stop (bounded by ctx). Callers must invoke the returned resume function (even on
// error) to unpause future syncs.
func (o *Orchestrator) PauseAndCancel(ctx context.Context) (resume func(), err error) {
	o.mu.Lock()
	o.paused++
	for _, r := range o.runs {
		r.cancel()
	}
	cancels := make([]context.CancelFunc, 0, len(o.mediaCancels))
	for c := range o.mediaCancels {
		cancels = append(cancels, *c)
	}
	o.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	resume = gosync.OnceFunc(func() {
		o.mu.Lock()
		o.paused--
		o.mu.Unlock()
	})

	if err := o.waitAll(ctx); err != nil {
		return resume, err
	}
	return resume, nil
}

// cancelMedia cancels every media phase of o (Shutdown).
func (o *Orchestrator) cancelMedia() {
	o.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(o.mediaCancels))
	for c := range o.mediaCancels {
		cancels = append(cancels, *c)
	}
	o.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// mediaPhase downloads the images of the run's hydrated issues. A run without a media store, or a source without
// content, has no media phase.
func (rn *runner) mediaPhase(f *Fetched) {
	o := rn.o
	store := o.opts.Media
	if store == nil || len(f.Details) == 0 || !rn.src.Capabilities().Content {
		return
	}
	ctx, done, ok := o.mediaContext()
	if !ok {
		return
	}
	defer done()
	defer func() {
		if p := recover(); p != nil {
			o.opts.Logger.Error("sync: media phase panicked", "run", rn.r.id, "panic", p, "stack", string(debug.Stack()))
		}
	}()

	issueIDs := make([]string, 0, len(f.Details))
	for _, d := range f.Details {
		issueIDs = append(issueIDs, d.SourceID)
	}
	targets, err := cache.MediaTargets(ctx, o.opts.DB.R, rn.conn.ID, issueIDs)
	if err != nil {
		o.opts.Logger.Debug("sync: media targets not read", "run", rn.r.id, "err", err)
		return
	}

	pool := newMediaPool(ctx, rn, store, len(targets))
	pool.report()
	pool.run(targets, pool.fetchImage)

	// Jira-sync §2.14 step 2: attachments whose media id is still unknown although the issue's ADF names a media id.
	if ctx.Err() == nil {
		if idTargets, err := cache.MediaIDTargets(ctx, o.opts.DB.R, rn.conn.ID, issueIDs); err == nil && len(idTargets) > 0 {
			pool.total += len(idTargets)
			pool.report()
			pool.run(idTargets, pool.learnMediaID)
		}
	}

	if ctx.Err() == nil {
		if _, err := store.Evict(ctx); err != nil {
			o.opts.Logger.Debug("sync: media eviction failed", "run", rn.r.id, "err", err)
		}
	}
}

// mediaPool runs the downloads of one phase.
type mediaPool struct {
	ctx   context.Context
	rn    *runner
	store MediaStore

	mu    gosync.Mutex
	done  int
	total int
}

func newMediaPool(ctx context.Context, rn *runner, store MediaStore, total int) *mediaPool {
	return &mediaPool{ctx: ctx, rn: rn, store: store, total: total}
}

func (p *mediaPool) report() {
	p.mu.Lock()
	done, total := p.done, p.total
	p.mu.Unlock()
	p.rn.prog.report(PhaseMedia, done, total)
}

func (p *mediaPool) step() {
	p.mu.Lock()
	p.done++
	p.mu.Unlock()
	p.report()
}

func (p *mediaPool) run(targets []cache.MediaTarget, fn func(cache.MediaTarget)) {
	if len(targets) == 0 {
		return
	}
	jobs := make(chan cache.MediaTarget)
	var wg gosync.WaitGroup
	for range min(mediaWorkers, len(targets)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range jobs {
				p.guarded(t, fn)
			}
		}()
	}
loop:
	for _, t := range targets {
		select {
		case jobs <- t:
		case <-p.ctx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()
}

// guarded runs one download; a panic in the source or the store is that download's failure.
func (p *mediaPool) guarded(t cache.MediaTarget, fn func(cache.MediaTarget)) {
	defer func() {
		if r := recover(); r != nil {
			p.rn.logPanic(r, string(debug.Stack()))
		}
	}()
	fn(t)
	if p.ctx.Err() == nil {
		p.step()
	}
}

// fail records the failure code on the attachment row (a cancelled phase records nothing).
func (p *mediaPool) fail(t cache.MediaTarget, err error) {
	if p.ctx.Err() != nil {
		return
	}
	code := string(apperr.From(err).Code)
	if serr := cache.SetAttachmentFetchError(context.WithoutCancel(p.ctx), p.rn.o.opts.DB.W, t.Att.LocalID, code); serr != nil {
		p.rn.o.opts.Logger.Debug("sync: media failure not recorded", "err", serr)
	}
	p.rn.o.opts.Logger.Debug("sync: media download failed", "attachment", t.Att.LocalID, "code", code)
}

// fetchImage downloads one image original into the media cache, falling back to the provider's thumbnail when the
// original cannot be decoded locally.
func (p *mediaPool) fetchImage(t cache.MediaTarget) {
	rn := p.rn
	att := t.Att
	blob, err := rn.src.FetchMedia(p.ctx, att, false)
	if err != nil {
		p.fail(t, err)
		return
	}
	cached, perr := func() (string, error) {
		if blob.Body != nil {
			defer func() { _ = blob.Body.Close() }()
		}
		return p.store.Put(p.ctx, att, rn.conn.ID, blob.Body)
	}()
	p.learn(t, blob.MediaFileID)
	if perr != nil {
		p.fail(t, perr)
		return
	}
	if cached == "none" || cached == "" {
		cached = p.thumbFallback(t)
		if cached == "" {
			return
		}
	}
	rn.o.emit(EvMediaReady, MediaReady{AttachmentID: att.LocalID, IssueID: t.IssueID, Cached: cached})
}

// thumbFallback fetches the provider-made thumbnail; "" when that fails too (the failure is recorded).
func (p *mediaPool) thumbFallback(t cache.MediaTarget) string {
	blob, err := p.rn.src.FetchMedia(p.ctx, t.Att, true)
	if err != nil {
		p.fail(t, err)
		return ""
	}
	if blob.Body != nil {
		defer func() { _ = blob.Body.Close() }()
	}
	if err := p.store.PutThumb(p.ctx, t.Att, p.rn.conn.ID, blob.Body); err != nil {
		p.fail(t, err)
		return ""
	}
	return "thumb"
}

// learnMediaID fetches an attachment only to learn its media id from the redirect, and drops the body unread.
func (p *mediaPool) learnMediaID(t cache.MediaTarget) {
	blob, err := p.rn.src.FetchMedia(p.ctx, t.Att, false)
	if err != nil {
		return
	}
	if blob.Body != nil {
		_ = blob.Body.Close()
	}
	p.learn(t, blob.MediaFileID)
}

// learn stores a media id taught by a download.
func (p *mediaPool) learn(t cache.MediaTarget, mediaFileID string) {
	if mediaFileID == "" || p.ctx.Err() != nil {
		return
	}
	if err := cache.SetAttachmentMediaID(context.WithoutCancel(p.ctx), p.rn.o.opts.DB.W, t.Att.LocalID, mediaFileID); err != nil {
		p.rn.o.opts.Logger.Debug("sync: media id not recorded", "err", err)
	}
}

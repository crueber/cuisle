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

// mediaStops lets Shutdown cancel media phases that outlive their run (a finished run is no longer in Orchestrator.runs
// and the frozen Orchestrator struct has no slot for them).
var mediaStops = struct {
	mu gosync.Mutex
	m  map[*Orchestrator]map[*context.CancelFunc]struct{}
}{m: map[*Orchestrator]map[*context.CancelFunc]struct{}{}}

// mediaContext returns a context for one media phase, cancelled by Shutdown. ok is false once the orchestrator is closed.
func (o *Orchestrator) mediaContext() (ctx context.Context, done func(), ok bool) {
	// Registered while o.mu is held: Shutdown sets closed under o.mu and cancels the registered phases after releasing it,
	// so a phase either sees closed here or is cancelled there, never neither.
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return nil, nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	key := &cancel
	mediaStops.mu.Lock()
	if mediaStops.m[o] == nil {
		mediaStops.m[o] = map[*context.CancelFunc]struct{}{}
	}
	mediaStops.m[o][key] = struct{}{}
	mediaStops.mu.Unlock()
	o.mu.Unlock()
	return ctx, func() {
		cancel()
		mediaStops.mu.Lock()
		delete(mediaStops.m[o], key)
		if len(mediaStops.m[o]) == 0 {
			delete(mediaStops.m, o)
		}
		mediaStops.mu.Unlock()
	}, true
}

// WaitMedia blocks until every run goroutine has ended, media phases included (tests; Wait(standupID) returns as soon as
// the run is released, which is before its media phase ends). Shutdown waits for the same goroutines.
func (o *Orchestrator) WaitMedia() { o.wg.Wait() }

// cancelMedia cancels every media phase of o (Shutdown).
func (o *Orchestrator) cancelMedia() {
	mediaStops.mu.Lock()
	defer mediaStops.mu.Unlock()
	for c := range mediaStops.m[o] {
		(*c)()
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
	jobs := make(chan cache.MediaTarget)
	var wg gosync.WaitGroup
	for range min(mediaWorkers, max(len(targets), 1)) {
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
	cached, perr := p.store.Put(p.ctx, att, rn.conn.ID, blob.Body)
	_ = blob.Body.Close()
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
	err = p.store.PutThumb(p.ctx, t.Att, p.rn.conn.ID, blob.Body)
	_ = blob.Body.Close()
	if err != nil {
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

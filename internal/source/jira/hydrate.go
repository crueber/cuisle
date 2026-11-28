package jira

import (
	"context"
	"strings"
	"sync"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

const (
	hydrateSearchBatch = 50  // ids per search/jql request
	hydrateBulkBatch   = 500 // ids per changelog/bulkfetch request
	hydrateParallel    = 4   // requests in flight per phase
)

// hydrateFields is the search projection of jira-sync.md §2.8 (F12): everything except comments and worklog.
var hydrateFields = []string{"*all", "-comment", "-worklog"}

// Hydrate fetches the complete details (content, changelog and every comment) for sourceIDs: issue searches by id in
// batches of 50 (4 in flight), then complete (§2.9 changelog, §2.10 comments). Output order follows sourceIDs.
func (a *Adapter) Hydrate(ctx context.Context, sourceIDs []string, progress source.ProgressFunc) ([]model.IssueDetail, error) {
	ids := uniqueStrings(sourceIDs)
	if len(ids) == 0 {
		return []model.IssueDetail{}, nil
	}
	reg, err := a.registry(ctx)
	if err != nil {
		return nil, err
	}
	fields := a.fieldIDs()

	// Phase 1: issues, searched by id in batches.
	issuesProg := newPhaseProgress(progress, source.PhaseIssues, len(ids))
	batches := chunkStrings(ids, hydrateSearchBatch)
	pages := make([][]wireIssue, len(batches))
	err = forEachParallel(ctx, len(batches), func(ctx context.Context, i int) error {
		issues, err := a.searchIssues(ctx, batches[i], hydrateFields)
		if err != nil {
			return err
		}
		pages[i] = issues
		issuesProg.add(len(batches[i]))
		return nil
	})
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.IssueDetail{}
	for _, page := range pages {
		for _, wi := range page {
			d, err := mapIssue(wi, fields, reg)
			if err != nil {
				return nil, err
			}
			byID[d.SourceID] = &d
		}
	}
	var hydrated []*model.IssueDetail
	for _, id := range ids {
		if d := byID[id]; d != nil {
			hydrated = append(hydrated, d)
		}
	}
	if err := a.complete(ctx, fields, hydrated, progress); err != nil {
		return nil, err
	}
	out := make([]model.IssueDetail, 0, len(hydrated))
	for _, d := range hydrated {
		out = append(out, *d)
	}
	return out, nil
}

// complete adds the changelog (bulkfetch in batches of 500, §2.9) and the complete comment list (§2.10) to the
// hydrated issues, and records their issue types. Shared by Hydrate and GetIssue.
func (a *Adapter) complete(ctx context.Context, fields hydrateFieldIDs, hydrated []*model.IssueDetail, progress source.ProgressFunc) error {
	changeProg := newPhaseProgress(progress, source.PhaseChangelog, len(hydrated))
	hidsAll := make([]string, 0, len(hydrated))
	for _, d := range hydrated {
		hidsAll = append(hidsAll, d.SourceID)
	}
	hbatches := chunkStrings(hidsAll, hydrateBulkBatch)
	histories := make([]map[string][]wireHistory, len(hbatches))
	err := forEachParallel(ctx, len(hbatches), func(ctx context.Context, i int) error {
		h, err := a.changelogBatch(ctx, hbatches[i], fields.flagged)
		if err != nil {
			return err
		}
		histories[i] = h
		changeProg.add(len(hbatches[i]))
		return nil
	})
	if err != nil {
		return err
	}
	merged := map[string][]wireHistory{}
	for _, h := range histories {
		for id, hs := range h {
			merged[id] = append(merged[id], hs...)
		}
	}
	for _, d := range hydrated {
		applyChangelog(d, merged[d.SourceID], fields.flagged)
	}

	commentProg := newPhaseProgress(progress, source.PhaseComments, len(hydrated))
	err = forEachParallel(ctx, len(hydrated), func(ctx context.Context, i int) error {
		if err := a.fetchComments(ctx, hydrated[i]); err != nil {
			return err
		}
		commentProg.add(1)
		return nil
	})
	if err != nil {
		return err
	}
	a.recordTypes(hydrated)
	return nil
}

// searchIssues runs one `id in (…)` search, following nextPageToken.
func (a *Adapter) searchIssues(ctx context.Context, ids []string, fields []string) ([]wireIssue, error) {
	jql := "id in (" + strings.Join(ids, ", ") + ")"
	var out []wireIssue
	var token string
	for {
		body := map[string]any{"jql": jql, "fields": fields, "maxResults": hydrateSearchBatch}
		if token != "" {
			body["nextPageToken"] = token
		}
		var page struct {
			Issues        []wireIssue `json:"issues"`
			NextPageToken string      `json:"nextPageToken"`
		}
		if err := a.c.PostJSON(ctx, searchJQLPath, nil, body, &page); err != nil {
			return nil, MapError(err)
		}
		out = append(out, page.Issues...)
		if page.NextPageToken == "" || page.NextPageToken == token || len(page.Issues) == 0 {
			return out, nil
		}
		token = page.NextPageToken
	}
}

// recordTypes remembers the issue types seen, for a later Reference call.
func (a *Adapter) recordTypes(ds []*model.IssueDetail) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.types == nil {
		a.types = map[string]model.IssueType{}
	}
	for _, d := range ds {
		if d.TypeID != "" {
			a.types[d.TypeID] = d.Type
		}
	}
}

// phaseProgress reports done/total for one phase. Calls are serialised so done is monotonic in call order.
type phaseProgress struct {
	mu    sync.Mutex
	fn    source.ProgressFunc
	phase string
	total int
	done  int
}

func newPhaseProgress(fn source.ProgressFunc, phase string, total int) *phaseProgress {
	p := &phaseProgress{fn: fn, phase: phase, total: total}
	if fn != nil && total > 0 {
		fn(phase, 0, total)
	}
	return p
}

func (p *phaseProgress) add(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += n
	if p.done > p.total {
		p.done = p.total
	}
	if p.fn != nil && p.total > 0 {
		p.fn(p.phase, p.done, p.total)
	}
}

// forEachParallel runs fn for i in [0,n) with at most hydrateParallel in flight. The first error cancels the rest
// and is returned; a cancelled parent context is returned as its error.
func forEachParallel(parent context.Context, n int, fn func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	sem := make(chan struct{}, hydrateParallel)
	var wg sync.WaitGroup
	var once sync.Once
	var first error
loop:
	for i := 0; i < n; i++ {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := fn(ctx, i); err != nil {
				once.Do(func() {
					first = err
					cancel()
				})
			}
		}(i)
	}
	wg.Wait()
	if first != nil {
		return first
	}
	return parent.Err()
}

// uniqueStrings drops empty and repeated values, keeping first occurrences.
func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// chunkStrings splits ids into consecutive slices of at most n.
func chunkStrings(ids []string, n int) [][]string {
	var out [][]string
	for len(ids) > 0 {
		k := min(n, len(ids))
		out = append(out, ids[:k])
		ids = ids[k:]
	}
	return out
}

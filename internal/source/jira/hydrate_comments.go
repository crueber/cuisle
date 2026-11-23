package jira

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// topUpHorizon is how far back comments are kept for the Configure/pulse views (jira-sync.md §2.10).
const topUpHorizon = 35 * 24 * time.Hour

// needsTopUp reports whether the embedded comments are incomplete and the oldest embedded comment is still within
// the horizon (an older oldest comment means the horizon is already covered).
func needsTopUp(d *model.IssueDetail, now time.Time) bool {
	if d.CommentTotal <= len(d.Comments) {
		return false
	}
	cutoff := now.Add(-topUpHorizon)
	for _, c := range d.Comments {
		if c.At.Before(cutoff) {
			return false
		}
	}
	return true
}

// topUpComments reads newest-first comment pages until a page reaches past the horizon or the total is exhausted.
// Results are merged into d.Comments without duplicates; comment bodies are never decoded.
func (a *Adapter) topUpComments(ctx context.Context, d *model.IssueDetail, now time.Time) error {
	cutoff := now.Add(-topUpHorizon)
	seen := make(map[string]bool, len(d.Comments))
	for _, c := range d.Comments {
		seen[c.ID] = true
	}
	path := "/rest/api/3/issue/" + url.PathEscape(d.SourceID) + "/comment"
	start := 0
	for {
		var page struct {
			Total    int           `json:"total"`
			Comments []wireComment `json:"comments"`
		}
		q := url.Values{"orderBy": {"-created"}, "maxResults": {"100"}, "startAt": {strconv.Itoa(start)}}
		if err := a.c.GetJSON(ctx, path, q, &page); err != nil {
			return MapError(err)
		}
		var oldest time.Time
		for i, wc := range page.Comments {
			c := commentOf(wc)
			if i == 0 || c.At.Before(oldest) {
				oldest = c.At
			}
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			d.Comments = append(d.Comments, c)
			if wc.Author != nil {
				addPerson(d, personOf(wc.Author))
			}
		}
		start += len(page.Comments)
		limit := d.CommentTotal
		if page.Total > limit {
			limit = page.Total
		}
		if len(page.Comments) == 0 || start >= limit || oldest.Before(cutoff) {
			return nil
		}
	}
}

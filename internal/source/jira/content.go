package jira

// Content methods of source.Source (ADR 0015, jira-sync.md §2.2, §2.8–2.10, §2.15). Fields is in fields.go and
// FetchMedia in media.go.

import (
	"context"
	"net/url"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

// GetIssue refreshes one issue completely (jira-sync.md §2.15 Refresh): one GET /rest/api/3/issue/{id} with the
// content projection, then the same changelog and comment pipeline as Hydrate of one id. An issue the provider returns
// without an id or key is not found.
func (a *Adapter) GetIssue(ctx context.Context, sourceID string) (model.IssueDetail, error) {
	reg, err := a.registry(ctx)
	if err != nil {
		return model.IssueDetail{}, err
	}
	var wi wireIssue
	q := url.Values{"fields": {"*all,-comment,-worklog"}}
	if err := a.c.GetJSON(ctx, "/rest/api/3/issue/"+url.PathEscape(sourceID), q, &wi); err != nil {
		return model.IssueDetail{}, MapError(err)
	}
	if wi.ID == "" || wi.Key == "" {
		return model.IssueDetail{}, apperr.New(apperr.NotFound, "Jira could not find that item.")
	}
	fields := a.fieldIDs()
	d, err := mapIssue(wi, fields, reg)
	if err != nil {
		return model.IssueDetail{}, err
	}
	if err := a.complete(ctx, fields, []*model.IssueDetail{&d}, nil); err != nil {
		return model.IssueDetail{}, err
	}
	return d, nil
}

// contentCapability reports Capabilities().Content: Fields, FetchMedia, GetIssue and the content of Hydrate work.
func contentCapability() bool { return true }

package jira

import (
	"context"
	"strings"

	"github.com/crueber/cuisle/internal/source"
)

const (
	scanPageSize    = 1000
	searchJQLPath   = "/rest/api/3/search/jql"
	approxCountPath = "/rest/api/3/search/approximate-count"
)

type scanPage struct {
	Issues []struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Fields struct {
			Updated Time `json:"updated"`
		} `json:"fields"`
	} `json:"issues"`
	NextPageToken string `json:"nextPageToken"`
}

// Scan runs POST /rest/api/3/search/jql with fields ["updated"], maxResults 1000, following nextPageToken, stopping
// at q.Limit when set; when q.Count, also POST /rest/api/3/search/approximate-count (failure → ApproxTotal -1).
func (a *Adapter) Scan(ctx context.Context, q source.Query) (source.ScanResult, error) {
	res := source.ScanResult{ApproxTotal: -1}
	var token string
	for {
		body := map[string]any{"jql": q.Expr, "fields": []string{"updated"}, "maxResults": scanPageSize}
		if token != "" {
			body["nextPageToken"] = token
		}
		var page scanPage
		if err := a.c.PostJSON(ctx, searchJQLPath, nil, body, &page); err != nil {
			return source.ScanResult{}, MapError(err)
		}
		for _, is := range page.Issues {
			res.Items = append(res.Items, source.ScanItem{
				SourceID: is.ID, Key: is.Key, UpdatedAt: is.Fields.Updated.Time,
			})
		}
		if q.Limit > 0 && len(res.Items) >= q.Limit {
			res.Items = res.Items[:q.Limit]
			break
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	if q.Count {
		total, err := a.approxCount(ctx, q.Expr)
		switch {
		case err == nil:
			res.ApproxTotal = total
		case ctx.Err() != nil:
			return source.ScanResult{}, MapError(ctx.Err())
		default:
			a.log.Debug("jira approximate count failed; total unknown", "err", err)
		}
	}
	return res, nil
}

// approxCount posts the membership JQL without its ORDER BY clause to the approximate-count endpoint.
func (a *Adapter) approxCount(ctx context.Context, expr string) (int, error) {
	var out struct {
		Count int `json:"count"`
	}
	body := map[string]any{"jql": strings.TrimSuffix(expr, orderByRank)}
	if err := a.c.PostJSON(ctx, approxCountPath, nil, body, &out); err != nil {
		return 0, MapError(err)
	}
	return out.Count, nil
}

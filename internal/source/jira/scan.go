package jira

import (
	"context"
	"encoding/json"
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
		ID     string                     `json:"id"`
		Key    string                     `json:"key"`
		Fields map[string]json.RawMessage `json:"fields"`
	} `json:"issues"`
	NextPageToken string `json:"nextPageToken"`
}

// Scan runs POST /rest/api/3/search/jql with fields ["updated"] (plus the Development summary field for board-role
// queries, when the site has one: the dev fingerprint rides along for free, jira-sync §2.11), maxResults 1000,
// following nextPageToken, stopping at q.Limit when set; when q.Count, also POST
// /rest/api/3/search/approximate-count (failure → ApproxTotal -1).
func (a *Adapter) Scan(ctx context.Context, q source.Query) (source.ScanResult, error) {
	res := source.ScanResult{ApproxTotal: -1}
	fields := []string{"updated"}
	devField := ""
	if q.Role != "backlog" { // backlog items are never dev targets
		id, err := a.devFieldID(ctx)
		if err != nil {
			return source.ScanResult{}, err
		}
		if id != "" {
			devField = id
			fields = append(fields, id)
		}
	}
	var token string
	for {
		body := map[string]any{"jql": q.Expr, "fields": fields, "maxResults": scanPageSize}
		if token != "" {
			body["nextPageToken"] = token
		}
		var page scanPage
		if err := a.c.PostJSON(ctx, searchJQLPath, nil, body, &page); err != nil {
			return source.ScanResult{}, MapError(err)
		}
		for _, is := range page.Issues {
			var updated Time
			if raw, ok := is.Fields["updated"]; ok {
				if err := updated.UnmarshalJSON(raw); err != nil {
					return source.ScanResult{}, MapError(err)
				}
			}
			item := source.ScanItem{SourceID: is.ID, Key: is.Key, UpdatedAt: updated.Time}
			if devField != "" {
				a.noteDevHint(is.ID, nil)
				if raw, present := is.Fields[devField]; present {
					if hint, ok := parseDevField(raw); ok {
						item.DevFingerprint = hint.fingerprint
						a.noteDevHint(is.ID, &hint)
					}
				}
			}
			res.Items = append(res.Items, item)
		}
		if q.Limit > 0 && len(res.Items) >= q.Limit {
			res.Items = res.Items[:q.Limit]
			break
		}
		if page.NextPageToken == "" || page.NextPageToken == token || len(page.Issues) == 0 {
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

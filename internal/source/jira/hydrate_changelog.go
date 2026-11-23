package jira

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/crueber/cuisle/internal/model"
)

const changelogBulkPath = "/rest/api/3/changelog/bulkfetch"

// changelogBatch returns the histories of ids keyed by issue id (jira-sync.md §2.9). It tries bulkfetch with
// fieldIds; on a 400 or 404 it retries the same batch without fieldIds (filtering is then client-side); if that is
// rejected too, it reads each issue's changelog from the per-issue endpoint.
func (a *Adapter) changelogBatch(ctx context.Context, ids []string, flaggedID string) (map[string][]wireHistory, error) {
	fieldIDs := []string{"status", "assignee"}
	if flaggedID != "" {
		fieldIDs = append(fieldIDs, flaggedID)
	}
	out, err := a.bulkChangelog(ctx, ids, fieldIDs)
	if err == nil {
		return out, nil
	}
	if !changelogRejected(err) {
		return nil, MapError(err)
	}
	a.log.Debug("jira bulk changelog rejected fieldIds; retrying without", "err", err)

	out, err = a.bulkChangelog(ctx, ids, nil)
	if err == nil {
		return out, nil
	}
	if !changelogRejected(err) {
		return nil, MapError(err)
	}
	a.log.Debug("jira bulk changelog rejected; reading per issue", "err", err)

	out = make(map[string][]wireHistory, len(ids))
	for _, id := range ids {
		hs, err := a.issueChangelog(ctx, id)
		if err != nil {
			return nil, MapError(err)
		}
		out[id] = hs
	}
	return out, nil
}

// changelogRejected reports whether err is a 400 or 404 from the server.
func changelogRejected(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && (he.Status == http.StatusBadRequest || he.Status == http.StatusNotFound)
}

// bulkChangelog posts one bulkfetch batch, following nextPageToken. fieldIDs nil means the field is omitted.
func (a *Adapter) bulkChangelog(ctx context.Context, ids []string, fieldIDs []string) (map[string][]wireHistory, error) {
	out := map[string][]wireHistory{}
	var token string
	for {
		body := map[string]any{"issueIdsOrKeys": ids, "maxResults": 1000}
		if fieldIDs != nil {
			body["fieldIds"] = fieldIDs
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		var page struct {
			NextPageToken   string `json:"nextPageToken"`
			IssueChangeLogs []struct {
				IssueID         flexID        `json:"issueId"`
				ChangeHistories []wireHistory `json:"changeHistories"`
			} `json:"issueChangeLogs"`
		}
		if err := a.c.PostJSON(ctx, changelogBulkPath, nil, body, &page); err != nil {
			return nil, err
		}
		for _, l := range page.IssueChangeLogs {
			id := string(l.IssueID)
			out[id] = append(out[id], l.ChangeHistories...)
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		token = page.NextPageToken
	}
}

// issueChangelog reads one issue's changelog from the paged per-issue endpoint (startAt paging, maxResults 100).
func (a *Adapter) issueChangelog(ctx context.Context, id string) ([]wireHistory, error) {
	var out []wireHistory
	startAt := 0
	path := "/rest/api/3/issue/" + url.PathEscape(id) + "/changelog"
	for {
		var page struct {
			Total  int           `json:"total"`
			IsLast bool          `json:"isLast"`
			Values []wireHistory `json:"values"`
		}
		q := url.Values{"startAt": {strconv.Itoa(startAt)}, "maxResults": {"100"}}
		if err := a.c.GetJSON(ctx, path, q, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Values...)
		startAt += len(page.Values)
		if page.IsLast || len(page.Values) == 0 || startAt >= page.Total {
			return out, nil
		}
	}
}

// applyChangelog maps the histories of one issue to status, assignee and flag changes (oldest first). Items match by
// fieldId or by field name; other fields are ignored.
func applyChangelog(d *model.IssueDetail, hs []wireHistory, flaggedID string) {
	sort.SliceStable(hs, func(i, j int) bool { return hs[i].Created.Time.Before(hs[j].Created.Time) })
	for _, h := range hs {
		id, at := string(h.ID), h.Created.Time
		for _, it := range h.Items {
			switch {
			case it.FieldID == "status" || it.Field == "status":
				d.StatusChanges = append(d.StatusChanges, model.StatusChange{ID: id, At: at, From: strOf(it.From), To: strOf(it.To)})
			case it.FieldID == "assignee" || it.Field == "assignee":
				d.AssigneeChanges = append(d.AssigneeChanges, model.AssigneeChange{ID: id, At: at, From: strOf(it.From), To: strOf(it.To)})
			case (flaggedID != "" && it.FieldID == flaggedID) || it.Field == "Flagged":
				to := strOf(it.To)
				d.FlagChanges = append(d.FlagChanges, model.FlagChange{ID: id, At: at, Flagged: to != ""})
			}
		}
	}
}

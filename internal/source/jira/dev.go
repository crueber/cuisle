package jira

import (
	"context"
	"crypto/sha1" //nolint:gosec // fingerprint for change detection only, not security
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

const (
	devSummaryPath = "/rest/dev-status/latest/issue/summary"
	devDetailPath  = "/rest/dev-status/latest/issue/detail"
)

// devSummary is the part of the dev-status summary this adapter reads (jira-sync.md §2.11).
type devSummary struct {
	Summary struct {
		Repository  devPart `json:"repository"`
		PullRequest devPart `json:"pullrequest"`
	} `json:"summary"`
}

type devPart struct {
	Overall struct {
		Count       int    `json:"count"`
		LastUpdated string `json:"lastUpdated"`
		State       string `json:"state"`
	} `json:"overall"`
	ByInstanceType map[string]json.RawMessage `json:"byInstanceType"`
}

// devDetail is one detail response; repositories carry commits, detail entries carry pull requests.
type devDetail struct {
	Detail []struct {
		Repositories []struct {
			Name    string `json:"name"`
			Commits []struct {
				ID              string `json:"id"`
				AuthorTimestamp Time   `json:"authorTimestamp"`
				Merge           bool   `json:"merge"`
			} `json:"commits"`
		} `json:"repositories"`
		PullRequests []struct {
			ID             flexID `json:"id"`
			Name           string `json:"name"` // the pull request title
			Status         string `json:"status"`
			LastUpdate     Time   `json:"lastUpdate"`
			RepositoryName string `json:"repositoryName"`
			Source         struct {
				Branch string `json:"branch"`
			} `json:"source"`
		} `json:"pullRequests"`
	} `json:"detail"`
}

// DevActivity (§2.11). When the last Scan carried the issue's Development summary field, its fingerprint decides:
// equal to prevFingerprint → no request at all; different → detail per instance type straight away (no summary).
// Otherwise (no field, absent, unparseable, stale, or dev-status was marked unavailable) it asks the dev-status
// summary first and reads detail only when that fingerprint changed.
// The probe (§2.11) is the first dev-status request this Adapter actually makes, whichever kind it is: a failure of the
// probe kind (401/403/404/non-JSON) sets devOff and returns source.ErrUnsupported; later calls return
// source.ErrUnsupported without I/O. Field-only answers make no request, so they prove nothing either way.
func (a *Adapter) DevActivity(ctx context.Context, sourceID, prevFingerprint string) (model.DevActivity, error) {
	a.mu.Lock()
	off := a.devOff
	a.mu.Unlock()
	if off {
		return model.DevActivity{}, source.ErrUnsupported
	}
	if hint, ok := a.devHintFor(sourceID); ok {
		out := model.DevActivity{Fingerprint: hint.fingerprint, Changed: hint.fingerprint != prevFingerprint}
		if !out.Changed {
			return out, nil
		}
		events, err := a.devDetails(ctx, sourceID, hint.repoTypes, hint.prTypes)
		if err != nil {
			return model.DevActivity{}, err
		}
		out.Events = events
		return out, nil
	}

	var sum devSummary
	q := url.Values{"issueId": {sourceID}}
	if err := a.c.GetJSON(ctx, devSummaryPath, q, &sum); err != nil {
		if devProbeFailure(err) {
			a.disableDev(err)
			return model.DevActivity{}, source.ErrUnsupported
		}
		return model.DevActivity{}, MapError(err)
	}
	a.mu.Lock()
	a.meta.DevActivity, a.devProven = "ok", true
	a.mu.Unlock()

	fp := devFingerprintOf(sum.Summary.Repository, sum.Summary.PullRequest)
	out := model.DevActivity{Fingerprint: fp, Changed: fp != prevFingerprint}
	if !out.Changed {
		return out, nil
	}
	events, err := a.devDetails(ctx, sourceID,
		sortedKeys(sum.Summary.Repository.ByInstanceType), sortedKeys(sum.Summary.PullRequest.ByInstanceType))
	if err != nil {
		return model.DevActivity{}, err
	}
	out.Events = events
	return out, nil
}

// devDetails reads detail for every instance type of both data types (repository first). Until a dev-status request
// has succeeded in this Adapter's lifetime, a failure of the probe kind marks dev-status unavailable (after a
// successful summary it is an ordinary error).
func (a *Adapter) devDetails(ctx context.Context, sourceID string, repoTypes, prTypes []string) ([]model.DevEvent, error) {
	var out []model.DevEvent
	for _, t := range []struct {
		dataType string
		types    []string
	}{{"repository", repoTypes}, {"pullrequest", prTypes}} {
		for _, appType := range t.types {
			events, err := a.devDetail(ctx, sourceID, appType, t.dataType)
			if err != nil {
				a.mu.Lock()
				proven := a.devProven
				a.mu.Unlock()
				if !proven && devProbeFailure(err) {
					a.disableDev(err)
					return nil, source.ErrUnsupported
				}
				return nil, MapError(err)
			}
			out = append(out, events...)
		}
	}
	return out, nil
}

// devDetail fetches one detail data type for one instance type and maps it to events.
func (a *Adapter) devDetail(ctx context.Context, sourceID, appType, dataType string) ([]model.DevEvent, error) {
	q := url.Values{"issueId": {sourceID}, "applicationType": {appType}, "dataType": {dataType}}
	var det devDetail
	if err := a.c.GetJSON(ctx, devDetailPath, q, &det); err != nil {
		return nil, err // raw: devDetails decides between the probe failure and MapError
	}
	a.mu.Lock()
	a.meta.DevActivity, a.devProven = "ok", true
	a.mu.Unlock()
	var out []model.DevEvent
	for _, d := range det.Detail {
		for _, r := range d.Repositories {
			for _, c := range r.Commits {
				out = append(out, model.DevEvent{Kind: model.DevCommit, ID: c.ID, Repo: r.Name, At: c.AuthorTimestamp.Time, Merge: c.Merge})
			}
		}
		for _, p := range d.PullRequests {
			out = append(out, model.DevEvent{
				Kind: model.DevPR, ID: p.RepositoryName + "#" + string(p.ID), Repo: p.RepositoryName,
				At: p.LastUpdate.Time, Status: prStatus(p.Status), Title: p.Name, Branch: p.Source.Branch,
			})
		}
	}
	return out, nil
}

// disableDev switches dev activity off for this Adapter's lifetime after a probe failure.
func (a *Adapter) disableDev(cause error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.devOff {
		a.log.Info("jira dev-status unavailable for this source; continuing without it", "err", cause)
	}
	a.devOff = true
}

// devProbeFailure reports the failures that mean dev-status is not usable: 401, 403, 404 or a non-JSON body.
func devProbeFailure(err error) bool {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden || he.Status == http.StatusNotFound
	}
	var syn *json.SyntaxError
	return errors.As(err, &syn)
}

// prStatus keeps the known pull request states and maps anything else to UNKNOWN.
func prStatus(s string) string {
	switch s {
	case "OPEN", "MERGED", "DECLINED":
		return s
	}
	return "UNKNOWN"
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DevFingerprint = hex(sha1(repoCount|repoLastUpdated|prCount|prLastUpdated|prState)).
func DevFingerprint(repoCount int, repoLastUpdated string, prCount int, prLastUpdated, prState string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("%d|%s|%d|%s|%s", repoCount, repoLastUpdated, prCount, prLastUpdated, prState)))
	return hex.EncodeToString(sum[:])
}

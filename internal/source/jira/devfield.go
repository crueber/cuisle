package jira

// Development summary custom field (jira-sync.md §2.11). Search results can carry it for free: a string that embeds
// JSON ("{pullrequest={...}, repository={...}, json={"cachedValue":{"errors":[],"summary":{...}},"isStale":false}}").
// It holds the same overall counts, lastUpdated and state the dev-status summary endpoint returns, so the dev
// fingerprint can be computed from the scan and the per-ticket summary request is skipped.

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
)

// devHint is what one scan learned about an issue's development activity.
type devHint struct {
	fingerprint string
	repoTypes   []string // keys of summary.repository.byInstanceType, sorted (the applicationType values for detail)
	prTypes     []string // keys of summary.pullrequest.byInstanceType, sorted
}

// devFieldValue is the JSON embedded after "json=" in the field's string value.
type devFieldValue struct {
	CachedValue *struct {
		Errors  []json.RawMessage `json:"errors"`
		Summary *struct {
			Repository  devPart `json:"repository"`
			PullRequest devPart `json:"pullrequest"`
		} `json:"summary"`
	} `json:"cachedValue"`
	IsStale bool `json:"isStale"`
}

// parseDevField turns one issue's raw Development field value into a hint. ok is false when the value cannot be
// trusted (not a string, no embedded JSON, errors, stale, no summary): the caller then falls back to the summary
// endpoint. An absent field is the caller's business (it never reaches here); null and "{}" mean no development
// information at all, which is the same fingerprint the summary endpoint gives for zero activity.
func parseDevField(raw json.RawMessage) (devHint, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return devHint{}, false
	}
	if string(trimmed) == "null" {
		return zeroDevHint(), true
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return devHint{}, false
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return zeroDevHint(), true
	}
	i := strings.Index(s, "json=")
	if i < 0 {
		return devHint{}, false
	}
	var v devFieldValue
	// A Decoder reads one value and ignores the closing "}" of the surrounding wrapper.
	if err := json.NewDecoder(strings.NewReader(s[i+len("json="):])).Decode(&v); err != nil {
		return devHint{}, false
	}
	if v.IsStale || v.CachedValue == nil || len(v.CachedValue.Errors) > 0 || v.CachedValue.Summary == nil {
		return devHint{}, false
	}
	return hintFromSummary(v.CachedValue.Summary.Repository, v.CachedValue.Summary.PullRequest), true
}

func zeroDevHint() devHint { return hintFromSummary(devPart{}, devPart{}) }

func hintFromSummary(repo, pr devPart) devHint {
	return devHint{
		fingerprint: devFingerprintOf(repo, pr),
		repoTypes:   sortedKeys(repo.ByInstanceType),
		prTypes:     sortedKeys(pr.ByInstanceType),
	}
}

// devFingerprintOf is the §2.11 fingerprint of a summary: the same digest whichever of the two sources it came from.
func devFingerprintOf(repo, pr devPart) string {
	r, p := repo.Overall, pr.Overall
	return DevFingerprint(r.Count, r.LastUpdated, p.Count, p.LastUpdated, p.State)
}

// devFieldID returns the Development summary field id to request in the scan. The first call of a connection whose
// meta predates the field (or that never discovered fields) reads GET /rest/api/3/field once; afterwards "" means the
// site has no such field.
func (a *Adapter) devFieldID(ctx context.Context) (string, error) {
	a.devFieldMu.Lock()
	defer a.devFieldMu.Unlock()
	a.mu.Lock()
	id, checked := a.meta.DevSummaryFieldID, a.meta.DevFieldChecked
	a.mu.Unlock()
	if checked {
		return id, nil
	}
	if err := a.discoverFields(ctx); err != nil {
		if ctx.Err() != nil {
			return "", MapError(ctx.Err())
		}
		// The fingerprint is an optimisation: without the field id the dev phase uses the summary endpoint.
		a.log.Debug("jira field discovery for the development field failed; using dev-status summaries", "err", err)
		return "", nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.meta.DevSummaryFieldID, nil
}

// noteDevHint remembers (or, when hint is nil, forgets) what the scan learned about one issue.
func (a *Adapter) noteDevHint(id string, hint *devHint) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if hint == nil {
		delete(a.devHints, id)
		return
	}
	if a.devHints == nil {
		a.devHints = map[string]devHint{}
	}
	a.devHints[id] = *hint
}

// devHintFor returns the hint for an issue, unless dev-status is known to be unavailable (then the real summary call
// is the re-probe, §2.11).
func (a *Adapter) devHintFor(id string) (devHint, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.meta.DevActivity == "unavailable" {
		return devHint{}, false
	}
	h, ok := a.devHints[id]
	return h, ok
}

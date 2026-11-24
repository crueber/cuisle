package jira

// Light writes of source.Source (ADR 0015, jira-sync.md §2.15). One attempt each, never retried: a write is sent through
// writeOnce, which marks the request WithWrite (the limiter refuses it while the 429 gate is closed) and calls the
// client's single-attempt path. Reads inside a write (the transitions lookup) are Interactive, not write-marked.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/adf"
	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
	"github.com/crueber/cuisle/internal/source/jira/limiter"
)

const (
	msgNeedsScreen  = "This transition needs more information. Open it in Jira."
	msgCredentials  = "Jira rejected the credentials. Check the account and API token."
	msgForbidden    = "You don't have permission to do that in Jira."
	msgGone         = "This issue no longer exists or you can't see it."
	msgConflict     = "The issue changed in Jira. Reload and try again."
	msgBusy         = "Jira is busy. Try again in a moment."
	msgTrouble      = "Jira is having trouble right now. Try again shortly."
	msgUnexpected   = "Jira returned an unexpected response."
	msgRejected     = "Jira rejected the change."
	msgNoTimeAnswer = "Jira did not answer in time. Check Jira before you try again."
	flagValue       = "Impediment"
)

// writesCapabilities is what Capabilities().Writes reports for a connection: Flag needs the discovered Flagged field.
func writesCapabilities(m Meta) source.Writes {
	return source.Writes{Transition: true, Assign: true, Flag: m.FlaggedFieldID != "", Comment: true}
}

// wireTransitions is the transitions list as Jira sends it with expand=transitions.fields.
type wireTransitions struct {
	Transitions []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		To   struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"to"`
		HasScreen   bool  `json:"hasScreen"`
		IsAvailable *bool `json:"isAvailable"`
		Fields      map[string]struct {
			Required        bool `json:"required"`
			HasDefaultValue bool `json:"hasDefaultValue"`
		} `json:"fields"`
	} `json:"transitions"`
}

// Transitions lists the available transitions with NeedsScreen computed (one read; jira-sync.md §2.15).
func (a *Adapter) Transitions(ctx context.Context, sourceID string) ([]model.Transition, error) {
	return a.listTransitions(ctx, sourceID)
}

// listTransitions is the interactive read behind Transitions and Transition. It keeps no cache.
func (a *Adapter) listTransitions(ctx context.Context, sourceID string) ([]model.Transition, error) {
	var w wireTransitions
	q := url.Values{"expand": {"transitions.fields"}}
	if err := a.c.GetJSON(WithInteractive(ctx), issuePath(sourceID)+"/transitions", q, &w); err != nil {
		return nil, MapError(err)
	}
	out := []model.Transition{}
	for _, t := range w.Transitions {
		if t.IsAvailable != nil && !*t.IsAvailable {
			continue
		}
		needs := false
		if t.HasScreen {
			for _, f := range t.Fields {
				if f.Required && !f.HasDefaultValue {
					needs = true
				}
			}
		}
		out = append(out, model.Transition{
			ID: t.ID, Name: t.Name, ToStatusID: t.To.ID, ToStatusName: t.To.Name, NeedsScreen: needs,
		})
	}
	return out, nil
}

// Transition performs a transition that needs no screen. It looks the transition up first (no cache), so a transition
// that needs a screen, or one the list no longer offers, is refused before anything is posted.
func (a *Adapter) Transition(ctx context.Context, sourceID, transitionID string) error {
	release, err := a.c.TryLockIssue(sourceID)
	if err != nil {
		return err
	}
	defer release()
	list, err := a.listTransitions(ctx, sourceID)
	if err != nil {
		return err
	}
	var found *model.Transition
	for i := range list {
		if list[i].ID == transitionID {
			found = &list[i]
		}
	}
	if found == nil {
		return apperr.New(apperr.WriteRejected, "Jira no longer offers that move for this issue. Reload and try again.")
	}
	if found.NeedsScreen {
		return apperr.New(apperr.TransitionNeedsScreen, msgNeedsScreen)
	}
	payload := map[string]any{"transition": map[string]string{"id": transitionID}}
	_, err = a.writeOnce(ctx, http.MethodPost, issuePath(sourceID)+"/transitions", payload, true)
	return err
}

// Assign sets the assignee ("" = unassign).
func (a *Adapter) Assign(ctx context.Context, sourceID, accountID string) error {
	release, err := a.c.TryLockIssue(sourceID)
	if err != nil {
		return err
	}
	defer release()
	var v any // null unassigns
	if accountID != "" {
		v = accountID
	}
	_, err = a.writeOnce(ctx, http.MethodPut, issuePath(sourceID)+"/assignee", map[string]any{"accountId": v}, false)
	return err
}

// SetFlag flags or unflags the issue through the discovered Flagged field. Without that field it is unsupported and
// sends nothing.
func (a *Adapter) SetFlag(ctx context.Context, sourceID string, flagged bool) error {
	a.mu.Lock()
	m := a.meta
	a.mu.Unlock()
	if !writesCapabilities(m).Flag {
		return source.ErrUnsupported
	}
	release, err := a.c.TryLockIssue(sourceID)
	if err != nil {
		return err
	}
	defer release()
	var v any // null unflags
	if flagged {
		v = []map[string]string{{"value": flagValue}}
	}
	payload := map[string]any{"fields": map[string]any{m.FlaggedFieldID: v}}
	_, err = a.writeOnce(ctx, http.MethodPut, issuePath(sourceID), payload, false)
	return err
}

// AddComment posts c as an ADF comment body (adf.FromPlainText; no visibility restriction) and returns the comment id.
func (a *Adapter) AddComment(ctx context.Context, sourceID string, c model.NewComment) (string, error) {
	release, err := a.c.TryLockIssue(sourceID)
	if err != nil {
		return "", err
	}
	defer release()
	body := adf.FromPlainText(c)
	if body.IsEmpty() {
		return "", apperr.Validationf("text", "Write something before you post.")
	}
	payload := map[string]json.RawMessage{"body": body.Raw}
	data, err := a.writeOnce(ctx, http.MethodPost, issuePath(sourceID)+"/comment", payload, false)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ID == "" {
		return "", apperr.Wrap(apperr.SourceHTTP, msgUnexpected, err)
	}
	return out.ID, nil
}

func issuePath(sourceID string) string {
	return "/rest/api/3/issue/" + url.PathEscape(sourceID)
}

// writeOnce sends one write: a single attempt, marked WithWrite, never retried (ADR 0015). It returns the 2xx body.
// transition selects the POST transitions 400 rule (a field-level complaint means the transition needs a screen).
func (a *Adapter) writeOnce(ctx context.Context, method, path string, payload any, transition bool) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s body: %w", path, err)
	}
	status, hdr, data, err := a.c.attempt(WithWrite(ctx), method, path, a.c.base+path, body)
	if err != nil {
		var ge *limiter.GateError
		if errors.As(err, &ge) { // refused locally while the connection's 429 gate is closed: nothing was sent
			return nil, rateLimitedWrite(ge.RetryIn)
		}
		if errors.Is(err, errResponseTooLarge) {
			return nil, apperr.Wrap(apperr.SourceHTTP, msgUnexpected, err)
		}
		return nil, writeTransportError(err)
	}
	if status/100 == 2 {
		return data, nil
	}
	return nil, a.writeStatusError(method, path, status, hdr, data, transition)
}

// writeStatusError maps a non-2xx answer to a write (jira-sync.md §2.15 table). A 429 also closes the connection's gate
// for the next request, the same way Client.do does for a write, but nothing is retried.
func (a *Adapter) writeStatusError(method, path string, status int, hdr http.Header, data []byte, transition bool) error {
	he := newHTTPError(method, path, status, hdr, data, a.c.now())
	switch {
	case status == http.StatusBadRequest:
		if transition && hasFieldErrors(data) {
			return apperr.Wrap(apperr.TransitionNeedsScreen, msgNeedsScreen, he)
		}
		msg := msgRejected
		if len(he.Messages) > 0 && strings.TrimSpace(he.Messages[0]) != "" {
			msg = strings.TrimSpace(he.Messages[0])
		}
		return apperr.Wrap(apperr.WriteRejected, msg, he)
	case status == http.StatusUnauthorized:
		return apperr.Wrap(apperr.AuthInvalid, msgCredentials, he)
	case status == http.StatusForbidden:
		return apperr.Wrap(apperr.PermissionDenied, msgForbidden, he)
	case status == http.StatusNotFound:
		return apperr.Wrap(apperr.NotFound, msgGone, he)
	case status == http.StatusConflict:
		return apperr.Wrap(apperr.Conflict, msgConflict, he)
	case status == http.StatusTooManyRequests:
		wait := limiter.RetryAfter(hdr, a.c.now())
		if wait <= a.c.pol.opts.MaxGate {
			a.c.Limiter().CloseGate(time.Now().Add(a.c.pol.wait429(wait)))
		}
		e := apperr.Wrap(apperr.RateLimited, msgBusy, he)
		e.Retryable = true
		return e.WithRetryIn(wait.Milliseconds())
	case retryable5xx(status):
		e := apperr.Wrap(apperr.SourceHTTP, msgTrouble, he)
		e.Retryable = true
		return e
	}
	return apperr.Wrap(apperr.SourceHTTP, msgUnexpected, he)
}

// hasFieldErrors reports whether Jira's 400 body carries a non-empty errors{} object (a named field was missing).
func hasFieldErrors(data []byte) bool {
	var b struct {
		Errors map[string]any `json:"errors"`
	}
	return json.Unmarshal(data, &b) == nil && len(b.Errors) > 0
}

// rateLimitedWrite is the local refusal while the gate is closed; RetryInMs is the gate's remaining wait.
func rateLimitedWrite(wait time.Duration) error {
	e := apperr.New(apperr.RateLimited, msgBusy)
	e.Retryable = true
	return e.WithRetryIn(wait.Milliseconds())
}

// writeTransportError classifies a failure of a write. Before a response: DNS, refused or unroutable dials and TLS
// errors are offline. Anything else means the request may have reached Jira, so it is timeout and is never re-sent
// (the next refresh decides whether the change landed).
func writeTransportError(err error) error {
	if unreachable(err) || isHTTPOnHTTPS(err) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || isNetTimeout(err) {
		return MapError(err)
	}
	return apperr.Wrap(apperr.Timeout, msgNoTimeAnswer, err).WithDetail(redactDetail(err.Error()))
}

package jiratest

// Light-write endpoints of the fake (jira-sync.md §2.15, WP-T5). A rejected write leaves the Dataset untouched: every
// check runs before the first mutation. Faults (Throttle, Fail, SetUnauthorized) are applied by dispatch before these
// handlers run, so they never mutate either.
//
// Fake workflow (SPEC-NOTE workflow in writes_test.go): from the issue's current status there is one transition per
// OTHER status, named after the target status, with id "t"+statusID and isAvailable true. "Done" needs a screen with a
// required field "resolution" (no default); "Code Review" has a screen with optional fields only; every other target
// has no screen.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const (
	transitionScreenDone   = "Done"
	transitionScreenReview = "Code Review"
	flagImpediment         = "Impediment"
)

// handleTransitions answers GET /rest/api/3/issue/{id}/transitions (fields only with expand=transitions.fields).
func (s *Server) handleTransitions(w http.ResponseWriter, r *http.Request) {
	is := s.issueByRef(r.PathValue("id"))
	if is == nil {
		s.writeIssueNotFound(w)
		return
	}
	withFields := strings.Contains(r.URL.Query().Get("expand"), "transitions.fields")
	out := []map[string]any{}
	for _, st := range s.ds.Statuses {
		if st.ID == is.StatusID {
			continue
		}
		hasScreen, fields := s.screenFor(st.Name)
		tr := map[string]any{
			"id":          transitionID(st.ID),
			"name":        st.Name,
			"to":          map[string]any{"id": st.ID, "name": st.Name},
			"hasScreen":   hasScreen,
			"isAvailable": true,
		}
		if withFields {
			tr["fields"] = fields
		}
		out = append(out, tr)
	}
	writeJSON(w, http.StatusOK, map[string]any{"transitions": out})
}

// handleTransitionPost answers POST /rest/api/3/issue/{id}/transitions with body {"transition":{"id":"..."}} → 204.
func (s *Server) handleTransitionPost(w http.ResponseWriter, r *http.Request) {
	is := s.issueByRef(r.PathValue("id"))
	if is == nil {
		s.writeIssueNotFound(w)
		return
	}
	var body struct {
		Transition *struct {
			ID string `json:"id"`
		} `json:"transition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	if body.Transition == nil || body.Transition.ID == "" {
		writeError(w, http.StatusBadRequest, "The transition id is required.")
		return
	}
	target, ok := s.transitionTarget(is, body.Transition.ID)
	if !ok {
		writeError(w, http.StatusBadRequest, "That transition is not available for this issue.")
		return
	}
	if target.Name == transitionScreenDone {
		// Jira's answer when a required screen field was not supplied: a field-level complaint in errors{}.
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"errorMessages": []string{},
			"errors":        map[string]string{"resolution": "Resolution is required."},
		})
		return
	}
	s.appendChange(is, "status", "status", is.StatusID, target.ID)
	is.StatusID = target.ID
	w.WriteHeader(http.StatusNoContent)
}

// handleAssign answers PUT /rest/api/3/issue/{id}/assignee: {"accountId":"<id>"}, or {"accountId":null} to unassign.
func (s *Server) handleAssign(w http.ResponseWriter, r *http.Request) {
	is := s.issueByRef(r.PathValue("id"))
	if is == nil {
		s.writeIssueNotFound(w)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	raw, ok := body["accountId"]
	if !ok {
		writeError(w, http.StatusBadRequest, "The accountId is required (null unassigns).")
		return
	}
	next := ""
	if raw != nil {
		id, isString := raw.(string)
		if !isString {
			writeError(w, http.StatusBadRequest, "The accountId must be a string or null.")
			return
		}
		if _, known := s.userByID(id); id != "" && !known {
			writeError(w, http.StatusBadRequest, "The user does not exist.")
			return
		}
		next = id
	}
	s.appendChange(is, "assignee", "assignee", is.Assignee, next)
	is.Assignee = next
	w.WriteHeader(http.StatusNoContent)
}

// handleIssuePut answers PUT /rest/api/3/issue/{id}. Only the Flagged field is writable (the light flag write).
func (s *Server) handleIssuePut(w http.ResponseWriter, r *http.Request) {
	is := s.issueByRef(r.PathValue("id"))
	if is == nil {
		s.writeIssueNotFound(w)
		return
	}
	var body struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	raw, only := body.Fields[FlaggedFieldID]
	if len(body.Fields) != 1 || !only {
		writeError(w, http.StatusBadRequest, "Only the flag field can be set.")
		return
	}
	flagged := false
	if string(raw) != "null" {
		var values []map[string]any
		if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 || values[0]["value"] != flagImpediment {
			writeError(w, http.StatusBadRequest, "The flag value is not valid.")
			return
		}
		flagged = true
	}
	from, to := "", ""
	if is.Flagged {
		from = flagImpediment
	}
	if flagged {
		to = flagImpediment
	}
	s.appendChange(is, FlaggedFieldID, "Flagged", from, to)
	is.Flagged = flagged
	w.WriteHeader(http.StatusNoContent)
}

// handleCommentPost answers POST /rest/api/3/issue/{id}/comment with {"body": <ADF object>} → 201 {id, …}. The author
// is the /myself account; the body is not stored (the fake keeps counts and ids only).
func (s *Server) handleCommentPost(w http.ResponseWriter, r *http.Request) {
	is := s.issueByRef(r.PathValue("id"))
	if is == nil {
		s.writeIssueNotFound(w)
		return
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	if raw := strings.TrimSpace(string(body["body"])); !strings.HasPrefix(raw, "{") {
		writeError(w, http.StatusBadRequest, "The comment body is required.")
		return
	}
	now := s.ds.Now
	c := DSComment{ID: s.nextWriteID(), At: now, Author: s.meAccount()}
	is.Comments = append(is.Comments, c)
	is.Updated = now
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": c.ID, "created": jiraTime(now), "updated": jiraTime(now), "author": s.authorOf(c.Author),
	})
}

// writeIssueNotFound answers Jira's 404 for an unknown or invisible issue.
func (s *Server) writeIssueNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
}

// transitionID is the fake's id for the transition into a status (any non-empty id unique per response works).
func transitionID(statusID string) string { return "t" + statusID }

// transitionTarget resolves a transition id offered for the issue's current status.
func (s *Server) transitionTarget(is *DSIssue, id string) (DSStatus, bool) {
	for _, st := range s.ds.Statuses {
		if st.ID != is.StatusID && transitionID(st.ID) == id {
			return st, true
		}
	}
	return DSStatus{}, false
}

// screenFor reports the fake's screen for a transition into the named status.
func (s *Server) screenFor(target string) (bool, map[string]any) {
	switch target {
	case transitionScreenDone:
		return true, map[string]any{"resolution": map[string]any{"required": true, "hasDefaultValue": false}}
	case transitionScreenReview:
		return true, map[string]any{"fixVersions": map[string]any{"required": false, "hasDefaultValue": false}}
	}
	return false, map[string]any{}
}

// meAccount is the account GET /myself reports.
func (s *Server) meAccount() string {
	if len(s.ds.Users) > 0 {
		return s.ds.Users[0].AccountID
	}
	return "fake-user"
}

// appendChange records one changelog entry on the issue, stamped with the fake clock.
func (s *Server) appendChange(is *DSIssue, fieldID, field, from, to string) {
	now := s.ds.Now
	is.Changelog = append(is.Changelog, DSChange{ID: s.nextWriteID(), At: now, FieldID: fieldID, Field: field, From: from, To: to})
	is.Updated = now
}

// nextWriteID returns an id that no change or comment in the dataset has (writes use the "w" prefix).
func (s *Server) nextWriteID() string {
	highest := 0
	note := func(id string) {
		if n, err := strconv.Atoi(strings.TrimPrefix(id, "w")); err == nil && strings.HasPrefix(id, "w") && n > highest {
			highest = n
		}
	}
	for _, is := range s.ds.Issues {
		for _, c := range is.Changelog {
			note(c.ID)
		}
		for _, c := range is.Comments {
			note(c.ID)
		}
	}
	return fmt.Sprintf("w%d", highest+1)
}

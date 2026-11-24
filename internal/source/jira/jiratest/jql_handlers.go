package jiratest

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"
)

// Handlers below run with the server lock held (see Server.ServeHTTP).

// handleSearch serves POST /rest/api/3/search/jql (fields projection, nextPageToken = opaque base64 offset).
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JQL           string   `json:"jql"`
		Fields        []string `json:"fields"`
		MaxResults    int      `json:"maxResults"`
		NextPageToken string   `json:"nextPageToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	q, err := ParseJQL(body.JQL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset := 0
	if body.NextPageToken != "" {
		o, ok := decodeBulkToken(body.NextPageToken)
		if !ok {
			writeError(w, http.StatusBadRequest, "The nextPageToken is not valid.")
			return
		}
		offset = o
	}
	matches := s.matchingIssues(q)
	start := min(offset, len(matches))
	end := min(start+s.limit(body.MaxResults, 50), len(matches))
	issues := []map[string]any{}
	for _, is := range matches[start:end] {
		issues = append(issues, s.issueJSON(is, body.Fields))
	}
	out := map[string]any{"issues": issues, "isLast": end >= len(matches)}
	if end < len(matches) {
		out["nextPageToken"] = encodeBulkToken(end)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleApproxCount serves POST /rest/api/3/search/approximate-count.
func (s *Server) handleApproxCount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JQL string `json:"jql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	q, err := ParseJQL(body.JQL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(s.matchingIssues(q))})
}

// handleJQLParse serves POST /rest/api/3/jql/parse: one entry per query, index-aligned with the request.
func (s *Server) handleJQLParse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Queries []string `json:"queries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	out := []map[string]any{}
	for _, query := range body.Queries {
		errs := []string{}
		if _, err := ParseJQL(query); err != nil {
			errs = append(errs, err.Error())
		}
		out = append(out, map[string]any{"query": query, "errors": errs})
	}
	writeJSON(w, http.StatusOK, map[string]any{"queries": out})
}

// matchingIssues returns the issues matching q: slice order, or Rank order when the query asks for it.
func (s *Server) matchingIssues(q *JQL) []*DSIssue {
	var out []*DSIssue
	for _, is := range s.ds.Issues {
		if q.Match(s.ds, is) {
			out = append(out, is)
		}
	}
	if q.OrderByRank {
		sort.SliceStable(out, func(a, b int) bool {
			if q.Desc {
				return out[a].Rank > out[b].Rank
			}
			return out[a].Rank < out[b].Rank
		})
	}
	return out
}

func (s *Server) issueTypeJSON(typeID string) map[string]any {
	var t DSType
	for _, dt := range s.ds.Types {
		if dt.ID == typeID {
			t = dt
			break
		}
	}
	level := t.Hierarchy
	if level == 0 && t.Subtask {
		level = -1
	}
	return map[string]any{"id": t.ID, "name": t.Name, "subtask": t.Subtask, "hierarchyLevel": level}
}

func (s *Server) statusJSON(statusID string) any {
	for _, st := range s.ds.Statuses {
		if st.ID == statusID {
			return map[string]any{
				"id": st.ID, "name": st.Name, "statusCategory": map[string]any{"key": st.Category},
			}
		}
	}
	return nil
}

func (s *Server) assigneeJSON(accountID string) any {
	if accountID == "" {
		return nil
	}
	u, ok := s.userByID(accountID)
	if !ok {
		u = FxUser{AccountID: accountID, DisplayName: accountID}
	}
	return map[string]any{
		"accountId": u.AccountID, "displayName": u.DisplayName, "active": true, "accountType": accountTypeOf(u),
	}
}

// commentJSON reports the total and embeds the most recent (up to three, oldest first). Bodies are never served.
func commentJSON(is *DSIssue) map[string]any {
	cs := is.Comments
	embedded := []map[string]any{}
	for _, c := range cs[max(0, len(cs)-3):] {
		embedded = append(embedded, map[string]any{
			"id": c.ID, "created": jiraTime(c.At), "author": map[string]any{"accountId": c.Author},
		})
	}
	return map[string]any{"total": len(cs), "comments": embedded}
}

// sprintsJSON lists the issue's sprints in the sprint field's shape; no sprints → null.
func (s *Server) sprintsJSON(is *DSIssue) any {
	var out []map[string]any
	for _, sid := range is.SprintIDs {
		sp := findSprint(s.ds, sid)
		if sp == nil {
			continue
		}
		out = append(out, map[string]any{
			"id": sp.ID, "name": sp.Name, "state": sp.State, "boardId": sp.BoardID,
			"startDate": jiraTime(sp.Start), "endDate": jiraTime(sp.End),
		})
	}
	if out == nil {
		return nil
	}
	return out
}

// flaggedJSON is a non-empty multicheckbox array when the issue is flagged; otherwise null.
func flaggedJSON(is *DSIssue) any {
	if !is.Flagged {
		return nil
	}
	return []map[string]any{{"value": "Impediment"}}
}

func optionalTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return jiraTime(t)
}

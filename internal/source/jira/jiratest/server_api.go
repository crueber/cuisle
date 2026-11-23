package jiratest

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Handlers below run with the server lock held (see Server.ServeHTTP).

func (s *Server) handleMyself(w http.ResponseWriter, r *http.Request) {
	u := FxUser{AccountID: "fake-user", DisplayName: "Fake User"}
	if len(s.ds.Users) > 0 {
		u = s.ds.Users[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accountId":   u.AccountID,
		"displayName": u.DisplayName,
		"active":      true,
		"accountType": accountTypeOf(u),
	})
}

func (s *Server) handleField(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]any{
		{"id": SprintFieldID, "key": SprintFieldID, "name": "Sprint", "custom": true, "orderable": true,
			"schema": map[string]any{"type": "array", "custom": "com.pyxis.greenhopper.jira:gh-sprint"}},
		{"id": RankFieldID, "key": RankFieldID, "name": "Rank", "custom": true, "orderable": true,
			"schema": map[string]any{"type": "string", "custom": "com.pyxis.greenhopper.jira:gh-lexo-rank"}},
		{"id": FlaggedFieldID, "key": FlaggedFieldID, "name": "Flagged", "custom": true, "orderable": false,
			"schema": map[string]any{"type": "array", "custom": "com.atlassian.jira.plugin.system.customfieldtypes:multicheckboxes"}},
	})
}

func (s *Server) handleProjectSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	needle := strings.ToLower(q.Get("query"))
	vals := []map[string]any{}
	for _, p := range s.ds.Projects {
		if needle != "" && !strings.Contains(strings.ToLower(p.Key), needle) && !strings.Contains(strings.ToLower(p.Name), needle) {
			continue
		}
		vals = append(vals, map[string]any{"id": p.ID, "key": p.Key, "name": p.Name})
	}
	start, end, limit := s.page(q, len(vals), 50)
	writeJSON(w, http.StatusOK, map[string]any{
		"values": vals[start:end], "startAt": start, "maxResults": limit, "total": len(vals), "isLast": end >= len(vals),
	})
}

func (s *Server) handleBoardSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectRef := q.Get("projectKeyOrId")
	needle := strings.ToLower(q.Get("name"))
	matches := []map[string]any{}
	for _, b := range s.ds.Boards {
		if projectRef != "" && !s.projectMatches(b.ProjectKey, projectRef) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(b.Name), needle) {
			continue
		}
		matches = append(matches, map[string]any{
			"id": b.ID, "name": b.Name, "type": b.Type, "location": map[string]any{"projectKey": b.ProjectKey},
		})
	}
	start, end, limit := s.page(q, len(matches), 50)
	writeJSON(w, http.StatusOK, map[string]any{
		"values": matches[start:end], "startAt": start, "maxResults": limit, "total": len(matches), "isLast": end >= len(matches),
	})
}

func (s *Server) handleBoardConfiguration(w http.ResponseWriter, r *http.Request) {
	b := s.boardByPath(r)
	if b == nil {
		writeError(w, http.StatusNotFound, "The board does not exist or you do not have permission to see it.")
		return
	}
	cols := []map[string]any{}
	for _, c := range b.Columns {
		sts := []map[string]any{}
		for _, name := range c.Statuses {
			sts = append(sts, map[string]any{"id": s.statusIDOf(name), "name": name})
		}
		cols = append(cols, map[string]any{"name": c.Name, "statuses": sts})
	}
	out := map[string]any{
		"id":           b.ID,
		"name":         b.Name,
		"type":         b.Type,
		"filter":       map[string]any{"id": strconv.Itoa(b.FilterID)},
		"columnConfig": map[string]any{"columns": cols},
		"ranking":      map[string]any{"rankCustomFieldId": rankFieldNumber()},
	}
	if b.SubQuery != "" {
		out["subQuery"] = map[string]any{"query": b.SubQuery}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSprint(w http.ResponseWriter, r *http.Request) {
	b := s.boardByPath(r)
	if b == nil {
		writeError(w, http.StatusNotFound, "The board does not exist or you do not have permission to see it.")
		return
	}
	if b.Type != "scrum" {
		writeError(w, http.StatusBadRequest, "The board does not support sprints.")
		return
	}
	var states []string
	if st := r.URL.Query().Get("state"); st != "" {
		states = strings.Split(st, ",")
	}
	vals := []map[string]any{}
	for _, sp := range b.Sprints {
		if len(states) > 0 && !containsString(states, sp.State) {
			continue
		}
		vals = append(vals, map[string]any{
			"id": sp.ID, "name": sp.Name, "state": sp.State, "originBoardId": b.ID,
			"startDate": jiraTime(sp.Start), "endDate": jiraTime(sp.End),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": vals, "startAt": 0, "maxResults": 50, "isLast": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	vals := []map[string]any{}
	for _, st := range s.ds.Statuses {
		vals = append(vals, map[string]any{
			"id": st.ID, "name": st.Name, "statusCategory": map[string]any{"key": st.Category},
		})
	}
	writeJSON(w, http.StatusOK, vals)
}

// bulkLog, bulkHistory and bulkItem are the wire shapes of POST /rest/api/3/changelog/bulkfetch (jira-sync §2.9).
type bulkLog struct {
	IssueID   string        `json:"issueId"`
	Histories []bulkHistory `json:"changeHistories"`
}

type bulkHistory struct {
	ID      string     `json:"id"`
	Created string     `json:"created"`
	Items   []bulkItem `json:"items"`
}

type bulkItem struct {
	Field   string  `json:"field"`
	FieldID string  `json:"fieldId"`
	From    *string `json:"from"`
	To      *string `json:"to"`
}

func (s *Server) handleBulkFetch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
		FieldIDs       []string `json:"fieldIds"`
		MaxResults     int      `json:"maxResults"`
		NextPageToken  string   `json:"nextPageToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
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
	wantField := map[string]bool{}
	for _, f := range body.FieldIDs {
		wantField[f] = true
	}
	type item struct {
		is *DSIssue
		ch DSChange
	}
	var all []item
	seen := map[*DSIssue]bool{}
	for _, ref := range body.IssueIDsOrKeys {
		is := s.issueByRef(ref)
		if is == nil || seen[is] {
			continue
		}
		seen[is] = true
		for _, ch := range is.Changelog {
			if len(wantField) > 0 && !wantField[ch.FieldID] {
				continue
			}
			all = append(all, item{is, ch})
		}
	}
	if offset > len(all) {
		offset = len(all)
	}
	end := min(offset+s.limit(body.MaxResults, 1000), len(all))
	logs := []bulkLog{}
	index := map[string]int{}
	for _, it := range all[offset:end] {
		i, ok := index[it.is.ID]
		if !ok {
			i = len(logs)
			index[it.is.ID] = i
			logs = append(logs, bulkLog{IssueID: it.is.ID, Histories: []bulkHistory{}})
		}
		logs[i].Histories = append(logs[i].Histories, bulkHistory{
			ID:      it.ch.ID,
			Created: jiraTime(it.ch.At),
			Items: []bulkItem{{
				Field: it.ch.Field, FieldID: it.ch.FieldID, From: nullable(it.ch.From), To: nullable(it.ch.To),
			}},
		})
	}
	out := map[string]any{"issueChangeLogs": logs, "isLast": end >= len(all)}
	if end < len(all) {
		out["nextPageToken"] = encodeBulkToken(end)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleComments(w http.ResponseWriter, r *http.Request) {
	is := s.issueByRef(r.PathValue("id"))
	if is == nil {
		writeError(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	q := r.URL.Query()
	desc := q.Get("orderBy") == "-created"
	idx := make([]int, len(is.Comments))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ta, tb := is.Comments[idx[a]].At, is.Comments[idx[b]].At
		if desc {
			return tb.Before(ta)
		}
		return ta.Before(tb)
	})
	start, end, limit := s.page(q, len(idx), 50)
	vals := []map[string]any{}
	for _, i := range idx[start:end] {
		c := is.Comments[i]
		vals = append(vals, map[string]any{
			"id":      c.ID,
			"created": jiraTime(c.At),
			"updated": jiraTime(c.At),
			"author":  s.authorOf(c.Author),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"startAt": start, "maxResults": limit, "total": len(idx), "comments": vals,
	})
}

// devFault answers the configured DevStatus failure mode; it returns true when it wrote the response.
func (s *Server) devFault(w http.ResponseWriter) bool {
	switch s.opts.DevStatus {
	case "404":
		writeError(w, http.StatusNotFound, "Not found.")
	case "403":
		writeError(w, http.StatusForbidden, "Forbidden.")
	case "garbage":
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><body>Log in to continue</body></html>"))
	default:
		return false
	}
	return true
}

type devCount struct {
	Count int    `json:"count"`
	Name  string `json:"name"`
}

func (s *Server) handleDevSummary(w http.ResponseWriter, r *http.Request) {
	if s.devFault(w) {
		return
	}
	is := s.issueByRef(r.URL.Query().Get("issueId"))
	if is == nil {
		writeError(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	repo := map[string]any{"overall": map[string]any{"count": len(is.Commits)}, "byInstanceType": map[string]any{}}
	if len(is.Commits) > 0 {
		var last time.Time
		for _, c := range is.Commits {
			if c.At.After(last) {
				last = c.At
			}
		}
		repo["overall"] = map[string]any{"count": len(is.Commits), "lastUpdated": jiraTime(last)}
		repo["byInstanceType"] = map[string]any{"GitHub": devCount{Count: len(is.Commits), Name: "GitHub"}}
	}
	pr := map[string]any{"overall": map[string]any{"count": len(is.PRs), "stateCount": 0}, "byInstanceType": map[string]any{}}
	if len(is.PRs) > 0 {
		latest := is.PRs[0]
		for _, p := range is.PRs[1:] {
			if !p.LastUpdate.Before(latest.LastUpdate) {
				latest = p
			}
		}
		stateCount := 0
		for _, p := range is.PRs {
			if p.Status == latest.Status {
				stateCount++
			}
		}
		pr["overall"] = map[string]any{
			"count": len(is.PRs), "lastUpdated": jiraTime(latest.LastUpdate), "state": latest.Status, "stateCount": stateCount,
		}
		pr["byInstanceType"] = map[string]any{"GitHub": devCount{Count: len(is.PRs), Name: "GitHub"}}
	}
	writeJSON(w, http.StatusOK, map[string]any{"summary": map[string]any{"repository": repo, "pullrequest": pr}})
}

type devCommit struct {
	ID              string `json:"id"`
	AuthorTimestamp string `json:"authorTimestamp"`
	Merge           bool   `json:"merge"`
}

type devRepo struct {
	Name    string      `json:"name"`
	Commits []devCommit `json:"commits"`
}

type devPR struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	LastUpdate     string `json:"lastUpdate"`
	RepositoryName string `json:"repositoryName"`
}

func (s *Server) handleDevDetail(w http.ResponseWriter, r *http.Request) {
	if s.devFault(w) {
		return
	}
	q := r.URL.Query()
	is := s.issueByRef(q.Get("issueId"))
	if is == nil {
		writeError(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	detail := []any{}
	if q.Get("applicationType") == "GitHub" {
		switch q.Get("dataType") {
		case "repository":
			detail = append(detail, map[string]any{"repositories": reposOf(is.Commits)})
		case "pullrequest":
			prs := []devPR{}
			for _, p := range is.PRs {
				prs = append(prs, devPR{
					ID: strconv.Itoa(p.ID), Status: p.Status, LastUpdate: jiraTime(p.LastUpdate), RepositoryName: p.Repo,
				})
			}
			detail = append(detail, map[string]any{"pullRequests": prs})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"detail": detail})
}

func reposOf(commits []DSCommit) []devRepo {
	repos := []devRepo{}
	index := map[string]int{}
	for _, c := range commits {
		i, ok := index[c.Repo]
		if !ok {
			i = len(repos)
			index[c.Repo] = i
			repos = append(repos, devRepo{Name: c.Repo, Commits: []devCommit{}})
		}
		repos[i].Commits = append(repos[i].Commits, devCommit{ID: c.SHA, AuthorTimestamp: jiraTime(c.At), Merge: c.Merge})
	}
	return repos
}

func (s *Server) handleAssignableSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	needle := strings.ToLower(q.Get("query"))
	vals := []map[string]any{}
	for _, u := range s.ds.Users {
		if needle != "" && !strings.Contains(strings.ToLower(u.DisplayName), needle) {
			continue
		}
		vals = append(vals, map[string]any{
			"accountId": u.AccountID, "displayName": u.DisplayName, "active": true, "accountType": accountTypeOf(u),
		})
	}
	start, end, _ := s.page(q, len(vals), 20)
	writeJSON(w, http.StatusOK, vals[start:end])
}

// ── helpers (called with the lock held) ─────────────────────────────────────────────────────────

// page returns the [start,end) window of n items for startAt/maxResults and the effective page size.
func (s *Server) page(q url.Values, n, def int) (start, end, limit int) {
	startAt, _ := strconv.Atoi(q.Get("startAt"))
	maxResults, _ := strconv.Atoi(q.Get("maxResults"))
	limit = s.limit(maxResults, def)
	start = min(max(startAt, 0), n)
	end = min(start+limit, n)
	return start, end, limit
}

// limit honours the requested size (or def when absent) and caps it at Options.PageSize when set.
func (s *Server) limit(requested, def int) int {
	n := requested
	if n <= 0 {
		n = def
	}
	if s.opts.PageSize > 0 && n > s.opts.PageSize {
		n = s.opts.PageSize
	}
	return n
}

func (s *Server) projectMatches(key, ref string) bool {
	if strings.EqualFold(key, ref) {
		return true
	}
	for _, p := range s.ds.Projects {
		if p.Key == key && p.ID == ref {
			return true
		}
	}
	return false
}

func (s *Server) boardByPath(r *http.Request) *DSBoard {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		return nil
	}
	for i := range s.ds.Boards {
		if s.ds.Boards[i].ID == id {
			return &s.ds.Boards[i]
		}
	}
	return nil
}

func (s *Server) issueByRef(ref string) *DSIssue {
	for _, is := range s.ds.Issues {
		if is.ID == ref || is.Key == ref {
			return is
		}
	}
	return nil
}

func (s *Server) statusIDOf(name string) string {
	for _, st := range s.ds.Statuses {
		if st.Name == name {
			return st.ID
		}
	}
	return ""
}

func (s *Server) userByID(id string) (FxUser, bool) {
	for _, u := range s.ds.Users {
		if u.AccountID == id {
			return u, true
		}
	}
	return FxUser{}, false
}

// authorOf renders a comment author like Jira does; unknown ids are served as plain atlassian accounts.
func (s *Server) authorOf(id string) map[string]any {
	u, ok := s.userByID(id)
	if !ok {
		u = FxUser{AccountID: id, DisplayName: id}
	}
	return map[string]any{"accountId": u.AccountID, "displayName": u.DisplayName, "accountType": accountTypeOf(u)}
}

func accountTypeOf(u FxUser) string {
	if u.AccountType == "" {
		return "atlassian"
	}
	return u.AccountType
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// rankFieldNumber is the numeric part of RankFieldID (Jira reports rankCustomFieldId as a number).
func rankFieldNumber() int {
	n, _ := strconv.Atoi(strings.TrimPrefix(RankFieldID, "customfield_"))
	return n
}

// jiraTime formats a time the way Jira does: ISO-8601 with milliseconds and a numeric offset.
func jiraTime(t time.Time) string {
	return t.Format("2006-01-02T15:04:05.000-0700")
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func encodeBulkToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodeBulkToken(tok string) (int, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		code = http.StatusInternalServerError
		b = []byte(`{"errorMessages":["fake jira: marshal failed"],"errors":{}}`)
	}
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"errorMessages": []string{msg}, "errors": map[string]string{}})
}

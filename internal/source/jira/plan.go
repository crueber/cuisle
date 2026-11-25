package jira

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

const (
	orderByRank        = " ORDER BY Rank ASC"
	defaultBacklogRows = 50
	doneWindowClause   = "updated >= -15d"
)

// Plan builds the membership queries of jira-sync.md §2.8 as JQL in Query.Expr (sprint/open/done/backlog).
// Empty status lists drop a query; an empty working set → apperr validation "Map at least one column".
// backlog has Limit 50 and Count true.
func (a *Adapter) Plan(ctx context.Context, in source.PlanInput) ([]source.Query, error) {
	filter, err := filterIDText(in.Board.Board.Meta["filterId"])
	if err != nil {
		return nil, err
	}

	// Partition the status ids by lane. Ids are sorted so the JQL is stable.
	var working, progress, done, backlog, todo []string
	for _, id := range sortedIDs(in.StatusLanes) {
		switch in.StatusLanes[id] {
		case model.LaneTodo:
			working = append(working, id)
			todo = append(todo, id)
		case model.LaneProg, model.LaneReview, model.LaneQA:
			working = append(working, id)
			progress = append(progress, id)
		default:
			// Custom lanes the user named are working lanes past To Do, like In Progress.
			if model.IsCustomLane(in.StatusLanes[id]) {
				working = append(working, id)
				progress = append(progress, id)
			}
		case model.LaneDone:
			done = append(done, id)
		case model.LaneBacklog:
			backlog = append(backlog, id)
		}
	}
	if len(working) == 0 {
		return nil, apperr.New(apperr.Validation,
			"Map at least one column to a working lane (To Do, In Progress, Review, QA or one you added) before standups can load.")
	}
	working = sortedIDList(working)
	progress = sortedIDList(progress)
	done = sortedIDList(done)

	limit := in.BacklogLimit
	if limit <= 0 {
		limit = defaultBacklogRows
	}
	subQuery := metaText(in.Board.Board.Meta, "subQuery")
	extra := strings.TrimSpace(in.ExtraFilter)
	base := "filter = " + filter
	suffix := func(clause string) string {
		s := clause
		if subQuery != "" {
			s += " AND (" + subQuery + ")"
		}
		if extra != "" {
			s += " AND (" + extra + ")"
		}
		return s + orderByRank
	}
	inList := func(ids []string) string { return "(" + strings.Join(ids, ", ") + ")" }
	backlogQuery := func(expr string) source.Query {
		return source.Query{Name: "backlog", Role: model.RoleBacklog, Limit: limit, Count: true, Expr: expr}
	}

	var out []source.Query
	if in.Board.Board.Kind == "scrum" {
		sprintIDs := make([]string, 0, len(in.ActiveSprints))
		for _, s := range in.ActiveSprints {
			sprintIDs = append(sprintIDs, s.ID)
		}
		hasSprints := len(sprintIDs) > 0
		if in.Scope == "sprint" {
			if hasSprints {
				out = append(out, source.Query{Name: "sprint", Role: model.RoleBoard,
					Expr: suffix(base + " AND sprint in " + inList(sprintIDs))})
			}
		} else {
			openClause := "sprint in openSprints()"
			if len(progress) > 0 {
				openClause += " OR status in " + inList(progress)
			}
			out = append(out, source.Query{Name: "open", Role: model.RoleBoard,
				Expr: suffix(base + " AND status in " + inList(working) + " AND (" + openClause + ")")})
			if len(done) > 0 {
				doneClause := doneWindowClause
				if hasSprints {
					doneClause += " OR sprint in " + inList(sprintIDs)
				}
				out = append(out, source.Query{Name: "done", Role: model.RoleBoard,
					Expr: suffix(base + " AND status in " + inList(done) + " AND (" + doneClause + ")")})
			}
		}
		// Scrum backlog: backlog and todo statuses together, outside any open sprint.
		backlogIDs := sortedIDList(append(append([]string(nil), backlog...), todo...))
		if len(backlogIDs) > 0 {
			out = append(out, backlogQuery(suffix(base+" AND status in "+inList(backlogIDs)+
				" AND (sprint is EMPTY OR sprint not in openSprints())")))
		}
		return out, nil
	}

	// Kanban: scope and sprints are ignored.
	out = append(out, source.Query{Name: "open", Role: model.RoleBoard,
		Expr: suffix(base + " AND status in " + inList(working))})
	if len(done) > 0 {
		out = append(out, source.Query{Name: "done", Role: model.RoleBoard,
			Expr: suffix(base + " AND status in " + inList(done) + " AND (" + doneWindowClause + ")")})
	}
	if len(backlog) > 0 {
		out = append(out, backlogQuery(suffix(base+" AND status in "+inList(sortedIDList(backlog)))))
	}
	return out, nil
}

// filterIDText renders a filterId meta value (int, int64, float64, string or json.Number) as a plain integer.
func filterIDText(v any) (string, error) {
	var s string
	switch x := v.(type) {
	case int:
		s = strconv.Itoa(x)
	case int64:
		s = strconv.FormatInt(x, 10)
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		s = x.String()
	case string:
		s = strings.TrimSpace(x)
	}
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return "", apperr.New(apperr.Validation, "The board has no usable filter. Pick the board again.")
	}
	return s, nil
}

// metaText reads a string value from board meta; anything else is "".
func metaText(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// sortedIDs returns the keys of m sorted numerically when they are digit strings.
func sortedIDs(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return sortedIDList(out)
}

// sortedIDList sorts ids ascending: by length, then lexicographically (numeric order for equal-length digit ids).
func sortedIDList(ids []string) []string {
	sort.Slice(ids, func(i, j int) bool {
		if len(ids[i]) != len(ids[j]) {
			return len(ids[i]) < len(ids[j])
		}
		return ids[i] < ids[j]
	})
	return ids
}

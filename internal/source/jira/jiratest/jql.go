package jiratest

import (
	"strings"
	"time"
)

// JQL is a parsed query of the supported subset: filter = N, sprint in (…), sprint in openSprints(),
// sprint is EMPTY, sprint not in openSprints(), status in (…) (ids or quoted names), id in (…), updated >= -Nd,
// AND/OR/NOT/() and ORDER BY Rank [ASC|DESC].
type JQL struct {
	root        node
	OrderByRank bool
	Desc        bool
}

type node interface {
	eval(ds *Dataset, is *DSIssue) bool
}

// ParseJQL parses s; unsupported syntax → error with a Jira-like message.
func ParseJQL(s string) (*JQL, error) {
	toks, err := lexJQL(s)
	if err != nil {
		return nil, err
	}
	p := &jqlParser{toks: toks}
	if p.peek().kind == tokEOF {
		return nil, jqlError(0, "the query is empty")
	}
	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	q := &JQL{root: root}
	if p.acceptKeyword("ORDER") {
		if !p.acceptKeyword("BY") {
			return nil, p.unexpected()
		}
		if !p.acceptKeyword("RANK") {
			return nil, jqlError(p.peek().pos, "only ORDER BY Rank is supported")
		}
		q.OrderByRank = true
		switch {
		case p.acceptKeyword("ASC"):
		case p.acceptKeyword("DESC"):
			q.Desc = true
		}
	}
	if p.peek().kind != tokEOF {
		return nil, p.unexpected()
	}
	return q, nil
}

// Match reports whether is matches q in ds ("filter = N" matches issues of the board whose FilterID is N).
func (q *JQL) Match(ds *Dataset, is *DSIssue) bool { return q.root.eval(ds, is) }

// ── evaluation ──────────────────────────────────────────────────────────────────────────────────

type orNode struct{ l, r node }

func (n orNode) eval(ds *Dataset, is *DSIssue) bool { return n.l.eval(ds, is) || n.r.eval(ds, is) }

type andNode struct{ l, r node }

func (n andNode) eval(ds *Dataset, is *DSIssue) bool { return n.l.eval(ds, is) && n.r.eval(ds, is) }

type notNode struct{ x node }

func (n notNode) eval(ds *Dataset, is *DSIssue) bool { return !n.x.eval(ds, is) }

type filterNode struct{ id int }

func (n filterNode) eval(ds *Dataset, is *DSIssue) bool {
	b := boardByID(ds, is.BoardID)
	return b != nil && b.FilterID == n.id
}

type sprintInNode struct{ ids []int }

func (n sprintInNode) eval(_ *Dataset, is *DSIssue) bool {
	for _, sid := range is.SprintIDs {
		for _, want := range n.ids {
			if sid == want {
				return true
			}
		}
	}
	return false
}

// openSprintsNode is "sprint in openSprints()": any sprint that is not closed (active or future).
type openSprintsNode struct{}

func (openSprintsNode) eval(ds *Dataset, is *DSIssue) bool {
	for _, sid := range is.SprintIDs {
		if sp := findSprint(ds, sid); sp != nil && sp.State != "closed" {
			return true
		}
	}
	return false
}

type sprintEmptyNode struct{}

func (sprintEmptyNode) eval(_ *Dataset, is *DSIssue) bool { return len(is.SprintIDs) == 0 }

// statusInNode matches a status by id, or (quoted values only) by id or case-insensitive name.
type statusInNode struct{ vals []jqlValue }

func (n statusInNode) eval(ds *Dataset, is *DSIssue) bool {
	name := statusName(ds, is.StatusID)
	for _, v := range n.vals {
		if v.text == is.StatusID || (v.quoted && strings.EqualFold(v.text, name)) {
			return true
		}
	}
	return false
}

type idInNode struct{ vals []jqlValue }

func (n idInNode) eval(_ *Dataset, is *DSIssue) bool {
	for _, v := range n.vals {
		if v.text == is.ID {
			return true
		}
	}
	return false
}

// updatedNode is "updated >= -Nd": inclusive, measured back from Dataset.Now.
type updatedNode struct{ days int }

func (n updatedNode) eval(ds *Dataset, is *DSIssue) bool {
	threshold := ds.Now.Add(-time.Duration(n.days) * 24 * time.Hour)
	return !is.Updated.Before(threshold)
}

// ── lookups ─────────────────────────────────────────────────────────────────────────────────────

func boardByID(ds *Dataset, id int) *DSBoard {
	for i := range ds.Boards {
		if ds.Boards[i].ID == id {
			return &ds.Boards[i]
		}
	}
	return nil
}

func findSprint(ds *Dataset, id int) *DSSprint {
	for i := range ds.Boards {
		for j := range ds.Boards[i].Sprints {
			if sp := &ds.Boards[i].Sprints[j]; sp.ID == id {
				return sp
			}
		}
	}
	return nil
}

func statusName(ds *Dataset, id string) string {
	for _, st := range ds.Statuses {
		if st.ID == id {
			return st.Name
		}
	}
	return ""
}

package jiratest

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ParseFixture decodes fixture JSON (DefaultFixture or a file).
func ParseFixture(b []byte) (*Fixture, error) {
	var fx Fixture
	if err := json.Unmarshal(b, &fx); err != nil {
		return nil, fmt.Errorf("jiratest: parse fixture: %w", err)
	}
	return &fx, nil
}

// Materialize turns relative days into absolute times at now in loc: statuses from board columns (category from the
// column's lane semantics: first column new, last done, others indeterminate), ids "10001"…, ranks in slice order,
// changelog from History (one status change per transition at 10:00 local of the day it happened), assignee and flag
// changes, comments/commits/PRs at 11:00 local of their day. Deterministic for the same inputs.
func Materialize(fx *Fixture, now time.Time, loc *time.Location) *Dataset {
	if fx == nil {
		fx = &Fixture{}
	}
	if loc == nil {
		loc = time.Local
	}
	m := &materializer{
		now: now, loc: loc, today: now.In(loc),
		nextChange: 30001, nextComment: 40001,
		statusID: map[string]string{}, statusCat: map[string]string{}, typeID: map[string]string{},
		users: fx.Users,
	}
	ds := &Dataset{
		Now:      now,
		Projects: append([]FxProject(nil), fx.Projects...),
		Users:    append([]FxUser(nil), fx.Users...),
	}
	m.ds = ds

	projectOf := map[int]string{}
	sprintOf := map[int]int{}
	firstStatus := map[int]string{}
	for _, fb := range fx.Boards {
		if _, ok := projectOf[fb.ID]; !ok {
			projectOf[fb.ID] = fb.ProjectKey
		}
		db := DSBoard{FxBoard: fb}
		db.Columns = append([]FxColumn(nil), fb.Columns...)
		if fb.Sprint != nil {
			sp := *fb.Sprint
			db.Sprint = &sp
			db.Sprints = []DSSprint{{
				ID: sp.ID, BoardID: fb.ID, Name: sp.Name, State: "active",
				Start: m.at(sp.StartDay, 9), End: m.at(sp.EndDay, 17),
			}}
			if _, ok := sprintOf[fb.ID]; !ok {
				sprintOf[fb.ID] = sp.ID
			}
		}
		for i, col := range fb.Columns {
			cat := "indeterminate"
			switch {
			case i == 0:
				cat = "new"
			case i == len(fb.Columns)-1:
				cat = "done"
			}
			for j, name := range col.Statuses {
				id := m.statusFor(name, cat)
				if i == 0 && j == 0 {
					if _, ok := firstStatus[fb.ID]; !ok {
						firstStatus[fb.ID] = id
					}
				}
			}
		}
		ds.Boards = append(ds.Boards, db)
	}

	// Known types first, so the list is stable whatever the fixture uses.
	for _, n := range []string{"Story", "Bug", "Task", "Sub-task", "Epic"} {
		m.typeFor(n)
	}

	// Board issues first (fixture order), then backlog. A key appears once; the board entry wins.
	seen := map[string]bool{}
	for _, fi := range fx.Issues {
		if seen[fi.Key] {
			continue
		}
		seen[fi.Key] = true
		ds.Issues = append(ds.Issues, m.boardIssue(fi, len(ds.Issues), projectOf[fi.Board], sprintOf[fi.Board], firstStatus[fi.Board]))
	}
	for _, b := range fx.Backlog {
		if seen[b.Key] {
			continue
		}
		seen[b.Key] = true
		ds.Issues = append(ds.Issues, m.backlogIssue(b, len(ds.Issues), projectOf[b.Board], firstStatus[b.Board]))
	}
	return ds
}

// materializer carries the counters and lookups of one Materialize call.
type materializer struct {
	now, today  time.Time
	loc         *time.Location
	nextChange  int
	nextComment int
	users       []FxUser
	ds          *Dataset
	statusID    map[string]string // name -> id
	statusCat   map[string]string // id -> category
	typeID      map[string]string // name -> id
}

// at returns hour:00 local time on the day `day` days from today. Today's slots that would still be in the future
// (a morning run of the fake) are pulled back before now, keeping their order and staying on today's date, so a
// morning standup still sees today's activity.
func (m *materializer) at(day, hour int) time.Time {
	y, mo, d := m.today.Date()
	t := time.Date(y, mo, d+day, hour, 0, 0, 0, m.loc)
	if day == 0 && t.After(m.now) {
		midnight := time.Date(y, mo, d, 0, 0, 0, 0, m.loc)
		t = m.now.Add(-time.Duration(24-hour) * time.Minute)
		if floor := midnight.Add(time.Duration(hour) * time.Second); t.Before(floor) {
			t = floor
		}
	}
	return t
}

func (m *materializer) statusFor(name, category string) string {
	if id, ok := m.statusID[name]; ok {
		return id
	}
	id := strconv.Itoa(10100 + len(m.ds.Statuses))
	m.statusID[name] = id
	m.statusCat[id] = category
	m.ds.Statuses = append(m.ds.Statuses, DSStatus{ID: id, Name: name, Category: category})
	return id
}

func (m *materializer) typeFor(name string) string {
	if id, ok := m.typeID[name]; ok {
		return id
	}
	t := DSType{ID: strconv.Itoa(10200 + len(m.ds.Types)), Name: name}
	switch name {
	case "Epic":
		t.Hierarchy = 1
	case "Sub-task":
		t.Subtask = true
	}
	m.typeID[name] = t.ID
	m.ds.Types = append(m.ds.Types, t)
	return t.ID
}

// boardIssue materialises one board ticket. idx is its position in the dataset (drives id and rank).
func (m *materializer) boardIssue(fi FxIssue, idx int, project string, sprintID int, firstStatus string) *DSIssue {
	is := &DSIssue{
		ID:         strconv.Itoa(10001 + idx),
		Key:        fi.Key,
		ProjectKey: project,
		BoardID:    fi.Board,
		TypeID:     m.typeFor(fi.Type),
		Summary:    fi.Summary,
		Assignee:   fi.Assignee,
		Rank:       rankFor(idx),
	}
	if fi.InSprint && sprintID != 0 {
		is.SprintIDs = []int{sprintID}
	}

	h := fi.History
	is.Created = m.at(0, 9)
	is.StatusID = firstStatus
	if len(h) > 0 {
		is.Created = m.at(entryStart(h, 0), 9)
		is.StatusID = m.statusFor(h[len(h)-1].Status, "indeterminate")
	}

	var changes []DSChange
	if fi.Assignee != "" {
		changes = append(changes, DSChange{At: is.Created, FieldID: "assignee", Field: "assignee", To: fi.Assignee})
	}
	for i := 1; i < len(h); i++ {
		changes = append(changes, DSChange{
			At:      m.at(entryStart(h, i), 10),
			FieldID: "status", Field: "status",
			From: m.statusFor(h[i-1].Status, "indeterminate"),
			To:   m.statusFor(h[i].Status, "indeterminate"),
		})
	}
	flagged := false
	for _, rng := range fi.FlaggedDays {
		from, to := rng[0], rng[1]
		changes = append(changes, DSChange{At: m.at(from, 10), FieldID: FlaggedFieldID, Field: "Flagged", To: "Impediment"})
		if to < 0 {
			changes = append(changes, DSChange{At: m.at(to+1, 10), FieldID: FlaggedFieldID, Field: "Flagged", From: "Impediment"})
		} else {
			flagged = true
		}
	}
	sort.SliceStable(changes, func(a, b int) bool { return changes[a].At.Before(changes[b].At) })
	for i := range changes {
		changes[i].ID = strconv.Itoa(m.nextChange)
		m.nextChange++
	}
	is.Changelog = changes
	is.Flagged = flagged

	author := fi.Assignee
	if author == "" && len(m.users) > 0 {
		author = m.users[0].AccountID
	}
	for _, d := range fi.Comments {
		is.Comments = append(is.Comments, DSComment{ID: strconv.Itoa(m.nextComment), At: m.at(d, 11), Author: author})
		m.nextComment++
	}
	repo := repoFor(project)
	for i, d := range fi.Commits {
		is.Commits = append(is.Commits, DSCommit{SHA: shaFor(fi.Key, i), Repo: repo, At: m.at(d, 11)})
	}
	for _, p := range fi.PRs {
		is.PRs = append(is.PRs, DSPR{ID: p.ID, Repo: repo, Status: p.Status, LastUpdate: m.at(p.Day, 11), Title: p.Title, Branch: p.Branch})
	}

	upd := is.Created
	bump := func(t time.Time) {
		if t.After(upd) {
			upd = t
		}
	}
	for _, c := range is.Changelog {
		bump(c.At)
	}
	for _, c := range is.Comments {
		bump(c.At)
	}
	for _, c := range is.Commits {
		bump(c.At)
	}
	for _, p := range is.PRs {
		bump(p.LastUpdate)
	}
	if upd.After(m.now) {
		upd = m.now
	}
	is.Updated = upd

	if m.statusCat[is.StatusID] == "done" {
		is.Resolved = is.Created
		for _, c := range is.Changelog {
			if c.FieldID == "status" {
				is.Resolved = c.At
			}
		}
	}
	return is
}

// backlogIssue materialises a backlog entry: first column's status, no sprint, no activity.
func (m *materializer) backlogIssue(b FxBacklog, idx int, project, firstStatus string) *DSIssue {
	created := m.at(-14, 9)
	return &DSIssue{
		ID:         strconv.Itoa(10001 + idx),
		Key:        b.Key,
		ProjectKey: project,
		BoardID:    b.Board,
		TypeID:     m.typeFor(b.Type),
		Summary:    b.Summary,
		StatusID:   firstStatus,
		Rank:       rankFor(idx),
		Created:    created,
		Updated:    created,
	}
}

// entryStart is the relative day on which history entry i begins: the last entry ends today, so entry i starts
// (sum of the days of entries i..n-1) − 1 days before today.
func entryStart(h []FxHistory, i int) int {
	sum := 0
	for _, e := range h[i:] {
		sum += e.Days
	}
	return 1 - sum
}

// rankFor is a zero-padded LexoRank-like string, so string order equals slice order at any board size.
func rankFor(idx int) string {
	return fmt.Sprintf("0|%06d", idx+1)
}

func shaFor(key string, i int) string {
	sum := sha1.Sum([]byte(key + "/commit/" + strconv.Itoa(i)))
	return hex.EncodeToString(sum[:])
}

func repoFor(project string) string {
	if project == "" {
		return "web"
	}
	return strings.ToLower(project) + "-web"
}

package view

// Shared fixtures and helpers for the view tests (WP-31). Everything here is hand-built and deterministic.
//
// World ("main"): scrum board, asOf = Wednesday 2026-11-25 12:00 America/Chicago (CST, UTC-6), 14-day window.
//
//	day index k = local date (2026-11-12 + k):  0=Nov12 1=Nov13 2=Nov14(Sat) 3=Nov15(Sun) 4=Nov16(Mon) … 8=Nov20 … 11=Nov23(Mon)
//	12=Nov24 13=Nov25 (today)
//
// Lane walk order is deliberately NOT the default: qa(limit 3), review(2), prog(5), todo(10), then done.

import (
	"fmt"
	"strconv"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/crueber/cuisle/internal/model"
)

var chi = mustLoc("America/Chicago")

func mustLoc(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return l
}

// ct builds a Chicago-local time.
func ct(mo time.Month, d, h, m int) time.Time { return time.Date(2026, mo, d, h, m, 0, 0, chi) }

// nv builds a Chicago-local November 2026 time.
func nv(d, h, m int) time.Time { return ct(time.November, d, h, m) }

var mainAsOf = nv(25, 12, 0)

const urlPrefix = "https://acme.atlassian.net/browse/"

// status ids used by every fixture
const (
	sTodo    = "s1"
	sProg    = "s2"
	sReview  = "s3"
	sQA      = "s4"
	sDone    = "s5"
	sBacklog = "s6"
	sParked  = "s7"
	sBlocked = "s8"
	sMystery = "s9" // in no board column → not shown
	sDoing   = "s10"
)

// baseConfig is the effective config shared by all fixtures (a scrum board).
func baseConfig() model.EffectiveConfig {
	return model.EffectiveConfig{
		StandupID: 7, Rev: 3, Name: "MOB standup",
		SpaceID: "10001", SpaceKey: "MOB", SpaceName: "Service Desk",
		BoardID: "42", BoardName: "MOB board", BoardType: "scrum", Scope: "sprint",
		ExtraFilter: "labels = standup",
		Lanes: []model.LaneConfig{
			{Lane: model.LaneQA, Position: 0, LimitDays: 3},
			{Lane: model.LaneReview, Position: 1, LimitDays: 2},
			{Lane: model.LaneProg, Position: 2, LimitDays: 5},
			{Lane: model.LaneTodo, Position: 3, LimitDays: 10},
		},
		Columns: []model.ColumnLane{
			{ColumnName: "Backlog", Position: 0, Lane: model.LaneBacklog, Auto: true},
			{ColumnName: "To Do", Position: 1, Lane: model.LaneTodo, Auto: true},
			{ColumnName: "In Progress", Position: 2, Lane: model.LaneProg, Auto: true},
			{ColumnName: "Blocked", Position: 3, Lane: model.LaneProg, Auto: false},
			{ColumnName: "Review", Position: 4, Lane: model.LaneReview, Auto: true},
			{ColumnName: "Testing", Position: 5, Lane: model.LaneQA, Auto: false},
			{ColumnName: "Done", Position: 6, Lane: model.LaneDone, Auto: true},
			{ColumnName: "Parked", Position: 7, Lane: model.LaneHidden, Auto: true},
			{ColumnName: "Icebox", Position: 8, Lane: model.LaneHidden, Auto: false},
		},
		StatusLanes: []model.StatusLane{
			{StatusID: sBacklog, StatusName: "Backlog", ColumnName: "Backlog", Lane: model.LaneBacklog},
			{StatusID: sTodo, StatusName: "To Do", ColumnName: "To Do", Lane: model.LaneTodo},
			{StatusID: sProg, StatusName: "In Progress", ColumnName: "In Progress", Lane: model.LaneProg},
			{StatusID: sDoing, StatusName: "Doing", ColumnName: "In Progress", Lane: model.LaneProg},
			{StatusID: sBlocked, StatusName: "Blocked", ColumnName: "Blocked", Lane: model.LaneProg},
			{StatusID: sReview, StatusName: "In Review", ColumnName: "Review", Lane: model.LaneReview},
			{StatusID: sQA, StatusName: "QA", ColumnName: "Testing", Lane: model.LaneQA},
			{StatusID: sDone, StatusName: "Done", ColumnName: "Done", Lane: model.LaneDone},
			{StatusID: sParked, StatusName: "Parked", ColumnName: "Parked", Lane: model.LaneHidden},
		},
		Speakers: []model.SpeakerConfig{
			{AccountID: "a1", Name: "Iris Calloway", Position: 0, Included: true},
			{AccountID: "a2", Name: "Silas Webb", Position: 1, Included: true, Manual: true},
			{AccountID: "a4", Name: "Cher", Position: 2, Included: true},
			{AccountID: "a3", Name: "Amara Osei", Position: 3, Included: false},
			{AccountID: "a5", Name: "Lee Park", Position: 4, Included: false},
		},
		UnassignedPosition: "last",
		PulseDays:          14,
		ShowCommits:        true, ShowPRs: false, ShowComments: true, ShadeWeekends: true,
		RowsMode: "roomy", DoneWindow: "sprint", BacklogRows: 2,
		ActiveSprintIDs: []string{"s-42"},
	}
}

func baseStatuses() map[string]model.Status {
	return map[string]model.Status{
		sTodo:    {ID: sTodo, Name: "To Do", Category: "new"},
		sProg:    {ID: sProg, Name: "In Progress", Category: "indeterminate"},
		sReview:  {ID: sReview, Name: "In Review", Category: "indeterminate"},
		sQA:      {ID: sQA, Name: "QA", Category: "indeterminate"},
		sDone:    {ID: sDone, Name: "Done", Category: "done"},
		sBacklog: {ID: sBacklog, Name: "Backlog", Category: "new"},
		sParked:  {ID: sParked, Name: "Parked", Category: "new"},
		sBlocked: {ID: sBlocked, Name: "Blocked", Category: "indeterminate", BlockedHint: true},
		sMystery: {ID: sMystery, Name: "Mystery", Category: "undefined"},
		sDoing:   {ID: sDoing, Name: "Doing", Category: "indeterminate"},
	}
}

// basePeople derives People from the config speakers so the two always agree, plus an app account.
func basePeople(cfg model.EffectiveConfig) map[string]model.Person {
	p := map[string]model.Person{"app1": {AccountID: "app1", DisplayName: "Build Bot", Active: true, Kind: "app"}}
	for _, s := range cfg.Speakers {
		p[s.AccountID] = model.Person{AccountID: s.AccountID, DisplayName: s.Name, Active: true, Kind: "user"}
	}
	return p
}

// world accumulates members and their history; it derives the duplicated facts (ids, times) from the config.
type world struct {
	snap model.BoardSnapshot
}

func newWorld(asOf time.Time, cfg model.EffectiveConfig) *world {
	snap := model.BoardSnapshot{
		StandupID: cfg.StandupID, AsOf: asOf, Live: true, Config: cfg,
		History: map[string]model.IssueHistory{}, People: basePeople(cfg), Statuses: baseStatuses(),
		BacklogTotal: -1, IssueURLPrefix: urlPrefix, DevActivity: "ok",
	}
	if cfg.BoardType == "scrum" {
		// ActiveSprintIDs and Sprints agree (a loader never produces one without the other).
		for _, id := range cfg.ActiveSprintIDs {
			snap.Sprints = append(snap.Sprints, model.Sprint{
				ID: id, BoardID: cfg.BoardID, Name: "Sprint " + id, State: "active",
				StartAt: asOf.AddDate(0, 0, -7), EndAt: asOf.AddDate(0, 0, 3),
			})
		}
	}
	return &world{snap: snap}
}

type chg struct {
	at time.Time
	to string
}

// tkt is a fluent ticket builder. The issue's StatusID is the last change's To (or the initial status), UpdatedAt is
// the last event time, so Timeline's synthetic-change rule never fires.
type tkt struct {
	id       int64
	key      string
	summary  string
	kind     string
	assignee string
	initial  string
	created  time.Time
	changes  []chg
	role     string
	sprints  []string
	flagged  bool
	flags    []model.FlagChange
	comments []model.Comment
	dev      []model.DevEvent
}

func newTkt(id int64, key, kind, assignee, initial string, created time.Time, changes ...chg) *tkt {
	return &tkt{
		id: id, key: key, summary: "Summary of " + key, kind: kind, assignee: assignee,
		initial: initial, created: created, changes: changes, role: model.RoleBoard,
	}
}

func (k *tkt) backlog() *tkt                    { k.role = model.RoleBacklog; return k }
func (k *tkt) inSprints(ids ...string) *tkt     { k.sprints = ids; return k }
func (k *tkt) withDev(e ...model.DevEvent) *tkt { k.dev = append(k.dev, e...); return k }
func (k *tkt) withComments(c ...model.Comment) *tkt {
	k.comments = append(k.comments, c...)
	return k
}
func (k *tkt) flaggedFrom(at time.Time) *tkt {
	k.flagged = true
	k.flags = append(k.flags, model.FlagChange{ID: "f-" + k.key, At: at, Flagged: true})
	return k
}

func (w *world) add(k *tkt) *world {
	cfg := w.snap.Config
	status := k.initial
	last := k.created
	var sc []model.StatusChange
	for i, c := range k.changes {
		sc = append(sc, model.StatusChange{ID: fmt.Sprintf("%s-sc%d", k.key, i), At: c.at, From: status, To: c.to})
		status = c.to
		last = c.at
	}
	for _, f := range k.flags {
		if f.At.After(last) {
			last = f.At
		}
	}
	for _, c := range k.comments {
		if c.At.After(last) {
			last = c.At
		}
	}
	for _, e := range k.dev {
		if e.At.After(last) {
			last = e.At
		}
	}
	sid := "10" + strconv.FormatInt(k.id, 10)
	spaceStatusName := w.snap.Statuses[status].Name
	issue := model.Issue{
		SourceID: sid, Key: k.key, SpaceID: cfg.SpaceID, Summary: k.summary,
		TypeID: "t-" + k.kind, StatusID: status, AssigneeID: k.assignee, Flagged: k.flagged,
		Rank: fmt.Sprintf("0|%06d:", len(w.snap.Members)), SprintIDs: k.sprints,
		CreatedAt: k.created, UpdatedAt: last,
	}
	w.snap.Members = append(w.snap.Members, model.Member{
		LocalID: k.id, Issue: issue, TypeKind: k.kind, StatusName: spaceStatusName,
		Role: k.role, RankIndex: len(w.snap.Members),
	})
	w.snap.History[sid] = model.IssueHistory{StatusChanges: sc, FlagChanges: k.flags, Comments: k.comments, Dev: k.dev}
	return w
}

// comment builds a comment at a local time by an author.
func comment(id string, at time.Time, author string) model.Comment {
	return model.Comment{ID: id, At: at, AuthorID: author}
}

// mainWorld is the rich fixture used by the field-by-field, speaker, golden and JSON tests.
// Members are in rank order (deliberately not lane order). Visible tickets, in order:
// MOB-2, MOB-5, MOB-1, MOB-10, MOB-3, MOB-9, MOB-4, MOB-11. Not shown: MOB-6 (hidden lane), MOB-7 (backlog lane),
// MOB-8 (unmapped status), MOB-12 (hidden lane, excluded speaker). Backlog role: MOB-101..MOB-103 (BacklogRows = 2).
func mainWorld() *world {
	w := newWorld(mainAsOf, baseConfig())

	// MOB-2: review since yesterday; 2 commits + a merge commit (ignored) on Nov 24, a PR on Nov 25, a comment on Nov 24,
	// and a comment by an app account (ignored).
	w.add(newTkt(2, "MOB-2", "bug", "a2", sTodo, ct(time.November, 3, 9, 0),
		chg{nv(10, 9, 0), sProg}, chg{nv(24, 15, 0), sReview}).
		withDev(
			model.DevEvent{Kind: model.DevCommit, ID: "c1", Repo: "mob", At: nv(24, 10, 0)},
			model.DevEvent{Kind: model.DevCommit, ID: "c2", Repo: "mob", At: nv(24, 11, 0)},
			model.DevEvent{Kind: model.DevCommit, ID: "m1", Repo: "mob", At: nv(24, 12, 0), Merge: true},
			model.DevEvent{Kind: model.DevPR, ID: "svc#7", Repo: "mob", At: nv(25, 9, 0), Status: "OPEN"},
		).
		withComments(comment("k1", nv(24, 14, 0), "a2"), comment("k2", nv(25, 9, 30), "app1")))

	// MOB-5: done on Monday Nov 23 14:00.
	w.add(newTkt(5, "MOB-5", "story", "a2", sTodo, ct(time.November, 5, 9, 0),
		chg{nv(10, 9, 0), sProg}, chg{nv(23, 14, 0), sDone}))

	// MOB-1: in progress since Nov 18 (7 days, limit 5).
	w.add(newTkt(1, "MOB-1", "story", "a1", sTodo, ct(time.November, 1, 9, 0),
		chg{nv(18, 9, 0), sProg}))

	// MOB-10: new unassigned subtask created yesterday.
	w.add(newTkt(10, "MOB-10", "subtask", "", sTodo, nv(24, 9, 0)))

	// MOB-3: flagged since Nov 24 08:00, in QA since Nov 20 (5 days, limit 3); 5 comments on 5 days (Nov 20..24).
	w.add(newTkt(3, "MOB-3", "task", "", sTodo, ct(time.November, 2, 9, 0),
		chg{nv(10, 9, 0), sProg}, chg{nv(14, 9, 0), sReview}, chg{nv(20, 11, 0), sQA}).
		flaggedFrom(nv(24, 8, 0)).
		withComments(
			comment("q1", nv(20, 13, 0), "a1"), comment("q2", nv(21, 13, 0), "a1"), comment("q3", nv(22, 13, 0), "a1"),
			comment("q4", nv(23, 13, 0), "a1"), comment("q5", nv(24, 13, 0), "a1")))

	// MOB-6: hidden lane, MOB-7: backlog lane, MOB-8: unmapped status (none of the three is shown).
	w.add(newTkt(6, "MOB-6", "story", "a1", sParked, ct(time.November, 1, 9, 0)))
	w.add(newTkt(7, "MOB-7", "story", "a1", sBacklog, ct(time.November, 1, 9, 0)))
	w.add(newTkt(8, "MOB-8", "story", "a1", sMystery, ct(time.November, 1, 9, 0)))

	// MOB-9: todo since creation Nov 9 (16 days, limit 10), assigned to an excluded speaker.
	w.add(newTkt(9, "MOB-9", "task", "a3", sTodo, ct(time.November, 9, 9, 0)))

	// MOB-12: hidden lane, assigned to an excluded speaker who therefore has no visible ticket.
	w.add(newTkt(12, "MOB-12", "task", "a5", sParked, ct(time.November, 1, 9, 0)))

	// MOB-4: created yesterday directly in the blocked-hint status (prog lane).
	w.add(newTkt(4, "MOB-4", "spike", "a1", sBlocked, nv(24, 10, 0)))

	// MOB-11: done on Friday Nov 20, excluded speaker.
	w.add(newTkt(11, "MOB-11", "epic", "a3", sTodo, ct(time.November, 8, 9, 0),
		chg{nv(20, 10, 0), sDone}))

	// Backlog role, in rank order.
	w.add(newTkt(101, "MOB-101", "story", "", sBacklog, ct(time.October, 1, 9, 0)).backlog())
	w.add(newTkt(102, "MOB-102", "bug", "a1", sBacklog, ct(time.October, 2, 9, 0)).backlog())
	w.add(newTkt(103, "MOB-103", "task", "", sBacklog, ct(time.October, 3, 9, 0)).backlog())

	w.snap.BacklogTotal = 28
	w.snap.Warnings = []string{"unmapped_statuses"}
	w.snap.LastSyncedAt = nv(25, 11, 30)
	return w
}

// doneWorld is a minimal world with only done tickets, one per entry (member order = argument order).
func doneWorld(asOf time.Time, scope, window string, doneAts []time.Time, sprints [][]string) *world {
	cfg := baseConfig()
	cfg.Scope = scope
	cfg.DoneWindow = window
	w := newWorld(asOf, cfg)
	for i, d := range doneAts {
		key := fmt.Sprintf("D-%d", i+1)
		k := newTkt(int64(200+i), key, "story", "a1", sTodo, d.AddDate(0, 0, -5),
			chg{d.AddDate(0, 0, -2), sProg}, chg{d, sDone})
		if i < len(sprints) {
			k.inSprints(sprints[i]...)
		}
		w.add(k)
	}
	return w
}

// ── helpers ──

// build runs Build, turning the stubs' panic into a per-test failure.
func build(t *testing.T, snap model.BoardSnapshot, asOf time.Time) (v StandupView) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Build panicked: %v", r)
		}
	}()
	return Build(snap, asOf)
}

func buildConfig(t *testing.T, cfg model.EffectiveConfig, in ConfigInput) (c StandupConfig) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("BuildConfig panicked: %v", r)
		}
	}()
	return BuildConfig(cfg, in)
}

func initials(t *testing.T, name string) (s string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Initials panicked: %v", r)
		}
	}()
	return Initials(name)
}

func laneLabel(t *testing.T, lane string) (s string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LaneLabel panicked: %v", r)
		}
	}()
	return LaneLabel(lane)
}

func keys(v StandupView) []string {
	out := []string{}
	for _, tk := range v.Tickets {
		out = append(out, tk.Key)
	}
	return out
}

func ticketByKey(t *testing.T, v StandupView, key string) TicketView {
	t.Helper()
	for _, tk := range v.Tickets {
		if tk.Key == key {
			return tk
		}
	}
	t.Fatalf("ticket %s not in view; have %v", key, keys(v))
	return TicketView{}
}

func speakerIDs(v StandupView) []string {
	out := []string{}
	for _, s := range v.Speakers {
		out = append(out, s.ID)
	}
	return out
}

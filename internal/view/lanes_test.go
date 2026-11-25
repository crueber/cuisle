package view

// Custom lanes and lane labels in the view models (ux-spec §5, §7, §9.4 Status mapping).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
)

const sDesign = "s11"

// customWorld is the main fixture plus a custom lane "Design" (c2, hue 2, limit 2, walked between review and prog)
// fed by a Design column, and the review lane renamed "Code Review" and Done renamed "Shipped".
func customWorld() *world {
	cfg := baseConfig()
	cfg.Lanes = []model.LaneConfig{
		{Lane: model.LaneQA, Position: 0, LimitDays: 3},
		{Lane: model.LaneReview, Position: 1, LimitDays: 2, Label: "Code Review"},
		{Lane: "c2", Position: 2, LimitDays: 2, Label: "Design", Hue: 2},
		{Lane: model.LaneProg, Position: 3, LimitDays: 5},
		{Lane: model.LaneTodo, Position: 4, LimitDays: 10},
	}
	cfg.DoneLabel = "Shipped"
	cfg.Columns = append(cfg.Columns, model.ColumnLane{ColumnName: "Design", Position: 9, Lane: "c2"})
	cfg.StatusLanes = append(cfg.StatusLanes, model.StatusLane{StatusID: sDesign, StatusName: "Designing", ColumnName: "Design", Lane: "c2"})
	w := newWorld(mainAsOf, cfg)
	w.snap.Statuses[sDesign] = model.Status{ID: sDesign, Name: "Designing", Category: "indeterminate"}
	// MOB-20: To Do, then Design since Nov 20 (5 days > limit 2), previously To Do.
	w.add(newTkt(20, "MOB-20", "story", "a1", sTodo, nv(16, 9, 0), chg{nv(20, 10, 0), sDesign}))
	// MOB-21: Design, then Code Review since Nov 24.
	w.add(newTkt(21, "MOB-21", "task", "a1", sDesign, nv(16, 9, 0), chg{nv(24, 10, 0), sReview}))
	return w
}

func TestBuild_CustomLanes_InWalkOrderWithLabelsHueAndKind(t *testing.T) {
	v := build(t, customWorld().snap, mainAsOf)
	var got []string
	for _, l := range v.Lanes {
		got = append(got, l.Lane+"|"+l.Label+"|"+l.Kind+"|"+strings.Join(l.Columns, ","))
	}
	want := []string{
		"qa|QA|working|Testing", "review|Code Review|working|Review", "c2|Design|working|Design",
		"prog|In Progress|working|In Progress,Blocked", "todo|To Do|working|To Do", "done|Shipped|done|Done",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lanes =\n %v\nwant\n %v", got, want)
	}
	c := v.Lanes[2]
	if !c.Custom || c.Hue != 2 || c.DefaultLabel != "" || c.LimitDays != 2 {
		t.Errorf("custom lane = %+v, want custom, hue 2, no default label, limit 2", c)
	}
	if r := v.Lanes[1]; r.Custom || r.Hue != 0 || r.DefaultLabel != "In Review" {
		t.Errorf("renamed review = %+v, want built-in with default label In Review", r)
	}
}

func TestBuild_CustomLanes_TicketsCountAsActiveWithLimitsAndLabels(t *testing.T) {
	v := build(t, customWorld().snap, mainAsOf)
	d := ticketByKey(t, v, "MOB-20")
	if d.Lane != "c2" || d.DaysInLane != 5 || !d.OverLimit {
		t.Errorf("MOB-20 = lane %s, %d days, over %v; want c2, 5, true", d.Lane, d.DaysInLane, d.OverLimit)
	}
	if !strings.HasPrefix(d.Pulse.Label, "Design 5 days") || !strings.Contains(d.Pulse.Label, "previously") {
		t.Errorf("MOB-20 label = %q, want the custom lane's name", d.Pulse.Label)
	}
	r := ticketByKey(t, v, "MOB-21")
	if !strings.Contains(r.Pulse.Label, "Code Review") || !strings.Contains(r.Pulse.Label, "Design") {
		t.Errorf("MOB-21 label = %q, want renamed review and the previous custom lane", r.Pulse.Label)
	}
	seen := map[string]bool{}
	for _, s := range r.Pulse.Segs {
		seen[s.Lane] = true
	}
	if !seen["c2"] || !seen["review"] {
		t.Errorf("MOB-21 segments %v, want c2 then review", r.Pulse.Segs)
	}
	if v.Counts.Active != 2 || v.Counts.Done != 0 {
		t.Errorf("active/done = %d/%d, want 2/0 (custom lanes are working lanes)", v.Counts.Active, v.Counts.Done)
	}
}

// Never hide tickets: every shown ticket's lane is one of the view's lanes, so the UI has a section for it.
func TestBuild_EveryTicketLaneIsAViewLane(t *testing.T) {
	for name, w := range map[string]*world{"main": mainWorld(), "custom": customWorld()} {
		v := build(t, w.snap, mainAsOf)
		lanes := map[string]bool{}
		for _, l := range v.Lanes {
			lanes[l.Lane] = true
		}
		for _, tk := range v.Tickets {
			if !lanes[tk.Lane] {
				t.Errorf("%s: %s is in lane %q, which is not in view.Lanes", name, tk.Key, tk.Lane)
			}
			for _, s := range tk.Pulse.Segs {
				if !lanes[s.Lane] && s.Lane != model.LaneTodo {
					t.Errorf("%s: %s has a segment in lane %q with no colour", name, tk.Key, s.Lane)
				}
			}
		}
	}
}

// A mapping that still names a lane the config no longer has (a deleted custom lane) draws as To Do: shown, not lost.
func TestBuild_UnknownCustomLaneIsDrawnAsToDo(t *testing.T) {
	w := customWorld()
	cfg := w.snap.Config
	cfg.Lanes = append([]model.LaneConfig{}, cfg.Lanes[:2]...)
	cfg.Lanes = append(cfg.Lanes, baseConfig().Lanes[2:]...)
	w.snap.Config = cfg
	v := build(t, w.snap, mainAsOf)
	d := ticketByKey(t, v, "MOB-20")
	if d.Lane != model.LaneTodo {
		t.Errorf("MOB-20 lane = %q, want todo", d.Lane)
	}
	for _, s := range d.Pulse.Segs {
		if s.Lane == "c2" {
			t.Errorf("segment still in the deleted lane: %v", d.Pulse.Segs)
		}
	}
}

// The ticket modal shows a status in a custom lane as that lane (its swatch and chips), and the header row's label
// uses the lane's configured name.
func TestBuildTicket_LaneResolverKeepsCustomLanes(t *testing.T) {
	w := customWorld()
	b := newTicketBuilder(w.snap, cache.Ticket{}, mainAsOf, TicketOptions{})
	for status, want := range map[string]string{sDesign: "c2", sReview: model.LaneReview, sDone: model.LaneDone, sParked: model.LaneTodo} {
		if got := b.lane(status); got != want {
			t.Errorf("lane(%s) = %q, want %q", status, got, want)
		}
	}
}

func TestBuildConfig_LaneOptionsAndLeftToRight(t *testing.T) {
	cfg := customWorld().snap.Config
	c := buildConfig(t, cfg, ConfigInput{})
	var opts []string
	for _, o := range c.LaneOptions {
		opts = append(opts, o.Lane+"|"+o.Label+"|"+o.Kind)
	}
	want := []string{
		"backlog|Backlog|backlog", "todo|To Do|working", "prog|In Progress|working", "review|Code Review|working",
		"qa|QA|working", "c2|Design|working", "done|Shipped|done", "hidden|Hidden|hidden",
	}
	if !reflect.DeepEqual(opts, want) {
		t.Errorf("laneOptions =\n %v\nwant\n %v", opts, want)
	}
	// Board order: To Do(1) In Progress(2) Review(4) Testing(5) Design(9).
	if got := c.WalkLeftToRight; !reflect.DeepEqual(got, []string{"todo", "prog", "review", "qa", "c2"}) {
		t.Errorf("walkLeftToRight = %v", got)
	}
	if got := len(c.Lanes); got != 5 {
		t.Errorf("config lanes = %d, want the 5 working lanes", got)
	}
	// A built-in lane with no column falls back to built-in order after the mapped ones.
	cfg.Columns = cfg.Columns[:len(cfg.Columns)-1]
	if got := buildConfig(t, cfg, ConfigInput{}).WalkLeftToRight; !reflect.DeepEqual(got, []string{"todo", "prog", "review", "qa", "c2"}) {
		t.Errorf("walkLeftToRight without a Design column = %v", got)
	}
}

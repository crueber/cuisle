package app_test

// Custom lanes end to end (ux-spec §9.4 Status mapping): create a lane from a column, sync against the fake Jira, and
// the tickets of that column show in the new lane, counted as active, on scrum and kanban boards.

import (
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/view"
)

// workingColumn returns a column mapped to In Progress.
func workingColumn(t *testing.T, c view.StandupConfig) string {
	t.Helper()
	for _, col := range c.Columns {
		if col.Lane == "prog" {
			return col.ColumnName
		}
	}
	t.Fatal("no In Progress column in the fixture")
	return ""
}

func activeIn(v view.StandupView, lane string) int {
	n := 0
	for _, tk := range v.Tickets {
		if tk.Lane == lane {
			n++
		}
	}
	return n
}

func TestCustomLanes_CreateSyncAndShowOnScrumAndKanban(t *testing.T) {
	for _, kind := range []string{"scrum", "kanban"} {
		t.Run(kind, func(t *testing.T) {
			ta := newTestApp(t)
			var id int64
			if kind == "scrum" {
				id = ta.scrumStandup()
			} else {
				ta.connect()
				id = ta.kanbanStandup()
				ta.syncNow(id)
			}
			before := ta.view(id)
			col := workingColumn(t, ta.config(id))

			cfg, err := ta.Svc.Config.CreateLane(ctx, id, "Design", col)
			noErr(t, err, "CreateLane")
			if !cfg.NeedsSync {
				t.Error("creating a lane for a column is a source change (needsSync)")
			}
			var created *view.LaneInfo
			for i, o := range cfg.LaneOptions {
				if o.Custom {
					created = &cfg.LaneOptions[i]
				}
			}
			if created == nil || created.Lane != "c1" || created.Label != "Design" || created.Hue != 1 || created.Kind != "working" {
				t.Fatalf("created lane = %+v, want c1 Design hue 1 working", created)
			}
			if got := cfg.Lanes[len(cfg.Lanes)-1].Lane; got != "c1" {
				t.Errorf("the new lane is not at the end of the walk: last = %s", got)
			}

			ta.syncNow(id)
			v := ta.view(id)
			if activeIn(v, "c1") == 0 {
				t.Fatalf("no tickets in the custom lane after sync (column %q)", col)
			}
			if v.Counts.Active != before.Counts.Active {
				t.Errorf("active = %d, want %d (the column's tickets moved lanes, none were lost)", v.Counts.Active, before.Counts.Active)
			}
			lanes := map[string]bool{}
			for _, l := range v.Lanes {
				lanes[l.Lane] = true
			}
			for _, tk := range v.Tickets {
				if !lanes[tk.Lane] {
					t.Errorf("%s in lane %s, which the view does not list", tk.Key, tk.Lane)
				}
			}
			tabs, err := ta.Svc.Standup.List(ctx)
			noErr(t, err, "List")
			for _, tab := range tabs {
				if tab.ID == id && tab.ActiveCount != v.Counts.Active {
					t.Errorf("tab active count = %d, want %d (custom lanes are active)", tab.ActiveCount, v.Counts.Active)
				}
			}

			// Rename, then delete moving the column back to In Progress.
			_, err = ta.Svc.Config.RenameLane(ctx, id, "c1", "Discovery")
			noErr(t, err, "RenameLane")
			for _, l := range ta.view(id).Lanes {
				if l.Lane == "c1" && l.Label != "Discovery" {
					t.Errorf("view label = %q, want Discovery", l.Label)
				}
			}
			cfg, err = ta.Svc.Config.DeleteLane(ctx, id, "c1", "prog")
			noErr(t, err, "DeleteLane")
			for _, o := range cfg.LaneOptions {
				if o.Custom {
					t.Errorf("lane %s survived the delete", o.Lane)
				}
			}
			ta.syncNow(id)
			if got := ta.view(id).Counts.Active; got != before.Counts.Active {
				t.Errorf("active after delete = %d, want %d", got, before.Counts.Active)
			}
		})
	}
}

func TestCustomLanes_ValidationReachesTheCaller(t *testing.T) {
	ta := newTestApp(t)
	id := ta.scrumStandup()
	_, err := ta.Svc.Config.CreateLane(ctx, id, "QA", "")
	wantCode(t, err, apperr.Validation, "CreateLane(QA)")
	_, err = ta.Svc.Config.RenameLane(ctx, id, "hidden", "Parked")
	wantCode(t, err, apperr.Validation, "RenameLane(hidden)")
	_, err = ta.Svc.Config.DeleteLane(ctx, id, "review", "prog")
	wantCode(t, err, apperr.Validation, "DeleteLane(review)")
}

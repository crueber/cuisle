package view

import (
	"testing"

	"github.com/crueber/cuisle/internal/model"
)

// Display names from real Jira sites carry suffixes like "(Contractor)" or "- Ops"; those must not become initials.
func TestInitials_IgnoresBracketsAndPunctuation(t *testing.T) {
	cases := map[string]string{
		"Iris Calloway":          "IC",
		"Cher":                   "C",
		"Mary Jane Watson":       "MW",
		"Sebastian (Contractor)": "S",
		"Sebastian Silva (Acme)": "SS",
		"Ana Lima - Ops":         "AL",
		"[EXT] Juan Pérez":       "JP",
		"élodie durand":          "ÉD",
		"(bot)":                  "",
		"   ":                    "",
	}
	for in, want := range cases {
		if got := Initials(in); got != want {
			t.Errorf("Initials(%q) = %q, want %q", in, got, want)
		}
	}
	if got := firstWord("[EXT] Juan Pérez"); got != "Juan" {
		t.Errorf("firstWord = %q, want Juan", got)
	}
	if got := firstWord("(bot)"); got != "(bot)" {
		t.Errorf("firstWord fallback = %q, want (bot)", got)
	}
}

// Tickets in a lane are grouped by their column's within-lane order (Configure > Status mapping), rank order inside.
func TestSortByColumnOrder_GroupsALaneByColumnOrderKeepingRank(t *testing.T) {
	snap := model.BoardSnapshot{Config: model.EffectiveConfig{
		Columns: []model.ColumnLane{
			{ColumnName: "In Progress", Lane: "prog", LaneOrder: 1},
			{ColumnName: "Code Review", Lane: "prog", LaneOrder: 0},
		},
		StatusLanes: []model.StatusLane{
			{StatusID: "3", ColumnName: "In Progress", Lane: "prog"},
			{StatusID: "7", ColumnName: "Code Review", Lane: "prog"},
		},
	}}
	status := map[string]string{"A": "3", "B": "7", "C": "3", "D": "7"}
	var views []TicketView
	for _, k := range []string{"A", "B", "C", "D"} { // rank order
		snap.Members = append(snap.Members, model.Member{Issue: model.Issue{SourceID: k, StatusID: status[k]}})
		views = append(views, TicketView{SourceID: k, Lane: "prog"})
	}
	sortByColumnOrder(views, snap)
	got := ""
	for _, v := range views {
		got += v.SourceID
	}
	if got != "BDAC" {
		t.Errorf("order = %s, want BDAC (Code Review first, rank kept within each column)", got)
	}
}

package cache

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/model"
)

func cfgWithLanes(lanes ...model.LaneConfig) model.EffectiveConfig {
	return model.EffectiveConfig{
		Scope: "sprint", PulseDays: 21, DoneWindow: "sprint", RowsMode: "fit", UnassignedPosition: "last",
		Lanes: lanes,
		StatusLanes: []model.StatusLane{
			{StatusID: "1", StatusName: "To Do", ColumnName: "To Do", Lane: "todo"},
			{StatusID: "2", StatusName: "Designing", ColumnName: "Design", Lane: "c2"},
		},
	}
}

var builtinLanes = []model.LaneConfig{
	{Lane: "qa", Position: 0, LimitDays: 3}, {Lane: "review", Position: 1, LimitDays: 2},
	{Lane: "prog", Position: 2, LimitDays: 5}, {Lane: "todo", Position: 3, LimitDays: 10},
}

// A standup with no custom or renamed lanes keeps exactly the pre-0006 document (no "lanes" key), so upgrading does
// not open a new config-history row.
func TestConfigJSON_NoCustomLanes_OmitsLaneDefinitions(t *testing.T) {
	doc, _ := ConfigJSON(cfgWithLanes(builtinLanes...))
	if strings.Contains(string(doc), `"lanes"`) {
		t.Errorf("config_json has lane definitions without custom lanes:\n%s", doc)
	}
}

// Custom lanes, renamed built-in lanes and a renamed Done are recorded with their labels, kinds and colours, so a
// lane deleted or renamed later still resolves in history; every such change moves the fingerprint.
func TestConfigJSON_RecordsLaneDefinitionsAndFingerprintsThem(t *testing.T) {
	lanes := append(append([]model.LaneConfig{}, builtinLanes...), model.LaneConfig{Lane: "c2", Position: 4, LimitDays: 4, Label: "Design", Hue: 2})
	lanes[1].Label = "Code Review"
	cfg := cfgWithLanes(lanes...)
	cfg.DoneLabel = "Shipped"
	doc, fp := ConfigJSON(cfg)
	var got struct {
		WalkOrder []string       `json:"walkOrder"`
		Limits    map[string]int `json:"limits"`
		Lanes     []configLane   `json:"lanes"`
	}
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatal(err)
	}
	want := []configLane{
		{Lane: "review", Label: "Code Review", Kind: "working"},
		{Lane: "c2", Label: "Design", Kind: "working", Hue: 2},
		{Lane: "done", Label: "Shipped", Kind: "done"},
	}
	if !reflect.DeepEqual(got.Lanes, want) {
		t.Errorf("lanes = %+v, want %+v", got.Lanes, want)
	}
	if got.Limits["c2"] != 4 || got.WalkOrder[4] != "c2" {
		t.Errorf("walk/limits = %v / %v, want c2 last with limit 4", got.WalkOrder, got.Limits)
	}

	_, base := ConfigJSON(cfgWithLanes(builtinLanes...))
	renamed := cfg
	renamed.Lanes = append([]model.LaneConfig{}, lanes...)
	renamed.Lanes[4].Label = "Discovery"
	_, fpRenamed := ConfigJSON(renamed)
	if fp == base || fpRenamed == fp {
		t.Errorf("fingerprints must differ: base %s, custom %s, renamed %s", base, fp, fpRenamed)
	}
}

package view

import (
	"github.com/crueber/cuisle/internal/model"
)

// noTicketsNote is the Configure note for a speaker with no active tickets.
const noTicketsNote = "no tickets right now"

// BuildConfig builds the Configure form model. Pure; lists are never nil.
func BuildConfig(cfg model.EffectiveConfig, in ConfigInput) StandupConfig {
	out := StandupConfig{
		ID:                 cfg.StandupID,
		Rev:                cfg.Rev,
		Position:           in.Position,
		Name:               cfg.Name,
		Scope:              cfg.Scope,
		ExtraFilter:        cfg.ExtraFilter,
		Columns:            []ColumnMapping{},
		Lanes:              laneInfos(cfg, false),
		LaneOptions:        laneOptions(cfg),
		WalkLeftToRight:    walkLeftToRight(cfg),
		UnmappedStatuses:   append([]string{}, in.UnmappedStatuses...),
		RowsMode:           cfg.RowsMode,
		Speakers:           []SpeakerConfig{},
		UnassignedPosition: cfg.UnassignedPosition,
		ReshuffleDaily:     cfg.ReshuffleDaily,
		PulseDays:          cfg.PulseDays,
		ShowCommits:        cfg.ShowCommits,
		ShowPRs:            cfg.ShowPRs,
		ShowComments:       cfg.ShowComments,
		ShadeWeekends:      cfg.ShadeWeekends,
		DoneWindow:         cfg.DoneWindow,
		BacklogRows:        cfg.BacklogRows,
		NeedsSync:          in.NeedsSync,
		Draft:              cfg.BoardID == "",
	}
	if cfg.SpaceID != "" || cfg.SpaceKey != "" {
		out.Space = &SpaceOption{ID: cfg.SpaceID, Key: cfg.SpaceKey, Name: cfg.SpaceName}
	}
	if cfg.BoardID != "" {
		out.Board = &BoardOption{ID: cfg.BoardID, Name: cfg.BoardName, Type: cfg.BoardType}
	}
	if in.FilterCheck != nil {
		fc := *in.FilterCheck
		fc.Errors = append([]string{}, in.FilterCheck.Errors...)
		out.FilterCheck = &fc
	}
	for _, c := range sortedColumns(cfg.Columns) {
		out.Columns = append(out.Columns, ColumnMapping{
			ColumnName:  c.ColumnName,
			StatusNames: statusNamesFor(cfg.StatusLanes, c.ColumnName),
			Lane:        c.Lane,
			Auto:        c.Auto,
			Order:       c.LaneOrder,
		})
	}
	for _, s := range sortedSpeakers(cfg.Speakers) {
		active := in.ActiveByAccount[s.AccountID]
		note := ""
		if active == 0 {
			note = noTicketsNote
		}
		out.Speakers = append(out.Speakers, SpeakerConfig{
			AccountID: s.AccountID,
			Name:      s.Name,
			Initials:  Initials(s.Name),
			Position:  s.Position,
			Included:  s.Included,
			Manual:    s.Manual,
			Active:    active,
			Note:      note,
		})
	}
	return out
}

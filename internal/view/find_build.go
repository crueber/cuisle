package view

import (
	"time"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/pulse"
)

// BuildHit fills a TicketHit (F14) from an issue's facts as seen from one standup: its lane in that standup's mapping
// (the same fallback the ticket modal uses for a status the standup does not map: the provider category), days in
// lane and the over-limit flag, computed exactly as the board row does. cfg nil (no standup to look from) leaves Lane
// empty and the counters zero. The caller sets StandupID, StandupIDs, Removed, Tier, Match and TitleMatch.
func BuildHit(f cache.HitFact, cfg *model.EffectiveConfig, asOf time.Time) TicketHit {
	h := TicketHit{
		IssueID:          f.LocalID,
		Key:              f.Key,
		Summary:          f.Summary,
		Type:             f.TypeKind,
		StatusName:       f.StatusName,
		Assignee:         f.AssigneeID,
		AssigneeInitials: Initials(f.AssigneeName),
		StandupIDs:       []int64{},
		Match:            []int{},
		TitleMatch:       []int{},
	}
	if cfg == nil {
		return h
	}
	statusLanes := drawableLanes(*cfg)
	for _, r := range cfg.StatusLanes {
		if r.StatusID == f.StatusID && r.StatusName != "" {
			h.StatusName = r.StatusName
		}
	}
	lane, ok := statusLanes[f.StatusID]
	if !ok || !isShownLane(lane) {
		switch f.StatusCategory {
		case "done":
			lane = model.LaneDone
		case "indeterminate":
			lane = model.LaneProg
		default:
			lane = model.LaneTodo
		}
	}
	h.Lane, h.Done = lane, lane == model.LaneDone
	tl := pulse.Timeline(pulse.Ticket{
		CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt, StatusID: f.StatusID, Flagged: f.Flagged, StatusChanges: f.StatusChanges,
	}, statusLanes, asOf)
	h.DaysInLane = pulse.DaysInLane(pulse.EnteredLane(tl), asOf)
	limit := 0
	for _, l := range cfg.Lanes {
		if l.Lane == lane {
			limit = l.LimitDays
		}
	}
	h.OverLimit = pulse.OverLimit(lane, h.DaysInLane, limit)
	return h
}

package view

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/pulse"
)

// unassignedDots is the Initials of the unassigned pseudo speaker (design/reference/prototype-logic.js `ini: '··'`).
const unassignedDots = "··"

// activeSprint picks the sprint that ends first (a zero EndAt never wins over a known one). Kanban has no sprint.
func activeSprint(cfg model.EffectiveConfig, sprints []model.Sprint, asOf time.Time) *SprintInfo {
	if cfg.BoardType == "kanban" {
		return nil
	}
	var best *model.Sprint
	for i := range sprints {
		s := &sprints[i]
		if best == nil || endsEarlier(s, best) {
			best = s
		}
	}
	if best == nil {
		return nil
	}
	info := &SprintInfo{
		ID:      best.ID,
		Name:    best.Name,
		StartAt: millisOrZero(best.StartAt),
		EndAt:   millisOrZero(best.EndAt),
	}
	if !best.EndAt.IsZero() {
		info.DaysLeft = dayNumber(best.EndAt.In(asOf.Location())) - dayNumber(asOf)
		info.EndsLabel = endsLabel(info.DaysLeft)
	}
	return info
}

func endsEarlier(s, best *model.Sprint) bool {
	if s.EndAt.IsZero() {
		return false
	}
	if best.EndAt.IsZero() {
		return true
	}
	return s.EndAt.Before(best.EndAt)
}

// dayNumber is the calendar day of t in its own location, as a count of days since 1970-01-01.
func dayNumber(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func endsLabel(n int) string {
	switch {
	case n == 0:
		return "ends today"
	case n == 1:
		return "ends tomorrow"
	case n >= 2:
		return fmt.Sprintf("ends in %d days", n)
	case n == -1:
		return "ended yesterday"
	default:
		return fmt.Sprintf("ended %d days ago", -n)
	}
}

// speakerViews turns the effective speaking order into the view, with per-speaker counts.
func speakerViews(cfg model.EffectiveConfig, slots []pulse.SpeakerSlot, activeBy, doneBy map[string]int) []SpeakerView {
	byID := make(map[string]model.SpeakerConfig, len(cfg.Speakers))
	for _, s := range cfg.Speakers {
		byID[s.AccountID] = s
	}
	out := make([]SpeakerView, 0, len(slots))
	for _, sl := range slots {
		if sl.Unassigned {
			out = append(out, SpeakerView{
				ID: model.UnassignedID, Name: "Unassigned", First: "Unassigned", Initials: unassignedDots,
				Unassigned: true, Included: sl.Included, Active: activeBy[""], Done: doneBy[""],
			})
			continue
		}
		name := byID[sl.ID].Name
		if name == "" {
			name = sl.ID
		}
		out = append(out, SpeakerView{
			ID: sl.ID, Name: name, First: firstWord(name), Initials: Initials(name),
			Included: sl.Included, Active: activeBy[sl.ID], Done: doneBy[sl.ID],
		})
	}
	return out
}

// statusNamesFor lists the status names of one board column, in StatusLanes order; never nil.
func statusNamesFor(rows []model.StatusLane, column string) []string {
	out := []string{}
	for _, r := range rows {
		if r.ColumnName == column {
			out = append(out, r.StatusName)
		}
	}
	return out
}

func sortedColumns(cols []model.ColumnLane) []model.ColumnLane {
	out := append([]model.ColumnLane(nil), cols...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

func sortedSpeakers(in []model.SpeakerConfig) []model.SpeakerConfig {
	out := append([]model.SpeakerConfig(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

// firstWord is the first whitespace-separated word as written ("" for blank input).
// firstWord is the first real word of a display name (bracketed parts and punctuation skipped, see nameWords); a name
// with no such word falls back to its first whitespace-separated field.
func firstWord(name string) string {
	if w := nameWords(name); len(w) > 0 {
		return w[0]
	}
	f := strings.Fields(name)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

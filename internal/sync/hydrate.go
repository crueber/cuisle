package sync

import (
	"context"
	"sort"
	"strconv"
	"strings"
	gosync "sync"
	"time"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
	"github.com/crueber/cuisle/internal/source"
)

const (
	devRecheckWindow = 28 * 24 * time.Hour // members that changed status this recently are dev targets until checked once
	devReprobeAfter  = 24 * time.Hour      // "unavailable" is remembered this long for incremental runs
	statusIDChunk    = 500
)

// hydratePhase is phases 3–5 (issues, changelog, comments): one Hydrate call for the ids the scan diff selected.
func (rn *runner) hydratePhase(ctx context.Context, f *Fetched, members []Member) error {
	toHydrate, stamps, err := rn.diffMembers(ctx, members)
	if err != nil {
		return err
	}
	rn.stamps = stamps
	rn.logf(ctx, "info", source.PhaseIssues, strconv.Itoa(len(toHydrate))+" of "+strconv.Itoa(len(members))+" issues to hydrate")
	if len(toHydrate) == 0 {
		return nil
	}
	rn.prog.report(source.PhaseIssues, 0, len(toHydrate))
	details, err := rn.src.Hydrate(ctx, toHydrate, rn.prog.report)
	if err != nil {
		return err
	}
	f.Details = details
	f.Types, f.People = collectSeen(f.Types, details)
	return nil
}

// collectSeen adds the issue types and the people (assignees, comment authors) the hydrated issues carry.
func collectSeen(types []model.IssueType, details []model.IssueDetail) ([]model.IssueType, []model.Person) {
	haveType := map[string]bool{}
	for _, t := range types {
		haveType[t.ID] = true
	}
	havePerson := map[string]bool{}
	var people []model.Person
	addPerson := func(p model.Person) {
		if p.AccountID == "" || havePerson[p.AccountID] {
			return
		}
		havePerson[p.AccountID] = true
		people = append(people, p)
	}
	for _, d := range details {
		if d.Type.ID != "" && !haveType[d.Type.ID] {
			haveType[d.Type.ID] = true
			types = append(types, d.Type)
		}
		if d.Assignee != nil {
			addPerson(*d.Assignee)
		}
		for _, p := range d.People {
			addPerson(p)
		}
	}
	return types, people
}

// devTargets applies jira-sync.md §2.11: board members in a prog/review/qa lane, plus board members that changed status
// within the last 28 days and were last dev-checked before that change (or never).
func (rn *runner) devTargets(ctx context.Context, f *Fetched, members []Member, lanes map[string]string) ([]string, error) {
	hydrated := make(map[string]*model.IssueDetail, len(f.Details))
	for i := range f.Details {
		hydrated[f.Details[i].SourceID] = &f.Details[i]
	}
	var cachedIDs []string
	for _, m := range members {
		if m.Role == model.RoleBoard && hydrated[m.SourceID] == nil {
			cachedIDs = append(cachedIDs, m.SourceID)
		}
	}
	cachedStatus, err := rn.cachedStatusIDs(ctx, cachedIDs)
	if err != nil {
		return nil, err
	}

	window := rn.r.started.Add(-devRecheckWindow)
	var out []string
	for _, m := range members {
		if m.Role != model.RoleBoard {
			continue
		}
		var statusID string
		var lastStatus time.Time
		stamp := rn.stamps[m.SourceID]
		if d := hydrated[m.SourceID]; d != nil {
			statusID = d.StatusID
			for _, c := range d.StatusChanges {
				if c.At.After(lastStatus) {
					lastStatus = c.At
				}
			}
		} else {
			statusID, lastStatus = cachedStatus[m.SourceID], stamp.LastStatusAt
		}
		switch lanes[statusID] {
		case model.LaneProg, model.LaneReview, model.LaneQA:
			out = append(out, m.SourceID)
			continue
		}
		if !lastStatus.IsZero() && !lastStatus.Before(window) &&
			(stamp.DevCheckedAt.IsZero() || stamp.DevCheckedAt.Before(lastStatus)) {
			out = append(out, m.SourceID)
		}
	}
	return out, nil
}

// cachedStatusIDs reads the cached status id of issues (source id → status id), in chunks.
func (rn *runner) cachedStatusIDs(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for start := 0; start < len(ids); start += statusIDChunk {
		chunk := ids[start:min(start+statusIDChunk, len(ids))]
		args := []any{rn.conn.ID}
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := rn.o.opts.DB.R.QueryContext(ctx,
			`SELECT source_id, status_id FROM issue WHERE connection_id = ? AND source_id IN (`+
				strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, apperr.Wrap(apperr.DB, "The issues could not be read.", err)
		}
		for rows.Next() {
			var id, status string
			if err := rows.Scan(&id, &status); err != nil {
				_ = rows.Close()
				return nil, apperr.Wrap(apperr.DB, "The issues could not be read.", err)
			}
			out[id] = status
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, apperr.Wrap(apperr.DB, "The issues could not be read.", err)
		}
	}
	return out, nil
}

// devPhase is phase 6. Failures degrade to warnings: an unsupported or unreachable API (unavailable) skips the rest of
// the phase, other per-issue failures keep the existing rows and make the run partial.
func (rn *runner) devPhase(ctx context.Context, f *Fetched, members []Member, lanes map[string]string) error {
	f.DevState = "unknown" // "ok"/"unavailable" only when this run probed
	f.Dev = map[string]model.DevActivity{}
	if !rn.src.Capabilities().HasDevActivity {
		return nil
	}
	if rn.conn.DevActivityState == "unavailable" && rn.mode == ModeIncremental && !rn.conn.DevCheckedAt.IsZero() &&
		rn.r.started.Sub(rn.conn.DevCheckedAt) < devReprobeAfter {
		rn.warn(WarnDevUnavailable)
		rn.logf(ctx, "info", source.PhaseDev, "dev activity known unavailable, skipped")
		return nil
	}

	targets, err := rn.devTargets(ctx, f, members, lanes)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}

	var (
		mu          gosync.Mutex
		unavailable bool
		partial     bool
		succeeded   int // dev activity answered by the source (a scan-only skip proves nothing about availability)
		skipped     int
		done        int
		checked     []string
	)
	rn.prog.report(source.PhaseDev, 0, len(targets))
	jobs := make(chan string)
	workers := min(rn.o.opts.HydrateConcurrency, len(targets))
	var wg gosync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				mu.Lock()
				skip := unavailable
				mu.Unlock()
				if skip || ctx.Err() != nil {
					continue
				}
				if rn.devUnchanged(id) {
					mu.Lock()
					skipped++
					checked = append(checked, id)
					done++
					d := done
					mu.Unlock()
					rn.prog.report(source.PhaseDev, d, len(targets))
					continue
				}
				act, err := rn.devOne(ctx, id)
				mu.Lock()
				switch {
				case err == nil:
					succeeded++
					checked = append(checked, id)
					if act.Changed {
						f.Dev[id] = act
					}
				case source.IsUnsupported(err):
					unavailable = true
				case ctx.Err() == nil:
					partial = true
				}
				done++
				d := done
				mu.Unlock()
				rn.prog.report(source.PhaseDev, d, len(targets))
			}
		}()
	}
	for _, id := range targets {
		jobs <- id
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return err
	}
	switch {
	case unavailable:
		f.DevState = "unavailable"
		f.Dev = map[string]model.DevActivity{}
		rn.warn(WarnDevUnavailable)
		rn.logf(ctx, "warn", source.PhaseDev, "dev activity unavailable")
	default:
		if succeeded > 0 {
			f.DevState = "ok"
		}
		sort.Strings(checked)
		f.DevChecked = checked
		if partial {
			f.Partial = true
			rn.warn(WarnDevPartial)
			rn.logf(ctx, "warn", source.PhaseDev, "dev activity failed for some issues")
		}
		rn.logf(ctx, "info", source.PhaseDev, strconv.Itoa(len(checked))+" of "+strconv.Itoa(len(targets))+
			" dev targets checked ("+strconv.Itoa(skipped)+" unchanged per the scan)")
	}
	return nil
}

// devUnchanged reports whether the scan already proved a dev target unchanged: it carried a fingerprint equal to the
// stored one, so there is nothing to ask the source (the only way to learn something new is a changed fingerprint, an
// unknown one, or the unavailable re-probe, which needs a real call).
func (rn *runner) devUnchanged(id string) bool {
	fp := rn.devFP[id]
	return fp != "" && fp == rn.stamps[id].DevFingerprint && rn.conn.DevActivityState != "unavailable"
}

// devOne queries one issue; a panic in the adapter is a per-issue failure.
func (rn *runner) devOne(ctx context.Context, id string) (act model.DevActivity, err error) {
	defer func() {
		if p := recover(); p != nil {
			rn.logPanic(p, "")
			err = apperr.New(apperr.Internal, "Something went wrong while syncing.")
		}
	}()
	return rn.src.DevActivity(ctx, id, rn.stamps[id].DevFingerprint)
}

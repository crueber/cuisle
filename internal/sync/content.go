package sync

// The content phase of a run (ADR 0015, jira-sync.md §4.3 phase 5): the adapter has already fetched descriptions, field
// values, links, attachments and complete comments inside Hydrate (they arrive on each IssueDetail); what is left for
// the orchestrator is the field registry, stored with the apply (field_def), and naming what changed per issue so the
// UI can react (ticket:updated {cause: sync}). WP-T6.

import (
	"context"

	"github.com/crueber/cuisle/internal/cache"
	"github.com/crueber/cuisle/internal/source"
)

// WarnContentPartial is the warning of a run whose field registry could not be read; the content tables of the
// hydrated issues are stored regardless.
const WarnContentPartial = "content_partial"

// issueChange is one hydrated issue that changed in the apply.
type issueChange struct {
	issueID int64
	key     string
	changed []string
}

// changedNames turns the cache's per-issue report into the Changed* names, in contract order.
func changedNames(r cache.IssueApply) []string {
	out := []string{}
	add := func(on bool, name string) {
		if on {
			out = append(out, name)
		}
	}
	add(r.Status, ChangedStatus)
	add(r.Assignee, ChangedAssignee)
	add(r.Flag, ChangedFlag)
	add(r.Comments, ChangedComments)
	add(r.Description, ChangedDescription)
	add(r.Fields, ChangedFields)
	add(r.Attachments, ChangedAttachments)
	return out
}

// announcedNames is what a sync announces with ticket:updated for one hydrated issue. The first content an issue ever
// gets (a new issue, or an existing one whose content row did not exist yet, as after migration 0003) is not news to a
// reader of the ticket, so it announces nothing of its content; a status, assignee or flag change still counts. This
// keeps the first sync after an upgrade from emitting one event per issue.
func announcedNames(r cache.IssueApply) []string {
	if !r.Created && !r.FirstContent {
		return changedNames(r)
	}
	out := []string{}
	if r.Status {
		out = append(out, ChangedStatus)
	}
	if r.Assignee {
		out = append(out, ChangedAssignee)
	}
	if r.Flag {
		out = append(out, ChangedFlag)
	}
	return out
}

// contentPhase reads the field registry when the run hydrated anything and the source serves content. The adapter holds
// the registry from Hydrate, so this costs no request (jira-sync.md §2.2). A failure is not fatal: the issues are still
// stored, with a warning.
func (rn *runner) contentPhase(ctx context.Context, f *Fetched) error {
	if len(f.Details) == 0 || !rn.src.Capabilities().Content {
		return nil
	}
	defs, err := rn.src.Fields(ctx)
	switch {
	case err == nil:
		rn.fieldDefs = defs
	case ctx.Err() != nil:
		return ctx.Err()
	case source.IsUnsupported(err):
	default:
		f.Partial = true
		rn.warn(WarnContentPartial)
		rn.logf(ctx, "warn", source.PhaseContent, "the field list could not be read")
	}
	return nil
}

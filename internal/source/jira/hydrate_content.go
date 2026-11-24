package jira

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/crueber/cuisle/internal/model"
)

// Content of a hydrated issue (ADR 0015, jira-sync.md §2.2, §2.8): description, environment, attachment metadata,
// links, subtasks, parent, watchers and every other non-empty field value, plus a content hash. Comments are not part
// of the hash (they come from §2.10).

// contentSkipKeys are fields modelled on Issue or on IssueContent, or excluded as noise (jira-sync §2.2 list).
var contentSkipKeys = map[string]bool{
	"summary": true, "status": true, "assignee": true, "issuetype": true, "created": true, "updated": true,
	"resolutiondate": true, "parent": true, "comment": true, "attachment": true, "issuelinks": true, "subtasks": true,
	"description": true, "environment": true, "worklog": true, "watches": true, "votes": true, "lastViewed": true,
	"statuscategorychangedate": true, "workratio": true, "progress": true, "aggregateprogress": true,
}

// wireIssueRef is a short issue reference inside links, subtasks and the parent.
type wireIssueRef struct {
	ID     flexID `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary string `json:"summary"`
		Status  struct {
			ID flexID `json:"id"`
		} `json:"status"`
		IssueType struct {
			ID flexID `json:"id"`
		} `json:"issuetype"`
	} `json:"fields"`
}

func (r *wireIssueRef) toRef() model.IssueRef {
	return model.IssueRef{
		SourceID: string(r.ID), Key: r.Key, Summary: r.Fields.Summary,
		StatusID: string(r.Fields.Status.ID), TypeID: string(r.Fields.IssueType.ID),
	}
}

type wireAttachment struct {
	ID       flexID    `json:"id"`
	Filename string    `json:"filename"`
	MimeType string    `json:"mimeType"`
	Size     int64     `json:"size"`
	Created  Time      `json:"created"`
	Author   *wireUser `json:"author"`
}

type wireLink struct {
	ID   flexID `json:"id"`
	Type struct {
		Name    string `json:"name"`
		Inward  string `json:"inward"`
		Outward string `json:"outward"`
	} `json:"type"`
	InwardIssue  *wireIssueRef `json:"inwardIssue"`
	OutwardIssue *wireIssueRef `json:"outwardIssue"`
}

// buildContent decodes the content of one issue from its provider fields. Every field value goes through the registry;
// fields are emitted sorted by id so the result and its hash are deterministic.
func buildContent(d *model.IssueDetail, fields map[string]json.RawMessage, reg *fieldRegistry, f hydrateFieldIDs) (*model.IssueContent, error) {
	c := &model.IssueContent{Watchers: -1}
	c.Description = richOf(fields["description"])
	c.Environment = richOf(fields["environment"])

	var atts []wireAttachment
	if err := decodeField(fields, "attachment", &atts); err != nil {
		return nil, err
	}
	for _, wa := range atts {
		a := model.Attachment{ID: string(wa.ID), Name: wa.Filename, Mime: wa.MimeType, Bytes: wa.Size, CreatedAt: wa.Created.Time}
		if wa.Author != nil {
			a.AuthorID = wa.Author.AccountID
		}
		c.Attachments = append(c.Attachments, a)
	}

	var links []wireLink
	if err := decodeField(fields, "issuelinks", &links); err != nil {
		return nil, err
	}
	for _, l := range links {
		var other *wireIssueRef
		outward := false
		label := l.Type.Inward
		switch {
		case l.OutwardIssue != nil:
			other, outward, label = l.OutwardIssue, true, l.Type.Outward
		case l.InwardIssue != nil:
			other = l.InwardIssue
		default:
			continue
		}
		r := other.toRef()
		c.Links = append(c.Links, model.IssueLink{
			ID: string(l.ID), TypeName: l.Type.Name, Label: label, Outward: outward,
			OtherSourceID: r.SourceID, OtherKey: r.Key, OtherSummary: r.Summary, OtherStatusID: r.StatusID, OtherTypeID: r.TypeID,
		})
	}

	var subs []wireIssueRef
	if err := decodeField(fields, "subtasks", &subs); err != nil {
		return nil, err
	}
	for i := range subs {
		c.Subtasks = append(c.Subtasks, subs[i].toRef())
	}

	var parent *wireIssueRef
	if err := decodeField(fields, "parent", &parent); err != nil {
		return nil, err
	}
	if parent != nil {
		r := parent.toRef()
		c.Parent = &r
	}

	var watches struct {
		WatchCount *int `json:"watchCount"`
	}
	if raw, ok := fields["watches"]; ok && !isEmptyJSON(raw) {
		if err := json.Unmarshal(raw, &watches); err != nil {
			return nil, fmt.Errorf("decode field watches: %w", err)
		}
		if watches.WatchCount != nil {
			c.Watchers = *watches.WatchCount
		}
	}

	c.Fields = contentFields(d, fields, reg, f)
	c.Hash = contentHash(c)
	return c, nil
}

// richOf wraps a non-empty ADF value; null and empty values are nil.
func richOf(raw json.RawMessage) *model.RichText {
	if isEmptyJSON(raw) {
		return nil
	}
	return &model.RichText{Format: model.RichTextADF, Raw: raw}
}

// contentFields decodes every non-excluded field, in id order.
func contentFields(d *model.IssueDetail, fields map[string]json.RawMessage, reg *fieldRegistry, f hydrateFieldIDs) []model.FieldValue {
	ids := make([]string, 0, len(fields))
	for id := range fields {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []model.FieldValue
	for _, id := range ids {
		if contentSkipKeys[id] || (f.sprint != "" && id == f.sprint) || (f.rank != "" && id == f.rank) ||
			(f.flagged != "" && id == f.flagged) || reg.isDevSummary(id) {
			continue
		}
		kind := model.FieldUnknown
		if def, ok := reg.byID[id]; ok {
			kind = def.Kind
		}
		if fv, ok := decodeFieldValue(d, id, kind, fields[id]); ok {
			out = append(out, fv)
		}
	}
	return out
}

// contentHash is a SHA-256 of the content without comments. Identical provider data gives identical hashes.
func contentHash(c *model.IssueContent) string {
	cp := *c
	cp.Hash = ""
	b, err := json.Marshal(cp)
	if err != nil {
		// Unreachable for these types; a fixed marker keeps the hash non-empty and still changes with content.
		b = []byte(fmt.Sprintf("%+v", cp))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

package jira

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/crueber/cuisle/internal/model"
)

// Wire shapes for the Jira search response. Comment bodies are deliberately not decoded (jira-sync.md §7).

// flexID accepts a JSON string or number and keeps its text.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" {
		s = ""
	}
	*f = flexID(s)
	return nil
}

type wireIssue struct {
	ID     string                     `json:"id"`
	Key    string                     `json:"key"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type wireUser struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
	AccountType string `json:"accountType"`
}

type wireType struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Subtask        bool   `json:"subtask"`
	HierarchyLevel int    `json:"hierarchyLevel"`
}

type wireStatus struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	StatusCategory struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

type wireSprint struct {
	ID           flexID `json:"id"`
	Name         string `json:"name"`
	State        string `json:"state"`
	BoardID      flexID `json:"boardId"`
	StartDate    Time   `json:"startDate"`
	EndDate      Time   `json:"endDate"`
	CompleteDate Time   `json:"completeDate"`
}

type wireItem struct {
	FieldID string `json:"fieldId"`
	Field   string `json:"field"`
	From    any    `json:"from"`
	To      any    `json:"to"`
}

type wireHistory struct {
	ID      flexID     `json:"id"`
	Created Time       `json:"created"`
	Items   []wireItem `json:"items"`
}

// hydrateFieldIDs are the custom field ids from source meta (fetched by Configure, never by Hydrate).
type hydrateFieldIDs struct {
	sprint, rank, flagged string
}

func (a *Adapter) fieldIDs() hydrateFieldIDs {
	a.mu.Lock()
	defer a.mu.Unlock()
	return hydrateFieldIDs{sprint: a.meta.SprintFieldID, rank: a.meta.RankFieldID, flagged: a.meta.FlaggedFieldID}
}

// decodeField decodes one field when present and not null.
func decodeField(fields map[string]json.RawMessage, key string, out any) error {
	raw, ok := fields[key]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode field %s: %w", key, err)
	}
	return nil
}

// mapIssue turns a search or single-issue hit into an IssueDetail with its content and people (no changelog or
// comments yet; those are added by Adapter.complete). The comment key of the hit is ignored (jira-sync §2.10).
func mapIssue(wi wireIssue, f hydrateFieldIDs, reg *fieldRegistry) (model.IssueDetail, error) {
	var d model.IssueDetail
	d.SourceID, d.Key = wi.ID, wi.Key
	fs := wi.Fields
	if err := decodeField(fs, "summary", &d.Summary); err != nil {
		return d, err
	}
	var it *wireType
	if err := decodeField(fs, "issuetype", &it); err != nil {
		return d, err
	}
	if it != nil {
		d.TypeID = it.ID
		d.Type = model.IssueType{ID: it.ID, Name: it.Name, Kind: hydrateTypeKind(it.Name, it.Subtask, it.HierarchyLevel)}
	}
	var st *wireStatus
	if err := decodeField(fs, "status", &st); err != nil {
		return d, err
	}
	if st != nil {
		d.StatusID = st.ID
		d.Status = model.Status{ID: st.ID, Name: st.Name, Category: st.StatusCategory.Key, BlockedHint: hydrateBlockedHint(st.Name)}
	}
	var as *wireUser
	if err := decodeField(fs, "assignee", &as); err != nil {
		return d, err
	}
	if as != nil {
		p := personOf(as)
		d.AssigneeID = p.AccountID
		d.Assignee = &p
		addPerson(&d, p)
	}
	var created, updated, resolved Time
	for key, dst := range map[string]*Time{"created": &created, "updated": &updated, "resolutiondate": &resolved} {
		if err := decodeField(fs, key, dst); err != nil {
			return d, err
		}
	}
	d.CreatedAt, d.UpdatedAt, d.ResolvedAt = created.Time, updated.Time, resolved.Time

	var parent *struct {
		Key string `json:"key"`
	}
	if err := decodeField(fs, "parent", &parent); err != nil {
		return d, err
	}
	if parent != nil {
		d.ParentKey = parent.Key
	}
	var rank string
	if err := decodeField(fs, f.rank, &rank); err != nil {
		return d, err
	}
	d.Rank = rank

	var flags []json.RawMessage
	if err := decodeField(fs, f.flagged, &flags); err != nil {
		return d, err
	}
	d.Flagged = len(flags) > 0

	var sprints []wireSprint
	if err := decodeField(fs, f.sprint, &sprints); err != nil {
		return d, err
	}
	for _, s := range sprints {
		d.SprintIDs = append(d.SprintIDs, string(s.ID))
		d.Sprints = append(d.Sprints, model.Sprint{
			ID: string(s.ID), BoardID: string(s.BoardID), Name: s.Name, State: s.State,
			StartAt: s.StartDate.Time, EndAt: s.EndDate.Time, CompleteAt: s.CompleteDate.Time,
		})
	}

	c, err := buildContent(&d, fs, reg, f)
	if err != nil {
		return d, err
	}
	d.Content = c
	return d, nil
}

func personOf(u *wireUser) model.Person {
	return model.Person{AccountID: u.AccountID, DisplayName: u.DisplayName, Active: u.Active, Kind: personKind(u.AccountType)}
}

// personKind maps Jira accountType to model.Person.Kind.
func personKind(accountType string) string {
	switch accountType {
	case "app":
		return "app"
	case "customer":
		return "customer"
	default:
		return "user"
	}
}

// addPerson appends p to d.People once per account id.
func addPerson(d *model.IssueDetail, p model.Person) {
	if p.AccountID == "" {
		return
	}
	for _, q := range d.People {
		if q.AccountID == p.AccountID {
			return
		}
	}
	d.People = append(d.People, p)
}

// hydrateTypeKind maps a Jira issue type to model kinds: subtask → subtask; hierarchyLevel 1 → epic; else by name.
func hydrateTypeKind(name string, subtask bool, hierarchyLevel int) string {
	switch {
	case subtask:
		return "subtask"
	case hierarchyLevel == 1:
		return "epic"
	}
	switch l := strings.ToLower(name); l {
	case "story", "bug", "task", "spike":
		return l
	}
	return "other"
}

var blockedHintRe = regexp.MustCompile(`(?i)\b(blocked|impeded|on hold)\b`)

// hydrateBlockedHint reports whether a status name looks like a blocker.
func hydrateBlockedHint(name string) bool { return blockedHintRe.MatchString(name) }

// strOf renders a changelog from/to value: null is "", strings are kept.
func strOf(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

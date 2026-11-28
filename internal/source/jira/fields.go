package jira

// Field registry (jira-sync.md §2.2, ADR 0015): GET /rest/api/3/field entries become model.FieldDef rows with a neutral
// Kind. One registry per connection is held process-wide and shared by Fields, Hydrate and GetIssue. It is refreshed
// when older than registryTTL, and on the first use of an Adapter whose meta carries no fetch time (a full sync, see
// New in contract.go). Failures are never cached.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/crueber/cuisle/internal/model"
)

// registryTTL is how long a fetched registry is served from memory (jira-sync §2.2: once per site per 24 h).
const registryTTL = 24 * time.Hour

// Custom field types that denote a team (jira-sync §2.2 table).
const (
	teamCustomRM   = "com.atlassian.teams:rm-teams-custom-field-team"
	teamCustomAtl  = "com.atlassian.jira.plugin.system.customfieldtypes:atlassian-team"
	devSummarySufx = ":devsummarycf"
)

// fieldRegistry is the decoded field list of one connection.
type fieldRegistry struct {
	defs []model.FieldDef
	byID map[string]model.FieldDef
}

type registryEntry struct {
	reg *fieldRegistry
	at  time.Time
}

// registryCache holds one registry per connection key (see registryKey).
var registryCache = struct {
	mu sync.Mutex
	m  map[string]registryEntry
}{m: map[string]registryEntry{}}

// wireField is one entry of GET /rest/api/3/field.
type wireField struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Custom      bool            `json:"custom"`
	ClauseNames []string        `json:"clauseNames"`
	Schema      json.RawMessage `json:"schema"`
}

// fieldSchema is the part of a field's schema the kind table reads.
type fieldSchema struct {
	Type   string `json:"type"`
	Items  string `json:"items"`
	System string `json:"system"`
	Custom string `json:"custom"`
}

// Fields returns the field registry with neutral kinds (jira-sync.md §2.2), in response order.
func (a *Adapter) Fields(ctx context.Context) ([]model.FieldDef, error) {
	reg, err := a.registry(ctx)
	if err != nil {
		return nil, err
	}
	return append([]model.FieldDef(nil), reg.defs...), nil
}

// registryKey names the connection's cache entry: the normalised base URL plus the policy it is read with, so a test
// server that reuses a port or a test policy never inherits another connection's registry.
func (a *Adapter) registryKey() string {
	base := strings.ToLower(strings.TrimRight(strings.TrimSpace(a.conn.BaseURL), "/"))
	return fmt.Sprintf("%s|%p", base, a.c.pol)
}

// registry returns the connection's field registry, performing GET /rest/api/3/field only when it is not cached, is
// older than registryTTL, or this Adapter has no fetch time yet (a full sync).
func (a *Adapter) registry(ctx context.Context) (*fieldRegistry, error) {
	key := a.registryKey()
	now := a.now()
	a.mu.Lock()
	force := a.meta.FieldsFetchedAt.IsZero()
	a.mu.Unlock()

	registryCache.mu.Lock()
	e, ok := registryCache.m[key]
	registryCache.mu.Unlock()
	if ok && !force && now.Sub(e.at) < registryTTL {
		return e.reg, nil
	}
	return a.loadFields(ctx, false)
}

// loadFields fetches GET /rest/api/3/field once, updates the connection's field registry cache, and (when updateMeta
// is true, i.e. discoverFields) updates a.meta's discovered field IDs (Sprint, Rank, Flagged, DevSummary).
func (a *Adapter) loadFields(ctx context.Context, updateMeta bool) (*fieldRegistry, error) {
	var raw []wireField
	if err := a.c.GetJSON(ctx, "/rest/api/3/field", nil, &raw); err != nil {
		return nil, MapError(err)
	}
	reg := buildRegistry(raw)
	now := a.now()

	registryCache.mu.Lock()
	registryCache.m[a.registryKey()] = registryEntry{reg: reg, at: now}
	registryCache.mu.Unlock()

	if updateMeta {
		refs := make([]refField, 0, len(raw))
		for _, e := range raw {
			rf := refField{ID: e.ID, Name: e.Name, Custom: e.Custom, ClauseNames: e.ClauseNames}
			if d, ok := reg.byID[e.ID]; ok {
				rf.Schema.Custom = d.CustomType
			}
			refs = append(refs, rf)
		}
		sprint, rank, flagged := refFindFields(refs)
		devSummary := refFindDevSummary(refs)

		a.mu.Lock()
		a.meta.SprintFieldID = sprint
		a.meta.RankFieldID = rank
		a.meta.FlaggedFieldID = flagged
		a.meta.DevSummaryFieldID = devSummary
		a.meta.DevFieldChecked = true
		a.meta.FieldsFetchedAt = now
		a.mu.Unlock()
	} else {
		a.mu.Lock()
		a.meta.FieldsFetchedAt = now
		a.mu.Unlock()
	}
	return reg, nil
}

// buildRegistry turns the response into one FieldDef per entry with an id, in response order.
func buildRegistry(entries []wireField) *fieldRegistry {
	reg := &fieldRegistry{byID: map[string]model.FieldDef{}}
	for _, e := range entries {
		if e.ID == "" {
			continue
		}
		var s fieldSchema
		if len(e.Schema) > 0 {
			_ = json.Unmarshal(e.Schema, &s) // a schema that is not an object is treated as having no schema
		}
		var schema json.RawMessage
		if len(e.Schema) > 0 && string(e.Schema) != "null" {
			schema = e.Schema
		}
		d := model.FieldDef{
			ID: e.ID, Name: e.Name, Kind: fieldKindOf(s), Custom: e.Custom,
			CustomType: s.Custom, Schema: schema,
		}
		reg.defs = append(reg.defs, d)
		if _, dup := reg.byID[d.ID]; !dup {
			reg.byID[d.ID] = d
		}
	}
	return reg
}

// fieldKindOf maps a schema to a neutral kind; the first matching rule of the jira-sync §2.2 table wins.
func fieldKindOf(s fieldSchema) string {
	if s.Type == "team" || s.Custom == teamCustomRM || s.Custom == teamCustomAtl {
		return model.FieldTeam
	}
	switch s.Type {
	case "string":
		switch {
		case hasSuffix(s.Custom, ":textarea") || s.System == "description" || s.System == "environment":
			return model.FieldParagraph
		case hasSuffix(s.Custom, ":url"):
			return model.FieldURL
		default:
			return model.FieldText
		}
	case "number":
		return model.FieldNumber
	case "date":
		return model.FieldDate
	case "datetime":
		return model.FieldDateTime
	case "option":
		return model.FieldOption
	case "option-with-child":
		return model.FieldCascade
	case "user":
		return model.FieldUser
	case "group":
		return model.FieldGroup
	case "version":
		return model.FieldVersion
	case "project":
		return model.FieldProject
	case "priority", "resolution", "issuetype", "status", "securitylevel":
		return model.FieldOption
	case "array":
		return fieldArrayKind(s)
	}
	return model.FieldUnknown
}

// fieldArrayKind maps an array schema by its item type.
func fieldArrayKind(s fieldSchema) string {
	switch s.Items {
	case "option":
		return model.FieldOptions
	case "user":
		return model.FieldUsers
	case "group":
		return model.FieldGroups
	case "version":
		return model.FieldVersions
	case "component":
		return model.FieldComponents
	case "string":
		if s.System == "labels" || hasSuffix(s.Custom, ":labels") {
			return model.FieldLabels
		}
	}
	return model.FieldUnknown
}

func hasSuffix(s, suffix string) bool { return suffix != "" && strings.HasSuffix(s, suffix) }

// isDevSummary reports whether a registry entry is a development-summary custom field (excluded from content).
func (r *fieldRegistry) isDevSummary(id string) bool {
	d, ok := r.byID[id]
	return ok && hasSuffix(d.CustomType, devSummarySufx)
}

package jira

import (
	"context"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/crueber/cuisle/internal/apperr"
	"github.com/crueber/cuisle/internal/model"
)

// refBoardCap is the most boards an unfiltered SearchBoards reads (jira-sync.md §2.4).
const refBoardCap = 200

var (
	refOrderByRe     = regexp.MustCompile(`(?i)\border\s+by\b`)
	refBlockedHintRe = regexp.MustCompile(`(?i)\b(blocked|impeded|on hold)\b`)
)

// Test: GET /rest/api/3/myself (jira-sync.md §2.1).
func (a *Adapter) Test(ctx context.Context) (model.Account, error) {
	var me struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	}
	if err := a.c.GetJSON(ctx, "/rest/api/3/myself", nil, &me); err != nil {
		return model.Account{}, MapError(err)
	}
	return model.Account{ID: me.AccountID, DisplayName: me.DisplayName}, nil
}

// SearchSpaces: GET /rest/api/3/project/search (§2.3).
func (a *Adapter) SearchSpaces(ctx context.Context, query string) ([]model.Space, error) {
	ctx = WithInteractive(ctx) // Interactive lane (jira-sync §3.1): the user is waiting
	q := url.Values{"maxResults": {"20"}, "orderBy": {"name"}, "typeKey": {"software"}, "status": {"live"}}
	if query != "" {
		q.Set("query", query)
	}
	var page refProjectPage
	if err := a.c.GetJSON(ctx, "/rest/api/3/project/search", q, &page); err != nil {
		return nil, MapError(err)
	}
	out := make([]model.Space, 0, len(page.Values))
	for _, p := range page.Values {
		out = append(out, model.Space{
			ID: string(p.ID), Key: p.Key, Name: p.Name, Meta: map[string]any{"style": p.Style},
		})
	}
	return out, nil
}

// SearchBoards: GET /rest/agile/1.0/board (§2.4). Board.Kind is normalised (simple → scrum/kanban via sprint probe).
// Paging happens only without a name, stops on isLast, an empty page, or after refBoardCap boards.
func (a *Adapter) SearchBoards(ctx context.Context, spaceKey, query string) ([]model.Board, error) {
	ctx = WithInteractive(ctx) // Interactive lane (jira-sync §3.1): the user is waiting
	out := []model.Board{}
	startAt := 0
	for {
		q := url.Values{"projectKeyOrId": {spaceKey}, "maxResults": {"50"}, "orderBy": {"name"}}
		if query != "" {
			q.Set("name", query)
		} else if startAt > 0 {
			q.Set("startAt", strconv.Itoa(startAt))
		}
		var page refBoardPage
		if err := a.c.GetJSON(ctx, "/rest/agile/1.0/board", q, &page); err != nil {
			return nil, MapError(err)
		}
		for _, b := range page.Values {
			kind, err := a.boardKind(ctx, string(b.ID), b.Type)
			if err != nil {
				return nil, err
			}
			out = append(out, model.Board{ID: string(b.ID), Name: b.Name, Kind: kind, RawType: b.Type})
		}
		startAt += len(page.Values)
		if query != "" || page.IsLast || len(page.Values) == 0 || len(out) >= refBoardCap {
			break
		}
	}
	return out, nil
}

// BoardConfig: GET /rest/agile/1.0/board/{B}/configuration (§2.5). Board.Meta gets filterId, subQuery, rankFieldId.
func (a *Adapter) BoardConfig(ctx context.Context, boardID string) (model.BoardConfig, error) {
	var cfg refBoardConfig
	if err := a.c.GetJSON(ctx, refBoardPath(boardID)+"/configuration", nil, &cfg); err != nil {
		return model.BoardConfig{}, MapError(err)
	}
	kind, err := a.boardKind(ctx, boardID, cfg.Type)
	if err != nil {
		return model.BoardConfig{}, err
	}
	meta := map[string]any{}
	if cfg.Filter != nil && cfg.Filter.ID != "" {
		meta["filterId"] = string(cfg.Filter.ID)
	}
	if cfg.SubQuery != nil && cfg.SubQuery.Query != "" {
		meta["subQuery"] = cfg.SubQuery.Query
	}
	if cfg.Ranking != nil {
		if f := refFieldID(cfg.Ranking.RankCustomFieldID); f != "" {
			meta["rankFieldId"] = f
		}
	}
	cols := make([]model.Column, 0, len(cfg.ColumnConfig.Columns))
	for i, c := range cfg.ColumnConfig.Columns {
		col := model.Column{Name: c.Name, Position: i}
		for _, s := range c.Statuses {
			col.StatusIDs = append(col.StatusIDs, string(s.ID))
		}
		cols = append(cols, col)
	}
	return model.BoardConfig{
		Board:   model.Board{ID: boardID, Name: cfg.Name, Kind: kind, RawType: cfg.Type, Meta: meta},
		Columns: cols,
	}, nil
}

// ActiveSprints: GET /rest/agile/1.0/board/{B}/sprint?state=active (§2.7); 400/404 → empty, nil (kanban).
func (a *Adapter) ActiveSprints(ctx context.Context, boardID string) ([]model.Sprint, error) {
	q := url.Values{"state": {"active"}, "maxResults": {"50"}}
	var page refSprintPage
	if err := a.c.GetJSON(ctx, refBoardPath(boardID)+"/sprint", q, &page); err != nil {
		if refNoSprints(err) {
			return nil, nil
		}
		return nil, MapError(err)
	}
	return refSprintsFromWire(boardID, page.Values), nil
}

// Reference: GET /rest/api/3/field (field ids into Meta, when stale > 24 h or never) and GET /rest/api/3/status (§2.2,
// §2.6). Issue types are discovered during Hydrate; Reference returns the types seen so far (may be empty).
func (a *Adapter) Reference(ctx context.Context) ([]model.Status, []model.IssueType, error) {
	var raw []refStatus
	if err := a.c.GetJSON(ctx, "/rest/api/3/status", nil, &raw); err != nil {
		return nil, nil, MapError(err)
	}
	statuses := make([]model.Status, 0, len(raw))
	for _, s := range raw {
		statuses = append(statuses, model.Status{
			ID: string(s.ID), Name: s.Name, Category: s.StatusCategory.Key, BlockedHint: StatusBlockedHint(s.Name),
		})
	}

	a.mu.Lock()
	stale := refStale(a.meta.FieldsFetchedAt, a.now())
	a.mu.Unlock()
	if stale {
		if err := a.discoverFields(ctx); err != nil {
			return nil, nil, err
		}
	}

	a.mu.Lock()
	types := make([]model.IssueType, 0, len(a.types))
	for _, t := range a.types {
		types = append(types, t)
	}
	a.mu.Unlock()
	sort.Slice(types, func(i, j int) bool { return types[i].ID < types[j].ID })
	return statuses, types, nil
}

// discoverFields reads GET /rest/api/3/field once and stores both the field registry and the discovered field ids.
func (a *Adapter) discoverFields(ctx context.Context) error {
	_, err := a.loadFields(ctx, true)
	return err
}

// SearchUsers: GET /rest/api/3/user/assignable/search (§2.12); keeps active atlassian accounts (Kind "user").
func (a *Adapter) SearchUsers(ctx context.Context, spaceKey, query string) ([]model.Person, error) {
	ctx = WithInteractive(ctx) // Interactive lane (jira-sync §3.1): the user is waiting
	q := url.Values{"project": {spaceKey}, "maxResults": {"20"}}
	if query != "" {
		q.Set("query", query)
	}
	var users []refUser
	if err := a.c.GetJSON(ctx, "/rest/api/3/user/assignable/search", q, &users); err != nil {
		return nil, MapError(err)
	}
	out := []model.Person{}
	for _, u := range users {
		if u.Active && u.AccountType == "atlassian" {
			out = append(out, model.Person{AccountID: u.AccountID, DisplayName: u.DisplayName, Active: true, Kind: "user"})
		}
	}
	return out, nil
}

// ValidateFilter: POST /rest/api/3/jql/parse?validation=strict (§2.8). ORDER BY → ok=false locally, no request.
func (a *Adapter) ValidateFilter(ctx context.Context, expr string) (bool, []string, error) {
	ctx = WithInteractive(ctx) // Interactive lane (jira-sync §3.1): the user is waiting
	if refOrderByRe.MatchString(expr) {
		return false, []string{"Remove ORDER BY: Cuisle orders the tickets itself."}, nil
	}
	var resp refJQLResponse
	body := refJQLRequest{Queries: []string{expr}}
	if err := a.c.PostJSON(ctx, "/rest/api/3/jql/parse", url.Values{"validation": {"strict"}}, body, &resp); err != nil {
		return false, nil, MapError(err)
	}
	if len(resp.Queries) == 0 {
		return false, nil, apperr.New(apperr.SourceHTTP, "Jira returned an unexpected response.")
	}
	errs := resp.Queries[0].Errors
	return len(errs) == 0, errs, nil
}

// boardKind normalises a Jira board type. "simple" boards are probed: a sprint endpoint that answers gives scrum,
// 400/404 gives kanban.
func (a *Adapter) boardKind(ctx context.Context, boardID, rawType string) (string, error) {
	if rawType != "simple" {
		return rawType, nil
	}
	var out struct{}
	err := a.c.GetJSON(ctx, refBoardPath(boardID)+"/sprint", url.Values{"maxResults": {"1"}}, &out)
	switch {
	case err == nil:
		return "scrum", nil
	case refNoSprints(err):
		return "kanban", nil
	default:
		return "", MapError(err)
	}
}

// IssueTypeKind maps a Jira issue type to model kinds: subtask → subtask; hierarchyLevel 1 → epic; else by lower(name)
// story|bug|task|spike; else other.
func IssueTypeKind(name string, subtask bool, hierarchyLevel int) string {
	switch {
	case subtask:
		return "subtask"
	case hierarchyLevel == 1:
		return "epic"
	}
	if l := strings.ToLower(name); slices.Contains([]string{"story", "bug", "task", "spike"}, l) {
		return l
	}
	return "other"
}

// StatusBlockedHint reports whether a status name matches (?i)\b(blocked|impeded|on hold)\b.
func StatusBlockedHint(name string) bool { return refBlockedHintRe.MatchString(name) }

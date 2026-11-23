package jira

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/crueber/cuisle/internal/model"
)

// refID is a Jira id that arrives either as a JSON string ("10001") or a JSON number (42). It keeps the text.
type refID string

func (r *refID) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*r = ""
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return fmt.Errorf("jira id: %w", err)
		}
		*r = refID(v)
	case s != "" && (s[0] == '-' || (s[0] >= '0' && s[0] <= '9')):
		*r = refID(s)
	default:
		return fmt.Errorf("jira id: unsupported JSON value")
	}
	return nil
}

// Wire shapes for the endpoints in endpoints_ref.go (only the fields the adapter reads).

type refProject struct {
	ID    refID  `json:"id"`
	Key   string `json:"key"`
	Name  string `json:"name"`
	Style string `json:"style"`
}

type refProjectPage struct {
	Values []refProject `json:"values"`
	IsLast bool         `json:"isLast"`
}

type refBoard struct {
	ID   refID  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

type refBoardPage struct {
	Values []refBoard `json:"values"`
	IsLast bool       `json:"isLast"`
}

type refBoardConfig struct {
	ID     refID  `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Filter *struct {
		ID refID `json:"id"`
	} `json:"filter"`
	SubQuery *struct {
		Query string `json:"query"`
	} `json:"subQuery"`
	ColumnConfig struct {
		Columns []struct {
			Name     string `json:"name"`
			Statuses []struct {
				ID refID `json:"id"`
			} `json:"statuses"`
		} `json:"columns"`
	} `json:"columnConfig"`
	Ranking *struct {
		RankCustomFieldID refID `json:"rankCustomFieldId"`
	} `json:"ranking"`
}

type refSprint struct {
	ID           refID  `json:"id"`
	Name         string `json:"name"`
	State        string `json:"state"`
	StartDate    Time   `json:"startDate"`
	EndDate      Time   `json:"endDate"`
	CompleteDate Time   `json:"completeDate"`
}

type refSprintPage struct {
	Values []refSprint `json:"values"`
}

type refField struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Custom      bool     `json:"custom"`
	ClauseNames []string `json:"clauseNames"`
	Schema      struct {
		Custom string `json:"custom"`
	} `json:"schema"`
}

type refStatus struct {
	ID             refID  `json:"id"`
	Name           string `json:"name"`
	StatusCategory struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

type refUser struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
	AccountType string `json:"accountType"`
}

type refJQLRequest struct {
	Queries []string `json:"queries"`
}

type refJQLResponse struct {
	Queries []struct {
		Errors []string `json:"errors"`
	} `json:"queries"`
}

// refBoardPath is "/rest/agile/1.0/board/{id}".
func refBoardPath(boardID string) string {
	return "/rest/agile/1.0/board/" + url.PathEscape(boardID)
}

// refNoSprints reports a 400 or 404 from a sprint endpoint: the board does not support sprints (kanban).
func refNoSprints(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && (he.Status == 400 || he.Status == 404)
}

// refFieldID turns Jira's rankCustomFieldId into a field id: the number 10019 becomes "customfield_10019"; a value that
// already has the prefix is kept.
func refFieldID(id refID) string {
	s := string(id)
	if s == "" {
		return ""
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return s
		}
	}
	return "customfield_" + s
}

// refFindFields picks the Sprint, Rank and Flagged field ids. Sprint and Rank match their Greenhopper schema first,
// then a custom field with the plain name. Flagged matches by name first, then by clause name.
func refFindFields(fields []refField) (sprint, rank, flagged string) {
	for _, f := range fields {
		if sprint == "" && strings.HasSuffix(f.Schema.Custom, "gh-sprint") {
			sprint = f.ID
		}
		if rank == "" && strings.HasSuffix(f.Schema.Custom, "gh-lexo-rank") {
			rank = f.ID
		}
	}
	for _, f := range fields {
		if sprint == "" && f.Custom && f.Name == "Sprint" {
			sprint = f.ID
		}
		if rank == "" && f.Custom && f.Name == "Rank" {
			rank = f.ID
		}
	}
	for _, f := range fields {
		if f.Name == "Flagged" {
			flagged = f.ID
			break
		}
	}
	if flagged == "" {
		for _, f := range fields {
			for _, c := range f.ClauseNames {
				if c == "Flagged" {
					flagged = f.ID
				}
			}
			if flagged != "" {
				break
			}
		}
	}
	return sprint, rank, flagged
}

// refSprintsFromWire maps active sprints, converting every timestamp to UTC.
func refSprintsFromWire(boardID string, in []refSprint) []model.Sprint {
	out := make([]model.Sprint, 0, len(in))
	for _, s := range in {
		out = append(out, model.Sprint{
			ID: string(s.ID), BoardID: boardID, Name: s.Name, State: s.State,
			StartAt: s.StartDate.Time, EndAt: s.EndDate.Time, CompleteAt: s.CompleteDate.Time,
		})
	}
	return out
}

// refStale reports whether the field metadata must be fetched again (never fetched, or older than 24 h).
func refStale(fetchedAt, now time.Time) bool {
	return fetchedAt.IsZero() || now.Sub(fetchedAt) > refFieldsTTL
}

const refFieldsTTL = 24 * time.Hour

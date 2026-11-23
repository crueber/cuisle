package jira

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
)

// WP-38: a transport error's Detail must never carry the JQL (or other search text).
func TestMapError_DetailRedactsJQLAndQueryValues(t *testing.T) {
	const jql = `project = MOB AND assignee = "Jo Q" ORDER BY rank`
	raw := "https://acme.atlassian.net/rest/api/3/search/jql?" + url.Values{"jql": {jql}, "maxResults": {"50"}}.Encode()
	rawQuery := "https://acme.atlassian.net/rest/api/3/project/search?" + url.Values{"query": {"secret term"}, "typeKey": {"software"}}.Encode()
	tests := []struct {
		name string
		err  error
		not  []string
		keep []string
	}{
		{"jql in a url.Error", &url.Error{Op: "Get", URL: raw, Err: context.DeadlineExceeded},
			[]string{jql, url.QueryEscape(jql), "Jo+Q", "ORDER"}, []string{"/rest/api/3/search/jql", "maxResults=50", "jql=[redacted]"}},
		{"query in a url.Error", &url.Error{Op: "Get", URL: rawQuery, Err: errors.New("dial tcp: lookup acme.atlassian.net: no such host")},
			[]string{"secret", "term"}, []string{"/rest/api/3/project/search", "typeKey=software", "query=[redacted]", "no such host"}},
		{"jql last in the url", &url.Error{Op: "Get", URL: "https://h/rest/api/3/search/jql?maxResults=1&jql=" + url.QueryEscape(jql),
			Err: errors.New("EOF")}, []string{"MOB", "assignee"}, []string{"maxResults=1", "jql=[redacted]"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var ae *apperr.Error
			if !errors.As(MapError(tc.err), &ae) {
				t.Fatalf("MapError(%v) is not an *apperr.Error", tc.err)
			}
			for _, bad := range tc.not {
				if strings.Contains(ae.Detail, bad) {
					t.Errorf("Detail %q contains %q", ae.Detail, bad)
				}
			}
			for _, good := range tc.keep {
				if !strings.Contains(ae.Detail, good) {
					t.Errorf("Detail %q lost %q", ae.Detail, good)
				}
			}
		})
	}
}

func TestMapError_DetailWithoutQueryIsUntouched(t *testing.T) {
	he := &HTTPError{Method: "GET", Path: "/rest/api/3/myself", Status: 500}
	var ae *apperr.Error
	if !errors.As(MapError(he), &ae) || ae.Detail != he.Error() {
		t.Errorf("Detail = %q, want %q", ae.Detail, he.Error())
	}
}

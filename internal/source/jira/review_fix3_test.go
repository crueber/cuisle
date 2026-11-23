package jira

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
)

// WP-38B: the board-search text (the name parameter) is redacted from a transport error's Detail like jql and query.
func TestMapError_DetailRedactsBoardName(t *testing.T) {
	raw := "https://h/rest/agile/1.0/board?" + url.Values{"name": {"secret board"}, "type": {"scrum"}}.Encode()
	var ae *apperr.Error
	if !errors.As(MapError(&url.Error{Op: "Get", URL: raw, Err: errors.New("EOF")}), &ae) {
		t.Fatalf("not an apperr")
	}
	for _, bad := range []string{"secret", "board+"} {
		if strings.Contains(ae.Detail, bad) {
			t.Errorf("detail %q leaks %q", ae.Detail, bad)
		}
	}
	for _, keep := range []string{"type=scrum", "name=[redacted]"} {
		if !strings.Contains(ae.Detail, keep) {
			t.Errorf("detail %q lost %q", ae.Detail, keep)
		}
	}
}

// WP-38B (found driving the real stack): an https request to a plain-http server is an address problem, not internal.
func TestMapError_HTTPSToHTTPServerIsOffline(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://127.0.0.1:1/rest/api/3/myself", Err: errors.New("http: server gave HTTP response to HTTPS client")}
	if code := apperr.CodeOf(MapError(err)); code != apperr.Offline {
		t.Fatalf("code = %q, want offline", code)
	}
}

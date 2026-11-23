package jira

// WP-06 tests for errors.go: MapError (jira-sync.md §3, doc comment on MapError).
//
// SPEC-NOTE: 403 "forbidden" message "names the resource from the path": only pinned as non-empty and, for a
// /board/ path, containing "board" (case-insensitive).
// SPEC-NOTE: 404 on a board configuration path or a filter path carries a message containing exactly
// "This board no longer exists" (Contains, so a trailing period is fine). Other 404s are not_found with a
// different message.
// SPEC-NOTE: 400 on a /search path → filter_invalid whose Message contains Jira's FIRST message and none of the
// later ones. 400 elsewhere is plain source_http (not retryable).
// SPEC-NOTE: MapError(nil) == nil; an *apperr.Error passes through with code and message unchanged; any other
// unrecognised error becomes apperr code internal (same as apperr.From). Wrapping (*url.Error, %w) is seen through.
// SPEC-NOTE: Only error CODES are asserted for transport failures, never OS error strings (CI also runs Windows).
// SPEC-NOTE: Retryable is pinned true for 5xx source_http, false for other-4xx source_http.

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/crueber/cuisle/internal/apperr"
)

func wp06AsApp(t *testing.T, err error) *apperr.Error {
	t.Helper()
	if err == nil {
		t.Fatal("MapError returned nil for a non-nil error")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("MapError returned %T (%v), want *apperr.Error", err, err)
	}
	return ae
}

func TestMapError_HTTPStatusTable(t *testing.T) {
	const (
		myself = "/rest/api/3/myself"
		boardC = "/rest/agile/1.0/board/12/configuration"
		filter = "/rest/api/3/filter/10000"
		search = "/rest/api/3/search/jql"
		issue  = "/rest/api/3/issue/ABC-1"
	)
	tests := []struct {
		name      string
		err       *HTTPError
		code      apperr.Code
		retryable *bool // nil = not asserted
		contains  []string
		excludes  []string
	}{
		{name: "401 anywhere", err: &HTTPError{Status: 401, Method: "GET", Path: issue}, code: apperr.AuthInvalid},
		{name: "401 on myself", err: &HTTPError{Status: 401, Method: "GET", Path: myself}, code: apperr.AuthInvalid},
		{name: "403 with AUTHENTICATED_FAILED", err: &HTTPError{Status: 403, Method: "GET", Path: issue, LoginReason: "AUTHENTICATED_FAILED"}, code: apperr.AuthInvalid},
		{name: "403 on myself", err: &HTTPError{Status: 403, Method: "GET", Path: myself}, code: apperr.AuthInvalid},
		{name: "403 on board names the board", err: &HTTPError{Status: 403, Method: "GET", Path: boardC}, code: apperr.Forbidden},
		{name: "403 elsewhere", err: &HTTPError{Status: 403, Method: "GET", Path: issue}, code: apperr.Forbidden},
		{name: "404 board configuration", err: &HTTPError{Status: 404, Method: "GET", Path: boardC}, code: apperr.NotFound, contains: []string{"This board no longer exists"}},
		{name: "404 filter", err: &HTTPError{Status: 404, Method: "GET", Path: filter}, code: apperr.NotFound, contains: []string{"This board no longer exists"}},
		{name: "404 other", err: &HTTPError{Status: 404, Method: "GET", Path: issue}, code: apperr.NotFound, excludes: []string{"This board no longer exists"}},
		{
			name: "400 on search carries the first Jira message",
			err: &HTTPError{Status: 400, Method: "POST", Path: search,
				Messages: []string{"Error in the JQL Query: The operator '~' is not supported.", "a second message"}},
			code:     apperr.FilterInvalid,
			contains: []string{"Error in the JQL Query: The operator '~' is not supported."},
			excludes: []string{"a second message"},
		},
		{name: "400 elsewhere is plain source_http", err: &HTTPError{Status: 400, Method: "GET", Path: issue}, code: apperr.SourceHTTP, retryable: wp06Bool(false)},
		{name: "429 after retries", err: &HTTPError{Status: 429, Method: "GET", Path: issue}, code: apperr.RateLimited},
		{name: "500", err: &HTTPError{Status: 500, Method: "GET", Path: issue}, code: apperr.SourceHTTP, retryable: wp06Bool(true)},
		{name: "502", err: &HTTPError{Status: 502, Method: "GET", Path: issue}, code: apperr.SourceHTTP, retryable: wp06Bool(true)},
		{name: "503", err: &HTTPError{Status: 503, Method: "POST", Path: search}, code: apperr.SourceHTTP, retryable: wp06Bool(true)},
		{name: "504", err: &HTTPError{Status: 504, Method: "GET", Path: issue}, code: apperr.SourceHTTP, retryable: wp06Bool(true)},
		{name: "418", err: &HTTPError{Status: 418, Method: "GET", Path: issue}, code: apperr.SourceHTTP, retryable: wp06Bool(false)},
		{name: "409", err: &HTTPError{Status: 409, Method: "GET", Path: issue}, code: apperr.SourceHTTP, retryable: wp06Bool(false)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ae := wp06AsApp(t, MapError(tc.err))
			if ae.Code != tc.code {
				t.Fatalf("code = %q, want %q (message %q)", ae.Code, tc.code, ae.Message)
			}
			if strings.TrimSpace(ae.Message) == "" || strings.Contains(ae.Message, "\n") {
				t.Fatalf("message %q must be one non-empty line", ae.Message)
			}
			if tc.retryable != nil && ae.Retryable != *tc.retryable {
				t.Fatalf("retryable = %v, want %v", ae.Retryable, *tc.retryable)
			}
			for _, s := range tc.contains {
				if !strings.Contains(ae.Message, s) {
					t.Fatalf("message %q lacks %q", ae.Message, s)
				}
			}
			for _, s := range tc.excludes {
				if strings.Contains(ae.Message, s) {
					t.Fatalf("message %q must not contain %q", ae.Message, s)
				}
			}
		})
	}
}

func wp06Bool(b bool) *bool { return &b }

func TestMapError_Forbidden_BoardMessageNamesBoard(t *testing.T) {
	ae := wp06AsApp(t, MapError(&HTTPError{Status: 403, Method: "GET", Path: "/rest/agile/1.0/board/12/configuration"}))
	if ae.Code != apperr.Forbidden {
		t.Fatalf("code = %q", ae.Code)
	}
	if !strings.Contains(strings.ToLower(ae.Message), "board") {
		t.Fatalf("message %q does not name the board", ae.Message)
	}
}

func TestMapError_HTTPErrorSeenThroughWrapping(t *testing.T) {
	base := &HTTPError{Status: 401, Method: "GET", Path: "/rest/api/3/myself"}
	wrapped := []error{
		fmt.Errorf("sync: %w", base),
		fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", base)),
	}
	for _, err := range wrapped {
		if got := apperr.CodeOf(MapError(err)); got != apperr.AuthInvalid {
			t.Fatalf("MapError(%v) code = %q, want auth_invalid", err, got)
		}
	}
}

type wp06TimeoutErr struct{}

func (wp06TimeoutErr) Error() string   { return "i/o timeout" }
func (wp06TimeoutErr) Timeout() bool   { return true }
func (wp06TimeoutErr) Temporary() bool { return true }

func TestMapError_TransportAndContextTable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code apperr.Code
	}{
		{"DNS failure", &net.DNSError{Err: "no such host", Name: "acme.atlassian.net", IsNotFound: true}, apperr.Offline},
		{"DNS failure wrapped by url.Error", &url.Error{Op: "Get", URL: "https://acme.atlassian.net/rest/api/3/myself",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "acme.atlassian.net", IsNotFound: true}}}, apperr.Offline},
		{"connection refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, apperr.Offline},
		{"connection refused wrapped", fmt.Errorf("get: %w", &url.Error{Op: "Get", URL: "http://127.0.0.1:1/x",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}), apperr.Offline},
		{"no route to host", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.EHOSTUNREACH}, apperr.Offline},
		{"network unreachable", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ENETUNREACH}, apperr.Offline},
		{"TLS unknown authority", &url.Error{Op: "Get", URL: "https://acme.atlassian.net/", Err: x509.UnknownAuthorityError{}}, apperr.Offline},
		{"deadline exceeded", context.DeadlineExceeded, apperr.Timeout},
		{"deadline exceeded wrapped by url.Error", &url.Error{Op: "Get", URL: "https://acme.atlassian.net/", Err: context.DeadlineExceeded}, apperr.Timeout},
		{"deadline exceeded wrapped with %w", fmt.Errorf("scan: %w", context.DeadlineExceeded), apperr.Timeout},
		{"net timeout", &net.OpError{Op: "read", Net: "tcp", Err: wp06TimeoutErr{}}, apperr.Timeout},
		{"canceled", context.Canceled, apperr.Cancelled},
		{"canceled wrapped by url.Error", &url.Error{Op: "Get", URL: "https://acme.atlassian.net/", Err: context.Canceled}, apperr.Cancelled},
		{"canceled wrapped with %w", fmt.Errorf("scan: %w", context.Canceled), apperr.Cancelled},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ae := wp06AsApp(t, MapError(tc.err))
			if ae.Code != tc.code {
				t.Fatalf("code = %q, want %q", ae.Code, tc.code)
			}
			if strings.TrimSpace(ae.Message) == "" {
				t.Fatal("empty message")
			}
		})
	}
}

func TestMapError_NilPassthroughAndUnknown(t *testing.T) {
	if got := MapError(nil); got != nil {
		t.Fatalf("MapError(nil) = %v, want nil", got)
	}
	orig := apperr.New(apperr.Validation, "Already typed.")
	ae := wp06AsApp(t, MapError(orig))
	if ae.Code != apperr.Validation || ae.Message != "Already typed." {
		t.Fatalf("typed error changed: %+v", ae)
	}
	unk := wp06AsApp(t, MapError(errors.New("something odd")))
	if unk.Code != apperr.Internal {
		t.Fatalf("unknown error code = %q, want internal", unk.Code)
	}
	if strings.Contains(unk.Message, "something odd") {
		t.Fatalf("message %q leaks the raw error text (belongs in Detail)", unk.Message)
	}
}

func TestMapError_DetailMayNameStatusAndPathButNeverHoldsABody(t *testing.T) {
	e := &HTTPError{Status: 503, Method: "GET", Path: "/rest/api/3/issue/ABC-1"}
	ae := wp06AsApp(t, MapError(e))
	// Detail is optional, but if present it must not exceed what HTTPError carries.
	if strings.Contains(ae.Detail, "Authorization") || strings.Contains(ae.Detail, "Basic ") {
		t.Fatalf("detail %q leaks credentials", ae.Detail)
	}
}

package jira

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"regexp"
	"strings"
	"syscall"

	"github.com/crueber/cuisle/internal/apperr"
)

// MapError converts a client error into *apperr.Error per jira-sync.md §3:
//
//	401, or 403 with LoginReason AUTHENTICATED_FAILED, or any 403 on /rest/api/3/myself → auth_invalid
//	other 403 → forbidden (message names the resource from the path)
//	404 on a board/filter path → not_found "This board no longer exists"; other 404 → not_found
//	400 on /search → filter_invalid with Jira's first message
//	429 after retries / gate budget exceeded → rate_limited
//	other HTTP → source_http (retryable for 5xx)
//	DNS / no route / TLS / connection refused before a response → offline
//	context.DeadlineExceeded / net timeout → timeout; context.Canceled → cancelled
//
// Messages never include tokens or raw bodies; Detail may include status and path, but never the JQL or search text:
// the values of jql, query and name parameters are redacted from the url.Error text a transport failure carries.
func MapError(err error) error {
	out := mapError(err)
	var ae *apperr.Error
	if errors.As(out, &ae) && ae.Detail != "" {
		if red := redactDetail(ae.Detail); red != ae.Detail {
			return ae.WithDetail(red)
		}
	}
	return out
}

// queryParamRe matches the value of a jql, query or name URL parameter (up to the next parameter, quote or space).
var queryParamRe = regexp.MustCompile(`([?&](?:jql|query|name)=)[^&\s"']*`)

func redactDetail(s string) string { return queryParamRe.ReplaceAllString(s, "${1}[redacted]") }

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return err
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return mapHTTPError(he)
	}
	if errors.Is(err, context.Canceled) {
		return apperr.Wrap(apperr.Cancelled, "Cancelled.", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || isNetTimeout(err) {
		return apperr.Wrap(apperr.Timeout, "Jira did not answer in time.", err)
	}
	if isHTTPOnHTTPS(err) {
		return apperr.Wrap(apperr.Offline, "That address answers over http, not https. Check the site address.", err)
	}
	if unreachable(err) || isOpError(err) {
		return apperr.Wrap(apperr.Offline, "Could not reach Jira. Check the site address and your connection.", err)
	}
	return apperr.From(err)
}

func mapHTTPError(he *HTTPError) error {
	const authMsg = "Jira rejected the credentials. Check the account and API token."
	onBoard := strings.Contains(he.Path, "/board/")
	onFilter := strings.Contains(he.Path, "/filter/")
	switch {
	case he.Status == 401:
		return apperr.Wrap(apperr.AuthInvalid, authMsg, he)
	case he.Status == 403 && (he.LoginReason == "AUTHENTICATED_FAILED" || strings.HasSuffix(he.Path, "/rest/api/3/myself")):
		return apperr.Wrap(apperr.AuthInvalid, authMsg, he)
	case he.Status == 403:
		msg := "Jira does not let you read this resource."
		switch {
		case onBoard:
			msg = "Jira does not let you see this board."
		case onFilter:
			msg = "Jira does not let you see this filter."
		}
		return apperr.Wrap(apperr.Forbidden, msg, he)
	case he.Status == 404 && (onBoard || onFilter):
		return apperr.Wrap(apperr.NotFound, "This board no longer exists.", he)
	case he.Status == 404:
		return apperr.Wrap(apperr.NotFound, "Jira could not find that item.", he)
	case he.Status == 400 && strings.Contains(he.Path, "/search"):
		msg := "Jira rejected the filter."
		if len(he.Messages) > 0 {
			if first := strings.Join(strings.Fields(he.Messages[0]), " "); first != "" {
				msg = first
			}
		}
		return apperr.Wrap(apperr.FilterInvalid, msg, he)
	case he.Status == 429:
		e := apperr.Wrap(apperr.RateLimited, "Jira is rate limiting requests. Wait a minute, then resync.", he)
		e.Retryable = true
		return e
	case retryable5xx(he.Status):
		e := apperr.Wrap(apperr.SourceHTTP, "Jira is having trouble right now. Try again shortly.", he)
		e.Retryable = true
		return e
	}
	return apperr.Wrap(apperr.SourceHTTP, "Jira returned an unexpected response.", he)
}

// unreachable reports failures before any response: DNS, refused or unroutable dials and TLS verification errors.
// Such errors are never retried.
func unreachable(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Op == "dial" {
		return true
	}
	for _, e := range []error{syscall.ECONNREFUSED, syscall.EHOSTUNREACH, syscall.ENETUNREACH} {
		if errors.Is(err, e) {
			return true
		}
	}
	var ua x509.UnknownAuthorityError
	var hn x509.HostnameError
	var ci x509.CertificateInvalidError
	var cv *tls.CertificateVerificationError
	return errors.As(err, &ua) || errors.As(err, &hn) || errors.As(err, &ci) || errors.As(err, &cv)
}

// isHTTPOnHTTPS reports the transport error Go raises when an https request reaches a plain-http server (a wrong
// scheme, found on the real stack against the fake Jira). It is an address problem, not an internal fault.
func isHTTPOnHTTPS(err error) bool {
	return err != nil && strings.Contains(err.Error(), "server gave HTTP response to HTTPS client")
}

func isOpError(err error) bool {
	var oe *net.OpError
	return errors.As(err, &oe)
}

func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// Package apperr defines Cuisle's typed error (architecture.md §3.4).
//
// FROZEN CONTRACT (Wave 0, implemented in full by the orchestrator).
package apperr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type Code string

const (
	Validation          Code = "validation"
	NotFound            Code = "not_found"
	Conflict            Code = "conflict"
	AuthMissing         Code = "auth_missing"
	AuthInvalid         Code = "auth_invalid"
	Forbidden           Code = "forbidden"
	Offline             Code = "offline"
	Timeout             Code = "timeout"
	RateLimited         Code = "rate_limited"
	SourceHTTP          Code = "source_http"
	FilterInvalid       Code = "filter_invalid"
	KeychainUnavailable Code = "keychain_unavailable"
	SinkFailed          Code = "sink_failed"
	Unsupported         Code = "unsupported"
	// Ticket modal (ADR 0015; architecture.md §3.2, §3.4, jira-sync.md §2.15).
	WriteRejected         Code = "write_rejected"          // provider refused the change; Message carries its reason
	TransitionNeedsScreen Code = "transition_needs_screen" // the transition needs a provider screen: open in the browser
	PermissionDenied      Code = "permission_denied"       // 403 on a write
	MediaUnavailable      Code = "media_unavailable"       // attachment bytes could not be fetched or decoded
	Cooldown              Code = "cooldown"                // TicketService.Refresh within 10 s of the last one; no request sent
	NotImplementedCode    Code = "not_implemented"
	DB                    Code = "db"
	Cancelled             Code = "cancelled"
	Internal              Code = "internal"
)

// Error is the only error type that crosses the Wails bridge.
type Error struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"` // safe, user-presentable, one sentence; never secrets or raw HTTP bodies
	Detail    string `json:"detail"`  // technical; shown in "Details", written to the log at debug
	Retryable bool   `json:"retryable"`
	Field     string `json:"field"` // validation: the patch field name
	// RetryInMs tells the UI when to try again (cooldown, rate_limited); 0 = not applicable (F12, jira-sync §3.1).
	RetryInMs int64 `json:"retryInMs,omitempty"`
	cause     error
}

// WithRetryIn returns a copy with RetryInMs set (for cooldown and rate_limited errors).
func (e *Error) WithRetryIn(ms int64) *Error {
	c := *e
	c.RetryInMs = ms
	return &c
}

// Error returns "code: message" only. Wails logs this string at ERR, so Detail is deliberately excluded.
func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func (e *Error) Unwrap() error { return e.cause }

// New builds an error. Retryable defaults to true for db and timeout.
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message, Retryable: code == DB || code == Timeout}
}

// Wrap builds an error with a cause; Detail is the cause's text.
func Wrap(code Code, message string, cause error) *Error {
	e := New(code, message)
	e.cause = cause
	if cause != nil {
		e.Detail = cause.Error()
	}
	return e
}

// Validationf builds a validation error for a patch field.
func Validationf(field, format string, args ...any) *Error {
	e := New(Validation, fmt.Sprintf(format, args...))
	e.Field = field
	return e
}

// NotImplemented is returned by Wave 0 stubs and by the v1 history placeholder.
var NotImplemented = &Error{Code: NotImplementedCode, Message: "Not implemented yet."}

// WithDetail returns a copy with Detail set.
func (e *Error) WithDetail(detail string) *Error {
	c := *e
	c.Detail = detail
	return &c
}

// CodeOf returns the Code of err: an *Error's code, cancelled for context.Canceled,
// timeout for context.DeadlineExceeded, "" for nil, internal otherwise.
func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return Cancelled
	case errors.Is(err, context.DeadlineExceeded):
		return Timeout
	}
	return Internal
}

// Is reports whether err carries code.
func Is(err error, code Code) bool { return CodeOf(err) == code }

// From converts any error into an *Error (unknown errors become internal with the text in Detail).
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	switch CodeOf(err) {
	case Cancelled:
		return Wrap(Cancelled, "Cancelled.", err)
	case Timeout:
		return Wrap(Timeout, "The operation timed out.", err)
	}
	return Wrap(Internal, "Something went wrong.", err)
}

// Marshal is application.Options.MarshalError: every error returned by a bound method becomes apperr JSON.
func Marshal(err error) []byte {
	b, mErr := json.Marshal(From(err))
	if mErr != nil {
		return nil
	}
	return b
}

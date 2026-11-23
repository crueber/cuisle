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
	NotImplementedCode  Code = "not_implemented"
	DB                  Code = "db"
	Cancelled           Code = "cancelled"
	Internal            Code = "internal"
)

// Error is the only error type that crosses the Wails bridge.
type Error struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"` // safe, user-presentable, one sentence; never secrets or raw HTTP bodies
	Detail    string `json:"detail"`  // technical; shown in "Details", written to the log at debug
	Retryable bool   `json:"retryable"`
	Field     string `json:"field"` // validation: the patch field name
	cause     error
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
	var e *Error
	if errors.As(err, &e) {
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

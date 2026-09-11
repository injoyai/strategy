package domain

import (
	"errors"
	"fmt"
)

// Error is the single error type produced by domain and application layers.
//
// Message is wire-safe: Error() deliberately omits Cause so internal reasons
// (stack traces, provider secrets, SQL text) never leak into HTTP responses.
// The cause stays reachable through errors.Unwrap / errors.Is for structured
// logging and tests.
type Error struct {
	Code    string // Stable wire-visible code; see codes.go.
	Message string // Human-readable message safe for clients.
	Cause   error  // Internal reason; never rendered by Error().
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Cause }

// NewError builds a wire-safe Error without an internal cause.
func NewError(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap attaches an internal cause to a wire-safe Error.
func Wrap(err error, code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), Cause: err}
}

// ErrorCode returns the stable code carried by err. Errors that are not
// *Error fail closed to CodeInternalError so handlers never guess.
func ErrorCode(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Code != "" {
		return e.Code
	}
	return CodeInternalError
}

// truncate keeps hostile input snippets short enough for messages and logs.
func truncate(s string) string {
	const max = 64
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

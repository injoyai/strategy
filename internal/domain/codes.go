// Package domain defines the core value objects, stable error codes and
// error type shared by every layer. The package is intentionally
// dependency-light: it must not import HTTP, SQL or provider packages, and it
// must not read the wall clock directly (see architecture_test.go).
package domain

// Stable error codes surfaced on the wire (HTTP error responses, Issue
// payloads). Existing values must never be renamed or removed; append new
// codes instead so clients can keep matching them.
const (
	// Request validation: malformed input rejected at the boundary.
	CodeValidationInvalid          = "validation.invalid"
	CodeValidationUnknownField     = "validation.unknown_field"
	CodeValidationDecimal          = "validation.decimal"
	CodeValidationInterval         = "validation.interval"
	CodeValidationUnitMismatch     = "validation.unit_mismatch"
	CodeValidationCurrencyMismatch = "validation.currency_mismatch"

	// Resource addressing and concurrency.
	CodeResourceNotFound        = "resource.not_found"
	CodeResourceVersionMismatch = "resource.version_mismatch"
	CodeResourceConflict        = "resource.conflict"

	// Pagination.
	CodePaginationCursorExpired = "pagination.cursor_expired"

	// Transport and platform.
	CodeAuthUnauthorized    = "auth.unauthorized"
	CodeAuthForbidden       = "auth.forbidden"
	CodeIdempotencyConflict = "idempotency.conflict"
	CodeRateLimited         = "rate.limited"
	CodeInternalError       = "internal.error"
	CodeInternalUnavailable = "internal.unavailable"
)

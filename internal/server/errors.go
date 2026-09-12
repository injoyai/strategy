// Single-point mapping between domain error codes and HTTP responses.
//
// Every API error leaves the process through writeError so the wire shape
// always matches the OpenAPI Error schema: code, message, request_id,
// retryable and issues. Unknown error values fail closed to 500 without
// leaking their internal messages.
package server

import (
	"errors"
	"net/http"

	"github.com/injoyai/strategy/internal/domain"
)

// errorEnvelope mirrors the Error schema in docs/api/openapi.json.
// issues is required by the contract and therefore always non-nil on the wire.
type errorEnvelope struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Retryable bool           `json:"retryable"`
	Issues    []domain.Issue `json:"issues"`
}

// StatusForError maps a stable domain error code to exactly one HTTP status.
// The ten statuses are fixed by the OpenAPI contract (400, 401, 403, 404,
// 409, 410, 422, 429, 500, 503); unknown codes fail closed to 500 so a
// future code cannot silently escape as a success-class response.
func StatusForError(code string) int {
	switch code {
	case domain.CodeValidationInvalid,
		domain.CodeValidationUnknownField,
		domain.CodeValidationDecimal,
		domain.CodeValidationInterval,
		domain.CodeValidationUnitMismatch,
		domain.CodeValidationCurrencyMismatch:
		return http.StatusBadRequest
	case domain.CodeAuthUnauthorized:
		return http.StatusUnauthorized
	case domain.CodeAuthForbidden:
		return http.StatusForbidden
	case domain.CodeResourceNotFound:
		return http.StatusNotFound
	case domain.CodeResourceGone:
		return http.StatusGone
	case domain.CodeResourceConflict,
		domain.CodeResourceVersionMismatch,
		domain.CodeIdempotencyConflict,
		domain.CodeIdempotencyKeyExpired:
		return http.StatusConflict
	case domain.CodePaginationCursorExpired:
		return http.StatusUnprocessableEntity
	case domain.CodeRateLimited:
		return http.StatusTooManyRequests
	case domain.CodeInternalUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

// writeError maps err to the standard envelope and writes the response.
// Only messages built by domain errors are trusted on the wire; any other
// error is reported as a generic internal error so implementation details
// (driver messages, file paths) never reach clients.
func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var derr *domain.Error
	message := "internal error"
	if errors.As(err, &derr) {
		message = derr.Message
	}
	code := domain.ErrorCode(err)
	status := StatusForError(code)
	a.writeErrorEnvelope(w, r, status, errorEnvelope{
		Code:      code,
		Message:   message,
		RequestID: RequestIDFrom(r.Context()),
		Retryable: status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable,
		Issues:    []domain.Issue{},
	})
}

// writeErrorEnvelope writes a contract-shaped error response. It is the only
// place API errors are serialized.
func (a *API) writeErrorEnvelope(w http.ResponseWriter, r *http.Request, status int, env errorEnvelope) {
	if env.RequestID == "" {
		// Requests rejected before the request-ID middleware (never in the
		// current chain) still get a contract-valid envelope.
		env.RequestID = a.newRequestID()
	}
	if env.Issues == nil {
		env.Issues = []domain.Issue{}
	}
	w.Header().Set("X-Request-ID", env.RequestID)
	WriteJSON(w, status, env)
}

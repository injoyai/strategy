// JSON transport boundary helpers.
//
// Handlers must only do transport validation and mapping (no task state
// transitions, no data rules), and every JSON body enters the domain through
// DecodeJSON: size-limited, UTF-8 JSON only, unknown fields rejected so
// clients cannot silently rely on fields the contract does not define.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/injoyai/strategy/internal/domain"
)

// WriteJSON serializes body as UTF-8 JSON, the only response media type of
// the API apart from artifact downloads and SSE streams.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(body)
}

// WriteAccepted answers a 202 with the contract-mandated Location header
// pointing at the job resource. body must satisfy the Job schema.
func WriteAccepted(w http.ResponseWriter, r *http.Request, jobID string, body any) {
	w.Header().Set("Location", "/api/v1/jobs/"+jobID)
	WriteJSON(w, http.StatusAccepted, body)
}

// DecodeJSON reads exactly one JSON value from the request body into dst.
// On any violation it writes the mapped error response and returns false;
// handlers must stop after a false result.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		decodeError(w, r, err)
		return false
	}
	// Reject trailing content after the first JSON value ("{} {}").
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		decodeError(w, r, errors.New("unexpected trailing data after JSON value"))
		return false
	}
	return true
}

func decodeError(w http.ResponseWriter, r *http.Request, err error) {
	var maxErr *http.MaxBytesError
	code := domain.CodeValidationInvalid
	message := "invalid JSON request body"
	switch {
	case errors.As(err, &maxErr):
		message = fmt.Sprintf("request body exceeds the %d byte limit", maxErr.Limit)
	case errors.Is(err, io.EOF):
		message = "request body is required"
	case isUnknownFieldErr(err):
		// DisallowUnknownFields reports the offending field name only;
		// safe to echo back to the client.
		code = domain.CodeValidationUnknownField
		message = err.Error()
	}
	writeBoundaryError(w, r, code, message)
}

// isUnknownFieldErr reports whether err came from DisallowUnknownFields.
// encoding/json exposes no dedicated error type for it, so the detection
// relies on the stable message text; every other failure already maps to a
// generic message, so a future wording change cannot leak input.
func isUnknownFieldErr(err error) bool {
	return strings.Contains(err.Error(), "unknown field ")
}

// writeBoundaryError emits a contract-shaped error for violations detected
// by transport helpers (decode, pagination). Request-ID middleware runs
// before any handler, so the ID normally comes from the request context;
// the fallback keeps the envelope valid even for responses produced before
// the middleware chain (defensive only).
func writeBoundaryError(w http.ResponseWriter, r *http.Request, code, message string) {
	requestID := RequestIDFrom(r.Context())
	if requestID == "" {
		requestID = mintRequestID()
	}
	w.Header().Set("X-Request-ID", requestID)
	WriteJSON(w, StatusForError(code), errorEnvelope{
		Code:      code,
		Message:   message,
		RequestID: requestID,
		Retryable: false,
		Issues:    []domain.Issue{},
	})
}

// mintRequestID produces a fallback request ID without needing the
// configured IDGenerator.
func mintRequestID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(buf[:])
}

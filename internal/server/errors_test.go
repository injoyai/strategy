package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

func TestStatusForError(t *testing.T) {
	cases := []struct {
		code   string
		status int
	}{
		{domain.CodeValidationInvalid, http.StatusBadRequest},
		{domain.CodeValidationUnknownField, http.StatusBadRequest},
		{domain.CodeValidationDecimal, http.StatusBadRequest},
		{domain.CodeValidationInterval, http.StatusBadRequest},
		{domain.CodeValidationUnitMismatch, http.StatusBadRequest},
		{domain.CodeValidationCurrencyMismatch, http.StatusBadRequest},
		{domain.CodeAuthUnauthorized, http.StatusUnauthorized},
		{domain.CodeAuthForbidden, http.StatusForbidden},
		{domain.CodeResourceNotFound, http.StatusNotFound},
		{domain.CodeResourceConflict, http.StatusConflict},
		{domain.CodeResourceVersionMismatch, http.StatusConflict},
		{domain.CodeIdempotencyConflict, http.StatusConflict},
		{domain.CodePaginationCursorExpired, http.StatusUnprocessableEntity},
		{domain.CodeRateLimited, http.StatusTooManyRequests},
		{domain.CodeInternalUnavailable, http.StatusServiceUnavailable},
		{domain.CodeInternalError, http.StatusInternalServerError},
		{"brand.new.code", http.StatusInternalServerError},
		{"", http.StatusInternalServerError},
	}
	for _, tc := range cases {
		if got := StatusForError(tc.code); got != tc.status {
			t.Errorf("StatusForError(%q) = %d, want %d", tc.code, got, tc.status)
		}
	}
}

func TestWriteErrorFailsClosed(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	api.Handle("GET", "/boom", func(w http.ResponseWriter, r *http.Request) {
		api.writeError(w, r, errors.New("raw driver failure: secret path /db/xyz"))
	}, RouteOptions{})

	rec := doRequest(t, h, http.MethodGet, "/api/v1/boom", nil, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := decodeWireError(t, rec)
	if env.Code != domain.CodeInternalError {
		t.Errorf("code = %q, want %q", env.Code, domain.CodeInternalError)
	}
	if env.Message != "internal error" {
		t.Errorf("message = %q, want the generic message without internal details", env.Message)
	}
	if env.Retryable {
		t.Error("500 must not be marked retryable")
	}
}

func TestWriteErrorDomainMessage(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	api.Handle("GET", "/bad", func(w http.ResponseWriter, r *http.Request) {
		api.writeError(w, r, domain.NewError(domain.CodeValidationInterval, "interval must be one of 1m,1h,1d"))
	}, RouteOptions{})

	rec := doRequest(t, h, http.MethodGet, "/api/v1/bad", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	env := decodeWireError(t, rec)
	if env.Code != domain.CodeValidationInterval {
		t.Errorf("code = %q, want %q", env.Code, domain.CodeValidationInterval)
	}
	if env.Message != "interval must be one of 1m,1h,1d" {
		t.Errorf("message = %q, want the domain message", env.Message)
	}
}

func TestWriteErrorWrappedCause(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	inner := errors.New("disk full")
	api.Handle("GET", "/wrapped", func(w http.ResponseWriter, r *http.Request) {
		api.writeError(w, r, fmt.Errorf("persist: %w", domain.Wrap(inner, domain.CodeInternalUnavailable, "storage unavailable")))
	}, RouteOptions{})

	rec := doRequest(t, h, http.MethodGet, "/api/v1/wrapped", nil, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	env := decodeWireError(t, rec)
	if env.Code != domain.CodeInternalUnavailable {
		t.Errorf("code = %q, want %q", env.Code, domain.CodeInternalUnavailable)
	}
	if !env.Retryable {
		t.Error("503 must be marked retryable")
	}
	if env.Message == "" || env.Message == "disk full" {
		t.Errorf("message = %q, want the wire-safe wrapper message", env.Message)
	}
}

func TestWriteErrorEnvelopeMintsRequestID(t *testing.T) {
	api := newTestAPI(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/any", nil)
	// No request-ID middleware ran, so the envelope must mint an ID itself.
	api.writeErrorEnvelope(rec, req, http.StatusBadRequest, errorEnvelope{
		Code:    domain.CodeValidationInvalid,
		Message: "x",
		Issues:  nil,
	})
	env := decodeWireError(t, rec)
	if env.RequestID == "" {
		t.Fatal("expected a minted request_id")
	}
}

func TestWriteError429Retryable(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	api.Handle("GET", "/limited", func(w http.ResponseWriter, r *http.Request) {
		api.writeError(w, r, domain.NewError(domain.CodeRateLimited, "too many requests"))
	}, RouteOptions{})

	rec := doRequest(t, h, http.MethodGet, "/api/v1/limited", nil, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	env := decodeWireError(t, rec)
	if !env.Retryable {
		t.Error("429 must be marked retryable")
	}
}

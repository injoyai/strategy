package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/ports"
)

// testBase pins every time-dependent behaviour (cursor TTL, idempotency
// window) so tests never depend on the wall clock.
var testBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newTestAPI returns an API wired with deterministic dependencies and the
// local (loopback-only) authenticator used in development.
func newTestAPI(t *testing.T) *API {
	t.Helper()
	return NewAPI(Options{
		Log:   silentLogger(),
		Auth:  LocalAuth{},
		Clock: fixedClock(),
	})
}

func fixedClock() *ports.FixedClock {
	return ports.NewFixedClock(testBase)
}

// doRequest sends a request through h and returns the recorded response.
func doRequest(t *testing.T, h http.Handler, method, target string, headers map[string]string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// wireError mirrors the Error schema in docs/api/openapi.json.
type wireError struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	RequestID string          `json:"request_id"`
	Retryable bool            `json:"retryable"`
	Issues    json.RawMessage `json:"issues"`
}

// decodeWireError asserts the envelope invariants the contract requires for
// every error response and returns the decoded value.
func decodeWireError(t *testing.T, rec *httptest.ResponseRecorder) wireError {
	t.Helper()
	var env wireError
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not a JSON error envelope: %v\nbody: %s", err, rec.Body.String())
	}
	if env.Code == "" || env.Message == "" || env.RequestID == "" {
		t.Fatalf("error envelope missing required fields: %+v", env)
	}
	if !json.Valid(env.Issues) || string(env.Issues) == "null" {
		t.Fatalf("issues must be a JSON array, got: %s", env.Issues)
	}
	if env.RequestID != rec.Header().Get("X-Request-ID") {
		t.Fatalf("envelope request_id %q does not match X-Request-ID header %q",
			env.RequestID, rec.Header().Get("X-Request-ID"))
	}
	return env
}

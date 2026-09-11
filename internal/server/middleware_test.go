package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	api.Handle("GET", "/echo", func(w http.ResponseWriter, r *http.Request) {
		// The handler observes the same ID the middleware put on the wire.
		w.Header().Set("X-Seen-ID", RequestIDFrom(r.Context()))
		WriteJSON(w, http.StatusOK, map[string]string{"ok": "1"})
	}, RouteOptions{})

	rec := doRequest(t, h, http.MethodGet, "/api/v1/echo", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	id := rec.Header().Get("X-Request-ID")
	if id == "" {
		t.Fatal("expected a generated X-Request-ID")
	}
	if got := rec.Header().Get("X-Seen-ID"); got != id {
		t.Errorf("handler context ID %q != header %q", got, id)
	}

	// A client-supplied valid ID is echoed, not replaced.
	rec = doRequest(t, h, http.MethodGet, "/api/v1/echo",
		map[string]string{"X-Request-ID": "client-id.42"}, nil)
	if got := rec.Header().Get("X-Request-ID"); got != "client-id.42" {
		t.Errorf("X-Request-ID = %q, want client value echoed", got)
	}
}

func TestRequestIDInvalidRejected(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	api.Handle("GET", "/x", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{})
	}, RouteOptions{})

	for _, id := range []string{"bad id!", "with/slash", strings.Repeat("a", 129), ""} {
		headers := map[string]string{}
		if id != "" {
			headers["X-Request-ID"] = id
		}
		_ = id
		rec := doRequest(t, h, http.MethodGet, "/api/v1/x", headers, nil)
		if id == "" {
			if rec.Code != http.StatusOK {
				t.Fatalf("empty header should generate an ID, got status %d", rec.Code)
			}
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("X-Request-ID %q: status = %d, want 400", id, rec.Code)
		}
		if env := decodeWireError(t, rec); env.Code != domain.CodeValidationInvalid {
			t.Errorf("X-Request-ID %q: code = %q, want validation.invalid", id, env.Code)
		}
	}
}

func TestRequestIDTooLongRejected(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	rec := doRequest(t, h, http.MethodGet, "/api/v1/x",
		map[string]string{"X-Request-ID": strings.Repeat("a", 300)}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRecoverMiddlewareProduces500(t *testing.T) {
	api := newTestAPI(t)
	h := api.Handler()
	api.Handle("GET", "/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("exploding secret detail")
	}, RouteOptions{})

	rec := doRequest(t, h, http.MethodGet, "/api/v1/panic", nil, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	env := decodeWireError(t, rec)
	if env.Code != domain.CodeInternalError {
		t.Errorf("code = %q, want internal.error", env.Code)
	}
	if strings.Contains(rec.Body.String(), "exploding secret detail") {
		t.Error("panic value must never reach the response body")
	}
}

func TestBodyLimitRejected(t *testing.T) {
	api := NewAPI(Options{Log: silentLogger(), Auth: LocalAuth{}, MaxBodyBytes: 64})
	h := api.Handler()
	api.Handle("POST", "/limited", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if !DecodeJSON(w, r, &body) {
			return
		}
		WriteJSON(w, http.StatusOK, body)
	}, RouteOptions{})

	rec := doRequest(t, h, http.MethodPost, "/api/v1/limited",
		map[string]string{"Content-Type": "application/json"},
		[]byte(`{"name":"`+strings.Repeat("x", 200)+`"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	env := decodeWireError(t, rec)
	if env.Code != domain.CodeValidationInvalid {
		t.Errorf("code = %q, want validation.invalid", env.Code)
	}
}

func TestAuthMiddlewareChallenges(t *testing.T) {
	api := NewAPI(Options{
		Log:  silentLogger(),
		Auth: NewBearerAuth("bearer-token-0123456789"),
	})
	h := api.Handler()
	api.Handle("GET", "/secure", func(w http.ResponseWriter, r *http.Request) {
		caller, ok := CallerFrom(r.Context())
		if !ok || caller.ID != "bearer" {
			t.Error("expected the bearer caller in context")
		}
		WriteJSON(w, http.StatusOK, map[string]string{})
	}, RouteOptions{})

	rec := doRequest(t, h, http.MethodGet, "/api/v1/secure", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing credentials: status = %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="researchd"` {
		t.Errorf("WWW-Authenticate = %q, want the bearer challenge", got)
	}
	if env := decodeWireError(t, rec); env.Code != domain.CodeAuthUnauthorized {
		t.Errorf("code = %q, want auth.unauthorized", env.Code)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/secure",
		map[string]string{"Authorization": "Bearer wrong-token-xxxxxxxx"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: status = %d, want 401", rec.Code)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/secure",
		map[string]string{"Authorization": "Bearer bearer-token-0123456789"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token: status = %d, want 200", rec.Code)
	}
}

func TestAccessLogStableFields(t *testing.T) {
	var buf bytes.Buffer
	api := NewAPI(Options{
		Log:  slog.New(slog.NewJSONHandler(&buf, nil)),
		Auth: LocalAuth{},
	})
	h := api.Handler()
	api.Handle("GET", "/logged", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{})
	}, RouteOptions{})

	doRequest(t, h, http.MethodGet, "/api/v1/logged", nil, nil)

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("access log is not one JSON object: %v\n%s", err, buf.String())
	}
	for _, field := range []string{"request_id", "method", "path", "status", "duration_ms"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("access log missing stable field %q in %v", field, entry)
		}
	}
	if entry["method"] != http.MethodGet || entry["path"] != "/api/v1/logged" {
		t.Errorf("unexpected log identity fields: %v", entry)
	}
}

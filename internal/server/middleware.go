// Middleware chain shared by every /api/v1 route.
//
// Order (outermost first): requestID, recover, accessLog, bodyLimit, auth.
// requestID runs first so that every response - including panics and 401s -
// carries the header and can reference it in the error envelope.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

const (
	headerRequestID = "X-Request-ID"
	// maxRequestIDLength matches the ID schema bound (maxLength 128).
	maxRequestIDLength = 128
	// defaultMaxBodyBytes bounds every request body; the import upload
	// endpoint currently shares this bound (M0 ships no large uploads).
	defaultMaxBodyBytes = 10 << 20
)

// requestIDPattern is deliberately narrow: printable ASCII used by the
// generated IDs plus the common client-supplied shapes (UUID, ULID, hex).
var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyCaller
	ctxKeyAPI
)

// RequestIDFrom returns the request ID assigned by the middleware chain.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyRequestID).(string)
	return id
}

// CallerFrom returns the authenticated caller, if any.
func CallerFrom(ctx context.Context) (Caller, bool) {
	caller, ok := ctx.Value(ctxKeyCaller).(Caller)
	return caller, ok
}

// APIFrom returns the owning API instance for handler helpers.
func APIFrom(ctx context.Context) *API {
	api, _ := ctx.Value(ctxKeyAPI).(*API)
	return api
}

// newRequestID mints an ID through the configured generator and falls back
// to a random hex ID if generation fails, so tracing never blocks serving.
func (a *API) newRequestID() string {
	if id, err := a.ids.NewID(); err == nil {
		return string(id)
	}
	return mintRequestID()
}

func (a *API) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id != "" {
			// Client-supplied IDs are validated, not sanitized: a malformed ID
			// is rejected outright so log correlation stays trustworthy.
			if len(id) > maxRequestIDLength || !requestIDPattern.MatchString(id) {
				writeBoundaryError(w, r, domain.CodeValidationInvalid,
					"X-Request-ID must match [A-Za-z0-9._-]{1,128}")
				return
			}
		} else {
			id = a.newRequestID()
		}
		w.Header().Set(headerRequestID, id)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
		ctx = context.WithValue(ctx, ctxKeyAPI, a)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// recoverMiddleware converts panics into contract 500s. The stack goes to
// the log only, never to the response.
func (a *API) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				a.log.Error("panic in http handler",
					slog.String("request_id", RequestIDFrom(r.Context())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.String("panic", toString(v)))
				a.writeError(w, r, domain.NewError(domain.CodeInternalError, "internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func toString(v any) string {
	if err, ok := v.(error); ok {
		return err.Error()
	}
	if s, ok := v.(string); ok {
		return s
	}
	return "non-string panic value"
}

// accessLogMiddleware records one structured line per request using the
// stable field names documented in internal/logging.
func (a *API) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := a.clock.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		a.log.InfoContext(r.Context(), "http request",
			slog.String("request_id", RequestIDFrom(r.Context())),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("duration_ms", time.Duration(a.clock.Now().Sub(start)).Milliseconds()))
	})
}

// statusRecorder captures the status code written by inner handlers.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.status = http.StatusOK
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}

// bodyLimitMiddleware bounds the request body before any handler reads it.
func (a *API) bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, a.maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// authMiddleware enforces the authenticator for every API route. Failures
// produce 401 with the WWW-Authenticate challenge required by bearer auth.
func (a *API) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, err := a.auth.Authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="researchd"`)
			a.writeError(w, r, domain.NewError(domain.CodeAuthUnauthorized, "authentication required"))
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyCaller, caller)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Idempotency-Key handling for write operations.
//
// Scope: every key is scoped to caller + workspace + method + path, so the
// same key reused on a different route or by a different caller can never
// collide. The recorded request hash is computed over a canonical form of
// the JSON body (object keys sorted, number literals preserved), so
// semantically identical payloads replay while any change conflicts.
//
// Replay window: claims and stored responses are retained for 24 hours
// (contract minimum). A key retried past the window conflicts with
// idempotency.key_expired instead of re-executing; the client must mint a
// new key.
package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

const (
	// IdempotencyReplayWindow is the contract minimum ("24 hours or more").
	IdempotencyReplayWindow = ports.IdempotencyReplayWindow
	// idempotencyKeyMin/Max mirror the OpenAPI header parameter bounds.
	idempotencyKeyMin = 8
	idempotencyKeyMax = 128
	// workspaceDefault is the single M0 workspace. The scope string layout
	// keeps a reserved field so persistence needs no migration.
	workspaceDefault = "default"
)

// IdempotencyRecord and IdempotencyStore are defined in ports so storage
// packages can implement them without importing the HTTP layer.
type (
	IdempotencyRecord = ports.IdempotencyRecord
	IdempotencyStore  = ports.IdempotencyStore
)

// MemoryIdempotencyStore is an in-process IdempotencyStore for tests and
// single-process deployments.
type MemoryIdempotencyStore struct {
	mu      sync.Mutex
	records map[string]IdempotencyRecord
}

// NewMemoryIdempotencyStore returns an empty store.
func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{records: make(map[string]IdempotencyRecord)}
}

// Begin implements IdempotencyStore. Existing records are never reclaimed:
// an expired key is returned so the caller can reject it instead of
// silently re-executing.
func (s *MemoryIdempotencyStore) Begin(scope, key, requestHash string, now time.Time) (*IdempotencyRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mapKey := scope + "\x00" + key
	if rec, ok := s.records[mapKey]; ok {
		return &rec, false, nil
	}
	s.records[mapKey] = IdempotencyRecord{
		Scope:       scope,
		Key:         key,
		RequestHash: requestHash,
		CreatedAt:   now,
		ExpiresAt:   now.Add(IdempotencyReplayWindow),
	}
	return nil, true, nil
}

// Commit implements IdempotencyStore. Committing with a mismatched request
// hash is a no-op error so a stale handler cannot overwrite the slot.
func (s *MemoryIdempotencyStore) Commit(record IdempotencyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	mapKey := record.Scope + "\x00" + record.Key
	current, ok := s.records[mapKey]
	if !ok || current.RequestHash != record.RequestHash {
		return domain.NewError(domain.CodeIdempotencyConflict, "idempotency claim no longer active")
	}
	current.Status = record.Status
	current.Body = record.Body
	current.Location = record.Location
	s.records[mapKey] = current
	return nil
}

// canonicalRequestHash returns the hex SHA-256 of the canonical request
// representation: JSON bodies are re-encoded with sorted object keys, any
// other media type (e.g. multipart uploads) is hashed as raw bytes.
func canonicalRequestHash(body []byte) string {
	if normalized, ok := canonicalJSON(body); ok {
		body = normalized
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(body []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	// encoding/json sorts map keys; UseNumber keeps numeric literals exact so
	// "1.0" and "1.00" hash differently (deliberately conservative).
	out, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return out, true
}

// idempotencyScope builds the caller|workspace|method|path scope string.
func idempotencyScope(caller Caller, method, path string) string {
	return caller.ID + "|" + workspaceDefault + "|" + method + "|" + path
}

// idempotencyMiddleware enforces the Idempotency-Key contract on routes
// that declare it. It buffers the (size-limited) body to compute the
// request hash and restores it for the handler.
func (a *API) idempotencyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("Idempotency-Key")
		if len(key) < idempotencyKeyMin || len(key) > idempotencyKeyMax {
			a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid,
				"Idempotency-Key header is required (8..128 characters)"))
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid, "request body could not be read"))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		caller, _ := CallerFrom(r.Context())
		scope := idempotencyScope(caller, r.Method, r.URL.Path)
		hash := canonicalRequestHash(body)

		existing, claimed, err := a.idem.Begin(scope, key, hash, a.clock.Now())
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		if !claimed {
			a.replayOrConflict(w, r, *existing, hash)
			return
		}

		rec := &captureWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		done := IdempotencyRecord{
			Scope:       scope,
			Key:         key,
			RequestHash: hash,
			Status:      rec.status,
			Body:        rec.body.Bytes(),
			Location:    rec.Header().Get("Location"),
		}
		if err := a.idem.Commit(done); err != nil {
			// The response already left; the log entry is the trace for why a
			// retry of the same key conflicts instead of replaying.
			a.log.Error("idempotency commit failed",
				slog.String("request_id", RequestIDFrom(r.Context())),
				slog.String("error", err.Error()))
		}
	})
}

// replayOrConflict answers a repeated claim: different payloads and claims
// still in flight are a 409, a key past the replay window is a 409
// idempotency.key_expired (never silently re-executed), and identical
// payloads within the window replay the stored response.
func (a *API) replayOrConflict(w http.ResponseWriter, r *http.Request, existing IdempotencyRecord, hash string) {
	now := a.clock.Now()
	if existing.RequestHash != hash {
		a.writeError(w, r, domain.NewError(domain.CodeIdempotencyConflict,
			"Idempotency-Key was already used with a different request payload"))
		return
	}
	if existing.InFlight() {
		a.writeError(w, r, domain.NewError(domain.CodeIdempotencyConflict,
			"a request with this Idempotency-Key is currently in progress"))
		return
	}
	if !now.Before(existing.ExpiresAt) {
		a.writeError(w, r, domain.NewError(domain.CodeIdempotencyKeyExpired,
			"Idempotency-Key is past the retry window; use a new key"))
		return
	}
	if existing.Location != "" {
		w.Header().Set("Location", existing.Location)
	}
	w.Header().Set("Idempotent-Replay", "true")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(existing.Status)
	_, _ = w.Write(existing.Body)
}

// captureWriter records the status code and body written by the handler so
// an idempotent route can persist them for later replay. Headers remain
// live on the wrapped writer, which is how Location reaches the response.
type captureWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	body        bytes.Buffer
}

func (c *captureWriter) WriteHeader(code int) {
	if !c.wroteHeader {
		c.status = code
		c.wroteHeader = true
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureWriter) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.status = http.StatusOK
		c.wroteHeader = true
	}
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
}
